---
summary: Required when a change touches a cross-version surface (MCP tool or argument, `/mcp` hello, shared store shape, tracker event type, config/rsrc manifest field) - directional T0–T4 tiers, source-backed scenarios, and release rules
---

# Release Compatibility

Several ws versions run on one machine at once: Claude and Codex each
install their own plugin copy and update on their own schedule. Decide a
change's compatibility tier during discussion and record it in the ticket's
`## Decisions`; release confirms that decision against the implementation.

## How Versions Meet

- **One daemon per cache root.** Python launchers and holders connect to the
  dashboard daemon; starting or replacing it is the executable lifecycle
  exception. Newer numeric cores win. Equal cores keep the healthy daemon
  regardless of suffix, build time or content, unless LAN mode changes or an
  explicit `daemon-start --replace` requests one swap (`wsdashboard/ensure.go`,
  `keepRunning` and `Ensure`).
- **Daemon-owned playbooks, plugin-owned skills.** The daemon serves its
  embedded package's playbooks, manifests and includes, selecting wsflow from
  `WS_MCP_NAMESPACE` and ws otherwise. Caller `WS_RSRC_ROOT` no longer selects
  text. Harnesses still discover skills in their plugin directories, so old
  skill shims can call a newer daemon. `WS_RSRC_DEV_CHECKOUT` is an explicit
  daemon-process-only source override.
- **Connection guards are strict today.** The hello accepts only its exact
  protocol and, when supplied, the daemon's major.minor
  (`wsdashboard/proxy.go`). Since 0.50 a minor
  mismatch does not create a second server: when any starter (dev rebuild,
  upgraded plugin, Werkschaft) replaces the daemon with a newer minor,
  previous-minor sessions fail their calls until the updated plugin's session
  restarts. 0.48/0.49 launchers instead fell back to a direct server.
  0.50.0–0.50.5 launchers fail each call only after a 75 s wait; from 0.50.6
  the launcher fails at once with an "update this plugin and restart this
  session" error and logs it once (`agents-plugin/bin/ws-mcp-launcher.py`,
  `DaemonNewerMinor`).
- **Reconnect is not compatibility negotiation.** The launcher replays
  initialization, not sent requests or tool discovery
  (`agents-plugin/bin/ws-mcp-launcher.py`, `StdioProxy`).
- **Store behavior is per writer.** Some writers preserve unknown raw fields;
  others serialize a fixed struct and lose them. Some readers/writers refuse
  newer schemas. The scenario sheet audits examples; a schema field alone
  establishes neither preservation nor refusal.

Go source references in this manual are relative to `agents-plugin-tool/internal/`;
other paths are repository-relative. They describe this checkout, not an
uninspected historical binary.

## Tiers

Grade directionally: identify the old and new versions, which side writes what,
which side reads or rewrites it, and what remains when the new version reopens.
Cite source lines for each concrete verdict, including the recovery path.
The highest tier across the affected paths controls the release.

| Tier | Old/new behavior | Release |
|---|---|---|
| **T0 transparent** | The old binary ignores unknown additions and works normally; any write preserves the additions | Patch allowed |
| **T1 display-degraded** | The old binary shows unknown/default values and changes no data | Patch allowed |
| **T2 recoverable overwrite** | The old binary may overwrite new data; reopening with the new version restores it from retained raw or append-only data | Patch allowed |
| **T3 old-version boundary** | The old binary is blocked from running or connecting, or the affected read/write is refused (hello/protocol or schema boundary) | Minor + explicit owner approval |
| **T4 unreadable history** | The new binary cannot read old data | Never ship; migration must lower it to T3 or below |

A migration executed by the new binary is fine if it reads the old data and
makes it usable. Its effect on subsequent old readers/writers still determines
the tier. For T2, retained bytes and the new reader's actual reconstruction are
both evidence: an append-only store alone does not prove that a particular
new value survives intervening old writes.

### Tools and Protocol Mapping

- A new tool, optional argument, or defaulted field is T0 when legacy calls
  still work normally. New callers using new arguments against old servers
  need capability gating or a compatible fallback; optional in the new schema
  does not mean accepted by an old server.
- A rename keeping the old name with the same behavior is normally T0, not
  automatically T1. T1 applies only when the old side merely displays
  unknown/default values and changes no data.
- Tool-list refresh is T0 if transparent, or T1 if its only degradation meets
  that display-only criterion, **provided old calls remain compatible**.
  Reconnection cannot repair a removed call or argument. Removal without a
  working alias is T3 because old clients break; retain the alias through any
  patch and require the minor boundary and approval for its removal.
  Refusing a call for the caller's role authority (e.g. read-only), with tool, arguments, schema and stored data unchanged and the refusal raised before any write, is a permission policy, not a removal: T0.
- A hello/protocol bump that refuses old connections is T3. Store or tracker
  event changes take their tier from the actual reader/writer behavior, not
  from being called a protocol change.

The current dispatcher refuses unknown tools (`mcp/server_dispatch.go:1662`),
and the argument precheck refuses missing required or undeclared top-level
arguments (`mcp/tool_arguments.go:65–128`). Thus discovery refresh or a migration
hint is not a behavior-preserving alias. These are wire-call refusals; they
perform no handler write, and reopening does not restore the old call.

## Scenario Sheet

Each verdict is limited to the path described. “Reopen” means reopening with
the new version, not reconnecting the transport.

| # | Situation and direction | Reads/writes and reopen result | Grade and evidence |
|---|---|---|---|
| 1 | A plugin updates a patch; another session uses old skill text against the new daemon | The old caller sends legacy tool or argument names and the new dispatcher checks them. A precheck or unknown-tool refusal writes nothing. Reopening cannot restore a removed call | T0 when the old call still works. T3 for removal without a working alias. `mcp/server_dispatch.go`, `mcp/tool_arguments.go` |
| 2 | A 0.51 Pi caller meets the 0.52 retirement boundary, or the operator rolls back to a matched v0.51.2 plugin and daemon | The old launcher refuses the newer minor at `/health`, and the 0.52 daemon rejects a versioned older-minor hello. 0.52 keeps the `ws-pi-bridge` initialization pin and the six hidden `pi.*` handlers. Authorized `pi.list` and `pi.transcript` read retained data. Execution calls refuse before a write. 0.52 snapshots the agent record, registration, dashboard snapshot, ring, and daemon-owned Pi JSONL where present in `retired-pi-history/<reply-id>/` beside `sessions/`. Startup refreshes changed snapshots from surviving sessions, preserves archived components missing from the live directory, and merges ring entries within the existing retention limit. The archived JSONL can keep later appends through a hard link, with a copied snapshot on filesystems without links. The v0.51.2 pruner walks only `sessions/` and can delete aged, retired, non-terminal-owned sessions. On return, 0.52 recovers the history it last saw before rollback by restoring missing session directories and reapplying retirement. Writes made by v0.51.2 during rollback follow its own retention. Recover a partial session directory manually. The 0.52 sweep retains retired Pi history. An explicit checkout purge commits a deletion state before removing the archive. Startup honors pending deletions. Retry a purge that returns 503 on the same checkout | T3 for the minor and execution boundaries. T2 for pruning history archived by 0.52, because it reconstructs that history from the retained copy. T0 for the pending deletion table, which v0.51.2 ignores. v0.51.2 `agents-plugin/bin/ws-mcp-launcher.py:1882–1890` and `wsdashboard/registry.go:386–425,461–472`; current `wsdashboard/proxy.go:250–275`, `mcp/server_harness.go:155–174`, `mcp/pi_tools.go:15–59`, `wsagent/pi_history_archive.go:28–119,146–183,235–369`, `wslive/ring_store.go:588–625`, `wsagent/runner_startup.go:98–99,129–144`, `wsdashboard/registry.go:386–425`, `wsagent/pi_history_purge.go:35–97`, `wsmeta/pi_history.go:8–46`, `wsdashboard/railworktreedelete.go:603–631` |
| 3 | Another harness replaces the daemon mid-session | The launcher forwards old requests to the successor; already-sent calls fail with unknown outcome and are not replayed. A handler may already have written data, so reconnect proves no rollback or reopen recovery. Discovery refresh alone says nothing about those writes | No standalone tier for replacement: grade each call/store path. Compatible discovery can be T0/T1 only under the mapping above; removed calls are T3. `agents-plugin/bin/ws-mcp-launcher.py:1473–1481,1642–1683`, row 1 |
| 4 | Old plugin connects across a minor/protocol boundary | The daemon reads its hello and refuses before handing it to the session handler. No session/store write on this path; reopening stored data is not a remedy for connection refusal | T3. `wsdashboard/proxy.go:232–254` |
| 4a | A newer-minor starter (dev rebuild, upgraded plugin, Werkschaft) replaces a previous-minor daemon | Ensure drains any older numeric core, never a newer one. The previous-minor launcher then finds a healthy newer-minor daemon. It sends no hello, writes nothing, and starts nothing. From 0.50.6 it errors each MCP request at once, logs once, and exits a CLI call non-zero at once; it reconnects if a same-minor daemon returns. Reopen does not apply: no data is touched | T0 behaviour over the row 4 boundary; recovery is the updated plugin plus a session restart. `wsdashboard/ensure.go:210–217`, `agents-plugin-tool/desktop/src/daemon.rs` (`replacement`:100–107, `connect_with`:192–225, `launch`:309–361), `agents-plugin/bin/ws-mcp-launcher.py:1530,1773–1776,1831–1843,1904–1905,1988–1990,2003,2116–2118` |
| 5 | Equal numeric core, different release/dev build | Ordinary connection retains the healthy daemon regardless of suffix, build time or hash. Explicit replacement and LAN changes still use authenticated drain. Selection is not a shared-data writer; reopening cannot establish handler compatibility | No independent grade from the version label; grade the content change. `wsdashboard/ensure.go`, `keepRunning` and `Ensure` |
| 6 | Local source changes without a version change | The launcher reads source stat metadata, including embedded assets, into its fingerprint. Drift is advisory while a compatible runtime exists; reuse retains the old source stamp. No shared-store write or recovery path changes | No standalone tier; grade any explicitly selected build's content. `agents-plugin/bin/ws-mcp-launcher.py`, `local_devenv_source_fingerprint` and `main` |
| 7 | New tracker event reaches an older reader on another machine | The new writer appends an event; the older domain reader flags its unknown type in a derived view without rewriting the event. New reopen reads and replays retained events; restoration of a particular new value is unverified until its new handler and intervening writes are traced | T1 for this read-only unknown-event display, not an automatic T2/T3 event rule. `wstracker/check.go:1471–1473`, `wstracker/replay.go:263–294`, `wsledger/store.go:725–763,1455–1472`, `wstracker/tracker.go:33–64` |
| 8 | New optional memo fields meet the current writer, which does not know them | The newer side writes extra root/item fields in schema 1; the current side reads raw maps and edits known memo text while preserving extras. New reopen still has those raw fields; no lost field needs reconstruction | T0 for those additions. `wsdashboard/memos.go:23–50,67–85`; preservation fixture: `wsdashboard/memos_test.go:22–42` |
| 9 | A newer memo schema meets the current writer | The newer side writes schema 2; the current reader projects known memo fields but save refuses before writing. New reopen sees the unchanged file | T3 write boundary, even though reads remain possible. `wsdashboard/memos.go:67–72`, `wsdashboard/memos_test.go:44–58` |
| 10 | A new field in `registration.json` meets a struct-only older writer | The newer side writes an unknown field; the current-style reader drops it during struct decode, and the writer replaces the whole JSON file. New reopen reads the replacement, not the lost bytes; this path provides no recovery | Not T2 and not patch-safe as-is: preserve unknown fields or enforce a T3 boundary. `wsdashboard/registry.go:75–140,236–254,280–316`, `wsstate/replace_file_unix.go:7–12`, `wsstate/replace_file_windows.go:13–32` |
| 11 | New config-manifest field or newer whiteboard folder schema meets the current reader | The newer side writes the manifest/folder; the current reader refuses unknown manifest fields or unsupported folder schema before normal use. No rewrite in these checks; new reopen retains the original bytes, but does not unblock the old reader | T3 read boundary. `mcp/config_manifest.go:172–185`, `wswhiteboard/folders.go:231–246` |
| 12 | A daemon without the `/mcp/session` route (v0.50.2 and earlier) rejoins a held hosted lead that a route-serving daemon started over HTTP MCP | The lead posts its token to `/mcp/session`, which the old daemon answers `401 login_required`; Claude keeps reporting the server connected, so the old daemon's MCP recovery never restarts it and the lead's ws tools fail until the process restarts. The old daemon keeps the record's unknown `mcp_*` members and writes nothing new; a new daemon's rejoin restores the token, or forgets it when another holder restarted the process. No data loss; reopening with the new version recovers fully. Arises only on a downgrade to a route-less build (an older ws-mcp never replaces a newer daemon), never on a forward update: a same-port rejoin restores the token and a moved port restarts the lead at its first idle point | Accepted downgrade-only functional degradation, patch-safe (owner (92)=a, 2026-11-18). `wsagent/runner_process.go` 322–340; v0.50.2 `localhost_auth.go` 150, `daemon.go` 753; `wsdashboard/ensure.go` 100–102 |
| 13 | A released config key or enum value is replaced by a later release (v0.51.0 `dashboard.companion_mode` off/visual/full and the director values `sidecar`/`lead-tool`, replaced by `dashboard.companion_enabled` and `rules`/`agent`) | An old caller's `config.tune` of the removed key or value is refused by the argument and enum check unless the key stays declared. Kept as an alias: the key stays declared global-only; a write of either key writes the other in the same locked file rewrite, and a global read takes a stored legacy mode as the enabled state, so a 0.51.0 writer's change is honoured and a 0.51.0 reader reads a matching `companion_mode`. The removed director values are accepted and stored as `rules`. An old reader ignores the new keys; `agent` reads as an invalid value that falls back to its default and changes no data. Residual: a 0.51.0 reset of `companion_mode` deletes only that key, so a stored new key stands | T0 for the old calls, the old reader's mode and a stored legacy value; T1 for the unknown director value `agent` on a 0.51.0 reader. Without the alias, T3. `wsdashboard/companion_legacy.go`, `wsconfig/override_compat.go`, `wsconfig/resolver.go` (`setOverrideInFileRMW`, `deleteOverrideInFile`), `wsconfig/global.go` (`loadGlobalConfig`), `mcp/config_legacy.go`; v0.51.0 `mcp/dashboard_settings_config.go` `dashboardCompanionConfig` (reads the stored mode, default off), `wsdashboard/assets/dashboard-lifecycle.js` `shCompanionConfigure`; tests `mcp/dashboard_companion_compat_test.go` |
| 14 | A new pack uses a new optional raster key (`closedL`/`closedR` closed-eye stickers) and meets an older dashboard that installs it | The older validator ignores the unknown eye keys but collects only the rasters it knows; the zip install refuses any file outside that set, so the pack is refused and nothing is stored. With each plate also listed under `head.mouths` as `eyeclosed-*`, the older validator collects it, installs the pack and draws the rigid blink, ignoring the extra mouth. A hand-placed directory is read by file list only, so extra rasters are ignored. No data is rewritten | T3 for the plain key (refused write); T1 with the `head.mouths` registration. `wsdashboard/companion_pack_schema.go` `companionEyeAssets`, `head` (mouths loop); `wsdashboard/companion_pack_zip.go` ("file is not referenced by pack.json"); `wsdashboard/companion_pack_store.go` `loadCompanionPack`; tests `companion_pack_install_test.go` `TestCompanionPackInstallClosedEyePlates` |
| 15 | A v0.51.0 or v0.51.1 launcher starts the retained v0.51.2 build after daemon absence | The launcher reads `last-running-executable.json`, then starts the recorded build synchronously while holding `launcher-start.lock`. The new starter leaves lock ownership with the launcher and prepares a successor before drain when replacing an incumbent. The daemon writes the same executable record shape for later restarts | T0. v0.51.0 `agents-plugin/bin/ws-mcp-launcher.py:1071–1091,1752–1764,1783–1806,1847–1877`, v0.51.1 `agents-plugin/bin/ws-mcp-launcher.py:1072–1092,1753–1765,1784–1807,1848–1878`. `agents-plugin-tool/cmd/ws-mcp/proxy.go:545–570`, `wsdashboard/ensure.go:162–171,198–205`, `wsdashboard/running_executable.go:12–20,38–42` |
| 16 | A v0.51.2 launcher starts a retained v0.51.0 or v0.51.1 build | The launcher holds the same lock and uses the existing `daemon-start` arguments. The added ownership marker is an environment variable. The older binary ignores it and leaves the lock with the plain starter's parent. Later restarts read the same record shape | T0. `agents-plugin/bin/ws-mcp-launcher.py:1072–1092,1784–1808,1850–1880`. v0.51.0 `agents-plugin-tool/cmd/ws-mcp/proxy.go:407–414,469–477` and v0.51.1 `agents-plugin-tool/cmd/ws-mcp/proxy.go:422–429,484–492`. Both tags use `wsdashboard/ensure.go:161–173` and write the schema 1 record via `wsdashboard/running_executable.go:12–20,38–42` |
| 17 | Daemon metadata switches to `metadata.sqlite` via one-time cutover, then a same-root downgrade to a pre-cutover binary runs and later re-upgrades | Under `dashboard.lock`, the new daemon stages legacy bundled files into `legacy_captures`, applies the four one-time fences (invalid `permission.json` on every imported session, hiring `Enabled=false` keeping `Hired`/`RequestID`, `closed_at` on open parentless support `agent.json` and `profile: "!cutover-fenced"` on open parented child `agent.json`, and `tracker-hooks.json` -> `tracker-hooks-v2.json`), and commits rows plus the completion marker atomically; afterwards it writes zero legacy bundled files and direct `ws-mcp` calls route via `/meta/*`. A downgraded older binary refuses fresh lead/worker starts, hiring, hooks, and fenced support/child tracker writes (while surviving holders can reattach and old prune keeps fenced child dirs), but may prune a session revived in SQLite using stale `agent.json`, replay/orphan a stale holder, replay `hire-proposals.json`, accept an owner's toggle to re-enable hiring on a post-cutover nested CLI lead, or leave a newly created old-side support record open. Re-upgrade restores SQLite state and discards old-side bundled edits (no T2 promise) | Patch-shipped under owner exception `(2125)=a` (2026-11-22) and `(2137)=b`; `wsmeta/cutover.go`, `wsmeta/capture.go`, `wsmeta/import.go`, `wsdashboard/daemon_start.go`, `wsdashboard/daemon_runtime.go`, `wsdashboard/meta_routes.go`, `wsagent/runner_meta.go`, `wsagent/support.go`, `wstrackerhook/runner.go`, `mcp/meta_client.go`; pre-cutover `b118102ab` `wsagent/permission_store.go`, `wsagent/profile.go`, `mcp/tracker_support.go`, `wsdashboard/registry.go`, `wsdashboard/session_hire.go`, `wstrackerhook/config.go` |
| 18 | A VRM pack carrying the new optional `pack.json` keys (`actions`, `sentinels`, `calibration`, `props`) and a `clips/` directory meets an older daemon | The older validator ignores the unknown manifest keys and reads only the flat files it lists, so the pack loads, serves and renders with its legacy `animations`. It never lists `clips/`, so the clip files are not read. A Studio write updates only `pack.json` and `clips/<key>.*` with a handle-relative pinned write, keeping the other manifest keys and their bytes. A clipless pack keeps its pre-change revision. Residual: on a downgraded daemon, a same-ID zip re-upload of a Studio-authored local pack replaces the directory and drops `clips/` and the new keys. The new daemon refuses that re-upload. Arises only on a downgrade | T0 for the read path; accepted downgrade-only data-loss residual on the same-ID zip re-upload, patch-safe (owner (1786)=a, 2026-11-28). Old read path (at `090ad7b06`): `wsdashboard/companion_pack_vrm.go:63-81` (`validateCompanionVRMManifest` binds only its known keys), `wsdashboard/companion_pack_schema.go:169-187` (`companionObject.field`/`fields` skip absent and unknown keys), `wsdashboard/companion_pack_store.go:277-335` (`loadCompanionPackWithValidator` reads `pack.json` plus the validator's flat `files` only). New: `wsdashboard/companion_pack_clips.go` (`parseCompanionClipCatalog`, `companionPackRevision`, `loadCompanionLocalClips`), `wsdashboard/companion_pack_clip_routes.go` (`editPack`), `wsdashboard/companion_pack_install.go` (Studio-authored refusal); tests `companion_pack_clips_test.go`, `companion_pack_clip_routes_test.go` |

For tracker changes, append preserves earlier event bytes
(`wsledger/store.go:725–763`), and a new
open refreshes the ledger (`wsledger/store.go:319–336`) before tracker replay
(`wstracker/tracker.go:33–64`); a proposed T2 change must additionally trace the
specific value through the old write and the new handler's reconstruction.
Likewise, a new reader that rejects old history is T4 until a migration is
implemented and its old-data read path verified; no such change is approved by
this sheet.

## Release Rules

1. **Patches may carry T0–T2.** Retain legacy call behavior and cite any T2
   overwrite/reopen reconstruction before settling the tier. Whole-file loss
   without that reconstruction needs preservation or a T3 boundary.
   **Owner exception, 2026-11-20, (1855)=a:** the workers-under-holders
   surface ships as T2 in a patch. Conversation-parent convergence on re-upgrade
   retires the downgrade-fenced child into a wakeable sleeping record rather
   than restoring its pre-downgrade held state. An old-daemon downgrade beyond
   the 7-day `RegistryRetention` may irrecoverably lose that child's session
   history. The owner explicitly accepted both outcomes. T1 describes only
   the surviving fenced child row; it does not cover parent rewrites or pruning.
   See `261120-refactor-ws-agent-workers-under-holders` and the daemon replacement
   section of `ws-mcp.md` for the directional evidence and recovery guards.
   **Owner exception, 2026-11-22, (2125)=a (and (2137)=b):** the daemon
   metadata SQLite cutover ships in a patch with one-time legacy fences and no
   write-through. Once the completion marker commits, re-upgrade restores from
   `metadata.sqlite` alone and discards bundled-store edits made while
   downgraded (no T2 recovery promise). Same-root downgrade keeps the disclosed
   mixed-version prune, stale holder replay/orphan, `hire-proposals.json`
   replay, post-cutover nested-CLI hiring toggle, post-downgrade new support
   record, and fenced-child fresh-launch residuals documented in row 17 above
   and in `ws-mcp.md` under "Metadata lock/drain barrier and downgrade
   residuals".

2. **T3 requires a minor bump and explicit owner approval.** Raise the affected
   protocol or store schema where that enforces the boundary. Verify the old
   side actually refuses before damaging writes; merely adding a schema number
   to a reader that ignores it does not protect data.
3. **T4 never ships.** Add migration in the new reader so old data remains
   readable, then grade its effect on the old side as T3 or below.
4. **The daemon judges skew.** The planned mechanism records each session's
   hello version. A minor mismatch gets a strong update notice in tool results
   and the workflow manual, plus a dashboard badge; a patch difference gets a
   quiet badge. Sessions may continue only where calls and stores meet the
   chosen tier; a notice does not waive a T3 refusal. Inspection uses `/health`
   or existing hello information, with no direct-execution fallback or daemon
   startup just to obtain a version.
5. **A daemon replacement refreshes the tool list.** The planned mechanism
   sends `tools/list_changed` first, with forced reconnect where a host ignores
   it. The planned Claude mod watchdog also reacts to version and content
   changes. Legacy calls still need a compatible handler.

## Enforcement State

Existing connection refusals are cited above. `SameMinor` compares version
components, not compatibility tiers (`wsdashboard/version.go:51–61`). This
change updates its explanatory comment only; runtime enforcement is unchanged.

Ticket `261112-feat-ws-mixed-version-compat-guards` tracks remaining mechanisms:
legacy tool-surface checks, shared-store guards, per-session skew notices and
badges, and tool-list refresh. Its older tier labels and decisions need a
separate re-grade under this policy. In particular, cross-minor notices cannot
replace a T3 refusal while an old writer can irrecoverably drop new fields.
The discussion record and release review remain the checks for a change's
source-backed tier until those mechanisms land.
