---
title: "대시보드 컴포저 우하단 세션 누적 사용량·추정 비용(Stats)"
related:
  261120-feat-ws-dashboard-numbers-screen: "reuses the shared cumulative token bucket type (storage stays separate: live snapshot vs daemon SQLite)"
---

# Composer footer cumulative session usage and estimated USD cost (Stats)

## Background

The dashboard currently surfaces session cost (`wslive.Usage.Cost` via
`BandView.Cost`) only inside rail hover cards, and only for harnesses that
report a native USD number (Claude and Pi). Codex and agy emit token telemetry
without USD, and no per-session cumulative token breakdown, per-model cost
split, or subtree child rollup is visible from the active conversation view.

The owner requested a compact cumulative session usage and estimated USD cost
("Stats") indicator at the bottom-right of the conversation composer footer
(`.cfoot`), opening a detail popover that works uniformly across all four
harnesses (Claude, Codex, agy, Pi) and supports viewing either the current
session or the session plus its child subagents.

## Decisions

- **Trigger presentation (`(93)=a`):** Display a slim cost chip at the
  bottom-right of `.cfoot`: `$1.23` (native USD), `~$1.42` (estimated via
  pricing table), `~$0.48+` (estimated with at least one unpriced model), or
  `<N>K tok` fallback when every observed model is unpriced. Hide the chip
  cleanly when a brand-new session has zero recorded usage.
  - *Rejected:* Inline cache-hit badge in the footer (`(93)=b`, too noisy at
    narrow widths); static `stats` text button without an inline figure
    (`(93)=c`); cost-only display without token/model breakdown or child rollup
    (`(93)=d`).
- **Popover content (`(93)=a`):** Clicking the trigger toggles a popover above
  the composer footer showing:
  1. Headline USD cost with source/estimate badge (`Native`, `Estimated`, or
     `Unpriced` / partial `+`), total tokens, and turn/call count.
  2. Cumulative token breakdown: `input`, `cache_read`, `cache_write`, `output`,
     `reasoning`, and cache-hit percentage.
  3. Per-model breakdown (model ID, total tokens, and per-model USD or
     unpriced badge).
  4. Scope toggle: **This session** vs **Include children** (subtree rollup).
- **Hybrid cost & usage model (`(93)=a`):** Prefer native USD when the harness
  provides it (Claude, Pi); collect cumulative per-model token buckets across
  all four harnesses in a shared `wslive` struct; compute estimated USD from a
  shared pure Go pricing package (`internal/wspricing`) when native USD is
  absent (Codex, agy), degrading gracefully to `Incomplete` / token-only when a
  model ID (e.g., an internal agy codename such as `gemini-4-neon-high`) is not
  in the table.
- **Pricing table maintenance & staleness visibility (`(94)=a`):** Ship a pure,
  zero-I/O `internal/wspricing` package with a built-in hardcoded table and an
  explicit `as-of` date surfaced in the popover's estimate badge tooltip so
  stale prices are visible.
  - *Rejected / deferred:* User-configurable price overrides in v1 (deferred to
    a later phase).
- **Approved visual reference (`(93)=a`, `(94)=a`):**
  `ai-docs/assets/mockups/261122/composer-session-stats/mockup.html`.
- **Two-phase ownership split & release sequencing (`(94)=a`):** Lands after
  `0.51.0` stable. Phase 1 covers the shared `wslive` schema,
  `internal/wspricing`, and `wsagent` 4-harness accounting; Phase 2 covers the
  `wsdashboard` view model, `.cfoot` chip, popover, i18n, and visual
  verification against the mockup (Phase 2 may start against fixture snapshots
  once Phase 1's shared struct merges).
- **Shared type without coupled storage (`(94)=a`):** Align cumulative token
  bucket field names so `261120-feat-ws-dashboard-numbers-screen` can reuse the
  same Go type later, while keeping storage strictly separate (live per-session
  snapshot here vs daemon-global SQLite in Numbers).
- **Compatibility tier & downgrade-only exception (`(95)=a`):** Ship additive
  `omitempty` cumulative usage fields on `wslive.Usage` in a `0.51.x` patch
  under an explicit owner-approved downgrade-only exception (following
  `ai-docs/manuals/release-compatibility.md` Scenario 12 and `(1855)=a`
  precedent):
  - *Forward upgrade (`0.51.0` -> `0.51.x`):* `wsdashboard/ensure.go:210-217`
    retains the newer numeric core daemon, so no older writer touches
    `snapshot.json`; older read-only JSON consumers ignore unknown `omitempty`
    fields.
  - *Manual downgrade (`0.51.x` -> older `0.51.0`):* `wslive.ReadSnapshot`
    (`wslive/snapshot.go:449-460`) decodes into a typed struct, `newLiveWriter`
    hydrates only `Context`/`Cost`/`Limits`/`LimitsAsOf`
    (`wsagent/record.go:450-468`), and `flushSnapshot` replaces `snapshot.json`
    whole (`wsagent/record.go:646-681`), dropping the newly added cumulative
    fields for active sessions.
  - *Re-upgrade outcome:* Reopening with the newer daemon restarts cumulative
    display counters cleanly from the next harness total or zero and never
    produces negative deltas; no workflow or conversation data is lost.
  - *Rejected:* Gating behind a `T3` minor boundary (`0.52.0`, disproportionate
    plugin-session lockout for a display-only live counter); adding a separate
    per-session sidecar file (extra Windows antivirus file touches under
    `performance.md`); deferring disk persistence until SQLite live-snapshot
    migration.
- **Codex accounting source & process-launch baseline (`(96)=ok`):** Use
  `thread/tokenUsage/updated` `tokenUsage.total` (never sum `tokenUsage.last`)
  for Codex cumulative accounting, and key per-process delta baselines on
  process/holder launch identity `(HolderID, HostPID)` rather than resetting on
  daemon reattach (`wsagent/runner_process.go:545-596,605-625`). Phase 1 runs
  explicit live `--resume` probes for both Claude (`result.total_cost_usd` &
  `modelUsage`) and Codex (`tokenUsage.total`) before finalizing baseline reset
  logic.
  - *Rejected:* Summing Codex `tokenUsage.last` (proven to overcount in
    `testdata/codex-0.160.0-hosted-lead.jsonl:53,68,79`, where `last` changes
    while `total` stays constant); resetting baselines on daemon restart
    (causes double-counting on same-holder reattach).
- **Child rollup descendant scope & memory-only cache (`(97)=ok`):** Compute
  `Include children` across all retained descendants
  (`Registration.ParentReplyID` in `registry.go:137-142` /
  `wsagent/children.go:880+`, including completed and waiting children within
  `RegistryRetention`), reading exclusively from in-memory cached snapshots
  (`snapshot_cache.go:121-134` `lastKnown`, warmed at daemon startup and on
  child close) with zero disk reads on interactive page loads.
  - *Rejected:* Limiting rollup to currently-running HUD nodes
    (`pageview_hud.go:52-64,86-147`, which drops completed worker costs upon
    turn finish); calling `SnapshotCache.Snapshot` (`snapshot_cache.go:245-270`)
    per child on page load (causes disk reads on cache misses, violating
    `performance.md` Rule 5).
- **Composer mount outside SSE-replaced `r-sline` (`(98)=ok`):** Mount the
  `.cfoot` stats trigger and popover outside SSE-replaced `r-sline` (following
  the `.fpills` / `#agpop` pattern in `assets/dashboard.tmpl:25-26,302-316` and
  `dashboard-composer-status.js:90-206`), updating DOM state via JS from
  structured live data so SSE ticks never disrupt focus or tear down an open
  popover.
  - *Rejected:* Rendering the interactive trigger or popover inside SSE-swapped
    `r-sline` (`pages.go:153-165`, `page_regions.go:118-120`, and asserted
    empty by `pageview_band_test.go:133-150`).

## Constraints

- **Release compatibility:** Follow `ai-docs/manuals/release-compatibility.md`
  (applicable to `agents-plugin-tool/internal/`). Honor the owner-approved
  `0.51.x` downgrade-only exception (`(95)=a`), ensuring re-upgrade cleanly
  restarts missing cumulative counters from zero or the next harness total
  without negative deltas.
- **Performance & Windows file-touch budget:** Follow
  `ai-docs/manuals/performance.md` (applicable to `agents-plugin-tool/`).
  Live snapshots are Storage Type 2 (small mutable state). Keep counters in
  memory, hydrate only from the record/snapshot the runner already loads on
  rejoin (`wsagent/record.go:450-468`), piggyback persistence on existing
  snapshot flushes (`wsagent/record.go:646-685`) with zero new interactive-path
  disk reads, and compute `Include children` strictly from in-memory `lastKnown`
  snapshots (`snapshot_cache.go:121-134`).
- **Shipped-surface boundary:** Follow
  `ai-docs/manuals/shipped-surface-boundary.md` for any strings emitted to
  downstream projects or dashboard i18n tables.
- **i18n parity:** Add all new UI strings across all 11 locale tables under
  `agents-plugin-tool/internal/wsi18n/tables/{de,en,es,fr,it,ja,ko,pt-BR,ru,zh-Hans,zh-Hant}.json`.
- **Release sequencing:** Do not land on `develop` before `0.51.0` stable cuts.
- **Scope boundary (v1):** Keep `internal/wspricing` pure hardcoded Go data
  with zero file I/O; user-configurable price overrides remain out of scope for
  v1.

## Evidence (2026-11-22 audit on `develop`)

- `wslive/snapshot.go:130-156,198-211,250-258,413-420`: `Snapshot.Usage *Usage`
  (`json:"usage,omitempty"`), `UsageCost{USD float64, Estimate bool, Incomplete bool}`,
  and existing `TokenUsage{Input, Output, CacheRead, CacheWrite int64}` (`input`,
  `output`, `cacheRead`, `cacheWrite`).
- `wslive/state.go:435,1385-1471`: `KindStatus` replaces whole `Usage` via
  `normalizeUsage` and `cloneUsage`; any new cumulative field must flow through
  event normalization, cloning, and `wsagent/record.go:450-468` hydration.
- `wsagent/runner_process.go:545-596,605-625,920-936`: `HolderID`/`HostPID`/
  `HolderSeq` track process identity; same-holder daemon reattach replays
  without launching a new process, while a new launch updates identity and
  resets `HolderSeq`. Replay cursor + accounting atomicity must prevent
  double-counting on reattach.
- **Claude:** `claude_protocol.go:211-214,234-243` decodes `result.total_cost_usd`
  and only `contextWindow` from `modelUsage`; `claude_events.go:252-258`
  currently marks `UsageCost` with `Estimate: true`; `--resume` is wired at
  `claude.go:151,242` (live `--resume` `total_cost_usd` accumulation behavior
  unproven in fixtures -> requires Phase 1 probe).
- **Pi:** `testdata/pi-abort-resume.jsonl:25` carries `tokens.{input,output,cacheRead,cacheWrite,total}`,
  `cost`, and message/tool counts; `pi_events.go:147-155,259-276,322-326,407-414`
  and `pi_event_details.go:219-239` currently discard token/message counts.
- **Codex:** `codex_mapper.go:640-656` decodes only `tokenUsage.last` and drops
  `tokenUsage.total`; `testdata/codex-0.160.0-hosted-lead.jsonl:53,68,79` shows
  `last` changing while `total` stays constant; `testdata/codex-0.160.0-resume.jsonl:15,30,41`
  starts resumed thread with non-zero `totalTokens` (requires paired pre/post
  restart live probe in Phase 1).
- **Agy:** `testdata/agy-1.5.12-compaction-probe-1.jsonl:4,11,14,18` carries
  per-turn `inputTokens`, `outputTokens`, `cachedReadTokens`,
  `cachedWriteTokens`, `thoughtTokens`, `totalTokens` and internal model IDs
  such as `gemini-4-neon-high`; `agy_output_rate.go:182-211` currently uses
  `outputTokens - thoughtTokens` only for TPS; active model comes from
  `agy.go:865-868,1193-1222,1241-1266`.
- **Dashboard:** `pageview_band.go:26-36,95-120,280-288` formats `BandView.Cost`
  (`$%.2f`, `~` prefix when `Estimate`, `+` suffix only when `Estimate && Incomplete`).
  `assets/dashboard.tmpl:25-26,274-279,302-316`, `dashboard-conversation.css:272-273,363-364,620-625,654-655`,
  and `dashboard-composer-status.js:90-206` establish the `#agpop` toggle/pin/Escape/outside-click
  pattern outside SSE-replaced `r-sline`.

## Agreed Interface Sketch

```go
// Shared cumulative usage types in internal/wslive (reusable by Numbers screen).
type TokenBuckets struct {
    Input      int64 `json:"input,omitempty"`
    CacheRead  int64 `json:"cacheRead,omitempty"`
    CacheWrite int64 `json:"cacheWrite,omitempty"`
    Output     int64 `json:"output,omitempty"`
    Reasoning  int64 `json:"reasoning,omitempty"`
}

type ModelUsageSummary struct {
    Tokens   TokenBuckets `json:"tokens"`
    CostUSD  float64      `json:"costUsd,omitempty"`
    Estimate bool         `json:"estimate,omitempty"`
    Unpriced bool         `json:"unpriced,omitempty"`
    Turns    int          `json:"turns,omitempty"`
}

type CumulativeUsage struct {
    Tokens      TokenBuckets                  `json:"tokens"`
    Turns       int                           `json:"turns,omitempty"`
    ByModel     map[string]*ModelUsageSummary `json:"byModel,omitempty"`
    PricingAsOf string                        `json:"pricingAsOf,omitempty"` // YYYY-MM-DD when estimated
}
```

## Phases

### Phase 1: Shared `wslive` cumulative usage schema, `internal/wspricing`, and 4-harness `wsagent` accounting

*(Owned by main lead / backend worker.)*

- **Goals:**
  1. Add `TokenBuckets`, `ModelUsageSummary`, and `CumulativeUsage` onto
     `wslive.Usage` (`omitempty`), wiring them through `wslive/state.go`
     (`KindStatus`, `normalizeUsage`, `cloneUsage`) and `wsagent/record.go`
     rejoin hydration (`newLiveWriter`) so counters hydrate from existing
     already-loaded snapshots and persist on existing flushes with zero extra
     disk reads/writes. Handle missing cumulative fields cleanly on re-upgrade
     (restart from zero or the next harness total; never emit negative deltas).
  2. Create pure, zero-I/O package `agents-plugin-tool/internal/wspricing`
     holding the built-in per-model API unit-price table, `AsOfDate()` metadata,
     normalization/prefix lookup, and
     `Estimate(modelID string, buckets wslive.TokenBuckets) (usd float64, priced bool)`.
     Unknown/internal model IDs return `priced = false` so callers mark
     `Unpriced: true` / `UsageCost.Incomplete = true`.
  3. Run explicit live `--resume` probes for Claude (`result.total_cost_usd` &
     `modelUsage`) and Codex (`thread/tokenUsage/updated` `tokenUsage.total`)
     across process restarts and record the verified semantics in the Phase 1
     Result before finalizing delta-baseline reset logic.
  4. Wire all four harness mappers to emit cumulative per-model usage and
     prefer native USD where available:
     - **Claude:** Decode full `modelUsage[model]` token buckets and `costUSD`;
       keep native `result.total_cost_usd` (`Estimate: false` when native USD is
       authoritative); apply per-process delta normalization keyed on
       `(HolderID, HostPID)` according to probe findings.
     - **Pi:** Decode `get_session_stats` cumulative `tokens` and message/turn
       counts alongside native `cost`; apply per-process delta normalization
       keyed on `(HolderID, HostPID)`.
     - **Codex:** Decode `tokenUsage.total` (never sum `tokenUsage.last`) and
       active model; apply per-process delta normalization keyed on
       `(HolderID, HostPID)` according to probe findings; compute estimated USD
       via `internal/wspricing` (`Estimate: true`, `Incomplete: true` if
       unpriced).
     - **Agy:** Accumulate per-turn `session/prompt` `usage` buckets keyed by
       active model ID (`models.currentModelId` / `configOptions`); compute
       estimated USD via `internal/wspricing` (`Incomplete: true` or unpriced
       token-only when internal codenames are not in the pricing table).
  5. Ensure runner replay/reattach (`HolderID`/`HostPID`/`HolderSeq`) preserves
     accounting/cursor atomicity so reattaching to the same process never
     double-counts or produces negative deltas.
- **Verification:**
  - Unit tests for `internal/wspricing` (exact match, versioned suffix/alias,
    cached-read/write/reasoning pricing, unknown/internal model -> unpriced,
    `AsOfDate()`).
  - Unit & fixture tests for `wslive` normalization/clone/hydration (including
    empty/downgrade-stripped snapshot re-upgrade handling) and all four
    `wsagent` mappers (restart/resume baseline reset, daemon reattach
    idempotency, non-negative delta guard, Codex `last`-without-`total` guard).
  - `go test ./internal/wslive/... ./internal/wspricing/... ./internal/wsagent/...`
    and `go vet ./...`.

### Phase 2: Dashboard `.cfoot` Stats chip, detail popover, child rollup, and i18n

*(Owned by UI lead; depends on Phase 1's shared `wslive` types landing on `develop`, and may start against fixture snapshots once those types merge.)*

- **Goals:**
  1. Build the `wsdashboard` stats view model from live session snapshots,
     including the in-memory `Include children` subtree rollup over all
     retained descendants (`Registration.ParentReplyID` /
     `wsagent/children.go:880+`, alive + finished/waiting within
     `RegistryRetention`) using no-disk `lastKnown` cached snapshots
     (`snapshot_cache.go:121-134`, warmed at daemon startup and on child close).
     Sum token buckets, per-model maps, turns, and costs, propagating
     `Estimate` / `Incomplete` flags accurately across parent and children.
  2. Mount the A-style slim cost chip and toggleable detail popover outside
     SSE-replaced `r-sline` at the bottom-right of `.cfoot` (following `.fpills`
     and `#agpop` in `assets/dashboard.tmpl:25-26,302-316` and
     `dashboard-composer-status.js:90-206`), keeping `r-sline` empty so
     `pageview_band_test.go:133-150` remains intact and SSE ticks never disrupt
     focus or close an open popover.
     - Trigger text: `$1.23` (native), `~$1.42` (estimated), `~$0.48+`
       (estimated + unpriced model), `<N>K tok` (all models unpriced); hidden
       cleanly when a session has zero recorded usage.
     - Popover interactions: click toggle, pin, `Escape`, outside-click close,
       keyboard focusable.
  3. Match the approved interactive mockup
     `ai-docs/assets/mockups/261122/composer-session-stats/mockup.html`,
     including:
     - Headline USD + source/estimate/unpriced badge with `PricingAsOf` date in
       the estimate badge tooltip.
     - Token breakdown grid (`Input`, `Cache read`, `Cache write`, `Output`,
       `Reasoning`, `Cache hit %`, turns).
     - Per-model breakdown list with per-model USD or `Unpriced` badge.
     - `This session` / `Include children` segmented toggle (showing child count
       when descendants exist; disabled or omitted cleanly when none exist).
     - Responsive layout at both normal width and `320px` narrow pane without
       horizontal overflow or colliding with `.fpills`.
  4. Add all new UI copy across all 11 locale tables under
     `agents-plugin-tool/internal/wsi18n/tables/*.json`.
- **Verification:**
  - Go tests for view-model formatting, unpriced fallbacks, child rollup math
    (mixed native + estimated + unpriced children, completed + running
    descendants, zero-disk-I/O `lastKnown` path), CSS class collision check,
    and i18n parity (`go test ./internal/wsi18n/... ./internal/wsdashboard/...`).
  - Browser / Playwright verification covering dark theme, light theme, `320px`
    narrow pane, unpriced agy fallback, and `Include children` toggle — with
    side-by-side visual comparison of result captures against
    `ai-docs/assets/mockups/261122/composer-session-stats/mockup.html`
    (0 console errors, 0 horizontal overflow).
