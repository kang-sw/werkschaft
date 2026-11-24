---
title: "백그라운드 ws/exec 완료 알림 채팅 표시 개선"
---

# Render background ws/exec completion notices as compact wake rows with monitor link

## Background

When a background `ws/exec` job finishes, `execjob/hosted_notice.go` formats a `[ws exec] <id> exited with code ...` completion notice and delivers it via `wsagent/exec_seams.go` (`AdmitCompletion`) into the session's user inbox (`c.inbox`) with `EventID` prefixed by `"exec:"` (such as `exec:<id>:exit` or `exec:<id>:stopped-by-user`).

Because `wsinbox/user_message.go` wraps every non-command, non-cron inbox entry in `<ws:user at="...">` and `wsagent` (`runner_conv.go`, `durable_steer.go`) records drained inbox items uniformly as `wslive.KindUser` (`Origin: OriginWhiteboard`), the dashboard (`pageview_conversation.go` and `dashboard.tmpl`) renders every background `ws/exec` completion as a right-aligned blue user chat bubble (`.msg.user`) carrying a `화이트보드` badge plus rewind and edit controls. Meanwhile, mailbox wakes and native Claude background task completions already record `wslive.KindWake` (`Cause: "background_exit"` or mail/subagent causes) and render as compact single-line collapsible `.conv-row` entries.

This ticket promotes `ws/exec` completion notices onto the existing `RingWake` (`Cause: "background_exit"`) path, adds a strict legacy `RingUser` fallback parser so historical chats render cleanly too, and wires an expandable "View output" button to the `ws-exec-bg-monitor` viewer when the job's output is still retained.

## Decisions

- **Option C — Source-based `RingWake(background_exit)` classification + legacy `RingUser` fallback + retained-job monitor jump button** *(confirmed by user `(125)=c`)*
  - `wsagent` / `wsinbox` stop wrapping `EventID`-prefixed `exec:` completion entries as `<ws:user>` bubbles and instead record them as `wslive.KindWake` with `Cause: "background_exit"`, separating them cleanly from real user messages even when a user message and an exec completion arrive in the same inbox drain batch.
  - `wsdashboard` renders both the new `RingWake(background_exit)` exec-completion entries and historical `RingUser` entries matching the strict multi-line `[ws exec]` completion format as a collapsed single-line `.conv-row` (terminal icon, 1-line command summary, exit code / stopped status and elapsed duration, red `.fail` tone on non-zero exit / failure; no `.msg.user` bubble, no `화이트보드` badge, no rewind/edit buttons).
  - Expanding the row reveals full `command`, exit/stop metadata, tail output preview (`<pre class="wake-body">`), and — when the referenced `exec_id` is still live or retained in the exec-jobs store — a secondary button that opens the existing `ws-exec-bg-monitor` popup/viewer directly to that job (hidden automatically when expired/unavailable).
  - Rejected:
    - *Option A (`RingWake` + legacy fallback without monitor button)*: leaves users clicking separately to find the same `exec_id` in the background job monitor that just landed on `develop@509b6d2dc`.
    - *Option B (display-only `RingUser` parsing without `wsagent`/`wsinbox` changes)*: keeps `<ws:user>` misclassification in model prompts and ring history, cannot cleanly prevent mixed user+exec batch collisions, and remains vulnerable to spoofed user text.
- **Scope: `ws/exec` completion notices only** *(announced default accepted with `(125)=c`)*
  - `<ws:cron>` inbox messages are explicitly left untouched in this ticket.
- **Compatibility tier: T1 display-degraded (new writes) / T0 transparent (legacy reads)**
  - New binary writes existing `RingWake` records (`wslive/ring.go:28`) using the already-shipped `"background_exit"` cause (`wslive/wake.go:12`, `wsagent/wake.go:20`), without adding a new required `RingEntry` kind or breaking enum values.
  - Old binaries read `RingWake` (`Cause: "background_exit"`) via `wslive/wake.go:12` and render it safely as a collapsed `.conv-row` wake note in `wsdashboard/assets/dashboard.tmpl` (`define "wakeentry"`), degrading only by omitting the new expandable preview / monitor jump button and changing no persisted ring data (the ring log is append-only).
  - Reopening with the new binary renders full metadata, expandable preview, and monitor jump button directly from the retained ring entry; historical `RingUser` entries written by older binaries remain readable via the strict display-layer fallback parser. No minor version bump or T3 boundary is required.

## Constraints

- **Implementation conventions (read before editing matching paths):**
  - `ai-docs/manuals/release-compatibility.md` (`agents-plugin-tool/internal/`)
  - `ai-docs/manuals/performance.md` (`agents-plugin-tool/`) — file-touch budget and Frontend Minimal Updates (no polling loops, no unrequested layout thrash, minimal DOM mutations)
  - `ai-docs/manuals/dashboard-style.md` (`agents-plugin-tool/internal/wsdashboard/assets/`) — borderless Material tonal surface system and canonical CSS tokens
  - `ai-docs/manuals/shipped-surface-boundary.md` (`agents-plugin-tool/`)
- **No spoofable false positives on legacy fallback:** the legacy `RingUser` fallback parser must match only genuine `[ws exec]` multi-line completion payloads (e.g., header `[ws exec] <id> ...` with structured command/status/tail-hint lines) so ordinary user messages mentioning `[ws exec]` are never misclassified.
- **Preserve `data-wake-cause="background_exit"`:** `dashboard-companion-relay.js` checks `row.dataset.wakeCause !== "background_exit"` so background exits do not trigger companion subagent chimes; keep that attribute intact on the rendered row.
- **Preserve mixed-batch ordering and delivery semantics:** when a real user message and an `exec:` completion entry are drained together, the model still receives the completion notice cleanly (without mislabeling it as `<ws:user>`), and the live ring records separate entries (`RingUser` for the user's text, `RingWake` for the exec completion) with rewind/edit available only on the real user bubble.
- **Read-only / LAN posture & expiry safety:** the monitor jump button opens the read-only output tail view (consistent with `ws-exec-bg-monitor` where output inspection is permitted and Stop controls stay gated by `can_control`) and is hidden or gracefully disabled when the job is no longer retained or `exec_id` is absent.
- **11-locale i18n parity:** every new UI string must be added to all 11 locale tables under `agents-plugin-tool/internal/wsi18n/tables/*.json` and pass `go test ./internal/wsi18n/...`.
- **Out of scope:** `<ws:cron>` rendering changes; refactoring or splitting `runner_conv.go`.

## Prior Art

- `wsagent/exec_seams.go` (`AdmitCompletion`) and `execjob/hosted_notice.go` — origin of `EventID: "exec:<id>:..."` completion notices.
- `wsinbox/user_message.go` — inbox batch formatting into `<ws:user>` vs special entries.
- `wslive/wake.go`, `wsagent/wake.go`, `wsdashboard/pageview_conversation.go`, and `wsdashboard/assets/dashboard.tmpl` (`define "wakeentry"`) — existing `RingWake` (`Cause: "background_exit"`) storage and `.conv-row` rendering.
- `wsdashboard/assets/dashboard-bg-exec-monitor.js` and `dashboard-composer-status.js` (landed in `509b6d2dc`) — background `ws/exec` monitor popup, live/retained job list, and tail viewer (`bgExecOpenPop` / job selection).

## Phases

### Phase 1: Classify ws/exec completions as background_exit wake rows with legacy fallback and monitor jump

Implement end-to-end Option C across `wsinbox`, `wsagent`, and `wsdashboard`:

1. **Source classification & prompt framing (`wsinbox` + `wsagent`):**
   - Identify `exec:` completion entries by their trusted `EventID` prefix (set by `AdmitCompletion`, never by user-supplied text).
   - Keep model-facing delivery intact without wrapping `exec:` completions inside `<ws:user at="...">` (preserve clean separation when real user messages and `exec:` completions appear in the same inbox drain batch).
   - Emit `wslive.KindWake` with `Cause: "background_exit"` (populated with structured/parseable command, exit/stopped state, elapsed duration, `exec_id`, and tail preview text) instead of `wslive.KindUser(OriginWhiteboard)` on both turn-start drain and durable-steer paths.
2. **Dashboard rendering & legacy fallback (`wsdashboard` + `wsi18n`):**
   - Render `RingWake(Cause: "background_exit")` exec-completion entries — and strictly-matched legacy `RingUser` `[ws exec]` completion entries — as a single-line collapsible `.conv-row` matching existing tool/wake row density and visual tokens:
     - Terminal icon, compact 1-line command summary, exit code (or stopped badge) and elapsed duration, red `.fail` tone for non-zero exit / failure.
     - No `.msg.user` bubble, no `화이트보드` badge, no rewind/edit buttons; preserve `data-wake-cause="background_exit"`.
   - Expand `<div class="row-details">` on click to show full `command`, exit/stop status line, and tail output preview (`<pre class="wake-body">`).
   - Add a secondary "View output" button inside the expanded details (or row action slot without breaking collapsed single-line layout) when the referenced `exec_id` is currently present in the session's live or retained background exec snapshot; clicking it opens `dashboard-bg-exec-monitor.js` directly focused on that `exec_id`'s tail view. Hide the button automatically when the job is expired/unknown.
   - Add all new UI strings across all 11 `wsi18n/tables/*.json` locales.
3. **Verification:**
   - Go unit & `-race` tests covering:
     - `wsinbox` formatting for pure `exec:`, pure user, and mixed user+`exec:` batches.
     - `wsagent` live ring emission (`RingWake` with `Cause: "background_exit"`, never `RingUser`) on normal drain and steer paths, plus mixed user+`exec:` separation.
     - `wsdashboard` conversation view model / SSR rendering for new `RingWake` exec completions, strict legacy `RingUser` fallback (both positive matches and negative spoof/plain-text cases), non-zero `.fail` styling, and absence of rewind/edit controls.
     - `wsi18n` 11-locale parity tests.
   - Playwright browser test verifying:
     - Collapsed 1-line row layout (success & failure tones), click-to-expand details (`command` + `<pre class="wake-body">`), absence of `.msg.user` / rewind / edit buttons.
     - Clicking "View output" on a retained `exec_id` opens the `ws-exec-bg-monitor` popup focused on that job's tail; expired/unknown `exec_id` hides the button.
     - Legacy `RingUser` fallback renders identically.
   - `GOOS=windows go -C agents-plugin-tool vet -stdversion=false ./...` passes cleanly.
