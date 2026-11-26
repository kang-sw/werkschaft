---
title: "대화창 토픽 카드 질문 아이템 연동 렌더링 및 화이트보드 양방향 응답"
related:
  261115-feat-ws-dashboard-conversation-rendering-consolidation: extends Phase 1 .wbupdate topic cards where item option/answer controls were deferred
  261126-feat-ws-unified-composer-input-component: shared inline note input for item notes
---

# Interactive Question Items Linked in Chat Stream Topic Cards

## Background

In `261115-feat-ws-dashboard-conversation-rendering-consolidation` (Phase 1), whiteboard updates in the conversation stream were consolidated into grouped `.wbupdate-topic` cards showing topic headers, jump links (`↗`), `L` log lines, panel recaps, and plain item ID links (`(1693)`). As noted in that ticket's Phase 1 Result, interactive item previews and answer controls were deliberately omitted because Phase 1 kept `ConvEntry.WhiteboardCard()` limited to committed ring-projection `{ID, Target}` pairs without live board binding.

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

## Constraints

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
- **Duplicate item references across multiple `.wbupdate` turns:** If an item is created in one turn and patched/re-asked in a later turn, both chat cards must reflect the item's live state consistently (or collapse older superseded instances gracefully — exact UX detail settled at `ready/` promotion).

## Prior Art

- Chat-stream whiteboard entry templates & presentation: `internal/wsdashboard/assets/dashboard.tmpl` (`whiteboardentry`, `whiteboardtopics`), `internal/wsdashboard/conversation_presentation.go` (`ConvEntry.WhiteboardCard`), `internal/wsdashboard/conversation_board_navigation.go`.
- Chat-stream board jump wiring: `internal/wsdashboard/assets/dashboard-conversation.js` (`effectJump`, `conversationBoardActivate`).
- Right-hand whiteboard controls, staging & sync: `internal/wswhiteboard/assets/page.tmpl` (`.card.policy`, `.arow`, `.ckrow`), `internal/wswhiteboard/assets/form.js` (`buildReply`), `internal/wswhiteboard/assets/answers.js`, `internal/wswhiteboard/assets/pill.js` (`toggleHold`), `internal/wsdashboard/assets/dashboard-composer.js` (`syncChips`, `putChip`, `dropChip`), `internal/wsdashboard/assets/dashboard-composer-status.js` (`applySent`).

## Phases

### Phase 1: Hydrated interactive question items in chat-stream `.wbupdate-topic` cards with bidirectional whiteboard sync

- **Live item data hydration (server/client view model):**
  - Supply live whiteboard item state (id, class, status, title, options, recommendation, rationale/why disclosure, recorded answer/quote) to mounted `.wbupdate-topic` cards in the chat stream (either via additive board-state hydration from `#r-board` / SSE snapshot or additive view-model fields, keeping historical ring entries untouched and cache-friendly).
- **Interactive chat-stream item card UI:**
  - Render compact interactive item cards under `.wbupdate-topic` for linked items: policy option buttons (with recommended badge), check/ack toggles (`ok`/`no`), item note input, hold (`보류`) toggle, and jump-to-board (`↗`) affordance.
  - Render already-settled / withdrawn / superseded items in a compact read-only state showing the recorded decision and status.
- **Bidirectional synchronization with `#r-board`, `pill.js`, and composer `<wb:answers>`:**
  - Route all chat-card picks, notes, and holds through the same state/sync bridge as `#r-board` so `#r-board` controls, composer `<wb:answers>` chips/capsules, `.sentdata` (`.sent-held`/`.sent-sent`) indicators, and `localStorage` holds stay strictly synchronized in real time.
  - Update mounted chat-stream item cards incrementally on SSE board/sent updates without forcing full conversation re-renders or disrupting scroll lock.

**Verification expectations:**
- Remote `w5090` Go tests (`GOOS=windows go vet ./...` and `go test -race ./internal/wsdashboard/...`) covering `.wbupdate-topic` rendering, item hydration/state projection, and compatibility with existing `whiteboard-effects` ring entries. Never run local `go test` on Windows.
- Node DOM harness tests covering:
  - Selecting a policy option, toggling check/ack, typing a note, or toggling `보류` in a chat-stream item card updates `#r-board` and composer `<wb:answers>` staging identically (and reverse: interacting on `#r-board` or removing a composer chip updates the chat-stream card).
  - Sending the composer message transitions both chat card and `#r-board` to `.sent-held`/`.sent-sent`, and settling via SSE transitions the chat card to compact read-only settled state.
  - No regression in right-hand jump (`↗`) navigation or chat scroll lock.
- Live dashboard check verifying answering from chat vs right-hand whiteboard in both dark and light themes.
