---
title: "컴포저 장문 붙여넣기 첨부 레퍼런스([Text #N] / <textref>)"
---

# Composer Long-Paste Text Attachment Reference (`[Text #N]` / `<textref>`)

## Background

Pasting large blocks of text (logs, stack traces, diffs, prose) into the dashboard composer currently floods the `.wsc-editor` input box inline, making the surrounding prompt hard to read and edit.

The goal is to treat long plain-text pastes (`Ctrl+V` / `Cmd+V`, `>= 1024` characters) like image attachments: insert a compact `[Text #N]` reference in the composer and attach the full text payload separately via `<textref id="#n">...</textref>` on send, while letting `Ctrl+Shift+V` (or `Cmd+Shift+V`) bypass conversion and paste the raw text inline verbatim.

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

## Constraints

- **Required manuals:** `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`), `ai-docs/manuals/dashboard-style.md`, `ai-docs/manuals/release-compatibility.md`, `ai-docs/manuals/shipped-surface-boundary.md`.
- **Compatibility tier:** T0 (`(1608)=a`) — `<textref id="#n">...</textref>` travels inside `req.Text`/`Parts` with no schema changes to `messageRequest`, `wsinbox.Entry`, or `wslive.RingEntry`.
- **Modifier detection:** DOM `paste` (`ClipboardEvent`) carries no modifier keys; capture `Ctrl+Shift+V` / `Cmd+Shift+V` on the preceding `keydown` forwarded by `WsCapsules` (`capsules.js`).
- **Draft persistence:** Ensure `emptyDraft()` (`rail.js`) and `readDraft`/`restoreDraft` (`dashboard-lifecycle.js`) preserve drafts containing text attachments across reloads and session switches without dropping them as empty.
- **CSS guard:** Do not introduce forbidden `:has()` selectors (`frontend-idle.test.js`).

## Prior Art

- Image attachment markers, `56×56` `#cimgs` tray tiles, undo trash, and draft persistence: search `dashboard-composer-images.js` (`MARKER`, `pasteImages`, `addFiles`, `unattach`, `syncMarkers`, `renderImgs`) and `dashboard-conversation.css` (`.cimgs`, `.ichip`).
- Composer editor paste forwarding & capsule serialization: search `capsules.js` (`attach`, `insertPlain`, `stripMarkers`).
- `<wb:selection>` inline block parsing and `.icapsule` / `.thumbs` rendering in `#convo`: search `pageview_inbox.go` (`ParseSelections`, `hydrateUserMessage`, `splitSelectionContent`, `markedText`) and `dashboard-composer-delivery.js` (`hydrateProseInto`).

## Phases

### Phase 1: Composer Long-Paste Collapse, Raw Bypass, Inline `<textref>`, and Rendering

- Intercept plain-text pastes in `.wsc-editor` / `#ctext`:
  - Track `Ctrl+Shift+V` / `Cmd+Shift+V` on `keydown` to bypass conversion and paste raw text inline.
  - On normal `Ctrl+V` / `Cmd+V` with plain text `>= 1024` chars, allocate monotonic `#N`, insert `[Text #N]` at caret, and render a `56×56` `.tchip` (`<doc icon> #N`) side-by-side with `.ichip` thumbnails in `#cimgs`.
  - Add click-to-preview popovers for both pending `.tchip` text attachments (full raw text) and pending `.ichip` pasted images in `#cimgs`, while preserving draft save/restore, undo/redo, and `×` remove + Undo trash restore.
- Serialize `<textref id="#n">...</textref>` inline in `req.Text`/`Parts` on send (escaping `</textref` safely), and hydrate `[Text #N]` / `<textref>` in `#convo` held/delivered user bubbles with matching tile + collapsible preview popover (`.thumbs` / `.icapsule` pattern).
- Verify with local `node --test` unit tests (1024-char boundary, `Ctrl+V` vs `Ctrl+Shift+V`, undo/trash restore, `.tchip`/`.ichip` preview popover behavior, draft round-trip, CSS `:has()` guard) and remote `w5090` `go test -race ./internal/wsdashboard`.
