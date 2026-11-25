---
title: "대시보드 컴포저 들여쓰기 유지 및 인라인 마크다운 하이라이트"
related:
  261125-feat-composer-long-paste-textref: sibling composer work (exclude [Text #N] from markdown link highlight)
  261125-feat-composer-mailbox-badge: sibling WsCapsules work (avoid conflicting capsules.js rewrites)
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 3d21cea2fdff9be8
sage-review-completeness-reviewed: 3d21cea2fdff9be8
completed: 2026-11-25
---

# Dashboard Composer Indentation Preservation and Cosmetic Inline Markdown Highlighting

## Background

The dashboard composer (`#ctext` backed by `WsCapsules` `.wsc-editor`) already supports `Tab`/`Shift+Tab` two-space indentation, inline capsules (`skill`, `topic`, `answer`, etc.), and `@` file mentions. However, authoring structured multi-line messages has two friction points:

1. Pressing `Enter` / `Shift+Enter` inserts a raw `\n` without preserving the current line's leading indentation or continuing markdown lists/blockquotes, `Tab` on a single-line list item inserts spaces at the caret instead of nesting the line, and `inlineSend()` calls `text.trim()` which strips intentional leading spaces on line 1.
2. Plain markdown syntax inside the composer has no visual hierarchy, and the legacy `#chl` mention highlight mirror is disabled whenever `WsCapsules` is active (so `@` mentions also lose visual highlighting).

## Decisions

1. **Cosmetic inline markdown via CSS Custom Highlight API (`CSS.highlights` + `::highlight(...)`)** — confirmed `(01)=a`.
   - Keep `.wsc-editor`'s flat DOM (`Text` nodes + `contenteditable="false"` `.wsc-capsule` spans) completely untouched so Korean/CJK IME composition, browser `execCommand` Undo history, and variable-width capsule wrapping remain intact with zero DOM churn or forced layout reads.
   - Style inline markdown tokens using `::highlight(...)` rules backed by canonical `--wb-*` tokens (`color`, `background-color`, `text-decoration` only):
     - Dimmed syntax markers (`**`, `*`, `_`, `~~`, `` ` ``, `#` heading markers, `>` quote markers, list bullets/numbers).
     - Inline code spans (`` `code` ``) with subtle tonal background wash.
     - Headings (`#`..`######`), blockquotes (`>`), list markers, links (`[label](url)`), bold/italic content tint, and strikethrough (`~~text~~`).
     - Restore `@` file-mention highlighting (from active `mentions[]` ranges) via the same `CSS.highlights` pipeline.
   - Exclude attachment/reference markers (`[Image #N]`, `[Text #N]`) so they are never mis-highlighted as markdown links.
   - Pause highlight refreshes while IME composition (`composing`) is active; coalesce updates via `requestAnimationFrame` on `input`, `compositionend`, draft restore, and programmatic value changes.
   - Gracefully no-op when `CSS.highlights` or `Highlight` is unsupported.
   - *Rejected:* wrapping tokens in `<span>` nodes inside `.wsc-editor` (breaks Korean IME composition and `execCommand` Undo stack, adds keystroke DOM mutation); unhiding `#chl` backdrop mirror behind transparent `.wsc-editor` (desyncs with variable-width `.wsc-capsule` pills and font metrics); replacing `.wsc-editor` with CodeMirror 6 (heavy dependency and full rewrite of all `WsCapsules` kinds and pickers); preview-tab-only markdown without inline styling.

2. **Newline auto-indent and markdown list/quote continuation** — confirmed `(02)=a`.
   - On non-send newline (`Enter` in `"mod"` mode, `Shift+Enter` or `Alt+Enter` in `"enter"` mode), strictly after popup consumption, `!` bang-mode check, and the `e.isComposing || e.keyCode === 229` IME guard:
     - Read the current line prefix up to the caret: leading indentation (`^[ \t]+`) plus optional markdown marker:
       - Unordered list: `[-*+] `
       - Task list: `[-*+] \[[ xX]\] ` (continues as unchecked `- [ ] ` preserving bullet char)
       - Ordered list: `(\d+)\. ` (continues as `(N+1). `)
       - Blockquote: `(?:>[ \t]?)+` (preserves quote prefix)
     - **Empty-marker exit:** if the current line contains only optional leading indent + a recognized list/quote marker (and optional trailing spaces, no body text) with a collapsed caret at or after the marker, replace that line's prefix with empty text (exiting the list/quote cleanly) instead of inserting another marker.
     - Otherwise insert `"\n" + indent + nextMarker` through `editText()` (`execCommand("insertText")`) so `Ctrl+Z`/`Cmd+Z` cleanly undoes the auto-inserted indent/marker.
   - **Preserve line-1 leading indent on send, end to end** — confirmed `(10)=b`. Two coordinated changes:
     - Frontend: replace `text.trim()` in `inlineSend()` and the matching `sendText().trim()` fallback in `sendMessage` with trimming only leading blank lines and trailing whitespace/newlines (e.g., `text.replace(/^(?:[ \t]*\r?\n)+|\s+$/g, "")`), while still rejecting whitespace-only messages.
     - Server: `wsinbox.Normalize` (`agents-plugin-tool/internal/wsinbox/normalize.go`) currently runs `strings.TrimSpace` on every part, which would strip the indent after the send route builds `wsinbox.Entry{Parts: []string{req.Text}}`. When `e.Operator` is true, per part: if `strings.TrimSpace(p) == ""` drop the part (whitespace-only input still yields `ErrEmpty`); otherwise strip leading blank lines (lines of only horizontal whitespace ending in `\n` or `\r\n`) and trailing whitespace (`strings.TrimRightFunc(p, unicode.IsSpace)`), preserving line-1 leading spaces/tabs. Non-operator parts keep `strings.TrimSpace(p)` unchanged, so mail, MCP, and board producers are unaffected. `Entry.bodyText()` (`wsinbox/inbox.go`, the shared source of `Entry.Text()` and so the delivered `<ws:user>` body) re-trims each part with `strings.TrimSpace`; it applies the same operator rule, so implement the rule once as a small unexported helper used by both `Normalize` and `bodyText()`. `Operator` is the send route's `local` flag, so a LAN viewer's parts keep `TrimSpace` and their line-1 indent is still stripped (expected, not a bug). Also check that `ack_check` (which compares `Parts[0]` as typed) still classifies an indented ack-only message correctly.
   - *Rejected:* preserving raw leading whitespace only without markdown list/quote continuation; (10)(a) dropping line-1 indent preservation to stay frontend-only (contradicts the owner's confirmed Decision 2); (10)(c) relaxing the trim for all parts (blast radius across mail/MCP/board producers).

3. **Smart `Tab` list nesting on single-line cursor** — confirmed `(03)=ok`.
   - Extend `indentLines(false)` in `dashboard-composer-keyboard.js`: when the selection is collapsed on a single line and either the line begins with a markdown list/quote marker (`^[ \t]*(?:[-*+] |\d+\. |> )`) or the caret is inside the line's leading whitespace, insert 2 spaces at the start of the line and advance the caret by 2 (nesting the list item).
   - Keep existing single-line mid-prose `Tab` behavior (insert 2 spaces at caret) on non-list lines past leading whitespace, and keep `Shift+Tab` outdenting up to 2 leading spaces.

4. **Compatibility tier: T0 additive (frontend plus an internal Go `wsinbox.Normalize` operator-part trim relaxation, with a Go unit test in `internal/wsinbox`).**
   - No MCP tool, wire protocol, config schema, inbox JSON field, or draft storage schema changes; only the stored text of an operator send may now begin with whitespace on line 1.

## Constraints

- **Highlight coordinate model:** range mapping goes through canonical value offsets (the same coordinates as `ctext.value` and `mentions[]`), not through concatenated `Text` nodes alone. The editor's newlines come from `<br>` and `DIV`/`P` boundaries and capsules contribute marker text (`capsules.js` `read()`), so the implementer maps value offsets to DOM points over the editor DOM (using `ctext.wsc.ordered()` rows or a small exported mapper) and skips `.wsc-capsule` subtrees when creating `Range`s. Line-anchored syntax (headings, quotes, list markers) anchors on value-offset line starts.
- **Reuse the `ws-bang` highlight precedent:** `dashboard-composer-exec.js` already feature-detects `CSS.highlights` and repaints from `paintMentions()` (called on `input`, `shiftMentions`, `clearMentions`), with `::highlight(ws-bang)` in `dashboard-conversation.css`. Hook the markdown repaint into that same path (covers draft restore and post-send clear) without touching `capsules.js`, and suppress or lower the `ws-md-*` priority on a `!` shell line so they do not overlap `ws-bang`.
- **Token precedence and fenced code:** inline code spans and fenced ```` ``` ```` blocks suppress all inner markdown parsing (fence lines and fenced bodies get only a marker/code tint, no other group). Otherwise tokens may nest (bold in heading, mention in quote); `CSS.highlights` priority orders overlaps, with code highest and markers lowest.
- **Mention highlight coordinates:** `mentions[]` ranges are canonical value offsets (verify in `dashboard-composer-pickers.js` `trackMentions`); convert them through the same mapper as the markdown ranges.
- **Existing test contract changes:** `testdata/composer-multiline.test.cjs` asserts Shift+Enter in `enter` mode and plain Enter in `mod` mode are not `defaultPrevented`; the new interception prevents default and inserts through `editText()` (compatible with the `capsules.js` Enter handler, which honors a prevented forwarded event), so those assertions are rewritten. That test slices the keyboard block by string markers, so any helper placed outside the sliced region must be added to its harness context.
- **Tab caret and fallback trim:** after `editText(ls, ls, "  ")` the caret must be set explicitly with `setSelectionRange` to the original caret + 2 (as `indentLines` already does for the multi-line case). The non-inline fallback `sendText().trim()` in `sendMessage` uses the same trim as `inlineSend()` (Decision 2).
- **Attachment-marker exclusion is a guard:** `[Image #N]` / `[Text #N]` are plain text and the link grammar is `[label](url)` only, so the exclusion is a cheap guard; test it with literal strings, independent of `261125-feat-composer-long-paste-textref` landing.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, agents-plugin-tool/internal/wswhiteboard/assets/, ai-docs/assets/mockups/)
- **Applicable manuals (from `AGENTS.md` `### Implementation Conventions`):**
  - `ai-docs/manuals/dashboard-style.md` — all `::highlight(...)` colors and washes must derive from canonical `--wb-*` tokens and `color-mix(in srgb, ...)` for both light and dark themes; no stark `#fff` on dark or garish neon accents.
  - `ai-docs/manuals/performance.md` — Frontend Minimal Updates: zero idle frame scheduling, zero extra DOM nodes in `.wsc-editor`, zero forced layout reads on keystrokes, rAF-coalesced highlight recalculation.
  - `ai-docs/manuals/release-compatibility.md` — T0: frontend plus an internal `wsinbox.Normalize` operator-part trim relaxation; no wire or schema change.
  - `ai-docs/manuals/shipped-surface-boundary.md` — no repo-specific text in shipped assets.
- **CSS `:has()` guard:** `frontend-idle.test.js` forbids high-cost `:has()` selectors (sibling `+`/`~`, `:empty` inside `:has()`, and high-ancestor `:root`/`html`/`body`/`.dash`/`.dbody`/`.dwrap`/`.dgrid`/`#bcol`) in dashboard CSS while permitting local component `:has()` (`agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js#L162-L287`).
- **Minimal `capsules.js` churn:** attach highlight updates via existing `#ctext` / `.wsc-editor` listeners (and the `paintMentions()` path) and build `Range`s over `.wsc-editor` `Text` nodes via the value-offset mapper above (skipping `.wsc-capsule` subtrees) so edits stay isolated from sibling `261125-feat-composer-mailbox-badge` work. Do not alter paste handling (`261125-feat-composer-long-paste-textref`).
- **Korean/CJK IME safety:** never mutate DOM or selection during active composition; auto-indent runs only after `isComposing || keyCode === 229` guards.

## Prior Decisions

- 261115-feat-ws-composer-inline-skill-and-topic-capsules (2026-11-15, ticket Result): "Implemented compact atomic skill/topic capsules over the existing textarea owners, with caret-local fuzzy pickers, whole-capsule deletion, canonical text serialization, IME/keyboard ownership" — bearing: constrains
- 261116-bug-ws-dashboard-composer-autosize-forced-layout (2026-11-16, ticket Decisions): "capsules.js is now under wsdashboard/assets/. Its native editor input stops propagation and forwards once to the backing field; retain that notification contract rather than removing the composer's only input." — bearing: constrains
- 31042a2b (2026-11-21, commit): "The fragments preserve the original source byte-for-byte when exactly one new header comment per fragment is stripped; each fragment is at most 500 lines. Ordered lists mirror the source sequence." — bearing: constrains
- 11caef15 (2026-11-22, commit): "A LAN viewer typing `!` fails closed (route refused, draft kept, hint to start with a space) rather than sending a command line as a prompt." — bearing: constrains
- c4a488d6 (2026-11-21, commit): "Kept the existing inbox JSON fields and wsinbox block formats for T0 wire compatibility; inline anchors preserve prose order." — bearing: supports
- 0f4add05 (2026-11-07, commit): "User board items (69)-(74): [Image #N] markers at the cursor, trailing 'Image #N: <path>' lines replacing [image: path] (old form still parsed), two-way marker/chip sync without renumbering; Tab=2-space indent, Shift+Tab outdent" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-keyboard.js, dashboard-composer-delivery.js, dashboard-composer-highlight.js, dashboard-conversation.css, agents-plugin-tool/internal/wsdashboard/pages.go, agents-plugin-tool/internal/wsinbox/normalize.go, agents-plugin-tool/internal/wsinbox/inbox.go |
| scope.surface | internal | internal/wsdashboard frontend JS/CSS fragments, unexported dashboardScriptFiles slice in pages.go, and the exported-but-internal-package wsinbox.Normalize behavior for operator parts; no new exported Go symbol, MCP tool, or wire-schema change (T0) |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js, testdata/composer-multiline.test.cjs, testdata/composer-inline.test.cjs, testdata/composer-outbox-resilience.test.cjs, testdata/capsules.test.cjs, agents-plugin-tool/internal/wsinbox/*_test.go (e.g. inbox_test.go), plus new unit tests and Playwright visual check |
| complexity.reuse_points | confirmed | reuses indentLines and keydown/IME/popup guards (dashboard-composer-keyboard.js#L6-L81), editText/editField (dashboard-composer-images.js#L61-L74), inlineSend (dashboard-composer-delivery.js#L11-L38), mentions[] and paintMentions (dashboard-composer-pickers.js#L13-L54), and dashboardScriptFiles (pages.go#L35-L81) |
| complexity.side_effect_risk | moderate | modifies shared composer keydown/Enter/Tab interception, inlineSend trimming, and .wsc-editor CSS.highlights rendering; interacts with IME composition, undo stack, ! bang mode, autoMultiline, and sibling composer tickets |
| risk.correctness | moderate | edge cases across CJK IME isComposing/keyCode 229, empty-marker exit vs selection, ordered-list numbering, capsule-spanning DOM Range mapping for CSS.highlights, fenced/inline code precedence, and inlineSend vs sendText fallback trimming |
| risk.fit | moderate | must register new fragment in pages.go dashboardScriptFiles within the 500-line cap, preserve zero-churn .wsc-editor DOM contract, stay isolated from sibling 261125-feat-composer-mailbox-badge and 261125-feat-composer-long-paste-textref edits, and satisfy dashboard-style.md light/dark tokens and frontend-idle.test.js :has() rules |
| risk.test | moderate | requires Node unit tests for auto-indent/list continuation/Tab/inlineSend and markdown scanner/DOM-range mapping, frontend-idle.test.js, go test ./internal/wsdashboard/... ./internal/wsinbox/..., and headless Playwright light/dark visual verification |
| risk.security_or_contract | low | cosmetic CSS.highlights over existing Text nodes (no innerHTML/DOM injection); operator-only Normalize trim relaxation keeps line-1 leading indent in the stored text with no schema or protocol change, non-operator parts unchanged |

## Phases

### Phase 1: Composer auto-indent, smart Tab nesting, and CSS.highlights inline markdown

Implement the three confirmed behaviors in the dashboard composer assets:

1. **Auto-indent & list/quote continuation (`dashboard-composer-keyboard.js`, `dashboard-composer-delivery.js`):**
   - Intercept non-send `Enter` / `Shift+Enter` / `Alt+Enter` (preserving existing `autoMultiline` flip, `!` bang mode, popup priority, and IME guards) to insert `\n` + preserved indent + next list/quote marker via `editText()`, or clear bare empty markers to exit the list/quote.
   - Update single-line `Tab` in `indentLines(false)` to indent the line start when on a list/quote line or inside leading whitespace.
   - Update `inlineSend()` and the `sendMessage` `sendText().trim()` fallback to preserve intentional leading indentation on line 1 while still stripping leading blank lines and trailing whitespace and blocking whitespace-only sends.
   - Relax `wsinbox.Normalize` (`agents-plugin-tool/internal/wsinbox/normalize.go`) and `Entry.bodyText()` (`agents-plugin-tool/internal/wsinbox/inbox.go`) for operator parts per Decision 2 so the indent survives storage and delivery; non-operator parts keep `strings.TrimSpace`.

2. **Cosmetic inline markdown & `@` mention highlighting (`dashboard-composer-highlight.js`, `dashboard-conversation.css`, `pages.go` `dashboardScriptFiles` (`agents-plugin-tool/internal/wsdashboard/pages.go#L35-L81`)):**
   - Add a pure, unit-testable inline markdown tokenizer/scanner over canonical composer text (respecting fenced code blocks or inline backticks so syntax inside inline code isn't double-parsed, skipping `[Image #N]` / `[Text #N]`, and mapping canonical value-offset ranges onto `.wsc-editor` `Text` nodes while skipping `.wsc-capsule` spans, per the Constraints coordinate model).
   - Register highlight groups in `CSS.highlights` (`ws-md-marker`, `ws-md-code`, `ws-md-head`, `ws-md-quote`, `ws-md-em`, `ws-md-strong`, `ws-md-strike`, `ws-md-link`, `ws-md-mention`) with rAF coalescing, paused during `composing`, cleared/rebuilt on session switch and draft restore, and safely no-oping when `CSS.highlights` is absent.
   - Add light/dark token-derived `::highlight(...)` styles in `dashboard-conversation.css` without `:has()`.

3. **Verification:**
   - Unit tests (Node JS test suite under `agents-plugin-tool/internal/wsdashboard/`) covering:
     - Enter auto-indent (spaces/tabs), unordered/ordered/task/blockquote continuation, ordered numbering increment, empty-marker exit, IME `isComposing`/`keyCode === 229` skip, `Tab` list-line nesting vs mid-prose Tab, and `inlineSend()` line-1 leading indent preservation.
     - Inline markdown scanner ranges (headings, bold, italic, strikethrough, inline code, links, blockquotes, list markers, `@` mentions, exclusion of `[Image #1]`/`[Text #1]`, range mapping around `.wsc-capsule` spans, IME pause/resume, and missing-`CSS.highlights` no-op).
     - Go unit test in `internal/wsinbox` for `Normalize`: operator part keeps line-1 leading spaces/tabs, strips leading blank lines (`\n` and `\r\n`) and trailing whitespace, whitespace-only operator part yields `ErrEmpty`, non-operator part still `TrimSpace`d; `Entry{Operator: true, Parts: []string{"  - a"}}.Text()` keeps the indent inside the `<ws:user>` body while a non-operator entry is unchanged (LAN viewer parts stay trimmed).
     - Existing `frontend-idle.test.js` (`:has()` and idle-frame guards), `go test ./internal/wsdashboard/...`, and `go test ./internal/wsinbox/...`.
   - Headless Playwright visual check of the composer in both dark and light themes confirming markdown highlights, `@` mentions, and capsules render cleanly with zero console errors and zero layout shift.

### Result (00d1c532) - 2026-11-25

- **Landed:**
  - **Auto-indent, list/quote continuation, and smart single-line Tab (`dashboard-composer-keyboard.js`):** Non-send `Enter`/`Shift+Enter`/`Alt+Enter` (after popup, `!` bang-mode, `autoMultiline`, and `isComposing || keyCode === 229` IME guards) preserves leading indentation and continues unordered (`- `, `* `, `+ `), task (`- [ ] `), ordered (`(N+1). `), and blockquote (`(?:>[ \t]?)+`) lines via `editText()`, and exits empty-marker lines cleanly when caret is collapsed. `indentLines(false)` nests list/quote lines and leading-whitespace carets with explicit `setSelectionRange(a + 2, a + 2)` while preserving mid-prose `Tab` and `Shift+Tab`.
  - **End-to-end line-1 leading indent preservation (`dashboard-composer-delivery.js`, `internal/wsinbox/normalize.go`, `internal/wsinbox/inbox.go`):** `inlineSend()` and the `sendMessage()` fallback replace `.trim()` with `.replace(/^(?:[ \t]*\r?\n)+|\s+$/g, "")`; Go `wsinbox.Normalize` and `Entry.bodyText()` share an unexported `trimPart(p string, operator bool)` helper preserving line-1 leading spaces/tabs for `Operator: true` while stripping leading blank lines and trailing whitespace (whitespace-only still yields `ErrEmpty`), leaving non-operator parts `strings.TrimSpace`d. Verified `ack_check.go` (`IsAnswerCheckNudge`) only matches daemon-generated non-operator nudges.
  - **Cosmetic inline markdown & `@` mention highlighting (`dashboard-composer-highlight.js`, `dashboard-composer-pickers.js`, `dashboard-conversation.css`, `pages.go`):** Added `scanComposerMarkdown`, `buildEditorTextMap`, and `valueSpanToDomRanges` (skipping `.wsc-capsule` subtrees, zero DOM mutations, zero layout reads), registering 9 `CSS.highlights` groups (`ws-md-marker`, `ws-md-head`, `ws-md-quote`, `ws-md-em`, `ws-md-strong`, `ws-md-strike`, `ws-md-link`, `ws-md-mention`, `ws-md-code`) hooked from `paintMentions()` with rAF coalescing, IME composition pause/resume, `!` bang-mode clearing, and graceful no-op when `CSS.highlights` is absent. Added canonical `--wb-*` token `::highlight(ws-md-*)` rules in `dashboard-conversation.css` without `:has()`.
  - **Tests (`composer-indent-highlight.test.cjs`, `composer-multiline.test.cjs`, `internal/wsinbox/inbox_test.go`):** Added Node unit tests for auto-indent/continuation/exit/IME/bang, Smart Tab, `inlineSend`/`sendMessage` indent preservation, markdown scanner, capsule-aware DOM offset mapping, rAF coalescing, and CSS token rules; updated `composer-multiline.test.cjs` for `defaultPrevented` on intercepted newlines; added `TestNormalizeAndTextPreserveOperatorLineOneIndent`.
- **Verification:**
  - `node --test internal/wsdashboard/testdata/composer-indent-highlight.test.cjs internal/wsdashboard/testdata/composer-multiline.test.cjs internal/wsdashboard/frontend-idle.test.js`: 29/29 pass.
  - `node internal/wsdashboard/testdata/composer-inline.test.cjs`, `composer-outbox-resilience.test.cjs`, `capsules.test.cjs`: all pass.
  - `go test ./internal/wsinbox/...`: pass.
  - `go test -v -run 'TestSelectionComposer' ./internal/wsdashboard/...`: 5/6 pass (`TestSelectionComposerSendSnapshotsAndCancel` fails pre-existing on `develop` due to `4dce5d7fa` adding `convoVisible` to `pinConvo` without adding it to `selection_composer_test.go`; `TestSessionPageOffersSystemOpenToLocalViewersOnly` also fails pre-existing unrelated to this change).
  - Headless Chromium Playwright visual check (light & dark themes): all 9 `ws-md-*` highlight groups active with expected priorities, zero console/page errors, zero DOM mutations, zero layout shift, clean `!` bang-mode clear/restore.
  - `code-review-lite` (`id:breechinglyricistplacidlyhuntsman`): clean, no remaining findings.
- **Decisions:**
  - Kept `MD_HIGHLIGHT_GROUPS` on a single line in `dashboard-composer-highlight.js` so `selection_composer_test.go`'s `var` extractor parses it without syntax errors.
  - Used `--wb-ok` mixed with `--wb-fg` for `::highlight(ws-md-em)` instead of `--wb-ph-reply` (which is a dark fill token in dark theme).
