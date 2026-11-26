---
title: "입력창 우하단 화이트보드 전용 토글 (프롬프트 주입 및 일반 출력 접힘)"
parent: 261126-epic-ws-dashboard-conversation-ux-restyle
blocked-by: 261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens
related:
  261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens: prerequisite (lands the shared turn-summary DOM contract)
  261126-feat-ws-unified-composer-input-component: preserves #csend and .cfoot placement beside WsInput host
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 554ea4827fc34d66
sage-review-completeness-reviewed: 554ea4827fc34d66
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
   - **Stored path (technical, auto-confirmed):** the route (`wsdashboard/inbox_routes.go`) writes a persisted `wsinbox.Entry` (`wsinbox/inbox.go`) that the runner consumes later, so the flag travels as an additive optional `WhiteboardFirst bool` (`json:"whiteboard_first,omitempty"`) on that `Entry`, preserved by steer rewrites and queued/outbox delivery. `conv.turnInput` (`wsagent/ambient.go`) composes one turn from several queued entries: the directive is added when **any** entry of the composed turn carries the flag (union rule), once, last, model-only, and exactly once across retry (`9dff34aa`); no flagged entry → output byte-identical to today.
   - **Steer path (technical, auto-confirmed):** a lead's mid-turn steers do not pass through `turnInput` (`conv.steer()` in `wsagent/runner_conv.go` calls the state's steer directly, or `steerDurableInbox` → `submitSteer` in `durable_steer.go`), so the flag is read there too: a flagged entry's steered text gets the same directive block appended (model-only, never in the visible bubble or stored text), once per delivery and exactly once across retry (`9dff34aa`); an unflagged steer is byte-identical to today. Planning confirms every backend's steer path (not only the lead's) honors this or states the gap.
   - **Rejected:**
     - Deferring a flagged steer's directive to the next turn's `turnInput` — the agent would answer the steered message in plain text, which is exactly what the toggle is meant to prevent.
     - Prepending raw instruction text to the user's message string on the client — pollutes visible chat bubbles, history, and outbox previews.
     - Last-entry-wins or first-entry-wins flag composition — a flagged message batched with an unflagged one would silently lose the directive.

3. **Folding `.msg.fromlead` plain-text output into Child 1's collapsed turn summary drawer when ON — 2026-11-26 21:52 KST, `(28)=ok`**
   - When `화이트보드 전용` is **OFF**, `.msg.fromlead` renders normally in the stream.
   - When `화이트보드 전용` is **ON**, plain-text `.msg.fromlead` entries belonging to a turn are folded inside that turn's `[data-conv-turn-drawer]` (updating the collapsed summary row with a subtle indicator such as `· 일반 응답 접힘`), so the main stream shows user bubbles, `.wbupdate-topic` cards, and 1-line turn summaries.
   - Clicking the turn summary row expands `[data-conv-turn-drawer]` to show both tool rows AND the folded plain-text output so nothing is ever lost or uninspectable.
   - Edge case: if a turn has plain-text `.msg.fromlead` output and **0 tool calls** while `화이트보드 전용` is ON, collapse it behind a matching compact 1-line expandable summary row (e.g., `일반 응답 접힘 · +Nk tok ▾`) rather than hiding it irreversibly or leaking it unfolded.
   - Toggling `화이트보드 전용` ON/OFF updates visibility incrementally in the DOM without re-fetching history or breaking `.convo` scroll lock (`84cd8692`).
   - **Turn membership and container (technical, auto-confirmed):** a `.msg.fromlead` belongs to the turn span Child 1 defines (ring entries after the previous `RingTurn` up to and including its `RingTurn`; Decision 5 of `261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens`). The fold applies retroactively to already rendered or restored history while ON. The 0-tool wrapper is Child 1's summary component instantiated through its reusable entry point (no new container), anchored where the turn's first folded reply sat. Fold open/closed state is Child 1's drawer expand state (`7d439405`: user toggle persists); toggling `화이트보드 전용` never resets it.
   - **Rejected:** a Child-2-owned turn container or grouping rule — would duplicate Child 1's contract.

4. **Surgical i18n keys across all locale tables — technical**
   - Add minimal surgical keys across all locale JSON files in `agents-plugin-tool/internal/wsi18n/tables/` (no re-sorting) for `화이트보드 전용`, the `<ws-ambient>` active hint, and `일반 응답 접힘`; every table gets the keys (locale parity test passes).

5. **Toggle widget semantics — technical (auto-confirmed)**
   - The toggle is a `button` with `aria-pressed`, keyboard-activatable, and the persistence key is `ws.dashboard.whiteboardFirst.<reply-id>` in `localStorage` (value `"on"`; absent = OFF), consistent with the other client-only `ws.dashboard.*` preferences. The active `<ws-ambient>` hint is a short localized text next to the toggle inside `.cfoot`. How the toggle sits under `.csend` within the flex `.cfoot` / `inline-flex` `.csend-wrap` at varying widths is settled in planning against the v10 mockup and verified by the two-theme captures.

6. **Hide the toggle on sessions without an `<ws-ambient>` injection path — `(29)=a`, owner decision 2026-11-26**
   - `<ws-ambient>` exists only in `wsagent/ambient.go` (`resolveAmbient` returns empty unless the conversation is a lead's); externally attached terminal sessions drain the inbox through an adapter and cannot receive the directive. On such sessions the `화이트보드 전용` toggle is not rendered at all, so the promise (directive plus fold) is never half-offered. Sessions that gain an ambient path show it automatically.
   - Capability source (technical, auto-confirmed): decided server-side by the same predicate `resolveAmbient` uses and exposed to the page as an additive T0 session attribute (exact carrier located in planning). A hidden toggle behaves as OFF: no `whiteboard_first` is sent and nothing is folded, even if a stale stored `"on"` exists for that session.
   - **Rejected:**
     - Showing the toggle disabled with an explanatory tooltip (`(29)=b`) — clutters `.cfoot` for no actionable gain.
     - Showing it enabled with fold-only behavior (`(29)=c`) — the agent would not be told to use the whiteboard, so plain replies would just be hidden.

## Constraints

- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- **Blocked by Child 1:** Consumes `[data-conv-turn-summary]` and `[data-conv-turn-drawer]` landed by `261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens`.
- **Non-collision with `WsInput`:** Do not alter `#composer` editor internals; mount the toggle inside `.cfoot` below `.csend`.
- **Never hide `.wbupdate-topic`, `.permwait`, or error banners:** Only plain-text `.msg.fromlead` assistant prose folds into the turn drawer when `화이트보드 전용` is ON.

## Prior Decisions

- 9dff34aa (2026-11-21, commit): "The regression proves ambient is model-only, last and exactly once across retry, while dashboard ring user entries preserve the original text and typed command." — bearing: supports
- 261126-feat-ws-chat-stream-interactive-question-cards (2026-11-26, Decisions): "Never re-render the full `.convo` stream ... patch only the matching mounted item capsule/status nodes incrementally and never disturb `.convo` scroll anchoring" — bearing: constrains
- 0dbed3cd (2026-11-26, commit): "T0 transparent compatibility ... surgical +7-line insertions per table across all 11 locales" — bearing: supports
- 7d439405 (2026-11-23, commit): "restored history starts folded to the bar, a fresh submission or an active run opens it; user toggle persists until the next run ends." — bearing: constrains

## Prior Art

- Composer footer & send flow: `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl` (`.crow`, `.csend-wrap`, `.cfoot`), `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.css`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer.js`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-delivery.js`.
- Message intake & `<ws-ambient>` injection: `agents-plugin-tool/internal/wsdashboard/` message handler & `internal/wsinbox/` / `internal/wsagent/ambient.go` ambient delivery path (the route is `agents-plugin-tool/internal/wsdashboard/inbox_routes.go#L27`; no `internal/wsrunner/` package exists — search of `agents-plugin-tool/internal/` found none; the `ws-ambient` block is built in `wsagent/ambient.go#L10-L28`).
- Turn summary drawer (Child 1): `[data-conv-turn-summary]`, `[data-conv-turn-drawer]` in `dashboard-conversation.js` / `dashboard.tmpl`.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | dashboard.tmpl, dashboard-composer*.js, dashboard-conversation.js/css, wsdashboard/inbox_routes.go, wsagent/ambient.go, wsi18n/tables/*.json |
| scope.surface | cross-module | new optional whiteboard_first field on POST /s/<reply-id>/message crossing wsdashboard to wsagent |
| scope.new_public_symbol | unknown | no exported symbol named; body field plus wsagent injection helper not specified |
| scope.new_type_contract | yes | optional JSON boolean whiteboard_first omitempty on the message request |
| scope.test_surface | new-files | existing wsdashboard/conversation_*_test.go and testdata *.test.cjs; no whiteboard_first test exists (grep returned nothing) |
| complexity.reuse_points | unconfirmed | turn drawer data-conv-turn-drawer not yet landed (pending 261126-feat-ws-dashboard-conv-collapsed-tool-summary-tokens) |
| complexity.side_effect_risk | moderate | ambient injection touches the shared delivery path and must stay byte-identical when omitted |
| risk.correctness | moderate | ambient must stay model-only and out of the stored user text; fold must handle 0-tool turns |
| risk.fit | moderate | depends on unlanded Child 1 drawer markup and on the unified composer .cfoot placement |
| risk.test | moderate | needs Go route test, Node DOM harness and Chromium scroll-lock checks across two themes |
| risk.security_or_contract | low | T0 optional field; the only input is a boolean that selects a fixed server-side directive |

## Phases

### Phase 1: Bottom-right `화이트보드 전용` toggle, `<ws-ambient>` injection, and `.msg.fromlead` drawer folding

- **Composer bottom-right toggle UI & per-session persistence:**
  - Add the `화이트보드 전용` toggle button at the right end of `.cfoot` aligned below `.csend` (default OFF, persisted per session in `localStorage`) with active cobalt tonal styling and subtle active `<ws-ambient>` hint matching `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`.
- **Server-side `<ws-ambient>` prompt injection (`T0`):**
  - Accept optional `whiteboard_first` boolean (`omitempty`) on `/s/<reply-id>/message` (and queued/steer delivery path, both `turnInput` and mid-turn steer per Decision 2) and inject the whiteboard-only directive inside `<ws-ambient>` without polluting the stored/rendered visible user bubble.
- **Plain-text `.msg.fromlead` folding into `[data-conv-turn-drawer]`:**
  - When ON, fold each turn's `.msg.fromlead` plain-text content into Child 1's `[data-conv-turn-drawer]` (and synthesize a compact 1-line `일반 응답 접힘` summary wrapper for 0-tool turns), keeping `.wbupdate-topic`, `.msg.user`, and `.permwait` visible and allowing 1-click expansion to inspect folded prose.
  - Live toggling ON ↔ OFF updates existing rendered turns incrementally without full stream re-render or scroll-lock disruption.
- **Capability gating (Decision 6):**
  - Render the toggle only for sessions with an ambient injection path (lead conversations); hide it otherwise and treat the session as OFF.
- **Surgical i18n keys:**
  - Append needed strings across all locale JSON tables in `agents-plugin-tool/internal/wsi18n/tables/` without re-sorting.

**Verification expectations:**
- Go tests (from `agents-plugin-tool/`): `go test -race ./internal/wsdashboard/... ./internal/wsi18n/...` (plus affected runner/inbox package tests) and `GOOS=windows go vet ./...`, verifying `whiteboard_first: true` injects the directive into `<ws-ambient>`, leaves visible user message text clean, and `whiteboard_first: false`/omitted preserves existing payload byte-for-byte (T0); a turn composed from mixed flagged/unflagged queued entries carries the directive exactly once (also across retry), a flagged mid-turn steer (state-direct and durable-inbox paths) carries it exactly once while an unflagged steer is unchanged, and a steer rewrite keeps the flag; the session capability attribute is true only for sessions with an ambient path.
- Node DOM harness tests covering: toggle absent (and `whiteboard_first` never sent, nothing folded despite a stored `"on"`) on a session without an ambient path, default OFF state, per-session toggle persistence, sending `whiteboard_first: true` in the message payload when ON, folding `.msg.fromlead` into `[data-conv-turn-drawer]` (both with tool calls and with 0 tool calls), expanding the drawer to read folded plain text, preserving `.wbupdate-topic` visibility, and toggling OFF to restore unfolded `.msg.fromlead` without scroll-lock drift.
- Headless Chromium (Playwright) visual verification in dark and light themes comparing composer `.cfoot` toggle alignment below `.csend` and folded/expanded `.msg.fromlead` states side-by-side against `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`.
