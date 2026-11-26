---
title: "feature-flag 기반 단일 대시보드·화이트보드 입력 컴포넌트 통합"
related:
  261126-feat-ws-composer-full-spec-markdown: shared full-spec markdown renderer for the unified editor
  261116-feat-ws-dashboard-generalized-mentions: generalized mention trigger providers
  261126-feat-ws-chat-stream-interactive-question-cards: consumer of inline note input in chat cards
---

# Unified Feature-Flagged Input Component Across Dashboard and Whiteboard

## Background

Text input behavior is currently fragmented across multiple surfaces in `wsdashboard` and `wswhiteboard`:

- **Main chat composer (`#ctext`)** owns markdown highlighting, `@` file mentions (`wsMention`), `/` slash command completion, `#` topic capsules (`WsCapsules`), inline/chip badges, image paste/drop, long-paste `[Text #N]`, list continuation, `!` shell mode, and recovery drafts — but `dashboard-composer-*.js` (`highlight`, `keyboard`, `pickers`, `delivery`, `exec`) is tightly coupled to document-level singletons (`#ctext`, `#composer`, `#chl`, `#cchips`, `#ccmd`, `#csend`) and global arrays (`imgs`, `textRefs`, `mentions`, `bselections`).
- **Secondary multi-line inputs** — whiteboard topic/item notes (`.tnote` / `.note`), board selection comment popover (`.wbmention-note`), close-request reason (`.close-request-reason`), workspace memo pad (`#memo-input` / `.memo-note`), and tracker support chat (`.supcomp`) — each re-implement subsets of `WsCapsules` attachment, image paste, keybindings, and auto-grow while lacking shared capabilities such as topic search, skill/slash autocomplete, markdown rendering, or consistent badge rendering.

Whenever a new input capability is added (markdown rendering, topic/skill completion, badges, paste handling), it must be re-implemented per widget or left missing everywhere outside `#ctext`. Each input keeps its own independent content/draft lifecycle; the goal is to share a single feature-flagged input component (`WsInput` / `WsComposer`) so features are built once and enabled per surface via capability flags and pluggable adapters.

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
- **Document-global highlight safety:** While `CSS.highlights` is active (or whenever any CSS Highlight API group is used), multiple live instances in the same `document` must aggregate `Range`s per document rather than overwriting each other's `CSS.highlights.set(...)` entries. When `261126-feat-ws-composer-full-spec-markdown` lands structured DOM rendering, both tickets should plug into the same renderer interface.
- **Iframe & packaging boundaries:**
  - `#memo-pad` lives in the outer shell document while `#ctext` and the whiteboard live inside `iframe.sframe`; the component must not assume session-frame globals and must accept cross-frame topic/command providers cleanly.
  - Standalone `wswhiteboard` HTML (`page.tmpl` / `render.go`) does not bundle `wsdashboard` assets; `.note`/`.tnote` must degrade gracefully to plain `<textarea>` when the unified component module is absent.
- **Top-layer & SSE DOM-swap compatibility:**
  - Autocomplete popovers (`@`/`#`/`/`) must mount inside the nearest open `<dialog open>` (or top-layer popover) when triggered inside a modal/drawer so `<dialog>::backdrop` never clips or hides them.
  - Dashboard SSE board swaps (`editing()` in `dashboard.js`) must continue to detect active focus inside unified `.wsc-editor` inputs inside `#bcol` so live typing is never interrupted by an incoming board morph.
- **File size discipline:** Create clean modular files rather than expanding already-large `dashboard-composer*.js` or `dashboard-board*.js` monoliths.

## Prior Art

- Element-scoped primitives already suitable as building blocks: `capsules.js` (`WsCapsules.attach(textarea)`), `mention.js` (`window.wsMention`).
- Existing cross-frame topic provider hook: `memo.js` reading `sourceWindow.wsCapsuleTopics`.
- Existing `<dialog>` top-layer tooltip mounting pattern: `tracker-render.js` (`#tktip`).

## Phases

### Phase 1: Core feature-flagged input component and migration of 6 multi-line surfaces

- **Step 1 — Core component (`WsInput` / `WsComposer` controller):**
  - Extract singleton state and event handlers out of `dashboard-composer-*.js` into a reusable per-instance controller supporting declarative `features` flags (`markdown`, `capsules`, `mentions`, `topics`, `slash`, `imagePaste`, `longPaste`, `listContinuation`, `bangMode`, `autoGrow`), pluggable `providers` (`@`, `#`, `/`), `keymap` profiles, and external `draft` / `onChange` / `onSubmit` / `onCancel` callbacks.
  - Implement document-scoped highlight aggregation (if CSS Highlights are active) and `<dialog>`-aware popover mounting so multiple simultaneous instances coexist cleanly in one page.
- **Step 2 — Migrate `#ctext` (main chat composer):**
  - Re-wire `#ctext` onto the unified component with all main-composer features enabled (`bangMode`, `outbox`/`steer`, recovery draft adapter, `[Image #N]`/`[Text #N]`, `@`/`#`/`/`), preserving 100% of existing DOM IDs/selectors and keyboard/scroll behaviors.
- **Step 3 — Migrate the 5 secondary surfaces:**
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
- Remote `w5090` Go verification (`GOOS=windows go vet ./...` and targeted `go test -race ./internal/wsdashboard/... ./internal/wswhiteboard/...`). Never run local `go test` on Windows.
- Live dashboard manual check across all 6 migrated surfaces confirming consistent `#` topic search, `/` autocomplete, badge rendering, and zero cross-input draft leakage.
