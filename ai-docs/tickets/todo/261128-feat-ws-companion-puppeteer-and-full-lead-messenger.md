---
title: "컴패니언 에이전트 아키텍처 전환: 고용량 모바일 풀 리드(Brain) 및 경량 퍼펫티어(Body)"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261128-feat-ws-companion-control-rig-studio-and-action-catalog#phase2
related:
  261128-feat-ws-companion-control-rig-studio-and-action-catalog: "(1794)=a prerequisite: Phase 2 delivers the runtime sentinel/custom clip resolver behind triggerGesture/setPhoneState before this ticket's implementation starts"
  261128-feat-ws-companion-visual-polish-and-metafiction-overlay: "(1769)=a parallel visual polish: Phase 3 adds additive mood/breakout fields and spring/phase-accumulator motion; this ticket restricts stage.js/colead.js edits to 1–2 line delegation hooks"
  261127-feat-ws-companion-layered-agents-and-naming: "(.done) 2-tier Single-Embodiment · Dual-Brain baseline (companion-body/companion-work, delegation FIFO, ws/daemon.* binding) that this ticket pivots"
  261128-feat-ws-companion-soul-memory-and-pacing: "(.done) Portable Soul (ws-companion-soul/v1), companion-episodes.sqlite, ws/companion.act, and memory tools whose write ownership moves from Body to Brain"
  261128-feat-ws-dashboard-control-mcp-api: "(.done) Shared ws/daemon.* MCP bridge and top-level shell 1-click confirmation banner consumed by Brain"
sage-review-design: required
sage-review-completeness: required
---

# Companion Architecture Pivot: High-Tier Mobile Full Lead (Brain), Lightweight Cosmetic Puppeteer (Body), and 0ms Client Physical Reflex

## Background

The current 2-tier Single-Embodiment · Dual-Brain companion architecture (`261127-feat-ws-companion-layered-agents-and-naming` and `261128-feat-ws-companion-soul-memory-and-pacing`) puts a lightweight Tier-1 session (`companion-body`, `small` tier) in charge of both VRM embodiment and direct Messenger conversation, delegating engineering work via a single-slot FIFO (`companion.delegate_work` / `delegation.report`) to a hidden headless Tier-2 session (`companion-work`, `medium` tier). In live dogfooding this design hits four structural limits:

1. **Latency and telephone-game loss.** Every non-trivial Messenger request pays two serial LLM round-trips (Body -> `phone_pickup` -> `phone_putdown` -> `delegate_work` -> Brain -> `delegation.report` -> Body -> `phone_pickup` -> `phone_send`) and loses nuance across the brief/report boundary.
2. **Duplicate chat renderer and approval friction.** `dashboard-companion-messenger.js` (1,225L) re-implements a custom thread/markdown/composer UI over `/api/companion/phone/*` instead of reusing the real hosted Lead conversation renderer, so Messenger lacks live streaming tool/exec cards, `#agcompact` subagent tree badges, attachment/interrupt controls, and native 1-click `#apbox`/`#qcbox` approvals (leaving harnesses such as `agy` without inline approval UX inside Messenger).
3. **Under-powered Brain and coupled Puppeteer.** `companion-work` currently skips the full Lead workflow manual (`runner_lead_start.go:74-80`, `runner_instructions.go:89-107`), cannot spawn subagents with full Lead ergonomics, and lacks Persona/Soul/Episodes ownership, while the ambient director (`companion_director.go`) is blocked whenever `EmbodimentLeaseActive()` is true or the user is looking at another tab (`companion_director.go:461-463`).
4. **Slow and flickery phone choreography.** Waiting on server-side `phone_pickup` round-trips introduces visible lag after sending a Messenger message, and unconfirmed client gestures auto-revert after 4s (`PHONE_UNCONFIRMED_GRACE = 4` in `dashboard-companion-vrm-colead.js:129-130,328-337`).

This ticket pivots the architecture to a clean **3-tier role separation** — **0ms Client Physical Reflex**, **Lightweight Cosmetic Puppeteer (`Body`)**, and **High-Tier Mobile Full Lead (`Brain`)** — reusing the real hosted Lead session page inside Messenger via `<iframe src="/s/<brainRid>?frame=1&compact=1">` while keeping release compatibility strictly **T0 / T1 (patch-safe, zero minor bump)**.

## Verified Codebase Surface

All paths are relative to `agents-plugin-tool/` unless stated; verified against `develop @ c0854c31b`.

- **Companion runtime & delegation FIFO.**
  - `cmd/ws-mcp/companion_runtime.go:20-38` (`small`/`medium` defaults, `companionBodyRotateTurns = 40`), `:59-117` (`companionRuntime` struct holding `active`/`queue` delegation FIFO, body/work fields), `:171-197` (`Start` reattach + deadline ticker), `:247-331` (`OnUserWake`), `:521-682` (`CompanionDelegateWork`/`CompanionDelegationReport`/`dispatchToWorkBrain`/`failActiveDelegation`), `:756-903` (`ensureBodySession`, `reattachBodyLocked`, `launchBodyLocked` with `CompanionHandoffCheckpoint`), `:1005-1040` (`ensureWorkSession`), `:1116-1124` (`companionWorkSystemPrompt`).
  - `cmd/ws-mcp/dashboard.go:195-197` (`newCompanionRuntime`), `:271` (`CompanionPool`), `:353-357` (`SetCompanionHolderAlive`, `SetCompanionWake`, `SetCompanionMessengerClosed`, `SetCompanionAgents`, `companion.Start()`).
- **Hosted Lead creation, instructions & tier resolution.**
  - `internal/wsagent/runner_create.go:57-80` (`LeadOptions.CompanionRole`, `CompanionSystemPrompt`), `:269-279` (`MintCompanionLead`), `:322-328` (`Listed = false`, `ConvHistoryAccess = false`, `DashboardControlAccess = false`), `:396-400` (playbook command seeding skipped when `CompanionRole != ""`).
  - `internal/wsagent/runner_instructions.go:61-87` (`composeLeadInstructions`), `:89-107` (`composeCompanionInstructions` bypasses full Lead manual).
  - `internal/wsagent/runner_lead_start.go:74-80` (`composeCompanionInstructions` vs `composeLeadInstructions`), `:159-163` (`LeadSpec.Manual`), `:326-331` (`leadStartStateMarker` skipped when `CompanionRole != ""`).
  - `internal/wsagent/runner_companion.go:18-37` (`CompanionHandoffCheckpoint`), `:46-81` (`CompanionSessions`), `:85-92` (`IsConversationOpen`), `:99-102` (`RotateCompanion`).
  - `internal/wsagent/companion_tier.go:14-45` (`CompanionTierAuto`, `CompanionTierResolution`), `:127-159` (`ResolveCompanionTier`), `:242-295` (`CompanionTierOptions`).
  - `internal/mcp/agent_keys.go:92-113` (`MintCompanionLead`, `EnsureCompanionLead`).
- **MCP tool inventory & role gating.**
  - `internal/mcp/companion_tools.go:14-35` (tool constants `companion.phone_*`, `companion.delegate_work`, `companion.act`, `companion.remember_soul`, `companion.remember_episode`, `companion.recall_memory`, `delegation.report`), `:95-132` (`isCompanionBodyOnlyTool`, `isDelegationReportTool`), `:153-355` (schemas), `:448-502` (posture refusal & piggyback), `:504-756` (`handleCompanionTool`, `handleDelegationReport`).
  - `internal/mcp/server_inventory.go:33-35` (`isLeadOnlyTool`), `:40-156` (`CompanionRoleBody`/`CompanionRoleWork`, `companionBodyBaseAllows`, `companionMarkerAllowsTool`, `companionWorkAllowsWithoutApproval`), `:247-273` (`companionWorkApprovalGate`), `:326-387` (`filteredToolsFor`).
- **Ambient director, commands, live hooks, phone & Soul/Episodes stores.**
  - `internal/wsdashboard/companion_director.go:44-81` (`CompanionEphemeralMode`, `CompanionEphemeralRequest`, `CompanionEphemeralRunner`), `:83-119` (routes, triggers, cooldown/cap/pool constants), `:258-325` (`directorActive`, `directorObserveSnapshot`, `directorObserveRing` focus-only), `:446-508` (`directorFire`: `EmbodimentLeaseActive()` blocker at `:461`, focus blocker at `:463`, `admitLocked` cooldown/cap/busy), `:554-612` (`directorRun`: `small` tier, `resolveAmbientCompanionPersona`, `Execute`, `PublishCompanionCommand`), `:628-683` (VRM vocabulary), `:873-959` (`companionPrompt`, `parseCompanionReply`).
  - `internal/wsdashboard/live_hosted.go:240-277` (`Publish` -> `directorObserveSnapshot` at `:262`; `RingAppended` -> `directorObserveRing` at `:276`).
  - `internal/wsdashboard/companion_command.go:12-21` (`CompanionCommand` struct), `:23-29` (`CompanionCommandMinTTL = 1000`, `CompanionCommandMaxTTL = 15000`, 140-rune quip cap), `:67-97` (`PublishCompanionCommand`, `normalizeCompanionCommand`).
  - `internal/wsdashboard/companion_act.go:16-67` (`CompanionActRequest`/`Result`), `:288-363` (`CompanionAct` blocking wait + `ConsumeUnreadForAct` + `RenewLease`), `:365-500` (`RememberSoul`/`RememberEpisode`/`RecallMemory`), `:571-636` (`POST /api/companion/knock`).
  - `internal/wsdashboard/companion_phone.go:18-57` (4-state embodiment, routes), `:109-129` (`CompanionPhoneSnapshot`), `:324-334` (`EmbodimentLeaseActive`), `:415-467` (`EnqueueUserMessage`, `ConsumeUnreadForAct`), `:1241-1290` (`CompanionPhonePickup`/`Read`/`Send`/`Putdown`), `:1446-1465` (`companionActivePackID`, `CompanionActivePersona`).
  - `internal/wsdashboard/companion_persona.go:45` (11 locales), `:327-400` (`ResolveCompanionPersona` 4-tier), `:446-460` (`CompanionResolvedPersona`).
  - `internal/wsdashboard/companion_soul_prompt.go:12-30` (budgets), `:872-911` (`BuildCompanionBodyLaunchPrompt`), `:956-976` (`CompanionActiveSoulLaunch`, `resolveAmbientCompanionPersona`), `:982-1037` (`BuildCompanionWakeEnvelope`).
  - `internal/wsdashboard/companion_soul.go:34` (`ws-companion-soul/v1`), `:54-61` (`SoulShardEntry`), `:300-342` (`Load`/`PeekExisting` RAM hot cache).
  - `internal/wsdashboard/companion_episodes.go:22-23` (`companion-episodes.sqlite`, `companionEpisodesSchemaVersion = 1`), `:121-164` (`episodes`, `episodes_fts`, `soul_pending` schema).
  - `internal/wsdashboard/companion_messenger_ring.go:26-50` (`companion-messenger/seg-*.jsonl`, `companionMessengerRecord`), `:196-230` (`readCompanionRingSegment`), `:391-400` (`hydrateHistory`).
  - `internal/wsdashboard/companion_agents_route.go:10-43` (`GET /api/companion/agents`, `SetCompanionAgents`).
  - `internal/wsdashboard/companion_pack_routes.go:15-28` (`registerCompanionPackRoutes` wiring all `/api/companion/*` routes) & `daemon_http.go:82`.
- **Dashboard framed session page, shell handshake & frontend assets.**
  - `internal/wsdashboard/page_routes.go:221-287` (`handleSessionPage` with `?frame=1`, `admitShellFrame(w, sessionPageCSP(true))`), `:326-327` (`framedRequest`).
  - `internal/wsdashboard/page_regions.go:41-64` (`sessionPageData` with `Frame bool`).
  - `internal/wsdashboard/page_history_routes.go:238-285` (`frameAncestorsSelf`, `admitShellFrame`).
  - `internal/wsdashboard/pages.go:36-157` (`dashboardScriptFiles`, `companionScriptFiles`, `companionStyleFiles`, `dashboardStyleFiles`).
  - `internal/wsdashboard/assets/dashboard.tmpl:1-36` (`<html ... data-frame="1">`, `#convo`, `#qcbox`, `#apbox`, `#agisland`/`#agcompact`, `#composer`), `:82-137` (`shell` template).
  - `internal/wsdashboard/assets/dashboard.js:60-79` (`framed`, `shHidden`, `shStaged`, `shUnstage`).
  - `internal/wsdashboard/assets/dashboard-shell.css:1-69` (`:root[data-frame]` rules, z-index tokens `--z-cards: 1`, `--z-panels: 40`, `--z-popovers: 60`, `--z-modal: 70`).
  - `internal/wsdashboard/assets/dashboard-lifecycle.js:824-886` (`shPost`, `shSend`, `shBroadcastTheme`), `:1420-1460` (shell `message` handler: `var ent = shEnt(e.source); if (!ent) return;` ignores non-shell iframes), `:1565-1751` (`shFrameInit`, `wsHideFrame`, `wsShell` `"shown"`/`"theme"`/`"settings"`).
  - `internal/wsdashboard/assets/dashboard-composer-delivery.js:44-126` (`sendMessage` -> `outboxAdd`).
  - `internal/wsdashboard/assets/dashboard-conversation.js` (1,860L — remains untouched), `dashboard-conversation.css` (1,362L).
  - `internal/wsdashboard/assets/dashboard-companion-messenger.js` (1,225L total; `:203-462` ~260L safe-markdown helper; `:482-1217` `createMessengerController`; `:1020` `/api/companion/phone/send`), `dashboard-companion-messenger.css` (926L).
  - `internal/wsdashboard/assets/dashboard-companion-stage.js` (1,702L; `:518-570` `phoneState` and `command` -> `rig.setPhoneState` / `rig.triggerGesture`; `:1090-1102` and `:1242-1248` bare `.ws-companion-quip` creation/positioning).
  - `internal/wsdashboard/assets/dashboard-companion-vrm-colead.js` (1,009L; `:126-130,328-337` `PHONE_UNCONFIRMED_GRACE = 4` auto-putdown; `:842-890` `triggerGesture` and `setPhoneState`).
  - `internal/wsdashboard/assets/dashboard-companion-scenarios.js:255-282` (`directedCommand` reads only `expression`, `gesture`, `prop`, `fx`, `quip`, `scenario`, `ttl_ms`, ignoring unknown fields).
  - `internal/wsdashboard/assets/dashboard-companion.css:193-208` (current `.ws-companion-quip` rules).
- **Config, Settings UI & i18n.**
  - `internal/wsconfig/config.go:37-40` (`Overrides map[string]string`), `internal/wsdashboard/companion_config.go:36-37` (`CompanionBodyTierKey`, `CompanionWorkTierKey`).
  - `../agents-plugin/dashboard-config-manifest.json:193-207` and `../agents-plugin-wsflow/dashboard-config-manifest.json:193-207` (`dashboard.companion_body_tier`, `dashboard.companion_work_tier`).
  - `internal/wsdashboard/assets/dashboard-settings-catalog.json:90-91`, `assets/dashboard-settings-companion-agents.js:8-11`.
  - `internal/wsi18n/tables/*.json` (11 locales: `de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`; `en.json:1771-1772,1799-1800` & `ko.json:1488-1498,1771-1772,1799-1800`).

## Prior Decisions

- `261122-epic-werkschaft-lead-companion` (`(1737)=a`, `(1738)`, `(1744)=a` — Single-Embodiment · Dual-Brain) — **bearing: supersedes** for companion role assignment: moves direct Messenger conversation, Persona voice, and Soul/Episodes memory ownership from Tier-1 (`companion-body`) to `Brain` (`companion-work`, promoted to Mobile Full Lead), while narrowing `Body` (`companion-body` / Puppeteer) to a pure cosmetic director that never replies in Messenger and never delegates work. The user's primary coding Leads remain completely untouched.
- `261127-feat-ws-companion-layered-agents-and-naming` (`.done`) — **bearing: supersedes** the Body-owned Messenger + single-slot delegation FIFO flow, while preserving `companion-body`/`companion-work` session markers, `dashboard.companion_body_tier`/`dashboard.companion_work_tier` keys, and `ws/daemon.*` auto-allow binding.
- `261128-feat-ws-companion-soul-memory-and-pacing` (`.done`) — **bearing: constrains** persisted memory formats (`ws-companion-soul/v1`, `companion-episodes.sqlite` schema v1, RAM hot cache, 2-layer scrubber, `soul_pending`) which stay intact; **supersedes** tool ownership so Brain owns `remember_soul`/`remember_episode`/`recall_memory` and Body calls `ws/companion.act` in pure-cosmetic mode.
- `261128-feat-ws-companion-control-rig-studio-and-action-catalog` (`ready`, Phase 2 in flight) — **bearing: constrains** (`(1794)=a`): this ticket's implementation begins after `1780` Phase 2 merges to `develop`, and all reflex/Puppeteer choreography routes through `triggerGesture`/`setPhoneState` so pack sentinel overrides (`phone_hold`, `phone_typing`, `phone_putdown`, `laptop_work`, `idle`) work seamlessly.
- `261128-feat-ws-companion-visual-polish-and-metafiction-overlay` (`ready`, Phase 3 pending) — **bearing: constrains** (`(1769)=a`): keeps `dashboard-companion-stage.js` and `dashboard-companion-vrm-colead.js` edits to 1–2 line delegation hooks and preserves `--z-popovers` (`60`) / `.ws-companion` (`58`) stacking and `60/30/6/0` FPS discipline.

## Decisions

### D1 — 3-Tier Companion Architecture (Owner-Settled)

Replace the old Dual-Brain relay (Body replies + delegates to headless Brain) with three cleanly separated tiers:

1. **Tier 0 — 0ms Client Physical Reflex (`dashboard-companion-reflex.js`):** Pure deterministic frontend motion layer with zero LLM/network wait. On Messenger send, triggers `phone_pickup` within 1 frame, holds a short client reflex lease suppressing stale snapshot `buzz` and the 4s `PHONE_UNCONFIRMED_GRACE` revert (`colead.js:129-130,328-337`), yields automatically when an authoritative Puppeteer `phone_*` command arrives, and safely falls back to `phone_putdown`/typing on Brain work-start/subagent-spawn or idle timeout.
2. **Tier 1 — Lightweight Cosmetic Puppeteer (`Body`, `dashboard.companion_body_tier`):** Evolved from `wsdashboard/companion_director.go`. Pure cosmetic stage director: NEVER replies in Messenger, NEVER delegates work, NEVER writes Soul/Episodes memory. Observes Brain's live hosted session directly via `live_hosted.go` `Snapshot` + `Ring` hooks and emits `CompanionCommand` (VRM expressions, gestures, phone states, gaze, FX, plus optional **1-line stage-bubble side-comment/reaction speech `Quip <= 140 runes`**, e.g. `"뭐라는 거야..."`, shown exclusively on the VRM stage bubble `.ws-companion-quip` and never mixed into the Messenger chat log).
3. **Tier 2 — High-Tier Mobile Full Lead (`Brain`, `dashboard.companion_work_tier`):** Promotes the hidden (`Listed = false`) hosted Lead `companion-work` to the direct conversational & orchestration Lead for Messenger — full Lead workflow manual, `ws/compact`, `agenda.set`, `agent.spawn` (background subagents + worktrees), `ws/daemon.*` (auto-allowed), active Persona voice, and exclusive Soul/Episodes memory write ownership (`remember_soul`, `remember_episode`, `recall_memory`).

### D2 — Compact Messenger Frame Embed (`(1793)=a`, Owner-Settled)

- **Chosen:** Reuse the core dashboard hosted session renderer inside the Companion Messenger floating panel as a **compact messenger layout (chat bubbles + subagent badges)** by embedding Brain's real hosted Lead page via `<iframe src="/s/<brainRid>?frame=1&compact=1">`.
  - **Zero duplicate streaming engine & zero edits to `dashboard-conversation.js` (1,860L):** The embedded page reuses `#convo` (live streaming entries + collapsible tool/exec cards + scroll pinning), `#agisland`/`#agcompact` subagent tree badges, `#apbox`/`#qcbox` 1-click approvals & questions (natively fixing approval UX on all harnesses including `agy`), and `#composer` (attachments, mentions, voice, interrupt).
  - **Styling (`dashboard-compact.css`):** New stylesheet loaded in `dashboardStyleFiles` (`pages.go:128-157`), keyed strictly on `:root[data-compact="1"]` (emitted on `<html>` when `?frame=1&compact=1` in `page_routes.go`/`dashboard.tmpl`). Hides shell chrome (`.top`, `.rail-ph`, `#tabbar`, `#chead`, `#bcol`, `#files`), collapses `#grid` to a single full-bleed flex column, styles `.msg.user` / `.msg.assistant` as Messenger-density tonal bubbles using `--wb-*` borderless Material tonal tokens (`dashboard-style.md`), compacts `#agcompact` into a slim pill bar above `#composer`, and keeps `#apbox`/`#qcbox` prominent and 1-click accessible.
  - **Shell handshake & isolation:** Existing shell message routing (`dashboard-lifecycle.js:1444-1445`: `var ent = shEnt(e.source); if (!ent) return;`) already ignores iframes outside `shFrames`, so the compact iframe never clobbers the main shell's URL, title, header, or `companion-context` relay. Inside the compact frame (`data-compact="1"`), `dashboard.js`/`dashboard-lifecycle.js` initialize `shStaged = false` (or accept `wsShell: "shown"` from the Messenger controller when opened/closed so `wsHideFrame` pauses SSE/WS when the Messenger popup is closed!) and disable `startCompanionRelay()`. When the user sends in compact mode (`dashboard-composer-delivery.js:44-126` `sendMessage`), the frame posts a single same-origin message `{ wsCompanionCompact: "user-send", rid }` to `window.parent`, triggering Tier 0's 0ms `phone_pickup` reflex. The Messenger controller forwards theme changes (`wsShell: "theme"`) and open/close visibility (`wsShell: "shown"`) to the compact iframe.
  - **Header pop-out & unlisted rail posture:** Brain stays `Listed = false` (`runner_create.go:322-328`) so it never clutters the main left session rail, while the Messenger header gains an optional **"Open in full tab"** button (`window.open("/s/" + brainRid, "_blank", "noopener")`) for inspecting long tool output or subagent trees at full width.
  - **Graceful fallback & file-size cleanup:** Split the ~260L safe-markdown helper (`dashboard-companion-messenger.js:203-462`) into `dashboard-companion-messenger-md.js` (registered right before `dashboard-companion-messenger.js` in `companionScriptFiles`), bringing `dashboard-companion-messenger.js` well under 1,000L. When `/api/companion/brain` returns unavailable (e.g. older daemon 404 or no configured harness), the Messenger panel renders a clean fallback status/retry card (and preserves legacy read-only thread view if present).
- **Rejected:** Building a second streaming/tool-card/approval renderer inside `dashboard-companion-messenger.js` (high duplication, drift risk, and missing `#apbox`/`#qcbox` & `#agcompact` parity).

### D3 — Parallel Sequencing & Merge-Conflict Guardrails (`(1794)=a`, Owner-Settled)

- Architecture ticket is drafted and cleared through Sage review in parallel with `261128-feat-ws-companion-control-rig-studio-and-action-catalog` (`1780`) Phase 2.
- Implementation starts immediately after `1780` Phase 2 merges to `develop` (enforced by `blocked-by: 261128-feat-ws-companion-control-rig-studio-and-action-catalog#phase2`).
- To coordinate cleanly with both `1780` Phase 2 and `1769` Phase 3, `dashboard-companion-stage.js` (1,702L) and `dashboard-companion-vrm-colead.js` (1,009L) receive only minimal 1–2 line delegation hooks (`C.reflex.*`, `C.quip.*`); all new state logic lives in dedicated `<500L` modules (`dashboard-companion-reflex.js`, `dashboard-companion-quip.js`, `dashboard-companion-quip.css`, `dashboard-compact.css`, `dashboard-companion-messenger-md.js`).

### D4 — Brain Full Lead Promotion (`companion-work`)

- **Full Lead capabilities:** When `CompanionRole == "companion-work"`, `runner_lead_start.go:74-80` and `runner_instructions.go:61-107` compose the full Lead workflow manual (`composeLeadInstructions`) plus a compact **Companion Brain Persona + Soul/Episodes** system addendum (built via `CompanionActiveSoulLaunch` + active pack/Soul persona voice + read/write rules for `remember_soul`/`remember_episode`/`recall_memory`), and enable `leadStartStateMarker` (`runner_lead_start.go:326-331`) and playbook command seeding (`runner_create.go:396-400`).
- **MCP tool posture (`server_inventory.go:40-156,247-273,326-387`):** Brain receives full Lead tools (`ws/compact`, `agenda.set`, `agent.spawn`, `agent.*`, `exec.*`, `git.*`, `tickets.*`, `mailbox.*`, `ws/daemon.*` auto-allowed without `companionWorkApprovalGate`), plus `companion.remember_soul`, `companion.remember_episode`, and `companion.recall_memory`. Brain does NOT receive `companion.act` or `companion.phone_*` (body choreography belongs exclusively to Puppeteer + 0ms reflex).
- **Persistent session & cross-harness handoff:** `companionRuntime` maintains one persistent `companion-work` hosted Lead session (reattaching across daemon restarts via `CompanionSessions`). Changing `dashboard.companion_work_tier` triggers a clean cross-harness handoff via `RotateCompanion` + `CompanionHandoffCheckpoint` (`runner_companion.go:18-37,99-102`) on the next ensure/turn boundary, preserving Soul/Episodes and recent conversation summary without dropping user continuity.
- **Bootstrap endpoint `GET /api/companion/brain`:** Registered under `registerCompanionPackRoutes` (`companion_pack_routes.go:15-28`), gated on `localBrowseViewer` (matching `companion_agents_route.go:33-36`). Lazily ensures/attaches the Brain Lead session via `companionRuntime.EnsureBrainSession`, returns `{ replyId, wsKey, harness, model, ready, personaName, seededFromLegacy }`, and — ONLY on a brand-new Brain session with an empty conversation ring when recent legacy `companion-messenger/seg-*.jsonl` records exist (`companion_messenger_ring.go:196-230`) — reads the last `<= 10` legacy messages once (bounded `< 4 KiB` text digest, read-only) into the initial launch context so upgrading users keep recent chat continuity.

### D5 — Lightweight Cosmetic Puppeteer (`Body`, `companion_director.go`)

- **Direct Brain observation (no focus or lease blockers):** `companionRuntime` registers Brain's active `replyID` with the daemon (`SetCompanionBrainReplyID(rid)`). In `companion_director.go:258-325,446-508`, `directorObserveSnapshot` / `directorObserveRing` and `directorFire` accept events from Brain's `replyID` regardless of which tab the user has focused and WITHOUT blocking on `EmbodimentLeaseActive()`. Focused-tab ambient observation for normal sessions continues to work when Brain is idle.
- **5 triggers + 250ms coalescing + priority preemption:**
  1. `user_message` (priority: **high**) — user sent a message to Brain (observed from Brain's ring append).
  2. `brain_work_start` (priority: **normal**) — Brain enters tool/exec/subagent turn.
  3. `subagent_event` (priority: **normal**) — Brain spawns or completes a child agent (`agent.spawn` / child turn end).
  4. `brain_reply` (priority: **high**) — Brain finishes an assistant reply turn.
  5. `brain_idle` (priority: **low**) — Brain transitions to idle after work.
  - Burst events on Brain coalesce over a **250ms** in-memory timer before admitting a single director turn.
  - Existing cooldown (`dashboard.companion_agent_cooldown_seconds`), hourly cap (`dashboard.companion_agent_hourly_cap`), and busy flag remain enforced; **high-priority triggers (`user_message`, `error`, `brain_reply`)** preempt pending normal/low coalesced timers and bypass the short cooldown floor (still strictly respecting the hourly cap and busy flag so Puppeteer never floods).
- **Loop-prevention tagging:** Puppeteer commands and tool calls carry an internal non-user origin tag (`source: "puppeteer"`), and `directorObserveRing`/`directorObserveSnapshot` ignore self-originated `companion-command` broadcasts and Puppeteer wake markers so Puppeteer can never trigger itself in a feedback loop.
- **Pure-cosmetic `ws/companion.act` & Puppeteer prompt:**
  - Puppeteer runs on `dashboard.companion_body_tier` (default `small` tier via `ResolveCompanionTier`, falling back cleanly on the ephemeral warm pool `CompanionEphemeralRunner` — zero persistent session overhead).
  - Prompt consumes read-only Persona profile via `resolveAmbientCompanionPersona` (`companion_soul_prompt.go:956-976`), plus the dynamic `<<<ACTIONS>>>` catalog added by `1780` Phase 1 (`companion_soul_prompt.go`).
  - Prompt instructs Puppeteer to emit ONLY VRM expression/gesture/phone/gaze/FX + an optional **1-line natural in-character stage-bubble side-comment/reaction (`quip <= 140 runes`, e.g. `"뭐라는 거야..."`)** reacting to what just happened — strictly for the VRM stage bubble (`.ws-companion-quip`), never answering the user's question and never writing to Messenger.
  - `CompanionAct` (`companion_act.go:288-363`) adds a pure-cosmetic fast path (`CosmeticOnly: true` when invoked by Puppeteer or without an active phone lease): skips `ConsumeUnreadForAct`, skips `RenewLease`, skips blocking animation-duration sleep, and immediately broadcasts `CompanionCommand` with monotonic `Seq` and `Priority`.

### D6 — 0ms Client Physical Reflex & Stage Speech Bubble Polish

- **`dashboard-companion-reflex.js` (<350L, loaded before `dashboard-companion-stage.js`):**
  - Subscribes to compact Messenger send notifications (`wsCompanionCompact: "user-send"`), Brain work/subagent/idle signals (from SSE / Messenger bridge), and incoming `companion-command` payloads.
  - On `user-send`: immediately (same frame, 0ms network wait) calls `rig.triggerGesture("phone_pickup")` (routing through `colead.js:842-859` so `1780` Phase 2 `phone_hold` sentinel overrides play automatically) and arms a short client reflex lease (`REFLEX_LEASE_MS = 6000`).
  - While the reflex lease is active: suppresses redundant snapshot `buzz` transitions and exposes `isReflexLeaseActive()` via a 1-line hook in `colead.js:328` so `PHONE_UNCONFIRMED_GRACE` (4s) does not flicker the phone down while Puppeteer/Brain is responding.
  - On any authoritative Puppeteer `companion-command` carrying a `phone_*` gesture (with `seq > lastSeq`): immediately releases the reflex lease and yields control to the Puppeteer command.
  - On Brain `brain_work_start` / `subagent_event` or reflex lease idle timeout: safely transitions to `phone_putdown` / `laptop_work` typing so the avatar never gets stuck holding the phone.
- **Stage Speech Bubble Polish (`dashboard-companion-quip.js` + `dashboard-companion-quip.css`):**
  - Extracts `.ws-companion-quip` lifecycle and layout out of `dashboard-companion-stage.js:1090-1102,1242-1248` into `C.quip` (1-line hook in `stage.js`).
  - Adds a crisp directional speech tail pointing toward the VRM head, head-relative anchoring (using `rig.getCharacterBounds()` / `bodyBox` with viewport clamping so it never clips off-screen or overlaps `.shell-bottom`), GPU-composited `transform`/`opacity` enter/exit transitions (respecting `prefers-reduced-motion` and `data-extra-motion="off"`), and distinct tonal styling (`--wb-card` / subtle `--wb-primary` accent border-glow via `box-shadow`, clearly distinct from Messenger chat bubbles).
  - Enforces strict `textContent`-only DOM assignment (zero `innerHTML`) and preserves the 140-rune cap (`companion_command.go:23-29`).

### D7 — Release Compatibility Tier: PURE T0 / T1 (NO T3, NO Minor Bump)

Per `ai-docs/manuals/release-compatibility.md`, every cross-version surface touched by this ticket is strictly **T0 (transparent)** or **T1 (display-degraded)** — patch-safe with **zero T3 boundaries and zero minor version bump**:

| Surface | Change | Tier | Verified Source Lines & Rollback/Coexistence Proof |
|---|---|---|---|
| Config keys `dashboard.companion_body_tier`, `dashboard.companion_work_tier` | Key names, regex patterns, `default_scope`, and `wsconfig.Overrides` scalar map shape unchanged; only human-facing Settings labels/descriptions updated across `ws` + `wsflow` manifests and 11 `wsi18n` tables | **T0** | `wsconfig/config.go:37-40` (`Overrides map[string]string`), `companion_config.go:36-37`, `agents-plugin/dashboard-config-manifest.json:193-207`, `agents-plugin-wsflow/dashboard-config-manifest.json:193-207`; old binary reads/writes the exact same scalar strings without loss |
| Legacy MCP tools (`companion.phone_pickup`, `phone_read`, `phone_send`, `phone_putdown`, `delegate_work`, `delegation.report`) | Kept registered in MCP tool table as safe deprecated no-op/redirect aliases returning structured deprecation guidance; never removed in a patch | **T0** | `mcp/companion_tools.go:18-27,153-355,504-756`; existing sessions/prompts calling any legacy tool succeed cleanly without unknown-tool errors or deadlocks |
| Memory tools (`companion.remember_soul`, `remember_episode`, `recall_memory`) | Schemas and tool names unchanged; role gating moves write access from `companion-body` to `companion-work` | **T0** | `mcp/companion_tools.go:24-26,275-355`, `mcp/server_inventory.go:40-156,326-387`; no wire or schema change |
| Persisted stores (`ws-companion-soul/v1`, `companion-episodes.sqlite` v1, `companion-messenger/seg-*.jsonl`) | Zero on-disk schema, filename, column, or JSONL field changes; optional Brain seed reads `seg-*.jsonl` read-only | **T0** | `companion_soul.go:34,54-61`, `companion_episodes.go:22-23,121-164`, `companion_messenger_ring.go:26-50,196-230`; reopening with an older binary reads and writes all stores identically |
| `CompanionCommand` SSE payload (`seq`, `priority`) | Additive optional JSON fields (`seq?: int64`, `priority?: string`, `omitempty`) | **T0** | `companion_command.go:12-21`, `assets/dashboard-companion-scenarios.js:255-282` (`directedCommand` reads only fixed allowlisted keys and ignores unknown fields) |
| Compact query param `/s/<rid>?frame=1&compact=1` | Additive query param controlling `data-compact="1"` attribute and `dashboard-compact.css` styling | **T1** | `page_routes.go:221-287,326-327`; an older daemon ignores `&compact=1` and serves the standard `?frame=1` session page read-only with no data loss |
| `GET /api/companion/brain` route | New local-viewer read/ensure endpoint | **T0** | `companion_pack_routes.go:15-28`; older daemon answers 404 and the Messenger UI falls back cleanly without mutating config or stores |

### D8 — File-Size Budgets, File-Touch Discipline & Frontend Update Budget

- **File-size budgets:** Every new file stays cleanly `< 1,000L` (target `< 500L`). `dashboard-conversation.js` (1,860L) has **0 lines edited**. Splitting `dashboard-companion-messenger-md.js` (~260L) reduces `dashboard-companion-messenger.js` from 1,225L to `< 950L`. `dashboard-companion-stage.js` (1,702L) and `dashboard-companion-vrm-colead.js` (1,009L) receive only 1–2 line delegation hooks.
- **Windows AV file-touch budget (`performance.md`):**
  - 0 file touches per 0ms client reflex, per dropped Puppeteer trigger, or per warm Puppeteer `act`.
  - Brain session persists only via existing hosted append-only ring/event stream (Type 1/2).
  - Optional legacy `companion-messenger/seg-*.jsonl` read occurs at most once when initializing a brand-new Brain session, never on steady-state opens or turns.
- **Frontend update budget (`performance.md`):**
  - Zero new `requestAnimationFrame` loops (reflex uses lightweight `setTimeout` lease expiration and calls existing VRM `markVrmActivity()`).
  - When the Messenger popup is closed, the controller sends `{ wsShell: "shown", shown: false }` to the compact iframe so `wsHideFrame` (`dashboard-lifecycle.js:1692-1701`) pauses its SSE/WebSocket stream immediately (zero idle background streaming cost).
  - Stage speech bubble animations use only GPU-composited `transform` and `opacity`.

## Constraints

- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/wsflow-mirroring.md` (declared for `agents-plugin-wsflow/`)
- Convention: `ai-docs/manuals/ws-mcp.md` (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/config-settings-ui.md` (declared for `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-wsflow/dashboard-config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
- Pure **T0 / T1** compatibility: no T3 boundary, no minor bump, no removal of legacy MCP tools or config keys.
- `dashboard-conversation.js` (1,860L) must NOT be edited; all new files must remain `< 1,000L`.
- Mirror `dashboard.companion_body_tier` and `dashboard.companion_work_tier` manifest updates across both `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json`, and keep all 11 `wsi18n` locale tables (`de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`) in 100% key parity.
- Keep zero third-party `.vrm` binaries in git or embedded in the Go binary; Playwright real-VRM checks run conditionally via `WS_VRM0_SAMPLE` and `WS_VRM1_SAMPLE`.
- All code comments, commit messages, and ticket prose stay in English; only ticket frontmatter `title:` is in Korean.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Spans `cmd/ws-mcp/companion_runtime.go`, `cmd/ws-mcp/dashboard.go`, `internal/wsagent/runner_{create,instructions,lead_start,companion}.go`, `internal/mcp/{companion_tools,server_inventory}.go`, `internal/wsdashboard/{companion_director,companion_act,companion_command,companion_phone,companion_pack_routes,page_routes,page_regions,pages,live_hosted}.go`, new `companion_brain_route.go`, frontend assets (`dashboard-compact.css`, `dashboard-companion-reflex.js`, `dashboard-companion-quip.js/.css`, `dashboard-companion-messenger-md.js`, `dashboard-companion-messenger.js`, minimal hooks in `stage.js`/`colead.js`/`dashboard-lifecycle.js`/`dashboard-composer-delivery.js`), `agents-plugin/` + `agents-plugin-wsflow/` manifests, and 11 `internal/wsi18n/tables/*.json` |
| scope.surface | cross-module | Touches hosted Lead launch (`wsagent`), MCP role-based tool filtering & deprecated aliases (`mcp`), daemon HTTP & SSE/director event bus (`wsdashboard`), embedded compact session frame (`?frame=1&compact=1`), frontend VRM reflex/quip modules, and mirrored `ws`/`wsflow` config manifests + 11 locales |
| scope.new_public_symbol | yes | `Daemon.SetCompanionBrain`, `Daemon.SetCompanionBrainReplyID`, `CompanionActRequest.CosmeticOnly`, `CompanionCommand.Seq`/`Priority`, `GET /api/companion/brain`, `window.WsCompanion.reflex`, `window.WsCompanion.quip` |
| scope.new_type_contract | yes | Additive `GET /api/companion/brain` JSON response, additive `CompanionCommand.{seq,priority}` fields, `?frame=1&compact=1` iframe `postMessage` contract (`wsCompanionCompact: "user-send"`), all strictly T0/T1 |
| scope.test_surface | existing | Existing Go tests (`companion_runtime_test.go`, `companion_director_test.go`, `companion_phone_test.go`, `companion_act_test.go`, `pages_routes_test.go`, `companion_vrm_browser_test.go`, `asset_source_test.go`), Node tests (`frontend-companion*.test.js`, `frontend-settings*.test.js`), and `wsflow` parity tests |
| complexity.reuse_points | confirmed | Reuses `/s/<rid>?frame=1` session page & SSE/WS stream (`page_routes.go:221-287`), `#convo`/`#agcompact`/`#apbox`/`#qcbox`/`#composer` (`dashboard.tmpl:27-31`), `CompanionEphemeralRunner` warm pool (`companion_director.go:73-81`), `RotateCompanion` + `CompanionHandoffCheckpoint` (`runner_companion.go:18-37,99-102`), `ResolveCompanionTier` (`companion_tier.go:127-159`), and `triggerGesture`/`setPhoneState` (`colead.js:842-890`) |
| complexity.side_effect_risk | moderate | Must prevent Puppeteer self-trigger feedback loops, avoid clobbering main shell frame state from the compact iframe, pause compact iframe streams when Messenger closes, and preserve T0 backward compatibility for legacy MCP tools and persisted Soul/Episodes stores |
| risk.correctness | moderate | Cross-harness Brain handoff, compact-frame `shStaged`/`shHidden` visibility lifecycle, 250ms coalescing + priority preemption races (`-race`), and reflex-lease handoff to Puppeteer `phone_*` commands |
| risk.fit | low | Directly follows existing `/s/<rid>?frame=1`, `companion_agents_route.go`, `companion_director.go`, and `window.WsCompanion` module conventions |
| risk.test | moderate | Requires Go `-race` tests, Node DOM-op/reflex unit tests, 11-locale & `wsflow` parity checks, and Playwright real-VRM 0.x/1.0 E2E visual QA |
| risk.security_or_contract | moderate | `/api/companion/brain` must enforce `localBrowseViewer`, compact iframe must enforce `frameAncestorsSelf` same-origin `postMessage`, `.ws-companion-quip` must remain strict `textContent`-only, and every cross-version surface must stay T0/T1 |

## Non-Scope

- Removing legacy MCP tools (`companion.phone_*`, `companion.delegate_work`, `delegation.report`) or removing/renaming `dashboard.companion_body_tier` / `dashboard.companion_work_tier` config keys in a patch release.
- Introducing any T3 compatibility boundary or minor version bump.
- Editing `dashboard-conversation.js` (1,860L) or altering primary user coding Lead behavior, rail visibility (`Brain` stays `Listed = false`), or ticket workflows.
- Changing on-disk schemas of `ws-companion-soul/v1`, `companion-episodes.sqlite` (v1), or `companion-messenger/seg-*.jsonl`.
- Committing or embedding third-party `.vrm` binaries in the repository or Go binary.

## Phases

### Phase 1: Brain Full Lead Promotion (`companion-work` + `/api/companion/brain` + Cross-Harness Handoff)

1. **Promote `companion-work` to Mobile Full Lead (`internal/wsagent/` & `internal/mcp/`):**
   - Update `runner_lead_start.go:74-80,326-331`, `runner_instructions.go:61-107`, and `runner_create.go:396-400` so `CompanionRole == "companion-work"` receives the full Lead workflow manual (`composeLeadInstructions`), `leadStartStateMarker`, and playbook command seeding, augmented with the active Companion Persona + Soul/Episodes system addendum (reusing `CompanionActiveSoulLaunch` from `companion_soul_prompt.go:956-976`), while keeping `Listed = false`, `ConvHistoryAccess = false`, and `DashboardControlAccess = false` (`runner_create.go:322-328`) so Brain stays unlisted in the main rail.
   - Update `internal/mcp/server_inventory.go:40-156,247-273,326-387` so `companion-work` (Brain) gets full Lead tools (`ws/compact`, `agenda.set`, `agent.spawn`, `agent.*`, `exec.*`, `git.*`, `tickets.*`, `mailbox.*`), auto-allowed `ws/daemon.*` (without `companionWorkApprovalGate`), and exclusive ownership of `companion.remember_soul`, `companion.remember_episode`, and `companion.recall_memory` — while excluding `companion.act` and `companion.phone_*`.
2. **Persistent Brain lifecycle & cross-harness handoff (`cmd/ws-mcp/companion_runtime.go`, `cmd/ws-mcp/dashboard.go`):**
   - Promote `ensureWorkSession` (`companion_runtime.go:1005-1040`) into `EnsureBrainSession(d *wsdashboard.Daemon)` returning `(BrainInfo, error)` with reattach across restarts (`CompanionSessions` in `runner_companion.go:46-81`).
   - When `dashboard.companion_work_tier` resolves to a different harness/model/effort than the live Brain session, perform a clean cross-harness handoff via `RotateCompanion` + `CompanionHandoffCheckpoint` (`runner_companion.go:18-37,99-102`) carrying Persona/Soul/Episodes + recent conversation digest.
   - On a brand-new Brain session with an empty ring, perform a one-time read-only check of legacy `companion-messenger/seg-*.jsonl` (`companion_messenger_ring.go:196-230`): if recent messages exist, inject a bounded (`<= 10` entries, `<= 4 KiB`) read-only context seed into the initial launch prompt so upgrading users keep conversational continuity (zero writes to legacy segments).
   - Register Brain's active `replyID` on the daemon (`d.SetCompanionBrainReplyID(replyID)`) and stop launching the old persistent Tier-1 `companion-body` chat session from `Start()`/`OnUserWake()` (keep `reattachBodyLocked` graceful teardown for any surviving pre-upgrade body session).
3. **`GET /api/companion/brain` endpoint (`internal/wsdashboard/companion_brain_route.go` <200L):**
   - Add `Daemon.SetCompanionBrain(fn func() (CompanionBrainInfo, error))` and register `GET /api/companion/brain` inside `registerCompanionPackRoutes` (`companion_pack_routes.go:15-28`), gated on `localBrowseViewer` (404/403 matching `companion_agents_route.go:29-43`), returning `{ replyId, wsKey, harness, model, tier, personaName, ready, seededFromLegacy }`.
   - Wire `daemon.SetCompanionBrain(companion.BrainEndpoint)` in `cmd/ws-mcp/dashboard.go:353-357`.
4. **Verification:**
   - `go test -race -count=1 ./cmd/ws-mcp -run 'TestCompanion'` covering Brain Full Lead manual/tool surface (`ws/compact`, `agenda.set`, `agent.spawn`, `ws/daemon.*`, `remember_soul`/`remember_episode`/`recall_memory`, refusal of `companion.act`), `GET /api/companion/brain` auth/lazy ensure/legacy seed (0 writes, <=1 read), and cross-harness handoff on `dashboard.companion_work_tier` change.
   - `go test -race -count=1 ./internal/mcp ./internal/wsagent -run 'TestCompanion'` verifying role gating and Lead instruction composition.

### Phase 2: Messenger Compact Frame Embed (`/s/<brainRid>?frame=1&compact=1`, `dashboard-compact.css`, Send Bridge & Markdown Split)

1. **Compact session frame support (`page_routes.go`, `page_regions.go`, `dashboard.tmpl`, `pages.go`):**
   - Extend `sessionPageData` (`page_regions.go:41-64`) with `Compact bool` (set in `handleSessionPage`, `page_routes.go:221-287`, only when `framedRequest(r) && r.URL.Query().Get("compact") == "1"`).
   - Emit `data-compact="1"` on `<html>` in `dashboard.tmpl:2` when `.Compact` is true; keep `admitShellFrame(w, sessionPageCSP(true))` (`frame-ancestors 'self'`) unchanged.
   - Add `dashboard-compact.css` (<350L) to `dashboardStyleFiles` (`pages.go:128-157`) and `asset_source_test.go`, scoped strictly under `:root[data-compact="1"]`: hides `.top`, `.rail-ph`, `#tabbar`, `#chead`, `#bcol`, `#files`, collapses `#grid` to a single full-height flex column, applies Messenger-density chat-bubble styling (user right-aligned tonal bubble, assistant left-aligned tonal bubble, collapsible tool/exec cards) using `--wb-*` borderless Material tokens (`dashboard-style.md`), compacts `#agisland`/`#agcompact` subagent badges into a crisp bar above `#composer`, and styles `#apbox`/`#qcbox` 1-click approvals & questions for narrow `360px` panels.
   - Zero edits to `dashboard-conversation.js` (1,860L).
2. **Compact frame lifecycle & send `postMessage` bridge (`dashboard.js`, `dashboard-lifecycle.js`, `dashboard-composer-delivery.js`):**
   - In `dashboard.js:62-79`, read `var compact = root.hasAttribute("data-compact");` and keep `shStaged = framed && !compact` so a compact frame renders immediately on load while still honouring `wsShell: "shown"` (`wsHideFrame` / `shHidden`) sent by the Messenger controller when opened/closed.
   - In `dashboard-lifecycle.js:1565-1751`, when `compact` is true, suppress `queueHead`/`title`/`companion-gate` relay to the main shell (`startCompanionRelay` never runs for Brain's own compact frame) while still listening for same-origin parent `wsShell: "shown"` and `wsShell: "theme"` messages.
   - In `dashboard-composer-delivery.js:122-126` (right after `outboxAdd`), add a 1-line compact hook: if `compact`, `window.parent.postMessage({ wsCompanionCompact: "user-send", rid: rid }, location.origin)`.
3. **Split markdown helper & wire Messenger panel to compact iframe (`dashboard-companion-messenger-md.js`, `dashboard-companion-messenger.js`, `dashboard-companion-messenger.css`):**
   - Extract the ~260L safe-markdown/badge helper (`dashboard-companion-messenger.js:203-462`) into `dashboard-companion-messenger-md.js` (<300L), registered right before `dashboard-companion-messenger.js` in `companionScriptFiles` (`pages.go:104-121`), `asset_source_test.go`, and JS test loaders (`frontend-companion*.test.js`).
   - Update `dashboard-companion-messenger.js` (now `< 950L`): on open, call `GET /api/companion/brain`, mount `<iframe class="ws-cm-frame" src="/s/<brainRid>?frame=1&compact=1" title="...">` inside the Messenger body, sync current theme (`wsShell: "theme"`), send `wsShell: "shown", shown: true` on open and `shown: false` on close (pausing background SSE/WS), and emit `C.reflex?.onUserSend?.()` on receiving `{ wsCompanionCompact: "user-send" }` from that exact iframe's `contentWindow` & same origin.
   - Add an optional **"Open in full tab"** icon button (`window.open("/s/" + brainRid, "_blank", "noopener")`) in the Messenger header (`dashboard-companion-messenger.css`).
   - When `/api/companion/brain` returns 404 or `ready: false`, show a clean tonal status/retry state (preserving read-only legacy fallback cleanly).
4. **Verification:**
   - `go test -race -count=1 ./internal/wsdashboard -run 'TestPages|TestAssets|TestDevAssets|TestDashboardAssetSourceAssembly|TestCompanion'` verifying `?frame=1&compact=1` HTML attribute, CSP `frame-ancestors 'self'`, and bundle assembly.
   - `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion*.test.js` covering compact iframe mount/open/close `shown` pause, theme sync, origin-checked `user-send` bridge, and pop-out button.

### Phase 3: Lightweight Puppeteer Event Bus (`companion_director.go` + Pure-Cosmetic `act` + Side-Comment Prompt)

1. **Direct Brain event observation (`companion_director.go`, `live_hosted.go`):**
   - Add `Daemon.SetCompanionBrainReplyID(rid string)` (atomic pointer/string, zero lock contention).
   - Update `directorObserveSnapshot` / `directorObserveRing` (`companion_director.go:258-325`) and `directorFire` (`:446-508`) so events from Brain's `replyID` are observed directly without requiring tab focus and WITHOUT blocking on `EmbodimentLeaseActive()`. Normal focused-tab ambient triggers remain supported when Brain is idle.
   - Map Brain snapshot/ring transitions onto the 5 Puppeteer triggers: `user_message` (priority `high`), `brain_work_start` (`normal`), `subagent_event` (`normal`), `brain_reply` (`high`), `brain_idle` (`low`), plus `error` (`high`).
2. **250ms coalescing, priority preemption & loop-prevention tagging (`companion_director.go`):**
   - Add a 250ms per-session coalescing window in `companionDirector` that folds burst events into the highest-priority trigger before calling `admitLocked`.
   - High-priority triggers (`user_message`, `error`, `brain_reply`) preempt pending lower-priority coalesced waits and bypass the short cooldown floor while strictly honouring the hourly cap and busy flag.
   - Tag Puppeteer commands/events with `source: "puppeteer"` and filter out self-originated `companion-command` / Puppeteer markers in `directorObserveRing` and `directorObserveSnapshot` so Puppeteer cannot self-trigger.
3. **Puppeteer tier, pure-cosmetic `act` & side-comment prompt (`companion_director.go`, `companion_act.go`, `companion_command.go`):**
   - Route Puppeteer's ephemeral call tier through `dashboard.companion_body_tier` (default `small` via `ResolveCompanionTier`), running on `CompanionEphemeralRunner` (warm pool, bounded rotation — zero persistent session or disk files).
   - Add optional `CosmeticOnly bool` on `CompanionActRequest` (`companion_act.go:16-67,288-363`): when true (or when no phone lease is active), skip `ConsumeUnreadForAct`, `RenewLease`, and blocking sleep, broadcasting `CompanionCommand` immediately with monotonic `Seq` (`atomic.Int64`) and `Priority`.
   - Upgrade `companionPrompt` (`companion_director.go:873-959`) to inject read-only Persona voice (`resolveAmbientCompanionPersona`, `companion_soul_prompt.go:956-976`) and the `1780` Phase 1 `<<<ACTIONS>>>` catalog, instructing Puppeteer to drive VRM expressions/gestures/phone/gaze/FX and an optional **1-line natural stage-bubble side-comment/reaction (`quip <= 140 runes`)** strictly for `.ws-companion-quip` — never replying in Messenger and never delegating work.
4. **Verification:**
   - `go test -race -count=1 ./internal/wsdashboard -run 'TestCompanionDirector|TestCompanionAct|TestCompanionCommand'` testing all 5 triggers on Brain `replyID` while unfocused and regardless of lease, 250ms coalescing, high-priority preemption, hourly cap enforcement, loop-prevention drop, pure-cosmetic `act` zero-wait / zero-unread-drain, and 140-rune quip clamping.

### Phase 4: 0ms Client Physical Reflex + Stage Speech Bubble Polish + Settings UI & 11 Locales

1. **0ms Client Physical Reflex (`dashboard-companion-reflex.js` <350L, minimal hooks in `stage.js` & `colead.js`):**
   - Add `dashboard-companion-reflex.js` to `companionScriptFiles` (`pages.go:104-121`), `asset_source_test.go`, and JS test loaders (`frontend-companion*.test.js`).
   - Implement `C.reflex`:
     - `onUserSend()`: immediately triggers `rig.triggerGesture("phone_pickup")` (compatible with `1780` Phase 2 `phone_hold` sentinel override) and starts a 6s client reflex lease.
     - `onCommand(payload)`: monotonic `seq` guard; when an authoritative Puppeteer `phone_*` command arrives, releases the reflex lease and yields immediately.
     - `onBrainState(kind)`: on `brain_work_start` / `subagent_event` / `brain_idle` or lease timeout, safely transitions back to `phone_putdown` / laptop typing.
     - `isLeaseActive()`: suppresses redundant snapshot `buzz` in `stage.js` (1-line hook in `phoneState`) and suppresses the 4s `PHONE_UNCONFIRMED_GRACE` revert in `colead.js:328` (1-line hook) while active.
2. **Stage Speech Bubble Polish (`dashboard-companion-quip.js` <300L + `dashboard-companion-quip.css` <180L):**
   - Register `dashboard-companion-quip.js` in `companionScriptFiles` and `dashboard-companion-quip.css` in `companionStyleFiles` (`pages.go:104-126`).
   - Delegate `.ws-companion-quip` creation, text assignment (`textContent`-only, 140-rune cap), head-relative anchoring (with viewport + `.shell-bottom` clamping), directional speech tail, and GPU-composited `transform`/`opacity` enter/exit animations from `dashboard-companion-stage.js:1090-1102,1242-1248` to `C.quip` (2-line hook in `stage.js`).
   - Style with distinct borderless Material tonal contrast (`--wb-card`, subtle `--wb-primary` edge glow, directional tail) clearly separated visually from Messenger chat bubbles.
3. **Settings UI labels, mirrored manifests (`ws` + `wsflow`) & 11 locales:**
   - Keep `dashboard.companion_body_tier` and `dashboard.companion_work_tier` keys intact (T0). Update descriptions in both `agents-plugin/dashboard-config-manifest.json:193-207` and `agents-plugin-wsflow/dashboard-config-manifest.json:193-207` so Body clearly denotes **Lightweight Cosmetic Puppeteer** and Work clearly denotes **High-Tier Mobile Full Lead (Brain)**.
   - Update `js.settings.key.dashboard.companion_body_tier.{label,description}`, `js.settings.key.dashboard.companion_work_tier.{label,description}`, and `js.settings.companion.agents.{body_hint,work_hint}` across all 11 locale files in `internal/wsi18n/tables/` (`de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`), plus new i18n keys for Messenger "Open in full tab" and Brain fallback status.
   - Update `dashboard-settings-companion-agents.js` / `AgentsReadback` labels to match Puppeteer (Body) vs Full Lead (Brain).
4. **Verification:**
   - `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion*.test.js agents-plugin-tool/internal/wsdashboard/frontend-settings*.test.js` covering 0ms reflex lease, 4s-revert suppression, Puppeteer `seq` yield, idle/work fallback, quip `textContent`-only XSS safety & clamping, and Settings companion-agent labels.
   - `go test -race -count=1 ./internal/wsi18n ./internal/wsdashboard -run 'Test.*Locale|Test.*Settings|Test.*Config|TestCompanion'` verifying 11-locale key parity and manifest/catalog alignment.

### Phase 5: Legacy Tool Deprecation Aliases, Dead FIFO Removal & Full Verification

1. **Legacy MCP tool T0 deprecation aliases & dead FIFO cleanup (`internal/mcp/companion_tools.go`, `cmd/ws-mcp/companion_runtime.go`):**
   - Retain `companion.phone_pickup`, `companion.phone_read`, `companion.phone_send`, `companion.phone_putdown`, `companion.delegate_work`, and `delegation.report` registered in `companion_tools.go` as safe deprecated T0 no-op/redirect aliases returning structured `{ deprecated: true, notice: "..." }` responses (never failing with unknown-tool errors or blocking on leases).
   - Remove the dead single-slot delegation FIFO dispatch/queue/deadline machinery (`companion_runtime.go:521-682,730-755`) and dead Body-owns-Messenger wake loop while keeping `CompanionPhoneStore` read-only history hydration (`companion_messenger_ring.go`) intact for legacy seed reads.
   - Keep `CompanionRuntime` clean and under 1,000L.
2. **Full verification & real-VRM E2E visual QA:**
   - Package asset & `wsflow` parity checks:
     - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`
     - `go test -race -count=1 ./internal/wsconfig ./internal/mcp ./internal/wsagent ./internal/wsdashboard ./internal/wsi18n ./cmd/ws-mcp`
     - `wsflow` package tests (manifest mirror + skill-shim parity).
     - `GOOS=windows go vet ./...`
   - Node frontend suites:
     - `node --test agents-plugin-tool/internal/wsdashboard/frontend-companion*.test.js agents-plugin-tool/internal/wsdashboard/frontend-settings*.test.js` (including DOM-op & reflex lease unit tests).
   - Playwright E2E visual QA (`TestCompanionVrm*` + new compact-messenger/reflex/quip browser test in `internal/wsdashboard/`, run with `WS_VRM0_SAMPLE` and `WS_VRM1_SAMPLE` when locally populated):
     - Messenger opens compact `<iframe src="/s/<brainRid>?frame=1&compact=1">`, streams live assistant bubbles + `#agcompact` subagent badge + `#apbox`/`#qcbox` 1-click approval, closes and pauses frame stream (`shHidden`).
     - Sending a message triggers instant 0ms `phone_pickup` with zero 4s flicker, yields cleanly to Puppeteer `phone_*` + expression/quip, and falls back on work-start/idle.
     - Stage speech bubble (`.ws-companion-quip`) renders head-anchored with speech tail, crisp dark/light contrast, zero overlap with Messenger bubbles or `.shell-bottom`, and zero console/page errors across both VRM 0.x and VRM 1.0.
     - Confirm `dashboard-conversation.js` has `0` diff lines against `develop` and every new file is `< 1,000L`.
