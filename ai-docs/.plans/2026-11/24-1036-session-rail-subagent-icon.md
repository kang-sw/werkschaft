# Plan: Session Rail Subagent Robot Icon Overlay (T0)

## Goal
Replace the session rail's bottom-right numeric subagent count badge (`.sbn`) with a blinking `#i-robot` icon overlaid at the top-right corner of the agent badge (`.hiw` / `.hb`) without expanding horizontal layout space.

## Changes
1. **Template (`agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl`)**:
   - In `{{define "rrow"}}`, replace `.sbn` inner text `{{.}}` with `{{icon "robot"}}` and add `role="img"` and `aria-label="{{t "srow.agents" .}}"` alongside `title="{{t "srow.agents" .}}"`.
   - Keep `.sbn` class name so existing animation-pause and reduced-motion rules and `css_collision_test.go` remain intact.

2. **CSS (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-rail.css`)**:
   - Convert `.rail .sbn` from in-flow flex item to `position: absolute; z-index: 3; top: -4px; right: -4px; width: 10px; height: 10px;` so it overlays the top-right corner of `.hb` without widening `.hiw`.
   - Use `background: var(--rs-ground); border-radius: 3px; box-shadow: 0 0 0 1px var(--rs-ground);` for the knockout halo because `#i-robot` in `sprite.svg` is stroke-based (setting `stroke: var(--rs-ground)` on the SVG would erase the robot outline).
   - Add `.rail .sbn .i` sizing (`width: 100%; height: 100%; stroke-width: 1.9;`) and `.rail .hb.sm ~ .sbn` (`top: -3.5px; right: -3.5px; width: 9px; height: 9px;`) for nested child rows.
   - Update the stale comment above `.rail .hb` describing `.sbn` placement.

3. **Tests (`rail_kid_busy_test.go`, `testdata/rail-row-meta.browser.cjs`)**:
   - Update `TestRailMarkupAvatarBadges` for the `#i-robot` `.sbn` markup and add a CSS contract check for `.rail .sbn` absolute top-right positioning.
   - Update `rail-row-meta.browser.cjs` to verify `.sbn` tooltip/icon and that `.sbn` no longer pushes `.rid` right (`gap === 6`).
