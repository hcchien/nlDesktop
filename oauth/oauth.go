// Package oauth 讓 CMS 兼任 MCP 用的 OAuth 2.1 Authorization Server：
//
//	GET  /.well-known/oauth-authorization-server   AS metadata（RFC 8414）
//	POST /oauth/register                           dynamic client registration（RFC 7591）
//	GET  /oauth/authorize                          登入頁（PKCE 必填）
//	POST /oauth/authorize                          驗證帳密 → 核發 authorization code
//	POST /oauth/token                              code + PKCE verifier → access token
//
// Access token 與 session token 同格式（HMAC），CMS 的 auth middleware
// 直接接受，因此 token 的權限視圖 = 登入使用者本人。
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"time"

	"github.com/hcchien/nl/auth"
	"github.com/hcchien/nl/ent"
	"github.com/hcchien/nl/ent/oauthclient"
	"github.com/hcchien/nl/ent/oauthcode"
	"github.com/hcchien/nl/ent/user"
)

// Server 是 OAuth authorization server 的 HTTP handlers。
type Server struct {
	Client *ent.Client
	Secret []byte
	// AccessTokenTTL 預設 7 天（v1 未實作 refresh token，見 PLAN）
	AccessTokenTTL time.Duration
}

func (s *Server) ttl() time.Duration {
	if s.AccessTokenTTL > 0 {
		return s.AccessTokenTTL
	}
	return 7 * 24 * time.Hour
}

// Mount 將所有 endpoints 掛上 mux。
func (s *Server) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.metadata)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorizeForm)
	mux.HandleFunc("POST /oauth/authorize", s.authorizeSubmit)
	mux.HandleFunc("POST /oauth/token", s.token)
	// 瀏覽器端 MCP client（claude.ai）需要 CORS preflight
	for _, p := range []string{"/.well-known/oauth-authorization-server", "/oauth/register", "/oauth/token"} {
		mux.HandleFunc("OPTIONS "+p, func(w http.ResponseWriter, r *http.Request) {
			cors(w)
			w.WriteHeader(http.StatusNoContent)
		})
	}
}

// BaseURL 推導對外 base URL（支援 reverse proxy 的 X-Forwarded-Proto）。
func BaseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	cors(w)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func (s *Server) metadata(w http.ResponseWriter, r *http.Request) {
	base := BaseURL(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                base,
		"authorization_endpoint":                base + "/oauth/authorize",
		"token_endpoint":                        base + "/oauth/token",
		"registration_endpoint":                 base + "/oauth/register",
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"none"}, // public clients
	})
}

// register：RFC 7591 dynamic client registration（public client，無 secret）。
func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RedirectURIs []string `json:"redirect_uris"`
		ClientName   string   `json:"client_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.RedirectURIs) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid_client_metadata", "error_description": "redirect_uris is required",
		})
		return
	}
	for _, u := range req.RedirectURIs {
		if parsed, err := url.Parse(u); err != nil || !parsed.IsAbs() {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "invalid_redirect_uri", "error_description": "redirect_uris must be absolute URLs",
			})
			return
		}
	}
	clientID := randomHex(16)
	if _, err := s.Client.OAuthClient.Create().
		SetClientID(clientID).
		SetName(req.ClientName).
		SetRedirectUris(req.RedirectURIs).
		Save(auth.WithSystem(r.Context())); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "server_error"})
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_name":                req.ClientName,
		"redirect_uris":              req.RedirectURIs,
		"token_endpoint_auth_method": "none",
	})
}

type authzParams struct {
	ClientID      string
	RedirectURI   string
	State         string
	CodeChallenge string
	Error         string // 登入失敗時的訊息
}

// validateAuthzParams 驗證 authorize 參數；redirect_uri 無效時回傳 error
// （此時不得 redirect，直接 4xx）。
func (s *Server) validateAuthzParams(ctx context.Context, q url.Values) (*authzParams, *ent.OAuthClient, error) {
	p := &authzParams{
		ClientID:      q.Get("client_id"),
		RedirectURI:   q.Get("redirect_uri"),
		State:         q.Get("state"),
		CodeChallenge: q.Get("code_challenge"),
	}
	client, err := s.Client.OAuthClient.Query().
		Where(oauthclient.ClientIDEQ(p.ClientID)).
		Only(auth.WithSystem(ctx))
	if err != nil {
		return nil, nil, fmt.Errorf("unknown client_id")
	}
	if !slices.Contains(client.RedirectUris, p.RedirectURI) {
		return nil, nil, fmt.Errorf("redirect_uri not registered")
	}
	return p, client, nil
}

var loginPage = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="zh-Hant"><head><meta charset="utf-8"><title>nl CMS 授權</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>
body{font-family:system-ui,sans-serif;display:flex;justify-content:center;padding-top:8vh;background:#f5f5f4}
form{background:#fff;padding:2rem;border-radius:12px;box-shadow:0 1px 8px rgba(0,0,0,.08);width:320px}
h1{font-size:1.1rem;margin:0 0 .25rem}p{color:#666;font-size:.85rem;margin:0 0 1.25rem}
label{display:block;font-size:.8rem;margin-bottom:.25rem;color:#444}
input{width:100%;box-sizing:border-box;padding:.5rem;margin-bottom:1rem;border:1px solid #ccc;border-radius:6px}
button{width:100%;padding:.6rem;background:#1a1a1a;color:#fff;border:0;border-radius:6px;cursor:pointer}
.err{color:#b91c1c;font-size:.85rem;margin-bottom:1rem}
</style></head><body>
<form method="post" action="/oauth/authorize">
  <h1>授權存取 nl CMS</h1>
  <p>{{if .ClientName}}「{{.ClientName}}」{{else}}應用程式{{end}}要求以你的身分存取。登入即代表同意。</p>
  {{if .P.Error}}<div class="err">{{.P.Error}}</div>{{end}}
  <label>Email</label><input type="email" name="email" required autofocus>
  <label>密碼</label><input type="password" name="password" required>
  <input type="hidden" name="client_id" value="{{.P.ClientID}}">
  <input type="hidden" name="redirect_uri" value="{{.P.RedirectURI}}">
  <input type="hidden" name="state" value="{{.P.State}}">
  <input type="hidden" name="code_challenge" value="{{.P.CodeChallenge}}">
  <button type="submit">登入並授權</button>
</form></body></html>`))

func (s *Server) renderLogin(w http.ResponseWriter, clientName string, p *authzParams, code int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	loginPage.Execute(w, map[string]any{"ClientName": clientName, "P": p})
}

func (s *Server) authorizeForm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p, client, err := s.validateAuthzParams(r.Context(), q)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if q.Get("response_type") != "code" || p.CodeChallenge == "" || q.Get("code_challenge_method") != "S256" {
		redirectError(w, r, p, "invalid_request", "response_type=code 與 PKCE S256 為必填")
		return
	}
	s.renderLogin(w, client.Name, p, http.StatusOK)
}

func (s *Server) authorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	p, client, err := s.validateAuthzParams(r.Context(), r.PostForm)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	sys := auth.WithSystem(r.Context())
	u, err := s.Client.User.Query().Where(user.EmailEQ(r.PostFormValue("email"))).Only(sys)
	if err != nil || !auth.VerifyPassword(u.Password, r.PostFormValue("password")) {
		p.Error = "帳號或密碼錯誤"
		s.renderLogin(w, client.Name, p, http.StatusUnauthorized)
		return
	}

	code := randomHex(32)
	if _, err := s.Client.OAuthCode.Create().
		SetCodeHash(hashToken(code)).
		SetClientID(p.ClientID).
		SetRedirectURI(p.RedirectURI).
		SetCodeChallenge(p.CodeChallenge).
		SetExpiresAt(time.Now().Add(5 * time.Minute)).
		SetUser(u).
		Save(sys); err != nil {
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	dest, _ := url.Parse(p.RedirectURI)
	q := dest.Query()
	q.Set("code", code)
	if p.State != "" {
		q.Set("state", p.State)
	}
	dest.RawQuery = q.Encode()
	http.Redirect(w, r, dest.String(), http.StatusFound)
}

func redirectError(w http.ResponseWriter, r *http.Request, p *authzParams, code, desc string) {
	dest, err := url.Parse(p.RedirectURI)
	if err != nil {
		http.Error(w, desc, http.StatusBadRequest)
		return
	}
	q := dest.Query()
	q.Set("error", code)
	q.Set("error_description", desc)
	if p.State != "" {
		q.Set("state", p.State)
	}
	dest.RawQuery = q.Encode()
	http.Redirect(w, r, dest.String(), http.StatusFound)
}

// token：authorization_code + PKCE verifier → access token。
func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_request"})
		return
	}
	if r.PostFormValue("grant_type") != "authorization_code" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported_grant_type"})
		return
	}
	sys := auth.WithSystem(r.Context())
	code := r.PostFormValue("code")
	oc, err := s.Client.OAuthCode.Query().
		Where(oauthcode.CodeHashEQ(hashToken(code))).
		WithUser().
		Only(sys)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	// 單次使用：無論後續驗證成敗都先銷毀
	_ = s.Client.OAuthCode.DeleteOne(oc).Exec(sys)

	verifier := r.PostFormValue("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	switch {
	case time.Now().After(oc.ExpiresAt):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "code expired"})
	case oc.ClientID != r.PostFormValue("client_id"):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "client mismatch"})
	case oc.RedirectURI != r.PostFormValue("redirect_uri"):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "redirect_uri mismatch"})
	case subtle.ConstantTimeCompare([]byte(challenge), []byte(oc.CodeChallenge)) != 1:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "PKCE verification failed"})
	default:
		writeJSON(w, http.StatusOK, map[string]any{
			"access_token": auth.SignToken(s.Secret, oc.Edges.User.ID, s.ttl()),
			"token_type":   "Bearer",
			"expires_in":   int(s.ttl().Seconds()),
		})
	}
}

func randomHex(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	return hex.EncodeToString(buf)
}

func hashToken(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
