package executors

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"broute/internal/store"
)

func TestResolveTraeMode(t *testing.T) {
	mode, strategy, model := ResolveTraeMode("work")
	if mode != "work" || strategy != "auto" || model != "" {
		t.Fatalf("work mode mismatch: %s %s %s", mode, strategy, model)
	}
	mode, strategy, model = ResolveTraeMode("gpt-5.4")
	if mode != "code" || strategy != "manual" || model != "gpt-5.4" {
		t.Fatalf("manual mode mismatch: %s %s %s", mode, strategy, model)
	}
}

func TestFlattenTraeQuery(t *testing.T) {
	query := FlattenTraeQuery([]Message{{Role: "system", Content: "be concise"}, {Role: "user", Content: "hello"}})
	if !strings.Contains(query, "[System]\\nbe concise") || !strings.Contains(query, "hello") {
		t.Fatalf("unexpected query: %s", query)
	}
}

func TestParseTraeSSE(t *testing.T) {
	input := "event: plan_item\ndata: {\"id\":\"a\",\"thought\":\"hel\"}\n\nevent: plan_item\ndata: {\"id\":\"a\",\"thought\":\"hello\"}\n\nevent: done\ndata: {}\n\n"
	var out strings.Builder
	if err := ParseTraeSSE(strings.NewReader(input), func(piece string) error { out.WriteString(piece); return nil }); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello" {
		t.Fatalf("unexpected stream text %q", out.String())
	}
}

func TestParseKiroEventFrame(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"content": "hello"})
	headers := buildHeader(":event-type", "assistantResponseEvent")
	total := 12 + len(headers) + len(payload) + 4
	frame := make([]byte, total)
	binary.BigEndian.PutUint32(frame[0:4], uint32(total))
	binary.BigEndian.PutUint32(frame[4:8], uint32(len(headers)))
	copy(frame[12:], headers)
	copy(frame[12+len(headers):], payload)
	parsed, err := ParseKiroEventFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Headers[":event-type"] != "assistantResponseEvent" || parsed.Payload["content"] != "hello" {
		t.Fatalf("unexpected frame: %#v", parsed)
	}
}

func TestBuildAntigravityEnvelope(t *testing.T) {
	env := BuildAntigravityEnvelope("projects/demo", "gemini-3-pro-preview", map[string]any{"x": 1})
	if env["project"] != "projects/demo" || env["model"] != "gemini-3-pro-preview" || env["requestId"] == "" {
		t.Fatalf("unexpected envelope: %#v", env)
	}
}

func TestCodexResponsesTransform(t *testing.T) {
	body := buildCodexResponsesBody("gpt-5.5-high", ChatRequest{Messages: []Message{{Role: "system", Content: "rules"}, {Role: "user", Content: "hello"}}}, zeroCred())
	if body["model"] != "gpt-5.5" {
		t.Fatalf("unexpected model: %#v", body["model"])
	}
	reasoning := body["reasoning"].(map[string]any)
	if reasoning["effort"] != "high" {
		t.Fatalf("unexpected reasoning: %#v", reasoning)
	}
	input := body["input"].([]map[string]any)
	if input[0]["role"] != "developer" {
		t.Fatalf("system role was not converted: %#v", input[0])
	}
}

func TestParseCodexResponsesSSE(t *testing.T) {
	input := "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hel\"}\n\nevent: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\"}\n\n"
	var out strings.Builder
	if err := ParseCodexResponsesSSE(strings.NewReader(input), func(piece string) error { out.WriteString(piece); return nil }); err != nil {
		t.Fatal(err)
	}
	if out.String() != "hello" {
		t.Fatalf("unexpected codex text %q", out.String())
	}
}

func TestParseCodexCompletedFallback(t *testing.T) {
	input := "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"fallback text\"}]}]}}\n\n"
	var out strings.Builder
	if err := ParseCodexResponsesSSE(strings.NewReader(input), func(piece string) error { out.WriteString(piece); return nil }); err != nil {
		t.Fatal(err)
	}
	if out.String() != "fallback text" {
		t.Fatalf("unexpected codex fallback %q", out.String())
	}
}

func TestParseCodexQuotaHeadersAndCooldown(t *testing.T) {
	reset := time.Now().UTC().Add(2 * time.Hour).Truncate(time.Second)
	headers := http.Header{}
	headers.Set("x-codex-5h-usage", "100")
	headers.Set("x-codex-5h-limit", "100")
	headers.Set("x-codex-5h-reset-at", reset.Format(time.RFC3339))
	headers.Set("x-codex-7d-usage", "50")
	headers.Set("x-codex-7d-limit", "100")
	quota := ParseCodexQuotaHeaders(headers)
	if quota.FiveHour.Usage != 100 || quota.FiveHour.Limit != 100 || !quota.FiveHour.ResetAt.Equal(reset) {
		t.Fatalf("unexpected quota: %#v", quota)
	}
	if cooldown := CodexCooldown(quota, reset.Add(-30*time.Minute)); cooldown != 30*time.Minute {
		t.Fatalf("unexpected cooldown: %s", cooldown)
	}
}

func zeroCred() store.ProviderConnection {
	return store.ProviderConnection{ProviderSpecificData: map[string]any{}}
}

func buildHeader(name, value string) []byte {
	var b bytes.Buffer
	b.WriteByte(byte(len(name)))
	b.WriteString(name)
	b.WriteByte(7)
	_ = binary.Write(&b, binary.BigEndian, uint16(len(value)))
	b.WriteString(value)
	return b.Bytes()
}
