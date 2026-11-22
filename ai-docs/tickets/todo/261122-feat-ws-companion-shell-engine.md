---
title: "앱 스코프 셸 오버레이, 좌측 레일 하단 기본 거처, v3.1 캔버스 컴포지터 및 활성 화면 컨텍스트 릴레이"
parent: 261122-epic-werkschaft-lead-companion
blocked-by:
  - 261122-feat-ws-companion-config-settings
  - 261122-feat-ws-companion-pack-store
related:
  261110-epic-ws-dashboard-agent-host: persistent outer shell and session rail DOM
  261121-research-werkschaft-lead-companion-rig: validated v3.1 Doll-Maker live prototype
---

# App-Scoped Shell Overlay, Left-Rail-Bottom Home Anchor, v3.1 Canvas Compositor & Active-View Relay

## Background

Werkschaft's dashboard already runs as a persistent same-origin iframe shell (`assets/dashboard.tmpl`, `assets/shell.js`, `assets/dashboard-lifecycle.js`) where normal session switches swap the inner iframe without reloading the outer shell. Mounting the companion once at the outer-shell level makes it genuinely **app-scoped** — smoothly accompanying the user across sessions while subscribing to a lightweight slice of whatever view/session is currently open.

Because main session panes are information-dense, the companion's default home anchor sits cleanly at the **bottom of the left session rail** (resting on the divider bar above `.rws-unreg` / `.rfoot`), scaling neatly to rail width while remaining freely draggable onto viewport landmarks (`composer-top`, `whiteboard-ask`, `hud-roster`).

## Decisions

- **App-scoped persistent shell mount (zero overhead when `off`):**
  - Mount the stage container (`position: fixed; inset: 0; pointer-events: none;`, with only the character hitbox and quick popover `pointer-events: auto`) once in the persistent outer shell (`dashboard.tmpl` / `shell.js`), never duplicated per session iframe.
  - When `dashboard.companion_mode === "off"`, keep the controller inert: zero pack fetches, zero `<canvas>` allocation, zero `requestAnimationFrame` loop, and zero iframe relay overhead. Toggling `off <-> visual/full` mounts/tears down cleanly via the live settings broadcast from `261122-feat-ws-companion-config-settings`.
  - Pause the `rAF` loop automatically when `document.hidden` is true or the companion is minimized/hidden.
- **Default home anchor = left session rail bottom:**
  - Compute the `rail-home` anchor rect dynamically from the outer shell's left session rail footer divider (the top border of `.rws-unreg` when visible, falling back to `.rfoot` / `.r-config-global` in `dashboard.tmpl`), responsive to rail collapse/resize (`ResizeObserver`).
  - Scale default display size automatically to fit rail width (`~96–136 px`), keeping zero overlap with session rows or footer links.
  - Support pointer drag-and-drop (`dangle` pose + `prop-grab` behind head + drag-velocity pendulum swing) onto any reported landmark or viewport edge, with double-click or compact context menu returning home to `rail-home`.
- **v3.1 Doll-Maker Canvas Compositor (`companion-pet-rigging.md`):**
  - Port the validated v3.1 Canvas compositor into a dedicated modular asset fragment (`assets/dashboard-companion-engine.js`, registered in `pages.go` and `asset_source_test.go`, keeping module size clean and bounded):
    - `512 × 512` logical offscreen-cached compositor rendered at `devicePixelRatio` crispness down to `72 px`.
    - Canonical `head-egg` (`z30`) + 2-track hybrid expressions (`z40`: Track 1 11-layer live sclera-clipped cursor/drone iris tracking + smooth blinking/wink/half-lids/`idle_doze`; Track 2 full-face anime decals with `0.0px` head silhouette shift).
    - `bodyLayers[]` supporting `static` (1-layer BYO packs & stationary untinted props like `z22` `prop-typing-desk`), `semantic-split` (cached offscreen multiply masks for `jacketFill`, `accentFill`, head-to-toe synced `skinFill`, fixed `#1e2433` `line`, untinted `detail`), `semantic-split-frames` (rapid micro-loops like `z24` `typing-hands`), and continuous `pivot` + `motion`: `wave-pivot`, `bob-pivot`, and **`sway-pivot`** (smooth out-of-phase sinusoidal sway `ampDeg`, `freqHz`, `phase` for `perched`/`dangle` lower legs).
    - Full graceful degradation when an optional pose, gesture, hair layer, or prop is omitted by a minimal BYO pack.
- **Lightweight Iframe-to-Shell Active-View Relay:**
  - Because the shell rail and active session iframe maintain separate WebSocket streams (`shell.js`, `dashboard-stream.js`), add a tiny, throttled same-origin `postMessage` bridge (`ws:companion-context`) from the active session iframe to the outer shell carrying only:
    - `sessionId`, `routeKind` (`session | tracker | settings | home`), `activity` (`idle | thinking | tool | typing_user | error | celebrate`), `openAskCount`, `runningSubagents`, `recentEvent` (`subagent_spawn | subagent_done | mail_bonk | commit_done | turn_error` with dedupe token), and `landmarks` (`composer-top`, `whiteboard-ask`, `hud-roster` rectangles translated by the shell using the active iframe's `getBoundingClientRect()`).
  - Keep the relay strictly read-only and one-way for state observation; non-framable fallback pages simply run the companion in ambient `rail-home` idle mode.
- **0-Token Local Behavior State Machine (Fast Path):**
  - Map relayed events + local pointer/idle timers into the 12 canonical scenarios (`idle_roam`, `perched_watch`, `working_tatata`, `thinking_ponder`, `attention_point`, `startled_poke`, `drag_dangle`, `gloom_fail`, `celebrate_done`, `subagent_dispatch` with `prop-drone`, `subagent_return`, `mailbox_bonk` with `prop-mail` burst coalescing) and always-on passive gaze priority stack.
- **Compatibility Tier Verdict — T0 (transparent):** Pure frontend shell/iframe addition gated off by default; introduces no wire protocol break, MCP tool change, or persisted store schema change.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/companion-pet-rigging.md` — follow v3.1 `bodyLayers[]` Z-stack, `sway-pivot`, 2-track expressions, and 12-scenario mapping.
- `ai-docs/manuals/performance.md` — zero disk I/O from frontend or daemon on interactive frames; zero `rAF`/memory overhead when `off` or tab hidden; throttle iframe `postMessage` landmark updates (`<= 4 Hz` or on Resize/mutation only).
- `ai-docs/manuals/shipped-surface-boundary.md` — all UI tooltips/labels localized via `wsi18n` and downstream-neutral.

## Prior Art

- Persistent shell & iframe lifecycle: search `shell.js`, `dashboard.tmpl` (`.rfoot`, `.rws-unreg`, `.r-config-global`), and `dashboard-lifecycle.js` (`shStart`, `shOpen`, `shRid`).
- Stream & region observation: search `dashboard-stream.js`.
- Asset fragment registration & size guards: search `pages.go` and `asset_source_test.go` in `agents-plugin-tool/internal/wsdashboard/`.
- Verified v3.1 compositor reference implementation: local prototype `style-d1-dollmaker-live.html` (gitignored mockup used during research; port cleanly into modular dashboard JS without embedding giant base64 strings — load via `/api/companion/packs/...`).

## Phases

### Phase 1: Shell Overlay, Rail-Bottom Anchor, v3.1 Compositor & Iframe Context Relay

- **Goals:**
  1. Add modular companion runtime JS/CSS fragments (`dashboard-companion-compositor.js`, `dashboard-companion-stage.js`, `dashboard-companion.css`) registered in `pages.go` and `asset_source_test.go`, keeping each file cohesive and well under size advisories.
  2. Implement the v3.1 Canvas compositor loading `pack.json` and WebP layers via `/api/companion/packs/{id}/...` with offscreen tint caching, `static` BYO fallback, `semantic-split`, `semantic-split-frames`, `wave-pivot`/`bob-pivot`/`sway-pivot`, 2-track hybrid expressions, and engine-shared props (`prop-drone`, `prop-mail`, `prop-grab`, `prop-typing-desk`).
  3. Mount the app-scoped overlay on the persistent outer shell (`dashboard.tmpl` / `shell.js`), compute the dynamic left-rail-bottom home anchor above `.rws-unreg` / `.rfoot`, support drag-and-drop `drag_dangle`, click-poke, compact quick-menu (preset switch, pack reload, return home, hide/mute), and live toggle via settings broadcast.
  4. Implement the throttled same-origin `postMessage` active-view relay from session iframes (activity state, subagent/mail/ask events, iframe-to-shell translated landmark rects) driving the 0-token local behavior engine across all 12 canonical scenarios.
  5. Verify with headless Playwright tests against a running local dashboard test server: `off` mode zero-canvas/zero-request check, `visual` mode rail-bottom anchoring, session-switch persistence without reload flicker, drag-and-drop `drag_dangle`, BYO 1-layer `static` pack rendering, `sway-pivot` motion, and `0` console/page errors on dark and light themes.
- **Deferred scope:** Optional LLM sidecar narrative director, `ws/emotion`, and `full`-mode lead persona prose injection (`261122-feat-ws-companion-director`), plus final official ship art pack replacement (`261122-chore-ws-companion-official-art-pack`).
- **Verification boundary:**
  - `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'AssetSource|Companion|Page'`
  - Headless Playwright verification script exercising dark/light themes, session switches, rail collapse/resize, drag-and-drop, and `off`/`visual` live toggle with `0` console errors.
