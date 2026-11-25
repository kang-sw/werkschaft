# Fix CI Failures (`internal/werk`, `internal/wsholder`) and Ship Pre-flight Doc

## Scope

1. **Windows `internal/werk` open-handle cleanup (`quota_alert_test.go`)**
   - `TestAgentTurnEndEmitsUsageLimitFields` constructs an `*Agent` via `New(Config{Dir: t.TempDir(), ...})` which opens `<id>.jsonl` and its `.lock` file and starts the background `writer` goroutine, but never calls `a.Close()`. On Windows, `t.TempDir()` cleanup fails to delete the open `.jsonl` file.
   - Add `defer a.Close()` in `TestAgentTurnEndEmitsUsageLimitFields`. Audit all other `internal/werk/*_test.go` callers of `New`/`OpenStore` to confirm no other test leaks handles.
2. **Linux `internal/wsholder` `processAlive` zombie-reap race (`zombie_linux.go`)**
   - In `kill_unix.go`, `processAlive(pid)` runs `syscall.Kill(pid, 0)` and then `!processZombie(pid)`.
   - On Linux, `waitGone` can observe the exiting holder as a zombie (`Z`) and return while the `cmd.Wait()` goroutine is about to reap it. A subsequent `processAlive(pid)` call (as in `TestStopAllCountsAHolderWhoseCleanupFailedAsStopped`) can pass `syscall.Kill(pid, 0)` while still a zombie, then hit `ENOENT`/`ESRCH` reading `/proc/<pid>/stat` after `cmd.Wait()` reaps it; `processZombie` returned `false` on any read error, misreporting the reaped process as alive.
   - In `zombie_linux.go`, when `/proc/<pid>/stat` returns `os.ErrNotExist` or `syscall.ESRCH`, re-check `errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)` so a just-reaped process is reported not alive (while preserving `hidepid=2` live processes where `Kill` still returns `EPERM`). Also treat kernel dead state `X` in `zombieStat` as not alive, and cover it in `zombie_linux_test.go`.
3. **Ship Pre-flight doc (`ai-docs/ship/ws.md`)**
   - Add `cd agents-plugin-tool && python3 scripts/generate-package-assets.py && python3 scripts/generate-package-assets.py --check` immediately before `bash scripts/ci-test-shard.sh all` in Pre-flight so fresh release worktrees populate `internal/wspackage/generated/` before running the suite.

## Verification

- `cd agents-plugin-tool && go vet ./internal/werk/... ./internal/wsholder/... && GOOS=windows go vet ./internal/werk/... ./internal/wsholder/... && GOOS=linux go vet ./internal/werk/... ./internal/wsholder/...`
- `cd agents-plugin-tool && go test -count=5 -race ./internal/werk ./internal/wsholder`
