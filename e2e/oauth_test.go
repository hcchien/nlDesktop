package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/hcchien/nl/mcpserver"
)

const redirectURI = "http://localhost:19999/callback"

// oauthToken 跑完整 OAuth 2.1 flow（DCR → authorize → code → token），
// 回傳綁定該使用者的 access token。
func oauthToken(t *testing.T, e *env, email string) string {
	access, _, _ := oauthTokens(t, e, email)
	return access
}

// oauthTokens 同 oauthToken，另回傳 refresh token 與 client_id。
func oauthTokens(t *testing.T, e *env, email string) (access, refresh, clientID string) {
	t.Helper()
	// 1. Dynamic client registration
	body, _ := json.Marshal(map[string]any{
		"redirect_uris": []string{redirectURI},
		"client_name":   "e2e-test-client",
	})
	resp, err := http.Post(e.ts.URL+"/oauth/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", resp.StatusCode)
	}
	var reg struct {
		ClientID string `json:"client_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		t.Fatal(err)
	}

	// 2. PKCE
	verifier := strings.Repeat("v", 43)
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// 3. authorize 登入頁（GET 應為 200）
	authzURL := fmt.Sprintf(
		"%s/oauth/authorize?response_type=code&client_id=%s&redirect_uri=%s&state=xyz&code_challenge=%s&code_challenge_method=S256",
		e.ts.URL, reg.ClientID, url.QueryEscape(redirectURI), challenge)
	if resp, err := http.Get(authzURL); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("authorize form: status=%v err=%v", resp.StatusCode, err)
	}

	// 4. 提交帳密 → 302 帶 code
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	form := url.Values{
		"email": {email}, "password": {"password123"},
		"client_id": {reg.ClientID}, "redirect_uri": {redirectURI},
		"state": {"xyz"}, "code_challenge": {challenge},
	}
	resp2, err := noRedirect.PostForm(e.ts.URL+"/oauth/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusFound {
		t.Fatalf("authorize submit status = %d", resp2.StatusCode)
	}
	loc, err := url.Parse(resp2.Header.Get("Location"))
	if err != nil || loc.Query().Get("code") == "" || loc.Query().Get("state") != "xyz" {
		t.Fatalf("bad redirect: %s", resp2.Header.Get("Location"))
	}

	// 5. token exchange（含 PKCE verifier）
	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "code": {loc.Query().Get("code")},
		"redirect_uri": {redirectURI}, "client_id": {reg.ClientID},
		"code_verifier": {verifier},
	}
	resp3, err := http.PostForm(e.ts.URL+"/oauth/token", tokenForm)
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	var tok struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		Error        string `json:"error"`
	}
	if err := json.NewDecoder(resp3.Body).Decode(&tok); err != nil {
		t.Fatal(err)
	}
	if tok.Error != "" || tok.AccessToken == "" || tok.RefreshToken == "" || tok.TokenType != "Bearer" {
		t.Fatalf("token response: %+v (status %d)", tok, resp3.StatusCode)
	}
	return tok.AccessToken, tok.RefreshToken, reg.ClientID
}

type authedTransport struct{ token string }

func (a *authedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r.Header.Set("Authorization", "Bearer "+a.token)
	return http.DefaultTransport.RoundTrip(r)
}

func mcpSession(t *testing.T, ctx context.Context, endpoint, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{
		Endpoint:   endpoint,
		HTTPClient: &http.Client{Transport: &authedTransport{token: token}},
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "e2e-oauth", Version: "0"}, nil).Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connecting MCP over HTTP: %v", err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func TestOAuthFlow(t *testing.T) {
	e := setup(t)
	ctx := context.Background()

	t.Run("PKCEVerifierRequired", func(t *testing.T) {
		// 錯誤的 verifier 必須被拒
		token := func() string {
			// 走一次合法流程拿 code，再用錯的 verifier 換 token
			body, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirectURI}})
			resp, _ := http.Post(e.ts.URL+"/oauth/register", "application/json", bytes.NewReader(body))
			var reg struct {
				ClientID string `json:"client_id"`
			}
			json.NewDecoder(resp.Body).Decode(&reg)
			resp.Body.Close()

			verifier := strings.Repeat("v", 43)
			sum := sha256.Sum256([]byte(verifier))
			challenge := base64.RawURLEncoding.EncodeToString(sum[:])
			noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}}
			resp2, err := noRedirect.PostForm(e.ts.URL+"/oauth/authorize", url.Values{
				"email": {"editor@test.local"}, "password": {"password123"},
				"client_id": {reg.ClientID}, "redirect_uri": {redirectURI},
				"code_challenge": {challenge},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer resp2.Body.Close()
			loc, _ := url.Parse(resp2.Header.Get("Location"))
			return loc.Query().Get("code")
		}()
		resp, err := http.PostForm(e.ts.URL+"/oauth/token", url.Values{
			"grant_type": {"authorization_code"}, "code": {token},
			"redirect_uri": {redirectURI}, "client_id": {"whatever"},
			"code_verifier": {"wrong-verifier-wrong-verifier-wrong-verifier"},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("token with wrong verifier: status = %d, want 400", resp.StatusCode)
		}
	})

	t.Run("WrongPasswordRejected", func(t *testing.T) {
		body, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirectURI}})
		resp, _ := http.Post(e.ts.URL+"/oauth/register", "application/json", bytes.NewReader(body))
		var reg struct {
			ClientID string `json:"client_id"`
		}
		json.NewDecoder(resp.Body).Decode(&reg)
		resp.Body.Close()
		resp2, err := http.PostForm(e.ts.URL+"/oauth/authorize", url.Values{
			"email": {"editor@test.local"}, "password": {"wrong"},
			"client_id": {reg.ClientID}, "redirect_uri": {redirectURI},
			"code_challenge": {"c"},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusUnauthorized {
			t.Errorf("wrong password: status = %d, want 401", resp2.StatusCode)
		}
	})

	t.Run("TokenGrantsUserIdentityOnGraphQL", func(t *testing.T) {
		token := oauthToken(t, e, "editor@test.local")
		body, _ := json.Marshal(map[string]any{"query": `{me{role}}`})
		req, _ := http.NewRequest(http.MethodPost, e.ts.URL+"/graphql", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out gqlResp
		json.NewDecoder(resp.Body).Decode(&out)
		if !strings.Contains(string(out.Data["me"]), `"editor"`) {
			t.Errorf("me via OAuth token = %s, want editor", out.Data["me"])
		}
	})

	t.Run("RefreshRotation", func(t *testing.T) {
		_, refresh, clientID := oauthTokens(t, e, "editor@test.local")

		exchange := func(rt string) (int, map[string]any) {
			resp, err := http.PostForm(e.ts.URL+"/oauth/token", url.Values{
				"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var out map[string]any
			json.NewDecoder(resp.Body).Decode(&out)
			return resp.StatusCode, out
		}

		status, out := exchange(refresh)
		if status != http.StatusOK || out["access_token"] == nil || out["refresh_token"] == nil {
			t.Fatalf("refresh exchange failed: %d %v", status, out)
		}
		// rotation：舊 refresh token 重放必須失敗
		if status, _ := exchange(refresh); status != http.StatusBadRequest {
			t.Errorf("replayed refresh token: status = %d, want 400", status)
		}
		// 新的一組可以繼續換
		if status, _ := exchange(out["refresh_token"].(string)); status != http.StatusOK {
			t.Errorf("rotated refresh token rejected: status = %d", status)
		}
	})

	t.Run("MCPOverHTTP", func(t *testing.T) {
		mcpTS := httptest.NewServer(mcpserver.NewHTTPHandler(e.ts.URL+"/graphql", e.ts.URL))
		t.Cleanup(mcpTS.Close)

		// protected resource metadata 指向 CMS
		resp, err := http.Get(mcpTS.URL + "/.well-known/oauth-protected-resource")
		if err != nil {
			t.Fatal(err)
		}
		var prm struct {
			AuthorizationServers []string `json:"authorization_servers"`
		}
		json.NewDecoder(resp.Body).Decode(&prm)
		resp.Body.Close()
		if len(prm.AuthorizationServers) != 1 || prm.AuthorizationServers[0] != e.ts.URL {
			t.Fatalf("authorization_servers = %v, want [%s]", prm.AuthorizationServers, e.ts.URL)
		}

		// 無 token → 401 + WWW-Authenticate 挑戰
		resp2, err := http.Post(mcpTS.URL, "application/json", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusUnauthorized {
			t.Errorf("no token: status = %d, want 401", resp2.StatusCode)
		}
		if !strings.Contains(resp2.Header.Get("WWW-Authenticate"), "resource_metadata") {
			t.Errorf("missing WWW-Authenticate challenge, got %q", resp2.Header.Get("WWW-Authenticate"))
		}

		// 兩個不同權限的使用者連同一個 nl-mcp，各自登入後權限視圖不同
		adminSession := mcpSession(t, ctx, mcpTS.URL, oauthToken(t, e, "admin@test.local"))
		contribSession := mcpSession(t, ctx, mcpTS.URL, oauthToken(t, e, "contributor@test.local"))

		count := func(s *mcp.ClientSession) float64 {
			res, err := s.CallTool(ctx, &mcp.CallToolParams{
				Name: "query_items", Arguments: map[string]any{"list": "Post"},
			})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError {
				t.Fatalf("query_items error: %+v", res.Content)
			}
			out, ok := res.StructuredContent.(map[string]any)
			if !ok {
				t.Fatalf("unexpected structured content: %#v (content: %+v)", res.StructuredContent, res.Content)
			}
			data, ok := out["data"].(map[string]any)
			if !ok {
				t.Fatalf("no data in tool result: %#v", out)
			}
			return data["items"].(map[string]any)["totalCount"].(float64)
		}
		if n := count(adminSession); n != 2 {
			t.Errorf("admin via OAuth MCP sees %v posts, want 2", n)
		}
		if n := count(contribSession); n != 1 {
			t.Errorf("contributor via OAuth MCP sees %v posts, want 1 (own only)", n)
		}
	})
}
