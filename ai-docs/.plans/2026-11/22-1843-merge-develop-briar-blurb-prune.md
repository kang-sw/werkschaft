# Merge develop into impl/develop/briar-blurb-prune (+ deferred exec-job follow-up)

Branch: impl/develop/briar-blurb-prune at 16d226728 (ticket 261121 dogfood tool surprises; worktree.release fence/lock and release-notice outcome reporting; reviewed LGTM in 4 rounds).
Goal 1: merge origin/develop (>= ccc4b5910) into this branch.
Known conflicts: agents-plugin-tool/internal/mcp/server_dispatch.go, worktree_release_lock.go, worktree_tools.go. Develop now carries the ws exec tools work (impl/develop/ninja-press-visor merged as ae3670e76) including a worktree.release exec-job guard (CheckExecJobs). Integrate develop's exec-job guard with this branch's release fence/lock so both apply: the exec-job check must run inside (or be ordered consistently with) this branch's fence, and neither side's refusal is lost.
Goal 2 (deferred from 261121 Phase 2, now unblocked): this branch deferred "the exec-job acquisition predicate wired to the release fence" and the test "a live exec job refuses a forced release" until exec P2 landed. After the merge, check whether develop's guard already makes worktree.release with force:true refuse while a live exec job holds the worktree, and whether a test covers it. If covered, cite the code and test file:line in the report and change nothing. If not, wire the predicate into the fence and add that test (minimal change). Record the decision in a separate commit's AI Context (or the merge commit if no code change).
Focused tests: go test ./internal/mcp -race -run 'WorktreeRelease|ReleaseLock|ReleaseNotice|ExecJob|Dispatch' (narrow; widen only by explicit test names), go build ./..., go vet ./internal/mcp.

Constraints (binding):
- Work only in this worktree. Never touch /Users/kang-sw/devenv (main checkout). No `git worktree add`, no `git pull`, no rebase, no push, no force operations.
- Integrate with `git merge origin/develop` (a merge commit; no rebase). Run `git fetch origin` first. The integrator reports develop at ccc4b5910 or later.
- Resolve conflicts preserving BOTH sides' intent: develop's behavior must survive unchanged unless it directly contradicts this branch's ticket; this branch's reviewed behavior must survive. Explain each hunk resolution in the merge commit's `## AI Context`.
- If a resolution needs a design decision neither side settles, stop and report `ask-user` with the options.
- Tests: prefix every go command with GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local and use narrow -run filters. Never run all of ./internal/mcp or ./internal/wsdashboard. `go build ./...` and `go vet` on touched packages are required. Run with -race for the touched wsagent tests.
- If the merge touches shipped rsrc (agents-plugin/rsrc, agents-plugin-wsflow, skills), regenerate Pi bundles in agents-plugin-pi: `python3 scripts/prepare-workflow-bundles.py` and `python3 scripts/prepare-native-payload.py --first-party-only --offline` (check exact script paths in agents-plugin-pi), then their --check.
- Never launch Electron, never run the real agy binary, never ssh anywhere, never touch credentials/Keychain, never kill processes or stop daemons.
- No Co-Authored-By trailer on your commits. Repo is public: no session keys, PIDs or local paths in commit messages.
- Report the new head sha, the list of conflicted files and how each was resolved, and the exact test commands with results.
