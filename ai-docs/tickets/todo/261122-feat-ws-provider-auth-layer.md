---
title: "프로바이더 인증 공통 레이어: OpenRouter API 키와 OpenAI Sign in with ChatGPT"
parent: 261114-epic-ws-common-session-foundation
related:
  261122-feat-ws-builtin-agent-harness: "first consumer; the harness gets provider credentials only through this layer"
  261122-feat-ws-web-tools: "consumer; web fetch/search configuration uses this layer's providers"
  261112-research-ws-ade-desktop-shell-and-rename: "zero-install built-in agent; (495)=a (bundled Pi) is superseded by the builtin harness, see the epic"
---

# Provider authentication layer

## Background

Owner direction (2026-11-22, board items (5001) note and (5002)=a): drop Pi
support and build two pieces of our own instead of fitting Pi beneath the
common hosted interface: (1) a shared provider-authentication layer and (2) a
builtin Go agent harness (`261122-feat-ws-builtin-agent-harness`). The owner's
rationale, verbatim excerpt: "(1) 은 나중에 decision이나 image, video 생성
등에서도 여기저기 써먹을 수 있을 거고(주로 openrouter가 1차 타겟이겠지만)".

Verified external facts (read-only research, 2026-11-22, official sources):

- OpenRouter authenticates with a bearer API key on
  `https://openrouter.ai/api/v1`; text (`/chat/completions`, `/responses`,
  `/messages`), image (`/images`) and video (`/videos`, async jobs) endpoints
  share that key. <https://openrouter.ai/docs/guides/features/compatibility#endpoint-compatibility-matrix>.
  `/api/v1/key` reports free-tier remaining and credit/budget; 401 is bad
  auth, 403 an exhausted budget, 429 a rate limit with `Retry-After` when
  given. <https://openrouter.ai/docs/api_reference/limits#rate-limits>.
- OpenAI "Sign in with ChatGPT" (SIWC) publicly supports eligible
  open-source and local apps (published 2026-09-28): a user/workspace-bound
  dynamically registered client carrying the app's own name and a stable
  opaque host ID, browser loopback PKCE, a plan-specific opt-in scope, and
  refresh. The token is used against the public
  `https://api.openai.com/v1/responses` (`store: false`, `stream: true`) and
  `GET /v1/models` returns the account's model catalog.
  <https://developers.openai.com/cookbook/articles/sign-in-with-chatgpt>,
  <https://developers.openai.com/siwc/token-sharing-open-source/sign-in>,
  <https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference>.
- SIWC Terms (effective 2026-11-04, sections 1, 3.1, 7):
  <https://openai.com/policies/sign-in-with-chatgpt-terms/>. Truthful app
  name; persistent tokens stored locally under the user's control, no remote
  or managed credential store; inference only from a local or exclusively
  user-controlled runtime for the authenticated user; express authorization
  for background use; no pooled, rotated, or shared use; no reuse as a
  generic API for other tools; SIWC is never a paid unlock. Paid or remotely
  hosted apps use a separate interest form.
- The Codex CLI's own ChatGPT login targets `chatgpt.com/backend-api/codex`
  with Codex's client ID; that is observed legacy behavior, not a public
  integration contract.
- Anthropic prohibits third-party use of Claude.ai subscription login and
  routing consumer subscription credentials
  (<https://code.claude.com/docs/en/legal-and-compliance#authentication-and-credential-use>);
  Google Antigravity terms section 6 forbid third-party access with
  Antigravity OAuth (<https://antigravity.google/terms>).

## Decisions

1. **One shared layer owns provider credentials (owner (5002)=a).** Consumers
   (the builtin harness, web tools, later decision/image/video features) ask
   it for a credential by provider ID; none reads tokens or keys on its own.
   Rejected: per-feature credential handling.
2. **First providers: OpenRouter by API key, then OpenAI by SIWC (owner
   (5001) note, (5002)=a).** Other API-key providers fit the same registry
   later without a contract change. Rejected: Anthropic and Google
   subscription OAuth - both vendors prohibit it (Background); the Codex
   legacy client ID and backend endpoint - not a public contract.
3. **The SIWC token serves only the user's own agent sessions ((5007),
   auto-confirmed from the SIWC Terms).** Only the builtin harness running a
   session for the signed-in user may obtain it. General-purpose consumers
   (web, decision, image, video) use API-key providers only. No MCP tool,
   HTTP route, or config surface returns the token to a model or another
   tool. Rejected: one OpenAI credential shared by every feature.
4. **Express consent before background SIWC use ((5007)).** The first time a
   worker or other non-interactive session would use the SIWC credential, the
   dashboard asks the user once; the answer is stored with the credential and
   can be revoked. A session without consent fails with an actionable error,
   never silently.
5. **Local, user-controlled storage ((5008), auto-confirmed from the SIWC
   Terms and the existing harness convention of a per-user `auth.json`).**
   Credentials live in one file in the ws per-user state directory, never in a
   project tree, never synced or uploaded. POSIX mode 0600; on Windows the
   file ACL is restricted to the current user. Rejected for now: OS secure
   stores (Keychain, DPAPI, libsecret) - more platform code and permission
   prompts for no gain the Terms require; a later ticket may add them behind
   the same interface.
6. **The daemon owns refresh; the harness fetches over its authenticated
   channel ((5015), technical).** Daemon-owned refresh over per-process
   refresh - OAuth refresh tokens can rotate on use, so two processes
   refreshing concurrently would invalidate each other. A harness process asks
   the daemon for a current access token over its existing per-session bearer
   channel through an internal route that is not an MCP tool. While the daemon
   is down (a swap), the harness keeps its last token and retries after
   reconnect.
7. **Compatibility tier T0 ((5012)).** New file, new internal route, new
   config keys; old binaries never read them.

## Constraints

- `ai-docs/manuals/performance.md`: the credential file is read once into
  daemon memory at first use and rewritten only when a credential changes
  (set, remove, refresh rotation); no file read on any interactive path. Name
  its storage type from that manual's `## Storage Types`.
- `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/config-settings-ui.md` (Phase 3 settings rows).
- Never log, trace, or capture (audit ring, transcripts, dashboard live feed)
  a token, key, or authorization header. Tests assert redaction.
- The app name presented to OpenAI is the product's real name.
- Out of scope: OS secure stores, Anthropic/Google OAuth, account pooling, a
  paid or remotely hosted SIWC deployment.

## Phases

### Phase 1: Credential store, provider registry, OpenRouter API key

- Provider registry with stable IDs (`openrouter`, later `openai`) and a Go
  interface consumers call: current credential for a provider, or a typed
  "not configured" / "consent required" error.
- Credential file per Decision 5; atomic replace on write.
- OpenRouter key entry without UI: a CLI subcommand of the ws binary to set,
  show (masked), and remove a key; an `OPENROUTER_API_KEY` environment
  fallback read at daemon start.
- The internal token route of Decision 6, authenticated by the per-session
  bearer, refusing callers that are not builtin-harness sessions.
- Verification: unit tests for registry, file mode/ACL, atomic write,
  redaction, and the route's refusal of non-harness bearers; a fake-provider
  test that the harness-side client obtains the key through the route.

### Phase 2: OpenAI Sign in with ChatGPT

- SIWC flow per the official docs current at implementation: dynamic client
  registration, browser loopback PKCE, token exchange, refresh, and the
  account model catalog. Record the doc revision used in the Result.
- Consent record of Decision 4.
- Verification: fixture tests against a fake authorization server for
  registration, exchange, refresh rotation, revoked refresh, and consent
  refusal; one owner-run live sign-in recorded in the Result (no token in the
  record).

### Phase 3: Dashboard sign-in and key management

- Dashboard settings rows to add/remove the OpenRouter key and to sign in to
  or out of OpenAI, plus the background-consent prompt. A text mockup under
  `ai-docs/assets/mockups/261122/provider-auth/` is approved by the owner
  before implementation; verification compares a capture of the result with
  it side by side.
