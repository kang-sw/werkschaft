---
title: 0.52 게이트 재리뷰 대시보드 결함 수정 (스냅 덱·오버뷰·작성기)
related:
  261124-bug-ws-0520-release-gate-rereview-blockers: the non-dashboard findings of the same re-review, run in parallel by the harness session
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: cdf3d25d02c4ab49
sage-review-completeness-reviewed: cdf3d25d02c4ab49
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
