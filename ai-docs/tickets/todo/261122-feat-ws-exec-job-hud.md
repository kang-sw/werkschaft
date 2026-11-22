---
title: "ws/exec 실행 작업 HUD: 채팅창 위 뱃지, 출력 peek, 사용자 중지"
related:
  261121-feat-ws-board-command-slots: prerequisite (Phase 1) - composer `!` route, submission->job mapping, private command card and pending-input seam this HUD reuses
  261117-feat-ws-exec-tools: landed hosted exec service (Hosted jobs, ring, stop) this HUD reads and controls
---

# ws/exec job HUD: composer-gap badges, output peek, user stop

## Background

Hosted ws/exec jobs (agent-launched `exec.shell` / `exec.spawn`, and the
user's composer `!` commands from 261121) run invisibly from the user's point
of view: the rail shows only a running count, and the user has no way to see
what is running, read its output, or stop it without asking the agent. This
ticket adds a job HUD to the chat page: compact chips for the open lead's own
jobs in the composer gap, a hover tooltip, an upward output "peek" panel, and
a user Stop.

The visual design was iterated as a text mockup and approved by the owner on
2026-11-22: [index.html](../../assets/mockups/261122/exec-job-hud/index.html)
(with `exec-hud.css`, `exec-hud.js`, `exec-hud-gallery.js` and the generated
`fragment.html` beside it under
`ai-docs/assets/mockups/261122/exec-job-hud/`). It is the approved visual
reference: Phase 2 verification compares Playwright captures against it side
by side. Where this ticket and the mockup disagree on layout priority, this
ticket's Decision 1 wins (the mockup let the subagent badge shrink; it must
not).

Source refs below are relative to `agents-plugin-tool/` at develop
`daf510d17` and are evidence, not edit targets. The mockup's mock data uses
neutral relative working directories (`web/`, `./`); real jobs show their
real cwd to the localhost user only.

## Product Contract

Owner-confirmed 2026-11-22 (the mockup is the visual contract; the points
below are the behavior it fixes):

1. **Chips.** In the composer gap (`.cbadges` -> `.sbwrap`, left of the
   subagent `sbadge`) each job of the open conversation is a 24 px pill: a
   `>_` glyph for an agent-launched job, `!` for a composer job; while
   running a ring, the command head and elapsed time; once ended the outcome
   (`ok`, `exit N`, `stopped`, `lost`). Corner controls: `■` stop on a
   running chip, `×` dismiss on an ended one.
2. **Space priority (owner 22:08).** Exec chips are the LOWEST priority in
   the strip. Space for the "agent N running" subagent badge and the
   Interrupt button is always reserved first; exec chips draw only in the
   remaining space and fold to `+N` (with a failure dot if a folded job
   failed), then to one aggregate chip (at 320 px), then hide into the
   aggregate as space shrinks. The subagent badge and Interrupt never shrink,
   wrap or ellipsize because of exec chips.
3. **Pinning.** Failed, lost and just-ended jobs are pinned first for 10 s.
   Ok jobs fade 12 s after ending; failures stay until dismissed; once
   nothing else is shown a quiet "ended N" chip remains.
4. **Tooltip.** Hover/focus shows the full command clamped to 7 lines, cwd,
   elapsed, start time and owner (agent or you), plus a "Stopped by you"
   line, with or without agent notification, for a user-stopped job.
5. **Peek.** Clicking a chip opens an upward, non-modal panel with one tab
   per job and the merged stdout/stderr (stderr tinted with a left bar),
   follow-tail with a "↓ N new" jump, copy, stop, and "Jump to card" for `!`
   jobs (the 261121 private command card). Keyboard toolbar semantics;
   `prefers-reduced-motion` honored.
6. **Stop.** Stop asks an inline confirmation (focus on Cancel, 8 s auto
   revert, Esc cancels). The wording is "Stop job", distinct from
   "Interrupt" (which stops the agent's turn); the text says the process
   group is killed with no cleanup (there is no separate force level). After
   a stop: a notice card in the peek and a toast.
7. **Stop consequences.**
   - Stopping the user's own `!` job does NOT wake the agent; the "stopped"
     receipt rides the next turn through 261121's passive hand-off.
   - Stopping an agent-launched job sends the owning agent a steer-turn
     notice through its mailbox, waking it if idle. The notice text uses the
     agent session's language, not the UI language.
8. **Scope and authority.** The HUD lists the open conversation's own jobs
   (owner-scoped, as the rail's own-job count is) and is localhost-only:
   commands, cwd and output never reach a LAN view, and stop is a
   `TrustedWrite` action.

## Decisions

### Owner-confirmed product decisions (2026-11-22)

- **Placement and priority.** Exec chips are the lowest-priority element of
  the strip (Contract 2). This supersedes the mockup's note that the subagent
  badge "keeps its width until it ellipsizes". Reservation is by layout: the
  Interrupt badge (`ibadge`) and the subagent wrap (`sbadge`, and the
  background/monitor badges that share `.sbwrap`) stay `flex: none`, and the
  exec-chip group takes only the leftover width (`min-width: 0`, measured by
  a `ResizeObserver` for the `+N` / aggregate folding). Rejected: letting the
  exec group and the subagent badge share one shrinking row.
- **Stopping a user `!` job is silent to the agent** (no wake); its
  "stopped" receipt is carried by the 261121 pending-input hand-off to the
  next turn. Rejected: waking the agent for a command it did not run.
- **Stopping an agent job notifies the owning agent** through its mailbox
  and wakes it if idle. Rejected: silent stop (the agent would keep waiting
  on, or relaunch, a job the user deliberately ended).
- **Ended jobs:** ok jobs fade after 12 s; failures stay until dismissed;
  then a quiet "ended N" chip.
- **Notice language** is the agent session's language.
- **No force-kill level.** Stop is already SIGKILL of the process group
  (`Hosted.stop` -> `Holders.Stop`, `internal/execjob/hosted_ops.go:18-62`);
  the confirm text says "no cleanup". Rejected: a second "force" tier.

### Lead technical decisions (auto-confirmed 2026-11-22)

Each carries its source evidence at develop `daf510d17`.

- **Stop route `POST /s/<reply-id>/exec/<job-id>/stop`.** Registered in
  `handleSessionRoutes` (`internal/wsdashboard/page_routes.go:49-69`, the
  `sub ==` chain), gated by `TrustedWrite` (`access.go:211`: local viewer +
  same origin, else 404/403), then `Hosted.Stop(owner, id)`
  (`internal/execjob/hosted_ops.go:86`). The owner is the conversation the
  page is for: a job's `Owner` is the conversation reply-id
  (`HostedJobRow.Owner`, `hosted_types.go`) and `Hosted.owned` refuses a job
  of another owner with `ErrExecNotFound` (`hosted_ops.go:278-286`), so a
  forged job id cannot stop another conversation's job. The generation and
  lead status come from runner state through the host binding, never from
  the browser. The browser supplies only the job id. Rejected: a browser-
  supplied owner. `Hosted` is not reachable from `wsdashboard` today
  (`DaemonOptions` carries only running counts: `HostedExecJobs`,
  `daemon.go:138-143`, bound at `cmd/ws-mcp/dashboard.go:183`), so a narrow
  typed seam (list, stop, read output) is bound beside it; it is the same
  seam 261121 adds for its dashboard launch binding and the two should share
  one definition.
- **Notice delivery.** Source shows a user stop is silent today: a stop sets
  `j.stopping` (`hosted_ops.go:25`) and `endLocked` owes a notice only when
  `!j.stopping` (`hosted.go:570-576`), so the agent never learns of it. The
  existing steer route only promotes a held message (`inbox_routes.go:693`)
  and is the wrong seam. The mailbox steer path already exists on the runner:
  `Runner.AdmitCompletion` (`internal/wsagent/exec_seams.go`, search
  `func (r *Runner) AdmitCompletion`) writes one `Steer: true` inbox entry
  carrying an event ID, which steers a running turn, starts a turn on an idle
  session, deduplicates by event ID (`Record.AcceptedEvents`, cap 64) and
  suppresses a closed or since-reopened conversation. The user-stop notice
  uses it (via `HostedSessions.Notify`, `hosted_types.go`) with its own
  stable event ID `exec:<id>:stopped-by-user`, so a Retry in the UI is
  deduplicated. It is a new notice, not a new injection mechanism. The
  notice's outcome (`NoticeAccepted | Duplicate | Dropped | Deferred`) is
  held in memory next to the stopped marker and maps to the HUD states sent /
  closed / failed (Retry offered on failed); it is NOT written to the row's
  persisted `Notice` column. The mockup's "steered" vs "woken" shares one
  user-visible line (the tooltip does); if the toast must distinguish them,
  derive it from the conversation's running-turn state, memory only. For `!`
  jobs the 261121 pending-input seam carries a "stopped" receipt instead and
  no notice is admitted (no wake). Rejected: a `wsinbox` kind other than the
  existing steered entry; reusing the row's `Notice` state machine
  (persisted, tied to the async-completion contract).
- **Agent notice language.** The conversation's language is `workflow.lang`,
  resolved per conversation (`internal/wsagent/ambient.go:29-52`), a
  free-form scalar (`internal/mcp/config_registry.go:581`), not a dashboard
  language code. `wsi18n` tables hold page chrome only and state that
  machine-facing strings never pass through them (`internal/wsi18n/i18n.go`
  header), so the notice does NOT use them. Default: a small notice-sentence
  table in `wsagent` keyed by normalized language (`ko`, `en`), English for
  an unrecognized value, inside a fixed machine-readable envelope (the
  mockup's `<ws-notice source="dashboard" kind="exec-stopped-by-user">`
  sample: job id, command head, cwd, runtime, output bytes, the
  `exec.tail` pointer, and "deliberate: do not relaunch unless the user
  asks"). Rejected: LLM-translated text; the UI language. This default is a
  technical choice the implementer may revise if workflow.lang values prove
  too irregular to key; record the change in the Result.
- **Stopped-by-user marker.** An in-memory field on the hosted job
  (alongside `stopping`, `hosted_types.go` / `hosted.go:88`), set inside
  `stop()` under `h.mu` at the point `stopping` is set (`hosted_ops.go:25`),
  so a job that already ended on its own is never marked. A stop issued by the
  close cascade (`StopOwner`, `hosted_ops.go:297`) or the agent's own
  `exec.stop` is not "by user". Not persisted: lost on daemon restart, which
  is acceptable because the notice was already delivered and the row still
  shows `stopped`. Rejected: a new `exec_jobs` column (a compatibility
  surface for little value). T0.
- **Job feed.** The page learns of changes through a new opaque SSE event
  `event: execjobs`, data `{"rev":N}`, and refetches the list. Shape:
  - Server: a daemon revision counter bumped from `Daemon.ExecJobsChanged`
    (`internal/wsdashboard/daemon_runtime.go:62`, which today only wakes the
    rail via `railListRepoKey`) plus `broker.signalAll()`, and sent in the
    stream's `send` closure beside the regions exactly like the `tracker`
    event (`sse.go:640-650`, including once on connect through the `-1`
    sentinel), but NOT gated on a tracker host. `ExecJobsChanged` already
    fires on every start and every terminal transition through
    `OnRunning` (`hosted.go:220`, call sites `hosted.go:354,417,476`,
    `hosted_launch.go:313,373`, `hosted_ops.go:42,80`), and the stop marker
    is set before the end is reported, so the refetch sees a consistent job.
    Output arrival does not wake it (peek polls; see below).
  - Content is never in the event or in a region: regions are one render
    shared by every viewer's stream with no viewer locality
    (`page_regions.go:19-30`, `sse.go` header), which would put commands and
    cwd in front of LAN viewers. The event carries only a revision; the list
    is read from `GET /s/<reply-id>/exec/jobs`, `TrustedWrite`-gated like
    the stop route (a non-local viewer gets 404 and the HUD never mounts).
  - Client: `events` map in `assets/dashboard-stream.js` (`:417`) gains
    `execjobs`; the refetch is coalesced (one in flight, one queued, the
    `tkBusy`/`tkQueued` pattern of `dashboard-tracker.js`). An old page
    ignores an event name it does not list (`dashboard-stream.js:195`).
  - Data: memory-only owner-scoped read. A summary view WITHOUT the
    per-job output tails (`rowLocked` copies a 4 KiB tail per terminal job,
    `hosted.go:584-593`; 200 jobs per refetch is avoidable), carrying id,
    tool, command, cwd, state, exit code or signal, started/ended, owner kind
    (agent or you), stopped-by-user and notice state, and written bytes.
  - Rejected: a shared region (above); polling (an interval wake for an idle
    page); signalling only the owner's streams via a per-owner broker token
    (more plumbing for a rare event; revisit only if the Phase 1 measurement
    shows the all-stream wake costs anything).
- **Live peek.** A cursor read over the in-memory per-job ring:
  `JobOutput.Read(stream, offset, limit)` already returns absolute-offset
  slices, clamps at `Evicted`/`Written` and copies without I/O
  (`hosted_ring.go:506-527`); the ring is 1 MiB per job
  (`hostedJobBytes`, `hosted_types.go:238`). `Hosted` exposes no offset read
  (`Hosted.Tail` clamps to 4 KiB, `hosted_ops.go:187-195`, `HostedPreviewBytes`
  = `InlineBudget` = 4096, `execjob.go:28`), so a new owner-checked
  `Hosted` read (`owned` first) takes `(owner, id, offset, limit)`, caps
  limit at 16 KiB (below `textreader.MaxReadLimit` 64 KiB) and returns the
  next offset, `Written`, `Evicted` (an offset below it reports a gap the UI
  shows as "earlier output dropped") and the job state, via
  `GET /s/<reply-id>/exec/<job-id>/output?from=<offset>`, `TrustedWrite`-gated.
  The page polls only while a peek is open and only the open tab's job, with
  backoff while idle; no file I/O. The merged view must tint stderr, but
  `Read` on the combined stream returns flat bytes: ring records carry their
  stream and combined start (`ringRecord`, `hosted_ring.go:64-72`), so the new
  read returns stream-attributed segments over the combined range. After a
  daemon restart restored previews are combined-only records
  (`Restore`, `hosted_ring.go:295`), so they render untinted. Rejected:
  raising `Hosted.Tail`'s 4 KiB clamp globally (it also bounds the exec reply
  and the completion notice); reading the holder's ring over the holder
  socket (I/O on an interactive path); pushing output through SSE (a shared
  stream with LAN viewers).
- **Origin (agent vs user).** An in-memory field on the hosted job set from
  261121's submission->job mapping (the composer-launched job); every other
  job is "agent". `HostedJobRow.Tool` names the launching tool but a composer
  launch shares `exec.shell`'s preparation (261121 Decisions), so it cannot
  discriminate. Not persisted: after a daemon restart a surviving `!` job
  shows as "agent". Accepted (T0, nothing stored); the `!` receipt/"Jump to
  card" affordances simply do not appear for it. Rejected: a column.
- **Panel list.** Owner-scoped, memory only, not capped by `HostedListMax`
  (50, `hosted_types.go:246`, applied at `hosted_ops.go:225`): the HUD list is
  bounded by the retained jobs, `HostedMaxJobs` (200). `Hosted.List` is not
  reused as is.
- **Compatibility: T0 for every surface** (release-compatibility tiers; no
  `exec_jobs` schema or `user_version` change):
  - *New routes* (`.../exec/jobs`, `.../exec/<id>/stop`,
    `.../exec/<id>/output`): an old daemon 404s them and the new assets treat
    a 404 as "no HUD"; an old page never calls them
    (`page_routes.go:49-69,139-188`).
  - *SSE event `execjobs`*: an old page ignores unknown event names
    (`dashboard-stream.js:195`); an old daemon never sends it, so a new page
    simply shows no HUD.
  - *In-memory fields* (stopped marker, origin, notice outcome): no row
    column, no store write, so the `exec_jobs` writer of an old daemon and the
    new daemon stay symmetric (`hosted_store.go`, explicit-column SELECT /
    INSERT / ON CONFLICT as 261121 verified).
  - *MCP*: unchanged. No tool, argument or result changes; the agent-facing
    change is one additional steered inbox notice, reusing an existing entry
    kind (`wsinbox.Entry{Steer: true, EventID}`), so an old reader needs no
    new kind.
  - *Notice during a downgrade*: new-only behavior; an old daemon sends none.

## Constraints

Manuals bound to the paths this ticket edits (AGENTS.md `Implementation
Conventions`); read each before editing a covered file:

- `agents-plugin-tool/` -> `ai-docs/manuals/shipped-surface-boundary.md` and
  `ai-docs/manuals/performance.md`.
- `agents-plugin-tool/internal/` -> `ai-docs/manuals/release-compatibility.md`
  (T0 target above; run its scenario sheet for the new -> old -> new cycle).
- `agents-plugin-tool/internal/mcp/` -> `ai-docs/manuals/ws-mcp.md`: applies
  only if the host binding or the notice path touches `internal/mcp/`; the
  MCP tool surface itself is unchanged.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- `ai-docs/manuals/config-settings-ui.md` does not apply: no setting is added
  (the 10 s / 12 s / 8 s timings and the 16 KiB bound are fixed constants).
- i18n: every user-visible string goes through `internal/wsi18n/tables/`
  with ko and en tables in sync (`keys_test.go` requires every table to hold
  every English key, so all shipped tables get the keys); page-script strings
  use the `js.` prefix (`ClientPrefix`). The agent notice is the exception
  (Decisions, "Agent notice language").

Behavioral boundaries:

- Performance (`ai-docs/manuals/performance.md`): the job list, the stop
  route, the output poll and the SSE revision are memory-only with no file
  I/O on the interactive path; the one durable write a stop causes is the
  row save `stop()` already performs (`h.save`, `hosted_ops.go:80`). Polling
  runs only while a peek is open; each poll is bounded (16 KiB). Measure
  file operations and latency for: page load with 0 / 1 / 50 / 200 jobs, a
  job start/end burst (SSE wake cost across open streams), an open peek on a
  1 MiB-output job, and a stop, against the Windows file-touch baseline;
  report before/after in the Result.
- Localhost-only: list, output and stop require the localhost cookie plus
  `TrustedWrite`; a LAN view receives only the opaque revision event.
  Command text, cwd and output are rendered escaped (text nodes, never
  markup) in the HUD, and framed as untrusted data in the agent notice.
- The subagent badge and Interrupt never shrink because of exec chips
  (Decision 1); this is a verified layout property, not a visual hope.
- Out of scope: stdin control for jobs from the HUD (`exec.write`),
  re-run / restart actions, jobs owned by spawned child conversations (the
  HUD shows the open conversation's own jobs, as the rail's own count does),
  terminal sessions (`261117-feat-ws-exec-mux-terminal`), and any change to
  the MCP exec tools.

## Prior Art

- `Hosted.Stop` / `stop()` (`internal/execjob/hosted_ops.go`): owner-checked
  stop, waits for the end, `StopOwner` for the close cascade.
- `JobOutput.Read` and the per-record stream attribution
  (`internal/execjob/hosted_ring.go`): the ring the peek reads.
- `Runner.AdmitCompletion` (`internal/wsagent/exec_seams.go`) and
  `Hosted.deliver` (`internal/execjob/hosted_notice.go`): the steered,
  event-ID-deduplicated notice path.
- The tracker revision event and `signalAll` (`sse.go`, `tracker_helpers.go`):
  the opaque-revision-then-refetch pattern; `dashboard-tracker.js` for the
  coalesced client refetch.
- The composer gap: `.cbadges` and `.sbwrap` (`assets/dashboard.tmpl:26,279`,
  `dashboard-conversation.css:237`, `dashboard-rail.css:762`); the
  `ibadge`/`sbadge` fill and hide logic in `dashboard-composer-status.js`.
- The agents popover HUD (`261120-feat-ws-dashboard-chat-agent-roster-hud`):
  24 px node, ring, clamped tooltip and keyboard conventions this reuses.
- 261121 Phase 1: the `!` route conventions (server-owned grammar,
  `TrustedWrite`, `/s/<reply-id>/...` shape), the submission->job mapping that
  sets the origin field, the private command card the peek's "Jump to card"
  targets, and the pending-input seam that carries the "stopped" receipt.

## Phases

### Phase 1: Backend: stop, notice, origin, feed and output read

Depends on 261121 Phase 1 for the `!`-job pieces only (origin tag from its
submission->job mapping, the stopped receipt through its pending-input seam,
the shared typed dashboard binding). The agent-job path (stop, notice, feed,
peek read) does not depend on it and can be built and tested first; the `!`
pieces land when 261121 Phase 1 has a Result.

Goal: the daemon exposes everything the HUD needs, with no UI.

Scope:
- `Hosted` additions: stopped-by-user marker (set in `stop()` under `h.mu`),
  origin field, the owner-scoped summary list without tails and without the
  50-cap, and the owner-checked offset output read returning
  stream-attributed segments (16 KiB cap, next offset, `Evicted` gap).
- The user-stop notice through `AdmitCompletion` for agent jobs (stable event
  ID, in-memory outcome, language table with ko/en), and the "stopped" receipt
  through the 261121 pending-input seam for `!` jobs (no wake).
- Routes: `GET /s/<reply-id>/exec/jobs`, `GET .../exec/<id>/output`,
  `POST .../exec/<id>/stop` (`TrustedWrite`; open-conversation and owner
  checks; stop of an already-ended job is an idempotent no-op that sends no
  notice and marks nothing).
- The `execjobs` SSE event (revision, connect-time send, wake from
  `ExecJobsChanged`), the typed dashboard seam bound in `cmd/ws-mcp`.

Deferred: everything visual (Phase 2).

Verification boundary:
- Go tests: stop marks only a job this call ended (a job that exited first is
  not marked; close-cascade and agent `exec.stop` do not mark); a user stop
  of an agent job admits exactly one notice (a repeated stop or a Retry is
  `Duplicate`), wakes an idle conversation and steers a running one, and a
  closed or reopened-generation conversation reports closed; a user stop of a
  `!` job admits no notice and adds one receipt to the pending input; the
  owner-scoped list excludes other owners' jobs, includes more than 50, and
  copies no tail; the output read returns contiguous offsets, a gap at
  `Evicted`, stderr-attributed segments, never more than 16 KiB, clamps an
  offset past `Written`, and refuses another owner; routes refuse non-local,
  cross-origin and non-open sessions and a job of another owner; the SSE
  event is sent on connect and after a job start, end and stop, never carries
  job content, and a LAN stream gets only the revision.
- Race: a stop racing natural exit yields exactly one terminal state and at
  most one notice; `go test -race` on the touched packages.
- Compatibility: the new -> old -> new cycle of `release-compatibility.md`
  for an `exec_jobs` DB with the new daemon (no schema change); old page
  against the new daemon (event ignored); new page against an old daemon
  (404 -> no HUD).
- Platform: `GOOS=windows go vet` on the touched packages (Windows tests run
  on the remote host only).
- Performance: the measurements listed under Constraints for the daemon
  paths (list, event wake, output read, stop).

### Phase 2: HUD UI

Depends on Phase 1.

Goal: the chip strip, tooltip, peek, stop flow, notice card and toast of the
approved mockup in the chat page, with the Decision 1 priority layout.

Scope: chips and folding (`+N`, failure dot, aggregate at 320 px, hide-to-
aggregate), pinning and fade timers (10 s / 12 s), dismiss and the "ended N"
chip, tooltip, the peek panel (tabs, merged stderr-tinted output, follow-tail
and "↓ N new", copy, gap notice, "Jump to card" for `!` jobs), inline stop
confirm (focus Cancel, 8 s revert, Esc) with "Stop job" wording, notice card
and toast with Retry, toolbar keyboard semantics, `prefers-reduced-motion`,
the `execjobs` event handler with coalesced refetch and open-peek polling,
HUD absence on a LAN or old-daemon page, and i18n keys in every shipped table
(ko and en text final, `js.` prefix for page strings). The mockup's `.cbadges`
integration follows the layout rule in Decision 1; nothing in
`.ibadge`/`.sbadge` styling changes except what keeps them from shrinking.

Deferred: nothing; later features (stdin, re-run) are out of scope.

Verification boundary:
- Playwright captures in `ko` and `en`, light and dark, compared side by
  side with the mockup gallery states (idle, running, ended ok/exit/stopped/
  lost, folded `+N`, aggregate, tooltip, peek, stop confirm, notice card,
  toast).
- Narrow width: at 320 px and at the widths where the strip folds, the
  subagent badge ("agent N running") and the Interrupt button keep their full
  width and are never ellipsized or wrapped while exec chips fold or hide
  (assert computed widths with and without jobs, and with a long subagent
  label); exec chips never push either out of the gap.
- Behavior: keyboard (toolbar arrows, Enter, Esc, focus returns), stop
  confirm timeout, a stop of agent and `!` jobs (notice shown vs "no agent
  notice"), the pinning/fade timers, a LAN view and an old-daemon page show
  no HUD, output follows the tail without jumping when the user scrolled up,
  and `prefers-reduced-motion`.
- Escaping: a command or output containing markup renders as text.
- Compatibility: new page against an old daemon degrades to no HUD with no
  console error.
- Performance: an open peek polls only while open; measure per-poll cost and
  chip render cost with 200 jobs (Constraints).
