# Fix dashboard shell tests broken by the Settings modal merge

## Problem

On develop `91b178e1c`, these fail locally (and will fail CI):

```
go test ./internal/wsdashboard -run 'TestBoardOverviewShortcutTargets|TestRailKeyFromDocFrame|TestShellSearchButtonClick' -count=1
```

Error: `ReferenceError: shSettingsEntry is not defined` (stack through
`shSearch`). The Settings modal merge `60377a909` added
`var shSettingsDialog = ..., shSettingsEntry = null;` in
`assets/dashboard-lifecycle.js` (~line 745) and references from functions the
tests extract (`overview_shortcut_test.go`, `rail_hotkey_test.go`,
`shell_search_test.go`) without the declaration.

## Steps

1. Confirm the cause by reading how each test extracts source.
2. Fix in the test harness (load or stub the declarations the extracted
   functions now need) when production code is correct. Change production
   code only if it is actually broken; then explain in the report and the
   commit's AI Context. Keep changes minimal.
3. Grep the other wsdashboard Go tests that extract `dashboard-lifecycle.js`
   functions for the same gap and fix them in the same commit.

## Constraints

- Read `ai-docs/manuals/performance.md` (frontend section) before editing assets.
- Never launch Electron; never stop or replace the running daemon; create
  no extra worktrees.
- Browser tests env: `WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)`
  and `PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`.

## Verification

From `agents-plugin-tool/`:

- the command above (all three pass)
- `go test ./internal/wsdashboard -count=1` (browser env): list remaining
  failures and whether each reproduces on develop `91b178e1c`.
- `GOOS=windows go vet ./internal/wsdashboard`
