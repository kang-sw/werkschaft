---
title: "#r-convlive Extra Motion 통합 엔진 · 좌하단 설정(레이아웃 제거) · 호버 모션 끄기"
parent: 261126-epic-ws-dashboard-conversation-ux-restyle
blocked-by: 261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens
related:
  261114-feat-ws-live-reasoning-summary: shares the #r-convlive live host and thinking/reasoning state slot
sage-review-design: pending
sage-review-completeness: pending
---

# Unified `#r-convlive` Extra Motion Engine, Bottom-Left Settings Cleanup, & Inline Hover Mute

## Background

During active lead turns, `#r-convlive` currently displays plain static activity text with no visual distinction between tool categories (`exec`, `tickets`/`whiteboard`, `agent`/`mailbox`, `git`/`worktree`, general file tools) or `thinking` state, and no tactile visual link connecting a finished tool call to the turn's growing token counter. Meanwhile, the bottom-left Settings popover still carries an obsolete top `레이아웃` (`자동` / `나란히` / `탭`) section even though dynamic layout is now the default.

As Axis B of `261126-epic-ws-dashboard-conversation-ux-restyle`, this ticket introduces a single, extensible `#r-convlive` **Extra Motion** 2D Canvas engine and motion registry (matching the owner-approved v10 choreography), wires instant inline hover disable (`모션 끄기`) bidirectionally synced with a new `Extra Motion` option (`ws.dashboard.extraMotion`, default ON) in the bottom-left Settings popover, and removes the obsolete `레이아웃` section from that popover.

Approved visual reference: `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html` (v10, confirmed at `(28)=ok`).

## Decisions

1. **Single `#r-convlive` Extra Motion 2D Canvas host & data-driven motion registry — `(13)`–`(15)`, `(18)`–`(20)`, `(23)=b`, `(26)`, `(28)=ok`**
   - One capsule-free 2D Canvas overlay spans `#r-convlive` and the active turn summary header, driven by a single extensible **Motion Registry** so every tool category and live state (`thinking`, `tool`, and future `permwait`/`compacting` slots) shares one host and zero duplicate renderers exist.
   - **Tool-category colors & 15fps stepped sway signatures** (calm dark-theme accents matching `dashboard-style.md` and v10 mockup, with light-theme legibility parity):
     1. `ws/exec` / shell (`#e5b567` warm amber): energetic micro-spark sway
     2. `ws/tickets` / `ws/whiteboard` (`#7aa2ff` cobalt blue): calligraphic ink sway
     3. `ws/agent` / `ws/mailbox` (`#b392f0` soft violet): twin-orbiting companion droplet sway
     4. `ws/git` / `ws/worktree` (`#5ccfe6` teal-cyan): dual-trail branching weave sway
     5. File / generic / unknown tools (`#8ea6c8` slate blue): calm classic painterly sway
   - **`생각 중` (thinking / reasoning) live state on the same host:** calm 15fps layered breathing ink-cloud bloom (`#96a8e8`) with drifting thought motes; integrates seamlessly with existing `#r-convlive` thinking/reasoning state (and `261114-feat-ws-live-reasoning-summary` when present).
   - **Elastic "Pop-In" transitions (`생각 중 → 툴` and consecutive `툴 → 툴`):** thinking cloud condenses inward (~140ms) then the incoming tool blob springs in (`scale 0 → ~1.27 → 0.94 → 1.0`) with an expanding color halo ring and micro-sparks.
   - **Modern 3-beat tension flight (`휘잉—피크스톱—슈욱!`) on OK:** C1-continuous velocity handoff from the 15fps sway into a 60fps flight (~0.72–0.98s) with **Launch Accelerate → Apex Peak-Stop / Hang (~100–140ms suspended dwell) → Razor-Sharp Snap-Dive** and a tapering calligraphic brushstroke ribbon tail + flung droplets, transitioning into emerald `#6cc39a`. Non-repeating randomized selection across **5 diverse directional archetypes** (High Loft Apex Dive, Under-Scoop Slingshot, Tight Diagonal S-Whip, Sleek Single Ribbon Bank, Short Punchy Snap-Dart) — no repetitive multi-loop spinning and no demo ghost-trail overlay in production.
   - **Frail, sputtering, jittery coral-red (`#f0797e`) flight on FAIL:** weak takeoff, engine-cough stalls, high-frequency hand-tremor jitter, thin struggling tail, and heavy leaky gravity-dripping droplets crawling across to the **same** top-right token target.
   - **Dynamic top-right `[data-conv-turn-tokens]` convergence & RPG floating `+m` damage-number pop:**
     - Measure the active turn summary row's right-side `[data-conv-turn-tokens]` (`+Nk tok`) anchor live via `getBoundingClientRect()` relative to the canvas container (falling back gracefully to the right edge of `[data-conv-turn-summary]` if absent), so target X shifts dynamically as tool count, duration, or `· 실패 N` widens the header.
     - On OK impact: emerald `#6cc39a` crisp `착!` absorption ring + subtle token highlight flash + elastic floating green `+m` token-delta number popping upward and fading out.
     - On FAIL impact: compact multi-lobe coral-red `#f0797e` `스플랫!` + subtle micro-shake + jittering floating coral-red `+m` popup as tool count and `[data-conv-turn-fails]` update.

2. **Extra Motion controls: bottom-left Settings (`localStorage`, default ON) + inline hover `모션 끄기` + `prefers-reduced-motion` — `(24)=a`, 2026-11-26 21:52 KST**
   - Persist `Extra Motion` in browser `localStorage` (`ws.dashboard.extraMotion`, default `"on"`, matching existing client-only preferences `ws.dashboard.input`, `ws.dashboard.stageFill`, `ws.dashboard.agpop.pin` — no `config_registry.go` or `dashboard-config-manifest.json` changes, pure **T0**).
   - Render `Extra Motion` (`✦ 켜기` / `○ 끄기`, localized via `wsi18n`) inside the bottom-left Settings popover matching its exact icon-badge + title + 1-line subtitle option-row style.
   - **Inline hover quick-disable (`모션 끄기`):** hovering the live `#r-convlive` spinner row reveals a subtle tonal `모션 끄기` micro-button right beside the spinner; clicking sets `ws.dashboard.extraMotion = "off"`, immediately cancels `requestAnimationFrame` (`0 rAF`), hides the canvas, and switches cleanly to the calm static semantic dot + label indicator — synced bidirectionally with the bottom-left Settings popover.
   - Automatically honor `window.matchMedia('(prefers-reduced-motion: reduce)')` and `document.visibilitychange` (pausing/canceling rAF immediately when hidden or reduced-motion is enabled).

3. **Remove obsolete top `레이아웃` (`자동` / `나란히` / `탭`) section from bottom-left Settings popover — owner explicit instruction (2026-11-26 22:18 KST)**
   - Dynamic layout is now the default; remove the `레이아웃` section and its manual `자동`/`나란히`/`탭` switcher UI from the bottom-left Settings popover (cleaning up dead `ws.dashboard.layout` UI controls while preserving dynamic responsive layout behavior and keeping existing stored `localStorage` keys harmlessly ignored so no errors occur on upgrade).

4. **Zero idle CPU / rAF budget & T0 compatibility — `performance.md`, `release-compatibility.md`**
   - Strictly `0 requestAnimationFrame` callbacks when idle (no active tool/thinking/flight/floating `+m`), when `Extra Motion` is OFF, when the tab is hidden, or when `prefers-reduced-motion: reduce` is active.
   - **Compatibility tier: T0 (transparent).** Pure frontend + optional additive `#r-convlive` `data-*` attributes (`data-tool`, `data-done`, `data-failed`, `data-phase`/`data-tokens-delta`, `omitempty` if emitted server-side); older clients/daemons ignore unknown attributes cleanly.

## Constraints

- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- **Blocked by Child 1:** Converges onto Child 1's `[data-conv-turn-tokens]` and `[data-conv-turn-fails]` hooks in `[data-conv-turn-summary]`.
- **Keep `dashboard-composer-delivery.js` untouched:** Follow existing `#r-convlive` observer/event architecture (`dashboard-conv*` / dedicated `dashboard-conv-motion.js` module + `pages.go` embed) without bloating existing near-limit JS files.
- **No `config_registry.go` / manifest edits:** `ws.dashboard.extraMotion` is a browser `localStorage` UI preference, consistent with `ws.dashboard.input` and `ws.dashboard.stageFill`.
- **Surgical i18n keys:** Append new strings (`Extra Motion`, `모션 끄기`, option subtitles) across all locale JSON tables in `agents-plugin-tool/internal/wsi18n/tables/` without re-sorting existing keys (and remove unused layout-switcher labels only if unreferenced elsewhere, otherwise leave harmlessly intact).

## Prior Art

- `#r-convlive` template & live snapshot: `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl` (`#r-convlive`), `agents-plugin-tool/internal/wslive/snapshot.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-status.js`.
- Bottom-left Settings popover & `localStorage` UI preferences: `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl` (settings popover), `agents-plugin-tool/internal/wsdashboard/assets/dashboard.js` (`ws.dashboard.layout`, `ws.dashboard.input`, `ws.dashboard.stageFill`).
- Turn summary target hooks (Child 1): `[data-conv-turn-summary]`, `[data-conv-turn-tokens]`, `[data-conv-turn-fails]`.

## Phases

### Phase 1: `#r-convlive` Extra Motion engine, hover quick-disable, bottom-left Settings `Extra Motion` & `레이아웃` removal

- **Bottom-left Settings popover update:**
  - Remove the obsolete top `레이아웃` (`자동`/`나란히`/`탭`) UI section from the bottom-left Settings popover while keeping dynamic responsive layout as the sole behavior (ignoring any stale `ws.dashboard.layout` value cleanly).
  - Add the `Extra Motion` (`✦ 켜기` default / `○ 끄기`) section in the popover's exact option-row style, backed by `localStorage` `ws.dashboard.extraMotion`.
- **Unified `#r-convlive` Extra Motion 2D Canvas engine & motion registry:**
  - Expose needed live tool/phase/outcome/token-delta attributes on `#r-convlive` (additive `data-*`, T0) and build a dedicated, clean JS module for the `#r-convlive` Extra Motion engine & registry matching `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html` (v10):
    - 5 tool categories (`exec`, `tickets`/`whiteboard`, `agent`/`mailbox`, `git`/`worktree`, file/generic) with distinct calm accents & 15fps sway signatures + `thinking` breathing ink-cloud bloom.
    - Elastic pop-in transitions (`thinking -> tool` condense-and-pop; consecutive `tool -> tool` spring pop-in).
    - Modern 3-beat `휘잉—피크스톱—슈욱` Hermite tension curve across 5 non-repeating directional flight archetypes (OK → emerald `#6cc39a`) and frail sputtering jittery coral-red (`#f0797e`) FAIL flight with leaky gravity drips.
    - Dynamic target tracking via `getBoundingClientRect()` of `[data-conv-turn-tokens]` (with fallback) and floating emerald/coral `+m` damage-number popups on arrival.
    - Inline hover `모션 끄기` micro-button right beside the live spinner, bidirectionally synced with the bottom-left Settings popover.
    - Strict `0 rAF` when idle, hidden, OFF, or `prefers-reduced-motion: reduce` (falling back cleanly to static semantic dot + label).
- **Surgical i18n keys:**
  - Append needed labels across all locale JSON tables in `agents-plugin-tool/internal/wsi18n/tables/` without re-sorting existing keys.

**Verification expectations:**
- Go tests (from `agents-plugin-tool/`): `go test -race ./internal/wsdashboard/... ./internal/wslive/... ./internal/wsi18n/...` and `GOOS=windows go vet ./...`, confirming bottom-left settings template renders `Extra Motion` without `레이아웃` switcher, `#r-convlive` additive attributes remain T0, and all locale tables pass.
- Node DOM / Canvas unit harness tests covering:
  - Tool-name → category classifier (`ws/exec.*`, `ws/tickets.*`, `ws/whiteboard.*`, `ws/agent.*`, `ws/mailbox.*`, `ws/git.*`, `ws/worktree.*`, file/unknown).
  - Motion state machine transitions (`idle` → `thinking` → `pop-in` → `sway` → `flight OK/FAIL` → `impact +m` → `idle`), verifying `rAF` cancels cleanly to `0` on idle, on hover `모션 끄기`, on Settings OFF, on `visibilitychange` hidden, and under `prefers-reduced-motion: reduce`.
  - Bidirectional sync between hover `모션 끄기` and bottom-left Settings `ws.dashboard.extraMotion` (default ON), and graceful handling of legacy `ws.dashboard.layout` in `localStorage`.
  - Dynamic target X tracking when `[data-conv-turn-tokens]` shifts horizontally (e.g., when `[data-conv-turn-fails]` appears).
- Headless Chromium (Playwright) visual verification in dark and light themes comparing the bottom-left Settings popover (no `레이아웃`, `Extra Motion` option rows), hover `모션 끄기` reveal, `#r-convlive` thinking/tool/flight/`+m` frames, and static fallback side-by-side against `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`.
