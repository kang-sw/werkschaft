---
title: "ws/exec 도구: 비동기 전환, 완료 푸시, 네이티브 프로세스 도구 제거를 포함한 데몬 소유 명령 실행"
parent: 261114-epic-ws-common-session-foundation
related:
  261117-feat-ws-single-session-holder: "idea-only context; not a prerequisite to the per-job holder start"
  261120-refactor-ws-agent-workers-under-holders: "landed holder-per-process pattern reused by per-job exec holders (merge 92c12990a)"
  261112-feat-pi-subagent-long-shell-auto-background: "absorbed; dropped (43); origin of the exec concept (owner, 2026-11-13)"
  261117-feat-ws-exec-mux-terminal: "owns the PTY mux direction split out of this ticket (51)=a"
  261114-feat-ws-conversation-audit-event-store: "future consumer of daemon-visible exec I/O"
  261115-feat-ws-permission-profile: "permission-profile caps are enforced here"
  261117-feat-ws-harness-capability-catalog: "default tool removal range and native subagent removability items"
  261117-feat-ws-agent-tools-mailbox-routing: "shares the sleep-not-close and cascading-close rules for child agents"
  261117-research-ws-daemon-file-touch-map: "release-gate file-touch constraint; not edited from here"
  261122-feat-ws-agent-turn-semaphore: "counts in-flight exec calls and gates completion notices that open a worker turn"
---

# ws/exec tools

## Background

The owner's 2026-11-13 exec concept, recorded in
`261112-feat-pi-subagent-long-shell-auto-background`, moves hosted shell
execution to ws so the daemon owns approval, async conversion, completion
delivery, and cleanup. The Pi prototype in
`agents-plugin-pi/src/execute-gateway.ts` (search
`do-i-really-have-to-run-this-myself`) had a 30 s window, a 4 KB limit, and
timeout kill. Its `background:true` tasks delivered a steer and died at
`session_shutdown` in `agents-plugin-pi/src/background-tasks.ts`. The daemon
already has `internal/execjob` and eight `exec.*` MCP tools. This ticket extends
that surface and uses the holder-per-process pattern landed for `agent.spawn`
workers in `261120-refactor-ws-agent-workers-under-holders` (merge `92c12990a`).

## Decisions

The following amendments are owner-confirmed on 2026-11-22.

1. **Surface (16), (18), (40), (42), (50), (51)=a.**
   - `exec.run(exe, args[], cwd?, env?, stdin?, async?)` runs argv directly.
     `exec.shell(cmd, cwd?, env?, stdin?, async?)` runs one shell line. The
     new shell shape uses the configured default, Bash or Git Bash on
     Windows. Use `exec.run(exe: "pwsh", ...)` to select another executable.
     The owner kept the `exec.run` name and the single `cmd` string for shell
     input in (40) and (42).
   - `exec.shell` keeps one name with two argument families. Legacy calls
     require `command` and accept `working_dir` and `shell`. New calls
     require `cmd` and accept `cwd` and `async`. Both accept `env` and
     `stdin`. Declare both shapes explicitly in the schema and dispatch
     on the family in the static top-level precheck
     (`mcp/tool_arguments.go:79-133`). Reject mixed shapes there. Preserve
     `exec.spawn` as the legacy direct-execution entry point and
     `exec.abort` as the legacy stop entry point, including their old
     inputs and response shapes.
   - With `async`, either launch returns immediately with
     `exec_<three words>`. Otherwise it waits through the configured 30 s
     execution window, shared by leads and children, then returns
     "converted to async, exec id". The conversion reply stays in the
     current turn. There is no `wait` tool.
   - `exec.stop` stops the process tree, `exec.tail` reads the last output
     slice, `exec.list` shows the session's running and recent jobs, and
     `exec.write(exec_id, data)` writes to a running job's stdin pipe.
     PTY screen sessions belong to `261117-feat-ws-exec-mux-terminal` ((51)=a).
   - Rejected: renaming legacy tools, breaking legacy argument shapes, and
     per-call shell selection on the new shape.
2. **One holder per job.** Reuse the landed holder-per-process lifecycle so
   jobs survive daemon replacement. Per-holder descriptor, exit, and
   `holder.log` files are accepted at cold start or exit. Keep terminal
   status outside generic `wsholder` cleanup for T0.
   `wsholder/manager.go:297-301` removes holder state and
   `cmd/ws-mcp/holder_exit.go:14-23` persists nothing for non-lead kinds.
   A shared holder remains optional future consolidation.
3. **Reuse `internal/execjob` and preserve the legacy surface (T0).** Use
   the configured 30 s foreground window in place of the current 5 s
   window and deliver completion as in Decision 10. The legacy
   launch and stop entry points coexist with distinct semantics for
   `exec.status`, `exec.result`, `exec.raw.tail`, `exec.raw.read`, and
   `exec.raw.grep`. Keep the existing `exec.result` wait option and
   preserve access to old records and raw byte offsets. See
   `260524-epic-async-exec-job-surface` (54-77) and
   `260524-feat-wsstore-runtime-metadata-migration-gate` (247-272).
4. **Daemon-owned advisory approval (18), (1505)=a.** Apply ws command
   rules first, including denies under `full`, then ask through the
   existing dashboard approval UI when the rules require a decision.
   Bind approval to the normalized command and its owning session. The
   current `answerApproval` requires a native answerer
   (`wsagent/runner_events.go:583-605`), so exec approvals need a daemon
   owner. The permission gate names exec and reads the live profile at
   approval and at start (`mcp/permission_gate.go:10-37`). Enforcement
   provides no OS confinement. The owner accepts the loss of the native
   Codex OS sandbox for read-only Codex sessions. Future LLM intent
   judgment belongs to `261118-feat-ws-exec-intent-filter`.
   Rejected: retain native sandboxed shells for read-only sessions ((1505)=b)
   or implement OS confinement in ws/exec ((1505)=c).
5. **Claude permission pipeline.** Allowlist `ws/exec.*` to avoid a second
   prompt. Verify in Phase 1 whether Claude raises `can_use_tool` for MCP
   tools.
6. **Native process tool removal (18), (1506)=a.** Preserve native
   Read/Write/Edit/Grep/Glob and web tools. Retain the existing native
   subagent policy from `261117-feat-ws-agent-tools-mailbox-routing`:
   Claude's Agent/Task removal where it applies and the advisory steer
   for agy native subagents (Decision 11).
   - Claude hosted launches add Bash, PowerShell, and native background
     shell and monitor tools to `--disallowedTools` through
     `claudeRemovedTools` in `wsagent/claude.go`. Pin the exact tool
     spellings in a hosted init-stream test.
   - Codex hosted launches set `features.shell_tool=false` on both
     `thread/start` and `thread/resume` (the shared start/resume config
     builder), verified on 0.160.0
     in `261115-feat-ws-hosted-codex-adapter-contract` (193, 218) and
     `wsagent/codex_fixture_test.go:365-370`.
   - Pi hosted launches omit bash from `--tools`. Worker argument
     construction is in `wsagent/pi_launch.go:62-89`, and hosted leads
     use the separate `piLeadArgs` path. The lead's MCP-only fallback,
     taken when the bundled extension is unavailable
     (`wsagent/pi_lead.go:80-90`), bypasses the extension's tool reshaping,
     so the removal must hold on that path too.
   - agy uses the daemon rejection in Decision 11.
   - Default the rollout on per harness with a verified mechanism and
     provide a per-harness config key to revert. Exclude the frozen Claude
     CLI tier, sessions without a permitted ws/exec replacement such as
     wsflow agentless, sessions already running or reattached before
     cutover, and unverified harness versions. Add the exec tools to worker
     allowlists before removing native tools (`wsagent/builtin.go:83-94`
     and `261115-feat-ws-worker-profile-session-spawn`).
7. **Job lifetime and sleep (18), (1507)=b.** Interrupt stops only a job
   still inside its synchronous window. Async and converted jobs survive
   interrupt, harness restart, handoff, compaction, and daemon replacement.
   Only `exec.stop` and explicit human close stop those jobs. Finishing a
   child turn leaves the session available to sleep ((57) in
   `261117-feat-ws-agent-tools-mailbox-routing`). Use one sleep predicate for
   `idleNow` (`wsagent/runner_events.go`) and `awakeLocked`
   (`wsagent/agent_admission.go:144-150`), which disagree today. A running
   turn, a live job (including a monitor), or an awake child keeps a session
   awake. Count live exec jobs owned by that session. A sleeping child does
   not keep its parent awake.
8. **Explicit close cascades (18), (60)=a.** Human dashboard close ends
   the session's jobs and children deepest-first. Agents have no session
   stop tool. Fence new job admissions before close and suppress completion
   after close (`wsagent/runner_operations.go:369-382`, `finishClose` at
   1107-1123). Keep `exec.stop` for individual jobs.
9. **Output and retention.** The daemon sees exec I/O directly for future
   consumption by the audit store. Keep new hosted output in bounded
   per-job and aggregate memory rings with restore-only deltas, without
   per-job output files. Provide a 4 KiB inline preview, explicit evicted
   offsets, and a finite list and metadata cap. Retain raw Windows bytes
   and validate UTF-8 when decoding them.
10. **Completion steer (owner, 2026-11-22).** Route completion through the
    existing `Runner.Deliver` path. A notice steers a running turn immediately,
    opens a new turn in an idle session, or enters the next-turn queue
    when the harness lacks steer support. Deliver once, only for async
    or converted jobs, durably across daemon replacement, keyed by a stable
    per-job completion event ID so replay and restart never deliver a second
    notice. Suppress completion after the session closes. `Runner.Deliver`
    currently marks steer only for spawned Pi children
    (`wsagent/runner_operations.go:118`); Phase 1 extends steer marking to
    every harness path that supports steering. Rejected: next-turn-only
    delivery.
11. **agy command blocking (1503), after the agy 1.5.14 live probe on
    2026-11-22.** The settled launch policy is native `default` mode.
    Native `auto` and `--dangerously-skip-permissions` are never used.
    For cutover sessions this also overrides a saved lead permission mode
    that `wsagent/agy.go:380-385` would otherwise reapply on new, resumed,
    and reattached sessions. The per-harness revert key restores the
    pre-cutover behavior, including the saved mode; excluded sessions keep
    their current mode.
    ws bypass and worker auto-approval stay daemon-side
    (`wsagent/agy_bypass.go:15-36`), except that the daemon answers
    `run_command` permission requests with `reject_once`.
    - In `default`, each tested `run_command` (`ls`, `git status`, `echo`,
      and an `IsDaemon:true` background sleep) raised
      `session/request_permission`. Both native `auto` and
      `--dangerously-skip-permissions` ran commands without a request.
    - Rejection immediately ended the turn with empty failed output and
      no retry. ACP provides no model-visible custom rejection text, and
      an immediate post-rejection steer failed with `-32603`. Append the
      instruction to use the ws exec tools to the system prompt at
      session creation. Once the rejected turn ends, the daemon sends
      that instruction as a new prompt.
    - A `run_command` that starts without a permission request is detected,
      logged as a warning, and answered with the same instruction prompt.
      A user-home always-allow rule is a possible, untested path.
    - `read_file`, `manage_task`, and `invoke_subagent` raised no permission
      request. Retain the existing advisory steer for native subagents.
    - Rejected: custom main agent, which replaces the default prompt and
      puts MCP tools behind `call_mcp_tool`
      (`261118-research-ws-agy-custom-agent-prompt`), and the originally
      selected per-process deny ((1503)=b), since top-level `agy --settings`
      parses but does not apply permission or mode rules to ACP sessions.
12. **Report hold.** Hold a worker's done report to its parent while the
    worker's session owns a live exec job. Cover both `holdsReport`
    (`wsagent/children.go:778-831`) and the successor that publishes a
    held result when a child report arrives (`wsagent/runner_conv.go:1340-1367`).
    Preserve interim supersession for report-free follow-ups. Recover jobs
    on replay before releasing any held report
    (`wsagent/runner_process.go:667-719`). Legacy agy ACP background tracking
    and its 6 s wake grace remain only for sessions outside the cutover.
13. **Ownership.** The authenticated originating conversation owns the
    exec job and the semaphore turn. Validate a supplied `session_key`
    only as a cwd/worktree grant. A descendant key cannot choose the owner
    (`mcp/server_sessions.go:286-298`, `mcp/hosted_key.go:17-48`). Apply
    the new approval policy to every hosted legacy launch shape.
14. **Process tree.** Put each job in a dedicated process group. On Windows
    use a job object or `taskkill /T`. Stop and close kill the whole group.
    Detached processes are outside the guarantee. Current holder stop
    signals only the direct child (`wsholder/holder.py:315-372`,
    `wsholder/manager.go:728-756`).
15. **Worktree guard.** Refuse `worktree.release` and `worktree.remove` while
    a job of that worktree is live. Serialize admission with release
    (`mcp/worktree_tools.go:864-907`, `mcp/worktree_remove.go:86-97`).
16. **Compaction.** Phase 1 exposes the session-scoped `exec.list` read
    seam from `261117-feat-ws-hosted-compaction-lifecycle` ((1893)=b).
    The restore bundle consumes it in a later phase or edition of that
    ticket. Share the harness-kill survival test with
    `261120-feat-ws-agy-timeout-process-respawn`.
17. **Semaphore coordination.** Measure the configured 30 s
    execution-to-conversion window independently of total RPC duration,
    which includes preflight, approval, the execution wait, and slot
    re-acquisition. An in-flight exec call counts as an in-flight ws MCP
    tool call under `261122-feat-ws-agent-turn-semaphore`.
18. **agy background prompt cleanup.** After agy cutover in Phase 2, remove
    `agyLeadBgShellRule` and its use (`wsagent/agy.go:117`, near 340) and
    the corresponding line in `wsagent/instructions/agent-worker-agy.md:5`.
    Update `TestAgyLeadPromptCarriesTheWorkflowManual` and
    `TestAgyWorkerProfile` in `wsagent/agy_lead_test.go`.

## Constraints

- Run Go tests locally on macOS and run Windows checks on the Windows host
  (owner (68), recorded in `261115-feat-ws-hosted-codex-adapter-contract`).
- Read before editing `agents-plugin-tool/`:
  `ai-docs/manuals/shipped-surface-boundary.md` and
  `ai-docs/manuals/release-compatibility.md`; for `agents-plugin-tool/internal/mcp/`
  also `ai-docs/manuals/ws-mcp.md`. If playbooks, skills, `agents-plugin/rsrc/` or
  `agents-plugin-wsflow/` text is touched, also read
  `ai-docs/manuals/skill-authoring.md` and `ai-docs/manuals/wsflow-mirroring.md`.
- Release-gate file-touch rule (owner, 2026-11-17): minimize daemon file touches
  at the root; files for backup/restore only; deltas (append) not rewrites; no
  heartbeat rewrites; no per-event O(N) rereads; interactive paths never wait on
  file I/O; judged by measured syscalls/s on the owner's PC.
- Blocking native process tools in hosted leads is an observable behavior change,
  already owner-confirmed by (18).
- The Claude CLI tier is frozen and keeps its native tools.

## Compatibility

The new exec shapes are additive (T0). Keep all eight legacy calls supported,
including both argument families on `exec.shell`, distinct status/result and
raw readers, and old records and byte offsets. Retain holder exit status
through a downgrade and reopen. State the tier of any exec-specific holder
protocol change here. Record the concrete verdict in Phase 1's Result with
source lines showing which version writes and reads each surface, per
`ai-docs/manuals/release-compatibility.md`.

## Phases

### Phase 1: Exec surface, per-job holders, approval, and completion steer

Implement the six exec tools and the T0 legacy contracts from Decisions 1-3
over `internal/execjob` and the landed holder-per-process lifecycle. Bind jobs
and daemon-owned approval to the authenticated conversation and enforce the
same approval policy for hosted legacy launches. Implement the process-group
lifecycle, output retention, and session-scoped `exec.list` read seam.
Deliver durable async completion with the existing steer, idle-turn, and
next-turn routes. Implement the independent 30 s execution window and check
Claude's MCP approval path. Phase 2 uses this surface before removing native
tools. Restore-bundle integration follows separately.

Verification:

- Exercise direct argv, both `exec.shell` shapes, rejection of mixed arguments
  by the precheck, original launch and stop shapes, all five distinct legacy
  readers, and preserved old records and raw byte offsets.
- Exercise sync, async, conversion in the current turn, and pipe stdin via
  `exec.write`. Prove preflight, approval, and slot re-acquisition do not
  consume the 30 s conversion window. Verify running, idle, and next-turn
  completion delivery on every steer-capable harness, once across daemon
  replacement and replay by its stable event ID, including suppression
  after close and absence of notices for synchronous completions.
- Verify T0 with an old daemon running the new holder, then holder exit, then
  new-daemon reopen and terminal-status retrieval. Share the harness-kill
  survival case with `261120-feat-ws-agy-timeout-process-respawn`.
- Verify process-tree kill for stop, including Windows, and the detached
  process boundary. Check Bash resolution, raw Windows bytes and validated
  UTF-8 decoding, 4 KiB previews, per-job and total-ring eviction offsets,
  finite metadata and list bounds, and session-scoped listings.
- Verify command-rule allow and deny, deny under `full`, dashboard approvals
  bound to command and owner, live profile changes before approval or start,
  legacy shape enforcement, and rejection of owner selection by a descendant
  key. Pin the Claude `can_use_tool` finding and verify one approval path.
- Measure syscalls/s on the owner's PC and account for cold-start and exit
  holder files. Verify that new hosted jobs create no per-job output files
  and that interactive paths stay off file I/O.

### Phase 2: Harness cutover, report hold, and session and worktree lifecycle

After Phase 1, add the exec tools to worker allowlists and then enable verified
per-harness native process tool removal with its rollout switch and exclusions.
On agy, use the native default launch mode and daemon `reject_once` for
`run_command`; preserve daemon-side bypass for other requests and handle
commands that start without a request. Append the creation-time instruction,
send its new-prompt follow-up after rejected turns, and remove the old agy
background prompt rules after cutover.

Hold worker done reports while owned exec jobs remain live, including the child
report successor and recovery replay paths. Keep legacy agy background tracking
and the wake grace only outside cutover. Drive sleep and admission from the
shared predicate. Serialize job admission with worktree release and protect
release and removal against live jobs. Apply interrupt semantics and fence
admission before a human close, kill the process trees deepest-first, and
suppress post-close completion.

Verification:

- Verify Claude's hosted init stream and exact removals, the verified Codex
  config fixture on both start and resume, Pi's lead and worker tool flags
  including the lead's MCP-only fallback, preserved tools, worker allowlist
  ordering, default-on behavior, and per-harness revert. Cover the frozen
  Claude CLI tier, missing replacement tools, existing and reattached sessions,
  and unverified versions.
- Verify agy default launch on new, resumed, and reattached sessions with
  a saved non-default mode, the `reject_once` exception under bypass and worker
  auto-approval, command-request detection for foreground and daemon jobs,
  prompt append, new prompt after turn end, and the warning and instruction
  path when a command starts without a request. Cover the advisory-only
  native subagent path and update both prompt regression tests in Decision 18.
- Verify interrupt during the sync window and survival of async or converted
  jobs across interrupt, harness restart, handoff, compaction, and daemon
  replacement. Check the common sleep predicate with a running turn, live
  job, awake child, or sleeping child.
- Verify report hold and later publication, including child report arrival,
  replay before release, and interim supersession for report-free follow-ups.
  Check that only pre-cutover agy sessions retain background tracking and
  the 6 s wake grace.
- Race job admission with worktree release, and verify release and removal
  refusal while a job is live. Verify close fences new admissions, cascades
  deepest-first, kills whole job groups, and suppresses post-close notices.
