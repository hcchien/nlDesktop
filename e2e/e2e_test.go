// Package e2e 對真實 PostgreSQL 執行端到端整合測試：
// 完整權限矩陣（list / row / field 三層 × 四角色），
// 以及「MCP 操作與 GraphQL 直連權限一致」的驗證。
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/post"
	"github.com/hcchien/nl/ent/user"
	"github.com/hcchien/nl/mcpserver"
	"github.com/hcchien/nl/nltest"
	"github.com/hcchien/nl/server"
)

var testSecret = []byte("test-secret")

type env struct {
	ts     *httptest.Server
	client *ent.Client
	users  map[string]*ent.User // key: role
	tokens map[string]string    // key: role
	posts  map[string]*ent.Post // "editor" 的已發布文章、"contributor" 的草稿
}

func setup(t *testing.T) *env {
	t.Helper()
	client := nltest.Open(t)
	sys := auth.WithSystem(context.Background())

	e := &env{
		client: client,
		users:  map[string]*ent.User{},
		tokens: map[string]string{},
		posts:  map[string]*ent.Post{},
	}
	for _, role := range []string{"admin", "moderator", "editor", "contributor"} {
		u, err := client.User.Create().
			SetName(strings.Title(role)).
			SetEmail(role + "@test.local").
			SetPassword("password123").
			SetRole(user.Role(role)).
			Save(sys)
		if err != nil {
			t.Fatalf("creating %s: %v", role, err)
		}
		e.users[role] = u
		e.tokens[role] = auth.SignToken(testSecret, u.ID, time.Hour)
	}

	p1, err := client.Post.Create().
		SetTitle("editor 的已發布文章").
		SetState(post.StatePublished).
		SetPublishTime(time.Now()).
		SetCreatedBy(e.users["editor"]).
		Save(sys)
	if err != nil {
		t.Fatalf("seeding post: %v", err)
	}
	e.posts["editor"] = p1

	p2, err := client.Post.Create().
		SetTitle("contributor 的草稿").
		SetCreatedBy(e.users["contributor"]).
		Save(sys)
	if err != nil {
		t.Fatalf("seeding post: %v", err)
	}
	e.posts["contributor"] = p2

	e.ts = httptest.NewServer(server.New(client, testSecret))
	t.Cleanup(e.ts.Close)
	return e
}

type gqlResp struct {
	Data   map[string]json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (e *env) gql(t *testing.T, role, query string, vars map[string]any) *gqlResp {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"query": query, "variables": vars})
	req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if role != "" {
		req.Header.Set("Authorization", "Bearer "+e.tokens[role])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("gql request: %v", err)
	}
	defer resp.Body.Close()
	var out gqlResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	return &out
}

func (r *gqlResp) hasErrorContaining(sub string) bool {
	for _, e := range r.Errors {
		if strings.Contains(e.Message, sub) {
			return true
		}
	}
	return false
}

func (r *gqlResp) mustNoErrors(t *testing.T) {
	t.Helper()
	if len(r.Errors) > 0 {
		t.Fatalf("unexpected errors: %+v", r.Errors)
	}
}

func postCount(t *testing.T, r *gqlResp) int {
	t.Helper()
	var conn struct {
		TotalCount int `json:"totalCount"`
	}
	if err := json.Unmarshal(r.Data["posts"], &conn); err != nil {
		t.Fatalf("parsing posts connection: %v (data: %s)", err, r.Data["posts"])
	}
	return conn.TotalCount
}

const postsQuery = `{posts(first:10){totalCount edges{node{id title state}}}}`

func TestPermissionMatrix(t *testing.T) {
	e := setup(t)

	t.Run("Login", func(t *testing.T) {
		r := e.gql(t, "", `mutation{login(email:"editor@test.local",password:"password123"){token user{role}}}`, nil)
		r.mustNoErrors(t)
		if !strings.Contains(string(r.Data["login"]), `"editor"`) {
			t.Errorf("login payload = %s", r.Data["login"])
		}
		r = e.gql(t, "", `mutation{login(email:"editor@test.local",password:"wrong"){token}}`, nil)
		if !r.hasErrorContaining("invalid email or password") {
			t.Errorf("wrong password not rejected: %+v", r.Errors)
		}
	})

	t.Run("UnauthenticatedDenied", func(t *testing.T) {
		r := e.gql(t, "", postsQuery, nil)
		if !r.hasErrorContaining("access denied") {
			t.Errorf("expected access denied, got %+v", r.Errors)
		}
	})

	t.Run("ListLevel_AdminAndModeratorSeeAll", func(t *testing.T) {
		for _, role := range []string{"admin", "moderator"} {
			r := e.gql(t, role, postsQuery, nil)
			r.mustNoErrors(t)
			if n := postCount(t, r); n != 2 {
				t.Errorf("%s sees %d posts, want 2", role, n)
			}
		}
	})

	t.Run("RowLevel_EditorAndContributorSeeOwn", func(t *testing.T) {
		for _, role := range []string{"editor", "contributor"} {
			r := e.gql(t, role, postsQuery, nil)
			r.mustNoErrors(t)
			if n := postCount(t, r); n != 1 {
				t.Errorf("%s sees %d posts, want 1 (own only)", role, n)
			}
			if !strings.Contains(string(r.Data["posts"]), role+" 的") {
				t.Errorf("%s sees wrong post: %s", role, r.Data["posts"])
			}
		}
	})

	t.Run("RowLevel_EditorCannotUpdateOthersPost", func(t *testing.T) {
		q := fmt.Sprintf(`mutation{updatePost(id:%d,input:{title:"hijack"}){id}}`, e.posts["contributor"].ID)
		r := e.gql(t, "editor", q, nil)
		if !r.hasErrorContaining("can only update own Post") {
			t.Errorf("expected row-level denial, got %+v", r.Errors)
		}
	})

	t.Run("FieldLevel_ContributorCannotUpdateState", func(t *testing.T) {
		q := fmt.Sprintf(`mutation{updatePost(id:%d,input:{state:published,publishTime:"2026-07-09T00:00:00Z"}){id}}`, e.posts["contributor"].ID)
		r := e.gql(t, "contributor", q, nil)
		if !r.hasErrorContaining("cannot update field Post.state") {
			t.Errorf("expected field-level denial, got %+v", r.Errors)
		}
		// 但可以改自己文章的其他欄位
		q = fmt.Sprintf(`mutation{updatePost(id:%d,input:{title:"改標題 OK"}){title}}`, e.posts["contributor"].ID)
		e.gql(t, "contributor", q, nil).mustNoErrors(t)
	})

	t.Run("CreatedByAutoSet", func(t *testing.T) {
		// contributor 不可查 User：展開 createdBy 時優雅降級為 null
		// （關聯讀取跟隨目標 list 權限，Keystone 語意），不影響操作本身
		r := e.gql(t, "contributor", `mutation{createPost(input:{title:"新草稿"}){id state createdBy{id}}}`, nil)
		r.mustNoErrors(t)
		if !strings.Contains(string(r.Data["createPost"]), `"createdBy":null`) {
			t.Errorf("expected createdBy degraded to null for contributor, got %s", r.Data["createPost"])
		}
		// 由 admin 驗證 createdBy 已自動掛上 contributor
		r = e.gql(t, "contributor", `mutation{createPost(input:{title:"新草稿2"}){id}}`, nil)
		r.mustNoErrors(t)
		var created struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(r.Data["createPost"], &created); err != nil {
			t.Fatal(err)
		}
		q := fmt.Sprintf(`{posts(where:{id:%s}){edges{node{createdBy{id}}}}}`, created.ID)
		r = e.gql(t, "admin", q, nil)
		r.mustNoErrors(t)
		want := fmt.Sprintf(`"id":"%d"`, e.users["contributor"].ID)
		if !strings.Contains(string(r.Data["posts"]), want) {
			t.Errorf("createdBy not auto-set: %s", r.Data["posts"])
		}
	})

	t.Run("Validation_PublishRequiresPublishTime", func(t *testing.T) {
		r := e.gql(t, "moderator", `mutation{createPost(input:{title:"缺發布時間",state:published}){id}}`, nil)
		if !r.hasErrorContaining("publishTime") {
			t.Errorf("expected publishTime validation error, got %+v", r.Errors)
		}
		r = e.gql(t, "moderator", `mutation{createPost(input:{title:"有發布時間",state:published,publishTime:"2026-07-09T00:00:00Z"}){id state}}`, nil)
		r.mustNoErrors(t)
	})

	t.Run("Delete_AdminOnly", func(t *testing.T) {
		q := fmt.Sprintf(`mutation{deletePost(id:%d)}`, e.posts["editor"].ID)
		for _, role := range []string{"contributor", "editor", "moderator"} {
			r := e.gql(t, role, q, nil)
			if !r.hasErrorContaining("cannot delete Post") {
				t.Errorf("%s should not delete, got %+v", role, r.Errors)
			}
		}
		e.gql(t, "admin", q, nil).mustNoErrors(t)
	})

	t.Run("FieldLevel_UserEmailReadRestricted", func(t *testing.T) {
		q := `{users(first:10){edges{node{id name email}}}}`
		r := e.gql(t, "editor", q, nil)
		if !r.hasErrorContaining("cannot read User.email") {
			t.Errorf("expected email read denial for editor, got %+v", r.Errors)
		}
		r = e.gql(t, "admin", q, nil)
		r.mustNoErrors(t)
		if !strings.Contains(string(r.Data["users"]), "admin@test.local") {
			t.Errorf("admin cannot read emails: %s", r.Data["users"])
		}
		// 本人例外：editor 可讀自己的 email
		r = e.gql(t, "editor", `{me{email}}`, nil)
		r.mustNoErrors(t)
		if !strings.Contains(string(r.Data["me"]), "editor@test.local") {
			t.Errorf("editor cannot read own email via me: %s", r.Data["me"])
		}
	})

	t.Run("ListLevel_ContributorCannotQueryUsers", func(t *testing.T) {
		r := e.gql(t, "contributor", `{users(first:1){totalCount}}`, nil)
		if !r.hasErrorContaining("cannot query User") {
			t.Errorf("expected user query denial, got %+v", r.Errors)
		}
	})

	t.Run("APIKeyBindsUserIdentity", func(t *testing.T) {
		r := e.gql(t, "editor", `mutation{createApiKey(name:"test"){key}}`, nil)
		r.mustNoErrors(t)
		var payload struct {
			Key string `json:"key"`
		}
		if err := json.Unmarshal(r.Data["createApiKey"], &payload); err != nil {
			t.Fatalf("parsing api key: %v", err)
		}
		// 用 API key 打 me，應為 editor 本人
		body, _ := json.Marshal(map[string]any{"query": `{me{role}}`})
		req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+payload.Key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out gqlResp
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(out.Data["me"]), `"editor"`) {
			t.Errorf("api key viewer = %s, want editor", out.Data["me"])
		}
	})
}

// TestRichText 驗證 richText 欄位：node 白名單（Go 端，永遠執行）
// 與 MCP 的 HTML ingest（需 converter 服務，未啟動則 skip）。
func TestRichText(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	t.Run("WhitelistRejectsUnknownNode", func(t *testing.T) {
		r := e.gql(t, "editor",
			`mutation($doc: JSON){createPost(input:{title:"惡意內容",content:$doc}){id}}`,
			map[string]any{"doc": map[string]any{
				"type":    "doc",
				"content": []any{map[string]any{"type": "script"}},
			}})
		if !r.hasErrorContaining("not allowed") {
			t.Errorf("expected whitelist rejection, got %+v", r.Errors)
		}
	})

	t.Run("ValidDocAccepted", func(t *testing.T) {
		r := e.gql(t, "editor",
			`mutation($doc: JSON){createPost(input:{title:"合法內容",content:$doc}){id content}}`,
			map[string]any{"doc": map[string]any{
				"type": "doc",
				"content": []any{map[string]any{
					"type":    "paragraph",
					"content": []any{map[string]any{"type": "text", "text": "hello"}},
				}},
			}})
		r.mustNoErrors(t)
		if !strings.Contains(string(r.Data["createPost"]), `"hello"`) {
			t.Errorf("content not round-tripped: %s", r.Data["createPost"])
		}
	})

	t.Run("MCPHTMLIngest", func(t *testing.T) {
		rtURL := os.Getenv("NL_RICHTEXT_URL")
		if rtURL == "" {
			rtURL = "http://localhost:8082"
		}
		if resp, err := http.Get(rtURL + "/healthz"); err != nil || resp.StatusCode != 200 {
			t.Skipf("skipping: richtext converter not running at %s (cd converter && npm start)", rtURL)
		}

		plain, hash := auth.NewAPIKey()
		if _, err := e.client.ApiKey.Create().SetName("rt-test").SetKeyHash(hash).
			SetUser(e.users["editor"]).Save(auth.WithSystem(ctx)); err != nil {
			t.Fatal(err)
		}
		srv := mcpserver.New(e.ts.URL+"/graphql", plain)
		st, ct := mcp.NewInMemoryTransports()
		if _, err := srv.Connect(ctx, st, nil); err != nil {
			t.Fatal(err)
		}
		session, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, ct, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { session.Close() })

		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "create_item", Arguments: map[string]any{
			"list": "Post",
			"input": map[string]any{
				"title":   "HTML ingest 測試",
				"content": `<h2>段落標題</h2><p>由 <strong>HTML</strong> 轉換而來。</p><div data-type="slideshow" data-photo-ids="1,2"></div>`,
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("tool error: %+v", res.Content)
		}
		out := res.StructuredContent.(map[string]any)
		item := out["data"].(map[string]any)["item"].(map[string]any)
		content, ok := item["content"].(map[string]any)
		if !ok || content["type"] != "doc" {
			t.Fatalf("content not converted to PM doc: %v", item["content"])
		}
		nodes := content["content"].([]any)
		last := nodes[len(nodes)-1].(map[string]any)
		if last["type"] != "slideshow" {
			t.Errorf("custom block not preserved: %v", last)
		}
	})
}

// TestMCPMatchesGraphQLPermissions 驗證經 MCP 操作的權限視圖
// 與 GraphQL 直連完全一致（editor 的 API key）。
func TestMCPMatchesGraphQLPermissions(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	plain, hash := auth.NewAPIKey()
	if _, err := e.client.ApiKey.Create().
		SetName("mcp-test").
		SetKeyHash(hash).
		SetUser(e.users["editor"]).
		Save(auth.WithSystem(ctx)); err != nil {
		t.Fatalf("creating api key: %v", err)
	}

	srv := mcpserver.New(e.ts.URL+"/graphql", plain)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, st, nil); err != nil {
		t.Fatalf("connecting server transport: %v", err)
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("connecting client: %v", err)
	}
	t.Cleanup(func() { session.Close() })

	call := func(t *testing.T, tool string, args map[string]any) map[string]any {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatalf("calling %s: %v", tool, err)
		}
		if res.IsError {
			t.Fatalf("%s returned tool error: %+v", tool, res.Content)
		}
		out, ok := res.StructuredContent.(map[string]any)
		if !ok {
			t.Fatalf("%s: unexpected structured content %T", tool, res.StructuredContent)
		}
		return out
	}

	t.Run("QueryRespectsRowFilter", func(t *testing.T) {
		out := call(t, "query_items", map[string]any{"list": "Post"})
		items := out["data"].(map[string]any)["items"].(map[string]any)
		if n := items["totalCount"].(float64); n != 1 {
			t.Errorf("editor via MCP sees %v posts, want 1 (own only)", n)
		}
	})

	t.Run("CreateSetsCreatedBy", func(t *testing.T) {
		out := call(t, "create_item", map[string]any{
			"list":  "Post",
			"input": map[string]any{"title": "MCP e2e 文章"},
		})
		item := out["data"].(map[string]any)["item"].(map[string]any)
		// createdBy 不在預設 selection（低權限角色無法展開 User），用 graphql tool 驗證
		out = call(t, "graphql", map[string]any{
			"query": fmt.Sprintf(`{posts(where:{id:%v}){edges{node{createdBy{id}}}}}`, item["id"]),
		})
		if !strings.Contains(fmt.Sprintf("%v", out["data"]), fmt.Sprintf("map[id:%d]", e.users["editor"].ID)) {
			t.Errorf("createdBy not set to editor: %v", out["data"])
		}
	})

	t.Run("DeleteDenied", func(t *testing.T) {
		out := call(t, "delete_item", map[string]any{"list": "Post", "id": e.posts["editor"].ID})
		errs, _ := out["errors"].([]any)
		if len(errs) == 0 || !strings.Contains(errs[0].(string), "access denied") {
			t.Errorf("expected access denied via MCP, got %+v", out)
		}
	})

	t.Run("DescribeExposesAccessRules", func(t *testing.T) {
		out := call(t, "describe_list", map[string]any{"list": "Post"})
		acc := out["access"].(map[string]any)
		ops := acc["operations"].(map[string]any)
		del := ops["delete"].([]any)
		if len(del) != 1 || del[0] != "admin" {
			t.Errorf("delete roles = %v, want [admin]", del)
		}
	})
}
