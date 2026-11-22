---
title: "ws/cron: 주기 예약 프롬프트 도구"
related:
  261121-refactor-ws-daemon-metadata-sqlite: "daemon metadata SQLite bundle; cron keeps its own small database instead of joining it (Decision 15)"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: ef56a4a38cfc41b3
sage-review-completeness-reviewed: ef56a4a38cfc41b3
---

# ws/cron: scheduled prompt tool

## Background

The owner asked for a `ws/cron` tool: a lead registers a prompt with a Linux
cron expression, and the dashboard daemon sends that prompt to the lead's own
session each time the expression matches. Today a lead can only react to input
that arrives; it cannot ask to be woken at a time. Typical uses are a periodic
board check, a nightly review sweep, or a reminder to re-run verification.

The daemon already owns everything the feature needs: the authenticated
binding from a hosted connection to its conversation, a per-conversation
inbox that a running turn does not interrupt, hosted-session lifecycle, and a
SQLite store precedent. What is missing is a schedule parser, a daemon-held
scheduler, and the delivery envelope.

## Decisions

Decisions 1-14 were settled with the owner (schema and timezone rule) and the
owner's lead (2026-11-22). Decisions 15-21 are technical consequences the
drafting research forced; each names what it was chosen over.

1. **Tool shape.** One MCP tool `cron`, in both `ws` and `wsflow`, with
   mutually exclusive arguments:
   - no arguments: list the caller's schedules, each as
     `{id, name, expr, tz, next_at, last_at, pending, dropped}`;
   - `add: {name, expr, prompt, tz?}`: returns `{id, next_at}`;
   - `delete: "<id or name>"`: returns ok.
   Both `add` and `delete` is an error. Default output is compact text;
   `format: "json"` is the opt-in machine form (ws-mcp manual, Tool Output
   Convention). **Rejected:** separate `cron.add` / `cron.delete` /
   `cron.list` tools (the owner specified one command); `once`, `max_runs`,
   `until` and `paused` fields in v1.
2. **Delete by id or name.** `delete` accepts either the id or the name; names
   are unique per session, so a name resolves to at most one of the caller's
   schedules.
3. **No overwrite.** `add` with a name the caller session already uses is
   refused. To change a schedule: delete, then add.
4. **Expression.** Standard 5-field Linux (Vixie) cron: minute, hour,
   day-of-month, month, day-of-week. Supports lists, ranges, steps, `*`, month
   and day names such as `MON` / `JAN` (case-insensitive, not as a step value),
   `0` and `7` both Sunday, and the day-of-month / day-of-week OR rule when both
   are restricted. A field beginning with `*` (including `*/n`) counts as
   unrestricted, as in Vixie cron. Macros: `@yearly`, `@annually`,
   `@monthly`, `@weekly`, `@daily`, `@midnight`, `@hourly`. `@reboot` and a
   seconds field (6 or 7 fields) are refused. An expression that can never
   fire (for example Feb 30) is refused at `add`. `go.mod` carries no cron
   parser (verified against its `require` blocks; the offline module cache
   holds a third-party cron module that `go.mod` does not require), so the
   parser is written in the repository with table tests; no new dependency.
   **Rejected:** a 6-field form with seconds, which is not Linux cron.
5. **Timezone.** `add` takes an optional `tz` field: `UTC` or `Z`, or a fixed
   offset `+HH:MM` / `-HH:MM`, the same style as the chat timestamps (the
   dashboard user-message parser accepts offsets from -12:00 to +14:00,
   `wsinbox/user_message.go`). The expression's fields are evaluated in that
   offset. When omitted, `tz` is UTC, so behavior is deterministic and
   independent of the daemon host's zone. List output, `next_at`, `last_at`
   and the envelope's `at` are RFC 3339 in the schedule's own offset.
   **Rejected:** IANA zone names in v1 (no DST handling); an inline zone
   prefix inside `expr` (`tz` is a separate field only); the daemon's local
   zone as the default.
6. **Target.** Always the calling session, identified from the connection's
   authenticated binding: the per-session token the daemon resolves to
   `ProxyHello.Conversation`, carried into the server as
   `ServerOptions.Conversation` (`Server.conversation`). It is never taken
   from launcher-supplied environment (`WS_HOSTED_SESSION_KEY` and friends)
   and never from a `session_key` argument, which grants a root and not
   ownership (the rule the hosted exec tools follow). No other-session
   targeting. Delivery is a user-turn prompt through the runner's inbox path
   that dashboard user messages use.
7. **Availability.** v1 is hosted-lead-only (`Record.Lead`): the daemon must
   be able to inject a prompt into the caller. Workers (`agent.spawn` children)
   and any caller the daemon cannot deliver to (an unhosted plain CLI session,
   a launcher connection) are refused at `add` with a clear error. Children
   sleep, expire and hand off holders, which adds moving parts; extending to
   them is future scope. **Rejected:** every daemon-held session in v1.
8. **Busy and coalescing.** A fire to a mid-turn target queues as the next
   turn's input; there is no steer into a running turn. At most one pending,
   undelivered fire exists per schedule. An overlapping fire is dropped and
   counted; the count is `dropped` in the list. A schedule is pending exactly
   while its cron entry is held in the target's inbox; every way the entry
   leaves the inbox (the turn-start drain, a cancel, the drop of held entries
   for a conversation that takes no more turns) clears it.
9. **Missed fires.** Fires missed while the daemon was down, replaced, or the
   machine slept are skipped, not caught up, as in cron. The next fire is
   computed from now. **Rejected:** anacron-style catch-up.
10. **Persistence.** Schedules survive daemon restart and replacement. The data
    is Small mutable state (performance manual, Storage Types type 2), so the
    store is SQLite. Live schedule state and timers are in memory. The fire path
    does no file I/O beyond the existing inbox delivery. Persisted: only add,
    delete and last-fire deltas. File operations per action are counted
    (Phase 2 verification).
11. **Lifetime.** A schedule is deleted when its target session ends
    permanently: the session is deleted or expired. A temporarily disconnected
    or restarting hosted session keeps its schedules.
12. **Limits.** 32 schedules per session; prompt at most 16 KiB; name
    non-empty and unique per session (Decision 3).
13. **Envelope.** The delivered prompt is wrapped
    `<ws:cron id="..." name="..." at="...">…</ws:cron>` so the receiver knows
    it is a scheduled prompt and which one, alongside the existing `<ws:user>`
    family. `at` is the matched cron minute, RFC 3339 in the schedule's offset.
14. **Compatibility and scope.** The new MCP tool and the new storage are T0
    (additive; old binaries lack the tool, never open the file, and the
    dispatcher refuses an unknown tool without a write). The inbox entry marker
    Decision 16 needs is T1, the highest tier of the change, which is still
    patch-allowed; the evidence is in Constraints. It differs from the
    optional-field T0 of `261115-feat-ws-dashboard-user-message-envelope`
    because that change kept the drain text shape unchanged, while here an
    older reader renders the cron entry under a different envelope name. No `/mcp` hello or tracker
    change. No dashboard UI in v1. Shipped text is tool description only: no
    playbook or skill text changes; no config key, so no manifest or dashboard
    settings change.
15. **Storage file.** A dedicated small SQLite database in the dashboard state
    directory, opened and held by the daemon beside the hosted exec store
    (`PRAGMA user_version`, a newer version refused, WAL, one connection), over
    adding tables to the `wsmeta` daemon metadata store. **Rejected:** `wsmeta`,
    because it is not opened by any production caller yet (its only importers
    are tests; `261121-refactor-ws-daemon-metadata-sqlite` Phase 2 owns the
    cutover), it opens in exclusive locking mode with import fences, and
    joining it would couple this tool to that cutover. A later child may fold
    the file into `wsmeta` as a table move.
16. **Envelope carriage.** The inbox `Entry` gains an optional cron marker
    (schedule id, name, matched minute with its offset); `Entry.Text()` renders
    `<ws:cron>` instead of `<ws:user>` for it, escaping the prompt body's
    closing delimiter as `escapeUserBody` does for `<ws:user>`, and escaping
    the attribute values. A cron entry is never merged with another and is not
    steered: the inbox steer admission refuses a cron-marked entry, so a
    dashboard steer cannot turn it into a mid-turn injection. **Rejected:** wrapping a pre-built `<ws:cron>` string in `Parts`,
    which would nest it inside a `<ws:user>` envelope.
17. **Identity declared, not used.** The tool declares an optional
    `session_key` like the other lead tools, so a caller that passes it per
    the usual convention is not refused by the argument precheck, but the value
    never selects the owner. **Rejected:** omitting `session_key` from the
    schema, which refuses callers who follow the ws-wide convention of passing
    it.
18. **Stale timers are missed fires.** A timer that fires more than one cron
    minute after its scheduled minute (a slept machine, a stalled daemon) is
    treated as missed under Decision 9: nothing is delivered and the next fire
    is computed from now. **Rejected:** delivering a late fire, which is
    catch-up by another name.
19. **Closed target.** A fire the runner refuses because the target is closed,
    handed off or no longer known is dropped, counted in `dropped`, and logged once; the
    schedule stays until Decision 11 deletes it. **Rejected:** deleting on
    close, which Decision 11 reserves for permanent session end.
20. **Name and id.** A name is a non-blank string of at most 64 runes with no
    control characters, because it is an envelope attribute and a list column.
    Ids have the fixed form `cron-<hex>`, and a name of that form is refused,
    so Decision 2's id-or-name lookup is never ambiguous. **Rejected:** an
    unbounded name; resolving an ambiguity by precedence.
21. **Offset parsing.** `tz` is normalized on add (`Z`, `z` and `utc` become
    `UTC`; `+00:00` stays an offset) and stored and listed in that normalized
    form; minutes must be 00-59 and the offset within -12:00 to +14:00.
    **Rejected:** storing the caller's spelling, which makes equal zones list
    differently.

## Constraints

Rows from `AGENTS.md` Implementation Conventions that match the paths this
ticket touches (`agents-plugin-tool/`, `agents-plugin/runtime.json`,
`agents-plugin-wsflow/runtime.json`):

| manual | applies because |
|---|---|
| `ai-docs/manuals/shipped-surface-boundary.md` | The tool description and every string the tool emits (envelope, refusals, list text) ship to downstream projects; they must not name this repository's tickets, commits, paths or tooling. The `agents-plugin/` and `agents-plugin-wsflow/` `runtime.json` edits are under the same row. |
| `ai-docs/manuals/ws-mcp.md` | New tool in `agents-plugin-tool/internal/mcp/`: compact text by default, `format: "json"` opt-in; `runtime.json` is active compatibility data. |
| `ai-docs/manuals/release-compatibility.md` | New tool, new database file, new inbox entry field under `agents-plugin-tool/internal/`. Tiers in Decision 14; evidence below. |
| `ai-docs/manuals/performance.md` | New persisted data (type 2, SQLite), a timer-driven fire path and an interactive-adjacent delivery. Rules 1, 2, 4, 5 and `## Measuring` apply. |
| `ai-docs/manuals/wsflow-mirroring.md` | The change edits `agents-plugin-wsflow/runtime.json`. The wsflow tool set must match ws; wsflow distributed text stays non-ws-aware (the tool description is shared, so it names no `ws/` form). |

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin-wsflow/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Out of scope by decision: dashboard UI, children, catch-up, config keys, IANA
  zones, any playbook or skill text, `once` / `max_runs` / `until` / `paused`.
- `config-settings-ui.md` does not apply: no config key, manifest field or
  dashboard settings asset changes.
- **Performance contract.** Schedules and the next-fire timer live in memory;
  one timer is armed for the earliest next fire across all schedules and is
  re-armed after each fire, add and delete. The fire path reads and writes no
  file except the inbox entry and the conversation record write the existing
  delivery path performs for its send counters (as `Runner.AdmitCompletion`
  does); the scheduler itself touches no file on it. SQLite is
  touched by `add`, `delete`, a last-fire delta and daemon start (one read of
  all rows) only. The database is opened once and held (Rule 4).
- **Compatibility evidence** (Go paths relative to
  `agents-plugin-tool/internal/`). The store open path to copy is
  `execjob/hosted_store.go` `OpenHostedStore` and `hostedStoreMigrate`
  (`PRAGMA user_version`, newer version refused with `ErrHostedStoreNewer`),
  opened by `cmd/ws-mcp/exec_hosted.go` `openHostedExec` with the file name in
  `hostedExecStoreName`. A new database file is read by no older binary, so an
  older daemon cannot misread or overwrite it. The dispatcher refuses an
  unknown tool at `mcp/server_dispatch.go#L1633` (the default case; the
  release-compatibility manual's `:1662` citation is stale) and the argument
  precheck refuses undeclared arguments in `precheckToolArguments`,
  `mcp/tool_arguments.go#L84-L155` (the manual's `:65-128` is stale), neither
  writes. The inbox
  `Entry` is a JSON struct with `SchemaVersion = 1` (`wsinbox/inbox.go`
  `Entry`); `wsinbox.normalize` rebuilds an `Entry` field by field
  (`wsinbox/normalize.go:32`), so a field that is not added there is silently
  dropped on the sender's own path; an old reader drops the unknown field and
  renders `<ws:user>` (`wsinbox/user_message.go:39`), which is the T1 display
  degradation.
- Hosted conversations of every kind, workers included, get a per-session
  token (`wsagent/mcp_session.go` `attachMCP`), so `Server.conversation` being
  non-empty does not mean "lead"; the lead check reads the runner's record
  (`Record.Lead`, `wsagent/record.go:65`), as `Runner.ExecOwner` does.
- Delivery seam precedent: `Runner.AdmitCompletion` (`wsagent/exec_seams.go`)
  admits a system notice to one conversation's inbox once, with a closed
  target suppressed; the cron seam follows its shape but is not steered.

## Prior Art

- `execjob.Hosted` and `cmd/ws-mcp/exec_hosted.go`: a daemon-held component
  with its own SQLite file, an owner-session seam over the runner
  (`execSessions`), and a server option read at each call
  (`ServerOptions.Exec func() *execjob.Hosted`, `Server.execJobs`).
  `Server.hostedExec` is the "trusted binding present" test and
  `errNoHostedExec` the refusal wording to imitate.
- `wsinbox.Entry`, `wsinbox.Send` and `escapeUserBody`: the envelope family
  and its body escaping.
- `wsagent.Runner.Send` (`wsagent/runner_operations.go`): an ordinary input
  is held until the running turn ends for non-Pi backends, which is Decision 8's
  queue behavior.
- Hosted lifecycle ends: `Runner.ForgetClosed` (the rail worktree-delete purge;
  the path a lead's permanent end takes, and it does not notify the daemon),
  the index's `ForgetSession` forwarding (`wsdashboard/daemon_runtime.go`),
  `Runner.forget` (reached only from `Runner.expire`, child expiry, which
  leads never take) and `wsdashboard.PruneRegistrations` (removes an ended
  session's directory after `RegistryRetention` without telling the runner).

## Prior Decisions

- 0100ef97 (2026-11-22, commit): "Trusted binding (Decision 13): ProxyHello.Conversation is json:\"-\" and only Runner.ResolveMCPToken sets it ... a supplied session_key stays a cwd grant only." — bearing: supports
- 0100ef97 (2026-11-22, commit): "Completion admission (Decisions 10, 21): AdmitCompletion writes one Steer=true inbox entry carrying EventID under c.wmu+c.mu, checking closed and Record.OpenGen in the same hold" — bearing: supports
- 261117-feat-ws-exec-tools (2026-11-22, Result): hosted exec jobs landed with their own store, owner-session seams and daemon wiring in cmd/ws-mcp/dashboard.go — bearing: supports
- 10b1c65f (2026-11-22, commit): "mailbox.recipients joins wsagent.ChildSafeWSTools (so AgentWSTools) and the three runtime.json manifests; the pi copy stays byte-identical to agents-plugin's." — bearing: constrains
- 1d089568 (2026-11-22, commit): "internal/mcp/process_env.go (ServerOptions): union of the branch's Exec/Conversation fields and develop's MailGroups/MailLabels fields" — bearing: supports
- 261115-feat-ws-dashboard-user-message-envelope (2026-11-15, Decisions): "Compatibility tier T0 (technical, acked): optional additive request and inbox fields, an unchanged drain text shape, ... Normalize and every inbox rewrite copy the new field" — bearing: contradiction-candidate
- 697c9508 (2026-11-15, commit): "Build the envelope only in Entry.Text so MCP drains, Pi, Claude and hosted delivery share one serializer without double wrapping" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | new wscron package; wsinbox/inbox.go, wsinbox/normalize.go, wsinbox/user_message.go; wsagent/ runner seam; mcp/server_schemas.go, server_dispatch.go; cmd/ws-mcp/dashboard.go; three runtime.json files |
| scope.surface | cross-module | tool surface, inbox Entry field, runner seam and daemon wiring span wsinbox, wsagent, mcp, cmd/ws-mcp |
| scope.new_public_symbol | yes | cron MCP tool, wscron package, wsinbox.Entry cron marker, Runner delivery method |
| scope.new_type_contract | yes | wsinbox.Entry cron marker field; scheduler-to-runner interface; SQLite schedule schema |
| scope.test_surface | new-files | no agents-plugin-tool/internal/wscron directory exists; scheduler, store and tool tests are new; existing wsinbox and wsagent tests extend |
| complexity.reuse_points | confirmed | execjob/hosted_store.go OpenHostedStore, wsagent/exec_seams.go AdmitCompletion and ExecOwner, mcp/server_exec_hosted.go hostedExec read |
| complexity.side_effect_risk | high | touches the inbox Entry shared by every delivery path, the runner conversation drain, and daemon startup/shutdown |
| risk.correctness | high | timer-driven fire path, coalescing and pending-clear at drain, cron semantics and offset handling |
| risk.fit | moderate | follows the hosted exec precedent, but Entry.Text and normalize are shared by all adapters |
| risk.test | moderate | fake clock and fake runner make most behavior testable; the drain callback and restart paths need integration coverage |
| risk.security_or_contract | moderate | per-session identity from the trusted binding, envelope escaping against forgery, T1 inbox field compatibility |

## Phases

### Phase 1: Cron expression parser and next-fire computation

Add a small package (suggested name `wscron`) that parses an expression and
answers "the next matching minute strictly after `t`" for a fixed offset
(`*time.Location` from `time.FixedZone`), plus a `tz` parser (Decisions 5, 21).
No runtime wiring.

- Grammar and semantics exactly as Decision 4. Parse errors name the field and
  the offending token. The macros expand to their 5-field forms (`@weekly` is
  `0 0 * * 0`, `@monthly` `0 0 1 * *`, `@yearly` and `@annually`
  `0 0 1 1 *`, `@daily` and `@midnight` `0 0 * * *`, `@hourly` `0 * * * *`).
  Whitespace between fields is any run of spaces or tabs; the stored `expr`
  text is the caller's, trimmed.
- Next-fire search walks days then fields, not minutes, and gives up after a
  horizon of 10 years: a match-less expression returns "never matches", which
  `add` refuses. `0 0 29 2 *` must succeed from any date, including just after
  a century non-leap year.
- A fixed offset has no DST, so every wall-clock minute of the offset exists
  exactly once.
- `tz` parsing: `UTC`, `Z` (case-insensitive) or `±HH:MM` within -12:00 to
  +14:00; an IANA name, a bare `+9`, a seconds component or an out-of-range
  value is refused with a message listing the accepted forms.
- Verification: table tests, pure and clock-free: each Decision 4 feature, the
  dom/dow OR rule versus AND when one field is `*` or `*/n`, `7` as Sunday
  including in a range such as `5-7`, names, steps over ranges (`1-10/3`),
  every macro, each refusal (`@reboot`, 6 fields, empty, out-of-range,
  `*/0`, reversed range, a name as a step), the never-matching refusal
  (`0 0 30 2 *`), and next-fire cases across a month end, a year end and a
  leap day, each in UTC, `+09:00` and `-05:30` so the same instant yields
  different local matches; `tz` accepted and refused forms. A fuzz or property
  check that `Next(t)` is strictly after `t` and matches the parsed fields.
- Deferred: the scheduler, store and any wiring (Phase 2).

### Phase 2: Scheduler, store, delivery and lifecycle

Depends on Phase 1. Add the daemon-held scheduler with an injectable clock
and timer, its SQLite store (Decision 15), the inbox cron marker and
envelope (Decision 16), the runner delivery seam, and the lifecycle
reconciliation. No MCP tool yet; tests drive the scheduler directly.

- **Scheduler.** In-memory schedules keyed by id and grouped by owning
  conversation id; one timer armed for the earliest next fire, re-armed after
  each fire, add and delete. The clock and the timer factory are injected so a
  fake clock drives every test. Add enforces Decisions 3, 12 and 20 (unique
  name per session, 32 per session, 16 KiB prompt, name rules, never-matching
  refusal) and returns an opaque id unique across the daemon plus `next_at`.
  Delete takes an id or a name and refuses one the caller does not own with the
  same "unknown schedule" answer as a missing one (no existence leak across
  sessions). List returns the caller's schedules in the Decision 1 shape.
- **Fire path.** On a timer fire: if the fire is more than one cron minute
  late, skip it (Decision 18); otherwise, for each schedule matching the
  minute, drop and count it when a fire is pending (Decision 8), else mark it
  pending and then hand it to the runner seam, rolling the mark back when the
  seam reports suppressed or an error. The mark is set before the hand-off,
  under the lock the pending-clear callback also takes, because an idle
  lead's loop can drain the entry before the seam returns; a late mark would
  never clear. Compute the next fire from now. Set
  last fire in memory and persist only the last-fire delta, off the delivery
  path (a coalesced write after delivery returns). No file read happens on this
  path.
- **Runner seam.** A runner method in the style of `Runner.AdmitCompletion`
  delivers one cron entry to a conversation's inbox and returns an outcome:
  accepted, or suppressed because the target is closed, handed off or not
  known (Decision 19). It writes the inbox entry as a non-steered `Entry` carrying
  the cron marker, bumps the same send counters `Runner.send` bumps (persisting the record as
  `AdmitCompletion` does), and wakes
  the conversation loop the way `Runner.send` does. The scheduler reaches the
  runner through a small interface, as `execjob.HostedOwner` / `execSessions`
  do, so tests use a fake.
- **Pending clears when the entry leaves the inbox.** The pending flag clears
  on every removal of a held cron entry, reported to the scheduler through a
  runner callback: the turn-start drain (`kick` in `wsagent/runner_conv.go`),
  a cancel of the held entry (`wsinbox.Cancel`), and `conv.dropHeld` for a
  conversation that takes no more turns. The implementer derives the signal
  from the inbox removal points (or from the entry's presence in the inbox)
  rather than from the drain alone, so no path leaves a schedule pending
  forever. After a daemon start, pending is rebuilt once from cron entries
  still held in the conversation inboxes; it is never persisted by the
  scheduler. The steer admission (`wsinbox.Steer` / `SteerWithAdmission`)
  refuses a cron-marked entry (Decision 16).
- **Envelope.** `<ws:cron id="..." name="..." at="...">` with the prompt body
  on its own lines inside (Decision 13). Attribute values and the body's
  `</ws:cron` delimiter are escaped so a prompt or name cannot close or forge
  the envelope. Update `wsinbox/normalize.go` so the marker survives
  normalization, and keep `ParseUserMessages` (dashboard transcript parse) from
  rejecting a turn that contains a cron envelope. Any shipped text that says
  adapters inject "each `ws:user` envelope" (the dashboard inbox drain tool
  description in `mcp/server_schemas.go`) is updated to cover the cron
  envelope.
- **Store.** One table of schedules (id, owner conversation id, name, expr, tz,
  prompt, created at, last fire) in the new database (Decision 15). Start loads
  all rows into memory once; after that only `add` inserts, `delete` removes and
  a last-fire delta updates. Newer `user_version` refused, matching
  `OpenHostedStore`; a store that cannot open is logged and leaves the tool
  refusing with an "unavailable" message, never a crash.
- **Lifecycle (Decision 11).** A schedule is deleted when its conversation is
  gone permanently. The hooks are the runner's permanent-removal path for a lead
  (`Runner.ForgetClosed`, plus `Runner.forget` for completeness) and the
  index's session-forget path (`ForgetSession`), each delivering a
  conversation-gone notification to the scheduler. Index removal alone is
  not permanence (an unmounted project or an identity mismatch also drops a
  session from the index), so the `ForgetSession` handler deletes only after
  confirming the runner no longer knows the conversation (neither open nor
  in its closed set), the same test the startup reconciliation uses. That
  reconciliation runs at daemon start that deletes rows whose conversation the runner does not know,
  which covers `PruneRegistrations` removing an ended session's directory
  behind the runner's back. A fire whose conversation is already unknown is
  dropped (Decision 19) and does not wait for a restart to be reaped. A
  disconnect, a daemon replacement or a holder restart deletes nothing.
- **Verification.**
  - Fake-clock fire: advance the fake clock to a matching minute and observe
    one `<ws:cron>` entry in a fake hosted lead's inbox with the right id,
    name and `at` (in the schedule's offset); no delivery before the minute;
    the next fire is the next match. A `+09:00` schedule fires at the
    corresponding UTC instant, not the UTC wall minute.
  - Busy and coalescing: with the lead mid-turn (entry held undrained), a
    second matching fire is dropped and `dropped` rises to 1; once the turn
    drains the entry, `pending` clears and the next fire delivers; two
    different schedules at the same minute both deliver.
  - Missed fires: a clock jump past a scheduled minute delivers nothing and
    computes the next fire from now; a daemon restart across a match minute
    delivers nothing for it.
  - Persistence: add two schedules, close the scheduler and store, open a new
    pair over the same file, and observe the same list with `last_at` and `tz`
    retained and timers armed; a database stamped with a newer `user_version`
    is refused unchanged.
  - Idle lead: with a lead that drains the entry during delivery (before the
    seam returns), `pending` ends false and the next fire delivers; a
    suppressed delivery leaves `pending` false.
  - Pending clearing: a held cron entry that is cancelled, or dropped by the
    held-entry drop for a conversation taking no more turns, clears `pending`
    so the next fire delivers; a dashboard steer of a held cron entry is
    refused and the entry stays non-steered.
  - Pending rebuild: with a cron entry still held undrained in a conversation
    inbox, a new scheduler started over the same store lists that schedule as
    `pending`, and a matching fire is dropped and counted; with no held entry
    it lists `pending` false.
  - Deletion on session end: the `ForgetClosed` and `ForgetSession` paths
    each remove that conversation's schedules and no others; an index drop of
    a session the runner still knows deletes nothing; a startup reconciliation removes rows of an
    unknown conversation; a disconnect and a restart keep them.
  - Limits and refusals: duplicate name, the 33rd schedule, a 16 KiB + 1
    prompt, a never-matching expression, an id-shaped name, deleting another
    session's id or name.
  - Closed target: a suppressed delivery is dropped, counted, and the
    schedule remains.
  - Envelope: a prompt containing `</ws:cron>` and a name containing `"`
    round-trip without forging or closing the envelope; a held cron entry is
    not merged with a user entry; `normalize` keeps the marker.
  - Performance: a counting test double or file-activity trace shows the fire
    path touches no file but the inbox entry write and the record write the send
    counters cause; record the file-operation count per fire (naming those two
    writes), add and delete next to the measured action (performance manual,
    `## Measuring`).
- Deferred: the MCP tool and daemon wiring (Phase 3); dashboard display of
  schedules.

### Phase 3: `cron` MCP tool, daemon wiring and tool-surface parity

Depends on Phase 2. Expose the scheduler as the `cron` tool and wire it into
the daemon.

- **Tool.** Register `cron` in the tool registry (`mcp/server_schemas.go`
  `tools()`), dispatch it in `mcp/server_dispatch.go`, and enforce the
  mutually exclusive-arguments rule. Schema: `add` (object: `name`, `expr`,
  `prompt` required, `tz` optional), `delete` (string, id or name), optional
  `session_key` (Decision 17) and `format`. Both `add` and `delete` is an error;
  neither lists. The description is tool-description text that passes the
  shipped-surface-boundary test: it states the 5-field syntax and macros, the
  UTC default and the accepted `tz` forms, the coalescing and no-catch-up
  rules, the limits, and the lead-only rule, with no repository-specific
  references and no `ws/` spelling that wsflow would have to rewrite. A refusal
  of a bad expression or `tz` names the field.
- **Gate.** The call requires a hosted connection (`Server.conversation`
  non-empty and a daemon scheduler present) and a runner record with `Lead`
  set. An unhosted or launcher connection, a worker, and a daemon without a
  usable store each get a distinct refusal (modeled on `errNoHostedExec`) and
  write nothing. Mechanism: like the hosted exec tools, `cron` is listed
  wherever daemon tools are listed and refused at call by the
  `Server.conversation` plus runner-record check, never by `session_key` role
  scope (Decision 17). Because `AgentWSTools` is built from
  `PiMiniLeadWSTools` and is the list every spawned child gets, the
  implementer either keeps `cron` out of the child list explicitly or relies
  on the call-time refusal, and the inventory tests state which: a lead's
  inventory lists `cron`, `ChildSafeWSTools` excludes it, and a worker's call
  is refused. The agy `DirectTools` list (`wsagent/agy.go`) is part of the
  checklist: an agy lead either gets `cron` as a direct tool or reaches it
  through the `call` fallback, and the choice is tested.
- **Daemon wiring.** Construct the scheduler beside the hosted exec jobs in
  `cmd/ws-mcp/dashboard.go` (open the database under the daemon lock, close it
  on shutdown after draining), bind it to the runner, and expose it to the
  server as a call-time option like `ServerOptions.Exec`. The surfaces that
  changed in the commit that added `mailbox.recipients` are the checklist:
  `cmd/ws-mcp/dashboard.go`, `cmd/ws-mcp/hosted_cli.go`, `cmd/ws-mcp/proxy.go`,
  `mcp/server.go`, `mcp/process_env.go`, `mcp/server_dispatch.go`,
  `mcp/server_schemas.go`, `wsagent/builtin.go`, `wsagent/agy.go`, and the `tools` map of
  `agents-plugin/runtime.json`, `agents-plugin-wsflow/runtime.json` and
  `agents-plugin-pi/runtime.json`. Confirm `ws` and `wsflow` list the same
  tool set.
- **Verification.**
  - Through the MCP tool on a hosted lead connection: `add` returns `{id,
    next_at}`; list shows it with `tz`, `next_at`, `pending` false and
    `dropped` 0; `delete` by id and, separately, by name removes it and a
    second `delete` is refused; a duplicate name is refused; both arguments
    is an error; the default output is compact text and `format: "json"`
    returns the structured form; omitted `tz` lists as `UTC`.
  - Fake-clock fire end to end: add through the tool, advance the clock,
    observe the `<ws:cron>` entry in the hosted lead's inbox.
  - Refusals: a worker (spawned child) connection, a launcher / CLI
    connection with no binding, a connection whose session token resolves to a
    non-lead record, and a daemon whose store failed to open; each refused
    with its own message and no row written. A hosted lead passing another
    session's `session_key` cannot add to that session.
  - Persistence across a daemon restart through the tool surface: add, restart
    the daemon harness, list shows the schedule and a fire after the restart
    delivers.
  - Deletion on session end through the tool surface: purge the lead's
    conversation through `ForgetClosed`, observe its schedules gone from the store and the list
    refused for the dead session.
  - Parity: the tool-inventory tests include `cron` for a lead and, per the
    chosen mechanism, omit it for a worker or refuse the worker's call; `ChildSafeWSTools` excludes it (the existing exclusion test
    lists it); the three `runtime.json` files list it and ws and wsflow list
    the same tool set; a tool-description check confirms no repository-specific
    text.
  - `go build ./...`, `go vet ./...` and the touched packages' tests pass; the
    wsflow package tests pass.
- Deferred: dashboard UI for schedules, worker support, catch-up, config keys,
  IANA zones.
