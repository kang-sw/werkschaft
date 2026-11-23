# HUD Spinner Phase Continuity and Color Transition

## Goal
Eliminate the visual phase/position jump on `.hud-node` / `.hud-ring` during `thinking` <-> `tool` transitions and add a smooth `0.5s` color transition for the track and spinning arc while keeping steady-state rotation 100% compositor-only.

## Plan
1. **Unify spinning animation & phase stamp:**
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.css`: Unify `thinking` and `tool` `.hud-ring` mask/containment and `::before` conic-gradient/animation onto a single `@keyframes hud-spin` (`1s linear infinite`, shared `from 145deg` sweep).
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-animation.js`: Set `ANIMATION_PERIOD_MS.thinking` and `ANIMATION_PERIOD_MS.tool` to `1000`.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-hud.js`: Map both `thinking` and `tool` to shared `"hud-spin"` `stateKey` in `animationPhaseDelay(...)` so `thinking` <-> `tool` never invalidates `wsAnimationStamp` or rewrites `animationDelay`, while entering/leaving spinning states from `holding`/`permission`/`idle`/`failed` still restamps or clears.
2. **Smooth `0.5s` color transitions (compositor-safe):**
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard.css`: Tokenize `--d-hud-ring-tool-tip` for light and dark themes.
   - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.css`: Register `@property --d-hud-ring-arc` and `@property --d-hud-ring-tip` (`syntax: "<color>"`, `inherits: true`, `initial-value: transparent`), replace `background: linear-gradient(...)` with `background-color`, transition `background-color 0.5s ease, --d-hud-ring-arc 0.5s ease, --d-hud-ring-tip 0.5s ease` on `.hud-ring`, keep `mask`/`contain: paint` strictly on spinning states, and add `.hud-ring, .hud-ring::before` to `prefers-reduced-motion: reduce` `transition: none !important`.
3. **Update & add regression tests:**
   - Update pinned selectors/periods/keyframes and add phase-continuity + color-transition assertions in `spinner-phase.test.cjs`, `frontend-idle.test.js`, `agents-island.test.cjs`, and `hud.browser.cjs`.
   - Verify locally with `node --test` on the Node test files and remotely on `w5090` with `generate-package-assets.py` + `go test ./internal/wsdashboard/...`.
