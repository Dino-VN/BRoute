package executors

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"broute/internal/providers"
	"broute/internal/store"

	"github.com/google/uuid"
)

type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Stream   bool           `json:"stream"`
	Raw      map[string]any `json:"-"`
	Debug    *DebugInfo     `json:"-"`
}

type DebugInfo struct {
	ConvertedBody any    `json:"convertedBody,omitempty"`
	ToolCallDump  string `json:"toolCallDump,omitempty"`
	UpstreamURL   string `json:"upstreamUrl,omitempty"`
	StatusCode    int    `json:"statusCode,omitempty"`
	ResponseBody  string `json:"responseBody,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type Result struct {
	Text   string
	Stream func(func(string) error) error
	Usage  map[string]any
	Meta   map[string]any
}

type Executor interface {
	Execute(ctx context.Context, p providers.Provider, model string, req ChatRequest, cred store.ProviderConnection) (Result, error)
}

func For(provider string) Executor {
	switch provider {
	case "trae":
		return Trae{}
	case "kiro":
		return Kiro{}
	case "codex":
		return Codex{}
	case "antigravity":
		return Antigravity{}
	default:
		return Mock{Provider: provider}
	}
}

type Mock struct{ Provider string }

func (m Mock) Execute(ctx context.Context, p providers.Provider, model string, req ChatRequest, cred store.ProviderConnection) (Result, error) {
	_ = ctx
	_ = p
	_ = cred
	text := fmt.Sprintf("%s/%s executor scaffold is ready. Configure credentials to enable live upstream routing.", m.Provider, model)
	return Result{Text: text, Stream: func(emit func(string) error) error { return emit(text) }}, nil
}

type Trae struct{}

func (Trae) Execute(ctx context.Context, p providers.Provider, model string, req ChatRequest, cred store.ProviderConnection) (Result, error) {
	if cred.AccessToken == "" {
		return Mock{Provider: "trae"}.Execute(ctx, p, model, req, cred)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	body, err := BuildTraeSoloSessionRequest(model, req.Messages, cred.ProviderSpecificData, req.Raw)
	if err != nil {
		return Result{}, err
	}
	if req.Debug != nil {
		req.Debug.ConvertedBody = body
		req.Debug.ToolCallDump = body.InitialMessage.Query
	}
	bodyBytes, _ := json.Marshal(body)
	base := strings.TrimRight(p.BaseURL, "/")
	if req.Debug != nil {
		req.Debug.UpstreamURL = base + "/chat_sessions"
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/chat_sessions", bytes.NewReader(bodyBytes))
	if err != nil {
		return Result{}, err
	}
	for k, v := range traeHeaders(cred) {
		hreq.Header.Set(k, v)
	}
	res, err := client.Do(hreq)
	if err != nil {
		return Result{}, err
	}
	defer res.Body.Close()
	if req.Debug != nil {
		req.Debug.StatusCode = res.StatusCode
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		data, _ := io.ReadAll(res.Body)
		if req.Debug != nil {
			req.Debug.ResponseBody = string(data)
		}
		return Result{}, fmt.Errorf("trae create session [%d] %s", res.StatusCode, string(data))
	}
	data, _ := io.ReadAll(res.Body)
	if req.Debug != nil {
		req.Debug.ResponseBody = string(data)
	}
	var createResponse struct {
		Code int `json:"code"`
		Data struct {
			ChatSessionID string `json:"chat_session_id"`
			MessageID     string `json:"message_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(data, &createResponse); err != nil {
		return Result{}, fmt.Errorf("decode trae create session response failed: %w", err)
	}
	if createResponse.Code != 0 || createResponse.Data.ChatSessionID == "" || createResponse.Data.MessageID == "" {
		return Result{}, fmt.Errorf("trae create session response invalid: %s", string(data))
	}
	eventsURL := fmt.Sprintf("%s/chat_sessions/%s/events?reply_to_message_id=%s", base, createResponse.Data.ChatSessionID, url.QueryEscape(createResponse.Data.MessageID))
	if req.Debug != nil {
		req.Debug.UpstreamURL = eventsURL
	}
	eventsReq, err := http.NewRequestWithContext(ctx, http.MethodGet, eventsURL, nil)
	if err != nil {
		return Result{}, err
	}
	for k, v := range traeHeaders(cred) {
		eventsReq.Header.Set(k, v)
	}
	eventsRes, err := client.Do(eventsReq)
	if err != nil {
		return Result{}, err
	}
	defer eventsRes.Body.Close()
	if req.Debug != nil {
		req.Debug.StatusCode = eventsRes.StatusCode
	}
	if eventsRes.StatusCode < 200 || eventsRes.StatusCode >= 300 {
		data, _ := io.ReadAll(eventsRes.Body)
		if req.Debug != nil {
			req.Debug.ResponseBody = string(data)
		}
		return Result{}, fmt.Errorf("trae events [%d] %s", eventsRes.StatusCode, string(data))
	}
	eventsData, _ := io.ReadAll(eventsRes.Body)
	if req.Debug != nil {
		req.Debug.ResponseBody = "create_session:\n" + string(data) + "\n\nevents:\n" + string(eventsData)
	}
	stream := func(emit func(string) error) error {
		return ParseTraeSSE(bytes.NewReader(eventsData), emit)
	}
	return Result{Stream: stream}, nil
}

func ResolveTraeMode(model string) (mode, strategy, modelName string) {
	m := strings.ToLower(strings.TrimSpace(model))
	if m == "work" || m == "auto-work" || m == "solo-work" {
		return "work", "auto", ""
	}
	if m == "" || m == "auto" {
		return "code", "auto", ""
	}
	return "code", "manual", model
}

func ParseTraeSSE(r io.Reader, emit func(string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	order := []string{}
	thoughts := map[string]string{}
	sent := 0
	event := ""
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if line == "" {
			event = ""
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &data); err != nil {
			continue
		}
		if event == "error" {
			return fmt.Errorf("trae error: %v", data)
		}
		if event == "done" {
			return nil
		}
		if event == "output" {
			text := ""
			if reasoning, _ := data["reasoning_content"].(string); reasoning != "" {
				text += reasoning
			}
			if response, _ := data["response"].(string); response != "" {
				text += response
			}
			if text != "" {
				if err := emit(text); err != nil {
					return err
				}
			}
			continue
		}
		if event != "plan_item" {
			continue
		}
		id, _ := data["id"].(string)
		thought, _ := data["thought"].(string)
		if id == "" {
			continue
		}
		if _, ok := thoughts[id]; !ok {
			order = append(order, id)
		}
		if len(thought) >= len(thoughts[id]) {
			thoughts[id] = thought
		}
		full := ""
		for _, key := range order {
			full += thoughts[key]
		}
		if len(full) > sent {
			if err := emit(full[sent:]); err != nil {
				return err
			}
			sent = len(full)
		}
	}
	return scanner.Err()
}

type Kiro struct{}

func (Kiro) Execute(ctx context.Context, p providers.Provider, model string, req ChatRequest, cred store.ProviderConnection) (Result, error) {
	if cred.AccessToken == "" {
		return Mock{Provider: "kiro"}.Execute(ctx, p, model, req, cred)
	}
	return Mock{Provider: "kiro-live-http-pending-eventstream"}.Execute(ctx, p, model, req, cred)
}

type EventFrame struct {
	Headers map[string]string
	Payload map[string]any
}

func ParseKiroEventFrame(frame []byte) (EventFrame, error) {
	if len(frame) < 16 {
		return EventFrame{}, errors.New("short eventstream frame")
	}
	totalLen := int(binary.BigEndian.Uint32(frame[0:4]))
	headersLen := int(binary.BigEndian.Uint32(frame[4:8]))
	if totalLen != len(frame) || headersLen < 0 || 12+headersLen > len(frame)-4 {
		return EventFrame{}, errors.New("invalid eventstream lengths")
	}
	headers, err := parseEventHeaders(frame[12 : 12+headersLen])
	if err != nil {
		return EventFrame{}, err
	}
	payloadBytes := frame[12+headersLen : len(frame)-4]
	payload := map[string]any{}
	if len(bytes.TrimSpace(payloadBytes)) > 0 {
		_ = json.Unmarshal(payloadBytes, &payload)
	}
	return EventFrame{Headers: headers, Payload: payload}, nil
}

func parseEventHeaders(data []byte) (map[string]string, error) {
	out := map[string]string{}
	for i := 0; i < len(data); {
		nameLen := int(data[i])
		i++
		if i+nameLen+3 > len(data) {
			return nil, errors.New("invalid header")
		}
		name := string(data[i : i+nameLen])
		i += nameLen
		typ := data[i]
		i++
		if typ != 7 {
			return nil, fmt.Errorf("unsupported event header type %d", typ)
		}
		valueLen := int(binary.BigEndian.Uint16(data[i : i+2]))
		i += 2
		if i+valueLen > len(data) {
			return nil, errors.New("invalid header value")
		}
		out[name] = string(data[i : i+valueLen])
		i += valueLen
	}
	return out, nil
}

type Codex struct{}

func (Codex) Execute(ctx context.Context, p providers.Provider, model string, req ChatRequest, cred store.ProviderConnection) (Result, error) {
	if cred.AccessToken == "" {
		return Mock{Provider: "codex"}.Execute(ctx, p, normalizeCodexModel(model), req, cred)
	}
	body := buildCodexResponsesBody(model, req, cred)
	bodyBytes, _ := json.Marshal(body)
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.BaseURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return Result{}, err
	}
	for k, v := range codexHeaders(cred) {
		hreq.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 10 * time.Minute}
	res, err := client.Do(hreq)
	if err != nil {
		return Result{}, err
	}
	quota := ParseCodexQuotaHeaders(res.Header)
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		defer res.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(res.Body, 64*1024))
		result := Result{Meta: map[string]any{"codexQuota": quota.ToMap()}}
		if cooldown := CodexCooldown(quota, time.Now()); cooldown > 0 {
			return result, fmt.Errorf("codex upstream [%d] quota cooldown %s: %s", res.StatusCode, cooldown.Round(time.Second), strings.TrimSpace(string(data)))
		}
		return result, fmt.Errorf("codex upstream [%d] %s", res.StatusCode, strings.TrimSpace(string(data)))
	}
	return Result{Stream: func(emit func(string) error) error {
		defer res.Body.Close()
		return ParseCodexResponsesSSE(res.Body, emit)
	}, Meta: map[string]any{"codexQuota": quota.ToMap()}}, nil
}

type CodexQuotaWindow struct {
	Usage   int
	Limit   int
	ResetAt time.Time
}

type CodexQuota struct {
	FiveHour  CodexQuotaWindow
	SevenDay  CodexQuotaWindow
	ThirtyDay CodexQuotaWindow
}

func ParseCodexQuotaHeaders(h http.Header) CodexQuota {
	return CodexQuota{
		FiveHour:  quotaWindowFromHeaders(h, []string{"x-codex-5h", "x-codex-5-hour"}),
		SevenDay:  quotaWindowFromHeaders(h, []string{"x-codex-7d", "x-codex-7-day"}),
		ThirtyDay: quotaWindowFromHeaders(h, []string{"x-codex-30d", "x-codex-30-day", "x-codex-30day", "x-codex-monthly"}),
	}
}

func CodexCooldown(q CodexQuota, now time.Time) time.Duration {
	cooldown := time.Duration(0)
	for _, w := range []CodexQuotaWindow{q.FiveHour, q.SevenDay, q.ThirtyDay} {
		if w.Limit > 0 && w.Usage >= w.Limit && w.ResetAt.After(now) {
			if d := w.ResetAt.Sub(now); d > cooldown {
				cooldown = d
			}
		}
	}
	return cooldown
}

func (q CodexQuota) ToMap() map[string]any {
	return map[string]any{"fiveHour": q.FiveHour.toMap(), "sevenDay": q.SevenDay.toMap(), "thirtyDay": q.ThirtyDay.toMap()}
}

func quotaWindowFromHeaders(h http.Header, prefixes []string) CodexQuotaWindow {
	return CodexQuotaWindow{
		Usage:   firstIntHeader(h, suffixes(prefixes, "usage")...),
		Limit:   firstIntHeader(h, suffixes(prefixes, "limit")...),
		ResetAt: firstTimeHeader(h, suffixes(prefixes, "reset-at")...),
	}
}

func suffixes(prefixes []string, suffix string) []string {
	names := make([]string, 0, len(prefixes))
	for _, prefix := range prefixes {
		names = append(names, prefix+"-"+suffix)
	}
	return names
}

func firstIntHeader(h http.Header, names ...string) int {
	for _, name := range names {
		if value := intHeader(h, name); value != 0 {
			return value
		}
	}
	return 0
}

func firstTimeHeader(h http.Header, names ...string) time.Time {
	for _, name := range names {
		if value := timeHeader(h, name); !value.IsZero() {
			return value
		}
	}
	return time.Time{}
}

func (w CodexQuotaWindow) toMap() map[string]any {
	out := map[string]any{"usage": w.Usage, "limit": w.Limit}
	if !w.ResetAt.IsZero() {
		out["resetAt"] = w.ResetAt.Format(time.RFC3339)
	}
	return out
}

func intHeader(h http.Header, name string) int {
	v := strings.TrimSpace(h.Get(name))
	if v == "" {
		return 0
	}
	var out int
	_, _ = fmt.Sscanf(v, "%d", &out)
	return out
}

func timeHeader(h http.Header, name string) time.Time {
	v := strings.TrimSpace(h.Get(name))
	if v == "" {
		return time.Time{}
	}
	if ts, err := time.Parse(time.RFC3339, v); err == nil {
		return ts
	}
	if ts, err := time.Parse(time.RFC1123, v); err == nil {
		return ts
	}
	return time.Time{}
}

func normalizeCodexModel(model string) string {
	for _, suffix := range []string{"-xhigh", "-high", "-medium", "-low", "-none", "-fast"} {
		model = strings.TrimSuffix(model, suffix)
	}
	return model
}

func buildCodexResponsesBody(model string, req ChatRequest, cred store.ProviderConnection) map[string]any {
	cleanModel, effort := splitCodexModel(model)
	body := map[string]any{
		"model":        cleanModel,
		"stream":       true,
		"store":        false,
		"instructions": "You are a ChatGPT agent.",
		"input":        codexInput(req.Messages),
	}
	if effort != "" {
		body["reasoning"] = map[string]any{"effort": effort}
	}
	if req.Raw != nil {
		if input, ok := req.Raw["input"]; ok {
			body["input"] = input
		}
		if instructions, ok := req.Raw["instructions"].(string); ok && strings.TrimSpace(instructions) != "" {
			body["instructions"] = instructions
		}
		if cacheKey, ok := req.Raw["prompt_cache_key"].(string); ok && cacheKey != "" {
			body["prompt_cache_key"] = cacheKey
		} else if sessionID := stringValue(cred.ProviderSpecificData, "workspaceId", ""); sessionID != "" {
			body["prompt_cache_key"] = sessionID
		}
	}
	return body
}

func codexInput(messages []Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		role := msg.Role
		if role == "system" {
			role = "developer"
		}
		if role == "" {
			role = "user"
		}
		out = append(out, map[string]any{
			"type":    "message",
			"role":    role,
			"content": []map[string]any{{"type": "input_text", "text": contentToText(msg.Content)}},
		})
	}
	return out
}

func splitCodexModel(model string) (string, string) {
	for _, effort := range []string{"xhigh", "high", "medium", "low", "none"} {
		suffix := "-" + effort
		if strings.HasSuffix(model, suffix) {
			return strings.TrimSuffix(model, suffix), effort
		}
	}
	return strings.TrimSuffix(model, "-fast"), ""
}

func codexHeaders(cred store.ProviderConnection) map[string]string {
	version := safeEnv("CODEX_CLIENT_VERSION", "0.132.0")
	ua := os.Getenv("CODEX_USER_AGENT")
	if ua == "" {
		ua = fmt.Sprintf("codex-cli/%s (Windows 10.0.26200; x64)", version)
	}
	headers := map[string]string{
		"Authorization":         "Bearer " + cred.AccessToken,
		"Content-Type":          "application/json",
		"Accept":                "text/event-stream",
		"Version":               version,
		"Openai-Beta":           "responses=experimental",
		"X-Codex-Beta-Features": "responses_websockets",
		"User-Agent":            ua,
		"originator":            "codex_cli_rs",
	}
	if sentinel := stringValue(cred.ProviderSpecificData, "sentinelToken", ""); sentinel != "" {
		headers["OpenAI-Sentinel-Chat-Requirements-Token"] = sentinel
	}
	if workspaceID := stringValue(cred.ProviderSpecificData, "workspaceId", ""); workspaceID != "" {
		headers["chatgpt-account-id"] = workspaceID
		headers["session_id"] = workspaceID
	}
	return headers
}

func safeEnv(name, fallback string) string {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" || len(v) > 64 {
		return fallback
	}
	return v
}

func ParseCodexResponsesSSE(r io.Reader, emit func(string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024), 4*1024*1024)
	event := ""
	emitted := 0
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if line == "" {
			event = ""
			continue
		}
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "[DONE]" {
			return nil
		}
		var data map[string]any
		if err := json.Unmarshal([]byte(payload), &data); err != nil {
			continue
		}
		if msg := codexDeltaText(event, data); msg != "" {
			if err := emit(msg); err != nil {
				return err
			}
			emitted += len(msg)
		}
		if event == "response.completed" || data["type"] == "response.completed" {
			if full := codexCompletedText(data); len(full) > emitted {
				if err := emit(full[emitted:]); err != nil {
					return err
				}
			}
			return nil
		}
		if event == "response.failed" || data["type"] == "response.failed" || data["error"] != nil {
			return fmt.Errorf("codex response failed: %s", payload)
		}
	}
	return scanner.Err()
}

func codexDeltaText(event string, data map[string]any) string {
	if delta, ok := data["delta"].(string); ok && strings.Contains(event, "delta") {
		return delta
	}
	if text, ok := data["text"].(string); ok && strings.Contains(event, "delta") {
		return text
	}
	if typ, _ := data["type"].(string); strings.Contains(typ, "delta") {
		if delta, ok := data["delta"].(string); ok {
			return delta
		}
		if text, ok := data["text"].(string); ok {
			return text
		}
	}
	return ""
}

func codexCompletedText(data map[string]any) string {
	response, _ := data["response"].(map[string]any)
	if response == nil {
		response = data
	}
	output, _ := response["output"].([]any)
	var b strings.Builder
	for _, rawItem := range output {
		item, _ := rawItem.(map[string]any)
		if item == nil {
			continue
		}
		if text, ok := item["content"].(string); ok {
			b.WriteString(text)
			continue
		}
		content, _ := item["content"].([]any)
		for _, rawPart := range content {
			part, _ := rawPart.(map[string]any)
			if part == nil {
				continue
			}
			if text, ok := part["text"].(string); ok {
				b.WriteString(text)
			}
		}
	}
	return b.String()
}

type Antigravity struct{}

func (Antigravity) Execute(ctx context.Context, p providers.Provider, model string, req ChatRequest, cred store.ProviderConnection) (Result, error) {
	return Mock{Provider: "antigravity"}.Execute(ctx, p, model, req, cred)
}

func BuildAntigravityEnvelope(project, model string, request map[string]any) map[string]any {
	return map[string]any{"project": project, "model": model, "userAgent": "antigravity", "requestType": "agent", "requestId": uuid.NewString(), "request": request}
}

func traeHeaders(cred store.ProviderConnection) map[string]string {
	psd := cred.ProviderSpecificData
	return map[string]string{"Authorization": "Cloud-IDE-JWT " + cred.AccessToken, "Content-Type": "application/json", "X-Trae-Client-Type": "web", "X-Preferenced-Language": stringValue(psd, "appLanguage", "en"), "x-user-region": stringValue(psd, "userRegion", "US"), "Referer": "https://solo.trae.ai/", "User-Agent": "Mozilla/5.0 AppleWebKit/537.36 Chrome/148.0.0.0 Safari/537.36"}
}

func traeIDEHeaders(cred store.ProviderConnection, baseURL string) map[string]string {
	psd := cred.ProviderSpecificData
	return map[string]string{
		"Content-Type":       "application/json",
		"x-app-id":           stringValue(psd, "appId", "682161"),
		"x-ide-version":      "3.5.65",
		"x-ide-version-code": "20260625",
		"x-ide-version-type": "stable",
		"x-device-cpu":       stringValue(psd, "deviceCPU", "AMD"),
		"x-device-id":        stringValue(psd, "deviceId", ""),
		"x-machine-id":       stringValue(psd, "machineId", ""),
		"x-device-brand":     stringValue(psd, "deviceBrand", "92L3"),
		"x-device-type":      stringValue(psd, "deviceType", "windows"),
		"x-ide-token":        cred.AccessToken,
		"accept":             "*/*",
		"Connection":         "keep-alive",
		"User-Agent":         "",
		"Referer":            strings.TrimRight(baseURL, "/") + "/",
	}
}

func traeCommonParams(psd map[string]any, mode, sessionID string) string {
	cp := map[string]any{"language": "en-us", "app_language": stringValue(psd, "appLanguage", "en"), "quality": "stable", "app_version": stringValue(psd, "appVersion", "1.0.0.1229"), "web_id": stringValue(psd, "webId", ""), "user_identity": stringValue(psd, "userIdentity", "Free"), "is_freshman": "0", "biz_user_id": stringValue(psd, "bizUserId", ""), "user_unique_id": stringValue(psd, "userUniqueId", ""), "scope": stringValue(psd, "scope", "marscode-us"), "tenant": stringValue(psd, "tenant", "marscode"), "region": stringValue(psd, "region", "US-East"), "aiRegion": stringValue(psd, "aiRegion", stringValue(psd, "region", "US-East")), "is_privacy_mode": 0, "privacy_mode": "off", "solo_chat_mode": mode}
	if sessionID != "" {
		cp["biz_session_id"] = sessionID
	}
	data, _ := json.Marshal(cp)
	return string(data)
}

func stringValue(m map[string]any, key, fallback string) string {
	if v, ok := m[key].(string); ok && v != "" {
		return v
	}
	return fallback
}

func contentToText(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := []string{}
		for _, item := range t {
			if s, ok := item.(string); ok {
				parts = append(parts, s)
				continue
			}
			if m, ok := item.(map[string]any); ok {
				parts = append(parts, fmt.Sprint(m["text"]))
			}
		}
		return strings.Join(parts, "")
	default:
		return fmt.Sprint(v)
	}
}
