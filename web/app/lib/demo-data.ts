export const providers = [
  {
    id: "codex",
    name: "OpenAI Codex",
    format: "openai-responses",
    authType: "oauth",
    baseUrl: "https://chatgpt.com/backend-api/codex/responses",
    models: [
      "gpt-5.5",
      "gpt-5.5-xhigh",
      "gpt-5.5-high",
      "gpt-5.5-medium",
      "gpt-5.4",
      "gpt-5.3-codex",
    ],
  },
  {
    id: "antigravity",
    name: "Antigravity",
    format: "antigravity",
    authType: "oauth",
    baseUrl: "https://daily-cloudcode-pa.googleapis.com",
    models: [
      "gemini-3.5-flash-preview",
      "gemini-3-pro-preview",
      "gemini-2.5-pro",
      "claude-sonnet-4-6",
    ],
  },
  {
    id: "kiro",
    name: "Kiro AI",
    format: "kiro",
    authType: "oauth",
    baseUrl:
      "https://codewhisperer.us-east-1.amazonaws.com/generateAssistantResponse",
    models: [
      "auto-kiro",
      "claude-opus-4.8",
      "claude-sonnet-4.6",
      "deepseek-3.2",
    ],
  },
  {
    id: "trae",
    name: "Trae",
    format: "openai",
    authType: "oauth",
    baseUrl: "https://core-normal.trae.ai/api/remote/v1",
    models: ["auto", "work", "gemini-3.1-pro", "gpt-5.4", "minimax-m3"],
  },
]

export const initialAccounts = [
  {
    id: "acc-codex-main",
    provider: "codex",
    name: "Codex Primary",
    email: "ops@example.com",
    priority: 0,
    model: "gpt-5.5",
    status: "Active",
  },
  {
    id: "acc-trae-work",
    provider: "trae",
    name: "Trae Work",
    email: "team@example.com",
    priority: 1,
    model: "work",
    status: "Active",
  },
]

export const initialKeys = [
  {
    id: "key-live",
    name: "Production gateway",
    key: "or_live_sk_codex_demo_7f2a",
    scope: "codex/gpt-5.5",
    limit: "120 rpm",
    status: "Active",
  },
]

export type Provider = (typeof providers)[number]
export type ProviderAccount = (typeof initialAccounts)[number]
export type ApiKey = (typeof initialKeys)[number]

export const modelOptions = providers.flatMap((provider) =>
  provider.models.map((model) => `${provider.id}/${model}`)
)
