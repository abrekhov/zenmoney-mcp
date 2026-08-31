package oauthserver

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	ScopeRead  = "zenmoney:read"
	ScopeWrite = "zenmoney:write"
)

type Config struct {
	BaseURL    string
	Password   string
	SigningKey string
}

type Server struct {
	baseURL     string
	resourceURL string
	password    string
	signer      *signer

	usedMu sync.Mutex
	used   map[string]int64
}

func New(config Config) (*Server, error) {
	baseURL := strings.TrimRight(config.BaseURL, "/")
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("MCP_BASE_URL must be an absolute URL")
	}
	if parsed.Scheme != "https" && parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" {
		return nil, fmt.Errorf("MCP_BASE_URL must use HTTPS outside localhost")
	}
	if len(config.Password) < 12 {
		return nil, fmt.Errorf("MCP_OAUTH_PASSWORD must contain at least 12 characters")
	}
	signer, err := newSigner(config.SigningKey)
	if err != nil {
		return nil, err
	}
	return &Server{baseURL: baseURL, resourceURL: baseURL + "/mcp", password: config.Password, signer: signer, used: make(map[string]int64)}, nil
}

func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.protectedResourceMetadata)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authorizationServerMetadata)
	mux.HandleFunc("POST /oauth/register", s.registerClient)
	mux.HandleFunc("GET /oauth/authorize", s.authorizeGet)
	mux.HandleFunc("POST /oauth/authorize", s.authorizePost)
	mux.HandleFunc("POST /oauth/token", s.token)
}

func (s *Server) Protect(next http.Handler) http.Handler {
	middleware := auth.RequireBearerToken(s.verifyAccessToken, &auth.RequireBearerTokenOptions{
		Scopes:              []string{ScopeRead},
		ResourceMetadataURL: s.baseURL + "/.well-known/oauth-protected-resource",
	})
	return middleware(next)
}

func (s *Server) protectedResourceMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.resourceURL,
		"authorization_servers":    []string{s.baseURL},
		"scopes_supported":         []string{ScopeRead, ScopeWrite},
		"bearer_methods_supported": []string{"header"},
	})
}

func (s *Server) authorizationServerMetadata(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.baseURL,
		"authorization_endpoint":                s.baseURL + "/oauth/authorize",
		"token_endpoint":                        s.baseURL + "/oauth/token",
		"registration_endpoint":                 s.baseURL + "/oauth/register",
		"scopes_supported":                      []string{ScopeRead, ScopeWrite, "offline_access"},
		"response_types_supported":              []string{"code"},
		"grant_types_supported":                 []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported": []string{"none"},
		"code_challenge_methods_supported":      []string{"S256"},
	})
}

type registrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	ClientName              string   `json:"client_name"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
}

func (s *Server) registerClient(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var request registrationRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	// RFC 7591 clients may send optional metadata we do not need. Ignore those
	// fields while strictly validating the security-relevant redirect URIs.
	if err := decoder.Decode(&request); err != nil {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid registration document")
		return
	}
	if len(request.RedirectURIs) == 0 || len(request.RedirectURIs) > 10 {
		oauthJSONError(w, http.StatusBadRequest, "invalid_redirect_uri", "at least one redirect URI is required")
		return
	}
	for _, redirect := range request.RedirectURIs {
		if !validRedirectURI(redirect) {
			oauthJSONError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect URI must be HTTPS or an HTTP loopback URI")
			return
		}
	}
	if request.TokenEndpointAuthMethod != "" && request.TokenEndpointAuthMethod != "none" {
		oauthJSONError(w, http.StatusBadRequest, "invalid_client_metadata", "only public clients are supported")
		return
	}
	now := time.Now()
	clientID, err := s.signer.sign(clientClaims{
		baseClaims:   baseClaims{Type: "client", IAT: now.Unix(), EXP: now.AddDate(1, 0, 0).Unix()},
		RedirectURIs: request.RedirectURIs, ClientName: request.ClientName,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id":                  clientID,
		"client_id_issued_at":        now.Unix(),
		"client_name":                request.ClientName,
		"redirect_uris":              request.RedirectURIs,
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
}

func (s *Server) authorizeGet(w http.ResponseWriter, r *http.Request) {
	claims, err := s.authorizationFromQuery(r.URL.Query())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	loginContext, err := s.signer.sign(claims)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	s.renderLogin(w, loginContext, claims, "")
}

func (s *Server) authorizePost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	var claims authorizationClaims
	if err := s.signer.verify(r.FormValue("login_context"), &claims); err != nil || !claims.baseClaims.valid("login", time.Now()) {
		http.Error(w, "authorization request expired; restart the connection", http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.FormValue("password")), []byte(s.password)) != 1 {
		s.renderLogin(w, r.FormValue("login_context"), claims, "Incorrect password")
		return
	}
	claims.Type = "code"
	claims.IAT = time.Now().Unix()
	claims.EXP = time.Now().Add(5 * time.Minute).Unix()
	code, err := s.signer.sign(claims)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	redirect, _ := url.Parse(claims.RedirectURI)
	query := redirect.Query()
	query.Set("code", code)
	if claims.State != "" {
		query.Set("state", claims.State)
	}
	// RFC 9207 Authorization Server Issuer Identification — required by MCP
	// clients (including Claude.ai) to prevent mix-up attacks.
	query.Set("iss", s.baseURL)
	redirect.RawQuery = query.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

func (s *Server) authorizationFromQuery(query url.Values) (authorizationClaims, error) {
	if query.Get("response_type") != "code" {
		return authorizationClaims{}, fmt.Errorf("response_type must be code")
	}
	var client clientClaims
	clientID := query.Get("client_id")
	if err := s.signer.verify(clientID, &client); err != nil || !client.baseClaims.valid("client", time.Now()) {
		return authorizationClaims{}, fmt.Errorf("invalid or expired client_id")
	}
	redirect := query.Get("redirect_uri")
	if !slices.Contains(client.RedirectURIs, redirect) {
		return authorizationClaims{}, fmt.Errorf("redirect_uri is not registered")
	}
	if query.Get("code_challenge_method") != "S256" || query.Get("code_challenge") == "" {
		return authorizationClaims{}, fmt.Errorf("S256 PKCE is required")
	}
	scopes, err := validateScopes(query.Get("scope"))
	if err != nil {
		return authorizationClaims{}, err
	}
	now := time.Now()
	return authorizationClaims{
		baseClaims: baseClaims{Type: "login", IAT: now.Unix(), EXP: now.Add(10 * time.Minute).Unix()},
		ClientID:   clientID, RedirectURI: redirect, State: query.Get("state"), Scopes: scopes,
		CodeChallenge: query.Get("code_challenge"), Nonce: randomString(24),
	}, nil
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		oauthJSONError(w, http.StatusBadRequest, "invalid_request", "invalid form body")
		return
	}
	switch r.FormValue("grant_type") {
	case "authorization_code":
		s.exchangeCode(w, r)
	case "refresh_token":
		s.refresh(w, r)
	default:
		oauthJSONError(w, http.StatusBadRequest, "unsupported_grant_type", "supported grants are authorization_code and refresh_token")
	}
}

func (s *Server) exchangeCode(w http.ResponseWriter, r *http.Request) {
	var claims authorizationClaims
	if err := s.signer.verify(r.FormValue("code"), &claims); err != nil || !claims.baseClaims.valid("code", time.Now()) {
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired authorization code")
		return
	}
	if s.isUsed(claims.Nonce) {
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "authorization code was already used")
		return
	}
	if r.FormValue("client_id") != claims.ClientID || r.FormValue("redirect_uri") != claims.RedirectURI {
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "client or redirect mismatch")
		return
	}
	digest := sha256.Sum256([]byte(r.FormValue("code_verifier")))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	if subtle.ConstantTimeCompare([]byte(challenge), []byte(claims.CodeChallenge)) != 1 {
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "PKCE verification failed")
		return
	}
	s.markUsed(claims.Nonce, claims.EXP)
	s.issueTokens(w, claims.ClientID, claims.Scopes)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var claims accessClaims
	if err := s.signer.verify(r.FormValue("refresh_token"), &claims); err != nil || !claims.baseClaims.valid("refresh", time.Now()) {
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "invalid or expired refresh token")
		return
	}
	if s.isUsed(claims.Nonce) {
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "refresh token was already used")
		return
	}
	if clientID := r.FormValue("client_id"); clientID != "" && clientID != claims.ClientID {
		oauthJSONError(w, http.StatusBadRequest, "invalid_grant", "client mismatch")
		return
	}
	s.markUsed(claims.Nonce, claims.EXP)
	s.issueTokens(w, claims.ClientID, claims.Scopes)
}

func (s *Server) issueTokens(w http.ResponseWriter, clientID string, scopes []string) {
	now := time.Now()
	accessToken, err := s.signer.sign(accessClaims{
		baseClaims: baseClaims{Type: "access", IAT: now.Unix(), EXP: now.Add(time.Hour).Unix()},
		Subject:    "owner", Audience: s.resourceURL, ClientID: clientID, Scopes: scopes, Nonce: randomString(24),
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	refreshToken, err := s.signer.sign(accessClaims{
		baseClaims: baseClaims{Type: "refresh", IAT: now.Unix(), EXP: now.Add(30 * 24 * time.Hour).Unix()},
		Subject:    "owner", Audience: s.resourceURL, ClientID: clientID, Scopes: scopes, Nonce: randomString(24),
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": accessToken, "token_type": "Bearer", "expires_in": 3600,
		"refresh_token": refreshToken, "scope": strings.Join(scopes, " "),
	})
}

func (s *Server) verifyAccessToken(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
	var claims accessClaims
	if err := s.signer.verify(token, &claims); err != nil || !claims.baseClaims.valid("access", time.Now()) || claims.Audience != s.resourceURL {
		return nil, auth.ErrInvalidToken
	}
	return &auth.TokenInfo{Scopes: claims.Scopes, Expiration: time.Unix(claims.EXP, 0), UserID: claims.Subject}, nil
}

func (s *Server) renderLogin(w http.ResponseWriter, loginContext string, claims authorizationClaims, message string) {
	var client clientClaims
	_ = s.signer.verify(claims.ClientID, &client)
	name := client.ClientName
	if name == "" {
		name = "MCP client"
	}
	redirect, _ := url.Parse(claims.RedirectURI)
	// The 302 after the form POST navigates to the client's redirect URI, and
	// browsers enforce form-action on redirect targets — so it must be allowed
	// here explicitly, or Chrome aborts the navigation with ERR_ABORTED.
	redirectOrigin := ""
	if redirect.Host != "" {
		redirectOrigin = redirect.Scheme + "://" + redirect.Host
	}
	csp := "default-src 'none'; style-src 'unsafe-inline'; form-action 'self' " + redirectOrigin + "; base-uri 'none'; frame-ancestors 'none'"
	data := struct {
		Context, Message, Client, Redirect, Scopes string
	}{loginContext, message, name, redirect.Host, strings.Join(claims.Scopes, ", ")}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Frame-Options", "DENY")
	_ = loginTemplate.Execute(w, data)
}

func validateScopes(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return []string{ScopeRead}, nil
	}
	var result []string
	for _, scope := range strings.Fields(raw) {
		if scope == "offline_access" {
			continue
		}
		if scope != ScopeRead && scope != ScopeWrite {
			return nil, fmt.Errorf("unsupported scope %q", scope)
		}
		if !slices.Contains(result, scope) {
			result = append(result, scope)
		}
	}
	if !slices.Contains(result, ScopeRead) {
		result = append(result, ScopeRead)
	}
	return result, nil
}

func validRedirectURI(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Fragment != "" || parsed.Host == "" {
		return false
	}
	if parsed.Scheme == "https" {
		return true
	}
	return parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || parsed.Hostname() == "127.0.0.1" || parsed.Hostname() == "::1")
}

func (s *Server) isUsed(nonce string) bool {
	s.usedMu.Lock()
	defer s.usedMu.Unlock()
	now := time.Now().Unix()
	for key, expiry := range s.used {
		if expiry <= now {
			delete(s.used, key)
		}
	}
	_, ok := s.used[nonce]
	return ok
}

func (s *Server) markUsed(nonce string, expiry int64) {
	s.usedMu.Lock()
	s.used[nonce] = expiry
	s.usedMu.Unlock()
}

func randomString(size int) string {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(data)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func oauthJSONError(w http.ResponseWriter, status int, code, description string) {
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

var loginTemplate = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Authorize ZenMoney MCP</title><style>
body{font-family:ui-sans-serif,system-ui,sans-serif;background:#f5f5f4;color:#1c1917;margin:0;display:grid;min-height:100vh;place-items:center}.card{background:white;padding:2rem;border-radius:1rem;box-shadow:0 10px 30px #0002;max-width:30rem;width:calc(100% - 3rem)}h1{margin-top:0}.meta{background:#f5f5f4;padding:1rem;border-radius:.6rem;font-size:.9rem}.error{color:#b91c1c;font-weight:600}label{display:block;margin:1.25rem 0 .4rem}input{box-sizing:border-box;width:100%;font:inherit;padding:.75rem;border:1px solid #a8a29e;border-radius:.5rem}button{margin-top:1rem;width:100%;padding:.8rem;border:0;border-radius:.5rem;background:#292524;color:white;font:inherit;font-weight:700;cursor:pointer}.warning{color:#57534e;font-size:.85rem}</style></head>
<body><main class="card"><h1>Authorize ZenMoney MCP</h1><p><strong>{{.Client}}</strong> requests access to your ZenMoney MCP server.</p><div class="meta">Redirect host: <strong>{{.Redirect}}</strong><br>Scopes: <strong>{{.Scopes}}</strong></div>{{if .Message}}<p class="error">{{.Message}}</p>{{end}}<form method="post" action="/oauth/authorize"><input type="hidden" name="login_context" value="{{.Context}}"><label for="password">Authorization password</label><input id="password" name="password" type="password" autocomplete="current-password" required autofocus><button type="submit">Authorize</button></form><p class="warning">Only continue if you initiated this connection. This server never exposes a delete tool.</p></main></body></html>`))
