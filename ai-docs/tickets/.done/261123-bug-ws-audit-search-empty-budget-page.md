---
title: "감사 검색 빈 예산 페이지가 이후 결과를 숨김 (스윕 d4fd2f54f..7edda8616 수정 묶음)"
completed: 2026-11-23
---

# Audit search empty budget page hides later matches (sweep repair batch)

## Background

The range sweep over `d4fd2f54f..7edda8616` stamped `block` with this
ticket as its route. Its consolidated findings (one Critical, five
Important, one Minor) are the contract for this repair; each finding names
its file, line, failure, and the test it needs. The lead routes the batch
as one bounded repair.

## Decisions

- Critical: the audit search UI keeps More available while the server
  returns a continuation cursor, even when the accumulated hit list is
  empty; "No matches" shows only once the cursor is exhausted
  (`dashboard-audit-search.js:425`, server budget at
  `audit_query.go:643`/`:668`). Test: a zero-hit page followed by a page
  with an older hit.
- Important 1: the audit-rows Node fake DOM gains the `style` surface the
  shard-reds change uses (`frontend-audit-rows.test.js:63`); the combined
  Node audit suites pass again.
- Important 2: the inspector keeps the server's head+tail line numbering
  but refuses a match that spans the omitted seam, matching
  `audit_query.go:704`; the frontend test that expects a cross-seam match
  is corrected.
- Important 3: the inspector uses spans consistent with the server's RE2
  matching (server-provided spans, or a translation that covers the Go
  syntax the server accepts); tested with `(?i)foo` and a start anchor.
- Important 4: `settings-root.browser.cjs` gains an assertion pinning the
  audit controls under denied storage.
- Important 5: the HUD peek tracks the current trigger on every open, so
  closing a peek opened from `+N` restores focus to `+N`; tested with the
  visible-chip then folded-chip sequence.
- Minor: `261117-feat-ws-exec-tools` is closed, so its decision is not
  edited; the cross-reference lives in this ticket's Result instead.
- Also in this batch (lead-queued, test-only): the `-race` data race on
  `Daemon.auditRead` in the audit-view browser tests (the tests assign it
  after the daemon serves).
- Compatibility: T0 (dashboard assets and tests only).

## Constraints

- Dashboard assets follow `ai-docs/manuals/shipped-surface-boundary.md`
  and `ai-docs/manuals/performance.md`.
- UI changes get a screenshot check by the lead before merge.

## Phases

### Phase 1: Repair the sweep findings

Fix every Decision above with its named test; run the combined Node audit
suites, the audit and HUD browser tests, `TestCSSClassesDoNotCollideAcrossModules`,
and the audit-view browser tests under `-race`.

### Result (4b39d94e3) - 2026-11-23

Merged in 286b595c1. Every Decision is fixed with its named test.

- Critical: "No matches" shows only when the hit list is empty and the
  cursor is exhausted; an empty page with a cursor reads "0+ hits" and keeps
  Load more, and Retry on an empty list continues the cursor. The Node suite
  and the Phase 3 browser test cover a zero-hit page followed by an older hit.
- Important 1: the audit-rows fake DOM gained `style.setProperty` and
  `offsetHeight`; the combined Node audit suites pass 38/38.
- Important 2: the inspector keeps head+tail numbering and rejects a match
  whose range contains the seam (whole-line matching, so anchors see the real
  line); it approximates the server's seam matcher and can differ only for
  exotic assertions on the seam line. The cross-seam test now expects none.
- Important 3: an RE2-to-JS translator (`auTranslateRe2`) replaces the raw
  `RegExp` recompile; server spans were rejected because the search response
  carries no spans and adding them is a backend API change. Unsupported
  syntax (`(?U)`, mid-pattern flags, `\C`, negated POSIX classes) highlights
  nothing while the hit and line still open. Tests cover `(?i)foo`, `\A`,
  and `(?-i)`.
- Important 4: `settings-root.browser.cjs` asserts the audit filter bar and a
  kind chip toggle under both storage-denial modes.
- Important 5: `xhOpenPeek` sets the origin on every open; `xhAskStop` keeps
  its guard so Stop inside a peek remembers the opening chip. The new
  FOCUS-MORE browser scenario fails against the old line.
- `-race`: the audit-view browser tests pass the fixture through a new
  unexported `DaemonOptions.auditRead` test seam set before the daemon
  serves; `-race` reports no data race.
- Sweep Important 6 (CHANGELOG claimed live audit capture) was fixed by the
  lead in fcecba92a, outside this branch.
- Minor: `261117-feat-ws-exec-tools` Decision 4 (command-content judgment
  goes to the planned intent filter) is narrowed for read-only workers by the
  owner-approved admission exception in
  `261123-bug-ws-exec-cutover-worker-authority`; a reader starting at the
  earlier ticket should treat that exception as the current contract.
- Verification on develop after merge: the Settings browser tests that the
  worker's worktree could not run (missing generated assets), the audit-view
  browser tests, `TestCSSClassesDoNotCollideAcrossModules`, and the Node
  audit suites all pass. Lead screenshot check passed.
