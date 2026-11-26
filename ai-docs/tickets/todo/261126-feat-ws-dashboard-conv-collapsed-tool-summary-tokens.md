---
title: "대화창 기본 턴 도구 호출 자동 접힘 및 추가 토큰(+Nk tok) 요약"
parent: 261126-epic-ws-dashboard-conversation-ux-restyle
related:
  261115-feat-ws-dashboard-conversation-rendering-consolidation: preserves .wbupdate-topic and mail entries while collapsing tool rows in .conv.centries
sage-review-design: pending
sage-review-completeness: pending
---

# Baseline Collapsed Turn Tool Summary & Added Tokens (+Nk tok)

## Background

In the dashboard conversation stream (`.conv.centries`), completed lead turns currently surface verbose tool-call rows (`.au-tools`, `.toolsline`) directly inline, visually drowning out user bubbles, whiteboard updates (`.wbupdate-topic`), and assistant replies. Meanwhile, `wslive` tracks live `UsageContext`, but `RingTurn` does not record per-turn token delta (`+Nk tok`), so users cannot gauge how much work a turn performed at a glance.

As Axis A (foundation) of `261126-epic-ws-dashboard-conversation-ux-restyle`, this ticket makes tool-call collapsing an always-on baseline UX feature of the existing single conversation view and establishes the shared turn-summary DOM contract (`[data-conv-turn-summary]`, `[data-conv-turn-drawer]`, `[data-conv-turn-fails]`, `[data-conv-turn-tokens]`) that Child 2 (`화이트보드 전용` plain-text folding) and Child 3 (`#r-convlive` Extra Motion dynamic token convergence) build on.

Approved visual reference: `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html` (v10 — Axis A completed & active turn summary rows).

## Decisions

1. **Always-on default collapsed 1-line tool summary on the existing single conv view — `(07)`, `(08)=b`, `(10)`, `(12)`, `(28)=ok`**
   - Completed lead turns with one or more tool calls automatically collapse their tool rows into a single clickable, borderless tonal summary row:
     `✓ N개 툴 호출 · 11.4s [· 실패 K] · +2.8k tok ▾` (localized via `wsi18n`).
   - Clicking the summary row smoothly toggles expansion of the turn's underlying tool call rows (`.brows` / `.au-tools`) inside `[data-conv-turn-drawer]` (showing calm category-colored dots per tool family as in the v10 mockup).
   - Active (in-progress) turns also expose a live summary header row updating tool count, elapsed time, failure count (`[data-conv-turn-fails]`), and token delta (`[data-conv-turn-tokens]`) as tools finish.
   - Existing `.wbeffects.wbupdate` / `.wbupdate-topic`, `.msg.user`, `.msg.fromlead`, and mail cards remain untouched (no dual view renderer).
   - **Rejected:**
     - Hiding completed tool calls completely with no expandable summary (`(08)=a`) — removes at-a-glance scale visibility and one-click drill-down.
     - Gating tool collapsing behind a separate `화이트보드 중심` view mode (`(10)`, 2026-11-26 21:52 KST) — tool collapsing is useful baseline chat UX regardless of whiteboard mode.

2. **Cross-harness `RingTurn.AddedTokens` with byte-estimate fallback — technical (`T0`)**
   - In `agents-plugin-tool/internal/wslive/`, capture `UsageContext` at turn start and compute `AddedTokens = max(latest - start, 0)` on turn completion (and incrementally during an active turn when context usage updates), persisted as an optional `added_tokens,omitempty` field on `RingTurn`.
   - When a harness does not report updated `UsageContext` for a turn (`AddedTokens == 0` despite tool/text activity), compute a deterministic byte-estimate fallback displayed with a leading tilde (`~+1.2k tok`) so every turn shows a consistent magnitude cue without misrepresenting an estimate as an exact count.
   - **Compatibility tier: T0 (transparent).** Additive `omitempty` JSON field; older binaries ignore it cleanly and render without breakage.
   - **Rejected:**
     - Claude-only or harness-specific token accounting — breaks Codex/Agy/Pi parity.

3. **Shared turn-summary DOM contract for Children 2 & 3 — epic invariant**
   - Every collapsed turn summary exposes stable attributes:
     - `[data-conv-turn-summary]` on the toggleable summary row (with `aria-expanded="false|true"`)
     - `[data-conv-turn-drawer]` on the collapsible drawer container holding tool rows (and ready for Child 2's folded plain-text output)
     - `[data-conv-turn-count]` on the tool count span
     - `[data-conv-turn-fails]` on the failure count badge (hidden when 0)
     - `[data-conv-turn-tokens]` on the right-side `+Nk tok` marker (target anchor for Child 3's dynamic `getBoundingClientRect()` convergence & `+m` damage-number pop)
   - Expand/collapse state survives incoming SSE conversation patches (`mergeEntries` / `keyedInputPatch`) for the same turn key without forcing full stream re-render or disturbing `.convo` scroll anchoring (`84cd8692`).

4. **Surgical i18n keys across all locale tables — technical**
   - Add minimal surgical i18n keys across all locale JSON files in `agents-plugin-tool/internal/wsi18n/tables/` (preserving existing key order; no whole-file re-sorting) for `N tools` / `N개 툴 호출`, failure badge `실패 N`, and expand/collapse aria labels.

## Constraints

- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- **Minimal DOM updates & scroll-lock safety:** Patch only the affected turn summary row during live updates; never re-render `.convo` wholesale or break `stick===false` scroll lock (`84cd8692`).
- **No disk I/O on interactive toggle:** Expanding/collapsing a turn drawer is pure in-memory DOM state.
- **No regression in `261115` consolidation or `261126` question capsules:** `.wbupdate-topic` cards and mail cards stay cleanly visible outside the collapsed tool drawer.

## Prior Art

- Live state & ring turn projection: `agents-plugin-tool/internal/wslive/state.go`, `agents-plugin-tool/internal/wslive/snapshot.go`, `agents-plugin-tool/internal/wslive/ring.go` (or ring turn definitions).
- Conversation presentation & templates: `agents-plugin-tool/internal/wsdashboard/pageview.go`, `agents-plugin-tool/internal/wsdashboard/page_format.go`, `agents-plugin-tool/internal/wsdashboard/conversation_presentation.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl`.
- Conversation client rendering & styling: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.js`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.css`.

## Phases

### Phase 1: Per-turn `AddedTokens` capture and default collapsed expandable tool summary row

- **Backend `wslive` token delta (`AddedTokens`):**
  - Record turn-start `UsageContext` in `wslive` live turn state and populate optional `AddedTokens` (`json:"added_tokens,omitempty"`) on `RingTurn` (and live snapshot if active) on completion, with deterministic byte-estimate fallback (`~+Nk tok`) when context usage did not advance.
  - Expose tool count, failed tool count, elapsed duration, and formatted token delta in conversation presentation view models cleanly (T0).
- **Collapsed 1-line turn summary UI & shared DOM hooks:**
  - Collapse completed lead tool-call groups in `.conv.centries` by default into the single borderless tonal summary row matching `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html` (`✓ N개 툴 호출 · 11.4s [· 실패 K] · +2.8k tok ▾`), with click-to-expand `[data-conv-turn-drawer]` displaying the turn's tool rows (including subtle tool-family color dots).
  - Expose `[data-conv-turn-summary]`, `[data-conv-turn-drawer]`, `[data-conv-turn-count]`, `[data-conv-turn-fails]`, and `[data-conv-turn-tokens]` consistently on both active and completed turn summary headers.
  - Preserve user expand/collapse state across SSE entry merges/patches and preserve `.convo` scroll anchoring.
  - Keep `.wbupdate-topic`, `.msg.user`, `.msg.fromlead`, and consolidated mail cards outside the tool drawer.
- **Surgical i18n keys:**
  - Append needed summary/failure labels across all locale tables in `agents-plugin-tool/internal/wsi18n/tables/` without re-sorting existing keys.

**Verification expectations:**
- Go tests (from `agents-plugin-tool/`): `go test -race ./internal/wslive/... ./internal/wsdashboard/... ./internal/wsi18n/...` and `GOOS=windows go vet ./...`, covering `AddedTokens` delta math (normal increase, reset/compaction clamp to 0, byte-estimate `~+Nk` fallback, `omitempty` T0 JSON compatibility), turn-summary template rendering, and locale table parity.
- Node DOM harness tests covering: default collapsed state on completed turns, click-to-expand and click-to-collapse toggling `[data-conv-turn-drawer]` & `aria-expanded`, preservation of expanded state across SSE `mergeEntries` / keyed patches without scroll-lock drift, failure count visibility when `failed > 0`, and presence of `[data-conv-turn-tokens]` anchor.
- Headless Chromium (Playwright) visual verification in dark and light themes compared side-by-side against the Axis A turn-summary rows in `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`.
