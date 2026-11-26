---
title: "대화창 기본 턴 도구 호출 자동 접힘 및 추가 토큰(+Nk tok) 요약"
parent: 261126-epic-ws-dashboard-conversation-ux-restyle
related:
  261115-feat-ws-dashboard-conversation-rendering-consolidation: preserves .wbupdate-topic and mail entries while collapsing tool rows in .conv.centries
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 17b23c13cb6f541a
sage-review-design-reviewed: 17b23c13cb6f541a
---

# Baseline Collapsed Turn Tool Summary & Added Tokens (+Nk tok)

## Background

In the dashboard conversation stream (`.conv.centries`), completed lead turns currently surface tool-call rows (`.au-tools`, `.toolsline`) inline; a `<details>`-style collapsed `.toolsline` summary row already exists with failure/duration/`.ttok` per-batch usage markup (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.css#L75-L83`, `agents-plugin-tool/internal/wslive/toolsummary.go#L68-L113`, commits `7c8391d5`, `be7860ad`), so this ticket restyles/extends it rather than introducing collapsing from scratch. Meanwhile, `wslive` tracks live `UsageContext` (`agents-plugin-tool/internal/wslive/snapshot.go#L275`), but the turn ring record (`RingEntry` with `Kind == RingTurn`, `agents-plugin-tool/internal/wslive/ring.go#L26,L59-L83`; there is no `RingTurn` struct) does not record per-turn token delta (`+Nk tok`; search for `AddedTokens` in `agents-plugin-tool/` returned nothing), so users cannot gauge how much work a turn performed at a glance.

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

2. **Cross-harness `AddedTokens` on the turn `RingEntry` with byte-estimate fallback — technical (`T0`)**
   - In `agents-plugin-tool/internal/wslive/`, capture `UsageContext` at turn start and compute `AddedTokens = max(latest - start, 0)` on turn completion (and incrementally during an active turn when context usage updates), persisted as an optional `added_tokens,omitempty` field on the turn `RingEntry` (`Kind == RingTurn`).
   - When a harness does not report updated `UsageContext` for a turn (`AddedTokens == 0` despite tool/text activity), compute, at render time (Decision 5), a deterministic byte-estimate fallback displayed with a leading tilde (`~+1.2k tok`) so every turn shows a consistent magnitude cue without misrepresenting an estimate as an exact count.
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

5. **One per-turn summary built from the existing ring rows (no parallel container) — technical (auto-confirmed)**
   - **Turn grouping rule (owned here; Children 2 and 3 consume it):** a turn's span is the ring entries after the previous `RingTurn` entry up to and including its own `RingTurn` entry (the ring's natural turn-end marker; `state.go` appends it at turn end). `RingTools` (`.toolsline`, per-gap rows: `wslive/toolsummary.go`, commits `7c8391d5` / `be7860ad`) entries inside a span are rendered inside that turn's `[data-conv-turn-drawer]`; user steer bubbles, `.wbupdate-topic` cards, mail, `.permwait`, compaction and command rows stay outside in their ring order. Planning verifies the ring-order invariant (the `ringStamp` receiver-path note in `wslive/ring.go`) before relying on it.
   - **Row and placement:** the single summary row (`[data-conv-turn-summary]`) is anchored at the position of the turn's first tool-gap row (matching the v10 mockup, where it follows the user bubble), and its counts come from the turn `RingEntry` (`Done`, `Failed`, `DurationMs`, `AddedTokens`) rather than from summing gaps. The existing end-of-turn `.turnline` row (`dashboard.tmpl`, `dashboard-conversation.css`) is replaced by this summary for turns that have it; turns with 0 tool calls keep `.turnline` unchanged unless Child 2 folds them. The `.turn-with-summary` reasoning-summary host of `261114-feat-ws-live-reasoning-summary` (ready, not landed) stays at its planned turn-end position and is not absorbed.
   - **Active turn:** the open span (entries after the last `RingTurn`) renders the same summary component as a live header, fed by the snapshot (`Lead.ToolGap` plus the already flushed gaps of the open span and the running `AddedTokens`); there is no second container. It appears once the first tool gap of the turn exists; before that `#r-convlive` alone shows activity.
   - **Running-token snapshot contract (shared with Child 3):** the live snapshot carries the open turn's running `AddedTokens` as an additive `omitempty` field on the lead snapshot (`wslive/snapshot.go`, e.g. `Lead.TurnAddedTokens`; Go name settled in planning), updated whenever `UsageContext` advances during the turn using the same `max(latest - start, 0)` definition, and cleared when the turn's `RingTurn` is written (the value then lives in `RingEntry.AddedTokens`). Child 3 reads only this field. The live header is gated on the snapshot's turn-running state, so an idle session never shows one.
   - **Spans without a closing `RingTurn`:** a turn start with an unfinished reply, a reset, or `turnStartedAt == 0` can leave gap rows with no `RingTurn`. Such a span (when not the running turn) falls back to a summary derived from its own `RingTools` entries (calls, failures, duration, `Usage`; no `AddedTokens`, so the `~` estimate) instead of showing a count-less row.
   - **`.turnline` replacement keeps its content and consumers:** the `Origin`/`WakeTurn` wake label and `Reason` move into the summary row, and consumers of `.turnline` (search `turnline`, e.g. `newestTurn` and the companion director) are listed and kept working, or a minimal hook remains.
   - **Reusable component:** the summary renderer exposes an entry point that can also instantiate the row for a 0-tool turn with a "folded plain response" label (consumed by Child 2); it is not a second container.
   - **Token value and byte-estimate fallback:** the single `[data-conv-turn-tokens]` shows the turn's `AddedTokens` only; the per-gap `.ttok` batch-usage markup stays inside the drawer rows and is never a second anchor. The `~+Nk tok` estimate is derived at render time (never stored as `AddedTokens`) when `AddedTokens == 0`, from bytes the ring already holds (`ToolSummary.InBytes + OutBytes` across the turn's gaps plus reply `Text` length, divided by 4), so no new wire field and no command/argument/result content is added (consistent with `9de28cbc`).
   - **Drawer rows show per-tool duration only:** the mockup's per-call `+340` meta is illustrative; no per-call token field exists, so it is not rendered.
   - Selectors consumed by `dashboard-audit-rows.js` and `companion-relay.js` keep working (update them in the same change if markup moves).
   - **Rejected:**
     - Extending only the per-gap `.toolsline` row — a turn with replies between tool batches would show several summaries and `AddedTokens` (turn-level) would have no single home.
     - A second parallel turn-summary row beside `.toolsline` or `.turnline` — duplicates the container, violates the epic's "neither child creates a duplicate turn summary container" invariant, and reverses the `7c8391d5` (143) b decision.
     - Storing the byte estimate in `AddedTokens` — an integer alone cannot distinguish estimate from exact.

## Constraints

- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`)
- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- **Minimal DOM updates & scroll-lock safety:** Patch only the affected turn summary row during live updates; never re-render `.convo` wholesale or break `stick===false` scroll lock (`84cd8692`).
- **No disk I/O on interactive toggle:** Expanding/collapsing a turn drawer is pure in-memory DOM state.
- **No regression in `261115` consolidation or `261126` question capsules:** `.wbupdate-topic` cards and mail cards stay cleanly visible outside the collapsed tool drawer.

## Prior Art

- Live state & ring turn projection: `agents-plugin-tool/internal/wslive/state.go` (turn `RingEntry` appended at `#L419`), `agents-plugin-tool/internal/wslive/snapshot.go`, `agents-plugin-tool/internal/wslive/ring.go`, `agents-plugin-tool/internal/wslive/toolsummary.go`.
- Conversation presentation & templates: `agents-plugin-tool/internal/wsdashboard/pageview.go`, `agents-plugin-tool/internal/wsdashboard/page_format.go`, `agents-plugin-tool/internal/wsdashboard/conversation_presentation.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl`.
- Conversation client rendering & styling: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.js`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.css`.

## Prior Decisions

- 7c8391d5 (2026-11-07, commit): "User board (143) b: a collapsed summary row of the lead's finished tool calls between conversation entries, no adapter change. Rejected: (a) turn-end line only; (c) argument previews" — bearing: supports
- 9de28cbc (2026-11-07, commit): "Commands, paths, arguments, and results stay off the wire and out of the ring; only names, clocks, counts, and tokens are sent." — bearing: constrains
- be7860ad (2026-11-12, commit): "per-tool counts pushed failed/duration/token numbers into the ellipsis. Collapsed pill splits counts and numbers into two rows; open hides the counts row. Asset-only, T0" — bearing: constrains
- 84cd8692 (2026-11-26, commit): "Merged #convo scroll-lock fix (overflow-anchor:none, guarded programmatic scrollTop writes, swap/resize clamp guards...) and floating jump-to-bottom capsule" — bearing: constrains
- 261126-feat-ws-chat-stream-interactive-question-cards (2026-11-26, ticket Decisions): "Hydrate a .wbupdate-topic when inserted live, paged in, merged or keyed-patched in place (mergeEntries, mergeEntriesRegion, entryInputPatch / keyedInputPatch)" — bearing: constrains
- 261126-epic-ws-dashboard-conversation-ux-restyle (2026-11-26, Cross-Child Decisions): "Shared turn-summary DOM contract owned by Child 1 and consumed by Children 2 & 3" — bearing: supports
- e354de2b (2026-11-26, commit): "Children 2 and 3 declare blocked-by Child 1 so all three promote in one batch while Child 1 lands the shared DOM contract first." — bearing: supports
- 1b3933bb (2026-11-25, commit): "Kept wire protocol strictly T0 ... zero schema changes to messageRequest, wsinbox.Entry, or wslive.RingEntry." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wslive/state.go, wslive/ring.go, wsdashboard/conversation_presentation.go, assets/dashboard-conversation.js, assets/dashboard-conversation.css, assets/dashboard.tmpl, wsi18n/tables/*.json |
| scope.surface | cross-module | wslive RingEntry JSON field consumed by wsdashboard and mcp (mcp/pi_tools.go reads RingTurn) |
| scope.new_public_symbol | yes | RingEntry.AddedTokens exported field; no AddedTokens exists today (search returned nothing) |
| scope.new_type_contract | yes | added_tokens,omitempty on RingEntry; data-conv-turn-* DOM contract consumed by Children 2 and 3 |
| scope.test_surface | existing | wslive/ring_test.go, wslive/state_test.go, wsdashboard/conversation_tools_test.go, frontend-*.test.js; new Node DOM harness tests expected |
| complexity.reuse_points | confirmed | existing .toolsline collapsed summary, wslive/toolsummary.go Usage sums, mergeEntries and keyedInputPatch |
| complexity.side_effect_risk | high | restyles existing .toolsline consumed by dashboard-audit-rows.js and companion-relay.js selectors, scroll anchoring, ring persistence |
| risk.correctness | moderate | token delta math across harnesses with compaction resets and byte-estimate fallback |
| risk.fit | moderate | existing toolsline/tsum markup must be reconciled with new data-conv-turn contract |
| risk.test | moderate | needs Node DOM and Playwright coverage plus cross-harness delta tests; not yet verified |
| risk.security_or_contract | moderate | additive omitempty wire field, T0 claimed; ring persisted to disk |

## Phases

### Phase 1: Per-turn `AddedTokens` capture and default collapsed expandable tool summary row

Ordered sub-steps (one reviewable unit): (a) backend `AddedTokens` capture and tests, (b) presentation grouping per Decision 5 and view models, (c) summary row, drawer and DOM hooks (client), (d) i18n keys, (e) verification captures.

- **Backend `wslive` token delta (`AddedTokens`):**
  - Record turn-start `UsageContext` in `wslive` live turn state and populate optional `AddedTokens` (`json:"added_tokens,omitempty"`) on the turn `RingEntry` on completion, and expose the running value through the additive lead-snapshot field of Decision 5 while the turn is active (cleared at `RingTurn` write) (left 0 when context usage did not advance; the `~+Nk tok` estimate is derived at render time per Decision 5, never stored).
  - Expose tool count, failed tool count, elapsed duration, and formatted token delta (exact, or the render-time `~` estimate of Decision 5) in conversation presentation view models cleanly (T0), grouped per the Decision 5 turn-span rule.
- **Collapsed 1-line turn summary UI & shared DOM hooks:**
  - Collapse each turn's lead tool-call groups (the turn span of Decision 5) in `.conv.centries` by default into the single borderless tonal summary row matching `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html` (`✓ N개 툴 호출 · 11.4s [· 실패 K] · +2.8k tok ▾`), with click-to-expand `[data-conv-turn-drawer]` displaying the turn's tool rows (including subtle tool-family color dots).
  - Expose `[data-conv-turn-summary]`, `[data-conv-turn-drawer]`, `[data-conv-turn-count]`, `[data-conv-turn-fails]`, and `[data-conv-turn-tokens]` consistently on both active and completed turn summary headers.
  - Preserve user expand/collapse state across SSE entry merges/patches and preserve `.convo` scroll anchoring.
  - Keep `.wbupdate-topic`, `.msg.user`, `.msg.fromlead`, and consolidated mail cards outside the tool drawer.
- **Surgical i18n keys:**
  - Append needed summary/failure labels across all locale tables in `agents-plugin-tool/internal/wsi18n/tables/` without re-sorting existing keys.

**Verification expectations:**
- Go tests (from `agents-plugin-tool/`): `go test -race ./internal/wslive/... ./internal/wsdashboard/... ./internal/wsi18n/...` and `GOOS=windows go vet ./...`, covering `AddedTokens` delta math (normal increase, reset/compaction clamp to 0, byte-estimate `~+Nk` fallback, `omitempty` T0 JSON compatibility), turn-summary template rendering, and locale table parity.
- Node DOM harness tests covering: default collapsed state on completed turns, click-to-expand and click-to-collapse toggling `[data-conv-turn-drawer]` & `aria-expanded`, preservation of expanded state across SSE `mergeEntries` / keyed patches without scroll-lock drift, failure count visibility when `failed > 0`, presence of exactly one `[data-conv-turn-tokens]` anchor per turn, a turn with several tool gaps and interleaved replies producing one summary, the active-turn live header (same component, updating as gaps finish), the `~` estimate rendering when `AddedTokens == 0`, the running snapshot field advancing with `UsageContext` and clearing at `RingTurn` write, a span with no closing `RingTurn` falling back to a gap-derived summary, no live header on an idle session, and the wake label surviving the `.turnline` replacement.
- Locale parity: every table in `wsi18n/tables/` contains the new keys (the parity test in `wsi18n` / `wsdashboard/i18n_test.go` passes).
- Headless Chromium (Playwright) visual verification in dark and light themes compared side-by-side against the Axis A turn-summary rows in `ai-docs/assets/mockups/261125/whiteboard-chat-view/mockup.html`.
