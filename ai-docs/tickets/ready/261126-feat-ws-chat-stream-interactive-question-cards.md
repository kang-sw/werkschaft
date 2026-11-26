---
title: "대화창 토픽 카드 질문 아이템 연동 렌더링 및 화이트보드 양방향 응답"
related:
  261115-feat-ws-dashboard-conversation-rendering-consolidation: extends Phase 1 .wbupdate topic cards where item option/answer controls were deferred
  261126-feat-ws-unified-composer-input-component: shared inline note input for item notes
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 40ae2c747cd87c52
sage-review-completeness-reviewed: 40ae2c747cd87c52
---

# Interactive Question Items Linked in Chat Stream Topic Cards

## Background

In `261115-feat-ws-dashboard-conversation-rendering-consolidation` (Phase 1), whiteboard updates in the conversation stream were consolidated into grouped `.wbupdate-topic` cards showing topic headers, jump links (`↗`), `L` log lines, panel recaps, and plain item ID links (`(1693)`). In the current code, interactive item previews and answer controls are absent: `ConvEntry.WhiteboardCard()` is limited to committed ring-projection `{ID, Target}` rows (`conversationEffectRow`) without live board binding, by design (it never reads current board text, keeping ring-entry rendering cache-friendly).

As a result, when a lead posts questions on the whiteboard, the user reading the conversation stream only sees bare item ID chips and must jump to the right-hand whiteboard panel to read the question, inspect options/recommendations, or stage an answer. Extending chat-stream `.wbupdate-topic` cards to render their linked question items interactively — synchronized two-way with the right-hand whiteboard — lets users answer directly inside the conversation stream *or* via the right-hand whiteboard interchangeably.

## Decisions

1. **Dedicated follow-up `todo/` ticket linked via `related:` to `261115-feat-ws-dashboard-conversation-rendering-consolidation` — `(1698)=a`**
   - Track this feature as its own ticket rather than absorbing a new Phase into `261115`, keeping scope and compatibility boundaries clean while honoring `261115` Decision 11 (no duplication of full panel bodies in the chat stream; only question items gain inline interactive rendering).
   - **Rejected:**
     - Absorbing directly into `261115-feat-ws-dashboard-conversation-rendering-consolidation` (`c`) — rejected because `261115` Phase 1 is already landed and its deferred Phase 2 covers delivery identity / typed reports rather than interactive board widgets.
2. **Two-way real-time staging synchronization with the right-hand whiteboard and composer `<wb:answers>` queue — `(1698)=a`**
   - Render linked question items inside each chat-stream `.wbupdate-topic` card with their live content and state (title, policy options & recommendation, check/ack controls, item note input, hold toggle, and current `open` / `held` / `sent` / `settled` status), while keeping the right-hand jump link (`↗`) intact.
   - Interactions in the chat-stream item card (selecting a policy option, toggling check/ack/no, typing an item note, toggling `보류`) synchronize bidirectionally in real time with the right-hand whiteboard (`#r-board`) and the shared composer `<wb:answers>` staging chips/capsules (and reflect `.sentdata` in-flight `.sent-held` / `.sent-sent` and settled answers as SSE updates arrive).
   - **Rejected:**
     - Adding a separate one-click direct-submit path bypassing composer staging (`b`) — rejected to preserve a single unified answer staging/submission model across chat and whiteboard without duplicate submissions or state divergence.
3. **Compatibility tier: T0 (transparent)**
   - Additive dashboard view/hydration data and frontend synchronization; existing `wslive` ring entries, `<wb:answers>` wire format, and older clients remain unaffected.
4. **Duplicate item references across `.wbupdate` turns: every mounted card for an item stays synchronized to live state — technical**
   - All mounted chat cards that reference the same item ID render and update from the same live item state and the same shared staging state (one pick/note/hold per item, mirrored on every instance). Instances whose item is settled collapse to the compact read-only settled summary per the settled-item constraint.
   - Optional: when an item appears in several turns, older instances may render as a compact reference line (`(1693)` + status + jump link) while only the most recent instance carries active controls, if that proves cleaner; either way no instance may diverge from live state. The implementation picks between these two renderings; both satisfy this decision.
   - **Rejected:**
     - Independent per-card state keyed by card instance — would let the same item show conflicting picks and violate the single-source-of-truth constraint.
5. **Inline item-note control mounts behind a lightweight seam; no hard block on `261126-feat-ws-unified-composer-input-component` — technical**
   - The chat-card item note is created through a single local mount function (e.g. `mountItemNote(hostEl, {value, onChange, onSubmit, onCancel})`) that returns a plain `<textarea>`-backed handle with `get`/`set`/`destroy` unless `window.WsInput` is available. When `window.WsInput` is present, the function mounts it on the plain host element via the contract settled in `261126-feat-ws-unified-composer-input-component` (global `WsInput`, no `WsComposer` alias; declarative `features` flags, `providers`, `keymapProfile`, isolated `draftAdapter` `get`/`set`/`persist`, `onChange`, `onSubmit`, `onCancel`; popovers mount in the nearest open `<dialog>`/top layer), enabling only the flags the note needs and honoring Decision 8's read-only rule for capsule-bearing board notes. The option names above follow the composer ticket's current text; the mount function binds to the final names recorded in that ticket's Result. Per Decision 6, the mounted instance's `draftAdapter` reads and writes the board `textarea.note` and never persists an independent draft. Absent `window.WsInput`, it falls back cleanly to the plain textarea, so both tickets land in parallel. No `blocked-by:` and no consumer code is added to the composer ticket.
   - **Rejected:**
     - Hard-blocking on the unified component — it is a large six-surface migration and would stall this unrelated feature.
     - Hand-rolling a bespoke capsule/mention/markdown note editor in chat cards — duplicates what the unified component will provide.

6. **The board DOM stays the single owner of staged answer state; chat cards are mirrored views — technical**
   - Staged state lives in the `#r-board` item controls (`aria-pressed` on `.opt`/`.btn`, `textarea.note`, `label.ack/ck > input`, `data-no`), read by `readAnswers`/`readItem`/`buildReply`. A chat-card interaction writes through to the matching board control and lets the existing change flow (`syncChips`, composer chips) run; chat cards re-render from board state on board change, SSE morph (`readAnswers`/`writeAnswers`), and `applySent`, with a re-entrancy guard in the style of the existing `syncingInline`. No parallel store is created.
   - A card whose item has no board DOM (e.g. item in a lazily paged closed topic) renders the compact read-only fallback: the `(N)` chip, status unknown, plus the jump link — never active controls (`setItem` is a silent no-op without a board control).
   - **Rejected:**
     - A new standalone answer store shared by chat and board — would diverge from `buildReply`/`readAnswers` and the composer chips.
7. **Live item data is hydrated client-side from `#r-board`; the server ring projection stays untouched — technical**
   - `ConvEntry.WhiteboardCard()` keeps its committed `{ID, Target}` projection (it deliberately never reads current board text; `261115` cache-friendliness). Item title/options/status come from the live board DOM / SSE snapshot on the client; the fallback for items with no live board data is Decision 6's read-only chip.
   - **Rejected:**
     - Server-side hydration of live board text into ring-entry rendering — breaks cache-friendly ring entries.
8. **Note mirroring preserves capsule state; capsule-bearing board notes are not editable from chat — technical**
   - The board `textarea.note` is capsule-capable (`readAnswers` snapshots `note.wsc`). When the board note carries capsules/attachments, the chat-card note renders read-only (text preview plus jump to the board note) so mirroring can never strip capsule state; plain-text board notes mirror bidirectionally.
   - **Rejected:**
     - Plain-text mirroring of every note — would desync or drop capsules.
9. **Active controls render only where a live board and composer share the document; hold is distinct from sent status — technical**
   - Interactive controls render only in a document hosting a live `#r-board` and the composer. History/worktree views, ended sessions, and embedded `?frame=1` session views (e.g. the agent-card mount in `261115-feat-ws-whiteboard-agent-card` Phase 2) get the compact read-only form.
   - User hold (browser-local `pill.js` `localStorage` store, never sent, cleared on answer) and sent state (`.sent-held`/`.sent-sent` from `applySent`) are separate in the card's status vocabulary; a chat hold toggle calls `wbPill.toggleHold` through the board and renders only where the board item has a `.wb-hold-toggle`.
   - **Rejected:**
     - Active controls in every chat document — they would write to a missing board and silently no-op.

## Constraints

- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, agents-plugin-tool/internal/wswhiteboard/assets/)
- **Applicable manuals (read before editing):**
  - `ai-docs/manuals/dashboard-style.md` (compact tonal card density inside the chat stream so inline question cards stay scannable without dominating conversation flow).
  - `ai-docs/manuals/performance.md` (avoid re-rendering the entire conversation stream on every whiteboard SSE tick or option click; hydrate/patch only mounted item cards cleanly, and obey frontend minimal-update budgets).
  - `ai-docs/manuals/release-compatibility.md` (T0).
  - `ai-docs/manuals/shipped-surface-boundary.md`.
- **Single source of truth for answer staging:**
  - Selecting option `b` on item `(1695)` in the chat stream must immediately highlight `b` on `#r-board` and stage the single `(1695)=b` chip/capsule in the composer (and vice versa). Removing the composer chip/capsule or sending the message must clear/transition both surfaces identically.
  - Staged picks and notes in chat-stream item cards must survive incoming board SSE morphs (`readAnswers`/`writeAnswers`) and conversation stream updates without losing focus or typing.
- **No duplicate panel bodies:** Preserve `261115` Decision 11 — panel entries inside `.wbupdate-topic` remain recap + jump link only.
- **Historical & settled item behavior:** Items that are already settled (`answered`, `assumed`, `withdrawn`, `superseded`) or belong to closed topics should render in a compact read-only settled summary state (showing recorded answer/status + jump link) rather than showing stale active controls.
- **Duplicate item references across multiple `.wbupdate` turns:** If an item is created in one turn and patched/re-asked in a later turn, every mounted card reflects the item's live state consistently (Decision 4).
- **Note input seam:** The chat-card item note uses the mount seam of Decision 5; this ticket neither requires nor edits the unified composer component.

## Prior Art

- Chat-stream whiteboard entry templates & presentation: `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl` (`whiteboardentry`, `whiteboardtopics`, #L398-L400), `agents-plugin-tool/internal/wsdashboard/conversation_presentation.go` (`ConvEntry.WhiteboardCard`, #L126), `agents-plugin-tool/internal/wsdashboard/conversation_board_navigation.go`.
- Chat-stream board jump wiring: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.js` (`effectJump` #L244, `conversationBoardActivate` #L366).
- Right-hand whiteboard controls, staging & sync: `agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl` (`.card.policy`, `.arow`, `.ckrow`), `agents-plugin-tool/internal/wswhiteboard/assets/form.js` (`buildReply` #L177), `agents-plugin-tool/internal/wswhiteboard/assets/answers.js`, `agents-plugin-tool/internal/wswhiteboard/assets/pill.js` (`toggleHold`), `agents-plugin-tool/internal/wsdashboard/assets/dashboard.js` (`readAnswers` #L820, `writeAnswers` #L858; not in `answers.js`), `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer.js` (`syncChips` #L146, `putChip` #L101, `dropChip` #L109), `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-status.js` (`applySent` #L456). (the unprefixed `internal/...` paths originally written resolve only under `agents-plugin-tool/`)

## Prior Decisions

- 84cd8692 (2026-11-26, commit): "Disabled browser scroll anchoring on .convo (live + history) and guarded programmatic scrollTop writes ... so stick===false never flips to true without a genuine downward user scroll gesture" — bearing: constrains
- 887dd1b6 (2026-11-22, commit): "Hydrate anchored board answers, topic notes, panel mentions ... inline as compact single-line .icapsule pills in both server-rendered history/held HTML and optimistic client outboxBubble rendering" — bearing: constrains
- 261115-feat-ws-whiteboard-panel-first-stream (2026-11-15, Decisions): "One ordered topic stream. Inside a topic, wb:log, wb:panel and wb:add question commands interleave in write order." — bearing: constrains
- acb199be (2026-11-15, commit): "Alternative rejected: items (wb:add) also emit system events, but each adds a question card to the topic and distorts its layout." — bearing: constrains
- 261115-feat-ws-whiteboard-topic-interleaved-timeline (2026-11-15, Result): "Secondary coalesced and eventless question articles now have distinct, stable, namespaced render keys, so Viewed deferral compares the exposed question." — bearing: constrains
- f203fd28 (2026-11-12, commit): "Staging only, no toggle: a second click is a no-op; a card already picked (any letter) keeps its pick" — bearing: constrains
- 82a7e7fc (2026-11-12, commit): "whiteboard answers come before the typed chat text, in a compact agent-readable block, and render as capsules like the composer chips" — bearing: supports
- 261112-feat-ws-dashboard-search-palette (2026-11-12, Decisions): "Scope (whiteboard (474) a): the current session's board (topic names, bodies, items, answers)" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | dashboard.tmpl, conversation_presentation.go, dashboard-conversation.js, dashboard-composer.js, dashboard-composer-status.js, pill.js under agents-plugin-tool/internal/ |
| scope.surface | cross-module | spans wsdashboard and wswhiteboard assets; shared readAnswers/writeAnswers in dashboard.js |
| scope.new_public_symbol | no | hydration is client-side per Decision 7; no Go view-model field is added (only a client-side note-mount function per Decision 5) |
| scope.new_type_contract | no | conversationEffectCard (conversation_presentation.go#L116) and the ring projection stay unchanged per Decision 7 |
| scope.test_surface | new-files | existing wsdashboard Go tests and testdata/*.test.cjs harnesses; new Node DOM harness named in Verification |
| complexity.reuse_points | confirmed | readAnswers/writeAnswers, syncChips/putChip/dropChip, applySent, pill.js hold storage all exist |
| complexity.side_effect_risk | high | chat-card controls share answer staging with board, composer chips and localStorage holds across SSE morphs |
| risk.correctness | high | bidirectional sync with SSE morph, sent states and duplicate item refs across turns |
| risk.fit | moderate | the composer ticket 261126-feat-ws-unified-composer-input-component is ready but unlanded; the note input goes through the Decision 5 mount seam, so no hard dependency |
| risk.test | moderate | DOM harness needed for two surfaces plus host-neutral Go tests and a headless Chromium check |
| risk.security_or_contract | low | T0 additive; no wire or MCP surface change per Decisions 3, but unverified by implementation |

## Phases

### Phase 1: Hydrated interactive question items in chat-stream `.wbupdate-topic` cards with bidirectional whiteboard sync

- **Live item data hydration (client-side, Decision 7):**
  - Supply live whiteboard item state (id, class, status, title, options, recommendation, rationale/why disclosure, recorded answer/quote) to mounted `.wbupdate-topic` cards in the chat stream via client-side hydration from `#r-board` / SSE snapshot (Decision 7), keeping historical ring entries untouched and cache-friendly. Items without live board data, and documents without a live board and composer, render the compact read-only fallback (Decisions 6, 9).
- **Interactive chat-stream item card UI:**
  - Render compact interactive item cards under `.wbupdate-topic` for linked items: policy option buttons (with recommended badge), check/ack toggles (`ok`/`no`), item note input, hold (`보류`) toggle, and jump-to-board (`↗`) affordance.
  - Create the item note input only through the Decision 5 mount seam (plain `<textarea>` handle today).
  - Render already-settled / withdrawn / superseded items in a compact read-only state showing the recorded decision and status.
- **Bidirectional synchronization with `#r-board`, `pill.js`, and composer `<wb:answers>`:**
  - Implement chat cards as views over the `#r-board` item controls (Decision 6): every chat-card pick, note, and hold is applied to the board DOM control and its `change` flow, so `#r-board` controls, composer `<wb:answers>` chips/capsules, `.sentdata` (`.sent-held`/`.sent-sent`) indicators, and `localStorage` holds stay strictly synchronized in real time.
  - Update mounted chat-stream item cards incrementally on SSE board/sent updates without forcing full conversation re-renders or disrupting scroll lock.

**Verification expectations:**
- Go verification (host-neutral, run on whichever host the worker uses): `go test -race ./internal/wsdashboard/...` and `GOOS=windows go vet ./...` (from `agents-plugin-tool/`), showing `.wbupdate-topic` rendering and the existing `whiteboard-effects` ring projection (`{ID, Target}` rows) stay unchanged; item hydration/state coverage lives in the Node DOM harness.
- Node DOM harness tests covering:
  - Selecting a policy option, toggling check/ack, typing a note, or toggling `보류` in a chat-stream item card updates `#r-board` and composer `<wb:answers>` staging identically (and reverse: interacting on `#r-board` or removing a composer chip updates the chat-stream card).
  - Sending the composer message transitions both chat card and `#r-board` to `.sent-held`/`.sent-sent`, and settling via SSE transitions the chat card to compact read-only settled state.
  - No regression in right-hand jump (`↗`) navigation or chat scroll lock.
  - Duplicate item references: two mounted cards for one item stay in sync on pick/note/hold and on settle (Decision 4).
  - Fallbacks: a card with no board DOM, a capsule-bearing board note, and a read-only document (no live board/composer) render the Decision 6/8/9 read-only forms and never write; hold toggle appears only where the board item has one.
  - Phase is done when all the above pass and the Playwright check shows chat and right-hand answering interchangeable.
- Headless Chromium (Playwright) check of answering from chat vs right-hand whiteboard in both light and dark themes, with a result capture.
