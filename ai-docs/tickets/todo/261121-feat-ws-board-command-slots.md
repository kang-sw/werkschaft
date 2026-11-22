---
title: "화이트보드 커스텀 커맨드 슬롯: 제목·설명·실행 버튼으로 등록한 명령을 사용자가 눌러 실행"
related:
  261117-feat-ws-exec-tools: prerequisite, landed (daemon-owned hosted exec service this reuses)
  261117-feat-ws-exec-mux-terminal: neighbouring exec surface (terminal sessions); independent
  261118-feat-ws-exec-intent-filter: neighbouring exec safety layer
---

# Whiteboard custom command slots

## Background

From the owner's idea memo (2026-11-21 triage, board topic
`idea-board-later`): register commands on the whiteboard as slots with a
title, a description, and a run button the user clicks. Ideas in the memo:
- show when a slot was registered;
- browse for a path with the system file explorer;
- record each run's stdout to a log;
- copy the log path so it can be handed to an agent.

A read-only exploration against develop `7c0b1c1c4` (2026-11-22) settled
the model against the landed `ws/exec` surface. Findings:

- `execjob.Hosted.Launch` is the reusable launch seam: one pipe holder per
  job, owned by a hosted conversation reply ID, with worktree admission,
  runner approval, process-group stop, and reattach across daemon
  replacement (`internal/execjob/hosted_launch.go`, `hosted_ops.go`,
  `internal/wsagent/exec_seams.go`). The dashboard HTTP side has no launch
  route or callback today. The installed `HostedSessions` adapter needs an
  open runner conversation, so Run is possible only on an open hosted
  lead's board without a new dashboard-owned principal.
- Hosted exec exposes no copyable stdout file. Output lives in bounded
  daemon memory and holder rings; finished jobs keep 4 KiB previews per
  stream in `exec-jobs.db`, and rows are evicted by the 200-job cap
  (`internal/execjob/hosted.go`, `hosted_types.go`). A stable log path needs
  a new artifact seam at the holder boundary.
- Boards live in `<worktree>/ai-docs/whiteboards/<reply-id>/.board.sqlite`
  (Type 4), scoped per session (`internal/mcp/whiteboard_store.go`,
  `internal/wswhiteboard/board_db.go`). The dashboard already starts PTY
  terminals through holders (`internal/wsdashboard/terminals.go`) and has a
  localhost directory picker (`internal/wsdashboard/local_browse.go`,
  `assets/dashboard-rail-popovers.js`).

## Decisions

- Run reuses the hosted exec path through a typed dashboard launcher
  callback bound in `cmd/ws-mcp` to the existing `execjob.Hosted` service.
  The callback keeps worktree admission, runner approval, holder lifecycle
  and stop. No parallel execution path is added (the owner's (1487)
  permission reading). Technical, auto-confirmed 2026-11-22.
- Slot definitions live in a separate daemon-local SQLite registry (Type 2,
  held handle, in-memory read model, one WAL commit per change). That keeps
  executable registration out of the agent-authored board store and its zip
  export. Technical, auto-confirmed.
- A command is an executable plus an argv array. A shell line is a
  distinctly labelled alternative that uses `execjob.ShellArgv`, so Windows
  keeps the existing Git Bash lookup and refusal. cwd is its own field: a
  worktree-relative or typed path, revalidated server-side. Technical,
  auto-confirmed.
- Run sends the slot ID and expected revision. The server resolves the
  stored definition, so the displayed command is exactly what runs.
  Register, edit, delete, Run and log lookup require the localhost cookie
  plus `TrustedWrite`, and LAN views never show commands, log paths or
  stdout. Technical, auto-confirmed.
- Slot runs launch async with explicit empty stdin and opt out of agent
  completion delivery through an internal no-notice option. The existing
  notify column carries it, so the schema does not change. Technical,
  auto-confirmed. Whether to notify agents automatically is (1548).
- Compatibility target T0: additive HTTP routes and assets, an independent
  registry DB and an independent log directory outside holder cleanup. The
  MCP schema, hello, and holder event and attach shapes do not change; a
  holder capture flag applies only to newly staged holders. Verify a
  new -> old -> new cycle: board writes, a holder exit through cleanup, and
  a usable log after reopening.

## Open Decision Queue

- (1548) policy: Phase 1 policy defaults, asked as one bundle on the board.
  The recommended bundle: (1) only the user registers slots, with agent
  proposals later; (2) slots are scoped to the session's board, with
  project sharing later; (3) results show only in the dashboard, with no
  automatic agent notice, and the user copies the log path; (4) logs keep
  1 MiB per run, 7 days and 100 MiB total; (5) the dashboard directory
  picker, not the native OS dialog. Alternatives: project-scoped slots,
  or agent-proposed slots confirmed by the user in Phase 1.

Last ID: (1548)

## Constraints

- Permission model (owner's reading, (1487) note): the dashboard already
  exposes a terminal, so a user-clicked command slot is close to exposing
  `ws/exec` itself. It adds no new class of capability, but it must reuse
  that execution path rather than invent a parallel one.
- `ai-docs/manuals/performance.md`: no file I/O on page load or list. One
  commit per slot change. Log retention deletes whole oldest files away
  from interactive reads. Measure the file operations for register, Run and
  page load.
- `ai-docs/manuals/release-compatibility.md`: keep the T0 target above.
- `ai-docs/manuals/config-settings-ui.md` applies if retention limits become
  settings.

## Phases

### Phase 1: User-managed board slots with Run and a stdout log

- Registry and routes: `internal/wsdashboard/board_command_slots.go` and
  `board_command_slots_store.go` (new), plus `page_routes.go`, `daemon.go`,
  `daemon_start.go`, `cmd/ws-mcp/dashboard.go` and
  `cmd/ws-mcp/exec_hosted.go`. Store stable slot revisions and run IDs, and
  gate Run on an open hosted lead board.
- Launch and capture: share transport-neutral preparation with
  `internal/mcp/server_exec_hosted.go`. Add an opt-in stdout capture in
  `internal/execjob/hosted.go`, `hosted_launch.go`,
  `internal/wsholder/manager.go` and `holder.py`. The holder finalizes one
  bounded stdout file before it exposes the exit event, so an exit during
  daemon replacement still yields a path. Keep artifact status and
  retention independent of exec row eviction, and show truncation,
  expiry and failure explicitly.
- UI: controls in dashboard-owned whiteboard chrome, outside authored topic
  content (`assets/dashboard.tmpl`, `dashboard-board.js`,
  `dashboard-actions.js` clipboard reuse, `internal/wsi18n/tables/`). Show
  title, description, command and cwd, registration time, the last run's
  status and exit code, a capped preview, and Copy log path.
- Tests: local/origin and owner restrictions, stale revisions, in-memory
  listing, approval and cwd behavior, restart and replacement,
  stdout/stderr isolation, zero and nonzero exit, truncation, missing
  artifacts, and the downgrade cycle (`board_command_slots_test.go`,
  `internal/execjob/hosted_test.go`, `internal/wsholder/tests/test_holder.py`).

### Phase 2: Browser path selection for cwd

Reuse or extract the dashboard directory picker for the slot cwd, starting
at the board worktree, with server-side revalidation. Cover Windows drives,
spaces and junctions, and browser-only use
(`assets/dashboard-rail-popovers.js`, `files.js`, `dashboard-board.js`; see
`local_browse_terminal_test.go` for a picker-test reference).
