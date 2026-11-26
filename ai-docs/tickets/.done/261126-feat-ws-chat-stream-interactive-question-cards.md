---
title: "대화창 토픽 카드 질문 셀로판 링크 캡슐 및 실시간 상태·응답 연동"
related:
  261115-feat-ws-dashboard-conversation-rendering-consolidation: extends Phase 1 .wbupdate-topic cards with hydrated cellophane question link capsules and live outside dim status/answer display
  261126-feat-ws-board-topic-header-hold-counts-and-rail-dot: reads hold state exclusively via pill.js entrypoints so chat-stream held indicators stay compatible with daemon-persisted holds
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 177d2eeb9ef67368
sage-review-completeness-reviewed: 177d2eeb9ef67368
completed: 2026-11-26
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
       - Cellophane tint hue follows the `#r-board` class color families in `agents-plugin-tool/internal/wswhiteboard/assets/page.css`; dark-theme tints are the mockup's `--qcm-c-*` RGB triples, light-theme tints per Decision 7:
         - `policy`: warm orange/amber (dark `#e0a36c` = `--wb-accent-policy` dark value)
         - `technical`: cobalt blue (dark `#7aa2ff`, a mockup-defined value; `page.css` `--wb-technical` is the darker `#26385e` fill, so it is not a token match)
         - `announced`: emerald (dark `#6cc39a` = `--wb-ck-ok` dark value)
         - `check`: neutral slate (dark `#a2a6ad` = `--wb-muted-fg` dark value)
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

7. **Capsule tint source per theme — technical**
   - Dark theme uses the mockup's fixed `--qcm-c-*` RGB triples as the approved visual reference (policy `224 163 108`, technical `122 162 255`, announced `108 195 154`, check `162 166 173`).
   - The mockup is dark-only, so light theme derives each tint from the `page.css` light-theme class tokens (`--wb-policy` `#b25000`, `--wb-technical` `#2457c5`, `--wb-announced` `#1d7348`, `--wb-muted-fg` `#5f646b`) at the same low alpha, adjusted by capture comparison until the hairline edge and outside dim ink stay legible.
   - Defined as dedicated capsule CSS custom properties (RGB triples) so alpha stays tunable; not consumed directly from `page.css` fills, whose dark values (`--wb-technical` `#26385e`) are too dark for a cellophane tint.
   - **Rejected:**
     - Consuming `--wb-policy` / `--wb-technical` / `--wb-announced` fills directly — dark-theme values are low-luminance badge backgrounds, not hue tints, and would not match the approved mockup.

8. **Settled-item class and anchor via additive `data-item` / `data-class` attributes on the live settled board rows — technical**
   - On the live `#r-board`, settled items render as `historyitem` rows (root `srow wb-hitem {{.Class}}`, `id="item-N"` only with `HistoryAnchor`), as the `original-question` row from `timelinequestion`, and settled checks through the settled branch of the `check` template (`ckrow`, `check-N`). The legacy `settled`/`srow` template is not reached by the live `topic` template, so attributes there would reach no live row. Class and item identity are not reliably readable from these rows by a stable attribute; a settled-at-load or settled-after-mount item would otherwise lose its tint and answer lookup.
   - Add additive `data-item="<ID>"` and `data-class="<class>"` attributes to the `historyitem` root, the `original-question` row, and the settled `ckrow` in `agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl` (server-rendered HTML only; no wire format, ring projection, or JS contract change). Hydration resolves class and node by these attributes for settled items, and by the existing open-card markup for open items.
   - Because `historyitem` renders both the ask-time row and the settlement row, also add `data-state="ask|settled"` to the `historyitem` root (`original-question` is already distinct by its class); hydration derives settled status only from `data-state="settled"` rows and settled `ckrow`, never from an ask-time row's status label.
   - Row precedence for one item: the settlement row (and settled `ckrow`) drives settled status and answer; the ask-time `original-question` row of an answered item is not a settled state and supplies class/title only.
   - Fixtures for this decision are produced from real `page.tmpl` output (the `ExecuteTemplate` pattern in `commented_status_test.go`), not hand-built DOM.
   - Settled answer display reuses the board row's own rendered pick/verdict/quote text (read as `textContent`), not a client-composed phrase, so `#r-board` stays the single answer source.
   - Compatibility stays T0: attributes are inert to older clients.
   - **Rejected:**
     - A sticky per-ID class cache filled from the first open sighting — a parallel client store (Decision 4) and blind to items already settled at page load.

9. **Status precedence when states overlap — technical**
   - The outside status shows the highest of: settled (`answered`, `assumed`, `withdrawn`, `superseded`) > in-flight sent (`.sent-sent` / `.sent-held`) > staged pick or memo > user hold > open unanswered.
   - Staged outranks hold because `261126-feat-ws-board-topic-header-hold-counts-and-rail-dot` clears a hold when a draft is staged; a stale hold under a staged draft must not display. Settled outranks everything so a stale staged/held/sent residue never contradicts `#r-board`.
   - **Rejected:**
     - Hold over staged — contradicts the hold ticket's staged-clears-hold rule.

10. **Hold changes observed through board DOM state, not a new `pill.js` event — technical**
    - Option picks and check `no` toggles also change board state through `aria-pressed` / `data-no` on button clicks, not `input`/`change` events. One board-scoped attribute-and-class `MutationObserver` (extended to those attributes and to settled-row insertion) is the sync source for picks, `no` toggles, and holds, alongside `input` events for note typing.
    - Check `ok` ticks and ack staging set the `checked` property (and `bulkAnswer` sets it programmatically) without any attribute mutation or event, so the observer does not cover them: a delegated board `change` listener plus a hook on `writeAnswers` / `build()` covers the `ok` and ack staging path.
    - `pill.js` exposes only `isHeld(el)` / `toggleHold(el)` (element-based, no event), and the hold ticket rewrites `pill.js` concurrently. Observe hold through a `MutationObserver` on the board's `.wb-held` / `aria-pressed` hold-toggle state (the DOM `pill.js` itself maintains, including after server-rendered SSE swaps), reading the value with `isHeld(el)` on the board item element; no `pill.js` edit and no `localStorage` access.
    - **Rejected:**
      - A new `pill.js` hold-change event — edits a file the hold ticket is rewriting and creates merge-order coupling.

11. **Hydration also runs on live-inserted and history-paged entries — technical**
    - Hydrate a `.wbupdate-topic` when it is inserted live, paged in from older history, merged or keyed-patched in place (`mergeEntries` added rows, `mergeEntriesRegion`, `entryInputPatch` / `keyedInputPatch`), or re-rendered from stream-cache reuse, not only on later board events, so no capsule stays a bare `(ID)` chip and no patch drops client-added capsule/status nodes. Hook the existing conversation entry merge/patch path rather than a stream-wide observer.

12. **Staged memo preview reads the note backing value, flattened to plain text — technical**
    - Read the note from the same field `pill.js` `noted()` reads (`textarea.note`, or the `.wsc-backing` value once the unified composer input lands), never from editor DOM. Flatten capsule tokens (`#topic`, `[Image #N]`, mentions) to their literal text, collapse whitespace, truncate to a single ellipsized line (~60 characters), and write via `textContent` only.

13. **Outside status renders in a dedicated non-live node — technical**
    - The existing `.wbupdate-status` is a `role=status aria-live=polite` node filled by `effectJumpTo` with jump-missing messages. Live board status goes in its own new node with no live-region role, so board ticks are not announced and jump errors are not overwritten.

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

## Prior Decisions

- 261126-feat-ws-board-topic-header-hold-counts-and-rail-dot (2026-11-26, ticket Decisions): "Ask hold becomes daemon state in the board SQLite — (1701)=a, replaces the earlier browser-local decision (1697)=a" — bearing: constrains
- 261126-feat-ws-board-topic-header-hold-counts-and-rail-dot (2026-11-26, ticket Decisions): "A staged draft on a held ask clears the hold — (1702)... removing the draft later does not restore the hold" — bearing: constrains
- 261115-feat-ws-dashboard-conversation-rendering-consolidation (2026-11-15, Decisions 11): "Keep panels out of the chat body — (828)=ok. Show only the panel's one-line recap and board link such as #pN" — bearing: constrains
- 261126-feat-ws-composer-full-spec-markdown (2026-11-26, ticket Result 8345b07a): "Replaced the paint-only ::highlight(ws-md-*) renderer in .wsc-editor with a structured DOM live hybrid WYSIWYG renderer" — bearing: supports
- 14d5373d (2026-11-26, commit): "Compatibility tier: T0 transparent (frontend-only WsCards 'panel' kind, browser localStorage only, reuses existing /s/<rid>/topic?target=pN endpoint)" — bearing: supports
- 0dbed3cd (2026-11-26, commit): "T0 transparent compatibility... surgical +7-line insertions per table" across all 11 locales for wsi18n — bearing: supports
- 261115-feat-ws-whiteboard-panel-first-stream (2026-11-15, ticket Decisions): "One ordered topic stream. Inside a topic, wb:log, wb:panel and wb:add question commands interleave in write order." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | dashboard.tmpl, dashboard-conversation.js, dashboard-conversation.css, wswhiteboard/assets/page.tmpl (additive `data-item`/`data-class` on `historyitem`, `original-question`, settled `ckrow`, Decision 8), wsi18n/tables/*.json (11 locales), new Node test files |
| scope.surface | internal | frontend assets only; ConvEntry.WhiteboardCard (conversation_presentation.go#L126) projection unchanged |
| scope.new_public_symbol | no | none; client-side hydration inside existing dashboard scripts |
| scope.new_type_contract | no | none; reads existing pill.js st.isHeld (pill.js#L536) and DOM |
| scope.test_surface | new-files | existing whiteboard-effects-browser.cjs and pill-hold.test.cjs found; new Node DOM harness tests required by Verification expectations |
| complexity.reuse_points | confirmed | effectJump, conversationBoardActivate (dashboard-conversation.js), pill.js isHeld, readAnswers/writeAnswers (dashboard.js#L833, #L871), applySent (dashboard-composer-status.js#L456) |
| complexity.side_effect_risk | moderate | incremental DOM patching across .convo must not disturb scroll anchoring; shares board SSE morph and composer chip paths |
| risk.correctness | moderate | many live-sync sources (board DOM, hold, sent, chips) kept consistent across duplicate references |
| risk.fit | moderate | hold persistence changes under pending ready ticket 261126-feat-ws-board-topic-header-hold-counts-and-rail-dot; must stay behind pill.js entrypoints |
| risk.test | moderate | DOM harness plus Playwright dark/light visual checks needed; sync paths are hard to cover |
| risk.security_or_contract | low | T0 frontend-only; note previews are rendered as plain text; no wire format change, but memo preview escaping is not yet verified in tree |

## Phases

### Phase 1: Hydrated cellophane question link capsules & live outside dim status/answer display in `.wbupdate-topic`

- **Cellophane 1-line capsule & outside dim status styling (dark + light theme):**
  - Style linked question items inside `.wbupdate-topic` matching `ai-docs/assets/mockups/261126/chat-stream-question-cards/mockup.html`: thin `22px` 1-line translucent cellophane capsule (`(ID)` + compact `[class]` micro-badge + single-line ellipsized title + hover `↗`) tinted per Decision 7 from the class color families (`policy`, `technical`, `announced`, `check`), with live status & answer text rendered outside the capsule in a quiet dim tone.
  - Ensure light theme uses legible, matching low-alpha cellophane tints and dim outside ink as well as dark theme.
- **Client-side hydration & incremental live sync:**
  - Add additive `data-item` / `data-class` (+ `data-state` on `historyitem`) attributes to the `historyitem` root, `original-question` row, and settled `ckrow` in `page.tmpl` (Decision 8) so settled items keep class and anchor.
  - Hydrate mounted `.wbupdate-topic` item links from live `#r-board` DOM (class, title, open/settled status, recorded answer/quote, staged pick & memo preview per Decision 12), `pill.js` (`isHeld`, never direct `localStorage`), and `applySent` (`.sent-held` / `.sent-sent`), resolving overlapping states by the Decision 9 precedence and rendering status in the dedicated non-live node (Decision 13). Hydrate on live insertion and history paging as well (Decision 11).
  - Subscribe incrementally to `#r-board` `input` events (note typing), SSE board morphs (`writeAnswers` / morph completion), and picks / `no` toggles / holds via the Decision 10 board `MutationObserver`, composer chip removals (`dropChip`/`syncChips`), and `applySent`, patching only the affected item rows across all mounted `.wbupdate-topic` cards (including duplicate references across turns) without full stream re-render or scroll-lock disruption.
  - Keep clicking the capsule wired to jump, scroll, and pulse-highlight the target card in `#r-board`.
  - Keep graceful fallback to the existing compact `(ID)` link chip when `#r-board` lacks the item (closed/lazily paged topic, history/worktree view, ended session, `?frame=1` frame).
- **Surgical i18n keys:**
  - Wire localized class micro-badge labels and short outside status strings using `wsi18n` (reusing existing keys where available and appending any missing keys surgically without re-sorting locale tables).

**Verification expectations:**
- Go verification (from `agents-plugin-tool/`): `go test -race ./internal/wsdashboard/... ./internal/wswhiteboard/... ./internal/wsi18n/...` and `GOOS=windows go vet ./...`, confirming `.wbupdate-topic` server rendering and committed `{ID, Target}` ring projections stay unchanged and all i18n tables validate cleanly.
- Node DOM harness tests covering:
  - Hydrating all 4 item classes (`policy`, `technical`, `announced`, `check`) into 1-line cellophane capsules with inline class micro-badges and outside dim status text.
  - Real-time `#r-board` → chat-stream sync: picking an option (`a`/`b`/`c`), toggling check `ok`/`no`, typing a note (including safe read-only preview of capsule-bearing board notes), toggling hold via `pill.js` `toggleHold`/`isHeld` (verified with zero direct `localStorage` `ws.wb.holds.*` reads), removing a composer chip, transitioning to `.sent-held`/`.sent-sent`, and settling via SSE (`✓ 확정 · ...`).
  - Settled-at-load and settle-after-mount items (all 4 classes) keeping their class tint and recorded answer via `data-item` / `data-class`, with fixtures generated from real `page.tmpl` output and an answered item's ask-time row not driving settled status. A Go template test asserts the new attributes (including `data-state` on `historyitem`) on the rendered `historyitem`, `original-question`, and settled `ckrow`; a harness case covers `ok` tick, `bulkAnswer`, and ack staging syncing without an attribute mutation.
  - Keyed-patched `.wbupdate-topic` rows retaining their hydrated capsule/status nodes.
  - Overlapping states resolving by Decision 9 precedence (held + staged, staged + sent, settled + stale residue).
  - Newly inserted, history-paged, and stream-cache-reused `.wbupdate-topic` entries hydrating immediately without a board event.
  - Memo preview flattening capsule tokens to plain text via `textContent`, with no HTML injection from a hostile note.
  - Board status updates leaving the `.wbupdate-status` live region untouched.
  - Duplicate item references across multiple `.wbupdate-topic` turns staying synchronized without full-stream re-render or scroll-lock drift.
  - Capsule click triggering jump/highlight to the matching `#r-board` card.
  - Graceful fallback to plain `(ID)` link chip when `#r-board` DOM is absent or lacks the item.
- Headless Chromium (Playwright) visual verification in both **dark** and **light** themes with result captures compared side-by-side against `ai-docs/assets/mockups/261126/chat-stream-question-cards/mockup.html` (the mockup is dark-only: dark captures match it directly; light captures match its proportions, class-tint distinction, and dim-ink hierarchy, with the light legibility judged per Decision 7 and the passing light captures committed under `ai-docs/assets/captures/<date>/`) (verifying 1-line capsule truncation, cellophane class tint distinction, outside dim answer legibility, and `#r-board` jump highlight).

### Result (23c4ca1d5) - 2026-11-26

- **Landed:**
  - `9f0e9270c` (`feat(wsdashboard): hydrate chat-stream question link capsules with live status`):
    - Added additive `data-item`, `data-class`, and `data-state` (`ask` / `settled`) attributes on `historyitem`, `original-question`, and settled `ckrow` templates in `agents-plugin-tool/internal/wswhiteboard/assets/page.tmpl`, with template tests in `question_card_attrs_test.go` and updated assertions in `render_test.go`.
    - Added 10 surgical `js.wbupdate.*` i18n keys across all 11 locale tables (`agents-plugin-tool/internal/wsi18n/tables/*.json`) preserving existing key order and reusing `js.pill.held`.
    - Added 22px 1-line cellophane question link capsule styling (`.wbupdate-cap`, `.wbupdate-cap-{id,cls,ttl,arr}`) and outside dim status/answer styling (`.wbupdate-ans`, `.st-{open,staged,staged-ok,staged-no,held,sent,settled}`) with dark and light theme variables in `dashboard-conversation.css` (placed outside the display-only presentation contract section).
    - Implemented T0 client-side progressive hydration and live `#r-board` sync in `dashboard-conversation.js` (delegating hold state strictly through `pill.js` with zero direct `localStorage` `ws.wb.holds.*` reads; leaving server-rendered `dashboard.tmpl` and `.wbupdate-status` untouched), wired into `dashboard.js`, `dashboard-board.js`, `dashboard-composer.js`, `dashboard-composer-status.js`, and `dashboard-lifecycle.js`.
    - Added loopback Playwright headless Chromium acceptance test (`question_cards_browser_test.go` + `testdata/question-cards.browser.cjs`).
  - `23c4ca1d5` (`fix(wsdashboard): address lite-review findings for question link capsules`):
    - Fixed `readBoardQuestionState` precedence so an actively open item (`openEl`) always wins over historical `.wb-hitem.commented[data-state="settled"]` rows while settled rows without `openEl` still win over stale `.sent-held` / `.sent-sent`.
    - Added `wbUpdateQueryBoard` fallback into `virtualStore` (`rec.stashed` and `rec.children`) so virtualized off-window board topics keep settled/assumed capsules hydrated instead of reverting to bare `(ID)` chips.
    - Extended `wbUpdateFlattenMemo` to strip `<textref>` tags in addition to `<mailbox>` tags.
    - Added default-running Node unit test `TestChatStreamQuestionCardsUnit` (`question_cards_test.go`) and expanded `testdata/question-cards.browser.cjs` with memo-only staging, composer unchip removal, and `#r-board` focus verification on capsule jump.
- **Verification:**
  - `go test -v -count=1 -run 'TestChatStreamQuestionCardsUnit|TestConversationPresentation' ./internal/wsdashboard/...`: PASS (0.935s).
  - `WS_PLAYWRIGHT_MODULE=... go test -v -count=1 -run TestChatStreamQuestionCardsBrowser ./internal/wsdashboard/...`: PASS (1.11s, `ALL QUESTION CARD BROWSER CHECKS PASSED`).
  - `go test -count=1 -run 'TestRenderRegionsSettledRowExpands|TestQuestionCardDataAttributes' ./internal/wswhiteboard/...`: PASS (0.591s); full `go test -count=1 ./internal/wswhiteboard/...`: PASS (333.388s).
  - `go test -count=1 ./internal/wsi18n/...`: PASS (3.005s).
  - `GOOS=windows go vet ./...`: PASS (exit 0).
  - Visual comparison: `compared ai-docs/assets/captures/261126-question-cards/live-dark.png and ai-docs/assets/captures/261126-question-cards/live-light.png vs ai-docs/assets/mockups/261126/chat-stream-question-cards/mockup.html: match` (temporary capture PNGs kept untracked per task instructions).
- **Decisions / Notes:**
  - Kept temporary visual capture PNGs untracked under `ai-docs/assets/captures/261126-question-cards/` (not committed, per explicit task instruction).
  - `code-review-lite` found 0 Critical, 3 Important, 2 Minor; all 3 Important findings and 1 Minor (`<textref>` stripping) were fixed in `23c4ca1d5` and re-verified.
