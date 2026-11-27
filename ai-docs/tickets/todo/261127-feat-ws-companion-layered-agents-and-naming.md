---
title: "컴패니언 명칭 정리 및 2계층 에이전트(경량 리액션 + 메신저 총괄·대시보드 UI 제어) 설계"
parent: 261122-epic-werkschaft-lead-companion
related:
  261126-research-ws-companion-hybrid-vrm-bust-spike: VRM humanoid character shift motivating naming review
  261126-feat-ws-companion-vrm-engine: VRM renderer and companion runtime foundation
  261127-feat-ws-ephemeral-lightweight-harness-pool: shared EphemeralPool / BoundedRotation + ResponseSchema runtime (.done; Phase 2 wired CompanionEphemeralRunner in companion_director.go)
sage-review-design: required
sage-review-completeness: required
---

# Companion Naming Cleanup and 2-Tier Layered Agent Architecture (Live Reaction + Messenger Coordinator with Dashboard UI Control)

## Background

As Werkschaft's companion evolves toward humanoid VRM Co-Lead characters (`261126-feat-ws-companion-vrm-engine`, `344452e4`), two sets of architectural gaps remain:

1. **Terminology mismatch:** Active code, config (`dashboard.companion_*`), i18n (`agents-plugin-tool/internal/wsi18n/tables/en.json#L504-L1666`, 148 lines across 11 locale tables), and JS assets (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-*.js`) already use `companion` / `companion character`, while legacy comments (`agents-plugin-tool/internal/wsdashboard/companion_legacy.go#L21,L25,L57,L70`), manual prose (`ai-docs/manuals/companion-pet-rigging.md#L2,L7`; read by `companion_pack_guide_test.go#L948`), and epic/research titles (`261122-epic-werkschaft-lead-companion`, `261121-research-werkschaft-lead-companion-rig`) still refer to `companion pet` / `desktop pet`.
2. **2-tier layered companion, posture lock, smartphone choreography, and dashboard UI control:**
   - With `261127-feat-ws-ephemeral-lightweight-harness-pool` now `.done`, `agents-plugin-tool/internal/wsdashboard/companion_director.go#L591-L601` runs passive session reactions through `CompanionEphemeralRunner` (`companion_director.go#L73-L81`) -> `companionPoolAdapter` (`agents-plugin-tool/cmd/ws-mcp/dashboard.go#L425-L453`) -> `wsagent.EphemeralPool` (`BoundedRotation`) and broadcasts `companion-command` on `regionRail` (`agents-plugin-tool/internal/wsdashboard/sse.go#L636-L647`).
   - However, `CompanionEphemeralRequest` (`companion_director.go#L57-L69`) and `companionPoolAdapter.Execute` (`cmd/ws-mcp/dashboard.go#L433-L453`) do not yet plumb `ResponseSchema` (supported on `wsagent.EphemeralRequest` at `agents-plugin-tool/internal/wsagent/ephemeral_pool.go#L68`), and there is still no **Tier-1 Fast Triage** -> **Tier-2 Hosted Co-Lead Escalation** loop, no server-enforced **Embodiment Lease & Posture Lock (`laptop | phone`)**, no **cosmetic glass knock / `#companion-chat-btn` messenger popup / VRM smartphone choreography**, and no **2-layer hybrid daemon & UI control bridge (`ws/daemon.curl`, `ws/daemon.dom|screenshot|ui`)**.

## Current Codebase Surface (Verified 2026-11-28)

- **Left-rail bottom-left companion slot & Co-Lead VRM baseline (`344452e4`):** `.rail-session-panel` (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-rail.css#L18`) wraps Sessions + `Unregistered workspaces` + footer, while `#rail-companion-slot` (`dashboard-rail.css#L1006`, `dashboard-companion-stage.js#L549-L605`) owns the bottom-left corner with `0px` vertical jitter and responsive `width: 100%`. `.ws-companion` mounts in `document.body` anchored over `#rail-companion-slot`, and `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm-colead.js` renders the pure-FK laptop typing pose and session-switch swap/dizzy choreography. `desktop/src/*.rs` has 0 `companion`/`pet` references (`rg -i 'companion|pet' desktop/src/` returned nothing).
- **Daemon visibility & reverse-bridge hooks:** As noted at `companion_director.go#L29-L33`, the daemon cannot directly observe live browser/WebView2 DOM, active iframe tabs, or open modals; today `shell.js` reports focus back via `POST /api/companion/focus` using a per-shell `companion-focus` SSE token (`sse.go#L622-L631`), and `KINDS` (`agents-plugin-tool/internal/wsdashboard/assets/shell.js#L71-L105`) governs frame<->shell `postMessage` channels.
- **MCP dispatch, piggyback, and wakeup seams:** `agents-plugin-tool/internal/mcp/server_dispatch.go#L118` invokes `applyMailboxPiggyback` (`agents-plugin-tool/internal/mcp/mailbox_tools.go#L596-L617`) on non-JSON tool replies; `server_inventory.go#L123-L160` implements `callDiscoveryTools` and `sessionToolAccess`; `Runner.Deliver` (`agents-plugin-tool/internal/wsagent/agent_delivery.go#L52`) and `conv.steerMail` (`runner_conv.go#L729`) deliver/steer mail into hosted sessions.
- **Missing surfaces (verified absent):** Search of `agents-plugin-tool/` returned nothing for `companion-chat-btn`, `companion_phone`, `phone_buzz`/`phone_pickup`/`phone_putdown`/`phone_send`/`phone_read`, `ws/daemon.curl`, `ws/daemon.dom`, `ws/daemon.screenshot`, `ws/daemon.ui`, or `/api/daemon/bridge-reply`.

## Decisions

*(Settled on whiteboard topics `companion-layered-agents` and Step 4 Co-Lead architecture; prerequisite `261127-feat-ws-ephemeral-lightweight-harness-pool` is `.done`)*

- **Official Companion naming cleanup — settled `(1727)=a` (T0 transparent):**
  - **Chosen:** Unify official terminology to `Companion` (English) / `컴패니언` (Korean). Keep existing `dashboard.companion_*` config keys unchanged (`T0` compatibility) and remove remaining `pet` / `desktop pet` phrasing from `ai-docs/manuals/companion-pet-rigging.md` (preserving its file path unless `companion_pack_guide_test.go#L948` is updated alongside), legacy comments (`companion_legacy.go`), and epic/ticket titles/references.
  - **Rejected:** Introducing a brand-new product term or renaming `dashboard.companion_*` config keys.
- **Left-Rail Bottom-Left Companion Handover — settled `(148)=a` (already live `344452e4`, T0):**
  - **Chosen:** Keep upper `.rail-session-panel` for Sessions + `Unregistered workspaces` + footer, and keep `#rail-companion-slot` as the dedicated bottom-left corner with `0px` vertical jitter and responsive `width: 100%` (compatible with Variant D `220–360px` resizable rail).
- **Cosmetic Glass Knock, Top-Right Messenger Button & Smartphone Choreography — settled `(146)` & `(149)` (T0):**
  - **Chosen:**
    - Direct character click is **purely cosmetic glass knock** (startle when awake, 3+ rapid knocks -> playful pout/tap, wake-up jolt when sleepy; NEVER opens messenger).
    - Top-right `#companion-chat-btn` opens/toggles a compact floating KakaoTalk-style messenger popup mounted directly under `document.body` (anchored beside `#rail-companion-slot`, viewport-clamped).
    - Sending a message triggers `phone_buzz` (desk phone light/vibration) -> `phone_pickup` (one-hand lift & check + thumb-typing while thinking) -> reply OR cheeky `"메-롱"` + flip phone face-down (`"탁!"`) back on desk if trivial.
  - **Rejected:** Opening the messenger drawer on direct character click (conflicts with playful glass-knock interaction).
- **Server-Enforced Posture Lock (`laptop | phone`), Embodiment Lease & 4-State Lifecycle — settled `(149)` & `(150)=a` (T0):**
  - **Chosen:**
    - Full 4-state lifecycle: `laptop_ambient` (free lease, Tier-1 ambient ticks active) / `laptop_sleepy` (`zZz`) ↔ `phone_active` (`posture = "phone"`, Embodiment Lease locked, work tools hard-blocked by MCP server, thinking plays phone-typing gesture) ↔ `laptop_work` (`posture = "laptop"`, Embodiment Lease locked, `phone_read`/`phone_send` hard-blocked by MCP server, thinking/tools play intense laptop typing).
    - `ws/companion.phone_pickup` atomically switches `posture -> "phone"` AND returns unread inbox messages in 1 round-trip (`phone_read` optional for mid-hold polling), with a turn-end auto-`phone_putdown(normal)` failsafe.
    - `applyMailboxPiggyback` (`agents-plugin-tool/internal/mcp/mailbox_tools.go#L596`, `server_dispatch.go#L118`) appends `[phone buzz: N unread]` on non-JSON tool replies when unread messages arrive.
  - **Rejected:** Advisory/prompt-only posture discipline without MCP server tool gating, or separate `phone_pickup` + `phone_read` round-trips on every wakeup.
- **Tier-1 Fast Triage (`EphemeralPool` + `ResponseSchema`) -> Tier-2 Laptop Escalation & Companion=Agent-Backed — settled `(150)=a` (T0):**
  - **Chosen:**
    - On `phone_buzz`, warm-cached Tier-1 (`EphemeralPool` `BoundedRotation` with strict `ResponseSchema` — zero MCP boot overhead, zero disk I/O) triages in 1 turn: trivial banter/merong is handled directly by daemon-driven `phone_pickup -> phone_typing -> phone_send/phone_putdown(normal|facedown_merong)`; real work triggers `phone_putdown(work)` (keeping the Embodiment Lease held) and wakes/steers Tier-2 Co-Lead (`Runner.Deliver` / `conv.steerMail`) in `laptop_work`, which uses `ws/daemon.*` work tools on laptop and `ws/companion.phone_{pickup,send,putdown}` to reply.
    - Companion Mode assumes the agent-backed pipeline (`dashboard.companion_director` key kept `T0`-compatible with graceful cosmetic fallback if explicitly disabled or unconfigured).
  - **Rejected:** Cold-spawning Tier-2 on every trivial ping, or letting Tier-1 directly execute daemon/UI mutations without Tier-2 escalation.
- **Companion Messenger authority & confirmation gate — settled `(1728)=a`:**
  - **Chosen:** Tier-2 Companion Co-Lead / Messenger operates at **user-equivalent control level** with a **1-click in-messenger confirmation gate** for high-impact mutations (e.g., global config writes, bulk agent model switches, bulk agent interrupts/stops). Read-only inspection and non-destructive UI navigation proceed without extra prompts.
  - **Rejected:** Unrestricted unconfirmed execution of high-impact mutations, or restricting the messenger to read/notify-only.
- **2-layer hybrid low-level control bridge — settled `(1729)=a`:**
  - **Chosen:** Pair (1) bearer-authenticated `ws/daemon.curl` for internal daemon API/config/roster access with (2) an extended `assets/shell.js` SSE-to-POST reverse bridge (`ws/daemon.dom`, `ws/daemon.screenshot`, `ws/daemon.ui`) over `regionRail` `/rail/events` + `POST /api/daemon/bridge-reply` so **both standard browsers and Tauri WebView2** work without DevTools/CDP debug ports.
  - **Rejected:** Attaching CDP/WebView2 remote debugging ports or API-only control with zero live DOM/UI visibility.
- **Lightweight reaction agent rotation & messenger summary sharing — settled `(1730)=a`:**
  - **Chosen:** Tier-1 live reaction / fast triage uses **warm caching + bounded rotation** (`EphemeralPool` `BoundedRotation` resetting on turn count, idle TTL, or token cap), paired with **thin 1-line recent-messenger summary backfeed** from Tier 2 so ambient reactions stay contextually aligned without sharing one heavyweight session.
  - **Rejected:** Cold stateless 1-shot spawns per tick, or collapsing Tier-1 reactions and Tier-2 coordination into one heavyweight session.
- **Promotion posture (supersedes `(1736)=a`):**
  - **Chosen:** With `261127-feat-ws-ephemeral-lightweight-harness-pool` landed in `.done/` and Step 4 decisions `(146)`, `(148)=a`, `(149)`, `(150)=a` settled, clear the resolved `blocked-by` and promote this ticket to `ready/`.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/)
- **Performance & Windows AV (`ai-docs/manuals/performance.md`):** Tier-1 live reaction ticks and Tier-1 `phone_buzz` fast triage must run through `EphemeralPool` (`BoundedRotation`, zero per-tick `wsmeta`/ring/transcript disk I/O), and interactive messenger/SSE hot paths must keep state in memory without per-message file-scan bursts.
- **Security boundary & server-enforced posture lock:**
  - `ws/daemon.curl` and `ws/daemon.dom|screenshot|ui` must remain restricted to the authorized companion Co-Lead / coordinator surface (bearer token + same-host/loopback trust boundary; never exposed to untrusted remote/LAN viewers or arbitrary worker subagents), and high-impact mutations must enforce the 1-click in-messenger confirmation gate (`(1728)=a`).
  - MCP server posture lock (`server_dispatch.go` / `server_inventory.go`) must enforce `laptop | phone` hard blocking server-side (prompt instructions alone are insufficient): `phone` posture blocks work tools; `laptop` posture blocks `phone_read` / `phone_send`; turn-end failsafe guarantees `phone_putdown(normal)` so a dropped or panicked turn never strands the posture lock or Embodiment Lease.
- **Compatibility (`ai-docs/manuals/release-compatibility.md`):** Preserve `dashboard.companion_*` keys (including `dashboard.companion_director`) and existing `shell.js` `companion-focus` handshake semantics (`T0` transparent), with graceful cosmetic fallback if the agent director is explicitly disabled or unconfigured.
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):** Any shipped tool descriptions (`ws/companion.*`, `ws/daemon.*`), prompts, or i18n strings must remain downstream-generic with zero devenv-only citations.
- **Visual & VRM 0.x/1.0 discipline (`ai-docs/manuals/dashboard-style.md`):** Follow borderless Material tonal separation for the floating messenger popup mounted under `document.body`, and conjugate/verify all new VRM smartphone/knock FK poses across both VRM 0.x and VRM 1.0 (`dashboard-companion-vrm.js`).

## Prior Decisions

- 344452e4 (2026-11-28, commit): "Implements owner decision (148)=a: wraps Sessions, Unregistered workspaces, and Settings/Home footer inside .rail-session-panel and hands the dedicated bottom-left corner (#rail-companion-slot) to Co" — bearing: supports
- 261127-feat-ws-ephemeral-lightweight-harness-pool (2026-11-28, Result): "Replaced the stateless CompanionOneShot func seam with the pool-backed CompanionEphemeralExecutor interface across internal/wsdashboard/daemon.go, internal/wsdashboard/companion_director.go, and cmd/ws" — bearing: supports
- 8695b37c (2026-11-28, commit): "Placed the Co-Lead seated typing FK pose, reading/glance-up choreography, soft screen bounce light, and beveled laptop prop into dedicated dashboard-companion-vrm-colead.js (loaded ahead of dashboa" — bearing: supports
- 3e30b026 (2026-11-28, commit): "Keep .ws-companion (hit surface, WebGL canvas, FX canvas, quip bubble, and context menu) mounted in document.body anchored to #rail-companion-slot.getBoundingClientRect() rather than reparenting it" — bearing: constrains
- 261126-feat-ws-companion-vrm-pack-store (2026-11-26, ticket): "Changed ws-companion-vrm/v1 persona sketch to reuse CompanionPersona ({name, voice, quipLanguage}), added companion_director.go + companion_director_test.go ownership across pack-store (renderer exposu" — bearing: constrains
- 261122-feat-ws-companion-director (2026-11-22, ticket): "Key reshape (companion_enabled bool, director rules|agent, companion_mode removed) is free: e79cf6399 is in no release tag, so no released consumer exists." — bearing: constrains
- 77dc4cd6 (2026-11-23, commit): "Compatibility tier T0 for old config.tune calls, a stored 0.51.0 value, and a 0.51.0 reader of what the new binary wrote (the mirror keeps companion_mode in step); T1 for director=agent on a 0.51.0" — bearing: constrains
- 6d786057 (2026-09-13, commit): "Suppressed (not restructured) the piggyback badge for JSON callers: threading a sibling JSON field through arbitrarily-shaped tool results (some of which marshal to a bare array) would have added" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | companion_legacy.go, companion_director.go, new companion_phone.go, sse.go, daemon.go, pages.go, mcp/companion_tools.go, server_dispatch.go, server_inventory.go, mailbox_tools.go, cmd/ws-mcp/dashboard.go, assets/shell.js, assets/dashboard-companion-*.js, wsi18n/tables/*.json, ai-docs/manuals/companion-pet-rigging.md |
| scope.surface | cross-module | new ws/companion.phone_* and ws/daemon.* MCP tools, server posture gate in internal/mcp, /api/companion/phone/* and /api/daemon/bridge-reply HTTP/SSE in wsdashboard, CompanionEphemeralRequest.ResponseSchema seam, shell.js postMessage KINDS |
| scope.new_public_symbol | yes | new MCP tools ws/companion.phone_pickup, phone_read, phone_send, phone_putdown, ws/daemon.curl, ws/daemon.dom, ws/daemon.screenshot, ws/daemon.ui; CompanionEphemeralRequest.ResponseSchema (absent from tree) |
| scope.new_type_contract | yes | 4-state posture lock (laptop\|phone) and Embodiment Lease state machine, SSE companion-phone event payload, bridge-reply reverse-bridge protocol, and Tier-1 ResponseSchema triage JSON schema |
| scope.test_surface | new-files | existing companion_director_test.go, companion_legacy_test.go, companion_vrm_browser_test.go, mailbox_tools_test.go, server_test.go, asset_source_test.go, frontend-companion-vrm.test.js; new tests required for posture lock, phone routes/UI, bridge-reply, and Tier-1->Tier-2 escalation |
| complexity.reuse_points | confirmed | EphemeralPool.Execute + ResponseSchema (ephemeral_pool.go#L65-L70), companionPoolAdapter (cmd/ws-mcp/dashboard.go#L425-L453), applyMailboxPiggyback (mailbox_tools.go#L596), Runner.Deliver (agent_delivery.go#L52), conv.steerMail (runner_conv.go#L729), shell.js KINDS#L71-L105, sse.go regionRail#L622-L647 |
| complexity.side_effect_risk | high | adds hard MCP server tool-blocking gates on server_dispatch.go/server_inventory.go, daemon-internal HTTP/DOM/screenshot control bridge, and Tier-2 Runner.Deliver/steerMail wakeup path |
| risk.correctness | high | Embodiment Lease and 4-state posture transitions across turn-end failsafe, concurrent phone_buzz/pickup/putdown, SSE-to-POST bridge correlation, and Tier-1 to Tier-2 handoff |
| risk.fit | moderate | wsdashboard cannot import wsagent so ResponseSchema and Tier-2 wakeup must cross cmd/ws-mcp/dashboard.go seams; VRM 0.x/1.0 conjugation and document.body overlay anchoring over #rail-companion-slot must hold |
| risk.test | high | real-VRM 0.x/1.0 Playwright phone/knock choreography, browser vs WebView2 reverse-bridge behavior, and end-to-end Tier-1 ResponseSchema to Tier-2 laptop_work escalation require dedicated automated tests |
| risk.security_or_contract | high | bearer-authenticated ws/daemon.curl and DOM/screenshot reverse bridge must stay strictly coordinator-scoped with 1-click confirmation gate for high-impact mutations, plus T0 compatibility for dashboard.companion_* |

## Phases

### Phase 1: Naming Cleanup, Server-Enforced Posture Lock, Embodiment Lease, Real-VRM Knock/Phone Choreography & Messenger Popup UI

1. **Naming cleanup (`(1727)=a`, T0):**
   - Replace remaining `pet` / `desktop pet` phrasing with `Companion` / `컴패니언` in `agents-plugin-tool/internal/wsdashboard/companion_legacy.go`, `ai-docs/manuals/companion-pet-rigging.md` (preserving the file path read by `companion_pack_guide_test.go#L948` or updating both atomically), and active ticket/epic references while keeping `dashboard.companion_*` keys untouched (`T0`).
2. **Server-enforced Posture Lock (`laptop | phone`), Embodiment Lease & `ws/companion.phone_*` MCP tools (`(149)`, `(150)=a`):**
   - Implement daemon-backed phone inbox/state and Embodiment Lease manager (`agents-plugin-tool/internal/wsdashboard/companion_phone.go`), HTTP routes (`/api/companion/phone/*`), and `regionRail` SSE event `companion-phone` (`sse.go`).
   - Add `ws/companion.phone_pickup`, `ws/companion.phone_read`, `ws/companion.phone_send`, and `ws/companion.phone_putdown` (`normal | work | facedown_merong`) in `agents-plugin-tool/internal/mcp/companion_tools.go` (plus `server_schemas.go`, `server_dispatch.go`, `server_inventory.go`).
   - Enforce posture lock in `server_dispatch.go` / `server_inventory.go`: `phone` posture (`phone_active`) locks the Embodiment Lease and hard-blocks work tools; `laptop` posture (`laptop_work`) locks the Embodiment Lease and hard-blocks `phone_read`/`phone_send`; `phone_pickup` atomically switches `posture -> "phone"` and drains/returns unread inbox messages in 1 round-trip; turn-end failsafe auto-invokes `phone_putdown(normal)` on exit.
   - Extend `applyMailboxPiggyback` (`agents-plugin-tool/internal/mcp/mailbox_tools.go#L596`, `server_dispatch.go#L118`) to append `[phone buzz: N unread]` on non-JSON tool replies when unread messenger items exist.
3. **Real-VRM Knock/Phone Choreography, `#companion-chat-btn` & Floating Messenger Popup (`(146)`, `(148)=a`, `(149)`):**
   - Update `dashboard-companion-vrm-colead.js`, `dashboard-companion-vrm.js`, and `dashboard-companion-stage.js`: direct character clicks play purely cosmetic glass-knock reactions (startle when awake, 3+ rapid knocks -> playful pout/tap, wake-up jolt when sleepy, never opening messenger); add 3D desk smartphone prop (`phone_buzz` light/vibration, `phone_pickup` one-hand lift & check + thumb-typing, `facedown_merong` cheeky `"메-롱"` + flip phone face-down `"탁!"` back on desk) conjugated cleanly for VRM 0.x and 1.0, plus 4-state posture visuals (`laptop_ambient`, `laptop_sleepy`, `phone_active`, `laptop_work`).
   - Add top-right `#companion-chat-btn` and a compact floating KakaoTalk-style messenger popup (`dashboard-companion-messenger.{js,css}`, registered in `pages.go` `companionScriptFiles` and `asset_source_test.go`), mounted directly under `document.body` anchored beside `#rail-companion-slot` (viewport-clamped, zero `.rail { overflow: hidden }` clipping) with i18n strings across all 11 `agents-plugin-tool/internal/wsi18n/tables/*.json` locales.
4. **Verification:**
   - Go unit/HTTP tests for `companion_phone.go`, MCP posture lock + `phone_pickup` atomic read + `applyMailboxPiggyback` `[phone buzz: N unread]` + turn-end failsafe, `TestDashboardAssetSourceAssembly`, JS unit tests, and real-VRM 0.x/1.0 Playwright browser QA covering glass knock, `#companion-chat-btn` popup, and phone/laptop posture transitions.

### Phase 2: 2-Layer Hybrid Daemon & UI Control Bridge (`ws/daemon.curl`, `ws/daemon.dom|screenshot|ui`) & 1-Click Confirmation Gate

*Depends on Phase 1 landing (messenger popup UI and posture-gated companion tool surface).*

1. **Bearer-authenticated internal daemon API tool (`ws/daemon.curl`, `(1729)=a`):**
   - Expose `ws/daemon.curl` on the authorized companion Co-Lead / coordinator surface (same-host/loopback bearer-authenticated access to internal daemon API/config/roster endpoints, strictly blocked from untrusted remote/LAN viewers and non-coordinator worker subagents).
2. **Extended `shell.js` SSE-to-POST reverse bridge (`ws/daemon.dom`, `ws/daemon.screenshot`, `ws/daemon.ui`, `(1729)=a`):**
   - Extend `regionRail` SSE events (`sse.go`), `assets/shell.js` `KINDS` (`shell.js#L71-L105`), and `POST /api/daemon/bridge-reply` (`wsdashboard`) so the daemon can request bounded live DOM inspection, viewport/panel screenshots, and non-destructive UI navigation/focus actions identically across standard browsers and Tauri WebView2 without DevTools/CDP debug ports.
3. **1-Click In-Messenger Confirmation Gate (`(1728)=a`):**
   - Route high-impact mutations (global config writes, bulk agent model switches, bulk agent interrupts/stops) through a 1-click approval card inside the Phase 1 messenger popup, while read-only inspection and non-destructive UI navigation execute immediately without extra prompts.
4. **Verification:**
   - MCP capability-gating tests (ensuring worker/untrusted callers cannot reach `ws/daemon.*`), reverse-bridge correlation/timeout tests across `regionRail` -> `shell.js` -> `/api/daemon/bridge-reply`, and browser tests verifying the 1-click confirmation card blocks unconfirmed high-impact mutations and executes upon approval.

### Phase 3: Tier-1 Fast Triage (`EphemeralPool` + `ResponseSchema`) & Tier-2 Hosted Co-Lead Escalation Wiring

*Depends on Phase 1 (phone posture/messenger/SSE) and Phase 2 (`ws/daemon.*` Co-Lead work tools).*

1. **Plumb `ResponseSchema` across the `wsdashboard` <-> `wsagent` boundary (`(150)=a`):**
   - Add `ResponseSchema json.RawMessage` to `CompanionEphemeralRequest` (`agents-plugin-tool/internal/wsdashboard/companion_director.go#L57-L69`) and forward it in `companionPoolAdapter.Execute` (`agents-plugin-tool/cmd/ws-mcp/dashboard.go#L433-L453`) into `wsagent.EphemeralRequest.ResponseSchema` (`agents-plugin-tool/internal/wsagent/ephemeral_pool.go#L68`).
2. **Tier-1 `phone_buzz` Fast Triage Loop (`BoundedRotation` + `ResponseSchema`):**
   - Separate the Tier-1 incoming-message fast triage loop from passive ambient director cooldown/focus drops (`companion_director.go`) while respecting Embodiment Lease exclusivity (`laptop_ambient` / `laptop_sleepy` only; never interrupt `laptop_work` or `phone_active`).
   - Run 1-turn structured JSON triage on warm-cached `EphemeralPool` (`BoundedRotation`, zero MCP boot overhead, zero disk I/O): trivial banter/merong is handled directly via daemon-driven `phone_pickup -> phone_typing -> phone_send / phone_putdown(normal | facedown_merong)`.
3. **Tier-2 Hosted Co-Lead Wakeup/Steer & Thin Summary Backfeed (`(150)=a`, `(1730)=a`):**
   - For real work requests, Tier-1 triggers `phone_putdown(work)` (keeping the Embodiment Lease held) and wakes/steers the Tier-2 hosted Co-Lead session (`Runner.Deliver` at `agents-plugin-tool/internal/wsagent/agent_delivery.go#L52` / `conv.steerMail` at `runner_conv.go#L729`) into `laptop_work`, where Co-Lead uses `ws/daemon.*` work tools on laptop and `ws/companion.phone_{pickup,send,putdown}` to report back.
   - Feed a thin 1-line recent-messenger summary from Tier-2 into Tier-1's bounded-rotation context (`(1730)=a`), preserve the turn-end `phone_putdown(normal)` failsafe, and keep graceful cosmetic fallback when `dashboard.companion_director` is explicitly disabled or unconfigured (`T0`).
4. **Verification:**
   - End-to-end integration tests (`cmd/ws-mcp` + `wsdashboard` + `wsagent`) verifying: (a) trivial `phone_buzz` handled in 1 Tier-1 `ResponseSchema` turn without waking Tier-2 (`facedown_merong` and `normal`), (b) work-classified `phone_buzz` transitions `phone_active -> phone_putdown(work) -> laptop_work`, wakes/steers Tier-2 via `Runner.Deliver`, blocks `phone_read`/`phone_send` until `phone_pickup`, and releases Embodiment Lease on turn-end failsafe, and (c) zero per-tick disk writes on the Tier-1 path.
