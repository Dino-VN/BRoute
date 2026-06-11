package providers

type Model struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
}

type Provider struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	Format     string  `json:"format"`
	AuthType   string  `json:"authType"`
	AuthHeader string  `json:"authHeader"`
	BaseURL    string  `json:"baseUrl"`
	LoginURL   string  `json:"loginUrl,omitempty"`
	LoginFlow  string  `json:"loginFlow,omitempty"`
	Models     []Model `json:"models"`
}

var Registry = map[string]Provider{
	"antigravity": {
		ID: "antigravity", Name: "Antigravity", Format: "antigravity", AuthType: "oauth", AuthHeader: "bearer",
		BaseURL:   "https://daily-cloudcode-pa.googleapis.com",
		LoginURL:  "https://accounts.google.com/o/oauth2/v2/auth",
		LoginFlow: "browser",
		Models:    models("antigravity", []string{"gemini-3.5-flash-preview", "gemini-3-flash-agent", "gemini-3-pro-preview", "gemini-3.1-pro-high", "gemini-3.1-pro-low", "gemini-3-flash-preview", "gemini-2.5-pro", "gemini-2.5-flash", "claude-sonnet-4-6", "claude-opus-4-6-thinking"}),
	},
	"codex": {
		ID: "codex", Name: "OpenAI Codex", Format: "openai-responses", AuthType: "oauth", AuthHeader: "bearer",
		BaseURL:   "https://chatgpt.com/backend-api/codex/responses",
		LoginURL:  "https://auth.openai.com/oauth/authorize",
		LoginFlow: "browser",
		Models:    models("codex", []string{"gpt-5.5", "gpt-5.4", "gpt-5.4-mini", "gpt-5.3-codex-spark", "gpt-5.3-codex", "gpt-5.2"}),
	},
	"kiro": {
		ID: "kiro", Name: "Kiro AI", Format: "kiro", AuthType: "oauth", AuthHeader: "bearer",
		BaseURL:   "https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
		LoginURL:  "https://prod.us-east-1.auth.desktop.kiro.dev/login",
		LoginFlow: "browser",
		Models:    models("kiro", []string{"auto-kiro", "claude-opus-4.8", "claude-opus-4.7", "claude-opus-4.6", "claude-sonnet-4.6", "claude-sonnet-4.5", "claude-haiku-4.5", "deepseek-3.2", "minimax-m2.5", "glm-5", "qwen3-coder-next"}),
	},
	"trae": {
		ID: "trae", Name: "Trae", Format: "openai", AuthType: "oauth", AuthHeader: "bearer",
		BaseURL:   "https://core-normal.trae.ai/api/remote/v1",
		LoginURL:  "https://www.trae.ai/authorization",
		LoginFlow: "browser",
		Models:    models("trae", []string{"auto", "work", "gemini-3.1-pro", "gemini-3-flash-solo", "minimax-m3", "minimax-m2.7", "kimi-k2.5", "gpt-5.4", "gpt-5.2"}),
	},
}

func models(provider string, ids []string) []Model {
	out := make([]Model, 0, len(ids))
	for _, id := range ids {
		out = append(out, Model{ID: id, Name: id, Provider: provider})
	}
	return out
}

func List() []Provider {
	ids := []string{"antigravity", "codex", "kiro", "trae"}
	out := make([]Provider, 0, len(ids))
	for _, id := range ids {
		out = append(out, Registry[id])
	}
	return out
}

func FindByModel(model string) (Provider, string, bool) {
	if p, ok := Registry[model]; ok {
		return p, defaultModel(p), true
	}
	for _, p := range Registry {
		for _, m := range p.Models {
			if model == m.ID || model == p.ID+"/"+m.ID {
				return p, m.ID, true
			}
		}
	}
	return Provider{}, "", false
}

func defaultModel(p Provider) string {
	if len(p.Models) == 0 {
		return ""
	}
	return p.Models[0].ID
}
