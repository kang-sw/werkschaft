---
title: "ws/exec 도구: 비동기 전환, 완료 푸시, 네이티브 프로세스 도구 제거를 포함한 데몬 소유 명령 실행"
parent: 261114-epic-ws-common-session-foundation
blocked-by: 261115-feat-ws-permission-profile#phase2
related:
  261117-feat-ws-single-session-holder: "idea-only context; not a prerequisite to the per-job holder start"
  261120-refactor-ws-agent-workers-under-holders: "landed holder-per-process pattern reused by per-job exec holders (merge 92c12990a)"
  261112-feat-pi-subagent-long-shell-auto-background: "absorbed; dropped (43); origin of the exec concept (owner, 2026-11-13)"
  261117-feat-ws-exec-mux-terminal: "owns the PTY mux direction split out of this ticket (51)=a"
  261119-epic-ws-conversation-audit: "future consumer of daemon-visible exec I/O; supersedes the dropped 261114-feat-ws-conversation-audit-event-store"
  261115-feat-ws-permission-profile: "prerequisite; landed Phase 2 supplies hosted permission controls and the effective full selection"
  261117-feat-ws-harness-capability-catalog: "default tool removal range and native subagent removability items"
  261117-feat-ws-agent-tools-mailbox-routing: "shares the sleep-not-close and cascading-close rules for child agents"
  261117-research-ws-daemon-file-touch-map: "release-gate file-touch constraint; not edited from here"
  261117-feat-ws-hosted-compaction-lifecycle: "consumes the Phase 1 exec.list seam in a later restore-bundle edition"
  261120-feat-ws-agy-timeout-process-respawn: "shares the harness-kill survival fixture first established by exec Phase 1"
  261122-feat-ws-agent-turn-semaphore: "downstream consumer of Phase 1 conversation attribution and completion delivery"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 338f16892c5ddf00
sage-review-completeness-reviewed: 338f16892c5ddf00
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

Decisions 1–18 reflect the owner-confirmed plan as of 2026-11-22.
Decisions 19–22 record technical settlements.

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
4. **Daemon-owned advisory approval (1512)=b, 2026-11-22.** Approval
   depends on session role and mode, without command parsing or rule
   matching. Spawned workers run permitted exec calls immediately, as
   their native requests are auto-resolved today. Hosted leads in bypass
   or `full` run immediately. Hosted leads in other modes get one prompt
   per call in the existing dashboard approval UI. Bind the answer to
   the command and owning session and route it to a daemon-owned answer
   path. The current `answerApproval` requires a native answerer
   (`wsagent/runner_events.go#L584-L609`). Re-read live mode before
   approving or starting the command. For an agy lead without a common
   permission profile, use its effective ws bypass state
   (`wsagent/agy_bypass.go#L9-L17`, `#L47-L61`); prompt when bypass is
   off. Enforcement is advisory, and the owner accepted losing the
   native Codex OS sandbox ((1505)=a).
   Rejected: (1512)=a would run all lead calls without asking.
   (1512)=c would add a configuration list and command matching. This
   decision supersedes (1511) and the approval mechanism in (18).
   Command-content judgment stays in `261118-feat-ws-exec-intent-filter`.
   Codex currently preapproves only `ChildSafeWSTools` and uses approval
   policy `never` for read-only workers (`wsagent/codex.go#L256-L289`,
   `#L307-L325`). For cutover, preapprove the exec tools each Codex worker
   role is permitted to call and all three `fs.*` tools from Decision 6.
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
     Selection for cutover, the trusted binding and the rollout switch
     determine shell removal. A live permission-mode change leaves shell
     eligibility intact and needs no thread reopen
     (`wsagent/codex.go#L205-L245`, `#L430-L441`).
   - **Codex read tools (owner, 2026-11-22 10:35 KST).** Before removing
     shell, expose three additive T0 MCP tools to hosted Codex leads
     and workers selected for cutover:
     - `fs.read(path, offset?, limit?)` reads text lines. The offset is
       the number of lines to skip, and limit is the number to return.
     - `fs.grep(pattern, path, glob?)` searches a file or directory and
       returns ripgrep-style matches with paths and one-based line numbers.
       The optional glob filters filenames.
     - `fs.list(path, glob?)` lists directory entries with an optional
       glob filter.
     The tools are read-only and run without approval in every session
     mode. Match the readable paths of Codex's native read-only mode with
     advisory checks and no OS confinement. Return a clear binary-file
     error from `fs.read` and skip binary files in `fs.grep`. Bound each
     inline result to 4 KiB and report truncation. The Phase 2 implementer
     chooses finite limits for returned lines, matches and directory
     entries, as well as the work spent scanning. Record the values in
     the Phase 2 Result and cover them in tests.
     Add all three to the Codex worker allowlist before disabling shell.
     Advertise and admit them only for hosted Codex sessions in cutover
     using the trusted binding in Decision 13. Other harnesses retain
     native Read/Grep/Glob. The owner chose a uniform ws read surface
     to avoid per-read approval prompts. Rejected alternatives were
     retaining shell for Codex leads outside bypass/full and relying on
     per-version experimental `read_file`, `list_dir` or `grep_files`.
   - Pi hosted launches omit bash from `--tools`. Worker argument
     construction is in `wsagent/pi_launch.go:62-89`, and hosted leads
     use the separate `piLeadArgs` path. The lead's MCP-only fallback,
     taken when the bundled extension is unavailable
     (`wsagent/pi_lead.go:80-90`), bypasses the extension's tool reshaping.
     Verify its launch flags when it has the trusted binding in Decision 13.
   - agy uses the daemon rejection in Decision 11.
   - Default the rollout on per harness with a verified mechanism and
     provide a per-harness config key to revert. Exclude the frozen Claude
     CLI tier, sessions without a permitted ws/exec replacement such as
     wsflow agentless, sessions already running or reattached before
     cutover, and unverified harness versions. Pi bridge and Claude stdio
     fallback connections keep native process tools until they have the
     trusted binding in Decision 13. Add the exec tools to worker allowlists
     before removing native tools (`wsagent/builtin.go:83-94` and
     `261115-feat-ws-worker-profile-session-spawn`).
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
    delegates to `Runner.Send`, which currently marks steer only for spawned
    Pi children (`wsagent/runner_operations.go:118`). Phase 1 extends steer
    marking to every harness path that supports steering, subject to
    Decisions 21–22. Rejected: next-turn-only delivery.
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
    `run_command` permission requests with `reject_once`. Apply the same
    guard to live settings updates, in-stream mode changes, reconciliation
    and replayed approvals so a mode transition cannot bypass the launch
    policy (`wsagent/permission.go#L365-L406`,
    `wsagent/runner_events.go#L1031-L1068`). Preserve the saved preference
    for the revert path.
    - In `default`, each tested `run_command` (`ls`, `git status`, `echo`,
      and an `IsDaemon:true` background sleep) raised
      `session/request_permission`. Both native `auto` and
      `--dangerously-skip-permissions` ran commands without a request.
    - Rejection immediately ended the turn with empty failed output and
      no retry. ACP provides no model-visible custom rejection text, and
      an immediate post-rejection steer failed with `-32603`. Append the
      instruction to use the ws exec tools to the system prompt at
      session creation. Send one instruction prompt at the turn boundary
      even when the rejected turn returns empty output. Deduplicate the
      follow-up across replay of the same turn.
    - Correlate permission requests and actual execution evidence by ACP
      `toolCallId` within the process and turn. Require execution evidence
      such as process-start or exit data before treating a command as having
      run. An initial `in_progress` tool announcement can precede the
      permission request (see `wsagent/testdata/agy-1.5.10-hosted-lead.jsonl`).
      If a command ran without a request, log a warning and send the same
      single instruction prompt at the turn boundary. A user-home
      always-allow rule is a possible, untested path.
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
    the new approval policy to every hosted legacy launch shape. Set the
    conversation and process identity in server-side request context after
    transport validation. The HTTP token resolves to a Runner binding
    (`wsagent/mcp_session.go#L153-L188`), while launcher hello fields come
    from the client (`wsdashboard/proxy.go#L186-L206`). A supplied key or
    hello field alone grants neither hosted job ownership nor turn
    attribution. Require a trusted binding for a hosted exec replacement.
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
19. **Legacy hosted timing.** Apply the approved 30 s execution window to
    hosted `exec.spawn` and legacy-form `exec.shell`. Keep their legacy
    launch arguments, shell resolution, `exec_key` and reply shape, including
    the running response and its reader guidance after time expires.
    Preserve finite stdin for these holder launches: EOF after supplied
    bytes or immediate EOF when stdin is absent
    (`internal/execjob/execjob.go#L226-L233`). Programs that read to EOF
    must still finish without any later `exec.write`. The timing follows
    Decision 3's explicit replacement of `ForegroundWindow` and
    keeps old clients able to use the returned key in legacy readers and
    `exec.abort`. Non-hosted legacy launches retain the existing 5 s window
    because the rollout targets hosted sessions. The old timed branch is
    `internal/execjob/execjob.go#L255-L269`; the legacy wrappers are in
    `internal/mcp/server_dispatch.go#L289-L309`.
20. **Integration order.** Phase 1 owns the authenticated conversation
    attribution and standard `Runner.Deliver` steer-or-wake seam, including
    when no semaphore is present. `261122-feat-ws-agent-turn-semaphore`
    consumes those seams after Phase 1 lands. The harness-kill survival
    fixture also lands here, using an independently stoppable harness and
    live job holder. These producer-side checks establish the handoff before
    the semaphore and the agy recovery ticket exercise their own behavior.
21. **Idempotent completion admission.** Use the stable event ID at Runner
    admission, with a durable acceptance record recoverable together with
    the inbox enqueue. A crash before enqueue leaves the completion
    retryable. A crash after enqueue recovers its acceptance without a
    second entry. Preserve deduplication after inbox drain and while the
    source event can still replay. Check the owning session's open
    generation in the same fence as acceptance so an explicit close
    invalidates pending delivery, including after manual revival. Completion
    must never take the closed-Pi auto-revival path for ordinary input
    (`wsagent/agent_delivery.go#L44-L67`,
    `wsagent/runner_operations.go#L75-L119`,
    `wsinbox/inbox_store.go#L199-L233`). This keeps recovery from duplicating
    an accepted notice or reopening a human-closed session.
22. **agy completion while the slot is yielded.** agy steer sends a
    replacement ACP prompt (`wsagent/agy.go#L401-L426`). When the current
    turn has yielded its semaphore slot for an MCP call, hold the completion
    steer until the originating MCP call has replied or been cancelled and
    the same turn generation is counted. A lead counts immediately on
    reacquisition. Cancellation drops the pending HTTP reply and restores
    accounting under S14. Release the steer once even if a late result
    follows cancellation (`wsdashboard/proxy_http.go#L200-L206`,
    `#L406-L415`). If the turn has ended, queue the completion for ordinary
    next-turn delivery. See S7 and S14 in
    `261122-feat-ws-agent-turn-semaphore`.
23. Downgrade boundary (owner (2165)=a, 2026-11-22): legacy-shaped hosted
    `exec.spawn`/`exec.shell` jobs launched by a 0.51 daemon live only in
    `exec-jobs.db`. A v0.50.8 daemon cannot read, return results for, or abort
    their `exec_key` ("not found"). Re-upgrading recovers status from the
    retained row and exit copy. Accepted as T3 within the 0.51 minor boundary.
    Rejected: mirroring legacy launches to the file store.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/skill-authoring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin-wsflow/rsrc/, agents-plugin-wsflow/skills/, agents-plugin-tool/internal/wsdoc/conventions/)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for agents-plugin/rsrc/, agents-plugin/skills/, agents-plugin/hooks/, agents-plugin-wsflow/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-pi/config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)
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

The new MCP shapes target T0. Keep all eight legacy calls supported, including
both argument families on `exec.shell`, distinct status/result and raw readers,
and old records and byte offsets. Capability-gate new names and arguments on
older servers. Verify the approved hosted timing and approval changes with a
legacy caller's timeout and result handling. Retain holder exit status through
a downgrade and reopen. Grade the holder protocol and restore data separately
and record the directional verdicts in Phase 1's Result, with source lines
showing which version writes and reads each surface and what survives reopening.
The Codex-only `fs.*` tools are additive T0. In Phase 2, verify that the
new inventory and allowlist keep legacy calls and other harness tool
inventories compatible. Check discovery before removing native shell.

## Prior Decisions

- 1f3a7477e (2026-11-19, commit): "Only undeclared metadata is stripped; a schema-declared property keeps its original value and handler validation. Other unknown inputs remain refused." — bearing: constrains
- 70678ab2e (2026-11-13, commit): "Keep optional exit fields in the session registration rather than relying on transient holder logs; preserve them across runner heartbeats and persist before directory cleanup, retrying failed writes." — bearing: constrains
- 261118-feat-ws-agy-tool-names-and-worker-role (2026-11-18, commit): "Worker eligibility, profile, HTTP attachment and auto-approval are registration metadata; common launch/admission code gains no agy branch." — bearing: constrains
- e83316aeb (2026-11-18, commit): "Approval ids are agy's toolCallId, not the JSON-RPC id: request ids restart per process, while the call id is stable and is what tool_call_update settles." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/execjob/, internal/mcp/, internal/wsagent/, internal/wsholder/; harness integration and config registry |
| scope.surface | public-interface | exec MCP calls and legacy contracts plus hosted Codex fs.read, fs.grep and fs.list |
| scope.new_public_symbol | yes | exec.run, exec.stop, exec.tail, exec.list, exec.write, fs.read, fs.grep, fs.list |
| scope.new_type_contract | yes | exec and fs MCP argument and response contracts, including exec.shell argument families |
| scope.test_surface | existing | agents-plugin-tool/internal/mcp/server_stdio_exec_test.go; wsagent/agy_lead_test.go |
| complexity.reuse_points | confirmed | internal/execjob/execjob.go, wsagent/agent_delivery.go, wsholder/manager.go |
| complexity.side_effect_risk | high | process execution, approval, lifecycle, and native tool cutover |
| risk.correctness | high | durable completion and concurrent process, session, worktree, and replay lifecycle |
| risk.fit | high | coordination across harnesses, legacy tools, semaphore, and holder behavior |
| risk.test | high | cross-platform process trees, downgrade and replay, harness-specific cutover |
| risk.security_or_contract | high | approval, authenticated ownership, native process tool removal, and legacy compatibility |

## Phases

### Phase 1: Exec surface, per-job holders, approval, and completion steer

Implement the six exec tools and the T0 legacy contracts from Decisions 1-3
over `internal/execjob` and the landed holder-per-process lifecycle. Bind jobs
and daemon-owned approval to the authenticated conversation. Apply the role
and live-mode approval policy in Decision 4 to new and legacy hosted
launches. Implement the process-group
lifecycle, output retention, and session-scoped `exec.list` read seam.
Deliver durable async completion with the existing steer, idle-turn, and
next-turn routes, fenced by session generation and deduplicated at enqueue.
Implement the independent 30 s hosted execution window and
check Claude's MCP approval path. Phase 2 uses this surface before removing
native tools. The semaphore and restore bundle integrate with the Phase 1
seams in their respective tickets.

Verification:

- Exercise direct argv, both `exec.shell` shapes, rejection of mixed arguments
  by the precheck, original launch and stop shapes, all five distinct legacy
  readers, and preserved old records and raw byte offsets. Compare the timed
  running response on a hosted legacy call with the existing non-hosted 5 s
  path, including an older client's follow-up status, result, and abort.
  Use a command that reads to EOF through both legacy holder launch shapes,
  with supplied stdin bytes and with stdin absent.
- Exercise sync, async, conversion in the current turn, and pipe stdin via
  `exec.write`. Prove preflight, approval, and slot re-acquisition do not
  consume the 30 s conversion window. Verify running, idle, and next-turn
  completion delivery on every steer-capable harness, once across daemon
  replacement and replay by its stable event ID. Inject exits immediately
  before and after enqueue and replay again after inbox drain. Race completion
  with close and manual revival of a Pi child. Verify no automatic revival,
  no stale notice after manual revival and no notices for synchronous
  completions. Exercise ordinary Runner admission without a semaphore.
- In the agy completion-gate fixture, cancel the originating MCP call
  before its pending slot grant, then deliver a late MCP result. Verify
  one steer after the same turn generation is counted, with no second
  steer or slot count from the late result.
- Verify T0 with an old daemon running the new holder, then holder exit, then
  new-daemon reopen and terminal-status retrieval. Share the harness-kill
  survival case with `261120-feat-ws-agy-timeout-process-respawn`.
- Verify process-tree kill for stop, including Windows, and the detached
  process boundary. Check Bash resolution, raw Windows bytes and validated
  UTF-8 decoding, 4 KiB previews, per-job and total-ring eviction offsets,
  finite metadata and list bounds, and session-scoped listings.
- Verify worker launches never prompt and hosted leads in bypass or
  `full` run immediately. Verify other hosted lead modes prompt once per
  call via the dashboard and honor approval and denial. For an agy lead
  without a common profile, test ws bypass both on and off. Change the
  mode while approval is pending and between preflight and process start.
  Verify that both approval and launch use the live mode. Apply these
  checks to new and legacy launches and verify that the approval binds
  the command and owning session.
  Verify that descendant keys or unauthenticated launcher hello fields
  cannot confer hosted job ownership. Pin the Claude `can_use_tool` finding
  and verify one approval path.
- Measure syscalls/s on the owner's PC and account for cold-start and exit
  holder files. Verify that new hosted jobs create no per-job output files
  and that interactive paths stay off file I/O.

### Result (88b0a3a24) - 2026-11-22

- **Landed** (`8e47d3b8e..88b0a3a24`):
  - Schemas for `exec.run`, `exec.stop`, `exec.tail`, `exec.list` and
    `exec.write`, and the new `exec.shell {cmd}` form beside the legacy
    `{command, shell}` form. A precheck refuses mixed argument families.
    `exec.raw.read` gains an additive `evicted_before`.
  - Hosted jobs (`internal/execjob/hosted*.go`):
    - Each job runs under its own holder, with holder session kind `exec`.
    - Output stays in memory rings: 1 MiB per job, 32 MiB total, a 1 MiB
      holder ring, and 4 KiB previews.
    - Metadata goes to `exec-jobs.db` (SQLite). It is written at start, at
      conversion and at end, never per output.
    - The holder copies terminal status to `exec-exits/<id>.json`, outside
      its own directory.
    - Bounds: 200 jobs kept, 50 per `exec.list`, and a 30 s window that
      starts at spawn.
  - Holder: `--exit-copy`, plus finite stdin delivered atomically with the
    spawn (`input`, `input_eof`). The protocol stays at version 1.
  - Runner seams (`wsagent/exec_seams.go`):
    - `ExecOwner` is the trusted binding. `ProxyHello.Conversation` is
      tagged `json:"-"` and only `ResolveMCPToken` sets it.
    - `ApproveExec` and `ExecImmediate` implement the daemon-owned approval
      of Decision 4.
    - `AdmitCompletion` writes Steer inbox entries carrying the stable event
      ID `exec:<id>:exit`. Deduplication uses `Record.AcceptedEvents`
      (capped at 64) plus an inbox scan. `Record.OpenGen` is the fence and
      close bumps it. A Pi child is never revived; a handed-off session
      answers Deferred.
    - `MCPCallBegin` counts in-flight ws MCP calls on the token HTTP route;
      agy's turn-replacing steer waits for that count to reach zero.
  - MCP routing:
    - Only trusted-binding connections get hosted jobs. A `session_key`
      grants a root and cwd, never ownership.
    - Legacy launches on those connections become hosted jobs, keeping the
      legacy key (`exec-<ms>-<16hex>`) and reply.
    - Legacy readers look in the hosted jobs first (by root and key), then
      fall back to the file-backed records.
  - Daemon wiring (`cmd/ws-mcp/exec_hosted.go`):
    - A late-bound holder sink and an exec exit hook.
    - The store opens under the daemon lock after `StartDaemon`.
    - `Start` replays live job holders and ends gone ones from their exit
      copy. It also stops unowned exec holders and delivers pending
      notices.
  - For `261122-feat-ws-agent-turn-semaphore`:
    - The conversation-attribution seam (`HostedLaunch.Owner`, `ExecOwner`)
      and completion-delivery seam (`AdmitCompletion`, `CompletionOutcome`)
      are in place.
    - The semaphore itself is not implemented.
- **Decisions made within the ticket's Decisions:**
  - Phase 1 keeps the tools out of `tools/list`. Advertising them is part
    of the Phase 2 cutover, so Codex is not prompted twice before its
    approval config exists.
  - Converted legacy launches notify, like converted new launches
    (Decision 3).
  - Inside the window, an interrupt (the request context ending) stops the
    job, while a drain (daemon replacement) converts it. A stop suppresses
    the notice.
  - Shell resolution is Bash on the job's PATH (Git for Windows' bash on
    Windows), else `/bin/sh`. There is no config key for it.
  - A role or mode allow is rechecked against the live mode after the holder
    starts and before the spawn, and a mode change settles pending exec
    prompts. Preflight and approval run before the window starts.
  - The approval shows what decides the run: command, cwd, the program
    resolved on the job's PATH before the prompt, the env overlay, and a
    1 KiB stdin preview with the full length.
- **Compatibility, holder protocol** (directional, T0):
  - The protocol stays at version 1 (`wsholder/holder.go:58`,
    `holder.py:121`).
  - A newer daemon never sends the new surfaces to an older holder.
    `--exit-copy` (`holder.py:27`, `:883-993`; `manager.go:580-586`,
    `:656-657`) and the spawn fields `input`/`input_eof` (`holder.py:733`,
    `:1198`; `client.go:100-105`, `:312-344`) only reach the holder script
    the launching daemon staged itself (`StageScript`, `holder.go:134`). An
    older holder ignores unknown spawn fields, and an older daemon sends
    neither.
  - When an older daemon meets a newer holder, it attaches the holder and
    ignores the unknown session kind. Its stop and cleanup remove the
    holder directory and record nothing. The exit copy survives outside
    that directory, and the new daemon reads the terminal status from it on
    reopen (`TestHostedExecSurvivesAnOldDaemonAndReopens`,
    `TestHostedExecChildEndsUnderAnOldDaemon`).
- **Compatibility, restore data** (directional):
  - `exec-jobs.db`: T0. Only new daemons write and read it
    (`hosted_store.go:22`). The schema is `user_version` 1 and a newer
    version is refused (`:129-132`). Old daemons never open it.
  - The agent `Record` gains `open_gen` and `accepted_events`: T0. Older
    writers of the same minor version keep unknown members
    (`record.go:192-234`, since v0.50.0 `184452a15`; current 0.50.8).
  - The inbox `eventId`: T1. An older daemon's `Steer` rewrite
    (`wsinbox/inbox_store.go:245`) drops it. This causes no duplicate,
    because `AcceptedEvents` survives in the record and the job's store row
    marks the notice delivered.
  - MCP:
    - The new tools and the new `exec.shell` form are additive and hidden.
      Older servers refuse the new arguments at precheck, so Phase 2 has to
      gate them on capability.
    - The eight legacy calls, both legacy argument families, old
      file-backed records and raw byte offsets are unchanged
      (`TestHostedExecOldFileBackedRecordsStayReadable`).
    - The hosted legacy timed reply has the same fields as the file-backed
      5 s reply. An older client's follow-up status, result and abort work
      on both (`TestHostedExecLegacyTimedReplyMatchesTheFileBackedShape`).
- **Lite review:** no critical findings. Both important findings were fixed
  in `88b0a3a24`:
  - A stale running row could overwrite a terminal row. Row writes are now
    ordered by a per-job revision.
    `TestHostedLateRunningRowNeverOverwritesTheEnd` fails on every run
    without that ordering.
  - The approval hid env, stdin and the resolved program. It now shows all
    three, and the program resolves before the prompt.
  - Of 5 minor findings, one is fixed: pruning keeps jobs whose notice is
    still pending. The others are listed under Known limits.
  - Coverage notes: a test now shows an answer from another conversation is
    refused, and the link test's doc comment was corrected.
- **Known limits** (minor, unfixed):
  - Approval waits watch the request context, not the drain. An unanswered
    prompt holds a daemon replacement until the drain deadline.
  - When the harness itself reports a mode change in-stream
    (`runner_events.go`), pending exec prompts are not settled. The live
    recheck before spawn still applies.
  - After a daemon restart, output offsets drift in two cases:
    - A running job with more than 1 MiB of output resumes offsets at the
      oldest record left in the holder ring.
    - A finished job keeps only its 4 KiB previews, and grep line numbers
      restart at those kept bytes.
  - Legacy hosted stdin beyond the holder's 8 MiB unread-input limit, given
    to a child that does not read it, records a process that did start as
    failed.
- **Verification:**
  - Run from `agents-plugin-tool/`; each Go command was prefixed with
    `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`. Every command passed
    except the one pre-existing failure noted below.
  - execjob, textreader and holder:
    - `go test ./internal/execjob/ -count=1` (24.4s).
    - `go test ./internal/execjob/ -run TestHosted -race -count=1`.
    - `go test ./internal/execjob/ -run 'TestHostedLateRunningRowNeverOverwritesTheEnd|TestHostedSyncEndInsideTheWindow' -count=20`.
    - `go test ./internal/textreader/ -count=1`.
    - `go test ./internal/wsholder/ -run 'TestLaunchExitCopySurvivesCleanup|TestSpawnDeliversFiniteInput|Restart|ExitCleanup|OlderProtocol|Stag'`.
    - `python3 -m unittest tests/test_holder.py`, run in `internal/wsholder`
      (40 tests OK).
  - wsagent:
    - `go test ./internal/wsagent/ -run 'TestExecApproval|TestExecImmediatePosture|TestAdmitCompletion|TestAgyNoticeWaitsOutTheInFlightMCPCall|TestClaudeLeadAllowsTheWholeWSServerInEveryMode|TestClaudeLeadArgs'`.
    - `go test ./internal/wsagent/ -run 'Approval|Steer|Close|Bypass|Permission|MCP|PiSend|Reopen|Revive|Inbox|Held'` (22.4s).
    - `go test ./internal/wsagent/ -run 'Approval|Exec|AdmitCompletion|Bypass|Permission|TestAgyNoticeWaitsOutTheInFlightMCPCall'`.
  - wsinbox and wsdashboard:
    - `go test ./internal/wsinbox/ -count=1`.
    - `go test ./internal/wsdashboard/ -run 'TestProxyHTTP|TestProxyHello' -count=1`.
  - mcp:
    - `go test ./internal/mcp/ -run 'TestHostedExec|TestPrecheckExecShellArgumentFamilies|TestExecToolsListNoAgentAndMCPFlow|TestExecMCPRunningLargeAndAbort|TestExecMCPResultReadableJSONStdoutAndTimeout|TestDrainEndsAnExecResultWaitAtOnce|TestHostedServerEnvAgentMode|TestHostedToolsRunGitOnTheConnectionPATH|TestHostedServersReadOnlyTheirOwnEnvironment|TestCallAgentlessGate'` (36.0s).
  - cmd/ws-mcp:
    - `go test ./cmd/ws-mcp/ -run 'TestHostedExec|TestHarnessKillSurvivalFixture'`.
    - `go test ./cmd/ws-mcp/ -run 'Holder|TestHostedWorkerOverHTTP|Proxy|RuntimeCapabilit|TestToolsCommand' -skip TestProxyWhiteboardLinkHasNoLoginCode`.
  - Pre-existing failure, not caused by this phase:
    `TestProxyWhiteboardLinkHasNoLoginCode` in `cmd/ws-mcp` fails the same
    way on base `8e47d3b8e`, checked on a read-only export of the base.
  - `go build ./...` passed. `go vet` was clean on execjob, wsholder,
    wsagent, wsdashboard, wsinbox, mcp, textreader and cmd/ws-mcp. gofmt was
    clean on every changed file.
- **Not verified here:**
  - Windows: process-tree stop, Git Bash resolution, and raw Windows bytes.
    Per Constraints these run on the owner's Windows host.
  - syscalls/s on the owner's PC. By construction:
    - New jobs create no per-job output files
      (`TestHostedExecRunAndShellForms` checks this).
    - The file touches are the holder's cold-start and exit files, plus
      three store writes per job.
  - Live completion steer on Codex and Pi. Both use the same Steer entry
    path that the existing steer tests cover. Running, idle and next-turn
    delivery are covered on the fake backend and agy's gate
    (`TestAdmitCompletion*`, `TestAgyNoticeWaitsOutTheInFlightMCPCall`).
  - Two parts belong to `261122-feat-ws-agent-turn-semaphore`:
    - The slot-grant half of the agy gate fixture: cancel before a pending
      grant, and no second slot count from a late result.
    - Showing that slot re-acquisition does not consume the window.
  - The Claude `can_use_tool` finding is pinned through the launch
    arguments only, with no live CLI run. `--allowedTools mcp__ws` is
    passed in every mode, so the daemon's prompt is the only approval
    path.
  - The binding is tested in two halves: `ResolveMCPToken` sets it, and the
    token route carries it to the jobs. No single test runs both halves end
    to end.

#### Edition (f70eacba7) - 2026-11-22

Corrects the two post-restart items under Known limits above, checked
against the source at this edition:

- The running-job item no longer holds. Holder output events carry absolute
  offsets (`wsholder/client.go`, `Offset` and `CombinedOffset`). A rejoin
  replays them through `OutputRings.AppendAt` (`execjob/hosted.go:401-413`,
  `execjob/hosted_ring.go:162-188`), which counts the bytes the holder no
  longer has as written and evicted. Offsets stay absolute, and a read below
  them reports the gap instead of resuming at the oldest ring record.
- The finished-job item keeps its preview-only limit: a finished job keeps
  only its 4 KiB previews.
- New remaining limit: absolute totals are lost when the job and its holder
  both finish while the daemon is absent. The holder's exit totals
  (`stream_bytes`, `output_bytes`, added in `73859f4ed`) travel only on the
  live exit event. The exit copy and `exit.json` still store status alone
  (`wsholder/holder.py:1003-1024`, `write_exit_record`; `wsholder.Exit` in
  `wsholder/manager.go`). `endFromExitCopy` (`execjob/hosted.go:284`) and
  `endFromExit` (`execjob/hosted.go:358`) end such a job from status alone,
  so its byte counters are not advanced.

### Phase 2: Harness cutover, report hold, and session and worktree lifecycle

After Phase 1, add the exec tools to worker allowlists and then enable verified
per-harness native process tool removal with its rollout switch and exclusions.
For Codex, implement and advertise the three read tools from Decision 6 and
allowlist them for workers before disabling shell on start or resume.
Preapprove each worker role's permitted exec tools and the three read tools
in Codex's own MCP approval config before disabling shell for that profile.
Apply the (1506) replacement exclusion when a version refuses these calls
for the profile, so the profile keeps native shell.
Use the existing config registry and settings catalog for the rollout switch.
On agy, enforce native default mode at startup, during live settings changes,
and on reconciliation. Apply daemon `reject_once` to live and replayed
`run_command` permission requests. Preserve daemon-side bypass for other
requests. Correlate executions that lack a request and deliver the instruction
at the turn boundary. Append the creation-time instruction and remove the
old agy background prompt rules after cutover.

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
  and unverified versions. Verify that the Pi bridge and Claude stdio fallback
  retain native tools when the trusted binding is absent.
- Verify a hosted Codex lead outside bypass/full can read, search and list
  without an approval prompt after shell is disabled. Exercise worker
  allowlist ordering and confirm the tools are absent from non-Codex
  inventories and Codex sessions outside cutover. Verify direct calls
  from those sessions are refused. Cover line offsets, glob filters,
  grep line numbers, matching readable-path reach, binary read refusal
  and binary grep skipping. Check the 4 KiB result budget, truncation
  signals and the chosen line, match, entry and scan caps.
- Switch a running hosted Codex lead from `full` to a prompt-required
  mode and back. Verify shell stays off and each `fs.*` call stays
  callable without a prompt, with no thread reopen.
- For a hosted Codex worker, verify a role-permitted exec call and each
  `fs.*` call reach ws without a Codex-side prompt or refusal. Cover
  read-only workers with approval policy `never` and verify that a
  version refusing a replacement call leaves shell available for that
  profile.
- Verify agy default launch on new, resumed, and reattached sessions with
  a saved non-default mode, the `reject_once` exception under bypass and worker
  auto-approval, and mode changes and reconciliation during the process
  lifetime. Include replayed approvals. Exercise foreground and daemon jobs,
  an announcement followed by a permission request for the same tool ID,
  and actual execution without a request. Verify the warning and single
  instruction prompt at turn end, including replay and a rejection with
  empty output. Cover the advisory-only native subagent path and update
  both prompt regression tests in Decision 18.
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
