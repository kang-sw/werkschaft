---
title: "컴패니언 에이전트 디렉터: 리드 비인지 경량 사이드카, 캐릭터 페르소나, 2단 설정(패널 토글 + 톱니 플로팅 모달)"
parent: 261122-epic-werkschaft-lead-companion
related:
  261121-research-werkschaft-lead-companion-rig: two-speed AI architecture
  261119-feat-ws-config-git-sync: companion keys are classified SYNC in its Appendix A; the synced layer lands there, not here
  261122-feat-ws-agent-turn-semaphore: Phase 2 honors its per-harness slot when landed (drop on no slot); not a prerequisite
  261118-refactor-ws-harness-name-branches: Phase 2 per-harness one-shot argv follows its no-harness-name-branch rule; not a prerequisite
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 822722363533d39a
sage-review-design-reviewed: 822722363533d39a
---

# Companion Agent Director: Lead-Unaware Sidecar, Character Persona, Two-Tier Settings

## Background

The 0-token rules engine (`261122-feat-ws-companion-shell-engine`,
`261122-feat-ws-companion-engine-v2`, both done) drives the companion from
deterministic HUD and rail events. The owner wants an optional second mode in
which a separate lightweight AI reads the recent tool calls and current
context of the visible session, decides what the companion does, and writes
short comments in the character's own voice taken from a per-character
persona prompt in the pack. The lead agent is completely unaware of the
companion: it gets no tool, no prompt text, and no persona.

The owner also split companion settings in two (2026-11-22 18:05 KST): the
dashboard settings panel keeps only "enabled" and "mode (rules | agent)";
everything else lives in a dedicated floating modal opened from a gear that
appears under the resize grip when the character is hovered, because
customization settings are unlike workflow settings. The companion is one
global instance, so its settings live only in the global layer (and the
synced layer once `261119-feat-ws-config-git-sync` lands).

## Decisions

1. **Sidecar only; the lead is unaware ((65)=a, owner 2026-11-22).** The
   director is a daemon-side sidecar. No `ws/emotion` MCP tool, no `lead-tool`
   director value, no persona text injected into any lead or worker prompt,
   manual, or `User preferences`. Rejected: a lead-driven `ws/emotion` tool -
   it spends lead tokens and couples workflow prompts to a visual feature.
   Former queue item (48) (precedence between `lead-tool` and sidecar) is
   withdrawn: with one AI director there is nothing to arbitrate beyond
   Decision 6.
2. **Event-triggered cadence with user-tunable limits ((66)=c, owner
   2026-11-22; supersedes former (47)).** The sidecar runs only on triggers:
   a lead turn end, a tool error, a commit or merge, a new board ask, and
   long idle (no activity for 10 minutes, once per idle stretch). A default
   30 s cooldown and a default hourly cap of 20 calls bound it; both are
   global config keys shown in the modal. A trigger inside the cooldown or
   over the cap is dropped, not queued. The cap is a rolling 60-minute window
   counting only calls actually spawned; dropped triggers are silent (counted
   in the daemon debug log, nothing shown to the user).
3. **Settings keys (announced default; no companion key has shipped).**
   `e79cf6399`, which added the companion keys, is in no release tag (latest
   tag v0.50.8, `git tag --contains e79cf6399` is empty), so the reshape has no
   released consumer. Develop-build configs holding a removed value fall back
   to the manifest default with a `config.get` warning
   (`mcp/dashboard_settings_config.go:163-170`,
   `mcp/config_manifest.go:559-566`).
   - `dashboard.companion_enabled` - boolean, default `false` (both `ws` and
     `wsflow`). Panel toggle.
   - `dashboard.companion_director` - enum `rules | agent`, default `rules`.
     Panel choice. `sidecar` and `lead-tool` are removed.
   - `dashboard.companion_mode` (`off | visual | full`) is removed; its role is
     `companion_enabled`. `full` had no implementation behind it.
   - `dashboard.companion_pack`, `dashboard.companion_preset` - unchanged
     values, moved to the modal.
   - New `dashboard.companion_agent_cooldown_seconds` - integer, min 10, max
     3600, default 30; and `dashboard.companion_agent_hourly_cap` - integer,
     min 1, max 120, default 20. Modal only.
   - All six keys are global-only through `manifestGlobalOnlyKeys`
     (`mcp/dashboard_config.go:61-71`), mirrored across the three dashboard
     manifests with identical keys.
4. **Panel/modal split via a catalog `surface` field (technical decision).**
   A settings-catalog row may carry `"surface": "companion-modal"`; the
   settings panel skips such rows and the companion modal renders them. Every
   key keeps exactly one catalog row and its i18n strings, so
   `Catalog.Compare` (`wsconfigservice/catalog.go:93-112`) and inspectability
   are unchanged. Rejected: a section value outside the panel's section list -
   it needs the drift rule at `mcp/dashboard_settings_ui_catalog_test.go:80-83`
   bent and hides the keys from the catalog's section model.
5. **Gear and floating modal (owner UX; technical details decided here).** A
   gear button is added inside `.ws-companion-hit` directly under the resize
   grip, with the grip's hover/focus reveal rules. It opens a non-blocking
   `<dialog>` via `show()`, positioned beside the companion and clamped to
   the viewport, closed by Escape, the close button, or a second gear click.
   Contents: pack and preset choice, the selected pack's persona (read-only:
   name and voice summary), agent cooldown and hourly cap, and a reset-size
   action. Writes use the existing `POST /api/config/set {scope:"global"}`
   path the right-click menu already uses
   (`assets/dashboard-companion-menu.js:37-50`), gated by `data-sysopen` and
   the server's TrustedWrite check; settings writes need no new server
   route (the focus report of Decision 8 is the only new route). Character size stays
   in `localStorage` (`ws.companion.size`) - it is a per-browser view setting.
6. **Command event and stage override (technical decision).** A new
   `companion-command` rail-stream event carries `{sessionId, scenario?,
   expression?, gesture?, prop?, fx?, quip?, ttl_ms}`. It is broadcast; the
   shell acts only on the visible session's commands. The browser validates
   every ID against the active pack's dictionaries and scenario registry,
   applying the pack's `fallback` chains; an unknown ID drops that field. The
   stage gains `runner.direct(beat, now, ttl_ms)`: the directed beat overrides
   the rules engine until the TTL ends (clamped 1-15 s), then the rules engine
   resumes; a newer command replaces an older one. Quips are rendered with
   `textContent`, at most 140 characters, in an auto-dismissing bubble that
   never blocks pointer events.
7. **Persona is pack data (technical decision).** `pack.json` gains an
   optional `persona` object: `name` (<= 40 chars), `voice` (<= 800 chars,
   speech style and character notes), `quipLanguage` (optional BCP-47 tag).
   Validated like `autoMatch` in `companion_pack_schema.go`; oversize or
   malformed persona drops the field, not the pack. The prompt quotes persona
   text as data inside a delimited block, never as instructions to obey over
   the system part.
8. **Sidecar runtime (technical decision).** Active only when
   `companion_enabled` is true and `companion_director` is `agent`;
   otherwise no digest is built and no process is spawned. The digest is
   memory-only, built from `wslive.State` via `daemonLiveSlot.Publish`, the
   RAM-pinned rail ring (`RingAppended`), and `OpenAskIDs` diffs, for the
   visible session only. The daemon has no visibility signal today (the
   shell opens `/rail/events` without a session id,
   `assets/dashboard-stream.js:406-407`; visible-iframe and `auto` pack
   resolution live in the browser, `assets/dashboard-companion-packs.js:93,132`),
   so the shell reports its visible session and resolved pack ID to the
   daemon on every session switch through a small authenticated `POST` on
   the companion route family. The daemon keeps one focus value, last report
   wins, so several open browsers still produce one digest and one call per
   trigger; Decision 6's per-shell `sessionId` filter keeps each window
   correct. Each report is tied to the reporting shell's live
   `/rail/events` connection (the stream hands the shell a per-stream token
   as an SSE event, echoed in the `POST`) and dropped when that stream ends;
   the daemon keeps one report per live stream and uses the newest live
   one, so closing one window falls back to another visible window; the shell
   sends a clear while `document.visibilityState` is hidden (including a
   window hidden to the tray) and reports again when visible. No focus (no
   viewer) → the sidecar does nothing. Triggers
   from other sessions are ignored. Digest bound: the last 20 rail entries of the
   focused session (kind, tool name, one-line summary, error flag), its turn
   state and open ask count, each entry cut to 200 characters, 4,000
   characters total. No in-process LLM path exists, so each call spawns
   one harness CLI process using the session's harness at the `small` tier
   (`wsconfig.ResolveAgentTierForHarness(opts, "small", harness)`), with a
   20 s timeout and process-tree kill (`execjob.KillTree`, as
   `plan_usage_agy.go:530-537` does). The process is isolated: no tools, no
   MCP servers, no settings sources, plugins, or hooks, and a neutral working
   directory, following the worker launch pattern
   (`wsagent/claude.go:207-213`: `--setting-sources ""`,
   `--strict-mcp-config`, `--tools`, `--permission-mode dontAsk`), because
   the digest carries untrusted tool output and side effects would happen
   before reply validation. Per-harness one-shot argv lives on
   `BackendRegistration` or a narrow `Backend` method, never a harness-name
   branch in common code (`261118-refactor-ws-harness-name-branches`
   Constraints); a harness without a usable one-shot mode drops the trigger
   and rules mode continues, with no fallback to another harness's account.
   Once `261122-feat-ws-agent-turn-semaphore` lands, a sidecar call takes a
   slot in the shared per-harness count for its duration and the trigger is
   dropped when no slot is free (Decision 2's drop-not-queue rule); until
   then there is no slot check. Commit/merge detection mirrors the browser's
   `directCommit` pattern (`assets/dashboard-companion-relay.js:13-15`) in Go.
   The prompt holds the persona block,
   the quip language (`persona.quipLanguage`, else `workflow.lang`, else
   English), the digest quoted as data like the persona, and the allowed
   IDs (the active pack's dictionaries plus the built-in scenario IDs, read
   from one source both the browser and Go embed, not a hand-kept copy); the
   reply must be one JSON
   object matching Decision 6's payload, else it is discarded. No persisted
   state is added.
9. **Compatibility tiers.** `companion-command` stream event: T0 (an older
   shell ignores unknown event types). `persona` pack field: T0 (unknown pack
   fields are preserved and ignored, `companion_pack_schema.go:117-155`).
   New keys, the `surface` catalog field, and the shell focus `POST`
   (Decision 8; an older daemon answers 404 and the shell ignores it): T0. Removing `companion_mode`
   and the `sidecar`/`lead-tool` values: no released consumer (Decision 3);
   once this ships, later changes to these keys follow
   `release-compatibility.md` normally.
10. **Sync layer belongs to `261119-feat-ws-config-git-sync`.** The six
    companion keys are classified SYNC in that ticket's Appendix A; until it
    lands they live in the global layer only.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/config-settings-ui.md` - manifests, catalog, i18n tables
  change together; document the new `surface` field in its checklist.
- `ai-docs/manuals/shipped-surface-boundary.md` - prompt text the sidecar
  sends and any shipped strings stay generic.
- `ai-docs/manuals/release-compatibility.md` - Decision 9 verdicts.
- `ai-docs/manuals/performance.md` - digest memory-only; count file
  operations and process spawns per trigger; off/rules mode adds none.
- `ai-docs/manuals/companion-pet-rigging.md` - its config and director
  lines already describe this design; keep them true to what lands.

Key files (from the 2026-11-22 fact-finding; line numbers drift):
- Settings: the three `dashboard-config-manifest.json` files plus
  `python3 agents-plugin-tool/scripts/generate-package-assets.py`;
  `mcp/dashboard_config.go`; `wsdashboard/companion_config.go`;
  `mcp/dashboard_settings_config.go:158-174` (string-only parsing - needs
  bool and int); `wsdashboard/assets/dashboard-settings-catalog.json`;
  `assets/dashboard-settings.js`; 11 `wsi18n/tables/*.json`.
- Shell: `assets/dashboard-lifecycle.js:761-777` (gate tests `visual|full`);
  `assets/dashboard-companion-stage.js:95-174` (grip, mount);
  `assets/dashboard-companion.css:30-45`; `assets/dashboard-companion-menu.js`;
  `assets/dashboard-companion-scenarios.js:136-179` (runner);
  `assets/dashboard-stream.js:417-424`; `wsdashboard/sse.go:586-594`.
- Packs: `wsdashboard/companion_pack_schema.go`,
  `wsdashboard/companion_pack_store.go`.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Three dashboard manifests, mcp config parsing, wsdashboard config/pack schema/SSE, shell stage/menu/scenario JS, settings catalog, 11 locale tables |
| scope.surface | public-interface | Config keys reshaped and added, `companion-command` stream event, optional `persona` pack field, catalog `surface` field, shell focus-report `POST` route |
| scope.new_public_symbol | yes | `dashboard.companion_enabled`, `dashboard.companion_agent_cooldown_seconds`, `dashboard.companion_agent_hourly_cap`, `companion-command` event, focus-report route on the companion route family |
| scope.new_type_contract | yes | Command payload (Decision 6), `persona` object (Decision 7) |
| scope.test_surface | existing | mcp/dashboard_config_test.go, mcp/dashboard_companion_test.go, mcp/dashboard_settings_ui_catalog_test.go, wsdashboard/companion_config_test.go, frontend-settings.test.js |
| complexity.reuse_points | confirmed | Global-only registration (mcp/dashboard_config.go:61-71), config set route used by the companion menu (dashboard-companion-menu.js:37-50), rail stream companion event (sse.go:586-594), scenario runner (dashboard-companion-scenarios.js:136-179) |
| complexity.side_effect_risk | moderate | Phase 2 spawns harness processes on triggers; Phases touch config, stream, and shell together |
| risk.correctness | moderate | Limiter and TTL override timing; rules engine must resume cleanly |
| risk.fit | moderate | Panel/modal split must keep every key inspectable in the catalog |
| risk.test | moderate | Process spawn path needs an inertness test and per-trigger spawn/file-op counts |
| risk.security_or_contract | high | Model output drives the shell (ID validation, textContent quips) and persona text enters a prompt (quoted as data) |

## Phases

### Phase 1: Two-tier settings, gear modal, command event, stage override

- **Goals:**
  1. Reshape the keys per Decision 3 across manifests, global-only
     registration, `CompanionConfig`, bool/int parsing, the shell gate, the
     `companion` stream payload, catalog, and i18n.
  2. Add the catalog `surface` field; the panel shows only `enabled` and
     `director`; document the field in `config-settings-ui.md`.
  3. Gear button and floating modal per Decision 5.
  4. `companion-command` event, broadcast with `sessionId`, and
     `runner.direct` with TTL per Decision 6, plus the quip bubble.
  5. Optional `persona` pack field with validation per Decision 7; the modal
     shows it.
  6. Re-check `companion-pet-rigging.md` against what landed (its config and
     director lines were rewritten to this design when the ticket settled).
- **Verification:** targeted Go tests for config parsing, global-only
  registration, catalog drift with the `surface` field, persona validation,
  and command event routing; frontend tests for the panel filter, modal
  open/close/write, ID validation with fallback, TTL expiry, and quip
  `textContent` rendering; a headless Chromium capture of the gear and modal.

### Result (588dfc45f) - 2026-11-22

- **Landed** (6683c2568, 8c87e23da, 114721b43, b0a104fda, 1a6c3cfeb,
  588dfc45f; develop merged in at 8999d97e6 on the lead's instruction):
  - Six global-only keys per Decision 3 (`companion_enabled` bool,
    `companion_director` rules|agent, pack, preset,
    `agent_cooldown_seconds` 10–3600/30, `agent_hourly_cap` 1–120/20). They
    are typed bool/int with a per-key fallback to the default; project, repo,
    and session layers never apply. Covered across manifests, `CompanionConfig`,
    the stream payload, catalog, and i18n.
  - Catalog `surface: "companion-modal"`; the panel's `sectionEntries` skips
    those rows. The shell embeds them as `window.wsCompanionModalRows`
    (`companionModalRows`) and keeps their strings through
    `wsi18n.ScriptExceptKeeping` + `shellSettingsStrings`. Documented in
    `config-settings-ui.md`.
  - Gear (`.ws-companion-gear`, directly under the resize grip, hover/focus
    reveal) opens a non-modal `<dialog>` (`dashboard-companion-settings.js`)
    beside the character, clamped to the viewport. Pack, preset, cooldown,
    and cap write to `POST /api/config/set` at the global scope (integers as
    JSON numbers), only on `data-sysopen` pages. It closes on Escape, a second
    gear click, or the close button.
  - `companion-command` rail event (newest-only hub, `sessionId`): the shell
    stream dispatches `ws-companion-command`, and the stage acts only for the
    shown session. `directedCommand` validates scenario, expression, gesture,
    prop, and fx IDs through the compositor's fallback chains (props and fx
    also from the shared pack, fx also from the built-in `fxDefaults`) and
    drops only an unknown field. `runner.direct` plays at priority 99 with
    the TTL clamped to 1–15 s and the last step held. Drag still wins, and a
    newer command replaces an older one. The quip is folded to at most 140
    code points and rendered with `textContent` in a `pointer-events: none`
    bubble.
  - Optional pack `persona` (validated server-side; `personaOf` normalizes
    it client-side), shown in the modal after the preset row.
  - `companion-pet-rigging.md` re-checked: its config and director lines
    match; added a line on directed commands.
- **Verification** (on the merged tree, head 588dfc45f):
  - `node --test frontend-companion.test.js frontend-settings.test.js`:
    34/34 pass. Covers the panel filter, the modal's open/close/write,
    read-only, and refused input, ID validation with fallback, TTL expiry,
    and the quip `textContent`.
  - `node --test *.test.js`: 189/192. The 3 failures ("closed opener
    clicks…", "lifecycle post coalesces…", "idle CSS bounds…") also fail
    without this ticket's changes (verified at 6683c2568).
  - `go test ./internal/wsi18n ./internal/wsconfig ./internal/wspackage`: ok.
  - `go test ./internal/mcp -timeout 45m -run
    'Companion|DashboardSettingsUI|ShippedDashboardManifests|DashboardConfig|DashboardCompanion|TierRouting'`:
    ok.
  - `go test ./internal/wsdashboard -timeout 30m`: the only failure was the
    stale assembly expectation, fixed in 588dfc45f. The rerun of the asset,
    companion, stream, and page tests is ok.
  - `TestCompanionSettingsBrowser` (headless Chromium, with
    `WS_PLAYWRIGHT_MODULE` set): PASS. Captures are in
    `/tmp/ws-companion-shots-kitty/`: `companion-gear-hover.png`,
    `companion-settings-modal.png`, `companion-directed-quip.png`.
  - `generate-package-assets.py --check`: ok. `go build ./...` and `go vet`
    on mcp, wsdashboard, wsi18n, and wsconfig: ok.
- **Decisions:**
  - The shell page embeds the modal rows instead of loading the settings
    catalog, because it loads no catalog and drops `js.settings.*`.
  - Directed beats sit at priority 99, so drag_dangle (100) still wins and
    rules scenarios cannot replace them.
  - The modal's refresh spares whichever control has focus. A refused or
    failed write reverts every control.
- **Open (lite review, minor):**
  - The newest-only hub coalesces across sessions (benign under Phase 2's
    single focus).
  - `scenario` combined with field overrides patches every step of a
    multi-step scenario.
  - Directed `drag_dangle` and reply-bound scenario IDs are accepted without
    drag or reply state.

### Phase 2: Agent sidecar director

- **Goals:**
  1. Focus report per Decision 8: the authenticated `POST` route on the
     companion route family and the shell reporter firing on every session
     switch with the visible session and resolved pack ID; clear on stream
     end and while the page is hidden; an older daemon's 404 is ignored.
  2. Trigger detection (including the Go port of `directCommit`) and limiter
     per Decision 2 (cooldown and hourly cap read live from config).
  3. Focused-session digest per Decision 8, memory-only, within its bound.
  4. One-shot `small`-tier harness runner per Decision 8: per-harness argv
     on the backend registration, isolation flags, timeout and tree kill,
     drop when the harness has no one-shot mode or (once
     `261122-feat-ws-agent-turn-semaphore` lands, taking a slot while
     running) no free slot; probe each
     harness's one-shot invocation and keep the probe notes at the
     registration.
  5. Prompt assembly with the persona and digest data blocks, quip language,
     and allowed IDs from the single shared scenario-ID source; strict JSON
     validation; emit `companion-command`.
- **Verification:** route tests for the focus report (auth refused without
  a trusted origin, last report wins, cleared on stream end and on a hidden
  page, no focus means inert, shell ignores 404); unit tests for
  the limiter (rolling window boundary), trigger mapping including
  commit/merge detection, digest bound, prompt assembly (persona and digest
  quoted as data, allowed IDs from the shared source), and reply validation;
  a test asserting the one-shot argv carries the isolation flags; an inertness test
  proving rules mode and disabled mode build no digest and spawn nothing;
  per-trigger file-operation and spawn counts reported per `performance.md`;
  one live run on the dashboard with a captured quip.
