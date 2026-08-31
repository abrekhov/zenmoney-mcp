package oauthserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func TestOAuthAuthorizationCodeAndRefreshFlow(t *testing.T) {
	server, err := New(Config{
		BaseURL: "http://localhost:8080", Password: "correct horse battery staple",
		SigningKey: strings.Repeat("k", 48),
	})
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.Register(mux)

	registration := `{"redirect_uris":["https://claude.ai/api/mcp/auth_callback"],"client_name":"Claude","token_endpoint_auth_method":"none"}`
	recorder := request(t, mux, http.MethodPost, "/oauth/register", "application/json", registration)
	if recorder.Code != http.StatusCreated {
		t.Fatalf("registration status %d: %s", recorder.Code, recorder.Body.String())
	}
	var registered struct {
		ClientID string `json:"client_id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &registered); err != nil {
		t.Fatal(err)
	}
	if registered.ClientID == "" {
		t.Fatal("empty client ID")
	}

	verifier := strings.Repeat("v", 64)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	query := url.Values{
		"response_type": {"code"}, "client_id": {registered.ClientID},
		"redirect_uri": {"https://claude.ai/api/mcp/auth_callback"}, "state": {"state-1"},
		"scope":          {ScopeRead + " " + ScopeWrite + " offline_access"},
		"code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	recorder = request(t, mux, http.MethodGet, "/oauth/authorize?"+query.Encode(), "", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("authorize status %d: %s", recorder.Code, recorder.Body.String())
	}
	// The 302 after the form POST targets the client redirect URI. Browsers
	// enforce form-action on redirect targets, so it must be allowed here or
	// Chrome aborts the navigation with ERR_ABORTED and the flow silently dies.
	if csp := recorder.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "https://claude.ai") {
		t.Fatalf("CSP does not allow redirect target: %s", csp)
	}
	match := regexp.MustCompile(`name="login_context" value="([^"]+)"`).FindStringSubmatch(recorder.Body.String())
	if len(match) != 2 {
		t.Fatal("login context missing")
	}

	form := url.Values{"login_context": {match[1]}, "password": {"correct horse battery staple"}}
	recorder = request(t, mux, http.MethodPost, "/oauth/authorize", "application/x-www-form-urlencoded", form.Encode())
	if recorder.Code != http.StatusFound {
		t.Fatalf("consent status %d: %s", recorder.Code, recorder.Body.String())
	}
	redirect, err := url.Parse(recorder.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if redirect.Query().Get("state") != "state-1" || redirect.Query().Get("code") == "" {
		t.Fatalf("bad redirect: %s", redirect)
	}

	tokenForm := url.Values{
		"grant_type": {"authorization_code"}, "code": {redirect.Query().Get("code")},
		"client_id": {registered.ClientID}, "redirect_uri": {"https://claude.ai/api/mcp/auth_callback"},
		"code_verifier": {verifier},
	}
	recorder = request(t, mux, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", tokenForm.Encode())
	if recorder.Code != http.StatusOK {
		t.Fatalf("token status %d: %s", recorder.Code, recorder.Body.String())
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		Scope        string `json:"scope"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &tokens); err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || !strings.Contains(tokens.Scope, ScopeWrite) {
		t.Fatalf("incomplete tokens: %+v", tokens)
	}

	protected := server.Protect(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	authorized := httptest.NewRecorder()
	protected.ServeHTTP(authorized, req)
	if authorized.Code != http.StatusNoContent {
		t.Fatalf("protected status %d: %s", authorized.Code, authorized.Body.String())
	}

	refreshForm := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {registered.ClientID}}
	recorder = request(t, mux, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", refreshForm.Encode())
	if recorder.Code != http.StatusOK {
		t.Fatalf("refresh status %d: %s", recorder.Code, recorder.Body.String())
	}
	replayed := request(t, mux, http.MethodPost, "/oauth/token", "application/x-www-form-urlencoded", refreshForm.Encode())
	if replayed.Code != http.StatusBadRequest || !strings.Contains(replayed.Body.String(), "invalid_grant") {
		t.Fatalf("refresh replay was accepted: %d %s", replayed.Code, replayed.Body.String())
	}
}

func TestMCPWithoutTokenGetsOAuthChallenge(t *testing.T) {
	server, err := New(Config{BaseURL: "http://localhost:8080", Password: "long-enough-password", SigningKey: strings.Repeat("s", 48)})
	if err != nil {
		t.Fatal(err)
	}
	protected := server.Protect(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	recorder := httptest.NewRecorder()
	protected.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("got status %d", recorder.Code)
	}
	challenge := recorder.Header().Get("WWW-Authenticate")
	if !strings.Contains(challenge, "/.well-known/oauth-protected-resource") {
		t.Fatalf("missing resource metadata challenge: %s", challenge)
	}
}

func request(t *testing.T, handler http.Handler, method, target, contentType, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
