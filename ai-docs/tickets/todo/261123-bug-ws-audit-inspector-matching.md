---
title: "감사 검사기 일치 표시를 서버 RE2 위치로 통일 (재스윕 71e875ecf..1982d074f 수정)"
---

# Audit inspector match marks come from the server matcher (re-sweep repair)

## Background

The re-sweep over `71e875ecf..1982d074f` stamped `block` with this ticket as
its route (findings: one Critical, three Important). The prior repair
(`261123-bug-ws-audit-search-empty-budget-page`) translated the server's RE2
query to a JavaScript `RegExp` and re-implemented the seam rule in the
browser. The re-sweep shows the two engines still diverge and that the local
loop can hang:

- Critical: `assets/dashboard-audit-window.js:295` advances an empty match by
  one UTF-16 unit while the translator supplies `u` (line 219); `\p{L}*` on
  an astral character followed by a letter returns to the same index forever
  and freezes the renderer. The seam restart at line 300 has the same
  advance.
- Important 1: for head `a`, tail `b` and `ab|a`, the browser discards the
  cross-seam `ab` and shows nothing, while Go (`audit_query.go:710`) finds
  `a` on the retained head.
- Important 2: accepted Go queries still mark nothing: `\p{Greek}+` on `α`,
  `(?i:foo)` on `FOO`, `^.$` on one emoji (Go matches by rune,
  `audit_query.go:545`).
- Important 3: `internal/mcp/permission_exec_test.go:230` builds its lead
  server from the caller's environment, so under
  `WS_MCP_PERMISSION_LEVEL=read-only` the unrestricted-lead case is refused.

## Decisions

- The inspector's match marks come from the server. The event route
  (`/audit/event/<id>`) accepts optional `q`, `regex` and `case` parameters,
  validated exactly as `/audit/search` validates them, and answers with the
  match spans of every stored body it returns. Rejected: improving the
  RE2-to-JS translator, because every RE2 construct, rune-width rule and the
  seam rule would need a second implementation that the re-sweep shows keeps
  diverging; server spans make one matcher the only authority.
- Spans use the search's own matcher (`newAuditSearch`, `matchLine`,
  `seamMatcher`) extended to every non-overlapping match on a line, in the
  same order Go's `FindAll` semantics give; no span consumes text from both
  sides of a stored_trunc seam, and on a seam line candidates on the retained
  head and tail are tried as `matchLine` tries them, so the same-start
  alternative case marks `a`. An empty match advances by one rune.
- Coordinates: each span carries its line number in the server's head+tail
  numbering and start/end offsets in UTF-16 code units within that line, so
  the browser slices without conversion. The server converts from byte
  offsets. Redaction boundaries keep their existing handling; a span is
  rendered around redaction chips, never inside a mask.
- The browser removes `auTranslateRe2` and the local regex match loop. The
  inspector's find box requests spans for its current text (debounced, with
  the latest-request guard the inspector already uses for envelope fetches);
  an invalid query shows the server's error text and zero matches. Match
  counting and Enter/Shift+Enter navigation use the returned spans.
- The lead fixture in `permission_exec_test.go` sets lead authority
  explicitly (not inherited from the environment); the worker cases keep
  their explicit levels.
- Compatibility: T0. The new route parameters are optional and additive; the
  dashboard assets are embedded in the same binary that serves the route, so
  no cross-version client reads them. An older daemon ignores unknown query
  parameters.

## Constraints

- Dashboard assets follow `ai-docs/manuals/shipped-surface-boundary.md` and
  `ai-docs/manuals/performance.md`: span computation runs over the in-memory
  store view; no file I/O on the request path. Bodies are bounded by
  `dashboard.audit_body_cap_kib`.
- `internal/mcp/` follows `ai-docs/manuals/ws-mcp.md`; `internal/` follows
  `ai-docs/manuals/release-compatibility.md`.
- UI changes get a screenshot check by the lead before merge.

## Phases

### Phase 1: Server spans and fixture isolation

Implement the Decisions. Tests:

- Go: span tests paired with `/audit/search` expectations for `\p{L}*` on an
  astral character followed by a letter (terminates, correct spans),
  `\p{Greek}+`, `(?i:foo)`, `(?i)foo`, `\A`, `^.$` on an emoji, and on a seam
  line the same-start alternative (`ab|a` with head `a`, tail `b`), a greedy
  match stopping at the seam, and an assertion reading across the seam.
- Frontend: the inspector renders server spans (Node suite with stubbed
  spans, including UTF-16 offsets after an astral character and a span next
  to a redaction chip); opening an inspector for the Unicode case completes.
- Browser: Phase 3 audit browser test marks a `(?i:...)` and a `\p{Greek}`
  query end to end against the real server.
- `permission_exec_test.go` passes with `WS_MCP_PERMISSION_LEVEL=read-only`
  set in the caller's environment.

Run the combined Node audit suites, the audit-view browser tests (also under
`-race`), `TestCSSClassesDoNotCollideAcrossModules`, the targeted MCP exec
permission tests, and `GOOS=windows go vet` for the touched packages.
