# Plan: test binaries scrub inherited WS_* environment

## Context
Peer-lead dogfood report: running `go test ./cmd/ws-mcp` inside a hosted ws session
without stripping WS_* let `TestForwardingWorkerRoutedBindingRefusesCrossHarnessAgy`
spawn a REAL claude child on the live daemon (it edited the worktree). Trace:
`newCallerBindingFixture -> cliHello` copies `os.Environ` without clearing
`WS_MCP_PI_TOKEN` / `WS_MCP_PI_ENDPOINT`; `proxyHost.Accept` withholds the fixture runner
when the token is present; `mcp.piForward` then uses the inherited pair. The cmd/ws-mcp
`TestMain` isolates HOME/cache/config but not WS_*. The same leak also fails ~8
cmd/ws-mcp tests in hosted sessions (known manual workaround: `env -u WS_MCP_PROJECT_ROOT
-u WS_MCP_HOSTED_PACKAGE -u WS_RSRC_ROOT -u WS_MCP_NAMESPACE -u WS_MCP_NO_AGENT -u WS_MCP_NO_DASHBOARD`).

Compatibility tier: T0 (test-only, no shipped surface).

## Change
In each TestMain below, before `m.Run()` (and before any existing isolation that may
set its own WS_* values), unset every environment variable whose name starts with `WS_`
that was inherited from the parent process:
- `agents-plugin-tool/cmd/ws-mcp/main_test.go`
- `agents-plugin-tool/internal/mcp/server_test.go`
- `agents-plugin-tool/internal/wsagent/standin_test.go`

Rules:
- Read each TestMain first. If it deliberately sets a WS_* variable for the test binary
  itself (or re-execs the test binary as a helper process that relies on a WS_* variable
  it sets), keep those: scrub inherited values first, then let the existing code set
  its own. Helper-process modes that detect themselves via an env var set by the parent
  test (re-exec pattern) must keep working: if TestMain branches on such a variable,
  perform the scrub only on the non-helper path, or exempt exactly that variable.
- Put the scrub in one small helper per package (e.g. `scrubInheritedWSEnv()`) with a
  one-line comment naming why (inherited hosted-session WS_* can reach the live daemon).
  Do not create a shared package for it.
- No production code changes.

## Tests
SAFETY: never let a test reach the live daemon. Every `go test` run below is prefixed with
`env -u WS_MCP_PI_TOKEN -u WS_MCP_PI_ENDPOINT WS_MCP_PI_TOKEN=bogus-test-token WS_MCP_PI_ENDPOINT=http://127.0.0.1:1`
so the dangerous pair is present but points at an unreachable port. Leave the other
inherited WS_* variables as they are (the scrub must handle them).

Add one regression test per package (e.g. `TestInheritedWSEnvIsScrubbed`) asserting that
`os.Getenv("WS_MCP_PI_TOKEN")` and `os.Getenv("WS_MCP_PI_ENDPOINT")` are empty inside the
test binary (plus any other WS_* the TestMain does not deliberately set).

Run from `agents-plugin-tool` with that prefix and WITHOUT the old env -u workaround:
- `go test ./cmd/ws-mcp -count=1 -timeout 20m`
- `go test ./internal/wsagent -count=1 -timeout 20m`
- `go test ./internal/mcp -count=1 -timeout 45m` (long; do not interrupt early)
Report pass/fail per package; for any failure, rerun that single test on unmodified
`develop` code via `git stash` comparison is NOT allowed - instead name the failing test
and paste its output; the lead compares against the known pre-existing reds list.
Also confirm no new agent was spawned: `TestForwardingWorkerRoutedBindingRefusesCrossHarnessAgy`
must pass and the worktree must stay clean apart from your commit.

## Out of scope
Fixing the fixture (`cliHello`) itself; other packages.
