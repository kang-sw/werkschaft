---
title: "입력창 풀스펙 마크다운 실시간 렌더링 지원 (제목 크기·볼드, 코드 블록 폰트 등)"
related:
  261126-feat-ws-unified-composer-input-component: shared input component adopting this markdown renderer
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 1b220b39af3d003f
sage-review-completeness-reviewed: 1b220b39af3d003f
completed: 2026-11-26
---

# Composer Full-Spec Live Markdown Rendering

## Background

The dashboard chat composer (`#ctext`) already runs on a `contenteditable="true"` editor surface (`.wsc-editor`, managed by `WsCapsules` with flat DOM `Text` nodes and atomic `contenteditable="false"` capsule spans) rather than a plain `<textarea>`. Its current Markdown styling (`dashboard-composer-highlight.js`) uses the browser CSS Custom Highlight API (`CSS.highlights` + `::highlight(ws-md-*)`).

Because the CSS Custom Highlight API specification limits `::highlight()` pseudo-elements to paint and decoration properties (`color`, `background-color`, `text-decoration`), browsers ignore layout-affecting typography rules (`font-size`, `font-weight`, `font-style`, `font-family`). As a result:

- Headings (`#` through `######`) only tint text color; they cannot render bold or scale font size.
- Fenced code blocks and inline code only tint background; they cannot switch to the monospace font (`var(--wb-mono)`).
- Several CommonMark/GFM constructs (horizontal rules, Setext headings, indented code blocks, GFM tables, autolinks, reference links, images, backslash escapes, blockquote borders, nested list visual structure) are not parsed or styled.

Users authoring structured prompts, specs, and notes in the composer need live full-spec Markdown typography (scaled bold headings, monospace code blocks/inline code, full CommonMark/GFM formatting) directly inside the editing surface without losing inline capsules, Korean/CJK IME stability, or undo history.

## Decisions

1. **Structured DOM live hybrid WYSIWYG rendering inside `.wsc-editor` — `(1695)=a`**
   - Replace the paint-only `::highlight(ws-md-*)` path with structured DOM inline/line-level span formatting directly inside `.wsc-editor` (`contenteditable`), so headings render bold with scaled `font-size`, inline and fenced code blocks switch to `var(--wb-mono)` with surface/border styling, and full-spec CommonMark/GFM constructs render live while typing.
   - Defer structural DOM reconciliation during active CJK/Korean IME composition (`compositionstart` through `compositionend`) and reconcile cleanly on composition end and safe edit boundaries, preserving caret/selection offsets, `WsCapsules` atomic `contenteditable="false"` spans, and undo/redo history.
   - **Supersedes** `261125-feat-ws-dashboard-composer-indent-inline-markdown` Decision 1 (flat DOM untouched, cosmetic `CSS.highlights` only). That choice was valid only while the target was color/background tinting; typography (`font-size`, `font-weight`, `font-family`) is unreachable through `::highlight()`, so the flat-DOM constraint is deliberately given up here. The IME, undo, and capsule safety that decision protected carry over as the Preserved composer behaviors below.
   - **Rejected:**
     - Extending `::highlight(ws-md-*)` only — rejected because browsers ignore `font-size`, `font-weight`, `font-style`, and `font-family` on `::highlight()`, making heading scale/bold and code font switching impossible.
     - Adding a Write/Preview tab toggle (like Tracker `.tked`) — rejected because the user wants live formatted Markdown typography directly while editing, not behind a tab switch.
     - Deferring to a separate research-only ticket to evaluate external editor cores first — rejected; proceed directly as an actionable feature ticket (concrete internal reconciliation details remain settled at `ready/` promotion).
2. **Compatibility tier: T0 (transparent)**
   - Changes stay inside dashboard frontend rendering assets (`internal/wsdashboard/assets/`). The submitted prompt wire format (`text` plain Markdown string + existing capsule/mention payload fields) and stored session/draft serialization remain unchanged.

## Constraints

- **Applicable manuals (read before editing):**
  - `ai-docs/manuals/dashboard-style.md` (borderless tonal surfaces, CSS tokens including `var(--wb-mono)`, dark/light theme parity).
  - `ai-docs/manuals/performance.md` (frontend minimal updates; avoid forced synchronous reflows on every keystroke; keep idle CPU zero).
  - `ai-docs/manuals/release-compatibility.md` (maintain T0 compatibility).
  - `ai-docs/manuals/shipped-surface-boundary.md`.
- **Preserved composer behaviors:**
  - `WsCapsules` atomic inline capsules (`contenteditable="false"` topic/item/attachment chips) must continue to serialize to exact placeholder offsets in the backing `#ctext.value` proxy.
  - Korean/CJK IME composition (`compositionstart` / `compositionupdate` / `compositionend`) must never drop characters, split jamo, or jump the caret.
  - Browser/managed Undo (`Ctrl+Z` / `Ctrl+Shift+Z` / `Ctrl+Y`), Enter / `Shift+Enter` / `Ctrl+Enter` send modes, list continuation, auto-scroll cursor following, and `!` shell execution mode (`ws-bang` mode disables Markdown formatting while active) must remain intact.
- **Element-scoped renderer interface (this ticket defines it; `261126-feat-ws-unified-composer-input-component` consumes it):** Expose the renderer as `WsMdEditor.attach(editorEl, opts)` returning `{reconcile(), detach()}`, with no document-level singleton state, so the unified `WsInput` component can mount one renderer per `.wsc-editor` instance regardless of which ticket lands first. `opts` carries every input that is a document global today: `getMentions()` (mention list for `ws-md-mention`-style painting), `isBang()` (bang-mode predicate), `isComposing()` (IME state), and an optional `onReconciled()` callback. Whichever ticket lands first records the final symbol and `opts` in its Result; the other consumes that.
- **Hybrid shape and DOM structure:** Markers (`#`, `**`, backticks, fences, table pipes) stay in the text as dimmed marker spans, never hidden, so `ctext.value` stays byte-identical to the typed text. Horizontal rules and tables are styled text, not real `<hr>` / `<table>` elements. The renderer wraps text only in inline and line-level `<span>` elements and never introduces `DIV` / `P` block wrappers: `capsules.js` `read()` injects `\n` at `DIV` / `P` boundaries, which would double or shift newlines and offsets. The renderer must keep `editorCaretRect` and the existing offset maps correct.
- **Undo contract:** Native browser undo cannot survive a DOM rewrite outside `execCommand`, so the renderer owns a managed history stack (snapshots of plain text plus selection offsets, coalesced per edit burst) that handles `historyUndo` / `historyRedo` `beforeinput` and `Ctrl+Z` / `Ctrl+Shift+Z` / `Ctrl+Y`, including capsule inserts and removals made through `capsules.js`. A worker may substitute another mechanism only if a harness round-trip test proves undo/redo across a reconcile.
- **Mention highlighting preserved:** The legacy `ws-md-mention` painting (from the `mentions` list in `dashboard-composer-highlight.js`) must continue through `opts.getMentions()` when the legacy path is removed. Keep the legacy `CSS.highlights` path (`dashboard-composer-highlight.js`) removable once this renderer lands; bang-mode amber `!` painting (`ws-bang`) is not part of the Markdown path and stays as is.
- **No external runtime CDN dependencies:** Keep assets self-contained in the embedded dashboard asset bundle.
- **File size discipline:** Do not bloat existing large files (`dashboard-conversation.css` / `dashboard-composer*.js`); split new parser/reconciler modules cleanly by responsibility.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/)

## Prior Art

- Current Markdown scanner and highlight mapping: `dashboard-composer-highlight.js` (`scanComposerMarkdown`, `buildEditorTextMap`, `scheduleComposerHighlights`).
- Current `contenteditable` capsule editor bridge: `capsules.js` (`WsCapsules.attach`, `.wsc-editor`, `.wsc-backing`).
- Current `!` shell mode styling hook: `dashboard-composer-exec.js` (`bangMode`, `bangPaint`).

## Prior Decisions

- 261125-feat-ws-dashboard-composer-indent-inline-markdown (2026-11-25, Decisions): "Cosmetic inline markdown via CSS Custom Highlight API — keep .wsc-editor's flat DOM (Text nodes + contenteditable=false .wsc-capsule spans) completely untouched so Korean/CJK IME, Undo history stay safe" — bearing: contradiction-candidate
- 00d1c532 (2026-11-25, commit): "Added cosmetic CSS Custom Highlight API (::highlight(ws-md-*)) markdown and @mention highlighting in dashboard-composer-highlight.js with rAF coalescing, IME composition pause/resume, bang-mode clearing, atomic .wsc-capsule skipping" — bearing: supports
- 00d1c532 (2026-11-25, commit): "Kept MD_HIGHLIGHT_GROUPS on a single line so selection_composer_test.go's var extractor parses dashboard-composer-highlight.js cleanly" — bearing: constrains
- b5e02c73 (2026-11-26, commit): "Coalesced rAF revealCaret triggers only on programmatic newline insertions, leaving grow() and the input listener completely layout-read-free (verified by composer-autosize.test.cjs)" — bearing: constrains
- 7d439405 (2026-11-23, commit): "Amber ! is painted with the CSS Custom Highlight registry (ws-bang) over the contenteditable editor, not through .chl: the composer always attaches the capsule editor" — bearing: constrains
- 07f1d093 (2026-11-25, commit): "Unified Ctrl/Cmd+Shift+V raw-paste bypass in capsules.js onto one module-level rawPasteArmed/rawPasteActive state" — bearing: constrains
- 261109-feat-ws-dashboard-composer-file-mention (2026-11-09, commit b27455b4): "Rejected: a contenteditable editor (ticket decision)." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-highlight.js, capsules.js, dashboard-composer-exec.js, dashboard-conversation.css, plus new parser/reconciler modules |
| scope.surface | internal | dashboard frontend JS/CSS only; no exported Go symbol, wire format unchanged (T0) |
| scope.new_public_symbol | no | none; new JS modules are internal dashboard fragments (asset registration in pages.go is unexported) |
| scope.new_type_contract | no | none |
| scope.test_surface | new-files | existing testdata/composer-*.test.cjs and capsules tests cover current behavior; ticket requires new Node DOM harness tests |
| complexity.reuse_points | confirmed | scanComposerMarkdown, buildEditorTextMap (dashboard-composer-highlight.js#L21, #L234), WsCapsules attach (capsules.js#L282), bangMode (dashboard-composer-exec.js#L299) |
| complexity.side_effect_risk | high | rewrites live contenteditable DOM shared by capsules, IME, undo, pickers, autosize, and bang mode |
| risk.correctness | high | caret/offset mapping across styled nodes, capsules, and IME composition is fragile |
| risk.fit | high | reverses the 261125 flat-DOM decision; unified-composer-input-component ticket adopts this renderer |
| risk.test | high | IME and undo behavior are hard to cover in a Node DOM harness; needs live browser check |
| risk.security_or_contract | moderate | wire text must stay byte-identical; DOM built from user text must avoid HTML injection |

## Phases

### Phase 1: Structured DOM full-spec Markdown renderer in `.wsc-editor`

Implement live full-spec CommonMark/GFM typography inside `.wsc-editor`. Done criterion: every construct listed below is parsed and styled in both themes; a construct the parser cannot classify renders as plain text (no partial styling, no error). A subset does not count as done.

- **Parser & construct coverage:** Extend Markdown scanning to cover ATX headings (`#`–`######` with bold + scaled `font-size` hierarchy), Setext headings, fenced code blocks (`` ``` `` / `~~~`) and 4-space indented code blocks with `var(--wb-mono)` font-family and tonal surface styling, inline code (`` `...` ``) with `var(--wb-mono)`, bold (`**`/`__`), italic (`*`/`_`), bold+italic, strikethrough (`~~`), blockquotes (`>` with tonal accent bar), unordered/ordered/task lists (`- [ ]`/`- [x]`), horizontal rules (`---`/`***`/`___`), inline links, autolinks, reference links, image syntax (preserving `[Image #N]` / `[Text #N]` capsule handling), backslash escapes (`\*`), and GFM tables.
- **IME-safe DOM reconciliation & selection/undo preservation:** Reconcile `.wsc-editor` into styled inline/line-level span elements only outside active IME composition (`!isComposing`), mapping logical plain-text character offsets accurately across styled nodes and atomic `WsCapsules` spans so caret restoration, `ctext.value` / `selectionStart` / `selectionEnd` proxy reads, and undo/redo behave predictably.
- **Theme & style tokens:** Style all rendered Markdown constructs in both dark and light themes using canonical `dashboard-style.md` tokens (`var(--wb-mono)`, tonal surfaces, muted marker opacity) without clipping line heights or breaking composer vertical auto-grow / scroll-to-caret.
- **Interoperability with `!` shell mode and `@`/`#`/`/` pickers:** Suppress Markdown formatting cleanly when `!` bang mode is active, and verify mention/slash/topic picker caret geometry works across varied heading font sizes and code blocks.

**Verification expectations:**
- Node DOM harness tests covering:
  - Full-spec construct parsing and DOM rendering (headings H1–H6 bold + font-size classes, fenced/inline code monospace class, blockquotes, lists/tasks, HR, tables, escapes, links).
  - Plain-text round-trip (`ctext.value`) parity with `WsCapsules` inline capsules embedded inside headings, lists, and paragraphs.
  - Simulated IME composition guard (no DOM rewrite between `compositionstart` and `compositionend`; clean reconciliation on `compositionend` with preserved caret offset).
  - `!` bang mode suppression and re-enable on exit.
  - Undo/redo round-trip across a reconcile (type, reconcile, `Ctrl+Z` restores prior plain text and caret offset, `Ctrl+Y` redoes), including a capsule insertion.
  - Mention highlighting still painted through `opts.getMentions()`.
  - Suggested new test files: `composer-md-render.test.cjs` (constructs, round-trip, bang) and `composer-md-ime-undo.test.cjs` (IME guard, undo) under `agents-plugin-tool/internal/wsdashboard/testdata/`.
- Go verification (host-neutral): `GOOS=windows go vet ./...` and targeted `go test -race ./internal/wsdashboard/...` for asset embedding/tests, plus the Node DOM harness (`node` over `agents-plugin-tool/internal/wsdashboard/testdata/composer-*.test.cjs`) run on whichever host the worker uses.
- Live dashboard check in both dark and light themes. Expected: Korean IME typing loses no jamo and the caret does not jump; H1–H6 scale down in size and are bold; code blocks and inline code use the monospace face; inline capsules survive inside headings and lists; `Ctrl+Z` / `Ctrl+Y` round-trip works after styling has applied; the composer still auto-grows and scrolls to the caret; `!` mode shows no Markdown styling. Capture a result screenshot per theme.

### Result (8345b07a) - 2026-11-26

Replaced the paint-only `::highlight(ws-md-*)` renderer in `.wsc-editor` with a structured DOM live hybrid WYSIWYG full-spec CommonMark/GFM renderer, while keeping `ctext.value` plain-text wire serialization, `WsCapsules` atomic spans, Korean/CJK IME composition, managed undo/redo, auto-grow/scroll-to-caret, and `!` shell execution mode intact (T0 compatible).

**Renderer Interface Contract (for `261126-feat-ws-unified-composer-input-component`):**
- Global symbol: `window.WsMdEditor.attach(editorEl, opts)` (bundled via `pages.go` `MentionScript` alongside `mention.js`, `capsules.js`, and `md-parser.js`; element-scoped with zero document-level singleton state).
- `opts`:
  - `getMentions?: () => Array<{ path?: string, token?: string, name?: string }>` — returns active mention list for `@` mention styling outside code blocks.
  - `isBang?: () => boolean` — suppresses Markdown formatting and flattens to plain text while `!` shell mode is active.
  - `isComposing?: () => boolean` — additional external IME composition predicate combined with internal `compositionstart`/`compositionend` tracking.
  - `onReconciled?: () => void` — post-reconcile hook (used by `#ctext` to re-run `bangPaint(true)` when bang mode is active so `::highlight(ws-bang)` stays attached to live text nodes).
- Returned handle: `{ reconcile: () => void, schedule: () => void, detach: () => void }`.

**What Landed (`2ad08d1a..8345b07a`):**
- `agents-plugin-tool/internal/wsdashboard/assets/md-parser.js` (`window.WsMdParser.parse`): pure, side-effect-free full-spec CommonMark/GFM parser returning exact UTF-16 line/token spans (ATX H1–H6, Setext headings, fenced & 4-space/tab indented code blocks, inline code with seamless pill markers, bold/italic/bold+italic/strikethrough with `snake_case` safety, nested blockquotes with per-level `wsmd-qbar-1..4` bars, unordered/ordered/task lists, horizontal rules, GFM tables, inline/angle/bare autolinks/reference links, images excluding `[Image #N]`/`[Text #N]`, backslash escapes, atomic capsule intervals, and `@` mentions outside code).
- `agents-plugin-tool/internal/wsdashboard/assets/md-editor.js` (`window.WsMdEditor.attach`): element-scoped live DOM reconciler emitting strictly `<span>` wrappers (never `DIV`/`P`/`HR`/`TABLE`, zero `innerHTML`) + real `\n` text nodes + trailing `<br>` on trailing `\n`, preserving existing `[data-wsc-kind]` `.wsc-capsule` DOM nodes, zero synchronous layout reads during `input`/`reconcile()`, IME composition guard, `!` bang-mode suppression, and per-instance managed Undo/Redo stack (`Ctrl/Cmd+Z`, `Ctrl/Cmd+Shift+Z`, `Ctrl+Y`, `beforeinput` `historyUndo`/`historyRedo`) with 600ms word/newline burst coalescing and atomic capsule snapshot restoration.
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-md.css`: token-backed (`--wb-*`, `wsmd-` prefix) dark & light parity styles for H1–H6 bold/scaled hierarchy, `var(--wb-mono)` tonal code pills & blocks, nested blockquote accent bars, task checkboxes, HR, tables, links, mentions, and `.wsc-capsule` typography reset (`12px`) inside styled lines.
- `agents-plugin-tool/internal/wsdashboard/assets/capsules.js`: added `_wsmdRenderHook()`, `compositionend` state sync, atomic `api.applyState()`, collapsed-range `api.anchor(at)` fallbacks across styled lines, and single-snapshot `spliceText()` for rich paste/capsule insertion.
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-highlight.js` & `dashboard-conversation.css`: wired `#ctext` to `window.WsMdEditor.attach(...)`, removed legacy `::highlight(ws-md-*)` rules, and retained `::highlight(ws-bang)`.
- `agents-plugin-tool/internal/wsdashboard/pages.go` & `asset_source_test.go`: embedded `md-parser.js`, `md-editor.js` in `MentionScript` and registered `dashboard-composer-md.css` in `dashboardStyleFiles`.
- Tests: added `composer-md-render.test.cjs` and `composer-md-ime-undo.test.cjs`, and updated `composer-indent-highlight.test.cjs`.

**Verification:**
- Node harness (`node --test agents-plugin-tool/internal/wsdashboard/testdata/composer-{md-render,md-ime-undo,indent-highlight,autosize,scroll-follow,bang,inline,multiline}.test.cjs`): 31 pass, 0 fail (15 Playwright tests in `composer-bang.test.cjs` skipped without `WS_PLAYWRIGHT_MODULE`).
- Capsules suite (`node agents-plugin-tool/internal/wsdashboard/testdata/capsules.test.cjs`): PASS.
- Go vet (`GOOS=windows go vet ./...` in `agents-plugin-tool`): PASS (0 warnings/errors).
- Targeted Go race suite (`go test -race -v -run 'TestDashboard|TestAsset|TestCSS|TestSelection|TestCapsule|TestComposer|TestMention|TestPage|TestStyle|TestScript' ./internal/wsdashboard/...`): PASS (`ok github.com/kang-sw/devenv/internal/wsdashboard 179.323s`, covering `TestDashboardAssetSourceAssembly`, `TestAssetIDFollowsAssetContent`, `TestCSSClassesDoNotCollideAcrossModules`, `TestComposer*`, `TestSelection*`, `TestPage*`).
- Live Chromium Playwright check in both dark and light themes (`verify-composer-md.browser.cjs`): PASS — H1 (`19.17px`, weight 700) > H2 (`17.01px`) > H3 (`15.39px`), capsule inside H1 reset to `12px`, inline & fenced code rendered in monospace, zero forbidden tags (`SPAN`/`BR` only), picker `anchor(at)` geometry verified across headings/tables/code, Korean IME composition performed 0 DOM mutations and reconciled cleanly on commit, `Ctrl+Z`/`Ctrl+Y` round-trip passed, auto-grow & scroll-to-caret passed, `!` bang mode suppressed markdown with `0px` height delta. Screenshots inspected: `composer-md-dark.png`, `composer-md-light.png`.
- Lite code review (`code-review-lite`, `88e37970-01-findings.md`): `clean` (0 Critical, 0 Important; 1 non-blocking Minor note that mention-list changes reconcile on the next `schedule()`/`input`, which `dashboard-composer-mention.js` already triggers via `scheduleComposerHighlights()`).


## Resolution (2026-11-26)

Completed Phase 1 in commit 8345b07a: replaced legacy ::highlight(ws-md-*) in .wsc-editor with element-scoped window.WsMdEditor.attach(editorEl, opts) -> {reconcile(), schedule(), detach()} structured DOM full-spec CommonMark/GFM renderer, IME composition guard, managed undo/redo stack with atomic WsCapsules support, dark/light token-backed CSS, Node DOM harness + Go vet/race + live Playwright dark/light verification, and clean code-review-lite pass.
