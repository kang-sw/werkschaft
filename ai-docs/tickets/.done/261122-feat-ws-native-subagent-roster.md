---
title: "네이티브 서브에이전트 시작·종료·상태를 채팅 상단 에이전트 목록에 표시"
related:
  261122-feat-ws-harness-native-subagent-preference: "steers agy leads toward native subagents, which this ticket makes visible; independent merge order"
  261118-feat-ws-agy-tool-names-and-worker-role: "agy native subagent tools stay available to hosted agy leads (Decisions at :119-126)"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 42d1950ac50f2d4b
sage-review-completeness-reviewed: 42d1950ac50f2d4b
completed: 2026-11-22
---

# Show native subagent start, end, and status in the chat's top agent list

## Background

Hosted agy leads are now steered to explore with native subagents
(`261122-feat-ws-harness-native-subagent-preference`). Those subagents are
invisible on the dashboard: the chat page's top agent list (the HUD) shows only
daemon-spawned `agent.spawn` workers and the subagents a harness already
reports. The owner asked for native subagents to appear in that list with their
name, start and end, and current state, through a common interface. Drilling
into a native subagent's transcript is out of scope.

## Current Behavior

- Common row path exists. `wslive.Agent` (`internal/wslive/snapshot.go:324-350`)
  carries `ID`, `ParentID`, `Label`, `Status`, `StartedAt`, `EndedAt`, optional
  `ReplyID`. Adapters emit `wslive.Event{Kind: KindAgent, AgentID, Status,
  Label}` through `Event.Live`; the HUD projects `Snapshot.Agents`
  (`internal/wsdashboard/pageview_session.go:327-328`,
  `pageview_hud.go:23-85,149-178`, `assets/dashboard.tmpl:232`,
  `assets/dashboard-hud.js:24-32,298-322`). The HUD folds idle, non-alive
  rows into a completed count (`pageview_hud.go:65-81,149-155`); a row with a
  `ReplyID` and a live link navigates to `/s/<id>`, others open details.
- Claude: already emits a running `KindAgent` row on `local_agent`
  `task_started` (label = description) and settles it on the first terminal
  `task_updated`/`task_notification` (`internal/wsagent/claude_events.go:131-188`).
- Pi: the ws Pi extension's `setWidget("ws-dashboard-agents")` rows are already
  forwarded as `KindAgent` (`internal/wsagent/pi_events.go:350-379`,
  `pi_event_details.go:51-76`).
- Codex: the app-server stream carries parent-side `subAgentActivity` items
  (`item/started` and `item/completed` envelopes per activity, `kind:
  started|completed`, keyed by `agentThreadId`, with `agentPath`)
  (`internal/wsagent/testdata/codex-0.160.0-subagent-baseline.jsonl:45-68`,
  `codex-0.160.0-resume.jsonl:198-217`). The mapper ignores this item type and
  drops foreign-thread traffic (`codex_mapper.go:171-172,218-234,358-437`).
- agy: `invoke_subagent` arrives as an ordinary ACP `tool_call` with
  `toolCallId` and args `Prompt`, `Role`, `TypeName`; its `tool_call_update`
  completes at once ("I have launched the research subagent"), so the child
  runs asynchronously. `manage_subagents Action=list` returns no structured
  fields. No child ID, progress, terminal event, or end time is observed; the
  lead relays the child's answer as its own `agent_message_chunk` before
  `end_turn` in the same prompt
  (`internal/wsagent/testdata/agy-1.5.9-subagent-baseline.jsonl:17-31`).
  The adapter handles it only as a generic tool call
  (`agy.go:1337-1370,1418-1451,1494-1519`).
- Hosted-lead launch policy removes native subagents for Claude
  (`--disallowedTools` Agent/Task, `claude.go:153-170`) and Codex
  (`agents.enabled=false`, `codex.go:256-279`); agy keeps its tools and is
  prompt-steered (`hosted_prompt.go:37-55`, `agent_tools.go:21-28`,
  `runner_process.go:176`). So in normal hosted use, rows come from agy and Pi
  leads; Claude and Codex mappings matter where native subagents run.
- Disk: `Snapshot.ForDisk()` strips only the lead reasoning summary
  (`internal/wslive/snapshot_disk.go:3-8`), so agent rows persist to the
  recovery cache. Unknown `AgentKind` values are dropped on read
  (`wslive/state.go:715-720`); restore seeds only finished rows
  (`wsagent/record.go:425-442`, `wslive/state.go:1032-1063`). Holder replay
  re-maps retained raw lines through the adapter (`wsagent/stream.go:169-205`).
- Wake and turn accounting: a widget `KindAgent` event carries no
  turn/reset/batch authority (`wsagent/wake.go:168-173`); batch status
  recomputation keys on `KindAgent` with an empty `AgentID` plus `Batches`
  (`wsagent/runner_conv.go:1197`).

## Prior Decisions

- `261118-feat-ws-agy-tool-names-and-worker-role` (149): agy native subagent
  tools stay; agy *workers* are told not to use them, with no daemon-side
  detection (`ai-docs/tickets/ready/261118-feat-ws-agy-tool-names-and-worker-role.md:119-128`).
  This ticket observes native subagents of hosted agy *leads* only and adds no
  detection, refusal, or prompt change for workers.
- `261122-feat-werkschaft-tray-notifications-full-quit` (2146): sessions whose
  adapter reports an unknown background count use the turn-ended fallback, and
  agy keeps an advisory-only native subagent path
  (`ai-docs/tickets/ready/261122-feat-werkschaft-tray-notifications-full-quit.md:41`).
  This ticket does not retire that fallback for any adapter.

## Decisions

1. Common interface: every native subagent row enters through the existing
   `Event.Live` `KindAgent` path with a non-empty `AgentID`, a `Label`, an
   existing `Status` value (`running`, `done`, `failed`;
   `wslive/snapshot.go:107-109`), and `StartedAt`/`EndedAt`. No new live event
   kind, no new persisted field, no new status value. Each adapter maps its own
   protocol at its boundary; `batchTracker` is not used for extraction, because
   it works on previews and force-closes calls at turn boundaries.
   Rejected: a separate native-roster channel (a second rendering path for the
   same HUD rows); parsing lead prose for child results (unattributable).
2. Row identity is namespaced so it cannot collide with daemon-spawned or other
   rows: agy `agy-native:<toolCallId>`; Codex `codex-native:<item id of the
   started activity>`. Claude and Pi keep their existing IDs.
3. agy mapping, hosted leads only:
   - Scope: only the hosted agy lead conversation maps native subagents; the
     agy mapper also serves workers (`wsagent/agy.go:178-206,1418-1451`), and a
     worker conversation emits nothing new (Prior Decisions, 149). The tool
     name is an adapter-local constant in the agy backend; the catalog's unset
     native-subagent removal entry stays unset, because filling it activates
     the "Never use" hosted-prompt line (`wsagent/catalog.go:532-559`,
     `wsagent/hosted_prompt.go:16-55`).
   - Start: an `invoke_subagent` `tool_call` creates a `running` row. Label:
     `Role`, else `TypeName`, else `subagent`. `Prompt` is never shown or
     stored.
   - Launch failure: a `tool_call_update` for that call ID with status
     `failed` ends the row `failed` with its observed end time.
   - Observation end: agy gives no child terminal event. When the runner
     accepts the end of the parent's logical turn for any reason (end of turn,
     cancellation, error), every still-running agy native row of that
     conversation ends with status `done` and **no `EndedAt`**: the
     live event carries an end-unknown flag that tells the reducer not to stamp
     the end time or add `ActiveMs` (`wslive/state.go:666-687`). A terminal
     native row without `EndedAt` is the persisted, restore-safe form of
     "observation ended, completion unknown"; the HUD renders it as such and
     never as a completion. Settlement happens in the runner at the accepted
     logical turn end, not in the mapper's per-prompt turn-end routine: the
     runner can intercept a prompt end and retry within the same logical turn
     (watchdog continue, `wsagent/agy.go:1274-1320`,
     `wsagent/runner_events.go:133-158,756-765`, `wsagent/agy_watchdog.go:373-400`),
     and the monotonic rule (Decision 6) would make an early settlement stick.
     Holder replay marks re-applied lines quiet and the quiet path skips the
     watchdog interception before `endTurn` (`wsagent/holder.go:255-270`,
     `wsagent/runner_events.go:17-24`), so a replayed turn end never settles
     agy native rows. Rows still open when replay completes settle end-unknown
     at that moment, whatever the turn state: observation is not continuous
     across a daemon replacement, the outcome is unknown either way, and a
     replayed cancellation cannot be told apart from a watchdog continuation
     still awaiting its first output (the open-prompt recovery hint exists only
     for workers, `wsagent/agy.go:744-758`, `runner_process.go:178-184`).
     Rejected: inferring idle from replayed turn state (ambiguous; a wrong guess
     keeps a row running or settles it early with no better outcome).
     Rationale: the only capture shows the lead
     relaying the child's answer before `end_turn`
     (`testdata/agy-1.5.9-subagent-baseline.jsonl:17-31`), and an unbounded
     `running` row would hold the HUD and the rail's busy state forever.
   - Process exit and reset keep today's generic handling: exit fails running
     rows (`wsagent/runner_events.go:805-809`, `record.go:586-593`), reset
     clears the tree (`wslive/state.go:309-313`).
   - `manage_subagents` is not polled and its output is not parsed.
   Rejected: keep agy rows `running` until a signal that does not exist;
   settle on the first later lead text chunk; stamp an inferred end time.
4. Codex mapping:
   - Admit only parent-thread `subAgentActivity` items; other foreign-thread
     traffic stays dropped.
   - Dedupe by the activity's item ID: the `item/started` and
     `item/completed` envelopes of one activity are one transition
     (`testdata/codex-0.160.0-subagent-baseline.jsonl:46-47,66-67`). Use the
     envelope timestamp (`startedAtMs`/`completedAtMs`) as the transition time.
   - `kind: started` opens row `codex-native:<item id>`, label the basename of
     `agentPath`, and records `agentThreadId -> row` in mapper state.
     `kind: completed` ends the open row for that thread `done`; a completion
     with no known open row is dropped.
   - These items bypass the current-turn gate, so a completion that arrives
     after the parent turn settled, or after a replay gap, still applies
     (`codex_mapper.go:171-172,216-234,358-367,465-466`).
5. Claude and Pi keep their current mappings. Claude's `task_progress` caption
   is out of scope.
6. Replay and restore: native transitions are monotonic. In the reducer, a row
   whose ID carries a native prefix from Decision 2 and is already terminal
   ignores a later `running` and a later terminal event for the same ID, so a
   restored finished row plus replayed holder lines
   (`record.go:425-442,503-511`, `holder.go:255-270`,
   `wslive/state.go:1041-1058`) keeps its status, `EndedAt` (or its absence),
   and `ActiveMs` unchanged.
   Retention: the finished-row cap ranks rows by descending `EndedAt`
   (`wslive/state.go:505-506,1013-1018,1048-1069`); a row without `EndedAt`
   ranks by its persisted idle-phase timestamp (the phase start set when it
   went idle at settlement, `wslive/snapshot.go`, `wslive/state.go:880-889,910-917`)
   instead, while `EndedAt` stays absent, so a just-settled end-unknown row
   ranks as the newest finished row at settlement and at restore.
7. HUD and presentation:
   - A row is native when its ID carries a Decision 2 prefix, or when it sits
     in a Claude lead's snapshot with no daemon identity (empty `Harness`,
     `ReplyID`, and `AgentKind`; daemon-spawned rows always carry one,
     `wsagent/spawn_live.go:30-36`). Pi extension rows keep their current
     presentation.
   - Additive `HUDNode` fields `native` (bool), `startedAt`, `endedAt` (ms).
     Native rows show a native badge, resolve harness from the parent as today
     (`pageview_hud.go:241-252`), never navigate to a transcript, and show
     start, end, and state; a terminal native row without `endedAt` reads
     "observation ended, completion unknown" (localized).
   - Running native rows stay in the compact HUD. Completed native rows fold
     into the completed count as today (`pageview_hud.go:65-81,149-178`); the
     count opens the expanded tree with its ended group
     (`assets/dashboard-composer-status.js:182-187`, `assets/dashboard.tmpl:232,238`),
     and that group carries the same lifecycle details.
   - Unknown outcome is never signalled as completion: end-unknown native rows
     do not add to the compact completed count; the HUD view carries them in a
     separate additive count rendered as "observation ended" (localized), and
     they stay accessible in the expanded ended group. The companion relay,
     which reads disappearance into the ended group or a rise in the completed
     count as `subagent.done` (`assets/dashboard-companion-relay.js:79-97,126-145`),
     emits no `subagent.done` for them (`pageview_hud.go:65-81,149-155,214-229`).
8. Lead state and notifications:
   - Native rows carry no wake, turn, reset, or batch authority. A non-empty-ID
     `KindAgent` already bypasses those handlers
     (`wsagent/runner_events.go:51-158`, `runner_conv.go:1197-1207`); the new
     mappers emit no `KindWake`.
   - A running native row counts toward the rail's busy state like any running
     agent row (`wsdashboard/pageview_rail.go:223-245`): the owner's
     fully-idle notification rule waits for children
     (`261122-feat-werkschaft-tray-notifications-full-quit` 2090). agy rows end
     at the parent turn's end, so they never hold busy past it. The
     unknown-background-count fallback (Prior Decisions, 2146) is unchanged.
9. Persistence: native row events publish RAM-only through the live slot and
   never call the recovery writer themselves; the ordinary hosted `KindAgent`
   path calls `writeSnapshot` on each change (`wsagent/runner_conv.go:719-725`,
   `record.go:566-577,625-665`), so native events take a RAM-only branch and
   the rows reach disk at the existing heartbeat and turn-end writes. No new
   file. `ForDisk()` is unchanged.
10. Launch policy is unchanged: Claude and Codex hosted leads keep native
    subagents disabled; enabling them is a separate owner decision outside this
    ticket.
11. Compatibility: T1 display-degraded. Additive HUD JSON served by the same
    daemon that renders it and an internal runtime event flag. Persisted agent
    rows keep their shape and existing status values and survive restore
    unchanged (`wslive/state.go:1041-1058`); an older daemon reading a recovery
    cache from this one shows an end-unknown agy row as an ordinary `done` row
    with a check mark (`assets/dashboard.tmpl:238`) and changes no data. No
    MCP tool, argument, `/mcp` hello, tracker event, or manifest field changes.
    T1 permits a patch (`ai-docs/manuals/release-compatibility.md:54-64`).

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `wsagent/agy.go`, `wsagent/codex_mapper.go`, `wslive/state.go` (end-unknown flag, monotonic native rows), `wsdashboard/pageview_hud.go`, `assets/dashboard-hud.js`, `assets/dashboard-composer-status.js`, `assets/dashboard-companion-relay.js` (+ css, i18n), `wsagent/runner_conv.go` (RAM-only branch) |
| scope.surface | internal | Additive HUD JSON and a runtime event flag; persisted rows keep their shape (T1: older daemon shows end-unknown as done); no MCP, hello, tracker, or manifest change |
| scope.new_public_symbol | no | No exported API beyond internal struct fields |
| scope.new_type_contract | no | Existing `KindAgent` event contract reused |
| scope.test_surface | existing | agy and Codex fixture tests, `wslive` snapshot tests, `wsdashboard` HUD projection tests, dashboard JS checks |
| complexity.reuse_points | confirmed | `KindAgent` row path (`claude_events.go:131-188`), HUD projection and fold (`pageview_hud.go:65-178`), holder replay re-map (`stream.go:169-205`) |
| complexity.side_effect_risk | low | No new file I/O, process, or poll; rows ride the existing RAM slot publication |
| risk.correctness | moderate | Duplicate envelopes, replay over restored rows, agy end-unknown settlement, lead-only scoping |
| risk.fit | moderate | agy and Codex native protocols are unversioned; unknown shapes yield no row |
| risk.test | moderate | Fixture-driven; agy end behavior beyond the single capture is unverified |
| risk.security_or_contract | low | agy `Prompt` never enters a row; no credential surface |

## Constraints

- Read before editing: `ai-docs/manuals/shipped-surface-boundary.md`
  (`agents-plugin-tool/`), `ai-docs/manuals/release-compatibility.md`
  (`agents-plugin-tool/internal/`), `ai-docs/manuals/performance.md`
  (`agents-plugin-tool/`).
- No new file I/O on the event path; rows publish through the existing RAM
  slot.
- Never run the real `agy` binary, never read agy credential or state files,
  never launch Electron. Fixture tests only.
- Tests: `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local`, narrow `-run` filters;
  never the whole `./internal/mcp` or `./internal/wsdashboard` package.
- Out of scope: drill-in to native transcripts, Claude `task_progress`
  captions, enabling native subagents for Claude or Codex hosted leads, any
  `manage_subagents` polling.

## Phases

### Phase 1: Native subagent rows in the HUD

Implement Decisions 1-11.

Verification:
- agy fixture (`agy-1.5.9-subagent-baseline.jsonl`) through a hosted lead
  conversation: exactly one row `agy-native:call_17051178851859014051`, label
  `Kiwi Responder`, `running` after the tool call, then `done` with no
  `EndedAt` and unchanged `ActiveMs` after `end_turn`; the row never contains
  the `Prompt`.
- The same fixture through an agy worker conversation produces no native row.
- agy: a `TypeName`-only and an args-less `invoke_subagent` get labels
  `TypeName` and `subagent`; a failed `tool_call_update` ends the row `failed`
  with an end time; a cancelled parent turn ends open rows end-unknown; a
  prompt end the runner intercepts for a watchdog continue leaves rows
  `running` until the logical turn's accepted end.
- Codex fixture (`codex-0.160.0-subagent-baseline.jsonl:45-68`): one row
  `codex-native:<started item id>`, label `ok_reply`, `running` then `done`,
  transition times from the envelopes; duplicate envelope pairs produce one
  transition each; a completion delivered after the parent turn settled still
  ends the row; a completion with no open row is dropped.
- Replay: seed a finished native row from a recovery cache, then replay the
  holder lines that created and ended it; status, `EndedAt` (or its absence),
  and `ActiveMs` are unchanged. Same for an agy end-unknown row.
- Retention: with the finished-row cap already full of rows that finished
  after the agy row's start, a newly settled end-unknown row survives
  settlement and a restore.
- Reattach: replayed turn ends (including a cancelled prompt whose watchdog
  continuation is still open, before its first output) never settle agy native
  rows during replay; rows still open settle end-unknown once replay
  completes, and the continuation's later live turn end leaves them
  unchanged.
- Writes: a native row event arriving after the recovery-writer interval has
  elapsed causes zero file writes; the row is on disk after the next turn-end
  write.
- Native rows produce no wake, turn-state change, or batch recompute (assert on
  the runner/conversation test seam used by existing `KindAgent` tests); a
  running Codex native row keeps the rail busy, and an agy row stops counting
  once the parent turn ends.
- HUD projection: running and completed native rows yield `native: true`,
  `startedAt`, and `endedAt` when known; no `linked`; completed rows fold into
  the count and appear with lifecycle details in the expanded ended group;
  end-unknown rows are excluded from the completed count and carried in the
  separate count; the companion relay emits no `subagent.done` for them;
  a Claude lead's no-daemon-identity row is native; daemon-spawned and Pi
  extension rows are byte-identical to before.
- Dashboard: headless-Chromium captures (no Electron) from a fixture page of
  (a) the compact HUD with a running agy native row and its tooltip and
  (b) the expanded tree's ended group with an end-unknown agy row and a
  completed Codex row, saved under
  `ai-docs/assets/captures/261122/native-subagent-roster/` (untracked PNG).

### Result (5fb755a22) - 2026-11-22

Landed Decisions 1-11 in 7a03e15ba (implementation and tests), 029261d05
(headless-Chromium capture test) and 5fb755a22 (status chip only on
end-unknown rows).

- wslive: native ID prefixes `agy-native:` / `codex-native:`, the
  daemon-internal `Event.EndUnknown` (`json:"-"`) settling a row `done` with
  no `EndedAt` or added `ActiveMs`, monotonic terminal native rows
  (`nativeSettled`), `RunningAgents(prefix)`, and finished-row retention
  ranked by `EndedAt` else the idle `PhaseStartedAt`.
- wsagent: the agy lead-only `invoke_subagent` mapping (label `Role` >
  `TypeName` > `subagent`, prompt never read, failed update ends `failed`);
  the Codex `subAgentActivity` mapping (before the turn gate, parent-thread
  only, item-id dedupe capped at 256, envelope times, orphan completions
  dropped); agy rows settle end-unknown at the accepted logical turn end
  (`endTurn` behind `!quiet`) and once `replayDone` runs; native row events
  publish RAM-only via `applyCoalesced`.
- wsdashboard: HUD `native`/`startedAt`/`endedAt` fields, `observationEnded`
  count kept separate from `completed`, native rows never linked, expanded
  tree native badge plus lifecycle times, `?` mark for end-unknown rows, the
  companion relay emits no `subagent.done` for end-unknown rows; new keys in
  all 11 locale tables.

Verification (GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local):
- `go test ./internal/wslive`, `./internal/wsi18n`: ok.
- `go test ./internal/wsagent` (full package, run alone): ok (138.7s).
- `go test ./internal/wsdashboard -run 'TestHUDProjection|TestRailBusy|TestAgentRow|TestAgentsIsland|TestAsset|TestCSS|I18n|Companion|Agent|Tree|Rail|Spawn|TestHandedOff'`: ok at 5fb755a22.
- Browser (`WS_PLAYWRIGHT_MODULE`): `TestHUDOrgChartBrowser`,
  `TestAgentBadgePopoverBrowser`, `TestNativeSubagentRosterBrowser`: ok.
- `node testdata/agents-island.test.cjs`: ok; `node --test
  frontend-companion.test.js`: 23/23.
- Captures (untracked PNG, en and ko) under
  `ai-docs/assets/captures/261122/native-subagent-roster/`:
  `compact-hud-running-agy-*.png`, `compact-hud-observation-ended-*.png`,
  `expanded-ended-group-*.png`.
- Lite review: no Critical or Important findings; three Minor left open
  (second Codex `started` for an open thread overwrites the open row; no
  conv-level restore+replay test for a restored end-unknown row; repeated
  agy `tool_call` re-emits `running` harmlessly).

Decisions:
- The RAM-only publication branch covers prefixed native IDs only; Claude
  no-identity rows keep their existing path (Decision 5).
- The tree count chip excludes end-unknown rows from finished/failed.
- An empty Codex `agentPath` gets the label `subagent`.
- The ended-row status chip shows only for end-unknown rows; done/failed
  native rows rely on the mark (keeps the existing popover contract).
- The retention fallback ranking applies to all finished rows.
- The observation-ended count renders in both the compact and idle HUD.
- The `done-leaf` HUD test fixture gained `EndedAt: 1` so it stays a
  completed row rather than end-unknown.

#### Edition (770d3bace) - 2026-11-22

End-unknown rows no longer encode their state as a zero `EndedAt`; this
changes Decision 4's representation only. A row whose observation ended
with the outcome unknown now carries a real `EndedAt` (when observation
ended: the accepted turn end, or a rejoin's replay end placed by the
restored history, 0232210cb) plus an additive `endUnknown` snapshot flag
(`wslive.Agent.EndUnknown`, `json:"endUnknown,omitempty"`). The HUD, tree
and companion relay key on the flag, never on `EndedAt == 0`. Retention
ranks finished rows by `EndedAt` alone again.

Compatibility evidence (T1, release-compatibility):
- Old reader: 7482aa91e `wslive/snapshot.go:451-460` decodes with plain
  `json.Unmarshal`, with no `DisallowUnknownFields` in wslive or
  wsdashboard, into an `Agent` without the member (`snapshot.go:341`
  declares only `endedAt`). The flag is ignored and the row shows done with
  its end time. This is the display degradation T1 already accepts.
- Old writer: 7482aa91e `wslive/state.go:1013-1015` ranks finished rows by
  `endedAt` alone at the cap, then `wsagent/record.go:685` replaces the
  recovery cache. An end-unknown row ranks by its real observation end, so
  it outlives older completions. The old writer drops the flag on rewrite;
  after re-upgrade the row is retained and shows done (display-only).
  `TestEndUnknownRowSurvivesAnOlderWriter` pins this path.

#### Edition (032bc13f0) - 2026-11-22

Rows a rejoin's replay rebuilds (supersedes the "placed by the restored
history, 0232210cb" clause above; the lead chose option B). Holder records
carry no time, so a replayed record cannot give its own end. Retention
already decided which finished rows the restored snapshot kept, so the
replay must not resurrect the rest:
- A row the replay created only from records the previous daemon had
  applied (seq <= HolderSeq) and which a readable restored snapshot does
  not list is dropped at replay end (also when the process ends during the
  replay), and its launch correlation is forgotten.
- A rebuilt row the restored snapshot listed running closes end-unknown at
  the snapshot's `UpdatedAt`. Its start moves back to that time if the
  replay clock put it later.
- A row a record past HolderSeq touched closes end-unknown at that record's
  time.
- With no readable restored snapshot (missing, unreadable, or another
  session's), quiet-only rows close end-unknown at the replay boundary,
  since no restored row is there to displace.
- A live turn end accepted during the replay still settles its open rows at
  its own time first. A dropped row is skipped.

Evidence: `TestAgyNativeReplayKeepsHistoricalRetention` (six launches, five
restored, the oldest not resurrected even after a buffered live turn end),
`TestAgyNativeReplayClosesRebuiltRowsWithoutSnapshot`,
`TestAgyNativeReplayClosesRebuiltRowsByEvidence`,
`TestLiveWriterRestoresAgyNativeRows`.
