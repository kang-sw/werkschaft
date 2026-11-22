---
title: "프로바이더 인증 공통 레이어: OpenRouter API 키와 OpenAI Sign in with ChatGPT"
parent: 261114-epic-ws-common-session-foundation
related:
  261122-feat-ws-builtin-agent-harness: "first consumer; the harness gets provider credentials only through this layer"
  261122-feat-ws-web-tools: "consumer; web fetch/search configuration uses this layer's providers"
  261112-research-ws-ade-desktop-shell-and-rename: "zero-install built-in agent; (495)=a (bundled Pi) is superseded by the builtin harness, see the epic"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 78ec6832f92dea6f
sage-review-completeness-reviewed: 78ec6832f92dea6f
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
   the same interface. Storage type 5 (plain file) of
   `ai-docs/manuals/performance.md` `## Storage Types`, not the type-2 SQLite
   store: the CLI subcommand of Phase 1 writes the file while no daemon may be
   running, and keeping secrets out of the shared SQLite state databases keeps
   them out of anything that copies those databases (diagnostics, board or
   state exports) and lets one file carry the restrictive mode/ACL. Writes are
   rare (set, remove, refresh rotation). Rejected: a row in an existing SQLite
   store.
6. **The daemon owns refresh; the harness fetches over its authenticated
   channel ((5015), technical).** Daemon-owned refresh over per-process
   refresh - OAuth refresh tokens can rotate on use, so two processes
   refreshing concurrently would invalidate each other. A harness process asks
   the daemon for a current access token over its existing per-session bearer
   channel through an internal route that is not an MCP tool. While the daemon
   is down (a swap), the harness keeps its last token and retries after
   reconnect.
7. **One live writer: the daemon (technical, from sage design review).** While
   a daemon runs, it is the only process that writes the credential file; the
   CLI subcommand sends set/remove to the running daemon over the local
   channel its other subcommands use, so the daemon's memory and the file
   never diverge. With no daemon running, the CLI writes the file directly
   while holding the daemon's single-instance lock, so no daemon starts
   mid-write; the next daemon reads it at its first credential use. Rejected: a CLI that writes
   the file beside a running daemon (the daemon would keep a removed key or
   overwrite a new one on its next refresh rewrite).
8. **No environment-variable credential source (technical, from sage design
   review).** The daemon's environment is inherited by every hosted session
   and by hosted exec subprocesses, so an `OPENROUTER_API_KEY` read from it
   would reach model-run tools. Keys enter only through the CLI subcommand or
   the dashboard (Phase 3). Rejected: an env fallback scrubbed from child
   environments - one more leak path to keep closed for a convenience the
   CLI already gives.
9. **Compatibility tier T0 ((5012)).** New file, new internal route, new
   config keys; old binaries never read them.

## Constraints

- `ai-docs/manuals/performance.md`: the credential file is read once into
  daemon memory at first use and rewritten only when a credential changes
  (set, remove, refresh rotation); no file read on any interactive path.
  Storage type 5 per Decision 5.
- `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/config-settings-ui.md` (Phase 3 settings rows).
- Never log, trace, or capture (audit ring, transcripts, dashboard live feed)
  a token, key, or authorization header. Tests assert redaction.
- The app name presented to OpenAI is the product's real name.
- Out of scope: OS secure stores, Anthropic/Google OAuth, account pooling, a
  paid or remotely hosted SIWC deployment.

## Prior Decisions

- 261122-feat-ws-builtin-agent-harness (2026-11-22, Decisions): "Credentials only from the provider auth layer. The harness fetches tokens per `261122-feat-ws-provider-auth-layer` Decision 6 and never reads the credential file itself." — bearing: supports
- 261122-feat-ws-web-tools (2026-11-22, Decisions): "Search is the provider's server tool; fetch is a ws MCP tool ((5017), technical). `ws/web.search` is realized by attaching the provider's server-side search tool to the builtin harness's model requests" — bearing: supports
- be7e3a76 (2026-11-22, commit): "External research (official docs, 2026-11-22): OpenRouter and OpenAI Sign in with ChatGPT both accept stateless Responses, so one wire adapter serves both; SIWC Terms forbid exposing the token as a general API" — bearing: supports
- 261117-feat-ws-harness-capability-catalog (2026-11-17, commit 4e8add8d): "An older daemon rejoining an HTTP lead answers its ws calls 401 until the process restarts: no data loss, functional degradation" — bearing: constrains
- 261118-feat-ws-agy-tool-names-and-worker-role (2026-11-18, commit b3e2e5e6): "Worker tokens never enter the record and are revoked on stop/shutdown; lead holder behavior stays unchanged." — bearing: constrains
- 261113-feat-ws-dashboard-rail-usage-gutter (2026-11-13, Decisions): "Codex/GPT data is a live query ((133)=b, (137)=c): primary source is a short-lived `codex app-server` process ... fallback is a `wham/usage` HTTP query with Pi's stored `openai-codex`" — bearing: constrains
- 261119-feat-ws-audit-policy-event-envelope (2026-11-22, Resolution): "Phase 1 landed in agents-plugin-tool/internal/wsaudit: v1 harness-neutral Envelope, 11-kind set, pure model-seen compaction cut, daemon-hosted capture policy gate, and Sink interface" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | the ticket names no code path; its phases need a new credential package, a ws-mcp CLI subcommand (agents-plugin-tool/cmd/ws-mcp/main.go dispatches serve, dashboard, daemon-start, mailbox), an internal daemon route, and dashboard settings rows |
| scope.surface | cross-module | new provider registry interface, CLI subcommand, internal token route, config keys and dashboard rows (Phase 1-3) |
| scope.new_public_symbol | yes | provider registry Go interface and typed not-configured / consent-required errors (Phase 1); new CLI subcommand |
| scope.new_type_contract | yes | credential-for-provider interface with typed errors, the credential file shape, and the token route request/response (Phase 1); searched agents-plugin-tool and agents-plugin-pi/src at HEAD: no openrouter or provider-registry code exists |
| scope.test_surface | new-files | the ticket lists new unit, fixture and fake-authorization-server tests; no existing provider-auth tests (git grep openrouter at HEAD finds only Pi code and an unrelated wsagent/support_test.go mention) |
| complexity.reuse_points | unconfirmed | per-session bearer channel exists (agents-plugin-tool/internal/wsagent/mcp_session.go, EnvMCPSessionToken) and wsstate.CacheRoot gives a per-user root (internal/wsstate/paths.go#L91), but the route's builtin-harness session check depends on the unlanded 261122-feat-ws-builtin-agent-harness |
| complexity.side_effect_risk | high | new secret store and daemon route; refresh rotation is daemon-global and a leak reaches logs, audit ring, transcripts, dashboard feed |
| risk.correctness | high | OAuth refresh-token rotation, atomic replace, revoked refresh, consent state, and a Windows ACL path |
| risk.fit | moderate | credential file is storage type 5 per Decision 5, a deliberate exception to the type-2 SQLite default in ai-docs/manuals/performance.md |
| risk.test | moderate | Phase 2 needs a fake authorization server and an owner-run live sign-in; Phase 3 needs a mockup capture comparison; Windows ACL cannot be tested on this host |
| risk.security_or_contract | high | holds user credentials under SIWC Terms constraints (local storage, no token exposure, express background consent) and adds a route that must refuse non-harness bearers |

## Phases

### Phase 1: Credential store, provider registry, OpenRouter API key

- Provider registry with stable IDs (`openrouter`, later `openai`) and a Go
  interface consumers call: current credential for a provider, or a typed
  "not configured" / "consent required" error.
- Credential file per Decision 5; atomic replace on write.
- OpenRouter key entry without UI: a CLI subcommand of the ws binary to set,
  show (masked), and remove a key, per Decision 7's single-writer rule.
- The internal token route of Decision 6, authenticated by the per-session
  bearer, refusing callers that are not builtin-harness sessions.
- Verification: unit tests for registry, file mode/ACL, atomic write,
  redaction, and the route's refusal of non-harness bearers; a fake-provider
  test that the harness-side client obtains the key through the route; a
  test that a CLI set/remove against a running daemon is served by the
  daemon and visible to the next token request; a test that the offline CLI
  write takes the daemon lock.

### Result (430ecfc61) - 2026-11-22

Landed on `impl/develop/shack-coma-jimmy` (`b5d2538ac..430ecfc61`):

- `internal/wscred`:
  - Provider registry (`openrouter`, kind `api_key`) and `Mask`.
  - `Secret`, which redacts every fmt verb and JSON/Text form.
  - `Source` with typed `ErrNotConfigured` / `ErrConsentRequired` / `ErrUnknownProvider` errors.
  - `Store` for `<cache root>/auth/credentials.json`:
    - Reads the file lazily once and serves later reads from memory.
    - Writes by atomic replace: file first, then memory.
    - Keeps members it does not know, and never rewrites a newer `schema_version`.
    - Owner-only: 0600 in a 0700 directory, or a protected owner-only DACL on Windows.
    - A failed load is kept until the file changes.
  - `ServeToken`, plus the harness-side `RouteClient`. The client keeps the last credential through a drain, an unreachable daemon, or a successor's 401, and forgets it on a refusal about the credential itself.
- `internal/wsdashboard`:
  - `POST /auth/provider/token`, admitted by the per-session bearer, loopback only, no Origin.
  - `POST /auth/provider/change`, admitted by the endpoint bearer.
  - `ApplyCredentialChange` (Decision 7) takes the daemon lock with `TryLock` and writes the file offline while holding it. Otherwise it checks the recorded daemon's identity (`Health`) and sends the change to that daemon. An older daemon is named, not waited out.
  - The file browser's deny set covers the credential directory and file.
- `internal/wsagent`:
  - `BackendRegistration.ProviderCredentials`.
  - `Runner.ResolveCredentialToken`, which does not mark the MCP session connected.
- `cmd/ws-mcp`:
  - `credential set|show|remove [--format json]`. The key comes only from the first stdin line, never argv.
  - Inside the daemon the command applies to the daemon's one store.
  - The launchers forward stdin for `credential set` and prompt with `getpass` on a terminal. All three launcher copies are kept identical.
  - Manual: a "Provider credential store" section in `ai-docs/manuals/ws-mcp.md`.

Decisions made here:

- The token route is gated on a registration capability flag (`ProviderCredentials`) that no current backend sets. It is not a harness-name branch, because no builtin-harness session kind exists yet. Tests refuse every current backend and admit a test-only registration.
- The route derives `Request` from the runner's own record of the session, never from the caller. `AgentSession` is always true, and `Background = !Lead`; a missing record counts as background.
- A 401 keeps the kept credential, as Decision 6 asks for a swap: a successor answers 401 before it restores the session's bearer.
- The key reaches the CLI only through stdin. A directly run binary reads it without echo suppression; through the launcher, `getpass` gives a no-echo prompt.
- Compatibility T0: an older daemon answers the change route with `login_required`, which is reported as "does not take credential changes".

Verification:

- Full suites of `internal/wscred`, `internal/wsagent` and `internal/wsdashboard` pass.
- `cmd/ws-mcp` credential tests pass. The package's three other failures (`TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild`, `TestProxyWhiteboardLinkHasNoLoginCode`, `TestWhiteboardConstructionBeforeDashboardBind`) fail identically at `b5d2538ac`.
- `go build ./...`, and `go vet` with and without `GOOS=windows`, are clean.
- The launcher unittest passes (64 tests).
- The Windows DACL test (`private_file_windows_test.go`) is compile-checked only; it runs on a Windows host.
- One lite review pass: no Critical or Important findings. Its Minor findings were fixed in `430ecfc61`, except the no-echo prompt for a directly run binary.
- Coverage of the phase's verification list:
  - Registry, file mode, atomic write and redaction: `store_test.go`, `private_file_unix_test.go`.
  - Route refusal of non-admitted bearers: `TestProviderTokenRouteAdmitsOnlyPermittedSessions`, `TestCredentialTokenFollowsTheRegistrationCapability`.
  - Harness client obtains the key through the route: `TestHarnessClientObtainsTheKeyThroughTheRoute`.
  - CLI against a running daemon is served by it and seen by the next token request: `TestCredentialChangeAgainstRunningDaemonIsServedByTheDaemon`, `TestCredentialCLIDirectWithARunningDaemon`.
  - Offline write takes the daemon lock: `TestOfflineCredentialChangeTakesTheDaemonLock`.

#### Edition (513e0dace) - 2026-11-22

- Trust boundary: the token route and ws tool outputs never return the secret; processes running as the same OS user are trusted, since the file's mode or ACL protects against other users only and a same-user process (a hosted exec job) can read the file directly.
- Follow-up fixes from an independent review (`a1b944bfc..513e0dace`):
  - The daemon closes its store to changes, after letting the change in flight finish, before it releases its lock.
  - A directly run binary now reads the key with echo off on a terminal, which supersedes the Result's no-echo note.
  - An unrecognized argument is never echoed.
  - The launcher forwards only the first stdin line for `credential set`.

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
