# Plan: Fix Main Board (`#bcol`) Designated-Y Virtual Scroll Flash & Snap-Back

## Problem
After jumping to the bottom of the Main Board (`#bcol`) via Overview or rail/dial navigation (`anchorY = FREE_RUNWAY_MID = 50000px`, `board.style.minHeight ≈ 50400px`, `scrollTop ≈ 49600px`), scrolling upward causes:
1. `freeScrollTick` -> `freeLayout({ scrollTick: true })` to hit `start === 0` early (while the viewport is still near `~49000px`) and execute a `-47000px` `freeShiftScroll` mid-gesture while `freeUpdateMoreAndStage` keeps `minHeight` at `~50400px` until 150ms idle.
2. In Safari/WebKit, the async scrolling thread overwrites `scrollTop` back toward `~49000px` while cards sit at `60..3000px`.
3. `dashboard-frames.js` `FAR` IntersectionObserver unmounts live iframes of active mounted window cards because `WsBoardAnchor.keepFrame(t)` only protects `freeDeferredCollapse` cards, not active window cards.
4. 150ms later, `freeIdleRecenter` shrinks `minHeight` to `~3000px`, clamping `scrollTop` back to the bottom, and `NEAR` IO remounts iframes -> visible flash and snap-back.
5. `freeOnCardResize` can also fire mid-scroll `freeShiftScroll` bursts when `freeWinStart === 0`, and `#bcol` retains `overflow-anchor: auto` under `data-board-free`.

## Plan (Options A + B + C)
1. **Option A (`dashboard-board-anchor.js`)**:
   - Track `freeTopRebasePending` (reset in `freeSuspend`).
   - In `freeLayout` Phase C (`start === 0`): when `scrollTick && shift < 0` (`ys[0] > topPad`) and `curTop > ys[0]`, defer the negative rebase (`freeTopRebasePending = true`, ensure `freeScheduleIdle()` is armed) instead of calling `freeShiftScroll` mid-gesture. Still shift immediately if `ys[0] < topPad` (`shift > 0`) or if `curTop <= ys[0]`.
   - In `freeScrollTick`: include `(freeTopRebasePending && freeWinStart === 0 && yFirst !== undefined && top <= yFirst)` in `needSlide` so scrolling all the way past `ys[0]` in one gesture rebases at the top boundary instead of entering empty runway.
   - In `freeIdleRecenter`: when `freeWinStart === 0`, complete any pending top-boundary rebase (`topShift = freeTopPad(board) - y0`), shifting `col.scrollTop` BEFORE calling `freeUpdateMoreAndStage(..., false)` to shrink `board.style.minHeight` and update `moreBtn`.
2. **Option B (`dashboard-board-anchor.js`)**:
   - In `WsBoardAnchor.keepFrame(t)`: return `true` when `freeActive` and either `freePos.has(t)` (active mounted window card) or `((freeScrollRaf || freeIdleTimer) && freeDeferredCollapse.has(t))`, so `FAR` IO never unmounts active window frames while off-window evicted cards still unmount normally.
3. **Option C (`dashboard-board.css`, `dashboard-board-anchor.js`)**:
   - Add `.dgrid:is(.live, .history, .worktree) > #bcol[data-board-free] { overflow-anchor: none; }` in `dashboard-board.css` (higher specificity than `dashboard-layout.css`'s `.dgrid.<mode> > .bcol`).
   - In `freeOnCardResize`: when `freeWinStart === 0`, `shift < 0` (`y0 > topPad`), and `freeIdleTimer` is active, defer the rebase (`freeTopRebasePending = true`) instead of calling `freeShiftScroll` mid-scroll; still shift immediately if `y0 < topPad` (`shift > 0`).
4. **Tests & Verification**:
   - Add targeted unit tests in `agents-plugin-tool/internal/wsdashboard/testdata/board-free-canvas.test.cjs` covering deferred `start === 0` rebase + idle completion + top-boundary swipe-through, `keepFrame(t)` protection for active window vs off-window cards, `freeOnCardResize` mid-scroll deferral, and `overflow-anchor: none` CSS rule.
   - Run full Node test suite, `go vet ./internal/wsdashboard/...`, and relevant Go tests.
