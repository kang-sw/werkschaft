---
title: "대화창 토픽 카드 질문 셀로판 링크 캡슐 및 실시간 상태·응답 연동"
related:
  261115-feat-ws-dashboard-conversation-rendering-consolidation: extends Phase 1 .wbupdate-topic cards with hydrated cellophane question link capsules and live outside dim status/answer display
  261126-feat-ws-board-topic-header-hold-counts-and-rail-dot: reads hold state exclusively via pill.js entrypoints so chat-stream held indicators stay compatible with daemon-persisted holds
sage-review-design: pending
sage-review-completeness: pending
---

# Cellophane Question Link Capsules & Live Status Display in Chat Stream Topic Cards

## Background

In `261115-feat-ws-dashboard-conversation-rendering-consolidation` (Phase 1), whiteboard updates in the conversation stream were consolidated into grouped `.wbupdate-topic` cards showing topic headers, jump links (`↗`), `L` log lines, panel recaps, and bare item ID chips (`(1693)`). `ConvEntry.WhiteboardCard()` intentionally emits committed ring-projection `{ID, Target}` rows without live board text so ring entries stay cache-friendly.

Because bare `(1693)` chips carry neither the question class/title nor live status, a user scanning the conversation stream cannot tell what was asked, whether an item is open/held/staged/sent/settled, or what decision was recorded without jumping to `#r-board`. During visual mockup iteration (`(21)`, `(25)`, `(27)`), duplicating full option buttons and note editors inside `.wbupdate-topic` proved cramped and noisy. Instead, wrapping only the question link in a thin, single-line cellophane-tint capsule — color-coded by real `#r-board` item class with a compact inline class micro-badge — and displaying live status and recorded answers **outside** the capsule in a quiet dim tone keeps the chat stream compact, effortlessly scannable, and synchronized in real time with `#r-board`.

Approved visual reference: `ai-docs/assets/mockups/261126/chat-stream-question-cards/mockup.html` (v3.1).

## Decisions

1. **Dedicated follow-up ticket linked via `related:` to `261115-feat-ws-dashboard-conversation-rendering-consolidation` — `(1698)=a`**
   - Track this feature as its own ticket rather than absorbing a new phase into `261115`, preserving `261115` Decision 11 (panel entries inside `.wbupdate-topic` remain recap + jump link only, never duplicating full panel bodies).
   - **Rejected:**
     - Absorbing directly into `261115-feat-ws-dashboard-conversation-rendering-consolidation` (`c`) — `261115` Phase 1 is already landed and its deferred Phase 2 targets delivery identity / typed reports.

2. **Thin single-line cellophane-tint question link capsule + outside dim live status/answer display — `(21)`, `(25)`, `(27)` (supersedes `(1698)` inline option/note editors)**
   - Inside `.wbupdate-topic`, each linked question item renders as a compact two-part unit matching `ai-docs/assets/mockups/261126/chat-stream-question-cards/mockup.html`:
     - **Question link capsule (strictly <= 1 line, clickable jump to `#r-board`):**
       - Thin translucent cellophane capsule (`height: 22px`, `~9%` fill / `~16%` hairline edge warming gently on hover) with `white-space: nowrap; overflow: hidden; text-overflow: ellipsis` so it never wraps past one line.
       - Content order: monospace `(ID)` + compact subtle inline class micro-badge (`height: 15px`, e.g., `정책` / `확인` / `기술` / `공지`, localized via `wsi18n`, no separate legend strip) + question title + subtle hover `↗` jump indicator.
       - Cellophane tint hue derives from the real `#r-board` class tokens in `agents-plugin-tool/internal/wswhiteboard/assets/page.css`:
         - `policy`: warm orange/amber (`--wb-accent-policy`, `#e0a36c`)
         - `technical`: cobalt blue (`#7aa2ff`)
         - `announced`: emerald (`--wb-ck-ok`, `#6cc39a`)
         - `check`: neutral slate (`--wb-muted-fg`, `#a2a6ad`)
       - Clicking the capsule jumps to, scrolls into view, and pulse-highlights the corresponding interactive card in `#r-board` via the existing board jump flow (`effectJump` / `conversationBoardActivate`).
     - **Outside dim status & answer display (outside the capsule, quiet low-contrast tone):**
       - No bright/saturated pill badges. Live status and answer text render outside the capsule in calm dim ink:
         - Open unanswered: quiet dim unanswered marker (e.g., `미응답`)
         - Held: quiet dim held marker (`보류`), visually distinct from in-flight sent status
         - Staged on `#r-board`: quiet dim staged pick (`b 선택됨`, `ok 선택됨`, etc.) plus optional truncated plain-text memo preview (capsule-bearing board notes show safe plain-text preview only and never mutate board note state)
         - In-flight (`.sent-held` / `.sent-sent` from `applySent`): quiet dim sent/waiting marker (`전송됨 · 대기`)
         - Settled (`answered`, `assumed`, `withdrawn`, `superseded`): quiet dim settled status + recorded answer/quote (e.g., `✓ 확정 · b "트랙 1b 우선 진행"`, or localized withdrawn/superseded label)
   - Answering itself remains in `#r-board` (and its shared composer `<wb:answers>` staging flow); chat-stream capsules are live, clickable jump-and-status views with zero inline option pickers or note inputs.
   - **Rejected:**
     - Duplicating full option pills, rationale accordions, and `WsInput` note inputs inside `.wbupdate-topic` (`(21)`) — visually cramped, low readability, and duplicates `#r-board` form/IME/capsule surfaces.
     - Heavy full-width dark card boxes with high-contrast status pills (`(25)`) — visually too loud inside the conversation stream.
     - Separate legend strip for class colors (`(27)`) — replaced by compact inline `[정책/확인/기술/공지]` micro-badges right after `(ID)`.
     - Separate one-click direct-submit path bypassing `#r-board` and composer `<wb:answers>` staging (`(1698)`).

3. **Compatibility tier: T0 (transparent)**
   - Purely additive frontend client-side hydration and styling; `ConvEntry.WhiteboardCard()` ring projections, `<wb:answers>` wire format, and older binaries/clients are completely unaffected.

4. **Client-side hydration from `#r-board` with `#r-board` DOM as single source of truth — technical**
   - `ConvEntry.WhiteboardCard()` preserves its committed `{ID, Target}` projection (cache-friendly ring entries, zero server-side board text reads).
   - Mounted `.wbupdate-topic` item rows hydrate class, title, open/settled status, recorded answer/quote, staged pick/note, and sent state on the client from `#r-board` / `applySent`.
   - Every mounted `.wbupdate-topic` reference to the same item ID (across multiple turns) stays synchronized to the same live item state.
   - When an item has no live `#r-board` DOM (e.g., lazily paged closed topic, history/worktree view, ended session, or embedded `?frame=1` view), it gracefully falls back to the compact `(ID)` link chip + jump link without errors.
   - **Rejected:**
     - Server-side hydration of live board text into ring entries — breaks cache-friendly ring projections.
     - A parallel client-side answer store — risks diverging from `#r-board` DOM and `readAnswers`/`buildReply`.

5. **Hold state delegated exclusively through `pill.js` entrypoints — technical**
   - Chat-stream held indicators must query and observe hold state strictly through `pill.js`'s public entrypoint (`isHeld` and hold-change notifications/DOM state), **never** by reading or writing `localStorage` `ws.wb.holds.*` directly.
   - This guarantees 100% merge-order compatibility with `261126-feat-ws-board-topic-header-hold-counts-and-rail-dot`, which migrates hold persistence to daemon `.board.sqlite` `item_holds` while preserving `pill.js` `toggleHold` / `isHeld` signatures.
   - User hold and in-flight sent status (`.sent-held` / `.sent-sent`) remain distinct in the outside dim status vocabulary.
   - **Rejected:**
     - Direct `localStorage` `ws.wb.holds.*` reads in chat-stream hydration — would break once daemon-persisted holds land.

6. **Surgical i18n reuse across all locale tables — technical**
   - Reuse existing `wsi18n` labels for item classes and statuses where present; add minimal surgical keys across all locale JSON tables in `agents-plugin-tool/internal/wsi18n/tables/` only where missing, preserving existing key order (no whole-file re-sorting).

## Constraints

- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`, `agents-plugin-tool/internal/wswhiteboard/assets/`)
- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- **Visual parity with approved mockup:** Match the compact density, 1-line cellophane capsule proportions (`height: 22px`, `~9%` tint, inline class micro-badge, `ellipsis` truncation), and outside dim answer typography of `ai-docs/assets/mockups/261126/chat-stream-question-cards/mockup.html` in both dark and light themes.
- **Minimal DOM updates & scroll-lock safety (`performance.md`):** Never re-render the full `.convo` stream on board SSE ticks, `#r-board` answer clicks, note typing, hold changes, or composer chip edits; patch only the matching mounted item capsule/status nodes incrementally and never disturb `.convo` scroll anchoring / stick state (`84cd8692`).
- **No duplicate panel bodies:** Preserve `261115` Decision 11 — panel entries inside `.wbupdate-topic` stay recap + jump link only.
- **No direct `localStorage` hold access:** Strictly use `pill.js` `isHeld` / board DOM hold state (Decision 5).
- **Zero dependency on `WsInput`:** Because `.wbupdate-topic` carries no inline note editor, there is no dependency on `261126-feat-ws-unified-composer-input-component`.

## Prior Art

- Chat-stream whiteboard entry templates & presentation: `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl` (`whiteboardentry`, `whiteboardtopics`), `agents-plugin-tool/internal/wsdashboard/conversation_presentation.go` (`ConvEntry.WhiteboardCard`), `agents-plugin-tool/internal/wsdashboard/conversation_board_navigation.go`.
- Chat-stream board jump wiring: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-conversation.js` (`effectJump`, `conversationBoardActivate`).
- Right-hand whiteboard class tokens, cards, hold & staging sync: `agents-plugin-tool/internal/wswhiteboard/assets/page.css` (`--wb-policy`, `--wb-accent-policy`, `--wb-technical`, `--wb-announced`, `--wb-ck-ok`, `--wb-muted-fg`), `agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl` (`.card.policy`, `.arow`, `.ckrow`), `agents-plugin-tool/internal/wswhiteboard/assets/pill.js` (`isHeld`, `toggleHold`), `agents-plugin-tool/internal/wsdashboard/assets/dashboard.js` (`readAnswers`, `writeAnswers`), `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer.js` (`syncChips`, `putChip`, `dropChip`), `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-status.js` (`applySent`).

## Phases

### Phase 1: Hydrated cellophane question link capsules & live outside dim status/answer display in `.wbupdate-topic`

- **Cellophane 1-line capsule & outside dim status styling (dark + light theme):**
  - Style linked question items inside `.wbupdate-topic` matching `ai-docs/assets/mockups/261126/chat-stream-question-cards/mockup.html`: thin `22px` 1-line translucent cellophane capsule (`(ID)` + compact `[class]` micro-badge + single-line ellipsized title + hover `↗`) tinted from `page.css` class colors (`policy`, `technical`, `announced`, `check`), with live status & answer text rendered outside the capsule in a quiet dim tone.
  - Ensure light theme uses legible, matching low-alpha cellophane tints and dim outside ink as well as dark theme.
- **Client-side hydration & incremental live sync:**
  - Hydrate mounted `.wbupdate-topic` item links from live `#r-board` DOM (class, title, open/settled status, recorded answer/quote, staged pick & memo preview), `pill.js` (`isHeld`, never direct `localStorage`), and `applySent` (`.sent-held` / `.sent-sent`).
  - Subscribe incrementally to `#r-board` input/change events, SSE board morphs (`writeAnswers` / morph completion), `pill.js` hold updates, composer chip removals (`dropChip`/`syncChips`), and `applySent`, patching only the affected item rows across all mounted `.wbupdate-topic` cards (including duplicate references across turns) without full stream re-render or scroll-lock disruption.
  - Keep clicking the capsule wired to jump, scroll, and pulse-highlight the target card in `#r-board`.
  - Keep graceful fallback to the existing compact `(ID)` link chip when `#r-board` lacks the item (closed/lazily paged topic, history/worktree view, ended session, `?frame=1` frame).
- **Surgical i18n keys:**
  - Wire localized class micro-badge labels and short outside status strings using `wsi18n` (reusing existing keys where available and appending any missing keys surgically without re-sorting locale tables).

**Verification expectations:**
- Go verification (from `agents-plugin-tool/`): `go test -race ./internal/wsdashboard/... ./internal/wswhiteboard/... ./internal/wsi18n/...` and `GOOS=windows go vet ./...`, confirming `.wbupdate-topic` server rendering and committed `{ID, Target}` ring projections stay unchanged and all i18n tables validate cleanly.
- Node DOM harness tests covering:
  - Hydrating all 4 item classes (`policy`, `technical`, `announced`, `check`) into 1-line cellophane capsules with inline class micro-badges and outside dim status text.
  - Real-time `#r-board` → chat-stream sync: picking an option (`a`/`b`/`c`), toggling check `ok`/`no`, typing a note (including safe read-only preview of capsule-bearing board notes), toggling hold via `pill.js` `toggleHold`/`isHeld` (verified with zero direct `localStorage` `ws.wb.holds.*` reads), removing a composer chip, transitioning to `.sent-held`/`.sent-sent`, and settling via SSE (`✓ 확정 · ...`).
  - Duplicate item references across multiple `.wbupdate-topic` turns staying synchronized without full-stream re-render or scroll-lock drift.
  - Capsule click triggering jump/highlight to the matching `#r-board` card.
  - Graceful fallback to plain `(ID)` link chip when `#r-board` DOM is absent or lacks the item.
- Headless Chromium (Playwright) visual verification in both **dark** and **light** themes with result captures compared side-by-side against `ai-docs/assets/mockups/261126/chat-stream-question-cards/mockup.html` (verifying 1-line capsule truncation, cellophane class tint distinction, outside dim answer legibility, and `#r-board` jump highlight).
