# Fix `internal/wsdashboard` CI Test Failures

## Goal

Resolve the 4 failing tests in `agents-plugin-tool/internal/wsdashboard` observed in pre-flight CI (`scripts/ci-test-shard.sh all`) and verify zero remaining failures across both the full `internal/wsdashboard` Go test suite and the Node dashboard JS test suite.

## Root-Cause Diagnosis & Changes

1. **`TestSessionHoverScriptLifecycle` (`pageview_hover_test.go:163`)** — *Test harness drift*
   - `rail.js` guarded `if (pop.hidden) pop.hidden = false;` to avoid redundant DOM writes when the hover popup is already visible.
   - `railJSHarness`'s `El` constructor in `railscript_test.go` left `this.hidden` undefined (serializing to `null`), whereas browsers initialize `HTMLElement.hidden` to `false`.
   - Fix: initialize `this.hidden = false;` in `El` in `railscript_test.go`.

2. **`TestGalleryLayoutModes` (`pincol_width_test.go:134`)** — *Test harness drift*
   - `dashboard-board.js` switched `if (galNarrow) placeGallery();` to `if (galNarrow) queueGallery();` for rAF batching.
   - `pincol_width_test.go` extracted the `layout` hook and stubbed `placeGallery()` without `queueGallery()`.
   - Fix: define `function queueGallery() { placeGallery(); }` in `pincol_width_test.go`.

3. **`TestSelectionComposerSendSnapshotsAndCancel` (`selection_composer_test.go:333`)** — *Test harness drift*
   - `dashboard.js` updated `pinConvo()` to call `convoVisible()`, which consults `paneObserver` and `paneVisible` before falling back to `visible(convo)`.
   - `selectionProductionRuntime` in `selection_composer_test.go` extracted `pinConvo` without `paneScroll` (`paneVisible`), `convoVisible`, or `paneObserver`.
   - Fix: extract `"paneScroll ="` and `"convoVisible"` in `selectionProductionRuntime`, and initialize `paneObserver = null` in `selectionRuntimeEnvironment`.

4. **`TestSessionPageOffersSystemOpenToLocalViewersOnly` (`sysopen_route_test.go:246`)** — *Production CSS selector inconsistency*
   - The LAN session page does not set `data-sysopen="1"` on `<html>` (`SysOpen: localViewer(r)` is `false`).
   - `assets/dashboard-conversation.css` used `html[data-mode="live"][data-sysopen="1"] .exec-view-out`, which is inlined in `<style>{{.Assets.CSS}}</style>` on non-frame pages, causing the literal substring `data-sysopen="1"` to appear on LAN pages and diverging from the attribute-presence convention (`[data-sysopen]` / `hasAttribute("data-sysopen")`) used across `dashboard-rail.css` and JS.
   - Fix: change lines 929–930 of `assets/dashboard-conversation.css` to `html[data-mode="live"][data-sysopen]`.

## Verification

- `cd agents-plugin-tool && go test -count=1 ./internal/wsdashboard -run 'TestSessionHoverScriptLifecycle|TestGalleryLayoutModes|TestSelectionComposerSendSnapshotsAndCancel|TestSessionPageOffersSystemOpenToLocalViewersOnly'`
- `cd agents-plugin-tool && go test -count=1 -timeout 20m ./internal/wsdashboard`
- `node --test agents-plugin-tool/internal/wsdashboard/testdata/*.test.cjs agents-plugin-tool/internal/wsdashboard/*.test.js`
