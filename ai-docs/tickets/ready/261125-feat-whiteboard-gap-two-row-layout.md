---
title: "화이트보드 양방향 로그 갭 컴팩트 2행 레이아웃"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: ed6050eabb5acfb9
sage-review-completeness-reviewed: ed6050eabb5acfb9
---

# Whiteboard Two-Sided Log Gap Compact Two-Row Layout

## Background

In the whiteboard stream (`wswhiteboard`), when a log gap expander has omitted entries reachable from both directions (`[data-gap-dir="both"]`, e.g., when auto-fold collapses a long middle section of history), `agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl#L47` currently renders a single horizontal flex row with a left `.gap-gutter`, a left-side `.gap-info` summary (`⋯ N history entries · time`, from `agents-plugin-tool/internal/wsi18n/tables/en.json#L124`), and right-aligned `.gap-actions` holding both `<button data-load="up">` (`▲ Load above`, `en.json#L125`) and `<button data-load="down">` (`▼ Load below`, `en.json#L126`) side by side.

In the narrow `#bcol` split column (typically 380–560px wide), the horizontal bar feels cramped, packs both small action buttons on the right while leaving `.gap-gutter` and `.gap-info` as a non-clickable dead zone (`agents-plugin-tool/internal/wswhiteboard/assets/page-stream.css#L83`, `agents-plugin-tool/internal/wswhiteboard/assets/stream-navigation.js#L308-L312`), and truncates or wraps awkwardly on narrower viewports.

## Decisions

- **(1612)=a (Owner confirmed 2026-11-25):** For two-sided log gaps (`[data-gap-dir="both"]`), stack **2 full-width clickable compact rows** (`~25px` height each, total `~50px`, separated by a subtle hairline divider, with individual row hover wash and zero dead click area):
  - **Top row (`<button data-load="up">`):** Full-width button displaying the upward load label (`▲ 위쪽 더 불러오기` / localized equivalent) on the left and the muted gap metadata (`⋯ N개 로그 항목 · <time range>`) right-aligned on the same row.
  - **Bottom row (`<button data-load="down">`):** Full-width button displaying the downward load label (`▼ 아래쪽 더 불러오기` / localized equivalent) on the left.
  - **Single-sided gaps (`[data-gap-dir="up"]` and `[data-gap-dir="down"]`):** Remain unchanged as a single compact `28px` 1-row bar so the existing single-sided ergonomics and `history-autofold.cjs` expectations remain intact.
- **Compatibility tier: T0 (transparent).** Pure server-rendered HTML/CSS layout refinement inside `wswhiteboard` assets (`page.tmpl`, `page-stream.css`, and accompanying tests). No MCP tool, `/mcp` hello, shared store schema, tracker event, or manifest contract changes; existing `data-gap-dir` and `data-load="up"|"down"` DOM hooks remain unchanged.

## Constraints

- Implementation conventions manuals to read before editing (per `AGENTS.md` `### Implementation Conventions`):
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md` (especially `## Frontend Minimal Updates` — keep virtual-scroll height measurement driven by existing `freeCardRO` without introducing new forced reflows or layout thrash)
  - `ai-docs/manuals/dashboard-style.md` (borderless tonal surface tokens, subtle divider/hover wash, dark & light theme parity)
- Preserve `data-gap-dir="both"|"up"|"down"` and `button[data-load="up"|"down"]` click delegation hooks used by stream expansion JS and `history-autofold.cjs`.
- Do not change single-sided (`up` or `down`) gap height (`28px` 1-row bar) or its click behavior.
- `#bcol` virtual scroll (`dashboard-board-anchor.js` `freeCardRO`) automatically tracks the `~50px` stacked height once rendered; do not hardcode a fixed gap height assumption that conflicts with `freeCardRO`.
- **Single shared gap DOM:** One gap shell serves every direction: `syncGapAttrs` flips `data-gap-dir` between `both`/`up`/`down` purely from `button.hidden`, `refresh` rewrites `.gap-label` / `.gap-range` by `querySelector`, and `shell()` / `refold` clone and re-insert the same markup. Therefore the metadata (`.gap-info`: `.gap-label`, `.gap-range`) must stay outside the `up` button in the DOM (otherwise hiding `up` after a load-above would drop the count/range from the remaining single-sided gap). Achieve the "metadata on the top row, whole row clickable" result in the `both` layout with CSS only: place `.gap-info` over the top row and give it `pointer-events: none` so clicks land on the full-width `up` button; keep the `.gap-label` / `.gap-range` selector contract of `stream.js` and the `stream_script_test.go` fixtures. Single-sided gaps keep their current placement of the metadata.
- **Gutter in `both`:** keep `.gap-gutter` (min width stays >= 36px as `history-autofold.cjs` asserts) as a non-interactive overlay (`pointer-events: none`) so it creates no dead click area over the two rows.
- **Labels:** reuse the existing i18n keys (`wb.timeline.up` / `wb.timeline.down` / `js.wb.timeline.gap`); the Korean strings in Decisions are the ko rendering, never hardcoded.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, agents-plugin-tool/internal/wswhiteboard/assets/, ai-docs/assets/mockups/)

## Prior Decisions

- 44d2c736 (2026-11-25, commit): "Replaced sibling :has(+/~), :has(...:empty), and document-root/high-ancestor (:root, html, body, .dash, .dbody, .dwrap, .dgrid) :has() rules with explicit SSR+JS data-* attributes, hidden, or :empty" — bearing: constrains
- 0c9eed20 (2026-11-24, commit): "Preserves existing [data-load=\"up\"]/down attributes, i18n keys, lazy template hydration (<template data-history-gap> remains a direct child), WINDOW_PX autofold, keyboard accessibility, and .gap-actions" — bearing: constrains
- 261116-feat-ws-dashboard-session-page-payload-and-render-cost (2026-11-17, commit): "Hydration fetches a complete topic section and fills placeholders, so the existing gap/restore/target code runs unchanged instead of a second row-level protocol." — bearing: constrains
- 261115-feat-ws-whiteboard-topic-interleaved-timeline (2026-11-15, commit): "Preserve the landed viewKit scroll owner unchanged. Gap insertion and target navigation use keep/jump, and dashboard visible-render deferral remains its existing owner." — bearing: constrains
- 261115-feat-ws-whiteboard-history-fold-rule (2026-11-15, Decisions): "Implement after v0.50.0 and after the history auto-fold regression fix lands (branch `fix/history-autofold-loadmore-capsule`)." — bearing: supports
- 261114-refactor-ws-dashboard-whiteboard-asset-split (2026-11-15, commit): "Separator-free fragment joins preserve every upstream asset byte, one dashboard IIFE, evaluation order, CSS cascade, and the original template/registration boundaries" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl, agents-plugin-tool/internal/wswhiteboard/assets/page-stream.css, agents-plugin-tool/internal/wswhiteboard/assets/stream.js, agents-plugin-tool/internal/wswhiteboard/assets/stream-navigation.js, agents-plugin-tool/internal/wswhiteboard/stream_test.go, agents-plugin-tool/internal/wswhiteboard/stream_script_test.go |
| scope.surface | internal | no exported Go symbol or public API changes; changes are internal to wswhiteboard HTML/CSS/JS assets and tests |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none |
| scope.test_surface | existing | agents-plugin-tool/internal/wswhiteboard/stream_test.go, agents-plugin-tool/internal/wswhiteboard/stream_script_test.go, agents-plugin-tool/internal/wswhiteboard/testdata/history-autofold.cjs, agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js |
| complexity.reuse_points | confirmed | reuses data-gap-dir, button[data-load="up"|"down"], wb.timeline.up/down and js.wb.timeline.gap i18n keys, stream.js syncGapAttrs/refresh/shell, and dashboard-board-anchor.js freeCardRO |
| complexity.side_effect_risk | moderate | stream.js refresh/split and stream-navigation.js refold dynamically transition gaps between up/down and both via h._wbGapShell cloning and [data-load="down"].hidden; mismatched markup between page.tmpl and JS transitions would break refold or metadata updates |
| risk.correctness | moderate | client-side gap state transitions in stream.js and stream-navigation.js click delegation must match the 2-row both vs 1-row single-sided markup, and stream_script_test.go#L61-L63 currently asserts clicking .gap-label on a bidirectional gap is a no-op |
| risk.fit | moderate | CSS must preserve dark/light theme tokens per dashboard-style.md, -12px/-10px paint-containment margins in page-stream.css, and the static CSS :has() regression guard in wsdashboard/frontend-idle.test.js |
| risk.test | moderate | existing Go SSR (stream_test.go#L329), Node DOM (stream_script_test.go#L61-L63), Playwright (testdata/history-autofold.cjs), and static CSS (wsdashboard/frontend-idle.test.js) tests pin gap selectors and click behavior |
| risk.security_or_contract | low | pure HTML/CSS/JS layout refinement inside wswhiteboard; T0 compatibility with no MCP, store, protocol, manifest, or untrusted-input parsing changes |

## Phases

### Phase 1: Implement stacked two-row layout for two-sided whiteboard log gaps

1. **Template (`agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl:47`):**
   - For `[data-gap-dir="both"]`, present two full-width `<button>` rows (same DOM as every other direction; the layout is selected by `data-gap-dir` CSS, see Constraints "Single shared gap DOM"):
     - Top `<button data-load="up">` containing the upward action text on the left, with the muted metadata (`⋯ N개 로그 항목 · time`) right-aligned on the same visual row (overlaid via CSS, `pointer-events: none`) so the entire row is clickable with zero dead zone.
     - Bottom `<button data-load="down">` containing the downward action text.
   - Keep `[data-gap-dir="up"]` and `[data-gap-dir="down"]` rendering as the existing 1-row compact bar.
   - Audit the JS gap-updating and cloning paths in `agents-plugin-tool/internal/wswhiteboard/assets/stream.js#L35-L54,L182-L197` (`syncGapAttrs`, `refresh`, `shell`, `split`) and `agents-plugin-tool/internal/wswhiteboard/assets/stream-navigation.js#L273-L275,L303-L320,L386-L405` (`init`, click delegation, `refold`; `stream-view.js` only implements `viewKit` and does not touch `.history-gap`) so client-side created or updated two-sided gaps emit the exact same 2-row DOM structure as `page.tmpl`.
2. **Styles (`agents-plugin-tool/internal/wswhiteboard/assets/page-stream.css:78-102`):**
   - Style `[data-gap-dir="both"]` as a vertical column stack (`flex-direction: column`) of two compact `~25px` full-width buttons separated by a subtle tonal hairline border.
   - Give each button row its own hover/focus-visible tonal wash across the full width, keeping the metadata muted and right-aligned on the top row with graceful ellipsis on very narrow panes.
   - Keep `[data-gap-dir="up"]` and `[data-gap-dir="down"]` at their compact `28px` 1-row styling.
   - Verify visual contrast in both dark and light themes using canonical CSS custom properties per `ai-docs/manuals/dashboard-style.md`.
3. **Tests & Verification:**
   - Update `agents-plugin-tool/internal/wswhiteboard/stream_test.go:329` CSS/HTML assertions to match the new 2-row `[data-gap-dir="both"]` layout while asserting single-sided gaps stay 1-row.
   - Flip `agents-plugin-tool/internal/wswhiteboard/stream_script_test.go:61-63`, which currently asserts that clicking `.gap-label` on a bidirectional gap is a no-op: the metadata now sits over the clickable top row, so replace it with an assertion of the CSS `pointer-events: none` contract plus continued `up`/`down` click delegation (the synthetic click in this test cannot observe pointer routing); verify label-to-`up` routing in a real browser (`elementFromPoint` in the Playwright script).
   - Confirm the first gap in the `history-autofold.cjs` fixture is single-sided (the "passes unchanged" claim fails its same-line action assertion at `:125` if it is bidirectional) and adjust only if so.
   - Add unit/DOM regression tests covering:
     - `[data-gap-dir="both"]` renders two full-width `<button data-load="up">` and `<button data-load="down">` rows with gap metadata overlaid on the top row, no dead-zone wrapper between them, and the `up`-hidden / `down`-hidden transitions (via `syncGapAttrs`/`refold`/`split`) keep the count/range visible.
     - Single-sided `up` and `down` gaps retain 1-row markup and `history-autofold.cjs` passes unchanged.
   - Run relevant Node frontend tests (including `history-autofold.cjs`) and remote `w5090` `go vet` + `go test -race ./internal/wswhiteboard/... ./internal/wsdashboard/...`.
