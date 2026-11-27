# Plan: Left-Rail Bottom-Left Dedicated Companion Handover (`(148)=a`)

## Goal
Eliminate vertical jitter ("들썩거림") on `#rail-companion-slot` when `Unregistered workspaces` (`details.rws-unreg`) or `.unreg-notif` expands/collapses underneath it, by shortening the upper session rail block (`#rlist` + `details.rws-unreg` + `.unreg-notif` + `.admission-feedback` + `.rfoot`) and handing the **true bottom-left corner** of the `>=1600px` left column over to `#rail-companion-slot` (`flex: none`, flush to the bottom edge of the viewport).

## Authoring & Style References
Read before editing:
- `ai-docs/manuals/dashboard-style.md` (borderless Material tonal surface system, dark + light themes, zero harsh `1px` borders)
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl`
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-rail.css`
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion.css`
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-stage.js`
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm.js`
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-vrm-colead.js`

## Architectural Contract
1. **Left Column Vertical Stack (`>=1600px` 3-column mode, Companion enabled):**
   - **Upper Session Rail Block (`flex: 1 1 auto; min-height: 0; display: flex; flex-direction: column`):**
     - Session rail header
     - `#rlist` (`flex: 1 1 auto; min-height: 0; overflow-y: auto`)
     - `details.rws-unreg` (`Unregistered workspaces`, bounded `max-height` with internal scroll when `[open]` so it never crushes `#rlist` or overflows the column) + `.unreg-notif` + `.admission-feedback`
     - `.rfoot` (`Workflow settings` / `Home` footer pinned cleanly to the bottom of the upper session rail block)
   - **Dedicated Bottom-Left Companion Space (`#rail-companion-slot`, `flex: none`):**
     - Placed as the **final bottom-left element** of the left column, flush to the bottom edge of the viewport (`0px` bottom gap so the Co-Lead laptop lid sits naturally on the bottom edge of the screen).
     - Styled with subtle borderless tonal surface separation (both Dark and Light themes per `dashboard-style.md`) so the upper session rail visibly terminates cleanly and hands the bottom-left space to the companion.
     - Hidden (`display: none`) when viewport `< 1600px` OR `companion_enabled == false`, in which case the upper session rail expands smoothly to fill 100% height just like before.
2. **Zero Vertical Jitter Invariant:**
   - Opening/closing `details.rws-unreg` or toggling `.unreg-notif` / `.admission-feedback` MUST change `#rail-companion-slot`'s `getBoundingClientRect().top`, `.bottom`, and `.height` by **strictly `0px`**.
3. **Bottom-Edge Popover & Hit-Target Safety:**
   - Verify `.ws-companion` popovers, speech bubble, and quick menu (mounted in `document.body` anchored to `slot.getBoundingClientRect()`) open cleanly **upward/rightward** inside the viewport (`rect.bottom <= window.innerHeight`, `rect.left >= 0`) now that the companion slot sits flush at the bottom-left corner.
   - Verify hover gear button / controls remain easily reachable and unclipped inside `#rail-companion-slot`.
   - Preserve Co-Lead laptop lid height/framing (`~16.5%` viewport height, zero chin/neck occlusion) and dizzy 2D FX clearance above the crown!

## Tests & Mandatory Real-VRM Visual QA
1. Update `agents-plugin-tool/internal/wsdashboard/testdata/companion-vrm-rail-dock.browser.cjs` (and any affected `companion_vrm_browser_test.go` / `frontend-companion*.test.js` assertions) to verify:
   - `#rail-companion-slot` is positioned **below** `details.rws-unreg` AND **below** `.rfoot`, flush to the bottom of `#r-rail` / viewport.
   - Toggling `details.rws-unreg` open & closed yields `0px` shift on `#rail-companion-slot` (`top`, `bottom`, `height`).
   - Quick menu / popovers stay 100% inside the viewport (`rect.bottom <= window.innerHeight`).
   - Below `1600px`, `#rail-companion-slot` is hidden and the session rail fills full height.
   - All existing VRM tests (`TestCompanionVrmBrowser`, `TestCompanionVrmRailDockBrowser`, `TestCompanionVrmCoLeadBrowser`, `TestCompanionVrmSwapDizzyBrowser`) pass on BOTH synthetic and real VRM 0.x & 1.0 (`WS_VRM_TEST_PHASE=1`).
2. Capture 4 high-DPI real-VRM screenshots into `/tmp/ws-vrm-bottom-left-qa/`:
   - `01-rail-unreg-collapsed.png` — 1680×960 left-rail context (VRM 1.0 + VRM 0.x), `Unregistered workspaces` collapsed, upper rail ending cleanly above bottom-left companion.
   - `02-rail-unreg-expanded-0px-shift.png` — `Unregistered workspaces` expanded, proving `0px` vertical shift of `#rail-companion-slot`.
   - `03-dark-and-light-tonal-separation.png` — Dark and Light theme tonal separation between upper session rail and dedicated bottom-left companion corner.
   - `04-quick-menu-and-laptop-bottom-flush.png` — Quick menu / controls unclipped inside viewport + laptop bottom edge flush at bottom-left.
3. Visually inspect **all 4 screenshots** via `read_file` and polish any spacing, contrast, clipping, or alignment issue before committing!

## Final Verification & Commit
- `node --test internal/wsdashboard/frontend-companion*.test.js`
- `go test -v -count=1 ./internal/wsdashboard -run "TestAsset|TestCompanionVrm"`
- `WS_VRM_TEST_PHASE=1 go test -v -count=1 ./internal/wsdashboard -run "TestCompanionVrm"` (with local real VRM 0.x + VRM 1.0 fixtures, never copying or referencing third-party names/files in git)
- `GOOS=windows go vet ./...` (from `agents-plugin-tool/`)
- Privacy/IP hygiene check: zero third-party character/model/creator/company names, zero `.vrm`/`.png` files tracked in git.
- Commit cleanly on `impl/develop/companion-rail-bottom-left` via `ws/git.commit` (Compatibility tier: `T0 transparent`; leave plan file unchanged).
