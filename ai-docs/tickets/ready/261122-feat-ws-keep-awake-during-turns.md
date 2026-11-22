---
title: "턴이 도는 동안 컴퓨터 절전 막기 (선택 설정, 기본 꺼짐)"
related:
  261120-feat-ws-agy-timeout-process-respawn: "related agy stall recovery"
  261122-feat-ws-agent-turn-semaphore: "shared runner lifecycle with distinct power and admission lifetimes"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: fcef8c4a26a6dd8f
sage-review-completeness-reviewed: fcef8c4a26a6dd8f
---

# Keep the host awake while hosted turns run (opt-in, default off)

## Background

The 2026-11-22 agy stall investigation reported that multiple live model
streams broke together at macOS sleep and dark-wake transitions recorded in
the OS power log. An idle-sleep assertion addresses automatic idle sleep.
Lid close remains under OS control.

## Decisions

- **(1536) Owner chose opt-in, default off.** An enabled daemon holds one
  idle-system-sleep assertion while at least one hosted lead or spawned
  agent turn is live. Always-on would change the power behavior of every
  installation. Omitting the feature would leave users to manage idle sleep
  during hosted work.
- **(1537) Runner turn lifetime.** Track live turns by conversation
  instance in RAM. The Runner's turn boundaries cover hosted leads,
  `agent.spawn` children including nested children, and already-running
  turns rejoined through the holder. A live turn remains counted through a
  tool call, approval, or retry within that turn. Count on admission or an
  unsolicited turn start from the harness.
  Queued input, idle hosted sessions, terminal sessions, and work that
  outlives a turn by itself, including detached exec jobs, leave the count
  unchanged. Decision S4 in `261122-feat-ws-agent-turn-semaphore` specifies
  yielding a slot around MCP calls, so the assertion needs the Runner's
  turn state.
- **(1538) Live setting and best-effort backend.** Keep the authoritative
  setting and current turn membership in memory. Load the global value at
  daemon startup and refresh it after successful MCP tuning or dashboard
  settings writes, including reset. Changes reconcile against turns already
  in progress. An assertion error leaves turn execution and the saved
  setting usable. Log a failure once for that assertion attempt and try
  again on the next transition to enabled with live work. A failed or lost
  assertion must leave teardown safe. Keep process launch, OS calls and
  waits off the conversation loop.
- **(1539) One shared core setting.** Declare
  `agent.keep_awake_during_turns` as a global-only, lead-writable boolean
  with default `false`, no selectors and an Agents settings toggle. Follow
  the core registry and scope pattern of `agent.child_waiting_days`. The
  daemon serves every project and harness, so it needs one value independent
  of the starter's project or package. Use the core registry as the
  canonical source shared by ws, wsflow, and Pi.
- **(1540) T0 configuration addition.** The new daemon stores the setting
  as a string-encoded boolean in the existing global `overrides` map.
  The prior daemon has no registry entry for this key. It decodes the entire
  string map and preserves this entry when writing a different key. On
  reopen, the new daemon reads the retained value from the global layer and
  falls back to the builtin value when the key is absent. The wire
  schemas, manifests and agent records retain their existing shapes.
  See `agents-plugin-tool/internal/wsconfig/config.go:34-40,783-800` and
  `agents-plugin-tool/internal/wsconfig/resolver.go:150-161,331-343`.
- **(1541) Platform backends.** On macOS, use the system `caffeinate`
  with `-i` and `-w` targeting the daemon. Manage only that helper's
  lifecycle and reap it before starting another. This uses the existing
  cgo-free release build
  (`agents-plugin-tool/scripts/build-release-assets.sh:41-49`) without a
  new foreign-function dependency. On Windows, set
  `ES_CONTINUOUS | ES_SYSTEM_REQUIRED` and clear with `ES_CONTINUOUS`
  on the same dedicated locked OS thread. On Linux and other platforms,
  use a no-op backend with a debug diagnostic when first requested. The
  help text names idle system sleep and supported platforms. Display
  sleep and user-requested sleep, including closing a laptop lid, remain
  under OS control.
  [Apple IOKit declaration](https://github.com/apple-oss-distributions/IOKitUser/blob/main/pwr_mgt.subproj/IOPMLib.h),
  [Apple caffeinate source](https://github.com/apple-oss-distributions/PowerManagement/blob/main/caffeinate/caffeinate.c),
  [Microsoft execution-state documentation](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-setthreadexecutionstate).
- **(1542) Admission and send rollback.** Publish membership once the
  Runner admits a turn, before attempting its prompt or command send.
  Carry that membership into the live turn on success. Roll it back if
  the send fails or produces no turn, even when the error path returns
  before the normal turn-end handler. Counting only at the harness's
  turn-start event would leave standalone sends uncovered.
- **(1543) Startup config ordering.** Reconcile the global value after
  the daemon endpoint is published and can accept invalidations. An
  MCP writer may commit a disable or reset while the endpoint is
  unavailable. A final serialized startup read observes the committed
  value even if the writer could not deliver its notification.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-pi/config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)
- Use existing global config storage (performance Type 5). Turn activation,
  release and overlap checks add zero application file operations to the
  runner path. The macOS helper starts only on a needed assertion and
  runs off that path.
- Use fixtures for lifecycle verification. Real agy, Electron and shared
  daemon lifecycle operations are outside the verification scope.

## Prior Decisions

- 261122-feat-ws-agent-turn-semaphore (2026-11-22, Decisions): "Keep a turn-granular semaphore in daemon memory for each harness backend. Count model-active worker and hosted lead turns." — bearing: constrains
- 261113-bug-ws-pi-subagent-turn-dies-on-daemon-restart (2026-11-13, Decisions): "Dev reruns and release replacements rejoin the same running turn." — bearing: constrains
- 1809708e (2026-11-22, commit): "Hosted leads derive lifecycle from runner state, so heartbeat staleness alone (machine sleep) no longer folds them into pending deletion." — bearing: constrains
- 261117-feat-ws-agent-tools-mailbox-routing (2026-11-18, Result): "Lifecycle: finished children no longer auto-close; they sleep on the idle timer." — bearing: constrains
- f4144db5 (2026-06-25, commit): "Added global-only resolver enforcement for workflow preferences so keyless bootstrap/tool-surface reads cannot diverge from session or project state." — bearing: supports
- d347e280 (2026-11-21, commit): "Reuse the MCP registry, manifest parser, scalar normalization and existing config writers to retain wire behavior and on-disk shapes." — bearing: supports
- 261117-feat-ws-exec-tools (2026-11-17, Decisions): "Async and converted jobs survive interrupt, harness restart, handoff, compaction, and daemon replacement." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent/runner_conv.go, agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-catalog.json |
| scope.surface | cross-module | daemon turn lifecycle, operating-system assertion and settings cross package boundaries |
| scope.new_public_symbol | yes | agent.keep_awake_during_turns is exposed through MCP tuning and dashboard settings |
| scope.new_type_contract | yes | the opt-in setting has a boolean on/off contract and default off |
| scope.test_surface | existing | wsagent/lead_runner_test.go, wsagent/lead_rejoin_test.go, wsagent/worker_rejoin_test.go, mcp/dashboard_settings_config_test.go and wsdashboard/config_routes_test.go provide relevant fixtures |
| complexity.reuse_points | confirmed | agents-plugin-tool/internal/wsagent/runner_conv.go and runner_events.go hold and update turn state |
| complexity.side_effect_risk | high | system power assertions and process or thread lifecycle affect the host |
| risk.correctness | high | overlapping turns, setting changes, shutdown and reattachment must balance assertions |
| risk.fit | high | hosted and child turn boundaries span multiple harnesses and daemon recovery |
| risk.test | high | concurrent lifecycle and platform behavior need deterministic fakes and platform checks |
| risk.security_or_contract | moderate | additive machine-wide configuration requires cross-version and scope validation |

## Phases

### Phase 1: Daemon assertion and setting

Add the core setting, global scope, catalog row and label and description
in every dashboard locale. Use existing config write notifications:
`mcp.notifyScalarTune` after set and reset, and the dashboard's
`ConfigBackend.Changed` after a successful mutation. Route both to the
daemon's reload against its own global config, following the authenticated
same-config notification pattern in `wsdashboard/companion_config.go`.
Serialize reload and publish so concurrent writes and notifications cannot
publish an older value after a newer one. Use the same serialization for
the startup reconciliation after endpoint publication and for later
invalidations. Read config on startup and invalidation only.

Use the runner's in-memory turn start and end boundaries
and its holder reattach and reconciliation (`startConvWith`, `replayDone`).
Cover ordinary input and command sends in `kickInput` and sends through
`sendStandalonePrompt`, including compaction prompts and restore. If a
semaphore gates a send, publish only after grant and revalidation. Preserve
membership across a successful send and the harness start event. Roll it
back on failures including the early return for a failed `/compact`.
Standalone native compaction settles through its command lifecycle when
there is no separate prompt turn event. Restore an attached, still-live turn
from trusted recovery. Publish the reconstructed current state and follow
the Runner's conservative live state when replay is incomplete. Release
membership on turn completion, transport loss, close or handoff. Fence late
events by conversation instance and remove membership when its loop exits.

Serialize transitions against the desired state of one daemon-wide
assertion. Acquire on the first live turn while enabled or when enabling
with live turns. Release on the last live turn, disable or reset, or
Runner shutdown. Keep the backend lifetime independent from any one
conversation. Drain the backend during shutdown so a late acquisition
cannot survive disable or shutdown.

Verification

- Use a fake assertion backend and hosted lead and spawned-worker fixtures
  to verify first start, overlap, normal end, interrupt, EOF, failed send,
  close and shutdown. Pause a standalone send inside the fake transport to
  verify that it already has membership,
  and that a failed standalone send and a failed ordinary `/compact`
  release it. Exercise standalone command completion without a separate
  prompt turn event. Cover an active turn during a tool or approval wait,
  an idle conversation, queued input and a native turn start. Exercise
  holder rejoin for a live turn and for a turn finished during downtime,
  with duplicate and late events unable to change the current count.
- Verify the default and global-only access through MCP and dashboard
  settings. While a turn is live, exercise enable, disable and reset on
  each write path. Test overlapping change and turn notifications, backend
  acquisition failure and retry after a new activation. Commit a reset or
  disable in an MCP fixture before the daemon endpoint is discoverable and
  verify that startup reconciles the newer value. Verify that a
  sibling override written using the previous config writer retains the
  key and that the new daemon reads the persisted value on reopen.
- Run package, registry, settings and translation coverage from
  `ai-docs/manuals/config-settings-ui.md`. Run `GOOS=windows CGO_ENABLED=0
  go vet ./internal/wsagent/...` from `agents-plugin-tool/` and verify
  the Windows set and clear calls use the same locked thread. Check the
  macOS backend with an isolated fixture and read-only
  `pmset -g assertions` while it holds and after it releases the idle
  assertion. Record platform evidence in the Phase 1 Result.
