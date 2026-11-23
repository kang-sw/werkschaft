---
title: "대시보드 보드 카드 노트 타이핑 시 #ctext 캡슐 매 키입력 재생성 제거"
related:
  261123-bug-ws-dashboard-webview-idle-cpu: sibling performance investigation (Trace-20261123T153130)
  261123-feat-ws-dashboard-board-topic-anchor-scroll: complementary Phase 2 DOM-size reduction
---

# Fix Per-Keystroke `#ctext` Capsule Recreation During Board Card Note Typing

## Background

Performance trace `Trace-20261123T153130.json.gz` (captured 2026-11-23 comparing board card note typing against main composer typing) showed severe input lag in board card note inputs (`textarea.note` / `textarea.tnote`):

- **Card note typing (`27.4-33.1s`):** 70.6% main-thread active, **26 Long Tasks (>50ms, max `138.61ms`)**, median keystroke-to-Paint **`66.27ms`** (p95 `115.00ms`, max `158.76ms`), **2.53 Layouts / `43.12ms` per key**, and **11.0 Style Recalcs / `12.95ms` per key (`elem=135..137`)**.
- **Main composer typing (`34.2-35.4s`):** 45.7% main-thread active, **0 Long Tasks (max `31.33ms`)**, median keystroke-to-Paint `30.94ms`, 1.33 Layouts / `15.05ms` per key, and Style Recalcs `elem=0..6` (`2.14ms` per key).

Root cause: every keystroke in a board card note invokes `syncChips()` (`dashboard-composer.js`, symbol `syncChips`) -> `ctext.wsc.upsert()`, which in `capsules.js` (`upsert`, lines 353-365 as evidence) tears down and rebuilds `#ctext` DOM (`render()` clearing `editor.textContent = ""`, calling `select()`, and emitting `wsc:change`) **twice per keystroke** even when the capsule anchor has not changed and only the note payload updated. Main composer typing invokes `render()` zero times.

This bug is independent of `261123-feat-ws-dashboard-board-topic-anchor-scroll`: Phase 1 removes full-board blur swaps and Phase 2 reduces the ~10,946-object layout tree, neither of which eliminates the per-keystroke `#ctext` capsule DOM teardown.

## Decisions

- **Independent client-only bug fix (`(1504)=a`):** Fix per-keystroke `#ctext` capsule recreation in `capsules.js` immediately as its own ticket rather than waiting for `board-scroll` Phase 2.
  - *Rejected:* Deferring until after `board-scroll` Phase 2, because `capsules.js` `upsert()` would continue triggering unnecessary DOM teardowns and double style/layout passes on every note keystroke.
- **In-place `upsert()` fast path and single-`render()` fallback:** In `capsules.js` (`upsert`), when an existing capsule has the same anchor (`marker(old) === payload.anchor`) and maps to a single matching `span[data-wsc-kind][data-wsc-key]` node in the editor:
  1. Update the internal `ranges` entry and the existing element's `data-wsc-payload` attribute in place.
  2. Update the element's `label` / `textContent` only if the displayed label actually changed.
  3. Skip `replace()`, `render()`, `select()`, and synthetic `input` dispatch when the serialized editor text is unchanged.
  4. When the anchor changes (e.g., switching an option on a card), fall back to a single `render()` call rather than two consecutive `render()` calls.
  - *Rejected:* Debouncing `syncChips()` in `dashboard-composer.js` alone, which risks stale payloads on fast submit/blur and still causes periodic `#ctext` DOM teardown spikes.
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

## Phases

### Phase 1: In-place capsule upsert fast path and card-note typing regression test

Eliminate per-keystroke `#ctext` DOM teardown during board card note typing and lock in regression coverage.

**Goals:**
1. Update `upsert()` in `capsules.js` (and any tightly coupled caller contract in `dashboard-composer.js` `syncChips()` if needed) so unchanged-anchor upserts mutate the single matching `span[data-wsc-kind][data-wsc-key]` in place (`ranges`, `data-wsc-payload`, and `label`/`textContent` only when changed) without calling `replace()`, `render()`, `select()`, or dispatching synthetic `input` when serialized text is unchanged.
2. Collapse the anchor-changed fallback path in `upsert()` from two `render()` calls to a single `render()` call.
3. Add unit coverage (`capsules.test.cjs`, `composer-note-chips.test.cjs`, and/or `composer-inline.test.cjs`) verifying:
   - Unchanged-anchor `upsert()` preserves the exact `span.wsc-capsule` DOM node identity (no `editor.textContent` wipe or rebuild) while keeping `data-wsc-payload` and serialized composer output up to date.
   - Label-change with unchanged anchor updates text in place without full rebuild.
   - Anchor-change `upsert()` performs at most one `render()` and produces the expected capsule and text.
4. Add a board card `textarea.note` typing regression test in `note-typing.browser.cjs` verifying that typing into a card note does not tear down or recreate `#ctext` capsule DOM nodes on each keystroke while still syncing the updated note payload.

**Verification:**
- Local targeted `node --test` on `capsules.test.cjs`, `composer-inline.test.cjs`, `composer-note-chips.test.cjs`, and `note-typing.browser.cjs` (when Playwright browser is available).
- Remote `w5090` Go verification for dashboard asset assembly (`TestDashboardAssetSourceAssembly` / `./internal/wsdashboard/...` as appropriate).
