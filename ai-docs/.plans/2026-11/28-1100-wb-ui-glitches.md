# wb-ui-glitches — plan

Two dashboard bugs, one T0 commit.

## Bug 1 — duplicate/cascading folded turn rows (whiteboard chat feed)

Cause: `groupTurnSummaries` anchors the folded summary at the first folded entry still inside the window (RingCap 50 or a `/history` page). When the window slides, the anchor moves to a new `data-entry` key; `mergeEntries` keys nodes by `data-entry`, so the old summary row stayed and a new one was added.

Fix:
- Server: `TurnSummaryView.TurnKey` (the closing RingTurn key) is emitted as `data-turn-key`, included in `CacheFingerprint`.
- Client (`dashboard-conversation.js`): `dedupTurn` — earliest-key anchor wins; a later anchor becomes a hidden `data-turn-absorbed` marker; an earlier arriving anchor retires the later one; visible `.turnline` footers inside a summarised turn are retired; same key+rev short-circuits.
- Tests: `TestTurnSummaryTurnKeyStaysStableWhenWindowTruncatesTurnHead` (Go), `mergeEntries keeps one summary per data-turn-key ...` (node).

## Bug 2 — whiteboard middle viewport blank until manual scroll

Cause: free-canvas virtual scroll measured `#bcol` while the frame was hidden/0-sized; the visibility handler only re-settled when `_skippedHidden` was set, and nothing settled after `shown:true`.

Fix (new fragment `dashboard-board-reveal.js`, keeps anchor file from growing):
- Every hidden→visible transition: re-measure `#bcol`, `WsBoardAnchor.settle()` (no `_skippedHidden` gate).
- `shown:true` handler schedules a coalesced double-rAF settle after `mountFramesPaced`.
- Coalesced triggers: `visibilitychange`, `pageshow`, filter/tab/view change (`revealPanes`, `selectFilter`) when `freeEnabled()`.
- `anchorSize` ignores transient 0×0 reports.
- Tests: 6 node tests in `frontend-perf-switch-scroll.test.js`; Chromium `TestBoardRevealBrowser` (gated by `WS_PLAYWRIGHT_MODULE`).

## Deviations / notes
- Plan file populated although plans are normally write-once: explicit user instruction.
- `dashboard-board-anchor.js` was 2275 lines at HEAD, now 2267 (still over 2000; reduced, not inflated; new logic extracted into the reveal fragment).
- The Chromium test passes on pre-fix assets as well (headless Chromium's ResizeObserver already settles); it is a regression guard, the node tests are the discriminating ones.
