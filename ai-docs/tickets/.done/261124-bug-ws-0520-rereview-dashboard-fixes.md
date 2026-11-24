---
title: 0.52 게이트 재리뷰 대시보드 결함 수정 (스냅 덱·오버뷰·작성기)
related:
  261124-bug-ws-0520-release-gate-rereview-blockers: the non-dashboard findings of the same re-review, run in parallel by the harness session
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: cdf3d25d02c4ab49
sage-review-completeness-reviewed: cdf3d25d02c4ab49
completed: 2026-11-24
---

# 0.52 release-gate re-review dashboard fixes

## Background

The 0.52 release-gate re-review of `b4d28c185..ae485f20b` (findings in the
review cache, `0f55934a-01-findings.md`, Important 4-8 and 10) found
caller-visible dashboard regressions in the snap-deck follow-ups
(`bb0217943`), the doc-frame wheel change (`acf669e30`), and the Overview and
composer changes merged through `ae485f20b`. The release stays blocked until
these and `261124-bug-ws-0520-release-gate-rereview-blockers` land and a
re-review clears.

## Decisions

- Each item follows the remedy the re-review named; no product behavior
  changes beyond restoring the intended one.
- The snap-deck constant-work contract from the snap-deck tickets stands:
  steady-state navigation cost does not grow with board size or with the
  number of far topics holding drafts or pending answers.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`) and
  `ai-docs/manuals/shipped-surface-boundary.md` apply (paths under
  `agents-plugin-tool/internal/wsdashboard/` and `internal/wswhiteboard/`).
- Every changed UI surface gets a screenshot check against the pre-change
  rendering before merge.
- Browser tests need `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH`
  set explicitly, because fixtures redirect HOME.

## Prior Decisions

- 261123-feat-ws-dashboard-main-panel-snap-deck (2026-11-24, Decisions): "Keep idle frames at zero" — bearing: constrains
- 5ddcd353 (2026-11-24, commit): "extracted fillRoots and hydrateDetached in wswhiteboard/assets/stream.js" — bearing: supports
- b49e101a (2026-11-24, commit): "Rejected keeping synthesized plain-text placeholder divs because they distorted card height, header bars, badges, and recap formatting during Quick Look hover." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phase 1 names dashboard-board-anchor.js, cards.js, overview.js, stream.js and composer-multiline.test.cjs under agents-plugin-tool/internal/. |
| scope.surface | cross-module | Dashboard virtualization delegates detached history hydration to wbHistory.hydrateDetached in wswhiteboard/assets/stream.js (dashboard-board-anchor.js:547-558). |
| scope.new_public_symbol | no | Phase 1 repairs existing WsCards.decorate, onFrameWheel, buildPlaceholder and hydrateDetached paths; no new public entry point is required. |
| scope.new_type_contract | no | Existing browser helpers and tests; no new wire, persisted data or type contract is specified. |
| scope.test_surface | existing | testdata/composer-multiline.test.cjs, gallery-frame-wheel.test.cjs, overview-preview-shape.test.cjs and board-snap-deck-p4.browser.cjs exist under internal/wsdashboard/. |
| complexity.reuse_points | confirmed | Read WsCards.decorate (cards.js:1417-1438), virtualSearchRoots and hydrateDetached delegation (dashboard-board-anchor.js:533-558), hydrateDetached (stream.js:315-336) and buildPlaceholder (overview.js:82-110). |
| complexity.side_effect_risk | moderate | Shared decoration and history admission affect live and detached topics during asynchronous navigation. |
| risk.correctness | high | Phase 1 repairs a response/navigation race, wrong-page wheel admission and unbounded steady-state reconciliation. |
| risk.fit | moderate | Overview placeholder geometry and accessible focus naming must preserve the live preview and inert cloned controls. |
| risk.test | high | Existing composer tests simulate the reset themselves; independent production reset and operation-count regressions are required. |
| risk.security_or_contract | moderate | Scope must retain the constant-work navigation contract and preserve inert cloned controls while restoring their destination's accessible name. |

## Phases

### Phase 1: Fix the dashboard findings

- **Snap-deck whole-board work** `internal/wsdashboard/assets/dashboard-board-anchor.js:444`
  and `:677-709`. Scope the whole hydration path of an SSE-updated stashed
  topic to that topic: `WsCards.decorate`, `bindCapsuleNotes()`, `wbForm()`
  and `applySent(true)` (`:444-449`), and exemption reconciliation to changed
  exemptions. Test: operation counts stay flat across two board sizes and
  across a growing number of far topics with drafts, measured when navigation
  enters an SSE-updated stashed topic.
- **Detached history reply** `internal/wswhiteboard/assets/stream.js:326`.
  Resolve the current target (live or detached) when the lazy-history reply
  arrives; a reply that resolves to a live topic goes through the live
  admission path (`stream.js:249-265`: panel state, control binding, frame
  mounting), not `hydrateDetached`. Test: navigation reattaches the topic
  while a palette search is pending; older matches still appear and the newly
  loaded content works (controls bound, frames mounted).
- **Doc-frame wheel** `internal/wsdashboard/assets/cards.js:1275`. Accept the
  wheel message only from a frame on the active gallery page. Test: a
  delayed message from a previously visible page does not change the active
  page's tension.
- **Overview geometry** `internal/wsdashboard/assets/overview.js:82`. The
  placeholder keeps the folded header selectors (`dashboard-board.css:73-76`
  require a `section`), hides the cloned recap as they would, and keeps the
  folded header padding. Test: rendered geometry of a folded topic in Quick
  Look matches the folded topic.
- **Overview focus name** `internal/wsdashboard/assets/overview.js:99`, `:205`.
  The focused destination carries the original card or topic label while
  cloned controls stay inert. Test: the focused element's accessible name.
- **Composer reset tests** `internal/wsdashboard/testdata/composer-multiline.test.cjs:130`,
  `:339-344`. Exercise the production send reset and the exec-ack reset
  independently, including an acknowledgement with remaining draft text.
  Test: removing either production reset site fails a test.

All code and test paths above are relative to `agents-plugin-tool/`.

Verification: the named tests, the snap-deck, gallery, composer, Overview
and rail Chromium suites, `go test ./internal/wsdashboard
./internal/wswhiteboard`, and screenshots of Overview Quick Look (folded
topic, keyboard focus) and the composer after send.

### Result (3a989afe4) - 2026-11-24

All six dashboard/whiteboard findings from the 0.52.0 release-gate re-review (Important #4–#8 and #10; T0 client-side changes, `dashboard-leads.js` untouched) are fixed and verified:

- **Important #4 (Snap-deck whole-board work):** Scoped `virtualHydrate`'s stashed path in `dashboard-board-anchor.js` to `sec` (`WsCards.decorate(sec)` with `scope` forwarded in `cards.js`, `bindCapsuleNotes(sec)` in `dashboard-composer-pickers.js`, `wbBindBoard(sec)` instead of `wbForm()`, and `applySent(true, sec)` via `arguments[1]` in `dashboard-composer-status.js`). Replaced full `virtualExemptSet` scans during delta `virtualReconcile` with dirty-only `virtualExemptDirty` tracking driven by `anchorBoardMO`, passive board `input`/`change`/`focusout`/`click` listeners, and `overview.js` restore hooks. Verified flat O(1) `describes`, `slotVisits`, `frameIndexes`, `fullReconciles === 0`, `prunes === 0`, and scoped-only hydration in Node (`N=12` vs `N=60`, `2` vs `20` far exempt topics) and Chromium (`N=12` vs `N=24`, `2` vs `12` far exempt topics).
- **Important #5 (Detached history reply race):** Updated `hydrateDetached` in `wswhiteboard/assets/stream.js` to re-resolve live non-virtualized `section.topic[data-topic]` when the async lazy-history reply arrives and route through live `hydrateWith` so panel state, `wbAgo`, `wbBindBoard`, and `wbhistoryload` run on reattached topics. Verified in Chromium (`board-snap-deck-0512.browser.cjs`) that navigating into a topic while a palette lazy-history fetch is in flight runs live admission, clears lazy gaps, and surfaces archived matches in the palette.
- **Important #6 (Doc-frame wheel):** Updated `onFrameWheel` in `cards.js` to require the frame to belong to the active gallery page (`activePage.contains(frame)`), rejecting delayed wheel messages from previously visible, still-attached inactive pages. Verified in `gallery-frame-wheel.test.cjs` (6/6 pass) and `TestGalleryBrowser`.
- **Important #7 & #8 (Overview folded geometry & focus name):** Updated `buildPlaceholder` in `overview.js` to create `<section>` for topic placeholders (matching `dashboard-board.css` folded selectors) and set `role="button"` + `aria-label` from `.tname`/`.bc-nm` while keeping cloned headers/controls `inert` and `aria-hidden="true"`. Verified in `overview-preview-shape.test.cjs` (3/3 pass), `board-snap-deck-0512.browser.cjs`, and before/after Chromium screenshots (`/tmp/0520-rereview-dashboard-shots/{before,after}/`: `phTag: DIV -> SECTION`, `tsubDisplay: flex -> none`, `phHeight: 40px` matching folded topic, `tbarPadRight: 12px`, `role: button`, `ariaLabel: "Folded Architecture Topic"`, `ariaSnapshot: - button "Folded Architecture Topic"`).
- **Important #10 (Composer reset tests):** Updated `composer-multiline.test.cjs` (10/10 pass) to execute production `sendMessage` (`dashboard-composer-delivery.js`) and `cexecAcknowledged` (`dashboard-composer-exec.js`) independently, including ack emptying draft and single-line/multiline remaining drafts. Confirmed via mutation check that removing any of the three production `resetAutoMultiline()` calls independently fails `composer-multiline.test.cjs`.
- **Verification:** `generate-package-assets.py` clean; 24/24 Node tests pass; `TestBoardSnapDeck0512Browser` (10/10), `TestGallery*`, `TestComposerMultiline*`, `TestOverview*`, `TestBoardOverview*`, `TestSearchPalette*`, `TestBoardAnchor*`, `TestBoardVirtualize*`, `TestBoardToolbar*`, `TestBoardStateFilter*`, `TestCheckRowClientPaths`, `TestCloseRequestComposerLifecycle` all pass; `go test ./internal/wswhiteboard` passes (`wsdashboard` full run had only pre-existing unrelated `TestConversationSendRearmsBottomFollow` and `TestHiring*` failures); `GOOS=windows go test -c ./internal/wsdashboard` and `GOOS=windows go vet ./internal/wsdashboard/... ./internal/wswhiteboard/...` pass.


## Resolution (2026-11-24)

Fixed all six 0.52.0 release-gate re-review dashboard/whiteboard findings (Important #4-#8 and #10) in commit 3a989afe4 with Node, Chromium, Go, Windows cross-compile/vet, mutation-proof, and before/after screenshot verification.
