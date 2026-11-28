---
title: "컴패니언 3계층 장기 메모리(Portable Soul / Local Episodes / VRM), 캐릭터성 및 하이브리드 MCP 애니메이션 딜레이 페이싱"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261127-feat-ws-companion-layered-agents-and-naming#phase1
related:
  261127-feat-ws-companion-layered-agents-and-naming: companion-body/companion-work markers, Tier-1 Companion burst session, companion.delegate_work, Tier-2 Hidden Work Brain, and ws/delegation.report (Phase 1 prerequisite)
  261126-feat-ws-companion-vrm-engine: VRM 0.x/1.0 runtime, procedural 2-bone IK, 3D props, and expression controller
  261122-epic-werkschaft-lead-companion: parent companion architecture and cross-child invariants
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: d1b0119e859e4898
sage-review-design-reviewed: d1b0119e859e4898
---

# Companion 3-Layer Long-Term Memory (Portable Soul / Local Episodes / VRM), Characteristic Sheet & Hybrid MCP Animation-Delay Pacing

## Background

The Werkschaft Companion currently executes bounded-rotation reaction calls via `companionPrompt` (`companion_director.go`), driven solely by transient session-digest rings and an optional static `CompanionPersona` (`name`, `voice`, `quipLanguage`) embedded inside a visual pack's `pack.json` (`companion_pack_schema.go`). While `261127-feat-ws-companion-layered-agents-and-naming` (Phase 1) establishes the `companion-body`/`companion-work` markers, Tier-1 Companion burst session, `companion.delegate_work`, Tier-2 Hidden Work Brain, and `ws/delegation.report`, three core capabilities remain missing:

1. **No orthogonal long-term memory or portable identity:** Character identity is coupled to visual pack manifests, there is no machine-portable user/companion Soul store (safe for Git sync across work and home machines without binary conflicts or secret leakage), and there is no machine-local episodic recall store scoped by project.
2. **No structured personality/mood continuity or single-embodiment dual-brain boundary:** There is no Characteristic Sheet or dynamic mood state machine feeding the Tier-1 Companion, and no structural separation ensuring the sole embodied Tier-1 Companion owns all user conversation and character presentation while delegating heavy engineering work via lossless curated task briefs to a headless Tier-2 work brain ("Companion AI's AI") whose structured results Tier-1 translates back in character with verbatim technical artifacts preserved.
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
    3. **Bounded active bursts via interruptible MCP animation-delay pacing:** During an active burst, `ws/companion.act` issues the visual/speech command and performs an interruptible `select` wait of **1.5–6 s** (clamped) across: animation timer expiry, incoming user message, glass-knock/touch interaction, or daemon shutdown — zero polling, immediate wake on interruption, and a clean turn end when the burst completes so conversation context never grows O(N²). Tier-1 runs as the **single MCP-burst Companion** (no duplicate zero-MCP triage layer), unifying the `261127` Phase 1 surface.
  - **Rejected:** `(b)` 24/7 always-on `wait_event` long-blocking loop (continuous idle token burn, O(N²) context accumulation, and client harness tool-timeout risk); `(c)` pure zero-MCP 1-turn JSON script only (cannot handle mid-burst user interruption or active memory recall/save tool calls) or a separate duplicate zero-MCP triage layer in front of Tier-1.
- **D2 — Gap-Moe Dual Tone & Delegated Pure-Tech Worker Reporting (`(152)=a`, `(1737)=a`, `(1744)=a`):**
  - **Chosen:** Enforce a strict structural split between companion embodiment/presentation and headless technical execution:
    - All direct user conversation (messenger, phone-check, ambient interactions) happens through Tier-1 in the companion's full persona, Characteristic Sheet, and dynamic mood voice.
    - Heavy engineering and daemon work (`ws/daemon.*`, coding/ticket delegation) runs silently inside the headless Tier-2 work brain and its delegated engineering workers in crisp, unadorned technical prose.
    - When Tier-2 returns structured technical results and artifact links to Tier-1, Tier-1 presents the outcome back to the user in character while **preserving verbatim technical artifacts** (commit hashes, diffs, file links, test results, and review verdicts).
  - **Rejected:** `(b)` Full character voice inside engineering execution or rewriting raw engineering output (risks distorting technical precision and auditability); `(c)` user tone-slider only without structural work/briefing separation.
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
- **D4 — Single-Embodiment · Dual-Brain — "Companion AI's AI" (`(1744)=a`, refining `(1737)=a`):**
  - **Chosen:** Separate the embodied conversational companion from the hidden engineering work brain with zero overlap in tools or user exposure:
    - **Tier-1 Companion (sole embodied persona):** Exclusively owns the VRM body, `ws/companion.*` (`phone_*`, `act`, `remember_soul`, `remember_episode`, `recall_memory`, `delegate_work`), the posture lock, Soul/Episodes memory, and **all** direct user conversation as a single unified MCP-burst Companion aligned with `261127` Phase 1.
    - **Tier-2 Hidden Work Brain ("Companion AI's AI", dedicated daemon-launched hosted session per `(1737)=a`):** Has **zero** body control, **zero** `phone_*` tools, **zero** persona prompt, and **never** surfaces directly to the user. It receives a **lossless curated task brief** from Tier-1 via `ws/companion.delegate_work` (with **no** artificial line cap such as a 3-line limit), executes pure engineering/daemon work (`ws/daemon.*`, coding/ticket delegation) silently while the VRM shows `laptop_work`, returns structured technical results plus artifact links to Tier-1 (which reports them in character per `(152)=a`), and compacts cleanly at idle boundaries — leaving the user's primary coding lead completely untouched.
  - **Rejected:** Allowing Tier-2 to speak directly to the user or drive VRM/phone tools (splits character embodiment and leaks raw system/work framing), imposing an artificial line cap on `delegate_work` task briefs (drops critical engineering constraints/context), or routing Tier-1 through a separate duplicate zero-MCP triage layer.
- **D5 — Compatibility Tier Verdict: T0 Transparent (`ai-docs/manuals/release-compatibility.md`):**
  - **Verdict:** **T0 (transparent, patch-safe)**.
  - **Directional evidence:**
    - New Portable Soul files under `~/.ws/companion-soul/<soul-id>/` and `<state-dir>/companion-episodes.sqlite` are untouched and ignored by older daemons.
    - New global-only scalar string config keys `dashboard.companion_soul` (and optional `dashboard.companion_soul_dir`) are stored in `wsconfig`'s `overrides` map and declared in `dashboard-config-manifest.json` using existing manifest fields (never top-level `config.json` members and never new manifest schema fields).
    - Additive MCP tools introduced by this ticket (`ws/companion.act`, `ws/companion.remember_soul`, `ws/companion.remember_episode`, `ws/companion.recall_memory`; `delegate_work` and `phone_*` land in the prerequisite / `b326952ee`) and additive HTTP routes (`/api/companion/soul/*`, `/api/companion/memory/*`, `/api/companion/knock`) leave all existing `ws/companion.*` / `ws/daemon.*` tools, `dashboard.companion_*` keys, and `pack.json` schemas unchanged; older daemons/clients ignore unknown additions with zero data loss (`mcp/server_dispatch.go`, `mcp/tool_arguments.go`).

- **D6 — Scope split against `261127#phase1` (`(1768)`, technical):**
  - **Chosen:** `261127#phase1` owns the markers, the Tier-1 burst session, `ws/companion.delegate_work` (lossless brief, laptop-posture, non-blocking, queued), Tier-2 isolation/`laptop_work`/idle compaction, `ws/delegation.report` verbatim forwarding, and the posture lock; `b326952ee` owns `ws/companion.phone_*` and `speak/face/motion`. This ticket consumes those contracts and adds only: the Soul/Episodes stores and scrubber, the Characteristic Sheet/mood and Soul injection, Tier-1 in-character re-voicing of delegation results with verbatim artifact preservation, `ws/companion.act`, the three memory tools, and the knock event. It never re-implements or forks the prerequisite's contracts.
  - **Rejected:** Restating `delegate_work`/Tier-2 as this ticket's deliverables (forks the prerequisite's contract).
- **D7 — `ws/companion.act` contract (`(1760)`, `(1761)`, `(1764)`, technical):**
  - **Chosen:** `act` reuses the same `companion-command` rail broadcast as the landed `speak/face/motion` (those lead-scoped tools stay unchanged) and adds `fx`/`speech`. Omitted `delay_ms` defaults to **3000 ms**; a provided value is clamped to `1500..6000` ms — so `act` always paces and never spins. Returning an interrupting `user_message` through `act` **consumes** that message from the unread inbox and from the queued busy-Tier-1 delivery, so Tier-1 is not re-woken for it; a message arriving while no `act` is blocked follows the `261127` inbox contract (unread until `phone_pickup`/`phone_read`, re-wake on lease release). `act` is permitted at both `phone` and `laptop` posture.
  - **Rejected:** Immediate return when `delay_ms` is omitted (defeats D1 pacing); leaving the message unread after `act` returns it (double delivery).
- **D8 — Posture allowlist for the new Tier-1 tools (`(1760)`, technical):**
  - **Chosen:** Extend the `261127` additive `companion-body` predicate so `ws/companion.act`, `remember_soul`, `remember_episode` and `recall_memory` are allowed at both `phone` and `laptop` posture for `companion-body` sessions and refused for every other session (including `companion-work`), enforced in `sessionToolAccess` and discovery.
  - **Rejected:** Leaving them under the "work tool" block during `phone_active` (memory and `act` could never run during a messenger reply).
- **D9 — Soul injection channel and persona source (`(1762)`, technical):**
  - **Chosen:** Tier-1 receives the stable `persona.md` Characteristic Sheet in its launch prompt (replacing `261127`'s pack-persona launch seam) and a per-wake `<<<SOUL>>>`/`<<<MOOD>>>`/`<<<RECALL>>>` envelope (delimiter-quoted, rune-budgeted) delivered with each `Runner.Deliver` wake, so mood decay, `persona.md` edits, soul switches and recalled snippets never go stale. The ambient director's `companionPrompt` reads the Soul's `persona.md` (no profile or memory) when a Soul exists and falls back to the pack persona otherwise, so bubble and messenger voices stay consistent.
  - **Rejected:** Launch-only injection (goes stale); ambient director keeping the pack persona once a Soul exists (voice divergence).
- **D10 — Knock event source (`(1763)`, technical):**
  - **Chosen:** Add a guarded frontend-to-daemon event route `POST /api/companion/knock` (`localViewer` + `sameOrigin`) with in-memory fan-out to blocked `act` waiters. The existing cosmetic knock animation stays unchanged and a knock never opens the messenger.
  - **Rejected:** Treating `glass_knock` as a branch with no input.
- **D11 — Soul write ownership, machine-id, scrubber mapping and approval queue (`(1765)`, `(1766)`, `(1767)`, technical):**
  - **Chosen:** `persona.md` and `profile.md` are user-edited only (Settings drawer or Git); agent `remember_soul` writes go only to per-machine JSONL shards, and the Tier-1 summary merges shards into the RAM cache. `<machine-id>` is a generated random id persisted once in the dashboard state dir, sanitized to `[a-z0-9-]`; a changed id simply starts a new shard. Scrubber mapping: secret-like tokens/keys → rejected outright and never persisted; filesystem paths, emails, URLs → held in the approval queue; ticket stems and active project/repo names → downgraded to Local Episodes. The approval queue persists in a `soul_pending` table of `companion-episodes.sqlite` (machine-local, never in Soul shards) and survives restart, with approve/edit/discard routes under `/api/companion/memory/*`. An empty `dashboard.companion_soul` resolves to soul id `default`, auto-created on first enablement and seeded from the pack's starter persona. The mood state is in-memory, reset to baseline on restart, driven by session events, user messages, knocks and idle decay. Active recall/save is bounded by a burst limit defined here (`261127` has none; its sliding 5-minute lease deadline is renewed by every call and cannot bound a loop): a named constant `companionBurstMaxCalls = 12` Tier-1 tool calls (`act`, memory tools, `delegate_work`) per burst (one lease/turn), enforced daemon-side in the `companion-body` tool-access path; the next call after the limit returns a structured `burst_limit` error so Tier-1 ends its turn, and the counter resets when the turn ends. A message consumed by `act` is handled by making the wake a content-free nudge that is dropped at delivery time when the unread inbox is empty (no runner-dequeue API needed; the wiring lives in `cmd/ws-mcp/dashboard.go`).
  - **Rejected:** Runtime rewrite of `profile.md` from two machines (Git conflicts, extra file touches); persisting secret-like tokens in a queue.

## Constraints

- **Implementation convention manuals:**
  - Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
  - Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
  - Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
  - Convention: ai-docs/manuals/config-settings-ui.md (declared for `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)
  - Convention: ai-docs/manuals/dashboard-style.md (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
  - Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
  - Convention: ai-docs/manuals/wsflow-mirroring.md (declared for `agents-plugin-wsflow/`)
- **Windows AV file-touch budget (`performance.md`):**
  - Portable Soul (`persona.md`, `profile.md`, `log/YYYY-MM-<machine-id>.jsonl`) uses a daemon RAM hot cache loaded once on startup/soul switch; `remember_soul` appends one JSONL line per delta without rewriting existing files or scanning directories on read/recall.
  - Local Episodes (`companion-episodes.sqlite`) follows Storage Type 4 (queried structured history): open once at daemon startup (or lazy first companion-memory enablement) and hold the SQLite connection in WAL mode for the daemon's lifetime, with zero per-call open/close/stat.
  - `ws/companion.act` animation-delay blocking uses in-memory Go channels (`select` on timer / user message / glass-knock / daemon shutdown), incurring zero file touches and zero polling.
- **Strict privacy, IP & note isolation:**
  - Never commit companion memory (`~/.ws/companion-soul/`, `companion-episodes.sqlite`) or user personal logs to any project repository.
  - `ws/note.*` (`ai-docs/ws-notes/`) remains strictly isolated from companion memory; companion recall/save tools must never read from or write into `ws/note.*`.
  - Every Portable Soul write passes through the deterministic 2-layer secret/PII guardrail before touching `log/YYYY-MM-<machine-id>.jsonl`; user edits of `persona.md`/`profile.md` through the Settings drawer are also scrubbed (held in the approval queue rather than rejected), while edits made directly in the Git-synced files are the user's own responsibility.
- **Settings UI & manifest parity (`config-settings-ui.md`, `wsflow-mirroring.md`):**
  - Mirror any new `dashboard.companion_soul*` keys in both `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json`, regenerate embedded assets via `python3 agents-plugin-tool/scripts/generate-package-assets.py`, add UI catalog rows in `dashboard-settings-catalog.json`, add `en`/`ko` (and all shipped locale) translations in `internal/wsi18n/tables/`, and verify `Settings|Manifest` parity tests.

## Prior Decisions

- 261127-feat-ws-companion-layered-agents-and-naming (2026-11-28, Result/commit): "ws/companion.{speak,face,motion,phone_*} tools landed as b326952ee; Phase 1 adds markers, Tier-1 burst session, delegate_work, Tier-2 Hidden Work Brain" — bearing: constrains
- 261122-epic-werkschaft-lead-companion (2026-11-28, Cross-Child Decisions): "Single-Embodiment · Dual-Brain supersession ((1737)=a, (1738), (1744)=a): Tier-1 exclusively owns the VRM body, persona, Soul/Episodes memory, posture lock, ws/companion.* tools" — bearing: supports
- ed958e66 (2026-11-28, commit): "Recorded owner-confirmed decisions (151)=a, (152)=a, (153)=a; graded compatibility tier T0 transparent per release-compatibility.md" — bearing: supports
- 261127-feat-ws-ephemeral-lightweight-harness-pool (2026-11-28, Result dfe58734): "Routed daemon-side companion reactions through EphemeralPool (BoundedRotation, maxTurns=20, idleTTL=5m) via a narrow CompanionEphemeralRunner interface" — bearing: constrains
- 261122-feat-ws-companion-director (2026-11-22, Decisions): "Persona is pack data: pack.json gains an optional persona object: name (<= 40 chars), voice (<= 800 chars), quipLanguage" — bearing: contradiction-candidate; deliberately superseded by D3 of this ticket (owner-confirmed `(153)=a`): `pack.json` `persona` stays as an optional starter template for a new Soul only, while the Portable Soul owns persona thereafter
- 261126-feat-ws-companion-vrm-pack-store (2026-11-26, f8ab72f0): "ws-companion-vrm/v1 persona sketch reuses CompanionPersona ({name, voice, quipLanguage})" — bearing: constrains
- ed93032a (2026-11-28, commit): "Moves the 10 companion keys into the native Dashboard Settings companion section (dashboard-settings-companion.js/css), T0 transparent" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `wsdashboard/` (new `companion_soul.go`, `companion_episodes.go`, `companion_memory_routes.go`, `companion_scrubber.go`, edits to `companion_config.go`, `companion_director.go`, `daemon_start.go`, `assets/dashboard-settings-companion.js`), `internal/mcp/` (`ws/companion.act`, `remember_soul`, `remember_episode`, `recall_memory`; the `companion-body` allowlist predicate and `sessionToolAccess`/discovery from `261127`; `cmd/ws-mcp/dashboard.go` launch/wake wiring for the Soul prompt/envelope, content-free wake nudge and the `wsdashboard`↔`mcp` interface for knock/message fan-out into `act` waiters; new `POST /api/companion/knock` route and in-memory knock fan-out), `dashboard-config-manifest.json` (ws & wsflow), `wsi18n/tables/`, `dashboard-settings-catalog.json` |
| scope.surface | cross-module | new MCP tools, additive `dashboard.companion_soul*` config keys, new `/api/companion/soul/*` and `/api/companion/memory/*` HTTP routes, SQLite WAL+FTS5 store, Portable Soul JSONL shards, Tier-1/Tier-2 delegation boundary, and Settings UI memory drawer |
| scope.new_public_symbol | yes | new MCP tools (`ws/companion.act`, `ws/companion.remember_soul`, `ws/companion.remember_episode`, `ws/companion.recall_memory`; `delegate_work` and `phone_*` are prerequisite-owned, per D6), new HTTP handlers (soul, memory, knock), and new daemon Soul/Episodes store types |
| scope.new_type_contract | yes | Portable Soul directory schema (`persona.md`, `profile.md`, `log/YYYY-MM-<machine-id>.jsonl`), Local Episodes SQLite FTS5 schema, Characteristic Sheet + mood state contract, per-wake Soul envelope, scrubber mapping + `soul_pending` approval queue, and `ws/companion.act` interruptible wait contract (the `delegate_work` brief/result contract is owned by `261127#phase1`) |
| scope.test_surface | new-files | new Go unit tests for Soul shard RAM cache, secret scrubber + approval queue, SQLite FTS5 episodes, Tier-1/Tier-2 boundary enforcement, interruptible `ws/companion.act` cancellation, settings/manifest drift, and frontend drawer tests |
| complexity.reuse_points | confirmed | `CompanionConfig` (`companion_config.go:76-91`), `companionPrompt` (`companion_director.go:871-898`), `CompanionPersona` (`companion_pack_schema.go:85-119`), `BoardDB` held SQLite WAL pattern (`board_db.go:47-55`), `dashboard-settings-companion.js:1536-1556` |
| complexity.side_effect_risk | moderate | additive stores and tools isolated from `ws/note.*` and primary coding leads; main risks are file-touch discipline under Windows AV, SQLite handle lifecycle on daemon drain, and interruptible MCP cancellation cleanup |
| risk.correctness | moderate | interruptible `select` timing vs `proxyDrainBudget`/`hostedCallLinger`, regex scrubber false-positive/false-negative tuning, Tier-2 idle compaction handoff, and Soul-vs-pack starter template precedence |
| risk.fit | moderate | integrates cleanly atop `261127-feat-ws-companion-layered-agents-and-naming#phase1` (unified Tier-1 MCP-burst Companion, headless Tier-2 work brain, and `ws/companion.*` surface) |
| risk.test | moderate | deterministic clock/channel injection required to test 1.5–6 s `ws/companion.act` interruption without flaky sleeps; full FTS5 and scrubber edge-case coverage needed |
| risk.security_or_contract | high | Portable Soul secret/PII scrubber, approval queue gating, `localViewer` + `sameOrigin` HTTP guardrails on Soul ZIP export/import and memory routes, and T0 config/manifest parity |

## Phases

### Phase 1: 3-Layer Orthogonal Storage (Portable Soul Shards + Local Episodes SQLite FTS5), Secret Guardrail & Settings Memory Drawer

Establish the orthogonal storage engine, deterministic privacy guardrail, config keys, and Settings memory management UI:

1. **Portable Soul Store (`~/.ws/companion-soul/<soul-id>/` or `dashboard.companion_soul_dir`):**
   - Implement the RAM hot cache and file layout: `persona.md`, `profile.md`, and append-only per-machine monthly shards `log/YYYY-MM-<machine-id>.jsonl`.
   - Read once on startup / soul selection into memory; append one JSONL line per accepted delta without full-file rewrites or directory rescans.
   - `persona.md` and `profile.md` are user-edited only (Settings drawer or Git); agent writes go only to shards, merged into the RAM cache for summaries (D11). `<machine-id>` is a generated random id persisted once in the state dir, sanitized to `[a-z0-9-]`. An empty `dashboard.companion_soul` resolves to soul id `default`, auto-created on first enablement from the pack starter persona.
   - Support Git-sync-friendly non-conflicting shard merges across machines and ZIP export/import (`/api/companion/soul/export`, `/api/companion/soul/import`, gated by `localViewer` + `sameOrigin`).
   - Treat `pack.json`'s `CompanionPersona` strictly as an optional starter template when initializing a brand-new Soul; never let a visual pack overwrite an existing Soul.
2. **2-Layer Secret & PII Guardrail:**
   - Separate `ws/companion.remember_soul` (portable personal/rapport memory) from `ws/companion.remember_episode` (machine-local project episodes) and isolate both from `ws/note.*`.
   - Implement a deterministic scrubber before every Soul write checking for filesystem paths, API keys/tokens, email addresses, URLs, ticket stems (`YYMMDD-*`), and active project/repo names: per D11, secret-like tokens/keys are rejected and never persisted; paths, emails and URLs are held in the pending **Settings approval queue** (a `soul_pending` table in `companion-episodes.sqlite`, surviving restart, with approve/edit/discard routes under `/api/companion/memory/*`) until the user approves, edits, or discards them; ticket stems and active project/repo names are downgraded to Local Episodes.
3. **Local Episodes Store (`companion-episodes.sqlite`):**
   - Open a single daemon-held SQLite handle in the dashboard state directory (`WAL` mode, matching `wsmeta/store.go` and `wswhiteboard/board_db.go` lifecycle + clean close on daemon drain) with an FTS5 virtual table indexed by `project_key`, timestamp, and summary/tags.
   - Keep strictly machine-local and excluded from Soul ZIP exports and Git sync.
4. **Config Keys, Manifests & Settings Memory Drawer (`dashboard-config-manifest.json`, `dashboard-settings-companion.js`):**
   - Add global-only `dashboard.companion_soul` and `dashboard.companion_soul_dir` keys to `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json`, regenerate embedded assets, update `dashboard-settings-catalog.json` and `wsi18n` locale tables, and extend `CompanionConfig`.
   - Add the Memory / Soul drawer section in `dashboard-settings-companion.js` following `dashboard-style.md` (borderless tonal surfaces): inspect active Soul (`persona.md` / `profile.md` / recent shard entries), review & approve/reject scrubber-held entries, search/prune Local Episodes, and trigger ZIP export/import.
5. **Verification:**
   - Go unit tests covering: RAM hot cache hit with zero warm disk reads, single-line JSONL shard append and multi-machine shard merge without conflicts, scrubber detection + approval queue hold/downgrade across paths/tokens/emails/URLs/ticket-stems/project-names, SQLite WAL + FTS5 project-scoped search, `pack.json` starter-only precedence, and `Settings|Manifest` parity (`generate-package-assets.py --check`, `go test ./internal/mcp ./internal/wsi18n ./internal/wspackage -run 'Settings|Manifest'`).
   - Node frontend tests for the Memory / Soul drawer and approval queue UI.

### Phase 2: Characteristic Sheet, Dynamic Mood State Machine, Soul Injection & In-Character Briefing of Delegated Results (`(1744)=a`)

Layer structured personality, mood continuity, Soul injection and in-character briefing atop Phase 1's memory stores. The Tier-1/Tier-2 boundary itself (`delegate_work`, Tier-2 isolation, `laptop_work`, compaction, `ws/delegation.report`) is delivered by `261127#phase1` (D6) and is only consumed and covered by tests here:

1. **Characteristic Sheet & Dynamic Mood State Machine (Tier-1 Exclusive):**
   - Define the structured Characteristic Sheet inside `persona.md` (archetype, speech quirks, gap-moe triggers, comfort/focus behaviors) and an in-memory dynamic mood state machine (energy, focus, rapport, stress/empathy shifts updated by session/user signals and decayed over idle time, with zero disk writes per mood tick).
   - Inject compact Soul summary (`persona.md` + `profile.md` + active mood state + relevant recalled snippets) exclusively into the unified Tier-1 Companion context (aligning cleanly with `261127` Phase 1 with no duplicate zero-MCP triage layer), respecting rune budgets and `<<<...>>>` delimiter quoting. Per D9: the stable `persona.md` Characteristic Sheet replaces `261127`'s pack-persona launch seam in Tier-1's launch prompt, and a per-wake `<<<SOUL>>>`/`<<<MOOD>>>`/`<<<RECALL>>>` envelope rides each `Runner.Deliver` wake; the ambient director's `companionPrompt` reads the Soul's `persona.md` when a Soul exists and falls back to the pack persona otherwise. The mood state is in-memory, reset to baseline on restart, driven by session events, user messages, knocks and idle decay (D11).
2. **In-Character Briefing of Delegated Results (`(152)=a`, `(1744)=a`; consumes `261127#phase1`):**
   - Consume the prerequisite's `delegate_work` / `ws/delegation.report` verbatim-forwarding path unchanged (D6); this ticket does not re-implement Tier-2, `laptop_work`, compaction, or the posture lock.
   - When a delegation result reaches Tier-1, Tier-1 briefs the outcome back to the user in character/mood voice while enforcing **verbatim preservation** of technical artifacts (commit hashes, file links, diffs, test outputs, and review verdicts), leaving the user's primary coding lead completely untouched.
3. **Verification:**
   - Go unit tests verifying Characteristic Sheet parsing, mood transitions + idle decay without disk touches, per-wake Soul envelope delimiter safety and rune budgeting on Tier-1, ambient `companionPrompt` Soul-vs-pack persona source/fallback (the `<<<RECALL>>>` envelope is tested with a stubbed recall provider; the real `recall_memory` tool lands in Phase 3), and Tier-1 in-character briefing preserving verbatim technical artifacts (hashes, file links, verdicts) through the prerequisite's `delegate_work` → `ws/delegation.report` path.

### Phase 3: Unified Tier-1 Hybrid MCP Animation-Delay Pacing (`ws/companion.act`) & Active Memory Recall/Save Loop

Implement interruptible multi-beat pacing and active memory tools on the unified Tier-1 MCP-burst Companion:

1. **Interruptible `ws/companion.act` MCP Tool (`internal/mcp/`, `wsdashboard/`):**
   - Expose `ws/companion.act` on the unified Tier-1 MCP-burst Companion (replacing any duplicate zero-MCP triage path) accepting visual/speech beat fields (expression, gesture, prop, fx, speech/quip) plus an optional `delay_ms` (default **3000 ms** when omitted; clamped to `1500..6000` ms when provided, per D7). `act` reuses the landed `companion-command` broadcast of `speak/face/motion` (those stay unchanged), and is permitted at both `phone` and `laptop` posture for `companion-body` sessions only (D8).
   - Broadcast the companion command immediately via `/rail/events`, then block inside a `select` waiting on: (a) animation delay timer completion, (b) incoming user message to the companion messenger, (c) companion glass-knock / touch interaction event, fed by the new guarded `POST /api/companion/knock` (`localViewer` + `sameOrigin`) with in-memory fan-out to blocked `act` waiters while the cosmetic knock animation and the "a knock never opens the messenger" rule stay unchanged (D10), or (d) context cancellation / daemon drain/shutdown (`<-r.Context().Done()`, `<-d.stop`).
   - Return immediately with the wake reason (`timer`, `user_message`, `glass_knock`, `cancelled`) and any interrupting user message/context — returning a message through `act` consumes it from the unread inbox and the queued busy-Tier-1 delivery so Tier-1 is not re-woken for it; a message arriving while no `act` is blocked follows `261127`'s inbox contract (D7) — so Tier-1 can react naturally mid-burst or end the turn cleanly without polling or O(N²) loop accumulation.
2. **Active Memory MCP Tools (`ws/companion.remember_soul`, `ws/companion.remember_episode`, `ws/companion.recall_memory`):**
   - Expose the three memory MCP tools exclusively to the embodied Tier-1 Companion surface (refused cleanly for Tier-2 and unrelated engineering worker subagents) by extending `261127`'s additive `companion-body` allowlist predicate so they are allowed at both `phone` and `laptop` posture and refused for all other sessions, enforced in `sessionToolAccess` and discovery (D8), backed by Phase 1's RAM hot cache, 2-layer secret scrubber, and SQLite FTS5 episode store.
   - Ensure zero access to `ws/note.*` and enforce the `companionBurstMaxCalls = 12` burst limit with a structured `burst_limit` error (D11) so active recall/save + `ws/companion.act` + `ws/companion.delegate_work` handoffs terminate cleanly and return the VRM to 0-token procedural idle.
3. **Verification:**
   - Deterministic Go tests (injected clock / event channels, zero flaky wall-clock sleeps) verifying `ws/companion.act` returns promptly on each `select` branch (`timer`, `user_message`, `glass_knock`, `cancelled`), applies the 3000 ms default when `delay_ms` is omitted, clamps delay bounds (`1500..6000` ms), consumes a returned `user_message` so Tier-1 is not re-woken for it (including a message arriving while Tier-1 is mid-`act`, via the content-free nudge dropped on an empty inbox), returns `burst_limit` after 12 calls in one burst, wakes on a `POST /api/companion/knock` event (with `localViewer`/`sameOrigin` rejection tests and no messenger open), enforces the `companion-body` posture allowlist for `act` and the memory tools at both postures (refused for `companion-work`/other sessions), and survives `proxyDrainBudget` / shutdown cleanly without hanging.
   - End-to-end integration tests for the unified Tier-1 active burst (`recall_memory` -> `act` interrupted by user message -> `delegate_work` triggering `laptop_work` while headless Tier-2 runs and compacts -> `remember_episode` / scrubbed `remember_soul` -> clean turn exit back to 0-token procedural idle), plus `GOOS=windows go vet ./...`.
