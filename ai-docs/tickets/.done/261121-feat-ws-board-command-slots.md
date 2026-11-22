---
title: "대화창 ! 커맨드 실행과 커맨드 슬롯"
related:
  261117-feat-ws-exec-tools: prerequisite, landed (daemon-owned hosted exec service this reuses)
  261117-feat-ws-exec-mux-terminal: neighbouring exec surface (terminal sessions); independent
  261118-feat-ws-exec-intent-filter: neighbouring exec safety layer
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: b71e948adb14f634
sage-review-completeness-reviewed: b71e948adb14f634
completed: 2026-11-23
---

# Chat composer `!` commands and command slots

## Background

Origin: the owner's idea memo (2026-11-21 triage) asked for user-clickable
commands registered on the whiteboard. The owner reshaped it on 2026-11-22:
the entry point and owner of such a command is an open hosted lead's chat, not
a whiteboard slot with its own run path. The earlier design (dashboard-owned
launcher callback, whiteboard-scoped slots, stdout log via a holder capture
flag, agent-silent runs) is superseded and removed from this ticket.

The feature has two parts:

1. **Composer `!` exec.** The user types `!<bash line>` in a hosted lead's
   dashboard chat composer and the line runs immediately as a (git-)bash
   command instead of being sent as a prompt. The result reaches the lead's
   context like an agent-run exec result, but does not start or steer a turn.
2. **Command slots (user commands).** Bash lines the agent registers so the user need not type
   `!...`; a click runs the stored line through the same path.

The design was settled by a static source review of develop `b87d0e96d`
(report: `composer-exec-design`, 2026-11-22). Source refs in this ticket are
relative to `agents-plugin-tool/` at that commit and are evidence, not edit
targets.

## Product Contract (owner-confirmed 2026-11-22)

1. In a hosted lead's dashboard chat composer, a message whose very first
   character is `!` runs immediately as a (git-)bash command instead of being
   sent as a prompt. The server, not the client, owns the `!` grammar: first
   byte only; a leading space means an ordinary prompt.
2. The run is registered as that chat session's ws/exec job: same owner,
   generation, worktree fence, holder lifecycle and close cascade as a
   lead-issued `exec.shell`.
3. The result enters the lead's context like an agent-run exec result but
   WITHOUT starting or steering a turn. Wait up to 10 s:
   - done within 10 s: output <= 4 KiB (raw combined bytes) is attached
     inline; larger output is attached as a file path;
   - still running after 10 s: the job converts to async and only the job ID
     is attached; its later completion uses the ordinary exec completion
     notice path unchanged (which may wake an idle lead, as today).
4. Command slots are (git-)bash commands the agent registers so the user need
   not type `!...`; a click runs the stored command through the same path.
5. An explicit user `!` send or slot click is a one-use approval for that exact
   invocation (slot: the displayed revision); the agent's ordinary exec
   approval policy is unchanged. The `!` input path, slot buttons, result
   cards and output-file links are localhost-only (`TrustedWrite` route); the
   async completion notice stays in chat exactly like an ordinary exec notice.

## Decisions

Technical decisions below were settled by the lead and auto-confirmed on
2026-11-22.

- **Shared launch through a typed dashboard binding.** Extract the shell
  preparation from `Server.callHostedExec` (`internal/mcp/server_exec_hosted.go`)
  so both frontends share `OverlayEnv`, `ResolveWorkingDir`, `ShellArgv` and
  `execjob.Hosted.Launch`. `cmd/ws-mcp` binds a narrow typed dashboard
  dependency to the same service `openHostedExec` builds. The wait policy is
  per launch: 10 s for composer launches; the MCP default stays 30 s. Today
  the window is a `Hosted`-wide option (`h.opts.Window` in
  `internal/execjob/hosted_launch.go`), so the launch request gains a window
  override. The
  request carries the expected open generation so admission can refuse a stale
  one, and the owner, generation and bound worktree come from server-side
  runner state, never from the browser. Rejected: a synthetic MCP call or fake
  session key; a separate dashboard-owned job owner (the installed
  `HostedSessions` adapter needs an open runner conversation, and a second
  owner would duplicate the fence and close cascade).
- **New route `POST /s/<reply-id>/composer-exec`.** Registered beside
  `message` in `handleSessionRoutes` (`internal/wsdashboard/page_routes.go`),
  `TrustedWrite` plus the runner's open hosted-lead authority. Execution runs
  outside `PageInput` and the inbox lock; the route acknowledges acceptance
  quickly and the card updates independently of the 10 s wait. A daemon-owned
  submission lifetime applies after acceptance: closing the tab does not cancel
  a command the user already submitted. The server validates the first-byte
  `!` grammar, strips exactly one byte, requires a non-blank payload and
  refuses a submission that also carries attachments (the UI keeps the draft).
  The browser sends a raw first-character candidate to this route before trim,
  mention processing or selection assembly; ordinary prompts keep using
  `message`, including a raw leading space followed by `!`. Rejected:
  client-side `!` interception into the `message` route; falling back to
  sending the text as a prompt when the route is unknown (an old daemon must
  fail closed, since a command line leaking to the model as a prompt is the
  worse failure). Existing `message` semantics for old clients do not change.
- **Passive hand-off, not delivery.** A bounded, runner-owned, one-shot,
  in-memory-only pending-input source keyed by reply ID + generation + exec
  ID, owned by the runner conversation, scoped to the open generation and
  dropped when it closes. Bounds: 16 receipts and 16 KiB per hand-off; on
  overflow the oldest receipts collapse to `job <id>: see exec.list`. There
  is no persistence: after a daemon restart the lead can still query the job
  through `exec.list` / tail, and the card shows the job state. `turnInput`
  (`internal/wsagent/ambient.go#L67`, called from `runner_conv.go#L1807,L1813`) consumes it on the next eligible send,
  the same seam as `<ws-ambient>`; it is acknowledged only on an accepted send,
  so a failed send leaves it pending and a new arrival is never lost. It never
  touches `wsinbox`, `canKick`, steering or the send/held counters. Output is
  framed as untrusted data, keeping the existing language/compaction framing.
  A converted job's ID is made visible under the same owner and generation
  before the first completion admission (`execSessions.Notify` →
  `AdmitCompletion`), so an exit racing conversion cannot order the notice
  ahead of the ID; the existing deferred retry covers a temporary admission
  failure. Rejected: a `wsinbox` entry (active delivery would wake or steer
  the lead, and old `wslive.State.Apply` rejects unknown kinds); `Runner.Send`
  (starts a turn); system-prompt mutation; a Type 2 pending store (adds file
  writes on the send path for a rare restart window).
- **Output classification and file.** Classify by raw combined byte count
  (stdout+stderr as the exec result already combines them), never by the
  returned tail length or decoded character count; <= 4096 bytes inline
  (decoded with `DecodeText`), 4097+ a file. The complete retained combined
  range is snapshotted at terminal classification while the job is still
  retained, not rebuilt from the 4 KiB `Tail` reader. A fast oversized result
  becomes a private Type 5 file under daemon state, written off the page and
  send read paths, atomically, and served through a local-only, owner-checked
  route `GET /s/<reply-id>/composer-exec/<submission-id>/output` (a
  TrustedWrite-equivalent read gate, owner-checked); the ID is the
  server-minted submission ID and the browser never supplies a filesystem
  path. A path is shown only once the file exists; truncation and capture
  failure are shown
  explicitly. Retention: 1 MiB per run (combined stdout+stderr), 7 days and
  100 MiB total, deleting whole oldest files away from interactive reads.
  There is no holder-side capture across daemon replacement: the snapshot is
  ring-bounded only. Rejected: an opt-in holder capture flag and holder-side
  stdout file (a new attach/downgrade surface for a short window);
  stdout-only logs (the exec reply is combined output); per-chunk storage in
  SQLite; a browser-supplied path or a separate artifact-ID namespace.
- **Shell and stdin.** Empty stdin for composer runs, so a reader gets EOF
  rather than waiting on an input control the composer lacks. Shell selection
  is identical to `exec.shell` (`execjob.ShellArgv`: bash on POSIX with the
  existing `/bin/sh` fallback, Git Bash on Windows with the existing lookup and
  refusal). cwd defaults to the lead's authorized worktree root as
  `exec.shell` does with no cwd argument; no browser-supplied env, cwd or
  credential.
- **Submission-ID idempotency.** Every typed or slot submission carries a
  submission ID. It is claimed before launch in an in-memory claim map keyed
  by submission ID and mapped to exactly one job, with the hosted job row as
  the durable truth; a retry after a lost response returns that mapping, and
  only a deliberate rerun uses a fresh ID. After a daemon restart a
  submission the map no longer knows shows "outcome unknown — check
  exec.list" on the card and is never re-run automatically; the user
  re-sends deliberately. Exactly-once delivery across the backend
  acceptance/daemon crash boundary is not promised; the design is stable IDs
  plus acknowledgement/recovery. Rejected: a durable receipt store.
- **Slot store.** A dedicated daemon-local SQLite store (Type 2, held handle,
  in-memory read model, one WAL commit per changed row; nothing committed for
  an unchanged state), separate from the agent-authored whiteboard DB and its
  export. Scoped to the lead reply ID and bound worktree. A slot is a stable
  ID, label, optional description, raw shell line and revision; no per-output
  storage. Run sends the slot ID and the displayed revision; the server
  resolves the stored definition so the displayed command is exactly what
  runs, and a stale revision rejects and refreshes the list. Rejected: slots
  as whiteboard content (executable registration inside an agent-authored,
  exportable store); direct-argv slots (the contract is bash lines);
  overloading `exec.shell` arguments (an old daemon refuses unknown
  arguments, a version trap). Tokenless Pi / Claude fallback transports
  cannot be authenticated by the hosted-exec bearer alone and get no
  registration in this ticket.
- **Registration tools: `session.register_user_command` and
  `session.unregister_user_command` (owner-confirmed 2026-11-22).** The
  concept is called "user commands" in the tool surface and code; the UI
  label may stay "command slots". `session.register_user_command` upserts by
  slot ID (stable ID, label, optional description, raw shell line) and
  returns the stored revision; `session.unregister_user_command` withdraws by
  slot ID. Limits: 32 user commands per lead conversation; label <= 64
  chars, description <= 256 chars, shell line <= 4 KiB; a register beyond
  the cap (or an over-long field) is refused with a clear error. Rejected:
  unbounded registration. They live in the `session.*` namespace beside `session.roster`
  (`internal/mcp/session_hire.go:18,128`, `internal/mcp/server_dispatch.go:239`);
  the owner's "daemon.(same namespace as roster)" referred to this
  namespace, and no `daemon.*` namespace exists. Advertisement rule: only to
  an authenticated hosted lead using its own binding. Rejected:
  `exec.slot.set` / `exec.slot.withdraw` (couples registration to the `exec`
  namespace and to native exec cutover); a `daemon.*` name (no such
  namespace).
- **User-commands refresh and removal.** An open localhost page refreshes its
  private user-command list by reusing the existing page SSE stream with an
  opaque user-commands revision event that makes the page refetch the list.
  Rejected: polling. Each slot also has a localhost-only remove control
  that calls the same service as `session.unregister_user_command`.
  Rejected: agent-only removal (the user would be stuck with stale buttons).
- **UI.** A private command card in chat reusing the existing command-card
  treatment (literal command, state, exit, inline result or output link,
  existing `copyText` action for the path) with escaped text rendering. It is
  keyed by submission ID and then by the hosted job ID and is not part of the
  shared transcript (session page sources are cached by reply ID, language and
  theme with no viewer locality). While active the card refreshes from
  owner-checked `Hosted.Get` / `Hosted.Tail`, with one bounded refresh on page
  load, stopping when terminal. The rail's existing exec counts are unchanged.
- **Superseded old (1548).** The previous Open Decision Queue item (1548:
  policy bundle - user-only registration, board-scoped slots, dashboard-only
  results with no agent notice, 1 MiB / 7 days / 100 MiB retention, dashboard
  directory picker) is superseded by the owner's reshaped contract above and
  by (1550) = (a): registration is by the agent, slots are scoped to the lead
  and its worktree, results enter the lead's context passively, an explicit
  user send or click is a one-use approval, and the 1 MiB / 7 days / 100 MiB
  retention carries over for the output file. The directory picker and the
  cwd field are dropped, confirmed as a consequence of the owner's contract
  (a slot has no cwd; cwd is always the lead's bound worktree root, as
  `exec.shell` does with no cwd argument). The old Phase 2 (browser cwd
  picking) is removed.
- **Compatibility: T0 for every surface** (no `exec_jobs` schema or
  `user_version` change - an increased `user_version` would make an old
  reader refuse the DB and disable hosted tools, a T3 boundary). Evidence:
  - *HTTP route and bundled assets.* An old daemon does not know
    `composer-exec` and the new assets fail closed on it; old assets keep
    POSTing `message` with the old acknowledgement and a new daemon keeps
    accepting existing inbox traffic
    (`internal/wsdashboard/page_routes.go:49–69,139–188`,
    `internal/wsdashboard/assets/dashboard-composer-delivery.js:632–648,666–671`).
  - *MCP user-command tools.* New `session.*` names, advertised only to an authenticated hosted
    lead; no existing tool name, argument or result changes. New callers gate
    on discovery; an old daemon refuses an unknown name
    (`internal/mcp/server_dispatch.go:76–105,1634–1640`,
    `internal/mcp/tool_arguments.go:127–151`,
    `internal/mcp/server_inventory.go:186–199`).
  - *Slot store and output directory.* Independent
    stores (submission receipts are in memory only). An old daemon keeps writing only its explicit `exec_jobs` columns
    and board/session stores and cannot overwrite the new paths; the new
    daemon reopens both and reuses old job rows under their existing
    200-job-cap retention, so a pruned job reports as no longer queryable
    rather than breaking (`cmd/ws-mcp/exec_hosted.go:15–20,53–71`,
    `internal/execjob/hosted_store.go:19–27,59–89,138–156,319–335`).
  - *Pending context and events.* The hand-off adds no inbox entry kind,
    tracker event, hello change or holder attach/event shape; the later
    completion reuses the existing `exec:<id>:exit` event ID. During a
    downgrade the passive hand-off is new-only behavior. Pending input is
    in memory only, so there is no stored state and nothing for an old
    binary to read or overwrite: T0 trivially
    (`internal/wsagent/exec_seams.go:370–413`,
    `internal/wsagent/runner_conv.go:1730–1748`,
    `internal/wslive/state.go:71–74,514`).

## Constraints

Manuals bound to the paths this ticket edits (AGENTS.md `Implementation
Conventions`); read each before editing a covered file:

- `agents-plugin-tool/` → `ai-docs/manuals/shipped-surface-boundary.md` and
  `ai-docs/manuals/performance.md`.
- `agents-plugin-tool/internal/` → `ai-docs/manuals/release-compatibility.md`
  (T0 target above; run its scenario sheet for the new → old → new cycle).
- `agents-plugin-tool/internal/mcp/` → `ai-docs/manuals/ws-mcp.md` (the new
  `session.register_user_command` / `session.unregister_user_command` tools).
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- `ai-docs/manuals/config-settings-ui.md` does not apply: retention limits are
  fixed constants in this ticket, not settings. It applies only if a later
  change exposes them as settings.

Behavioral boundaries:

- Performance (`ai-docs/manuals/performance.md`): page load, slot list and
  composer ack are memory-only with no file I/O on the interactive path; the
  output file is Type 5 and slots are Type 2, both written off page/list/send
  reads; one WAL commit per slot change; retention deletes whole oldest files
  off interactive reads. Measure file operations and latency for slot
  registration, page load/list, a successful short composer send, an
  oversized send and an async conversion against the Windows file-touch
  baseline, and report before/after in the Result.
- The worktree fence regulates launch locations and lease races; it is not a
  filesystem sandbox for bash. The UI always shows the exact stored command,
  and the revision gate keeps the clicked command identical to the displayed
  one. A slot click can still run code loaded from the worktree.
- Command output is escaped in the dashboard and framed as untrusted data in
  model input.
- Localhost-only: the `!` route, slot buttons and slot list, result cards and
  output-file links require the localhost cookie plus `TrustedWrite`; LAN
  views never show commands, output or file links. A local user action is
  proven by route admission, never by a caller-supplied field. The async
  completion notice includes the command and 4 KiB combined tail in chat like
  any exec notice and so is not LAN-private; this is accepted by (1550)
  (the notice stays exactly like an ordinary exec notice).
- Out of scope: slot registration over tokenless Pi / Claude fallback
  transports (no hosted-exec bearer to authenticate with; extending them is a
  separate change that must not accept an arbitrary reply ID, claimed harness
  name or caller-supplied environment marker as identity); a native OS file
  dialog; project-shared slots; a strict-bash resolver option (shell selection
  stays identical to `exec.shell`); a terminal-style interactive stdin for
  composer runs.

## Prior Art

- `execjob.Hosted.Launch` and its wait/convert state machine
  (`internal/execjob/hosted_launch.go`, `hosted.go`): conversion arbitrates exit
  versus timeout under one lock; caller cancel before conversion stops the job.
- `<ws-ambient>` rendering through `turnInput` (`internal/wsagent/ambient.go`,
  `runner_conv.go`): the seam the passive hand-off shares.
- `Hosted.deliver` / `AdmitCompletion` (`internal/execjob/hosted_notice.go`,
  `internal/wsagent/exec_seams.go`): the ordinary completion notice, deduped
  by event ID and guarded by open generation.
- The `command` ring entry card in `dashboard.tmpl` and `copyText` in
  `dashboard-actions.js`: the card treatment and path-copy action. Note
  `wslive.State.Apply` accepts that entry only for a leading slash, so the
  composer card is a separate private projection rather than a ring entry.
- The dashboard outbox draft/unknown-result behavior in
  `dashboard-composer-delivery.js`: adapt the acknowledgement to the
  submission receipt; do not insert a held inbox bubble.

## Prior Decisions

- 0100ef97 (2026-11-22, commit): "AdmitCompletion writes one Steer=true inbox entry carrying EventID ... Record.AcceptedEvents (cap 64) dedupe" — bearing: constrains
- 0100ef97 (2026-11-22, commit): "Trusted binding (Decision 13): ProxyHello.Conversation is json:\"-\" and only Runner.ResolveMCPToken sets it; a supplied session_key stays a cwd grant only" — bearing: constrains
- 0100ef97 (2026-11-22, commit): "Daemon-owned approval (Decision 4): ApproveExec re-reads the live posture from memory; Non-leads run at once" — bearing: constrains
- 1793cab6 (2026-11-22, commit): "Compatibility verdict, T0 ... An older daemon names only its own columns: its SELECT, INSERT and ON CONFLICT DO UPDATE" — bearing: supports
- 261117-feat-ws-exec-tools (2026-11-21, commit d4becf20): "Owner sees its permission model as close to exposing ws/exec, so it stays idea until after 261117-feat-ws-exec-tools." — bearing: supports
- 0e35c11a (2026-11-22, commit): "failStart stops the holder synchronously with the job marked stopping and notice off" — bearing: constrains
- 261112-feat-ws-dashboard-slash-commands (2026-11-12, Result 49e0da567): "Only lead sessions report a command list." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | internal/mcp/server_exec_hosted.go, internal/mcp/session_hire.go, internal/wsdashboard/page_routes.go, internal/wsagent/ambient.go, internal/wsagent/runner_conv.go, internal/execjob, cmd/ws-mcp/exec_hosted.go, dashboard assets, internal/wsi18n/tables/ |
| scope.surface | cross-module | new HTTP routes, MCP session tools, runner pending input, execjob launch binding, dashboard assets |
| scope.new_public_symbol | yes | session.register_user_command, session.unregister_user_command, composer-exec routes, typed dashboard launch dependency |
| scope.new_type_contract | yes | typed dashboard launch binding, pending-input source, slot store row and submission claim map |
| scope.test_surface | new-files | existing tests in internal/execjob/hosted_test.go, cmd/ws-mcp/exec_hosted_test.go; new route, store, pending-input and Playwright coverage needed |
| complexity.reuse_points | confirmed | execjob.Hosted.Launch, ShellArgv, OverlayEnv, ResolveWorkingDir (execjob/hosted_launch.go#L478, execjob.go#L647-L651), turnInput seam (wsagent/ambient.go#L67), Hosted.Get/Tail (hosted_ops.go#L178-L187), TrustedWrite (wsdashboard/access.go#L211) |
| complexity.side_effect_risk | high | touches launch fence, completion notice ordering, turn input seam and a new on-disk store without starting or steering turns |
| risk.correctness | high | exit-versus-conversion race, ID visibility before first admission, idempotency claim map and generation staleness |
| risk.fit | moderate | extracts shared prep from callHostedExec and adds a dashboard binding in cmd/ws-mcp; the HostedSessions adapter fits but is not a drop-in |
| risk.test | high | needs Playwright ko/en captures, Windows Git Bash and four harness prompt shapes; Windows tests run only on the remote host |
| risk.security_or_contract | high | browser-triggered command execution behind TrustedWrite, one-use approval, a localhost-only output route, and T0 compatibility to preserve |

## Phases

### Phase 1: Composer `!` exec end to end

Goal: typing `!<line>` in an open hosted lead's chat composer runs it as that
session's ws/exec job and hands the result to the lead passively, with a
private card for the user. Slots are not part of this phase. Phase 2 builds on
this phase's route, launch binding, output file and card.

Scope: the shared launch preparation extracted from `callHostedExec` and the
dashboard binding in `cmd/ws-mcp`; per-launch 10 s wait; the
`composer-exec` route and first-byte grammar; the one-use approval of an
explicit user send; submission-ID idempotency; the passive hand-off source and
its `turnInput` consumption, including the pre-admission ID visibility; output
classification, the Type 5 file and the local artifact route with retention;
the private command card with i18n (`internal/wsi18n/tables/`), refresh and
`copyText`; the fail-closed old-daemon behavior in the new client assets.

Deferred: everything in Phase 2.

Verification boundary:

- Grammar: `!` as the first byte runs; a leading space, or a space before
  `!`, is an ordinary prompt; blank payload after `!` is refused; a
  multiline shell source runs as one script; a submission carrying
  attachments is refused with the draft and attachments retained and the
  attachment consumed exactly once across a failed send (no loss, no
  duplicate).
- Output classification: exactly 4096 raw combined bytes inline and 4097 to a
  file; stderr-only output; invalid UTF-8 and binary output (decoded inline
  safely, byte count taken from raw bytes); high concurrent output; the file
  retained to 1 MiB with explicit truncation; retention by age and total
  size; a missing or expired artifact shown explicitly; the artifact route
  refuses another owner and any browser-supplied path. Confirm the output file
  is readable by an agent in the real harnesses (Codex cutover `fs.read`
  accepts an absolute path but its text reader refuses binary).
- Wait and conversion: a command finishing within 10 s attaches the result; a
  command running past 10 s converts and attaches only the ID; async conversion
  racing exit yields exactly one outcome (never two notices for the same
  job, the ID visible before the first admission); duplicate notice after a
  crash is deduped by event ID; the MCP default stays 30 s (regression).
- No turn side effects: while the lead is idle, a composer run alone starts no
  model request; while the lead is busy it does not steer; the send/held
  counters and `canKick` are untouched. An ordinary later exec completion
  still behaves as today. A failed next send leaves the pending result
  attached to the following accepted send. Cover the four prompt shapes
  (Claude, Codex, Pi, agy including its holder branch) for the receipt text.
- Lifecycle: close/reopen of the lead, worktree release/acquire, hidden/idle
  lead, a stale open generation refused at both launch and receipt
  consumption, and the close cascade stopping a composer job like an
  `exec.shell` job.
- Idempotency: a retry after a lost response with the same submission ID
  returns the same job; two concurrent identical submissions map to one job; a
  fresh submission ID is a new run; after a daemon restart an unknown
  submission shows "outcome unknown — check exec.list" and is never re-run
  automatically, while the job row still shows the real state.
- Pending-input bounds: 17+ receipts and over-16 KiB hand-offs collapse the
  oldest to `job <id>: see exec.list`; the source is dropped when the
  generation closes and is empty after a daemon restart.
- Authority: the route and the `GET .../composer-exec/<submission-id>/output`
  route refuse a LAN or non-`TrustedWrite`
  request, a cross-origin request and a non-open or non-lead session; the
  approval is one-use and does not alter the agent's ordinary exec policy.
- Compatibility: old client against the new daemon (`message` semantics
  unchanged); new client against an old daemon (route unknown, fails closed,
  nothing sent as a prompt, draft retained); a new → old → new cycle covering
  `exec_jobs` rows and the output directory (pending input and receipts are
  in memory only).
- Platform: `GOOS=windows go vet` on the touched packages and a Windows
  Git Bash fallback check (shell selection identical to `exec.shell`).
- UI: a Playwright capture of the private command card in both `ko` and `en`
  locales (submitting, running, done with inline output, done with output
  link, refused, async-converted), plus a check that the card is absent from
  the shared transcript and from a LAN view.
- Performance: the file-operation and latency measurements listed under
  Constraints for the composer paths.

### Result (1321e061b) - 2026-11-22

Landed (develop 02147864c..1321e061b, six commits): 2510c5f03 execjob
per-launch window, `UserApproved`, `ExpectGen`/`CheckGen`, `OnConvert` and
`ShellLaunch` (exec.shell now shares it); f9a112a45 runner pending-input
source consumed at the `turnInput` seam; e0a88cd80 the `composer-exec` route,
claim map, output file store and the ws-mcp binding; 11caef152 the composer
UI, card, JS, CSS and eleven i18n tables; 0bf851713 end-to-end binding tests
over real holders, the latency probe and the `sendMessage` node-fixture stubs;
1321e061b review follow-up (below).

Verification (each run with a narrow `-run`, `-race` where noted):
- execjob composer tests, wsagent passive tests (`-race`), wsdashboard
  composer route/claim/classification/authority/idempotency/async/output-route
  tests (`-race`), `TestComposerExecBrowser` (Playwright, passes), cmd/ws-mcp
  composer binding tests: all pass. The node fixtures that run `sendMessage`
  in isolation (`TestCloseRequestComposerLifecycle`,
  `TestComposerInlineWireAndHelpers`, capsules and the toast stub) pass with
  a `cexecSend` stub.
- `GOOS=windows go vet` on wsagent, wsdashboard, cmd/ws-mcp, execjob: clean.
  The Windows Git Bash run was not done here; shell selection is
  `ShellArgv`, identical to exec.shell.
- Unrelated and environmental: `TestHostedLauncherSurvivesReplacementAndRestartsRecordedBuild`
  in cmd/ws-mcp fails in a fresh worktree because the embedded workflow
  packages lack `generated/inventory.json`; every other cmd/ws-mcp test passes.

T0 compatibility evidence:
- The diff touches no `hosted_store`, proxy/hello, dispatch/tool_arguments,
  wsinbox, wslive, wsholder or tracker file: no `exec_jobs` schema or
  `user_version` change, no inbox kind, hello field or holder shape.
- Composer jobs are ordinary `exec_jobs` rows (tool `exec.shell`), so the
  existing new -> old -> new test `TestHostedExecSurvivesAnOldDaemonAndReopens`
  covers them; the output directory is independent of the job store; pending
  input and receipts are memory only.
- New client assets against an old daemon fail closed (route 404/405, no
  `message` POST, draft kept), asserted in the browser test. Old assets keep
  using `message`, unchanged.

Performance: the route ack, card read and page load touch memory only. The
oversize result's file operations (MkdirAll, CreateTemp, write, close, chmod,
rename, ReadDir/Stat/Remove prune sweep) run on the submission goroutine or
the async finalizer goroutine, never in a request handler and never under the
claim or submission lock. Short sends add no dashboard file writes beyond the
job-row WAL commits exec.shell already makes. Latencies were measured on macOS
only (the probe in `composer_exec_perf_test.go`); a Windows file-touch
baseline could not be measured here and stays unmeasured.

Decisions:
- Cards render in a strip above the composer (`.d-cexec` inside `#composer`)
  rather than in the transcript region: regions are swapped by SSE, so a
  stable DOM host is needed; the card is still absent from the shared
  transcript, asserted in the browser test.
- A LAN viewer typing `!` fails closed: the draft is kept and a hint says to
  start with a space; the route itself refuses non-`TrustedWrite`.
- One-use approval is the in-process `UserApproved` launch field; no tool
  argument can set it and the agent's approval policy is never consulted
  (tests). `CheckGen` is a separate boolean because generation 0 is real.
- `OnConvert` runs before `Notify` is set so the exec id is in the lead's
  pending input before the completion notice can exist; the dashboard's final
  receipt upserts by exec id, so exactly one outcome reaches the lead (a
  real-holder test proves the order `hand` then `notice`).
- Async jobs are finalized lazily when a page polls the card, off the request
  goroutine (review follow-up); the card reads as running until the file lands.

Review (lite, one pass): the node-fixture finding was already fixed at
0bf851713; the finalize-in-GET-handler finding is fixed in 1321e061b, along
with eviction behind an active card, an unreadable retained output (now an
explicit file error, not an empty file) and `</ws-user-exec>` inside output
(escaped in the frame). Left open (minor): the input is cleared when a slow
success response arrives, discarding anything typed meanwhile; async jobs
that no page polls are never finalized (their completion still reaches the
lead as the ordinary exec notice); no test of the end-between-OnConvert-and-
Notify interleaving beyond the unit cases.

Not verified: that a real harness (for example Codex `fs.read`) can read the
output file, which lives under the daemon state directory.

Left for Phase 2: the slot store, the `session.*_user_command` tools, the
user-commands SSE event, slot list/Run/remove routes and the revision gate.
Phase 2 reuses the route, binding, passive hand-off, output file and card.

#### Edition (1e7dfcdc2) - 2026-11-22

Pre-merge review (FIX-FIRST: 3 Critical, 9 Important) found the Phase 1 Result
overstated several things. This Edition corrects the wording of the
Decisions and of the Result above to what the code does after the fixes
(commits 567241777, 60f4e94ca, 76e6157c3, ce20146ad, 1e7dfcdc2);
the frozen text above is not edited.

Corrections to the recovery behavior (Decisions "Submission-ID
idempotency", "Slot store and output directory", the Result's compatibility
and "Decisions" bullets):
- Admission is two-part. The card map holds at most 64 cards per session,
  evicting finished ones. A separate in-memory replay fence holds the newest
  4096 admitted submission ids, independent of the cards. A resend that
  carries `retry` (the browser records its attempt in session storage before
  sending and marks only a resend of an unresolved attempt) never launches: it
  maps to the card, or, when the daemon has no card for it, answers "outcome
  unknown". An id the fence holds without a card also answers unknown. Only a
  fresh submission under an id neither holds launches. After a daemon restart
  both maps are empty: the page shows "outcome unknown", a retry runs nothing,
  and the user's deliberate resend is a fresh submission. Exactly-once across a
  crash is still not promised.
- Reopening output after a daemon replacement: the `composer-exec` directory
  and its files survive a replacement (verified new -> old -> new: an older
  daemon has no reference to the directory, the reopened daemon leaves the
  files intact and its retention still governs them), but the claim map does
  not. The replaced daemon's card and file link are gone: the page shows
  outcome unknown and the link route answers not found. The file stays
  readable by its path (the lead's receipt carries it) until the retention
  removes it. The Decisions' "reopens both" means the directory, not the
  links.
- Authority: every private read and write (list, card, output file, submit)
  asks the live open-hosted-lead check (`Runner.ComposerOpen`, memory only),
  not the index's `Hosted` flag, which a closed lead's registration keeps.
- Generation: the route reads the open lead and its generation before
  acknowledging; the launch carries it; the binding refuses a lead that moved
  on, and `Hosted.Launch` reads the owner's live generation again after the
  job is registered, so a close and revival that completes between the
  owner read and the admission cannot leave a job on a generation nobody asked
  for (tested from generation 0 on both sides).
- Conversion decides once: a job found running at the window is converted for
  good (it owes its notice), its id is published to the lead's pending input
  while the notice is held back, then the notice is admitted; a job found
  ended is done and publishes nothing. A converted run's end is only the
  ordinary notice's, whatever state the job has reached since; the dashboard
  hands only a run that ended inside its window. The converted receipt
  carries the job id only.
- Passive hand-off bound (Decisions' "16 receipts and 16 KiB"): measured on the
  rendered block, with the frame, the closing-tag escaping and the separators
  inside 16 KiB. Whole receipts stay at 16; collapsed id lines are not whole
  receipts, share the byte bound and a 64-entry cap, and the oldest lines
  drop first.
- Output artifact representation: UTF-8 text, an invalid byte shown as U+FFFD
  and a NUL as U+2400 (so the file stays within 1 MiB after the
  substitutions and a harness text reader does not refuse it as binary), the
  newest raw range that fits, with the number of dropped leading raw bytes
  reported. `fs.read` (internal/mcp/server_fs.go) refuses a file with a NUL
  in its first 8192 bytes, scans up to 64 MiB, cuts lines at 512 bytes and
  budgets each reply at 4096 bytes with offset continuation; a test reads the
  artifact of hostile output (invalid UTF-8, NULs, over the cap) with the real
  `fsRead`. Reading through a real harness session was not exercised.
- Retention: one background loop, not the write path. It sweeps at start (after
  a minute), every 30 minutes and when a write asks, expires idle files,
  treats an already-removed file as removed (no over-delete when sweeps
  overlap), and publishes the absence of a file to its card. It also settles
  converted cards and retires a card whose job was pruned, without a page
  poll; card and list reads are memory only.

Performance, measured on macOS (the probes under `WS_COMPOSER_PERF=1`,
`composer_exec_perf_test.go` and `TestComposerExecConversionProbe`; p50 / p95):
- Session page (shared cached render): 3.3 / 5.0 ms with the composer unbound
  and 4.2 / 9.3 ms bound, the same code path (the template carries one
  attribute), so the difference is run order and noise. What the change adds
  to a page load is bytes: dashboard-composer-exec.js is 13.9 KB (4.5 KB
  gzipped), the stylesheet grew 1.2 KB, the send script 0.2 KB, plus the
  20 `js.cexec.*` strings per language table; and one list read, 0.11 / 0.14
  ms, only when the browser remembers cards.
- Acknowledgement of a send: 0.10 / 0.14 ms; a card read 0.09 / 0.12 ms;
  a short send submit to card done 0.19 ms / 10.7 ms (the p95 is the
  probe's own 10 ms poll step, not the daemon); a 1 MiB oversize result submit to card done 11.4 / 13.0 ms
  (one atomic file write); a converted run's end to its card done 21 / 22 ms (the
  settle tick, 20 ms in the probe, 1 s in production); a retention sweep
  of the directory listing 0.07 / 0.14 ms.
- Real hosted conversion: the decision, the id's publication and the
  notice release take 34 us / 106 us; the 114 / 374 ms before the window is
  the holder start every launch pays, and a quick run's whole launch is 245 /
  250 ms.
- File operations (counted by a hook and pinned by
  `TestComposerInteractivePathsTouchNoFiles`): the acknowledgement, card and
  list reads, the page and the settle ticks make none; a short send writes no
  file; an oversize result is mkdir, create-temp, write, close, chmod and
  rename, then the background sweep's readdir and one stat per file; the
  output route opens the file. The shared localhost cookie guard's one stat per
  authenticated request is the common baseline of every route, not the
  composer's. Short sends add no dashboard file write beyond the job-row WAL
  commits exec.shell already makes.
- Pending, lead-owned (needs the Windows host, which workers do not access):
  the Windows file-touch baseline comparison the performance manual asks
  for, and the Windows Git Bash fallback run of the composer shell selection.
  Only `GOOS=windows go vet` of the touched packages was run here.

Review items resolved: C1 replay fence and retry/fresh, C2 generation at the
route and at registration, C3 single conversion outcome, I1 attached state,
I2 acknowledged-revision clearing, I3 live authority and the restore gate,
I4 background settle and snapshot drop, I5 artifact representation, I6
retention loop, I7 rendered hand-off bound, I8 artifact directory cycle and
this wording, I9 measurement (Windows part pending as above).

For Phase 2: the HUD ticket's chip Jump-to-card action should connect to this
private card; the slot Run reuses the same admission (a fresh submission id per
click, `retry` only for an unresolved attempt), the live authority check and
the generation captured at the route.

#### Edition (15ade9428) - 2026-11-22

Round-2 review of the fixes above (FIX-FIRST: 3 Critical, 5 Important, 1
Minor) plus three failures from the Windows host. Commits 4e84b388d, 2389cfcf2,
2dd837ec0, 15ade9428. The frozen text and the Edition above are not edited;
where they differ from what follows, this Edition governs.

Replay (supersedes the admission bullet of the Edition above): a fresh
submission is admitted only when the daemon can prove it was made after
everything the daemon forgot. The page holds an admission token, the daemon
instance's random epoch and the admission count the page last saw, and sends it
with every fresh submission. A token of another epoch (a restart), a missing
token, or one older than the fence's eviction horizon (the count at the newest
id the fence evicted) is refused as "outcome unknown": nothing runs, the
response carries the current token, the draft stays, and a deliberate rerun is
a new submission id with that token. So the unchanged original request replayed
after a restart, or after the 4096-id fence forgot its id, never launches; the
restart test that expected it to launch again was replaced. The token reaches
the page on its private list read at load (and from every submit response)
instead of being rendered into the template, because a rendered page can be
served from a cache while the count part must be current; a fresh submission
that finds no token reads the list first. A retry still never launches.

Conversion (C2): neither the live nor the stored job owes a notice until the id
is published. While `OnConvert` runs the stored snapshot carries no Notify and
no pending notice (a daemon lost in that interval restores a job that owes
none); after it returns the conversion and the notice are stored once.
`OnConvert` now returns an error: a refused passive hand-off is logged and the
ordinary notice, which carries the id, is the single delivery. Tested at the
stored rows, with the job ending inside the publication, for success and
refusal.

Shutdown (C3): composer state (channels, periods, epoch, maps) is built before
the service is stored; admission counts its worker in the wait group under the
lock that sets `closing`, so Close never waits past a request it accepted and
no Add follows the Wait; an arriving request is refused with 503. A launch that
meets a closed drain starts no holder. Tested with an admitted request held
across Close and with a burst racing Close under the race detector.

Client and body (I1, I2): the server decodes the body strictly and refuses any
field other than the submission id, text, retry flag and token, naming
attachments. The browser test now covers refusal followed by a regular send:
the same draft, made an ordinary message, carries its pick once and a second
click sends nothing. An acknowledgement clears the draft only while it is still
the acknowledged revision (only plain appends at the very end of the draft keep
it so), so a replacement that begins with the submitted text and the same text
typed again are left whole, and an append made meanwhile is kept.

Settlement (I3): a wake arms the settle timer only when it is not armed, so a
stream of conversions never postpones a due tick, and each settlement trims
finished cards over the 64-card bound. Tested with a continuous wake stream and
with a burst that goes idle above the bound.

Artifact reading (I4): the receipt of a file result says how to read it: the ws
fs read tool by lines (offset and limit count lines; a line over 512 bytes is
cut), or by byte range through exec (`head -c`, `tail -c`, `tail -c +START`)
for long or single-line output. The real `fsRead` test stays. Reading the path
in a live hosted session through its real harness and permission context is
lead acceptance after the merge; it was not exercised here.

Downgrade cycle (I5), correcting the Edition above: the artifact's new -> old
-> new cycle was not run with an old binary. The wording "verified new -> old
-> new" for the output directory rests on source evidence only, agreed with the
lead: at 02147864c `cmd/ws-mcp/exec_hosted.go` (lines 18-19 and 58-71) opens
only `exec-jobs.db` and `exec-exits`, and the head writes the independent
`composer-exec` directory, so an older daemon has no reference to it. The
runtime evidence is `TestComposerArtifactSurvivesDaemonReplacement` (a new
daemon after a new daemon on the same state directory) and the job rows'
existing `TestHostedExecSurvivesAnOldDaemonAndReopens`.

Minor: the card's replacement note names both substitutions (invalid UTF-8 as
U+FFFD, NUL as U+2400) in all ten languages, as the receipt does.

Windows host findings against efef5eef2, fixed: retention sweeps are serialized
by a mutex and a failed remove whose file is gone or delete-pending (access
denied on Windows) counts as removed, so a lost remove race no longer deletes a
newer file (`TestComposerPruneSweepsRaceWithoutOverDeleting` kept as is); the
0o600 mode assertion is gated to non-Windows (Windows reports 0666); and the
composer shell test has a sibling, `TestComposerExecSelectsItsShellThroughShellArgv`,
that runs portable lines through the shell `execjob.ShellArgv` picks (Git Bash
on Windows) and skips only where exec.shell has no shell. Its Windows run is
lead-owned and pending; here it ran against bash and `GOOS=windows go vet` of
the touched packages is clean.

#### Edition (b6537dfcc) - 2026-11-22

Round-3 review of the round-2 fixes (FIX-FIRST: 3 Critical, 4 Important),
commits 986c24dc3, eff415549, b6537dfcc. The
frozen text and the Editions above are not edited; where they differ from what
follows, this Edition governs.

Replay token (C1, refines the replay bullet of the Edition above): a token is
current only for a count the daemon has issued, from the eviction horizon
through its current sequence, inclusive. A future count is refused as "outcome
unknown" like a stale one, so it can neither admit a request nor stay
admissible after the fence evicts that request's id.

Publication (C2, a lead exception next to decision 2):
notice eligibility stays off during publication; a refused publication falls
back to the ordinary notice as the single delivery. This is a lead exception to
the rule that a notice follows only a successful publication, and the pin is
`TestComposerExecRefusedHandOffLeavesTheOrdinaryNotice`. It holds only because
publication is all-or-nothing: `OnConvert` returns an error only when nothing
reached the owner or the client, and `PassiveHand` checks the generation and
stores the receipt under one hold, so a refusal never leaves both the receipt
and the notice. The contract is stated on `HostedLaunch.OnConvert`.

Shutdown (C3): the background loop is registered under the same mutex as
admission and `composerWait`, and the service is published only while
admission is open, so a Close racing the binding neither waits past the loop's
registration nor lets a closed daemon publish a service. Tested with a Close
before the binding and a Close racing it under the race detector.

Body (I1): after the one JSON object a second decode must return end of input;
a stray delimiter, a second value or garbage is malformed and launches nothing
(`Decoder.More` is a container check and passed them).

Trimming (I2): the card bound is also applied when a submission's own goroutine
finishes its run, so a burst whose every job ends inside its window (no
conversion, no settle tick) trims without another request. Tested with a burst
held unfinished until all of it is admitted.

Client (I3): the first private list read routes a body that fails to read or
parse after good headers into the same completion callback as a transport
failure, so the send clears its busy state and keeps the draft. Covered in the
browser test (a cut-short list body, then a working read).

Windows retention (I4): a removal counts as done only on success, an absent
file, or Windows errno 303 (ERROR_DELETE_PENDING). Access denied (5) and a
sharing violation (32), the answers for a file another handle holds without
share-delete, leave the file present: its size stays counted, its card keeps
its link, and the failure is logged by the sweep. The errno check is a small
per-OS helper (`composer_remove_windows.go` and a stub); the classifier is
unit-tested with synthetic errors in the portable suite, and the 303 case in a
Windows-only test the lead runs on the host. The earlier Lstat-based
`composerGone` and its mapping of permission errors to removed are gone.

### Phase 2: Command slots

Depends on Phase 1 (the route, launch binding, passive hand-off, output file
and card).

Goal: an authenticated hosted lead registers bash lines as slots; the user
sees them as buttons in that lead's chat and a click runs the stored line
through the Phase 1 path as a one-use approval of the displayed revision.

Scope: the Type 2 slot store with its in-memory read model; the MCP
`session.register_user_command` and `session.unregister_user_command` tools
advertised only to an authenticated hosted lead (direct exposure and profile
allowlists wired like the other `session.*` tools, not tied to native exec
cutover); the registration limits (32 per lead, label 64 chars, description
256 chars, line 4 KiB); the localhost-only slot list and Run route; slot
buttons showing label, description and the exact command in dashboard chrome
with i18n, each with a localhost-only remove control calling the same
service as `session.unregister_user_command`; the opaque user-commands
revision event on the existing page SSE stream that makes an open page
refetch its list; the revision gate with stale rejection and refresh; a
click carrying its own submission ID into the Phase 1 idempotent
path.

Deferred: slot registration over tokenless Pi / Claude fallback transports;
project-shared slots.

Verification boundary:

- Store: register, replace (revision advances), withdraw, unchanged `set`
  commits nothing; the in-memory read model serves list and page with no file
  I/O; restart and daemon replacement reload definitions scoped to the lead
  reply ID and worktree; another lead or worktree never sees them. The 33rd
  command and an over-long label, description or line are refused with a
  clear error, while replacing an existing command at the cap succeeds.
- Refresh and removal: register, replace and unregister (by agent or by the
  remove control, including a removal that races a click) push the opaque
  revision event and an open localhost page refetches without polling; the
  remove control is refused for a non-local request.
- Authority and discovery: the `session.*_user_command` tools are listed only for an authenticated
  hosted lead using its own binding and refused for tokenless transports, a
  non-lead, or an arbitrary caller-supplied reply ID; an old daemon refuses
  the unknown tool name.
- Revision gate: a click with a stale revision rejects and refreshes without
  running; a click on a withdrawn slot rejects; concurrent clicks and a
  double click map to one run per submission ID; the executed text equals the
  displayed text.
- Execution: a click follows the Phase 1 contract (10 s window, inline or
  file result, async conversion, passive hand-off, idempotency, close
  cascade) with no turn started or steered.
- Compatibility and platform: the new → old → new cycle for the slot store
  (an old daemon ignores it, the new one restores it); `GOOS=windows go vet`.
- UI: a Playwright capture of the slot buttons and a slot-run card in `ko`
  and `en`.
- Performance: slot registration and list/page-load file-operation counts
  against the Windows baseline.

### Result (bc6fa0448) - 2026-11-23

Landed (develop 1679c7691..bc6fa0448, one commit): the `wsusercmd` registry
and its own `user-commands.db` (held connection, WAL, one commit per changed
row, none for an unchanged definition, memory read model; scope is the lead's
reply ID plus its bound worktree; 32 per scope, label 64, description 256,
line 4 KiB; revisions clock-seeded and never reused across withdrawal and
restart; startup reconcile drops forgotten conversations); the dashboard
routes behind `composerGate` (`GET /s/<id>/user-commands`, `POST .../<slot>/run`
and `.../remove`, strict bodies that never carry command text); the run path
through the Phase 1 composer admission (`composerAdmitSlot`, known-card and
retry answers first, 409 `stale`/`withdrawn` with the fresh list, a card that
shows the slot label); the opaque `usercmd` stream event (local viewers with
a reply ID, on connect and on change); the `session.register_user_command`
and `session.unregister_user_command` tools (trusted binding owner only,
listed only to an open hosted lead, lead-only for keys, in `LeadToolNames`,
the three `runtime.json` files and the Pi owner-lead set); the slot strip UI
and eleven i18n tables; the `ws-mcp.md` store bullet.

Verification (narrow `-run`, no whole-package runs of `internal/mcp` or
`wsdashboard` as a filter-less sweep):
- `wsusercmd` (all, including concurrency and a newer-schema refusal),
  `wsdashboard` user-command route/authority/stale/withdrawn/idempotency/
  stream tests, `mcp` user-command tool tests (listing, lead-only,
  forged owner argument, limits, shipped-text), `cmd/ws-mcp` binding tests
  (scope, 10 s root cache, restart and older-daemon reload), `wsagent`
  `ComposerRoot`: pass. The wsdashboard package also passed in full once.
- `TestUserCommandBrowser` (Playwright; captures left untracked) passes in
  `ko` and `en`. i18n key tests and the asset-source list test pass.
- Pi: bridge and child-tool-inheritance tests pass; two other Pi tests fail
  identically on the base commit (pre-existing).
- `GOOS=windows go vet` on wsusercmd, wsdashboard, wsagent, mcp, cmd/ws-mcp:
  clean. The Windows host run and the Git Bash run are not done here.
- T0 evidence: no change under `internal/execjob`; `exec_jobs` and its
  `user_version` are untouched; `TestUserCommandsReloadAfterARestartAndAnOldDaemon`
  opens the older daemon's stores in the same directory, leaves the slot
  database byte-identical, and a reopen restores every command; an older
  daemon refuses the unknown tool name.
- File operations (`TestUserCommandInteractivePathsTouchNoStore`): list,
  page load, stream connect and click acknowledgement touch the store zero
  times; an unchanged registration commits nothing; a registration, a
  replacement and a removal each make one commit. macOS probe
  (`WS_COMPOSER_PERF=1`): register p50 28 µs, unchanged 0.5 µs, replace 21 µs,
  remove 69 µs, list read 96 µs, session page with and without the binding
  3.2 ms / 3.0 ms, click acknowledgement 94 µs. The worktree root is read from
  the key record, so page reads reuse it for 10 s per open generation; a click,
  a removal and the tools read it fresh. The Windows baseline comparison is
  left to the lead.

Decisions: the browser sends the slot ID and displayed revision, never text,
and the daemon runs the stored line only while its revision matches; the
owner is the trusted conversation binding, never a key or an argument; the
stream event is opaque and the page refetches (no polling); a slot click
reuses the Phase 1 submission, token and card machinery rather than a second
exec path.

Review (lite, one pass): no Critical or Important. Four Minor left unfixed:
the list can show the previous worktree's commands for up to 10 s after a
rebind (a click re-reads fresh, so nothing wrong can run); removal is
check-then-act and skips the revision check when none is sent (the page
always sends it); every local stream gets a `usercmd` event on connect;
`Reconcile` does not notify (it only runs at startup).

Not tested here: LAN stream gating of the event beyond the unit gate, a run
against a real older binary, the Windows host and Git Bash.
