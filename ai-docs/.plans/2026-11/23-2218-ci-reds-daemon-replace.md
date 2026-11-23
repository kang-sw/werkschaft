# Fix CI reds from the daemon-replace merge

## Problem

CI run on develop `52b3d365c` (after merge `39043582a`, daemon-replace-gap
Phases 2-5) failed on ubuntu and windows:

1. Build failures: `t.Context undefined` - `go.mod` declares `go 1.23` and CI
   uses `go-version-file`, but new tests call `t.Context()` (Go 1.24+):
   - `internal/wsagent/runner_startup_test.go:97`
   - `internal/wsagent/worker_mcp_swap_test.go:121,152`
   - `internal/wsdashboard/ensure_electron_test.go:60,164`
   Grep the whole module for any other `t.Context()` / `b.Context()` and
   other post-1.23 APIs added in that merge range (`f75a179e6..39043582a`).
2. `cmd/ws-mcp` `TestHostedLeadsFirstToolListWaitsForTheUserCommandsAlone`:
   `usercmd_prod_test.go:170: the worker's initialize: tok-worker initialize = 401 unauthorized`.
   Reproduces locally on develop `91b178e1c` (run with the hosted-session
   variables unset: `env -u WS_MCP_PROJECT_ROOT -u WS_MCP_HOSTED_PACKAGE -u WS_RSRC_ROOT -u WS_MCP_NAMESPACE -u WS_MCP_NO_AGENT -u WS_MCP_NO_DASHBOARD`).
   Suspects in that merge: `1a0a04f73` (MCP access guard rejection logging),
   `6691d3b59` (worker MCP rejoin/authorization diagnosis),
   `8f59ff63d` (prepare replacement before draining). Confirm with the
   merge parent `f75a179e6` (use `git stash`-free means: e.g. `git worktree`
   is NOT allowed; instead check out the parent's versions of suspect files
   temporarily in this worktree, or bisect by reverting commits locally
   without committing) whether it passed before.
3. Windows-only `internal/wsindex` `TestA4UnreachableWithCacheIsBoundedAndPending`
   (`storage_test.go:168`, push "timed out"): read the test; report whether it
   is a timing flake unrelated to this merge (no change needed) or a real
   regression. Fix only if the cause is in this merge.

## Steps

- (1) Replace `t.Context()` with a context bound to the test
  (`ctx, cancel := context.WithCancel(context.Background()); t.Cleanup(cancel)`)
  or a small helper; do not bump `go.mod`.
- (2) Diagnose the 401. If production auth regressed (a worker token that
  should be accepted is refused), fix production minimally and add/keep the
  regression test; if the test fixture is stale against an intended behavior
  change, update the fixture and say why in the commit AI Context.
- Keep changes minimal; one commit per cause.

## Constraints

- Read `ai-docs/manuals/ws-mcp.md` and `ai-docs/manuals/release-compatibility.md`
  before changing auth behavior; state the compatibility tier if production
  changes.
- Never launch Electron; never stop or replace the running daemon; create
  no extra worktrees.

## Verification

From `agents-plugin-tool/`:

- `go vet ./...` with a Go 1.23 toolchain if available (`GOTOOLCHAIN=go1.23.0 go vet ./internal/wsagent ./internal/wsdashboard ./cmd/ws-mcp`); otherwise grep proof that no 1.24+ testing API remains.
- `go test ./cmd/ws-mcp -run TestHostedLeadsFirstToolListWaitsForTheUserCommandsAlone -count=5` (hosted vars unset)
- `go test ./internal/wsagent -run 'Startup|Swap|Rejoin' -count=3`
- `go test ./internal/wsdashboard -run 'Electron|Replace' -count=1`
- `GOOS=windows go vet ./internal/wsagent ./internal/wsdashboard ./cmd/ws-mcp`
