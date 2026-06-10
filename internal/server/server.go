package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"broute/internal/auth"
	"broute/internal/config"
	"broute/internal/executors"
	"broute/internal/providers"
	"broute/internal/sse"
	"broute/internal/store"
)

type Server struct {
	cfg      config.Config
	store    *store.Store
	auth     *auth.Manager
	mux      *http.ServeMux
	webProxy *httputil.ReverseProxy
	oauthMu  sync.Mutex
	oauth    map[string]oauthSession
}

type oauthSession struct {
	Provider    string
	Verifier    string
	RedirectURI string
	CreatedAt   time.Time
}

func New(cfg config.Config, st *store.Store) http.Handler {
	s := &Server{cfg: cfg, store: st, auth: auth.New(cfg.JWTSecret, cfg.AuthCookieSecure), mux: http.NewServeMux(), oauth: map[string]oauthSession{}}
	if cfg.WebDevProxy != "" {
		if target, err := url.Parse(cfg.WebDevProxy); err == nil {
			s.webProxy = httputil.NewSingleHostReverseProxy(target)
		}
	}
	s.routes()
	return s.withCORS(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/api/health/ping", s.handlePing)
	s.mux.HandleFunc("/api/settings/require-login", s.handleRequireLogin)
	s.mux.HandleFunc("/api/auth/login", s.handleLogin)
	s.mux.HandleFunc("/api/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("/api/auth/logout", s.handleLogout)
	s.mux.HandleFunc("/api/update", s.requireAuth(s.handleUpdate))
	s.mux.HandleFunc("/api/oauth/", s.requireAuth(s.handleOAuth))
	s.mux.HandleFunc("/api/providers", s.requireAuth(s.handleProviders))
	s.mux.HandleFunc("/api/providers/", s.requireAuth(s.handleProviderByID))
	s.mux.HandleFunc("/api/v1/models", s.handleModels)
	s.mux.HandleFunc("/api/v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("/api/v1/responses", s.handleResponses)
	s.mux.HandleFunc("/", s.handleStatic)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUpdate(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		latest, err := latestGitHubRelease(r.Context())
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"currentVersion": s.cfg.AppVersion, "latestVersion": "", "updateAvailable": false, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"currentVersion": s.cfg.AppVersion, "latestVersion": latest, "updateAvailable": versionDifferent(s.cfg.AppVersion, latest)})
	case http.MethodPost:
		cmd := exec.CommandContext(r.Context(), "npx", "broute", "update")
		cmd.Env = append(os.Environ(), "BROWSER=none")
		output, err := cmd.CombinedOutput()
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error(), "output": strings.TrimSpace(string(output))})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "output": strings.TrimSpace(string(output)), "restartRequired": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleRequireLogin(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.store.GetSettings()
		if err != nil {
			writeJSON(w, http.StatusOK, requireLoginFallback(s.cfg))
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"requireLogin": settings.RequireLogin, "hasPassword": settings.PasswordHash != "", "setupComplete": settings.SetupComplete, "nodeVersion": s.cfg.GoVersion, "nodeCompatible": true})
	case http.MethodPost:
		settings, err := s.store.GetSettings()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to read settings")
			return
		}
		if settings.PasswordHash != "" && !s.auth.Authenticated(r) {
			writeError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		var body struct {
			RequireLogin *bool  `json:"requireLogin"`
			Password     string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		updates := map[string]string{"setupComplete": "true"}
		if body.RequireLogin != nil {
			if *body.RequireLogin {
				updates["requireLogin"] = "true"
			} else {
				updates["requireLogin"] = "false"
			}
		}
		if body.Password != "" {
			hash, err := auth.HashPassword(body.Password)
			if err != nil {
				writeError(w, http.StatusInternalServerError, "Failed to hash password")
				return
			}
			updates["password"] = hash
		}
		if err := s.store.UpdateSettings(updates); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to update settings")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if !s.auth.Configured() {
		s.store.Audit("auth.login.misconfigured", "system", "failed", nil)
		writeError(w, http.StatusInternalServerError, "Server misconfigured: JWT_SECRET not set. Contact administrator.")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Password == "" {
		writeError(w, http.StatusBadRequest, "Invalid password payload")
		return
	}
	settings, err := s.store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Internal server error")
		return
	}
	ip := clientIP(r)
	if ok, retry := s.auth.CheckGuard(ip, settings.BruteForceProtection); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(retry))
		writeError(w, http.StatusTooManyRequests, "Too many failed attempts. Try again later.")
		return
	}
	if settings.PasswordHash == "" {
		s.store.Audit("auth.login.setup_required", "anonymous", "failed", map[string]any{"reason": "missing_persisted_password"})
		writeJSONStatus(w, http.StatusForbidden, map[string]any{"error": "No password configured. Complete onboarding first.", "needsSetup": true})
		return
	}
	if auth.VerifyPassword(body.Password, settings.PasswordHash) {
		if err := s.auth.IssueCookie(w, r); err != nil {
			writeError(w, http.StatusInternalServerError, "Internal server error")
			return
		}
		s.auth.ClearFailures(ip)
		s.store.Audit("auth.login.success", "admin", "success", nil)
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	if ok, retry := s.auth.RecordFailure(ip, settings.BruteForceProtection); !ok {
		w.Header().Set("Retry-After", fmt.Sprint(retry))
		writeError(w, http.StatusTooManyRequests, "Too many failed attempts. Try again later.")
		return
	}
	s.store.Audit("auth.login.failed", "anonymous", "failed", map[string]any{"reason": "invalid_password"})
	writeError(w, http.StatusUnauthorized, "Invalid password")
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": s.auth.Authenticated(r)})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.auth.ClearCookie(w)
	s.store.Audit("auth.logout.success", "admin", "success", nil)
	writeJSON(w, http.StatusOK, map[string]any{"success": true})
}

func (s *Server) handleOAuth(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/oauth/"), "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	switch parts[1] {
	case "start":
		s.handleOAuthStart(w, r, parts[0])
	case "complete":
		s.handleOAuthComplete(w, r, parts[0])
	default:
		writeError(w, http.StatusNotFound, "Not found")
	}
}

func (s *Server) handleOAuthStart(w http.ResponseWriter, r *http.Request, providerID string) {
	var input struct {
		RedirectURI string `json:"redirectUri"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&input)
	}
	redirectURI := strings.TrimSpace(input.RedirectURI)
	if redirectURI == "" {
		redirectURI = "http://localhost:1455/auth/callback"
	}
	state, err := randomURLToken(32)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create OAuth state")
		return
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to create OAuth verifier")
		return
	}
	challenge := codeChallenge(verifier)
	authorizeURL, err := buildAuthorizeURL(providerID, state, challenge, redirectURI)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.oauthMu.Lock()
	s.oauth[state] = oauthSession{Provider: providerID, Verifier: verifier, RedirectURI: redirectURI, CreatedAt: time.Now().UTC()}
	s.oauthMu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"authorizeUrl": authorizeURL, "url": authorizeURL, "state": state})
}

func (s *Server) handleOAuthComplete(w http.ResponseWriter, r *http.Request, providerID string) {
	var input struct {
		Code        string `json:"code"`
		State       string `json:"state"`
		ResponseURL string `json:"responseUrl"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	code, state, err := parseOAuthCallback(input.ResponseURL, input.Code, input.State)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.oauthMu.Lock()
	session, ok := s.oauth[state]
	if ok {
		delete(s.oauth, state)
	}
	s.oauthMu.Unlock()
	if !ok || session.Provider != providerID {
		writeError(w, http.StatusBadRequest, "OAuth state is expired or invalid")
		return
	}
	if time.Since(session.CreatedAt) > 10*time.Minute {
		writeError(w, http.StatusBadRequest, "OAuth state is expired")
		return
	}
	if providerID != "codex" {
		writeError(w, http.StatusBadRequest, "OAuth completion is not supported for this provider")
		return
	}
	tokens, err := exchangeCodexCode(r, code, session.RedirectURI, session.Verifier)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	profile := profileFromIDToken(tokens.IDToken)
	fetchedProfile, err := fetchCodexUserInfo(r, tokens.AccessToken)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	profile = mergeCodexProfile(profile, fetchedProfile)
	name := strings.TrimSpace(profile.Name)
	if name == "" {
		name = strings.TrimSpace(profile.Email)
	}
	if name == "" {
		name = "Codex Account"
	}
	connection := store.ProviderConnection{ID: uuid.NewString(), Provider: providerID, Name: name, Email: profile.Email, DisplayName: profile.Name, AuthType: "oauth", IsActive: true, DefaultModel: "gpt-5.1-codex-max", AccessToken: tokens.AccessToken, RefreshToken: tokens.RefreshToken, ProviderSpecificData: map[string]any{"callbackUrl": session.RedirectURI, "responseUrl": input.ResponseURL, "idToken": tokens.IDToken, "tokenType": tokens.TokenType, "expiresIn": tokens.ExpiresIn, "userId": profile.Subject}}
	if err := s.store.UpsertConnection(connection); err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to save provider connection")
		return
	}
	maskConnection(&connection)
	writeJSON(w, http.StatusOK, connection)
}

type codexTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
}

type codexUserInfo struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
	UPN               string `json:"upn"`
}

func parseOAuthCallback(responseURL string, codeInput string, stateInput string) (string, string, error) {
	code := strings.TrimSpace(codeInput)
	state := strings.TrimSpace(stateInput)
	callback := strings.TrimSpace(responseURL)
	if callback != "" {
		parsed, err := url.Parse(callback)
		if err == nil && parsed.RawQuery != "" {
			values := parsed.Query()
			if code == "" {
				code = strings.TrimSpace(values.Get("code"))
			}
			if state == "" {
				state = strings.TrimSpace(values.Get("state"))
			}
		} else if code == "" {
			code = callback
		}
	}
	if code == "" {
		return "", "", errors.New("authorization code is required")
	}
	if state == "" {
		return "", "", errors.New("OAuth state is required; paste the full callback URL")
	}
	return code, state, nil
}

func exchangeCodexCode(r *http.Request, code string, redirectURI string, verifier string) (codexTokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", "app_EMoamEEZ73f0CkXaXp7hrann")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, "https://auth.openai.com/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		return codexTokenResponse{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return codexTokenResponse{}, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return codexTokenResponse{}, fmt.Errorf("Token exchange failed [%d]: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	var tokens codexTokenResponse
	if err := json.Unmarshal(data, &tokens); err != nil {
		return codexTokenResponse{}, err
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" {
		return codexTokenResponse{}, errors.New("Token exchange did not return access_token and refresh_token")
	}
	return tokens, nil
}

func fetchCodexUserInfo(r *http.Request, accessToken string) (codexUserInfo, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://auth.openai.com/oauth/userinfo", nil)
	if err != nil {
		return codexUserInfo{}, err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return codexUserInfo{}, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return codexUserInfo{}, fmt.Errorf("Token verification failed [%d]: %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	var profile codexUserInfo
	if err := json.Unmarshal(data, &profile); err != nil {
		return codexUserInfo{}, err
	}
	return profile, nil
}

func profileFromIDToken(idToken string) codexUserInfo {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return codexUserInfo{}
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return codexUserInfo{}
	}
	var profile codexUserInfo
	if err := json.Unmarshal(payload, &profile); err != nil {
		return codexUserInfo{}
	}
	profile.Email = firstNonEmpty(profile.Email, profile.PreferredUsername, profile.UPN)
	return profile
}

func mergeCodexProfile(primary codexUserInfo, fallback codexUserInfo) codexUserInfo {
	return codexUserInfo{
		Subject: firstNonEmpty(primary.Subject, fallback.Subject),
		Email:   firstNonEmpty(primary.Email, primary.PreferredUsername, primary.UPN, fallback.Email, fallback.PreferredUsername, fallback.UPN),
		Name:    firstNonEmpty(primary.Name, fallback.Name),
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func buildAuthorizeURL(providerID string, state string, codeChallenge string, redirectURI string) (string, error) {
	switch providerID {
	case "codex":
		params := []string{
			"response_type=" + url.QueryEscape("code"),
			"client_id=" + url.QueryEscape("app_EMoamEEZ73f0CkXaXp7hrann"),
			"redirect_uri=" + url.QueryEscape(redirectURI),
			"scope=" + strings.ReplaceAll(url.QueryEscape("openid profile email offline_access"), "+", "%20"),
			"code_challenge=" + url.QueryEscape(codeChallenge),
			"code_challenge_method=" + url.QueryEscape("S256"),
			"id_token_add_organizations=" + url.QueryEscape("true"),
			"codex_cli_simplified_flow=" + url.QueryEscape("true"),
			"originator=" + url.QueryEscape("codex_cli_rs"),
			"prompt=" + url.QueryEscape("login"),
			"state=" + url.QueryEscape(state),
		}
		return "https://auth.openai.com/oauth/authorize?" + strings.Join(params, "&"), nil
	case "kiro":
		redirectURI := "kiro://kiro.kiroAgent/authenticate-success"
		params := url.Values{}
		params.Set("idp", "Google")
		params.Set("redirect_uri", redirectURI)
		params.Set("code_challenge", codeChallenge)
		params.Set("code_challenge_method", "S256")
		params.Set("state", state)
		params.Set("prompt", "select_account")
		return "https://prod.us-east-1.auth.desktop.kiro.dev/login?" + params.Encode(), nil
	case "antigravity":
		clientID := strings.TrimSpace(configEnv("ANTIGRAVITY_OAUTH_CLIENT_ID"))
		if clientID == "" {
			return "", errors.New("ANTIGRAVITY_OAUTH_CLIENT_ID is required to generate Antigravity login URL")
		}
		params := url.Values{}
		params.Set("client_id", clientID)
		params.Set("response_type", "code")
		params.Set("redirect_uri", redirectURI)
		params.Set("scope", strings.Join([]string{"openid", "https://www.googleapis.com/auth/cloud-platform", "https://www.googleapis.com/auth/userinfo.email", "https://www.googleapis.com/auth/userinfo.profile", "https://www.googleapis.com/auth/cclog", "https://www.googleapis.com/auth/experimentsandconfigs"}, " "))
		params.Set("state", state)
		params.Set("access_type", "offline")
		params.Set("prompt", "consent")
		return "https://accounts.google.com/o/oauth2/v2/auth?" + params.Encode(), nil
	default:
		return "", errors.New("OAuth browser login is not supported for this provider")
	}
}

func randomURLToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func codeChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func configEnv(key string) string {
	return os.Getenv(key)
}

func (s *Server) handleProviders(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		provider := r.URL.Query().Get("provider")
		connections, err := s.store.ListConnections(provider)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list providers")
			return
		}
		if connections == nil {
			connections = []store.ProviderConnection{}
		}
		maskConnections(connections)
		writeJSON(w, http.StatusOK, map[string]any{"providers": providers.List(), "connections": connections})
	case http.MethodPost:
		var c store.ProviderConnection
		if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		if _, ok := providers.Registry[c.Provider]; !ok {
			writeError(w, http.StatusBadRequest, "Unsupported provider")
			return
		}
		isNew := c.ID == ""
		if isNew {
			c.ID = uuid.NewString()
			c.IsActive = true
		}
		if c.Name == "" {
			c.Name = c.Provider + " account"
		}
		if c.AuthType == "" {
			c.AuthType = "oauth"
		}
		if c.ProviderSpecificData == nil {
			c.ProviderSpecificData = map[string]any{}
		}
		if c.ID != "" {
			if existing, err := s.store.GetConnection(c.ID); err == nil {
				preserveMaskedSecrets(&c, existing)
			}
		}
		now := time.Now().UTC()
		if c.CreatedAt.IsZero() {
			c.CreatedAt = now
		}
		c.UpdatedAt = now
		if err := s.store.UpsertConnection(c); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save provider connection")
			return
		}
		maskConnection(&c)
		writeJSON(w, http.StatusOK, c)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleProviderByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/providers/")
	if id == "" {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.store.DeleteConnection(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "Not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Failed to delete provider connection")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
		return
	}
	methodNotAllowed(w)
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	data := []map[string]any{}
	for _, p := range providers.List() {
		for _, m := range p.Models {
			data = append(data, map[string]any{"id": p.ID + "/" + m.ID, "object": "model", "owned_by": p.ID})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": data})
}

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	s.handleChatCompletions(w, r)
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	req := executors.ChatRequest{Raw: raw, Stream: raw["stream"] == true}
	if model, ok := raw["model"].(string); ok {
		req.Model = model
	}
	if msgs, ok := raw["messages"].([]any); ok {
		for _, item := range msgs {
			if m, ok := item.(map[string]any); ok {
				req.Messages = append(req.Messages, executors.Message{Role: fmt.Sprint(m["role"]), Content: m["content"]})
			}
		}
	}
	provider, model, ok := providers.FindByModel(req.Model)
	if !ok {
		writeError(w, http.StatusBadRequest, "Unsupported model/provider. Use antigravity/*, codex/*, kiro/*, or trae/*.")
		return
	}
	cred, err := s.store.FirstConnection(provider.ID)
	if err != nil {
		cred = store.ProviderConnection{Provider: provider.ID, ProviderSpecificData: map[string]any{}}
	}
	result, err := executors.For(provider.ID).Execute(r.Context(), provider, model, req, cred)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	if req.Stream {
		sse.Headers(w)
		id := "chatcmpl-" + uuid.NewString()
		created := time.Now().Unix()
		_ = sse.WriteEvent(w, chunk(id, created, req.Model, map[string]any{"role": "assistant"}, nil))
		stream := result.Stream
		if stream == nil {
			stream = func(emit func(string) error) error { return emit(result.Text) }
		}
		if err := stream(func(piece string) error {
			return sse.WriteEvent(w, chunk(id, created, req.Model, map[string]any{"content": piece}, nil))
		}); err != nil {
			_ = sse.WriteEvent(w, map[string]any{"error": map[string]any{"message": err.Error(), "type": "api_error"}})
		}
		_ = sse.WriteEvent(w, chunk(id, created, req.Model, map[string]any{}, "stop"))
		sse.WriteDone(w)
		return
	}
	text := result.Text
	if text == "" && result.Stream != nil {
		var b strings.Builder
		_ = result.Stream(func(piece string) error { b.WriteString(piece); return nil })
		text = b.String()
	}
	body := map[string]any{"id": "chatcmpl-" + uuid.NewString(), "object": "chat.completion", "created": time.Now().Unix(), "model": req.Model, "choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}}}
	if result.Meta != nil {
		body["metadata"] = result.Meta
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		settings, _ := s.store.GetSettings()
		if settings.RequireLogin && !s.auth.Authenticated(r) {
			writeError(w, http.StatusUnauthorized, "Unauthorized")
			return
		}
		next(w, r)
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, "/api/") {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if s.webProxy != nil {
		s.webProxy.ServeHTTP(w, r)
		return
	}
	dist := s.staticDistDir()
	if dist == "" {
		writeError(w, http.StatusNotFound, "Web assets were not found. Run web build or install a release bundle.")
		return
	}
	file := filepath.Join(dist, filepath.Clean(path))
	if path == "/" {
		file = filepath.Join(dist, "index.html")
	}
	if _, err := http.Dir(dist).Open(strings.TrimPrefix(path, "/")); err != nil || strings.HasSuffix(path, "/") {
		file = filepath.Join(dist, "index.html")
	}
	http.ServeFile(w, r, file)
}

func (s *Server) staticDistDir() string {
	for _, dir := range staticDistCandidates() {
		if _, err := http.Dir(dir).Open("index.html"); err == nil {
			return dir
		}
	}
	return ""
}

func staticDistCandidates() []string {
	candidates := []string{}
	if webDir := strings.TrimSpace(os.Getenv("WEB_DIR")); webDir != "" {
		candidates = append(candidates, webDir)
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(exeDir, "web", "build", "client"),
			filepath.Join(exeDir, "web", "dist"),
			filepath.Join(exeDir, "..", "web", "build", "client"),
			filepath.Join(exeDir, "..", "web", "dist"),
		)
	}
	return append(candidates,
		filepath.Join("web", "build", "client"),
		filepath.Join("web", "dist"),
	)
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,PUT,PATCH,DELETE,OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func chunk(id string, created int64, model string, delta map[string]any, finish any) map[string]any {
	return map[string]any{"id": id, "object": "chat.completion.chunk", "created": created, "model": model, "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
}

func requireLoginFallback(cfg config.Config) map[string]any {
	return map[string]any{"requireLogin": true, "hasPassword": true, "setupComplete": true, "nodeVersion": cfg.GoVersion, "nodeCompatible": true}
}

func writeJSON(w http.ResponseWriter, status int, v any) { writeJSONStatus(w, status, v) }
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, map[string]any{"error": msg})
}
func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "Method not allowed")
}

func latestGitHubRelease(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/Dino-VN/BRoute/releases/latest", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "broute-update-check")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("GitHub returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var body struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	if body.TagName == "" {
		return "", errors.New("latest release did not include a tag")
	}
	return strings.TrimPrefix(body.TagName, "v"), nil
}

func versionDifferent(current, latest string) bool {
	return strings.TrimPrefix(strings.TrimSpace(current), "v") != strings.TrimPrefix(strings.TrimSpace(latest), "v")
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("x-forwarded-for"); xff != "" {
		return strings.TrimSpace(strings.Split(xff, ",")[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func maskConnections(conns []store.ProviderConnection) {
	for i := range conns {
		maskConnection(&conns[i])
	}
}
func maskConnection(c *store.ProviderConnection) {
	if c.AccessToken != "" {
		c.AccessToken = "********"
	}
	if c.RefreshToken != "" {
		c.RefreshToken = "********"
	}
	if c.APIKey != "" {
		c.APIKey = "********"
	}
}

func preserveMaskedSecrets(c *store.ProviderConnection, existing store.ProviderConnection) {
	if c.AccessToken == "" || c.AccessToken == "********" {
		c.AccessToken = existing.AccessToken
	}
	if c.RefreshToken == "" || c.RefreshToken == "********" {
		c.RefreshToken = existing.RefreshToken
	}
	if c.APIKey == "" || c.APIKey == "********" {
		c.APIKey = existing.APIKey
	}
}
