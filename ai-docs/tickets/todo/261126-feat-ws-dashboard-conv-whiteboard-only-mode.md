---
title: "입력창 우하단 화이트보드 전용 토글 (프롬프트 주입 및 일반 출력 접힘)"
parent: 261126-epic-ws-dashboard-conversation-ux-restyle
blocked-by: 261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens
related:
  261126-feat-ws-unified-composer-input-component: preserves #csend and .cfoot placement beside WsInput host
sage-review-design: pending
sage-review-completeness: pending
---

# Bottom-Right Composer "Whiteboard-Only" Mode Toggle (Prompt Injection & Plain-Text Folding)

## Background

When working primarily through the live whiteboard, users want a single per-session switch that (1) instructs the lead agent on every message to communicate exclusively through whiteboard topics (`wb:log` / `wb:panel` / `wb:add`) rather than plain-text chat chatter, and (2) folds any stray plain-text assistant replies (`.msg.fromlead`) into the turn's collapsed summary drawer so the conversation stream highlights only user bubbles, `.wbupdate-topic` cards (with inline question capsules), and 1-line expandable turn summaries — while never discarding plain text when the user clicks to inspect it.

As Axis C of `261126-epic-ws-dashboard-conversation-ux-restyle`, this ticket places a compact `화이트보드 전용` toggle directly below the Send button in `.cfoot`, wires optional `whiteboard_first` `<ws-ambient>` injection on message submission, and folds `.msg.fromlead` into Child 1's `[data-conv-turn-drawer]`.

Approved visual reference: `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html` (v10 — Axis C composer bottom-right toggle, `<ws-ambient>` hint chip, and plain-text folding into turn summary drawer).

## Decisions

1. **Placement: right end of `.cfoot` directly below the Send button (`.csend`), default OFF — `(24)=a`, 2026-11-26 21:52 KST, `(28)=ok`**
   - Render a compact tonal toggle button (`화이트보드 전용`, localized via `wsi18n`) at the right end of `.cfoot` aligned directly under `.csend` (preserving `.csend-wrap`'s `.cvox` / `.csend` / `.csend-revert` horizontal row and staying clear of `WsInput` inside `#composer`).
   - Default state is **OFF** (`(24)=a`), persisted per session (e.g., keyed by session reply-id in `localStorage` so reload preserves a session's active choice without forcing other sessions into whiteboard-only mode).
   - When ON, the button takes the calm cobalt active surface (`#7aa2ff` tint matching `dashboard-style.md` and the v10 mockup) and a subtle left-side `.cfoot` hint indicates `<ws-ambient>` whiteboard-only prompt injection is active.
   - **Rejected:**
     - Top header view-mode switcher (`(10)`–`(12)`) — rejected as dual-renderer clutter.
     - Upper `.cbadges` bar (superseded by owner 2026-11-26 21:52 KST preference for placing it directly under the Send button).

2. **Server-side `<ws-ambient>` prompt injection excluded from visible user bubble — `(07)`, 2026-11-26 21:52 KST (`T0`)**
   - When `화이트보드 전용` is ON, `POST /s/<reply-id>/message` includes optional JSON field `whiteboard_first: true` (`omitempty`, T0).
   - On the server (turn input / steer delivery path), when `whiteboard_first` is true, append a concise directive inside `<ws-ambient>` instructing the agent that user-facing output is displayed through the whiteboard only and all responses, progress notes, and explanations must be recorded on the relevant topic via `wb:log` or `wb:panel` (minimizing plain-text output).
   - The injected directive lives strictly inside `<ws-ambient>` and is **never** included in the persisted/rendered visible user message bubble (`.msg.user`).
   - **Compatibility tier: T0 (transparent).** Optional boolean `whiteboard_first,omitempty`; older daemons ignore it cleanly and older clients omit it cleanly.
   - **Rejected:**
     - Prepending raw instruction text to the user's message string on the client — pollutes visible chat bubbles, history, and outbox previews.

3. **Folding `.msg.fromlead` plain-text output into Child 1's collapsed turn summary drawer when ON — 2026-11-26 21:52 KST, `(28)=ok`**
   - When `화이트보드 전용` is **OFF**, `.msg.fromlead` renders normally in the stream.
   - When `화이트보드 전용` is **ON**, plain-text `.msg.fromlead` entries belonging to a turn are folded inside that turn's `[data-conv-turn-drawer]` (updating the collapsed summary row with a subtle indicator such as `· 일반 응답 접힘`), so the main stream shows user bubbles, `.wbupdate-topic` cards, and 1-line turn summaries.
   - Clicking the turn summary row expands `[data-conv-turn-drawer]` to show both tool rows AND the folded plain-text output so nothing is ever lost or uninspectable.
   - Edge case: if a turn has plain-text `.msg.fromlead` output and **0 tool calls** while `화이트보드 전용` is ON, collapse it behind a matching compact 1-line expandable summary row (e.g., `일반 응답 접힘 · +Nk tok ▾`) rather than hiding it irreversibly or leaking it unfolded.
   - Toggling `화이트보드 전용` ON/OFF updates visibility incrementally in the DOM without re-fetching history or breaking `.convo` scroll lock (`84cd8692`).

4. **Surgical i18n keys across all locale tables — technical**
   - Add minimal surgical keys across all locale JSON files in `agents-plugin-tool/internal/wsi18n/tables/` (no re-sorting) for `화이트보드 전용`, the `<ws-ambient>` active hint, and `일반 응답 접힘`.

## Constraints

- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- **Blocked by Child 1:** Consumes `[data-conv-turn-summary]` and `[data-conv-turn-drawer]` landed by `261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens`.
- **Non-collision with `WsInput`:** Do not alter `#composer` editor internals; mount the toggle inside `.cfoot` below `.csend`.
- **Never hide `.wbupdate-topic`, `.permwait`, or error banners:** Only plain-text `.msg.fromlead` assistant prose folds into the turn drawer when `화이트보드 전용` is ON.

## Prior Art

- Composer footer & send flow: `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl` (`.crow`, `.csend-wrap`, `.cfoot`), `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.css`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer.js`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-delivery.js`.
- Message intake & `<ws-ambient>` injection: `agents-plugin-tool/internal/wsdashboard/` message handler & `internal/wsinbox/` / `internal/wsrunner/` ambient delivery path.
- Turn summary drawer (Child 1): `[data-conv-turn-summary]`, `[data-conv-turn-drawer]` in `dashboard-conversation.js` / `dashboard.tmpl`.

## Phases

### Phase 1: Bottom-right `화이트보드 전용` toggle, `<ws-ambient>` injection, and `.msg.fromlead` drawer folding

- **Composer bottom-right toggle UI & per-session persistence:**
  - Add the `화이트보드 전용` toggle button at the right end of `.cfoot` aligned below `.csend` (default OFF, persisted per session in `localStorage`) with active cobalt tonal styling and subtle active `<ws-ambient>` hint matching `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`.
- **Server-side `<ws-ambient>` prompt injection (`T0`):**
  - Accept optional `whiteboard_first` boolean (`omitempty`) on `/s/<reply-id>/message` (and queued/steer delivery path) and inject the whiteboard-only directive inside `<ws-ambient>` without polluting the stored/rendered visible user bubble.
- **Plain-text `.msg.fromlead` folding into `[data-conv-turn-drawer]`:**
  - When ON, fold each turn's `.msg.fromlead` plain-text content into Child 1's `[data-conv-turn-drawer]` (and synthesize a compact 1-line `일반 응답 접힘` summary wrapper for 0-tool turns), keeping `.wbupdate-topic`, `.msg.user`, and `.permwait` visible and allowing 1-click expansion to inspect folded prose.
  - Live toggling ON ↔ OFF updates existing rendered turns incrementally without full stream re-render or scroll-lock disruption.
- **Surgical i18n keys:**
  - Append needed strings across all locale JSON tables in `agents-plugin-tool/internal/wsi18n/tables/` without re-sorting.

**Verification expectations:**
- Go tests (from `agents-plugin-tool/`): `go test -race ./internal/wsdashboard/... ./internal/wsi18n/...` (plus affected runner/inbox package tests) and `GOOS=windows go vet ./...`, verifying `whiteboard_first: true` injects the directive into `<ws-ambient>`, leaves visible user message text clean, and `whiteboard_first: false`/omitted preserves existing payload byte-for-byte (T0).
- Node DOM harness tests covering: default OFF state, per-session toggle persistence, sending `whiteboard_first: true` in the message payload when ON, folding `.msg.fromlead` into `[data-conv-turn-drawer]` (both with tool calls and with 0 tool calls), expanding the drawer to read folded plain text, preserving `.wbupdate-topic` visibility, and toggling OFF to restore unfolded `.msg.fromlead` without scroll-lock drift.
- Headless Chromium (Playwright) visual verification in dark and light themes comparing composer `.cfoot` toggle alignment below `.csend` and folded/expanded `.msg.fromlead` states side-by-side against `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`.
