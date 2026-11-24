---
summary: Apply Werkschaft's borderless Material tonal surface system, canonical CSS tokens, interaction patterns, and dark-theme mockup rules when styling dashboard or whiteboard UI.
---

# Dashboard & Mockup Visual Style Guide

This manual defines the visual language for the Werkschaft dashboard
(`agents-plugin-tool/internal/wsdashboard/assets/`), the whiteboard
(`agents-plugin-tool/internal/wswhiteboard/assets/`), and standalone HTML/SVG
mockups (`ai-docs/assets/mockups/`). All token values below are sourced from
`page.css`, `dashboard.css`, `dashboard-shell.css`, `dashboard-rail.css`,
`dashboard-conversation.css`, `dashboard-exec-hud.css`, `dashboard-board.css`,
`dashboard-drawer.css`, and `tracker.css`.

## 1. Design Philosophy: Seamless, Low-Contrast, Borderless Material Tonal Surfaces

- **Hierarchy through tonal elevation and soft shadows, not outlines.** Separate
  canvas, cards, headers, controls, and recessed wells by stepping along the
  tonal surface ladder (`--wb-bg` -> `--wb-card` -> `--wb-muted` / `--wb-topic`)
  paired with `--wb-shadow` or `--wb-shadow-hi`. Do not wrap every card, row,
  or button in a high-contrast `1px` border.
- **Calm, readable ink hierarchy.** Use `--wb-fg` for primary text and
  `--wb-muted-fg` for metadata, timestamps, and secondary labels. Reserve bold
  weights (`700`–`800`) for titles, compact chips, and status markers.
- **Quiet `color-mix()` washes for state.** Derive hover fills, selection
  tints, and semantic callouts with `color-mix(in srgb, ...)` over `--wb-card`
  or `transparent` (typically `6%` for neutral hover washes and `12–16%` for
  primary or semantic tints).
- **Restrained accent usage.** Reserve `--wb-primary` for active selection,
  focus rings, and primary actions. Keep semantic colors (`--wb-ok`,
  `--wb-warn`, `--wb-fail`, `--wb-hot`) muted and purposeful so status signals
  stand out without visual noise.

### Anti-Patterns to Avoid

- **Boxy borders on individual rows or buttons.** Never surround list items,
  menu rows, or inline action buttons with `1px solid` outlines.
- **Stark `#fff` text on dark backgrounds.** Use `--wb-fg` (`#e7e7e4`), not
  pure `#ffffff`, for body ink in dark mode.
- **Garish neon or full-saturation outlines.** Avoid `100%`-opacity bright
  borders around cards, badges, or containers; use tonal fills or soft
  `color-mix()` hairlines.
- **Light backgrounds in dark mockups.** Never use `#fafafa` or `#fff` as a
  mockup canvas; whiteboard mockups must match the dark tonal surface ladder.
- **Bulky multi-row text action buttons.** Do not let verbose text buttons wrap
  onto extra rows in dense headers or list rows when compact `24px` icon-only
  buttons with clear tooltips fit inline.

## 2. Canonical Design Tokens (Light & Dark)

Shared surface, ink, shadow, and typography tokens live in
`wswhiteboard/assets/page.css` (`:root` for light; `@media (prefers-color-scheme: dark)`
and `:root[data-theme="dark"]` for dark). Dashboard-specific status, phase, and
HUD tokens extend them in `wsdashboard/assets/dashboard.css`.

### Core Tonal Surface Ladder

| Token / Role | Light | Dark | Usage |
|---|---|---|---|
| `--wb-bg` | `#f3f3f0` | `#141518` | Page canvas, drawer sheet backdrop |
| `--wb-card` | `#ffffff` | `#1e2024` | Elevated cards, rail, composer, popovers, dialogs |
| `--wb-muted` | `#e9e8e3` | `#2b2d32` | Hover surfaces, header strips, chips, recessed tracks |
| `--wb-topic` / `--wb-on-topic` | `#2c3038` / `#ffffff` | `#2a2d33` / `#e4e5e8` | Filled topic and workspace title bars |
| Recessed input well (dark convention) | — | `#16181c` | Deep recessed well inside `#1e2024` cards (intermediate hover/chip step: `#25272c`) |

### Ink, Hairline & Neutral Wash

| Token | Light | Dark | Usage |
|---|---|---|---|
| `--wb-fg` | `#1b1d21` | `#e7e7e4` | Primary body ink |
| `--wb-muted-fg` | `#5f646b` | `#a2a6ad` | Secondary text, metadata, inactive icons |
| `--wb-line` | `color-mix(in srgb, var(--wb-fg) 16%, transparent)` | same | Whisper dividers and floating-popover edge definition |
| `--wb-wash` | `color-mix(in srgb, var(--wb-fg) 6%, transparent)` | same | Subtle hover/selection wash over cards |

### Primary, Semantic, Role & Phase Tokens

| Token | Light | Dark | Usage |
|---|---|---|---|
| `--wb-primary` / `--wb-primary-fg` | `#2b62d9` / `#ffffff` | `#7aa2ff` / `#0c1424` | Primary action, active focus, links |
| `--wb-now` / `--wb-on-now` | `var(--wb-primary)` / `var(--wb-primary-fg)` | `#243457` / `#d3defb` | Live/current banner fill |
| `--wb-ok` | `#1d7348` | `#6cc39a` | Success, healthy, completed, diffs added |
| `--wb-warn` / `--wb-warn-ink` | `#c27a00` / `#a35f00` | `#e0a36c` / `#e0a36c` | Warning, pending admission, dirty/behind state |
| `--wb-fail` | `#c0262d` | `#f0797e` | Error, destructive action, diffs removed |
| `--wb-hot` | `#d95d16` | `#fb923c` | High-heat / elevated usage accent |
| `--wb-policy` / `--wb-on-policy` | `#b25000` / `#ffffff` | `#5b3a1c` / `#f6d9bd` | Policy/decision chips (`--wb-accent-policy`: `#b25000` / `#e0a36c`) |
| `--wb-technical` / `--wb-on-technical` | `#2457c5` / `#ffffff` | `#26385e` / `#d2dcf6` | Technical/ok chips and wake accents |
| `--wb-announced` / `--wb-on-announced` | `#1d7348` / `#ffffff` | `#1f4433` / `#c6ead6` | Announced / answered decision pills |
| `--wb-accent-assumed` | `#b0245a` | `#e48aae` | Assumed-answer highlight |
| `--wb-ph-think` / `--wb-on-ph-think` | `#6a4fc4` / `#ffffff` | `#3d2f6b` / `#ddd3fb` | Agent phase: thinking |
| `--wb-ph-reply` / `--wb-on-ph-reply` | `#0e7a83` / `#ffffff` | `#1b4a4f` / `#c5ecef` | Agent phase: replying |
| `--wb-ph-tool` / `--wb-on-ph-tool` | `#2457c5` / `#ffffff` | `#26385e` / `#d2dcf6` | Agent phase: tool execution |
| `--wb-ph-perm` / `--wb-on-ph-perm` | `#a35f00` / `#ffffff` | `#55400f` / `#f6dfae` | Agent phase: awaiting permission |
| `--wb-ph-compact` / `--wb-on-ph-compact` | `#9a3a73` / `#ffffff` | `#4f2342` / `#f5d2e7` | Agent phase: compacting |
| `--hud-glass` | `color-mix(in srgb, var(--wb-card) 82%, transparent)` | `color-mix(in srgb, var(--wb-card) 78%, transparent)` | Translucent HUD surface (`--hud-line`: `14%` / `16%`, `--hud-conn`: `28%` / `30%` of `--wb-fg`) |

### Elevation, Layers, Radius & Typography

- **Shadows (`page.css`):**
  - `--wb-shadow`: `0 1px 2px rgb(0 0 0 / .14), 0 2px 6px rgb(0 0 0 / .06)` (light); `0 1px 2px rgb(0 0 0 / .5), 0 2px 8px rgb(0 0 0 / .3)` (dark) — resting cards.
  - `--wb-shadow-hi`: `0 2px 4px rgb(0 0 0 / .16), 0 6px 18px rgb(0 0 0 / .10)` (light); `0 2px 4px rgb(0 0 0 / .55), 0 8px 22px rgb(0 0 0 / .4)` (dark) — popovers, composer, hanging handles, modals.
- **Z-index layers (`dashboard-shell.css`):**
  `--z-cards: 1`, `--z-panels: 40`, `--z-popovers: 60`, `--z-modal: 70`.
- **Radius scale:**
  - `12px` (`--wb-radius`): top-level cards (`.topic`, `.bc-in`, `.rail`, `.convo`, `.composer`, `.xpeek`, dialogs).
  - `10px`: popovers and floating menus (`.gmenu`, `.rpop`, `.tpop`, `.xtip`), composer input well (`.cfield`), nested sub-cards (`.apcard`, `.dfile`).
  - `8px`: menu action buttons (`.gmenu button`, `.spact`), top-bar hanging handles (`0 0 8px 8px`), segmented tracks.
  - `6–7px`: list/menu rows (`.rs`, `.rmi`, `.ir-main`, `.topt`), compact icon buttons (`.ibtn`, `.rref`, `.xibtn`), `24px` HUD nodes (`.hud-node`, `.xnode`).
  - `5px`: micro badges, keycaps, inline ref pills (`.rf`, `.gk`, `button.rp`).
  - `999px`: pills and capsules (`.chip`, `.phase`, `.sbadge`, `.xchip`, `.ibadge`, `.oq-pill`, `.scopes`).
- **Typography:**
  - `--wb-sans`: `system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans KR", sans-serif`
  - `--wb-mono`: `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace`
  - Scale: `14px` base (`14px/1.5` body, topic titles `.tbar .tname`, dialog headers); `13px` panel headers (`.thead`), menu rows (`.rmi`, `.gmenu button`), conversation rows; `12.5px` dense rows (`.rail .rid`, `.glr`, `.bc-hd`, `.rpop`); `11.5px` metadata, badges, timestamps (`.sbadge`, `.xchip`, `.msg .mh`); `11px` uppercase tracked section labels (`font-weight: 700–800; text-transform: uppercase; letter-spacing: .04em`, e.g. `.rpop .splbl`, `.glcols`, `.tk .sub`).
  - Apply `font-variant-numeric: tabular-nums` to all timers, counters, percentages, and timestamps.

## 3. Component & Interaction Patterns

### Cards & Popovers (`.topic`, `.bc-in`, `.gmenu`, `.rpop.rmenu`, `.xpeek`)

- Use `background: var(--wb-card)` with `border-radius: var(--wb-radius)` (cards,
  `.xpeek`) or `10px` (popovers `.gmenu`, `.rpop.rmenu`) and `--wb-shadow` /
  `--wb-shadow-hi`.
- Omit outer `border` on cards. When a popover floats over a same-tone surface
  (`.rpop`, `.xpeek`), add at most a whisper `inset 0 0 0 1px var(--wb-line)`
  alongside `--wb-shadow-hi`.

### List Rows & Menus (`.rmi`)

- Keep rows flat and borderless at rest (`border: 0; background: transparent;
  border-radius: 6–7px;`), lifting smoothly on `:hover` or `:focus-visible` via
  `--wb-muted` (menus such as `.rpop .rmi`, `.gmenu button`) or `--wb-wash`
  (dense tables such as `.glr`).
- Active/selected rows use a soft primary tint
  (`color-mix(in srgb, var(--wb-primary) 12–14%, var(--wb-card))`) rather than a
  heavy outline.

### Compact Controls & Pills (`.sbadge`, `.xchip`, `.rp`, icon buttons)

- **Floating status pills (`.sbadge`, `.xchip`, `.ibadge`, `.bgbadge`):**
  `height: 24px; border: 0; border-radius: 999px; background: var(--wb-card);`
  with `--wb-shadow-hi` and a subtle semantic `inset 0 0 0 1.5px color-mix(...)`
  ring when active.
- **Inline row parts (`button.rp`) & icon buttons (`.ibtn`, `.rref`, `.xibtn`):**
  `border: 0; background: transparent;` at rest (`22–24px` square for icon
  buttons), gaining a quiet tonal fill (`--wb-muted` or `10%` primary tint) on
  hover/active. Prefer compact icon-only action buttons with clear tooltips over
  multi-word text buttons that force row wrapping.

### Inputs & Inline Editing

- Style text inputs and textareas as borderless recessed wells (`border: 0`,
  `background: var(--wb-muted)` or `#16181c` in dark-surface mockups) with a
  subtle focus ring (`outline: 1.5px solid var(--wb-primary)` or
  `color-mix(in srgb, var(--wb-primary) 55%, transparent)`).
- Temporary mode overrides provide an instant one-click revert (`↺`, e.g.,
  `.csend-revert` on multiline composer mode) rather than burying reset inside a
  menu.

### Destructive Actions — 1.5s Hold-to-Confirm (`hold-confirm.js`)

- Destructive or unsafe actions (unsafe process stop, archive, close, delete)
  use `WsHoldConfirm` (`hold-confirm.js`, `DEFAULT_DURATION = 1500ms`) instead
  of jarring confirmation modals:
  - **Icon buttons (`.rail .drstop.d-stop-unsafe`):** inside-out radial red fill
    (`::before` circle scaling `0 -> 1` via `d-hold-radial`, solid `--wb-fail`
    when `.d-hold-ready`).
  - **Menu rows (`.rpop .spact.d-hold-btn`):** horizontal left-to-right progress
    bar (`::before` `scaleX(0 -> 1)` via `d-hold-bar` with `--wb-fail` tint for
    `.danger`).
- Honor `prefers-reduced-motion: reduce` (static translucent fill while holding,
  full opacity when ready), support keyboard `Space`/`Enter`, and cancel cleanly
  on pointer leave, `Escape`, `blur`, or tab visibility change.

### Sticky Headers inside Rounded Containers

- Put `border-radius: var(--wb-radius)` and `overflow: hidden; overflow: clip;`
  on the outer card container (`.bc-in`, `.wb-rail .topic`, `.gal-pane > section.topic`).
  `overflow: clip` clips rounded corners without creating a scroll container, so
  `position: sticky` continues to stick to the outer scroller (whereas
  `overflow: hidden` alone breaks `position: sticky`).
- Do **not** add redundant top `border-radius` to a sticky header inside a
  clipped rounded container — it causes corner-horn bleed when scrolled. (Only
  unclipped headers that stick against the page background mask their corners
  with a spread shadow in `--wb-bg`, as `.wb-stuck` does.)

## 4. Whiteboard HTML/SVG Mockup Checklist

Before saving or posting any mockup panel (`wb:panel src="..."`):

1. **Dark theme tonal ladder only.** Use `#141518` (`--wb-bg`) -> `#1e2024`
   (`--wb-card`) -> `#25272c` / `#2b2d32` (hover / `--wb-muted`) -> `#16181c`
   (recessed input well), with `#e7e7e4` primary ink and `#a2a6ad` muted ink.
   Never use `#fafafa` or `#fff` page/card backgrounds.
2. **Borderless Material tonal rules.** Apply every surface, shadow, pill, row,
   and anti-pattern rule from Sections 1–3 so mockups match the live Werkschaft
   dashboard without visual translation.
3. **Self-contained fragment contract.**
   - Pure HTML fragment: no `<!doctype>`, `<html>`, `<head>`, or `<body>`.
   - Strictly scoped class prefix unique to the mockup so styles never leak.
   - Zero external URLs, remote fonts, or `<script>` tags.
   - Total size `<= 2 MiB`.
4. **Canonical storage path.** Save under
   `ai-docs/assets/mockups/YYMMDD/<topic-name>/` and reference from the
   whiteboard via `wb:panel src="..."`.
