---
title: "화이트보드 양방향 로그 갭 컴팩트 2행 레이아웃"
---

# Whiteboard Two-Sided Log Gap Compact Two-Row Layout

## Background

In the whiteboard stream (`wswhiteboard`), when a log gap expander has omitted entries reachable from both directions (`[data-gap-dir="both"]`, e.g., when auto-fold collapses a long middle section of history), `agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl:47` currently renders both `<button data-load="up">` (`▲ Load older above`), a middle summary label (`⋯ N log entries · time`), and `<button data-load="down">` (`▼ Load newer below`) horizontally inside a single flex row.

In the narrow `#bcol` split column (typically 380–560px wide), the horizontal 3-piece bar feels cramped, splits clickable targets across opposite corners with a non-clickable dead middle zone, and truncates or wraps awkwardly on narrower viewports.

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

## Phases

### Phase 1: Implement stacked two-row layout for two-sided whiteboard log gaps

1. **Template (`agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl:47`):**
   - For `[data-gap-dir="both"]`, render the gap container with two full-width `<button>` rows:
     - Top `<button data-load="up">` containing the upward action text on the left and the muted metadata span (`⋯ N개 로그 항목 · time`) right-aligned inside the button so the entire row is clickable with zero dead zone.
     - Bottom `<button data-load="down">` containing the downward action text.
   - Keep `[data-gap-dir="up"]` and `[data-gap-dir="down"]` rendering as the existing 1-row compact bar.
   - Audit any JS gap-rendering / incremental DOM updater in `agents-plugin-tool/internal/wswhiteboard/assets/` (e.g., `stream-view.js` or gap builders) so client-side created or updated two-sided gaps emit the exact same 2-row DOM structure as `page.tmpl`.
2. **Styles (`agents-plugin-tool/internal/wswhiteboard/assets/page-stream.css:78-102`):**
   - Style `[data-gap-dir="both"]` as a vertical column stack (`flex-direction: column`) of two compact `~25px` full-width buttons separated by a subtle tonal hairline border.
   - Give each button row its own hover/focus-visible tonal wash across the full width, keeping the metadata muted and right-aligned on the top row with graceful ellipsis on very narrow panes.
   - Keep `[data-gap-dir="up"]` and `[data-gap-dir="down"]` at their compact `28px` 1-row styling.
   - Verify visual contrast in both dark and light themes using canonical CSS custom properties per `ai-docs/manuals/dashboard-style.md`.
3. **Tests & Verification:**
   - Update `agents-plugin-tool/internal/wswhiteboard/stream_test.go:329` CSS/HTML assertions to match the new 2-row `[data-gap-dir="both"]` layout while asserting single-sided gaps stay 1-row.
   - Add unit/DOM regression tests covering:
     - `[data-gap-dir="both"]` renders two full-width `<button data-load="up">` and `<button data-load="down">` rows with gap metadata inside the top button and no dead-zone wrapper between them.
     - Single-sided `up` and `down` gaps retain 1-row markup and `history-autofold.cjs` passes unchanged.
   - Run relevant Node frontend tests (including `history-autofold.cjs`) and remote `w5090` `go vet` + `go test -race ./internal/wswhiteboard/... ./internal/wsdashboard/...`.
