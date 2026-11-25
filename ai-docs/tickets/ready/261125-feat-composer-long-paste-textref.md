---
title: "컴포저 장문 붙여넣기 첨부 레퍼런스([Text #N] / <textref>)"
sage-review-design: completed
sage-review-completeness: completed
related:
  261125-feat-composer-mailbox-badge: "parallel sibling; shares the composer paste/keydown pipeline, the Ctrl+Shift+V raw-bypass flag, and #convo hydration entry points; no landing order required"
sage-review-design-reviewed: e83a438c491c0bda
sage-review-completeness-reviewed: e83a438c491c0bda
---

# Composer Long-Paste Text Attachment Reference (`[Text #N]` / `<textref>`)

## Background

Pasting large blocks of text (logs, stack traces, diffs, prose) into the dashboard composer currently floods the `.wsc-editor` input box inline, making the surrounding prompt hard to read and edit.

The goal is to treat long plain-text pastes (`Ctrl+V` / `Cmd+V`, `>= 1024` characters) like image attachments: insert a compact `[Text #N]` reference in the composer and attach the full text payload separately via `<textref id="#n" bytes="N">...</textref>` on send (length-framed, see `(1613)=a`), while letting `Ctrl+Shift+V` (or `Cmd+Shift+V`) bypass conversion and paste the raw text inline verbatim.

## Decisions

- **Long-paste collapse trigger & raw bypass:** Pasting long text via `Ctrl+V` collapses it into a `[Text #N]` attachment reference paired with a `<textref id="#n">...</textref>` block on send; `Ctrl+Shift+V` pastes raw text inline verbatim.
  - Rationale: Keeps the composer readable for large logs/snippets while preserving a zero-friction raw-paste escape hatch. No existing shortcut across `wsdashboard` or `wswhiteboard` uses `KeyV`/`Ctrl+Shift+V`.
  - Rejected: Always collapsing every paste regardless of modifier.
- **Threshold, inline `<textref>` transport, and T0 compatibility (`(1608)=a`):** Collapse plain-text pastes `>= 1024` characters by default and serialize `<textref id="#n">...</textref>` inline in `req.Text`/`Parts` (strictly **T0**, matching `<wb:selection>`, subject to the existing `64 KiB` `wsinbox.maxTextBytes` / `wslive.TextBytes` cap).
  - Rationale: Preserves strict T0 wire and downgrade compatibility with zero `wsinbox.Entry` or `messageRequest` schema changes; older daemons/pages pass `<textref>` through cleanly as literal text.
  - Rejected: Out-of-band file storage in `wsinbox.AttachmentsDir` (either expanded on delivery or path-referenced) — rejected for initial scope to keep T0 simplicity.
- **Composer `#cimgs` thumbnail tiles, `#convo` rendering, and unified preview popovers (`(1609)=a`):**
  - Composer: inline `[Text #N]` marker in `.wsc-editor` paired with a same-size `56×56` `.tchip` tile (`<doc icon> #N`, click full-text preview popover, `×` remove + Undo trash restore) rendered side-by-side with `.ichip` image thumbnails inside `#cimgs`.
  - `#convo`: display `[Text #N]` tag alongside a matching thumbnail tile / collapsible preview popover in user bubbles (same pattern as `.thumbs` / `.icapsule`).
  - Pending pasted images (`#cimgs` `.ichip`): clicking a staged pasted image thumbnail in `#cimgs` also opens a preview popover (sharing the preview popover interaction alongside `.tchip`).
  - Rationale: Keeps text and image attachments visually uniform at `56×56` in `#cimgs` and gives users immediate click-to-inspect previews for both pending long-text pastes and pending pasted images before sending.
  - Rejected: Compact inline-only `.icapsule` in `#convo` without matching tile parity (`(1609)=b`), or leaving staged `.ichip` tiles non-previewable on click.
- **Length-framed `<textref>` body (`(1613)=a`, technical, auto-confirmed):** The wire tag is `<textref id="#n" bytes="N">…</textref>`, where `N` is the body's UTF-8 byte length; the parser reads exactly `N` bytes of body and then expects `</textref>`, so no escaping of the body is needed and a literal `</textref` inside it is harmless. A length mismatch, a missing closing tag, or truncation by the 64 KiB cap falls back to literal text. This extends the `<textref id="#n">` shape named in `(1608)=a` by one attribute and remains strictly T0 (`req.Text`/`Parts` only).
  - Rationale: The agent receives the paste byte-for-byte with no `&amp;`/`&lt;` noise in logs and stack traces, and injection is impossible by construction.
  - Rejected: Escaping only `</textref` (needs a reversible pre-existing-escape rule), and full XML-escape of `&`/`<` (noise for the agent).
- **Over-cap behavior (`(1614)=a`, owner-confirmed):** When the serialized textref payloads plus prose exceed the 64 KiB cap (`wsinbox.maxTextBytes` / `wslive.TextBytes`), refuse the send with an inline error and keep the draft intact; never truncate or send partially. Additionally warn at paste time when a single paste already exceeds the cap.
  - Rationale: No silent data loss and no schema change; the user trims the paste.
  - Rejected: Refusing to collapse an over-cap paste and inserting nothing (b), and sending truncated with a warning (c).

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, agents-plugin-tool/internal/wswhiteboard/assets/, ai-docs/assets/mockups/)
- **Required manuals:** `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`), `ai-docs/manuals/dashboard-style.md`, `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/shipped-surface-boundary.md`.
- **Compatibility tier:** T0 (`(1608)=a`, wire shape extended by `bytes="N"` per `(1613)=a`) — `<textref id="#n" bytes="N">...</textref>` travels inside `req.Text`/`Parts` with no schema changes to `messageRequest`, `wsinbox.Entry`, or `wslive.RingEntry`.
- **Modifier detection:** DOM `paste` (`ClipboardEvent`) carries no modifier keys; capture `Ctrl+Shift+V` / `Cmd+Shift+V` on the preceding `keydown` forwarded by `WsCapsules` (`capsules.js`).
- **Draft persistence:** Ensure `emptyDraft()` (`rail.js`) and `readDraft`/`restoreDraft` (`dashboard-lifecycle.js`) preserve drafts containing text attachments across reloads and session switches without dropping them as empty.
- **CSS guard:** Do not introduce forbidden `:has()` selectors (`frontend-idle.test.js`).
- **Textref body is opaque to the rest of the hydration pipeline:** `<textref>` extraction runs first, in both the server path (`splitSelectionContent` / `markedText` / `hydrateProse` in `pageview_inbox.go`) and the client twin (`hydrateProseInto`). Its body must never be scanned by `ParseSelections`, `[Image #N]` / image-definition parsing, anchor `(03)` matching, or `<mailbox>` badge conversion (`261125-feat-composer-mailbox-badge`). Likewise a `>= 1024`-char paste that collapses is never subject to mailbox badge conversion in the composer.
- **Opaque on ingest too:** `wsinbox.Normalize` runs `normalizeSelectionText` over every Part and re-serializes any valid `<wb:selection>` block (and counts its mentions toward `maxMentions`). That walk must skip length-framed `<textref>` bodies so a pasted body is not rewritten (which would break the `bytes="N"` frame) or refused for mention count; add a Go test with a valid selection block inside a textref body.
- **Byte accounting:** `bytes="N"` is the UTF-8 byte length. The client must compute and slice it byte-aware (`TextEncoder` or equivalent, not UTF-16 string length), pinned by a multibyte test case; the client-side cap check approximates the server accounting (`wsinbox/normalize.go` sums Parts plus other fields; ring text via `wslive.TextBytes` may exceed it), erring toward refusing early.
- **Bypass-flag interface:** expose the flag from `capsules.js` as a single readable accessor consumed by both the capsule `insertPlain` path and the textref `paste` listener; the later merger reconciles any duplicate tracker found by the merge-tree check.
- **Tile naming:** `.tchip` already exists as `.achip.tchip` (topic-note chip, `data-tchip` / `data-untchip` hooks in `dashboard-actions.js` and `dashboard-composer.js`). Use a distinct class/attribute for the text-attachment tile (and its delegated handlers) so rules and handlers cannot leak across the two chip types; where this ticket says `.tchip` tile, read the distinct name.
- **Shared raw-bypass flag:** The `Ctrl+Shift+V` / `Cmd+Shift+V` flag has a single owner in `capsules.js` (set on `keydown`, cleared on `keyup` and when consumed by a `paste`, so an unconsumed flag from focus loss or a context-menu paste cannot make a later `Ctrl+V` look raw). Whichever of this ticket and `261125-feat-composer-mailbox-badge` lands second reuses the first's flag instead of adding a second tracker.
- **Mixed clipboard:** `pasteImages` deliberately lets `text/plain` through when an image is also on the clipboard; a `>= 1024`-char `text/plain` in such a paste collapses like any other long paste, and the image attaches as before.
- **Marker lifecycle (follows the `ImageNums` precedent, f73f009a):** `#N` is monotonic within a draft with gaps allowed and no renumbering; a removed or manually deleted `[Text #N]` marker drops its attachment (same `syncMarkers` path as images, with Undo trash restore), and `#N` is not reused after removal.

## Prior Art

- Image attachment markers, `56×56` `#cimgs` tray tiles, undo trash, and draft persistence: search `dashboard-composer-images.js` (`MARKER`, `pasteImages`, `addFiles`, `unattach`, `syncMarkers`, `renderImgs`) and `dashboard-conversation.css` (`.cimgs`, `.ichip`).
- Composer editor paste forwarding & capsule serialization: search `capsules.js` (`attach`, `insertPlain`, `stripMarkers`).
- `<wb:selection>` inline block parsing and `.icapsule` / `.thumbs` rendering in `#convo`: search `wsinbox/selections.go` (`ParseSelections`, `agents-plugin-tool/internal/wsinbox/selections.go#L76`), `pageview_inbox.go` (`hydrateUserMessage`, `splitSelectionContent`, `markedText`, `agents-plugin-tool/internal/wsdashboard/pageview_inbox.go#L528-L709`), and `dashboard-composer-delivery.js` (`hydrateProseInto`).

## Prior Decisions

- 261107-feat-ws-dashboard-composer-image-attach (2026-11-07, ticket Decisions): "The browser holds a pasted or dropped image (a removable thumbnail chip in the composer) and uploads it only when the message is sent (user, 2026-11-07), so an abandoned paste never touches disk." — bearing: supports
- f73f009a (2026-11-07, commit): "Numbers are stored on the entry as ImageNums, parallel to Images, rather than derived from order: the plan allows gaps within a draft (no renumbering of text the user is editing)." — bearing: supports
- 261114-feat-ws-selection-bundle-comment (2026-11-15, commit): "The owned spine is splitSelectionContent on bare message content after leading legacy blocks; ParseSelections merges malformed envelopes with adjacent plain text. Preserve that whole segment as literal display." — bearing: constrains
- 261115-feat-ws-composer-inline-skill-and-topic-capsules (2026-11-15, commit): "The textarea remains each field's owner for existing form, image, draft and send code. Its rich sibling presents compact atomic capsules while value remains canonical text; snapshot ranges live only in drafts." — bearing: constrains
- 31042a2b (2026-11-21, commit): "The fragments preserve the original source byte-for-byte when exactly one new header comment per fragment is stripped; each fragment is at most 500 lines. Ordered lists mirror the source sequence." — bearing: constrains
- c4a488d6 (2026-11-21, commit): "Kept the existing inbox JSON fields and wsinbox block formats for T0 wire compatibility; inline anchors preserve prose order." — bearing: supports
- 800ff72e (2026-11-23, commit): "Forwarded KeyboardEvent.repeat through capsules.js ... Reused chist-armed instead of bare .confirm/.hot classes so CSS module class collision checks remain isolated across modules." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-images.js, capsules.js, dashboard-composer-delivery.js, dashboard-lifecycle.js, rail.js, dashboard-conversation.css, agents-plugin-tool/internal/wsdashboard/pageview_inbox.go, agents-plugin-tool/internal/wsinbox/selections.go |
| scope.surface | internal | no exported MCP or daemon API/wire schema changes (T0 inline `<textref>` in req.Text/Parts); internal dashboard JS/CSS and pageview rendering |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none (no messageRequest, wsinbox.Entry, or wslive.RingEntry schema changes) |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js, frontend-interaction.test.js, capsules_test.go, attachments_test.go, selections_routes_test.go |
| complexity.reuse_points | confirmed | reuses #cimgs/.ichip tray and imgTrash/syncMarkers (dashboard-composer-images.js#L23-L228), WsCapsules keydown/paste forwarding (capsules.js#L288-L324), .icapsule/.thumbs hydration (pageview_inbox.go#L178-L626, dashboard-composer-delivery.js#L244-L523), and draft persistence (rail.js#L62-L67, dashboard-lifecycle.js#L258-L630) |
| complexity.side_effect_risk | moderate | modifies shared composer paste/keydown interception, draft save/restore, undo trash sync, and #convo user-bubble hydration; overlaps composer paste handling with 261125-feat-composer-mailbox-badge |
| risk.correctness | moderate | keydown-to-paste modifier handoff, length-framed (`bytes="N"`) parsing and malformed-tag fallback (no body escaping per `(1613)=a`), 64 KiB over-cap send refusal, and marker/trash/draft round-trip sync have edge cases |
| risk.fit | moderate | `.tchip` already appears on `.achip.tchip` (dashboard-conversation.css#L584,L716, dashboard-composer-delivery.js#L501); must share preview popover with `.ichip`, respect the 500-line JS fragment cap, and pass the `:has()` CSS guard |
| risk.test | moderate | requires Node unit tests (1024-char boundary, Ctrl+V vs Ctrl+Shift+V, undo/trash restore, `.tchip`/`.ichip` popovers, draft round-trip, `:has()` guard) and Go SSR hydration parity tests for held and delivered bubbles |
| risk.security_or_contract | moderate | inline `<textref>` parsing/rendering handles arbitrary pasted text (HTML/XSS escaping; `</textref` injection is neutralized by length framing) and must preserve T0 wire compatibility under wsinbox.maxTextBytes/wslive.TextBytes |

## Phases

### Phase 1: Composer Long-Paste Collapse, Raw Bypass, Inline `<textref>`, and Rendering

- Intercept plain-text pastes in `.wsc-editor` / `#ctext`:
  - Track `Ctrl+Shift+V` / `Cmd+Shift+V` on `keydown` to bypass conversion and paste raw text inline.
  - On normal `Ctrl+V` / `Cmd+V` with plain text `>= 1024` chars, allocate monotonic `#N`, insert `[Text #N]` at caret, and render a `56×56` `.tchip` (`<doc icon> #N`) side-by-side with `.ichip` thumbnails in `#cimgs`.
  - Add click-to-preview popovers for both pending `.tchip` text attachments (full raw text) and pending `.ichip` pasted images in `#cimgs`, while preserving draft save/restore, undo/redo, and `×` remove + Undo trash restore.
- Serialize `<textref id="#n" bytes="N">...</textref>` inline in `req.Text`/`Parts` on send (length-framed per `(1613)=a`, no body escaping; refuse the send with an inline error and keep the draft when the total exceeds the 64 KiB cap per `(1614)=a`, with an early per-paste warning), and hydrate `[Text #N]` / `<textref>` in `#convo` held/delivered user bubbles with matching tile + collapsible preview popover (`.thumbs` / `.icapsule` pattern).
- Verify with local `node --test` unit tests (1024-char boundary, `Ctrl+V` vs `Ctrl+Shift+V`, undo/trash restore, manual marker deletion and `#N` non-reuse, mixed image + long-text clipboard, text-attachment tile vs topic-note chip isolation, `.ichip` and text-tile preview popover behavior, draft round-trip with gaps, CSS `:has()` guard), Go SSR hydration parity tests for held and delivered `#convo` bubbles (including a body that contains `<wb:selection`, `[Image #N]` and `<mailbox` text staying opaque, and `bytes` length mismatch / missing closing tag / cap-truncation fallback to literal text), composer tests for the over-cap send refusal (draft kept, inline error) and the early per-paste warning, and remote `w5090` `go test -race ./internal/wsdashboard`.
