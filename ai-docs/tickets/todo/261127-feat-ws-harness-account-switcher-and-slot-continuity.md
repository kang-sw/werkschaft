---
title: "하네스 계정 변경 프레임워크·상단 원클릭 스위처 및 장기 세션 연속성"
related:
  261113-feat-ws-dashboard-harness-auth-slots: extends (builds on Orca (131)=a per-slot config_dir isolation with topbar 1-click switcher UX, werk zero-restart switching, and hybrid 2a->2b forever-session continuity; 261113 untouched)
  261127-feat-ws-werk-usage-metrics-and-openai-codex-unification: companion (usage metrics per OpenRouter model and workspace-matched OpenAI/Codex card unification)
---

# Harness account switching framework, topbar 1-click switcher, and forever-session continuity

## Background

The dashboard needs a unified framework for registering multiple account slots per harness, switching the active account cleanly from the topbar usage popover, and continuing long-lived sessions across account switches (such as when a weekly or 5-hour quota window is exhausted mid-task).

`261113-feat-ws-dashboard-harness-auth-slots` captured the initial Orca `(131)=a` direction for per-slot isolated config homes (`CODEX_HOME`, `CLAUDE_CONFIG_DIR`) targeting new sessions only. It left open how existing forever-sessions preserve identity and conversation context across a switch, how the built-in `werk` harness switches accounts without restarting, and how the topbar usage popover stays compact alongside `/settings?section=account`. This ticket settles those decisions for backlog implementation without modifying `261113`.

## Decisions

- **D1 — Role-separated topbar UX (`(1772)=a`).** The `.usage-toggle` popover (`assets/usage.js`, `assets/usage.css`, `assets/dashboard-rail-popovers.js`) renders usage charts, per-harness active account slot 1-click switcher controls, and a shortcut link to `/settings?section=account`. Full credential onboarding and configuration—OAuth login, API key registration, background-usage consent prompts, voice keys, and local endpoints (`account_routes.go`, `WsSettingsAccount` in `assets/dashboard-settings-account.js`)—remain on `/settings?section=account`. This preserves the compact `261115-feat-ws-dashboard-settings-in-usage-strip` usage popover and `.tu` topbar strip readability.
  - *Rejected:* Embedding OAuth login, API-key forms, or consent banners inside `.usage-toggle`, which crowds the compact header bar.
- **D2 — Per-slot isolated `config_dir` + hybrid forever-session continuity on Stop+Restart (`(1773)=a`).**
  - **`werk` (`werkBackend`, `wscred` `openai_store.go`):** `openAIState.Accounts` (`map[client_id]json.RawMessage`), `openAIState.Active`, and `openAIMu` already support multi-workspace SIWC registrations. Switching accounts updates the active (or session-pinned) `ClientID` in-process via a new `SetActiveOpenAIAccount` store method and `/api/account/*` route with **zero process restart**; any in-flight turn completes on its already-bound `ClientID`, and the next turn picks up the newly selected `ClientID`.
  - **External CLIs (`codex`, `claude`, `agy`):** Long-lived child processes managed by `wsholder` (`codex app-server`, `claude -p --input-format stream-json`, `agy acp`) cannot swap auth mid-process. Pure in-place swapping of shared default credential files is rejected because concurrent sessions/terminals overwrite rotating refresh tokens, prevent parallel multi-account runs, and amplify Windows AhnLab V3 / file-lock contention (`261113` Orca `(131)=a`). Instead, isolate per-slot `config_dir` values (`CODEX_HOME`, `CLAUDE_CONFIG_DIR`, etc.) passed via `StartSpec.Env` and bound to the conversation via additive slot-affinity metadata on `wsagent.Record`.
  - **Stop+Restart continuity for an existing forever-session (`2a` -> `2b`):** When switching an active external-CLI forever-session to Slot B and performing Stop+Restart:
    1. **Path `2a` (primary when upstream permits):** Copy/migrate the session transcript (e.g., `CODEX_HOME/sessions/.../rollout-...-<id>.jsonl` or `CLAUDE_CONFIG_DIR/projects/.../<id>.jsonl`) into Slot B's `config_dir` and attempt native `--resume` / `thread/resume` using `Record.SessionID`.
    2. **Path `2b` (automatic fallback on upstream cross-account resume refusal):** Rotate cleanly through `rotateExternalLead` (`agents-plugin-tool/internal/wsagent/compaction.go:803-894`), clearing `Record.SessionID` / `Record.SessionFile` and starting a fresh native thread on Slot B with `<ws-restore>` checkpoint handoff inside the **exact same permanent `ws` session** (`Record.ReplyID`, `Record.WSKey`, dashboard ring history, and worktree preserved).
- **D3 — Two-stage incremental rollout (`(1774)=a`).**
  - **Stage 1 (Phases 1–2):** Shared account slot registry + topbar 1-click switcher UI + **`werk`** (OpenAI SIWC multi-account zero-restart switching + OpenRouter) + **`codex`** (`CODEX_HOME` per-slot isolation, fixing global `os.Getenv("CODEX_HOME")` reads such as `codex_choices.go`, plus the empirical cross-account `thread/resume` spike determining `2a` vs `2b`).
  - **Stage 2 (Phase 3):** Empirical Windows CLI spike for **`claude`** (verify whether Windows Claude CLI stores OAuth strictly under `%CLAUDE_CONFIG_DIR%\.credentials.json` vs `.claude.json` or Windows Credential Manager, and whether cross-account `--resume` works) and **`agy`** (verify Windows auth storage and whether `--gemini-dir` isolates auth cleanly while strictly respecting the ToS ban on extracting third-party OAuth or Keychain tokens from `261119`/`261122`), then integrate both into the shared slot framework.
- **D4 — Capture to `todo/` backlog (`(1782)=a`).** Scope this design as an actionable `todo/` feature ticket rather than immediate in-flight release work.
- **D5 — Compatibility Tier T0 (transparent).** Additive slot-affinity fields on `wsagent.Record` / `LeadOptions`, additive active-slot / account-list fields on `/api/account/status`, and additive topbar switch endpoints preserve wire and on-disk compatibility: older daemons keep unknown JSON fields on `Record` intact and older clients ignore extra status fields without error.
- **D6 — Queued account switch applied at compaction / `/clear` boundary via forced process cold-restart (`(1798)=a`).**
  - **Deferred non-disruptive queuing:** Add an additive T0 field (`pending_account_slot` / `PendingAccountSlotID` on `wsagent.Record` / `conv` in `agents-plugin-tool/internal/wsagent/record.go`) so a user can queue an account slot switch without interrupting an in-flight turn, taking effect at the next compaction or `/clear` boundary.
  - **No process reuse when a queued switch is pending (per-harness lifecycle):**
    - **`ws` compaction mode (`rotateExternalSession`, `agents-plugin-tool/internal/wsagent/compaction.go:998-1100`):** Already tears down and respawns external CLI processes (`claude`, per-conversation `codex app-server`, `agy`) via `retireCompactionProcess()` (`compaction.go:1102-1116`) -> `startLead` / `startWorker` (`runner_lead_start.go:21`, `runner_process.go:140`). Committing the queued slot between teardown and respawn applies the new slot env (`CLAUDE_CONFIG_DIR`, `CODEX_HOME`, etc.) with zero extra restarts.
    - **`native` compaction mode & manual `/clear`:** Both `native` compaction (`compaction.lead_mode=native` / `compaction.worker_mode=native`, `compactIdle()` -> `/compact` or `thread/compact/start`) and manual `/clear` (`kickInput()` in `agents-plugin-tool/internal/wsagent/runner_conv.go:1919-2300`) currently reuse the running `c.proc` and would keep stale account env/credentials. When `PendingAccountSlotID` is set, explicitly block process reuse at `compactIdle()` / `postCompact` (`compaction.go:673-896`, even in `native` mode) and when intercepting `/clear` in `kickInput()`, forcing cold teardown + fresh respawn (`retireCompactionProcess()` -> commit queued slot -> `startLead` / `startWorker` with new slot env + `<ws-restore>`). Place this hook in `compactIdle()` / `postCompact` and `/clear` handling, **not** `restartIfDue()` (`agents-plugin-tool/internal/wsagent/runner_recovery.go:262-267`, which returns `false` while `compactPending != nil || compactResume`).
    - **Mid-turn native auto-compaction & `resume=false` workers:** Never tear down mid-turn during Claude's internal native auto-compaction (`CompactStart` / `CompactEnd` in `compaction.go:1331-1392` before `TurnEnd`); wait for `endTurn` (`runner_events.go:381`) -> `compactIdle()`. For workers completing natural turn-end auto-hard compaction (`resume=false`), commit the pending slot before idle stop so the next wake spawns with the new slot's env.
    - **`werk` (`ProcessDurable`, `agents-plugin-tool/internal/wsagent/werk_loop.go:71-83`):** In-daemon goroutine resolves `Token` / `PlanToken` per model request (`providerToken` / `BeginOpenAIPlanRequest`), so flipping the active (or queued) slot applies cleanly with no process teardown.

## Constraints

- **Implementation convention manuals:**
  - `ai-docs/manuals/release-compatibility.md` (applies to `agents-plugin-tool/internal/`): maintain T0 compatibility across `Record`, `LeadOptions`, `wscred` store state, and `/api/account/*` payloads.
  - `ai-docs/manuals/performance.md` (applies to `agents-plugin-tool/`): minimize file touches on Windows PCs running AhnLab V3; keep zero file I/O on `.usage-toggle` popover open and `GET /api/account/status` by serving from in-memory state + SSE push; never fan out concurrent `agy -p /usage` probes across slots (use staggered/lazy probes honoring the existing 10-minute debounce / 30s timeout from `261119-feat-ws-agy-plan-usage-display`).
  - `ai-docs/manuals/dashboard-style.md` (applies to `agents-plugin-tool/internal/wsdashboard/assets/`): match dark/light tokens, maintain compact `.usage-toggle` and `.tu` single-row footer height, and preserve responsive accessibility.
  - `ai-docs/manuals/config-settings-ui.md` (applies to `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*` and manifests): update settings catalog, localized `en`/`ko` tables, and asset drift checks if any account settings key or control is added.
  - `ai-docs/manuals/shipped-surface-boundary.md` (applies to `agents-plugin-tool/`): keep daemon/UI account logic free of repo-local assumptions.
- **Security & credential isolation:**
  - Register every per-slot credential/config directory root in `deniedBasePaths` (`agents-plugin-tool/internal/wsdashboard/fs_denied.go:16`) so child `fs.*` tools cannot inspect slot credentials or `.credentials.json` / `auth.json` files outside default env paths.
  - Preserve `wscred` Storage Type 5 invariants: atomic `GenerationPaths` writes, Windows User-SID DACL protection, and single-daemon store locking. Never emit raw tokens or secret file contents in status APIs, errors, or logs.
  - Gate every account-switching API route and dashboard UI control behind `localViewer(r)` + `sameOrigin(r)` + `loopbackPeer` (`loopback_peer` on `accountStatus`, `agents-plugin-tool/internal/wsdashboard/access.go` & `account_routes.go`), rejecting LAN or cross-origin requests.
  - Respect `261119-feat-ws-agy-plan-usage-display` and `261122-feat-ws-builtin-agent-harness` ToS rules: never extract third-party OAuth or Keychain tokens from `agy`.

## Prior Art

- `agents-plugin-tool/internal/wscred/openai_store.go:37-90`: `openAIState` already stores multiple SIWC registrations in `Accounts map[string]json.RawMessage`, tracks `Active`, and exports `OpenAIAccountStatus` (without raw tokens) under `openAIMu`.
- `agents-plugin-tool/internal/wsagent/compaction.go:803-894`: `rotateExternalLead` performs crash-safe hosted lead rotation with explicit `wsaudit.KindCompaction` cut, holder retirement, cleared `SessionID` / `SessionFile`, and `<ws-restore>` checkpoint delivery while preserving `Record.ReplyID` and `Record.WSKey`.
- `agents-plugin-tool/internal/wsdashboard/account_routes.go:76-152`: existing `localViewer` + `sameOrigin` + `loopback_peer` route pattern for `/api/account/*`.

## Phases

### Phase 1: Stage 1a — Empirical `codex` `thread/resume` spike, shared slot registry, and security boundaries

Run the empirical spike for cross-account Codex session continuation and lay down the storage/security foundation required before switching live backends or rendering UI:

1. **Empirical `codex` cross-account `thread/resume` spike (`2a` vs `2b`):**
   - Test with isolated Slot A and Slot B `CODEX_HOME` directories on Windows: start a `codex app-server` thread under Account A, copy `CODEX_HOME/sessions/.../rollout-...-<id>.jsonl` into Slot B's `CODEX_HOME`, and invoke `thread/resume` under Account B.
   - Document whether Codex accepts the copied rollout under another workspace/account (confirming Path `2a`) or rejects it (confirming automatic Path `2b`), plus the exact error/rejection signature needed to trigger fallback safely.
2. **Shared per-slot registry & credential isolation:**
   - Define the daemon's per-harness account slot registry (slot ID, harness, masked display label, isolated `config_dir` path where applicable, active default slot flag) backed cleanly without per-request disk scans, maintaining `wscred` Storage Type 5 atomic writes, User-SID DACL, and single-daemon lock.
   - Update `deniedBasePaths` in `agents-plugin-tool/internal/wsdashboard/fs_denied.go` so all registered multi-slot directories and their `auth.json` / `.credentials.json` files are blocked from child `fs.*` access.
   - Add additive T0 slot-affinity and queued-switch fields (`pending_account_slot` / `PendingAccountSlotID`) on `wsagent.Record`, `wsagent.LeadOptions`, and `wsagent.StartSpec` (`internal/wsagent/record.go`, `runner_create.go`, `backend.go`).
   - **`codex` model-cache note:** `agents-plugin-tool/internal/wsagent/codex_choices.go:22-57` (`CodexBackend.hostedChoices` / `codexChoices`) currently reads global `os.Getenv("CODEX_HOME")` for `models_cache.json`; make `codexChoices` and `models_cache.json` lookup slot-aware when per-slot `CODEX_HOME` is introduced.
3. **Verification:** unit tests covering `deniedBasePaths` slot directory exclusion, T0 round-trip persistence with older/newer `Record` JSON (including `pending_account_slot`), slot-aware `codexChoices` / `models_cache.json` lookup contract, and documented spike findings in `## Result`.

### Phase 2: Stage 1b — `werk` zero-restart switch, `codex` isolation + `2a`/`2b` continuity, and topbar 1-click switcher

Implement Stage 1 backend switching for `werk` and `codex` alongside the role-separated topbar switcher UI:

1. **`werk` zero-restart account switching:**
   - Add `SetActiveOpenAIAccount(clientID string) error` on `wscred.Store` (under `openAIMu` + `mu`, flushing pending refreshes first), plus optional session-level `ClientID` pinning for `werk` sessions.
   - Ensure `werkBackend` (`ProcessDurable`, `werkLoop` in `agents-plugin-tool/internal/wsagent/werk_loop.go:71-83`) resolves `Token` / `PlanToken` (`providerToken` / `BeginOpenAIPlanRequest`) per model request so switching requires no process teardown, leaves any in-flight turn on its original `ClientID`, and picks up the newly selected/queued slot cleanly.
2. **`codex` `CODEX_HOME` isolation, queued boundary cold-restart, & hybrid Stop+Restart continuity (`2a` -> `2b`):**
   - Inject the selected slot's `CODEX_HOME` cleanly via `StartSpec.Env` (preserving default home untouched) and make `CodexBackend.hostedChoices()` / `codexChoices` (`agents-plugin-tool/internal/wsagent/codex_choices.go:22-57`) and session helpers slot-aware rather than reading global `os.Getenv("CODEX_HOME")`.
   - Apply queued `PendingAccountSlotID` switches at compaction or `/clear` boundaries without process reuse:
     - In `ws` compaction mode (`rotateExternalSession`, `compaction.go:998-1100`), commit the queued slot between `retireCompactionProcess()` and `startLead` / `startWorker` so the respawned process inherits the new slot env with zero extra restarts.
     - In `native` compaction mode (`compactIdle()` / `postCompact`, `compaction.go:673-896`) and on manual `/clear` (`kickInput()`, `runner_conv.go:1919-2300`), block `c.proc` reuse when `PendingAccountSlotID` is set and force cold teardown + fresh respawn (`retireCompactionProcess()` -> commit queued slot -> `startLead` / `startWorker` with new slot env + `<ws-restore>`). Do not hook `restartIfDue()` (`runner_recovery.go:262-267`, which returns `false` while `compactPending != nil || compactResume`).
     - Defer any teardown during mid-turn native auto-compaction (`CompactStart`/`CompactEnd` before `TurnEnd`) until `endTurn` -> `compactIdle()`; for workers finishing natural turn-end auto-hard compaction with `resume=false`, commit `PendingAccountSlotID` before idle stop so the next wake spawns with the new slot env.
   - On Stop+Restart (or boundary cold-restart) of a `codex` forever-session after slot change: execute Path `2a` (migrate transcript to target slot's `CODEX_HOME` and attempt `thread/resume` when Phase 1 confirms viable), automatically falling back to Path `2b` (`rotateExternalSession` with `<ws-restore>` checkpoint inside the same `ReplyID` / `WSKey` / ring / worktree) whenever upstream refuses or transcript migration fails.
3. **Protected HTTP routes & role-separated topbar UI:**
   - Add `localViewer` + `sameOrigin` + `loopbackPeer`-gated slot selection / switch routes under `/api/account/*` (supporting immediate Stop+Restart or queuing `pending_account_slot` for the next compaction / `/clear` boundary, and returning fixed error codes, never raw credentials).
   - Update `.usage-toggle` popover (`assets/usage.js`, `assets/usage.css`, `assets/dashboard-rail-popovers.js`) to display per-harness active account slot badges, 1-click slot switch actions (triggering instant update for `werk` or queued boundary switch / guided Stop+Restart for hosted `codex` leads), and a direct link to `/settings?section=account` without adding login/API-key/consent controls to the popover or bloating `.tu` strip height.
   - Serve slot status strictly from memory + SSE updates (zero disk I/O on popover open or `GET /api/account/status`), updating localization tables (`en.json`, `ko.json`) cleanly.
4. **Verification:** unit tests for `SetActiveOpenAIAccount` concurrency/in-flight turn isolation and `werk` per-request `Token`/`PlanToken` resolution without teardown, `codex_choices` per-slot `models_cache.json` resolution, queued `PendingAccountSlotID` cold-restart tests (`ws` rotation commit, `native` `compactIdle()`/`postCompact` forced cold-restart without `c.proc` reuse, `/clear` in `kickInput()`, mid-turn `CompactStart`/`CompactEnd` deferral to `endTurn` -> `compactIdle()`, and worker `resume=false` pre-idle commit), `2a` success and `2b` `rotateExternalSession` fallback paths, route access-control tests (`localViewer` / `sameOrigin` / `loopbackPeer`), and JS/browser tests verifying topbar popover layout in dark/light and `.tu` modes.

### Phase 3: Stage 2 — Empirical Windows CLI spike & integration for `claude` and `agy`

Extend the verified Stage 1 slot framework to `claude` and `agy` after resolving Windows auth-storage and quota-probe behavior empirically:

1. **Empirical Windows `claude` and `agy` spikes:**
   - **`claude`:** Verify on Windows whether Claude Code stores OAuth tokens strictly inside `%CLAUDE_CONFIG_DIR%\.credentials.json` or spills into `~/.claude.json` / Windows Credential Manager, and test whether copying `CLAUDE_CONFIG_DIR/projects/.../<id>.jsonl` enables cross-account `--resume` (`2a`) or requires `2b` `rotateExternalSession`.
   - **`agy`:** Verify Windows `agy` credential storage behavior and whether `--gemini-dir` isolates accounts without inspecting or extracting third-party OAuth/Keychain tokens (`261119`/`261122` ToS rule).
2. **Backend integration & staggered quota probing:**
   - Wire `claude` (`CLAUDE_CONFIG_DIR` per-slot isolation + `2a`/`2b` Stop+Restart continuity + queued `PendingAccountSlotID` cold-restart at compaction / `/clear` boundaries with mid-turn `CompactStart`/`CompactEnd` waiting for `endTurn` -> `compactIdle()`; audit `claude_lead_plugins.go` / `plan_usage.go` / `fs_denied.go` for any remaining single-home assumptions) and `agy` (slot mechanism verified in step 1 + queued boundary cold-restart) into the shared slot registry and topbar switcher.
   - Enforce staggered/lazy `agy -p /usage` probing across slots (never concurrent fan-out; honor the existing 10-minute debounce and 30s timeout with process-tree cleanup).
3. **Verification:** unit and mock-CLI tests for `claude` and `agy` slot env isolation, queued `PendingAccountSlotID` compaction / `/clear` cold-restart and mid-turn native auto-compaction deferral, `2a` vs `2b` fallback transitions, staggered `agy` probe serialization, and `deniedBasePaths` protection for any new slot directories.
