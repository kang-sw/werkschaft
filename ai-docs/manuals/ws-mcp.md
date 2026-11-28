---
summary: MCP operational runbook, launcher environment, release and verification steps
---

# ws MCP Operations Runbook

Operational runbook for the host-neutral `ws-mcp` runtime used by the `ws`
plugin package.

This file is not the MCP tool contract or current tool inventory. That contract
is the test suite under `agents-plugin-tool/internal/`, and the implementation
coupling is the code itself:

- MCP dispatch, schemas, and tool allowlist: `agents-plugin-tool/internal/mcp/`
- plugin launcher and release packaging: `agents-plugin-tool/internal/wsrsrc/`
  and each package's `runtime.json`

For live tool schemas and current inventory, ask the runtime: use MCP
`tools/list`, `ws/runtime.read`, `ws-mcp runtime capabilities`, or the source
registry in `agents-plugin-tool/internal/mcp/server_schemas.go:11`.

## Tool Output Convention

An MCP tool's output is read by an LLM before it is read by anything else, so a
new tool's default response is compact, readable text. Return JSON only when a
caller needs stable machine parsing, protocol metadata, or a
compatibility-preserving shape, and put it behind an explicit opt-in (a
`format: "json"` argument) rather than making it the default. Nothing enforces
this - `toolTextResponse` and `toolJSONResponse` are equally available at every
dispatch site in `agents-plugin-tool/internal/mcp/server_dispatch.go:21` - so it has to be
applied when the tool is written.

## Hidden Call Summaries

Every tool accepts optional top-level `summary`, `toolSummary`, or
`tool_summary` metadata without advertising those fields in its schema. The
precheck harvests the first nonblank string in that order and strips all
undeclared aliases before argument validation, including inner calls through
`call`. A tool-declared property with one of those names remains its own input.
Descriptions reach the rows through harness-stream previews, not per-call
debug logging. Previews use the same key list and prefer the description in
running and completed tool rows; the Claude hook places these keys before
ordinary flattened arguments so truncation does not hide them. Existing
masking and display limits still apply.

This is T0 for existing calls: no summary is required and no store shape
changes. It does not make old binaries accept metadata: their precheck rejects
undeclared arguments, so callers targeting an older server must omit the
optional metadata or retry without it.

## Process Model

One dashboard daemon per cache root hosts the MCP servers and one-shot CLI
handlers. Harnesses run Python adapters, not ws executables:

```bash
python3 <plugin>/bin/ws-mcp-launcher.py serve --stdio --root <repo-root>
```

Stdin/stdout remains newline-delimited JSON-RPC (MCP protocol `2025-03-26`).
The same launcher accepts `version`, `doctor`, `runtime`, `tools`, `call`,
`config`, `path`, `git`, `tickets`, `mailbox`, and `credential` commands. Only a necessary
`daemon-start`/replacement executes the staged ws binary; there are no
executable version/capability probes or direct server/CLI fallbacks.

`doctor` is a host-independent smoke check. It verifies the repository root,
`ai-docs/`, and `AGENTS.md`; downstream projects should rely on MCP tools for
bundled conventions rather than repository-local source paths.

### Mailbox store

Mailbox state lives in memory, one store per cache root
(`internal/wsmailbox/memstore.go`): every scope's presence and queues, the
reply-id registry, each session's 100-entry history window, and the
armed-wait listening markers. The daemon holds it for its life. A committed
change (a send, a recv, a heartbeat) is one JSONL record appended to the
restore ring `<cache root>/mailbox-ring/` before the call returns, so mail
survives a killed daemon; there is no fsync, so an OS crash may lose the
tail. The ring keeps ten segments of about 1 MiB, each opening with a
snapshot of the whole live state; a rotation reaps idle history windows and
expired reply entries, never live mail. The ring is read only at open; a
torn last record is cut.

The per-scope `mailbox.json` files, `mailbox-replyids.json`, and the
`mailbox-history/` entry files are the layout a previous ws-mcp version
uses. The store imports them at open (then clears their queues, so nothing
is delivered twice) and writes them back at a graceful daemon shutdown, so a
downgrade keeps its mail; the old version's changes are imported at the next
open. `mailbox-listening/` is no longer read or written.

The daemon holds the ring's lock. A directly run ws-mcp binary may still
read the ring (lock-free: every committed call is already in it), but its
mailbox writes fail while the daemon runs: direct mailbox calls are
unsupported, like direct `serve --stdio`.

### Session key store

Each session key is a directory, `<cache root>/sessions/<key>/`, holding
`key.json` (the identity - root, scope, parent - plus the session-scope
config overrides, the child note, and the once-only flags), `agenda.json`,
and `todos.json`. A pre-0.50 `whiteboard.json` remains an immutable rollback
snapshot. Whiteboards now live under the project's
`ai-docs/whiteboards/<reply-id>/`: versioned session `meta.json`, opaque topic
folders holding `meta.json`, immutable panel versions, and append-only
`log.jsonl`. The daemon renders pages from this store without writing page
artifacts. Its first board open schedules one background cleanup per project
and cache root: retired index/stamp and hashed Mermaid files, plus dead
sessions' repository embed snapshots. The cleanup preserves board data and
live embeds and uses no marker or recurring GC. The first board open (queries and
dashboard reads included) converts the old snapshot once under the folder
lock. New writes never update either old board representation, and key expiry
does not delete authoritative topic folders. An absent agenda/todo part is empty. A write replaces only the part files it changes, each atomically, and
the lookup every keyed call makes reads `key.json` alone, except
`dashboard.intake`'s: intake and the live snapshot writer's activity check
keep each key's identity (root, scope, parent, written only at mint) in
memory and stat `key.json` at most every 10 s, so a removed key stops intake
within 10 s while every other tool's gate sees it at once. Every content write,
mint's claim included, holds the key's record lock (`key.json.lock` in its
directory) across its read-modify-write, so ws processes serving one key do
not lose each other's updates; a lock not acquired within 2 s fails the tool
call. Readers take no lock, so a write of two parts (a mode switch's agenda
and todos) can be seen half-applied from another process. `key.json` is also
the key's claim (mint creates the directory exclusively) and its activity
mark: keyed calls refresh its mtime at most hourly, and the prune that
`ws.ferrule` runs at most daily deletes a key directory whose `key.json` is
older than 30 days.

The older layout, one `<cache root>/keys/<key>.json` per key, is still read:
a key with no `key.json` is served whole from its legacy file, and the
session's first write moves its session metadata into `sessions/<key>/`.
A board-bearing legacy file is retained byte-for-byte, and its original board
is copied into split `whiteboard.json` for rollback; boardless legacy files
are deleted. Reads never migrate session metadata, and no pass moves the rest;
an unwritten legacy file ages out through the same prune.

Restart every ws process after updating ws, as an install update already
expects: the session MCP servers (a host restart or MCP reconnect) and the
dashboard daemon (a newer numeric core replaces it; an equal-core build
requires explicit `daemon-start --replace`, or `POST /shutdown` then startup).
An older ws-mcp knows only `keys/`, so once a newer one has moved a session, the older one reports that key unknown. Downgrading loses
sight of moved sessions the same way; session keys are cache state, so a
fresh key is the recovery.

### Provider credential store

Provider credentials for OpenRouter, Sign in with ChatGPT, and optional keys
for named local endpoints (`internal/wscred`) live in one owner-only file,
`<cache root>/dashboard/auth/credentials.json`:
POSIX mode 0600 in a 0700 directory, on Windows a protected DACL granting
the current user alone; never in a project tree. It sits under the daemon's
state directory because the file preview has denied that whole directory
since v0.51.1, so a rolled-back daemon cannot show it. A pre-release build kept
it at `<cache root>/auth/`; the daemon at start, and the offline CLI under
the daemon lock, move that file once (`Store.MigrateLegacy`). It is a plain file (storage type 5),
not a SQLite row, so the offline CLI can write it and nothing that copies
the state databases copies a secret. At startup the daemon reads the file
while reconciling saved endpoint keys with the machine configuration. It
serves later reads from memory. While running, the daemon is the file's
only writer and atomically replaces it when a credential changes. A writer
keeps members it does not know and leaves files with a newer
`schema_version` untouched. The daemon's environment reaches every hosted
session and exec subprocess, so credentials are read from the credential
file.

On update, the writer stages the new file beside `credentials.json`. It
adds a store-owned retiring hard link to the current generation and opens
that file for scrubbing before atomically publishing the replacement. A
successful update truncates the retiring file and removes the store's link,
so existing aliases to that generation read empty. If publication fails,
the writer removes the retiring link and leaves the current bytes intact.
On recovery, a retiring link with the same identity as the current file is
removed with its bytes intact. A different retiring generation and any
staged file are truncated and removed. On Windows, replacement requires
native POSIX rename and unlink support. A volume without that support or
a reader that blocks opening the retiring handle causes the update to
fail before publication.

On the machine running the dashboard, open Settings → Account to manage
provider credentials and named vLLM or Ollama endpoints. The OpenRouter row
checks a new key with OpenRouter and shows its saved mask and verification
result. A rejected key leaves the previous key in place. Connection and
temporary errors leave the new key available for a re-check.

The OpenAI row runs Sign in with ChatGPT in the system browser and shows the
active registration and its background-use consent. Sign-out deletes that
local registration and its consent. When a background request first needs
an answer, the dashboard shows a prompt. Use the Account toggle to change
the answer later.

To edit a named endpoint from the CLI, use
`credential endpoint set <name> --base-url <url> --kind <vllm|ollama>`
or `credential endpoint remove <name>`. Select `local/<name>` as the
Werk tier provider. Use `credential set local/<name>` if the endpoint
requires a key. Removing an endpoint or changing its API root deletes the
retired root's key. The machine's endpoint map stays in the global
`agent.werk_endpoints` setting.

`credential set <provider>` (the key on stdin, never in argv),
`credential show [<provider>]` (masked), and `credential remove <provider>`
manage it (`--format json` for structured output). The command reads only
the first stdin line, and the launcher forwards only that line; on a
terminal both the launcher and a directly run binary prompt for it without
echo. An unrecognized argument is never echoed back, since it may be a
misplaced key. Through the launcher the daemon applies the change itself. A
directly run ws binary sends it to the running daemon's
`POST /auth/provider/change` (authenticated by the endpoint file's token);
with no daemon running it writes the file while holding the daemon's
single-instance lock, so no daemon starts mid-write. A stopping daemon
closes its store to changes, and lets a change already in flight finish,
before it releases the lock; a change that arrives later is answered as a
drain, so the sender retries against the successor.

Run the local `ws-mcp credential sign-in openai` binary command with the
dashboard daemon stopped to authorize ChatGPT plan usage in the system
browser. The command holds the daemon's writer lock during sign-in.
`credential show openai` lists saved registrations. Use `--new-account`
to register another account, `--account <client-id>` to reauthorize a
saved registration, or `--enable-plan` to request plan consent again.
The builtin harness uses the daemon's in-process store for its account
catalog and access token. The store serializes refresh rotation and
requires explicit consent on that registration before background use.

Werk carries the operator flag from trusted lead input through the daemon's
inbox, queue, and turn replay. Automatic nudges run as background input.
Model file tools refuse writes anywhere in the daemon state directory
(`<cache root>/dashboard`, `wsdashboard.Dir`), by path and by file identity.
Nothing there is a model write target: it holds the durable metadata store,
session state, and every other daemon-owned store. An existing write target
with more than one hard link is also compared by identity against
`metadata.sqlite` and its `-wal`, `-shm`, and `-journal` sidecars, and against
the session files the daemon reads back as authority (`agent.json`,
`registration.json`, `permission.json`, `system_prompt.md`, `snapshot.json`,
hosted Werk journals, and held or claimed inbox entries), so an external
hard link cannot bypass the path check. If the state directory cannot be
resolved, model writes fail closed. Model reads of that state still follow
normal file-read permissions.
Project-scoped workflow paths outside it are unaffected.

Background consent also covers scheduled prompts. A durable Werk lead on a
ChatGPT plan records the consent decision when it claims a `ws/cron` entry,
whether the lead is idle or already in a turn. An authorized claim can steer
the running turn and keeps its authorization across restart. A refused claim
settles separately with the consent-required error, after any running turn
ends. Its text stays out of the model history, including across restart.
Recovery checks current consent for any interrupted claim without a recorded
decision. Other providers keep their harness's delivery rules.

Scoped file replacement (`fs.write`/`fs.edit` over an existing file) never
lets the staging file hold new bytes with wider access than the destination.
On Linux and macOS the staging file is created exclusively at `0600` and,
before the first byte, takes the destination's mode, owner, group, and
access ACL, read from the verified destination handle; both are rechecked
before the rename. On macOS the replacement holds an exclusive lock on the
destination inode through publication. Each write exclusively creates a
fresh `0700` child directory (`.ws-write-<random hex>`) and clears its inherited
ACL before creating the staging file. A later write leaves any stage from an
interrupted write in place. On Windows the staging file is created with
the destination's DACL, protection flag, owner, and integrity label, and the
result is verified before writing and again before publication. Metadata
that cannot be reproduced, or any mismatch, aborts the write without
publishing. A brand-new file keeps the default creation mode.

`POST /auth/provider/token` is an internal HTTP route for an external
hosted harness with a per-session bearer (`WS_MCP_SESSION_TOKEN`) and a
backend registration that enables `ProviderCredentials`. Current external
harnesses use their own logins. Werk uses the credential store in process,
and the route refuses `openai` (Sign in with ChatGPT). The dashboard file
browser and daemon file tools protect the credential directory and its
current, staged, and retiring files by path and file identity. The guards
also cover the default and legacy store locations. Keys and tokens are
never logged.

Trust boundary: the token route's answer to an admitted session is the only
response that carries a secret; no ws tool output returns one. Processes
running as the same OS user are trusted. The file's owner-only mode or DACL
keeps other users out, but not a same-user process such as a hosted exec
job, which can read the file directly; OS permissions cannot prevent that.

### Dashboard daemon

`ws-mcp dashboard` is the machine-wide daemon (one per OS environment, one
per cache root) that the plugin launcher starts when none runs and connects
every `serve --stdio` session to (see Daemon-Hosted MCP below): it hosts one
MCP server per connected session and serves the dashboard pages. Whether a
session's pages exist follows `dashboard.enabled` per session; the daemon
runs either way, and the MCP tools never depend on its pages. Its state lives
under `<cache root>/dashboard/`:

- `last-running-executable.json` - owner-only restart evidence written once by
  the lock-owning daemon: schema 1, retained executable path, version, SHA-256
  and build time. Hosted launchers reread and validate it only when a daemon
  start is needed. Older daemons ignore it; settled binaries remain available.
  The retired `hosted-plugin-roots.json` registry is ignored, not migrated.
- `hosted-packages/<ws|wsflow>/<hash>/` - retained launcher plus that package's
  five embedded manifests.
- `dashboard.lock` - the single-instance OS file lock; `endpoint.json` - port,
  pid, version, and the shutdown token; `launcher-start.lock` - the persistent
  advisory lock updated Python launchers hold across a necessary daemon start;
  `dashboard.log` - the running daemon's
  log, including one line per session added to or removed from its index;
  `lan-token` - the LAN access token (LAN mode only), kept across daemon
  restarts; deleting it issues a new one at once. `localhost-secret` - the
  separate owner-only persisted key for the localhost browser cookie; deleting
  it invalidates existing cookies without a restart. `last-port` - the port the
  last daemon bound: when `dashboard.port` is taken by another program, the
  daemon binds this port before an OS-assigned one, so a restart keeps its
  origin (page localStorage, LAN bookmark); a free `dashboard.port` always
  wins.
- `user-commands.db` stores dashboard user commands in two scopes. Agent
  commands belong to a hosted lead's reply-id and worktree root. The lead
  registers and unregisters them through `session.register_user_command`
  and `session.unregister_user_command`. The command stays with that
  conversation while it is known to the runner, including while its process
  sleeps. Commands created by the user belong to the worktree root. Leads
  in that root share them, and they outlive any one conversation.
  The store keeps a held SQLite connection and a WAL (storage type 2), loads
  into memory at startup, and commits one transaction per changed command.
  Each change or withdrawal advances the durable revision high-water mark.
  An opaque `usercmd` stream event prompts affected pages to refetch.
  The registering tools are listed to authenticated open hosted leads and
  resolve the scope from the calling conversation. The dashboard's localhost
  composer routes list both scopes (`GET /s/<reply-id>/user-commands`),
  create root commands (`POST` to the list), and edit either scope
  (`PUT .../<id>`) or delete a command (`POST .../<id>/remove`).
  Commands carry a label, optional description, bash line, optional
  multiline Ask prompt, optional cwd, and a revision. PUT accepts field
  patches, including prompt and cwd. Ask sends a conversation message.
  Run (`POST .../<id>/run`) executes through the composer's `!` path and
  accepts a one-off line or cwd override without changing the command.
  The effective cwd must resolve to an existing directory under the
  worktree root. Run and Delete check the displayed revision at admission.
  The v1 daemon already loads and updates agent-scope rows. Its SQL leaves
  the root table and the agent prompt and cwd columns intact on an update
  (`bbdbba130^`, `internal/wsusercmd/store.go:173,204–206`).
  Root rows are invisible to v1 and reappear with v2 (T0). Agent prompt and
  cwd are omitted by v1 and restored when v2 reloads the surviving rows (T1,
  `internal/wsusercmd/store.go:263–289`). Withdrawing an agent command
  still deletes that row in either version.
- `metadata.sqlite` (`internal/wsmeta`) - owner-only SQLite store (storage type
  2 in `ai-docs/manuals/performance.md`, opened by the lock-owning daemon under
  `dashboard.lock` with `PRAGMA locking_mode=EXCLUSIVE`, `journal_mode=WAL`,
  `wal_autocheckpoint=0`, write-class `PRAGMA synchronous` (`NORMAL` for
  routine updates, `FULL` for durable journal/commit boundaries), a 30 s idle
  WAL checkpoint away from interactive actions, and a final checkpoint on
  close). It owns the bundled daemon-only metadata stores loaded once into
  memory at startup: conversation records (`agent_records`, bounded top-level
  deltas in `agent_record_deltas`, and per-holder cursors in
  `agent_holder_cursors` so sequence-only updates never rewrite whole records),
  durable Werk records (`durable_agent_records`), pending retired-Pi purges
  (`retired_pi_purges`), explicit permission selections (`permissions`),
  tracker support selection and store mappings (`support_state`,
  `support_mappings`), Pi probe observations (`pi_probes`), rail layout
  (`rail_layout`, `rail_layout_workspaces`), latest usage projections and
  history samples (`usage_latest_*`, `usage_history_*`), workspace memos
  (`memo_docs`, `memo_items`), session labels (`session_labels`), answer checks
  (`ack_checks`), hiring metadata (`session_hiring`), and tracker hook progress
  (`tracker_hooks_state`). Shared/external contracts stay in their existing
  files outside SQLite: `workspaces.json`, tracker `ui-seen.json`,
  `workspace-admission.json`, `hire-proposals.json`, `visibility.json`,
  `registration.json`, holder files, `wslive` snapshots/rings, `wsinbox`,
  `wsmailbox`, `wswhiteboard`, and `wscred`.
  - **One-time cutover and fences:** On the first start against a legacy state
    directory, `wsmeta.Cutover` runs under `dashboard.lock`, stages original
    legacy bytes into the durable `legacy_captures` journal, applies four
    one-time fences so a downgraded older binary cannot silently act on stale
    state — (1) invalid `permission.json` selector on every imported session
    (including previously absent files), (2) `Enabled=false` in `hiring.json`
    while keeping `Hired` and `RequestID`, (3) `closed_at` on open parentless
    non-lead support `agent.json` records and `profile: "!cutover-fenced"`
    (preserving `cutover_profile`) on open parented non-lead child `agent.json`
    records so the older tracker gate refuses stale writes without making child
    directories prune-eligible, and (4) moving `tracker-hooks.json` to the
    live reloadable input `tracker-hooks-v2.json` — and commits imported rows
    plus the completion marker in one transaction. An interrupted cutover
    re-stages unfenced files on retry so intervening permission lowerings or
    pruned sessions are respected; an unreadable removal target returns
    `ErrCutoverUnsupported` and leaves the daemon in legacy file mode. Once
    committed, SQLite is authoritative, the surviving legacy files remain as
    the frozen cutover snapshot, and the daemon makes zero further bundled-file
    publications.
  - **Direct Go callers (`/meta/*`):** Raw `ws-mcp call` and `serve --stdio`
    never open `metadata.sqlite` directly; they query daemon-owned state over
    authenticated loopback routes (`/meta/agent-record`,
    `/meta/hosted-descendant`, `/meta/unregister-check`, `/meta/prune`). When
    no daemon is running, an affected `ws-mcp call` fails immediately with an
    error naming `ws-mcp daemon-start` for the same cache/config root, while
    `serve --stdio` ensures the daemon once under dashboard policy and
    otherwise returns a recoverable unavailable error.
  - **Offline SQLite recovery (schema/error protection):** Legacy files that
    failed import as invalid JSON or a newer schema version are recorded in
    `legacy_captures` (`import_status` of `invalid` or `future`) and surfaced
    through `Snapshot.ImportIssues` so owners (such as rail layout and usage
    history) refuse destructive overwrites without re-reading legacy files. To
    inspect or repair stored metadata offline: (1) stop the dashboard daemon so
    it checkpoints WAL and releases `PRAGMA locking_mode=EXCLUSIVE`, (2) back
    up `metadata.sqlite` (and `metadata.sqlite-wal` if present), (3) open
    `metadata.sqlite` with an offline SQLite client to inspect
    `legacy_captures` (original captured bytes and `import_detail`) or repair /
    remove corrupted rows (for example in `usage_history_docs` /
    `usage_history_samples` or `rail_layout*`), and (4) restart the daemon.
- `sessions/<reply-id>/registration.json` - one entry per top-level lead
  session whose `dashboard.enabled` resolves on (session scope included),
  written by the serving ws-mcp at its first keyed call and heartbeated every
  2 minutes (a session the daemon hosts under `WS_HOSTED_SESSION` is
  registered by the daemon, not by the serving ws-mcp: its row carries
  `hosted: true` and `host_pid`, the session's harness process, 0 until the
  daemon has started it, with `host_stopped: true` while it has no process
  and starts none), and one per agent
  runner conversation (below), written by the runner with `agent: true`
  (and `unlisted: true` when its profile keeps it off the index page) while
  the daemon runs. A session counts as live while
  its heartbeat is under 5 minutes old and it has not ended. The daemon's
  index only reads this registry; pruning runs through daemon-owned metadata
  (in-process or via `/meta/prune`), consulting `AgentForgetter.ForgetClosed`
  for hosted sessions before removing entries unseen for 7 days and deleting
  their per-session SQLite rows (`wsmeta.Store.DeleteSession`). Agent
  conversations stay protected while the runner may still serve them; ended
  records age from their last registry row write. Retired Pi runner records
  keep their session directory and recovery archive (see
  [Retired Pi history](#retired-pi-history)).
- `sessions/<reply-id>/snapshot.json` and `sessions/<reply-id>/ring/` - the
  session's live snapshot (current lead and subagent activity, served from
  daemon RAM for slotted sessions and flushed for recovery on turn end, close,
  and debounced heartbeat) and its conversation ring (finished messages; while
  a session holds an active daemon `HostedSlot`, its ring stays pinned in RAM
  and appends to owner-only JSONL segment files `seg-<seq>.jsonl` rotated
  whole; unpinned/external writers write atomic per-entry `.json` files and
  close immediately; readers merge segments and `.json` files on load; the
  newest 2000 are kept, the page shows the newest 50 and
  pages older ones in through the session's history route).
  ws-mcp writes them from host adapter events
  (`dashboard.intake`) and from its mailbox send and recv path, only for a
  registered session; `internal/wslive` holds the schema and readers. An
  intake result carries an `accepts: status, compact, commands, command`
  line naming the optional event kinds this ws-mcp takes; an adapter sends a
  `status` event (the session's context, usage limits, and cost for the
  page's status band), a `compact` event (a lead compaction's `start`, then
  its `end` or `failed`, with an `auto` or `manual` trigger), a `commands`
  event (the slash commands it runs from a page command entry, which the
  snapshot keeps as `commands` until the next list or a reset), or a
  `command` event (a slash command it ran: its typed line and output text,
  a `command` ring entry) only while the latest result names that kind,
  since an older ws-mcp skips the kind per event, and the "dashboard off"
  result names none. A running compaction is the lead's
  `compacting` phase in the snapshot; its end or failure is a `compact` ring
  entry, and a reset or the next `turn_start` clears a compaction whose end
  never came. The snapshot's `repo` fields (the
  root's folder, branch, upstream, ahead and behind, and the counts of
  modified, staged, and untracked working-tree entries) come from ws-mcp's own
  `status --porcelain=v2 --branch --ahead-behind` read (plus short HEAD
  discovery when detached), cached per session and refreshed at most once
  per heartbeat interval from the writer's timer, never on an intake call, and
  only when a tool batch ended since the last read (an idle session runs no
  git); the snapshot write that finds the cache missing or stale (a session's
  first, then at most one per heartbeat interval, change or heartbeat alike)
  waits for the refresh, up to its 5 s timeout. There is no automatic fetch, so
  behind reflects the last one. The status read takes no optional
  lock and gets a 3 s sub-deadline within the 5 s overall timeout; when it
  fails, the reserved remainder lets cheap branch/ref reads preserve the
  available standing while counts stay zero. The
  page's status band shows only the folder; the git handle reads the rest
  itself (below).
- `sessions/<reply-id>/inbox/` - the board-answer inbox (`internal/wsinbox`):
  held messages the user sent from the page, one file each; `drained/` keeps
  the newest 50 as sent records. The adapter-only `dashboard.inbox_drain` tool
  drains it, and `mailbox wait --board-inbox` also wakes on it (peek only). A
  message promoted to steering is rewritten with `steer: true` and a fresh
  name, so it joins the end of the steer queue; a full drain claims the
  steered messages first, in the order promoted, then the rest, oldest first.
  `dashboard.inbox_drain steer_only: true` claims only
  those, and `mailbox wait --board-steer` wakes only on one (the wait an
  adapter arms mid-turn; exclusive with `--board-inbox`). An interrupt entry
  (`interrupt: true`, only its creation time as `sentAt`) asks the adapter to
  stop the running turn; it is never a message (no image cap, no
  held bubble; cancel and steer refuse it).
  `dashboard.inbox_drain interrupt_only: true` claims only those (each
  record `interrupt: true`; exclusive with `steer_only`), the steer-only
  drain leaves them, and the full drain drops them without a `drained/`
  record. `mailbox wait --board-interrupt` also wakes on one (alone, or with
  `--board-inbox` or `--board-steer`), counted as `interrupt` (its own
  `kinds` value), never as `board`. A command entry (`command: {name,
  args}`) is one slash command the page sent, only to a session whose
  snapshot lists `commands`, only from a browser on this machine (403 to any
  other viewer, a LAN token holder included), and only with a name that
  list holds (400 otherwise). Steering converts it to a steered text
  message containing its typed slash-command line, so the agent reads it
  mid-turn instead of the adapter running it natively. Left unsteered, a
  drain returns it in its place among the unsteered messages with a
  `command` field (steered messages drain ahead of it), where the adapter
  splits its batch and runs it natively. A hosted backend passes a plain
  message verbatim and runs a leading-slash line as a command, so a message
  from any viewer not on this machine whose text, trimmed of leading
  whitespace, starts with `/` is
  refused too (403); a slash later in the text is held as usual.
  A board item's sent mark comes from these records (the newest send's chip per item,
  held over drained; a chip sent before the item's latest re-ask marks
  nothing). A drained note-only chip (a note, no value) sent before the
  start of the session's newest settled turn clears its item's mark, so the
  item counts as waiting on the owner again; the note stays in the
  delivered message and the drained record. The settled turn is the ring's
  newest `turn` entry, which starts at its time minus `durationMs`. A
  `turn` entry without a duration settles nothing.
- `sessions/<reply-id>/ack-check.json` - legacy cutover input for the daemon's
  answer check (`internal/wsdashboard/ack_check.go`); live state is stored in
  `metadata.sqlite` (`ack_checks`, zero post-cutover writes to `ack-check.json`):
  `{"through": <epoch ms>, "interruptedAt": <epoch ms>}`. When a session's
  turn settles, the daemon checks the batch of answers that turn received
  and holds one plain inbox message (no chips) naming the batch items still
  `open` on the board, asking the agent to record them with `wb:answer`.
  The batch is every drained value chip (an answer, with or without a note;
  never a note-only chip or a topic note) sent in `[through, settled turn
  start)`, minus the chips sent before the item's re-ask. The check runs
  about 1 s after a session change, an agent runner write, a hosted
  snapshot change, or a hosted ring append (a daemon start re-checks every
  session through the index's first refresh), and only when all of these
  hold:
  - the session source reads board items;
  - the session is indexed, has a board, and is not an agent conversation;
  - its host adapter is live;
  - the lead is idle (no turn, phase idle, no permission wait);
  - the ring's newest `turn` entry has a duration and an end `reason` other
    than `aborted` (the Claude mod's terminal stop);
  - that turn started after `interruptedAt`, which the dashboard interrupt
    route records;
  - the inbox holds no message (held input means another turn is coming).

  A skip leaves `through` as it was. A non-empty batch moves `through` to
  the turn's start, whether a nudge goes out or not, so each answer is
  checked once and a nudge (no chips) never triggers another. The
  session's first check (no `through`) only seeds it and sends nothing.
  The record is written, under the daemon's inbox lock, before the nudge,
  and only when it is unchanged since the check read it. A crash can
  therefore lose a nudge but never double one, and two checks never both
  send.
- `sessions/<reply-id>/agent.json` and `instructions.md` - live conversation
  records (`internal/wsagent`) are stored in `metadata.sqlite` (`agent_records`
  + bounded `agent_record_deltas`, with holder sequence-only updates written to
  `agent_holder_cursors` rather than rewriting the whole record); `agent.json`
  is the frozen/fenced cutover snapshot only. The record holds what a process
  start needs after a daemon restart (profile, backend, working root, backend
  session id, model and effort, the conversation's ws session key), and
  `instructions.md` stays on disk as the composed instruction file appended to
  the harness's system prompt (owner-only; it carries the session key when the
  profile allows ws tools, and a Claude lead's file always carries the key and
  the `workflow_manual` instruction). A legacy Pi record can still refer to the
  session file under its `pi/` directory.
  The daemon reads the conversation from its live stores alone, since no
  ws-mcp serves it and it has no session record.
- `sessions/<reply-id>/permission.json` - legacy cutover input and one-time
  downgrade fence for a hosted lead's explicit common permission selection
  (`{permission_level}`); live selections are stored in `metadata.sqlite`
  (`permissions`), preserving unknown members and keeping an existing
  unreadable or invalid selection distinct from absence (fail closed,
  `internal/wsagent/permission_store.go`, `internal/wsagent/permission.go`).

After a store write the serving ws-mcp sends `POST /nudge {reply_id,
project}`. The daemon wakes every `mailbox wait` it hosts on each nudge,
registered or not, before it checks the named session (at most one recheck
per wait per second, plus a trailing one). A server the daemon
hosts (Daemon-Hosted MCP below) instead hands each live snapshot to the
daemon in memory (`wsdashboard.HostedSlot`): the daemon serves that copy in
place of `snapshot.json` and wakes the session's pages with a live-only
wake, whose render re-reads only the snapshot (and the ring after an append,
which the slot signals in place of a nudge). The hosted writer still writes
`snapshot.json` for recovery: once per heartbeat interval while the state
changed, at each turn end, and when its server closes (which also drops the
copy, so the file renders again); an idle heartbeat writes nothing and only
moves the copy's `updatedAt`. A session whose directory the daemon does not
serve stays on the file-and-nudge path. The daemon runs full registry
safety discovery every five minutes, with targeted 30-second legacy
liveness/recovery checks between scans. An ended session already applied
is not re-read unless a nudge, resume, or retry requires it. Scheduled per-session re-reads are coalesced to at most one per second,
with a trailing read. A runner write refreshes only the live-store fingerprint
(snapshot and live directory entry names), retaining the last full read's
record, board, tickets, mailbox history and contacts. A nudge, periodic safety
refresh or board poke reads both parts. Known inbox-only changes on boardless
sessions list only the inbox stores; unknown runner writes still list every
live store. A re-read builds no panel; the board renders only when a page
reads the changed session. Active hosted sessions pin their conversation ring
in daemon RAM (loaded once from segments plus any `.json` entry files, with
held-open append handles released on slot close or shutdown; closed and
non-hosted rings are fingerprinted by directory metadata without loading bodies
or pinning RAM); drained-inbox entry files are parsed once per daemon and kept by
name for the window the page shows; mailbox
history, contacts, and unread counts come from the memory store (Mailbox
store below) with no file read. The small files every read and render
re-reads (session snapshots, the session record's part files) share parsed
values and stat validation for up to one second per path. Recent files are reread at validation until
outside the two-second timestamp ambiguity window. Explicit session change
signals expire the targeted source and snapshot validation before the index
reads, and write admission/capability checks read current evidence rather than
the presentation cache. A session's entries drop when it leaves the index. A
page's refresh stamp and the board memo stamp a ticket status directory by
its listing rather than each ticket file, and key the board on the
session's own reply-registry contacts and unread count rather than the
whole registry file.

The daemon serves its pages with no CORS: `/` lists sessions by project
(ended ones for a day), `/s/<reply-id>` shows one session, and `/events` and
`/s/<reply-id>/events` are their SSE streams, which send only the page regions
whose hash changed. A session with a fresh adapter snapshot gets the live
layout (lead, agent tree, conversation, board); one without gets the
board-first page. The live layout's composer carries the status band: the
context bar (percent, tokens of the window, the last response's cache hit
rate) above the message box, and below it the folder, the 5-hour and
weekly usage limits (each with its reset and its
pace, which the page computes), and the cost (`~$` for an estimate, a
trailing `+` when calls of unknown price were left out). A part with no
figure is left out; context and limits warn at 80% and turn critical at 95%;
below a 360px band only the limit under more pressure stays. The composer also
holds the `cmds` region, the snapshot's `commands` as hidden data, rendered
for a viewer on this machine only (a LAN viewer's page has no region and no
picker): typing `/`
in an empty message box opens a picker over them (grouped under user and
agent headings when any command names its `layer`), and a pick becomes a
command selection whose box text is its arguments, sent as `{command, args}`
alone (answer chips and notes stay for the next send; images refuse it). A
session with no list renders the region empty and gets no picker. A
`command` ring entry shows its typed line and output text, or an "opens in
the terminal" note when the text is empty.

The session page's drawer frame (not the `/` hub) carries a git handle left
of the panels handle: the session root's branch, abbreviated (an
`impl/<parent>/<stem>` branch as a hammer and the stem, a `goal/...` one as a
gold trophy and the stem, a branch naming both kinds as both icons; any
other branch whole, ellipsized; a detached HEAD as its short id), its ahead
and behind counts, and a dot for uncommitted changes. Its title spells the
whole branch and, for an impl or goal stem, the title of the ticket
`<root>/ai-docs/tickets/*/<stem>.md` names (front matter `title:`, else the
first `# ` heading; 16 KB read, cached a minute). The daemon reads it itself
(`internal/wsdashboard/gitbadge.go`) rather than from the snapshot, but
follows it: when a session's live snapshot brings repo figures that differ
from the ones it brought before and from the handle's last read, the stream
asks for a read as a refresh would, so the two agree once it lands. It reads
a root only while a page watches its git display: once on first use,
then checks Git administration metadata at the prior 3-minute cadence.
Unchanged metadata suppresses commands, but a complete read is forced within
five minutes for in-place edits/untracked contents. Explicit refresh bypasses
the hint, including `POST /s/<reply-id>/git` (sent when the git drawer opens; it needs
an `Origin` equal to the daemon's own, answers 204, and 409 when no open page
watches the root). One goroutine per watched root runs every read, so pages
sharing a root share one read and no two reads of a root overlap; a refresh
inside 2 seconds of the last read waits for that gap's end, and requests
arriving meanwhile fold into that one read. The last stream's close ends the
root's reads and drops its value. New WebSocket pages opt into `display=0|1`
and optional client visibility frames: hiding releases only git/rail watches,
not functional region/tracker streams; showing refreshes the display. Older
WebSocket clients and SSE retain stream-lifetime watches. A complete badge
read is porcelain-v2 status plus top-level discovery (and short HEAD when
detached), with no optional lock, in the C locale,
bounded by 10 s together, with no daemon-wide lock held; it never fetches. A
non-repository root shows no handle; a failed read shows it dimmed, with the
reason (git's message) in its title.

The handle opens the git drawer (`internal/wsdashboard/gitdrawer.go`,
`gitpush.go`), which shares the panels drawer's frame. Its right panel lists
the changed files from `GET /s/<reply-id>/git/status` (JSON: the branch
standing and the staged, unstaged, and untracked files, each with its status
letter and `diff --numstat` line delta, at most 300 per group; read when the
drawer opens and after each action, with no optional lock and no fetch) and
carries three actions, each a `POST /s/<reply-id>/git/<push|pull|fetch>`
under the same rules (POST only, the daemon's own `Origin`, the session's
root, one action per root at a time, 60 s): push re-reads the branch,
refuses a detached HEAD, a branch with no upstream, and one with nothing to
push, and runs `git push -- <remote> refs/heads/<branch>:<merge ref>` with
no option, so the remote moves by fast-forward only; pull fetches the
upstream's remote and fast-forwards only, refusing a detached HEAD, a branch
with no upstream (also one the fetch pruned), modified or staged tracked
files (untracked ones do not count), and a branch that has diverged; fetch
is a plain `git fetch`. No network step of push, pull, or fetch ever
prompts on a console (the daemon's console is hidden, so ssh runs in batch
mode): an ssh key that
needs a passphrase must be in ssh-agent, and an unknown host must be
accepted once in a terminal, while a Git Credential Manager sign-in dialog
still appears. An action answers 204, 200 with a note (nothing to
pull), 409 with the refusal's reason, or 502 with git's message (a ref's
`! [rejected]` line first, then an `error:` or `fatal:` line, over a hook's
`remote:` lines), which the panel shows inline; each re-reads the handle.
The drawer's reads parse git's stdout alone, so a warning on stderr (CRLF
conversion) never corrupts them. The wide left area is reserved for a
future commit graph. There is no commit action.

The panels drawer's pooled-worktree card (`internal/wsdashboard/worktrees.go`,
served when the daemon has a `WorktreeSource`, else 404) reads `GET
/s/<reply-id>/worktrees`: JSON `{pool, worktrees, sizing}`, the session
project's `worktree_pool` and the pooled worktrees `worktree.list` owns
(path, profile, branch or detached, lease with holder and parent reply-ids,
acquire and last-activity times, quarantine, disk size), read when the
drawer opens. Sizes are measured off the request path (two walks at once,
cached 2 minutes); an answer with a size still unmeasured says `sizing`, and
`?wait=1` holds it until those sizes land (20 s at most). `POST
/s/<reply-id>/worktrees/cleanup` (body ignored) follows the composer's send
rules below and holds one message: a line in the page's language asking the
session to clean up unused worktrees, then the pool as text, cut at 48 KiB
with a `... N more worktrees` line (singular for one). The dashboard never
removes or releases a pooled worktree itself.

Every session page carries the session rail (`internal/wsdashboard/rail.go`,
`pageview_rail.go`): the listed sessions grouped by repository (or plain
folder) and worktree. It is one shared render (region `rail`, keyed by
language and theme, no reply-id) that each session stream sends beside its
page with a broker subscription of its own, so a session's change renders
the rail at most once for all open pages and no other session's page;
unchanged visible rail inputs reuse the render. The daemon retains each
session's snapshot, repository and last message in memory, refreshing only
sessions whose index revision or named session/snapshot/ring generation moved.
Explicit current-state queries such as the hiring roster still read through.
Clock-derived liveness is recomputed from those retained inputs. A transient
snapshot or ring read failure retains last-good presentation and keeps only
that session eligible for retry on refresh, even without another wake.
Rail passes are capped at one per second per shared language/theme view, with
a trailing pass so the last wake is not lost (`internal/wsdashboard/rail.go`). Streams opt into row deltas
with `railrows=1`: the first send on each connection is a full `region`
(`name: "rail"`), even when `h` holds the current hash. Later `rail-rows`
SSE events carry `{hash, order, rows: [{key, hash, html}]}`: the full row
order and only new or changed rows, keyed by reply-id. A changed grouping
shell or an uncertain row decomposition falls back to the full region;
without the opt-in, every rail update stays a full region
(`internal/wsdashboard/sse.go`, `Daemon.stream`,
`internal/wsdashboard/rail_rows.go:28-32,88-106`). The page also uses these
event names over WebSocket, reconnecting for a full rail if it cannot apply
a row patch (`assets/dashboard-stream.js:144-175,178-191,214-224`). A session's
repository comes from the registration's `git_common_dir` (ws-mcp's one
`rev-parse --git-common-dir` at first registration), else from its root's
`.git` on disk. The worktree rows come from `git worktree list --porcelain`
through the source's optional `RepoWorktreeSource` (pooled worktrees under
the `worktree_pool` roots show only while they hold a session), read only
while a page watches the rail display: on first use, for a newly shown
repository alone, and after changed Git metadata or the five-minute safety
deadline. The prior 3-minute checks remain; metadata never replaces mutation
admission, and pool configuration is re-resolved on complete reads. Explicit
refresh and opt-in visible resumption bypass the hint, including `POST /rail/refresh` (own Origin;
409 with no streaming page). Change counts come from the newest session
snapshot in the worktree; the rail never reads a working tree. Drafts,
stars, and collapsed workspaces are browser-local (`assets/rail.js`).
A linked worktree row that no pool root holds (the source's pools, the
default `<root>/../.ws-worktrees/<name>`, the legacy `<root>/.ws-worktrees`)
offers a delete to a browser on this machine (`railworktreedelete.go`):
`POST /rail/worktrees/delete/preview` answers the check (changes, a detached
HEAD's unreached commits, initialized submodules, lock, the sessions and
terminals in it, whether force is needed, and a token of that state); `POST
/rail/worktrees/delete` checks again and answers 409 with the fresh check
when it is refused (primary, pooled, missing, locked, a live session or
terminal, or a live retired Pi holder whose working directory is in the
checkout, even when its session row has ended or lacks a holder ID), needs force the submit did not carry, or was forced on a stale
token; else it runs `git worktree remove [--force]` from the primary and
keeps the branch. A viewer elsewhere gets 404. The retired Pi holder check
runs at preview, at final authorization, and again before the archive purge;
a holder inventory that has not finished scanning counts as occupied. Force
applies only to Git's own removal checks.
Ended sessions fold apart under each worktree while their registry directory
and session record exist, including beyond the index page's 24-hour window.
An eligible closed hosted lead offers `reopen` and an eligible Claude terminal
session offers `takeover` (`RailRow.Revive`). The takeover route checks whether
the terminal harness has exited. A retired Pi row opens its history without
a revival action or deletion countdown. `PruneRegistrations` spares the
directory while the runner may serve it. Other ended rows normally age out
after 7 days unseen.

On the served board and in the conversation's lead
replies, a file link renders as an action link (`data-wl="file"`, titled with
its repo-relative path) and an http(s) link is marked `data-wl="url"`: a
click opens a menu (open in new tab, open in system browser, open in native
app for an image file, copy relative or absolute path; copy URL for an
http(s) link), and Cmd/Ctrl+click opens a new tab. Every markdown renderer (the board, lead replies, tracker text) also
links a bare http(s) URL (a loopback or LAN host included) and a bare file
path (absolute or drive, two segments or more, or relative with a `/` and a
file extension; optional `:line[:col]`) outside code and raw anchors,
rendered as a written link to the same target would be. A file inside the
session's project root links `GET
/s/<reply-id>/file?path=<root-relative path>`, the file view, which serves a
regular, non-symlink file that still resolves inside the root after every
symlink (404 otherwise), to a loopback viewer and a LAN token holder alike:
an image inline, markdown rendered in the board's escaped mode with relative
links resolved against the file's own directory, other UTF-8 text as
`text/plain`, and a binary or over-2-MiB file as a page offering the path
copies. A preview card shows an HTML or SVG file inside the session's own
root in a sandboxed frame, through a 15-minute capability on
`/s/<reply-id>/fs/cap/`; for a viewer on this machine the capability route
refuses only a dot-named path, and for a LAN viewer a dot-named or
git-ignored path (failing closed when the ignore check fails), and a file it
refuses previews as its source (`internal/wsdashboard/fs_capability.go`
states the rules). A file outside the root offers the copies only, not the file view,
with two exceptions. For a viewer on this machine, a link to one on the
board or in a lead reply the live ring still holds opens in the preview
card, and an HTML file also in a sandboxed tab of its own: the click posts
`POST /s/<reply-id>/fs/outside {"path"}`, which answers only a loopback,
same-origin POST naming a path the page links (404 otherwise, a LAN token
holder included) and mints a 15-minute capability for the linked file's
folder, served to this machine only on `/s/<reply-id>/fs/ocap/` and
`/fs/oview/`. A volume root, the home, or a folder directly under either
grants the linked file alone; `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.config/gh`,
`~/.netrc`, and `~/.docker` never open; below the granted folder a
dot-named path is refused, the linked file itself excepted, while a
git-ignored one is served, as the in-root capability route serves it to this
machine (`internal/wsdashboard/fs_outside.go` states the rules). A card re-mints on
every load, and a tab whose capability expired lands on a page that re-posts
the mint. The other exception is an image
written as an absolute path in a lead reply, a board item field, or (through
the relay below) a topic body. A board image inside
the root loads through `GET /s/<reply-id>/image?path=<root-relative path>`,
which serves only an image-extension file under the same containment, and
any other image renders as its path, with that one exception: an absolute
image path outside the root (not a UNC or device path, image extension on
the name and on the file its symlinks resolve to) renders inline on the
image route and links through it (`data-out`), and the route serves it only
to a viewer on this machine (404 to a LAN viewer, token or not, and to a
request whose `Sec-Fetch-Site` is `same-site` or `cross-site`; absent,
`same-origin`, and `none`, a navigation the user started, are served), with
`Cross-Origin-Resource-Policy: same-origin`. Where such an image fails to
load (a LAN viewer, a missing file), the page puts its alt text and a
copy-only path link in its place, and its link offers the copies only; for
a viewer on this machine its menu offers open in new tab and open in native
app, not open in system browser. On a served page each topic body on the board
renders in its own sandboxed `srcdoc` frame (`allow-scripts allow-popups
allow-popups-to-escape-sandbox`, an opaque origin that sends no cookie). No
server response inlines an image as a `data:` URL: each file image in a
topic body, inside the root or outside it, stays path text marked
`data-relay` with its image route URL, and the frame element lists those
URLs as its own `data-relay`. The frame cannot load
that route itself (its opaque origin's requests are `Sec-Fetch-Site:
cross-site`, which the route 404s), so it asks the page by `postMessage`;
the page's relay handler accepts the request only from one of its own body
frames and only for a URL on that frame's list that is this session's image
route for an image name, fetches it same-origin (served only to a loopback
viewer when outside the root), and posts the frame an image `Blob`, which
the frame shows in the path text's place once it decodes. A LAN viewer's
fetch of an outside image 404s and the path text stays. The route streams
the file as is with an ETag of its size and modification time, and caches
a URL whose `&v=` matches that version as immutable. A loopback viewer is
also served a message attachment by its absolute path in a session's inbox
attachments directory, which the credential deny set otherwise refuses with
the rest of the daemon's state directory. The served board renders the 30 most
recently closed topics (with nothing waiting on the user) and a show-more
control; `GET /s/<reply-id>/closed?from=<index>&n=<count>` (from at least 1,
n default 30, at most 300) answers `{html}` with the next closed topics in
close order, rendered and framed as on the board, through the optional
`wsdashboard.ClosedTopicsSource`; a session without a board is 404. A finished lead reply draws its mermaid
fences; the reply still streaming keeps them code blocks. A finished lead
reply also keeps its inline HTML and SVG through an allow-list sanitizer: no
script, form, embed, or fetching element or attribute survives, and a raw
link or image follows the same link rule, so it renders as the same action
link or path text as its markdown form; the streaming reply, user messages,
and mail bodies show HTML as text. `POST
/s/<reply-id>/open {target}` opens an http(s) URL or this session's own file
view path in the host's default browser, and `{native}` opens an absolute
image path (inside the root or outside it, under the image rules above; the
file its symlinks resolve to is what opens) in the host's default app for
it, with the OS open command, run with no shell (`rundll32
url.dll,FileProtocolHandler` on Windows, `open` on macOS, `xdg-open`
elsewhere); a body names exactly one of them. It requires a
viewer on this machine (404 to any other, token or not) and an `Origin`
equal to the daemon's own, and the page offers the items only to such a
viewer. The live layout's
composer sends: `POST /s/<reply-id>/message {text, chips, topics, images,
refs, refNums}` holds the message in the session's inbox as its own held
message; sends never merge. `images` are new uploads `{name, data, n}` (base64,
sniffed as PNG/JPEG/GIF/WebP, at most 8 per held message), `refs` name files
a cancel returned, and `refNums` numbers them; the daemon writes the uploads
into the inbox's `attachments/` directory. The text names image `n` with an
`[Image #n]` marker, and the delivered message is the `<wb:answers>` block
of the board answers and topic notes (`wsinbox.AnswerBlock`), then the text,
then one `Image #n: <absolute path>` line per image. Numbers are per
message, as the draft numbered them: a turn that delivers several held
messages joins their texts, so the same `Image #n` can name a different
image in each, and each `[Image #n]` marker means the image of its own
message's lines. The page still reads the older `[image: <path>]` lines. `POST
/s/<reply-id>/cancel {id}` takes a held one back, answering `{text, chips,
topics, images, nums}`, and `POST
/s/<reply-id>/steer {id}` promotes a held one to steering, answering the
rewritten entry's new `{id}` (409 once the session has received it). The
adapter claims a steered message at the session's next tool boundary instead
of at the turn end. `POST /s/<reply-id>/interrupt` (body ignored) takes back
every held message, steered or not, and writes an interrupt entry in one step
under the inbox lock, answering `{held: [{id, text, chips, topics, images, nums}]}`
in queue order (steered first, then the rest), each shaped as a cancel's answer. All four require an `Origin` equal to the daemon's own,
and a session without a fresh adapter snapshot gets 409. `whiteboard.update`'s
`full:` line names the session's dashboard URL once its last nudge was answered,
and the page path otherwise. The URL never carries a login code, because tool
results land in transcripts; a browser without the localhost cookie gets the
login gate, whose clipboard button mints the single-use link out of band when
the host has a native clipboard writer.

The daemon creates one tracker host (`wstrackerhost.New`) before it starts
serving, shared by the tracker's background poll and the tracker routes, and
never a second; a host that could not be created still goes in, so every
tracker route answers 503 with its (redacted) error and the page's "+ store"
dialog shows it. The routes, JSON in and out with errors as `{"error"}`:
`GET /tracker/stores` (`{stores, rev}`), `GET /tracker/stores/<id>/view`
(`{view, seen}`; the view adds `html {items, comments}`, item bodies and
comment texts rendered as markdown under untrusted-text rules: only http(s)
links and in-page fragments become anchors, tags stay text), `GET
/tracker/stores/<id>/search?q=` (`{ids}`: the items passing the tracker
search grammar, `wstracker.ParseQuery`, the one `tracker.query`'s `q` takes;
a malformed query is 400 with the message the Items panel shows), `POST
/tracker/stores/<id>/dispatch {events}` (origin forced
to `human`, `"$N"` names the batch's Nth event; a refusal is 409 with
`refusal {index, type, reason}`), `POST .../check` and `.../sync` (a report; an
unreachable remote is `offline: true`, not an error), `POST .../seen {badge,
read}` (204), `GET`/`POST /tracker/profiles`, and `POST /tracker/register`.
The editor routes: `POST .../render {text, attachments, wiki}` (an editor's
preview through the view's renderer; `wiki` names a wiki whose tree images
also show), `POST .../attachments?name=` (raw image bytes staged for a later
write) and `GET .../attachments/<sha256>` (the bytes, magic-byte typed). The
wiki routes over the store's `refs/heads/wiki/<name>` branches: `GET
.../wiki` (the list), `GET .../wiki/<name>/pages`, `GET
.../wiki/<name>/page?path=` (content plus the commit a save sends back as
`base`), `POST .../wiki/<name>/save` (an edit; a merge conflict is 409 with
`conflict {path, kind, reason, base, theirs, oursText, theirsText}`, a
refused edit 422), and `GET .../wiki/<name>/attachments/<sha256>`.
An unknown store id is 404. Every POST requires an `Origin` equal to the
daemon's own and a body under 256 KiB, and runs to completion even when the
page closes mid-request. Every string a route returns has credentials
redacted. The tracker revision bumps after a route's write (and after a
check or sync, even a failed one), after the daemon's first poll round and
each later round that changed something (`wstrackerhost` `StartPoll`,
`PollOnce`), when the 10 s store watch sees a write by another process, and
when a reload that a page read stopped waiting for lands with a view other
than the one that read was served (`now` aside): a page read waits at most
500 ms behind whatever holds the store's lock (a wiki save, another
process's write), or a slow reload, then serves the last folded state
(`ViewWithin`). A check or sync holds that lock only for its local steps:
its fetch and push run under the store's network lock (`net.lock`), which
only a read of an attachment not yet fetched waits for, with the same
500 ms bound. The daemon's tracker git
runs resolve the git executable once per `PATH`. Both pages' SSE streams send `event:
tracker` with `{"rev": N}` when it differs from what that stream last sent
(once on connect too), and the page re-reads.

The daemon also hosts headless agent conversations (`internal/wsagent`, wired
in by `cmd/ws-mcp/dashboard.go`). Each conversation starts from a named profile
in `agent.profile.<name>`. The whole object resolves through the conversation's
working root at project, repo, or global scope. Repo scope may set neither
`mcp_servers` nor `plugins`. The built-in `tracker-support-claude` profile
can be replaced as a whole by a profile of the same name. Its
`instructions` value `builtin:tracker-support` names embedded text.
The profile allows the `Agent` tool as its only built-in tool, so it may spawn
subagents: Claude applies the launch's `--tools` and dontAsk allowlist to
them too (a subagent type whose every tool is outside it is refused), and
its system/init reports the tool as `Task`, which the init check accepts
for `Agent`. Claude runs such a subagent as a background task: the
runner maps its `task_started` and terminal `task_updated` /
`task_notification` lines to a snapshot subagent (running, then done or
failed; failed too when the process ends first), which drives the
conversation's subagent badge, and drops the subagent's own
`parent_tool_use_id` lines from the lead's reply and batches. Each
conversation is a dashboard session (registration, snapshot, ring, inbox)
whose page shows it like a lead session. Its routes, behind the same access
guard: `GET /agents` (every conversation's state), `POST /agents {profile,
root}` (open one), `GET /agents/<id>`, and `POST /agents/<id>/send {text}`,
`/interrupt` (answers the held text it took back), `/stop` (ends the
process; the next send resumes the backend session), `/close` (ends the
conversation for good; later writes get 409), and `/settings {model?,
effort?}` (from the next turn). Every POST needs a JSON body
(`Content-Type: application/json`) and either no `Origin` (a client on this
machine) or the daemon's own, so no other web page can drive an agent. After
a daemon restart or replace, process continuity follows the ownership matrix
in Daemon replacement and process ownership below.

An attached ws-mcp gets
`WS_MCP_ALLOWED_TOOLS` (the profile's ws tools) and `WS_MCP_NO_DASHBOARD`,
and no mailbox identity. Its command is Python plus the selected build-owned
package's staged `bin/ws-mcp-launcher.py serve --stdio`, never the daemon's
staged executable as a stdio server.
The built-in global-only `hosted.plugin_package` key (`config.tune`, values
`ws` or `wsflow`, fixed default `wsflow`, visible with no-agent) selects the
whole package for hosted Claude leads and this attached MCP. Each process
start reads the setting; running and holder-reattached processes stay unchanged.
Both packages come from the daemon's embedded inputs (or its explicit process
source override), never a connected plugin root or registry. An invalid
hand-edited selection warns and uses `wsflow`; missing/corrupt build inputs
refuse the launch. Non-hosted connections keep their own package identity. The launcher's interpreter (`WS_MCP_HOLDER_PYTHON`) is reused
when known, else `python3` on PATH (Windows: `py -3` fallback). The conversation's
root overrides `WS_MCP_PROJECT_ROOT` in that command's environment, keeping
proxy mode's exact arguments and preventing the daemon's lead root leaking
into the worker. A proxy-hosted worker still uses its capability token for
nested `agent.*` calls and authorized legacy Pi history reads.

A spawned conversation (`internal/wsagent/spawn.go`) is a non-lead
conversation another session opens with its first message (`Runner.Spawn`:
profile, root, optional cwd, model, effort, label, and parent `{kind,
reply_id}`) and steers by its id; its record keeps `cwd`, `label`, and
`parent`, and it is unlisted like a support conversation. Each turn settles
once: done, failed (the harness's error, its process ending under it, or a
process that could not start or take the message), or interrupted (an open
turn the next daemon cannot recover because its holder/process is gone,
or it ran without a holder; the record's `turn_open` marks it). A daemon
replace lets a surviving holder continue its turn. A settle of a
conversation with a parent goes to the runner's `Options.Report`; closing
one settles nothing.
`Runner.Wait` waits for a turn that took every message sent before the
wait (a message the page cancelled before a drain counts for no turn, and
a slash command's turn covers none of the text drained behind it); the
last reported turn is kept in the record (`last_turn`), so after a daemon
restart a wait on a conversation that settled before it answers at once.
`Runner.Watch` reports each change of a conversation with a parent.
A spawn records its `tier` (`SpawnSpec.Tier`, `Record.tier`).

In 0.52, `claude.exec_cutover_enabled` defaults to true in both ws and wsflow
for hosted Claude leads and spawned workers. Set false to opt out. The native
removal switch, verified Claude Code version, trusted binding, and worker
`exec.shell` allowlist conditions still apply; config read errors keep native tools.

#### Legacy Pi compatibility

0.52 is a T3 upgrade. A 0.51.2 launcher reading the shared 0.52 daemon's
`/health` refuses the newer minor and tells the operator to update the
plugin and restart the session. The daemon also checks the protocol and
major.minor on a versioned `/mcp` hello before accepting the connection.
Update the installed `ws` or `wsflow` plugin in each affected harness to
0.52, then restart each affected harness session so its launcher and skill
shims load the update. A reconnect from the old session still uses its
older launcher.

The daemon keeps the `ws-pi-bridge` initialization identity and two hidden
legacy history reads (`pi.list`, `pi.transcript`) out of `tools/list`
(`internal/mcp/pi_tools.go`). Authorized callers can use `pi.list` for their
own recorded Pi children and `pi.transcript` for a child's retained transcript;
these reads work without the retired `dashboard.pi_spawn_enabled` setting. In
0.53, the four legacy execution refusal stubs (`pi.spawn`, `pi.send`, `pi.stop`,
`pi.wait`), their worker-token routes, direct `pi.<tier>` call acceptance, and
the Pi-specific refusal branches in `session.request_hire` are removed
(unknown tool / normal harness admission). Current worker operations use
`agent.spawn`, `agent.interrupt`, `agent.list`, `agent.tiers`, and
`mailbox.send`.

Stored `pi` tier pointers, aliases, and bundles continue resolving to `werk` at
runtime under werk's availability check, while directly passing a `pi.<tier>`
token on a call surface is refused. When the werk slot is unset, the resolver
uses a stored Pi model only if its ID exactly matches the OpenRouter catalog.
Stored Pi records, profiles, and routing values remain readable. Retired Pi
conversations show as ended. The startup sweep leaves live legacy holders to
exit on their own, including orphan holders. Revival, wake, handoff, and new Pi
execution are retired.

##### Retired Pi history

Before retiring a Pi record, 0.52 commits a copy to
`<daemon state>/retired-pi-history/<reply-id>/`. It preserves `agent.json`
and, when present, `registration.json`, `snapshot.json`, `ring/`, and the
daemon-owned `pi/session.jsonl`. On startup, 0.52 refreshes changed archives
from surviving sessions and keeps archived components missing from the
live directory. It combines archived and surviving ring entries within
the existing ring retention limit. A complete copy stays recoverable
during refresh. The archived JSONL keeps later appends through a hard link
where supported. Other filesystems keep a copy from the last archive snapshot.

During a matched v0.51.2 rollback, the old pruner walks only `sessions/`
and can remove aged, retired sessions that lack a terminal owner (v0.51.2
`internal/wsdashboard/registry.go:386–425,461–472`). On return, 0.52 can
recover the Pi history it last saw before rollback. History written by
v0.51.2 during rollback follows v0.51.2 retention. On startup, 0.52 restores
missing session directories from the archive and retires restored open
metadata again. Recover an existing partial session directory manually from
the archive, or explicitly purge it. The 0.52 seven-day sweep leaves
retired Pi session directories and archives in place.

When you delete a checkout from the dashboard, 0.52 commits pending Pi
history deletions before removing their archives and running
`git worktree remove`. If archive removal fails, the dashboard returns
HTTP 503. Retry deletion on the same checkout, even if its session row has
disappeared. Startup honors the committed deletion and skips restoring or
refreshing that archive. If Git fails after archive removal, the live
session directory and pending deletion stay available for retry. Successful
purge removes the live session directory and clears the pending state once
both copies are gone.

#### Worker mail and delegation

Mail to a spawned child's address (`id:<reply-id>`, as `agent.spawn`
returns it) is ordinary `mailbox.send` to that reply-id queue in the
memory-first store: one restore-ring line per send and no `mailbox.json`
while the daemon runs, with the usual history and reply-id handling. The
runner watches every spawned child's mailbox (`Options.Mail`, the daemon's
`LeadMailbox` in `internal/mcp/lead_mail.go`), awake or asleep, and claims
what arrives. Each claimed message's body opens with a `[relation:
parent|child|peer]` line, computed at claim time from the `Record.Parent`
lineage (no Envelope field); system mail carries none. A running turn takes
the mail as a steer when the harness's catalog profile does not grade steer
`unsupported` and the process has a steer handle. A child's steered mail
is held like a steered report, so its turn's report covers it. Otherwise the mail waits in `Record.Mail` for the
next turn. Mail to a sleeping child wakes it, ignoring the width cap, and
resets its waiting clock (`LastActiveAt`); a refused wake keeps the mail and
tells each sender once (`Options.WakeRefused`, posted as `agent:<id>`
system mail). Each intake-fed
server watches the runner and feeds the owning lead's recursive spawn tree
(linked to each child's page) into its live state;
a live session the server creates, or one an adapter resets, gets its open
descendants' rows again, so they come back after a reconnect. A dashboard-hosted
lead instead gets those rows from its runner-owned snapshot writer, including
after daemon replacement; hosted intake remains off. Descendants nest under
their spawner through ordinary `parentId` links. When a harness also reports the
same conversation (its reply-id or native conversation id), the snapshot keeps
one daemon-owned lifecycle row with the harness's activity and telemetry. An
intake-only session whose dashboard feed is off gets no row.
The composer's subagent badge opens the agents popover over the conversation;
it stays visible but muted with no running agents, even on a non-live
session. Escape or a click outside closes an unpinned popover; a pin is
browser-local and reopens it on the next load, and close unpins it
(`assets/dashboard.tmpl:239-250`,
`assets/dashboard-composer.js:1459-1464,1483-1489,1525-1565`).

A spawned worker delegates through the daemon's capability-token route
(`internal/wsagent/children.go`, `internal/mcp/agent_tools.go`). The daemon
owns the tree. Current `agent.*` calls and legacy Pi history reads share
this route.

- **Token.** A spawned conversation with a parent and a recorded tier gets a
  token when its profile allows delegation tools. The attached ws-mcp reads
  the token from `WS_MCP_PI_TOKEN` and the daemon's loopback base URL from
  `WS_MCP_PI_ENDPOINT`. The runner sets the endpoint before start.
  The token is drawn at every actual process launch and void once that
  process ends or the conversation closes. A holder-backed worker keeps it
  in its record; the successor restores it only after checking the recorded
  holder association, so reattachment does not revoke a surviving worker's
  ability to call delegation tools. A conversation opened through `POST /agents`, or
  spawned with no tier, gets none. Its session key stays a parent-less
  delegate key: the token authorizes delegation tools alone, so
  every other lead-only tool stays refused.
- **Forwarding.** A ws-mcp with no runner and both variables set forwards
  worker operations and authorized legacy Pi history reads. The token scopes
  the route to the caller's children. Forwarded calls rediscover the current
  daemon endpoint.
- **Cwd and limits.** A nested spawn defaults to the worker's working
  directory and resolves relative paths there. The lead is depth 0 and the
  tree is capped at depth 4. `agent.spawn` allows up to 99 awake direct
  children per spawner.
- **Close.** Closing a conversation closes open descendants deepest first.
  A worker's idle timeout stops its process and puts the conversation to sleep.
  A forced `worktree.release` closes the conversations bound there and ends
  their processes (holder stop: graceful, then killed) before it persists the
  closure; a stop that fails keeps the closure with its holder, which still
  counts as occupancy until a later daemon with a complete holder inventory
  ends it (`wsagent/dead_binding.go`). A spawned non-lead conversation with evidence
  that its worktree was removed (a bound checkout's directory, `.git` entry
  or admin directory gone, or its root or working directory gone from a
  parent that still exists and inside no live bound checkout) is closed at
  startup and refused at any later start or wake, so a removed worktree is
  never repopulated (`goneBinding`, `wsgit.CheckoutRegistrationGone`). A
  missing subdirectory of a live checkout, or a path on an unreachable
  volume, is not evidence: that start fails and can be retried. An emptied
  directory that still exists but has lost its `.git`, and a worktree of an
  in-repository pool removed while no daemon ran, cannot be told apart and
  are not caught. `worktree.remove` refuses, before it touches
  files or Git state, while any bound conversation occupies the worktree (an
  open binding, or a released one whose process still lives), naming each
  one's address, state, holder, PID and cwd; a daemon that cannot answer
  fails it closed.
- **Worktree removal.** After its checks and `pre_remove` hooks,
  `worktree.remove` renames the checkout into `<pool root>/.trash/` and the
  worktree's admin directory into `<git common dir>/ws-worktree-trash/`
  (same-volume renames, O(1) on the request path), then deletes both on one
  background goroutine (`mcp/worktree_trash.go`). A failed checkout rename
  (on Windows, a process outside ws with a file or working directory open
  inside it; or a cross-volume pool) changes nothing; a failed admin rename
  moves the checkout back, and a failed move back names both paths. A locked
  worktree is refused before the hooks. Entries a process left behind are
  retried by a background sweep of both trash directories on the first
  `worktree.*` call or dashboard rail read per repository in each process,
  then at most every 30 minutes; a sweep skips entries younger than one
  minute (a concurrent remove's rollback window, read from the entry name)
  and deletes at most 32 per directory. It also keeps a pool trash entry
  whose `.git` names an admin directory still under `worktrees/` whose
  registered path has no checkout (the failed-move-back case), until it is
  moved back or the registration is pruned. Pool scans (`worktree.list`,
  `worktree.acquire` reuse, the stale nudge, the dashboard pool card) skip
  `.trash/`, and release and remove refuse paths in it. A path under an
  owned pool root that is no longer a registered worktree (git unregistered
  it but could not delete its directory) is resolved from the calling lead's
  repository by filesystem path and cleaned up through the same trash, under
  the pool acquisition lock, only when it holds directories alone (no file,
  `.git` entry, symlink or junction, and is not one itself); the pool root
  and `.trash/` are refused.
  Waiting children expire after `agent.child_waiting_days` (default 15),
  measured from their last activity.
- **Keep-awake.** With the global-only `agent.keep_awake_during_turns` on
  (default false), the runner holds one idle-system-sleep assertion while any
  conversation instance has a live turn (`wsagent/keep_awake.go`,
  `internal/wsawake`). A turn counts from admission, before its send, through
  tool calls, approvals and retries, to its end, transport loss, close,
  hand-off or shutdown; a standalone prompt counts during its send and a
  native `/compact` until its command settles. A rejoin keeps the membership
  it had while replaying and then publishes what the replay rebuilt. Queued
  input, idle processes, terminal sessions and exec jobs never count. The
  daemon reads the value after publishing its endpoint and rereads it on the
  companion-changed invalidation (`config.tune` set or reset) and on a
  settings write. One worker goroutine makes every OS call: `caffeinate -i -w
  <daemon pid>` on macOS, `SetThreadExecutionState` on one locked thread on
  Windows, nothing elsewhere. A failed assertion is logged once and retried
  only at the next activation.

The runner also hosts lead sessions. A headless Claude Code, agy, or Codex
process that loads the user's own harness setup (no profile allowlist or
plugins of the runner's; a Claude lead gets one appended daemon-written
instruction file with its session key and the `workflow_manual` instruction,
so a machine that blocks some plugin hooks still teaches it), started from
the corresponding built-in lead profile. The daemon also hosts werk leads
in process.
`POST /agents` refuses a lead profile; a lead is spawned
by `POST /agents/leads {root, harness: <registered lead harness>, provider?, model?, effort?,
permission_mode?, plugin_isolation?}` (201 with the state; `root` must be
inside git, since the session key is minted at the canonical root).
`plugin_isolation` accepts `default` (the default, replace ws only) or
`isolated` (bundled plugins only); a Claude lead ignores it.

For `harness: "werk"`, supply a `model` ID for the selected provider. The
`provider` selector defaults to `openrouter` and also accepts `openai`
(Sign in with ChatGPT) or a configured `local/<name>` (see [Provider
credential store](#provider-credential-store)). Omit `effort` to use the
built-in profile's unset reasoning effort. The default permission mode is
`workspace-write`; `read-only` and `full-access` are also supported.
Werk's `default` isolation loads the normal skill roots. `isolated` uses
only enabled bundled skills, including on reopen. The daemon mints the
lead key at the canonical root and binds the tool session to the werk
conversation, its working directory, and its authority in process. A werk
lead has no harness PID or OS holder.

Differences from a support conversation:

- **Process.** An external harness runs under a session holder (`internal/wsholder`,
  pipe mode), not as the daemon's child, so it outlives the daemon. The
  record (`agent.json`) keeps the holder id, the harness PID, and the last
  holder record sequence applied. A starting daemon rejoins each lead's
  holder, replays its ring up to the holder's current sequence, and applies
  only records past the recorded one: no entry is written twice, and a
  permission prompt that waited across the restart waits again. It never
  starts a `--resume` process while the holder's process runs. A lead holder
  no conversation runs under is ended after the first scan, subject to the
  legacy Pi holder guard. The hosted session's launcher restarts the daemon
  on ws-mcp reconnect (Daemon-Hosted MCP below).
- **Identity.** The daemon mints the session's lead key at spawn and writes
  the registration itself (`hosted: true`, `host_pid`), heartbeats it, and
  end-marks it at close. An external harness gets `WS_HOSTED_SESSION`,
  `WS_HOSTED_SESSION_KEY`, and `WS_HOSTED_SESSION_ROW` (Environment below);
  a Claude lead's appended instruction file (`instructions.md`) is a second
  route for the key; the daemon's own `CLAUDE*`, `WS_MAILBOX*`, `WS_MCP_*`, `WS_RSRC*`,
  `WS_HOSTED_*`, and `WS_PI_*` variables are not passed on. A ws-mcp that is
  not the hosted harness's never writes, heartbeats, removes, or end-marks a
  row that carries `hosted: true`, whatever key it is given.
- **Prompt.** Each launch (a resume included) of a Claude, agy, or Codex lead
  appends `<session dir>/instructions.md` to the harness's own system
  prompt (Claude `--append-system-prompt-file`, agy's
  `_meta.antigravity.systemPrompt.append`, Codex `developerInstructions`): the session key, and the
  workflow manual the daemon renders for it as of that launch (the daemon's
  `LeadManual`, the same render as `workflow_manual` on the session's own
  connection). A failed render leaves the key sentence alone and is logged.
  A session under the subagent-removal policy (a lead or an `agent.spawn` worker) whose backend catalog names a
  native subagent tool for its role also gets a line never to use that
  tool and to delegate through `agent.spawn`/`mailbox.send`
  (`Runner.subagentSteer`); a worker gets it after its key sentence. A lead
  of a backend with `PreferNativeSubagent` (agy) gets a native-first line
  instead, whether or not its catalog names the tool: its native subagent
  explores without ws tools at the harness's default model (a tier-specific
  question goes to a read-only `agent.spawn` subagent at an external-harness
  tier), rendered playbooks and ws-tool work go to `agent.spawn` with the
  rendered tier passed as returned (a role tier may end on agy, which is
  allowed; the Antigravity terms risk steers it to an external fixed tier
  such as `claude.small` when another harness fits, never a harness prefix
  before a role tier), and a native subagent is one-shot.
- **Playbook commands.** A backend with `PlaybookCommands` (Claude, agy)
  hosts leads with no ws skills: the lead's connection (not a worker's)
  renders under the `ws` server's names whatever package the daemon
  serves, a `{{ns}}:lead-*` skill reference renders as the `/lead-*`
  dashboard command, and delegation renders as `agent.spawn`. Its prompt
  says what a `/lead-*` reference is and, when the manual rendered, keeps
  it as the only copy: for fresh state, and in place of `/lead-revive`, the
  lead calls `workflow_state`. The runner adds the package's
  self-preloading lead skills (`wsrsrc.PreloadCommands`, read once per
  runner, a failed read included, the workflow manual's own left out) to
  the lead's command list whenever its conversation opens or its harness
  reports a list, replacing a harness command of the same name and any
  earlier runner's entries. Running one sends the
  playbook the daemon renders for the lead's key (`RenderLeadPlaybook`, the
  same text as `playbook.read` on its connection) as the turn; a failed
  render sends a pointer to `playbook.read`. The page's user entry stays the
  typed line.
- **Backend descriptor.** What differs per harness here is data on the
  backend's `BackendRegistration`, read through the conversation record's
  `Backend`; common code names no backend. `ManualHarness` is the harness the
  lead's manual renders under (for example, claude, agy, or codex);
  `PlaybookCommands` is described above. `PreferNativeSubagent` (agy only)
  selects the native-first prompt line above and, for text the hosted lead
  executes itself (`workflow_manual`, `playbook.read`, the playbook-command
  render), native-first terminology: `ExploreAgent` names its native
  subagent, `SpawnIdiom` names `agent.spawn` with the same tier rule as the
  prompt line (`nativeFirstTierRule`, `internal/mcp/playbook_terminology.go`),
  and the continuity tip names `agent.spawn`'s subagents.
  For that lead's workflow manual, the renderer also rewrites three rendered
  sentences (`nativeFirstManual`; plain text, not declared variables, so the
  resource and caller-context validation are unchanged). The exploration
  bullet loses the "tier the question needs" suffix. A rendered delegate is
  spawned through `agent.spawn` with the returned path and the rendered tier
  (no model/effort bindings, which `agent.spawn` does not take), and the tier
  line states the tier rule. agy-to-agy restraint is this prompt only: agy
  adds no harness-specific count or tier limit, while the generic depth,
  nested-tier, and width limits of every `agent.spawn` still apply.
  `playbook.render` output is unchanged for every caller: a rendered
  delegate runs on another process.
  `CallFallback` opts hosted lead connections into the compact `call` tool
  and a generated callable-tool index appended to their workflow manual.
  Only agy sets it today; ordinary plugin/stdio connections, other hosted
  harnesses, and every worker neither list nor accept `call`. This is a
  boolean for clients that may hide tools for any reason, not a size cap:
  agy drops definitions over 1,500 B including its 39 B description prefix;
  other clients may cap tool count. The independent listed-definition test
  cap is 1,450 B. The index uses the same permission- and role-filtered set
  as `call` list/schema discovery, with advertised names and the first
  sentence of each description. Runtime catalogs omit this conditional
  tool from their default connection surface.
  `ToolNameScheme` is how its connection's `tools/list` spells ws's dotted
  tool names. agy's `sanitize` scheme writes `tickets_query` for
  `tickets.query`. agy adds `mcp_ws_` itself and the Anthropic API refuses dots.
  A call by alias maps back to the canonical name first in
  `callTool`, so allowlists, the session-key rule, and dispatch see dotted
  names; the dotted name stays accepted. The alias table refuses a collision
  when it is built. Workers identify their own record with
  `WS_MCP_AGENT_SESSION_KEY` in the attached MCP environment; this selects
  spelling only, without the hosted lead's implicit-key rule or authority.
  `WorkerCapable` and `WorkerProfile` admit worker tiers; `WorkerHTTPMCP`
  selects HTTP attachment. Claude uses HTTP.
  **agy workers run for agy callers only** (owner decision (1546),
  2026-11-22, revising (1545)): Antigravity's terms say its login used
  through third-party software can get the account suspended and that
  wrapping its CLI in another coding agent is unsupported on consumer plans,
  which a Claude or Codex session driving an agy worker is; agy-to-agy
  use is the owner's accepted risk. The registration option
  `crossHarnessRefusal` (set only for agy, which is `WorkerCapable`) admits
  a worker on that backend only for a caller verified on the same backend,
  and is the refusal's text; a backend without it admits every caller. One
  check (`admitWorker`, `internal/wsagent/agent_tools.go`) decides every
  worker entry: `agent.spawn` (an `agy.<tier>`, an agy caller's bare tier,
  an alias, or a graph route or bundle ending on agy), the profile-based
  `POST /agents` (no spawning session, so 403), and every launch, wake, or
  rejoin of an existing agy worker record (`admitWorkerRecord`).
  The caller's backend counts only when verified (`AgentCaller.VerifiedBackend`):
  on an MCP connection, the backend of the open conversation the connection
  is authenticated as, read from the daemon runner's memory
  (`agentAdmissionBackend`, `internal/mcp/agent_tools.go`;
  `Runner.CallerBackend`): a hosted session token's binding
  (`ProxyHello.Conversation`, never decoded from a launcher hello) or, on a
  launcher connection, the worker capability token the runner issued into
  its environment; on the token route, the holder's record backend for a
  nested spawn. Hosted-session variables, the registration row, and the
  record file are not a binding, and no `agent.json` is read on an
  `agent.*` call. The connection's detected harness (launcher hello,
  initialize `clientInfo`) selects tiers only; an unbound connection (a
  lead started outside the daemon) is cross-harness. A spawn stamps the verified caller backend on the child
  (`Record.SpawnerBackend`, a top-level omitempty field, apart from
  `Parent`, whose whole value keys the child index); a record without the
  stamp on an option backend takes its parent record's backend, resolved at
  startup from all records read before any rejoin
  (`resolveSpawnerBackends`), and is otherwise refused. The record check
  reads the backend, lead flag, and stamp before any profile, so an edited
  or removed profile cannot mask the refusal; a lead is never refused. A
  refused message, mail, or resume stays queued and the record stays
  listed, with the refusal as its error; it is derived again on every load,
  with no stored reason and before any profile, compaction, watchdog, or
  permission config read.
  On daemon start a held cross-harness agy worker is never rejoined. Its
  holder is attached only for the ownership match every held worker passes
  (`checkReattach`, `workerHolderMatches`), with replay, events, and the MCP
  bearer kept closed and no profile or config read. A matching holder is
  then stopped (`retireCrossHarnessWorker`): an open turn settles failed
  with the refusal. A failed stop keeps the holder in the record and is
  retried on every heartbeat until it succeeds; a user's Stop meanwhile
  retries the holder's own stop and keeps the holder if that fails too. A
  close (directly or through a parent's cascade) also retries that stop
  first and, if it fails, is refused (`errHolderNotEnded`, HTTP 409). The
  conversation stays open with its holder and heartbeat retry, the cascade
  closes no ancestor, and closing again retries. A downgrade retirement
  whose stop failed behaves the same way. Only the heartbeat's successful
  retry wakes input queued meanwhile: after a deliberate Stop it waits for
  the next send. A daemon that dies between clearing the holder and
  settling the turn leaves the next load to settle it failed with the same
  refusal, not as interrupted. A mismatched or already rejected holder is
  never stopped; the record only drops the association. Either way the
  record stays listed, stopped, and its activity is stamped, so child
  expiry gives it one full interval. An agy-parented agy worker rejoins
  and wakes as any other worker.
  `agent.tiers` and route resolution depend on the caller: for a verified
  agy caller agy tiers are spawnable; for any other, agy is listed under
  `Unavailable` with the refusal, and aliases, routes, and bundles ending
  on agy carry it. The dashboard tier view checks availability for its
  simulated caller harness (an agy preview admits agy, a Claude preview
  shows the refusal), and the MCP routed binding (`playbook.render`'s
  recommendation, `config.resolve_agent`) returns no binding after a
  refusal, even at the caller fallthrough (`errCallerRefused`,
  `internal/mcp/tier_routing.go`). Every daemon-hosted connection gets that
  check (`ServerOptions.CallerBinding`, the runner's read-only memory view),
  a token-forwarding worker's too, which holds no runner handle. agy leads keep peer mail and the
  watchdog's automatic `continue` and may spawn available workers, including
  agy. The dashboard's hosted lead popover still offers agy and shows a
  terms warning while agy is picked.
- **No ws plugin (Claude).** A Claude lead loads no ws bundle: the installed
  `ws` and `wsflow` plugins are turned off for the launch (`--settings`
  `enabledPlugins`), its ws-mcp is the `--mcp-config` server `ws` (no
  `--strict-mcp-config`, so the user's own servers still load) with an
  `--allowedTools mcp__ws` rule, and a launch with no ws-mcp at all is
  refused. Its ws tools are `mcp__ws__*`, not `mcp__plugin_<pkg>_<pkg>__*`,
  so user permission rules written for the plugin names do not match them.
- **Codex app-server.** A hosted Codex lead uses one sustained `codex
  app-server` under the existing holder, with thread/start or thread/resume
  and thread.cliVersion as the catalog version. It shadows the installed
  plugins' `ws` and `wsflow` MCP servers with required HTTP entries, with
  the bearer token only in the child's environment; skills and the user's
  hooks remain enabled. `agents.enabled=false` removes native subagents;
  delegation is through `agent.*`.
  An `agent.spawn` worker (`agent-worker-codex`, tiers `codex.<tier>`) uses
  the same start with no holder and no lead spec: the
  runner sets `StartSpec.PermissionLevel` from the child's authority.
  Workdir-write authority runs in the `workspace-write` sandbox with approval
  policy `on-request`, which `WorkerAutoApprove` answers and traces; full
  authority uses the `danger-full-access` sandbox with the same approval
  policy; read-only runs in the native `read-only` sandbox with policy
  `never` on `thread/start` and every `turn/start`, so Codex refuses
  an escalation itself and nothing reaches the auto-allow. Both are
  advisory: the sandbox does not confine a user-config MCP server shell, and
  a worker's sandbox is not changed after spawn.
  Send, steer, interrupt, next-turn model/effort and native sandbox mode
  changes, and `/compact` use app-server controls. Approvals stay binary:
  allow accepts, deny declines, permission grants last one turn; session
  grants, policy amendments, cancel and elicitation persistence are not
  offered. User-question requests get a chat-only error. Output is scoped
  by thread and turn, so late interrupted items cannot finish newer work.
  After a lost holder ring, thread/read recovers status and final output
  before held input is released. Native reply identities in the existing
  conversation-ring `source` member prevent already-persisted replies from
  being appended again; they are read once at reattach, not per message.
  Missing aggregate background telemetry stays unknown, never known zero.
- **Key rule.** On a hosted lead connection, a tool call that omits
  `session_key` (or leaves it blank) runs with the connection's key when the
  tool declares one; an explicit key must be the connection's own or the
  key of one of its descendant sessions (a runner conversation whose
  `Record.Parent` chain reaches the lead), and any other is refused naming
  the rule. A key a `ferrule` call minted under the hosted key has no runner
  record and is refused. Child and non-hosted connections are unchanged.
- **One holder.** A conversation is the daemon's or a terminal harness's,
  never both, and the registration row says which. The adapters report the
  harness conversation at login (`ferrule`'s unadvertised `harness_session`
  and `harness_cwd`, stored on the key record as `harness`,
  `harness_session`, `harness_cwd`; the directory is the harness's own, not
  the record's root), and a terminal ws-mcp writes its harness process on
  its row (`harness_pid`). Neither PID makes a session live; each only
  refuses a change of holder. A terminal re-login (`relogin_session_key`)
  to a key whose row is hosted is refused with the marker
  `hosted-session-held` while the row's process may run: `host_pid` alive,
  or no `host_pid` on a row that is neither `host_stopped` nor ended (a
  spawn under way). The adapter then stays keyless and shows the refusal
  instead of minting. The hosted harness's own login (the hosted flag, or a
  harness PID equal to `host_pid`) is accepted. Any other re-login to a
  hosted row takes it: the row becomes the terminal's (`hosted` cleared,
  `harness_pid` set), and the daemon lets the session go when it next reads
  the row - its record is marked `handed_off_at`, its mail watch and
  heartbeat end, mail it had claimed and not delivered goes back to the
  key's reply queue, and no later daemon start serves it. The daemon reads
  the row under `registration.json.lock` before every row write, mail
  claim, and spawn, and the login takes the same lock, so the two cannot
  both decide they hold the session. An accepted re-login to a row that
  is not hosted marks it at once as the terminal's (`harness_pid`, no
  `ended_at`, a fresh heartbeat), whatever the session's dashboard key
  says, so a row left ended (by an earlier run, or by a hand to the
  terminal) never reads as free for a takeover while that harness runs. A
  terminal ws-mcp whose dashboard key
  turns off leaves such a row in place (its directory holds `agent.json`)
  rather than removing it.
- **Moves.** Three control routes move a lead between the daemon and a
  terminal, each one step under the row lock and each behind the trusted
  gate for every id (`internal/wsagent/handoff.go`):
  - `POST /agents/<id>/takeover` makes the terminal session `<id>` a hosted
    one. The daemon finds its lead key by the row's root and reply-id
    (`AgentKeyStore.LeadSession`), keeps that key (no mint), and resumes
    the conversation its key record names on the next input or mail:
    Claude resumes with `--resume <harness_session>` in `harness_cwd`.
    The record preserves the working directory for later starts. Takeover
    refuses with 409 while the terminal harness may run. It requires an
    eligible terminal harness, a lead key with a recorded conversation,
    and a working directory that still exists.
    A ws-mcp that outlived the session's harness re-reads the row under
    the lock before each of its row writes and stops writing once the row
    is hosted.
  - `POST /agents/<id>/handoff` **(Deprecated — scheduled for removal in
    `261124-refactor-remove-terminal-handoff`)** hands a hosted session to
    the terminal: the process ends, the row becomes an ended terminal row
    (`hosted`, `host_pid`, `harness_pid` cleared, `ended_at` set), the record
    is marked `handed_off_at` as in an implicit hand-off, and the state
    carries `resume_command` (`cd <dir> && claude --resume <id>` for Claude),
    which is answered to a viewer with `can_control` only. It is refused
    (409) for a session with no harness conversation yet, and fails when the
    process could not be ended. The session's page carries `data-handed`
    while the row is an ended terminal row and the record is handed off, and
    then reads the command from `GET /agents/<id>`. UI entry points are
    hidden; `POST /agents/<id>/handoff`, `Runner.HandOff`,
    `terminalSession.terminalResume`, and `State.ResumeCommand` remain
    working for T0 compatibility until Phase 2 removal. **New harnesses do
    not need to implement terminal handoff (`terminalResume`).** Implicit
    terminal re-login (`conv.release()`, `handed_off_at`, `StatusHandedOff`)
    and reverse `POST /agents/<id>/takeover` (`TakeOver`,
    `checkTerminalSession`, `adoptTerminalSession`) remain active and are not
    deprecated.
  - `POST /agents/<id>/reopen` opens a closed hosted session again under
    its key, conversation, and settings. It is refused (409) once the
    session's row is a terminal's, and for a handed-off record; such a
    session comes back through takeover.
- **Input.** Every input is the daemon's. A composer message is held in the
  inbox and submitted at the turn's end; a steered one is written to the
  running process at once (a mid-turn user message in Claude). The daemon
  watches the lead key's mailbox (`LeadMail`, in
  `internal/mcp/lead_mail.go`) and claims what arrives: during a turn it is
  steered into the running process like a composer steer, else it goes into
  the record and is submitted as a turn whose text is what the mailbox wait
  hands a session; the session arms no wait of its own. A turn no send started (a background
  task's result) and a compaction's replayed assistant line are handled as
  the stream reports them. A page command entry splits the drain: the
  messages before it go as one turn (with the claimed mail), the command
  runs alone as the next turn. The text drained after it waits in the record
  (`queued`) for later turns, across a daemon restart. Close or hand-off drops
  it. Interrupt keeps it. Claude supplies `slash_commands` at init.
- **Turns.** With `CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1` the CLI reports
  `session_state_changed`; once one is seen, only `idle` ends a turn and
  releases held sends (a `result` does not: background work may still run).
  Without those lines a `result` ends the turn.
- **Approvals.** Claude runs with `--permission-prompt-tool stdio`. A
  `can_use_tool` request becomes a pending approval in the state
  (`approvals[]`, keyed by request id; several may wait, a subagent's with
  `agent_id`), shown on the page as a card, and is answered by `POST
  /agents/<id>/approvals/<request-id> {behavior: "allow"|"deny", message?}`
  once (a later or unknown answer is 409). `AskUserQuestion`,
  `ExitPlanMode`, and any request that needs user interaction are denied at
  once with a message telling the model to ask in chat. A harness that
  reports `requires_action` with no prompt pending for two seconds shows
  `action_needed` in the state. An answered id is kept in the record, so a
  restart does not ask again.
- **Settings.** `POST /agents/<id>/settings {model?, effort?,
  permission_mode?}` applies in-stream where the harness has a control
  (Claude: `set_model`, `apply_flag_settings` for effort,
  `set_permission_mode`) and keeps
  the process; a restart on the same backend session is the last resort.
  The record holds the current values, and every resume launches with them.
  Native permission modes: `auto` (the default), `default`, `acceptEdits`,
  `plan`, `dontAsk`, `bypassPermissions`. The legacy
  spawn and settings URLs reject `permission_level` or
  `permission_full_ack` with 400, including explicit nulls. On a
  common-aware backend a native `permission_mode` change that raises the
  common level is 403 unless it is the local, own-Origin user's request;
  the public `Runner.SetPermissionMode` cannot grant that authority. An
  explicit selection cannot be raised to `full` through the native-only
  URL, which carries no full acknowledgement
  (`internal/wsagent/http.go:202-207,273-284`,
  `internal/wsagent/runner.go:1004-1020`,
  `internal/wsagent/permission.go:214-242`).
- **Common permissions.** `POST /agents/leads/permission {root, harness,
  permission_level, permission_full_ack?, model?, effort?, plugin_isolation?}`
  spawns a lead (201 with its state; `plugin_isolation` has the same choices
  and default as `/agents/leads`); `POST /agents/<id>/settings/permission
  {permission_level, permission_full_ack?, model?, effort?}` changes one
  (200 with its state). Both take the trusted gate, require a level from
  `read-only`, `workdir-write`, `full`, and require
  `permission_full_ack: true` for `full`. Their strict JSON objects reject
  unknown or null fields (including `permission_mode`) with 400; unsupported
  selections and worker selections are also 400. Only the trusted route
  supplies user authority; an acknowledgement alone grants none
  (`internal/wsagent/http.go:94-106,158-177,183-207,249-271,364-398`,
  `internal/wsagent/permission.go:78-96,180-210`). These are lead-only intent,
  not sandbox guarantees or worker capability changes: Claude maps the
  levels to `plan`, `acceptEdits`, `bypassPermissions` respectively. Lead state
  includes `permission_level`,
  `permission_source` (`explicit` from `permission.json`, else
  `legacy-derived`), and `permission_enforcement`: `partial` only for
  Claude `read-only` while a running harness reports `plan`, `advisory`
  otherwise. Worker state omits these fields. Without an explicit
  selection, Claude `plan` derives `read-only`, `bypassPermissions` derives
  `full`, and its other admitted modes derive `workdir-write`
  (`internal/wsagent/permission.go:53-75,98-109`).
- **Recovery and failures.** A Claude lead's init line lists its MCP
  servers. A server reported `failed`, a failed `mcp__*` tool call, or the
  lead's call of a `*__request_mcp_reconnect` tool makes the runner ask the
  harness in-stream, off the loop: `mcp_status` for the failed servers
  when none is named, then `mcp_reconnect {serverName}` and `mcp_status`
  for each. A server back as `connected` restarts nothing. One that is not
  is listed in the state's `mcp_broken` (and the snapshot's
  `host.mcpBroken`), and the process restarts on the same backend session
  at the first idle point with no background task (`stop` then a
  `--resume` start). A server an automatic restart was for that fails again
  without having connected restarts nothing more until it connects (no
  restart loop). `POST /agents/<id>/restart` restarts by hand. A typed failure on an
  assistant line (`error`: `authentication_failed`, `model_not_found`,
  `rate_limit`; `api_error: usage_limit_reached`, with the reset time from
  the refusal's or the last rejected `rate_limit_event`'s `resetsAt`) is a
  `notice` ring line and the state's `failure` (snapshot `host.failure`)
  until a turn ends with no failure; an `api_retry` line is the snapshot's
  `host.retry` until output resumes or the turn ends; a compaction that
  failed carries its `compact_error` on the compaction line. The
  background task count (`background_tasks_changed`, subagents included)
  is in the state and `host.backgroundTasks`; a subagent the lead's Agent
  call started takes its effort from an `effort-<level>` subagent type and
  its model and context size from its own assistant lines. Idle stop is
  the profile's `idle_timeout_seconds`: absent defaults to 0 (off) for leads
  and 600 seconds for workers; an explicit 0 disables it. Idle stop waits for
  no running turn or pending input, a zero reported background task count,
  and no active spawned child (including a child's background work or held
  report).
- **Streaming.** A hosted Claude lead starts with
  `--include-partial-messages`. The `stream_event` mapper accumulates text
  deltas into the reply so far, emitting at most once per 100 ms within a
  block; `message_start` and `content_block_start` reset the partial text
  and throttle. The whole assistant text block supplies the final reply.
  Subagent stream events and non-text deltas supply no partial reply.
  The lead's holder uses `--ring-skip` to keep matching tool-input,
  thinking, and signature delta lines out of its ring and client stream.
  It skips only whole stdout lines that fit one record and match from the
  line's start; split or unmatched lines stay. Skipped lines consume no
  sequence number. The daemon sets the holder manager's `AuditChannel` to
  offer the optional `audit` field on attach. Capable Claude lead holders
  with `--ring-skip` accept it. Older holders ignore the offer and attach
  as before. On a negotiated connection the holder tees each skipped line
  as an unsequenced `audit` event anchored on the last kept stdout sequence
  and fences every kept run (`audit_fence`); lines skipped while no daemon
  is attached, or past the audit queue, are only counted in the fence. The
  daemon's audit capture (`wsagent/audit_capture.go`) orders tee lines
  before the kept lines a fence releases and never blocks the holder reader
  or the stream mapper. When the audit store opens, the daemon binds it as
  the runner's `AuditSink` before hosted Claude, Codex, and Antigravity sessions can
  start or reattach. If the store is unavailable, hosted sessions still run.
  The Claude adapter writes `meta.request = {id, prev_id, ctx}` on every
  event from a model request (`wsaudit.RequestChain`): one chain for the lead and one per
  native subagent (`parent_tool_use_id`), advancing once per assistant
  message ID, text-only messages included; `ctx` is input + cache read +
  cache creation tokens and is absent without usage. Codex streams carry
  usage but no request ID, so their events omit it. The Antigravity adapter
  caps an assembled thought at 1 MiB of decoded text: past the cap it drops
  the body and records one bodyless `thinking` event with `incomplete` and
  `loss_reason: capture_cap`, then ignores chunks until the next thought
  boundary. On reattachment it recovers the latest retained manual
  compaction cut for that holder (the `inferred.compaction.<boundary>.<counter>`
  source key) and resets the usage baseline at that position, so the replay
  does not record a second cut.
- **Access.** Spawn, settings, stop, restart, close, and an approval answer take the
  trusted gate (`wsdashboard.TrustedWrite`): 404 to any viewer that is not a
  browser on this machine, 403 without the daemon's own `Origin`. A LAN
  viewer reads the session and may send messages and interrupt, but not slash
  commands: a command entry, or a message whose text starts with `/`, is 403
  to it; the state's `can_control` tells the page which viewer it serves.
- **Page.** The rail's new-session "+" (right after a worktree row's label,
  quiet until the row is hovered like the other rail "+" buttons; worktree
  rows only, a plain folder's bar has none, since the spawn route takes only
  a Git worktree root) opens the spawn popover, with model and effort choices from
  `GET /support/choices`. A hosted session's page reads `GET /agents/<id>`
  on load, on a head swap, when the live region's permission line changes,
  and when its menu opens (no timer): approval cards, the `action_needed`
  notice, and the harness chip's menu (model, permission mode, effort,
  stop, restart while `mcp_broken` is non-empty, close; stop, restart, and
  close confirm naming `background_tasks` when it is above zero) come from
  that state. From the snapshot's `host` block the page draws an error ring
  on the head's harness chip and the rail row plus a failure chip while
  `failure` is set, an MCP-broken chip, a retry line in the live region,
  and the background task count in the agents popover and beside the
  composer's subagent badge; `notice` ring lines and a failed compaction's
  `text` render as failure lines in the conversation. The rail and index mark a hosted row and show its waiting approval
  from the live snapshot, which names the oldest pending prompt only. The
  local-viewer controls are hidden by CSS unless `<html>` carries
  `data-sysopen`; the routes refuse regardless. A tab asks for notification
  permission on Start or a composer send and raises one notification when a
  session starts waiting (a toast without permission, which is every
  plain-http LAN tab).

A tracker store's support conversation is an ordinary runner conversation
on a support profile whose working root is the store's registry `Dir`; the
daemon keeps one per store under `/support` (`internal/wsagent/support.go`,
mounted when `cmd/ws-mcp/dashboard.go` passes it, with store roots read from
the daemon's own tracker host). Routes, behind the same guard and POST rule
as `/agents`, an unknown store 404: `GET /support/choices` (`{harnesses:
[{harness, profile, available, error?, models: [{id, thinking?}],
efforts, lead?, permission_modes?, permission_levels?}], last}`: optional
`lead` is a bool marking admission as a hosted lead, optional
`permission_modes` is that harness's native modes, and optional
`permission_levels` is its supported common lead spawn/settings selections,
not worker support; these fields belong to each harness entry. Levels are
listed in `read-only`, `workdir-write`, `full` order, restricted to the
registration's supported choices, and omitted when none are supported
(`internal/wsagent/support.go:216-230,422-430,449-450`). Support
profiles' backends resolve through the global and builtin layers, with
duplicate backends listed once; registered lead-capable backends not already
listed are appended in backend-name order, with an empty `profile`.
`BackendRegistration` is the runtime-only descriptor pairing a backend
implementation with its optional `LeadProfile`, `PermissionModes`, and
`PermissionLevels` (`internal/wsagent/registration.go:10-20`); process
controls remain instance-owned and choice listing comes from the
backend's `ChoiceLister` implementation. A harness is unavailable when the
daemon lacks the backend or, for a `ChoiceLister`, its executable is not on
PATH; Claude's models are the CLI's
version-less aliases `opus`, `sonnet`, `haiku`, `fable` and its efforts the
Claude levels, Codex's models and effort choices come from `model/list` on
its last opened app-server process while that report is younger than the
five-minute choices TTL (a newer report replaces it), else from the CLI's
`models_cache.json` (read once per TTL window with no account probe on a
dashboard read: a missing file is an empty list, an unreadable, empty, or
model-less one an error on the 30 s failure TTL). A listing failure appears
under that harness's `error`. The support routes also include `GET
/support/stores/<store>` (`{store, conversation: State|null, selection}`),
`POST .../send {text}` (opens the conversation with the current selection
first when none is open), `POST .../new` (closes the open one and opens a
fresh one; sends nothing), and `POST .../select {harness?, model?,
effort?}` (`{conversation, selection}`: saves the machine-wide last choice;
a harness change replaces the open conversation with one on that harness's
profile, resetting model and effort unless given; a model or effort change
goes through the runner's settings, from the next message; with no open
conversation it only saves). Live support selection (`last`) and per-store
conversation mappings (`stores`) are stored in `metadata.sqlite`
(`support_state` and `support_mappings`, loaded after the daemon acquires
`dashboard.lock`, importing legacy `<state dir>/support.json` once at cutover),
so a store keeps
the same conversation across restarts; with no saved choice, or one whose
harness is unavailable, the selection is the first available harness with
the profile's defaults. A saved legacy Pi choice falls back with a retirement
notice.

The tracker gate admits a support conversation's key like a top-level
lead when the daemon holds an open agent record for the key's reply-id that
names this key at the key's root, and that record's profile, resolved at
call time, names the tool. Its writes are pinned to the store whose
canonical registry `Dir` equals that root (any other `store` is refused),
and the store commands (`tk:register`, `tk:sync`, `tk:unregister`,
`tk:relabel`, `tk:hide`, `tk:unhide`) stay refused, since the conversation
runs in its store's clone; every other spawned key is
refused as before. `tracker.show {item}` is that conversation's card call:
an item of its own store answers `card <store> <alias>` plus the mini-card
fields, which the chat draws as a card; another store's item is answered as
text; a lead gets a refusal. `tracker.ui {op, ...}` is its dashboard UI
command: `search {query}`, `focus {item, comment?}`, `scope {scope}`, or
`view {mode}`, checked first (an item of its own store, a comment on that
item's thread, the scope and mode keys of `wslive.UIScopes` /
`UIViewModes`, a query `wstracker.ParseQuery` accepts), answering `ui <op> <arg>`; the runner writes that as a `ui`
ring entry, and the store chat acts on it once, only when it arrives live
after the stream's first (history) entries region, so a reload or reconnect
draws it as an inert line. A narrow-layout search, scope, or view switch
closes the chat's stream, so a later command in that reply is lost; the
prompt and the tool's answer ask for at most one per reply. `git.search` runs `log`, `show`, `grep`,
`blame`, or `ls-files` read-only (a per-subcommand flag allowlist, no git
global options, external diff and textconv off, no pager, a 20 s timeout and
64 KiB of output) in a repository at or under an allowed root: the project
roots of non-agent dashboard registrations seen within the registry
retention, plus `agent.git_search_roots` (a global-only path list that needs
lead authority); without `repo` it lists the roots.

Before a worktree read, `git.search` checks the protected credential files,
including any retiring generation, for extra hard links and for a credential
filename that is itself a symbolic link or Windows reparse point. Worktree reads
include `grep` outside the index or a revision, all `blame`, `ls-files`
modes and format fields that inspect the worktree, and `log` or `show`
with mailmap enabled or a mailmap-aware format. Index and revision reads
that do not inspect the worktree continue normally. If a protected file has
an extra link, the tool answers:

> git.search: worktree search refused because a provider credential file has
> an extra hard link; remove the extra link while keeping the store's file,
> then retry

A linked credential filename is refused with:

> git.search: worktree search refused because a provider credential filename
> is a symbolic link or reparse point; restore a regular credential file,
> then retry

The credential store itself still loads through a linked filename. The
loader and the file-tool guards resolve it the same way, so both the link
path and the resolved target's identity are protected.

Pages render in one language end to end, chosen by the server from the first
byte: the `ws_dashboard_lang` cookie when it names an available language (the
gear menu's language `<select>` writes it; no cookie, or the empty choice,
follows the browser), else the best `Accept-Language` match, else English. A
language is one table file, `internal/wsi18n/tables/<code>.json`, embedded in
the binary: the file name is the code (matched case-insensitively), its
`lang.name` key is the name the menu shows, and a new file adds the language
with no code change. English is the fallback and the key set: another table
may hold only keys English has, and `internal/wsi18n`'s key sweep fails on a
key a source names that English lacks or an English key nothing uses. Keys
that start with `js.` ship to the page scripts as the page's own table.

The theme works the same way: the gear menu's theme buttons write the
`ws_dashboard_theme` cookie (`light` or `dark`; auto deletes it and follows
the OS), and the server renders the page root's `data-theme` from it on the
first byte, on every page. A topic body's frame is a separate document, so
the daemon pins the same `data-theme` on each frame document's root, on the
session page and its SSE stream; a switch needs no reload: the page sets its
root, posts the theme to every body frame (and again to a frame that reports
its height, which covers a frame swapped in after the switch), and redraws
drawn mermaid diagrams, in the page and in the frames, in the new palette.

By default the daemon binds loopback and every route requires a loopback
Host. Browser-facing routes additionally require the localhost HttpOnly,
SameSite=Strict cookie, derived from `localhost-secret` and kept for up to 400
days. Cookieless navigations show the self-contained login gate; fetches and
WebSocket handshakes answer `401 {"error":"login_required"}`. Explicit gate
copying asks the daemon to place a fresh single-use ten-minute link in the OS
clipboard, then the page reads it once and automatically logs in with the
link. The browser may prompt for clipboard permission; if the read is unavailable
or refused, or returns no login link, the copied hint stays visible for manual
paste. This route accepts only a loopback peer and Host with its own Origin and
same-origin fetch metadata when present; it is rate-limited and returns no
credential. Unavailable native writers hide the
action, while runtime write failures allow retry. Nothing reads or writes the
clipboard on page load. Settings and the desktop Connection menu provide
additional explicit login-link paths. Tauri requests the browser cookie through
the native endpoint bridge (`agents-plugin-tool/desktop/src/endpoint.rs`,
`browser_cookie`) and sets it before navigation
(`agents-plugin-tool/desktop/src/shell.rs`, `attach`).
A same-origin status probe recovers a Strict-cookie browser
that followed a cross-site link, retaining the requested fragment.

LAN mode (`dashboard.lan_enabled`, global-only, passed as `dashboard --lan`)
binds every interface. Local peer plus Host still requires the localhost cookie;
a non-local browser needs the separate LAN token. Neither credential grants
access through the other branch, and a LAN token never grants trusted writes.
The LAN token arrives as `?token=` on the Settings addresses shown only to a
local viewer and becomes its existing HttpOnly cookie. Machine routes retain
their own checks without requiring the browser cookie: `/health`, `/mcp`,
`/shutdown`, `/shutdown/all`, `/nudge`, `/session-hire/roster`, and
`/agents/by-token/<op>`.
Nudge and roster clients now send the endpoint bearer, but enforcement is deferred
to preserve older clients. GET/HEAD public frame assets, path-capability routes
and `/pwa/*` also remain cookieless. Every other route is gated by default.
`/health` reports the mode as `lan`, and the daemon re-reads the global value
when a session connects: a changed mode replaces it through the drain path below.

At an equal numeric core, ordinary connection keeps the healthy daemon
regardless of version suffix, build time or content hash. `/health` retains
`build_time` and `content_hash` as identity, not automatic swap authority.
`daemon-start --replace` explicitly drains an equal-core daemon once, including
identical content; a newer numeric core stays protected against downgrade.
The existing LAN-mode change still drains an equal-core daemon. In local
development, `python3 scripts/dev.py rerun daemon` owns explicit build+swap
and passes `--replace` with `--build-time` to its newly built staged binary.

A daemon asked to stop (a replacement, or `/shutdown`) drains: it closes its
listener at once, so the successor can take the lock and the port, refuses
new sessions, lets the MCP calls in flight finish for up to the lock wait
minus 1.8 s (`proxyDrainBudget`), cancels long waits (a `mailbox wait`) at
once, then closes its session streams; a git commit or merge in flight is
not cut midway, and the sessions' launchers reconnect to the successor.
The session holders outlive that stop and attach to the next daemon.

`POST /shutdown/all` is the desktop app's Full Quit. It takes the same
endpoint bearer as `/shutdown`. The daemon fences holder launches and
durable starts, wakes, and reopens. An admitted start finishes before the
stop pass. The daemon rescans the holders directory, stops holders in
parallel via the protocol or a start-time-guarded kill, and closes the
open durable sessions, including sleeping ones, through normal close.

Full Quit counts holder processes and durable sessions separately. It
answers `202 {"shutting_down": true, "stopped": [...], "remaining": []}`
and drains when both sets are clear. It can answer `409` with no OS holder
left if a durable session remains open. On `409` the daemon reports the
survivors and reasons in `remaining`, stays running, and reopens admission.
Durable IDs use `durable.<reply-id>` in `stopped`, `remaining`, and
`GET /holders`. A daemon already stopping answers
`503 {"error": "shutting_down"}`. An older daemon can answer `401` with
`login_required` or `404` for an unsupported route. The route itself uses
`401 unauthorized` for a bad bearer. A still-running MCP client can start
a fresh daemon through its launcher's normal recovery.

Dev assets: with `WS_MCP_DASHBOARD_DEV_SOURCE=<repo>/agents-plugin-tool` in the
environment of `ws-mcp dashboard`, the daemon serves the dashboard and
whiteboard page assets (`internal/wsdashboard/assets`,
`internal/wswhiteboard/assets`: templates, CSS, JS, sprites) from that working
tree instead of its embedded copies, re-reading and re-parsing them on every
request, so an edit shows on a browser reload and only Go changes need a
rebuild. Off when unset; a root without those directories fails the daemon at
startup, and a file that resolves outside the root (a symlink) is refused. The
vendored Mermaid bundle stays embedded. A running daemon keeps its mode, so shut it down first
(`POST /shutdown` above), then start
`WS_MCP_DASHBOARD_DEV_SOURCE=$PWD/agents-plugin-tool ws-mcp dashboard`; a
half-edited template answers 500 (the error is in `dashboard.log`) and the next
request retries.

An isolated probe daemon needs a disposable `HOME`, scratch `WS_CACHE_HOME`
(state directory, session keys, mailboxes) and `WS_CONFIG_HOME` (global config,
workspace list), and its own `-port`. `WS_CONFIG_HOME` directs the rail's
workspace routes away from the default `~/.ws/dashboard/workspaces.json`.
Unset `CLAUDE_CONFIG_DIR`, `CODEX_HOME`, `PI_CODING_AGENT_DIR`, all `XDG_*`
overrides, and `ZDOTDIR` so the daemon and its children use the scratch home.
Hosted harnesses use the probe daemon's embedded packages and retained restart
record. Give their launchers the same isolated environment so they restart
only the probe daemon. External harness/plugin tests retain their own runtime
path. Use an explicit scratch bootstrap binary for those.

The repository's `scripts/{board,rail,palette,webcard}-headless.mjs` capture
helpers start their daemons with scratch home, XDG, and ws state directories.
Their launch environment allows Git for the fixture repositories and sets
`WS_MCP_DASHBOARD_SCRATCH_CAPTURE=1`. The daemon then serves empty usage with
no provider lookup and offers only the scratch home in its folder picker.
The Go build and Playwright still use the runner's toolchain and browser
caches. Generate the embedded package assets before running the captures with
`python3 agents-plugin-tool/scripts/generate-package-assets.py` from the repo
root.

For a board capture, pass the URL and `--endpoint FILE` printed by
`node scripts/board-headless.mjs serve` to the scenario command so the
browser can authenticate to that scratch daemon. For standalone captures,
pass an existing HTML fixture to `--url`.

Profiling: with `WS_MCP_DASHBOARD_PPROF=1` (also `true`/`yes`/`on`) in the
environment of `ws-mcp dashboard`, the daemon serves `net/http/pprof` under
`/debug/pprof/` to loopback viewers only; a LAN-mode request carrying the LAN
token is refused (403), and with the variable unset the routes do not exist
(404). The daemon the launcher starts inherits the launcher's environment
(the daemon-start step runs under it), so the simplest route is the same
restart as dev assets: `POST /shutdown`, then
`WS_MCP_DASHBOARD_PPROF=1 ws-mcp dashboard --port 29417`.
The pprof routes sit behind the default localhost cookie gate
(`localhost_auth.go`), so a bare `curl` or
`go tool pprof http://127.0.0.1:29417/...` gets
`401 {"error":"login_required"}`. Get the cookie from `POST /auth/cookie`.
Send the bearer token from `<cache root>/dashboard/endpoint.json` and no
`Origin` header; the route refuses any request that has one. Download the
profile with that cookie, then read the file:

```sh
EP=<cache root>/dashboard/endpoint.json
PORT=$(jq -r .port "$EP"); TOKEN=$(jq -r .token "$EP")
COOKIE=$(curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  "http://127.0.0.1:$PORT/auth/cookie" | jq -r .value)
curl -s -H "Cookie: ws_dashboard_localhost=$COOKIE" -o cpu.pb.gz \
  "http://127.0.0.1:$PORT/debug/pprof/profile?seconds=30"
go tool pprof -top cpu.pb.gz
curl -s -H "Cookie: ws_dashboard_localhost=$COOKIE" \
  "http://127.0.0.1:$PORT/debug/pprof/streams"
```

The same cookie works for `heap`, `allocs`, `goroutine` and the other
profiles. The answer's `maxAge` gives the cookie's lifetime; never log
the cookie or the token.
`/debug/pprof/streams` answers the live-update counters since the daemon
started as JSON: wakes by source, signals, renders, `nudges`,
`session_reads_changed` and `session_reads_unchanged` (re-reads whose
fingerprint did or did not change), `tracker_reloads`,
`tracker_late_reloads`, and `tracker_late_reloads_changed`, and each parse
cache's `<kind>_cache_hits` and `<kind>_cache_misses` (a miss reads the
file): `snapshot` (the daemon's pages), `source_snapshot` (session
re-reads), `record`, `reply_registry`, and `scope_store`.

The same route includes `file_io_total`, per-operation `file_io_<operation>`,
and `file_io_site:<package>/<file>:<line>:<operation>` counters for instrumented
hot identity, inbox, permission, evidence, snapshot and folder-store paths.
These count attempted OS API operations, including failures and retries, not
estimated costs or every kernel syscall. `read_file` includes its internal
open/read; `create_temp` includes temp creation, and `atomic_replace` counts
actual replacement attempts. Third-party flock and directory-entry `Info`
operations are not included. Counts are process-lifetime and never reset by
reading the route; compare two samples over the same observation interval.

### Daemon replacement and process ownership

For a graceful replace, the process owner determines whether an in-flight turn
continues (`internal/wsagent/runner_events.go` L804-818).

| Child kind | Owner | Survives graceful replace? | What to do |
|---|---|---|---|
| Hosted Claude, agy, or Codex lead | Session holder (`internal/wsagent/runner_process.go` L229-258, L294-312) | Yes, while holder and child survive | Replace; check rejoin if progress stalls. |
| Durable werk lead or worker | Daemon (`internal/wsagent/runner_durable.go`) | The loop stops and the successor recovers the conversation and its recoverable turn from the journal | Replace to keep durable sessions open. Use Full Quit to close them. |
| Claude, Codex, or agy-parented agy `agent.spawn` worker | Session holder (`internal/wsagent/runner_process.go`, `startWorker`) | Yes inside a turn, while holder and child survive; a finished, idle worker is put to sleep instead (see below) | Replace; check rejoin if progress stalls. A sleeping worker wakes on its next message. |
| Legacy direct worker still running under an older daemon | Daemon subprocess | No; shutdown stops it | Send a new message after replace; its next launch uses a holder. |

Healthy held leads and workers can continue through a normal daemon replacement.
A successor validates the worker holder's kind, backend, reply id, cwd,
capability hash, and harness PID before restoring its delegation capability
and HTTP-MCP tuple. The tuple keeps the original token, URL, environment, and
holder binding: an already-running worker cannot learn a rotated token.
Attach uses the child's persisted authority; current-parent admission is checked
after records load and governs its next launch. Replay rebuilds turn state,
deduplicates answered controls, and auto-allows remaining requests for backends
whose worker policy enables it.

New workers use the `agent-worker` holder kind and a `handed_off_at` fence.
An older daemon sees that child as ended, starts no worker, leaves its record
unchanged, and does not recognize its holder kind. This surviving-child-row
behavior is T1. The complete downgrade surface follows owner decision (1855)=a:
T2 with the explicit exception that a downgrade beyond the 7-day
`RegistryRetention` may irrecoverably delete the child's session history.
An old daemon may also release and rewrite a held conversation parent's turn.
Live legacy `pi-worker` holders keep running while their records open as ended
history.

Closing a werk conversation records the intentional turn close before
cancelling its execution and discards staged inbox claims at that boundary.
Reopen can accept a new turn in the same conversation. A normal replacement
leaves the recovery state for the next daemon.

On re-upgrade, a fenced open child of a settled conversation parent is silently
retired only when its nonzero start strictly predates the parent's terminal
settlement stamp, the parent's sends do not exceed its last turn's mark, and
its held mail and queued input are empty. Invalid, equal, or same-second
stamps adopt the child; the parent timestamp has second precision. Retirement
clears the active turn and holder fence in one record update and leaves a
wakeable sleeping child, preserving its last result and sending no second
parent report. A lead parent instead restores its held turn, adopts the child,
and receives one late report. An old-pruned child leaves an unowned holder for
the successor's orphan sweep.

A Claude `agent.spawn` worker is launched with session-state lines on and
reads its harness's background-task list (`StartSpec.SessionState`), as a lead
does: a worker whose native background task (for example a Bash
`run_in_background`) still runs is not idle, so neither the idle timer nor the
stale-route restart (`restartIfDue`) stops it, and its done turn is held until
the task ends and any wake-up turn it brings settles. A worker started before
this change has no such lines and keeps the old behaviour until its next launch.

A spawned non-lead worker that has finished its turn and is idle (no open or
held turn, no queued input, mail or reports, no approvals, compaction, quota
or model-wait continuation, no held children or unsettled sends; a Codex
worker's unknown background count does not keep it awake) does not survive
a replacement as a running process. At shutdown its holder is detached and
asked to stop without waiting, so the successor's lock budget is not spent,
and the record drops its holder fields. A worker the successor rejoins and
finds finished and idle after replay is put to sleep the same way, instead
of the stale-route restart (`replayDone`, `finishedIdleChild` in
`internal/wsagent/runner_events.go`). Either way the successor runs no
standalone restart-restore turn for it; the restore bundle is prepended to
the input of its next wake. Leads and workers still inside a turn keep the
detach-and-rejoin behaviour above. This keeps an idle worker's process (and
its working directory) from outliving its usefulness in a worktree that is
about to be released or removed.

Interrupt remains turn-scoped. Idle timeout, explicit stop, close, and expiry
end the holder and revoke its tokens. A legacy direct worker's open turn
settles `interrupted` once before queued input starts its next held process.
Output lost from the holder's ring uses existing backend recovery. A backend
that cannot reconcile the lost turn reports failure honestly. Re-arming agy's
model-wait watchdog across replacement remains outside this contract, matching
held leads.
A bearer the successor has not registered yet (its startup holder scan is
still restoring the surviving workers') waits for that rejoin, up to 8 s
(`mcpRejoinWait`), then resolves; past the rejoin an unknown bearer is a 401 at
once, so a hard 401 never reaches a restored worker in that window.
MCP calls during drain may receive 503. Windows survival requires a
restart-safe holder; one still inside its starter's job object may end with it.

After rolling back the daemon, terminal sessions refresh their tool list.

#### Metadata lock/drain barrier and downgrade residuals

During daemon replacement, the predecessor stops `/meta/*` admission, drains
in-flight HTTP/MCP/proxy calls, polling, agent runner shutdown, and tracker
hook/watch/poll goroutines (`BeforeUnlock`), and checkpoints and closes
`metadata.sqlite` strictly before removing `endpoint.json` and releasing
`dashboard.lock`. Every successor — including a prestarted successor and its
tracker `Support` controller — acquires `dashboard.lock` before opening
`metadata.sqlite`, running cutover/snapshot load, or serving requests.

Under owner compatibility exception `(2125)=a` (2026-11-22), running a
pre-cutover binary against an already-cutover state directory (same-root
downgrade) has the following disclosed residuals:

- **No T2 recovery for old-side bundled edits:** once the cutover completion
  marker commits, re-upgrade always restores from `metadata.sqlite` and ignores
  edits, additions, or deletions made to legacy bundled files while downgraded.
- **Mixed-version prune residual:** an older `ws-mcp` binary pruning `sessions/`
  consults stale on-disk `agent.json` and can delete an aged session directory
  that was revived in SQLite after cutover.
- **Stale holder replay or orphaning:** an older daemon reading stale holder
  identity or `holder_seq` from frozen `agent.json` can replay or orphan a
  holder.
- **Pending hire approval journal replay and nested CLI toggle:** an older
  daemon can replay pending entries from the separate `hire-proposals.json`
  journal (the hiring gate still applies), and can accept an owner's trusted
  toggle to re-enable hiring on a CLI lead nested after cutover because the
  frozen `rail-layout.json` still lists it as a root (`(2137)=b`).
- **Post-downgrade support records:** a support conversation newly created by a
  downgraded older binary stays open on re-upgrade because the one-time `(2136)`
  support fence already ran, so stale authenticated tracker writes from its
  retained key remain admissible against a downgraded binary.
- **Fenced open children during downgrade:** an older daemon cannot launch a
  fresh process for a child that was open at cutover because its legacy
  `profile` was fenced to `!cutover-fenced`; surviving holders can still
  reattach (and held Pi workers keep their stored resolved definition), tracker
  writes stay refused because the gate resolves profile on every call, old
  prune keeps their directories because `closed_at` stays empty, and re-upgrade
  restores their original profile and open state from SQLite.

Minor floor (`<daemon dir>/daemon-minor-floor`): a plain text file beside
`dashboard.lock` and `last-port` holding one numeric `major.minor` (storage
type 5 in `performance.md`, read raw by the Python launchers). The lock-owning
daemon advances it atomically only to a higher minor, after startup
initialization and before writing `endpoint.json` (no write when equal). An
absent file is legacy state and admits any candidate; an unreadable or
malformed existing file is a startup error, never an admit. Cold starts and
replacements enforce it at three points, each refusing a lower minor with the
"update this plugin" error shape: Go `Ensure` (before requesting any drain, so
a refused successor never stops a running daemon, passing its `--start-intent`
of `absence`, `upgrade`, or `replace` to the child), Go `StartDaemon` (under
the singleton lock, before side effects, using that same intent), and the
Python launchers (after candidate selection and before staging, including the
hosted launcher's `last-running-executable.json` restart). Live newer daemons
are still protected by `keepRunning`. The floor also applies when no healthy
daemon answers.

To roll back from 0.52 to a matched v0.51.2 plugin and daemon:

1. End sessions that use the 0.52 plugin so their launchers stop reconnecting,
   and stop the newer daemon. The desktop app's Full Quit also closes hosted
   sessions.
2. Install v0.51.2 in each affected harness and start a fresh harness session
   with that plugin. With the daemon stopped, the v0.51.2 plugin launcher
   cold-starts its own verified v0.51.2 binary.
3. Verify the daemon reports v0.51.2 at `/health` before resuming work.

The v0.51.2 pair predates the floor. It leaves the floor file in place and
starts with its normal arguments. For a floor-aware rollback candidate, use
that candidate's `daemon-start --below-floor` after stopping the newer
daemon. Its launcher admits and forwards the flag, and `StartDaemon` lowers
the floor under the singleton lock before publishing the endpoint.

Session holders: a holder is a small Python process (standard library only,
3.10 or newer) that owns one child process, over pipes or a PTY (ConPTY on
Windows), and outlives the daemon, so the child survives a daemon restart
or replace while the holder and child remain alive (Windows holders marked
not restart-safe have no such guarantee). Dashboard terminals also use holders;
see Daemon replacement and process ownership above for lead and worker ownership.
The script is embedded in `ws-mcp` (`internal/wsholder/holder.py`, whose
module docstring is the descriptor and protocol reference) and staged to
`<dashboard dir>/holders/bin/holder-<content hash>.py` before a holder
starts; a running holder keeps the file it started from, and at start the
daemon removes staged scripts that no live holder's descriptor names. Each
holder has its own directory `<dashboard dir>/holders/<holder id>/` with
`descriptor.json` (owner-only; PID, loopback port, attach token, protocol
version, start time (wall clock, plus boot-clock ticks on Linux), script
hash, restart-safe flag, and the consumer's
session description) and `holder.log`. The holder rewrites a deleted
descriptor within a minute, removes its directory when it exits, and exits
on its own 24 hours after the last detach once its child has exited, never
while the child lives.

The daemon scans the holders directory at start and every 3 minutes,
attaches to every holder over loopback TCP with the descriptor's token, and
replays the holder's sequenced output. A terminal holder keeps a fixed
4 MiB ring. A newly launched hosted lead or worker holder retains 32 MiB
and can grow to 1 GiB while output waits to be sent. A spawn may carry `http2_capture`: the holder then routes a pipe child's Go HTTP/2 debug stderr lines to a filter that keeps only framer summaries in a capture file, so the ring and the daemon's stderr tail never hold that trace; the holder is the file's only writer, and a `capture_note` operation adds the daemon's cutoff marker under the same cap (the script's docstring has the rules). A successful socket
send lets the holder reclaim the sent records above 32 MiB. At the ceiling
the oldest records are evicted. Running holders keep their launch settings
across daemon replacement. A reattach within one daemon process
continues after the last sequence it saw. A new daemon process replays the
whole ring. The newest authenticated attach takes over,
so a replaced daemon and its successor never both write to one child. A
descriptor whose process is gone is removed with its directory, and so is
one whose PID now belongs to another process (the endpoint does not answer
and the process under the PID did not start when the descriptor says). A
live holder stays unattached for one of three causes: its endpoint does not
answer, its protocol is neither the daemon's version nor the one before it,
or it refuses the token. `GET /holders` lists every holder with its state
and cause, the recent holder start failures, and whether the viewer may
stop one. It also lists open durable sessions as `durable.<reply-id>` with
`restart_safe` and an `attached` flag that reflects whether the private
record is available. The drawer's holders card shows the unattached holders, the ones
marked not restart-safe (a Windows holder that could not leave its
starter's job object), and the start failures, with a stop button on each
unattached one. `POST /holders/<id>/stop`
ends a holder and its child: through the protocol when attached, otherwise
by killing the holder's process group (POSIX) or process tree (Windows),
and only while the process under the PID is still the one the descriptor
records, which guards against a reused PID (409 with the reason otherwise):
on Linux the process's start in boot-clock ticks must equal the recorded
`start_ticks`, because the creation time Linux reports is derived from the
boot clock and moves with every wall-clock step; elsewhere the OS creation
time must be within 2 seconds of the recorded wall-clock start. The stop route answers only a browser on this
machine that sends the daemon's own Origin. For a durable ID, it uses the
conversation's close path. A LAN viewer sees the card
without the stop button. Closing the daemon detaches and ends no holder,
except a finished, idle spawned worker's, which it asks to stop (Daemon
replacement and process ownership).

The interpreter is `WS_MCP_HOLDER_PYTHON`, which the launcher sets to its
own interpreter when it starts the daemon (the detached daemon inherits
it); without it the daemon uses `python3` on PATH, and a holder that cannot
start is reported on the card. Holder tests: `go test ./internal/wsholder/`
runs real holders (skipped without Python 3.10 or newer on PATH), and
`python3 -m unittest discover -s agents-plugin-tool/internal/wsholder/tests`
runs the script's own tests.

## Plugin-Managed Startup

Codex plugin bundles can reference plugin-local MCP configuration:

```json
{
  "skills": "./skills/",
  "mcpServers": "./.mcp.json"
}
```

The plugin-local `.mcp.json` starts the runtime through the Python launcher:

```json
{
  "mcpServers": {
    "ws": {
      "command": "python3",
      "cwd": ".",
      "args": ["./bin/ws-mcp-launcher.py", "serve", "--stdio"],
      "startup_timeout_sec": 30,
      "tool_timeout_sec": 600
    }
  }
}
```

`cwd: "."` is intentional. Codex normalizes it to the installed plugin cache so
`./bin/ws-mcp-launcher.py` resolves inside the plugin package. Because that also
makes the MCP process cwd the plugin cache, tools must not infer the project
root from process cwd.

The launcher exports `WS_MCP_PROJECT_ROOT` when it can infer the caller project
root from the parent Codex process. Skills may still pass explicit absolute
roots for compatibility, and lead actor setup should pass the repository's
absolute filesystem path.

Native Windows plugin-managed startup uses the same Python launcher. Windows
users need a working `python3` command on `PATH`; if the Windows Store alias is
present without Python installed, install Python 3 and refresh the plugin.

## Daemon-Hosted MCP

Every product command uses the Python launcher. `serve --stdio` copies MCP
bytes to the cache root's dashboard daemon; other commands run in-process
handlers there through the authenticated CLI transport. CLI commands, including
mailbox stream and one-shot waits, have network-only authority on initial
connect and retry: they never repair, stage or start a runtime. An unavailable
daemon yields an explicit error/backoff. `version` can read
live health or content-bound build metadata locally. The only ws executable
launch is a needed daemon start/replacement, never a probe or fallback.
`WS_MCP_LAUNCHER_DIRECT` is obsolete and cannot restore an executable bypass.

Finding and starting the daemon (`DaemonLink` in the launcher):

1. The daemon directory is `<cache root>/dashboard`, the cache root being
   `WS_CACHE_HOME` (absolute, symlinks resolved when it exists) or
   `~/.cache/ws@kang-sw-devenv`, as `wsstate.CacheRoot` resolves it; the test
   `test_cache_root_and_staged_name_follow_the_go_rules` pins the parity.
2. `endpoint.json` names the port and token; `GET /health` answers the
   daemon's version, `build_time`, and `content_hash` (the sha256 of the
   daemon's executable). For an unreachable endpoint, the launcher probes
   Go's `dashboard.lock` non-blockingly to prove absence; it does not consult
   the recorded PID. Only a held or unqueryable lock gets the 20-second swap
   grace. A cold cache or a stale endpoint with a free lock starts without
   that wait. After starter coordination and a health recheck, a lock still
   held or unqueryable refuses absence-based startup; a connected port that
   never answers is diagnosed as hung. TCP handshake failure alone establishes
   only unreachability. A missing health route or version field falls back to
   the existing `/mcp` hello's
   returned version, not to executable inspection.
3. Ordinary startup starts an absent daemon or replaces an older numeric
   core using the selected runtime. Equal cores keep the healthy daemon,
   regardless of suffix, timestamp or hash (`keepRunning` in Go,
   `needs_start` in Python). Explicit `daemon-start --replace` bypasses the
   launcher's lifecycle keep shortcut and requests one equal-core swap;
   Go consumes that request before checking the successor.
   The launcher copies the runtime binary to
   `<daemon dir>/bin/ws-mcp-dashboard-<sha256[:16]><ext>` (the name
   `StageExecutable` produces) and runs `<staged> daemon-start --build-time
   <mtime ms>` with its own environment: Go resolves `dashboard.port` and
   `dashboard.lan_enabled` from config as `serve` did and runs the `Ensure`
   handoff, printing one JSON line (`outcome`, `port`, `pid`, `version`).
   A starter the dashboard does not apply to (no dashboard manifest, or
   `WS_MCP_NO_DASHBOARD=1`) does not decide the LAN mode, the same rule as
   for a hosted session below: a running daemon keeps its mode, and with
   none running the global file's `dashboard.lan_enabled` decides.
   Python launchers and `scripts/dev.py` acquire `launcher-start.lock` before
   invoking the Go starter and hold it through health confirmation or bounded
   failure. Waiting launchers poll health within the 75-second budget.
   Plain `daemon-start` leaves this lock to its caller, including same-minor
   launchers that predate `WS_MCP_LAUNCHER_START_LOCK_HELD=1`. When supplied,
   the Go starter consumes the marker before spawning a child.
   Direct `ws-mcp serve` and daemon-initiated LAN changes acquire the lock in
   Go. Desktop `daemon-start` joins when `--desktop-start` or
   `--require-independent-lifetime` is set and no ownership marker is present.
   Tauri invokes `daemon-start --desktop-start` through its native starter
   (`agents-plugin-tool/desktop/src/daemon.rs`, `launch`).
   On replacements through `Ensure`, the successor
   reports readiness before the starter requests an incumbent drain.
   Staging reuses matching hashes and failed copies clean only their own temp
   file. Settled artifacts remain: neither age nor singleton ownership proves
   process exit or accepted/rollback safety, and no retention index exists.
   The launcher then waits up to
   `DefaultStartWait` + 5 s (Windows
   + 10 s) for a healthy daemon and applies the replace rule to the one
   that answers: when that is still the older daemon (it refused to stop,
   or won the lock race), the adapter diagnoses the failed replacement and
   retries; no second server is launched.
4. Hosted stdio launches set `WS_MCP_HOSTED_PACKAGE=1`: they neither repair nor
   download a plugin runtime and do not wait for a local rsrc tree. Healthy
   daemons are followed without package-contract-driven replacement. After
   absence is proven and starter coordination is acquired, the launcher reads
   `last-running-executable.json`, validates its retained path/content hash,
   and starts that build. Each later restart rereads the record, so a launcher
   surviving replacement never pins its original build. A missing/corrupt record
   refuses restart. External plugin launchers keep their own runtime and
   repair path.
5. The current hello rejects another minor. A newer same-minor daemon is
   used as is, with no mandatory local binary or metadata upgrade. The
   mixed-version guards ticket separately owns cross-minor acceptance,
   notices, schema guards, and tool-list refresh.

A hosted session's global `dashboard.lan_enabled` can switch the daemon's
LAN mode (the daemon respawns itself with the other setting), but only a
session that takes part in the dashboard has a say: a hello whose server
declares no dashboard manifest, sets `WS_MCP_NO_DASHBOARD=1`, or reads a
global config other than the daemon's (a different `WS_CONFIG_HOME`) never
changes the mode, so two harnesses with different config homes cannot flip
the daemon back and forth.

Drain: a daemon being replaced drains its hosted sessions within
`DefaultLockWait` − 1.8 s, then releases its lock and endpoint so the
successor proceeds, and only then waits for the calls still running (up to
two minutes, `hostedCallLinger`) before the process exits: a `git.commit`,
`git.merge`, or `tickets.close` in flight finishes and its answer is
delivered on the old connection before it closes. A draining server refuses
a new request with `-32000` "server draining", and the drain cancels a
blocked `exec.result` wait at once, which answers with the job state it
finds. A CLI-mode `mailbox wait` is cancelled by the drain (exit 130).

Compatibility and version inspection execute zero ws binaries: the live
identity comes from `/health` or the existing hello; staged identity comes
from a SHA-256-bound `<binary>.metadata.json`. A missing stamp requests
verified release repair or a source rebuild, never a temporary MCP server or
an executable capabilities/command/version probe. Inspection alone never
starts a daemon.

Unavailability is explicit: stderr includes "dashboard daemon not reachable"
and the underlying cause. MCP requests receive `-32000` with their original
IDs when a sent request's outcome is unknown; no tool side effect is replayed.
Requests not yet sent wait up to 75 s for the daemon, re-reading its endpoint
about every 3 s; exhaustion adds `(waited 75s for the dashboard daemon)` to
the existing error. This includes MCP `initialize`, `tools/list`, and
`tools/call`, and one-shot CLI forwarding before its command-carrying hello
is sent. A failure after sending that hello or an MCP request is not retried.
Codex Stop retains its fail-open exit 0.

Reconnect: when the connection ends while the client is still attached (the
daemon was replaced or died), the session MCP proxy connects again (giving a
successor the swap grace only when the unreachable endpoint's singleton lock
is held or unqueryable, before coordinated absence startup), re-sends the
client's captured `initialize` request under an id of its own
(`ws-mcp-launcher-replay-<n>`, so a late answer from the old daemon can never
be mistaken for it) and drops its
answer, re-sends `notifications/initialized`, and answers every request that
was in flight with a JSON-RPC error (`-32000`, "ws-mcp daemon connection was
replaced"); the client never sees the server go away. Reconnecting is
paced: a connection the daemon closes within 2 s counts as a quick close;
retries wait at least 3 s, rising by 0.5 s × the count to a 5 s cap. Three
quick closes, failed starts, refused hellos, and reconnect failures never
switch to a child server. In-flight calls fail before waiting for recovery;
calls not yet sent wait within the 75 s budget instead. The stdio proxy stays
alive even after a `tools/list` wait expires, so later requests can use the
successor. A hosted server for a reconnected session re-registers that session's
mailbox presence on its first keyed call and keeps the owner pointer and the
`WS_MAILBOX_AUTO` stem its predecessor published, provided
that record is still live (its PID alive and `last_seen` within the ten-minute
liveness threshold); a stale record under a reused PID is replaced whole, and
among several live auto records the first name in order is the one kept.

Client EOF: when the client closes the proxy's stdin, the proxy half-closes
the daemon stream for writing and keeps copying answers to its stdout until
the daemon closes the stream or 10 s pass (`PROXY_DRAIN_SECONDS`), so a
response to the client's last request is not dropped.

Wire format (`internal/wsdashboard/proxy.go`): `GET /mcp HTTP/1.1` with
`Upgrade: ws-mcp`, `Connection: Upgrade`, and `Authorization: Bearer <token
from endpoint.json>`, from a loopback peer (also in LAN mode) with no
`Origin` header; the daemon answers `101 Switching Protocols` (401 for a
missing or wrong token, 403 for a non-loopback peer or a browser Origin, 426
without the upgrade headers) and the connection becomes a raw
newline-delimited stream with no framing. The launcher's first line is the
hello, one JSON object:

```json
{"protocol": 1, "mode": "mcp" | "cli", "argv": ["serve", "--stdio"],
 "cwd": "<launcher cwd>", "project_root": "<WS_MCP_PROJECT_ROOT or empty>",
 "env": {"<every variable of the launcher's environment>": "..."},
 "harness": "", "owner_pid": <launcher pid>, "version": "<runtime.json plugin_version>"}
```

Path-valued ws variables in the hello (`WS_CACHE_HOME`, `WS_CONFIG_HOME`,
`WS_RSRC_ROOT`, `WS_SKILLS_ROOT`, `WS_MCP_PROJECT_ROOT`, `WS_WORKTREE_DIR`,
`WS_MCP_DEBUG_LOG`, `WS_MCP_RUNTIME_BINARY`, `WS_MCP_RUNTIME_DIR`, and each
entry of `WS_MCP_CONFIG_MANIFESTS`) are made absolute against the launcher's
cwd before sending: a harness may set them relative (Codex's `.mcp.json`
names `./config-manifest.json` with cwd `.`), and the daemon would resolve
them against its own cwd.

The daemon answers one line, `{"ok": true, "version": "<daemon version>",
"pid": <daemon pid>}` or `{"ok": false, "reason": "..."}` (a protocol other
than 1, an unknown mode, an empty `cwd`, a missing `env` or `owner_pid`, a
version outside the daemon's minor, or a malformed CLI hello), closing the connection after a refusal. After `ok` the stream is the MCP session in `mcp` mode: the daemon
builds one `mcp.Server` from the hello (`NewServerWithOptions`: the hello's
environment is the server's whole environment and that of every subprocess
its tools start, `owner_pid` is its PID for mailbox presence and listening
markers, the debug event ring and `WS_MCP_DEBUG_LOG` are its own, while
resource and workflow-skill/template reads select the daemon's embedded package
by `WS_MCP_NAMESPACE` (`wsflow`, otherwise `ws`). Hello resource paths and
hello `WS_RSRC_DEV_CHECKOUT` do not redirect those reads. Startup diagnostics such as a rejected
config manifest go to the daemon log and the connection's debug events). The
harness is detected from `initialize` as on stdio; the hello's `harness` is
an optional hint. Closing the connection ends the session: its dashboard
session, its mailbox presence, and any wait it holds.

HTTP route (`internal/wsdashboard/proxy_http.go`): a session the agent runner
hosts reaches the same server with no launcher, by MCP Streamable HTTP with
JSON responses only. It exists only when the daemon hosts MCP sessions and its
agent runner implements `AgentMCPTokenHost`. `POST /mcp/session` carries one
JSON-RPC message with `Authorization: Bearer <per-session token>`, from a
loopback peer with no `Origin` (403 otherwise); the token is resolved on every
request (401 for a missing, unknown, or void one) to the hello a launcher would
have sent, and other methods (a client's SSE `GET`, its exit `DELETE`) get 405.
No `Mcp-Session-Id` is issued: the token is the session, one per token, built
by the same `acceptProxy` and counted, drained, and closed with the stdio
sessions; voiding the token (re-issue, process exit, close) cancels its calls
in flight and ends it. A request
is answered in its POST's body once the session writes the answer with that
id; a notification or client response gets 202; a POST whose client goes away
sends `notifications/cancelled` for its id; a session end answers `-32000`
"ws-mcp session ended". A token's first message that is not `initialize` (its
client initialized against a replaced daemon) is preceded by a replayed
`initialize` (id `ws-mcp-http-replay-<n>`, `clientInfo` mapped from the
hello's harness, answer dropped) and `notifications/initialized`. A body is
limited to 65535 bytes (413), must be one JSON object (400: `-32700` invalid
JSON, `-32600` batch), and is compacted to one line; a duplicate in-flight id
gets `-32600`. A refused hello answers 403 and a draining daemon 503, each
with a JSON-RPC error body.

Who uses it (`internal/wsagent/mcp_session.go`): a hosted Claude process the
runner starts while it knows its daemon endpoint. Every start draws a new
token; the harness gets the URL and token in `WS_MCP_SESSION_URL` and
`WS_MCP_SESSION_TOKEN`, never in argv or a file. A worker's and a lead's
inline `--mcp-config` entry `ws` is the HTTP server; a lead loads no ws
bundle, so it has exactly one ws server, named `ws`. (A lead an older daemon
started ran a hosted variant of its bundle under `<daemon
dir>/hosted-bundles/`, whose server is `plugin:<package>:<package>`; the lead
result mapper still accepts those names for a rejoined process.)
A held lead's token is kept in its `agent.json` (`mcp_token_secret`) with its
URL (`mcp_url`), the ws-mcp environment its hello was built from (`mcp_env`:
the package its ws-mcp was resolved from) and
the holder it was drawn for (`mcp_holder_id`), so a daemon that rejoins the
process restores the token and its hello without resolving the package again.
When the recorded URL is not the rejoining daemon's (it listens on another
port) or the hello is missing, the process restarts at its first idle point
with a new token; when the record names another holder (a daemon without
this route started the process again over stdio), the token is forgotten and
nothing restarts. With no endpoint the `ws` entry is the bundled stdio
launcher instead. A daemon without this route (an older
one rejoining an HTTP lead) answers its calls `401 login_required` until the
process restarts.

In `cli` mode the daemon dispatches the existing command argv in-process.
An optional `stdin` string carries Codex Stop JSON or `git commit
--description-file -` input; older hellos omit it. A relative `--root`
resolves against the hello's `cwd`; stores, manifests, tool restrictions,
subprocess environment and owner identity belong to that hello, never another
package's daemon-wide values. The daemon answers in frames, one
JSON object per line: `{"stream": "stderr", "data": "..."}` for each stderr
write as it happens, then one `{"final": true, "exit_code": <n>, "data":
"<the whole stdout>"}`, `exit_code` always present (0 included). An explicitly
streaming command (`mailbox wait --stream`) instead sends live stdout in
`{"stream": "stdout", "data": "..."}` frames before its final frame; ordinary
one-shot stdout remains buffered. The
launcher writes them as UTF-8 bytes to its own stderr and stdout and exits
with `exit_code`. Closing the connection (the launcher exits, or the
daemon drains) cancels the wait at once and clears its listening marker
(`exit_code` 130, "interrupted"). A hosted wait rechecks on an in-process
signal (a mailbox send by a server the daemon hosts, a board-answer inbox
write, or a nudge) with a five-minute lost-notification backstop poll.
The daemon's undelivered-mail maintenance likewise discovers queue stores
at startup and every five minutes, visiting changed stores and pending
notice deadlines between scans; presence-only heartbeats do not schedule
maintenance. Failed notice delivery retains its 30-second retry cadence.

`mailbox wait --stream` emits one JSON line per arrival without draining and
keeps its listening marker until interruption, client disconnect, or parent
death. `--timeout` becomes the unchanged-pending-arrival reminder interval
(default 10 minutes), not a process deadline; an empty timeout emits nothing.
The Claude mod reuses that child across wakes and restarts it when board flags
change. A rejected stream flag (usage/unknown-flag diagnostics, or a nonzero
exit before any arrival) selects the one-shot loop for the remaining session.
Model-armed waits keep the one-shot contract.

## Launcher Environment

Current launcher inputs:

| Variable | Purpose |
|----------|---------|
| `WS_MCP_RUNTIME_DIR` | Override the runtime binary directory; defaults to plugin-local `.runtime/<os>-<arch>`. |
| `WS_MCP_RUNTIME_BINARY` | Exact repaired runtime binary path exported by an external plugin launcher for async worker recovery. Hosted launchers choose the retained executable only at necessary daemon restart. |
| `WS_MCP_HOSTED_PACKAGE` | Internal hosted-launch marker (`1`): the staged launcher has no plugin runtime and restarts from the daemon's last-running-executable record. Cleared from the daemon starter's environment. |
| `WS_MCP_BOOTSTRAP_BINARY` | Copy a prebuilt local binary into the runtime directory. |
| `WS_MCP_BOOTSTRAP_URL` | Download a prebuilt binary when no runtime binary exists. |
| `WS_MCP_BOOTSTRAP_SHA256` | Optional SHA-256 checksum for `WS_MCP_BOOTSTRAP_URL`. |
| `WS_MCP_RELEASE_REPOSITORY` | Override the GitHub release repository from `runtime.json`. |
| `WS_MCP_RELEASE_TAG` | Override the release tag from `runtime.json`, for example `v0.53.2`. |
| `WS_MCP_RELEASE_BASE_URL` | Override the full release asset base URL; useful for local file or HTTP smoke tests. |
| `WS_MCP_LAUNCHER_DEBUG` | Print launcher diagnostics to stderr when set to `1`. |
| `WS_CODESIGN_IDENTITY` | macOS local dev builds only: code-signing identity name or SHA-1 passed to `codesign --force -s "$WS_CODESIGN_IDENTITY" -i dev.ws.mcp <binary>` before SHA-256 metadata generation; unset leaves builds untouched, and a signing failure aborts the build. |
| `WS_MCP_LAUNCHER_DIRECT` | Obsolete; ignored. It never enables executable fallback. |
| `WS_MCP_PROJECT_ROOT` | Project root default for root-aware tools and CLI commands when no higher-priority root exists. |
| `WS_RSRC_DEV_CHECKOUT` | Explicit daemon-process-only repository checkout for package inputs (rsrc, workflow skills/templates, and manifests). Uses the package's canonical directory, logs the override, and permits live playbook edits. Never honored from a connection hello. Production embeds always win over `WS_RSRC_ROOT`. |
| `WS_MCP_DASHBOARD_DEV_SOURCE` | Read by `ws-mcp dashboard`: the `agents-plugin-tool` source root the daemon serves dashboard and whiteboard page assets from instead of the embedded copies (see Dashboard daemon, Dev assets). Unset is off. |
| `WS_MCP_DASHBOARD_PPROF` | Read by `ws-mcp dashboard`: `1`/`true`/`yes`/`on` serves `net/http/pprof` under `/debug/pprof/` to loopback viewers only, never to a LAN-token request (see Dashboard daemon, Profiling). Unset is off: the routes do not exist. |
| `WS_MCP_HOLDER_PYTHON` | Read by `ws-mcp dashboard`: the Python interpreter that runs session holders (see Dashboard daemon, Session holders). The launcher sets it to its own interpreter on the daemon-start step unless it is already set; unset, the daemon uses `python3` on PATH. |
| `WS_MCP_NO_AGENT` | Product-mode gate for agentless distributions such as wsflow. |
| `WS_MCP_NO_DASHBOARD` | `1`/`true`/`yes`/`on` turns the dashboard off for this ws-mcp whatever `dashboard.enabled` resolves: no session registration, no prune (the daemon itself is started by the launcher, never by this ws-mcp). The agent runner sets it on the ws-mcp it attaches to a conversation. |
| `WS_HOSTED_SESSION` | `1`/`true`/`yes`/`on` marks a session the dashboard daemon hosts; the daemon sets it, with `WS_HOSTED_SESSION_KEY` and `WS_HOSTED_SESSION_ROW`, on the harness it spawns. Every process below that harness inherits the three, so each reader (this ws-mcp and the Claude mod) honours them only when the row names its own harness process (see `WS_HOSTED_SESSION_ROW`), resolves that once, and never becomes hosted later. Hosted, this ws-mcp neither registers, heartbeats, nor prunes (the daemon writes the session's registration) but still nudges the daemon and names the daemon's page in `whiteboard.update`; `ferrule` returns the hosted key for a parent-less lead bootstrap and refuses a re-login with any other key; every tool call that omits `session_key` runs with the hosted key, and one that passes a key other than the hosted key or a descendant session's (Key rule above) is refused, `workflow_manual` included; child mints are unchanged. Set without a key, it changes nothing here. Unset is off. |
| `WS_HOSTED_SESSION_KEY` | The parent-less lead key the daemon minted for the session it hosts, read only while `WS_HOSTED_SESSION` is on; the session's key stays fixed to it. |
| `WS_HOSTED_SESSION_ROW` | Absolute path of the hosted session's `registration.json`. Its `host_pid` is the one harness process the flag holds for: equal to the reader's harness PID, hosted; another nonzero PID, an ordinary session, silently (a nested harness, a CLI call from the session's shell); 0, absent, or unreadable, read again about every 100 ms for about 3 s, then an ordinary session with one warning. Unset (an older daemon, a stray flag) is not hosted. The harness PID is `WS_MCP_HARNESS_PID` for ws-mcp (with a Windows `cmd.exe` wrapper skipped, see that row), the mod's own process for Claude (read as the parent of a host command; on Windows, when that parent is the `cmd.exe` shell the host command ran through, that shell's parent, by the same one-level rule). |
| `WS_MCP_HARNESS_PID` | Set by the launcher on every start to its own parent PID, over any inherited value: the harness that started this ws-mcp. The runtime reads it as that harness's PID, except on Windows when that process is a `cmd.exe` (the wrapper a harness commonly starts a stdio server through): then its parent. Exactly one level is skipped, so a harness nested under another still tells itself from the outer one, and a failed process lookup keeps the PID as set. It reaches the runtime on both paths (the direct child's environment, and the hello's `env` for a daemon-hosted connection, so it is per caller). A ws-mcp started without the launcher has none and is never hosted; `ws-mcp dashboard` drops it, with the three `WS_HOSTED_SESSION*` variables, from its own environment at start, so the conversations it spawns inherit no session's. |
| `WS_MCP_ALLOWED_TOOLS` | Comma list of ws tool names; when set, `tools/list` advertises and `tools/call` accepts only these. A hosted lead whose descriptor opts into `CallFallback` also gets `call`; naming `call` in an allowlist alone never enables it. Its inner tool still obeys this allowlist and the normal dispatch gates. Fallback discovery applies permission and role gates using its optional `session_key` (implicit on hosted connections); without a known key it hides role-gated tools. The agent runner sets the allowlist to a profile's ws tools. `tracker.show`, `tracker.ui`, and `git.search` are listed and callable only when it names them. |
| `WS_MCP_PI_TOKEN` | Per-process worker-delegation capability for the daemon's `POST /agents/by-token/<op>` route. With `WS_MCP_PI_ENDPOINT` and no local runner, ws-mcp forwards `agent.*` and authorized legacy Pi history calls to that route. The variable keeps its historical name for installed readers. |
| `WS_MCP_PI_ENDPOINT` | Set beside `WS_MCP_PI_TOKEN`: the dashboard daemon's loopback base URL. |
| `WS_MCP_NAMESPACE` | MCP namespace and embedded package identity: `wsflow` selects wsflow assets; empty/unset or a custom display namespace selects ws. |
| `WS_MCP_SETUP_TOOL` | Advertised setup tool name override; empty or unset defaults to `ws.setup`. |
| `WS_MCP_CONFIG_MANIFESTS` | Adapter config key manifests, separated by the platform path-list separator (`:` POSIX, `;` Windows); `;` is accepted on every platform, which the static launcher configs use. Declared keys become `config.tune`-writable, cataloged by `config.list`, and typed in `config.get`; a rejected manifest is logged to stderr and listed by `config.list`. The ws and wsflow packages each list three package-local manifests in both launch configs: `config-manifest.json` (namespace `claude.`; the same keys, differing only in `default`, pinned by `TestShippedClaudeManifestsDifferOnlyInDefaults`), `agy-config-manifest.json` (namespace `agy.`; `agy.model_wait_timeout_seconds`, default 120 in ws and 0 in wsflow, `agy.model_wait_retries`, default 3 in both, `agy.stall_log_check`, default on in both, which lets a hosted agy lead's silence timer cut a model request only when agy's own worker log shows it has no response headers, and `agy.http2_frame_trace`, default off in both, which starts agy with `GODEBUG=http2debug=2` and has its holder keep filtered frame summaries in the session's `agy-http2-frames.log`), and `dashboard-config-manifest.json` (namespace `dashboard.`: `dashboard.enabled`, default on in every package; `dashboard.port`, default 29417; `dashboard.lan_enabled`, global-only and default off in every package). Codex paths are relative to `cwd: "."` (the plugin root); Claude paths use `${CLAUDE_PLUGIN_ROOT}`. A path that does not resolve fails nothing: the keys stay undeclared, so check `config.list` after changing the list. |
| `WS_MCP_CLI_NAME` | Declared `ws-cli` / `wsflow-cli` shim or Python launcher command used in printed mailbox wait and re-arm guidance when it matches the supported safe form. The launcher sets it unless already set; an absent or unrecognized value uses an illustrative Python launcher path, never the runtime binary. |

Launcher diagnostics must go to stderr. Stdout belongs to the MCP JSON-RPC
stream from the moment the launcher connects to the daemon.

## Runtime Repair And Drift

The launcher reads plugin-local `runtime.json` and connects to a compatible
running daemon without requiring a local binary upgrade. When a start or
replacement is needed, it repairs a missing/incompatible runtime from
hash-bound metadata, then runs only the daemon starter.

Runtime binaries live in the installed plugin cache by default:

```text
<installed-plugin-cache>/.runtime/<os>-<arch>/ws-mcp-<plugin-version>-<runtime-json-sha12>[.exe]
```

Compatibility checks read `<binary>.metadata.json`, validating its content
SHA-256 and actual build/release version. Build-written protocol and surface
inventories are optional and checked when present. Compatibility stamps also
bind content hash and accepted actual version, not the connecting package's
desired version. Unstamped older release bytes remain usable through a
versioned release's verified SHA256SUMS or metadata-producing repair/rebuild.
Build, bootstrap, copy, stage and seed paths carry that identity; none runs
the binary to discover it.

`runtime.json` is active compatibility data, not release notes. Version, tool,
command, and prompt-bundle drift can make a working binary stale. Use the
version bump helper instead of editing release/version references by hand:

```bash
agents-plugin-tool/scripts/bump-ws-version.sh <X.Y.Z>
```

The helper updates plugin manifests, runtime contracts, Go runtime development
defaults, release workflow references, build script defaults, and selected
documentation references. Development binaries such as `0.53.2-dev` satisfy the
plugin version the helper just set; older or newer minor releases are stale. The
concrete range lives in `runtime.json.required_mcp` — read it there rather than
restating a version here, which froze at `0.29.2` for seven minor releases
because no bump-script regex matches a bare `X.Y.Z`.

## Release Distribution

`ws-mcp` is distributed as prebuilt native binaries produced from
`agents-plugin-tool/`. End users should not need Go, Node, Cargo, or Visual
Studio Build Tools to run the MCP server.

Release asset names follow this pattern:

```text
ws-mcp-<os>-<arch>[.exe]
```

Initial assets:

```text
ws-mcp-darwin-arm64
ws-mcp-darwin-amd64
ws-mcp-linux-amd64
ws-mcp-linux-arm64
ws-mcp-windows-amd64.exe
ws-mcp-windows-arm64.exe
SHA256SUMS
```

`SHA256SUMS` is a plain `shasum -a 256` manifest covering every release binary.
The launcher verifies the matching checksum line before installation or daemon start. Offline or
proxy failures should keep stdout clean and report the URL or runtime directory
that needs manual repair.

Every supported build entry generates the gitignored embedded package set
before Go compilation. For a direct source build/test, run
`python3 scripts/generate-package-assets.py` from `agents-plugin-tool` first;
`--check` verifies drift. Bare `go build` compiles with a placeholder but cannot
serve playbooks without generation or the explicit dev checkout. Text-only
production edits require a rebuilt daemon; the rsrc schema version tracks
shape, not content. Skills still ship in the external plugin for harness discovery.

Desktop assembly consumes an already-built binary and checks its embedded
inventory against canonical inputs; generating assets after compilation cannot
repair that binary. It still stages the complete external package for its
outside-daemon validation/launcher/config readers. Hosted sessions no longer
use that copy.

The local build script is:

```bash
agents-plugin-tool/scripts/build-release-assets.sh [version]
```

GitHub Actions workflow `.github/workflows/ws-mcp-ci.yml` runs the regular Go
test group on Ubuntu and sharded Windows jobs for pull requests and manual
dispatch. The boolean dispatch input `long_tests` defaults to false; true adds
long-group jobs on both platforms. The optional `windows_shard` choice input
defaults to `all` (full matrix + desktop); selecting a specific Windows shard
runs only that Windows leg for manual debugging without desktop or full-matrix
jobs (and therefore does not satisfy the release gate). `scripts/ci-test-shard.sh` owns group
membership: `all` excludes long tests, `rest` also excludes `internal/mcp`, and
`rest` + every MCP shard + `long` covers the full suite. Locally, run the script
from `agents-plugin-tool` with `bash scripts/ci-test-shard.sh all`, adding a
separate `long` invocation when explicitly requested.

Workflow `.github/workflows/ws-mcp-release.yml` requires a successful CI run
on the tagged commit, allowing only the two named long jobs to be skipped.
It cross-compiles assets, uploads workflow artifacts, and publishes GitHub
release assets for pushed `v*` tags (passing the resolved build version to
both the native build and `windows-smoke`). Pull requests touching workflow or
plugin/runtime paths run checks without publishing. This project's
`ai-docs/ship/ws.md` records the owner's long-test choice and requires those
jobs to succeed when requested.

Workflow `.github/workflows/ws-canary.yml` provides a one-click
`workflow_dispatch` canary build directly from `develop` without merging to
`main`, cutting an ephemeral branch, or pushing tags manually.
`agents-plugin-tool/scripts/resolve-canary-version.py` resolves the build plan
and emits non-blocking `::warning::` annotations if `develop` is ahead of the
review-watermark frontier (`ai-docs/.review-ledger.md`):

- `mode=artifact` builds `ws-mcp-assets` and `werkschaft-tauri-assets` at
  `<current-core>-canary.0` (skipping CI by default unless `run_ci=true`). It
  defaults to evaluation signing (`signing=eval`, `WERKSCHAFT_EVALUATION=1`),
  and restricts `signing=production` to `refs/heads/develop`.
- `mode=prerelease` enforces `refs/heads/develop`, runs `.github/workflows/ws-mcp-ci.yml`
  via `workflow_call`, selects the next patch core `X.Y.(Z+1)` (never a minor
  bump, failing fast if `v<next-patch-core>` already exists) and next monotonic
  `v<next-patch-core>-canary.<N>` (`N >= 1`), runs `bump-ws-version.sh`
  ephemerally in `build`, `windows-smoke`, and `werkschaft-tauri-build.yml`
  `package` checkouts (via `bump_core`), and publishes an atomic
  draft-then-undraft GitHub prerelease targeting `GITHUB_SHA`.

## Local Devenv Repair

Local development has one repository-specific repair exception. The
gitignored `.local-devenv-runtime` marker in the plugin directory is the only
opt-in: with no marker the launcher takes the normal release path whatever the
plugin's location. A valid contract selects source repair for a missing or
incompatible runtime when the plugin runs from a per-user host plugin tree (`~/.codex/plugins/cache/kang-sw-devenv/<ws|wsflow>/`,
`~/.claude/plugins/cache/kang-sw-devenv/<ws|wsflow>/`, or the `scripts/install-claude-plugin.sh`
snapshot under `~/.claude/plugins/ws-plugin/<ws|wsflow>/`), or straight from
its source checkout, i.e. when the plugin directory lies inside the marker's own
`source_root` (resolved paths; case-insensitive on Windows). Both the Codex and
Claude plugin trees are recognized so the same source-build dogfood loop works
on either host.

The contract format is:

```json
{
  "schema_version": 1,
  "source_root": "/Users/kang-sw/devenv",
  "tool_dir": "/Users/kang-sw/devenv/agents-plugin-tool",
  "go": "/opt/homebrew/bin/go"
}
```

All paths must be absolute. `source_root` and `tool_dir` must exist, `tool_dir`
must contain `cmd/ws-mcp`, and `go` must be an existing file (an executable bit is
required on POSIX; on Windows the `go.exe` file is trusted because
`os.access(X_OK)` is not meaningful there). If the marker is missing, invalid
JSON, missing required fields, or references missing paths, local repair is
inactive and the launcher uses the normal cache/release path. Local devenv repair
is honored on all platforms including Windows; the build inherits a launch
environment with `HOME` recovered on POSIX and `USERPROFILE`/`LOCALAPPDATA`
recovered on Windows so `go build` resolves its module/build caches under a
sanitized launch env.

When the contract is valid, ordinary startup reuses a hash-bound compatible
runtime even when its source fingerprint is stale. A stale or missing source
stamp emits one stderr advisory pointing at `python3 scripts/dev.py rerun daemon`;
reuse leaves the old fingerprint intact. Missing or incompatible runtimes still
build `<tool_dir>/cmd/ws-mcp` with the declared Go executable. Explicit bootstrap
binary/URL overrides retain their installation semantics. Non-forced repair may
copy a local runtime binary from `<tool_dir>/dist/` or the contract-addressed
`<source_root>/agents-plugin/.runtime/<os>-<arch>/ws-mcp-<plugin-version>-<runtime-json-sha12>`
path before building. Legacy fixed-name source-cache binaries such as
`agents-plugin/.runtime/darwin-arm64/ws-mcp` are not local repair candidates.

If no compatible local runtime can be installed while a valid marker is active,
startup fails instead of falling back to the published release asset.

While a valid marker is active, the runtime version gate is relaxed from an exact
patch match to a **same-minor** match: a locally built runtime is accepted when
its `(major, minor)` equals the installed snapshot's `plugin_version`, regardless
of patch (a `-dev` suffix is ignored). The live source version routinely drifts a
few patches ahead of the installed snapshot's `runtime.json` between
`scripts/install-claude-plugin.sh` runs, and the exact-match gate would otherwise reject the
freshly built runtime after every local patch bump, breaking the MCP server until
a re-snapshot. A different minor or major is still rejected, because a minor bump
is a real contract change that must go through a re-snapshot. Released/downloaded
runtimes (no marker) keep the strict exact-match gate unchanged.

### Enabling the local dogfood loop (manual setup)

The marker is per-machine and gitignored, so place it once in the source
checkout for each package you dogfood, then let the plugin install or refresh
copy it into the plugin cache:

1. Write `agents-plugin/.local-devenv-runtime` with this machine's absolute
   paths (use `command -v go` for the Go path). If dogfooding the separate
   wsflow package, write the same marker to
   `agents-plugin-wsflow/.local-devenv-runtime`:

   ```json
   {
     "schema_version": 1,
     "source_root": "/home/you/devenv",
     "tool_dir": "/home/you/devenv/agents-plugin-tool",
     "go": "/home/linuxbrew/.linuxbrew/bin/go"
   }
   ```

2. Run `./scripts/install-claude-plugin.sh`. The snapshot step copies the marker into the
   plugin snapshot and `claude plugin install` carries it into the versioned
   cache, so the next MCP launch can build when no compatible runtime exists.

After setup, edit source and run `python3 scripts/dev.py rerun daemon` at the
coordinated swap time to build, stage and seed the selected runtime. Reconnecting
alone reuses it and reports source drift. To disable local
mode, delete the marker and re-run `./scripts/install-claude-plugin.sh` (the launcher reverts to
the cache/release path).

This path exists only for the repository-local Codex or Claude plugin
development loop. The marker file is gitignored and should not exist in normal
GitHub release installs, downstream repositories, or Windows installs.

### Stable macOS code signing for local builds

On macOS, `ws-mcp` launches the dashboard daemon from the stable path
`<cache-root>/dashboard/bin/ws-mcp-dashboard`. This executable is a separate
copy of the retained `ws-mcp-dashboard-<sum16>` file. Ad-hoc linker-signed dev
rebuilds still rotate their `cdhash` on every compile, which causes macOS TCC
prompts (for example Desktop folder access) to repeat. To keep one TCC identity
across local rebuilds:

1. In **Keychain Access**, open **Certificate Assistant → Create a
   Certificate…**, choose **Self-Signed Root** with **Certificate Type: Code
   Signing**, and create it in your `login` keychain.
2. Export `WS_CODESIGN_IDENTITY` to that certificate's name (or SHA-1
   fingerprint) in your shell profile. When unset, local builds are unchanged.
3. Rebuild locally (for example `python3 scripts/dev.py rerun daemon --local` or
   a local-devenv launcher build); `codesign --force -s "$WS_CODESIGN_IDENTITY"
   -i dev.ws.mcp <binary>` runs before `.metadata.json` SHA-256 generation, and
   any signing error fails the build step rather than installing an unsigned
   binary.
4. Grant **Full Disk Access** once in **System Settings → Privacy & Security →
   Full Disk Access** to `<cache-root>/dashboard/bin/ws-mcp-dashboard`.

## Development Verification

Use three verification levels while developing `ws-mcp` and plugin-managed
runtime behavior.

Level 1 validates the Go runtime and host-independent MCP server:

```bash
cd agents-plugin-tool
bash scripts/ci-test-shard.sh all
# Only when explicitly requested:
# bash scripts/ci-test-shard.sh long
scripts/smoke-ws-mcp.sh ..
```

Level 2 validates local release assets:

```bash
cd agents-plugin-tool
scripts/build-release-assets.sh 0.53.2-dev
python3 ../agents-plugin/bin/ws-mcp-launcher.py version
cd dist
shasum -a 256 -c SHA256SUMS
```

Level 3 validates Codex plugin-managed MCP startup from the installed plugin
cache. This level requires a human-in-the-loop plugin cache refresh when plugin
files change: uninstall/install `ws` in the Codex UI or start a fresh session
after cache refresh is available. Then run:

```bash
codex mcp get ws
codex exec --dangerously-bypass-approvals-and-sandbox --json \
  'There is an enabled MCP server named ws. Use its tool named project_tree with arguments {"root":"/Users/kang-sw/devenv"}. Do not use shell commands. Reply with the exact server name, exact tool name, and the first non-empty line of the tool result.' \
  < /dev/null
```

Success means the JSONL output contains an MCP tool call with server `ws`, tool
`project_tree`, and a result whose first non-empty line is `ai-docs/`.

Use Level 1 for ordinary Go/MCP changes, Level 2 for release/build changes, and
Level 3 whenever `.codex-plugin/plugin.json`, `.mcp.json`, launcher behavior, or
installed plugin packaging changes.

## Troubleshooting

- MCP startup fails before JSON-RPC: inspect launcher stderr; stdout must remain
  clean.
- Plugin-managed root resolution points at the plugin cache: pass an explicit
  absolute `root`, call setup with the repository's absolute filesystem path, or
  check `WS_MCP_PROJECT_ROOT` propagation.
- Installed plugin files changed but Codex still starts old behavior: refresh
  the plugin cache through UI uninstall/install or a fresh Codex session.
- Runtime surface mismatch: inspect the running daemon with the Python
  launcher's `runtime capabilities`, or check the binary's hash-bound metadata
  and compare it with `runtime.json`; never execute the binary as a probe.
- Windows startup reports Python alias problems: install Python 3 and refresh
  the plugin so Codex rematerializes the MCP entry.
- Routing or implementation context lost after context compaction: expected — the
  transcript is summarized, but session state persists in
  `<cache-root>/sessions/<session-key>/` (see Session key store). Recover by calling
  `ws.workflow_manual(session_key: <key preserved in the compaction summary>)`,
  which reloads the manual primitives and restores the agenda/todo Session State.
  Do not call `ws.ferrule` to "re-enter" — it is non-idempotent and mints a new key,
  orphaning the prior state.
