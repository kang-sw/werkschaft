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
- **Data-Driven `ws-companion/v1` Doll-Maker Canvas Compositor (`companion-pet-rigging.md`):**
  - Port the validated v3.1 Canvas compositor into modular asset fragments (`assets/dashboard-companion-compositor.js`, `assets/dashboard-companion-stage.js`, `assets/dashboard-companion.css`, registered in `pages.go` and `asset_source_test.go`, keeping module size clean and bounded):
    - `512 × 512` logical offscreen-cached compositor rendered at `devicePixelRatio` crispness down to `72 px`, implementing `ws-companion/v1` smart defaults (inferred layer mode, `z: 20`, `fallback: "stand"`, `headScale: 1.0`).
    - Canonical `head-egg` (`z30`) + open `expressions` dictionary (`z40`: entries with `decal` render Track 2 full-face anime decals with `0.0px` silhouette shift; parametric entries drive Track 1 11-layer live sclera-clipped eyes/lids/brows/mouth/optional `overlay`).
    - Open `poses` (`layers[]`) & `gestures` (`pose`, `replaces[]` slot replacement, additive `layers[]`) dictionaries with `fallback` chains and **generic part/joint anchored 2D tracks around `pivot`** (inline or resolved via reusable `motions` refs like `"breathe"` / `{ use, phase }`):
      - `oscillate` (`rotDeg`, `tx`, `ty`, `sx`, `sy`, `hz`, `phase`, `ease` — subsuming arm waves, breathing, and out-of-phase perched leg sway; accepts legacy shorthand aliases).
      - `keyframes` (`[{ t, rotDeg?, tx?, ty?, sx?, sy? }]`, `loop?`) and `frames` (`fps` + frame list).
    - Open `props` & `fx` registries backed by engine-shared defaults (`drone`, `mail`, `grab`, `prop-typing-desk`, comic badges/particles/floats) plus reusable prop behaviors (`attach`, `orbit`, `arc-to-landmark`, `bonk-from-edge`, `burst`) and whole-body stage physics (`stay`, `hop-bounce`/`double-hop`, `pendulum-drag`, `squash-pop`).
    - Full graceful degradation when any optional pose, gesture, expression, hair layer, or prop is omitted by a minimal 7-line BYO pack.
- **Lightweight Iframe-to-Shell Active-View Relay:**
  - Because the shell rail and active session iframe maintain separate WebSocket streams (`shell.js`, `dashboard-stream.js`), add a tiny, throttled same-origin `postMessage` bridge (`ws:companion-context`) from the active session iframe to the outer shell carrying only:
    - `sessionId`, `routeKind` (`session | tracker | settings | home`), `activity` (`idle | thinking | tool | typing_user | error | celebrate`), `openAskCount`, `runningSubagents`, `recentEvent` (`subagent.spawn | subagent.done | mail.recv | session.celebrate | session.error` with dedupe token), and `landmarks` (`composer-top`, `whiteboard-ask`, `hud-roster` rectangles translated by the shell using the active iframe's `getBoundingClientRect()`).
  - Keep the relay strictly read-only and one-way for state observation; non-framable fallback pages simply run the companion in ambient `rail-home` idle mode.
- **Declarative 0-Token Scenario Runner (Fast Path):**
  - Implement a generic priority/cooldown scenario matcher driven by the relayed event stream + local pointer/idle triggers (`user.poke`, `user.drag`, `idle.long`), seeded with the **12 built-in starter scenarios** (`idle_roam` through `mailbox_bonk`) as a declarative data table merged with pack-provided `pack.json` `scenarios` (supporting single-beat shorthand, multi-beat `steps[]`, safe declarative `when` guards `streak`/`windowMs`/`idleMs`/`hourRange`/`routeKind`/`pose`, and `disabled: true`).
  - Pair with the always-on passive gaze priority stack.
- **Compatibility Tier Verdict — T0 (transparent):** Pure frontend shell/iframe addition gated off by default; introduces no wire protocol break, MCP tool change, or persisted store schema change.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/companion-pet-rigging.md` — follow the v3.1 3-layer extensible schema (open asset maps, generic `oscillate`/`keyframes`/`frames` tracks, stage physics, prop trajectories, and declarative scenario registry).
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
  2. Implement the data-driven v3.1 Canvas compositor loading `pack.json` and WebP layers via `/api/companion/packs/{id}/...` with offscreen tint caching, open `poses`/`gestures`/`expressions`/`props`/`fx` maps + `fallback` resolution, `static` BYO fallback, `semantic-split`, `semantic-split-frames`/`static-frames`, generic anchored `oscillate`/`keyframes` tracks (plus shorthand aliases), stage physics (`hop-bounce`/`pendulum-drag`/`squash-pop`), and prop trajectories (`attach`/`orbit`/`arc-to-landmark`/`bonk-from-edge`/`burst`).
  3. Mount the app-scoped overlay on the persistent outer shell (`dashboard.tmpl` / `shell.js`), compute the dynamic left-rail-bottom home anchor above `.rws-unreg` / `.rfoot`, support drag-and-drop `drag_dangle`, click-poke, compact quick-menu (preset switch, pack reload, return home, hide/mute), and live toggle via settings broadcast.
  4. Implement the throttled same-origin `postMessage` active-view relay from session iframes (activity state, subagent/mail/ask events, iframe-to-shell translated landmark rects) feeding the declarative 0-token scenario runner (built-in 12 starter scenarios merged with pack-level `scenarios` overrides/additions).
  5. Verify with headless Playwright tests against a running local dashboard test server: `off` mode zero-canvas/zero-request check, `visual` mode rail-bottom anchoring, session-switch persistence without reload flicker, drag-and-drop `drag_dangle`, BYO 1-layer `static` pack rendering, custom pack-defined pose + scenario override execution, continuous knee `oscillate` motion, and `0` console/page errors on dark and light themes.
- **Deferred scope:** Optional LLM sidecar narrative director, `ws/emotion`, and `full`-mode lead persona prose injection (`261122-feat-ws-companion-director`), plus final official ship art pack replacement (`261122-chore-ws-companion-official-art-pack`).
- **Verification boundary:**
  - `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/wsdashboard -run 'AssetSource|Companion|Page'`
  - Headless Playwright verification script exercising dark/light themes, session switches, rail collapse/resize, drag-and-drop, and `off`/`visual` live toggle with `0` console errors.
