---
title: 0.52 최종 게이트 대시보드 결함 수정 (전송 표시 색인·설정 정리·브라우저 테스트)
related:
  261124-bug-ws-0520-release-gate-final-blockers: parallel sibling for the same re-review, run by the harness session; it owns the dashboard checkout-deletion occupancy fix, this ticket owns sent-state scoping and settings cleanup
  261124-bug-ws-0520-rereview-dashboard-fixes: the previous dashboard repair; its Important #4 sent-state scoping is completed here
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 16d1aa3a9b846884
sage-review-completeness-reviewed: 16d1aa3a9b846884
---

# 0.52 final release-gate dashboard fixes

## Background

The 0.52 release-gate re-review of `cee60ac89..247f6c724` (findings in the
review cache, `35bb2392-01-findings.md`) recorded `block`. Two of its
Important findings are dashboard UI defects, and seven Chromium tests in
`internal/wsdashboard` fail identically on `2c1d50fef` and `247f6c724`, which
would turn the release CI red. The release stays blocked until this ticket and
`261124-bug-ws-0520-release-gate-final-blockers` land and a re-review clears.

## Decisions

- Sent-state lookup (Important 2): scoped hydration resolves sent state for
  its items through an id-keyed index, so per-item work is constant and the
  cost does not grow with unrelated board topics or the total sent-row count.
  The index is maintained where sent rows are added or removed, so a hydration
  does no work proportional to the total sent-row count and no document-wide
  `.sentdata` query per hydrated item. Rejected: per-item document-wide
  attribute selectors (the measured regression); building the index once per
  call (its construction is proportional to the total sent-row count, which
  the flat-count test forbids).
- Settings legacy cleanup (Important 3): every override entry the settings
  banner displays carries its own scope target (Global, or the specific
  Project), so the normal Global gear can clear a Project-only override, and
  with both scopes present the action clears each displayed scope it names.
  Rejected: hiding Project overrides from the Global gear (the user loses
  sight of an override that still applies).
- Failing Chromium tests: for each, bisect against `a680b9216`; fix the
  product when it regressed and the test when its expectation drifted (for
  example `TestRailSessionRowBrowser`'s one-digit count geometry after the
  rail robot icon change). No test is skipped or deleted.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)
- `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`),
  `ai-docs/manuals/shipped-surface-boundary.md`, and
  `ai-docs/manuals/config-settings-ui.md` (for `dashboard-settings*`) apply.
- Every changed UI surface gets a screenshot check against the pre-change
  rendering before merge.
- Browser tests need `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH`
  set explicitly, because fixtures redirect HOME. The full
  `internal/wsdashboard` package with Chromium took 1753s in the harness
  session's run on `247f6c724`, so it runs with `-timeout 30m`.

## Prior Decisions

- a0357fa7 (2026-11-24, commit): "replaced whole-board boardItem()/boardQ() scans in the scoped branch with an O(scope) Map of [data-id] items inside scope" — bearing: constrains
- 3a989afe (2026-11-24, commit): "T0 client-side fixes for 0.52.0 release-gate re-review Important #4, #5, #6, #7, #8, and #10" — bearing: constrains
- 8c96c4a6 (2026-11-24, commit): "remove the default bucket from the Workflow Settings agents.tier harness selector UI while keeping default in config_registry.go and wsconfig resolveAliasMapping/ResolveAgent" — bearing: constrains
- ca0597e3 (2026-11-21, commit): "Legacy tier seeds and ancestor settings affect resolution, while reset belongs to the exact persisted override the user selected." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phase 1 names dashboard-composer-status.js, dashboard-settings.js and eight existing browser tests under agents-plugin-tool/internal/wsdashboard/. |
| scope.surface | internal | Existing dashboard hydration and settings reset callers; no new externally exposed interface is specified. |
| scope.new_public_symbol | no | Phase 1 repairs existing applySent and settings cleanup; no new public symbol is required. |
| scope.new_type_contract | no | Existing /api/config/reset scope targeting; no new wire or persisted shape is specified. |
| scope.test_surface | existing | testdata/board-snap-deck-0512.browser.cjs, config_settings_browser_test.go and all eight named Go browser tests exist. |
| complexity.reuse_points | confirmed | Read applySent scoped branch (dashboard-composer-status.js:310-337), preferenceTarget (dashboard-settings.js:378-384), and syncLegacyBanner (dashboard-settings.js:1172-1225). |
| complexity.side_effect_risk | moderate | Index invalidation must follow changing sent rows; cleanup mutates persisted overrides in multiple scopes. |
| risk.correctness | high | Stale sent-state indexes or incorrect reset targets can misrepresent sent items or clear the wrong override. |
| risk.fit | moderate | Banner must expose actionable Project overrides through the ordinary Global entry; rail geometry expectations need comparison with current UI. |
| risk.test | high | Existing selector instrumentation misses document-level scans (board-snap-deck-0512.browser.cjs:879-895); reported baseline failures require focused and full-package verification. |
| risk.security_or_contract | moderate | Preserve constant-work hydration and exact-scope config reset contracts while fixing cleanup. |

## Phases

### Phase 1: Fix the dashboard findings and browser reds

- **Sent-state index** `internal/wsdashboard/assets/dashboard-composer-status.js:324`.
  Test: the budget assertion instruments document-level queries
  (`document.querySelector`, `document.querySelectorAll`) as well as board and
  gallery selectors; measured hydration issues zero document-wide sent-state
  queries, visits only the sent records of the hydrated items, and its
  operation counts stay flat across board sizes and across sent-row counts
  (for example 10 vs 3,000 sent rows). A flat count alone is not sufficient,
  because one document-wide query per item is also flat in count.
- **Settings cleanup** `internal/wsdashboard/assets/dashboard-settings.js:1186`.
  Test: opening Settings from the normal dashboard Global gear with a
  Project-only override and with Global plus Project overrides; each displayed
  override can be cleared and the stored scopes match afterward.
- **Browser reds**: `TestAgentBadgePopoverBrowser`, `TestBoardHoldPillBrowser`,
  `TestComposerExecBrowser`, `TestUserCommandBrowser`,
  `TestRailSessionRowBrowser`, `TestWarmSwitchReadsBrowser`,
  `TestWhiteboardEffectsBrowser`, plus `TestRailNavigationBrowser`, which
  times out only in the full package run. Test: each passes in a focused run
  and in the full package run.

All code paths above are relative to `agents-plugin-tool/`.

Verification: the named tests; full `go test ./internal/wsdashboard -timeout
30m` and `./internal/wswhiteboard` with Chromium; screenshots of the settings
banner (Project-only and mixed scopes) and of any surface a browser-red fix
changes.
