# Plan: Overview Summary Card Preview Shape (`(1525)=a`)

## Goal
Preserve clean 4-corner rounded-rect geometry and pixel-identical summary card shape during Overview ("한 눈에 보기") hover / Quick Look preview on branch `impl/develop/overview-preview-shape` (T0 transparent, frontend-only).

## Changes
1. **`agents-plugin-tool/internal/wsdashboard/assets/dashboard-board.css`**
   - Change Overview `section.topic[data-topic]` rule from `overflow-y: visible;` to `overflow-y: clip;` so `border-radius: var(--wb-radius)` clips all 4 corners without creating a scroll container.
   - Add `:root[data-overview] .r-board .tophead { box-shadow: none; }` to suppress the sticky-mask negative shadow (`0 -12px 0 var(--wb-bg)` from `page-items.css`) that painted over the card above.
   - Replace the `1.5px dashed` border and protruding `outline: ...; outline-offset: -1px;` on Overview hover/focus and `.overview-placeholder` with a clean inset primary ring (`box-shadow: inset 0 0 0 1.5px var(--wb-primary), var(--wb-shadow)` plus an inset `::after` ring overlay at `z-index: 2` so opaque header backgrounds do not occlude it while `.overview-here::before` stays on top at `z-index: 3`).
   - Style `.overview-placeholder` with `padding: 0; border: 0; border-radius: var(--wb-radius); overflow: clip; background: var(--wb-card);` and remove the obsolete `.overview-placeholder-head/title/badge/recap` rules.

2. **`agents-plugin-tool/internal/wsdashboard/assets/overview.js`**
   - Update `buildPlaceholder(node, height)` to clone the visible Overview summary shell instead of synthesizing stripped plain-text divs:
     - For `section.topic`: clone `:scope > .tophead` (`cloneNode(true)`), recursively strip `id` attributes, mark `inert = true` and `aria-hidden="true"`, and place inside `.overview-placeholder.overview-previewing.topic` (preserving `.quiet`, `.closed`, `.overview-here`, and CSS-parity attributes `data-state`, `data-size`, `data-board-held` without copying `data-topic`).
     - For `.bcard`: clone `:scope > .bc-in > .bc-hd` inside `.bc-in`, strip `id` attributes, mark `inert = true` and `aria-hidden="true"`, and place inside `.overview-placeholder.overview-previewing.bcard` (preserving `.overview-here` and CSS-parity attributes `data-kind`, `data-file-state`, `data-exited`, `data-size` without copying `data-card`).

3. **`agents-plugin-tool/internal/wsdashboard/testdata/overview-preview-shape.test.cjs`**
   - Add offline `node --test` unit tests verifying `buildPlaceholder` summary-shell cloning, `inert` / `aria-hidden`, duplicate-`id` stripping, live-node preservation, and CSS contract invariants.

## Verification
- Run local Node tests (`node --test`) on `overview-preview-shape.test.cjs` and related board/overview offline tests.
- Run remote Go tests on `w5090` (`go test ./internal/wsdashboard -run "TestDashboardAsset|TestOverview|TestGallery"`).
- Commit cleanly on `impl/develop/overview-preview-shape` via `ws/git.commit` including this plan file.
