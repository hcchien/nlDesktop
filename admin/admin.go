// Package admin 提供 server-rendered、schema-driven 的管理介面（/admin）。
//
// 所有資料操作經 in-process GraphQL 呼叫、帶登入者自己的 token，
// 因此權限與 GraphQL API / MCP 完全一致（同一個資料層關卡），
// admin 本身沒有任何特權路徑。表單由 meta（nl DSL registry）動態生成。
package admin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hcchien/nl/access"
	"github.com/hcchien/nl/meta"
)

const cookieName = "nl_admin"

// Handler 是 admin UI 的 HTTP handlers。
type Handler struct {
	gql    http.Handler // CMS 的 /graphql handler（in-process 呼叫）
	secret []byte
}

// New 建立掛在 /admin 底下的 handler。
func New(gql http.Handler, secret []byte) http.Handler {
	h := &Handler{gql: gql, secret: secret}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /admin/login", h.loginForm)
	mux.HandleFunc("POST /admin/login", h.loginSubmit)
	mux.HandleFunc("GET /admin/logout", h.logout)
	mux.HandleFunc("GET /admin", h.requireAuth(h.home))
	mux.HandleFunc("GET /admin/l/{list}", h.requireAuth(h.listView))
	mux.HandleFunc("GET /admin/l/{list}/new", h.requireAuth(h.itemForm))
	mux.HandleFunc("POST /admin/l/{list}/new", h.requireAuth(h.itemSubmit))
	mux.HandleFunc("GET /admin/l/{list}/{id}", h.requireAuth(h.itemForm))
	mux.HandleFunc("POST /admin/l/{list}/{id}", h.requireAuth(h.itemSubmit))
	mux.HandleFunc("POST /admin/l/{list}/{id}/delete", h.requireAuth(h.itemDelete))
	return mux
}

// ---- auth ----

type session struct {
	token string
	name  string
	role  string
}

func (h *Handler) sessionFrom(r *http.Request) *session {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	// 以 me query 驗證 token 並取得身分（無效 token 會拿到 null）
	data, errs := h.exec(c.Value, `{me{name role}}`, nil)
	if len(errs) > 0 || data["me"] == nil {
		return nil
	}
	var me struct {
		Name string `json:"name"`
		Role string `json:"role"`
	}
	if err := json.Unmarshal(data["me"], &me); err != nil || me.Role == "" {
		return nil
	}
	return &session{token: c.Value, name: me.Name, role: me.Role}
}

func (h *Handler) requireAuth(next func(http.ResponseWriter, *http.Request, *session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s := h.sessionFrom(r)
		if s == nil {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next(w, r, s)
	}
}

func (h *Handler) loginForm(w http.ResponseWriter, r *http.Request) {
	renderLogin(w, "", http.StatusOK)
}

func (h *Handler) loginSubmit(w http.ResponseWriter, r *http.Request) {
	data, errs := h.exec("", `mutation($e: String!, $p: String!){login(email:$e,password:$p){token}}`,
		map[string]any{"e": r.PostFormValue("email"), "p": r.PostFormValue("password")})
	if len(errs) > 0 || data["login"] == nil {
		renderLogin(w, "帳號或密碼錯誤", http.StatusUnauthorized)
		return
	}
	var payload struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data["login"], &payload); err != nil {
		renderLogin(w, "登入失敗", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: payload.Token, Path: "/admin",
		HttpOnly: true, SameSite: http.SameSiteLaxMode,
		MaxAge: int((7 * 24 * time.Hour).Seconds()),
	})
	http.Redirect(w, r, "/admin", http.StatusFound)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/admin", MaxAge: -1})
	http.Redirect(w, r, "/admin/login", http.StatusFound)
}

// ---- in-process GraphQL ----

func (h *Handler) exec(token, query string, vars map[string]any) (map[string]json.RawMessage, []string) {
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.gql.ServeHTTP(rec, req)
	var out struct {
		Data   map[string]json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		return nil, []string{"internal error: bad GraphQL response"}
	}
	msgs := make([]string, len(out.Errors))
	for i, e := range out.Errors {
		msgs[i] = e.Message
	}
	return out.Data, msgs
}

// ---- pages ----

// navLists 回傳該角色可查詢的 lists（首頁與側欄）。
func navLists(role string) []meta.List {
	var out []meta.List
	for _, l := range meta.All() {
		if access.CanOperate(l.Name, access.OpQuery, role) {
			out = append(out, l)
		}
	}
	return out
}

func (h *Handler) home(w http.ResponseWriter, r *http.Request, s *session) {
	renderPage(w, "nl CMS", s, navLists(s.role), homeBody(navLists(s.role)))
}

func (h *Handler) listView(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	after := r.URL.Query().Get("after")
	args := "first: 20, orderBy: {field: UPDATED_AT, direction: DESC}"
	vars := map[string]any{}
	if after != "" {
		args += ", after: $after"
		vars["after"] = after
	}
	varDef := ""
	if after != "" {
		varDef = "($after: Cursor)"
	}
	q := fmt.Sprintf(`query%s{ items: %s(%s){ totalCount pageInfo{hasNextPage endCursor} edges{node{%s}} } }`,
		varDef, l.QueryField, args, l.Selection())
	data, errs := h.exec(s.token, q, vars)
	if len(errs) > 0 {
		renderPage(w, l.Name, s, navLists(s.role), errorBody(errs))
		return
	}
	var conn struct {
		TotalCount int `json:"totalCount"`
		PageInfo   struct {
			HasNextPage bool   `json:"hasNextPage"`
			EndCursor   string `json:"endCursor"`
		} `json:"pageInfo"`
		Edges []struct {
			Node map[string]any `json:"node"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(data["items"], &conn); err != nil {
		renderPage(w, l.Name, s, navLists(s.role), errorBody([]string{err.Error()}))
		return
	}
	rows := make([]map[string]any, len(conn.Edges))
	for i, e := range conn.Edges {
		rows[i] = e.Node
	}
	next := ""
	if conn.PageInfo.HasNextPage {
		next = conn.PageInfo.EndCursor
	}
	renderPage(w, l.Name, s, navLists(s.role), listBody(l, rows, conn.TotalCount, next))
}

func (h *Handler) itemForm(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	var item map[string]any
	if id != "" {
		// 以 where:{id} 查單筆（node(id) 需要 global-unique-ID，本框架採 per-table id）
		q := fmt.Sprintf(`query($id: ID){ items: %s(where:{id: $id}, first: 1){ edges{node{%s}} } }`,
			l.QueryField, l.Selection())
		idNum, _ := strconv.Atoi(id)
		data, errs := h.exec(s.token, q, map[string]any{"id": idNum})
		if len(errs) > 0 {
			renderPage(w, l.Name, s, navLists(s.role), errorBody(errs))
			return
		}
		var conn struct {
			Edges []struct {
				Node map[string]any `json:"node"`
			} `json:"edges"`
		}
		if err := json.Unmarshal(data["items"], &conn); err != nil || len(conn.Edges) == 0 {
			renderPage(w, l.Name, s, navLists(s.role), errorBody([]string{"找不到項目（或無權限）"}))
			return
		}
		item = conn.Edges[0].Node
	}
	fields := h.buildFormFields(s, l, item)
	renderPage(w, l.Name, s, navLists(s.role), formBody(l, id, fields, ""))
}

func (h *Handler) itemSubmit(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	input, err := h.formToInput(l, r, id != "")
	if err == nil {
		var errs []string
		if id == "" {
			q := fmt.Sprintf(`mutation($input: Create%sInput!){ item: create%s(input: $input){ id } }`, l.Name, l.Name)
			var data map[string]json.RawMessage
			data, errs = h.exec(s.token, q, map[string]any{"input": input})
			if len(errs) == 0 {
				var created struct {
					ID string `json:"id"`
				}
				json.Unmarshal(data["item"], &created)
				http.Redirect(w, r, fmt.Sprintf("/admin/l/%s/%s", l.Name, created.ID), http.StatusFound)
				return
			}
		} else {
			idNum, _ := strconv.Atoi(id)
			q := fmt.Sprintf(`mutation($id: ID!, $input: Update%sInput!){ item: update%s(id: $id, input: $input){ id } }`, l.Name, l.Name)
			_, errs = h.exec(s.token, q, map[string]any{"id": idNum, "input": input})
			if len(errs) == 0 {
				http.Redirect(w, r, fmt.Sprintf("/admin/l/%s/%s", l.Name, id), http.StatusFound)
				return
			}
		}
		err = fmt.Errorf("%s", strings.Join(errs, "；"))
	}
	// 失敗：重建表單並顯示錯誤
	var item map[string]any
	if id != "" {
		item = map[string]any{}
	}
	fields := h.buildFormFields(s, l, item)
	renderPage(w, l.Name, s, navLists(s.role), formBody(l, id, fields, err.Error()))
}

func (h *Handler) itemDelete(w http.ResponseWriter, r *http.Request, s *session) {
	l, ok := meta.Get(r.PathValue("list"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	idNum, _ := strconv.Atoi(r.PathValue("id"))
	q := fmt.Sprintf(`mutation($id: ID!){ delete%s(id: $id) }`, l.Name)
	if _, errs := h.exec(s.token, q, map[string]any{"id": idNum}); len(errs) > 0 {
		renderPage(w, l.Name, s, navLists(s.role), errorBody(errs))
		return
	}
	http.Redirect(w, r, "/admin/l/"+l.Name, http.StatusFound)
}

// ---- 表單建構 ----

type option struct {
	ID       string
	Label    string
	Selected bool
}

type formField struct {
	Name     string
	Type     string
	Required bool
	Note     string
	Value    string   // text / integer / timestamp
	Checked  bool     // boolean
	Enum     []string // select
	JSON     string   // richText
	Ref      string   // relationship
	Many     bool
	Options  []option
	Denied   bool // 無權查詢關聯目標 list（欄位唯讀）
}

// buildFormFields 依 meta 欄位型別產生表單欄位（含關聯選項與現值）。
func (h *Handler) buildFormFields(s *session, l *meta.List, item map[string]any) []formField {
	var out []formField
	for _, f := range l.Fields {
		if f.Name == "createdBy" || f.Name == "createdAt" || f.Name == "updatedAt" {
			continue
		}
		ff := formField{Name: f.Name, Type: f.Type, Required: f.Required, Note: f.Note, Enum: f.Enum, Ref: f.Ref, Many: f.Many}
		v := item[f.Name]
		switch f.Type {
		case "boolean":
			ff.Checked, _ = v.(bool)
		case "richText":
			if v != nil {
				b, _ := json.MarshalIndent(v, "", "  ")
				ff.JSON = string(b)
			}
		case "relationship":
			ff.Options, ff.Denied = h.relationOptions(s, f, v)
		case "timestamp":
			if str, ok := v.(string); ok && str != "" {
				if t, err := time.Parse(time.RFC3339, str); err == nil {
					ff.Value = t.Local().Format("2006-01-02T15:04")
				}
			}
		case "select":
			ff.Value, _ = v.(string)
		default:
			switch tv := v.(type) {
			case string:
				ff.Value = tv
			case float64:
				ff.Value = strconv.FormatFloat(tv, 'f', -1, 64)
			}
		}
		out = append(out, ff)
	}
	return out
}

// relationOptions 載入關聯目標 list 的選項（前 100 筆）並標記現值；
// denied 表示無權查詢目標 list（欄位唯讀）。
func (h *Handler) relationOptions(s *session, f meta.Field, current any) (opts []option, denied bool) {
	target, ok := meta.Get(f.Ref)
	if !ok {
		return nil, true
	}
	selected := map[string]bool{}
	switch cv := current.(type) {
	case map[string]any: // 單值關聯 {id label}
		if id, ok := cv["id"].(string); ok {
			selected[id] = true
		}
	case []any: // 多值關聯
		for _, item := range cv {
			if m, ok := item.(map[string]any); ok {
				if id, ok := m["id"].(string); ok {
					selected[id] = true
				}
			}
		}
	}
	q := fmt.Sprintf(`{ items: %s(first: 100){ edges{node{ id %s }} } }`, target.QueryField, target.LabelField)
	data, errs := h.exec(s.token, q, nil)
	if len(errs) > 0 || data["items"] == nil {
		return nil, true // 無權查詢目標 list：欄位唯讀
	}
	var conn struct {
		Edges []struct {
			Node map[string]any `json:"node"`
		} `json:"edges"`
	}
	if err := json.Unmarshal(data["items"], &conn); err != nil {
		return nil, true
	}
	for _, e := range conn.Edges {
		id, _ := e.Node["id"].(string)
		label, _ := e.Node[target.LabelField].(string)
		opts = append(opts, option{ID: id, Label: label, Selected: selected[id]})
	}
	sort.Slice(opts, func(i, j int) bool { return opts[i].Label < opts[j].Label })
	return opts, false
}

// formToInput 將表單值轉為 Create/Update input 物件。
func (h *Handler) formToInput(l *meta.List, r *http.Request, isUpdate bool) (map[string]any, error) {
	input := map[string]any{}
	for _, f := range l.Fields {
		if f.Name == "createdBy" || f.Name == "createdAt" || f.Name == "updatedAt" {
			continue
		}
		raw := strings.TrimSpace(r.PostFormValue(f.Name))
		switch f.Type {
		case "boolean":
			input[f.Name] = r.PostFormValue(f.Name) == "on"
		case "integer":
			if raw == "" {
				continue
			}
			n, err := strconv.Atoi(raw)
			if err != nil {
				return nil, fmt.Errorf("%s 必須是整數", f.Name)
			}
			input[f.Name] = n
		case "timestamp":
			if raw == "" {
				continue
			}
			t, err := time.ParseInLocation("2006-01-02T15:04", raw, time.Local)
			if err != nil {
				return nil, fmt.Errorf("%s 時間格式錯誤", f.Name)
			}
			input[f.Name] = t.Format(time.RFC3339)
		case "select":
			if raw != "" {
				input[f.Name] = raw
			}
		case "richText":
			if raw == "" {
				continue
			}
			var doc map[string]any
			if err := json.Unmarshal([]byte(raw), &doc); err != nil {
				return nil, fmt.Errorf("%s 必須是合法的 JSON（ProseMirror doc）", f.Name)
			}
			input[f.Name] = doc
		case "password":
			if raw != "" {
				input[f.Name] = raw
			} else if !isUpdate {
				return nil, fmt.Errorf("%s 必填", f.Name)
			}
		case "relationship":
			// 欄位未渲染（無權瀏覽目標 list）時不得動到關聯：
			// 多選清空與未渲染在表單上無法區分，靠 hidden marker 辨別
			if r.PostFormValue("_present_"+f.Name) != "1" {
				continue
			}
			ids := r.PostForm[f.Name]
			var idNums []int
			for _, s := range ids {
				if s == "" {
					continue
				}
				n, _ := strconv.Atoi(s)
				idNums = append(idNums, n)
			}
			singular := singularName(f.Name)
			if f.Many {
				if isUpdate {
					// set 語意：先清空再加入
					input["clear"+upperFirst(f.Name)] = true
					if len(idNums) > 0 {
						input["add"+upperFirst(singular)+"IDs"] = idNums
					}
				} else if len(idNums) > 0 {
					input[singular+"IDs"] = idNums
				}
			} else {
				if len(idNums) > 0 {
					input[f.Name+"ID"] = idNums[0]
				} else if isUpdate {
					input["clear"+upperFirst(f.Name)] = true
				}
			}
		default: // text
			if raw == "" && isUpdate && !f.Required {
				continue
			}
			if raw != "" || !isUpdate {
				input[f.Name] = raw
			}
		}
	}
	return input, nil
}

func singularName(s string) string {
	switch {
	case strings.HasSuffix(s, "ies"):
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "s"):
		return s[:len(s)-1]
	default:
		return s
	}
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
