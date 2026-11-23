# Plan: usage band shows the last value greyed out when stale

## Context
Owner decision (whiteboard (5112)=b, refined 02:20 KST): the top-gutter plan-usage band
currently replaces a stale window's number with an em dash ("주 —"). Claude only pushes
rate-limit events at session start and when the 7-day utilization rises 1%, so a stale
reading is usually still the current value. The owner wants the band to keep the last
value, greyed out, instead of the dash. The age ("N분 전") already exists in the tooltip
and the clock badge; keep both.

## Change
- `agents-plugin-tool/internal/wsdashboard/assets/usage.js`, footer band renderer
  (~lines 236-257, the `["five_hour", "seven_day"].forEach` in the band button):
  - When `noRecent` AND an observation exists (`at` truthy): render
    `Math.round(w.percent)` + the `%` span, exactly like the fresh branch, inside the
    existing `usage-stale` column (already muted via `usage.css:33`). Do not render the
    pace delta for stale columns. Keep the existing stale tooltip text.
  - When there is no observation at all (`!at`): keep the em dash.
- Do not change the usage panel cards (~line 393), the clock badge, the 30-minute stale
  threshold, or any Go code.

## Tests
- Update or extend the existing JS/browser test covering the band's stale state (grep
  `usage-stale` / `"—"` under `internal/wsdashboard/` tests and `testdata/`) so it asserts:
  stale with observation -> number + "%" visible, column has `usage-stale`, no
  `.usage-pace`; never observed -> em dash.
- Run from `agents-plugin-tool`, with
  `WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)`,
  `PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`, and env scrub
  `env -u WS_MCP_PROJECT_ROOT -u WS_MCP_HOSTED_PACKAGE -u WS_RSRC_ROOT -u WS_MCP_NAMESPACE -u WS_MCP_NO_AGENT -u WS_MCP_NO_DASHBOARD`:
  `go test ./internal/wsdashboard/ -run 'Usage|Band|PlanUsage' -count=1 -timeout 9m`
  plus any `node --test` file covering usage.js. Broad dashboard suites take ~4 minutes;
  do not interrupt before the timeout.
- Capture one PNG of the band in the stale state (light and dark) to
  `/tmp/usage-stale-last-value/` (never commit PNGs).

## Out of scope
Server-side plan_usage logic, history chart, freshness rules.
