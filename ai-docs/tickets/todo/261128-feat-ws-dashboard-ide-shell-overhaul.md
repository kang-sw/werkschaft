---
title: "대시보드 외곽 쉘 IDE 스타일 개편(D안·하단 전역 상태바·상단 구역 분리)"
related:
  261119-feat-ws-dashboard-shell-topbar-rail-redesign: prior topbar and full-height rail shell baseline superseded by Variant D outer chrome
  261119-feat-ws-dashboard-paged-docking-gallery: Band 4 gallery (#bgal) coexistence invariant; Track 2 always-open gallery rework is separate
  261127-feat-ws-harness-account-switcher-and-slot-continuity: preserves .usage-toggle 1-click account switcher popover contract when relocated to bottom-right status bar
---

# Dashboard Outer Shell IDE-Style Overhaul (Variant D, Global Bottom Bar, and Topbar Zone Split)

## Background

The Werkschaft dashboard currently mixes floating cards, a top bar that carries both per-session identity and global usage/alert chrome, and a left rail whose footer holds global navigation (`Home`, global settings, and `#gear`) directly competing for vertical space with the session tree and VRM Companion dock. During visual exploration (`dashboard-shell-ide-style`), the owner evaluated four shell archetypes and a card-harmony matrix and settled on **Variant D** (seamless IDE outer chrome wrapping an inset rounded stage well) paired with a dedicated **26px Global Bottom Status Bar** and a clear **Per-Session / Centered Search / Global** topbar architecture.

This ticket delivers **Track 1** (the outer shell overhaul). Always-open Paged Docking Gallery (`#bgal`), migrating Ticket Map (`#thandle`) into `#bgal`, and reworking `#drawer` workpanels (`todo`, `agenda`, `tickets`, `mailbox`, `worktrees`, `holders`, `notes`, `git`) are intentionally split into a separate Track 2 design effort so the outer shell can land cleanly without blocking on gallery rework.

## Decisions

- **Compatibility tier: T0 transparent.** All changes are limited to dashboard frontend templates, stylesheets, client-side JavaScript, tests, and `ai-docs/manuals/dashboard-style.md`. No MCP tool schemas, `/mcp` hello payloads, tracker wire formats, or on-disk store shapes change.
- **Variant D outer chrome + inset rounded stage well (`(1707)=a`, `(1710)=d`).** Render a continuous outer frame surface (`#111216` dark / `#e7e7e2` light) unifying the `36px` topbar, collapsible & drag-resizable (`220px–360px`, default `280px`) left session rail, and `26px` bottom status bar with no harsh `1px` grid dividers between chrome bands. Wrap the inner workspace stage in an inset rounded `12px` well (`#16181d` dark / `#f4f4f1` light, subtle `1px` rim, `6px` right margin, and `6px` left margin when the rail is collapsed) so existing `12px` Material tonal chat and whiteboard cards sit inside unchanged.
  - *Rejected:* Variant A (VS Code 0px sharp full-bleed gridlines), Variant B (Arc/Linear floating rounded rail and stage cards), Variant C (JetBrains/Zed dense flat toolbar), Variant E (flattening inner cards to match sharp IDE borders), Variant F (double-boxed cards with drop shadows).
- **26px Global Bottom Status Bar & bottom-right `.usage-toggle` (`(1708)=a`).** Add a seamless `26px` bottom bar structured as a 3-column CSS Grid (`minmax(0, 1fr) auto minmax(0, 1fr)`) holding strictly **Global Only** controls:
  - **Bottom-Left:** `.rail-home` (Home), `.r-config-global` (Global Settings), and `#gear` → `#gmenu` (upward-opening, viewport-clamped popover for Theme, Motion, Input, Language `#glang`, Login link copy, and Settings link), retiring `.rfoot` from desktop `.rail-session-panel` so the rail maximizes vertical room above `#rail-companion-slot`.
  - **Bottom-Center:** `#talert` cross-session global alert strip, centered at `0px` horizontal offset, gracefully collapsible when no cross-session alert is active.
  - **Bottom-Right:** `.usage-toggle` ("Plan usage" + 1-click account switcher popover), preserving its DOM anchor, SSE `account-changed` updates, and switcher contents while flipping popover placement **upward** (viewport-clamped above the `26px` bar).
  - *Rejected:* Keeping `.usage-toggle` in the topbar (`(1708)=b`); moving `.usage-toggle` into the left rail (`(1708)=c`); retaining `.rfoot` inside the desktop rail alongside the bottom bar.
- **Scoped chrome hairline exception & tonal active rail row (`(1709)=a`).** Document an explicit scoped exception in `ai-docs/manuals/dashboard-style.md` permitting a subtle `1px` rim around the Variant D inset stage well and outer shell chrome boundaries while preserving the borderless Material tonal surface rule for all inner cards and panels. Render active session rows in `#rlist` with tonal fill and a clean left accent bar.
  - *Rejected:* Pure zero-hairline stage well with low edge contrast; neon glow / boxed outline active rail rows.
- **Topbar zone split: Per-Session Left + Centered Search + Global Right (`(1711)`, `(1713)=c`).** Structure the `36px` topbar as a 3-column CSS Grid (`minmax(0, 1fr) auto minmax(0, 1fr)`):
  - **Top-Left (Per-Session):** `#railbtn` (rail collapse toggle, `Ctrl+\``), current session identity (`.tph-parent`, `.sren`, `.snm`, `.sseq`, `.child-close`), and per-session live telemetry — worktree branch badge (when git-backed), harness/host badge, and `#sse` Live stream indicator. On non-session pages (Home/Index, Settings), per-session telemetry hides cleanly without JS errors.
  - **Top-Center (True Visual Center):** `#qsslot` / `#tkqs` / `#shandle` IDE-style centered search trigger pill (`⌘K` / `Ctrl+K`), responsive across `1200px`–`1920px+` without colliding with left or right clusters.
  - **Top-Right (Global Zone + Temporary Legacy Session Handles):** Keep daemon-global tracker store handles `#tkhandles` (`.tk-handle` + `#tkadd`) in Top-Right so `#tkdrawer` continues sliding smoothly from the right edge. Preserve existing `#drhandle`, `#ghandle`, and `#thandle` beside them without behavioral regression during Track 1 until Track 2 migrates session surfaces into `#bgal`.
  - *Rejected:* Moving `#tkhandles` into the `26px` bottom bar (`(1713)=a`) or left rail (`(1713)=b`).
- **Track 1 / Track 2 boundary (`(1714)=a`).** Do not change `#bgal` always-open semantics, Ticket Map `#tmback` modal behavior, or `#drawer` workpanel internals in this ticket; those belong to the separate Track 2 design and implementation ticket.

## Constraints

- **Implementation Conventions manuals (must be read before editing):**
  - `ai-docs/manuals/dashboard-style.md` — borderless Material tonal tokens, Dark/Light parity, plus the `(1709)=a` scoped outer-chrome hairline exception.
  - `ai-docs/manuals/performance.md` — Frontend Minimal Updates: no per-frame layout thrash, RAF-throttled splitter drag via CSS custom properties, no unnecessary DOM rebuilds on SSE updates.
  - `ai-docs/manuals/shipped-surface-boundary.md` and `ai-docs/manuals/release-compatibility.md` — T0 compatibility verification.
- **Approved visual reference:** `ai-docs/assets/mockups/261127/dashboard-shell-d-layout/index.html` (with sibling `styles.css` and `app.js`). Note one deliberate delta from the mockup's Preset 1: per `(1713)=c`, `#tkhandles` (`.tk-handle` + `#tkadd`) remain in Top-Right (Global Zone) alongside temporary `#drhandle`/`#ghandle`/`#thandle` rather than moving to the bottom bar.
- **Left-rail VRM Companion contract (`(148)=a`, landed in `344452e44` & `b326952ee`):** Preserve `.rail[data-companion-dock="rail-slot"]` (`>= 1600px`) flex-column layout with `.rail-session-panel` (`flex: 1 1 0; min-height: 0`) above fixed-height `#rail-companion-slot` (`flex: 0 0 auto; width: 100%; box-sizing: border-box`). Expanding/collapsing `details.rws-unreg` or scrolling `#rlist` must keep `0px` vertical jitter on `#rail-companion-slot`; dragging the rail splitter across `220px–360px` must smoothly resize `#rail-companion-slot` and wake the idle VRM loop in `dashboard-companion-stage.js`; the `document.body`-mounted Companion Messenger popup must remain unclipped by outer shell overflow rules.
- **Usage & Account Switcher contract (`261127-feat-ws-harness-account-switcher-and-slot-continuity`, landed in `83b6741c7`):** Preserve `.usage-toggle` trigger/popover DOM selectors, `account-changed` SSE updates, and 1-click account switching inside the upward-anchored bottom-right popover.
- **Responsive breakpoints & gallery coexistence:** Preserve the `1600px` `WIDE` threshold (`gallery.js`), Band 4 `canFitFourCol` math (`cards.js`), `data-pincol`, narrow `< 1600px` slide-over rail with scrim, and `drRoom()` / `--drhw` / `--tkhw` layout calculations so Top-Right handles and centered search never overlap at `1200px`, `1440px`, or `1920px+`. Keep global utilities (`Home`, global settings, `#gear`) reachable on narrow viewports as well.
- **Non-session pages:** Index/Home, Settings (`?section=...`), and Worktree pages must render cleanly inside the Variant D outer shell and bottom status bar without broken selectors or orphan spacing.
- **Modular JS/CSS hygiene:** Avoid further inflating oversized monoliths (`dashboard-rail.css`, `dashboard-rail.js`, `dashboard-leads.js`); isolate new shell/splitter/bottom-bar CSS and JS cleanly where appropriate.

## Phases

### Phase 1: Implement Variant D IDE Outer Shell, Global Bottom Status Bar, and Topbar Zone Split

Implement the complete Track 1 outer shell transformation across the dashboard templates, stylesheets, client scripts, and `ai-docs/manuals/dashboard-style.md`:

1. **Variant D Outer Chrome & Inset Stage Well:**
   - Apply the continuous outer chrome background (`#111216` dark / `#e7e7e2` light) across topbar, left rail, and bottom status bar, and frame the inner stage (`#frames` / `.dbody` / stage container) inside the `12px`-rounded inset well (`#16181d` dark / `#f4f4f1` light) with the subtle `1px` rim and `6px` outer breathing margins (`6px` right; `6px` left when rail collapses).
   - Add a smooth, persisted (`localStorage`), RAF-throttled drag splitter on the left rail (`220px–360px`, default `280px`) that updates rail width via CSS custom property while keeping `#rail-companion-slot` (`width: 100%`) and `canFitFourCol` / `placeGallery` synchronized.
   - Style active session rows in `#rlist` with tonal background fill + left accent bar (`(1709)=a`).
   - Update `ai-docs/manuals/dashboard-style.md` to record the `(1709)=a` scoped hairline exception for outer shell chrome and the inset stage well.
2. **Topbar (`36px`, 3-Column Grid):**
   - Left cluster: `#railbtn`, session header region (`.tph-parent`, `.sren`, `.snm`, `.sseq`, `.child-close`), plus live per-session branch badge (for git-backed sessions), harness/host badge, and `#sse` Live stream indicator.
   - Center cluster: Centered search trigger pill (`#qsslot` / `#tkqs` / `#shandle`, `⌘K` / `Ctrl+K`) centered via 3-column grid with graceful shrinking on `1200px`.
   - Right cluster: Global `#tkhandles` (`.tk-handle` + `#tkadd`) plus preserved legacy `#drhandle`, `#ghandle`, and `#thandle`, updating `drRoom()` / `--drhw` / `--tkhw` so handles stay aligned and never collide with centered search.
3. **Global Bottom Status Bar (`26px`, 3-Column Grid):**
   - Left cluster: Relocate `.rail-home`, `.r-config-global`, and `#gear` → `#gmenu` from `.rfoot` into the bottom-left global bar, flipping `#gmenu` to open **upward** and clamp within the viewport.
   - Center cluster: Relocate `#talert` cross-session alert strip into bottom-center, supporting both empty and active cross-session alert states cleanly.
   - Right cluster: Relocate `.usage-toggle` into bottom-right with **upward**, viewport-clamped popover anchoring while preserving all `83b6741c7` 1-click account switcher and usage card behaviors.
4. **Verification:**
   - Go tests & cross-platform vet: `go test ./internal/wsdashboard/...`, `GOOS=windows go vet ./...`.
   - JS unit tests: run relevant Node test suites under `agents-plugin-tool/internal/wsdashboard/` (including `usage.test.js`, companion tests, drawer/rail tests).
   - Playwright browser & visual acceptance: verify at `1200×800`, `1440×900`, and `1920×1080` in both **Dark** and **Light** themes:
     - `0px` horizontal overflow; centered search and `#talert` alignment with no overlap against left/right clusters.
     - Left-rail drag resize (`220px` ↔ `360px`) + collapse/expand + `0px` vertical jitter on `#rail-companion-slot` when toggling `details.rws-unreg` or scrolling `#rlist`.
     - Upward popover clamping for both `#gmenu` (bottom-left) and `.usage-toggle` + 1-click account switcher (bottom-right).
     - Non-session pages (Home/Index, Settings) and Band 4 (`#bgal`) coexistence.
     - Side-by-side visual capture comparison against the approved visual reference `ai-docs/assets/mockups/261127/dashboard-shell-d-layout/index.html` (Dark and Light), explicitly confirming parity and the intentional `(1713)=c` Top-Right `#tkhandles` delta.
