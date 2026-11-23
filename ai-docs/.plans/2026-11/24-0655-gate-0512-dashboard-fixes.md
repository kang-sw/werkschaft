# Plan: 0.51.2 release-gate dashboard fixes (Important #3-#6)

Authority: release-gate review findings for ddf6d78bf..85c646162
(`/Users/kang-sw/.cache/ws@kang-sw-devenv/proj/17da6bdc/review-paths/8c197a5a-01-findings.md`,
Important 3-6; reviewer detail in `d5435358-01-findings.md` and `8b7173c1-01-findings.md`
in the same directory). Owner chose to fix these before 0.51.2.

Scope boundary: only the files below plus their tests. Do not touch
dashboard-board*.js, dashboard-conversation.js, dashboard-tracker.js (another
session owns them), CHANGELOG.md, ws-mcp.md, or wsreview (another worker).

## Steps

1. #3 `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-preferences.js:42`
   - The slot editor shows the All-harness legacy preference while the old
     editor hides harness-specific entries (`dashboard-settings.js:1170`).
     Migrating the All value writes an unscoped default that overrides a
     distinct legacy harness-specific value (`internal/mcp/config_preferences.go:38-49`).
   - Fix: keep harness-specific values visible, and guard migration when one
     slot would replace distinct per-harness preferences (refuse or keep the
     harness entries; do not silently override).
   - Test: configured All + distinct harness-specific pair, through preview
     and apply.
   - Read `ai-docs/manuals/config-settings-ui.md` before editing.
2. #4 `assets/dashboard-composer-exec.js:588`
   - An aborted or invalid JSON body in the exec-jobs poll rejects after fetch
     resolved, leaving `histPollInflight` set until reload.
   - Fix: clear the latch and resume bounded polling on both request and body
     failure.
   - Test: a rejected body followed by a valid response.
3. #5 `assets/dashboard-audit-rows.js:93`
   - The approval owner classifier misses Antigravity's
     `request:session/request_permission` audit record.
   - Fix: recognize the actual adapter subkind (verify it in the agy audit
     adapter source).
   - Test: pairing it with the live approval by call ID.
4. #6 `assets/dashboard-audit-rows.js:765`
   - A non-2xx inline audit fetch resolves null and marks the preview loaded.
   - Fix: track success separately from in-flight so reopening after a
     failure refetches.
   - Test: reopen after a failed HTTP response.
5. Make sure each new test runs under `go test ./internal/wsdashboard`,
   through an existing Node/browser fixture wrapper pattern.
6. Commit one logical commit per finding, or one commit if they are trivially
   small, with `## AI Context`.

## Verification (the lead reruns these)

- `cd agents-plugin-tool && env -u WS_MCP_PI_TOKEN -u WS_MCP_PI_ENDPOINT WS_MCP_PI_TOKEN=bogus-test-token WS_MCP_PI_ENDPOINT=http://127.0.0.1:1 go test -count=1 ./internal/wsdashboard -run '<the touched tests>'`
- Browser tests need `WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)` and `PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`.
- Run the full `./internal/wsdashboard` package once (timeout 20m). Report any failure with its name. `TestDashboardSettings*` and `TierRouting*` need `generated/inventory.json`. Generate it with `python3 agents-plugin-tool/scripts/generate-package-assets.py` if needed. Do not commit generated output that is gitignored.
- `git diff --check`.
- Frontend changes follow `## Frontend Minimal Updates` in `ai-docs/manuals/performance.md`.

No merge, no push, no daemon swap, no Electron.
