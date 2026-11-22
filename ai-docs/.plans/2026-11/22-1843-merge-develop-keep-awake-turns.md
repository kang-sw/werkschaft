# Merge develop into impl/develop/keep-awake-turns

Branch: impl/develop/keep-awake-turns at f323b6371 (keep-awake during agent turns: platform sleep assertion held while turns run, Windows clear retried once before retiring its thread; config knob agent.keep_awake_during_turns; reviewed LGTM).
Goal: merge origin/develop (>= ccc4b5910) into this branch so the integrator can merge it cleanly.
Known conflict: agents-plugin-tool/internal/wsagent/runner_process.go (content). Auto-merged but verify: internal/mcp/config_registry.go, server_config.go, wsagent/runner.go, runner_conv.go, wsdashboard settings catalog + config_routes.go, wsi18n tables (JSON must stay valid and key-complete across all locales).
Focused tests after merge: go test ./internal/wsagent -race -run 'KeepAwake|Process|Turn' ; go test ./internal/mcp -run 'Config|KeepAwake|SettingsUICatalog' (narrow); go test ./internal/wsi18n/... ; dashboard settings catalog checks the branch already used (see its commits' AI Context).

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
