# Plan: open the hosted exec, user-command and cron SQLite stores via a normalized file URI

## Problem (live evidence, 2026-11-23)

On the owner's Windows daemon, startup logs
`unable to open database file: out of memory (14)` (SQLITE_CANTOPEN) for
three stores, and none of their DB files exist:

- hosted exec: `agents-plugin-tool/internal/execjob/hosted_store.go:105-109`
  (caller `cmd/ws-mcp/exec_hosted.go:58-62`, which then returns nil, so
  `cmd/ws-mcp/dashboard.go:256-263` never binds the composer: GET
  `/composer-exec` answers 503 and the `!` composer is dead).
- user commands: `internal/wsusercmd/store.go:81-85` (`cmd/ws-mcp/usercmd.go:39`).
- cron: `internal/wscron/store.go:69-73` (`cmd/ws-mcp/cron_hosted.go:52`).

Each MkdirAll's its parent (the parent exists) and passes the raw native
path to `sql.Open`. The whiteboard DB in the same process opens fine; its
opener `internal/wswhiteboard/board_db.go:395-400,443-469` builds a
normalized, escaped `file:` URI (commit a233e15d8 fixed literal `?`
preservation).

## Leading hypothesis (verify by reading code, not by running on Windows)

The daemon's state dir can be an extended-length path: other ws paths on
this machine render as `\\?\C:\Users\...\.cache\ws@...\...`. modernc.org
sqlite treats `?` in a raw DSN as the start of query parameters, so a raw
`\\?\C:\...` path is cut to `\\` and fails with CANTOPEN. Confirm where the
dashboard state dir comes from (`cmd/ws-mcp/dashboard.go:49-54`) and whether
it can carry the `\\?\` prefix; confirm modernc's DSN split
(module cache `modernc.org/sqlite` v1.30.2, sqlite.go). If confirmed, that
is the root cause; if not, report what you found before changing code.

## Fix

- Extract (or reuse) the board's path-to-`file:` URI normalization into one
  small shared helper that all four openers use (board keeps identical
  behavior). Apply it at the three open calls above. Keep DB file names,
  schemas, pragmas (WAL), permissions and the MkdirAll step unchanged.
- Add a unit test that opens each store (or the helper) with a directory
  name containing `?` and, where the helper handles it, a `\\?\`-prefixed
  path form, asserting the DB file is created at the intended place. Keep it
  OS-neutral so it runs on w5090 (Linux); a Windows-only prefix case may use
  a pure string test of the helper.
- Compatibility: T0 (same files and schema; previously the stores never
  opened on affected machines).

## Verification

- w5090 only, never local Go:
  `bash D:/sys_lock.sh remote w5090 run sqlite-uri 40 --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/tassel-sneak-enjoyably' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go vet ./internal/execjob ./internal/wsusercmd ./internal/wscron ./internal/wswhiteboard ./cmd/ws-mcp && go test ./internal/execjob/... ./internal/wsusercmd/... ./internal/wscron/... ./internal/wswhiteboard/... ./cmd/ws-mcp/...' > D:/Temp/sqlite-uri/w5090.log 2>&1`
  then read the log back. gofmt the touched files on w5090.
- The live Windows check is done by the lead after merge (daemon restart and
  dashboard.log): do not launch ws/ws-mcp exes or the dashboard locally.

## Constraints

- No local `go test`/`go build`/Go tools, ws exes, launcher, Electron or
  Tauri on this PC. No network downloads. Temp only under
  `D:/Temp/sqlite-uri/`, deleted when done (keep nothing).
- Never print credentials, tokens, cookies or URLs with query strings; do
  not read endpoint.json or connect to the owner's live dashboard. Report
  state-dir facts as shapes (e.g. "has \\?\ prefix"), not full user paths.
- Commit on `impl/develop/sqlite-store-uri-open` in this worktree via
  ws/git.commit, including this plan, with no Co-Authored-By trailer.
  No push, no merge.

## Return boundary

Stop and report if the hypothesis is refuted, if the fix needs a change to
store file names/locations or schema, or if more than these four openers
need edits.
