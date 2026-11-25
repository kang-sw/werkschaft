# Plan: Unified Dashboard Session-Switch & Heavy-Session Scroll Performance (`ui-session-switch-scroll-perf`)

## Context & Goal
Chrome DevTools traces of the dashboard (`Trace-20261125T143528.json.gz`, `Trace-20261125T152359.json.gz`, `Trace-20261125T160505.json.gz`) after the `:has()` removal (`f4c9d4a2e`) identified two severe user-facing latency bottlenecks:
1. **Cold session switching (`Trace-20261125T152359.json.gz`):** `10,524.2 ms` main-thread busy across 3 cold switches (`57.9%` / `6,097.9 ms` forced synchronous Style+Layout; Switch 3 took `4.34 s` click-to-visible with a `2.12 s` main-thread freeze).
2. **Heavy-session `#bcol` whiteboard scrolling (`Trace-20261125T160505.json.gz`):** `8,499.3 ms / 8,585 ms` (`99.0%`) main-thread saturation -> **3.24 main-thread FPS** (`776` dropped frames, `79.1%` / `6,726.6 ms` Style+Layout with `49.1%` forced reflows + `~578 ms` `about:srcdoc` iframe teardown/reload churn + `478 ms` mid-scroll `ws.onmessage` interference).

Per confirmed owner decisions `(1602)=a`, `(1603)=a`, `(1605)=a`, and `(1607)=a`, eliminate these forced synchronous reflows, `iframe.onload` blocking, `about:srcdoc` scroll churn, and unbatched DOM/style invalidations in a single, strictly T0-compatible frontend performance branch (`impl/develop/ui-session-switch-scroll-perf`, branched from `3ba7e1889`).

## Required Manuals (Read Before Editing)
- `ai-docs/manuals/performance.md` (especially `## Frontend Minimal Updates` Rules 1–5)
- `ai-docs/manuals/dashboard-style.md`
- `ai-docs/manuals/shipped-surface-boundary.md`

## Implementation Scope (Strictly T0, Zero API/Schema Changes)

### Part A — Session-Switch Perceived & Cold-Boot Latency (`(1603)=a` + `(1602)=a`)
1. **Early `shShow` reveal & offscreen `about:srcdoc` deferral (`dashboard-lifecycle.js`, `dashboard-frames.js`):**
   - Today `shShow` (`dashboard-lifecycle.js:929-942,1074`) waits for `iframe.onload`, which blocks on every nested `about:srcdoc` doc-frame inside the switched session (`~2,000 ms` perceived delay in Switch 3).
   - Reveal the frame on `DOMContentLoaded` / frame-ready signal (or ready postMessage once main document layout/shell handshake is ready) rather than waiting for all nested `about:srcdoc` sub-iframes to finish loading; ensure offscreen `about:srcdoc` frames stay deferred (`mountFramesPaced` / NEAR-band only) and never block initial session reveal.
   - Preserve all existing shell handshake invariants (`shell-post-layout.test.cjs`, `shell_frame_test.go`, `hud.browser.cjs`, `data-fpopover`).
2. **Cold-boot synchronous init coalescing (`dashboard-drawer.js`, `tracker-render.js`, `dashboard-board.js`, `cards.js`, `dashboard-lifecycle.js`):**
   - Coalesce cold-boot layout reads (`drHeaderBottom`/`drRoom`, `tracker-render.measure`, `paintGallery`/`fitToolbar`, initial board measurements, `~1,300 ms`) into a batched read-then-write rAF pass (Rule 4) instead of interleaved read->write->read calls during synchronous script boot.
3. **`visible(convo)` `paneVisible` cache & `#convo` CSS containment (`dashboard.js`, `dashboard-audit-rows.js`, `dashboard-conversation.css`):**
   - Replace repeated `visible(convo)` `getClientRects()` forced reflows (`dashboard.js:231,399`, `dashboard-audit-rows.js:519`, `606.2 ms`, 50 ULT / 24 Layout) by reusing the existing `paneVisible["convo"]` tracked by `ResizeObserver` (`dashboard.js:325-332`) and batching scroll pinning into rAF.
   - Add safe CSS containment (`contain: layout style` on `#convo` / message rows and safe `content-visibility` only where scroll-anchor / scrollHeight / `scrollTo` invariants remain 100% intact; verify no jump or broken sticky/popover clipping!).
4. **Whiteboard `readyHeight` + `scrollerOf` + `stream-view.js` scroll listener (`wswhiteboard/assets/stream.js`, `wswhiteboard/assets/stream-view.js`):**
   - Batch `readyHeight` (`stream.js:65-77`, `596.9 ms`) via rAF read-then-write and cache `scrollerOf` (`stream-view.js:112-118`, `176.2 ms`) instead of calling `getComputedStyle` repeatedly.
   - In `stream-view.js:238-245` (`25.5 ms`, 38 forced ULT), read `scroller.scrollTop` at most once per scroll event and skip layout reads when `o.state === "user-scrolling"` (just extend `o.until`).
5. **Secondary cold-boot/switch forced reflows (`rail.js`, `terminal.js`, `panel-diff.js`):**
   - `terminal.js:rgb()` (`195.3 ms`): eliminate temporary DOM-append + `getComputedStyle` color probing (cache resolved CSS variables once per theme change or read from `:root`).
   - `panel-diff.js:layout()` (`75.6 ms`): split read-write loop into read pass then write pass.
   - `rail.js:show()` (`208.3 ms`): avoid redundant forced reflows when showing/activating rail state.

### Part B — `#bcol` Virtual-Scroll Engine Overhaul (`(1603)=a` #1 + `(1605)=a` #1–#6)
6. **`freeApplyOffWindow` -> `virtualCollapse` read/write batching (`dashboard-board-anchor.js:196-232,1031-1054,1250-1253`, `1,250 ms` switch / `1,755.8 ms` scroll):**
   - Batch ALL DOM reads (`sec.scrollTop`, height capture, frame heights) BEFORE mutating `data-wb-offwindow="hidden"` or detaching children (Rule 4).
   - On cold initial load (`wasActive === false`), skip unnecessary `sec.scrollTop` / layout reads altogether.
7. **`freeLayout` -> `mountSlot` + `freeMeasureHeight` (`dashboard-board-anchor.js:865-881,1209-1243`, `973.6 ms`):**
   - During scroll ticks, use `freeCachedHeight(n)` instead of synchronously reading `n.offsetHeight` right after `mountSlot`/`virtualHydrate` inside the `while (aboveH < need)` / `while (belowH < need)` loops.
   - Let the already-attached `freeCardRO` (`ResizeObserver`) update `freeHeightCache` and reconcile positions on the next frame (Rules 2, 4).
8. **`freeScrollTick` + `freeScheduleIdle` + `anchorScrollEnd` (`dashboard-board-anchor.js:1425-1452,1495-1502,1910-1919`, `843.6 ms`):**
   - In `freeScrollTick`, reuse `top` already read at line 1425 (plus any `shift` returned by `freeLayout`) instead of re-reading `col.scrollTop` at line 1447 right after layout mutations.
   - In `freeScheduleIdle`, track `lastScrollTop` / scroll generation in `freeScrollTick` instead of reading `col.scrollTop` inside the `setTimeout` while layout is dirty.
   - In `anchorScrollEnd`, schedule via `freeOnScroll()` (rAF) instead of calling `freeScrollTick()` synchronously during `scrollend`.
9. **`about:srcdoc` iframe churn prevention during scroll (`dashboard-board-anchor.js`, `dashboard-frames.js`, `~578 ms+`):**
   - Decouple `data-wb-offwindow="hidden"` hiding from immediate `virtualCollapse` body/iframe eviction during active scrolling: keep `iframe.doc-frame` mounted across scroll bursts and only evict/unmount collapsed bodies after scroll settles (`FREE_IDLE_RECENTER_MS`) or beyond a wider hysteresis band, matching `dashboard-frames.js`'s NEAR/FAR band design so scrolling back and forth never tears down and re-parses `about:srcdoc` iframes.
10. **Mid-scroll `ws.onmessage` & `cards.js:floatToolbar` suppression (`dashboard-stream.js`, `dashboard-rail.js`, `dashboard-workspace-admission.js`, `cards.js`, `584.5 ms`):**
    - Defer non-urgent rail DOM swaps (`onRailRows`, `302.6 ms`) while `#bcol` is actively scrolling (coalesce and apply on `scrollend`/idle).
    - In `wsAdmissionSync` (`dashboard-workspace-admission.js:53-93`, `43.9 ms`), skip `list.scrollTop = wsUnregScroll` when `.rws-unreg` is closed or unchanged.
    - Reduce/avoid full `frag(rowInputHTML)` HTML reparsing (`117.2 ms` `about:blank`) for unchanged rail rows where safe (patch changed rows by key / skip unchanged rows, Rule 5).
    - In `cards.js:floatToolbar` (`cards.js:181-237`, `106.4 ms`), ignore scroll events whose `e.target` is `#bcol` when `cachedScroller && cachedScroller.wbScrollOwner && !overviewOn` (where `snapDeck` unconditionally forces `floating = false`), running `update()` only on `ResizeObserver` / `resize` / overview toggle.

## Constraints & Verification
- **Strict CSS `:has()` guard:** Do NOT introduce any forbidden `:has()` selectors (`frontend-idle.test.js` enforces this!).
- **Do NOT touch `railKey()` or `#tkdrawer` overlay dismiss logic** (being modified concurrently in `0522-tkdrawer-overlay-fix`); keep `dashboard-lifecycle.js` edits cleanly scoped to `shShow` / frame reveal / cold-boot init so merges are conflict-free.
- **No `Co-Authored-By` trailer** in commits.
- **Windows AV safety (CRITICAL):** NEVER run local `go`, `go test`, `go build`, `go vet`, or `gofmt` on this Windows host.
- **Tests:**
  - Add focused Node unit/regression tests covering:
    1. `freeApplyOffWindow` / `virtualCollapse` batched read-before-write (no interleaved write->read thrashing) + cold-load skip
    2. `freeLayout` scroll tick using cached height instead of sync `offsetHeight`, `freeScrollTick` reusing `top + shift` without post-layout `col.scrollTop` reread, and scroll-active iframe eviction deferral
    3. `cards.js:floatToolbar` skipping `#bcol` scroll when `snapDeck` is active
    4. `stream-view.js` single `scrollTop` read / `user-scrolling` skip
    5. `shShow` early reveal / `visible(convo)` `paneVisible` cache / `wsAdmissionSync` closed-list skip
  - Run full relevant local `node --test` suites across `agents-plugin-tool/internal/wsdashboard/assets/` and `agents-plugin-tool/internal/wswhiteboard/assets/`.
  - Run Go verification on remote `w5090`:
    ```bash
    bash D:/sys_lock.sh remote w5090 run ui-perf-switch 20 --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/unfixable-vitalize-barterer' -- 'export PATH=$HOME/sdk/go/bin:$HOME/.local/node/bin:$PATH; cd agents-plugin-tool && go vet ./internal/wsdashboard ./internal/wswhiteboard && go test -race ./internal/wswhiteboard && go test -race ./internal/wsdashboard -run "TestShell|TestBoard|TestAnchor|TestFrame|TestRail|TestFrontend|TestStream|TestDrawer|TestTracker|TestCard"'
    ```
- Commit cleanly on `impl/develop/ui-session-switch-scroll-perf` (1 or 2 logical commits, including this unchanged plan file `ai-docs/.plans/2026-11/25-1707-ui-session-switch-scroll-perf.md`), do NOT merge or push, and report commit SHA(s) + verbatim Node and `w5090` verification output.
