---
title: "컴패니언 명칭 정리 및 2계층 에이전트(경량 리액션 + 메신저 총괄·대시보드 UI 제어) 설계"
parent: 261122-epic-werkschaft-lead-companion
blocked-by: 261127-feat-ws-ephemeral-lightweight-harness-pool#phase1
related:
  261126-research-ws-companion-hybrid-vrm-bust-spike: VRM humanoid character shift motivating naming review
  261126-feat-ws-companion-vrm-engine: VRM renderer and companion runtime foundation
  261127-feat-ws-ephemeral-lightweight-harness-pool: shared EphemeralPool / BoundedRotation runtime (Phase 1 prerequisite; Phase 2 wires companion_director.go)
---

# Companion Naming Cleanup and 2-Tier Layered Agent Architecture (Live Reaction + Messenger Coordinator with Dashboard UI Control)

## Background

As Werkschaft's companion evolves from early 2D prototypes toward humanoid VRM characters (`261126-feat-ws-companion-vrm-engine`), two architectural gaps have emerged:

1. **Terminology mismatch:** Active code, config (`dashboard.companion_*`), i18n (`internal/wsi18n/.../en.json:2905-2964`), and JS assets (`dashboard-companion-*.js`) already use `companion` / `companion character`, while legacy comments (`companion_legacy.go:21,25,57,70`), manuals (`ai-docs/manuals/companion-pet-rigging.md`), and epic/research titles still refer to `companion pet` / `desktop pet`.
2. **Single-tier cold director vs layered companion interaction:** Today `companion_director.go:511-563` runs a cold-spawn `wsagent.RunOneShot` (currently Claude-only) to emit `companion-command` SSE events on `/rail/events` (`sse.go:612-647`). Users need a **2-tier layered companion**:
   - **Tier 1 — Lightweight Live Reaction Agent:** Runs on a warm-cached rotating context window built atop `261127-feat-ws-ephemeral-lightweight-harness-pool` (`EphemeralPool` `BoundedRotation`, avoiding per-turn cold boot and disk persistence). Drives real-time visual/audio presence via structured action tools (`ws/companion.speak`, `ws/companion.face`, `ws/companion.motion`) and consumes a thin recent-messenger summary from Tier 2.
   - **Tier 2 — Companion Messenger (Coordinator Agent):** Touching/clicking the companion opens a messenger-style chat drawer/window (KakaoTalk-like conversation UI). Operates as a **top-level coordinator agent** at user-equivalent control level — able to inspect what is on screen (including live dashboard UI via the 2-layer hybrid bridge: `ws/daemon.curl` + `ws/daemon.dom|screenshot|ui`), navigate the user to settings, or broadcast instructions across active agents with a 1-click in-messenger confirmation gate for high-impact mutations.

## Current Codebase Surface (Verified 2026-11-27)

- **Companion backend & UI:** Lives in `agents-plugin-tool/internal/wsdashboard/companion_*.go` and `assets/dashboard-companion-*.js` + `assets/shell.js` (`postMessage` bridge). No `internal/wscompanion/` package exists; `desktop/src/*.rs` has 0 `companion`/`pet` references.
- **Daemon visibility gap:** As noted at `companion_director.go:29-33`, the daemon cannot directly observe live browser/WebView2 DOM, active iframe tabs, or open modals; today `shell.js` reports focus back via `POST /api/companion/focus` using a per-shell `companion-focus` SSE token (`sse.go:622-631`).
- **Missing low-level control tools:** Daemon HTTP routes (`/api/config/*`, `/agents/*`, `/session-hire/roster`, `/rail/*`) and MCP tools (`mailbox.*`, `session.roster`, `config.*`) exist, but no `ws/daemon.curl`, `ws/daemon.dom`, `ws/daemon.screenshot`, `ws/daemon.ui`, or `ws/companion.{speak,face,motion}` MCP tools exist yet.

## Decisions

*(Settled on whiteboard topic `companion-layered-agents`; dependency pool settled on `ephemeral-lightweight-harness` in `261127-feat-ws-ephemeral-lightweight-harness-pool`)*

- **Official Companion naming cleanup — settled `(1727)=a` (T0 transparent):**
  - **Chosen:** Unify official terminology to `Companion` (English) / `컴패니언` (Korean). Keep existing `dashboard.companion_*` config keys unchanged (`T0` compatibility) and remove remaining `pet` / `desktop pet` phrasing from `ai-docs/manuals/companion-pet-rigging.md`, epic/ticket titles, and legacy comments (`companion_legacy.go`).
  - **Rejected:** Introducing a brand-new product term or renaming `dashboard.companion_*` config keys (would require migration/compatibility churn).
- **Companion Messenger authority & confirmation gate — settled `(1728)=a`:**
  - **Chosen:** Tier 2 Companion Messenger (coordinator agent) operates at **user-equivalent control level** with a **1-click in-messenger confirmation gate** for high-impact actions (e.g., global config writes, bulk agent model switches, bulk agent interrupts/stops). Read-only inspection and non-destructive UI navigation proceed without extra prompts.
  - **Rejected:** Unrestricted unconfirmed execution of high-impact mutations, or restricting the messenger to read/notify-only.
- **2-layer hybrid low-level control bridge — settled `(1729)=a`:**
  - **Chosen:** Pair (1) bearer-authenticated `ws/daemon.curl` for internal daemon API/config/roster access with (2) an extended `assets/shell.js` SSE-to-POST reverse bridge (`ws/daemon.dom`, `ws/daemon.screenshot`, `ws/daemon.ui`) over `/rail/events` + authenticated POST callbacks so **both standard browsers and Tauri WebView2** work without DevTools/CDP debug ports.
  - **Rejected:** Attaching CDP/WebView2 remote debugging ports (fragile across browser vs desktop shells and widens local attack surface) or API-only control with zero live DOM/UI visibility.
- **Lightweight reaction agent rotation & messenger summary sharing — settled `(1730)=a`:**
  - **Chosen:** Tier 1 live reaction agent (`ws/companion.speak`, `ws/companion.face`, `ws/companion.motion`) uses **warm caching + bounded rotation** (cleanly resetting on turn count, elapsed time/idle TTL, or token cap) built atop `261127-feat-ws-ephemeral-lightweight-harness-pool` (`EphemeralPool` `BoundedRotation`), paired with **thin recent-messenger-summary sharing** from Tier 2 so reactions stay contextually aligned without sharing one heavyweight session.
  - **Rejected:** Cold stateless 1-shot spawns per tick, or collapsing Tier 1 reactions and Tier 2 messenger coordination into a single long-lived session.
- **Backlog posture & cross-linking — settled `(1736)=a`:**
  - **Chosen:** Keep this ticket in `todo/` for now (do **not** promote to `ready/`), enriched with the settled decisions and cross-linked to `261127-feat-ws-ephemeral-lightweight-harness-pool` (`blocked-by: ...#phase1`, plus `related:`) so follow-up design refinement can resume smoothly later.

## Constraints

- **Performance & Windows AV (`ai-docs/manuals/performance.md`):** Live reaction ticks must run through `EphemeralPool` (`BoundedRotation` from `261127-feat-ws-ephemeral-lightweight-harness-pool`) and must not create per-tick `wsmeta`/ring/transcript files on disk (zero file touches on the hot reaction path).
- **Security boundary:** `ws/daemon.curl` and `ws/daemon.dom|screenshot|ui` must remain restricted to authorized coordinator/companion surfaces (bearer token + same-host/loopback trust boundary; never exposed to untrusted remote/LAN viewers or arbitrary worker subagents), and high-impact mutations must enforce the 1-click in-messenger confirmation gate (`(1728)=a`).
- **Compatibility (`ai-docs/manuals/release-compatibility.md`):** Preserve `dashboard.companion_*` keys and existing `shell.js` `companion-focus` handshake semantics (`T0` transparent).
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):** Any shipped tool descriptions (`ws/companion.*`, `ws/daemon.*`) or coordinator prompt guidance must remain downstream-generic without devenv-only citations.

## Phases

### Phase 1: Naming Cleanup & Companion Action Surface (`ws/companion.{speak,face,motion}`)

Per `(1727)=a` and `(1730)=a`: remove stale `pet` / `desktop pet` phrasing from `ai-docs/manuals/companion-pet-rigging.md`, legacy comments (`companion_legacy.go`), and epic/ticket references while preserving `dashboard.companion_*` keys (`T0`). Expose structured companion expression actions (`ws/companion.speak`, `ws/companion.face`, `ws/companion.motion`) backed by `PublishCompanionCommand` on `/rail/events`, driven by the Tier 1 lightweight rotating reaction agent atop `261127-feat-ws-ephemeral-lightweight-harness-pool` (`EphemeralPool` `BoundedRotation`, turn/time/token reset caps, and thin recent-messenger-summary injection).

### Phase 2: Low-Level Daemon & UI Control Bridge (`ws/daemon.curl`, `ws/daemon.dom|screenshot|ui`)

Per `(1728)=a` and `(1729)=a`: implement the 2-layer hybrid bridge — bearer-authenticated internal daemon API access (`ws/daemon.curl`) plus the extended `assets/shell.js` SSE-to-POST reverse bridge (`ws/daemon.dom`, `ws/daemon.screenshot`, `ws/daemon.ui`) working identically across browsers and Tauri WebView2 without debug ports, with strict coordinator capability scoping and the 1-click in-messenger confirmation gate for high-impact mutations (global config writes, bulk agent model switches, bulk interrupts).

### Phase 3: Companion Messenger Drawer & Coordinator Wiring

Implement the touch/click Companion Messenger UI drawer (KakaoTalk-style chat thread) backed by the Tier 2 coordinator agent, connected to both the Tier 1 live reaction agent (via thin recent-messenger-summary sharing) and the Phase 2 daemon/UI control bridge so users can ask the companion to inspect the current view, guide/open settings, or confirm and broadcast directives across active agents.
