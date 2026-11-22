---
title: "agy 헤더 무응답 스트림 120초 차단과 HTTP/2 프레임 추적 옵트인"
related:
  261122-feat-ws-agy-lead-only-harness: "agy workers are refused there; this ticket scopes to hosted agy leads and does not depend on its merge order"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 06c73425b5249759
sage-review-completeness-reviewed: 06c73425b5249759
---

# Cut headerless agy model streams after 120 seconds and add an opt-in HTTP/2 frame trace

## Background

A read-only analysis of 528 of agy's own ACP logs (2026-11-18..22, agy
1.5.11-1.5.14) found 259 ten-minute stalls: 246 in the request phase, with no
response headers at all, and 13 in the body phase. About 2.9% of main model
requests stall. A normal request receives headers within a p50 of 5.4s, a p99
of 16.5s, and a 32s maximum. After agy's own 10-minute stream idle timeout,
its retry receives headers in 9-32s and succeeds. No GOAWAY, RST, 429, or TLS
error was seen. Other requests to the same host succeeded during 235 of the
246 stall windows. ws prompts do not cause it: the stall rate is 1.97% with a
ws prompt in the prior 60s and 3.73% without. The cause is most likely
server-side, but this is INFERRED, because agy's logs cannot prove that the
server sent no frame. Analysis:
`ai-docs/assets/mockups/261122/agy-stall-trace/analysis.md`.

Owner decision (1544)=b, 2026-11-22: add an opt-in HTTP/2 frame trace to
confirm the evidence, and cut a headerless request after 120 seconds with an
interrupt plus automatic `continue`. Each stall then costs about 2.5 minutes
instead of 10.

Go paths are relative to `agents-plugin-tool/internal/`.

## Current Behavior

- The agy model-wait watchdog arms a timer of `agy.model_wait_timeout_seconds`
  (default 120) on raw ACP silence during an active model wait. While any tool
  call or approval is pending, it is paused. On firing, it interrupts and sends
  an automatic `continue`, up to `agy.model_wait_retries` (default 3)
  (`wsagent/agy_watchdog.go:144-175,329-345,424-470`;
  `agents-plugin/agy-config-manifest.json:5-18`).
- The owner disabled silence detection, `agySilenceWatchdogEnabled = false`
  (`wsagent/agy_watchdog.go:57-59`), because a long tool-call generation is
  silent on ACP exactly like a stalled stream. Only agy's in-band failure lines
  still trigger recovery, and agy's native idle-timeout retries get an
  11-minute drain (`wsagent/agy_watchdog.go:30-45`).
- agy writes one log per session process under
  `~/.gemini/antigravity-cli/log/acp-worker-<YYYYMMDD_HHMMSS>-<pid>.log`
  (glog format, `I<MMDD> <HH:MM:SS.micros> <goroutine> <file:line>] <msg>`):
  - The `server.go` line `Sending user message to conversation <id>` names the
    ACP session id ws records as the backend session id.
  - Every model response with headers logs `http_helpers.go`
    `URL: https://<host>/v1internal:streamGenerateContent?alt=sse Trace: <hex>
    ResponseID: <id>` on the requesting goroutine.
  - agy's safety-monitor requests also log `streamGenerateContent`, on
    goroutines that first log `llm_monitor.go` `... Calling LLM`.
  - A stall ends with `run.go` `Run: attempt N failed (request failed: stream
    idle timeout (10m0s) ...)` on the main run goroutine.
  - Observed in local logs 2026-11-22; the format is agy's, not a contract.
- ws launches `agy acp --process-per-session` with `agyEnv(spec)`
  (`wsagent/agy.go:164-172,192-194`). The process runs under a session holder.

## Decisions

- **Owner (1544)=b.** Implement both the opt-in frame trace and the 120-second
  headerless cutoff.
- **Lead, cutoff signal.** Re-enable the existing silence timer only behind a
  log confirmation. Do not use a new timer:
  - When the timer fires (ACP silence for `agy.model_wait_timeout_seconds`, no
    tool call or approval pending), read the tail of this session's agy worker
    log once.
  - The comparison point is the request boundary, not the last ACP activity.
    A boundary needs positive evidence that agy starts a fresh model request.
    Only three events qualify:
    - ws sending a user-turn prompt while no turn is active on the session;
    - ws sending the watchdog's own automatic `continue` after its interrupt;
    - a tool call of the current turn, one that paused the timer, reaching a
      terminal status (agy's next model request follows the tool result).
    The runner records the wall-clock time of the latest boundary.
  - Not boundaries: a steer sent while a turn is active (agy carries the run
    forward under the new prompt, `wsagent/agy.go` steer handling, and the
    stream may already have headers), and a background tool completion that
    ws tracks apart from the current turn's pending tools. The boundary stays
    where it was. If a steer did start a new request that then stalls, the
    earlier request's header in the window suppresses the cut, and agy's
    native retry recovers it: an ambiguous attempt never gets cut.
  - A successful steer restarts the silence timer with a full
    `agy.model_wait_timeout_seconds`, keeping the boundary. The steer
    acknowledgment is a prompt response, not model activity, so without the
    restart a steer landing just before the deadline could be cut moments
    later. After the restart, a cut still needs no main header since the
    unchanged boundary: whether the steer carried the old stream forward or
    replaced it, a full timeout with no header on either is a headerless
    stall, and a header on either suppresses the cut.
  - An ACP agent text, thought, or plan update after the boundary proves the
    stream has a response, so the attempt is not headerless: the timer
    re-arms and the cutoff never fires for that attempt. Recovery for it stays
    with agy's native retry and the in-band failure path.
  - Cut (interrupt plus automatic `continue`, the existing recovery path and
    retry budget) only when no ACP content update followed the boundary and
    the verified log tail shows no main-request `streamGenerateContent`
    header line timestamped at or after the boundary minus a 2-second skew
    margin.
  - A main header line in that window means the model is generating (the long
    tool-call case the owner protected), so re-arm the timer without spending
    a retry. A previous request's header before the window does not count,
    so an automatic `continue` or a new user turn starts a fresh window.
  - A log that cannot be located, read, verified, or parsed means do not cut:
    fall back to today's behavior and log one diagnostic line per session.
  - Rejected: the last ACP activity as the comparison point (a text update
    after the headers moves it past them and cuts a live tool-call
    generation); silence alone (the owner's reason for disabling it);
    continuous log tailing (file I/O on every turn,
    `ai-docs/manuals/performance.md`).
- **Lead, main vs monitor requests.** A `streamGenerateContent` header line
  counts as a main request unless its goroutine id logged an `llm_monitor.go`
  `Calling LLM` line within the tail being read. A monitor header whose
  marker fell outside the tail therefore counts as main and only suppresses a
  cut; it can never cause one.
- **Lead, tail verification.** A negative result (no main header) is used
  only when the tail is verified; otherwise the result is "unknown":
  - Coverage: the first complete glog line in the tail is timestamped before
    the boundary minus the skew margin. A tail that starts after that point
    is unknown, whatever it contains.
  - Format, per tail: the tail being read must itself contain at least one
    recognized `streamGenerateContent` header line (`http_helpers.go` ...
    `URL: ... streamGenerateContent`), main or monitor, anywhere in the tail.
    agy's safety monitors issue requests around every tool call, so a normal
    tail has one. A tail with none, including a glog-framed tail whose HTTP
    marker changed, is unknown, not headerless. No process-wide flag: an
    earlier valid tail never validates a later one. The first request of a
    process, before any recognized header, is therefore not cut.
  - Timestamps: glog lines carry local month, day, and time without a year;
    they are compared in the local zone against the boundary, with the year
    taken from the boundary.
- **Lead, log location.** Find the worker log once per session process, on
  the first timer firing. The candidate is the newest `acp-worker-*.log` whose
  content names the session's backend session id in `Sending user message to
  conversation <id>`; consider only files modified since the process started.
  Cache the path in memory for the process lifetime. Read a bounded tail
  (512 KiB) with one open, seek, and read per firing.
- **Lead, knobs.** Two knobs:
  - `agy.stall_log_check`, boolean, default `true` in both ws and wsflow.
    Turns the log-confirmed cutoff on; `false` keeps today's in-band-only
    recovery.
  - `agy.http2_frame_trace`, boolean, default `false` in both packages. Adds
    `GODEBUG=http2debug=2` to agy's environment at process start and turns on
    the frame capture below.
  - Both go into `agents-plugin/agy-config-manifest.json` and
    `agents-plugin-wsflow/agy-config-manifest.json`, with descriptions written
    as the existing ones are. They are read at process start like the
    existing agy knobs.
  - The existing `agy.model_wait_timeout_seconds` stays the cutoff duration:
    ws default 120, wsflow default 0. With 0 the timer never arms, so wsflow
    keeps the cutoff off until its user sets a timeout, even with
    `agy.stall_log_check=true`. The descriptions say so.
- **Lead, frame trace deliverable.** Stream-level transport evidence, not
  agy request ids:
  - Level 2 (`http2debug=2`) is required: it adds Go's framer read and write
    lines, which carry the framer pointer (one per connection) and the stream
    id. Level 1 has no connection identifier.
  - A headerless stall shows as a written `HEADERS` frame on some stream with
    no read `HEADERS` frame for that stream on that framer. The stalled
    request is matched by time to the request boundary; agy's `Trace` and
    `ResponseID` appear only after headers, so they cannot name it.
  - When the knob is on and the cutoff fires, ws appends one marker line to
    the capture file with the boundary time and the cutoff time, so the
    stream can be found by hand.
  - Whether agy's binary honors `GODEBUG=http2debug` is unverified (no agy
    execution here). The knob description says the trace is best-effort.
- **Lead, frame trace capture and redaction.** Go's verbose HTTP/2 logs
  include outgoing header values (credentials) and received DATA previews.
  None of that may be retained or shared:
  - Hosted agy leads run under a session holder, which reads the child's
    stderr into its replay ring (`wsholder/holder.py` `_read_output`). When
    the knob is on, the daemon passes a capture option in the holder spawn
    request; the holder then routes every stderr line that starts with Go's
    `http2:` log prefix (after the optional `log` timestamp) through a
    filter, never into the ring.
  - The holder's line splitter can cut one long stderr line into several
    partial records. The classification is made on the first record of a
    logical line and carries to its end: every continuation record of a
    trace line goes through the filter too, never to the ring.
  - The filter keeps only framer summary lines (`Framer 0x<ptr>: wrote|read
    <FRAME TYPE>`), truncated after the allowlisted fields (`stream=`,
    `len=`, `flags=`, `ErrCode=`, `LastStreamID=`, `incr=`). Every other
    `http2:` line is dropped: header encoding and decoding lines, DATA
    previews, and anything unrecognized.
  - Kept lines are appended to `<session dir>/agy-http2-frames.log`, with
    the holder's wall-clock time per line. The append stops at 64 MiB with
    one marker line.
  - Non-`http2:` stderr lines keep today's path unchanged, so the replay ring
    and the daemon's fatal-event stderr tail (`wsagent/stream.go`) never see
    raw trace text.
  - Knob off: no `GODEBUG` change, no capture option, no file. The holder
    spawn option is additive, and a new daemon always launches its own
    embedded holder script.
  - Rejected: a raw stderr append (retains credentials and response text);
    level 1 alone (no connection identity); filtering in the daemon (the
    holder ring would already hold the raw lines).
- **Lead, scope.** Hosted agy leads only. agy workers are refused under
  `261122-feat-ws-agy-lead-only-harness`, and this ticket adds nothing for
  them.
- **Lead, compatibility.** T0. Two additive manifest keys and one optional
  holder spawn field read only by the holder script embedded in the same
  daemon; no stored shape, MCP argument, or record field changes. An older
  daemon ignores the new keys and keeps silence detection off.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `wsagent/agy_watchdog.go`, `wsagent/agy.go` env and boundaries, a new agy log tail parser, `wsholder/holder.py` stderr filter, both `agy-config-manifest.json` files and the settings catalog |
| scope.surface | internal | Two additive manifest keys and one optional holder spawn field read by the embedded holder; no MCP argument or stored shape |
| scope.new_public_symbol | yes | Manifest keys `agy.stall_log_check`, `agy.http2_frame_trace` |
| scope.new_type_contract | no | No exported type or signature contract |
| scope.test_surface | existing | `wsagent` agy watchdog tests with a fake clock, new parser tests on synthetic fixtures, the `wsholder` test harness, manifest and settings catalog tests |
| complexity.reuse_points | confirmed | Reuses the existing silence timer, interrupt plus `continue` recovery, and retry budget (`wsagent/agy_watchdog.go:144-175,329-345,424-470`) |
| complexity.side_effect_risk | moderate | Reads agy's log file on timer firing; writes a capped capture file only when the trace knob is on |
| risk.correctness | high | A false cut interrupts a live generation; boundary, coverage, and format rules must hold |
| risk.fit | moderate | Depends on agy's unversioned log format; unknown falls back to today's behavior |
| risk.test | high | Fake-clock watchdog cases, clipped tails, format drift, steer timing, split holder records |
| risk.security_or_contract | high | HTTP/2 trace output carries credentials; redaction must keep them out of files, the replay ring, and fatal events |

## Constraints

- Read before editing: `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/performance.md`, `ai-docs/manuals/config-settings-ui.md`
  (manifest keys and settings catalog), and
  `ai-docs/manuals/shipped-surface-boundary.md`.
- Never run the real `agy` binary. Build parser and watchdog tests from
  synthetic log fixtures in agy's format. Do not commit real log content.
- Never read agy credential or state files (`google_accounts.json`,
  `settings.json`, `state.json`, `*.pb`, `*.pbtxt`, `installation_id`). Read
  only `log/acp-worker-*.log`.
- Do not change the in-band failure path, the 11-minute native-retry drain,
  or peer mail delivery.

## Phases

### Phase 1: Log-confirmed headerless cutoff and opt-in frame trace

Implement the log locator and tail parser, gate the silence timer's cutoff on
it, add both knobs, and inject the frame-trace environment variable with its
output handling.

Verification:
- Go unit tests on synthetic fixtures:
  - The parser tells main from monitor header lines; a monitor header whose
    `Calling LLM` marker is clipped counts as main.
  - Locating by session id picks the right file among several and ignores
    files older than the process.
  - A missing, unreadable, or garbled log returns "unknown".
  - A tail whose first line is after the boundary minus the skew margin
    returns "unknown"; so does a covered, glog-framed tail with no
    recognized header line, including a valid first tail followed by a
    covered tail whose HTTP marker changed (format drift).
- Watchdog tests with a fake clock and a fake log:
  - Silence after a prompt send or a tool-call completion, with no main
    header in the window, cuts with an interrupt plus `continue` from the
    shared retry budget.
  - A main header inside the window re-arms and spends no retry.
  - Headers, then an agent text update, then silence longer than the timeout
    (the long tool-call generation): no cut, no retry spent.
  - An automatic `continue` after an earlier request's header: that header
    does not count, and silence with no new header cuts.
  - A steer during a request that already has headers: the boundary does not
    move, the header stays in the window, and there is no cut.
  - A background tool completion does not move the boundary.
  - Neither request has headers: a steer lands one second before the
    original deadline; no cut at that deadline; a cut one full timeout after
    the steer if still no main header since the boundary.
  - An unknown log does not cut.
  - Pending tool calls and approvals still pause the timer.
  - `agy.stall_log_check=false` restores today's behavior.
  - `agy.model_wait_timeout_seconds=0` (the wsflow default) never arms the
    cutoff, with `agy.stall_log_check=true`.
  - In-band failures and the 11-minute drain are unchanged.
- File-operation count per firing (one open, seek, and read; the locator scan
  once per process) recorded in the commit.
- `agy.http2_frame_trace=true` adds `GODEBUG=http2debug=2` to the agy
  environment and the capture option to the holder spawn; `false` adds
  neither.
- Holder tests (the existing holder test harness) with a synthetic child
  writing mixed stderr:
  - Framer summary lines reach `agy-http2-frames.log`, truncated after the
    allowlisted fields; header encoding lines, DATA previews, and unknown
    `http2:` lines are dropped; none of them reach the replay ring.
  - Non-`http2:` stderr lines reach the ring as today.
  - A trace line longer than the record limit, split into partial records:
    no fragment reaches the ring.
  - The file stops at the cap with one marker line; knob off writes no file
    and leaves stderr handling unchanged.
- Settings catalog and manifest tests for both keys, in ws and wsflow, with
  the package defaults above.
