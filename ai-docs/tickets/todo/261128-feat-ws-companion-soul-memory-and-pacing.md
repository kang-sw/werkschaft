---
title: "컴패니언 3계층 장기 메모리(Portable Soul / Local Episodes / VRM), 캐릭터성 및 하이브리드 MCP 애니메이션 딜레이 페이싱"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261127-feat-ws-companion-layered-agents-and-naming#phase3
related:
  261127-feat-ws-companion-layered-agents-and-naming: Tier 1 reaction agent, Tier 2 Companion Messenger coordinator, and ws/companion.* surface (Phase 3 prerequisite)
  261126-feat-ws-companion-vrm-engine: VRM 0.x/1.0 runtime, procedural 2-bone IK, 3D props, and expression controller
  261122-epic-werkschaft-lead-companion: parent companion architecture and cross-child invariants
sage-review-design: required
sage-review-completeness: required
---

# Companion 3-Layer Long-Term Memory (Portable Soul / Local Episodes / VRM), Characteristic Sheet & Hybrid MCP Animation-Delay Pacing

## Background

The Werkschaft Companion currently executes bounded-rotation reaction calls via `companionPrompt` (`companion_director.go`), driven solely by transient session-digest rings and an optional static `CompanionPersona` (`name`, `voice`, `quipLanguage`) embedded inside a visual pack's `pack.json` (`companion_pack_schema.go`). While `261127-feat-ws-companion-layered-agents-and-naming` splits live visual reactions (Tier 1) from the Companion Messenger coordinator (Tier 2) and adds `ws/companion.{speak,face,motion}`, three core capabilities remain missing:

1. **No orthogonal long-term memory or portable identity:** Character identity is coupled to visual pack manifests, there is no machine-portable user/companion Soul store (safe for Git sync across work and home machines without binary conflicts or secret leakage), and there is no machine-local episodic recall store scoped by project.
2. **No structured personality/mood continuity or tech-result briefing boundary:** There is no Characteristic Sheet or dynamic mood state machine feeding Tier 1/Tier 2, and no structural separation ensuring heavy engineering work stays pure-technical on engineering agents while the dedicated Companion Co-Lead (`(1737)=a`) briefs results in gap-moe character voice with verbatim technical artifacts preserved.
3. **No interruptible multi-beat pacing:** Discrete `ws/companion.{speak,face,motion}` calls cannot hold natural 1.5–6 s animation/speech beats while remaining immediately interruptible by incoming user messages or glass-knock interactions without polling, idle token burn, or O(N²) context growth.

## Verified Codebase Surface

- `agents-plugin-tool/internal/wsdashboard/companion_config.go:76-91`: `CompanionConfig` holds the 12 existing global companion configuration fields (`Enabled`, `Director`, `Pack`, `Preset`, `Outfit`, `Hair`, `Eyes`, `Attachments`, `Palette`, `AgentCooldownSeconds`, `AgentHourlyCap`, `Sources`) with no Soul selector or Soul directory field yet.
- `agents-plugin-tool/internal/wsdashboard/companion_director.go:88-90,566-601,831-898`: `companionPrompt` constructs the prompt with `<<<PERSONA>>>` and `<<<SESSION>>>` delimiters and runs `c.pool.Execute` in `CompanionModeBoundedRotation` (`companionPoolMaxTurns = 20`, `companionPoolIdleTTL = 5 * time.Minute`, `companionDirectorTimeout = 20 * time.Second`).
- `agents-plugin-tool/internal/wsdashboard/companion_pack_schema.go:46,85-119`: `CompanionPersona` (`name` <= 40 runes, `voice` <= 800 runes, `quipLanguage`) lives inside `pack.json`, coupling persona definition to visual packs.
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-companion.js:1536-1556`: `render` mounts 5 stacked `<section class="cs-group">` groups (`renderCoreGroup`, `renderAvatarGroup`, `renderSourcesGroup`, `renderWardrobeGroup`, `renderAdvancedGroup`) with no Memory / Soul drawer or approval queue UI yet.
- `agents-plugin/dashboard-config-manifest.json:67-176`: declares the 13 existing global `dashboard.*` companion keys (`dashboard.companion_enabled` through `dashboard.companion_sources`, including deprecated `dashboard.companion_mode`).
- `agents-plugin-tool/internal/wsmeta/store.go:18-39` and `agents-plugin-tool/internal/wsdashboard/daemon_start.go:82,330`: `wsmeta.Open` holds a single daemon-owned SQLite connection (`metadata.sqlite`, `locking_mode=EXCLUSIVE`), and `d.server` configures `ReadHeaderTimeout: 10 * time.Second` with no `ReadTimeout`, `WriteTimeout`, or `IdleTimeout` (permitting long-lived or interruptible MCP HTTP calls without server-side stream cutoffs).
- `agents-plugin-tool/internal/wsdashboard/proxy.go:44-54,327-335`, `agents-plugin-tool/internal/wsdashboard/proxy_http.go:231-245`, and `agents-plugin-tool/cmd/ws-mcp/proxy.go:35`: `/mcp` and `/mcp/session` impose no server-side per-tool-call timeout (`proxyDrainBudget` drains idle streams while preserving in-flight calls up to `hostedCallLinger = 2 * time.Minute`; `proxy_http.go` selects on `<-ch`, `<-r.Context().Done()`, `<-s.ended`).
- `agents-plugin-tool/internal/wswhiteboard/board_db.go:22,47-55`: `BoardDB` establishes the daemon's held SQLite handle + full in-memory state cache pattern (`BoardDBCheckpointIdle = 30 * time.Second`, `BoardDBCheckpointWALBytes = 4 << 20`).

## Decisions

- **D1 — Hybrid Tier-1 Pacing (`(151)=a`):**
  - **Chosen:** Three-mode hybrid pacing combining zero-token idle with interruptible bounded bursts:
    1. **0-token idle:** Procedural VRM animations (breathing, typing, glancing, sleepy/doze) run purely in the frontend renderer with zero LLM turns.
    2. **Event / low-frequency active-user wake:** Wake Tier 1 only on qualifying session events or low-frequency active-user triggers (gated by cooldown/hourly caps).
    3. **Bounded active bursts via interruptible MCP animation-delay pacing:** During an active burst, `ws/companion.act` issues the visual/speech command and performs an interruptible `select` wait of **1.5–6 s** (clamped) across: animation timer expiry, incoming user message, glass-knock/touch interaction, or daemon shutdown — zero polling, immediate wake on interruption, and a clean turn end when the burst completes so conversation context never grows O(N²).
  - **Rejected:** `(b)` 24/7 always-on `wait_event` long-blocking loop (continuous idle token burn, O(N²) context accumulation, and client harness tool-timeout risk); `(c)` pure zero-MCP 1-turn JSON script only (cannot handle mid-burst user interruption or active memory recall/save tool calls).
- **D2 — Gap-Moe Dual Tone & Delegated Pure-Tech Worker Reporting (`(152)=a`, cross-referencing `(1737)=a`):**
  - **Chosen:** Enforce a structural split between engineering execution and companion presentation:
    - Casual messenger, phone-check, and ambient interactions use the companion's full persona, Characteristic Sheet, and dynamic mood voice.
    - Heavy engineering work is always delegated to clean engineering workers/leads that communicate in crisp, unadorned technical prose.
    - The dedicated **Companion Co-Lead** hosted session (`(1737)=a`) owns Tier-2 persona briefing and memory curation without touching the user's real coding lead, and presents delegated technical outcomes back to the user in character while **preserving verbatim technical artifacts** (commit hashes, diffs, file links, test results, and review verdicts).
  - **Rejected:** `(b)` Full character voice flattening or rewriting raw engineering output (risks distorting technical precision and auditability); `(c)` user tone-slider only without structural work/briefing separation.
- **D3 — 3-Layer Orthogonal Companion Memory & Embodiment (`(153)=a`):**
  - **Chosen:** Three strictly orthogonal layers:
    1. **Layer 1 — Portable Soul (`~/.ws/companion-soul/<soul-id>/`, or user-configured Git repo path):**
       - `persona.md` (core identity, voice, Characteristic Sheet), `profile.md` (stable user preferences, rapport traits, long-term relationship facts), and `log/YYYY-MM-<machine-id>.jsonl` (append-only per-machine monthly shards).
       - Backed by a daemon RAM hot cache (loaded once at startup/soul switch, delta-appended one JSONL line per write per `performance.md`).
       - Per-machine monthly shards (`YYYY-MM-<machine-id>.jsonl`) guarantee zero-conflict Git sync across machines, alongside ZIP export/import.
       - **2-layer secret/PII guardrail:** (a) strict tool separation (`ws/companion.remember_soul` handles portable personal/rapport memory only, separate from `ws/companion.remember_episode` and isolated from `ws/note.*`); (b) deterministic regex/pattern scrubber inspecting every Soul write for filesystem paths, API tokens/keys, emails, URLs, ticket stems (`YYMMDD-*`), and active project/repo names — downgrading/redirecting project-specific details to Local Episodes or holding suspicious writes in a **Settings approval queue** before persisting to Portable Soul.
    2. **Layer 2 — Local Episodes (`companion-episodes.sqlite` in dashboard state dir):**
       - Single held daemon SQLite handle in WAL mode (`wsmeta/store.go` / `wswhiteboard/board_db.go` pattern), FTS5 full-text search, `project_key` scoping, strictly machine-local and untracked (never included in Git sync or Soul ZIP exports, completely separate from `ws/note.*`).
    3. **Layer 3 — VRM Embodiment:**
       - Swappable `.vrm` packs (`ws-companion-vrm/v1`) and 2D packs (`ws-companion/v1`); `pack.json`'s `persona` (`CompanionPersona`) acts **only** as an optional starter template when initializing a new Soul and never owns or overwrites the Portable Soul.
  - **Rejected:** `(b)` Separate portable SQLite file for Soul (binary Git merge conflicts across work/home machines and opaque secret auditing); `(c)` 1-file-per-entry `traits/<ulid>.md` (triggers Windows antivirus startup scan explosion after years of growth, violating `performance.md`).
- **D4 — Dedicated Companion Co-Lead Ownership (`(1737)=a`):**
  - **Chosen:** The dedicated Companion Co-Lead hosted session (`261127-feat-ws-companion-layered-agents-and-naming` / `(1737)=a`) owns Tier-2 persona briefing, delegated tech-result presentation, and memory curation (`ws/companion.remember_soul`, `ws/companion.remember_episode`, `ws/companion.recall_memory`), leaving the user's primary coding lead completely untouched.
- **D5 — Compatibility Tier Verdict: T0 Transparent (`ai-docs/manuals/release-compatibility.md`):**
  - **Verdict:** **T0 (transparent, patch-safe)**.
  - **Directional evidence:**
    - New Portable Soul files under `~/.ws/companion-soul/<soul-id>/` and `<state-dir>/companion-episodes.sqlite` are untouched and ignored by older daemons.
    - New global-only scalar string config keys `dashboard.companion_soul` (and optional `dashboard.companion_soul_dir`) are stored in `wsconfig`'s `overrides` map and declared in `dashboard-config-manifest.json` using existing manifest fields (never top-level `config.json` members and never new manifest schema fields).
    - Additive MCP tools (`ws/companion.act`, `ws/companion.remember_soul`, `ws/companion.remember_episode`, `ws/companion.recall_memory`) and additive HTTP routes (`/api/companion/soul/*`, `/api/companion/memory/*`) leave all existing `ws/companion.*` / `ws/daemon.*` tools, `dashboard.companion_*` keys, and `pack.json` schemas unchanged; older daemons/clients ignore unknown additions with zero data loss (`mcp/server_dispatch.go`, `mcp/tool_arguments.go`).

## Constraints

- **Implementation convention manuals:**
  - Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
  - Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
  - Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
  - Convention: ai-docs/manuals/config-settings-ui.md (declared for `agents-plugin/dashboard-config-manifest.json`, `dashboard-settings*`, `wsi18n`)
  - Convention: ai-docs/manuals/dashboard-style.md (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
  - Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
  - Convention: ai-docs/manuals/wsflow-mirroring.md (declared for `agents-plugin-wsflow/dashboard-config-manifest.json` when manifest keys are added)
- **Windows AV file-touch budget (`performance.md`):**
  - Portable Soul (`persona.md`, `profile.md`, `log/YYYY-MM-<machine-id>.jsonl`) uses a daemon RAM hot cache loaded once on startup/soul switch; `remember_soul` appends one JSONL line per delta without rewriting existing files or scanning directories on read/recall.
  - Local Episodes (`companion-episodes.sqlite`) follows Storage Type 4 (queried structured history): open once at daemon startup (or lazy first companion-memory enablement) and hold the SQLite connection in WAL mode for the daemon's lifetime, with zero per-call open/close/stat.
  - `ws/companion.act` animation-delay blocking uses in-memory Go channels (`select` on timer / user message / glass-knock / daemon shutdown), incurring zero file touches and zero polling.
- **Strict privacy, IP & note isolation:**
  - Never commit companion memory (`~/.ws/companion-soul/`, `companion-episodes.sqlite`) or user personal logs to any project repository.
  - `ws/note.*` (`ai-docs/ws-notes/`) remains strictly isolated from companion memory; companion recall/save tools must never read from or write into `ws/note.*`.
  - Every Portable Soul write passes through the deterministic 2-layer secret/PII guardrail before touching `log/YYYY-MM-<machine-id>.jsonl` or `profile.md`.
- **Settings UI & manifest parity (`config-settings-ui.md`, `wsflow-mirroring.md`):**
  - Mirror any new `dashboard.companion_soul*` keys in both `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json`, regenerate embedded assets via `python3 agents-plugin-tool/scripts/generate-package-assets.py`, add UI catalog rows in `dashboard-settings-catalog.json`, add `en`/`ko` (and all shipped locale) translations in `internal/wsi18n/tables/`, and verify `Settings|Manifest` parity tests.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `wsdashboard/` (new `companion_soul.go`, `companion_episodes.go`, `companion_memory_routes.go`, `companion_scrubber.go`, edits to `companion_config.go`, `companion_director.go`, `daemon_start.go`, `assets/dashboard-settings-companion.js`), `internal/mcp/` (`ws/companion.act`, `remember_soul`, `remember_episode`, `recall_memory`), `dashboard-config-manifest.json` (ws & wsflow), `wsi18n/tables/`, `dashboard-settings-catalog.json` |
| scope.surface | cross-module | new MCP tools, additive `dashboard.companion_soul*` config keys, new `/api/companion/soul/*` and `/api/companion/memory/*` HTTP routes, SQLite WAL+FTS5 store, Portable Soul JSONL shards, and Settings UI memory drawer |
| scope.new_public_symbol | yes | new MCP tools (`ws/companion.act`, `ws/companion.remember_soul`, `ws/companion.remember_episode`, `ws/companion.recall_memory`), new HTTP handlers, and new daemon Soul/Episodes store types |
| scope.new_type_contract | yes | Portable Soul directory schema (`persona.md`, `profile.md`, `log/YYYY-MM-<machine-id>.jsonl`), Local Episodes SQLite FTS5 schema, Characteristic Sheet + mood state contract, and `ws/companion.act` interruptible wait contract |
| scope.test_surface | new-files | new Go unit tests for Soul shard RAM cache, secret scrubber + approval queue, SQLite FTS5 episodes, interruptible `ws/companion.act` cancellation, settings/manifest drift, and frontend drawer tests |
| complexity.reuse_points | confirmed | `CompanionConfig` (`companion_config.go:76-91`), `companionPrompt` (`companion_director.go:871-898`), `CompanionPersona` (`companion_pack_schema.go:85-119`), `BoardDB` held SQLite WAL pattern (`board_db.go:47-55`), `dashboard-settings-companion.js:1536-1556` |
| complexity.side_effect_risk | moderate | additive stores and tools isolated from `ws/note.*` and primary coding leads; main risks are file-touch discipline under Windows AV, SQLite handle lifecycle on daemon drain, and interruptible MCP cancellation cleanup |
| risk.correctness | moderate | interruptible `select` timing vs `proxyDrainBudget`/`hostedCallLinger`, regex scrubber false-positive/false-negative tuning, and Soul-vs-pack starter template precedence |
| risk.fit | moderate | integrates cleanly atop `261127-feat-ws-companion-layered-agents-and-naming#phase3` (Tier 1 reaction agent, Tier 2 Companion Co-Lead, and `ws/companion.*` command surface) |
| risk.test | moderate | deterministic clock/channel injection required to test 1.5–6 s `ws/companion.act` interruption without flaky sleeps; full FTS5 and scrubber edge-case coverage needed |
| risk.security_or_contract | high | Portable Soul secret/PII scrubber, approval queue gating, `localViewer` + `sameOrigin` HTTP guardrails on Soul ZIP export/import and memory routes, and T0 config/manifest parity |

## Phases

### Phase 1: 3-Layer Orthogonal Storage (Portable Soul Shards + Local Episodes SQLite FTS5), Secret Guardrail & Settings Memory Drawer

Establish the orthogonal storage engine, deterministic privacy guardrail, config keys, and Settings memory management UI:

1. **Portable Soul Store (`~/.ws/companion-soul/<soul-id>/` or `dashboard.companion_soul_dir`):**
   - Implement the RAM hot cache and file layout: `persona.md`, `profile.md`, and append-only per-machine monthly shards `log/YYYY-MM-<machine-id>.jsonl`.
   - Read once on startup / soul selection into memory; append one JSONL line per accepted delta without full-file rewrites or directory rescans.
   - Support Git-sync-friendly non-conflicting shard merges across machines and ZIP export/import (`/api/companion/soul/export`, `/api/companion/soul/import`, gated by `localViewer` + `sameOrigin`).
   - Treat `pack.json`'s `CompanionPersona` strictly as an optional starter template when initializing a brand-new Soul; never let a visual pack overwrite an existing Soul.
2. **2-Layer Secret & PII Guardrail:**
   - Separate `ws/companion.remember_soul` (portable personal/rapport memory) from `ws/companion.remember_episode` (machine-local project episodes) and isolate both from `ws/note.*`.
   - Implement a deterministic scrubber before every Soul write checking for filesystem paths, API keys/tokens, email addresses, URLs, ticket stems (`YYMMDD-*`), and active project/repo names: auto-route project-scoped items to Local Episodes or hold suspicious entries in a pending **Settings approval queue** until the user approves, edits, or discards them.
3. **Local Episodes Store (`companion-episodes.sqlite`):**
   - Open a single daemon-held SQLite handle in the dashboard state directory (`WAL` mode, matching `wsmeta/store.go` and `wswhiteboard/board_db.go` lifecycle + clean close on daemon drain) with an FTS5 virtual table indexed by `project_key`, timestamp, and summary/tags.
   - Keep strictly machine-local and excluded from Soul ZIP exports and Git sync.
4. **Config Keys, Manifests & Settings Memory Drawer (`dashboard-config-manifest.json`, `dashboard-settings-companion.js`):**
   - Add global-only `dashboard.companion_soul` and `dashboard.companion_soul_dir` keys to `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json`, regenerate embedded assets, update `dashboard-settings-catalog.json` and `wsi18n` locale tables, and extend `CompanionConfig`.
   - Add the Memory / Soul drawer section in `dashboard-settings-companion.js` following `dashboard-style.md` (borderless tonal surfaces): inspect active Soul (`persona.md` / `profile.md` / recent shard entries), review & approve/reject scrubber-held entries, search/prune Local Episodes, and trigger ZIP export/import.
5. **Verification:**
   - Go unit tests covering: RAM hot cache hit with zero warm disk reads, single-line JSONL shard append and multi-machine shard merge without conflicts, scrubber detection + approval queue hold/downgrade across paths/tokens/emails/URLs/ticket-stems/project-names, SQLite WAL + FTS5 project-scoped search, `pack.json` starter-only precedence, and `Settings|Manifest` parity (`generate-package-assets.py --check`, `go test ./internal/mcp ./internal/wsi18n ./internal/wspackage -run 'Settings|Manifest'`).
   - Node frontend tests for the Memory / Soul drawer and approval queue UI.

### Phase 2: Characteristic Sheet, Dynamic Mood State Machine & Delegated Tech-Result Character Briefing

Layer structured personality, mood continuity, and the gap-moe Companion Co-Lead briefing boundary atop Phase 1's memory stores:

1. **Characteristic Sheet & Dynamic Mood State Machine:**
   - Define the structured Characteristic Sheet inside `persona.md` (archetype, speech quirks, gap-moe triggers, comfort/focus behaviors) and an in-memory dynamic mood state machine (energy, focus, rapport, stress/empathy shifts updated by session/user signals and decayed over idle time, with zero disk writes per mood tick).
   - Inject compact Soul summary (`persona.md` + `profile.md` + active mood state + relevant recalled snippets) into Tier-1 reaction prompts (`companionPrompt` / `EphemeralPool`) and Tier-2 Companion Co-Lead context (`(1737)=a`), respecting rune budgets and `<<<...>>>` delimiter quoting.
2. **Delegated Pure-Tech Worker Reporting & Gap-Moe Briefing (`(152)=a`, `(1737)=a`):**
   - Wire the dedicated Companion Co-Lead hosted session (`261127-feat-ws-companion-layered-agents-and-naming` Phase 3) to delegate heavy engineering tasks to clean engineering workers/leads without injecting companion persona into those engineering sessions.
   - When delegated engineering results return, have the Companion Co-Lead brief the outcome in the companion's characteristic/mood voice while enforcing verbatim preservation of technical artifacts (commit hashes, file links, diffs, test outputs, and review verdicts).
3. **Verification:**
   - Go unit tests verifying Characteristic Sheet parsing, mood transitions + idle decay without disk touches, `companionPrompt` injection with `<<<PERSONA>>>`/`<<<SESSION>>>` delimiter safety, and Companion Co-Lead briefing contract tests asserting verbatim preservation of technical artifacts (hashes, file links, verdicts) alongside clean non-persona prompts on delegated engineering workers.

### Phase 3: Hybrid MCP Animation-Delay Pacing (`ws/companion.act` Interruptible Blocking) & Active Memory Recall/Save Loop

Implement interruptible multi-beat pacing and active agent memory tools:

1. **Interruptible `ws/companion.act` MCP Tool (`internal/mcp/`, `wsdashboard/`):**
   - Expose `ws/companion.act` accepting visual/speech beat fields (expression, gesture, prop, fx, speech/quip) plus an optional `delay_ms` (clamped to `1500..6000` ms when provided for pacing).
   - Broadcast the companion command immediately via `/rail/events`, then block inside a `select` waiting on: (a) animation delay timer completion, (b) incoming user message to the companion messenger, (c) companion glass-knock / touch interaction event, or (d) context cancellation / daemon drain/shutdown (`<-r.Context().Done()`, `<-d.stop`).
   - Return immediately with the wake reason (`timer`, `user_message`, `glass_knock`, `cancelled`) and any interrupting user message/context so Tier 1 / Tier 2 can react naturally mid-burst or end the turn cleanly without polling or O(N²) loop accumulation.
2. **Active Memory MCP Tools (`ws/companion.remember_soul`, `ws/companion.remember_episode`, `ws/companion.recall_memory`):**
   - Expose the three memory MCP tools restricted to authorized companion surfaces (Tier-1 bounded burst / Tier-2 Companion Co-Lead; refused cleanly for unrelated worker subagents), backed by Phase 1's RAM hot cache, 2-layer secret scrubber, and SQLite FTS5 episode store.
   - Ensure zero access to `ws/note.*` and enforce bounded burst turn limits so active recall/save + `ws/companion.act` beats terminate cleanly and return to 0-token procedural VRM idle.
3. **Verification:**
   - Deterministic Go tests (injected clock / event channels, zero flaky wall-clock sleeps) verifying `ws/companion.act` returns promptly on each `select` branch (`timer`, `user_message`, `glass_knock`, `cancelled`), clamps delay bounds (`1500..6000` ms), and survives `proxyDrainBudget` / shutdown cleanly without hanging.
   - End-to-end integration tests for Tier-1/Tier-2 active burst -> `recall_memory` -> `act` (interrupted by user message) -> `remember_episode` / scrubbed `remember_soul` -> clean turn exit back to 0-token procedural idle, plus `GOOS=windows go vet ./...`.
