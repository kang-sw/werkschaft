---
title: "백그라운드 ws/exec 완료 알림 채팅 표시 개선"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 850f6d7c47de10f7
sage-review-completeness-reviewed: 850f6d7c47de10f7
completed: 2026-11-25
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
  - Expanding the row reveals full `command`, exit/stop metadata, tail output preview (`<pre class="wake-body">`), and — when the referenced `exec_id` is still live or retained in the exec-jobs store — a secondary button that opens the existing `ws-exec-bg-monitor` popup/viewer directly to that job (hidden when the job is found expired/unavailable, resolved at click time while the popup is closed; see "Monitor jump is click-then-resolve").
  - Rejected:
    - *Option A (`RingWake` + legacy fallback without monitor button)*: leaves users clicking separately to find the same `exec_id` in the background job monitor that just landed on `develop@509b6d2dc`.
    - *Option B (display-only `RingUser` parsing without `wsagent`/`wsinbox` changes)*: keeps `<ws:user>` misclassification in model prompts and ring history, cannot cleanly prevent mixed user+exec batch collisions, and remains vulnerable to spoofed user text.
- **Wake payload carries the notice text verbatim; no new `RingEntry` field** *(technical, auto-confirmed)*
  - The `RingWake(background_exit)` entry for an `exec:` completion stores the `noticeText` payload in `Text` (the existing `RingEntry.Text` field, clipped by the existing wake text limit), a one-line summary in `Caption`, and the exec id in `Source`. `wsdashboard` derives command, exit/stop state, elapsed duration, `exec_id`, and the tail preview by parsing `Text` with one shared strict parser also used for the legacy `RingUser` fallback, so new and legacy entries render identically.
  - Rejected: new structured `RingEntry` fields (exec id, exit code, duration) — they widen the persisted ring contract and add a compatibility surface for no display gain, since the notice text is already a stable, tested format (`execjob/hosted_notice.go` `noticeText`) and the legacy fallback needs the text parser anyway.
- **Strict payload grammar for the shared parser** *(technical, auto-confirmed)*
  - A text is an exec completion only when, after trimming surrounding whitespace, it is exactly one payload of the `noticeText` / `stopNoticeText` shape: a first line `[ws exec] <id> <end>[ after <duration>].` where `<end>` is one of `exited with code <n>`, `was ended by signal <n>`, `was stopped`, `is running`, `failed to start: ...`, `was lost: ...`, `ended` (every `endText` variant); an optional `command: ...` value that may span several lines, ending at the first line that begins a following section; for a user-stop notice, the `stopped by user: ...` line (its note may span several lines) between the command and the output section; an output section (`output:`, `output (last <n> of <m> bytes):`, or `no output.`); and a final line exactly `Read more with exec.tail (exec_id <id>).` whose id equals the header id. Anything else (extra leading/trailing prose, multiple payloads, missing or mismatched trailer) is not an exec completion.
  - Legacy `RingUser` entries are matched per unwrapped `<ws:user>` block (the per-block split the conversation view already produces for user batches), so an enveloped historical notice matches at the block level; an exec block in a legacy mixed batch renders as an exec row beside the user bubbles, and a non-matching block stays an ordinary user bubble.
  - A new `RingWake(background_exit)` whose `Text` fails the parser renders through the existing generic wake/`agent-note` path (the same path Claude-native `local_bash` `background_exit` wakes use), never as an error.
  - The parser lives beside the other conversation view-model helpers in `wsdashboard` and is tested against real `noticeText` and `stopNoticeText` output for every `endText` variant, multi-line commands and multi-line stop notes, plus spoof cases.
  - Rejected: header-only matching (`[ws exec]` prefix) — a user can type that line, and the ticket's no-false-positive constraint requires the full shape including the matching trailer.
- **Monitor jump is click-then-resolve; no request while the popup is closed** *(technical, auto-confirmed)*
  - The monitor keeps its invariant of zero network requests and zero timers while closed (file header of `dashboard-bg-exec-monitor.js`; commit `97d6e41e`). `dashboard-bg-exec-monitor.js` exposes one small hook (e.g. `window.wsBgExecJump(id)`). The exec row's "View output" button is shown in the expanded details whenever the row has an `exec_id` and the conversation is live. Clicking calls the hook, which opens the popup (its existing list fetch) with a pending selection of that id applied after the list loads, taking precedence over the list's default auto-selection; the job's tail then opens via `bgSelectJob`.
  - Hidden-when-expired is realized at resolution time: while the popup is open (its list is current), a list lacking the id when the row is expanded keeps the button hidden; while the popup is closed `bgSt.byId` is stale and is never used to hide, so the click's own load decides; if the id is absent from the list loaded by the click, the popup stays open and the button is hidden for that row with a one-line "no longer retained" note (new i18n string). No new endpoint, interval, or closed-state fetch.
  - The popup is anchored to `#bgbadge`, which `dashboard-composer-status.js` hides when no job is live (the normal case for a completed job). When the badge is hidden or has a zero rect, the popup positions from the invoking button's rect (or the composer container) instead of opening off-screen.
  - Rejected: a closed-state one-shot list fetch on row expand — it would supersede `97d6e41e` and the file-header invariant for a cosmetic pre-hide; server-side per-row availability — it adds a snapshot dependency to the SSR view model and goes stale as jobs expire.
- **Model-facing framing: a distinct `<ws:exec>` envelope with escaped body** *(technical, auto-confirmed)*
  - `wsinbox` stops wrapping `exec:` entries in `<ws:user at>` and instead delivers each as `<ws:exec at="..." id="...">` … `</ws:exec>`, mirroring the existing `<ws:cron>` precedent (`cronText`, `cronOpen`, and the cron branch of `ParseUserMessages`): the body (command line plus up to `HostedPreviewBytes` of arbitrary command output) has its `</ws:exec` delimiter escaped one level so command output cannot close the envelope, and `<ws:user` / `<ws:cron` forgeries inside output are inert because they sit inside the escaped `<ws:exec>` block that the parsers treat as one verbatim block. `ParseUserMessages` treats a `<ws:exec>` block as one plain block, like cron. The whiteboard playbook (`rsrc/whiteboard/whiteboard.md`, source location found by search; regenerate any generated copies per repo convention) gains one sentence naming `<ws:exec>` next to its `<ws:user>` description: it is a background `ws/exec` completion notice, not dashboard user input.
  - The ring `Text` for the wake is the unwrapped payload, not the envelope.
  - Rejected: raw unwrapped notice text (forgery hole and no boundary for the model); keeping the `<ws:user>` wrapper (the misclassification this ticket removes).
- **Classification must survive every inbox-to-model path** *(technical, auto-confirmed)*
  - `AdmitCompletion` sends `Steer: true`, so most notices arrive mid-turn rather than at turn start. The implementer enumerates every site that drains or queues inbox entries into text — search `QueuedInput`, `takeSteer`, `durableSteer`, `Record.Queued`/`Reports`, `conv.steer`, the worker path in `children.go`, and the `kick`/`nextTurn` drain — and carries the exec classification (an additive `omitempty` field on the persisted queue/report structs) so no site flattens an exec notice into a `KindUser` or a joined user string. A mixed user+exec batch is split into `RingUser` plus `RingWake` at each site, and ring order and model-facing order are both queue order (no reordering of wake rows before user text). The classification is per part, never per joined text: where a persisted field is a plain `[]string` (`Record.Reports`, `Record.Mail`), do not change the element type (an older binary would fail to parse the record); use a parallel optional slice, or recover exec parts by parsing the stored `<ws:exec>` blocks, and make `requeueDurableSteers` split an already-joined mixed `durableSteer.Text` by part. Held-inbox listings (`pageview_inbox.go`) and any other `Entry.Text()` consumer that renders held `exec:` entries must be checked and given `<ws:exec>`-aware display.
  - Ordering and origin: an exec-only turn-start batch applies the wake events before `KindTurnStart` and uses a non-whiteboard (mail-like/empty) turn origin so `wslive` marks the `WakeTurn` ("lead turn") continuation; a mixed batch keeps `OriginWhiteboard` because of the user text, with rows in queue order.
  - Compatibility: the additive persisted-struct field is T0 for new data read by old binaries (unknown field ignored, notice then delivered as plain text, the pre-change behavior, i.e. T1 display-degraded); covered by the compatibility tier below and `ai-docs/manuals/release-compatibility.md`.
  - Rejected: patching only the two named drain sites — the common mid-turn steer path would keep emitting `KindUser`.
- **Scope: `ws/exec` completion notices only** *(announced default accepted with `(125)=c`)*
  - `<ws:cron>` inbox messages are explicitly left untouched in this ticket.
- **Compatibility tier: T1 display-degraded (new writes) / T0 transparent (legacy reads)**
  - New binary writes existing `RingWake` records (`wslive/ring.go:28`) using the already-shipped `"background_exit"` cause (`wslive/wake.go:12`, `wsagent/wake.go:20`), without adding a new required `RingEntry` kind or breaking enum values.
  - Old binaries read `RingWake` (`Cause: "background_exit"`) via `wslive/wake.go:12` and render it safely as a collapsed `.conv-row` wake note in `wsdashboard/assets/dashboard.tmpl` (`define "wakeentry"`), degrading only by omitting the new expandable preview / monitor jump button and changing no persisted ring data (the ring log is append-only).
  - Reopening with the new binary renders full metadata, expandable preview, and monitor jump button directly from the retained ring entry; historical `RingUser` entries written by older binaries remain readable via the strict display-layer fallback parser. No minor version bump or T3 boundary is required.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/)
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

## Prior Decisions

- 261122-feat-ws-exec-job-hud (2026-11-22, Decisions): "The user-stop notice reuses the existing completion notice format (`noticeText`, `internal/execjob/hosted_notice.go`: the `[ws exec] <id> was stopped after <runtime>.` head, `command:` line, output tail and the `exec.tail` pointer)" — bearing: constrains
- 563302fc (2026-11-22, commit): "Completion (Decisions 10, 21): notice only for async or converted jobs, keyed exec:<id>:exit; Notice state pending->delivered/suppressed persisted with the row; exec.stop/abort/interrupt suppress the notice" — bearing: constrains
- 9dcc49d9 (2026-11-22, commit): "Lead directive: the stopped-by-user notice extends the existing [ws exec] completion-notice format; the mockup's <ws-notice> sample is illustrative only." — bearing: constrains
- 261121-feat-ws-board-command-slots (2026-11-22, Result): "the dashboard's final receipt upserts by exec id, so exactly one outcome reaches the lead (a real-holder test proves the order `hand` then `notice`)" — bearing: constrains
- 829a1f2f (2026-11-25, commit): "Gated bgSyncTick(), bgTailSchedule(), bgTailFetch(), and the visibility listener on pageHidden() ... so cached hidden iframes leak zero intervals or network polls." — bearing: constrains
- 97d6e41e (2026-11-25, commit): "zero network polling or timers while the popover is closed" — bearing: constrains
- 509b6d2d (2026-11-25, commit): "Compatibility tier: T0 transparent (additive exec-jobs snapshot fields, additive bg-exec monitor modal UI/CSS/JS assets, additive 24 js.bgexec.* i18n keys across 11 locales)." — bearing: supports

## Route Facts

| fact | allowed values | evidence |
|---|---|---|
| scope.span | multi-file | wsinbox/user_message.go, wsagent/runner_conv.go, wsagent/durable_steer.go, wsdashboard/pageview_conversation.go, wsdashboard/assets/dashboard.tmpl, dashboard-bg-exec-monitor.js, wsi18n/tables/*.json (all under agents-plugin-tool/internal/), wsinbox envelope code, wsagent persisted queue/record structs and children.go, and the whiteboard playbook rsrc source plus generated copies |
| scope.surface | cross-module | wsinbox, wsagent, wslive ring consumers and wsdashboard; persisted ring format unchanged, no exported API named |
| scope.new_public_symbol | yes | model-facing `<ws:exec>` envelope token, `window.wsBgExecJump` JS hook, and a shipped whiteboard-playbook sentence (shipped surface, see shipped-surface-boundary.md); no new RingEntry field; internal wsdashboard parser |
| scope.new_type_contract | yes | persisted ring format unchanged, but an additive omitempty classification field is carried on persisted queue/report structs (QueuedInput, Record.Queued/Reports; see Decisions); plus a dashboard-local parsed-notice view struct |
| scope.test_surface | existing | wsinbox/user_message_test.go, wsagent/exec_seams_test.go, wsdashboard/testdata/*.browser.cjs, wsi18n/i18n_test.go; new Go and Playwright test files also planned |
| complexity.reuse_points | confirmed | RingWake background_exit (wslive/ring.go#L28, wslive/wake.go#L12, wsagent/wake.go#L127), wakeentry template, bgSelectJob in dashboard-bg-exec-monitor.js#L343 |
| complexity.side_effect_risk | high | changes inbox-to-model framing and ring record kind for every exec completion; mixed user+exec batches, durable-steer replay, data-wake-cause companion chime gate |
| risk.correctness | high | strict legacy RingUser parser must not misclassify user text; mixed-batch separation in runner_conv.go and durable_steer.go |
| risk.fit | moderate | extends existing wake rows, but wakeentry already routes background_exit through agenthead agent-note, so the expandable exec layout needs a new branch |
| risk.test | moderate | needs new Go -race tests plus Playwright for live monitor jump and expiry; existing fixtures to extend |
| risk.security_or_contract | moderate | spoof resistance depends on trusted EventID prefix; model prompt framing change; old-binary read compatibility of the ring |

## Phases

### Phase 1: Classify ws/exec completions as background_exit wake rows with legacy fallback and monitor jump

Implement end-to-end Option C across `wsinbox`, `wsagent`, and `wsdashboard`:

1. **Source classification & prompt framing (`wsinbox` + `wsagent`):**
   - Identify `exec:` completion entries by their trusted `EventID` prefix (set by `AdmitCompletion`, never by user-supplied text).
   - Deliver `exec:` completions to the model in the escaped `<ws:exec>` envelope instead of `<ws:user at="...">` (preserve clean separation when real user messages and `exec:` completions appear in the same inbox drain batch), and update the whiteboard playbook sentence (see Decisions).
   - Carry the exec classification through every drain/steer/queue site, including the mid-turn steer, worker, and persisted `Queued`/`Reports` paths, and apply the ordering/origin rules (see Decisions).
   - Emit `wslive.KindWake` with `Cause: "background_exit"` (`Text` = the verbatim notice payload, `Caption` = one-line summary, `Source` = exec id; no new `RingEntry` field, see Decisions) instead of `wslive.KindUser(OriginWhiteboard)` on both turn-start drain and durable-steer paths.
2. **Dashboard rendering & legacy fallback (`wsdashboard` + `wsi18n`):**
   - Render `RingWake(Cause: "background_exit")` exec-completion entries — and legacy `RingUser` entries matching the strict payload grammar in Decisions — as a single-line collapsible `.conv-row` matching existing tool/wake row density and visual tokens:
     - Terminal icon, compact 1-line command summary, exit code (or stopped badge) and elapsed duration, red `.fail` tone for non-zero exit / failure.
     - No `.msg.user` bubble, no `화이트보드` badge, no rewind/edit buttons; preserve `data-wake-cause="background_exit"`.
   - Expand `<div class="row-details">` on click to show full `command`, exit/stop status line, and tail output preview (`<pre class="wake-body">`).
   - Add a secondary "View output" button inside the expanded details (or row action slot without breaking collapsed single-line layout) for a row with an `exec_id`; clicking it runs the click-then-resolve flow in Decisions (open the popup, apply the pending selection after the list loads, anchor fallback when `#bgbadge` is hidden). Hide the button when the open popup's current list lacks the id or the click's list load shows the job is no longer retained (never from a stale closed-popup list).
   - Add all new UI strings across all 11 `wsi18n/tables/*.json` locales.
3. **Verification:**
   - Go unit & `-race` tests covering:
     - `wsinbox` formatting for pure `exec:`, pure user, and mixed user+`exec:` batches, plus a case proving a dashboard/user-submitted entry cannot acquire an `exec:` `EventID` (only `AdmitCompletion` sets it) so user text is never promoted to a wake row.
     - `wsagent` live ring emission (`RingWake` with `Cause: "background_exit"`, never `RingUser`) on normal drain and steer paths, plus mixed user+`exec:` separation.
     - `wsdashboard` conversation view model / SSR rendering for new `RingWake` exec completions, strict legacy `RingUser` fallback (both positive matches and negative spoof/plain-text cases), non-zero `.fail` styling, and absence of rewind/edit controls.
     - `wsi18n` 11-locale parity tests.
   - Playwright browser test verifying:
     - Collapsed 1-line row layout (success & failure tones), click-to-expand details (`command` + `<pre class="wake-body">`), absence of `.msg.user` / rewind / edit buttons.
     - Clicking "View output" on a retained `exec_id` while `#bgbadge` is hidden (no live job) opens the `ws-exec-bg-monitor` popup on-screen and focused on that job's tail rather than the first running job; an expired/unknown `exec_id` hides the button with the "no longer retained" note; no request is made while the popup is closed.
     - Mid-turn steer and persisted-queue paths record `RingWake`, not `RingUser`, for exec notices, with `<ws:exec>` framing and `</ws:exec` escaping in the model-facing text.
     - Legacy `RingUser` fallback renders identically.
   - `GOOS=windows go -C agents-plugin-tool vet -stdversion=false ./...` passes cleanly.

### Result (b8c084db7) - 2026-11-25

- **Source classification & `<ws:exec>` framing (`8762dec82`):**
  - Classified hosted `ws/exec` completion and user-stop notices by their trusted `EventID` prefix (`"exec:"`, set only by `execjob`/`AdmitCompletion`) in `wsinbox/user_message.go`, rendering them in `<ws:exec at="..." id="...">` … `</ws:exec>` with `</ws:exec` escaping and keeping `ParseUserMessages` plain-block handling. Updated `rsrc/whiteboard/whiteboard.md` (plus manifest and `agents-plugin-wsflow/` mirror) with one sentence naming `<ws:exec>`.
  - Carried per-part `ExecNotice` classification through every `wsagent` inbox drain, mid-turn steer, durable-steer, requeue, worker (`children.go`), and persisted `QueuedInput` / `Record.Queued` / `Record.Reports` / `Record.Mail` path (additive `omitempty` / `<ws:exec>` block recovery; T0/T1 compatible).
  - Emitted `wslive.KindWake` (`Cause: "background_exit"`, `Source: exec_id`, one-line `Caption`, unwrapped `Text`) instead of `RingUser(OriginWhiteboard)` across normal drain and durable-steer paths, separating mixed user+exec batches in queue order and using empty origin (`WakeTurn`) for exec-only turn-start batches.
- **Dashboard rendering, legacy fallback & monitor jump (`8e1b0a3e7`, `b8c084db7`):**
  - Added strict shared parser `wsdashboard/exec_notice.go` matching `execjob/hosted_notice.go` (`noticeText`) and `execjob/hosted_hud.go` (`stopNoticeText`) after trimming surrounding whitespace (all `endText` variants, multi-line command/stop notes, output/no-output sections, exact matching `exec.tail` trailer; rejects extra prose, concatenated notices, and spoofs).
  - Rendered both new `RingWake(background_exit)` exec notices and legacy `RingUser` single/mixed-batch exec blocks identically as compact borderless collapsible `.exec-notice` rows (`data-wake-cause="background_exit"`, `.fail` tone for non-zero/error, no `.msg.user`, no rewind/edit/copy controls; non-matching `background_exit` wakes fall back to `agent-note`), compact `.held-exec` display for held `exec:` entries, aligned `userBlockTexts` / `countUserEntries`, and registered `.exec-notice` in `dashboard-audit-rows.js`.
  - Wired click-then-resolve `window.wsBgExecJump(id, anchorEl)` in `dashboard-bg-exec-monitor.js` preserving zero closed-state requests/timers, gating `.exec-view-out` on `html[data-sysopen="1"] body.live` (matching `#bgbadge` `.SysOpen` posture), anchoring `#bgpop` on-screen when `#bgbadge` is hidden, applying `bgPendingSel` over default first-running selection, and hiding expired rows with the localized note on resolution (without stale closed-state or in-flight list races).
  - Added 13 `entry.exec.*` keys across all 11 locale tables under `wsi18n/tables/*.json`.
- **Review (`code-review-lite`, one pass):**
  - Fresh read-only reviewer (`id:ivorylungcrablikegawk`) reported 0 Critical, 1 Important (vacuous `.act-rewind`/`.act-edit` selector in `exec-completion-row.browser.cjs`), and 2 Minor (in-flight `bgListRequest` toggle guard + present-row reconciliation in `dashboard-bg-exec-monitor.js`; multi-line `stopped by user:` note test case in `exec_notice_test.go`). All 3 findings were fixed in `b8c084db7` and re-verified.
- **Verification:**
  - Remote `w5090` (`go test -race`): `./internal/wsinbox/...` PASS, `./internal/wsrsrc/...` PASS, `./internal/execjob/...` PASS, `./internal/wsi18n/...` PASS (all 11 locales), `./internal/wsagent -run 'ExecNotice|ExecClassification|DurableSteer|Child|Inbox|Runner'` PASS, `./internal/wsdashboard -run 'TestParseExecNotice|TestExecCompletionRowSSRAndLegacyParity|TestMessageRouteCannotForgeExecEventID|TestEntryText|TestUserMessage|TestHeld'` PASS (`33.2s` pre-fix, `2.6s` post-fix).
  - Remote `w5090` Playwright Chromium (`WS_PLAYWRIGHT_MODULE=/tmp/shellredesign-pw/node_modules/playwright`): `TestBgExecMonitorBrowser` PASS (`15.61s`), `TestExecCompletionRowBrowser` PASS (`5.20s`, covering LAN read-only hide, compact ok/fail rows, non-vacuous rewind/edit absence, zero closed-state requests, hidden-badge on-screen jump, open-monitor and click-time expiry hide, `bgPendingSel` overriding first-running on legacy `RingUser`, zero post-close requests).
  - Remote `w5090` `GOOS=windows go vet -stdversion=false ./...`: PASS (0 warnings).
  - Local checks: `node --check` on edited JS clean, `git diff --check` clean, `python -m unittest discover -s agents-plugin-wsflow/tests` PASS (22 tests, 1 skipped), `node --test` on `frontend-audit-rows.test.js`, `frontend-audit.test.js`, `frontend-companion.test.js`, `frontend-user-jump.test.js`, `frontend-interaction.test.js`, `frontend-history.test.js`, `frontend-stream-message.test.js`, `frontend-scroll.test.js`, `frontend-idle.test.js`, `composer-outbox-resilience.test.cjs`, `notifications.test.cjs` PASS.
