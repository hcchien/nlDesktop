// Package mcpserver 實作 nl 的 MCP server。
// 不直接碰 DB —— 所有操作經 CMS 的 GraphQL API，以 API key 認證，
// 權限視圖與該 key 綁定的使用者完全一致。cmd/nl-mcp 與整合測試共用。
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hcchien/nl/access"
	"github.com/hcchien/nl/meta"
	"github.com/hcchien/nl/richtext"
)

// New 建立 MCP server，所有 tools 經 gqlURL 的 GraphQL API 操作。
// richText 欄位收到字串（HTML/Markdown）時，經 NL_RICHTEXT_URL 的轉換服務
// 轉為 ProseMirror doc JSON 後落地。
func New(gqlURL, apiKey string) *mcp.Server {
	rtURL := os.Getenv("NL_RICHTEXT_URL")
	if rtURL == "" {
		rtURL = "http://localhost:8082"
	}
	c := &gqlClient{url: gqlURL, key: apiKey, rt: &richtext.Client{URL: rtURL}}
	server := mcp.NewServer(&mcp.Implementation{Name: "nl-mcp", Version: "0.1.0"}, nil)
	registerTools(server, c)
	return server
}

// convertRichTextInputs 將 input 中 richText 欄位的字串值（HTML/Markdown）
// 轉為 PM doc JSON；已是物件（doc JSON）者原樣通過。
func (c *gqlClient) convertRichTextInputs(ctx context.Context, l *meta.List, input map[string]any) error {
	for _, f := range l.Fields {
		if f.Type != "richText" {
			continue
		}
		raw, ok := input[f.Name].(string)
		if !ok {
			continue
		}
		doc, err := c.rt.ToDoc(ctx, raw)
		if err != nil {
			return fmt.Errorf("converting %s.%s from HTML/Markdown: %w（也可直接提供 ProseMirror doc JSON 物件）", l.Name, f.Name, err)
		}
		input[f.Name] = doc
	}
	return nil
}

// NewHTTPHandler 回傳支援 MCP OAuth（RFC 9728 protected resource）的
// streamable HTTP handler：
//   - GET /.well-known/oauth-protected-resource 指向 CMS（authorization server）
//   - 無 Bearer token → 401 + WWW-Authenticate 挑戰（client 據此啟動 OAuth 流程）
//   - 有 token → 逐請求轉發至 CMS GraphQL，權限視圖 = 該 token 的使用者
//
// cmsURL 是 CMS base URL（authorization server），例：https://cms.example.com。
func NewHTTPHandler(gqlURL, cmsURL string) http.Handler {
	server := New(gqlURL, "") // 無固定 key：token 逐請求從 ctx 取得
	inner := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
		h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Mcp-Session-Id, Mcp-Protocol-Version")
		h.Set("Access-Control-Expose-Headers", "Mcp-Session-Id, WWW-Authenticate")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			h.Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]any{
				"resource":              baseURL(r),
				"authorization_servers": []string{cmsURL},
			})
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if token == "" {
			h.Set("WWW-Authenticate",
				fmt.Sprintf(`Bearer resource_metadata=%q`, baseURL(r)+"/.well-known/oauth-protected-resource"))
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		// token 逐請求由 SDK 帶進 tool handler（req.Extra.Header）
		inner.ServeHTTP(w, r)
	})
}

func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ---- per-request token ----

type tokenKey struct{}

func withToken(ctx context.Context, token string) context.Context {
	if token == "" {
		return ctx
	}
	return context.WithValue(ctx, tokenKey{}, token)
}

func tokenFrom(ctx context.Context) string {
	t, _ := ctx.Value(tokenKey{}).(string)
	return t
}

// reqCtx 將 MCP request 的 Bearer token（HTTP 模式，SDK 經 req.Extra.Header 帶入）
// 併入 ctx，供 gqlClient 逐請求使用；stdio 模式無 header，回落到固定 API key。
func reqCtx(ctx context.Context, req *mcp.CallToolRequest) context.Context {
	if req == nil || req.Extra == nil || req.Extra.Header == nil {
		return ctx
	}
	return withToken(ctx, strings.TrimPrefix(req.Extra.Header.Get("Authorization"), "Bearer "))
}

// ---- GraphQL client ----

type gqlClient struct {
	url string
	key string // 固定憑證（stdio 模式的 API key）；HTTP 模式為空、逐請求從 ctx 取
	rt  *richtext.Client
}

type gqlResult struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Errors []struct {
		Message string `json:"message"`
		Path    []any  `json:"path,omitempty"`
	} `json:"errors,omitempty"`
}

// do 執行 GraphQL 操作。GraphQL errors（例如 field-level access denied 的
// partial error）不視為 transport 失敗，一併回傳給 agent 判讀。
func (c *gqlClient) do(ctx context.Context, query string, vars map[string]any) (*gqlResult, error) {
	body, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	token := c.key
	if t := tokenFrom(ctx); t != "" {
		token = t
	}
	if token == "" {
		return nil, fmt.Errorf("no credential: missing Bearer token (OAuth) and no API key configured")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling CMS API: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out gqlResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("CMS API returned non-JSON (status %d): %s", resp.StatusCode, raw)
	}
	return &out, nil
}

func toolResult(res *gqlResult) (map[string]any, error) {
	out := map[string]any{}
	if res.Data != nil {
		out["data"] = json.RawMessage(res.Data)
	}
	if len(res.Errors) > 0 {
		msgs := make([]string, len(res.Errors))
		for i, e := range res.Errors {
			msgs[i] = e.Message
		}
		out["errors"] = msgs
	}
	return out, nil
}

// ---- Tools ----

type emptyIn struct{}

type describeIn struct {
	List string `json:"list" jsonschema:"list 名稱，例如 Post；可用 list_schemas 取得清單"`
}

type queryIn struct {
	List           string         `json:"list" jsonschema:"list 名稱，例如 Post"`
	Where          map[string]any `json:"where,omitempty" jsonschema:"GraphQL WhereInput 物件，例如 {\"titleContains\":\"氣候\"} 或 {\"hasTagsWith\":[{\"name\":\"淨零\"}]}"`
	OrderField     string         `json:"orderField,omitempty" jsonschema:"排序欄位（見 describe_list 的 orderFields）"`
	OrderDirection string         `json:"orderDirection,omitempty" jsonschema:"ASC 或 DESC，預設 DESC"`
	First          int            `json:"first,omitempty" jsonschema:"回傳筆數上限，預設 20"`
	After          string         `json:"after,omitempty" jsonschema:"分頁 cursor（上一頁的 pageInfo.endCursor）"`
}

type getIn struct {
	List string `json:"list" jsonschema:"list 名稱"`
	ID   int    `json:"id" jsonschema:"項目 id"`
}

type createIn struct {
	List  string         `json:"list" jsonschema:"list 名稱"`
	Input map[string]any `json:"input" jsonschema:"Create<List>Input 物件；關聯欄位命名見 describe_list 的 mutationHints"`
}

type updateIn struct {
	List  string         `json:"list" jsonschema:"list 名稱"`
	ID    int            `json:"id" jsonschema:"項目 id"`
	Input map[string]any `json:"input" jsonschema:"Update<List>Input 物件；關聯欄位命名見 describe_list 的 mutationHints"`
}

type graphqlIn struct {
	Query     string         `json:"query" jsonschema:"GraphQL query 或 mutation 原文"`
	Variables map[string]any `json:"variables,omitempty"`
}

func registerTools(s *mcp.Server, c *gqlClient) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_schemas",
		Description: "列出這個 CMS 所有可操作的 lists（資料模型）與摘要。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, map[string]any, error) {
		type item struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			FieldCount  int    `json:"fieldCount"`
		}
		lists := meta.All()
		items := make([]item, len(lists))
		for i, l := range lists {
			items[i] = item{Name: l.Name, Description: l.Description, FieldCount: len(l.Fields)}
		}
		return nil, map[string]any{"lists": items}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "describe_list",
		Description: "取得某個 list 的完整 schema：欄位、型別、關聯、排序欄位、權限規則與 mutation input 命名慣例。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in describeIn) (*mcp.CallToolResult, map[string]any, error) {
		ctx = reqCtx(ctx, req)
		l, ok := meta.Get(in.List)
		if !ok {
			return nil, nil, fmt.Errorf("unknown list %q（用 list_schemas 取得清單）", in.List)
		}
		return nil, map[string]any{
			"name":          l.Name,
			"description":   l.Description,
			"fields":        l.Fields,
			"orderFields":   l.OrderFields,
			"mutationHints": l.MutationHints(),
			"access":        accessSummary(l.Name),
		}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "query_items",
		Description: "查詢某個 list 的項目，支援 filter（WhereInput）、排序與 cursor 分頁。回傳結果已套用你的權限視圖。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in queryIn) (*mcp.CallToolResult, map[string]any, error) {
		ctx = reqCtx(ctx, req)
		l, ok := meta.Get(in.List)
		if !ok {
			return nil, nil, fmt.Errorf("unknown list %q", in.List)
		}
		first := in.First
		if first <= 0 {
			first = 20
		}
		vars := map[string]any{"first": first}
		varDefs := []string{"$first: Int"}
		args := []string{"first: $first"}
		if in.Where != nil {
			vars["where"] = in.Where
			varDefs = append(varDefs, fmt.Sprintf("$where: %sWhereInput", l.Name))
			args = append(args, "where: $where")
		}
		if in.After != "" {
			vars["after"] = in.After
			varDefs = append(varDefs, "$after: Cursor")
			args = append(args, "after: $after")
		}
		if in.OrderField != "" {
			dir := strings.ToUpper(in.OrderDirection)
			if dir != "ASC" {
				dir = "DESC"
			}
			args = append(args, fmt.Sprintf("orderBy: {field: %s, direction: %s}", in.OrderField, dir))
		}
		q := fmt.Sprintf("query(%s){ items: %s(%s){ totalCount pageInfo{hasNextPage endCursor} edges{node{%s}} } }",
			strings.Join(varDefs, ", "), l.QueryField, strings.Join(args, ", "), l.Selection())
		res, err := c.do(ctx, q, vars)
		if err != nil {
			return nil, nil, err
		}
		out, err := toolResult(res)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_item",
		Description: "以 id 取得單一項目。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, map[string]any, error) {
		ctx = reqCtx(ctx, req)
		l, ok := meta.Get(in.List)
		if !ok {
			return nil, nil, fmt.Errorf("unknown list %q", in.List)
		}
		// 以 where:{id} 查單筆（node(id) 需要 global-unique-ID，本框架採 per-table id）
		q := fmt.Sprintf("query($id: ID){ items: %s(where:{id: $id}, first: 1){ edges{node{%s}} } }",
			l.QueryField, l.Selection())
		res, err := c.do(ctx, q, map[string]any{"id": in.ID})
		if err != nil {
			return nil, nil, err
		}
		out, err := toolResult(res)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "create_item",
		Description: "建立項目。input 欄位見 describe_list；richText 欄位可直接給 HTML 或 Markdown 字串（自動轉為結構化格式）。權限不足會回傳 access denied。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in createIn) (*mcp.CallToolResult, map[string]any, error) {
		ctx = reqCtx(ctx, req)
		l, ok := meta.Get(in.List)
		if !ok {
			return nil, nil, fmt.Errorf("unknown list %q", in.List)
		}
		if err := c.convertRichTextInputs(ctx, l, in.Input); err != nil {
			return nil, nil, err
		}
		q := fmt.Sprintf("mutation($input: Create%sInput!){ item: create%s(input: $input){%s} }", l.Name, l.Name, l.Selection())
		res, err := c.do(ctx, q, map[string]any{"input": in.Input})
		if err != nil {
			return nil, nil, err
		}
		out, err := toolResult(res)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_item",
		Description: "更新項目。關聯欄位用 add/remove/clear 前綴（見 describe_list 的 mutationHints）；richText 欄位可直接給 HTML 或 Markdown 字串。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in updateIn) (*mcp.CallToolResult, map[string]any, error) {
		ctx = reqCtx(ctx, req)
		l, ok := meta.Get(in.List)
		if !ok {
			return nil, nil, fmt.Errorf("unknown list %q", in.List)
		}
		if err := c.convertRichTextInputs(ctx, l, in.Input); err != nil {
			return nil, nil, err
		}
		q := fmt.Sprintf("mutation($id: ID!, $input: Update%sInput!){ item: update%s(id: $id, input: $input){%s} }", l.Name, l.Name, l.Selection())
		res, err := c.do(ctx, q, map[string]any{"id": in.ID, "input": in.Input})
		if err != nil {
			return nil, nil, err
		}
		out, err := toolResult(res)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_item",
		Description: "刪除項目（多數 list 僅 admin 可刪）。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getIn) (*mcp.CallToolResult, map[string]any, error) {
		ctx = reqCtx(ctx, req)
		l, ok := meta.Get(in.List)
		if !ok {
			return nil, nil, fmt.Errorf("unknown list %q", in.List)
		}
		q := fmt.Sprintf("mutation($id: ID!){ deletedId: delete%s(id: $id) }", l.Name)
		res, err := c.do(ctx, q, map[string]any{"id": in.ID})
		if err != nil {
			return nil, nil, err
		}
		out, err := toolResult(res)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "graphql",
		Description: "進階：直接執行任意 GraphQL query/mutation（me、巢狀關聯、複雜 filter 等）。權限與其他 tools 完全一致。",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in graphqlIn) (*mcp.CallToolResult, map[string]any, error) {
		ctx = reqCtx(ctx, req)
		res, err := c.do(ctx, in.Query, in.Variables)
		if err != nil {
			return nil, nil, err
		}
		out, err := toolResult(res)
		return nil, out, err
	})
}

// accessSummary 將 access registry 的規則整理成 agent 可讀的摘要。
func accessSummary(list string) map[string]any {
	rules, ok := access.RulesFor(list)
	if !ok {
		return nil
	}
	ops := map[string]any{}
	for op, roles := range rules.Operations {
		ops[string(op)] = roleNames(roles)
	}
	out := map[string]any{"operations": ops}
	if len(rules.OwnerScoped) > 0 {
		scoped := map[string]any{}
		for op, roles := range rules.OwnerScoped {
			scoped[string(op)] = roleNames(roles)
		}
		out["ownerScoped"] = scoped
		out["ownerScopedNote"] = "這些角色在該操作下僅能觸及自己建立的資料"
	}
	if len(rules.Fields) > 0 {
		fields := map[string]any{}
		for name, fr := range rules.Fields {
			f := map[string]any{}
			if fr.Read != nil {
				f["read"] = roleNames(fr.Read)
			}
			if fr.Create != nil {
				f["create"] = roleNames(fr.Create)
			}
			if fr.Update != nil {
				f["update"] = roleNames(fr.Update)
			}
			fields[name] = f
		}
		out["fields"] = fields
	}
	return out
}

func roleNames(s access.RoleSet) []string {
	names := make([]string, 0, len(s))
	for r := range s {
		names = append(names, r)
	}
	return names
}
