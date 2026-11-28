---
summary: Native-Windows source-build dogfood / Phase C cold-load acceptance procedure
---

# Windows source-build dogfood

How to dogfood a branch's `ws` plugin on a native Windows host with an automatic
source-build loop, mirroring the Linux `install.sh` flow. Used for
`260622-chore-windows-shipping-hardening` Phase C and any later Windows cold-load
verification.

## How it works

The launcher's local-devenv path (gitignored `.local-devenv-runtime` marker ->
build the Go runtime from the checkout on cold-load, fingerprint-cached) is
honored on Windows as of `260622-feat-windows-local-devenv-autobuild`. A clone on
the Windows host is required regardless: the Go module (`agents-plugin-tool/`) is
not part of the `agents-plugin/` plugin package, so the source must be physically
present to build.

`scripts/install-claude-plugin.ps1` is the Windows equivalent of `install.sh`'s
plugin registration: it snapshots `agents-plugin/` into
`%USERPROFILE%\.claude\plugins\ws-plugin\ws`, generates `marketplace.json`, patches
`settings.json` + `known_marketplaces.json`, and writes the marker pointing at the
clone. After that, every Claude Code restart rebuilds the runtime from the clone
when the Go source changed.

## Prerequisites

- Native Windows host with the target branch checked out from a clone.
- Go toolchain on `PATH`.
- `python3` on `PATH` (the launcher is Python; the installer reuses it for JSON
  merges. A Windows Store alias without a real install fails — install Python 3).

## Procedure (one-shot)

```powershell
# In the clone, on the target branch
pwsh -NoProfile -File scripts\install-claude-plugin.ps1
# Preview without writing:
#   pwsh -NoProfile -File scripts\install-claude-plugin.ps1 -DryRun
```

Restart Claude Code. On first MCP load the launcher reads the marker, builds
`agents-plugin-tool/cmd/ws-mcp` with the clone's `go.exe`, validates the runtime
contract, stages a content-hash-named copy under the dashboard daemon's `bin/`
directory, and connects to the daemon that copy runs (starting it when none
answers); the launcher itself only proxies stdio to the daemon-hosted MCP
server (ws-mcp.md, "Daemon-Hosted MCP"). The plugin version is bumped per
dev-merge, so the cache invalidates and cold-extracts on reinstall — this is
the first-load path under test.

## Iteration loop

- **Edit Go source** -> just restart Claude Code; the source fingerprint changes
  and the runtime rebuilds automatically.
- **Edit `launcher.py` / skills / `runtime.json`** -> re-run the installer to
  re-snapshot, then restart.

## Fallback: no clone / no Go toolchain

If building on the host is not possible, inject a prebuilt binary instead (skips
the marker/auto-build path; still exercises `install_tmp_runtime`):

```powershell
cd agents-plugin-tool
go build -o C:\ws\ws-mcp-windows-amd64.exe ./cmd/ws-mcp   # or cross-compile elsewhere
setx WS_MCP_BOOTSTRAP_BINARY C:\ws\ws-mcp-windows-amd64.exe
```

Then register the plugin (installer above, or your usual branch-pin install) and
restart. `WS_MCP_LAUNCHER_DEBUG=1` prints cold-load diagnostics to stderr.

## Acceptance checklist (Phase C)

- Cold MCP load succeeds (no failure from rsrc-tree race, unreadable contract, or
  replace contention; the source build completes within the startup window).
- `ws.ferrule` login resolves a session key.
- Process round-trip: `exec.spawn` -> `exec.status` -> `exec.result`, plus
  `exec.abort` (the surviving per-platform process start/snapshot/cancel path).
  The job runs as a child of the dashboard daemon, under the connection's
  environment (its PATH and cache home), not as a child of the launcher.
  These tools dispatch but are never advertised in `tools/list`, so the host
  agent cannot be asked to call them: drive this item from a raw JSON-RPC
  stdio client the way `agents-plugin-tool/scripts/smoke-ws-mcp.sh` does.
- Workflow bootstrapping: skill invoke -> `playbook.read` / `playbook.render`.

## Hard constraint

Live-host safety: kill only PID/job-scoped subtrees. Never image-name kill
(`taskkill /IM`) or sweep — a dogfooding host may run a live `claude.exe`.

## Remote headless verification over SSH tunnel (`scripts/win-tunnel-smoke.py`)

Verify a branch's Windows `ws-mcp.exe` dashboard daemon from macOS over SSH +
`ssh -L` + headless Chromium (Playwright), including when the Windows host's
display is off or console is locked.

- **Target host:** set `WS_WIN_HOST=<user>@<host>` or pass `--host <user>@<host>`.
  Do not commit machine-local hosts or usernames; local values live in the
  gitignored `ai-docs/manuals/local-machine-context.local.md`.
- **What it does:**
  1. Generates embedded package assets and cross-compiles `ws-mcp.exe`
     (`GOOS=windows GOARCH=amd64 CGO_ENABLED=0`) from `agents-plugin-tool/`,
     unless `--remote-binary <win-path>` is given.
  2. Creates an isolated `%TEMP%\ws-tunnel-smoke-<nonce>` directory on Windows
     over SSH (`BatchMode=yes`, `ConnectTimeout=10`), copies `ws-mcp.exe` into
     it, and starts `ws-mcp.exe dashboard --port <remote-port>` detached with
     `WS_CACHE_HOME` and `WS_CONFIG_HOME` pointing inside that scratch dir.
  3. Opens `ssh -N -o ExitOnForwardFailure=yes -L 127.0.0.1:<local>:127.0.0.1:<remote>`,
     waits for `GET /health` (matching the launched PID), and reads the scratch
     `endpoint.json` token without printing it.
  4. Exercises `POST /auth/cookie`, `GET /`, `GET /settings`,
     `GET /settings?frame=1`, and single-use `POST /auth/link` (`?login=<code>`),
     then runs macOS headless Chromium (resolved via `WS_PLAYWRIGHT_MODULE` or
     `~/.npm/_npx/*/node_modules/playwright`) against `/` and `/settings` to
     assert non-empty DOM, zero `pageerror` / console errors / HTTP 5xx / `401`
     or `403`, and live `/rail/events` WebSocket frame delivery.
  5. Always tears down in `finally` (and on `SIGINT`/`SIGTERM`): authenticated
     `POST /shutdown`, local tunnel termination, exact-PID `taskkill /PID <pid> /T /F`
     fallback if still running (never `/IM`), and removal of only the scratch dir.

```bash
# Default: cross-compile current worktree, run HTTP + Playwright checks, clean up
WS_WIN_HOST=<user>@<host> python3 scripts/win-tunnel-smoke.py

# HTTP-only (skip Playwright Chromium)
WS_WIN_HOST=<user>@<host> python3 scripts/win-tunnel-smoke.py --no-browser

# Keep tunnel open with a fresh one-time login URL until Ctrl+C
WS_WIN_HOST=<user>@<host> python3 scripts/win-tunnel-smoke.py --keep-open
```
