---
title: "대시보드 xterm 문자 폭 측정 폭주"
related:
  261123-bug-ws-dashboard-webview-idle-cpu: related dashboard renderer CPU and forced layout findings
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 9eed8268695e3087
sage-review-completeness-reviewed: 9eed8268695e3087
---

# Dashboard xterm character width measurement storm

## Background

In the lead's analysis of one Werkschaft dashboard webview capture
(`Trace-20261123T105817.json.gz`, 2026-11-23 10:58 local time), the webview
committed about one frame every 5–6 s. The renderer main thread was busy
for 45.2 s of 50.3 s, and the longest task took 6.9 s. The following figures
describe that capture.

- The CPU profile ties `requestAnimationFrame` (rAF) work in xterm's
  `DomRenderer.renderRows` and the `handleSelectionChanged` / `_refresh` path
  to `createRow → WidthCache.get → WidthCache._measure → offsetWidth`.
  Native `offsetWidth` took 35.9 s of self time, about 80% of main-thread
  busy time.
- `UpdateLayoutTree` ran 11,144 times for 36.3 s total. Of these calls,
  10,576 had `_measure` on the stack, with individual recalculations
  increasing from about 2.0 to 3.5 ms. The reported per-rAF callback range
  was 1,305–1,748 forced recalculations. Phase 1 measures the actual row
  range and tests whether width-cache reuse breaks across rows or frames.
- The dashboard's `rendered` / `eligible` / `reading` / `keep` / `scrollerOf`
  chain accounted for about 1.3 s of forced `Layout`. `checkVisibility`
  took 1.4 s. These are secondary findings linked to the idle CPU ticket.
  The functions live in `agents-plugin-tool/internal/wswhiteboard/assets/stream-view.js#L106-L205`
  and `agents-plugin-tool/internal/wswhiteboard/assets/stream-view.js#L286-L310`.

The capture is owner-local. The checkout has no copy of the raw trace, and
its recorded aggregates were reviewed without opening the download. If the
raw timings or workload need to be checked, obtain a shareable export from
the owner or capture a fresh baseline under documented conditions.

Start with `agents-plugin-tool/internal/wsdashboard/assets/terminal.js` and
the vendored `agents-plugin-tool/internal/wsdashboard/assets/xterm/` scripts.
The current source bytes reproduce the profiled `xterm-2107c771a5c2.js`
filename using the `xterm.js` + newline + `addon-fit.js` concatenation and
SHA-256 prefix in `agents-plugin-tool/internal/wsdashboard/terminal_routes.go#L99-L118`.

### Hidden PTY terminal hypothesis

The owner confirmed that a hidden PTY terminal was open in the current
session during the capture. Phase 1 checks whether that instance ran the
profiled refresh path and measured zero widths.

- Dashboard xterm instances belong to PTY terminals. Board PTY cards
  reconcile on mount. Chat PTY tabs restore inactive, and their widgets can
  initialize after the page's first show while the tabs remain unselected.
  Tab switching hides inactive panes through `data-active` and
  `display: none`. Close and removal dispose widgets, while restoration
  deduplicates tabs by PTY ID. Several distinct hidden PTYs can therefore
  coexist. Composer `!` commands render output through `pre.textContent`.
  See `assets/terminal.js` in the dashboard above (lazy-load guard, xterm
  creation, `reconcile`, `openTab`, `reconcileTabs`, and removal),
  `assets/dashboard.js` (`shStage`), `assets/dashboard-layout.css`
  (`.d-tpane`), `assets/cards.js` (card removal), and
  `assets/dashboard-composer-exec.js` (`pre.textContent`).
- The vendored `xterm.js` connects `SelectionService.refresh` via an rAF
  redraw event to `RenderService.handleSelectionChanged` and
  `DomRenderer.handleSelectionChanged`. This path can render the full
  viewport outside the `RenderService` intersection pause gate. Scrolling,
  including scrolling from PTY output, can request the selection refresh.
  `WidthCache` caches positive measurements. A terminal under
  `display: none` may return zero from `offsetWidth`, so repeated glyph
  lookups may each write measurement text and force another width read.
  Phase 1 verifies the instance, measurement results and actual trigger
  under the panel-hidden workload.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Decisions

- (1421) Use the owner's Chrome trace aggregates as historical observations.
  In Phase 1, use a shareable owner export or a fresh baseline capture to
  establish actual refreshed rows, width-cache lifetime, and the workload
  Phase 2 must match. Rejected: requiring access to the owner-local download,
  which is unnecessary when equivalent evidence is available.

## Prior Decisions

- e13999721 (2026-11-23, commit): "Decision 1416 assigns source analysis and daemon work to Codex and frontend fixes to Claude. … Keep xterm measurement and native renderer memory investigations separately scoped." — bearing: constrains
- 79e33b18f (2026-11-11, commit): "Size frames go only when rows/cols change, and folded or hidden cards never refit, so a fold or a window resize while folded sends no resize." — bearing: constrains
- 7b51aa5da (2026-11-11, commit): "Per span it only sets letter-spacing to cell width minus the glyph's measured width, which fixes the advance and nothing else." — bearing: constrains
- 261110-feat-ws-dashboard-terminal (2026-11-10, Decisions): "`@xterm/xterm` 5.x with `addon-fit`, added the way `internal/wswhiteboard/assets/mermaid/` is (npm pack, single-file dist plus LICENSE, hash-named served file)." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/internal/wsdashboard/assets/terminal.js`, `agents-plugin-tool/internal/wsdashboard/assets/xterm/`, and `agents-plugin-tool/internal/wsdashboard/terminal_routes.go` are investigation inputs. Phase 1 locates the Phase 2 edit. |
| scope.surface | internal | Phase 2 targets dashboard rendering and measurement. |
| scope.new_public_symbol | no | No new export is specified. |
| scope.new_type_contract | no | No new type or signature is specified. |
| scope.test_surface | existing | `agents-plugin-tool/internal/wsdashboard/testdata/session-load.test.cjs#L46-L71` exercises off-screen and folded terminal activation. The phase specifies touched Node tests and a reproduction or comparable trace. |
| complexity.reuse_points | confirmed | `agents-plugin-tool/internal/wsdashboard/assets/terminal.js#L160-L184` has fit and debounce hooks, and lines 401–412 construct xterm and load FitAddon. The vendored `xterm.js` contains `WidthCache`. |
| complexity.side_effect_risk | moderate | A change to fit or refresh can affect card sizing and the dimensions sent to the PTY. |
| risk.correctness | high | Stale measured widths or incomplete row refresh can break glyph alignment or leave selection rendering stale. |
| risk.fit | moderate | Preserve sizing and refresh behavior across folded, visible and maximized terminal cards. |
| risk.test | high | The timing and forced-recalculation acceptance checks need a reproducible workload in a real renderer. |
| risk.security_or_contract | moderate | Fit updates reach the PTY size contract if that path is implicated (`agents-plugin-tool/internal/wsdashboard/assets/terminal.js#L157-L184`). |

## Phases

### Phase 1: Diagnose xterm row refreshes and width cache misses (Codex)

Trace the dashboard's xterm construction, addon loading, sizing, selection
and render paths through to the profiled bundle. Confirm which renderer is
active (DOM, canvas or WebGL) and what selects it. Measure the row range
reaching `createRow` per refresh and account for why `WidthCache._measure`
repeats. Check font or option reassignment on refresh, explicit cache
invalidation or recreation, unbounded container height or row count,
selection changes that refresh the full row range, and fit/resize feedback.
When no terminal panel is visible, inventory which xterm instances still
exist, including hidden, folded or off-screen cards, and record the instance
count and visibility. Identify which instance and lifecycle state account
for any observed refresh work and check whether its width measurements
return zero during that work.

Record the cause, trigger, implicated instance and its visibility state,
row range, cache lifetime and renderer choice in the phase Result with
`file:line` evidence. Record the baseline conditions the before/after
reproduction or trace must match and the precise frontend change it needs
to verify. This phase is analysis only, with no product code changes.

### Result (6d8a8bbb8) - 2026-11-23

**Cause reproduced.** PTY output that scrolls an inactive tab or folded card
queues a selection redraw even with no active selection. The redraw bypasses
xterm's intersection pause and renders the whole viewport. Under
`display: none`, each uncached glyph/style lookup reads a zero
`offsetWidth`. The cache admits positive widths only, so every repeated
lookup for that key measures again, across rows and later frames.

- `assets/terminal.js:194-213,235-247,399-412` delivers PTY output via
  `term.write` and loads only FitAddon. The bundle hash matched
  `xterm-2107c771a5c2.js` using `terminal_routes.go:99-118`. The vendored
  `assets/xterm/xterm.js:1` (columns 32892, 35859) installs DomRenderer
  when no addon has set a renderer. Both fixture instances had
  `.xterm-rows`. The cell-size service can use OffscreenCanvas
  (columns 98834-101045), which accounts for valid cell dimensions on hidden
  open despite zero DOM glyph widths.
- In `xterm.js:1`, buffer scroll emits the core scroll event (columns
  264099, 146268), which requests `SelectionService.refresh` (34423). Its
  rAF emits the redraw signal (118104-118560) and calls
  `RenderService.handleSelectionChanged` (34169, 114284-114560).
  This directly reaches DomRenderer and `renderRows(0, rows - 1)` even
  with undefined selection endpoints (81692-81895). Ordinary
  `refreshRows` does honor the intersection pause (111884-112420).
  `renderRows` calls `createRow` for each viewport row at `ydisp + row`
  (83500-83939), where the row factory looks up width before span merging
  (85889-89300).
- The renderer owns one cache for its lifetime (75055-75450).
  `setFont` clears it when the family, size or weights change, and
  character-size and DPR handlers clear it directly (80657-81490,
  83101-83430, 92440-93510). `_measure` writes the glyph repeated 32
  times and reads `offsetWidth / 32` (93401). Across fixture refreshes
  the cache identity stayed fixed, with zero post-open `clear`,
  `setFont` or renderer replacements. The tab and fold used the 80-column,
  24-row default (276443). Revealing the tab at 640 × 420 px kept 24 rows,
  fit once to 86 columns and sent one matching size frame. The hidden
  writes caused zero resizes or size frames. See
  `assets/terminal.js:145-184,442-447,775-795` for the visibility and
  resize guards.
- The fixture had two mounted xterms while no terminal body was visible.
  The restored inactive tab and the nearby folded card each had a
  0 × 0 host and a paused render service. After the pause settled, each
  redrew on its own output while the other stayed idle. The third was a fresh
  off-screen board card, with no xterm or socket. Board cards start
  within a 200 px intersection margin, including folded cards for their
  previews, then keep the widget until removal. Restored tabs can open
  unselected after the page's first show. Multiple distinct hidden tabs
  may coexist, and previously mounted off-screen cards can still exist.
  See `assets/terminal.js:373-398,438-460,527-535,753-814,837-909`,
  `assets/dashboard.js:65-79`, `assets/dashboard-layout.css:118-122,214-215`,
  `assets/dashboard-board.css:71,218` and `assets/cards.js:1619-1625`.
  Composer `!` output uses `pre.textContent`
  (`assets/dashboard-composer-exec.js:130`).

**Before/after fixture.** Use a loopback static page with the real
`terminal.js`, hash-named vendor bundle, xterm and dashboard CSS, and the
vendored WsNerdSymbols font. Use a synthetic terminal list and WebSocket,
a stored tab ID with `data-ctab="convo"`, a near folded card, and a fresh
off-screen card 2,000 px down. Use an isolated headless Chrome profile
(158.0.8127.36, 1200 × 850, DPR 1 for this run). Wait for both mounted
render services to report paused before sending output. Each `out` message
carries 56 lines of 72 alternating ASCII glyphs followed by CRLF, cycling
normal, bold, italic and bold+italic with an SGR reset before every line.
Use `AB` for the first five writes, including the folded card and the
visible and re-hidden controls, then `CD` for fresh keys after hiding.
Reveal using `syncCtab(true, true)` at 640 × 420 px, then hide using
`syncCtab(false, false)`. Let rendering settle between writes
(280 ms in the fixture after the initial readiness gate).

Count `createRow`, cache lookups and positive/zero `_measure` results
by instance and glyph/style. Mark each `renderRows` interval and count
same-thread CDP `UpdateLayoutTree` and `Layout` events within it, using
`devtools.timeline,blink.user_timing`. Time the enclosing
`FireAnimationFrame`; the longest row-producing callback was 9.27 ms
in this run. Fixture assertions passed.

| Write or transition | Buffer rows passed to `createRow` | Width reads per refresh, positive / zero | `UpdateLayoutTree` / `Layout` in row render |
|---|---|---:|---:|
| Restored hidden tab, first output | 33–56 | 0 / 1,657 | 0 / 0 |
| Same tab, repeat hidden output | 89–112 | 0 / 1,657 | 0 / 0 |
| Near folded card, first output | 33–56 | 0 / 1,657 | 0 / 0 |
| Tab revealed, first visible redraw | 89–112 | 9 / 0 | 9 / 9 |
| Tab visible, repeat output | 145–168 | 0 / 0 | 0 / 0 |
| Tab hidden again, warmed glyphs | 201–224 | 0 / 0 | 0 / 0 |
| Tab hidden again, new glyphs | 257–280 | 0 / 1,656 | 0 / 0 |

Each measured row refresh covered viewport indices 0–23, with 1,657 cell
lookups from 23 populated rows plus the cursor cell. The cold visible
redraw measured eight glyph/style keys and space once each. Later visible
and warmed hidden redraws reused them. The fold preview still showed the
last nonempty line. The fixture reproduces redundant width reads and records
zero forced style recalculations in those hidden row renders. The owner's
trace counts forced recalculations, and its 35.9 s of `offsetWidth` self time
remains unattributed to a specific instance. This fixture already falls
below 100 ms, so it measures regression work counts.

**Phase 2 target.** Gate the direct selection redraw in xterm's
`RenderService.handleSelectionChanged`, or at its renderer call, so hidden
terminals retain the latest selection state and resume rendering it on show.
Keep PTY writes and fold previews active. Repeat the fixture and require
zero hidden `createRow` and glyph reads after settling, one positive read per
visible cold key, warm reuse, and the existing character-size/DPR invalidation.

Also queue a redraw with a known selection while visible and hide in the
same task. Hold the hidden intersection notification in the test until the
queued callback runs and assert zero `createRow` and glyph reads in that
callback. Show the pane and check that the first drawable refresh shows
the correct selection without any new output or selection input. Check glyph
alignment and verify visible fit and PTY dimensions.

For historical attribution and the 100 ms gate on the expensive path, the
owner-side follow-up is a shareable before/after Werkschaft Performance
capture with a known PTY output burst and no terminal panel visible. First
reproduce the expensive path before the fix. Correlate expensive selection
rAFs with a redacted terminal ID, host size and ancestor visibility,
rows/columns and `ydisp`, cache identity and invalidation counts,
zero/positive width-read counts by key, and stack-attributed
`UpdateLayoutTree` and `Layout` durations. Compare a visible control replay of
the same output in the same webview. Measure all relevant xterm rAFs in
both captures, including callbacks that skip row rendering after the fix.
Historical attribution and the expensive-path timing gate remain unverified
until a comparable capture exists.

### Phase 2: Fix xterm refresh and width measurement (frontend: Claude)

Use the Phase 1 Result to fix the source of excessive row refreshes and
width-cache misses.

Verification:

- Run the Phase 1 workload before and after the fix. Record the longest xterm
  rAF task and the count of `_measure`-triggered forced recalculations per
  comparable refresh, including cold work. The longest xterm rAF task is
  at most 100 ms after the fix. Verify that the redundant measurements
  identified in Phase 1 are eliminated, and count legitimate first
  measurements separately. For repeated lookups within a valid cache
  generation, reuse the first positive measurement for each key. Check
  that character-size and device-pixel-ratio changes still invalidate the
  measurements they affect.
- Verify visible glyph alignment and selection highlighting across refresh
  and terminal hide/show, and verify folded previews still update.
  Exercise fitting on resize and confirm that the rendered rows and columns
  and the PTY dimensions agree. Include the hidden-panel case from Phase 1
  in the workload.
- Run the touched Node test files with `node --test`.
