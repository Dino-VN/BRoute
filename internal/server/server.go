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
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
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
	debugMu  sync.Mutex
	debugLog []debugLogEntry
}

type debugLogEntry struct {
	ID             string         `json:"id"`
	CreatedAt      time.Time      `json:"createdAt"`
	Method         string         `json:"method"`
	Path           string         `json:"path"`
	Provider       string         `json:"provider,omitempty"`
	Model          string         `json:"model,omitempty"`
	ConnectionID   string         `json:"connectionId,omitempty"`
	AccountName    string         `json:"accountName,omitempty"`
	Stream         bool           `json:"stream"`
	Status         string         `json:"status"`
	HTTPStatus     int            `json:"httpStatus,omitempty"`
	DurationMS     int64          `json:"durationMs"`
	Error          string         `json:"error,omitempty"`
	OriginalBody   map[string]any `json:"originalBody,omitempty"`
	ConvertedBody  any            `json:"convertedBody,omitempty"`
	ToolCallDump   string         `json:"toolCallDump,omitempty"`
	UpstreamURL    string         `json:"upstreamUrl,omitempty"`
	UpstreamStatus int            `json:"upstreamStatus,omitempty"`
	UpstreamBody   string         `json:"upstreamBody,omitempty"`
}

type providerQuotaConnectionResponse struct {
	ID                  string                `json:"id"`
	Provider            string                `json:"provider"`
	Name                string                `json:"name"`
	Email               string                `json:"email,omitempty"`
	DisplayName         string                `json:"displayName,omitempty"`
	AuthType            string                `json:"authType"`
	IsActive            bool                  `json:"isActive"`
	Priority            int                   `json:"priority"`
	DefaultModel        string                `json:"defaultModel,omitempty"`
	RateLimitProtection bool                  `json:"rateLimitProtection"`
	ProjectID           string                `json:"projectId,omitempty"`
	Quota               providerQuotaResponse `json:"quota"`
	CreatedAt           time.Time             `json:"createdAt"`
	UpdatedAt           time.Time             `json:"updatedAt"`
}

type providerQuotaResponse struct {
	Provider  string                        `json:"provider"`
	State     string                        `json:"state"`
	Plan      string                        `json:"plan,omitempty"`
	Limited   bool                          `json:"limited"`
	ResetAt   string                        `json:"resetAt,omitempty"`
	CheckedAt string                        `json:"checkedAt,omitempty"`
	Windows   []providerQuotaWindowResponse `json:"windows"`
}

type providerQuotaRefreshResult struct {
	ConnectionID string `json:"connectionId"`
	Provider     string `json:"provider"`
	OK           bool   `json:"ok"`
	State        string `json:"state,omitempty"`
}

type providerQuotaWindowResponse struct {
	Key       string  `json:"key"`
	Label     string  `json:"label"`
	Usage     float64 `json:"usage"`
	Limit     float64 `json:"limit"`
	Remaining float64 `json:"remaining"`
	Percent   float64 `json:"percent"`
	Exhausted bool    `json:"exhausted"`
	ResetAt   string  `json:"resetAt,omitempty"`
}

type oauthSession struct {
	Provider    string
	Verifier    string
	RedirectURI string
	DeviceID    string
	MachineID   string
	CreatedAt   time.Time
}

func New(cfg config.Config, st *store.Store) *Server {
	s := &Server{cfg: cfg, store: st, auth: auth.New(cfg.JWTSecret, cfg.AuthCookieSecure), mux: http.NewServeMux(), oauth: map[string]oauthSession{}}
	if cfg.WebDevProxy != "" {
		if target, err := url.Parse(cfg.WebDevProxy); err == nil {
			s.webProxy = httputil.NewSingleHostReverseProxy(target)
		}
	}
	s.routes()
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.withCORS(s.mux).ServeHTTP(w, r)
}

func (s *Server) StartBackgroundTasks(ctx context.Context) {
	go s.runTraeTokenRefreshLoop(ctx)
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
	s.mux.HandleFunc("/api/provider-quota", s.requireAuth(s.handleProviderQuota))
	s.mux.HandleFunc("/api/debug-logs", s.requireAuth(s.handleDebugLogs))
	s.mux.HandleFunc("/api/api-keys", s.requireAuth(s.handleAPIKeys))
	s.mux.HandleFunc("/api/api-keys/", s.requireAuth(s.handleAPIKeyByID))
	s.mux.HandleFunc("/api/v1/models", s.handleModels)
	s.mux.HandleFunc("/api/v1/chat/completions", s.handleChatCompletions)
	s.mux.HandleFunc("/api/v1/responses", s.handleResponses)
	s.mux.HandleFunc("/", s.handleStatic)
}

func (s *Server) handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": s.cfg.AppVersion})
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
		cmd := exec.CommandContext(r.Context(), "npx", "--yes", "broute-cli@latest", "update")
		cmd.Env = append(os.Environ(), "BROWSER=none")
		output, err := cmd.CombinedOutput()
		if err != nil {
			writeJSONStatus(w, http.StatusBadGateway, map[string]any{"success": false, "error": err.Error(), "output": strings.TrimSpace(string(output))})
			return
		}
		latest, _ := latestGitHubRelease(context.Background())
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "output": strings.TrimSpace(string(output)), "restarting": true, "restartRequired": false, "latestVersion": latest})
		go restartProcessSoon()
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
	session := oauthSession{Provider: providerID, Verifier: verifier, RedirectURI: redirectURI, CreatedAt: time.Now().UTC()}
	if providerID == "trae" {
		if parsed, err := url.Parse(authorizeURL); err == nil {
			values := parsed.Query()
			session.DeviceID = firstNonEmpty(values.Get("device_id"), values.Get("x_device_id"))
			session.MachineID = firstNonEmpty(values.Get("machine_id"), values.Get("x_machine_id"))
		}
	}
	s.oauthMu.Lock()
	s.oauth[state] = session
	sessionCount := len(s.oauth)
	s.oauthMu.Unlock()
	if providerID == "trae" {
		log.Printf("trae oauth start: state=%s redirect=%s callback=%s device=%t machine=%t sessions=%d", shortOAuthValue(state), redirectURI, traeAuthorizeCallbackURL(redirectURI), session.DeviceID != "", session.MachineID != "", sessionCount)
	}
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
	if providerID == "trae" {
		state, err = parseTraeCallbackState(input.ResponseURL, input.State)
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.oauthMu.Lock()
	session, ok := s.oauth[state]
	if ok {
		delete(s.oauth, state)
	}
	availableStates := make([]string, 0, len(s.oauth))
	for storedState := range s.oauth {
		availableStates = append(availableStates, shortOAuthValue(storedState))
	}
	sessionCount := len(s.oauth)
	s.oauthMu.Unlock()
	if !ok || session.Provider != providerID {
		if providerID == "trae" {
			log.Printf("trae oauth complete invalid state: parsed=%s input=%s query_keys=%v found=%t session_provider=%q expected_provider=%q available=%v sessions=%d", shortOAuthValue(state), shortOAuthValue(input.State), callbackQueryKeys(input.ResponseURL), ok, session.Provider, providerID, availableStates, sessionCount)
		}
		writeError(w, http.StatusBadRequest, "OAuth state is expired or invalid")
		return
	}
	if time.Since(session.CreatedAt) > 10*time.Minute {
		if providerID == "trae" {
			log.Printf("trae oauth complete expired state: state=%s age=%s query_keys=%v", shortOAuthValue(state), time.Since(session.CreatedAt).Round(time.Second), callbackQueryKeys(input.ResponseURL))
		}
		writeError(w, http.StatusBadRequest, "OAuth state is expired")
		return
	}
	if providerID == "trae" {
		log.Printf("trae oauth complete accepted: state=%s age=%s query_keys=%v device=%t machine=%t", shortOAuthValue(state), time.Since(session.CreatedAt).Round(time.Second), callbackQueryKeys(input.ResponseURL), session.DeviceID != "", session.MachineID != "")
		connection, err := parseTraeCallbackConnection(input.ResponseURL, session)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err := s.store.UpsertConnection(connection); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save provider connection")
			return
		}
		maskConnection(&connection)
		writeJSON(w, http.StatusOK, connection)
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

func parseTraeCallbackConnection(responseURL string, session oauthSession) (store.ProviderConnection, error) {
	parsed, err := url.Parse(strings.TrimSpace(responseURL))
	if err != nil {
		return store.ProviderConnection{}, errors.New("Invalid Trae callback URL")
	}
	values := parsed.Query()
	userJWTRaw := values.Get("userJwt")
	if userJWTRaw == "" {
		return store.ProviderConnection{}, errors.New("Missing userJwt in Trae callback")
	}
	var userJWT struct {
		Token           string `json:"Token"`
		TokenExpireAt   int64  `json:"TokenExpireAt"`
		RefreshToken    string `json:"RefreshToken"`
		RefreshExpireAt int64  `json:"RefreshExpireAt"`
		ClientID        string `json:"ClientID"`
	}
	if err := json.Unmarshal([]byte(userJWTRaw), &userJWT); err != nil {
		return store.ProviderConnection{}, errors.New("Malformed Trae userJwt payload")
	}
	if userJWT.Token == "" {
		return store.ProviderConnection{}, errors.New("Trae userJwt.Token missing")
	}
	var userInfo struct {
		UserID            string `json:"UserID"`
		TenantID          string `json:"TenantID"`
		Region            string `json:"Region"`
		AIRegion          string `json:"AIRegion"`
		ScreenName        string `json:"ScreenName"`
		NonPlainTextEmail string `json:"NonPlainTextEmail"`
	}
	if raw := values.Get("userInfo"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &userInfo)
	}
	clientID := userJWT.ClientID
	if clientID == "" {
		clientID = traeClientID()
	}
	refreshToken := userJWT.RefreshToken
	if refreshToken == "" {
		refreshToken = values.Get("refreshToken")
	}
	region := userInfo.Region
	if region == "" {
		region = "US-East"
	}
	aiRegion := userInfo.AIRegion
	if aiRegion == "" {
		aiRegion = region
	}
	userID := userInfo.UserID
	name := strings.TrimSpace(userInfo.ScreenName)
	if name == "" {
		name = strings.TrimSpace(userInfo.NonPlainTextEmail)
	}
	if name == "" && userID != "" {
		name = "Trae " + userID
	}
	if name == "" {
		name = "Trae Account"
	}
	providerData := map[string]any{
		"appId":            traeAppID(),
		"clientId":         clientID,
		"callbackUrl":      session.RedirectURI,
		"responseUrl":      responseURL,
		"userId":           userID,
		"tenantId":         userInfo.TenantID,
		"bizUserId":        userID,
		"userUniqueId":     userID,
		"webId":            userID,
		"scope":            "marscode-us",
		"tenant":           "marscode",
		"region":           region,
		"aiRegion":         aiRegion,
		"userRegion":       "US",
		"userIdentity":     "Free",
		"host":             values.Get("host"),
		"deviceId":         firstNonEmpty(values.Get("device_id"), values.Get("x_device_id"), session.DeviceID),
		"machineId":        firstNonEmpty(values.Get("machine_id"), values.Get("x_machine_id"), session.MachineID),
		"deviceCPU":        "AMD",
		"deviceBrand":      firstNonEmpty(values.Get("x_device_brand"), "92L3"),
		"deviceType":       firstNonEmpty(values.Get("x_device_type"), "windows"),
		"screenName":       userInfo.ScreenName,
		"tokenExpireAt":    userJWT.TokenExpireAt,
		"refreshExpireAt":  userJWT.RefreshExpireAt,
		"tokenRefreshedAt": time.Now().UTC().Format(time.RFC3339),
		"authMethod":       "oauth_callback",
	}
	if providerData["host"] == "" {
		providerData["host"] = "https://api-us-east.trae.ai"
	}
	return store.ProviderConnection{ID: uuid.NewString(), Provider: "trae", Name: name, Email: userInfo.NonPlainTextEmail, DisplayName: userInfo.ScreenName, AuthType: "oauth", IsActive: true, DefaultModel: "auto", AccessToken: userJWT.Token, RefreshToken: refreshToken, ProviderSpecificData: providerData}, nil
}

func (s *Server) refreshTraeConnectionToken(ctx context.Context, cred store.ProviderConnection) (store.ProviderConnection, error) {
	if cred.RefreshToken == "" {
		return cred, nil
	}
	if !traeTokenNeedsRefresh(cred.ProviderSpecificData) {
		return cred, nil
	}
	clientID := stringMapValue(cred.ProviderSpecificData, "clientId", traeClientID())
	userID := stringMapValue(cred.ProviderSpecificData, "userId", "")
	if userID == "" {
		userID = stringMapValue(cred.ProviderSpecificData, "bizUserId", "")
	}
	if clientID == "" || userID == "" {
		return cred, nil
	}
	payload := map[string]string{"ClientID": clientID, "RefreshToken": cred.RefreshToken, "ClientSecret": "-", "UserID": userID}
	body, _ := json.Marshal(payload)
	endpoint := strings.TrimRight(configEnv("TRAE_REFRESH_TOKEN_URL"), "/")
	if endpoint == "" {
		endpoint = "https://api-sg-central.trae.ai"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/cloudide/api/v3/trae/oauth/ExchangeToken", strings.NewReader(string(body)))
	if err != nil {
		return cred, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return cred, fmt.Errorf("trae token refresh failed: %w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return cred, fmt.Errorf("trae token refresh [%d] %s", res.StatusCode, string(data))
	}
	var decoded struct {
		Result struct {
			Token           string `json:"Token"`
			TokenExpireAt   int64  `json:"TokenExpireAt"`
			RefreshToken    string `json:"RefreshToken"`
			RefreshExpireAt int64  `json:"RefreshExpireAt"`
		} `json:"Result"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return cred, fmt.Errorf("decode trae token refresh response failed: %w", err)
	}
	if decoded.Result.Token == "" {
		return cred, errors.New("trae token refresh response missing Token")
	}
	cred.AccessToken = decoded.Result.Token
	if decoded.Result.RefreshToken != "" {
		cred.RefreshToken = decoded.Result.RefreshToken
	}
	if cred.ProviderSpecificData == nil {
		cred.ProviderSpecificData = map[string]any{}
	}
	cred.ProviderSpecificData["tokenExpireAt"] = decoded.Result.TokenExpireAt
	cred.ProviderSpecificData["tokenRefreshedAt"] = time.Now().UTC().Format(time.RFC3339)
	if decoded.Result.RefreshExpireAt > 0 {
		cred.ProviderSpecificData["refreshExpireAt"] = decoded.Result.RefreshExpireAt
	}
	if err := s.store.UpdateConnectionTokens(cred.ID, cred.AccessToken, cred.RefreshToken, cred.ProviderSpecificData); err != nil {
		return cred, fmt.Errorf("failed to save refreshed Trae token: %w", err)
	}
	return cred, nil
}

func (s *Server) runTraeTokenRefreshLoop(ctx context.Context) {
	interval := traeTokenRefreshInterval()
	log.Printf("trae token auto-refresh enabled interval=%s", interval)
	s.refreshTraeTokensOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshTraeTokensOnce(ctx)
		}
	}
}

func (s *Server) refreshTraeTokensOnce(ctx context.Context) {
	connections, err := s.store.ListConnections("trae")
	if err != nil {
		log.Printf("trae token auto-refresh list failed: %v", err)
		return
	}
	for _, cred := range connections {
		if !cred.IsActive || cred.RefreshToken == "" || !traeTokenNeedsRefresh(cred.ProviderSpecificData) {
			continue
		}
		refreshed, err := s.refreshTraeConnectionToken(ctx, cred)
		if err != nil {
			log.Printf("trae token auto-refresh failed connection_id=%s name=%q: %v", cred.ID, cred.Name, err)
			continue
		}
		if refreshed.AccessToken != cred.AccessToken {
			log.Printf("trae token auto-refresh ok connection_id=%s name=%q", cred.ID, cred.Name)
		}
	}
}

func traeTokenRefreshInterval() time.Duration {
	value := strings.TrimSpace(configEnv("TRAE_TOKEN_REFRESH_INTERVAL"))
	if value == "" {
		return 10 * time.Minute
	}
	parsed, err := time.ParseDuration(value)
	if err == nil && parsed >= time.Minute {
		return parsed
	}
	log.Printf("invalid TRAE_TOKEN_REFRESH_INTERVAL=%q, using 10m", value)
	return 10 * time.Minute
}

func traeTokenNeedsRefresh(data map[string]any) bool {
	expireAt := int64MapValue(data, "tokenExpireAt", 0)
	if expireAt == 0 {
		return true
	}
	return time.Now().UnixMilli() >= expireAt-5*60*1000
}

func stringMapValue(data map[string]any, key, fallback string) string {
	if value, ok := data[key].(string); ok && value != "" {
		return value
	}
	return fallback
}

func int64MapValue(data map[string]any, key string, fallback int64) int64 {
	switch value := data[key].(type) {
	case int64:
		return value
	case int:
		return int64(value)
	case float64:
		return int64(value)
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return parsed
		}
	}
	return fallback
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

func parseTraeCallbackState(responseURL string, stateInput string) (string, error) {
	state := ""
	callback := strings.TrimSpace(responseURL)
	if callback != "" {
		parsed, err := url.Parse(callback)
		if err == nil {
			values := parsed.Query()
			state = firstNonEmpty(values.Get("loginTraceID"), values.Get("login_trace_id"), values.Get("state"))
		}
	}
	if state == "" {
		state = strings.TrimSpace(stateInput)
	}
	if state == "" {
		return "", errors.New("OAuth state is required; paste the full Trae callback URL")
	}
	return state, nil
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

func shortOAuthValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "<empty>"
	}
	if len(value) <= 12 {
		return value
	}
	return value[:6] + "..." + value[len(value)-4:]
}

func callbackQueryKeys(responseURL string) []string {
	parsed, err := url.Parse(strings.TrimSpace(responseURL))
	if err != nil {
		return nil
	}
	keys := make([]string, 0, len(parsed.Query()))
	for key := range parsed.Query() {
		keys = append(keys, key)
	}
	return keys
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
	case "trae":
		callbackURL := traeAuthorizeCallbackURL(redirectURI)
		machineID, err := randomHexToken(32)
		if err != nil {
			return "", err
		}
		deviceID, err := randomDigitToken(19)
		if err != nil {
			return "", err
		}
		params := url.Values{}
		params.Set("login_version", "1")
		params.Set("auth_from", "solo")
		params.Set("login_channel", "native_ide")
		params.Set("plugin_version", "2.3.24254")
		params.Set("auth_type", "local")
		params.Set("client_id", traeClientID())
		params.Set("redirect", "0")
		params.Set("login_trace_id", state)
		params.Set("auth_callback_url", callbackURL)
		params.Set("machine_id", machineID)
		params.Set("device_id", deviceID)
		params.Set("x_device_id", deviceID)
		params.Set("x_machine_id", machineID)
		params.Set("x_device_brand", "92L3")
		params.Set("x_device_type", "windows")
		params.Set("x_os_version", "Windows 11")
		params.Set("x_env", "")
		params.Set("x_app_version", "0.1.7")
		params.Set("x_app_type", "stable")
		params.Set("hide_saas_login", "true")
		return "https://www.trae.ai/authorization?" + params.Encode(), nil
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

func randomHexToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func randomDigitToken(size int) (string, error) {
	buf := make([]byte, size)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	var b strings.Builder
	for _, value := range buf {
		b.WriteByte('0' + value%10)
	}
	return b.String(), nil
}

func traeAuthorizeCallbackURL(redirectURI string) string {
	parsed, err := url.Parse(redirectURI)
	if err != nil || parsed.Port() == "" {
		return "http://127.0.0.1:1455/authorize"
	}
	return "http://127.0.0.1:" + parsed.Port() + "/authorize"
}

func traeClientID() string {
	if value := strings.TrimSpace(configEnv("TRAE_CLIENT_ID")); value != "" {
		return value
	}
	return "en1oxy7wnw8j9n"
}

func traeAppID() string {
	if value := strings.TrimSpace(configEnv("TRAE_APP_ID")); value != "" {
		return value
	}
	return "931507"
}

func generateGatewayAPIKey() (string, error) {
	token, err := randomURLToken(32)
	if err != nil {
		return "", err
	}
	return "br_live_sk_" + token, nil
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

func (s *Server) handleProviderQuota(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.refreshProviderQuota(w, r)
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	connections, err := s.store.ListConnections(provider)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list provider quota")
		return
	}
	if connections == nil {
		connections = []store.ProviderConnection{}
	}
	maskConnections(connections)
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers.List(), "connections": providerQuotaConnectionsResponse(connections)})
}

func (s *Server) refreshProviderQuota(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Provider     string `json:"provider"`
		ConnectionID string `json:"connectionId"`
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	provider := strings.TrimSpace(input.Provider)
	connectionID := strings.TrimSpace(input.ConnectionID)
	log.Printf("provider quota refresh requested provider=%q connection_id=%q", provider, connectionID)
	connections, err := s.providerQuotaTargets(provider, connectionID)
	if err != nil {
		log.Printf("provider quota refresh target lookup failed provider=%q connection_id=%q error=%v", provider, connectionID, err)
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "Provider account not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "Failed to load provider accounts")
		return
	}
	log.Printf("provider quota refresh targets provider=%q connection_id=%q count=%d", provider, connectionID, len(connections))
	results := make([]providerQuotaRefreshResult, 0, len(connections))
	for _, c := range connections {
		results = append(results, s.refreshConnectionQuota(r.Context(), c))
	}
	providerFilter := provider
	if connectionID != "" {
		providerFilter = ""
	}
	updated, err := s.store.ListConnections(providerFilter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Failed to list provider quota")
		return
	}
	maskConnections(updated)
	writeJSON(w, http.StatusOK, map[string]any{"providers": providers.List(), "connections": providerQuotaConnectionsResponse(updated), "results": results})
}

func (s *Server) providerQuotaTargets(provider, connectionID string) ([]store.ProviderConnection, error) {
	if connectionID != "" {
		c, err := s.store.GetConnection(connectionID)
		if err != nil {
			return nil, err
		}
		if provider != "" && c.Provider != provider {
			return nil, sql.ErrNoRows
		}
		return []store.ProviderConnection{c}, nil
	}
	connections, err := s.store.ListConnections(provider)
	if err != nil {
		return nil, err
	}
	active := make([]store.ProviderConnection, 0, len(connections))
	for _, c := range connections {
		if c.IsActive {
			active = append(active, c)
		}
	}
	return active, nil
}

func (s *Server) refreshConnectionQuota(ctx context.Context, cred store.ProviderConnection) providerQuotaRefreshResult {
	result := providerQuotaRefreshResult{ConnectionID: cred.ID, Provider: cred.Provider, OK: false}
	log.Printf("provider quota check start provider=%q connection_id=%q name=%q active=%t", cred.Provider, cred.ID, cred.Name, cred.IsActive)
	if cred.Provider == "trae" {
		quotaData, resetAt, err := fetchTraeQuota(ctx, cred)
		if err != nil {
			message := err.Error()
			_ = s.store.UpdateConnectionQuota(cred.ID, "error", message, nil, map[string]any{"error": message})
			result.State = "error"
			return result
		}
		status := quotaPlanStatus(cred.Provider, quotaData)
		detail := "Trae quota refreshed"
		if resetAt != nil {
			detail += ". Quota resets " + resetAt.Format(time.RFC3339)
		}
		if err := s.store.UpdateConnectionQuota(cred.ID, status, detail, resetAt, quotaData); err != nil {
			result.State = "error"
			return result
		}
		result.OK = true
		result.State = status
		return result
	}
	provider, ok := providers.Registry[cred.Provider]
	if !ok {
		message := "Unknown provider"
		_ = s.store.UpdateConnectionQuota(cred.ID, "error", message, nil, map[string]any{"error": message})
		result.State = "error"
		return result
	}
	if !cred.IsActive {
		message := "Provider account is inactive"
		_ = s.store.UpdateConnectionQuota(cred.ID, "error", message, nil, map[string]any{"error": message})
		result.State = "error"
		return result
	}
	model := cred.DefaultModel
	if cred.Provider == "codex" {
		model = "gpt-5.4-mini"
	}
	if model == "" && len(provider.Models) > 0 {
		model = provider.Models[0].ID
	}
	if model == "" {
		message := "No model is configured for this provider"
		_ = s.store.UpdateConnectionQuota(cred.ID, "error", message, nil, map[string]any{"error": message})
		result.State = "error"
		return result
	}
	log.Printf("provider quota check execute provider=%q connection_id=%q model=%q", cred.Provider, cred.ID, model)
	checkCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	execResult, err := executors.For(provider.ID).Execute(checkCtx, provider, model, executors.ChatRequest{Model: model, Messages: []executors.Message{{Role: "user", Content: "quota"}}, Stream: true}, cred)
	if err != nil {
		quotaData := quotaMetaForProvider(cred.Provider, execResult.Meta)
		log.Printf("provider quota check error provider=%q connection_id=%q has_quota_meta=%t has_quota_limit=%t meta=%s error=%v", cred.Provider, cred.ID, quotaData != nil, quotaHasLimit(quotaData), quotaDebugString(quotaData), err)
		if quotaData != nil && !isQuotaError(err.Error()) {
			s.recordQuotaSuccess(cred, execResult)
		} else {
			s.recordQuotaError(cred, err)
		}
		if isQuotaError(err.Error()) {
			result.State = "limited"
		} else {
			result.State = "error"
		}
		return result
	}
	if execResult.Stream != nil {
		_ = execResult.Stream(func(string) error { return nil })
	}
	log.Printf("provider quota check success provider=%q connection_id=%q meta=%s", cred.Provider, cred.ID, quotaDebugString(quotaMetaForProvider(cred.Provider, execResult.Meta)))
	s.recordQuotaSuccess(cred, execResult)
	result.OK = true
	result.State = "available"
	return result
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		keys, err := s.store.ListAPIKeys()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to list API keys")
			return
		}
		maskAPIKeys(keys)
		writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
	case http.MethodPost:
		var input struct {
			Name          string   `json:"name"`
			AllowedModels []string `json:"allowedModels"`
			IsActive      *bool    `json:"isActive"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "Invalid JSON body")
			return
		}
		input.Name = strings.TrimSpace(input.Name)
		if input.Name == "" {
			writeError(w, http.StatusBadRequest, "API key name is required")
			return
		}
		allowed, err := s.validAllowedModels(input.AllowedModels)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		active := true
		if input.IsActive != nil {
			active = *input.IsActive
		}
		value, err := generateGatewayAPIKey()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to generate API key")
			return
		}
		key := store.APIKey{ID: uuid.NewString(), Name: input.Name, Key: value, AllowedModels: allowed, IsActive: active}
		if err := s.store.UpsertAPIKey(key); err != nil {
			writeError(w, http.StatusInternalServerError, "Failed to save API key")
			return
		}
		writeJSON(w, http.StatusOK, key)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleDebugLogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.debugMu.Lock()
		logs := make([]debugLogEntry, len(s.debugLog))
		for index := range s.debugLog {
			logs[len(s.debugLog)-1-index] = s.debugLog[index]
		}
		s.debugMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"logs": logs})
	case http.MethodDelete:
		s.debugMu.Lock()
		s.debugLog = nil
		s.debugMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"success": true})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) appendDebugLog(entry debugLogEntry) {
	entry.ID = uuid.NewString()
	entry.CreatedAt = time.Now().UTC()
	s.debugMu.Lock()
	s.debugLog = append(s.debugLog, entry)
	if len(s.debugLog) > 100 {
		s.debugLog = s.debugLog[len(s.debugLog)-100:]
	}
	s.debugMu.Unlock()
}

func (s *Server) handleAPIKeyByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/api-keys/")
	if id == "" {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	if r.Method == http.MethodDelete {
		if err := s.store.DeleteAPIKey(id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				writeError(w, http.StatusNotFound, "Not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "Failed to delete API key")
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
	apiKey, ok := s.gatewayAuthorized(w, r)
	if !ok {
		return
	}
	data := []map[string]any{}
	available := s.availableProviderIDs()
	for _, p := range providers.List() {
		if !available[p.ID] {
			continue
		}
		for _, m := range p.Models {
			id := p.ID + "/" + m.ID
			if apiKey != nil && !apiKeyAllowsModel(*apiKey, id) {
				continue
			}
			data = append(data, map[string]any{"id": id, "object": "model", "owned_by": p.ID})
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
	startedAt := time.Now()
	apiKey, ok := s.gatewayAuthorized(w, r)
	if !ok {
		return
	}
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	debugInfo := &executors.DebugInfo{}
	debugEntry := debugLogEntry{Method: r.Method, Path: r.URL.Path, Stream: raw["stream"] == true, OriginalBody: raw}
	req := executors.ChatRequest{Raw: raw, Stream: raw["stream"] == true, Debug: debugInfo}
	defer func() {
		debugEntry.DurationMS = time.Since(startedAt).Milliseconds()
		debugEntry.ConvertedBody = debugInfo.ConvertedBody
		debugEntry.ToolCallDump = debugInfo.ToolCallDump
		debugEntry.UpstreamURL = debugInfo.UpstreamURL
		debugEntry.UpstreamStatus = debugInfo.StatusCode
		debugEntry.UpstreamBody = debugInfo.ResponseBody
		s.appendDebugLog(debugEntry)
	}()
	if model, ok := raw["model"].(string); ok {
		req.Model = model
	}
	debugEntry.Model = req.Model
	if msgs, ok := raw["messages"].([]any); ok {
		for _, item := range msgs {
			if m, ok := item.(map[string]any); ok {
				req.Messages = append(req.Messages, executors.Message{Role: fmt.Sprint(m["role"]), Content: m["content"]})
			}
		}
	}
	provider, model, ok := providers.FindByModel(req.Model)
	if !ok {
		debugEntry.Status = "error"
		debugEntry.HTTPStatus = http.StatusBadRequest
		debugEntry.Error = "Unsupported model/provider. Use antigravity/*, codex/*, kiro/*, or trae/*."
		writeError(w, http.StatusBadRequest, "Unsupported model/provider. Use antigravity/*, codex/*, kiro/*, or trae/*.")
		return
	}
	debugEntry.Provider = provider.ID
	debugEntry.Model = provider.ID + "/" + model
	if apiKey != nil && !apiKeyAllowsModel(*apiKey, provider.ID+"/"+model) {
		debugEntry.Status = "error"
		debugEntry.HTTPStatus = http.StatusForbidden
		debugEntry.Error = "API key is not allowed to use this model"
		writeError(w, http.StatusForbidden, "API key is not allowed to use this model")
		return
	}
	cred, err := s.store.FirstConnection(provider.ID)
	if err != nil {
		debugEntry.Status = "error"
		debugEntry.HTTPStatus = http.StatusBadGateway
		debugEntry.Error = "No active account is configured for this provider"
		writeError(w, http.StatusBadGateway, "No active account is configured for this provider")
		return
	}
	debugEntry.ConnectionID = cred.ID
	debugEntry.AccountName = cred.Name
	if provider.ID == "trae" {
		refreshed, err := s.refreshTraeConnectionToken(r.Context(), cred)
		if err != nil {
			debugEntry.Status = "error"
			debugEntry.HTTPStatus = http.StatusBadGateway
			debugEntry.Error = err.Error()
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		cred = refreshed
	}
	result, err := executors.For(provider.ID).Execute(r.Context(), provider, model, req, cred)
	if err != nil {
		s.recordQuotaError(cred, err)
		debugEntry.Status = "error"
		debugEntry.HTTPStatus = http.StatusBadGateway
		debugEntry.Error = err.Error()
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	s.recordQuotaSuccess(cred, result)
	debugEntry.Status = "ok"
	debugEntry.HTTPStatus = http.StatusOK
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
	if path == "/api" || strings.HasPrefix(path, "/api/") {
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
			filepath.Join(exeDir, "web"),
			filepath.Join(exeDir, "web", "build", "client"),
			filepath.Join(exeDir, "web", "dist"),
			filepath.Join(exeDir, "..", "web"),
			filepath.Join(exeDir, "..", "web", "build", "client"),
			filepath.Join(exeDir, "..", "web", "dist"),
		)
	}
	return append(candidates,
		filepath.Join("web"),
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

func restartProcessSoon() {
	time.Sleep(500 * time.Millisecond)
	executable, err := os.Executable()
	if err != nil {
		log.Printf("restart failed: locate executable: %v", err)
		return
	}
	log.Printf("restarting BRoute with %s", executable)
	if err := syscall.Exec(executable, os.Args, os.Environ()); err != nil {
		log.Printf("restart failed: exec new process: %v", err)
	}
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

func (s *Server) recordQuotaSuccess(cred store.ProviderConnection, result executors.Result) {
	data := quotaMetaForProvider(cred.Provider, result.Meta)
	if data == nil {
		data = map[string]any{}
	}
	resetAt := quotaResetAt(data)
	detail := "Last request succeeded"
	if resetAt != nil {
		detail = "Last request succeeded. Quota resets " + resetAt.Format(time.RFC3339)
	}
	_ = s.store.UpdateConnectionQuota(cred.ID, quotaPlanStatus(cred.Provider, data), detail, resetAt, data)
}

func (s *Server) recordQuotaError(cred store.ProviderConnection, err error) {
	message := err.Error()
	status := "error"
	if isQuotaError(message) {
		status = "limited"
	}
	resetAt, planType := quotaErrorDetails(message)
	data := map[string]any{"error": message}
	if resetAt != nil {
		data["resetAt"] = resetAt.Format(time.RFC3339)
	}
	if cred.Provider == "codex" && planType != "" {
		status = normalizeCodexPlanStatus(planType)
		data["planType"] = status
	}
	_ = s.store.UpdateConnectionQuota(cred.ID, status, message, resetAt, data)
}

func quotaMetaForProvider(provider string, meta map[string]any) map[string]any {
	if meta == nil {
		return nil
	}
	if provider == "codex" {
		if quota, ok := meta["codexQuota"].(map[string]any); ok {
			return quota
		}
	}
	return nil
}

func fetchTraeQuota(ctx context.Context, cred store.ProviderConnection) (map[string]any, *time.Time, error) {
	if strings.TrimSpace(cred.AccessToken) == "" {
		return nil, nil, errors.New("trae access token is missing")
	}
	body := strings.NewReader(`{"require_usage":true}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api-sg-central.trae.ai/trae/api/v1/pay/user_current_entitlement_list", body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Cloud-IDE-JWT "+cred.AccessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", "https://www.trae.ai")
	req.Header.Set("Referer", "https://www.trae.ai/")
	req.Header.Set("User-Agent", "Mozilla/5.0 AppleWebKit/537.36 Chrome/148.0.0.0 Safari/537.36")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("trae quota request failed: %w", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("trae quota [%d] %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	quotaData, resetAt, err := normalizeTraeQuotaResponse(data)
	if err != nil {
		return nil, nil, err
	}
	return quotaData, resetAt, nil
}

func normalizeTraeQuotaResponse(data []byte) (map[string]any, *time.Time, error) {
	type traeQuotaLimits struct {
		BasicUsageLimit              float64 `json:"basic_usage_limit"`
		AdvancedModelRequestLimit    float64 `json:"advanced_model_request_limit"`
		PremiumModelFastRequestLimit float64 `json:"premium_model_fast_request_limit"`
		PremiumModelSlowRequestLimit float64 `json:"premium_model_slow_request_limit"`
		AutoCompletionLimit          float64 `json:"auto_completion_limit"`
		EnableSoloLite               bool    `json:"enable_solo_lite"`
		EnableSoloWeb                bool    `json:"enable_solo_web"`
		EnableSoloAgent              bool    `json:"enable_solo_agent"`
		EnableSuperModel             bool    `json:"enable_super_model"`
		NoBonusQuota                 bool    `json:"no_bonus_quota"`
	}
	type traeQuotaUsage struct {
		BasicUsageAmount             float64 `json:"basic_usage_amount"`
		AdvancedModelRequestUsage    float64 `json:"advanced_model_request_usage"`
		PremiumModelFastRequestUsage float64 `json:"premium_model_fast_request_usage"`
		PremiumModelSlowRequestUsage float64 `json:"premium_model_slow_request_usage"`
		AutoCompletionUsage          float64 `json:"auto_completion_usage"`
		IsFlashConsuming             bool    `json:"is_flash_consuming"`
	}
	type traeEntitlementPack struct {
		DisplayDesc         string `json:"display_desc"`
		EntitlementBaseInfo struct {
			EndTime int64           `json:"end_time"`
			Quota   traeQuotaLimits `json:"quota"`
		} `json:"entitlement_base_info"`
		Usage traeQuotaUsage `json:"usage"`
	}
	var decoded struct {
		UserEntitlementPackList []traeEntitlementPack `json:"user_entitlement_pack_list"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, nil, fmt.Errorf("decode trae quota response failed: %w", err)
	}
	if len(decoded.UserEntitlementPackList) == 0 {
		return nil, nil, errors.New("trae quota response missing entitlement pack")
	}
	pack := decoded.UserEntitlementPackList[0]
	plan := strings.ToLower(strings.TrimSpace(pack.DisplayDesc))
	planType := "paid"
	if strings.Contains(plan, "free") || pack.EntitlementBaseInfo.Quota.BasicUsageLimit <= 3 {
		planType = "free"
	}
	var resetAt *time.Time
	resetString := ""
	if pack.EntitlementBaseInfo.EndTime > 0 {
		parsed := time.Unix(pack.EntitlementBaseInfo.EndTime, 0).UTC()
		resetAt = &parsed
		resetString = parsed.Format(time.RFC3339)
	}
	basicUsage := roundMoney(pack.Usage.BasicUsageAmount)
	basicLimit := roundMoney(pack.EntitlementBaseInfo.Quota.BasicUsageLimit)
	quotaData := map[string]any{
		"planType":    planType,
		"displayDesc": pack.DisplayDesc,
		"resetAt":     resetString,
		"basicUsage": map[string]any{
			"usage":   basicUsage,
			"limit":   basicLimit,
			"resetAt": resetString,
		},
		"advancedRequests": map[string]any{"usage": pack.Usage.AdvancedModelRequestUsage, "limit": pack.EntitlementBaseInfo.Quota.AdvancedModelRequestLimit, "resetAt": resetString},
		"premiumFast":      map[string]any{"usage": pack.Usage.PremiumModelFastRequestUsage, "limit": pack.EntitlementBaseInfo.Quota.PremiumModelFastRequestLimit, "resetAt": resetString},
		"premiumSlow":      map[string]any{"usage": pack.Usage.PremiumModelSlowRequestUsage, "limit": pack.EntitlementBaseInfo.Quota.PremiumModelSlowRequestLimit, "resetAt": resetString},
		"autoCompletion":   map[string]any{"usage": pack.Usage.AutoCompletionUsage, "limit": pack.EntitlementBaseInfo.Quota.AutoCompletionLimit, "resetAt": resetString},
		"features": map[string]any{
			"enableSoloLite":   pack.EntitlementBaseInfo.Quota.EnableSoloLite,
			"enableSoloWeb":    pack.EntitlementBaseInfo.Quota.EnableSoloWeb,
			"enableSoloAgent":  pack.EntitlementBaseInfo.Quota.EnableSoloAgent,
			"enableSuperModel": pack.EntitlementBaseInfo.Quota.EnableSuperModel,
			"noBonusQuota":     pack.EntitlementBaseInfo.Quota.NoBonusQuota,
			"isFlashConsuming": pack.Usage.IsFlashConsuming,
		},
	}
	return quotaData, resetAt, nil
}

func roundMoney(value float64) float64 {
	return float64(int(value*100+0.5)) / 100
}

func isQuotaError(message string) bool {
	lower := strings.ToLower(message)
	return strings.Contains(lower, "usage_limit") || strings.Contains(lower, "quota") || strings.Contains(lower, "rate_limit") || strings.Contains(lower, "too many requests") || strings.Contains(lower, "[429]")
}

func quotaErrorDetails(message string) (*time.Time, string) {
	var body struct {
		Error struct {
			PlanType string `json:"plan_type"`
			ResetsAt int64  `json:"resets_at"`
		} `json:"error"`
	}
	start := strings.Index(message, "{")
	if start < 0 {
		return nil, ""
	}
	if err := json.Unmarshal([]byte(message[start:]), &body); err != nil {
		return nil, ""
	}
	planType := strings.TrimSpace(body.Error.PlanType)
	if body.Error.ResetsAt == 0 {
		return nil, planType
	}
	reset := time.Unix(body.Error.ResetsAt, 0).UTC()
	return &reset, planType
}

func normalizeCodexPlanStatus(planType string) string {
	switch strings.ToLower(strings.TrimSpace(planType)) {
	case "free":
		return "free"
	case "plus", "pro", "team", "enterprise", "paid":
		return "paid"
	default:
		return strings.ToLower(strings.TrimSpace(planType))
	}
}

func normalizeTraePlanStatus(planType string) string {
	switch strings.ToLower(strings.TrimSpace(planType)) {
	case "free", "free plan":
		return "free"
	case "plus", "pro", "paid", "premium", "premium plan":
		return "paid"
	default:
		return ""
	}
}

func quotaResetAt(data map[string]any) *time.Time {
	var latest *time.Time
	for _, key := range []string{"fiveHour", "sevenDay", "thirtyDay", "basicUsage", "advancedRequests", "premiumFast", "premiumSlow", "autoCompletion"} {
		window, ok := data[key].(map[string]any)
		if !ok {
			continue
		}
		value, ok := window["resetAt"].(string)
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			continue
		}
		if latest == nil || parsed.After(*latest) {
			v := parsed.UTC()
			latest = &v
		}
	}
	return latest
}

func quotaPlanStatus(provider string, data map[string]any) string {
	if provider == "trae" {
		if plan := normalizeTraePlanStatus(stringValue(data, "planType", "")); plan != "" {
			return plan
		}
		return "available"
	}
	if provider != "codex" {
		return "available"
	}
	if quotaWindowLimit(data, "fiveHour") > 0 || quotaWindowLimit(data, "sevenDay") > 0 {
		return "paid"
	}
	if quotaWindowLimit(data, "thirtyDay") > 0 {
		return "free"
	}
	return "available"
}

func providerQuotaConnectionsResponse(connections []store.ProviderConnection) []providerQuotaConnectionResponse {
	out := make([]providerQuotaConnectionResponse, 0, len(connections))
	for _, connection := range connections {
		out = append(out, providerQuotaConnectionResponse{
			ID:                  connection.ID,
			Provider:            connection.Provider,
			Name:                connection.Name,
			Email:               connection.Email,
			DisplayName:         connection.DisplayName,
			AuthType:            connection.AuthType,
			IsActive:            connection.IsActive,
			Priority:            connection.Priority,
			DefaultModel:        connection.DefaultModel,
			RateLimitProtection: connection.RateLimitProtection,
			ProjectID:           connection.ProjectID,
			Quota:               normalizeProviderQuota(connection),
			CreatedAt:           connection.CreatedAt,
			UpdatedAt:           connection.UpdatedAt,
		})
	}
	return out
}

func normalizeProviderQuota(connection store.ProviderConnection) providerQuotaResponse {
	resetAt := timePtrString(connection.QuotaResetAt)
	checkedAt := timePtrString(connection.QuotaCheckedAt)
	quota := providerQuotaResponse{Provider: connection.Provider, State: normalizeQuotaState(connection.QuotaStatus), Plan: normalizeQuotaPlan(connection.Provider, connection.QuotaStatus, connection.QuotaData), Limited: isQuotaError(connection.QuotaDetail) || connection.QuotaStatus == "limited", ResetAt: resetAt, CheckedAt: checkedAt, Windows: quotaWindowsResponse(connection.Provider, connection.QuotaData)}
	if quota.Plan == "" && connection.Provider == "codex" {
		quota.Plan = normalizeCodexPlanStatus(stringValue(connection.QuotaData, "planType", ""))
	}
	if quota.Plan == "available" {
		quota.Plan = ""
	}
	if quota.ResetAt == "" {
		quota.ResetAt = stringValue(connection.QuotaData, "resetAt", "")
	}
	return quota
}

func normalizeQuotaState(status string) string {
	switch status {
	case "free", "paid", "plus", "available":
		return "available"
	case "limited":
		return "limited"
	case "error":
		return "error"
	default:
		if strings.TrimSpace(status) == "" {
			return "unknown"
		}
		return status
	}
}

func normalizeQuotaPlan(provider, status string, data map[string]any) string {
	if provider == "trae" {
		if plan := normalizeTraePlanStatus(stringValue(data, "planType", "")); plan != "" {
			return plan
		}
		return normalizeTraePlanStatus(status)
	}
	if provider != "codex" {
		return ""
	}
	if plan := normalizeCodexPlanStatus(stringValue(data, "planType", "")); plan != "" {
		return plan
	}
	switch status {
	case "paid", "plus", "free":
		return normalizeCodexPlanStatus(status)
	}
	if quotaWindowExists(data, "fiveHour") || quotaWindowExists(data, "sevenDay") {
		return "paid"
	}
	if quotaWindowExists(data, "thirtyDay") {
		return "free"
	}
	return ""
}

func quotaWindowsResponse(provider string, data map[string]any) []providerQuotaWindowResponse {
	defs := []struct{ key, label string }{{"basicUsage", "Usage ($)"}, {"advancedRequests", "Advanced"}, {"premiumFast", "Fast"}, {"premiumSlow", "Slow"}, {"autoCompletion", "Autocomplete"}}
	includeMissing := false
	if provider == "codex" {
		defs = []struct{ key, label string }{{"fiveHour", "5h"}, {"sevenDay", "7d"}, {"thirtyDay", "30d"}}
		includeMissing = true
	}
	windows := make([]providerQuotaWindowResponse, 0, len(defs))
	for _, def := range defs {
		window, ok := quotaWindowValues(data, def.key)
		if !ok {
			if !includeMissing {
				continue
			}
			window = providerQuotaWindowResponse{Usage: 0, Limit: 0, Remaining: 0, Percent: 100, Exhausted: true}
		}
		window.Key = def.key
		window.Label = def.label
		windows = append(windows, window)
	}
	return windows
}

func quotaWindowValues(data map[string]any, key string) (providerQuotaWindowResponse, bool) {
	window, ok := data[key].(map[string]any)
	if !ok {
		return providerQuotaWindowResponse{}, false
	}
	usage, usageOK := numberValue(window["usage"])
	limit, limitOK := numberValue(window["limit"])
	if !usageOK || !limitOK {
		return providerQuotaWindowResponse{}, false
	}
	remaining := limit - usage
	if remaining < 0 {
		remaining = 0
	}
	percent := float64(100)
	if limit > 0 {
		percent = usage / limit * 100
		if percent > 100 {
			percent = 100
		}
	}
	resetAt, _ := window["resetAt"].(string)
	return providerQuotaWindowResponse{Usage: usage, Limit: limit, Remaining: remaining, Percent: percent, Exhausted: remaining <= 0, ResetAt: resetAt}, true
}

func quotaWindowExists(data map[string]any, key string) bool {
	_, ok := quotaWindowValues(data, key)
	return ok
}

func numberValue(value any) (float64, bool) {
	switch v := value.(type) {
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case float64:
		return v, true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func stringValue(data map[string]any, key, fallback string) string {
	if data == nil {
		return fallback
	}
	value, ok := data[key].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func timePtrString(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
}

func quotaDebugString(data map[string]any) string {
	if data == nil {
		return "null"
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return fmt.Sprintf("<marshal error: %v>", err)
	}
	return string(encoded)
}

func quotaHasLimit(data map[string]any) bool {
	return quotaWindowLimit(data, "fiveHour") > 0 || quotaWindowLimit(data, "sevenDay") > 0 || quotaWindowLimit(data, "thirtyDay") > 0
}

func quotaWindowLimit(data map[string]any, key string) float64 {
	window, ok := data[key].(map[string]any)
	if !ok {
		return 0
	}
	switch value := window["limit"].(type) {
	case int:
		return float64(value)
	case int64:
		return float64(value)
	case float64:
		return value
	case json.Number:
		parsed, _ := value.Float64()
		return parsed
	default:
		return 0
	}
}

func (s *Server) availableProviderIDs() map[string]bool {
	connections, err := s.store.ListConnections("")
	if err != nil {
		return map[string]bool{}
	}
	available := map[string]bool{}
	for _, connection := range connections {
		if !connection.IsActive {
			continue
		}
		if strings.TrimSpace(connection.AccessToken) == "" && strings.TrimSpace(connection.APIKey) == "" {
			continue
		}
		available[connection.Provider] = true
	}
	return available
}

func (s *Server) validAllowedModels(models []string) ([]string, error) {
	if len(models) == 0 {
		return []string{}, nil
	}
	available := map[string]bool{}
	availableProviders := s.availableProviderIDs()
	for _, p := range providers.List() {
		if !availableProviders[p.ID] {
			continue
		}
		for _, m := range p.Models {
			available[p.ID+"/"+m.ID] = true
		}
	}
	seen := map[string]bool{}
	allowed := []string{}
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || seen[model] {
			continue
		}
		if !available[model] {
			return nil, fmt.Errorf("model %s is not available", model)
		}
		seen[model] = true
		allowed = append(allowed, model)
	}
	return allowed, nil
}

func (s *Server) gatewayAuthorized(w http.ResponseWriter, r *http.Request) (*store.APIKey, bool) {
	value := bearerToken(r.Header.Get("Authorization"))
	if value != "" {
		key, err := s.store.GetAPIKey(value)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "Invalid API key")
			return nil, false
		}
		return &key, true
	}
	settings, _ := s.store.GetSettings()
	if settings.RequireLogin && !s.auth.Authenticated(r) {
		writeError(w, http.StatusUnauthorized, "API key is required")
		return nil, false
	}
	return nil, true
}

func apiKeyAllowsModel(key store.APIKey, model string) bool {
	if len(key.AllowedModels) == 0 {
		return true
	}
	for _, allowed := range key.AllowedModels {
		if allowed == model {
			return true
		}
	}
	return false
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return strings.TrimSpace(parts[1])
	}
	return ""
}

func maskAPIKeys(keys []store.APIKey) {
	for i := range keys {
		keys[i].Key = maskSecret(keys[i].Key)
	}
}

func maskSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "********"
	}
	return value[:4] + "..." + value[len(value)-4:]
}
