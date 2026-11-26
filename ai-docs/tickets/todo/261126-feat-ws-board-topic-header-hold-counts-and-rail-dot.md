---
title: "토픽 카드 헤더(한 눈에 보기 포함) 보류·응답함·미결 분리 표시 및 세션 레일 오렌지 점 보류 제외"
related:
  261119-feat-ws-whiteboard-frame-css-isolation: CSS scoping for whiteboard .tbar and .chip classes
---

# Topic Header Deferred/Answered/Open Breakdown and Session Rail Hold-Aware Pending Dot

## Background

Today, two whiteboard/dashboard summary cues conflate deferred (`보류`) items with actively pending (`미결`) items or omit answered counts:

1. **Topic card headers (`.tophead .tbar`) — including Overview (`한 눈에 보기`) tiles:**
   - Currently `.tbar` only renders server-baked `.chip.policy` (`? N`, equal to `TopicView.AskCount()` = open policies + open checks, including locally held items) and `.chip.ok` (`len .Acks`).
   - Overview mode (`:root[data-overview]` / `overview.js` `buildPlaceholder`) clones `:scope > .tophead`, so Overview cards show the exact same conflated `? N` badge.
   - Only the floating edge pill (`pill.js` `.oq-pill`) distinguishes `미결` (open, non-held), `보류` (held), `응답` (answered/staged), and `노트` — and `.oq-wrap` is hidden in Overview mode.
2. **Left session rail orange pending dot (`.rail .qdot`) and rail header badge (`.rhd .chip.policy`):**
   - Rendered strictly from server-side `RailRow.CountPolicy` (`OpenAskCount()`) and `RailView.CountPolicy`.
   - When a user defers all open asks in a session via `보류`, the session row in the left rail still displays the orange `.qdot` (and the rail header keeps counting those deferred asks), causing false urgency across sessions.

Inspection of the current data flow confirms a key structural advantage: the server **already** embeds `data-asks="{{.OpenAskIDs}}"` on every `.rs` rail row (including inactive sessions not currently open in any frame) in both SSR and SSE `railSource` updates (`dashboard.tmpl`, `pageview_rail_row.go`). However, `pill.js` stores local holds under `ws.wb.holds.<rid>@<boardIncarnation>`, and `RailRow` does not yet emit `data-board-incarnation` (nor does `dashboard-rail.js` / `rail.js` filter `.qdot` against held IDs).

## Decisions

1. **Keep browser-local `localStorage` hold storage and filter client-side over `data-asks` — `(1697)=a`**
   - Retain `localStorage` (`ws.wb.holds.<rid>@<inc>`) as the hold store (no new daemon persistence store, API mutation route, or Windows file-touch churn).
   - Expose `data-board-incarnation` on `RailRow` SSR/SSE rows (additive HTML attribute) so the rail client can look up `ws.wb.holds.<rid>@<inc>` for **every** session row — including sessions not currently opened in the main frame — subtract held IDs from `data-asks`, hide or update `.qdot` (and its `title`/`aria-label` count) accordingly, and recalculate the rail header `.chip.policy` total.
   - Update topic card headers (`.tophead .tbar`, propagating cleanly to Overview `.overview-placeholder` tiles as well) to distinguish **`미결` (open, non-held)**, **`응답함` (answered)**, and **`보류` (deferred)** counts live from `pill.js` / board state rather than showing a single combined `? N` chip.
   - Propagate hold toggles immediately across the active whiteboard, Overview placeholders, and enclosing shell/rail (not relying solely on `window` `"storage"`, which does not fire in the same document).
   - **Rejected:**
     - Persisting `보류` state server-side in daemon/board SQLite via a new `POST /s/{rid}/wb/holds` route (`b`) — rejected to avoid unnecessary storage schema surface, network latency on click, and Windows file-touch overhead when `data-asks` is already delivered per rail row.
2. **Compatibility tier: T0 (transparent)**
   - Adding `data-board-incarnation` (and optional lightweight item-revision metadata if settled at promotion) to `RailRow` SSR/SSE HTML plus client-side JS/CSS updates is strictly additive; older binaries/clients ignore the extra attribute cleanly.

## Constraints

- **Applicable manuals (read before editing):**
  - `ai-docs/manuals/dashboard-style.md` (tonal pill/chip colors, compact header density, dark/light theme readability, no heavy borders).
  - `ai-docs/manuals/performance.md` (zero extra server file I/O; avoid layout thrashing when updating rail rows and Overview placeholders on hold toggles or SSE morphs).
  - `ai-docs/manuals/release-compatibility.md` (T0).
  - `ai-docs/manuals/shipped-surface-boundary.md`.
- **Cross-surface reactivity & consistency:**
  - Toggling `보류` on an item must synchronously update: (1) the item's own `.wb-held` visual state, (2) its parent topic's `.tophead .tbar` counts (and any cached Overview `.overview-placeholder`), (3) the floating `.oq-pill`, (4) the active session's `.qdot` and rail header count (crossing the `iframe.sframe` ↔ shell boundary via `postMessage` or shared shell hook when running in shell mode), and (5) other open tabs via `window` `"storage"`.
  - SSE `WsRail.mergeRows` morphs and whiteboard SSE swaps must immediately re-apply hold filtering without flashing stale un-filtered counts or `.qdot` dots.
- **Details to settle cleanly at `ready/` promotion (not invented unilaterally during implementation):**
  - Exact visual semantics of `응답함` in `.tbar` (e.g., staged/in-flight client answers vs server-settled answers vs combined breakdown, and how `bulk` quick-pick clicks interact with the separated `미결` chip).
  - Whether background re-ask invalidation for non-open sessions needs an additive lightweight fingerprint/version attribute on `RailRow` or stays reconciled when that session's board is opened.
  - Scope alignment for `ack` items (`OpenAskIDs` currently covers `policy` + `check`, whereas `pill.js` also allows holding `.arow` acks).

## Prior Art

- Existing hold storage and reconciliation: `internal/wswhiteboard/assets/pill.js` (`HOLD_PREFIX`, `boardKey`, `reconcileHolds`, `toggleHold`, `KINDS`).
- Existing topic header chips & Overview placeholder cloning: `internal/wswhiteboard/assets/page.tmpl` (`.tophead` / `.tbar`), `internal/wswhiteboard/view.go` (`AskCount`, `SettledCount`), `internal/wsdashboard/assets/overview.js` (`buildPlaceholder`).
- Existing rail `data-asks` delivery & row morph hook: `internal/wswhiteboard/store.go` (`OpenAskCount`, `OpenAskIDs`), `internal/wsdashboard/pageview_rail_row.go`, `internal/wsdashboard/assets/dashboard.tmpl` (`rrow`, `r-notif-row`), `internal/wsdashboard/assets/rail.js` (`rowAsks`, `mergeRows`), `internal/wsdashboard/assets/dashboard-rail.js` (`railMarks`).

## Phases

### Phase 1: Topic header open/answered/deferred chips (board + Overview) and hold-aware rail `.qdot`

- **Go SSR/SSE (`wsdashboard`):**
  - Carry `BoardIncarnation` onto `RailRow` and emit `data-board-incarnation` on session rail rows (`rrow` and `r-notif-row`) so client JS can resolve `ws.wb.holds.<rid>@<inc>` for every session in the rail.
- **Topic header breakdown (`wswhiteboard` + `wsdashboard` Overview):**
  - Render/sync distinct **`미결`**, **`응답함`**, and **`보류`** count chips in `.tophead .tbar` (preserving bulk-action behavior on the open ask chip and keeping compact visual hierarchy in both expanded board view and Overview mode).
  - Ensure `overview.js` placeholders reflect live hold/answer updates cleanly when switching to or staying in Overview.
- **Session rail `.qdot` & header count filtering (`wsdashboard`):**
  - Add a client helper reading `data-rid`, `data-board-incarnation`, and `data-asks` against `localStorage` `ws.wb.holds.*` to compute effective non-held open ask counts per row.
  - Hide `.qdot` when all open asks for a session are held, update `.qdot` tooltip/aria-label count to the non-held count, and update the rail header `.chip.policy` sum accordingly.
  - Wire immediate updates from `pill.js` `toggleHold` / `reconcileHolds` (including iframe-to-shell notification), `WsRail.mergeRows` / `railMarks()`, and `window` `"storage"` events.

**Verification expectations:**
- Remote `w5090` Go tests (`GOOS=windows go vet ./...` and `go test -race ./internal/wsdashboard/... ./internal/wswhiteboard/...`) verifying `data-board-incarnation` emission on `RailRow` SSR/SSE templates and existing `OpenAskIDs` invariants. Never run local `go test` on Windows.
- Node DOM harness tests covering:
  - Topic `.tbar` + Overview placeholder showing distinct `미결` / `응답함` / `보류` counts as items are held, unheld, staged, and settled.
  - Session rail `.qdot` hiding when every ID in `data-asks` is in `ws.wb.holds.<rid>@<inc>`, reappearing when a new unheld ask ID arrives via SSE `mergeRows`, and updating rail header total across both active and inactive sessions.
- Live dashboard check verifying board headers, Overview view, and left rail `.qdot` behavior across session switches.
