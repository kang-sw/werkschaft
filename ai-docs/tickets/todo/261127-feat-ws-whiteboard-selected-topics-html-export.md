---
title: "화이트보드 선택 토픽 정적 단일 HTML 스냅샷 내보내기"
related:
  261118-feat-ws-whiteboard-zip-handoff: existing full-board .zip export for daemon/session takeover
  261119-feat-ws-whiteboard-frame-css-isolation: doc-frame CSS scoping affecting framed mockup snapshots
---

# Whiteboard Selected-Topics Static Standalone HTML Snapshot Export

## Background

Users want to share one or more whiteboard topics with colleagues, over email, or with external reviewers as a clean, readable standalone document — without handing over a raw `.zip` board archive or requiring a running Werkschaft daemon. Existing board export (`261118-feat-ws-whiteboard-zip-handoff`) packages raw board files for daemon/session takeover (`whiteboard.load`), which is not directly viewable in a browser offline.

## Decisions

- **Export mechanism — settled `(1738)=a`** (owner decision on whiteboard topic `whiteboard-html-dump`, 2026-11-27):
  - **Chosen:** Client-side selected-card expansion -> static single `.html` snapshot (~1–1.5d scope). The user selects one or more visible topic cards on the whiteboard, the client expands/loads their panels as needed, and serializes already-rendered Mermaid SVGs, `doc-frame` HTML snapshots, and inlined images/styles into a single self-contained static `.html` file that opens cleanly offline in any modern browser (Chrome, Edge, Safari, Firefox).
  - **Rejected alternatives:**
    - Go server-side SSR endpoint rendering a single `.html` (~2–3d+; requires bundling/re-rendering Mermaid ~3.41 MiB and offline frames server-side).
    - Tauri WebView2-only `.mhtml` (`Page.captureSnapshot`) — restricted to Chromium/Edge, unsupported in standard Safari/Firefox browser tabs, and frequently blocked by corporate mail gateways.
    - No ticket / research-only close.

## Constraints

- **Visual & theme fidelity (`ai-docs/manuals/dashboard-style.md`):** Exported snapshots must preserve the borderless Material tonal surface ladder (`--wb-bg`, `--wb-card`, `--wb-muted`, `--wb-topic`, `--wb-fg`, `--wb-muted-fg`, `--wb-primary`, and semantic/chip tokens) across light and dark themes.
- **Performance & zero blocking daemon I/O (`ai-docs/manuals/performance.md`):** Interactive export stays off blocking daemon file I/O; client-side serialization works from browser DOM and existing fetched board/panel data, avoiding daemon disk and CPU churn.
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):** Exported snapshot chrome and whiteboard UI labels must remain generic and downstream-safe.
- **Release compatibility (`ai-docs/manuals/release-compatibility.md`):** T0 transparent — purely client-side serialization (or an optional additive static export helper if ever needed) leaves stored board schemas, existing routes, and older binaries unaffected.

## Promotion-Time Technical Planning Notes

The exact implementation choices below are intentionally deferred for concrete planning at `ready/` promotion (not settled decisions yet):

- **Topic selection UX:** How selection is entered and cleared on whiteboard cards and the toolbar (e.g., selection toggle/checkboxes on visible cards, selected count, export button placement).
- **Lazy-loaded panel hydration:** Ensuring collapsed or unhydrated panels inside selected topic cards are expanded/loaded before DOM capture so exported topics are complete.
- **SVG, CSS, and data-URI inlining:** Embedding already-rendered Mermaid SVGs, required board/panel stylesheets, and referenced local images as inline data URIs, with practical size guardrails/warnings for large media.
- **`doc-frame` / sandboxed mockup iframe snapshots:** Capturing framed HTML mockup content (`srcdoc` / frame styles and theme tokens) into the static output without requiring a live daemon or losing frame styling.
- **Theme fidelity & offline browser download:** Preserving light/dark tonal rendering offline and triggering a client-side `Blob` download with a clean filename across Chrome, Edge, Safari, and Firefox.

## Phases

### Phase 1: Client Selection UX & Static Single-HTML Serializer

Add topic selection controls on the whiteboard UI and implement client-side hydration and single-file static `.html` serialization for selected topic cards, including inlined stylesheets/theme tokens, already-rendered Mermaid SVGs, inlined image data URIs, and the browser download trigger.

### Phase 2: `doc-frame` Mockup Snapshots, Size Guardrails & Verification

Add `doc-frame` / sandboxed mockup iframe snapshot serialization with preserved CSS scoping and theme fidelity, enforce size/safety guardrails for large embedded assets, and verify via Playwright/headless browser tests that the exported `.html` opens cleanly offline with zero daemon dependency across selected topics, Mermaid diagrams, and framed mockups.
