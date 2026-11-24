# Home session handoff (2026-11-24)

Stopped on the owner directive in 7c1538220 / repo note
`home-agent-stop-handoff-261124`. Nothing below was merged into or pushed to
develop after the stop.

## State

- Release-gate re-review of `cee60ac89..247f6c724`: block (ledger a2e6b3248).
  Cleared prefix `cee60ac89..41e81115c`. Findings were routed to two tickets
  (both ready on develop at 436537ba5).
- `261124-bug-ws-0520-release-gate-final-blockers` (harness session):
  implemented on `impl/develop/plant-step-chuck` (first parent of this
  handoff commit), ticket closed on that branch, develop merged into it at
  2bdf4a5ab (merge of develop 34cdad4f2 into 0a6371574). Not merged into
  develop. Verified on 0f4e40110 (all fixes, before the develop merge): full
  `go test ./internal/mcp -timeout 45m` pass (706 s); wsagent, wsfs, wscron,
  cmd/ws-mcp, `wscred -race`, focused wsdashboard pass; native Windows wsfs,
  wsstate, wsmeta, wscred, focused wsdashboard and focused mcp FS/credential
  runs pass. On 2bdf4a5ab: build, vet, GOOS=windows vet, wsagent, wsholder,
  wsfs, wscron, cmd/ws-mcp, focused wsdashboard pass; the full mcp rerun was
  stopped by the directive and is NOT verified (wsagent changed via the
  develop merge, runner_conv.go overlap merged cleanly).
- `261124-bug-ws-0520-final-dashboard-fixes` (UI/UX session):
  `impl/develop/fix-0520-browser-ci-red` at f07dd1c10 (second parent of this
  handoff commit, recorded with the `ours` strategy, so its content is NOT in
  this tree). Base cd8859297. Reported: independent review pass; full
  `./internal/wswhiteboard` pass; full `./internal/wsdashboard` (Chromium,
  ~32 min, needs `-timeout 45m`) pass for all 17 target tests, two unrelated
  flakes passed on rerun; lead screenshot check pass. It conflicts with
  develop's Designated-Y free-canvas scroll (496eb4e35, 63030ac5c) in
  `dashboard-board-anchor.js` and `testdata/board-anchor.test.cjs`; resolve
  when merging.
- Remaining steps toward 0.52.0: merge both, re-review
  `41e81115c..<fix head>`, cut 0.52.0 (fold the stale CHANGELOG Unreleased
  block), CI on the exact SHA; publish after the owner's live checks
  (43) OpenRouter, (45) SIWC, (46) vLLM.
- Note: `agents-plugin-tool/scripts/ci-test-shard.sh` runs packages with
  `-timeout 30m`; the full Chromium `internal/wsdashboard` run now takes ~32
  min serially, so check the CI shard split before the release CI.
