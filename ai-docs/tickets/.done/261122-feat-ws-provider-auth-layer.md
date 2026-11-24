---
title: "프로바이더 인증 공통 레이어: OpenRouter API 키와 OpenAI Sign in with ChatGPT"
parent: 261114-epic-ws-common-session-foundation
related:
  261122-feat-ws-builtin-agent-harness: "first consumer; the harness gets provider credentials only through this layer"
  261122-feat-ws-web-tools: "consumer; web fetch/search configuration uses this layer's providers"
  261112-research-ws-ade-desktop-shell-and-rename: "zero-install built-in agent; (495)=a (bundled Pi) is superseded by the builtin harness, see the epic"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 426b912f0778c416
sage-review-completeness-reviewed: 426b912f0778c416
completed: 2026-11-25
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
9. **Phase 1 and 2 compatibility tier T0 ((5012)).** New file, new internal route, new
   config keys; old binaries never read them.
10. **Phase 3 follows the approved mockup (owner (52), (54)).** The
    [v4 mockup](../../assets/mockups/261122/provider-auth/mockup.html), its
    [whiteboard fragment](../../assets/mockups/261122/provider-auth/board.html),
    and the [README](../../assets/mockups/261122/provider-auth/README.md) are
    the visual and behavioral reference. Follow the README's Settled items
    and close its backend gaps as amended by this decision and Decision 12.
    The dashboard-wide background-consent banner has allow, deny, close,
    and a link to the OpenAI row for local viewers. The account section uses
    one active OpenAI account and sign-out deletes its account record and
    consent locally. OpenRouter key verification uses the key-info endpoint.
    Prefix mismatch is a soft warning. Definitive 401/403 refuses the key,
    while network errors, 5xx, and 429 save it as unverified with a re-check
    action. Display the backend mask for saved keys. Credit display is out
    of scope. Deleting an endpoint or changing its base URL deletes the
    retired endpoint key.
    **LAN access ((5019), lead settlement).** Keep Settings, the account
    status route, and its mutations on the existing local dashboard access
    boundary. Require the dashboard's own Origin for browser mutations.
    When the consent banner appears on the authenticated LAN dashboard,
    keep the message and replace allow, deny, and the OpenAI link with a
    one-line hint to open the dashboard on the daemon's machine. This
    supersedes the non-loopback hint placement in mockup Settled 6.
    Rejected: provider rows inside the agents section (v1-v3 placement) and
    a LAN Account view, which adds a credential-state surface.

11. **A separate global-only 계정 (Account) settings section (owner (52),
    (53)=a).** Provider credentials and local endpoints live in their own
    settings section, beside the agents section, which keeps only the tier
    picker and links to the account section for an unusable provider. The
    section has no scope switcher; it shows a fixed "this whole machine"
    target (mockup Settled 11). Credentials stay one machine-wide file
    (Decision 5). Rejected: per-project credentials (machine-local, project
    over global) - the owner chose global only; repo-scoped credentials - the
    repo scope is the committed `.ws-workflow/config.json`, so a secret there
    would be published.
12. **Remove `agent.werk_endpoints` from agent discovery ((5018), announced
    default).** Hide it from `config.list` and the lead-tune flow per the
    owner's (52) direction. The tier provider description directs readers
    to the Account section. Keep the stored value and dashboard projection
    for the Account editor, alongside the owner CLI. Preserve direct
    `config.get` and lead-authorized `config.tune` set/reset with their
    0.52.0 behavior. This supersedes the direct-call withdrawal in mockup
    Settled 12 and backend gap 11.
    **Patch compatibility:** T1 for discovery, where an old client sees one
    fewer listed setting and that omission changes no data. T0 for stored
    values and direct calls, which continue through the existing read and
    write paths
    (`agents-plugin-tool/internal/mcp/config_manifest.go:553-567`,
    `server_dispatch.go:371-399,493-499`). Rejected for now: withdrawing
    direct MCP access is T3 and requires a separately approved minor
    boundary (`ai-docs/manuals/release-compatibility.md:72-91,129-145`).
    Credential keys, tokens, and consent stay in the credential store,
    outside config.

13. **Bind OpenRouter verification to the exact saved key ((5020), technical).**
    Store the verification result and timestamp with the key's fingerprint in
    the owner-only credential record. Keep the fingerprint inside the store.
    Treat a missing or mismatched fingerprint as unverified. Publish a re-check
    result only while the checked key record is still current. The 0.52.0
    baseline's `Store.Apply` replaces the key while preserving unknown fields
    (`agents-plugin-tool/internal/wscred/store.go:460-470,519-541`).
    On re-upgrade, unchanged keys retain their result and keys replaced by the
    old writer are unverified. Rejected: a standalone verified flag and
    timestamp, which the old writer carries onto a replacement key.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
- Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for `agents-plugin-tool/internal/mcp/config_registry.go`, `agents-plugin/config-manifest.json`, `agents-plugin/agy-config-manifest.json`, `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/config_routes.go`, `agents-plugin-tool/internal/wsdashboard/config_proposals.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)
- Convention: ai-docs/manuals/dashboard-style.md (declared for `agents-plugin-tool/internal/wsdashboard/assets/`, `agents-plugin-tool/internal/wswhiteboard/assets/`, `ai-docs/assets/mockups/`)

- `ai-docs/manuals/performance.md`: the credential file is read once into
  daemon memory at first use and rewritten only when a credential changes
  (set, remove, refresh rotation); no file read on any interactive path.
  Storage type 5 per Decision 5.
- `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/config-settings-ui.md` (Phase 3 settings rows).
- Phase 3 ships in a 0.52.x patch (owner (51)=b, recorded in `59fda372e`);
  affected storage and call paths must stay within T0-T2.
- Never log, trace, or capture (audit ring, transcripts, dashboard live feed)
  a token, key, or authorization header. Tests assert redaction.
- The app name presented to OpenAI is the product's real name.
- Out of scope: OS secure stores, Anthropic/Google OAuth, account pooling, a
  paid or remotely hosted SIWC deployment.

## Prior Decisions

- 261122-feat-ws-builtin-agent-harness (2026-11-22, Decisions): "**Providers are profiles over one Responses adapter (technical (16)).** A profile holds the endpoint, the auth source, the request fields the server refuses" — bearing: supports
- 992f0db8 (2026-11-24, commit): "Provider readiness still controls selectable models. Carry the ChatGPT plan remedy as a separate optional picker notice so an available OpenRouter or local model does not hide it." — bearing: constrains
- d211cd80 (2026-11-23, commit): "Legacy werk tiers resolve to OpenRouter with no write to their stored leaf. The shared tier writer accepts the explicit provider and refuses unlaunchable values before saving." — bearing: constrains
- 261122-feat-ws-web-tools (2026-11-22, Decisions): "**Builtin harness only (owner (5001) note).** Claude, Codex, and agy keep their native web capability; `ws/web.*` is not offered to them." — bearing: supports
- be7e3a76 (2026-11-22, commit): "External research (official docs, 2026-11-22): OpenRouter and OpenAI Sign in with ChatGPT both accept stateless Responses, so one wire adapter serves both" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Next unfinished phase is Phase 3 only: agents-plugin-tool/internal/wsdashboard, internal/wscred, internal/mcp/config_registry.go, cmd/ws-mcp, settings assets and i18n; approved reference: ai-docs/assets/mockups/261122/provider-auth/README.md |
| scope.surface | cross-module | Phase 3 adds browser credential routes, sign-in lifecycle and CLI endpoint commands; changes discovery while preserving direct MCP calls and dashboard projection (Decision 12) |
| scope.new_public_symbol | yes | New browser status / set / remove / consent and sign-in start / progress / cancel routes, plus credential endpoint set/remove CLI commands (Phase 3; mockup README backend gaps 1-3, 13) |
| scope.new_type_contract | yes | Browser-safe credential status, loopback-peer and pending-consent signals, sign-in lifecycle, verified/unverified key metadata, and registry visibility flag are Phase 3 additions; existing OpenAIAccountStatus is at agents-plugin-tool/internal/wscred/openai_store.go#L66-L87 |
| scope.test_surface | existing | Extend agents-plugin-tool/internal/wsdashboard/credential_routes_test.go, dashboard_settings_ui_catalog_test.go, internal/wscred/store_test.go and openai_test.go; Phase 3 also requires route secrecy, LAN banner and access checks, legacy MCP calls and four visual captures |
| complexity.reuse_points | confirmed | Read Store.Status / Apply (agents-plugin-tool/internal/wscred/store.go#L404-L465), OpenAI status and SignInOpenAI contract (openai_store.go#L66-L102), credentialStore interface (internal/wsdashboard/credential_routes.go#L42-L50), and URL-bound local provider keyID (internal/wscred/local_endpoints.go#L65-L74); Phases 1-2 and the builtin harness have landed |
| complexity.side_effect_risk | high | Browser mutations persist machine-wide credentials and consent; sign-out and endpoint URL changes delete secrets; daemon-side key validation makes outbound requests (Phase 3; Decisions 10-12) |
| risk.correctness | high | Sign-in cancellation and progress, definitive rejection versus unverified key saves, consent signaling, endpoint key cleanup and global-only scope must agree with approved mockup v4 as amended by Decisions 10 and 12 |
| risk.fit | moderate | New account section spans settings catalog, i18n, agents links and CLI; discovery filtering preserves direct MCP calls, dashboard config projection, and existing picker remedies (config_registry.go#L434-L447; prior commit 992f0db8) |
| risk.test | high | Phase 3 requires secret-free route bodies and LAN access checks plus catalog and direct-call checks and 1280/390 px dark/light captures; existing tests predate these browser flows |
| risk.security_or_contract | high | Browser access to credential mutations and loopback-only SIWC initiation must protect secrets and consent; owner limits Phase 3 to a 0.52.x patch, T0-T2 only; new metadata must preserve old-reader writes and new-reader recovery (store.go#L233-L286, L521-L534) |

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

### Result (39dbe7359) - 2026-11-24

Landed Phase 2 on `impl/track/harness/shack-coma-jimmy`. The
`internal/wscred` OpenAI provider uses system-browser loopback PKCE and
OpenID verification for dynamic registration and reauthorization.
The owner-only credential file stores the stable host ID and separate
registrations, including the active account and each account's explicit
background-consent answer. The in-process source gates builtin harness
sessions, serializes refresh rotation, clears a revoked grant while
retaining its registration, and returns the ordered list of visible models
from that account's catalog. The external harness token route refuses SIWC.
Dashboard sign-in and the consent prompt are Phase 3.

Compatibility stays T0. The Phase 1 reader at `ebcf1c4fc` keeps provider
entries as raw JSON in `decodeDocument` and rewrites only the selected
API-key entry in `Store.Apply`. This phase retains schema 1 and reloads the
OpenAI entry in `Store.useDocumentLocked`. Routine credential reads use
memory. The first registration writes the host ID before opening the browser
and writes the account after validation. A returning sign-in, changed
consent, or refresh rotation each uses one atomic file replacement.

OpenAI documentation revision used: public pages retrieved on 2026-11-24
(the pages display no revision stamp), with SIWC Terms effective 2026-11-04.
[Registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in),
[accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions),
[token reference](https://developers.openai.com/siwc/token-sharing-open-source/token-reference),
[refresh recovery](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery),
[models and inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference),
[OpenID configuration](https://auth.openai.com/.well-known/openid-configuration),
[SIWC Terms](https://openai.com/policies/sign-in-with-chatgpt-terms/).

Verification:

- `go test -race ./internal/wscred -count=1` and the credential tests in
  `./internal/wsdashboard` and `./cmd/ws-mcp` passed. The fixtures cover
  registration and returning sign-in, PKCE and identity refusals, per-account
  consent and revocation, rotation and persistence retry, revoked refresh,
  and the filtered catalog.
- `go test -timeout 45m ./internal/mcp` passed with generated workflow assets.
- The full `./internal/wsagent` and `./cmd/ws-mcp` suites passed. The
  CLI suite required generating the embedded workflow assets. The full
  dashboard suite hit two failures also reproduced on `ebcf1c4fc`
  with the same Node version: `TestAgentsIslandBehavior` (intermittent
  animation timestamp) and `TestBoardToolbarPlacementAndCounts`
  (fixture JSON parse).
- `go build ./...` and native and Windows-targeted `go vet` over
  `./internal/wscred ./internal/wsdashboard ./cmd/ws-mcp` passed.
- Independent lite review found no Critical or Important findings.

Owner-run live verification is pending. From the repository root, run this
with the system browser available. The temporary cache isolates the
credential file from the running daemon. A successful check prints
`Signed in to OpenAI with ChatGPT plan usage enabled.` Record the outcome
only.

```sh
env -i HOME="$HOME" PATH="$PATH" WS_CACHE_HOME="$(mktemp -d)" go -C agents-plugin-tool run ./cmd/ws-mcp credential sign-in openai
```

### Phase 3: Dashboard sign-in and key management

- Build the global-only 계정 settings section with OpenRouter, OpenAI and
  its consent sub-row, the structured local-endpoint editor, the
  dashboard-wide consent banner, and the agents-section links. Follow the
  [approved v4 mockup](../../assets/mockups/261122/provider-auth/mockup.html)
  and the amendments in Decisions 10 and 12.
- Close the
  [README backend gaps](../../assets/mockups/261122/provider-auth/README.md#backend-facts-the-ui-needs-that-do-not-exist-yet)
  with those amendments. Add local browser status, set, remove, and consent
  routes with secret-free responses, and sign-in start, progress, and
  cancel. Keep the loopback-peer flag in local status. Use existing
  dashboard viewer access to render the authenticated LAN consent banner
  independently of Account status. Sign-out deletes the account record
  and its consent. Add the pending consent signal and OpenRouter key
  verification on save and re-check, with exact-key metadata per
  Decision 13. Clean up endpoint keys on endpoint removal or base-URL
  change.
- Filter `agent.werk_endpoints` from config discovery and lead-tune,
  preserve direct legacy reads and lead-authorized set/reset, and keep the
  dashboard projection for the Account editor. Add the Account section to
  the catalog, manual, and i18n. Add CLI endpoint set/remove and point
  provider error texts at Account.
- Verification: exercise each new route, including redaction on success
  and error, LAN refusal of Account reads and mutations including sign-in
  start, and same-origin enforcement for local mutations. Verify the LAN
  banner shows the message and hint with credential actions confined to
  local viewers. Cover config discovery and lead-tune omission, continued
  direct get/set/reset behavior, and catalog drift. Run the 0.52.0
  old-writer/new-reader key replacement and concurrent re-check tests from
  Decision 13. Compare headless-browser captures of the Account section
  against the linked mockup side by side at 1280 px and 390 px in both
  dark and light, and capture the LAN banner hint per Decision 10.

### Result (9f1a0fe14) - 2026-11-25

Settings → Account is machine-wide. It manages OpenRouter verification,
OpenAI sign-in and plan consent, and named vLLM and Ollama endpoints. The
Werk tier links lead to Account while retaining the selected workflow
scope. The Settings catalog and manual include the section, and Account
strings cover all 11 supported locales.

The browser API uses the dashboard viewer cookie and requires a loopback
peer and Host. Mutations also require the dashboard Origin. Responses
carry masks and fixed error codes. OpenAI sign-in supports progress and
cancellation. Sign-out deletes the displayed active registration and its
consent locally per Decision 10. The Account row uses a generic ChatGPT
plan label and reports authorization and pause state. When a signed-in
account with plan access has no consent choice, a background refusal
opens the dashboard prompt. The local prompt supplies
allow, deny, close and an Account link. An authenticated LAN viewer gets
the message and on-machine hint.

OpenRouter verification checks the key-info endpoint on the daemon. A
rejected replacement leaves the saved key intact. Transient HTTP and
connection failures save it as unverified with a re-check action.
Endpoint writes share the global configuration lock and retire a key
when its endpoint is removed or changes API root. The daemon also
reconciles stale keys on startup and global reload. Standalone credential
status reads use a read-only endpoint catalog. The owner CLI uses
`credential endpoint set` and `credential endpoint remove` with the same
retirement logic.

**Patch compatibility**

- T1 for discovery. `internal/mcp/server_config.go:294-308` drops the
  endpoint key from the discovery view used by config.list and lead-tune.
  T0 for stored settings and direct calls. The old config.get/config.tune
  dispatch (`f74ed1b74:agents-plugin-tool/internal/mcp/server_dispatch.go:360,589`)
  and the existing endpoint JSON shape still serve direct reads and
  lead-authorized set/reset. The Account editor uses the global dashboard
  projection.
- T0 for the credential document. The 0.52.0-compatible writer at
  `f74ed1b74:agents-plugin-tool/internal/wscred/store.go:466,521-541`
  replaces known key fields and preserves raw unknown members. The new
  status reader at `internal/wscred/openrouter_verify.go:32-39` uses the
  private fingerprint to recover metadata for an unchanged key and show
  unverified after an old writer replaces it. Re-check publication also
  checks the record generation at lines 99-121. The file schema remains
  version 1.

**Verification**

- Full race suites passed for `internal/wscred`, `internal/wsconfig`,
  `internal/wsagent`, and `cmd/ws-mcp`. The dashboard Go suite passed
  1,542 tests with `TestBoardFrameBands` excluded. Its unsupported fake
  DOM selector fails on the unmodified `f74ed1b74` base too.
- The full MCP suite passed 1,528 tests. The i18n and package suites
  passed, including Settings catalog and locale coverage.
- `go build ./...`, the generated-package asset check, and scoped native and
  Windows cross-vet passed. The wsagent vet
  runs disabled the version analyzer because the existing
  `werk_lead_test.go:62` uses `testing.Context` in a Go 1.23 module.
- The isolated integration preview over `develop` at `7053c78de` had
  no textual conflicts. Focused race tests passed with
  `TestWerkCronSteeringRecoversClaimAfterConsentRevocation` excluded after
  the same failure reproduced on that develop commit alone.
- Focused race tests covered Account route redaction, loopback and origin
  enforcement, key verification and legacy-writer recovery, sign-in
  cancellation, consent and SSE, endpoint cleanup and CLI ownership.
- Six Settings Chromium suites and 195 frontend Node tests passed. The
  combobox suite edited through the real Account endpoint and observed
  model picker refresh. Mock Account responses and a simulated LAN SSE
  stream drove the visual and lifecycle captures.
- Compared `compare-1280-dark.png` and `compare-1280-light.png` with
  `ai-docs/assets/mockups/261122/provider-auth/mockup.html` at 1280 px.
  The Account hierarchy, actions, and themes matched. At 390 px,
  `compare-390-dark.png` and `compare-390-light.png` showed the endpoint
  rows below the initial viewport due to drawer chrome and row spacing.
  `account-endpoints-390-dark.png` and `account-endpoints-390-light.png`
  show the endpoints after scrolling. `lan-consent-390-light.png`
  showed the on-machine hint with the message.
- The independent lite review of `f74ed1b74..9f1a0fe14` found no
  actionable correctness or test-integrity issues.
- Credential and OAuth checks used fake values and isolated fixtures.
  The owner-run live sign-in check is recorded under Phase 2.
