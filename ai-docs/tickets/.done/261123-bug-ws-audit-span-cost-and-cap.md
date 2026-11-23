---
title: "감사 검사기 일치 위치 계산 비용·상한·UTF-8 경계 수정 (재스윕 71e875ecf..dbc990805 수정)"
related:
  261123-bug-ws-audit-inspector-matching: "introduced the server spans this ticket repairs"
completed: 2026-11-23
---

# Audit inspector span cost, cap and UTF-8 boundary (re-sweep repair)

## Background

The re-sweep over `71e875ecf..dbc990805` stamped `block` with this ticket as
its route (findings: one Critical, three Important; the fourth Important is
ticket housekeeping the lead fixes in the commit that files this ticket).
The server spans that `261123-bug-ws-audit-inspector-matching` added have
three defects:

- Critical: `agents-plugin-tool/internal/wsdashboard/audit_query.go:822`
  materializes every match in a retained window before checking the span
  limit. For a word-boundary query such as `\b\w`, a word character before
  the window makes the synthetic left sentinel look like a match; the
  fallback accepts one real match and restarts, scanning the suffix again.
  Measured: about 13 ms for 500 words, 50 ms for 1,000, 210 ms for 2,000 and
  6.56 s for 10,000 on one roughly 20 KiB retained line. The default body cap
  is 1 MiB and spans are computed synchronously on the request.
- Important 1: `audit_query.go:1002` applies the 10,000-span cap to raw regex
  matches and only then drops empty matches. 10,001 digits followed by a
  letter with `\p{L}*` returns zero spans and `capped:true`.
- Important 2: `agents-plugin-tool/internal/wsdashboard/audit_routes.go:317`
  passes the raw 8 MiB implicit cutoff to a helper that rounds forward to a
  UTF-8 boundary while the envelope clips backward. An emoji crossing the
  cutoff gives an envelope prefix of 8,388,607 bytes and a span prefix of
  8,388,611 bytes, so a span lies outside the returned text.

## Decisions

- Span matching does linear-bounded work per line: a restart never rescans a
  suffix already scanned, and matching stops as soon as the visible-span cap
  is reached (no materializing all matches first). The seam rule is kept: no
  span consumes text from both sides of a stored_trunc seam, assertions at
  the seam read the real neighbouring text as today, and spans stay
  identical to the current results for every case the existing agreement
  tests cover. Rejected: a per-request time budget, because it would truncate
  ordinary queries nondeterministically instead of removing the repeated
  work.
- The visible-span cap counts only nonempty spans; empty matches advance by
  one rune and never count. `capped:true` means at least 10,000 visible spans
  exist. Rejected: a separate cap for empty matches, because the bounded scan
  above already limits the work and a second limit adds a second `capped`
  meaning.
- When the caller omits `through_*`, spans are computed over exactly the
  envelope's returned prefix (the same backward-clipped UTF-8 boundary), so
  every span lies inside the returned text. Explicit `through_*` values keep
  their current meaning.
- Compatibility: T0. No route parameter or response field changes; the
  dashboard assets are embedded in the same binary.

## Constraints

- `agents-plugin-tool/` follows `ai-docs/manuals/performance.md` and
  `ai-docs/manuals/shipped-surface-boundary.md`; `internal/` follows
  `ai-docs/manuals/release-compatibility.md`. Span computation stays over the
  in-memory view; no file I/O on the request path.
- `audit_query.go` is already over the 1000-line size advisory (1063); do not
  split it here (the split question is with the owner). Keep the change
  surgical; a new helper may go in a new file if it keeps responsibility
  clean.
- No UI change is expected; if the inspector rendering changes, the lead
  takes a screenshot check before merge.

## Phases

### Phase 1: Bounded spans, nonempty cap, envelope-aligned prefix

Implement the Decisions. Tests:

- Go: many boundary matches on a retained side of a seam line (for example
  `\b\w` over 10,000 words with a word character before the window) returns
  the same spans as an unbounded reference and completes within a small
  bound; a timing assertion with generous headroom or an operation-count
  probe, not a tight wall-clock threshold.
- Go: 10,001 digits followed by a letter with `\p{L}*` returns the letter's
  span and `capped:false`; 10,000 visible spans still report `capped:true`.
- Go: an event whose body has a multi-byte rune crossing the implicit 8 MiB
  cutoff returns no span beyond the envelope's prefix, and a span ending just
  before the rune is kept.
- The existing span/search agreement tests keep passing unchanged.

Run the targeted wsdashboard audit Go tests (also under `-race`), the
combined Node audit suites, the audit-view browser tests, and
`GOOS=windows go vet ./internal/wsdashboard/`.

### Result (84b3a0db8) - 2026-11-23

Landed in `agents-plugin-tool/internal/wsdashboard/`:

- `audit_span_scan.go` (new) replaces `seamMatcher.all`, `findAll` and `lineMatches`: `eachLineMatch` streams matches to a callback. A window is copied once; a resume reads the same text from the character before the resume point (its real context, a substring, no copy), so a resume costs the distance to the next match. Matches are read in batches of 64. A match that touches a sentinel is replaced by the first match by shape (`shapeFirst`: an unbounded form that stops at the match, the bounded form only when the match reaches the right sentinel). Assertion-free patterns resume on the rest of the window without context. Scanning ends when `bodySpans`' callback stops it.
- `audit_query.go`: `bodySpans` skips empty matches (they never count) and marks `capped` when a visible span beyond `auditSpansMax` exists.
- `audit_routes.go`: the implicit `through_*` is `len(headUTF8(body, auditBodyMax))`, the envelope's own backward clip.
- Tests (`audit_spans_test.go`): boundary matches on a retained side (9,000 and 10,500 words, spans checked against constructed offsets, 3 s bound); 10,001 digits plus a letter with `\p{L}*` (and the same across a seam, and empties between visible spans at the cap); a character crossing the 8 MiB cutoff (`end` kept, the emoji and what follows absent, `through` = envelope prefix); empty match at a seam window's end.

Decisions / deviations:

- `capped` stays "a further visible span exists beyond 10,000", not "at least 10,000": the existing `TestAuditEventSpansAreCapped` pins exactly 10,000 spans as uncapped and had to pass unchanged. The Decisions' wording "at least 10,000 visible spans exist" should be read that way (answer holds 10,000 spans and more exist).
- The old scan panicked (`slice bounds out of range`) on an empty match at the end of a seam window (`\w*`, `b*`, `$`, `\b` with a seam); the rewrite does not, and a test pins it.
- Route Facts were absent from the ticket; implemented on the lead's explicit instruction.

Verification:

- The new tests fail on the previous code (8.9 s and 13.7 s stalls, a slice-bounds panic) and pass now.
- A scratch differential test (not committed) compared the new scan with the old seam matcher over 143,068 random line/seam/pattern cases: identical nonempty spans (the old code panicked on 6,932 of them).
- `go test -run Audit ./internal/wsdashboard/` ok; `-race` on the span/search/seam tests ok; `TestAuditViewBrowser`, `Phase2Browser`, `Phase3Browser` pass; `frontend-audit*.test.js` (11, 19, 11 pass, 0 fail); `GOOS=windows go vet ./internal/wsdashboard/` clean.
- Lite review: no Critical or Important findings; three minor observations (capped wording, unexercised bounded `shapeFirst` under a long run, generous timing bound).
