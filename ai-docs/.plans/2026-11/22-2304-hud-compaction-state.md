# HUD hover card: compaction state line

## Outcome

Owner decision (5016)=b: the compact agent HUD hover card (just gained the
context bar, develop a4f2cd4be; `hudContextRow` in
`agents-plugin-tool/internal/wsdashboard/assets/dashboard-hud.js`) also shows
whether the agent is compacting right now and how many compactions it has had.
Mockup (text, commit it with the change):
`ai-docs/assets/mockups/261122/hud-context-bar/hover-card-v2-compaction.html`
(variants D compacting now, E idle with a count, F never compacted).

## Settled decisions

1. Placement: one new line `.tt-cmp` directly under the `.tt-ctx` context row.
   - Compacting now: an accent chip "압축 중" with a pulsing dot, then the count
     text.
   - Not compacting, count > 0: muted text only, e.g. "압축 3회 · 마지막 ws 자동".
   - Count 0 and not compacting: no line.
   - The header status pill is unchanged.
2. The trailing origin label names who triggered the latest compaction: ws auto
   (runner threshold), manual (a user/lead request through ws), or harness
   native (the harness compacted on its own). Show it only when the origin is
   known; otherwise the count alone.
3. Data: project from the runner's in-memory compaction state onto the child's
   agent row, the same way 635ae3007 carried ContextWindow/ContextCompaction
   (`wsagent.State` -> `SpawnAgentEvent` in `spawn_live.go` -> `wslive.Event` /
   `wslive.Agent` -> `HUDNode`):
   - compacting now: the runner's queued/running compaction state (see
     `compaction.go` around `compactPending`, `compacting`, `compactCommand`,
     ~378-400 and ~469-490);
   - count + latest origin: count completed compactions of the child's
     conversation; reuse an existing counter if the runner or the session
     snapshot already keeps one (check `wslive` usage `Compaction.Milestones`
     and the compact records), else keep one in memory on the runner.
   - New JSON fields are `omitempty` (T0); no new file I/O on the HUD render
     path or per event (performance.md). A linked node may use its own session
     snapshot's figures when the HUD already read that snapshot (same
     precedence as the context bar).
4. Attribution bug (explorer finding, fix it in this change): ws auto
   compaction requests reportedly land on the compact record as the harness's
   "manual" trigger (compaction.go ~388 sets `TriggerAuto` on the start event,
   but the recorded completion may take the harness's own trigger). Make the
   recorded origin of a ws-auto compaction "auto" end to end, and cover it with
   a test. If this turns out already correct on develop, say so with evidence.
5. Text via i18n keys (`js.hud.*`, every table, as the i18n tests require).
   ko: "압축 중", "압축 {0}회", "마지막 ws 자동" / "마지막 수동" /
   "마지막 하네스". en equivalents.
6. The card updates in place on HUD refresh while hovered (follow the
   context-bar fields in the keyed node data; reset absent fields).

## Verification

- Go tests: runner state -> row projection (compacting toggles on/off; count
  increments on completion; origin auto/manual/native), the attribution fix,
  HUD projection of the new fields, JSON omitted when zero.
- `go test ./internal/wsagent ./internal/wslive ./internal/wsdashboard
  ./internal/wsi18n -count=1` (wsdashboard full suite ~9 min) and `go vet`
  (darwin + GOOS=windows).
- Node/browser HUD tests extended for D/E/F (existing `hud.browser.cjs`,
  `agents-island.test.cjs`, `agent_popover_browser_test.go` capture path with
  `WS_HUD_SHOTS`).
- Headless captures of D/E/F, dark and light, under
  `ai-docs/assets/captures/261122/hud-compaction-state/` (PNG, NOT committed);
  report the paths.

## Boundary

HUD hover card + the runner/row plumbing it needs + the attribution fix. Do
not change the status band, the HUD node buttons, the agent popup list, or
compaction behavior itself (thresholds, when it fires). Do not edit
`codex_mapper.go` beyond what attribution strictly needs. Another worker is
editing harness-name branches in wsagent (impl/develop/yoga-fever-sip): keep
wsagent edits minimal. Cached Playwright only; never launch Electron or any GUI
app. Do not create checkouts with raw `git worktree add`. Commit on
`impl/develop/hud-compaction-state`; never merge or push. No session keys,
local absolute paths, or PIDs in commits.
