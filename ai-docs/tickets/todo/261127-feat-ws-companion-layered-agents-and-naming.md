---
title: "컴패니언 명칭 정리 및 2계층 에이전트(경량 리액션 + 메신저 총괄·대시보드 UI 제어) 설계"
parent: 261122-epic-werkschaft-lead-companion
related:
  261126-research-ws-companion-hybrid-vrm-bust-spike: VRM humanoid character shift motivating naming review
  261126-feat-ws-companion-vrm-engine: VRM renderer and companion runtime foundation
---

# Companion Naming Cleanup and 2-Tier Layered Agent Architecture (Live Reaction + Messenger Coordinator with Dashboard UI Control)

## Background

As Werkschaft's companion evolves from early 2D paper-doll/desktop-pet prototypes toward humanoid VRM characters (`261126-feat-ws-companion-vrm-engine`), two architectural gaps have emerged:

1. **Terminology mismatch:** Active code, config (`dashboard.companion_*`), i18n (`internal/wsi18n/.../en.json:2905-2964`), and JS assets (`dashboard-companion-*.js`) already use `companion` / `companion character`, while legacy comments (`companion_legacy.go:21,25,57,70`), manuals (`ai-docs/manuals/companion-pet-rigging.md`), and epic/research titles still refer to `companion pet` / `desktop pet`.
2. **Single-tier cold director vs layered companion interaction:** Today `companion_director.go:511-563` runs a cold-spawn `wsagent.RunOneShot` (currently Claude-only) to emit `companion-command` SSE events on `/rail/events` (`sse.go:612-647`). Users need a **2-tier layered companion**:
   - **Tier 1 — Lightweight Live Reaction Agent:** Runs on a warm-cached rotating context window (built on the common ephemeral/lightweight harness mode discussed in whiteboard topic `ephemeral-lightweight-harness`, avoiding per-turn cold boot and disk persistence). Drives real-time visual/audio presence via action tools such as `ws/companion.speak`, `ws/companion.face`, and `ws/companion.motion`, and links to the Tier 2 messenger agent.
   - **Tier 2 — Companion Messenger (Coordinator Agent):** Touching/clicking the companion opens a messenger-style chat drawer/window (KakaoTalk-like conversation UI). Conceptually a **top-level coordinator agent** with the same visibility and control level as the user viewing the dashboard — able to inspect what is on screen (including live dashboard UI via low-level tools like `ws/daemon.curl` and `ws/daemon.dom|screenshot`), navigate the user to settings, or broadcast instructions to all live agents (e.g., *"tell all active agents to use Claude from now on"*).

## Current Codebase Surface (Verified 2026-11-27)

- **Companion backend & UI:** Lives in `agents-plugin-tool/internal/wsdashboard/companion_*.go` and `assets/dashboard-companion-*.js` + `assets/shell.js` (`postMessage` bridge). No `internal/wscompanion/` package exists; `desktop/src/*.rs` has 0 `companion`/`pet` references.
- **Daemon visibility gap:** As noted at `companion_director.go:29-33`, the daemon cannot directly observe live browser/WebView2 DOM, active iframe tabs, or open modals; today `shell.js` reports focus back via `POST /api/companion/focus` using a per-shell `companion-focus` SSE token (`sse.go:622-631`).
- **Missing low-level control tools:** Daemon HTTP routes (`/api/config/*`, `/agents/*`, `/session-hire/roster`, `/rail/*`) and MCP tools (`mailbox.*`, `session.roster`, `config.*`) exist, but no `ws/daemon.curl`, `ws/daemon.dom`, `ws/daemon.screenshot`, or `ws/companion.{speak,face,motion}` MCP tools exist yet.

## Open Decision Queue

*(Tracked on whiteboard topics `companion-layered-agents` and `ephemeral-lightweight-harness`; last-ID: 1733)*

- `(1727)` [policy] Official companion terminology cleanup (`Companion` / `컴패니언` vs alternative name) as humanoid VRM characters replace `pet`/`desktop pet` phrasing.
- `(1728)` [policy] Companion messenger coordinator authority level (user-equivalent control with inline confirmation gate for high-impact mutations vs unrestricted immediate execution vs read/notify-only first).
- `(1729)` [policy] Dashboard UI inspection & control bridge architecture (`ws/daemon.curl` + `shell.js` reverse SSE↔POST bridge for `ws/daemon.dom|screenshot|ui` vs DevTools/CDP attachment vs API-only Phase 1).
- `(1730)` [policy] Lightweight reaction agent context rotation and messenger link policy (warm-cached bounded rotation + thin messenger summary vs stateless 1-shot vs single shared session).
- Related dependency decisions on `ephemeral-lightweight-harness`: `(1731)` common `EphemeralPool` harness scope & native no-persistence probes, `(1732)` warm pool lifecycle/TTL & clear/rotation modes, `(1733)` coupling with `agent.spawn` dynamic tier routing.

## Constraints

- **Performance & Windows AV (`ai-docs/manuals/performance.md`):** Live reaction ticks must not create per-tick `wsmeta`/ring/transcript files on disk (zero file touches on the hot reaction path).
- **Security boundary:** `ws/daemon.curl` and `ws/daemon.dom|screenshot|ui` must remain restricted to authorized coordinator/companion surfaces (bearer token + same-host/loopback trust boundary; never exposed to untrusted remote/LAN viewers or arbitrary worker subagents).
- **Compatibility (`ai-docs/manuals/release-compatibility.md`):** Preserve `dashboard.companion_*` keys (T0) unless an explicit compatibility tier is settled in `## Decisions`.

## Phases

### Phase 1: Naming Cleanup & Companion Action Surface (`ws/companion.{speak,face,motion}`)

Settle `(1727)` and `(1730)`; remove stale `pet`/`desktop pet` phrasing from docs, manuals, and legacy comments; expose structured companion expression actions (`speak`, `face`, `motion`) backed by `PublishCompanionCommand` on `/rail/events`, wired to the lightweight rotating reaction agent (consuming the common ephemeral/warm-cached harness pool).

### Phase 2: Low-Level Daemon & UI Control Bridge (`ws/daemon.curl`, `ws/daemon.dom|screenshot|ui`)

Settle `(1728)` and `(1729)`; implement bearer-authenticated internal daemon API access (`ws/daemon.curl`) and the `shell.js` reverse SSE↔POST bridge (`ws/daemon.dom`, `ws/daemon.screenshot`, UI focus/navigation actions) with explicit capability scoping and high-impact confirmation gates.

### Phase 3: Companion Messenger Drawer & Coordinator Wiring

Implement the touch/click messenger UI drawer (KakaoTalk-style chat thread) backed by the coordinator agent, connected to both the live reaction agent and the daemon/UI control bridge so users can ask the companion to inspect the current view, guide/open settings, or broadcast directives across all active agents.
