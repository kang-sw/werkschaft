---
title: "대화창 ! 커맨드 실행과 커맨드 슬롯"
related:
  261117-feat-ws-exec-tools: prerequisite, landed (daemon-owned hosted exec service this reuses)
  261117-feat-ws-exec-mux-terminal: neighbouring exec surface (terminal sessions); independent
  261118-feat-ws-exec-intent-filter: neighbouring exec safety layer
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
  per launch: 10 s for composer launches; the MCP default stays 30 s. The
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
- **Passive hand-off, not delivery.** A bounded, runner-owned, one-shot
  pending-input source keyed by reply ID + generation + exec ID. `turnInput`
  (`internal/wsagent/runner_conv.go`) consumes it on the next eligible send,
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
  (starts a turn); system-prompt mutation.
- **Output classification and file.** Classify by raw combined byte count
  (stdout+stderr as the exec result already combines them), never by the
  returned tail length or decoded character count; <= 4096 bytes inline
  (decoded with `DecodeText`), 4097+ a file. The complete retained combined
  range is snapshotted at terminal classification while the job is still
  retained, not rebuilt from the 4 KiB `Tail` reader. A fast oversized result
  becomes a private Type 5 file under daemon state, written off the page and
  send read paths, atomically, and served through a local-only, owner-checked
  artifact-ID route; the browser never supplies a filesystem path. A path is
  shown only once the file exists; truncation and capture failure are shown
  explicitly. Retention: 1 MiB per run (combined stdout+stderr), 7 days and
  100 MiB total, deleting whole oldest files away from interactive reads.
  There is no holder-side capture across daemon replacement: the snapshot is
  ring-bounded only. Rejected: an opt-in holder capture flag and holder-side
  stdout file (a new attach/downgrade surface for a short window);
  stdout-only logs (the exec reply is combined output); per-chunk storage in
  SQLite.
- **Shell and stdin.** Empty stdin for composer runs, so a reader gets EOF
  rather than waiting on an input control the composer lacks. Shell selection
  is identical to `exec.shell` (`execjob.ShellArgv`: bash on POSIX with the
  existing `/bin/sh` fallback, Git Bash on Windows with the existing lookup and
  refusal). cwd defaults to the lead's authorized worktree root as
  `exec.shell` does with no cwd argument; no browser-supplied env, cwd or
  credential.
- **Submission-ID idempotency.** Every typed or slot submission carries a
  submission ID. It is claimed before launch and mapped to exactly one job; a
  retry after a lost response returns that mapping, only a deliberate rerun
  uses a fresh ID, and an unknown receipt (a crash between spawn and durable
  correlation) is reconciled against the existing job row and never
  auto-relaunched. Exactly-once delivery across the backend acceptance/daemon
  crash boundary is not promised; the design is stable IDs plus
  acknowledgement/recovery.
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
  slot ID. They live in the `session.*` namespace beside `session.roster`
  (`internal/mcp/session_hire.go:18,128`, `internal/mcp/server_dispatch.go:239`);
  the owner's "daemon.(same namespace as roster)" referred to this
  namespace, and no `daemon.*` namespace exists. Advertisement rule: only to
  an authenticated hosted lead using its own binding. Rejected:
  `exec.slot.set` / `exec.slot.withdraw` (couples registration to the `exec`
  namespace and to native exec cutover); a `daemon.*` name (no such
  namespace).
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
  cwd field are dropped (a slot has no cwd; it runs in the worktree root). The
  old Phase 2 (browser cwd picking) is removed.
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
  - *Slot store, output directory and submission receipts.* Independent
    stores. An old daemon keeps writing only its explicit `exec_jobs` columns
    and board/session stores and cannot overwrite the new paths; the new
    daemon reopens both and reuses old job rows under their existing
    200-job-cap retention, so a pruned job reports as no longer queryable
    rather than breaking (`cmd/ws-mcp/exec_hosted.go:15–20,53–71`,
    `internal/execjob/hosted_store.go:19–27,59–89,138–156,319–335`).
  - *Pending context and events.* The hand-off adds no inbox entry kind,
    tracker event, hello change or holder attach/event shape; the later
    completion reuses the existing `exec:<id>:exit` event ID. During a
    downgrade the passive hand-off is new-only behavior; an old runner drains
    only its ordinary inbox and leaves any passive record untouched
    (`internal/wsagent/exec_seams.go:370–413`,
    `internal/wsagent/runner_conv.go:1730–1748`,
    `internal/wslive/state.go:71–74,474–488`).

## Constraints

Manuals bound to the paths this ticket edits (AGENTS.md `Implementation
Conventions`); read each before editing a covered file:

- `agents-plugin-tool/` → `ai-docs/manuals/shipped-surface-boundary.md` and
  `ai-docs/manuals/performance.md`.
- `agents-plugin-tool/internal/` → `ai-docs/manuals/release-compatibility.md`
  (T0 target above; run its scenario sheet for the new → old → new cycle).
- `agents-plugin-tool/internal/mcp/` → `ai-docs/manuals/ws-mcp.md` (the new
  `session.register_user_command` / `session.unregister_user_command` tools).
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
  fresh submission ID is a new run; an unknown receipt is surfaced, not
  relaunched.
- Authority: the route and artifact route refuse a LAN or non-`TrustedWrite`
  request, a cross-origin request and a non-open or non-lead session; the
  approval is one-use and does not alter the agent's ordinary exec policy.
- Compatibility: old client against the new daemon (`message` semantics
  unchanged); new client against an old daemon (route unknown, fails closed,
  nothing sent as a prompt, draft retained); a new → old → new cycle covering
  `exec_jobs` rows, the output directory and the pending record.
- Platform: `GOOS=windows go vet` on the touched packages and a Windows
  Git Bash fallback check (shell selection identical to `exec.shell`).
- UI: a Playwright capture of the private command card in both `ko` and `en`
  locales (submitting, running, done with inline output, done with output
  link, refused, async-converted), plus a check that the card is absent from
  the shared transcript and from a LAN view.
- Performance: the file-operation and latency measurements listed under
  Constraints for the composer paths.

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
cutover); the localhost-only slot list and Run
route; slot buttons showing label, description and the exact command in
dashboard chrome with i18n; the revision gate with stale rejection and
refresh; a click carrying its own submission ID into the Phase 1 idempotent
path.

Deferred: slot registration over tokenless Pi / Claude fallback transports;
project-shared slots.

Verification boundary:

- Store: register, replace (revision advances), withdraw, unchanged `set`
  commits nothing; the in-memory read model serves list and page with no file
  I/O; restart and daemon replacement reload definitions scoped to the lead
  reply ID and worktree; another lead or worktree never sees them.
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
