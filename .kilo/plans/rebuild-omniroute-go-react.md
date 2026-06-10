# Plan: Rebuild OmniRoute Backend In Go With React UI

## Goal

Recreate the current `OmniRoute/` app in the current workspace with a Go backend replacing Next.js server/API behavior, while preserving the existing React UI behavior as closely as practical. Prioritize functional support for these providers before broader parity:

- Antigravity, excluding Antigravity CLI-specific surface
- OpenAI Codex
- Kiro AI
- Trae

## Key Findings From Existing App

- Current app is a Next.js 16 App Router project under `OmniRoute/` with React 19 and Tailwind v4.
- Login UX is implemented in `src/app/login/page.tsx` and calls:
  - `GET /api/settings/require-login`
  - `POST /api/auth/login`
  - `GET /api/auth/status`
  - `POST /api/auth/logout`
- Login behavior uses an `auth_token` JWT cookie, 30 day expiry, `httpOnly`, `sameSite=lax`, optional secure cookie via HTTPS or `AUTH_COOKIE_SECURE=true`, and requires `JWT_SECRET`.
- Primary OpenAI-compatible chat entrypoint is `POST /api/v1/chat/completions`, delegating to the open-sse pipeline.
- The prioritized providers already have specialized TS executors:
  - `open-sse/executors/antigravity.ts`
  - `open-sse/executors/codex.ts`
  - `open-sse/executors/kiro.ts`
  - `open-sse/executors/trae.ts`
- Provider registry metadata lives mainly in `open-sse/config/providerRegistry.ts` and `src/shared/constants/providers.ts`.
- Kiro requires AWS EventStream parsing and conversion to OpenAI-style SSE.
- Trae requires a two-step session flow: `POST /chat_sessions`, then `GET /chat_sessions/{id}/events?...` SSE.
- Codex is extensive. A first Go pass should implement HTTP Responses/chat behavior and quota headers; WebSocket/wreq parity can follow after HTTP parity.
- Antigravity is extensive. A first Go pass should implement the normal Antigravity upstream envelope/request/stream conversion, token use, project/model resolution, and skip Antigravity CLI-specific routes or docs per user request.

## Proposed Target Structure

Create a new Go/React implementation at the workspace root, keeping `OmniRoute/` as the read-only reference during implementation.

- `go.mod`
- `cmd/omniroute/main.go`
- `internal/server/` for routing, middleware, static React serving, CORS, health
- `internal/auth/` for password hashing, JWT cookie sessions, login guard
- `internal/config/` for env and data directory resolution
- `internal/store/` for SQLite persistence and migrations
- `internal/providers/` for provider registry and credential types
- `internal/executors/` for provider-specific upstream execution
- `internal/translator/` for OpenAI chat/responses normalization and provider request transforms
- `internal/sse/` for OpenAI-compatible SSE framing helpers
- `internal/httpclient/` for timeouts, retries, proxy/header behavior
- `web/` for React app, likely Vite, reusing UI concepts/styles from the Next app

## Implementation Phases

### Phase 1: Go Backend Foundation

- Initialize Go module and application entrypoint.
- Add HTTP router using the Go standard library or `chi` for clear route grouping.
- Add env/config loader for at least:
  - `PORT` or default existing OmniRoute port if detected during implementation
  - `DATA_DIR`
  - `JWT_SECRET`
  - `AUTH_COOKIE_SECURE`
  - provider timeouts such as `TRAE_STREAM_TIMEOUT_MS`
- Add SQLite store with migrations for minimum required tables:
  - settings
  - provider_connections
  - api_keys if OpenAI-compatible API key auth is enabled
  - usage/call logs for basic parity
  - audit events for login/logout and provider requests
- Add JSON response helpers and sanitized error responses.
- Add CORS handling matching existing API behavior where relevant.

### Phase 2: Login And Management Auth Parity

Implement these routes with behavior matching the current frontend contract:

- `GET /api/settings/require-login`
  - Return `{ requireLogin, hasPassword, setupComplete, nodeVersion, nodeCompatible }`.
  - Since backend is Go, map `nodeVersion` to a compatibility field that keeps the current login UI from showing a false Node error, or update React copy to a Go runtime equivalent while preserving layout.
- `POST /api/settings/require-login`
  - Allow unauthenticated writes only before password is configured.
  - Store bcrypt password hash.
  - Toggle `requireLogin` and `setupComplete` consistently.
- `POST /api/auth/login`
  - Validate `{ password }`.
  - Fail fast if `JWT_SECRET` missing.
  - Verify bcrypt password.
  - Set `auth_token` HS256 JWT cookie with `{ authenticated: true }` and 30 day expiry.
  - Implement brute-force lockout comparable to current `checkLoginGuard`/`recordLoginFailure` behavior.
  - Return `needsSetup` on missing password.
- `GET /api/auth/status`
  - Read/verify `auth_token` and return `{ authenticated: boolean }`.
- `POST /api/auth/logout`
  - Delete `auth_token` and return `{ success: true }`.

### Phase 3: React UI Shell

- Use React/Vite in `web/` and build assets served by Go.
- Recreate the existing login screen first from `src/app/login/page.tsx`, preserving:
  - loading state
  - initial setup state
  - password-not-enabled state
  - normal sign-in state
  - forgot-password link layout
  - same API calls and response handling
- Port shared design primitives needed by login (`Button`, `Input`) and global CSS variables/classes closely enough to match visual behavior.
- Add dashboard shell routes required for navigation after login:
  - `/dashboard`
  - `/dashboard/onboarding`
  - `/forgot-password`
- For non-priority dashboard pages, provide either preserved layout stubs or progressively ported pages later. Do not block provider backend work on full dashboard parity.

### Phase 4: Provider Registry And Connection Management

Implement only the provider catalog entries and management APIs needed for the prioritized providers.

- Add registry entries for:
  - `antigravity`
  - `codex`
  - `kiro`
  - `trae`
- Include aliases where useful and already present:
  - Antigravity alias `agy` only if it does not reintroduce Antigravity CLI-specific UI/workflows.
- Port minimal model lists from `open-sse/config/providerRegistry.ts` for these providers.
- Implement provider connection CRUD endpoints compatible with the existing provider pages as needed:
  - list connections
  - create/update connection with auth type and credentials
  - delete connection
  - test connection
  - reveal/masked credential behavior if UI needs it
- Store provider-specific data as JSON.
- Encrypt secrets at rest if the existing app’s encryption contract can be reproduced within this pass; otherwise keep the store abstraction ready and document that encryption must be added before production use.

### Phase 5: OpenAI-Compatible API Surface

Implement core client-facing routes first:

- `POST /api/v1/chat/completions`
  - Accept OpenAI chat payloads.
  - Support streaming and non-streaming responses.
  - Resolve provider/model from request model IDs and/or configured connections.
  - Dispatch to the prioritized provider executor.
  - Return OpenAI-compatible JSON/SSE.
- `GET /api/v1/models`
  - Return prioritized provider models only.
- `POST /api/v1/responses`
  - Implement after chat completions if Codex parity requires it.
  - Convert Responses API requests to internal chat/executor format or implement provider-specific flow directly.

Defer embeddings/images/audio/video/search/MCP/A2A/compression/combos until the four providers are stable unless a provider requires a specific subset.

### Phase 6: Provider Executors In Go

#### Shared Executor Base

- Define an executor interface:
  - `Execute(ctx, model, body, stream, credentials, headers) (*http.Response or internal stream response, error)`
- Add shared logic for:
  - timeouts
  - retry/backoff
  - upstream extra header merge
  - sanitized errors
  - request/response logging
  - SSE framing
  - usage capture

#### Trae

Port behavior from `open-sse/executors/trae.ts`:

- Build `Authorization: Cloud-IDE-JWT <token>` and browser-like headers.
- Flatten OpenAI messages into Trae query JSON.
- Resolve mode:
  - `work`, `auto-work`, `solo-work` => work/auto
  - empty or `auto` => code/auto
  - otherwise code/manual with model name
- `POST {base}/chat_sessions` with initial message.
- `GET {base}/chat_sessions/{id}/events?reply_to_message_id=...` and parse SSE.
- Stream `plan_item` cumulative thoughts as OpenAI `chat.completion.chunk` deltas.
- Capture `token_usage`, `done`, and `error` events.
- Support non-streaming by collecting streamed text and returning `chat.completion` JSON.

#### Kiro

Port behavior from `open-sse/executors/kiro.ts`:

- Build AWS-style headers:
  - `Amz-Sdk-Request`
  - `Amz-Sdk-Invocation-Id`
  - `x-amzn-bedrock-cache-control`
  - `anthropic-beta`
  - `Authorization: Bearer <accessToken>`
- Implement OpenAI-to-Kiro request transform preserving allowed fields:
  - `conversationState`
  - `profileArn`
  - `inferenceConfig`
- Implement AWS EventStream binary frame parser:
  - total length / headers / payload / CRC handling
  - event type extraction
  - payload JSON parsing
- Convert assistant events, tool events, usage/metering events, and finish to OpenAI SSE chunks.
- Add token refresh hook only after basic bearer-token execution works.

#### Codex

Port a staged subset from `open-sse/executors/codex.ts`:

- Start with HTTP Responses endpoint, not WebSocket transport.
- Build Codex client headers and identity metadata equivalent enough for current upstream.
- Normalize model suffixes for effort and fast tier:
  - low/medium/high/xhigh/none
  - fast/priority mapping
- Preserve request transformations for Codex Responses API where needed:
  - instructions defaults
  - client metadata
  - session id normalization
  - sanitization of Responses input items
- Parse Codex quota headers:
  - `x-codex-5h-usage`
  - `x-codex-5h-limit`
  - `x-codex-5h-reset-at`
  - `x-codex-7d-usage`
  - `x-codex-7d-limit`
  - `x-codex-7d-reset-at`
- Implement cooldown decision logic equivalent to `getCodexDualWindowCooldownMs`.
- Defer WebSocket/wreq parity to a follow-up phase unless HTTP endpoint fails to satisfy required Codex clients.

#### Antigravity

Port the non-CLI Antigravity path from `open-sse/executors/antigravity.ts`:

- Build Antigravity request envelope:
  - project
  - model
  - userAgent
  - requestType
  - requestId
  - request
  - optional enabled credit types
- Implement project/model resolution and basic project bootstrap if required.
- Build OAuth bearer request headers using stored access token.
- Apply Antigravity user-agent/profile headers needed for upstream acceptance.
- Implement stream response parsing and conversion to OpenAI chunks.
- Implement textual tool-call parsing and tool call output compatibility.
- Implement upstream error shaping compatible with existing Antigravity errors.
- Implement 429/retry-after handling and credit-exhaustion cache.
- Exclude Antigravity CLI-specific UI/workflow/config generation by default.

### Phase 7: Provider UI Parity For Prioritized Providers

- Port provider listing and provider detail pages only for the four providers first.
- Preserve forms and credential guidance for:
  - Antigravity OAuth token/project data
  - Codex token/refresh/session/client metadata fields
  - Kiro access/refresh token fields
  - Trae Cloud-IDE-JWT and providerSpecificData fields
- Keep same route shape where feasible:
  - `/dashboard/providers`
  - `/dashboard/providers/[id]`
- Hide or disable non-priority providers and clearly indicate only the requested providers are included in this Go build.
- Port model test UI only if it is needed to validate connections.

### Phase 8: Verification

Backend tests:

- Auth login success/failure/missing secret/needs setup/lockout/logout/status.
- Provider registry returns exactly the four requested providers.
- Trae flattening, mode resolution, SSE parser, non-stream collect path.
- Kiro AWS EventStream parser with fixture frames.
- Codex quota header parsing and cooldown logic.
- Antigravity envelope building and textual tool-call parsing.

Integration tests with mocked upstreams:

- `POST /api/v1/chat/completions` streaming and non-streaming for each provider.
- Provider connection CRUD and credential masking.
- React login flow against Go API.

Manual verification:

- Run Go server.
- Open `/login`.
- Complete initial setup.
- Login/logout/status cycle.
- Add each provider connection.
- Send streaming chat request through each provider with a test credential.

## Explicit Deferrals

To keep the first implementation aligned with the user’s priority, defer these until the four providers work:

- All non-requested providers.
- Antigravity CLI-specific support.
- Electron app.
- MCP, A2A, ACP, cloud agents, plugins, memory, skills, tunnels, MITM proxy.
- Full compression, combo routing, auto fallback, pricing, gamification, analytics dashboards.
- Codex WebSocket transport unless required after testing HTTP parity.
- Full i18n migration; start with strings needed by login/provider pages.

## Risks And Mitigations

- Risk: “100% chức năng” for all OmniRoute is too large for a single rewrite.
  - Mitigation: Preserve contracts for login and the four providers first, then expand page/API parity incrementally.
- Risk: Provider upstreams are reverse-engineered and may reject slightly different headers/body shapes.
  - Mitigation: Port executor logic line-by-line from the TS implementation and add mocked upstream tests before live tests.
- Risk: Existing React code is Next-specific.
  - Mitigation: Recreate the needed pages in Vite React with matching route paths and API calls, rather than trying to run Next components directly.
- Risk: Secret storage parity may require encryption details from current SQLite implementation.
  - Mitigation: Isolate credential storage behind an interface and implement encryption before production use.

## First Implementation Checkpoint

The first checkpoint should be considered complete when:

- `go run ./cmd/omniroute` starts a server.
- `/login` visually matches the current login page closely and works against Go auth routes.
- Initial setup, login, status, logout all work.
- `/api/v1/models` returns only Antigravity, Codex, Kiro, and Trae models.
- `/api/v1/chat/completions` can route to mocked executor implementations for all four providers.
- Trae and Kiro have real parser/transform logic covered by unit tests.
