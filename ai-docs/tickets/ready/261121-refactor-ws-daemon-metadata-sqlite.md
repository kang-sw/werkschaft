---
title: "데몬 메타데이터 묶음을 SQLite로 전환"
parent: 261119-epic-ws-daemon-sqlite-storage
related:
  261119-refactor-ws-whiteboard-sqlite-board-store: "SQLite pilot and measurement precedent"
  261118-feat-ws-display-transcript-memory-first: "Phase 3 record delta work transferred by (1326)"
  261120-refactor-ws-agent-workers-under-holders: "temporary applied-sequence write exception (1862)"
  261117-research-ws-daemon-file-touch-map: "historical file-touch and crash-recovery evidence"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 7d176a5cf8df70fa
sage-review-completeness-reviewed: 7d176a5cf8df70fa
---

# Move daemon metadata stores to SQLite

## Background

The owner selected the daemon-only metadata bundle as the next SQLite epic
child (board (1953)=a, 2026-11-21). The candidate set is Scope item 4 of
`261119-epic-ws-daemon-sqlite-storage`. The child absorbs the conversation
record delta work and must eliminate whole-record writes made solely to
record an applied holder sequence. The source shows that some candidates are
shared across processes or depend on file-change detection. Their scope and
cutover requirements are settled below.

Source citations are pinned to `e29019758`. Go paths are relative to
`agents-plugin-tool/internal/` unless they start with `cmd/`, which is relative
to `agents-plugin-tool/`, or `scripts/`, which is relative to the repository root
(`scripts/rail-headless.mjs`). `<dash>` is `<cache>/dashboard`
(`wsdashboard/endpoint.go:37,61-70`, `wsstate/paths.go:91-103`). `<session>` is
`<dash>/sessions/<reply-id>` (`wsdashboard/registry.go:30-46,257-261`).
`<config>` is the directory of global `config.json`, by default `~/.ws` and
overridable with `WS_CONFIG_HOME` (`wsconfig/global.go:11-29`).

### Current store inventory

The paths and cadence below are **VERIFIED** from their read and write sites.

| Store | Readers | Writers and write frequency |
|---|---|---|
| `<session>/agent.json` (the runner's `Record`, called `record.json` in the transferred phase) | Runner startup enumerates session directories and restores open and closed records. Handoff reads prior records. MCP reads the record for tracker support admission, playbook terminology, hosted parent resolution, Pi child rows and board nudges. Dashboard lifecycle and Pi revival read a projection, and the page watches the file. `wsagent/record.go:288-332`, `wsagent/runner_startup.go:68-89`, `wsagent/handoff.go:146-173`, `mcp/tracker_support.go:49-64`, `mcp/playbook_terminology.go:74-104`, `mcp/hosted_key.go:84-99`, `mcp/pi_live.go:65-89`, `mcp/whiteboard_nudges.go:388-402`, `wsdashboard/registry.go:386-425,470-498`, `wsdashboard/page_reader.go:230-254,304-312`. | Runner writes on creation, child creation, takeover/handoff, recovery changes and conversation mutations. Each holder line that adds a ring entry can trigger a full replacement, with another replacement on a dirty heartbeat or graceful detach. `wsagent/runner_create.go:217-249`, `wsagent/children.go:380-390`, `wsagent/handoff.go:146-173,272-287`, `wsagent/runner_startup.go:190-202`, `wsagent/runner_conv.go:653-667,812-839`, `wsagent/runner_process.go:797-816`, `wsagent/runner_events.go:827-842`. |
| `<session>/permission.json` | Cached projection and provenance checks, fresh read at launch/admission, and metadata revalidation on hosted lead `Runner.Get`. Absence allows legacy derivation. An invalid existing selection fails closed, including a dangling symlink. `wsagent/permission_store.go:49-140,148-179`, `wsagent/permission.go:125-191`, `wsagent/runner_operations.go:15-31`. | Explicit lead creation or spawn, authorized permission change, and a mapped native-mode change when it updates an explicit common selection. The writer rereads and preserves unknown JSON members and the presence of `delegated_authority`. There is no periodic write. `wsagent/runner_create.go:217-237`, `wsagent/permission.go:329-356,375-405`, `wsagent/permission_store.go:182-239`. |
| `<dash>/support.json` | `NewSupport` loads last selection and store-to-conversation mappings during daemon construction. Choices and current support-session lookups use memory. `cmd/ws-mcp/dashboard.go:112-119`, `wsagent/support.go:82-91,157-182,350-384,450-486`. | Opening support writes the mapping. Restart can delete and then add a mapping in two writes. Store unregister removes its mapping. Selection writes the last choice, with an extra mapping write on a harness replacement. `wsagent/support.go:197-212,387-414,489-528,560-581,634-657`. |
| `<dash>/pi-probes-v1/<fingerprint>-{models,version}.json` | Go computes the fingerprint from the Pi executable/package identity and, for models, the agent directory and credential/config stamps. Cache reads occur on the version probe and after Support's model memory-cache miss. Models retain the original observation with a five-minute TTL. The MCP version probe is once per process, and the direct server refuses this Pi route before probing when it has no runner. `wsagent/pi_probe_cache.go:101-139,157-232`, `wsagent/pi.go:94-116,280-300`, `wsagent/support.go:40-41,282-332`, `mcp/pi_tools.go:77-98,129-143,521-532`. | Success after a cache miss or staleness only. An unrecognized future schema is protected during publication. The Pi subprocess returns listing/version output. Go reads and writes the recovery cache. `wsagent/pi.go:94-116,280-300`, `wsagent/pi_probe_cache.go:235-261`, `cmd/ws-mcp/dashboard.go:354`. |
| `<dash>/rail-layout.json` | Startup reads the document. The rail reads it when the file stamp changes, and the stream stamp revalidates at the foreign-files boundary. `wsdashboard/rail_layout.go:285-320,344-394,419-431`, `wsdashboard/rail.go:37-50,235-248`, `wsdashboard/foreign_schedule.go:23-26,81-93`. | A changed `POST /rail/layout` operation (move, attach, star, import, etc.) or a worktree removal that changes saved order. A mutation reads the current file first. `wsdashboard/rail_layout_route.go:18-42,104-111`, `wsdashboard/rail_layout.go:442-484`, `wsdashboard/rail_layout_worktrees.go:65-115`. |
| `<dash>/plan-usage.json` and `plan-usage-agy.json` | Startup loads each latest projection. `GET /rail/usage` reads their in-memory entries, can ingest Claude observations, and can ask for a refresh. `wsdashboard/daemon_start.go:135`, `wsdashboard/daemon_http.go:65-66`, `wsdashboard/plan_usage.go:20,114-133,397-460`, `wsdashboard/plan_usage_agy.go:22-23,85-144`. | Changed accepted Claude observations and Codex/Pi usage query results update the first file; stale observations and expired reported Claude windows are ignored (agents-plugin-tool/internal/wsdashboard/plan_usage.go#L210-L240). Codex/Pi queries are gated at five minutes ordinarily and five seconds on forced refresh. Successful agy results update the second file when its encoding changes. Activity or forced refresh can schedule a probe, with a default ten-minute debounce. `wsdashboard/plan_usage.go:179-188,266-281`, `wsdashboard/plan_usage_probe.go:23-55,71-125`, `wsdashboard/plan_usage_agy.go:22-28,138-144,189-209,241-263`, `wsdashboard/daemon_runtime.go:16-22,57-69`. |
| `<dash>/plan-usage-history.json`, `plan-usage-codex-history.json` and `plan-usage-agy-history.json` | The history response and nonempty append batches stat the file and reread on a stamp miss. Codex checks for retained five-hour samples before reading its projected history. `wsdashboard/plan_usage_history.go:17-20,119-164,265-272,408-460`, `wsdashboard/plan_usage_agy.go:22-23,553-564`, `wsdashboard/plan_usage.go:447-457`. | A changed retained sample batch rewrites the relevant history file. Shared history receives Claude and eligible Codex/Pi samples. Codex five-hour and agy histories have separate files. Identical or immediately pruned samples cause no write. `wsdashboard/plan_usage.go:273-281`, `wsdashboard/plan_usage_probe.go:109-123`, `wsdashboard/plan_usage_agy.go:254-263`, `wsdashboard/plan_usage_history.go:265-332`. |
| `<dash>/memos/<sha256-of-workspace>.json` | GET and POST `/rail/memos` and one read per displayed workspace per rail render. The file key is the hash of the workspace string. `wsdashboard/daemon_http.go:65`, `wsdashboard/memos.go:23-51,118-151`, `wsdashboard/rail.go:37-51,214-225`. | Each successful add, edit or delete replaces that workspace's memo document. The writer retains unknown document/item members and rejects a future schema. `wsdashboard/memos.go:67-84,153-197`. |
| `<session>/label.json` | Dashboard index reads the label after a successful lane read. `wsdashboard/index.go:574-596`, `wsdashboard/label_route.go:73-89`. | Each accepted session-label POST replaces the label or removes the file when cleared. Hiring can also write the label before publishing a hired lead. `wsdashboard/label_route.go:91-105,131-152`, `wsdashboard/session_hiring.go:42-49`, `wsagent/runner_create.go:243-249`. |
| `<session>/ack-check.json` | Eligible answer checks read it and reread under `inboxMu` before updating. Interrupt reads it under the same mutex. Change pokes coalesce while a one-second timer is armed. `wsdashboard/ack_check.go:57-64,113-143,219-272,275-301,304-342`, `wsdashboard/daemon_runtime.go:35-54`. | Interrupts write the interrupt timestamp. A check that passes the commit guard writes the checked turn before it attempts to enqueue a nudge. `wsdashboard/inbox_routes.go:735-743`, `wsdashboard/ack_check.go:134-143,279-297`. |
| `<session>/hiring.json` | Dashboard hiring routes, lead eligibility and rail attach check it. The runner reads hire request identity during a retry search. `wsdashboard/session_hire_routes.go:162-175`, `wsdashboard/session_hire.go:108-136`, `wsdashboard/rail_layout_route.go:91-102`, `wsagent/hire.go:57-81`. | The trusted hiring-enabled POST writes on acceptance. Hired lead creation writes the initial marker after its label and before the conversation record. `wsdashboard/session_hire_routes.go:154-197`, `wsdashboard/session_hiring.go:19-49`, `wsagent/runner_create.go:243-249`. |
| `<config>/dashboard/workspaces.json` | Startup loads the list. The rail and memo path checks read memory. Mutations and sweeps reload the file to incorporate changes from another process. `cmd/ws-mcp/dashboard.go:61,132-134`, `wsdashboard/workspaces.go:23-30,50-105,124-139,179-249`, `wsdashboard/rail.go:137`, `wsdashboard/memos.go:88-92`. | A new workspace, forget or sweep that folds a stored linked worktree into its root rewrites the list. Preview and no-op sweeps read it. Batch admission preflights it and can add more than one workspace. `wsdashboard/workspaces.go:179-261`, `wsdashboard/workspace_routes.go:204,227,249,310-320,365-383`, `wsdashboard/daemon_start.go:175-186`. |
| `<dash>/tracker-hooks/<store-id>.json` | The hook runner loads it on each active store scan, validating the registration nonce. Scans run at startup, on nudge or configuration change, and on a one-minute backstop. `cmd/ws-mcp/dashboard.go:255-266`, `wstrackerhook/state.go:15-16,44-68`, `wstrackerhook/runner.go:24-28,177-195,280-330`. | A successful activity read results in one whole-file replacement for that store, including no-event scans. The cursor, processed IDs and due marks are saved after queueing. Hook commands receive events on stdin, and the separate `tracker-hooks.json` is input configuration. `wstrackerhook/runner.go:39-46,210-247,323-369,449-478`, `wstrackerhook/state.go:72-95`, `wstrackerhook/config.go:16`. |
| `<config>/tracker/<store-id>/ui-seen.json` (specifically `StoreEntry.Dir`) | The daemon's tracker view and projection read seen marks. The first access loads that registration's file and subsequent reads return memory keyed by store ID, directory and nonce. `wstrackerhost/registry.go:32-40,43-64`, `wstrackerhost/host_register.go:105`, `wstrackerhost/seen.go:36-56,78-101`, `cmd/ws-mcp/tracker_host.go:211-215`, `cmd/ws-mcp/tracker_projection.go:24-36`. | Each accepted seen POST makes `MarkSeen` recheck the store registration, union badge/read IDs and replace the file, even if the union adds nothing. Only the dashboard route calls the writer in current product code. `wsdashboard/tracker_routes.go:332-345`, `cmd/ws-mcp/tracker_host.go:531-535`, `wstrackerhost/seen.go:104-129`, `wstrackerhost/registry.go:178-191,250-260`. |

### Process and ownership boundaries

- **VERIFIED: direct Go MCP reads `agent.json`.** Raw `ws-mcp serve --stdio`
  starts an independent server. Raw `ws-mcp call` constructs one without
  `c.host`. Tracker support admission uses the record's key, root, profile
  and open state. Playbook rendering and tool alias admission also use the
  matching record. Ordinary dashboard registration can stat the record
  before unregister and read it before pruning directories, where its
  lifecycle can protect a terminal-owned Pi session file.
  `cmd/ws-mcp/main.go:19-55,58-74`, `cmd/ws-mcp/hosted_cli.go:74-91,162-163,529-542`,
  `mcp/server_dispatch.go:145-154`, `mcp/server_inventory.go:140-165`,
  `mcp/tracker_support.go:49-64`, `mcp/playbook_render.go:107-115`,
  `mcp/tool_aliases.go:102-114`, `mcp/dashboard_sessions.go:370-409,460-472`,
  `wsdashboard/registry.go:386-425,448-498`. Current sites include
  agents-plugin-tool/internal/mcp/dashboard_sessions.go#L468-L468 and
  agents-plugin-tool/internal/wsdashboard/registry.go#L490-L496.
- **VERIFIED: an external non-Go fixture uses two file formats.**
  `scripts/rail-headless.mjs:135-149,167-180,232-243,579-582,979-982`
  seeds `label.json` in its scratch registry and removes/reads the scratch
  `rail-layout.json`. This is a test consumer and a cutover verification
  dependency. **INFERRED:** its sandboxed fixture contract gives no evidence of
  a production dependency on either JSON file.
- **VERIFIED: three stores explicitly account for foreign file changes.**
  Permission revalidation tests the file identity and metadata, rail layout
  refresh detects a foreign write, and history rereads a newer version or a
  repaired file. Workspace mutations also reload external edits. These paths
  establish current behavior. **INFERRED:** a supported production editor
  has yet to be identified for permission, rail layout or plan-usage history.
  `wsagent/permission_store.go:70-98`,
  `wsdashboard/rail_layout.go:322-327,354-379,446-459`,
  `wsdashboard/plan_usage_history.go:119-127,265-272`,
  `wsdashboard/workspaces.go:50-52,134-139`.
- **VERIFIED:** the daemon lock is per dashboard state directory, while the
  workspace list and tracker root are per config directory.
  **INFERRED:** separate `WS_CACHE_HOME` values with the same `WS_CONFIG_HOME`
  allow independent dashboard daemons to share `workspaces.json` and
  `ui-seen.json`. Workspace reload behavior explicitly anticipates another
  process. Seen currently assumes one dashboard writer, so its in-memory
  cache has no foreign seen-mark invalidation. Moving either into a
  cache-local DB could fork state. `wsdashboard/daemon_start.go:21-44`,
  `wsstate/paths.go:91-103`, `wsdashboard/workspaces.go:16-30,50-52`,
  `wstrackerhost/registry.go:32-40`, `wstrackerhost/seen.go:36-49,104-128`,
  `ai-docs/manuals/ws-mcp.md:1480-1487`.
- **INFERRED from the tracked source search:** no other production launcher,
  packaged Pi/Claude extension, Python holder or external Go caller was
  found opening the listed metadata files. In the adjacent **VERIFIED**
  contracts the launcher opens `endpoint.json`, Pi and Claude read the
  registration path supplied by the runner, and the Python holder writes
  its descriptor and exit files. A workspace or tracker hook can run user
  code, so repository search cannot classify private consumers.
  `agents-plugin/bin/ws-mcp-launcher.py:963,1149`,
  `wsagent/runner_process.go:373-387`, `agents-plugin-pi/src/hosted-session.ts:95-105`,
  `agents-plugin/hooks/claude-mod/hosted.ts:93-105`, `wsholder/holder.py:13-23,108-110`,
  `wstrackerhook/runner.go:39-46,449-478`.

### Before: file operations per action (derived from source)

Counts below are **VERIFIED** as API-level counts from the cited success
paths, with stated branch conditions. Count one for a read, stat, mkdir,
create, chmod, write, replace or remove. Exclude close, retries, work outside
the named stores and the internals of a compound call such as `ReadFile`.
The wsagent replacement is five calls (`MkdirAll`, create, chmod, write,
replace) and the dashboard private-file replacement is four (create, write,
chmod, replace). The tracker seen writer omits chmod and mkdir.
`wsagent/record.go:335-365`, `wsdashboard/endpoint.go:97-122`,
`wstrackerhost/registry.go:133-158`. Windows can issue more calls on sharing
violations (`wsagent/replace_file_windows.go:15-60`). These are source counts,
with measured syscall counts and latency still due.

| Action and conditions | Metadata calls | Source |
|---|---:|---|
| Held lead or worker finishes a line that advances the sequence and touched the final ring | 5 for the whole record per line. Clean heartbeat 0. Dirty heartbeat 5 for the whole record, on the default 30 s runner tick. | agents-plugin-tool/internal/wsagent/runner_process.go#L811-L830, agents-plugin-tool/internal/wsagent/runner_conv.go#L672-L678, agents-plugin-tool/internal/wsagent/runner_conv.go#L831-L846, agents-plugin-tool/internal/wsagent/record.go#L324-L365, `wsagent/runner_startup.go:35-37`, `wslive/snapshot.go:85-89` |
| Direct support admission with a matching candidate key | 1 whole-record read per admission invocation | `mcp/tracker_support.go:49-64,86-97`, `wsagent/record.go:294-321` |
| Hosted lead state poll or explicit common-permission change to an existing lead selection | 1 stat for a warm stable poll, 3 calls when a loaded existing file revalidates and rereads. Stable absent poll 2 (stat + lstat). The change uses 8 on the selection file (authorization stat + read, writer read + 5), plus any record update or later launch checks. A worker's parent authorization reads are additional. | `wsagent/runner_operations.go:23-29`, `wsagent/permission_store.go:76-125,201-238`, `wsagent/permission.go:128-130,294-350`, `wsagent/record.go:335-365` |
| Support store read, opening send, or New replacing an existing conversation | 0 in `support.json` for read or send to an existing conversation, 5 to set a mapping, 10 to delete and replace a mapping | `wsagent/support.go:197-212,411-414,474-528` |
| Pi probe cache hit or successful cold miss with valid identity and publication | 1 cache read on hit. Miss 9 (lookup read, header read, mkdir, chmod, 5-call replacement). Executable/package/credential stamps and the actual probe are extra. | `wsagent/pi_probe_cache.go:101-139,157-217,220-261`, `wsagent/record.go:335-365` |
| Changed rail layout mutation, no-op mutation, or one rail render showing W workspaces | 7 on a change (stat + read + 4 + stat), 2 on no-op. Render 1 layout stat + W memo reads, plus 1 layout read on change. | `wsdashboard/rail_layout.go:285-286,314-319,354-379,446-484`, `wsdashboard/rail.go:37-51`, `wsdashboard/memos.go:28-35`, `wsdashboard/endpoint.go:99-122` |
| Usage observation that changes latest usage, and each changed history batch it produces | Latest 4 per changed file. History 6 on a warm stamp (stat + 4 + stat), 7 when an existing history file must be reread. Warm history-only read 1 stat for shared history, 1 for displayed agy history, and 1 or 2 for Codex five-hour history (the second when samples are present). A changed existing file adds a read. Empty append batch 0. | `wsdashboard/plan_usage.go:107-112,179-188,273-281`, `wsdashboard/plan_usage_history.go:132-162,265-332,408-460`, `wsdashboard/plan_usage_agy.go:138-144,254-263,553-564` |
| Memo GET or accepted add/edit/delete | GET 1. Mutation 6 (read + mkdir + 4). | `wsdashboard/memos.go:28-35,67-84,145-197`, `wsdashboard/endpoint.go:99-122` |
| Session label set or clear | Set 4, clear 1 remove. A following successful index lane read adds 1 label read. | `wsdashboard/label_route.go:75-105,142-150`, `wsdashboard/index.go:574-589`, `wsdashboard/endpoint.go:99-122` |
| Answer check reaches and passes its commit guard, or interrupt updates the marker | Check 6 (initial and guarded read + 4), interrupt 5 (read + 4). | `wsdashboard/ack_check.go:115-143,247-272,279-297`, `wsdashboard/endpoint.go:99-122` |
| Accepted hiring toggle or hired lead with nonempty label | Toggle 5 (read + 4), hired lead 8 for label + hiring writes, before the separate agent-record write. | `wsdashboard/session_hire_routes.go:169-197`, `wsdashboard/session_hiring.go:21-49`, `wsagent/runner_create.go:243-249`, `wsdashboard/endpoint.go:99-122` |
| Workspace add/forget/sweep that changes the list, or warm rail access to the list | Change 6 (reload read + mkdir + 4), no-op forget or sweep 1, warm rail accessor 0. Additional path and Git probes are outside the count. | `wsdashboard/workspaces.go:77-107,108-131,179-261`, `wsdashboard/endpoint.go:99-122` |
| Active tracker hook scan for one store with a successful activity read | 5 even if it finds nothing to queue (read + mkdir + create + write + replace) | `wstrackerhook/runner.go:314-369`, `wstrackerhook/state.go:52-95` |
| Tracker seen POST on a warm seen cache, or a warm seen read | POST 3 for marks plus 1 registry stat and, on a registry memo miss, 1 registry read. A cold marks access adds 1 seen-file read. Warm seen read 0. | `wstrackerhost/seen.go:36-49,78-129`, `wstrackerhost/registry.go:107-109,115-117,138-158,178-191,250-260`, `wsstate/read_cache.go:38-53` |

The older profile in `261117-research-ws-daemon-file-touch-map`, ranked touch
sources and Q2 (lines 190-205 and 280-290), gives context for record rewrites,
permission polling, rail/memo reads and answer checks. Its rates describe
that snapshot of the code and machine.

## Decisions

- **Bundle next and scoped to daemon-only metadata** (board (1953)=a).
  Apply the parent epic's five-store scope and exclusions ((1325)=a).
  Git-tracked state, git transport, non-Go production file contracts,
  child-owned inputs and raw blobs stay files. `wsmailbox` remains on its
  existing memory-first segment ring. `registration.json`, Python holder
  files, Pi `session.jsonl` and `system_prompt.md` are among the file contracts.
- **Conversation record deltas are part of this child** ((1326)=a, the
  transferred Phase 3 in `261118-feat-ws-display-transcript-memory-first`).
  **Remove the whole-record holder cursor write** ((1862)=a,
  `261120-refactor-ws-agent-workers-under-holders`, Decisions). It applies to
  ring-writing final lines and dirty heartbeats for held leads and workers.
  The inherited cost was approved for the holders change as a temporary
  performance exception. **Rejected there:** adding a separate cursor store
  in the holders ticket. This child settles the replacement recovery mechanism.
- **SQLite starting point** ((1325)=a, epic Cross-Child Decisions). Use the
  `modernc.org/sqlite` precedent, WAL, `SetMaxOpenConns(1)`, `busy_timeout`,
  foreign keys and bounded `SQLITE_BUSY`/`SQLITE_LOCKED` retry. Measure the
  chosen configuration on the target platform. `wsstore/store.go:145-162,209-225`,
  `wsstore/retry.go:22-48,61-72`.
- **Whiteboard precedent is board-specific** ((1325)=a, refined by (1347)=c).
  The whiteboard child owns one DB per board, lazy open, a small cache and
  close after a long idle (about 30 minutes). **Rejected there:** a shared
  board database and a board-only append journal. The embed snapshot is a
  separate epic child. These decisions provide the structural precedent for
  this draft. Metadata DB topology is settled below; handle lifetime remains
  subject to measurement.

- **(1980) Agent records:** Move `agent.json` authority into SQLite in this
  bundle. First route direct Go record readers through the daemon, including
  tracker admission, aliases, hosted lineage, unregister and registration
  pruning (`mcp/tracker_support.go:49-64`, `mcp/tool_aliases.go:97-132`,
  `mcp/hosted_key.go:74-95`, `mcp/dashboard_sessions.go:405-408,467-472`).
  Legacy records become the cutover snapshot under (2125).
  Rejected: leave the shared file authoritative and only remove holder
  whole-file rewrites.
- **(1981) Workspaces:** Leave `workspaces.json` outside the bundle: its
  config root is shared across processes and it has no cursor-driven rewrite.
  Rejected: include it and add a shared-ownership contract.
- **(1982) Tracker seen:** Leave `ui-seen.json` outside the bundle: the tracker
  config root can be shared across daemons. Rejected: include seen marks and
  add a shared-ownership contract.
- **(1983) Permission selection:** Edits go only through the daemon's
  authorized entry point. Capture the old file for one-time import before the
  (2125) fence. Preserve fail-closed behavior and origin validation. Rejected:
  continue accepting direct live file edits.
- **(1984) Rail layout:** Dashboard actions are the only live write path;
  update fixtures to test dashboard actions and legacy import separately.
  Preserve ordering, generation and wake behavior. Rejected: continue
  accepting live direct file edits.
- **(1985) Usage history repair:** Preserve schema and error protection on
  legacy import and document an offline SQLite recovery path. Rejected: keep
  the file as the live out-of-process recovery source.
- **(1986) Direct callers:** Route affected `ws-mcp call` and `serve --stdio`
  operations through authenticated daemon requests; specify behavior when no
  daemon is running. These direct entry paths remain compatibility paths;
  the daemon handles the work. Prove predecessor drain, writer shutdown and
  successor startup during replacement. Rejected: multi-process SQLite WAL
  access; refuse direct calls as a T3 boundary.
- **(1987) Compatibility:** Apply the directional criteria in
  `ai-docs/manuals/release-compatibility.md:54-71,127-145`. The bundled stores'
  same-root downgrade follows the dated (2125) owner exception. Any T3
  boundary requires a new owner decision.
- **(1988) Topology:** Use one shared daemon state DB for cache-rooted stores;
  revisit only if measurements show contention. Rejected: per-store DBs with
  multiplied handles and checkpoints.
- **(1989) Import:** Use transactional, repeatable legacy import with the
  (2125) completion marker and authority handoff. Keep original import bytes,
  unknown fields and invalid or future inputs for recovery. Discover bundled
  data in hosted and CLI-only session directories, including labels and hiring
  flags alongside registration rows (`wsdashboard/label_route.go:73-105,108-125`,
  `wsdashboard/session_hire_routes.go:148-175`). Protect adjacent files during
  discovery and import. Rejected: request-path fallback and continuous dual
  writes.
- **(1990) Legacy files:** Retain the fenced cutover snapshot under (2125).
  Keep original import bytes with recovery data.
- **(1991) Conversation state:** Use reply-ID keyed state and bounded recovery
  deltas; decide snapshot/compaction and legacy unknown-field representation.
  Rejected: rewrite the whole record on every mutation.
- **(1992) Applied sequence:** Use a delta keyed by conversation and holder
  identity. Prove replay with partial ring append, cursor write failure, holder
  change and process death between stores. Rejected: persist a whole-record
  image or delay sequence persistence by time alone.
- **(1993) Permissions:** Use reply-ID keyed explicit permission records with
  presence-sensitive provenance and preservation of unknown members. Keep
  invalid and absent legacy inputs distinguishable. Rejected: infer selection
  from conversation settings.
- **(1994) Support:** Separate last selection from store-to-conversation keyed
  mappings; preserve replacement and unregister recovery. Rejected: rewrite
  one support document for a single mapping change.
- **(1995) Pi probes:** Key probe records by fingerprint and kind; preserve
  success-only admission, freshness and invalidation. Decide whether to import
  legacy cache hits or re-probe as part of (1989). Rejected: merge both probe
  kinds into runner or support state.
- **(1996) Rail storage:** Use workspace-scoped layout changes and an in-memory
  generation; preserve ordering, legacy extra fields and consumer wake.
  Rejected: a monolithic layout rewrite or DB-file polling.
- **(1997) Latest usage:** Key projections by source/group; preserve the wire
  projection, fallback and debounce behavior. Rejected: rewrite the shared
  current usage document.
- **(1998) Usage samples:** Use keyed samples with existing history identity
  and retention; prove shared, Codex five-hour and agy projections, including
  unknown legacy fields. Rejected: serialize the entire history on append.
- **(1999) Memos:** Use keyed records with stable order and preservation of
  legacy document/item fields; retain or resolve the workspace hash on import.
  Rejected: rewrite every memo for a one-item edit.
- **(2000) Labels:** Key labels by reply ID and delete on clear; preserve index
  publication and newly hired lead behavior. Rejected: persist an empty value.
- **(2001) Answer checks:** Use a guarded reply-ID keyed marker; preserve the
  interrupt/check race and trace marker update before inbox nudge. Rejected:
  an unguarded batched cursor advance.
- **(2002) Hiring:** Key metadata by reply ID. Preserve request identity and
  retry after interrupted creation, coordinating label, hiring and record
  changes through the daemon store. The approval journal stays in its separate
  file (`wsagent/hire.go:59-86`, `wsdashboard/session_hire.go:78-105,418-452`).
  Rejected: embed hiring flags in registration.
- **(2004) Tracker hooks:** Scope cursors by registration nonce and use keyed
  processed/due updates; preserve queue/receipt order and catch-up policy.
  Rejected: replace all hook state every scan.
- **(2006) Durability:** Define requirements per write class and evaluate
  `PRAGMA synchronous` against recovery and measured cost, especially for
  holder cursors and consumed input. Rejected: one immediate flush rule for
  every metadata update.
- **(2007) Checkpoints:** Measure WAL checkpoint cadence outside interactive
  actions; set handle lifetime, checkpoint method and idle/shutdown drain from
  topology and Windows evidence. Rejected: assume default auto-checkpoint is
  cheaper.
- **(2008) Staging:** Use the whiteboard precedent: an unwired store/import
  phase, an authority switch, and a measured result. Rejected: switch authority
  without the staged import and measurement.
- **(2009) Baseline:** Capture the pinned baseline before wiring authority and
  compare on the target Windows AhnLab V3 machine before release. Rejected:
  gate the switch on a WAL prototype; the wired comparison supplies the needed
  evidence for the chosen topology.
- **(2121) No daemon running:** An affected raw `ws-mcp call` attaches to a
  healthy daemon and otherwise fails at once with an error naming
  `ws-mcp daemon-start` for the same cache/config root. An affected
  `serve --stdio` session ensures the daemon once under the existing
  dashboard policy, then attaches; when disabled or unavailable it returns a
  recoverable unavailable error. Authority checks route together with their
  operation; a connection failure after a mutation is sent keeps the
  existing unknown-outcome behavior. This matches the launcher's CLI/MCP
  split (`agents-plugin/bin/ws-mcp-launcher.py:1612-1641,1821-1877`).
  Rejected: auto-start for one-shot `call` (detached startup and AV latency
  on a one-shot call); an in-process read-only SQLite fallback (conflicts
  with (1986)).
- **(2122) Replacement writer barrier:** Stop metadata admission and drain
  routed calls and background users, including tracker hooks, poll and watch.
  Close SQLite before releasing `dashboard.lock`. The successor takes that
  lock before metadata open, import and restore, including Support
  initialization, and validates before serving. This also applies to a prestarted
  successor under `261115-feat-werkschaft-electron-daemon-override` Phase 3.
  The current constructor reads Support before the lock and the current shutdown
  can leave hosted calls and tracker work running after unlock
  (`cmd/ws-mcp/dashboard.go:112-120,186-224`, `wsagent/support.go:157-179`,
  `wsdashboard/daemon_start.go:24-45`, `wsdashboard/daemon_runtime.go:118-169`,
  `wsdashboard/ensure.go:158-167`).
- **(2125)=a. Freeze bundled legacy metadata at cutover (owner, 2026-11-22).**
  Under `dashboard.lock`, wait for the legacy metadata writers to exit before
  discovering inputs. The existing daemon can release the lock while old calls
  or hooks are still running (`cmd/ws-mcp/dashboard.go:196-224`,
  `mcp/dashboard_sessions.go:405-423`). Durably stage the original bytes and
  import states before applying the one-time fences. Interrupted cutover
  ((2135), owner 2026-11-22): while a cutover is pending, unfenced legacy
  files stay the live authority. Keep a durable capture journal per file and
  per session generation outside session directories. A retry drains legacy
  writers again, re-stages the current bytes of every unfenced file
  (permission selector together with its record), and reuses a staged
  original only where the migration-owned fence demonstrably landed on that
  exact file. Handle a crash after a replacement but before its journal
  mark, and discard journal state for a session deleted or recreated since;
  never recreate a pruned directory on retry (the current writer calls
  `MkdirAll`). The final import re-reads unfenced files and re-enumerates
  sessions inside the commit boundary and imports no session that has
  vanished. A raw old registration prune racing that boundary joins the
  post-cutover prune residual below (`wsdashboard/daemon_start.go:24-45`,
  `wsagent/permission.go:294-349`, `mcp/dashboard_sessions.go:405-423`,
  `wsdashboard/registry.go:386-425`, `wsagent/record.go:335-365`).
  Rejected: retrying blindly from staged originals, which loses a permission
  lowering the old daemon accepted in between and reincarnates a pruned
  session; an enforced exclusive maintenance window, which costs downtime
  and cannot be enforced for downstream installs. Once the fences are durable,
  commit the imported state and completion marker in the same SQLite transaction.
  SQLite owns the bundled stores after that commit. Subsequent opens restore
  from SQLite and ignore legacy edits, additions and deletions. The surviving
  files are the fenced cutover snapshot. The new daemon makes zero later
  bundled-store publications to legacy paths. New raw calls route under
  (1986) and (2121). Shared registration, holder, ring, inbox and adjacent
  file contracts continue through their existing owners
  (`wsdashboard/registry.go:257-285,333-367`, `wsdashboard/daemon_runtime.go:154-162`,
  `wsagent/runner_process.go:208-220,237-245`, `wslive/snapshot.go:125-128`,
  `wsinbox/inbox_store.go:203-221`).

  Apply the four one-time fences from the staged originals.
  - Give every imported session a present, invalid `permission.json` selector,
    including sessions whose old file was absent. The old reader distinguishes
    an invalid selector from omission and refuses fresh lead and worker
    admission. Already-held processes can rejoin without applying the
    selection (`wsagent/permission_store.go:60-67,119-140`,
    `wsagent/permission.go:128-161`, `wsagent/runner_process.go:69-91,274-285`).
  - Clear legacy hiring `Enabled` and retain `Hired` and `RequestID`. The old
    eligibility check refuses hiring with this flag cleared. The new daemon
    uses the imported original flag and request identity
    (`wsdashboard/session_hiring.go:10-31`, `wsdashboard/session_hire.go:110-133`,
    `wsagent/hire.go:59-86`).
  - Support tracker authority ((2136)=a, owner 2026-11-22): set a nonempty
    `closed_at` on every eligible legacy support record (parentless,
    non-lead), preserving its key, root and unknown fields; SQLite keeps the
    imported original open state. The pinned raw binary (`ws-mcp call` or
    `serve --stdio`) admits tracker support writes from a retained key by
    re-reading `ClosedAt`, regardless of the daemon lock, so without this
    fence a conversation SQLite has closed stays writable through it while
    the new daemon runs (`cmd/ws-mcp/main.go:19-74`,
    `mcp/tracker_support.go:49-80`, `mcp/tracker_update.go:17-21,49-73`,
    `wsagent/runner_events.go:1033-1056`). The old binary then shows those
    support conversations as closed; the old prune predicate requires a lead
    or parent, so the mark does not make them prune-eligible
    (`wsagent/support.go:370-392`, `wsdashboard/registry.go:463-467`). Leads
    and children get no synthetic `closed_at`. Rejected: a documented
    residual only, which leaves every existing support key writable; a
    close-only mirror, which is write-through under (2125); an old-side
    access boundary, which is T3.
  - Child tracker authority (same fence, reviewer findings auto-confirmed
    2026-11-22). The pinned gate tests the session key's parent and never
    reads `Record.Parent`, spawned children carry parentless delegate keys,
    and it resolves the profile and the registered stores on every call,
    so any open non-lead child can become admissible after cutover through
    a profile or store change (`mcp/tracker_tools.go:52-67`,
    `mcp/tracker_support.go:49-80`, `mcp/agent_keys.go:40-43`,
    `wsagent/runner_create.go:172-212`, `wsagent/spawn.go:125-127`). The
    fence therefore covers every non-lead record with a parent that is
    open at cutover, whatever its current profile or root. Such a record
    keeps an empty `closed_at`; its `profile` is set to the sentinel
    `!cutover-fenced`, which fails the profile-name grammar that
    `ResolveProfile` checks before reading any configuration, so no
    configured definition can resolve it (`wsagent/profile.go:57,361,398-401`,
    `mcp/tracker_support.go:61-64`). The original profile is kept in
    SQLite and in the record's unknown field `cutover_profile`. The old
    prune predicate retires a child only on `closed_at` or a stale
    hand-off, so the directory is kept
    (`wsdashboard/registry.go:386-425,463-467`). Parentless non-lead records
    take the `closed_at` fence above. Rejected: `closed_at` on children,
    which lets an old daemon after a downgrade delete the child's directory
    once retention passes, unrecoverable on re-upgrade; a grammar-valid
    sentinel such as `ws-cutover-fenced`, which a configured profile can
    define; fencing only children eligible at cutover, which a later
    profile or store change makes admissible; rewriting `WSKey`, which old
    restore, key setup and mailbox drain depend on (`wsagent/record.go:320`,
    `wsagent/runner_process.go:147-153`, `wsagent/runner_conv.go:351-355`).
  - Remove the cache-root dashboard's legacy `tracker-hooks.json` after retaining
    its bytes and making the config available through a new-daemon-only input
    path. Preserve owner-controlled reload and
    nonce-scoped progress in the new daemon. The old runner goes idle when
    the file is absent (`wstrackerhook/config.go:13-16,31-45`,
    `wstrackerhook/runner.go:204-247,280-299`, `wstrackerhook/state.go:25-34,52-60`).
    Update both shipped hook instructions,
    `agents-plugin/rsrc/tracker-hooks/tracker-hooks.md` and
    `agents-plugin-wsflow/rsrc/tracker-hooks/tracker-hooks.md`, to name the
    new input path in the same change, and verify that an edit made as they
    document reloads in the new daemon. Left unchanged, they direct edits to
    the retired file, so new hooks stay idle and the file the old runner
    watches reappears (`wstrackerhook/runner.go:210-245`).

  **Owner compatibility exception, 2026-11-22.** A same-root downgrade after
  cutover is accepted in a patch as a ticket-specific exception, recorded on
  the precedent of (1855) in `ai-docs/manuals/release-compatibility.md:129-140`.
  Re-upgrade discards old edits to bundled stores.
  Disclose that the old binary can prune a shared session directory revived
  after cutover based on stale `agent.json`, can replay or orphan a holder from
  stale identity or sequence, and can replay pending entries from the separate
  hiring approval journal. It can also accept stale authenticated tracker
  writes from a support record an old binary created after a later same-root
  downgrade, which stays open on re-upgrade because the support fence ran
  once ((2136); `wsagent/support.go:387-414`, `wsagent/runner_create.go:248-249`).
  An older daemon after a downgrade cannot launch a fresh process for a
  child that was open at cutover, because the fence renamed its profile to
  `!cutover-fenced`; it can still reattach a surviving holder, and a held Pi
  worker keeps its stored resolved definition, but tracker writes stay
  refused because the gate resolves the record's profile on every call
  (`wsagent/runner_process.go:38-59,528-549`,
  `wsagent/runner_conversation.go:209-217`). The old daemon still starts and
  serves other sessions, and re-upgrade restores the child's original
  profile and open state from SQLite.
  An older daemon serving the same root after a downgrade can also accept
  the owner's trusted toggle to re-enable hiring for a CLI lead nested after
  cutover, because the frozen layout still shows it as a root, and approval
  then checks that stale hierarchy; both need deliberate owner action, and
  the new daemon keeps refusing per (955) ((2137)=b, owner 2026-11-22;
  `wsdashboard/session_hire_routes.go:148-197`,
  `wsdashboard/session_hire.go:110-151,418-437`). Rejected: a one-time
  per-row `Enabled=false, Hired=true` sentinel, which misreports every
  ordinary lead in the old dashboard and leaves later CLI rows uncovered;
  an old-root boundary, which is T3.
  The hiring gate still applies on a replay attempt
  (`mcp/dashboard_sessions.go:405-408`, `wsdashboard/registry.go:386-425,456-468`,
  `wsagent/runner_startup.go:68-123`, `wsagent/record.go:84-92`,
  `wsagent/runner_process.go:237-245,482-487`,
  `wsdashboard/session_hire.go:78-105,314-348,418-427`).
  The owner rejected (b) freeze with a T3 boundary requiring a minor bump and
  (c) continued write-through.
- **Permission and hiring consumers:** At Phase 2 cutover,
  `261115-feat-ws-permission-profile` Decision 8,
  `261115-feat-ws-worker-profile-session-spawn` Phase 2, and
  `261115-feat-ws-session-hire-approval` Decision (957) use the daemon's
  SQLite-backed store service for their live reads, writes and recovery.
  The legacy `permission.json` and `hiring.json` are one-time import inputs
  and stay as the fenced snapshot under (2125).

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for `agents-plugin/rsrc/`, `agents-plugin/skills/`, `agents-plugin/hooks/`, `agents-plugin-wsflow/`)
- Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
- Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
- Follow `ai-docs/manuals/performance.md:12-40,63-73` for memory-first reads,
  deltas, file/handle lifetime, interactive I/O and Windows before/after
  measurement. Rule 8 governs any proposed exception. Count the store reads
  and writes as well as SQLite open, WAL/SHM, locks, sync and checkpoint work.
- **Owner compatibility exception, 2026-11-22, (2125)=a.** The patch accepts
  loss of old bundled-store changes on re-upgrade and the same-root stale
  prune, holder and pending-hire replay risks recorded in Decisions. Keep
  this exception scoped to this cutover. Record directional evidence under
  `ai-docs/manuals/release-compatibility.md:54-71,127-145` for old import,
  old-binary fences and re-upgrade. The frozen snapshot and discarded old
  edits carry no T2 recovery promise. Document the residuals for users.
- **VERIFIED existing contracts:** the holder reattaches using `HolderSeq`
  together with holder identity, for leads and workers. Ring apply precedes
  line completion, and graceful detach finishes an in-flight line. Preserve
  replay ordering and deduplication across the record, ring and snapshot
  recovery boundaries. Preserve the durable copies of consumed input,
  including the queued turn around `proc.Send`, mailbox claims, steers and
  pending reports. Preserve protection of terminal-owned files during handoff,
  existing downgrade-fenced worker recovery, and owner-only treatment of the
  record and permission data, including SQLite side files when applicable. `wsagent/runner_process.go:208-254,286-299,480-496,797-816`,
  `wsagent/record.go:324-357,525-555`, `wsagent/runner_events.go:827-847`,
  `wsagent/runner_conv.go:318-365,1209-1230,1284-1313`,
  `wsagent/children.go:654-679,744-775`, `wsdashboard/registry.go:448-467`.
- Route permission edits through the authorized daemon entry point and rail
  layout writes through dashboard actions. Use their legacy files for the
  initial import under (2125). Preserve usage history import schema/error
  protection and document offline SQLite recovery.
- Preserve the current permission error/omission distinction, unknown fields,
  and presence-sensitive delegation provenance. Preserve hook catch-up, due
  and retry semantics, and seen-mark identity across unregister/re-register.
  Dashboard views and routes retain existing label clearing, workspace
  placement, usage retention/dedupe, memo, notify and hiring behavior.
  `wsagent/permission_store.go:119-161,201-239`, `wsagent/record.go:187-239`,
  `wstrackerhook/runner.go:303-369`, `wstrackerhost/seen.go:36-56,104-128`,
  `wsdashboard/plan_usage_history.go:17-20,216-250,265-332` (14 days and
  5,000 samples per harness/group/kind), `wstrackerhook/runner.go:447-480`
  (a failed hook run is logged).
- Keep `workspaces.json` and tracker `ui-seen.json` outside this bundle.
  Keep the scope of neighboring stores as set by the epic. Session-key
  metadata, wslive snapshots and rings, inbox, whiteboard, tracker synced
  data and tracker host registry/sync files have their own owners. Adjacent
  `workspace-admission.json`, `hire-proposals.json` and `visibility.json`
  remain outside the listed bundle. Preserve their integration points.
  Follow `ai-docs/manuals/shipped-surface-boundary.md` for any new diagnostics
  or other shipped text.

## Prior Decisions

- 261118-feat-ws-display-transcript-memory-first (2026-11-19, commit): "Keep both kick writes and every consumed-input/replay-sensitive record mutation immediate: Mail, Reports, Steered/SteerSeq/TurnMark, PendingReports, and ring-tied HolderSeq." — bearing: constrains
- 261120-refactor-ws-agent-workers-under-holders (2026-11-20, commit): "Owner1862=a accepts inherited cursor writes for this merge and directs their removal to the SQLite daemon-metadata bundle." — bearing: supports
- 261113-feat-ws-dashboard-session-rail-tree (2026-11-14, commit): "Store follows workspaceStore: a mutex, a re-read of the file before every op, and writePrivateFile." — bearing: constrains
- 1680ca3a8 (2026-11-22, commit): "Reattached agy workers kept a daemon-sent prompt's turn open only by the in-memory outstanding map, which a replacement daemon lacks" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsagent/record.go, agents-plugin-tool/internal/wsdashboard/rail_layout.go, agents-plugin-tool/internal/wstrackerhook/state.go and direct MCP callers |
| scope.surface | cross-module | Record authority crosses wsagent, wsdashboard and MCP; agents-plugin-tool/internal/wsagent/record.go#L290-L298 |
| scope.new_public_symbol | unknown | The daemon routing and store API are not specified |
| scope.new_type_contract | yes | SQLite recovery records and authenticated daemon request contract are required by Decisions (1986), (1991) and (1992) |
| scope.test_surface | existing | agents-plugin-tool/internal/wsagent/transcript_hot_path_test.go covers record replacements; Phase 1 also requires old-format import and restart tests |
| complexity.reuse_points | confirmed | agents-plugin-tool/internal/wsstore/store.go#L145-L162 and agents-plugin-tool/internal/wsstore/retry.go#L22-L48 provide SQLite setup and retry |
| complexity.side_effect_risk | high | Authority cutover changes persistence and daemon replacement across multiple stores |
| risk.correctness | high | Holder replay, consumed input and cursor ordering must survive crashes |
| risk.fit | high | Direct callers, shared config roots and existing dashboard integration constrain the cutover |
| risk.test | high | Mixed-version recovery, daemon replacement and Windows AV performance require broad verification |
| risk.security_or_contract | high | Tracker admission and permission provenance depend on protected records and authenticated routing |

## Phases

Sequence under (2008).

### Phase 1: Build the store and legacy import without wiring callers

- Implement the shared store, repeatable import and transactional completion
  marker for eligible metadata. Keep active callers on their existing paths.
- Verify old-data import including CLI-only labels/hiring, original byte
  retention, absent/invalid/future inputs, unknown fields, and registration or
  holder identity isolation. Verify a staged import can retry after
  interruption. Capture a comparable legacy baseline and record the
  directional compatibility evidence for cutover.

### Result (09cf15e92) - 2026-11-22

Landed in 73434818e (store and import) and 09cf15e92 (lite review fixes).
The new package `agents-plugin-tool/internal/wsmeta` has no callers yet:
every live store still uses its legacy file.

- **Store** (`store.go`): one `<dash>/metadata.sqlite` per dashboard directory
  (1988). It runs WAL with `locking_mode=EXCLUSIVE`, so there is no `-shm` and
  a second daemon cannot open the database while the first holds it (bounded
  2 s open retry). Other settings: `busy_timeout=100` with a bounded busy
  retry, one connection, foreign keys on. The database and its WAL are
  pre-created owner-only (0600). Schema version 1; a newer schema is refused
  (`ErrNewerSchema`). Live record and cursor writes commit at
  `synchronous=NORMAL`; stage, fence and import commits run at `FULL` (2006).
  Auto-checkpoint is off, a 30 s idle `TRUNCATE` checkpoint runs, and `Close`
  checkpoints too.
- **Capture journal** (`capture.go`, 2125/2135):
  - `Stage` discovers every bundled file, CLI-only session directories
    included. Session files: `agent.json`, `permission.json`, `label.json`,
    `ack-check.json`, `hiring.json`. Dashboard files: `support.json`,
    `rail-layout.json`, `plan-usage*.json` and its histories,
    `tracker-hooks.json`. Directories: `pi-probes-v1/`, `memos/`,
    `tracker-hooks/`.
  - Each file's original bytes and digest are stored per path with the
    session generation (unix `dev:ino`, Windows creation time).
  - `PrepareFence` durably records the fenced result's digest (`absent` for a
    removal) before the replacement, and `MarkFenceLanded` marks it in place.
    A retry reuses a staged original only when the live digest equals the
    fence digest. Otherwise it re-stages the live bytes and reports the path
    in `Unfenced`.
  - A vanished or recreated session directory loses its journal rows and is
    never recreated.
  - A transient read failure (a sharing violation that outlives
    `wsstate.ReadFile`'s retry) keeps the earlier row unchanged and reports
    the path in `Unread`.
- **Import commit** (`import.go`):
  - `Commit` runs in one `FULL` transaction: it re-runs the stage pass,
    refuses on any `Unfenced` (`ErrFenceInvalidated`) or `Unread`
    (`ErrLegacyUnread`) path, replaces the keyed tables, decodes each capture
    under a savepoint, runs `BeforeMarker`, and writes
    `import_state=complete`.
  - Each capture's status is recorded in the journal: imported, absent,
    invalid, future, ignored or retained. An invalid or unreadable
    (including dangling-link) permission selection becomes a fail-closed
    `invalid` row (1993).
  - After the marker, `Stage` and the fence calls return `ErrImportComplete`
    and `Commit` returns `AlreadyComplete`. Before it, live writes and `Load`
    return `ErrNotImported`.
- **Records and cursors** (`records.go`, `snapshot.go`):
  - `PatchRecord` appends top-level member deltas (null removes a member) and
    folds them into the snapshot after `RecordDeltaBound`=32 (1991).
  - `SetHolderCursor` is keyed by (reply_id, holder_id) and only advances. It
    accepts writes from the record's current holder only (`ErrStaleHolder`
    otherwise) and drops earlier holders' cursors (1992).
  - `Load` returns the whole startup snapshot. Unknown members stay as raw
    JSON.

Verification (macOS arm64):

- `go test ./internal/wsmeta/`: ok.
- `go test -race -count=3 ./internal/wsmeta/`: ok (11.9 s).
- `go build ./...`: ok.
- `go vet ./internal/wsmeta ./internal/wsagent`: ok.
- `GOOS=windows go vet ./internal/wsmeta`: ok.
- `gofmt -l`: clean.
- Tests cover:
  - Old-data import: CLI-only label and hiring, original bytes, absent,
    invalid and future inputs, unknown members, registration and holder files
    left untouched, and a tree-state check showing the import writes no
    legacy file.
  - Interrupted stage, and an interrupted commit retried from live unfenced
    files (a pruned session is not imported or recreated; a latecomer is
    imported).
  - A recreated session directory losing its journal rows.
  - A fence landed before its journal mark, and a foreign write over a fence.
  - A transient read leaving a fenced original intact and blocking `Commit`;
    a stable read error failing closed.
  - `BeforeMarker` rollback, and a restart that ignores legacy edits.
  - Exclusive, `-shm`-free, owner-only files, and newer-schema refusal.
  - Holder cursor keying, including a late write from the old holder, and
    bounded deltas surviving a restart.

Baseline, macOS M3, no antivirus, `-benchtime 2000x` (supporting evidence
only; the Windows measurement is Phase 3):

| Write | Legacy path | wsmeta |
|---|---|---|
| Holder cursor (one holder_seq update) | ~190 µs (whole-record rewrite, 2 counted file ops, 5 by source count) | `BenchmarkHolderCursor` ~41 µs |
| Record member patch | ~190 µs (whole-record rewrite) | `BenchmarkRecordPatch` ~41–65 µs |
| Permission change | `BenchmarkMetadataBaselinePermissionChange` ~200 µs (3 counted file ops) | n/a |

The legacy benchmarks are in
`wsagent/metadata_baseline_bench_test.go`
(`BenchmarkMetadataBaselineHolderSeqRecordRewrite`,
`BenchmarkMetadataBaselinePermissionChange`). Syscall counts were not captured
because macOS tracing needs root.

Compatibility evidence:

- Phase 1 is T0. The only new files are `<dash>/metadata.sqlite` and its
  `-wal`. No old code enumerates the `<dash>` root: wsdashboard's `ReadDir`
  calls cover session subdirectories and fs routes only, and `registry.go:297`
  reads `<dash>/sessions`.
- Cutover fences, read from current source as a proxy for the pinned old
  binary:
  - An invalid permission file fails closed:
    `wsagent/permission_store.go:120-138`, dangling link at `:124`.
  - Hiring is refused when `Enabled` is false:
    `wsdashboard/session_hire.go:131`.
  - Tracker support refuses a set `ClosedAt` (`mcp/tracker_support.go:58`)
    and resolves the profile at `:61`.
  - The profile grammar is `^[a-z][a-z0-9-]*$`
    (`wsagent/profile.go:57,361`), and `ResolveProfile` validates it first
    (`:398-400`).
  - Hooks go idle when `tracker-hooks.json` is absent:
    `wstrackerhook/runner.go:226-229`.
- Tests that run the old binary belong to Phase 2.

Decisions:

- Pi probe results are imported, not re-probed.
- Memo documents keep their sha256 key.
- `tracker-hooks.json` is captured for its retained bytes only and is not
  decoded into a table.
- Histories keep the all-or-nothing `schema_version=1` rule.
- Read errors: dangling links, non-regular files and POSIX permission errors
  are stable `unreadable` inputs. On Windows every other read error is
  transient, because `ACCESS_DENIED` also comes from pending deletes and
  scanner opens.

Deferred to Phase 2/3:

- Applying the fences and old-binary execution tests (Phase 2).
- Confirming the idle-checkpoint interval and the NORMAL/FULL split with the
  Windows measurement (Phase 3).
- Per-harness history validity predicates beyond the schema rule (left to
  the history owners when they switch).
- Lite review Minor items, not fixed:
  - `delegated_authority: null` is imported as invalid, while the old reader
    accepts it (fails closed).
  - Labels are imported without the old `cleanLabel` validation; the Phase 2
    owner must re-validate on load.
  - Inode reuse can hide a recreated session directory on ext4/tmpfs, and NTFS
    tunneling can hide one on Windows.
  - Untested fence branches: an intent whose replacement never landed, fence
    calls on an unknown path, and pairing a lowered permission selector with
    its record (2135).

#### Edition (a6157bdfe) - 2026-11-22

Pre-merge review of 2976c3d53..de6bee0d8 (3 Critical, 9 Important, 3
Minor) is fixed in 3df153f83..a6157bdfe. Every finding is fixed and none is
rebutted. Each fix has a test that fails without it.

- **C1, fence proof** (ba9261aba): the store now applies the fences itself.
  `ApplyFence(rel, bytes)` records the fenced digest and a random past
  even-second sentinel mtime durably, writes a temp file with that mtime and
  atomically replaces the path. A landed replacement is proven by digest and
  exact sentinel mtime, so a byte-identical legacy write is not the fence.
  `ApplyRemovalFence(rel, witness)` moves the file to a fresh witness path
  that only the new daemon reads. Proof is `rel` absent and the witness
  digest equal to the staged original, so an owner's own deletion is not
  taken for the fence and its original is not restored. Tests are in
  `fence_test.go`.
- **C2, durable fence obligation** (ba9261aba): `fence_required` and
  `fence_kind` survive a re-stage. `StageResult.Pending` lists required
  fences not proven in the pass, and `Commit` returns `ErrFencePending`
  until they are proven, including after a restart.
- **C3, owner-only files** (81a62ad33): every `Open` narrows the database,
  `-wal` and `-journal` to owner-only. On POSIX this is 0600, tested by
  `TestReopenNarrowsWidenedFiles`. On Windows it is a protected DACL with one
  full-access ACE for the token user, set with `SetNamedSecurityInfo`, and
  the `-wal` is pre-created. `GOOS=windows go vet` passes.
  `TestDatabaseFilesHaveOwnerOnlyACL` is still unrun; it is left to the lead
  on a Windows host.
- **I1, DSN** (81a62ad33): the driver gets an escaped `file:` URI.
  `TestOpenUnderRootWithURIMetacharacters` uses a root containing `?#%`.
- **I2, holder change** (3df153f83): a `holder_id` change without
  `holder_seq` now adds `holder_seq: null` and drops the reply's cursors. This
  matches legacy `runner_process.go:283`, which resets `HolderSeq` to 0.
- **I3/I4, record and history validity** (3df153f83): `CommitOptions.Validate`
  injects the legacy owners' validators, and `RequiredValidators` lists
  `agent_record` and the three usage histories. A missing one returns
  `ErrValidatorMissing`. Each validator runs after wsmeta's own decode, inside
  the capture's savepoint. The new exports `wsagent.ValidateLegacyRecord` and
  `wsdashboard.ValidateLegacyUsageHistory` share the legacy readers' decode,
  and tests pin each one against its reader.
- **I5, null delegation** (3df153f83): `delegated_authority: null` imports as
  set and false, as in `wsagent/permission_store.go:148-153`.
- **I6, persistent Windows denial** (b68961a96): read errors fall into three
  classes.
  - Windows sharing and lock violations stay transient.
  - Windows `ACCESS_DENIED` is confirmable. It becomes `unreadable`, a
    fail-closed selection for its own session, after
    `UnreadConfirmPasses`=3 passes and `UnreadConfirmAfter`=5 s, counted in
    `legacy_unread`.
  - On POSIX, `ErrPermission` remains stable.

  A denial that clears resets its count. Fenced rows never confirm.
- **I7, FIFO through a link** (81a62ad33): a symlink is judged by its target.
  POSIX reads open with `O_NONBLOCK` and require a regular file by `fstat`.
  `TestLinkToFIFOIsUnreadableAndNeverBlocks` covers this.
- **I8, benchmark fairness** (a6157bdfe): both sides now advance the sequence
  on the same prebuilt record (`wsmeta/testdata/baseline-record.json`), and
  only the advance is timed.
- **I9, session generation** (51822bc25): the generation is now:
  - darwin: `dev:ino` plus birthtime;
  - linux: `dev:ino` plus statx btime;
  - Windows: volume serial, file index and creation time;
  - other unix: `dev:ino`.

  A recreation still missed (for example a filesystem without birth time)
  cannot reuse the deleted session's original, because the C1 proof needs
  the store's own sentinel or witness.
  `TestMissedRecreationNeverReusesTheDeletedSessionsOriginal` covers this.
- **m1, labels** (3df153f83): labels follow legacy `cleanLabel`: trimmed, no
  control characters or U+2028/2029, at most 64 runes. A blank label is no
  label; anything else is invalid.
- **m2, memo nulls** (3df153f83): a null memo item is kept, as in legacy
  `memos.go:48-66`.
- **m3, cursor range** (3df153f83): `SetHolderCursor` rejects a sequence
  above `MaxInt64`, and an imported one gets no cursor.

API changes against the Result above:

- `ApplyFence` and `ApplyRemovalFence` replace `PrepareFence` and
  `MarkFenceLanded`.
- `Commit` needs `CommitOptions.Validate`.
- `StageResult` gains `Pending`.
- The read-error decision in the Result is refined by I6: `ACCESS_DENIED` is
  no longer transient forever.

Of the deferred items above, these are now resolved:

- The history validity predicates (I4).
- All four lite-review Minor items: null delegation, `cleanLabel`, inode
  reuse and fence branch coverage.

Verification (macOS arm64):

- `go test -race -count=1 ./internal/wsmeta/`: ok.
- `go test -count=1 ./internal/wsagent/`: ok.
- The wsdashboard validator tests: ok.
- `go vet` on wsmeta, wsagent and wsdashboard: ok.
- `GOOS=windows` and `GOOS=linux` `go vet ./internal/wsmeta`: ok.
- `go build ./...`: ok.
- `gofmt -l internal/wsmeta`: clean.

#### Edition (ddc951bfa) - 2026-11-22

Follow-up to C1, at the lead's request: does a backdated sentinel mislead
any reader that caches a fenced file by mtime? All non-test mtime readers
were checked; the fenced paths are `agent.json`, `permission.json`,
`hiring.json` and the removed `tracker-hooks.json`.

- No legacy reader of a fenced path orders mtimes; the caches compare for
  equality only.
  - `wsagent/permission_store.go:85` (the hosted page's revalidation)
    compares mode, size, mtime and identity through
    `wsstate.SameFileMetadata`. Launch admission re-reads unconditionally
    (`:62-68`).
  - `wstrackerhook/runner.go:215` stamps `tracker-hooks.json` by size and
    mtime. The removal fence makes the file absent, which changes the stamp.
  - `wsdashboard/registry.go:417` ages a session by its directory mtime,
    which the fence's rename advances.
- The equality hazard: on Windows the identity is the creation time, which
  NTFS tunneling can carry across the replace. A same-size re-fence over an
  earlier sentinel could then look unchanged. `ApplyFence` now draws its
  sentinel until its second differs from the live file's mtime.
  `TestRefenceNeverReusesTheLiveMtime` fails without this change.
- When a proof fails, the live bytes are re-staged and `Commit` refuses
  (`ErrFenceInvalidated`, then `ErrFencePending`) until the path is fenced
  again. The original is never written back.

Verification: `go test -race -count=1 ./internal/wsmeta/` ok;
`go vet` (darwin, `GOOS=windows`, `GOOS=linux`) ok; `gofmt` clean.

#### Edition (ef0259ce7) - 2026-11-22

The second re-review of 3df153f83^..e93782cd3 found 4 Critical and 6
Important issues. All are fixed in ef0259ce7 and none is rebutted. Each
fix's test was run with the fix reverted and failed.

The fences now follow one lifecycle, stated with its invariants at the
fence constants in `capture.go`:

```text
none -> intent -> landed
any state -> none, when a pass cannot prove the publication
```

Its rules:

- A fence record changes only while its publication is not live.
- A fence applies only over the staged original, or over its own proven
  publication. Anything else returns `ErrFenceStale`.
- A publication is made durable before it is marked landed.
- `Commit` needs every required fence to be both landed and proven by its
  own pass.
- When proof fails, the live state is re-staged and the fence must be
  applied again. The original is never written back.

The earlier rule that a removal is satisfied when the path was staged
absent is gone; a required removal is re-applied like any other fence.

Critical fixes:

- **Retry overwrote the proof of a landed fence (C1):** a retry interrupted
  before it publishes now keeps the record of a publication already in
  place, so the migration's bytes are never staged as the original. Test:
  `TestInterruptedRetryKeepsTheFirstPublicationProvable`.
- **Stale witness blocked a re-fence (C2):** the witness is now fixed for
  each path and replaced on re-fence. Removing a path whose original was
  absent also removes any stale witness. Tests:
  `TestRemovalRefenceReplacesAStaleWitness` and
  `TestOwnerDeletionDuringPendingRemovalIsNotRestored`.
- **No durability barrier before the marker (C3):** a replacement now
  syncs its temporary file after setting the sentinel time, renames it,
  then syncs the directory. A removal renames, then syncs the directory.
  - On POSIX the directory sync is an fsync.
  - On Windows it is a write-through rename of a scratch file, which
    relies on NTFS's ordered metadata log.

  A publication that a pass finds live but not marked landed stays pending
  until the fence is re-applied. Re-applying only syncs the directory.
  Tests: `TestFencePublicationIsDurableBeforeItLands` and
  `TestUnsyncedPublicationNeedsReapplyBeforeCommit`.
- **Failed identity lookup discarded a session (C4):** a lookup that fails
  now fails the pass and changes nothing, instead of returning a weaker
  token. On Linux, a missing statx (`ENOSYS`, or `EPERM` from seccomp) uses
  the device and inode token consistently. Test:
  `TestInconclusiveSessionIdentityKeepsItsJournal`.

Important fixes:

- **I1, Windows journal ACL:** every open creates the `-journal` with the
  owner-only ACL if it is missing, and propagates errors other than
  absence. SQLite removes that empty journal only on an empty database, and
  its own journal then holds no page images. Runtime check: the Windows
  test now covers the journal (left to the lead).
- **I2, validator identity:** the validator signature is
  `LegacyValidator(rel, replyID, raw)`. `wsagent.ValidateLegacyRecord` and
  `wsdashboard.ValidateLegacyUsageHistory` now have this signature, so they
  wire in without adapters. Test: `TestOwnersValidatorsWireIntoCommit`.
- **I3, pending deletes:** a confirmable denial is confirmed only for an
  input that was never readable. A file read earlier keeps its staged
  original and blocks the commit. Test:
  `TestDenialAfterAReadablePassKeepsTheOriginal`.
- **I4, relative root:** `Open` resolves the root to an absolute path once.
  Test: `TestOpenResolvesARelativeRoot`.
- **I5 and I6, witness path and moved links:** the witness must be a plain
  file name in the path's own directory. It rejects separators, `:`,
  dot names and bundled names. Because the witness is a sibling, a moved
  relative link still resolves to the same file. Test:
  `TestRemovalWitnessStaysBesideItsPath`.

API changes:

- `ApplyRemovalFence(rel, witness)` now takes a file name, not a
  dashboard-relative path.
- New errors: `ErrFenceStale` and `ErrFenceConflict`.
- `CommitOptions.Validate` holds `LegacyValidator` values.

Verification (macOS arm64):

- `go test -race -count=1 ./internal/wsmeta/`: ok (12.3 s).
- `go test -count=1 ./internal/wsagent/`: ok (147.5 s). Two earlier runs
  were disturbed by sibling test runs loading the machine.
  - The first hit the default 10-minute timeout.
  - The second failed two timing tests:
    `TestDaemonStartDeletesAnExpiredSubtreeWithoutALoop` and
    `TestAdmitCompletionWaitsForTheNextTurnWithoutASteer`.

  Both tests pass 5 out of 5 runs in isolation (`-count=5`), and neither
  touches code this branch changes.
- The wsdashboard validator tests: ok.
- `go vet` on wsmeta, wsagent and wsdashboard: ok.
- `GOOS=windows` and `GOOS=linux` `go vet ./internal/wsmeta`: ok.
- `go build ./...`: ok.
- `gofmt`: clean.

#### Edition (a87930b1a) - 2026-11-22

The third re-review of 2976c3d53..8f6f007e8 found 4 Critical and 3
Important issues. Five are fixed. One is partly fixed and its remainder
waits on a product decision (NEEDS-DECISION). None is rebutted. Each
fix's test was run against the code before the fix and failed.

Lifecycle changes (`capture.go`, at the fence constants):

- The cutover calls (`Stage`, `ApplyFence`, `ApplyRemovalFence`,
  `Commit`) hold one cutover lock end to end. A fence's journal record
  and its file-system change therefore form one transition.
- A new invariant: the migration deletes or overwrites only files it put
  in place itself.
- No earlier state can prove a new record. A new sentinel differs from
  the live mtime, and the witness path is durably empty before a removal
  record is written.
- A re-applied replacement is published again with the same bytes and
  sentinel.

Critical fixes:

- **Concurrent fences (`capture.go:260`), 33495fc96:** two fence calls
  could plan over the same original and publish in reverse order, or a
  publication could follow `Commit`. The cutover lock serializes them,
  and the landed mark updates only the exact record it published
  (`ErrFenceConflict` otherwise). Test:
  `TestConcurrentCutoverCallsWaitForAPublication`.
- **Stale witness proving a new removal (`capture.go:683`),
  6cb11f6cd:** before a removal record is written, the witness path is
  now durably emptied: a rename to a dot name, a directory sync, then
  the unlink. Test: `TestStaleWitnessNeverProvesANewRemoval`.
- **Witness name overwrote or deleted other files (`capture.go:315`),
  6cb11f6cd:**
  - Each removal record stores the identity (a token that survives a
    rename) and the content of the file it moves (`witness_id`,
    `witness_content`).
  - Only a file matching both is cleared. Any other file there refuses
    the removal with `ErrWitnessOccupied`, and so does a legacy symbolic
    link that resolves to the witness.
  - A hard link moves intact.
  - Removal fences are limited to fixed bundled paths, whose rows survive
    absence.

  Test: `TestRemovalNeverTouchesAFileItDidNotPlace`.
- **Windows file creation (`private_windows.go:35`), a87930b1a:** a
  missing database, WAL or journal file is created with `CreateFile`
  (`CREATE_NEW`, no sharing), using the owner-only security descriptor as
  its security attributes. No other principal can open it at any point.
  Test: `TestDatabaseFilesAreOwnerOnlyFromCreation` (Windows only;
  compiled and vetted, not run).

Important fixes:

- **Confirmed denial blocked its fence (`capture.go:388`), ca51e2576:**
  `planFence` now plans over a denial a pass confirmed. A shared rule
  (`denialConfirmable`) keeps an unreadable original confirmed under a
  fence record. Test: `TestConfirmedDenialTakesItsFence`.
- **Linux identity without statx (`gen_linux.go:36`), 5b0955758:** the
  fallback emits the same device (major:minor) and inode components.
  `sameGeneration` compares birth times only when both tokens carry one.
  Test: `TestIdentityWithoutBirthTimeIsTheSameSession`.
- **Windows durability barrier (`sync_windows.go:29`), partly fixed in
  3667ab036:**
  - Every rename and deletion the migration makes is now itself
    write-through on Windows, including a re-applied replacement, which
    is published again. Test: `TestReappliedReplacementIsPublishedAgain`.
  - Still on the barrier alone: a re-applied removal whose rename an
    interrupted attempt made, and an original that was already absent.
    Redoing that removal would mean moving the original back, which the
    lifecycle forbids.
  - NEEDS-DECISION: verify that the volume is NTFS or ReFS and refuse
    the barrier elsewhere, or accept the NTFS ordering assumption as
    documented.

API changes:

- New error: `ErrWitnessOccupied`.
- `ApplyRemovalFence` refuses paths without a fixed bundled name.
- New schema columns: `legacy_captures.witness_id` and
  `legacy_captures.witness_content`.

Verification (macOS arm64):

- `go test -race -count=1 ./internal/wsmeta/`: ok (11.6 s).
- `go test -count=1 -timeout 25m ./internal/wsagent/`: ok (149.8 s).
- The wsdashboard validator tests and the wsagent record validator
  tests: ok.
- `go vet` on wsmeta, wsagent and wsdashboard: ok.
- `GOOS=windows` and `GOOS=linux` `go vet ./internal/wsmeta`: ok, and the
  Windows test binary compiles.
- `go build ./...`: ok.
- `gofmt`: clean on this round's files. `plan_usage_history.go:278` was
  already unformatted on 2976c3d53.


#### Edition (d5ccd98db) - 2026-11-22

This resolves the NEEDS-DECISION left on the Windows barrier in Edition
(a87930b1a). The lead chose option (a): verify the file system and refuse
the cutover on any other.

- **What changed:** every cutover call checks the file system of each
  directory it can change. On Windows only NTFS and ReFS are accepted.
  - On any other file system, or one that cannot be determined, the call
    refuses with `ErrCutoverUnsupported` before it changes anything.
  - On POSIX the barrier is a directory fsync and needs no check.
  - The Windows query is `GetVolumeInformationByHandle` on the directory
    itself, so a mounted folder reports the volume it mounts.
- **Where it is checked:**
  - `Stage` and `Commit`: the dashboard directory, every session directory
    and every keyed-file directory.
  - `planFence`: the fence's own directory, before its record is written.
  - `syncDir`: so `landed` never rests on an unverified barrier.

  Each check runs after the import-state check, so a completed import
  still answers `AlreadyComplete` or `ErrImportComplete`.
- **What a refusal leaves:** no capture or fence record in the journal, the
  legacy tree unchanged and the import incomplete. The legacy files stay
  the authority. Each refusal is logged through `Options.Logf` with its
  reason (the directory and its file system).
- **Startup is not blocked:** `Open` and pre-import reads never run the
  check, and wsmeta is still unwired in Phase 1. Phase 2's caller must
  treat `errors.Is(err, ErrCutoverUnsupported)` as staying on the legacy
  store.
- **Tests:**
  - `TestUnsupportedFileSystemRefusesTheCutover` covers:
    - a refusal before staging (the store still opens, the reason is
      logged)
    - a single session directory on another volume
    - an unknown file system
    - fences refused before their record
    - landed never set on a refused barrier
  - `TestSupportedFileSystemsCompleteTheCutover` runs the cutover through
    for POSIX, `NTFS`, `ntfs` and `ReFS`.
  - Removing any one of the four checks (Stage, planFence, Commit,
    syncDir) fails a subtest.
  - `TestPlatformFileSystemNamesTheVolume` is Windows only: compiled, not
    run.
- **API change:** new error `ErrCutoverUnsupported`.

Verification (macOS arm64):

- `go test -race -count=1 ./internal/wsmeta/`: ok (13.4 s).
- `go vet` on wsmeta, wsagent and wsdashboard: ok.
- `GOOS=windows` and `GOOS=linux` `go vet ./internal/wsmeta`: ok, and
  `GOOS=windows go test -c` builds.
- `go build ./...`: ok.
- `gofmt`: clean.


#### Edition (b018f9ca8) - 2026-11-22

The re-review of 2976c3d53..a16126ad4 found 3 Critical and 3 Important
issues. All six are fixed, one commit each, none rebutted. Each new test
failed on the code before its fix, either run directly or with the old
behavior restored.

Critical fixes:

- **Removal proof ignored the witness identity (C1), fc34f1605:** a
  removal is now proven only when the witness carries the identity
  recorded with the intent (`witness_id`). This holds on re-application
  and in every pass, so a byte-identical copy the owner placed at the
  witness proves nothing.
  - An empty recorded identity never proves.
  - A failed identity lookup is unread, not a mismatch.

  Test: `TestCopiedWitnessNeverProvesARemoval`.
- **Witness cleanup through a link chain (C2), 4cb24d692:**
  `routesThrough` now resolves the source one component at a time. It
  refuses (`ErrWitnessOccupied`) when any step names the witness entry:
  compared by name without case, in the witness's directory compared by
  identity. That covers second links and directory aliases.
  - Hard links still move intact.
  - A chain of more than 40 links is refused.

  Test: `TestRemovalNeverClearsAWitnessTheSourceResolvesThrough`.
- **The fallback error after a publication (C3), e834548f0:**
  `ErrCutoverUnsupported` (keep serving from legacy) is returned only
  while the journal holds no fence record. Otherwise the refusal is the
  new `ErrCutoverInterrupted`, across restarts too. The barrier re-check
  after a rename always reports interrupted. Test:
  `TestRefusalAfterAFenceIsNeverTheFallback`. Commit's call site landed
  separately in b018f9ca8: e834548f0 alone does not build.

Important fixes:

- **ReFS (I1), c30356343:** only NTFS is accepted. The barrier's ordering
  argument does not cover ReFS, and a documented ReFS setting ignores
  write-through. A cache root on a ReFS Dev Drive stays on the legacy
  store. Test: `TestUnsupportedFileSystemRefusesTheCutover/refused_on_ReFS`.
- **NTFS tunneling of the witness token (I2), c1c924803:** on Windows the
  witness identity is the volume serial plus the file index, without the
  creation time. Test: `TestWitnessIdentitySurvivesTunneling` (Windows
  only: compiled, not run).
- **A denied witness blocked Commit (I3), ffc386452:** over a confirmed
  denied original, a confirmable denial at the witness is that same state,
  and the removal is proven by identity. Test:
  `TestConfirmedDenialMovesWithTheWitness`.

Caller contract for Phase 2:

- `ErrCutoverUnsupported`: stay on the legacy store.
- `ErrCutoverInterrupted`: do not resume legacy service. Surface the
  error, because a fence may be live.

API changes: new error `ErrCutoverInterrupted`; the Windows allowlist is
NTFS only.

Verification (macOS arm64):

- `go test -race -count=1 ./internal/wsmeta/`: ok (15.5 s).
- The wsagent and wsdashboard validator tests: ok.
- `go vet` on wsmeta, wsagent and wsdashboard: ok.
- `GOOS=windows` and `GOOS=linux` `go vet ./internal/wsmeta`: ok, and
  `GOOS=windows go test -c` builds.
- `go build ./...`: ok.
- `gofmt`: clean.

#### Edition (58507fcbf) - 2026-11-22

The re-review of 2976c3d53..68fdc2bd1 confirmed I1 and I2 fixed. It found
3 Critical, 1 Important and 1 Minor issue. All five are fixed, one commit
each, none rebutted. Each new test failed on the code before its fix.

Critical fixes:

- **The fallback decision forgot a discarded fence (C3), 45636bf75:**
  every fence record now sets the meta key `cutover_started` in its own
  transaction, before its publication, and nothing clears it. Before,
  the decision read only capture rows, which a pass deletes for a
  vanished session. Test: `TestRefusalAfterAFenceIsNeverTheFallback/
  after_a_pass_discarded_the_fenced_rows`.
- **A reusable POSIX identity proved a removal (C1), a0b075d71:** witness
  identities are compared with `sameIncarnation`: equal tokens that carry
  a birth time (POSIX) or the NTFS file index. Device and inode alone
  never prove a witness.
  - The cutover is refused, by the same rule as the barrier, on a volume
    whose directory identity has no such evidence. That is Linux without
    statx birth times (a seccomp sandbox, older tmpfs) and other Unix
    systems.
  - A removal source whose own identity lacks it is refused before its
    record.
  - macOS (APFS), ext4/xfs/btrfs and NTFS pass; CI's ubuntu `/tmp` is
    ext4.

  Test: `TestReusableIdentityNeverProvesARemoval`.
- **An NTFS short name of the witness (C2), 677dc5def:** each path
  component in the witness's directory is resolved to its long name
  (`entryName`, which calls FindFirstFile on Windows) before the
  comparison. Hard links still move intact.
  - Seam test: `TestRemovalNeverClearsAWitnessThroughAnAlternateName`.
  - Real short-name test: `TestShortNameOfTheWitnessIsTheWitness` (Windows
    only: compiled, not run).

Important fix:

- **A denied witness blocked a re-fence (I3), 4e8324637:** the witness
  cleanup now reads a confirmable denial as the unreadable state the
  last removal recorded. The recorded identity then decides, as before.
  Test: two subtests of `TestConfirmedDenialMovesWithTheWitness`.

Minor fix:

- **Windows symlink privilege (58507fcbf):** `mustSymlink` skips only on
  `ERROR_PRIVILEGE_NOT_HELD`.

Remaining class of problem: the removal fence moves the owner's
directory entry and later proves "this witness entry is the file we
moved". Each round has found another way two names or two files can
look alike on some platform:

- inode reuse
- creation-time tunneling
- case folding
- link chains
- directory aliases
- 8.3 short names
- an ACL that moves with the file

Cases not yet handled, from the same class:

- APFS normalization-insensitive names: an NFD spelling of the witness
  passes `EqualFold`.
- NTFS stream syntax: `name::$DATA` opens the witness itself.
- overlayfs copy-up: the identity changes without the file changing, so
  the removal stays pending.

Replacement fences have needed no such fixes since their sentinel proof
landed.

Simpler alternative (needs a lead decision): publish the removal like a
replacement and never move the owner's entry.

- Write the staged original's bytes to the witness through `publishFile`
  under a fresh sentinel mtime, then delete the legacy path durably.
- Proof is the path absent and the witness matching the original's
  digest under its exact sentinel. That is the replacement proof.
- This removes `witness_id`, `sameIncarnation`, `routesThrough`,
  `entryName` and the denied-witness logic.
- The witness cleanup check becomes the same sentinel check.
- The barrier for a re-applied deletion stays.

It fits the Decision at line 398 ("retaining its bytes and making the
config available through a new-daemon-only input path"). The move is
only an implementation choice of the C1 Result at line 706. Two
behaviors differ:

- A legacy `tracker-hooks.json` that is a symbolic link becomes a copy
  of its target. Later edits to the target no longer reach the new
  daemon, which touches "owner-controlled reload".
- An unreadable original has no bytes to publish. It needs its own rule,
  either a refusal or a plain retire.

New observation, not fixed: a session whose directory is moved aside
during the cutover loses its journal rows, its staged original included.
When the directory is restored, its fenced selector is staged as the
original and imported as invalid. C3 above only keeps the fallback
decision correct. The original itself is lost to the journal.

Verification (macOS arm64):

- `go test -race -count=1 ./internal/wsmeta/`: ok (15.7 s).
- `go test -count=1 ./internal/wsmeta/`: ok.
- `go vet` on wsmeta, wsagent and wsdashboard: ok.
- `GOOS=windows` and `GOOS=linux` `go vet ./internal/wsmeta`: ok, and
  `GOOS=windows go test -c` builds.
- `go build ./...`: ok.
- `gofmt`: clean.

#### Edition (b72e720c4) - 2026-11-22

Decision note. This amends the Phase 1 Result's removal fence. The
Decision at line 398 is unchanged.

- **Owner decision (board 2162 = a): a removal publishes and never
  moves.** `ApplyRemovalFence(rel, witness)` does the following:
  1. Records the original's digest and a sentinel mtime.
  2. Publishes the staged original bytes at the witness through the
     replacement path (`publishFile`).
  3. Deletes `rel` durably.

  Proof is the replacement proof: `rel` absent and the witness holding
  the digest under the exact sentinel. For an absent original, proof is
  `rel` and the witness both absent.
  - The witness path is emptied durably before a new record, but only of
    the migration's own last publication there. The sticky
    `witness_digest` and `witness_mtime` columns replace `witness_id` and
    `witness_content`. Any other entry is `ErrWitnessOccupied`.
  - A symbolic-link legacy `tracker-hooks.json` becomes a regular copy of
    its target. The owner accepted this.
  - Rare aliases are intentionally not fenced: hard links, links swapped
    mid-cutover, short names and similar. The only guarantee is that no
    data is lost. The staged original keeps the bytes, a link target is
    never touched, and the witness only ever loses the migration's own
    file.
  - Deleted with their tests: file identity (`ident_*`), short-name
    resolution (`entryname_*`), `routesThrough`, `sameIncarnation` and the
    identity preflight, and the denied-witness logic. This was bd08ab4dd.
    Non-test code went down by 126 lines over the round.
- **Technical decision (auto-confirmed): an unreadable original refuses
  the cutover.** `ApplyRemovalFence` and `Commit` return the new
  `ErrRemovalUnreadable`. It is wrapped in `ErrCutoverUnsupported`, or in
  `ErrCutoverInterrupted` once a fence exists. The legacy file is kept
  and nothing is imported. For Phase 2: apply the removal before the
  replacement fences, so that this refusal can still be the clean legacy
  fallback.
- **Moved-aside sessions (b72e720c4):** a vanished session with a fence
  record is kept as missing, not discarded (`legacy_sessions.missing`,
  `StageResult.Missing`). Its rows are neither re-read, deleted nor
  imported.
  - Restored under the same generation, it is picked up and its fences
    proven.
  - Recreated under another generation, it is discarded.
  - A commit while it is away imports everything else and keeps its rows.
  - Each session's reads run in a savepoint, so a directory that vanishes
    mid-read changes nothing.

  Test: `TestMovedAsideSessionKeepsItsJournal`; all three subtests failed
  on the old logic.

Other test changes:

- New: `TestUnreadableOriginalRefusesTheCutover` and
  `TestRemovalInterruptedBeforeItsDeletionIsReapplied`.
- `TestRemovalWitnessStaysBesideItsPath` now asserts the
  symbolic-link-to-copy behavior.
- `TestRefusalAfterAFenceIsNeverTheFallback` forces its discard with a
  recreated session.

API changes:

- `ErrRemovalUnreadable` and `StageResult.Missing` are new.
- `ErrCutoverUnsupported`'s text no longer names a file system.
- The new hit point `fence-deleted` follows the legacy deletion.

Verification (macOS arm64):

- `go test -race -count=1 ./internal/wsmeta/`: ok (15.4 s).
- `go test -count=1 -v ./internal/wsmeta/`: ok, no failures or skips.
- `go vet ./internal/wsmeta`, also with `GOOS=windows` and `GOOS=linux`:
  ok.
- `GOOS=windows go test -c ./internal/wsmeta`: builds.
- `go build ./...`: ok.
- `gofmt`: clean.

#### Edition (19e629158) - 2026-11-22

Round 8: fixes from the codex re-review of d89e955ee, as graded by the
lead. This is post-cut work, not in 0.51.0. Each fix has a test that
failed before it.

- **Compare before delete (018ba7047).** `ApplyRemovalFence` reads `rel`
  again right before deleting it. Only the planned version, which is the
  one published at the witness, is retired. An owner save made in between
  is kept, and the call returns `ErrFenceStale`; the caller stages and
  applies again. An original staged absent and created in the window is
  handled the same way.
  - The gap between that read and the deletion is not fenced. The lead
    accepted narrowing it to a check right before the delete.
  - Test: `TestRemovalKeepsAnOwnerSaveMadeDuringIt`.
- **An unreadable removal outranks fence errors at commit (2907271ae).**
  `Commit` checks for an unreadable hooks config before it checks for
  invalidated or pending fences. The caller gets `ErrRemovalUnreadable`
  with its classification instead of a fence retry that cannot succeed.
  - Test: `TestUnreadableRemovalOutranksAPendingFence`.
- **A fence waits for its session (19e629158).** `planFence` refuses a
  session row with `ErrFenceStale` and writes nothing while either holds:
  - the session is marked missing (or has no `legacy_sessions` row);
  - its live directory is absent or of another generation than the row.

  A pass that confirms the directory lifts the refusal.
  - Test: `TestFenceWaitsForAPassToConfirmTheSession`.
- **Accepted without a fix (minor follow-up):** suppose the process
  crashes after the witness publish and before the legacy deletion. If the
  owner deletes the legacy file before the retry, the retry commits the
  staged bytes. This needs a crash in that window plus a manual deletion
  before restart, and the owner can delete the config again.

Verification (macOS arm64):

- `go test -count=1 -v ./internal/wsmeta`: ok, 90 PASS lines, no failures
  or skips.
- `go test -race -count=1 ./internal/wsmeta`: ok (16.5 s).
- `go vet ./internal/wsmeta`, also with `GOOS=windows` and `GOOS=linux`:
  ok.
- `GOOS=windows go test -c ./internal/wsmeta`: builds.
- `go build ./...`: ok.
- `gofmt`: clean.


### Phase 2: Switch authority and remove the holder sequence rewrites

- Depends on Phase 1. Route affected direct Go readers through the daemon
  before agent record cutover under (1980), (1986) and (2121). The holder's
  sequence-only writes for a final ring line and a dirty heartbeat cease
  replacing the whole record.
- Apply (2125) under the (2122) lock and drain boundary. Interrupt before a
  fence, after a partial fence, and on both sides of the import commit. Verify retry
  after the legacy writers exit, recovery of the original staged bytes,
  import and marker visibility together, and protected adjacent files.
  With the pinned old daemon as an actual intervening writer after an
  interruption before fencing (2135), lower a permission and verify the
  retry imports the lowered selection; kill after a fence replacement but
  before its journal mark and verify recovery; prune a session between
  interruption and retry and verify it is neither imported nor recreated.
- Use the pinned old binary to verify fresh lead and worker launch refusal
  from the invalid permission file, including the case where it was absent at
  import. Verify the existing held-process rejoin behavior, hiring disabled
  with the hired identity preserved, tracker support writes refused through
  `ws-mcp call` with a retained support key whose record was open at
  cutover (2136), with the key of a child open at cutover at a store root
  whose profile grants the tracker tool, and with the key of a child that
  qualifies only after cutover through a profile override or a new store
  registration, even with a configured profile named like the sentinel;
  verify the old daemon starts with fenced children present, tracker
  writes still fail after a fenced child's holder rejoins, the old prune
  keeps their directories after retention, and re-upgrade restores their
  original profile and open state; and
  hooks disabled while the legacy
  config path is absent. Confirm the new daemon sees the imported values and
  preserves hook reload and progress on its new input path.
- After cutover, exercise new writes and shutdown with zero legacy
  publications. Edit and delete existing bundled files and add a new bundled
  legacy file with the old binary. Verify re-upgrade restores from SQLite
  alone and preserves the shared contracts.
- Verify hard-kill/rejoin and graceful replacement for held leads and
  workers, including interrupted ring append and pending consumed input.
  Verify a prestarted successor, including Support, reads metadata only after
  acquiring the lock, and the predecessor drains SQLite users and closes its
  handle before unlock. Exercise authenticated direct calls and stdio with the
  daemon available and unavailable, tracker admission, permission provenance
  and fail-closed origin validation, terminal ownership/pruning, dashboard-only
  layout actions, and dashboard refresh/notifications. Verify schema/error
  protection and the documented offline SQLite recovery path.
- Reproduce an old registry prune of an expired on-disk record after the
  corresponding session was revived in SQLite. Document the mixed-version
  prune residual and the other same-root downgrade residuals in user-facing
  upgrade guidance under (2125).

### Result (2fac4e31b) - 2026-11-27

Phase 2 landed across seven commits (`645c62d87`..`2fac4e31b`) on
`impl/develop/tweak-canon-owl` (base `b118102ab`). Ticket stays in `ready/`
with Phase 3 (Windows heavy-AV baseline/after measurement) remaining.

What landed:

- **SQLite live CRUD and cutover orchestrator (`645c62d87`, `internal/wsmeta`):**
  added live CRUD APIs for permissions, support mappings/selection, rail
  layout, workspace memos, labels, guarded answer checks, hiring metadata,
  latest/delta plan usage history, tracker hook state, and session deletion,
  plus the `(2125)`/`(2135)`/`(2136)` `Cutover` orchestrator applying all four
  fences in order (`tracker-hooks.json` removal to `tracker-hooks-v2.json`
  first, invalid `permission.json` selector on every session including absent,
  hiring `Enabled=false` preserving `Hired` and `RequestID`, support
  `closed_at` on parentless non-lead records, and `!cutover-fenced` profile on
  open non-lead child records) with retry/backoff, `BeforeMarker` fence
  re-verification, and `ErrCutoverUnsupported` vs `ErrCutoverInterrupted`
  classification.
- **Daemon `/meta/*` routing for direct Go callers (`2702dad66`, `internal/mcp`,
  `internal/wsdashboard`):** added loopback-only, bearer-authenticated
  `POST /meta/*` routes with `Origin`/`Sec-Fetch-Site` and body-size guards,
  routed direct Go readers (tracker support admission, playbook terminology,
  tool aliases, hosted lineage, Pi live rows, whiteboard nudges, unregister
  checks, and registration pruning) through the daemon, and implemented
  `(1986)`/`(2121)` daemon-unavailable handling (raw `ws-mcp call` fails fast
  naming `ws-mcp daemon-start`; `serve --stdio` returns a recoverable
  unavailable error).
- **`wsagent` SQLite authority and cursor delta (`ce66e6ef4`, `internal/wsagent`):**
  switched conversation records (`PatchRecord` deltas against `recordBase`),
  holder sequence updates (`SetHolderCursor` only for ring-writing final lines
  and dirty heartbeats, removing whole-record `agent.json` rewrites under
  `(1992)`), fail-closed permissions, handoff/hire lookup, session deletion,
  and deferred Support loading (`DeferLoad`, keyed mappings/selection under
  `(1994)`) onto `wsmeta`.
- **Dashboard stores and tracker hooks v2 (`3b7892924`, `internal/wsdashboard`,
  `internal/wstrackerhook`):** wired SQLite-backed store modes for rail layout
  (in-memory generation + `ImportIssues`), workspace memos, latest plan usage
  and delta history (`MutateUsageHistory` + `ImportIssues`), labels, guarded
  answer checks (`ack_check` CAS), hiring (keeping `hire-proposals.json`
  separate), and `wstrackerhook` (`tracker-hooks-v2.json` reload + nonce-scoped
  `UpdateTrackerHook`).
- **Cutover activation and `(2122)` lock/drain barrier (`afa051209`,
  `internal/wsdashboard`, `internal/wsagent`, `cmd/ws-mcp`):** opened the
  single shared `wsmeta.Store`, ran `Cutover`, and completed deferred Support
  loading strictly under `dashboard.lock`; graceful shutdown stops `/meta/*`
  admission, drains in-flight requests, poll loop, and `BeforeUnlock` hooks,
  and closes SQLite before removing the endpoint and unlocking.
  `ErrCutoverUnsupported` stays on legacy; `ErrCutoverInterrupted` refuses
  startup.
- **Docs, mirrored resources, and fixture (`11c02cc41`):** updated both
  `agents-plugin/rsrc/tracker-hooks/tracker-hooks.md` and
  `agents-plugin-wsflow/rsrc/tracker-hooks/tracker-hooks.md` (with regenerated
  `rsrc/manifest.json` mirrors) to `tracker-hooks-v2.json`, updated
  `scripts/rail-headless.mjs` for dashboard/SQLite-backed labels and rail
  layout, and documented offline SQLite recovery `(1985)` plus `(2125)`/`(2136)`/`(2137)`
  same-root downgrade residuals (including mixed-version prune) in
  `ai-docs/manuals/ws-mcp.md` and `ai-docs/manuals/release-compatibility.md`.
- **Pinned pre-cutover integration suite (`2fac4e31b`,
  `cmd/ws-mcp/meta_cutover_integration_test.go`):** builds the pinned
  `b118102ab` binary via `git archive` alongside the current binary and proves
  interrupted cutover with an intervening old daemon lowering permissions and
  pruning sessions `(2135)`, fenced old-binary launch/hiring/hooks/all five
  `(2136)` tracker-write refusals/post-rejoin fenced child/prune retention and
  re-upgrade reload, zero post-cutover legacy writes and re-upgrade from SQLite
  alone, direct `ws-mcp call` and `serve --stdio` daemon-unavailable errors
  `(2121)`, `(2122)` `dashboard.lock` + Support drain barrier and `SIGKILL`
  hard-kill/rejoin cursor advancement, `(1985)` `schema_version` refusal and
  offline SQLite recovery, and mixed-version prune reproduction.

Decisions:

- Used separate leaf mutexes `d.hiringMu` and `d.labelMu` (lock order
  `hireMu -> hiringMu` and `hireMu -> labelMu`) so `approveHireLocked` ->
  `CreateHireLead` -> `OnHired` -> `RecordHired` cannot deadlock.
- Kept `pi-probes-v1` import-only in `wsmeta` because no live writer exists in
  the codebase.
- Kept excluded/shared contracts outside SQLite (`workspaces.json`,
  `ui-seen.json`, `workspace-admission.json`, `hire-proposals.json`,
  `visibility.json`, `registration.json`, holder files, `wslive`, `wsinbox`,
  `wsmailbox`, `wswhiteboard`, `wscred`).

Verification (macOS arm64):

- `cd agents-plugin-tool && go test -race -count=1 ./internal/wsmeta/... ./internal/wsagent/... ./internal/wstrackerhook/... ./internal/wsrsrc/... ./cmd/ws-mcp/...`:
  PASS (`wsmeta` 19.57s, `wsagent` 161.91s, `wstrackerhook` 7.18s, `wsrsrc`
  5.56s, `cmd/ws-mcp` 280.59s; all 6 `TestMetaCutover*` tests PASS under `-race`).
- `cd agents-plugin-tool && go test -race -count=1 -timeout 25m -skip "TestExecJobsChangedReachesTheRailStream|TestWideExplorerRouting" ./internal/wsdashboard/...`:
  PASS (922.38s). `TestExecJobsChangedReachesTheRailStream` was skipped per
  lead guidance; `TestWideExplorerRouting` is a pre-existing failure on base
  `b118102ab`.
- `cd agents-plugin-tool && go test -count=1 -timeout 45m ./internal/mcp/...`:
  PASS (698.81s).
- `cd agents-plugin-tool && go vet ./... && GOOS=windows go vet ./... && GOOS=windows go build ./...`:
  PASS.
- `python3 -m unittest discover -v agents-plugin-wsflow/tests`: PASS (22
  tests, 2 Windows-only skipped).
- `scripts/rail-headless.mjs`: `parent-adjacent-up` and `worktree-up` PASS;
  `.star` scenarios fail identically on base `b118102ab`.
- Independent `code-review-lite` over `b118102ab..2fac4e31b`: clean (0
  Critical, 0 Important, 3 Minor remaining — `persistSQLiteHolderSeq` monotonic
  guard/comment on `recordBase`, `ErrStaleHolder` silent swallow without log in
  `persistSeq`, and `handleMetaPrune` accepting caller-supplied `retention_ns > 0`
  below `RegistryRetention` to preserve `PruneRegistrations` caller/test
  contract).

### Phase 3: Record the measured result

- Depends on Phase 2. Compare the same actions and dataset on the Windows
  heavy-AV baseline. Trace cold open/import and warm actions separately.
  Include held final ring lines and dirty/clean heartbeats, permission polls,
  representative UI reads/mutations and hook scans for the stores included
  in the bundle; workspace and tracker seen files remain excluded.
- Record syscall counts and action-to-visible-effect latency, including WAL,
  SHM, sync, checkpoint, replacement and shutdown bursts, against the prior
  paths. Verify the two cursor-driven whole-file writes are absent in the
  traced cases. Record requested exceptions and their measured costs for the
  owner.
