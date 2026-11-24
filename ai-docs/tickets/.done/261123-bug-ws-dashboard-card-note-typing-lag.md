---
title: "대시보드 보드 카드 노트 타이핑 시 #ctext 캡슐 매 키입력 재생성 제거"
related:
  261123-bug-ws-dashboard-webview-idle-cpu: sibling performance investigation (Trace-20261123T153130)
  261123-feat-ws-dashboard-board-topic-anchor-scroll: complementary Phase 2 DOM-size reduction
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: eebef4f4c95cdbf6
sage-review-completeness-reviewed: eebef4f4c95cdbf6
completed: 2026-11-24
---

# Fix Per-Keystroke `#ctext` Capsule Recreation During Board Card Note Typing

## Background

Performance trace `Trace-20261123T153130.json.gz` (captured 2026-11-23 comparing board card note typing against main composer typing) showed severe input lag in board card note inputs (`textarea.note` / `textarea.tnote`):

- **Card note typing (`27.4-33.1s`):** 70.6% main-thread active, **26 Long Tasks (>50ms, max `138.61ms`)**, median keystroke-to-Paint **`66.27ms`** (p95 `115.00ms`, max `158.76ms`), **2.53 Layouts / `43.12ms` per key**, and **11.0 Style Recalcs / `12.95ms` per key (`elem=135..137`)**.
- **Main composer typing (`34.2-35.4s`):** 45.7% main-thread active, **0 Long Tasks (max `31.33ms`)**, median keystroke-to-Paint `30.94ms`, 1.33 Layouts / `15.05ms` per key, and Style Recalcs `elem=0..6` (`2.14ms` per key).

Root cause: every keystroke in a board card note invokes `syncChips()` (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-keyboard.js#L66-L73`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer.js#L146-L154`) -> `ctext.wsc.upsert()`, which in `agents-plugin-tool/internal/wsdashboard/assets/capsules.js#L353-L365` tears down and rebuilds `#ctext` DOM (`render()` clearing `editor.textContent = ""` and calling `select()`, `capsules.js#L186-L193`) **twice per keystroke** (once in `replace()` at `capsules.js#L224`, `#L360` and once at `capsules.js#L363`, followed by a single `notify()` dispatching `input` and `wsc:change` at `capsules.js#L210-L216`, `#L364`) even when the capsule anchor has not changed and only the note payload updated. Main composer typing invokes `render()` zero times (`capsules.js#L254-L258`).

This bug is independent of `261123-feat-ws-dashboard-board-topic-anchor-scroll`: Phase 1 switches Main Board to card-keyed scrolling and per-card staging while removing outer-board pixel capture/restore and FLIP passes (`ai-docs/tickets/ready/261123-feat-ws-dashboard-board-topic-anchor-scroll.md#L118-L130`; `focusout` release in `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board.js#L1166-L1173` remains) and Phase 2 reduces mounted DOM size via outward lazy topic body hydration (`261123-feat-ws-dashboard-board-topic-anchor-scroll.md#L137-L141`, ~10,946-object layout tree in the external trace), neither of which eliminates the per-keystroke `#ctext` capsule DOM teardown.

## Decisions

- **Independent client-only bug fix (`(1504)=a`):** Fix per-keystroke `#ctext` capsule recreation in `capsules.js` immediately as its own ticket rather than waiting for `board-scroll` Phase 2.
  - *Rejected:* Deferring until after `board-scroll` Phase 2, because `capsules.js` `upsert()` would continue triggering unnecessary DOM teardowns and double style/layout passes on every note keystroke.
- **In-place `upsert()` fast path and single-`render()` fallback (`(1511)`):** In `capsules.js` (`upsert`), when an existing capsule has the same anchor (`marker(old) === payload.anchor`) and maps to a single matching `.wsc-capsule` node in the editor (located by iterating `editor.querySelectorAll(".wsc-capsule")` or child nodes and comparing `getAttribute("data-wsc-kind") === kind` and `getAttribute("data-wsc-key") === key` — never raw CSS attribute-selector interpolation, because `capsules.test.cjs#L35` `El.matches()` only supports `.class` selectors and keys with quotes/brackets break selector interpolation):
  1. Update the internal `ranges` entry and the existing element's `data-wsc-payload` attribute in place.
  2. If the displayed label changed, update both `data-wsc-label` (read by `read()` at `capsules.js#L120`) and `textContent` in place.
  3. Still emit `wsc:change` via `changed(before, "update")` so payload-change observers stay consistent, while skipping `replace()`, `render()`, `select()`, and synthetic `input` dispatch when the serialized editor text is unchanged.
  4. When the anchor changes (e.g., switching an option on a card), fall back to a single `render()` call rather than two consecutive `render()` calls.
  - *Rejected:* Debouncing `syncChips()` in `dashboard-composer.js` alone (risks stale payloads on fast submit/blur and still causes periodic `#ctext` DOM teardown spikes); locating the existing node via `editor.querySelectorAll('span[data-wsc-kind="..."][data-wsc-key="..."]')` (breaks `capsules.test.cjs#L35` and unescaped keys).
- **Keep `dashboard-lifecycle.js` `post()` RAF `[hidden]` chatter separate:** Secondary `MutationObserver` -> `post()` RAF overhead (`dashboard-lifecycle.js:1260` evidence) is explicitly out of scope here to keep this fix minimal and isolated.
- **Compatibility tier: T0 (transparent).** Pure client-side dashboard JS asset and test change. No MCP tool, `/mcp` hello, store schema, tracker event, or manifest surface changes; old and new binaries are unaffected.

## Constraints

- **Required manuals (read before editing):**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`)
  - `ai-docs/manuals/release-compatibility.md`
- **Behavioral invariants:**
  - Composer serialization (`ctext.wsc` payload read on send/submit), capsule editing/deletion, focus/caret state in `textarea.note` / `textarea.tnote` and `#ctext`, and option-switch anchor changes must preserve existing user-visible semantics.
  - If `wsc:change` or any observer relies on payload updates, ensure state stays consistent without triggering DOM teardown when only payload metadata changed.
- **Out of scope:** `dashboard-lifecycle.js` `post()` RAF `[hidden]` chatter, `cards.js` `fitToolbar` / `toolbarRowFits`, and `board-scroll` Phase 2 lazy topic body mounting.
- **Verification environment:** On Windows, run Go tests only on remote `w5090` (never local `go test`); run targeted JS tests locally via `node --test`.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- c4a488d6 (2026-11-21, commit): "Board answer and topic-note controls synchronize with atomic editor deletion, while local drafts and held returns retain capsule metadata." — bearing: constrains
- 31042a2b (2026-11-21, commit): "The explicit historical baseline read in note-typing.browser.cjs remains intentional." — bearing: constrains
- 572c68b5 (2026-11-17, commit): "Chromium/CDP invalidation tracking first reproduced whole-document restyling: strip child replacement invalidates body through dashboard-rail.css's body:not(:has(#grid[data-layout="tabs"])) rule." — bearing: supports
- a11c73e8 (2026-11-17, commit): "Worker 572c68b5c replaced the measured broad :has() invalidation and preserves chip nodes while typing." — bearing: supports
- 36abfa78 (2026-11-17, commit): "Shell posts reuse the mirrored header until actual header mutation/resize or explicit sync. Ordinary board/root notifications with no open overlays perform no geometry/style reads" — bearing: supports
- 76d8f797 (2026-11-16, commit): "Exposed stale selector in whiteboard-effects-browser.cjs (#ctext hidden backing textarea since d2921cf02) fixed to '#ctext + .wsc-editor' as in e08f9c3fe; no assertion weakened." — bearing: constrains
- 261115-feat-ws-composer-inline-skill-and-topic-capsules (2026-11-15, commit): "The textarea remains each field's owner for existing form, image, draft and send code. Its rich sibling presents compact atomic capsules while value remains canonical text: skill name or [topic-stem]." — bearing: constrains
- 261114-refactor-ws-dashboard-whiteboard-asset-split (2026-11-15, commit): "Concatenate contiguous JavaScript source fragments without separators into the original single IIFE, preserving declaration hoisting, early return, shared state, handler ordering and initialization." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/assets/capsules.js, dashboard-composer.js (if needed), testdata/capsules.test.cjs, composer-note-chips.test.cjs, composer-inline.test.cjs, note-typing.browser.cjs |
| scope.surface | internal | client-only dashboard JS assets and tests; window.WsCapsules.attach and field.wsc.upsert API signatures unchanged |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/testdata/capsules.test.cjs, composer-note-chips.test.cjs, composer-inline.test.cjs, note-typing.browser.cjs; Go wrappers in capsules_test.go, capsules_browser_test.go, asset_source_test.go |
| complexity.reuse_points | confirmed | upsert, capsule, read, notify, select in capsules.js#L109-L216,L353-L365; syncChips, putChip, syncInlineTopics in dashboard-composer.js#L58-L154 |
| complexity.side_effect_risk | moderate | capsules.js#L353-L365 upsert is shared across answer, topic-note, and mention capsules and drives wsc:change, caret selection, and composer serialization |
| risk.correctness | moderate | in-place span mutation must keep ranges, data-wsc-label, data-wsc-payload, textContent, caret offsets, and single-render anchor-change fallback consistent |
| risk.fit | low | localized to capsules.js#L353-L365 upsert and existing syncChips contract in dashboard-composer.js#L146-L154 |
| risk.test | moderate | Node unit tests run locally, while note-typing.browser.cjs requires Playwright Chromium and Go tests run only on remote w5090 |
| risk.security_or_contract | low | T0 client-only JS change; no MCP, wire, store, or manifest contract touched, and plain-text/HTML capsule serialization boundary in capsules.js#L176-L185 is preserved |

## Phases

### Phase 1: In-place capsule upsert fast path and card-note typing regression test

Eliminate per-keystroke `#ctext` DOM teardown during board card note typing and lock in regression coverage.

**Goals:**
1. Update `upsert()` in `capsules.js` (and any tightly coupled caller contract in `dashboard-composer.js` `syncChips()` if needed) so unchanged-anchor upserts locate the single matching `.wsc-capsule` node by iterating `.wsc-capsule` elements and comparing `getAttribute("data-wsc-kind")` / `getAttribute("data-wsc-key")` (avoiding raw CSS attribute-selector interpolation), mutate it in place (`ranges`, `data-wsc-payload`, and both `data-wsc-label` and `textContent` when the label changed), and emit `wsc:change` via `changed(before, "update")` without calling `replace()`, `render()`, `select()`, or dispatching synthetic `input` when serialized text is unchanged.
2. Collapse the anchor-changed fallback path in `upsert()` from two `render()` calls to a single `render()` call.
3. Add unit coverage (`capsules.test.cjs`, `composer-note-chips.test.cjs`, and/or `composer-inline.test.cjs`) verifying:
   - Unchanged-anchor `upsert()` preserves the exact `span.wsc-capsule` DOM node identity (no `editor.textContent` wipe or rebuild) while keeping `data-wsc-payload`, `wsc:change` notification, and serialized composer output up to date without synthetic `input` dispatch.
   - Label-change with unchanged anchor updates both `data-wsc-label` and `textContent` in place without full rebuild.
   - Anchor-change `upsert()` performs at most one `render()` and produces the expected capsule and text.
4. Add a board card `textarea.note` typing regression test in `note-typing.browser.cjs` verifying that typing into a card note does not tear down or recreate `#ctext` capsule DOM nodes on each keystroke while still syncing the updated note payload.

**Verification:**
- Local targeted `node --test` on `agents-plugin-tool/internal/wsdashboard/testdata/capsules.test.cjs`, `composer-inline.test.cjs`, and `composer-note-chips.test.cjs`.
- Playwright regression check on `agents-plugin-tool/internal/wsdashboard/testdata/note-typing.browser.cjs` (standalone script requiring `WS_PLAYWRIGHT_MODULE`, run via `node` when Playwright Chromium is available or via Go `capsules_browser_test.go` on remote `w5090`).
- Remote `w5090` Go verification for dashboard asset assembly and tests (`TestDashboardAssetSourceAssembly` / `./internal/wsdashboard/...` as appropriate).

### Result (a255d1409) - 2026-11-24

Implemented the T0 client-only fix for per-keystroke `#ctext` capsule recreation and the owner-approved `(117)=a` `form.js` `grow()` guard:

- **Changed files:**
  - `agents-plugin-tool/internal/wsdashboard/assets/capsules.js`: Added an in-place fast path in `upsert()` when `marker(old) === payload.anchor` that locates the single matching `.wsc-capsule` node by iterating `editor.querySelectorAll(".wsc-capsule")` and comparing `data-wsc-kind` / `data-wsc-key` (no raw CSS attribute-selector interpolation), updates `ranges`, `data-wsc-payload`, and (when changed) `data-wsc-label` + `textContent` in place, and emits `wsc:change` via `changed(before, "update")` without calling `replace()`, `render()`, `select()`, or dispatching synthetic `input`. Extracted `spliceText()` so the anchor-changed fallback path performs a single `render()` call instead of two, and falls back to a single `render()` on unexpected DOM drift.
  - `agents-plugin-tool/internal/wswhiteboard/assets/form.js`: Guarded `grow(n)` to return early when `n.classList && n.classList.contains("wsc-backing")`, avoiding synchronous `style.height = "auto"` / `scrollHeight` layout flushes on hidden capsule-backed note textareas while preserving auto-grow on plain visible textareas.
  - `agents-plugin-tool/internal/wsdashboard/testdata/capsules.test.cjs`: Added unit coverage for unchanged-anchor in-place `upsert()`, label-change with selector-hostile key (`topic"1[x]`), single-`render()` anchor-change fallback, and DOM drift fallback.
  - `agents-plugin-tool/internal/wsdashboard/testdata/note-typing.browser.cjs`: Added Playwright Chromium regression checks verifying `#ctext` `.wsc-capsule` DOM node identity and zero synthetic `#ctext` `input` events across both `.tnote` and board card `textarea.note` keystrokes (also fixed the pre-existing 40-char label truncation assertion for Korean IME composition).
  - `agents-plugin-tool/internal/wswhiteboard/check_form_test.go`: Added `TestFormGrowSkipsWscBackingTextarea` verifying `grow()` skips `.wsc-backing` textareas on `input` and `note-toggle` click while still auto-growing plain `textarea.note`.

- **Verification (macOS local Go + Node + Playwright Chromium):**
  - `node --test agents-plugin-tool/internal/wsdashboard/testdata/capsules.test.cjs agents-plugin-tool/internal/wsdashboard/testdata/composer-inline.test.cjs agents-plugin-tool/internal/wsdashboard/testdata/composer-note-chips.test.cjs` — PASS (3/3).
  - `WS_PLAYWRIGHT_MODULE=... PLAYWRIGHT_BROWSERS_PATH=... go test ./internal/wsdashboard -run 'TestDashboardAssetSourceAssembly|TestCapsules|TestInlineCapsules|TestComposer|TestNoteTyping' -v` — PASS.
  - `WS_PLAYWRIGHT_MODULE=... PLAYWRIGHT_BROWSERS_PATH=... go test ./internal/wsdashboard -run 'TestBoardNoteTypingBrowser' -v` — PASS (`styleElements: [3, 1]`, `stylePerKeyMs: 0.35ms`, `layoutPerKeyMs: 0.48ms`, `mainTaskMaxMs: 5.07ms` on 14,722 elements; exact `.wsc-capsule` DOM node preserved and 0 synthetic `#ctext` `input` events).
  - `WS_PLAYWRIGHT_MODULE=... PLAYWRIGHT_BROWSERS_PATH=... go test ./internal/wswhiteboard -run 'TestForm|TestWhiteboardAsset|TestCheckRows|TestTypingBuilds|TestPillRedraws' -v` — PASS (including `TestFormGrowSkipsWscBackingTextarea`).
  - `GOOS=windows go vet ./internal/wsdashboard ./internal/wswhiteboard` — PASS.


## Resolution (2026-11-24)

Implemented in-place capsule upsert fast path and single-render anchor-change fallback in capsules.js, added the owner-confirmed (117)=a grow() guard for hidden .wsc-backing textareas in form.js, and added Node, Go, and Playwright Chromium regression coverage.
