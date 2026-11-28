---
title: "Pi(pi-rpc) 관리 런타임 계정 UI · 임시 PTY 로그인 · 하네스 연동"
related:
  261128-feat-ws-pi-rpc-managed-runtime-and-backend: prerequisite (managed Pi v1.5.2 runtime manager, pi-rpc wsagent backend, and multi-slot PI_CODING_AGENT_DIR wiring)
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 66adc2d8900573a3
sage-review-completeness-reviewed: 66adc2d8900573a3
---

# Managed Pi (pi-rpc) Account UI, Ephemeral PTY Login, and Harness Integration

## Background

Ticket 1 (`261128-feat-ws-pi-rpc-managed-runtime-and-backend`) landed the pinned standalone Pi `v1.5.2` runtime downloader (`internal/wspiruntime`), the embedded `.mjs` MCP/provider-response bridge, the `pi-rpc` `wsagent` backend, explicit-only `wsconfig` bucket handling, multi-slot `PI_CODING_AGENT_DIR` resolution in `internal/wscred/account_slots.go`, and `fs_denied`/`fs_outside` protection for `auth.json`.

However, `pi-rpc` still has no user-facing management surface:

1. `cmd/ws-mcp/dashboard.go:666` constructs `wspiruntime.NewManager` locally inside `newAgentRunnerWithHooks` instead of sharing one `*wspiruntime.Manager` across the runner and dashboard daemon, and there is no hook to clear `pirpcChoices` or `Support` choices caches when runtime installation or Pi accounts change.
2. `probePiRPCChoices` (`internal/wsagent/pirpc.go:355-377`) silently drops `resp.Error` when `resp.Success == false`, causing `Support.listChoices` to cache an empty model list for `choicesTTL` (5m) instead of `choicesFailTTL` (30s) (recorded as a minor review finding in Ticket 1).
3. `wscred.SlotAuthReady` (`internal/wscred/slot_login.go:141-184`) only validates `codex` and `claude`; `handleAccountStatus` (`internal/wsdashboard/account_routes.go:194-200`) does not expose `slots["pi-rpc"]` or managed Pi runtime readiness/progress.
4. There are no local-only HTTP routes to install/uninstall the managed Pi runtime or run an ephemeral interactive PTY login (`pi` TUI `/login` or `pi auth login <provider>`) into an isolated `PI_CODING_AGENT_DIR` slot without needing an existing session `replyID`.
5. `Settings -> Account`, the topbar account switcher (`usage-popover.js`), harness badges (`.hb[data-h="pi-rpc"]`), and tier/settings bucket ordering do not yet surface managed Pi controls, multi-slot switching, or 11-locale i18n strings.

This is **Ticket 2 of 2** for the managed Pi (`pi-rpc`) integration: it wires the shared runtime manager, local-only runtime & ephemeral PTY login endpoints, and full dashboard UI across two sequentially dependent phases.

## Decisions

### Confirmed Owner Policy Decisions

- **Install-implies-enabled (zero config manifest churn):** Installing the managed Pi runtime (`wspiruntime.Manager`, pinned `v1.5.2`) via `Settings -> Account` makes `pi-rpc` available across Support choices, New Session, Tier Settings, and Harness Switch without a separate feature-flag toggle or `config-manifest.json` / `dashboard-config-manifest.json` change. Uninstalling hides availability cleanly through `ChoiceLister.Executable()` and `supportBackendAvailability` (`internal/wsagent/support.go:385-413`) and invalidates cached choices.
  - *Rejected:* Adding an enablement toggle in `wsconfig` manifests or duplicating config keys across `agents-plugin/` and `agents-plugin-wsflow/`.
- **Single credential ownership & unfiltered providers (inherited from Ticket 1 `(1841)=a`):** Pi's own `<PI_CODING_AGENT_DIR>/auth.json` remains the sole credential owner. Credentials are never mirrored into `wscred` provider stores, never logged or returned over HTTP, and all providers offered by the `pi` CLI remain unfiltered.
  - *Rejected:* Mirroring Pi tokens into `wscred.Store` or building a custom per-provider OAuth form in Go.
- **Dedicated local-only ephemeral PTY login over `wsholder` + vendored `xterm.js`:** Because `Settings -> Account` has no session `replyID` and Pi `v1.5.2` interactive login requires a real TTY (`pi auth login <provider>` requires a provider arg; bare `pi auth login` prints usage and exits, while interactive `pi` TUI `/login` lets the user pick any provider via OAuth or API key), expose dedicated local-only endpoints under `/api/account/pi/login/*` backed by `d.holders` (`wsholder.SpawnSpec{Mode: "pty", ...}`, supporting POSIX PTY and Windows ConPTY) and the existing `terminal_routes.go` JSON WebSocket frame protocol + vendored `xterm.js` + `FitAddon` (`XtermAsset()`).
  - *Rejected:* Reusing `/s/<replyID>/terminals/*` (requires a session `replyID` and persists interactive shells across restarts); spawning raw pipes without a PTY (Pi TUI/OAuth prompts fail or drop interactivity).

### Auto-Confirmed Technical Decisions

- **Shared `*wspiruntime.Manager` & choices invalidation seam:** Hoist `piRuntime := wspiruntime.NewManager(wspiruntime.Options{})` from `newAgentRunnerWithHooks` (`cmd/ws-mcp/dashboard.go:666`) into `dashboardCommand` (`cmd/ws-mcp/dashboard.go:165-172`), passing it into both `newAgentRunnerWithHooks` (`wsagent.WithPiRuntime` + `wsagent.Options.PiRuntime`) and `wsdashboard.DaemonOptions.PiRuntime`. Export `wsagent.ClearPiRPCChoices(home string)` (`internal/wsagent/pirpc.go`) and `(*wsagent.Support).InvalidateChoices(harness string)` (`internal/wsagent/support.go`), and wire an optional `DaemonOptions.OnPiRuntimeChanged func()` callback in `cmd/ws-mcp/dashboard.go` that clears both caches. Invoke it on runtime install completion, runtime uninstall, Pi login slot completion, and Pi slot switch/remove/add, followed by `d.accountChanged()`.
  - *Rejected:* Exposing `*wsagent.Support` directly to `internal/wsdashboard` (import cycle); waiting up to `choicesTTL` (5m) after install or login before New Session / Support see Pi models.
- **`probePiRPCChoices` error propagation (`internal/wsagent/pirpc.go:355-377`):** When `resp.Success == false` on `"probe-models"`, record `modelsErr = strings.TrimSpace(resp.Error)` (defaulting to `"pi rpc get_available_models failed"` when empty) and return `nil, efforts, errors.New(modelsErr)` instead of `(nil, efforts, nil)`. On `"probe-thinking"` failure when `"probe-models"` succeeded, keep models usable and return default/empty efforts; if neither succeeded or the process exited before `"probe-models"`, return a non-nil error so `Support.listChoices` caches under `choicesFailTTL` (30s) rather than `choicesTTL` (5m).
  - *Rejected:* Caching an empty model list for 5 minutes when an unauthenticated or crashing Pi process returns `success: false`.
- **`SlotLoginHarnesses()` unchanged & `wscred.SlotAuthReady("pi-rpc", dir)`:** Keep `wscred.SlotLoginHarnesses()` (`internal/wscred/slot_login.go:39-41`) strictly `["codex", "claude"]` (pinned by `internal/wscred/account_slots_pirpc_test.go:21-23`) because `slots/sign-in/*` is the non-PTY OAuth/device-auth flow for Codex and Claude. Extend `wscred.SlotAuthReady("pi-rpc", configDir)` (`internal/wscred/slot_login.go:141-184`) to read `<configDir>/auth.json`, parse it as `map[string]map[string]any`, and return `true` iff at least one provider entry is a non-empty object containing at least one non-empty string field (never returning or logging raw values).
  - *Rejected:* Adding `"pi-rpc"` to `SlotLoginHarnesses()` (would break existing `slots/sign-in/*` assumptions and `TestAccountSlotPiRPCD10ResolutionAndT0Preservation`); accepting an empty `{}` `auth.json` as ready.
- **Adapter-owned dashboard files & `(1881)` harness-name scan compliance:** Place Pi runtime HTTP handlers and ephemeral PTY login orchestration in dedicated adapter-owned files `agents-plugin-tool/internal/wsdashboard/account_pi_routes.go` and `agents-plugin-tool/internal/wsdashboard/account_pi_login.go`, registering `^internal/wsdashboard/account_pi_[a-z0-9_]*\.go$` in `adapterOwned` inside `internal/wsagent/harness_name_scan_test.go:259-269` (matching `plan_usage_agy.go` and `internal/mcp/pi_[a-z0-9_]*\.go`). In shared `internal/wsdashboard/account_routes.go`, delegate route dispatch via `if d.handleAccountPi(w, r, action) { return }` right after `localViewer(r)` + `Cache-Control: no-store` and delegate status enrichment via `d.populatePiAccountStatus(&status)` so shared `account_routes.go` adds only two `classExternal` allowlisted JSON struct tags (`json:"pi_runtime,omitempty"`, `json:"pi_login,omitempty"`) with `// Retained name check:` comments and pinning test fixtures.
  - *Rejected:* Scattering `"pi-rpc"`, `"PI_CODING_AGENT_DIR"`, or `pi/*` route branches throughout shared `account_routes.go`.
- **Ephemeral PTY login invocation & dual completion paths:**
  - `POST /api/account/pi/login/start` accepts `{label?, slot_id?, existing?, provider?, rows?, cols?}`:
    - When `existing` is false (default: new managed account slot): derive a valid non-default `slot_id` via `deriveSlotID("pi-rpc", body.SlotID, body.Label)`, create `<d.dir>/slots/pi-rpc/<slotID>` via `wscred.CreateManagedSlotDirUnder(d.dir, "pi-rpc", slotID)` (`0700` / Windows protected User-SID DACL), and call `wscred.RegisterTempSlotDir(dir)` immediately so child `fs.*` calls cannot read staging credentials.
    - When `existing` is true: resolve an existing `"pi-rpc"` slot's `PI_CODING_AGENT_DIR` (including implicit `"default"` via `wscred.ResolveHarnessHome`, or a registered slot's `ConfigDir` via `d.accountSlots.ResolveSlot("pi-rpc", slotID)`) so users can add another provider, re-authenticate, or run `/logout` in an existing slot without deleting its directory on cancel.
    - Argv selection (verified against Pi `v1.5.2`): when `provider` is empty, launch `[binPath, "--no-session", "-ne", "-ns", "-np", "--no-themes", "-na", "-nc"]` (interactive Pi TUI where user runs `/login` or `/logout` and exits); when `provider` is non-empty, validate it strictly against `^[a-z0-9][a-z0-9._-]{0,63}$` (rejecting leading `-`, whitespace, or path separators) and launch `[binPath, "auth", "login", provider]`.
    - Isolated env: scrub parent harness vars (`PI_*`, `CLAUDE*`, `CODEX_*`, `ORCA_*`, `WS_PI_*`), preserve base OS env (`PATH`, `HOME`/`USERPROFILE`, `TERM=xterm-256color`, `COLORTERM=truecolor`), and set `PI_CODING_AGENT_DIR=<dir>`, `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0`.
    - Holder descriptor uses `kind: "pi-login"` (distinct from `terminalKind = "terminal"`, so ephemeral login PTYs never appear as session terminal cards or orphan shells in `terminals.go`).
    - Lifecycle & cleanup: single active login attempt at a time (`409 Conflict` with `login_in_progress` if another is active); idle timeout (5m) and max session timeout (15m) overridable in tests; explicit `POST /api/account/pi/login/cancel` stops the holder (`Holders.Stop(holderID)`), releases `RegisterTempSlotDir`, and removes the staging directory when `existing == false`; explicit `POST /api/account/pi/login/complete` OR normal child exit verifies `SlotAuthReady("pi-rpc", dir)` (or `d.slotAuthPreflight` when injected), commits `AddSlot(..., Managed: true)` for new slots (ignoring `AddSlot` when `existing == true`), invalidates choices caches, and emits `d.accountChanged()`. Dropping the WebSocket starts a short disconnect grace period (e.g. 5s, allowing brief reconnect) before auto-cancelling and cleaning up the holder + temp staging dir. `handleAccountSlotsRemove` refuses (`409 login_in_progress`) to delete a slot currently targeted by an active `pi-login` attempt, daemon startup sweeps any crash-orphaned `kind: "pi-login"` holders via `Holders.List`/`Holders.Stop`, and `Daemon.Close()` (`daemon_runtime.go:200`) stops any active install context and calls `d.piLogin.stop()` before `d.holders.Close()` and `d.accountSlots.Close()`.
- **Settings -> Account UI layout & `<1000`-line modularity:**
  - Keep `dashboard-settings-account-harness.js` Group 1 (`data-account-group="harness"`) strictly filtered to `HARNESS_ORDER = ["claude", "codex"]` (fixing `orderedHarnesses` at line 11 so `"pi-rpc"` never leaks into Group 1).
  - Add `dashboard-settings-account-pirpc.js` (<1,000 lines, `window.WsSettingsAccountPiRPC`) loaded in `pages.go:615` after `dashboard-settings-account-runtime.js` and before `dashboard-settings-account.js`, rendered as Group 3 (`data-account-group="pirpc"`, hidden when `nav.status?.pi_runtime` is absent). Follow `ai-docs/manuals/dashboard-style.md` (borderless Material tonal `.acc-grp` card, `WsHoldConfirm` 1.5s hold bar for destructive Uninstall and slot Remove, zero `:has()` in CSS) and `ai-docs/manuals/performance.md` (compositor-only `transform: scaleX(...)` progress bar, zero idle RAF/timers, pause/stop polling when settled). Call `window.WsSettingsCombobox?.invalidateChoices()` on runtime install settlement, uninstall, login completion, and Pi slot mutations so `Settings -> Tiers` reflects updated Pi availability/models without reloading `/settings`.
  - Serve `FrameXtermFile` stylesheet and `data-term="{{.XtermFile}}"` on `config-settings` (`dashboard.tmpl:683-695`, `pages.go:684-698`, `config_routes.go:315-320`) when `localViewer(r)` and `d.holders != nil` so the Settings page can lazy-load `/s/xterm-<hash>.js` + `FitAddon` only when the user opens the Pi PTY login modal.
  - Topbar switcher (`usage-charts.js` + `usage-popover.js`): keep `pi-rpc` out of quota `DEFS` / `activeGroupDefs()` / `inactiveSlotsList()` (use a separate non-quota badge lookup or `noQuota` guard so `defForHarness("pi-rpc", "")` resolves for `drawSessions()` without adding `"No curve: Pi"` notes or no-cache overlays under 5H/7D charts). Render a compact `Pi` (`pi-rpc`) multi-slot switcher row in Group 1 when `accountData.pi_runtime?.installed` and `slotsOf("pi-rpc").length > 0` without `.up-hr[data-k]` focus/lock triggers (`usage-popover.js:759-763, 813-816`), and skip `setPreview` / `applyVisualState` dimming for non-quota harnesses so hovering or focusing inactive Pi slot pills never dims Claude/Codex/Agy charts or rows. Add `pi-rpc` badge entries in `usage.css` and `dashboard.css` (`.hb[data-h="pi-rpc"]`).
  - Settings/Tier ordering (`dashboard-settings.js`): append `"pi-rpc"` to `tierHarnessOrder` (`line 30`) and `harnessChoices` order (`line 1617`), and return `"Pi"` from `bucketName("pi-rpc")` (`line 524`).
- **Compatibility Tier — T0 Transparent:** Per `ai-docs/manuals/release-compatibility.md`, every touched cross-version surface is strictly T0:
  1. `GET /api/account/status` (`internal/wsdashboard/account_routes.go:31-46`, `191-212`): adds optional `omitempty` JSON fields `pi_runtime` and `pi_login` plus additive `"pi-rpc"` key inside `slots` map; old clients ignore unknown JSON fields and unknown `slots` keys (`dashboard-settings-account-harness.js` and `usage-popover.js` read only known fields/harnesses).
  2. New local-only HTTP & WebSocket endpoints (`/api/account/pi/runtime/*`, `/api/account/pi/login/*` routed under `accountPrefix` at `internal/wsdashboard/account_routes.go:13,84-151`): purely additive routes; old daemons return `404 not_found` and old browsers never call them.
  3. Go structs `wsdashboard.DaemonOptions` (`internal/wsdashboard/daemon.go:42-200`) and `wsagent.Options` (`internal/wsagent/runner.go` / `cmd/ws-mcp/dashboard.go:678-682`): additive optional fields (`PiRuntime`, `OnPiRuntimeChanged`) with safe nil defaults; no persisted on-disk schema changes.
  4. `account-slots.json` (`internal/wscred/account_slots.go:326-385`): already verified T0 in Ticket 1 (`decodeAccountSlotsDocument` / `encode` preserve unknown harness rows and active entries verbatim; no schema bump).
  5. Dashboard static assets & 11-locale i18n tables (`internal/wsdashboard/pages.go:614-615`, `internal/wsi18n/tables/*.json`): content-hashed asset IDs (`assetID` at `pages.go:670-680`) and additive i18n keys; zero `wsconfig` manifest or `/mcp` wire changes.

## Constraints

- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
- Convention: `ai-docs/manuals/config-settings-ui.md` (declared for `agents-plugin-tool/internal/wsdashboard/config_routes.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)
- **File-touch & frontend update budget (`performance.md`):**
  - `GET /api/account/status` reads `d.piRuntime.Status()` from memory (zero `stat()` polling after initial warm-up).
  - `wscred.SlotAuthReady("pi-rpc", dir)` runs only at login completion/preflight, never on page load or SSE ticks.
  - Progress bar animation uses compositor-only `transform: scaleX(...)` with zero active timers/RAF once install settles; xterm.js script is lazy-loaded only when the PTY login modal opens.
- **Visual language (`dashboard-style.md`):** Borderless Material tonal cards (`--wb-card`, `--wb-muted`, `--wb-shadow` / `--wb-shadow-hi`), `--z-modal` for the PTY login dialog, 1.5s `WsHoldConfirm` (`holdBar`) for destructive Uninstall and slot Remove, zero `:has()` selectors in `dashboard-settings-account.css` (enforced by `frontend-settings-account-rework.test.js:647`).
- **Modular JS line budget:** Every split account JS module (including `dashboard-settings-account-pirpc.js`) must stay strictly under 1,000 lines and be verified in `frontend-settings-account-rework.test.js:372-394`.
- **Harness-name isolation (`(1881)`):** `go test ./internal/wsagent -run 'TestCommonCodeHasNoHarnessNameBranch|TestHarnessNameScan'` must pass with zero un-allowlisted harness names in common code.
- **Security boundaries:** Every `/api/account/pi/*` endpoint inherits `Cache-Control: no-store`, `localViewer(r)` (LAN viewers get `404 not_found`), and strict `sameOrigin(r)` / `crossSiteFetch(r)` checks (WebSocket upgrade on `/api/account/pi/login/ws` must also require `localViewer(r)` + `sameOrigin(r)` + `wsUpgrade(r)`). Never log, echo, or expose `auth.json` token contents; always register staging directories via `wscred.RegisterTempSlotDir` before spawning the PTY.
- **No `wsconfig` manifest changes & no `pi.*` keys:** Do not modify `config-manifest.json`, `dashboard-config-manifest.json`, or `agents-plugin-wsflow/`.
- **11-locale i18n parity:** Add every new `js.settings.account.pi_*` / `js.usage.*` key across all 11 locale JSON files in `agents-plugin-tool/internal/wsi18n/tables/` (`de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`) so `go test ./internal/wsi18n` passes cleanly.
- **Cross-platform:** Both POSIX PTY and Windows ConPTY paths must compile and pass `GOOS=windows go vet ./...`.

## Prior Art

- `agents-plugin-tool/internal/wsdashboard/account_routes.go` & `account_slots_routes.go` — `/api/account/*` routing, `localViewer`, `sameOrigin`/`crossSiteFetch`, `accountBody`, `accountError`, `deriveSlotID`, slot CRUD, and `d.accountChanged()`.
- `agents-plugin-tool/internal/wsdashboard/account_slot_signin.go` — single-attempt state machine, staging dir lifecycle (`CreateManagedSlotDirUnder`, `RegisterTempSlotDir`), `SlotAuthPreflight`, timeout, and `Daemon.Close()` `stop()`.
- `agents-plugin-tool/internal/wsdashboard/terminal_routes.go` & `terminals.go` — `d.holders` (`TerminalHost` / `*wsholder.Manager`), `wsholder.SpawnSpec{Mode: "pty", ...}`, JSON WebSocket frame protocol (`hello`/`out`/`exit`/`gap`/`closed`/`ping`/`in`/`size`/`focus`), `XtermAsset()`, and `TermSymbolsAsset()`.
- `agents-plugin-tool/internal/wsdashboard/assets/terminal.js` — lazy loading `/s/xterm-<hash>.js` + `FitAddon` + `WsNerdSymbols`, theme sync, and WebSocket frame handling.
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-account-shared.js`, `dashboard-settings-account-harness.js`, `dashboard-settings-account-runtime.js`, `dashboard-settings-account.js` — 3-level account card hierarchy, `WsHoldConfirm` `holdBar`, inline rename with SSE focus/draft preservation.
- `agents-plugin-tool/internal/wsdashboard/assets/usage-charts.js` & `usage-popover.js` — topbar account switcher pills and active-session slot rows.

## Prior Decisions

- `261128-feat-ws-pi-rpc-managed-runtime-and-backend` (2026-11-29, Result (111ab928)): "Recorded 1 Minor review finding (not fixed per lite-review policy): probePiRPCChoices (internal/wsagent/pirpc.go:355-377) drops resp.Error when resp.Success == false" — bearing: supports
- `984cc772` (2026-11-29, commit): "Added cached chartCache/rowCache references with signature early-out in applyVisualState(), conditional place() in setPreview() when popover dimensions actually change, and rAF-coalesced" — bearing: constrains
- `9831d2c9` (2026-11-28, commit): "Split the 1,446-line dashboard-settings-account.js into four focused modules (shared 234L, harness 623L, runtime 776L, thin coordinator 160L) registered in pages.go SettingsScript" — bearing: constrains
- `dcb83b49` (2026-11-28, commit): "Implemented D15/D17/D18 single-slot agy auth management: agy remains single-slot read-only in the topbar switcher" — bearing: constrains
- `617ecf14` (2026-11-28, commit): "Implemented additive POST /api/account/slots/rename (T0) under existing localViewer/sameOrigin/72KiB DisallowUnknownFields guards, delegating to wscred.AccountSlotRegistry.UpdateSlotLabel" — bearing: supports
- `261128-feat-ws-dashboard-ide-shell-overhaul` (2026-11-28, commit): "T0 transparent change: pure client/template/CSS shell overhaul with no new wire or payload fields; preserves .rail-session-panel/#rail-companion-slot and .usage-toggle/railPopPlace peer contracts." — bearing: constrains
- `261127-feat-ws-harness-account-switcher-and-slot-continuity` (2026-11-28, commit): "Kept common wsagent/wsdashboard code free of literal harness-name branching (verified by TestCommonCodeHasNoHarnessNameBranch) via AccountSlotRegistry.ActiveSlots() and HarnessTraitSet." — bearing: constrains
- `4c112faf` (2026-11-24, commit): "The Account API shares dashboard viewer access and requires a loopback peer and Host. POST operations require the dashboard origin." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | cmd/ws-mcp/dashboard.go, internal/wsagent/{pirpc.go,support.go,harness_name_scan_test.go}, internal/wscred/slot_login.go, internal/wsdashboard/{daemon.go,daemon_start.go,daemon_runtime.go,account_routes.go,account_slots_routes.go,account_pi_routes.go,account_pi_login.go,pages.go,config_routes.go}, assets/{dashboard.tmpl,dashboard.css,usage.css,usage-charts.js,usage-popover.js,dashboard-settings.js,dashboard-settings-account*.js,dashboard-settings-account.css}, internal/wsi18n/tables/*.json |
| scope.surface | cross-module | wsagent.ClearPiRPCChoices, (*wsagent.Support).InvalidateChoices, wscred.SlotAuthReady, wsdashboard.DaemonOptions.{PiRuntime,OnPiRuntimeChanged}, /api/account/status, /api/account/pi/runtime/*, /api/account/pi/login/* |
| scope.new_public_symbol | yes | wsagent.ClearPiRPCChoices, (*wsagent.Support).InvalidateChoices, wsdashboard.DaemonOptions.PiRuntime, wsdashboard.DaemonOptions.OnPiRuntimeChanged |
| scope.new_type_contract | yes | ClearPiRPCChoices(string), (*Support).InvalidateChoices(string), DaemonOptions fields, and additive /api/account/status and /api/account/pi/* HTTP/WS JSON contracts |
| scope.test_surface | new-files | new internal/wsdashboard/account_pi_routes_test.go plus internal/wsagent/pirpc_test.go, internal/wscred/account_slots_pirpc_test.go, internal/wsdashboard/frontend-settings-account-rework.test.js, internal/wsdashboard/usage*.test.js, internal/wsi18n |
| complexity.reuse_points | confirmed | wspiruntime.Manager, wscred.{CreateManagedSlotDirUnder,RegisterTempSlotDir,ResolveHarnessHome,AccountSlotRegistry}, wsdashboard.{TerminalHost,accountBody,localViewer,sameOrigin,crossSiteFetch,wsUpgrade,XtermAsset}, WsSettingsAccountShared, terminal.js WS frame protocol |
| complexity.side_effect_risk | high | spawns managed Pi PTY child processes under wsholder, creates and removes slot credential directories, manages WebSocket viewers and daemon shutdown teardown, and touches shared dashboard assets and 11 i18n tables |
| risk.correctness | high | single-flight runtime install, PTY lifecycle/timeout/disconnect-grace state machine, dual exit/complete auth verification, and choices/SSE cache invalidation |
| risk.fit | moderate | must preserve (1881) TestHarnessNameScan isolation in shared Go files, <1,000-line JS modularity, zero :has() CSS, compositor-only progress animation, and existing codex/claude/agy flows |
| risk.test | moderate | covers fake PTY login with real wsholder.Manager, -race tests across 3 Go packages, Node frontend unit tests, 11-locale i18n checks, and GOOS=windows go vet |
| risk.security_or_contract | high | localViewer/sameOrigin/crossSiteFetch/wsUpgrade enforcement on PTY routes, RegisterTempSlotDir credential isolation, strict provider arg validation, and T0 JSON compatibility |

## Phases

### Phase 1: Backend Runtime Wiring, `probePiRPCChoices` Fix, Status Extensions, and Local-Only Runtime + Ephemeral PTY Login Endpoints

Wire the shared managed Pi runtime across daemon and runner, fix `probePiRPCChoices` error propagation, extend `wscred.SlotAuthReady` and `/api/account/status`, and implement local-only `/api/account/pi/runtime/*` and `/api/account/pi/login/*` (HTTP + WebSocket over `wsholder` PTY).

#### Deliverables

1. **Daemon & Runner Wiring + `probePiRPCChoices` Fix + Choices Cache Invalidation:**
   - `agents-plugin-tool/internal/wsagent/pirpc.go`:
     - Fix `probePiRPCChoices` (`pirpc.go:355-377`): track `modelsErr` / `effortsErr` when `resp.Success == false` (fallback `"pi rpc get_available_models failed"` if `resp.Error == ""`), and return a non-nil error whenever `!gotModels || modelsErr != ""` so `Support.listChoices` caches failures under `choicesFailTTL` (30s) rather than `choicesTTL` (5m).
     - Export `ClearPiRPCChoices(home string)`: under `pirpcChoices.Lock()`, clear all entries when `strings.TrimSpace(home) == ""` or delete `normalizePiRPCHomeKey(home)` when non-empty.
   - `agents-plugin-tool/internal/wsagent/support.go`:
     - Export `func (s *Support) InvalidateChoices(harness string)`: under `s.choicesMu.Lock()`, delete `s.cache[harness]` when `harness != ""` or `clear(s.cache)` when `harness == ""`.
   - `agents-plugin-tool/internal/wsdashboard/daemon.go` & `daemon_start.go` & `daemon_runtime.go`:
     - Add additive `PiRuntime *wspiruntime.Manager` and `OnPiRuntimeChanged func()` (plus test seams `piLoginTimeout`, `piLoginIdleTimeout`, `piLoginDisconnectGrace`, `piLoginCommand func(provider string) (string, []string, error)`) on `DaemonOptions` and store them on `Daemon`.
     - On daemon startup (once `d.holders` is attached in `daemon_start.go`), invoke `d.sweepOrphanPiLoginHolders()` (defined in adapter-owned `account_pi_login.go`) to list and stop any leftover `kind: "pi-login"` holders surviving an abrupt prior daemon crash.
     - In `Daemon.Close()` (`daemon_runtime.go:199-202`), cancel any in-flight Pi runtime install context and call `d.piLogin.stop()` before `d.holders.Close()` and `d.accountSlots.Close()`.
   - `agents-plugin-tool/cmd/ws-mcp/dashboard.go`:
     - Hoist `piRuntime := wspiruntime.NewManager(wspiruntime.Options{})` from `newAgentRunnerWithHooks` (`dashboard.go:666`) into `dashboardCommand` (`dashboard.go:165-172`).
     - Pass `piRuntime` into `newAgentRunnerWithHooks` (wiring `wsagent.WithPiRuntime` and `wsagent.Options.PiRuntime`) and into `wsdashboard.DaemonOptions.PiRuntime`.
     - Wire `DaemonOptions.OnPiRuntimeChanged` to call `wsagent.ClearPiRPCChoices("")` and `support.InvalidateChoices("")` (using `""` rather than `wsagent.BackendPiRPC`, because `TestHarnessNameScan` flags `BackendPiRPC` and `harnessAliases` in `cmd/ws-mcp/dashboard.go` at `internal/wsagent/harness_name_scan_test.go:250,256,273,314-383,487-490`).

2. **`wscred.SlotAuthReady("pi-rpc", dir)` & `/api/account/status` Extensions:**
   - `agents-plugin-tool/internal/wscred/slot_login.go`:
     - Extend `SlotAuthReady(harness, configDir)` (`slot_login.go:141-184`) with `case "pi-rpc":`: read `<configDir>/auth.json`, unmarshal as `map[string]map[string]any`, and return `true` iff at least one provider key maps to a non-empty object containing at least one non-empty string field. Never return or log token material. Keep `SlotLoginHarnesses()` unchanged (`["codex", "claude"]`).
   - `agents-plugin-tool/internal/wsdashboard/account_routes.go`:
     - Add `PiRuntime *piRuntimeStatusView `json:"pi_runtime,omitempty"`` and `PiLogin *piLoginStatus `json:"pi_login,omitempty"`` to `accountStatus` (lines 31-46) with `// Retained name check:` comments.
     - In `handleAccount` (line 90, after `Cache-Control: no-store` and `localViewer(r)`): delegate `if d.handleAccountPi(w, r, action) { return }`.
     - In `handleAccount` (line 128): allow `action == "status"` when `d.piRuntime != nil` in addition to `d.accountSlots != nil || d.planUsage != nil`.
     - In `handleAccountStatus`: invoke `d.populatePiAccountStatus(&status)` (defined in adapter-owned `account_pi_routes.go`), which populates:
       - `status.PiRuntime` when `d.piRuntime != nil`:
         ```json
         {
           "installed": true,
           "version": "1.5.2",
           "binary_path": "/path/to/runtimes/pi/v1.5.2-darwin-arm64/pi",
           "install_dir": "/path/to/runtimes/pi/v1.5.2-darwin-arm64",
           "installing": false,
           "progress": { "phase": "download", "downloaded": 10485760, "total": 52428800 },
           "error": ""
         }
         ```
       - `status.PiLogin` (current ephemeral login status snapshot when active or recently ended).
       - `status.Slots["pi-rpc"] = d.accountSlots.Slots("pi-rpc")` when `d.accountSlots != nil && d.piRuntime != nil` (keeping `SlotLoginModes` unchanged for `"codex"`/`"claude"`).
     - In `account_slots_routes.go`:
       - In `handleAccountSlotsRemove` (`account_slots_routes.go:111-143`), before calling `RemoveSlot` / `os.RemoveAll(slot.ConfigDir)`, check `d.piLoginBusyForSlot(harness, slotID)` (defined in adapter-owned `account_pi_login.go`) and refuse with `409 login_in_progress` if an active `pi-login` attempt targets that slot (avoids Windows ConPTY CWD lock and POSIX unlinked-dir write races).
       - When `slots/add`, `slots/remove`, or `slots/switch` mutates a slot (or after any slot change), trigger `d.notifyPiRuntimeChanged()` when `d.onPiRuntimeChanged != nil` so `pirpcChoices` and `Support` choices refresh cleanly on active-slot switches too.

3. **Local-Only Managed Runtime Routes (`agents-plugin-tool/internal/wsdashboard/account_pi_routes.go`):**
   - Enforce `localViewer(r)` (inherited from `handleAccount`), `Cache-Control: no-store`, strict method checks, and `sameOrigin(r)` / `crossSiteFetch(r)` checks.
   - `POST /api/account/pi/runtime/install`:
     - If `d.piRuntime == nil`, return `503 runtime_unavailable`.
     - Single-flight background execution bound to daemon shutdown context: if an install is already running, return `202 Accepted` with the live `piRuntimeStatusView` (`installing: true`, current `progress`).
     - Otherwise start a goroutine calling `d.piRuntime.Install(installCtx, onProgress)` where `onProgress` updates in-memory progress (`phase`, `downloaded`, `total`) and emits throttled `d.accountChanged()` (at most every ~250ms plus on phase transitions). On completion (success or error), record any error in memory, call `d.notifyPiRuntimeChanged()`, and emit `d.accountChanged()`. Return `202 Accepted` with current `piRuntimeStatusView`.
   - `POST /api/account/pi/runtime/uninstall`:
     - If `d.piRuntime == nil`, return `503 runtime_unavailable`.
     - Refuse with `409 Conflict` (`runtime_busy`) while an install is in progress or an active Pi PTY login is running.
     - Call `d.piRuntime.Uninstall(r.Context())`, clear any recorded runtime error/progress, call `d.notifyPiRuntimeChanged()`, emit `d.accountChanged()`, and return `200 OK` with updated `piRuntimeStatusView`.

4. **Local-Only Ephemeral PTY Login (`agents-plugin-tool/internal/wsdashboard/account_pi_login.go`):**
   - Route dispatch (handled inside `d.handleAccountPi` under `/api/account/`):
     - `POST /api/account/pi/login/start`
     - `GET  /api/account/pi/login/status`
     - `POST /api/account/pi/login/complete` (verifies `SlotAuthReady("pi-rpc", dir)`, stops PTY if still running, commits slot, returns final status)
     - `POST /api/account/pi/login/cancel`
     - `GET  /api/account/pi/login/ws` (WebSocket upgrade: requires `localViewer(r)`, `sameOrigin(r)`, `wsUpgrade(r)`, and valid matching `attempt` query param)
   - Concrete request/response shapes:
     - `POST /api/account/pi/login/start`:
       - Request: `{"label": "Work Pi", "slot_id": "", "existing": false, "provider": "", "rows": 24, "cols": 80}` (unknown fields rejected via `accountBody`).
       - Preconditions: `d.piRuntime != nil` and `d.piRuntime.Status().Installed` (else `409 runtime_not_installed`), `d.accountSlots != nil` (else `503 slot_registry_unavailable`), `d.holders` implements `TerminalHost` (else `503 pty_unavailable`), no active Pi login attempt (else `409 login_in_progress`), no runtime install in progress (else `409 runtime_busy`).
       - Staging & isolation:
         - New slot (`existing == false`): derive `slotID := deriveSlotID("pi-rpc", body.SlotID, body.Label)`, reject if slot ID already exists (`409 slot_exists`), create `<d.dir>/slots/pi-rpc/<slotID>` via `wscred.CreateManagedSlotDirUnder(d.dir, "pi-rpc", slotID)`, call `releaseDeny := wscred.RegisterTempSlotDir(dir)`.
         - Existing slot (`existing == true`): resolve `slotID := wscred.NormalizeAccountSlotID(body.SlotID)`; for `"default"`, resolve via `wscred.ResolveHarnessHome(Harness: "pi-rpc")` (mkdir `0700` if absent); for named slots, look up via `d.accountSlots.ResolveSlot("pi-rpc", slotID)` (else `404 account_slot_not_found`). Never delete an existing slot's directory on cancel or failure.
       - Process launch via `d.holders.(TerminalHost)`:
         - Launch holder with descriptor `{"kind":"pi-login","attempt":"<uuid>","slot_id":"<slotID>"}` (`terminals.go:178` skips non-`terminal` kinds automatically).
         - Spawn child with `wsholder.SpawnSpec{Mode: "pty", Argv: argv, Cwd: dir, Env: isolatedEnv, Rows: clampedRows, Cols: clampedCols}`.
         - Argv: `[binPath, "--no-session", "-ne", "-ns", "-np", "--no-themes", "-na", "-nc"]` when `provider == ""`, or `[binPath, "auth", "login", provider]` when `provider` matches `^[a-z0-9][a-z0-9._-]{0,63}$` (else `400 invalid_provider`).
         - Isolated env: drop `PI_*`, `CLAUDE*`, `CODEX_*`, `ORCA_*`, `WS_PI_*`, set `PI_CODING_AGENT_DIR=<dir>`, `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0`, `TERM=xterm-256color`.
       - Response (`200 OK`):
         ```json
         {
           "state": "running",
           "attempt": "<uuid>",
           "slot_id": "work-pi-1a2b3c4d",
           "label": "Work Pi",
           "existing": false,
           "provider": "",
           "ws_path": "/api/account/pi/login/ws?attempt=<uuid>"
         }
         ```
     - `GET /api/account/pi/login/ws?attempt=<uuid>&from=<seq>`:
       - Streams the exact `terminal_routes.go` JSON frame protocol (`hello`/`out`/`exit`/`gap`/`closed`/`ping` daemon -> page; `in`/`size`/`focus`/`pong` page -> daemon) backed by the login holder's `wsholder.Client` + `wsholder.Sink` events.
       - Tracks connected viewer count: when the last WebSocket viewer disconnects while `state == "running"`, arm a short disconnect grace timer (default 5s, cancelled if the viewer reconnects) that cancels the attempt, stops the holder, and removes the uncommitted staging directory.
     - Completion, timeouts & cleanup:
       - Enforce max session timeout (15m) and idle I/O timeout (5m), both resetting/closing cleanly.
       - On child exit or `POST /api/account/pi/login/complete`:
         - Check `SlotAuthReady("pi-rpc", dir)` (or `d.slotAuthPreflight("pi-rpc", dir)` if configured).
         - For new slots: if ready, commit `d.accountSlots.AddSlot(AccountSlot{ID: slotID, Harness: "pi-rpc", Label: label, ConfigDir: dir, Managed: true})`, release `RegisterTempSlotDir` (now covered by `RegisteredSlotDirs()`), set `state = "succeeded"`, call `d.notifyPiRuntimeChanged()`, and emit `d.accountChanged()`. If not ready on exit, set `state = "failed"`, `reason = "auth_not_ready"`, stop holder, release temp deny, and remove staging dir. On `complete` while still running with `!ready`, return `422 auth_not_ready` without tearing down the PTY so the user can finish logging in.
         - For existing slots: stop holder if still running, report `state = "succeeded"` (even if user logged out, `auth_ready: bool` reflects current state), call `d.notifyPiRuntimeChanged()`, and emit `d.accountChanged()`.
       - `POST /api/account/pi/login/cancel` and `d.piLogin.stop()` (called from `Daemon.Close()`): stop holder via `Holders.Stop(holderID)`, cancel timers, release `RegisterTempSlotDir`, remove uncommitted staging dir when `existing == false`, set `state = "cancelled"`, and emit `d.accountChanged()`.

5. **Harness-Name Scan & Phase 1 Verification:**
   - Update `agents-plugin-tool/internal/wsagent/harness_name_scan_test.go`:
     - Add `regexp.MustCompile("^internal/wsdashboard/account_pi_[a-z0-9_]*\\.go$")` to `adapterOwned`.
     - Add `classExternal` allowlist entries for the two JSON struct tags in `internal/wsdashboard/account_routes.go` (`json:"pi_runtime,omitempty"`, `json:"pi_login,omitempty"`) with pinning test fixtures.
   - Unit & race tests:
     - `internal/wsagent/pirpc_test.go`: test `probePiRPCChoices` error propagation when `resp.Success == false` (and verify `Support.listChoices` caches under `choicesFailTTL` rather than `choicesTTL`) + `ClearPiRPCChoices` + `Support.InvalidateChoices`.
     - `internal/wscred/account_slots_pirpc_test.go`: test `SlotAuthReady("pi-rpc", dir)` covering missing file, invalid JSON, empty `{}`, empty provider object `{"openai":{}}`, non-string/whitespace values, and valid provider credentials (OAuth / API key) without leaking secrets.
     - `internal/wsdashboard/account_pi_routes_test.go`:
       - Local-only & origin/CSRF enforcement (`404` for LAN viewers, `403` for cross-origin POST/WS).
       - `/api/account/status` exposes `pi_runtime` and `slots["pi-rpc"]` while keeping `claude`/`codex` intact and `SlotLoginHarnesses()` unchanged.
       - `/api/account/pi/runtime/install` (single-flight, progress updates, SSE `account-changed`, choices invalidation callback) & `/api/account/pi/runtime/uninstall` (busy guard during install/login, cache invalidation).
       - `/api/account/pi/login/*` end-to-end with fake PTY login script + real `wsholder.Manager`: start -> WS `hello`/`out`/`in`/`size`/`exit` -> staging dir protected by `RegisterTempSlotDir` -> `SlotAuthReady` validation -> `AddSlot` commit -> choices invalidation; plus cancel cleanup (temp dir removed), failed/unready exit cleanup, single-attempt `409`, WS disconnect grace cleanup, existing-slot re-auth (never deletes dir), `POST /api/account/slots/remove` returning `409 login_in_progress` while an `existing: true` login targets that slot, startup sweep stopping crash-orphaned `kind: "pi-login"` holders, invalid provider rejection, and `Daemon.Close()` holder cleanup.
   - Run `go test -race ./internal/wsagent -run 'TestPiRPC|TestSupport|TestCommonCodeHasNoHarnessNameBranch|TestHarnessNameScan'`, `go test -race ./internal/wscred/...`, `go test -race ./internal/wsdashboard -run 'TestAccount|TestPiAccount|TestPiLogin|TestPiRuntime'`, and `GOOS=windows go vet ./...`.

### Phase 2: Frontend Settings UI, Ephemeral `xterm.js` Modal, Topbar/Settings Wiring, and 11-Locale i18n

Build the Settings -> Account Pi UI and ephemeral `xterm.js` PTY login modal on top of Phase 1's endpoints, wire topbar switching, harness badges, tier/settings ordering, and all 11 locale tables.

#### Deliverables

1. **Settings Page Xterm Asset Plumbing (`pages.go`, `dashboard.tmpl`, `config_routes.go`):**
   - `agents-plugin-tool/internal/wsdashboard/pages.go`:
     - Add `XtermFile string` to `settingsPageData` (`pages.go:684-688`) and pass it through `renderSettingsPage(lang, theme string, hasAccount bool, xtermFile string)` (`pages.go:690-698`).
     - Register `"dashboard-settings-account-pirpc.js"` in `SettingsScript` (`pages.go:615`) after `"dashboard-settings-account-runtime.js"` and before `"dashboard-settings-account.js"`.
   - `agents-plugin-tool/internal/wsdashboard/config_routes.go`:
     - At `config_routes.go:316-318`, compute `hasAccount = isBrowserCred || d.accountSlots != nil || d.piRuntime != nil` and pass `xtermFile` (`name, _ := XtermAsset()`) when `localViewer(r) && d.holders != nil` (else `""`).
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl`:
     - In `config-settings` (`dashboard.tmpl:683-695`), emit `{{with .XtermFile}} data-term="{{.}}"{{end}}` on `<html>` and `<link rel="stylesheet" href="/s/{{.Assets.FrameXtermFile}}">` when `.XtermFile` is set, so `dashboard-settings-account-pirpc.js` can lazy-load `/s/<xtermFile>` only when the login terminal modal opens.

2. **Settings -> Account Modular Pi Card & Ephemeral `xterm.js` Modal (`dashboard-settings-account-pirpc.js`, `dashboard-settings-account-harness.js`, `dashboard-settings-account.js`, `dashboard-settings-account-shared.js`, `dashboard-settings-account.css`):**
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-account-harness.js`:
     - Fix `orderedHarnesses(slotsMap)` (`line 11-21`) to filter strictly to `HARNESS_ORDER` (`["claude", "codex"]`) so `"pi-rpc"` slots never duplicate inside Group 1.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-account-shared.js`:
     - Add error code mappings in `errorText` (`lines 112-147`) for `runtime_unavailable`, `runtime_not_installed`, `runtime_busy`, `pty_unavailable`, `login_in_progress`, `invalid_provider`.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-account-pirpc.js` (strictly `< 1,000` lines, exports `window.WsSettingsAccountPiRPC = {create}`):
     - Renders Group 3 (`data-account-group="pirpc"`, `h.groupCard("3", ...)`), hidden when `!nav.status?.pi_runtime`.
     - **Managed Runtime block:**
       - Shows pinned version badge (`v1.5.2`), readiness pill (`Installed` / `Not installed` / `Installing…`), and `Install` (or `Reinstall`) button + destructive `Uninstall` 1.5s hold bar (`h.holdBar`, disabled while installing or login active).
       - While `pi_runtime.installing` is true, renders a slim tonal progress track whose inner bar animates exclusively via `transform: scaleX(ratio)` (`performance.md`, zero layout thrash) plus phase label (`verify_manifest` / `download` with `tabular-nums` MB progress / `extract`). Stops all timers cleanly once settled.
       - Surfaces any `pi_runtime.error` inline via `h.setNotice(..., "fail")`.
     - **Multi-Slot Accounts block** (shown when installed or when existing `slots["pi-rpc"]` entries exist):
       - Renders each `pi-rpc` slot row (`data-slot-id`) with active indicator pill, `Switch` action (`POST /api/account/slots/switch`), **Terminal Login / Manage Auth** button (opens modal with `existing: true`), inline `Rename` (`POST /api/account/slots/rename`, preserving open editor & draft across SSE refreshes just like Group 1, hidden for `default`), and `Remove` 1.5s hold bar (`POST /api/account/slots/remove` with `delete_dir: true`, hidden for `default`).
       - Primary **Add Account (Terminal Login)** button (disabled if `!installed` or `!data-term` or non-loopback): opens a compact pre-launch drawer or modal header allowing optional slot label + optional direct provider name (empty = interactive Pi TUI `/login`).
     - **Embedded Ephemeral `xterm.js` PTY Modal (`z-index: var(--z-modal)`):**
       - Borderless Material tonal dialog (`--wb-card`, `--wb-shadow-hi`, `border-radius: var(--wb-radius)`), accessible `role="dialog"` + `aria-modal="true"`, `Escape` or Close button prompts/cancels cleanly.
       - Lazy-loads `/s/<data-term>` (`window.Terminal` + `window.FitAddon`) and `WsNerdSymbols` font once on first open (reusing `terminal.js` theme palette synced with `data-theme` / `prefers-color-scheme`).
       - Connects WebSocket to `ws_path` (`/api/account/pi/login/ws?attempt=...`), handles `hello`/`out`/`exit`/`gap`/`closed`/`ping` -> `pong`, forwards `term.onData` as `{"t":"in","data":...}`, `ResizeObserver` / `FitAddon.fit()` debounced as `{"t":"size","rows":...,"cols":...}`.
       - Displays an inline guidance banner (e.g. *"Run `/login` inside Pi to sign in to any provider, then exit or click Verify & Save"*), live status pill, **Verify & Save** button (`POST /api/account/pi/login/complete`), and **Cancel** button (`POST /api/account/pi/login/cancel`).
       - Tears down `Terminal.dispose()`, `ResizeObserver.disconnect()`, and WebSocket cleanly on close, completion, or `destroy()`.
     - **Client-side choices cache invalidation:** Call `window.WsSettingsCombobox?.invalidateChoices()` (matching `dashboard-settings-account-runtime.js:107, 127, 403, 645`) whenever Pi runtime install settles (`installing` transitions `true -> false` or install succeeds), Pi runtime uninstall succeeds, Pi PTY login reaches `succeeded`, or a Pi slot is switched/removed, so switching to `Settings -> Tiers` sees fresh `/support/choices` without reloading `/settings`.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-account.js`:
     - Mount `pirpcGroup = window.WsSettingsAccountPiRPC ? window.WsSettingsAccountPiRPC.create(ctx) : null` after `runtimeGroup`, wire `applyStatus`, `closeEditors`, `focus("pi-rpc")`, and `destroy`.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-account.css`:
     - Add compact borderless tonal styles for `.ic-pirpc`, `.acc-pi-prog` (`transform-origin: left center; transform: scaleX(var(--p, 0));`), and `.acc-pty-backdrop` / `.acc-pty-modal` / `.acc-pty-term` using canonical `--wb-*` and `--z-modal` tokens and **zero `:has()` selectors**.

3. **Topbar Account Switcher, Harness Badges & Tier/Settings Ordering:**
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard.css`:
     - Add `.hb[data-h="pi-rpc"]` right alongside `.hb[data-h="pi"]` (`lines 221-222`, displaying `"Pi"` with consistent badge styling so New Session, Harness Switch, rail rows, and Tier tabs never render `"??"`).
   - `agents-plugin-tool/internal/wsdashboard/assets/usage.css`, `usage-charts.js`, `usage-popover.js`:
     - Add `.up-hb[data-k="pi-rpc"]` in `usage.css`.
     - Keep `pi-rpc` out of quota `DEFS` / `activeGroupDefs()` / `inactiveSlotsList()` (`usage-charts.js:2-7, 104-113, 567-574`) by placing `{ k: "pi-rpc", short: "Pi", name: "Pi", harness: "pi-rpc", group: "", noQuota: true }` in a separate non-quota badge lookup (or gating `activeGroupDefs()` / `inactiveSlotsList()` on `!d.noQuota`), so `defForHarness("pi-rpc", "")` resolves for `drawSessions()` without appending `"No curve: Pi"` notes (`usage-charts.js:617-619`) or creating no-cache overlays.
     - In `buildSlotPills()` (`usage-popover.js:141-146`) and `applyVisualState()` (`usage-charts.js:629-672`), skip `setPreview` binding and chart/row dimming whenever the harness/def is non-quota (`noQuota`), so hovering or focusing inactive Pi slot pills never applies `is-dim` to Claude/Codex/Agy chart series, pins, or rows.
     - In `usage-popover.js` `drawGroup1` (`lines 368-420`), render a compact `Pi` row when `switcherOn && slotsOf("pi-rpc").length > 0 && (accountData?.pi_runtime?.installed || slotsOf("pi-rpc").length > 1)` displaying the `Pi` badge, name, `buildSlotPills("pi-rpc")` (when >= 2 slots) or active slot label + Settings shortcut, and a quiet status note (no fake 5H/7D quota bars), plus `buildRowBanner("pi-rpc")`. Omit `data-k` (or use a non-`.up-hr` row class) on this row so the popover's `.up-hr[data-k]` hover/click listeners (`usage-popover.js:759-763, 813-816`) never call `setFocus`/`toggleLock` for `pi-rpc`.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings.js`:
     - Append `"pi-rpc"` to `tierHarnessOrder` (`line 30`) and `harnessChoices` order (`line 1617`).
     - Update `bucketName(name)` (`line 524`) to return `"Pi"` for `"pi-rpc"`.
   - `agents-plugin-tool/internal/wsi18n/tables/*.json` (all 11 locales: `de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`):
     - Add translated keys for the Pi runtime group, install/reinstall/uninstall controls, progress phases, slot actions, PTY login modal title/hint/buttons, error codes, and topbar Pi label with full key parity across all 11 tables.

4. **Phase 2 Verification:**
   - Extend `agents-plugin-tool/internal/wsdashboard/frontend-settings-account-rework.test.js`:
     - Add `"dashboard-settings-account-pirpc.js"` to the `< 1,000`-line check and `pages.go` dependency-order assertion.
     - Update the purpose-groups assertion to verify `["harness", "runtime", "pirpc"]` (and verify `pirpc` group stays hidden when `pi_runtime` is omitted and visible when `pi_runtime` is present).
     - Add unit tests verifying:
       - `dashboard-settings-account-harness.js` filters out `"pi-rpc"` from Group 1 even when `status.slots["pi-rpc"]` is populated.
       - Pi runtime install/progress/error/uninstall (holdBar) state transitions, `POST /api/account/pi/runtime/*` calls, and `WsSettingsCombobox.invalidateChoices()` invocation on install settlement, uninstall, login success, and slot switch/remove.
       - Pi multi-slot switch, inline rename, remove (holdBar), and ephemeral PTY login modal open -> WS `hello`/`out`/`in`/`size` -> `complete` / `cancel` -> cleanup.
       - Zero `:has()` in `dashboard-settings-account.css`.
   - Add/extend usage popover frontend test (`usage*.test.js`) verifying `pi-rpc` multi-slot switcher pills and active-session slot rows render cleanly, produce zero `"No curve: Pi"` notices, and do not dim or lock Claude/Codex/Agy charts/rows on Pi pill/row hover or focus.
   - Run:
     - `node --test agents-plugin-tool/internal/wsdashboard/frontend-settings-account-rework.test.js` (and any touched frontend `*.test.js` files)
     - `go test -race ./internal/wsi18n/...`
     - `go test -race ./internal/wsdashboard -run 'TestDashboardAssetSourceAssembly|TestAccount|TestPiAccount|TestPiLogin|TestPiRuntime|TestSettingsPage'`
     - `go test -race ./internal/wsagent -run 'TestPiRPC|TestSupport|TestCommonCodeHasNoHarnessNameBranch|TestHarnessNameScan'`
     - `GOOS=windows go vet ./...`
