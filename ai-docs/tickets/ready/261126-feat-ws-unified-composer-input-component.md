---
title: "feature-flag 기반 단일 대시보드·화이트보드 입력 컴포넌트 통합"
related:
  261126-feat-ws-composer-full-spec-markdown: shared full-spec markdown renderer for the unified editor
  261116-feat-ws-dashboard-generalized-mentions: generalized mention trigger providers
  261126-feat-ws-chat-stream-interactive-question-cards: consumer of inline note input in chat cards
sage-review-design: completed
sage-review-completeness: completed
sage-review-completeness-reviewed: 06844ee4429d3854
sage-review-design-reviewed: 06844ee4429d3854
---

# Unified Feature-Flagged Input Component Across Dashboard and Whiteboard

## Background

Text input behavior is currently fragmented across multiple surfaces in `wsdashboard` and `wswhiteboard`:

- **Main chat composer (`#ctext`)** owns markdown highlighting, `@` file mentions (`wsMention`), `/` slash command completion, `#` topic capsules (`WsCapsules`), inline/chip badges, image paste/drop, long-paste `[Text #N]`, list continuation, `!` shell mode, and recovery drafts — but `dashboard-composer-*.js` (`highlight`, `keyboard`, `pickers`, `delivery`, `exec`) is tightly coupled to document-level singletons (`#ctext`, `#composer`, `#chl`, `#cchips`, `#ccmd`, `#csend`) and global arrays (`imgs`, `textRefs`, `mentions`, `bselections`).
- **Secondary multi-line inputs** — whiteboard topic/item notes (`.tnote` / `.note`), board selection comment popover (`.wbmention-note`), close-request reason (`.close-request-reason`), workspace memo pad (`#memo-input` / `.memo-note`), and tracker support chat (`.supcomp`) — each re-implement subsets of `WsCapsules` attachment, image paste, keybindings, and auto-grow while lacking shared capabilities such as topic search, skill/slash autocomplete, markdown rendering, or consistent badge rendering.

Whenever a new input capability is added (markdown rendering, topic/skill completion, badges, paste handling), it must be re-implemented per widget or left missing everywhere outside `#ctext`. Each input keeps its own independent content/draft lifecycle; the goal is to share a single feature-flagged input component (`WsInput`) so features are built once and enabled per surface via capability flags and pluggable adapters.

## Decisions

1. **Single feature-flagged multi-line input component for Phase 1 core surfaces — `(1696)=a`**
   - Extract the `#ctext` singleton composer infrastructure into an instance-scoped, feature-flagged component with pluggable adapters:
     - **Feature flags:** `markdown`, `capsules`, `mentions` (`@`), `topics` (`#`), `slash` (`/`), `imagePaste`, `longPaste`, `listContinuation`, `bangMode`, `autoGrow`, `voice` (optional hook).
     - **Pluggable adapters (never sharing draft state across instances):** `draftAdapter` (`get`/`set`/`persist`), `keymapProfile` (`enter-send` / `mod-send` / `enter-save` / `newline-only` + `Esc` hook), `triggerProviders` (`@`, `#`, `/`), and `attachmentHandler`.
   - **Phase 1 unified target surfaces (6 surfaces):**
     1. Main chat composer (`#ctext`)
     2. Whiteboard topic & item notes (`textarea.note.tnote`, `textarea.note`)
     3. Board selection comment popover (`textarea.wbmention-note`)
     4. Close-request chip reason (`textarea.close-request-reason`)
     5. Workspace memo pad (`#memo-input` and inline `.memo-note` editors)
     6. Tracker support chat composer (`.supcomp`)
   - **Explicitly excluded from Phase 1:**
     - Single-line search/filter/rename `<input>` controls (`wb-find-input`, `au-sinp`, `sinp`, settings/rail/tracker search boxes).
     - Bash-specific `.ucmd-code` syntax overlay (`ucmdBashTokens`).
     - Tracker `.tked` editors (`cbox`, `iw-ebody`, `tkcedit`, `wk-ebody`, which carry a dedicated server-rendered Preview tab, tracker-specific `#` item / `@` member pickers, and `AD.upload` attachments — kept out of Phase 1 to avoid over-scoping; may adopt the component later via custom providers).
   - **Rejected:**
     - Including tracker `.tked` in Phase 1 (`b`) — rejected to keep Phase 1 cohesive around the session/board/memo/support surfaces that already share `WsCapsules`/session concepts.
     - Writing a research-only ticket first (`c`) — rejected; proceed as an actionable feature ticket and settle any low-level API details at `ready/` promotion.
2. **Compatibility tier: T0 (transparent)**
   - Pure frontend modularization and widget adoption; all server endpoints, `<wb:answers>` wire formats, draft storage schemas, and standalone whiteboard fallback HTML remain backward-compatible.

## Constraints

- **Applicable manuals (read before editing):**
  - `ai-docs/manuals/dashboard-style.md` (surface tokens, focus rings, compact inline sizing).
  - `ai-docs/manuals/performance.md` (no forced layout thrashing across multiple mounted instances; preserve `form.js`'s `.wsc-backing` guard to avoid synchronous `scrollHeight` flushes).
  - `ai-docs/manuals/release-compatibility.md` (T0).
  - `ai-docs/manuals/shipped-surface-boundary.md`.
- **Content & state isolation:** Every mounted instance owns isolated text, selection, undo, active trigger popover state, and external draft persistence. Typing or clearing one input must never mutate `#ctext`'s `imgs`, `textRefs`, `mentions`, or `bselections` unless explicitly wired by that surface's adapter.
- **Document-global highlight safety:** While the legacy `CSS.highlights` path is active (or whenever any CSS Highlight API group is used), multiple live instances in the same `document` must aggregate `Range`s per document rather than overwriting each other's `CSS.highlights.set(...)` entries. This applies only to the legacy `dashboard-composer-highlight.js` path: once `261126-feat-ws-composer-full-spec-markdown` lands its structured DOM renderer, no aggregation is needed for Markdown.
- **Markdown renderer seam (no hard ordering with `261126-feat-ws-composer-full-spec-markdown`):** The `markdown` feature flag mounts a renderer through `WsMdEditor.attach(editorEl, opts)` returning `{reconcile(), detach()}`, the interface the markdown ticket defines (`opts`: `getMentions()`, `isBang()`, `isComposing()`, optional `onReconciled()`). Whichever ticket lands first records the final symbol and `opts` in its Result and the other consumes it; do not add a `blocked-by:`. If the markdown ticket has not landed, the flag wraps the legacy highlight path behind the same `WsMdEditor.attach` shape with per-document `Range` aggregation, which requires de-globalizing `scanComposerMarkdown`, `applyComposerHighlights`, and the composition listeners in `dashboard-composer-highlight.js` (they read the globals `ctext`, `mentions`, `bangMode` today) while keeping `MD_HIGHLIGHT_GROUPS` on a single line for the `selection_composer_test.go` extractor.
- **Component name and flag scope:** The per-instance global is `WsInput` (`WsComposer` is not introduced as a second name; the existing `dashboard-composer-*.js` modules keep their names). The `voice` flag is out of Phase 1: no voice behavior is implemented, and the name is only reserved in the flags list.
- **Per-surface defaults (preserve today's keys and attachments):** Every surface gets `markdown`, `capsules`, `mentions`, `topics`, `slash`, `listContinuation`, and `autoGrow` on, matching the Phase 1 live check that `#` search, `/` autocomplete, and badges behave the same on all six. `imagePaste` / `longPaste` are on only for surfaces that already accept attachments or long paste today (`#ctext` always; the worker confirms each secondary surface against its current behavior) and off elsewhere. `bangMode` is `#ctext` only. Each surface's `keymapProfile` is its current submit/cancel key behavior unchanged (`#ctext` keeps the user's `enter-send` / `mod-send` setting); no surface's key behavior changes in this ticket. A worker who finds a surface cannot honor a default (for example a surface that never had attachments but must keep them off) records it as a deviation in the Result.
- **Packaging across bundles:** The memo pad page loads `mention.js` + `capsules.js` through the separate `MentionScript` bundle (`pages.go`), distinct from the session-frame composer bundle, and the standalone whiteboard is a third context. The new `WsInput` module must be registered in every bundle whose surface adopts it, and any new shared `wsc-*` CSS classes must be allowlisted in `css_collision_test.go` or scoped under `.memo-pad`, or that test fails.
- **`?frame=1` and `#ctext` IDs:** Step 2 keeps every `#ctext` / `#composer` / `#chl` / `#cchips` / `#ccmd` / `#csend` ID and the single-instance semantics of the embedded `?frame=1` session page, which `261115-feat-ws-whiteboard-agent-card` Phase 2 embeds and does not rewrite into a multi-instance shell.
- **Externally consumable inline note input:** The component's mount API must work on any plain host element (not only the six Phase 1 surfaces) with `draftAdapter`, `onChange`, `onSubmit`, and `onCancel`, so a later consumer such as `261126-feat-ws-chat-stream-interactive-question-cards` can mount an inline item-note input without importing session-frame globals. No consumer-specific code lands in this ticket.
- **Iframe & packaging boundaries:**
  - `#memo-pad` lives in the outer shell document while `#ctext` and the whiteboard live inside `iframe.sframe`; the component must not assume session-frame globals and must accept cross-frame topic/command providers cleanly.
  - Standalone `wswhiteboard` HTML (`page.tmpl` / `render.go`) does not bundle `wsdashboard` assets; `.note`/`.tnote` must degrade gracefully to plain `<textarea>` when the unified component module is absent.
- **Top-layer & SSE DOM-swap compatibility:**
  - Autocomplete popovers (`@`/`#`/`/`) must mount inside the nearest open `<dialog open>` (or top-layer popover) when triggered inside a modal/drawer so `<dialog>::backdrop` never clips or hides them.
  - Dashboard SSE board swaps (`editing()` in `dashboard.js`) must continue to detect active focus inside unified `.wsc-editor` inputs inside `#bcol` so live typing is never interrupted by an incoming board morph.
- **File size discipline:** Create clean modular files rather than expanding already-large `dashboard-composer*.js` or `dashboard-board*.js` monoliths.
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, agents-plugin-tool/internal/wswhiteboard/assets/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Art

- Element-scoped primitives already suitable as building blocks: `capsules.js` (`WsCapsules.attach(textarea)`), `mention.js` (`window.wsMention`).
- Existing cross-frame topic provider hook: `memo.js` reading `sourceWindow.wsCapsuleTopics`.
- Existing `<dialog>` top-layer tooltip mounting pattern: `tracker-render.js` (`#tktip`).

## Prior Decisions

- 261116-bug-ws-dashboard-composer-autosize-forced-layout (2026-11-16, ticket Decisions): "capsules.js native editor input stops propagation and forwards once to the backing field; retain that notification contract rather than removing the composer's only input." — bearing: constrains
- 261116-bug-ws-dashboard-composer-autosize-forced-layout (2026-11-16, ticket Result): "composer-autosize.test.cjs and capsules.test.cjs exercise production autosize setup and input listener with visible-field geometry getters" — bearing: constrains
- b5e02c73 (2026-11-26, commit): "Coalesced rAF revealCaret triggers only on programmatic newline insertions, leaving grow() and the input listener completely layout-read-free (verified by composer-autosize.test.cjs)." — bearing: constrains
- 07f1d093 (2026-11-25, commit): "Unified Ctrl/Cmd+Shift+V raw-paste bypass in capsules.js onto one module-level rawPasteArmed/rawPasteActive state ... updated dashboard-composer-textrefs.js to check rawPasteBypass() first." — bearing: constrains
- 261115-feat-ws-composer-inline-skill-and-topic-capsules (2026-11-15, commit eb0ef4d4): "Phase 1 complete and closed after exact integration; owner-screen acceptance remains MAIN-owned" — bearing: supports
- 51af100f (2026-11-26, commit): "Kept dashboard-composer-delivery.js untouched (already 1010 lines) by hooking #r-convlive via MutationObserver in dashboard-composer-status.js" — bearing: constrains
- 7dcd594e (2026-11-26, commit): "Implements ... client-side performance bottlenecks (#1-#8 forced reflows + Overview content-visibility/contain ...); T0 transparent" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsdashboard/assets/dashboard-composer*.js, capsules.js, mention.js, memo.js, dashboard-tracker.js, dashboard-board.js, dashboard.js; wswhiteboard/assets/form.js, page.tmpl, render.go |
| scope.surface | cross-module | spans wsdashboard and wswhiteboard embedded assets plus the cross-frame shell/iframe boundary; no Go exported symbol named |
| scope.new_public_symbol | yes | new WsInput window-level component (no WsInput class exists: search of agents-plugin-tool/internal returned nothing) |
| scope.new_type_contract | yes | features flags, providers, keymap profiles, draft/onChange/onSubmit/onCancel adapter contract |
| scope.test_surface | new-files | existing testdata/composer-autosize.test.cjs, capsules.browser.cjs, memo_test.cjs, note-typing.browser.cjs, support_chat_test.go; multi-instance isolation and flag-gating harness are new |
| complexity.reuse_points | confirmed | capsules.js WsCapsules.attach (.wsc-editor, .wsc-backing at capsules.js#L310), mention.js wsMention, memo.js wsCapsuleTopics reader (memo.js#L39), dashboard-tracker.js#L884 provider |
| complexity.side_effect_risk | high | #ctext singleton composer state shared by many dashboard-composer-*.js modules and SSE board swap editing() at dashboard.js#L881-L884 |
| risk.correctness | high | six surfaces migrated at once with draft isolation, CSS Highlight aggregation and iframe/cross-frame providers |
| risk.fit | moderate | extraction from document-level singletons (imgs, textRefs, mentions, bselections) into instances; overlaps pending markdown renderer ticket |
| risk.test | high | Node DOM harness plus live browser checks needed |
| risk.security_or_contract | low | T0 frontend-only, no wire or storage change (ticket Decisions 2); wbFlushReply/readAnswers/writeAnswers contracts must be preserved |

## Phases

### Phase 1: Core feature-flagged input component and migration of 6 multi-line surfaces

- **Step 1 — Core component (`WsInput` controller):**
  - Extract singleton state and event handlers out of `dashboard-composer-*.js` into a reusable per-instance controller supporting declarative `features` flags (`markdown`, `capsules`, `mentions`, `topics`, `slash`, `imagePaste`, `longPaste`, `listContinuation`, `bangMode`, `autoGrow`), pluggable `providers` (`@`, `#`, `/`), `keymap` profiles, and external `draft` / `onChange` / `onSubmit` / `onCancel` callbacks.
  - Implement the element-scoped markdown renderer seam (see Constraints), document-scoped highlight aggregation for the legacy path (if CSS Highlights are active), and `<dialog>`-aware popover mounting so multiple simultaneous instances coexist cleanly in one page.
  - Checkpoint: Step 1 is done when two instances coexist in a Node DOM harness with isolated state.
- **Step 2 — Migrate `#ctext` (main chat composer):**
  - Checkpoint: Step 3 starts only after all existing `composer-*.test.cjs` and `capsules` tests pass on the migrated `#ctext`.
  - Re-wire `#ctext` onto the unified component with all main-composer features enabled (`bangMode`, `outbox`/`steer`, recovery draft adapter, `[Image #N]`/`[Text #N]`, `@`/`#`/`/`), preserving 100% of existing DOM IDs/selectors and keyboard/scroll behaviors.
- **Step 3 — Migrate the 5 secondary surfaces (one surface at a time, each with its regression tests green before the next):**
  - Whiteboard `.tnote` / `.note` (keeping standalone HTML graceful fallback, `wbFlushReply` / `readAnswers` / `writeAnswers` SSE morph survival, and `editing()` deferral).
  - Selection comment popover (`.wbmention-note`).
  - Close-request chip reason (`.close-request-reason`).
  - Workspace memo pad (`#memo-input` and `.memo-note`, wired via cross-frame provider adapter from shell to active session frame).
  - Tracker support chat composer (`.supcomp`, preserving per-store `supChats` state and `supGrow` / `supSend`).

**Verification expectations:**
- Node DOM harness tests covering:
  - Multi-instance isolation (two simultaneous editors in one document: independent values, drafts, keymaps, and non-clobbering highlights/capsules).
  - Feature-flag gating (disabling `bangMode` or `slash` on a secondary input ensures those triggers do not fire, while enabling `topics` + `slash` + `markdown` activates them identically to `#ctext`).
  - Regression tests for existing `#ctext` Enter/Shift+Enter scroll-follow, capsules, whiteboard `readAnswers`/`writeAnswers` morph preservation, and `.wbmention-note` / `#memo-input` submit/cancel flows.
- Go verification (host-neutral): `GOOS=windows go vet ./...` and targeted `go test -race ./internal/wsdashboard/... ./internal/wswhiteboard/...`, plus the Node DOM harness (`node` over `agents-plugin-tool/internal/wsdashboard/testdata/*.test.cjs`) run on whichever host the worker uses.
- Live dashboard manual check across all 6 migrated surfaces confirming consistent `#` topic search, `/` autocomplete, badge rendering, and zero cross-input draft leakage.
