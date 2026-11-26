---
title: "입력창 풀스펙 마크다운 실시간 렌더링 지원 (제목 크기·볼드, 코드 블록 폰트 등)"
related:
  261126-feat-ws-unified-composer-input-component: shared input component adopting this markdown renderer
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
   - Replace the paint-only `::highlight(ws-md-*)` path with structured DOM block/inline formatting directly inside `.wsc-editor` (`contenteditable`), so headings render bold with scaled `font-size`, inline and fenced code blocks switch to `var(--wb-mono)` with surface/border styling, and full-spec CommonMark/GFM constructs render live while typing.
   - Defer structural DOM reconciliation during active CJK/Korean IME composition (`compositionstart` through `compositionend`) and reconcile cleanly on composition end and safe edit boundaries, preserving caret/selection offsets, `WsCapsules` atomic `contenteditable="false"` spans, and undo/redo history.
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
- **No external runtime CDN dependencies:** Keep assets self-contained in the embedded dashboard asset bundle.
- **File size discipline:** Do not bloat existing large files (`dashboard-conversation.css` / `dashboard-composer*.js`); split new parser/reconciler modules cleanly by responsibility.

## Prior Art

- Current Markdown scanner and highlight mapping: `dashboard-composer-highlight.js` (`scanComposerMarkdown`, `buildEditorTextMap`, `scheduleComposerHighlights`).
- Current `contenteditable` capsule editor bridge: `capsules.js` (`WsCapsules.attach`, `.wsc-editor`, `.wsc-backing`).
- Current `!` shell mode styling hook: `dashboard-composer-exec.js` (`bangMode`, `bangPaint`).

## Phases

### Phase 1: Structured DOM full-spec Markdown renderer in `.wsc-editor`

Implement live full-spec CommonMark/GFM typography inside `.wsc-editor`:

- **Parser & construct coverage:** Extend Markdown scanning to cover ATX headings (`#`–`######` with bold + scaled `font-size` hierarchy), Setext headings, fenced code blocks (`` ``` `` / `~~~`) and 4-space indented code blocks with `var(--wb-mono)` font-family and tonal surface styling, inline code (`` `...` ``) with `var(--wb-mono)`, bold (`**`/`__`), italic (`*`/`_`), bold+italic, strikethrough (`~~`), blockquotes (`>` with tonal accent bar), unordered/ordered/task lists (`- [ ]`/`- [x]`), horizontal rules (`---`/`***`/`___`), inline links, autolinks, reference links, image syntax (preserving `[Image #N]` / `[Text #N]` capsule handling), backslash escapes (`\*`), and GFM tables.
- **IME-safe DOM reconciliation & selection/undo preservation:** Reconcile `.wsc-editor` into styled block/inline DOM elements only outside active IME composition (`!isComposing`), mapping logical plain-text character offsets accurately across styled nodes and atomic `WsCapsules` spans so caret restoration, `ctext.value` / `selectionStart` / `selectionEnd` proxy reads, and undo/redo behave predictably.
- **Theme & style tokens:** Style all rendered Markdown constructs in both dark and light themes using canonical `dashboard-style.md` tokens (`var(--wb-mono)`, tonal surfaces, muted marker opacity) without clipping line heights or breaking composer vertical auto-grow / scroll-to-caret.
- **Interoperability with `!` shell mode and `@`/`#`/`/` pickers:** Suppress Markdown formatting cleanly when `!` bang mode is active, and verify mention/slash/topic picker caret geometry works across varied heading font sizes and code blocks.

**Verification expectations:**
- Node DOM harness tests covering:
  - Full-spec construct parsing and DOM rendering (headings H1–H6 bold + font-size classes, fenced/inline code monospace class, blockquotes, lists/tasks, HR, tables, escapes, links).
  - Plain-text round-trip (`ctext.value`) parity with `WsCapsules` inline capsules embedded inside headings, lists, and paragraphs.
  - Simulated IME composition guard (no DOM rewrite between `compositionstart` and `compositionend`; clean reconciliation on `compositionend` with preserved caret offset).
  - `!` bang mode suppression and re-enable on exit.
- Remote `w5090` Go verification (`GOOS=windows go vet ./...` and targeted `go test -race ./internal/wsdashboard/...` for asset embedding/tests). Never run local `go test` on Windows.
- Live dashboard check in both dark and light themes verifying Korean IME typing, heading scaling, monospace code blocks, capsules, and undo/redo.
