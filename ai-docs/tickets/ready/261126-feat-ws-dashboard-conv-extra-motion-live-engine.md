---
title: "#r-convlive Extra Motion 통합 엔진 · 좌하단 설정(레이아웃 제거) · 호버 모션 끄기"
parent: 261126-epic-ws-dashboard-conversation-ux-restyle
blocked-by: 261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens
related:
  261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens: prerequisite (lands [data-conv-turn-tokens] and [data-conv-turn-fails] hooks)
  261114-feat-ws-live-reasoning-summary: shares the #r-convlive live host and thinking/reasoning state slot
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: ced3c5ef86d4e00e
sage-review-completeness-reviewed: ced3c5ef86d4e00e
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
     - **`+m` source (technical, auto-confirmed):** `m` is the increase of the active turn summary's own running token value (Child 1's running `AddedTokens` snapshot field, the shared contract defined in Child 1 Decision 5) observed at the impact, in the same unit as `[data-conv-turn-tokens]`, so the popups add up to the header the flights converge on. When the value did not advance (no usage reported yet, or only the render-time `~` estimate exists), the impact plays without a number. The mockup's per-call `+340` row meta is illustrative only (no per-call token field exists; Child 1 does not render it). **Rejected:** deriving `m` from per-batch `ToolBatch.Usage` — different unit (per-request tokens), would not sum to the header.
     - **Canvas host (technical, auto-confirmed):** `#r-convlive` and `#r-entries` are `display: contents` regions replaced by SSE swaps and have no box, so the single canvas overlay mounts on a real, swap-stable ancestor (`.convo` or its wrapper), positions targets relative to it, and re-measures `[data-conv-turn-tokens]` after swaps and on scroll/resize while any motion is active. **Rejected:** mounting the canvas inside `#r-convlive` (no box; destroyed on swap).

2. **Extra Motion controls: bottom-left Settings (`localStorage`, default ON) + inline hover `모션 끄기` + `prefers-reduced-motion` — `(24)=a`, 2026-11-26 21:52 KST**
   - Persist `Extra Motion` in browser `localStorage` (`ws.dashboard.extraMotion`, default `"on"`, matching existing client-only preferences `ws.dashboard.input`, `ws.dashboard.stageFill`, `ws.dashboard.agpop.pin` — no `config_registry.go` or `dashboard-config-manifest.json` changes, pure **T0**).
   - Render `Extra Motion` (`✦ 켜기` / `○ 끄기`, localized via `wsi18n`) inside the bottom-left Settings popover matching its exact icon-badge + title + 1-line subtitle option-row style.
   - **Inline hover quick-disable (`모션 끄기`):** hovering the live `#r-convlive` spinner row reveals a subtle tonal `모션 끄기` micro-button right beside the spinner; clicking sets `ws.dashboard.extraMotion = "off"`, immediately cancels `requestAnimationFrame` (`0 rAF`), hides the canvas, and switches cleanly to the calm static semantic dot + label indicator — synced bidirectionally with the bottom-left Settings popover.
   - Automatically honor `window.matchMedia('(prefers-reduced-motion: reduce)')` and `document.visibilitychange` (pausing/canceling rAF immediately when hidden or reduced-motion is enabled).

3. **Remove obsolete top `레이아웃` (`자동` / `나란히` / `탭`) section from bottom-left Settings popover — owner explicit instruction (2026-11-26 22:18 KST)**
   - Dynamic layout is now the default; remove the `레이아웃` section and its manual `자동`/`나란히`/`탭` switcher UI from the bottom-left Settings popover (cleaning up dead `ws.dashboard.layout` UI controls while preserving dynamic responsive layout behavior and keeping existing stored `localStorage` keys harmlessly ignored so no errors occur on upgrade).
   - **Removal scope (technical, auto-confirmed):** the switcher handler (`dashboard-actions.js` `.gmenu` `data-layout`), the `LAYOUT_KEY` load/save and cross-tab storage listener (`dashboard.js`, `dashboard-lifecycle.js`, `dashboard-board.js`), and the `tabs`-mode consumers (`dashboard-audit.js`, `overview.js` `data-layout="tabs"`) are located by search for `LAYOUT_KEY` / `data-layout` and reconciled. A stored `ws.dashboard.layout` of `tabs` or `split` must be forced to dynamic (`auto`) at load, otherwise upgraded users keep a layout they can no longer change; the stale key is never written again. Tests that set `ws.dashboard.layout` or assert `data-layout` (about 20 files) are updated to the dynamic default.

4. **Zero idle CPU / rAF budget & T0 compatibility — `performance.md`, `release-compatibility.md`**
   - Strictly `0 requestAnimationFrame` callbacks when idle (no active tool/thinking/flight/floating `+m`), when `Extra Motion` is OFF, when the tab is hidden, or when `prefers-reduced-motion: reduce` is active.
   - **Stale-state guard (technical, auto-confirmed):** the engine animates only while the live snapshot says the lead is busy; a busy/thinking state with no live event for a bounded interval (planning picks it from the snapshot's own staleness handling, see `261123-bug-ws-dashboard-webview-idle-cpu`) drops to the static dot, so a stale state cannot hold a 15fps loop. Data source: the client derives tool/outcome/token attributes from the existing live snapshot and DOM; additive Go-emitted `data-*` is only added if a needed signal is absent from them.
   - Light-theme tints for the five category accents, thinking cloud and OK/FAIL colors are chosen in planning for legibility and confirmed by the dark/light captures.
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

## Prior Decisions

- 261126-epic-ws-dashboard-conversation-ux-restyle (2026-11-26, Cross-Child Decisions): "Single `#r-convlive` live-motion host & extensible registry ((22), (23)=b, (24)=a, (28)=ok): all live states share one canvas overlay and motion registry, with Extra Motion default ON" — bearing: supports
- 261126-epic-ws-dashboard-conversation-ux-restyle (2026-11-26, Cross-Child Decisions): "Remove the obsolete top 레이아웃 (자동 / 나란히 / 탭) section from the bottom-left Settings popover and place Extra Motion in the same option-row style" — bearing: supports
- 261126-epic-ws-dashboard-conversation-ux-restyle (2026-11-26, Cross-Child Decisions): "Shared turn-summary DOM contract owned by Child 1 and consumed by Children 2 & 3: stable root [data-conv-turn-summary]" — bearing: constrains
- 1fe76263 (2026-11-26, commit): "Global persistence under localStorage key ws.dashboard.stageFill reflected on :root early in dashboard.js" — bearing: supports
- 261126-feat-ws-chat-stream-interactive-question-cards (2026-11-26, Decisions): "Surgical i18n reuse across all locale tables: add minimal surgical keys across all locale JSON tables in wsi18n/tables/, preserving existing key order" — bearing: supports
- 0dbed3cd (2026-11-26, commit): "across all 11 locales with surgical +7-line insertions per table" — bearing: supports
- 14d5373d (2026-11-26, commit): "Compatibility tier: T0 transparent (frontend-only, browser localStorage only)" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | assets/dashboard.tmpl, assets/dashboard.js, assets/dashboard-actions.js, new dashboard-conv-motion.js, pages.go embed, wsi18n/tables/*.json (11 locales), optional wslive/snapshot.go |
| scope.surface | cross-module | frontend assets plus wsi18n tables plus additive data-* attributes on #r-convlive (wsdashboard, wsi18n, optionally wslive) |
| scope.new_public_symbol | no | none; new browser-side module, no exported Go symbol named |
| scope.new_type_contract | no | additive data-tool, data-done, data-failed, data-tokens-delta attributes only; no Go type or signature named |
| scope.test_surface | new-files | new Node canvas/DOM harness for classifier and rAF state machine; existing tests under wsdashboard/testdata/*.browser.cjs reference ws.dashboard.layout and need review |
| complexity.reuse_points | unconfirmed | #r-convlive .clive hooks in dashboard-composer-status.js and the data-conv-turn-tokens hooks of the blocker, not yet landed (pending 261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens) |
| complexity.side_effect_risk | moderate | removing the layout switcher touches dashboard.js LAYOUT_KEY, dashboard-actions.js .gmenu data-layout handler, and tests that set ws.dashboard.layout; rAF loop affects idle CPU |
| risk.correctness | moderate | 60fps canvas state machine with rAF cancellation on hidden, reduced-motion, hover-off, and a target that moves with the header width |
| risk.fit | moderate | data-conv-turn-* hooks do not exist yet (grep found none), so integration depends on the blocker landing |
| risk.test | high | canvas animation and rAF-zero budget are hard to assert; visual verification needed in two themes |
| risk.security_or_contract | low | browser localStorage preference and additive attributes only, grounded in T0 decision; no auth or wire contract change |

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
  - Bidirectional sync between hover `모션 끄기` and bottom-left Settings `ws.dashboard.extraMotion` (default ON), and legacy `ws.dashboard.layout` of `tabs`/`split` in `localStorage` loading as the dynamic default; a stale busy/thinking state dropping the rAF loop to `0`; `+m` equal to the header's token increase (and no number when it did not advance).
  - Dynamic target X tracking when `[data-conv-turn-tokens]` shifts horizontally (e.g., when `[data-conv-turn-fails]` appears).
- Headless Chromium (Playwright) visual verification in dark and light themes comparing the bottom-left Settings popover (no `레이아웃`, `Extra Motion` option rows), hover `모션 끄기` reveal, `#r-convlive` thinking/tool/flight/`+m` frames, and static fallback side-by-side against `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`.
