---
title: "werk 사용량 지표: OpenRouter 잔고·모델별 집계 및 OpenAI/Codex 통합"
related:
  261120-feat-ws-dashboard-numbers-screen: shares long-horizon per-model SQLite usage telemetry discipline
  261122-feat-ws-dashboard-session-stats: shares per-model token/cost accounting and composer stats UX
  261113-feat-ws-dashboard-harness-auth-slots: future multi-slot account switcher integration (harness-account-switcher)
---

# Werk Usage Metrics: OpenRouter Balance/Spend & OpenAI/Codex Unification

## Background

The dashboard topbar usage strip (`/rail/usage`, `wsdashboard/plan_usage*.go`,
`assets/usage.js`) currently displays quota windows for Claude, Codex CLI / Pi,
and Antigravity (`agy`), leaving `werk` providers (OpenRouter and OpenAI SIWC)
untracked in the usage strip.

Three gaps prevent accurate `werk` usage visibility today:

1. `wscred/openrouter_verify.go` probes `GET https://openrouter.ai/api/v1/key`
   only to classify key verification (`verified` / `rejected` / `unverified`),
   surfacing neither `/api/v1/credits` account balance nor `/api/v1/key` rate
   limits and daily/weekly/monthly spend.
2. `werk.Line` (`LineUsage` in `werk/protocol.go`, emitted by `Agent.report` in
   `werk/agent.go`) does not carry `Model` (`Response.model`), and `werk`
   compaction (`werk/compaction.go`) does not emit durable per-model usage
   records.
3. For OpenAI SIWC (`wscred/openai_oauth.go`, `wscred/openai_store.go`), it is
   not yet confirmed whether live SIWC tokens (`resource=https://api.openai.com/v1`,
   `scope=... chatgpt.tokens.use.direct`) include workspace/account claims or
   are accepted by remote quota endpoints, nor is there a workspace-ID-gated
   rule deciding when `werk` OpenAI merges with `codex` CLI (`~/.codex/auth.json`)
   versus rendering its own card.

## Decisions

*(Settled on whiteboard `(1775..1777)=a`, `(1781)=a`)*

- **Hybrid OpenRouter metrics and per-model SQLite deltas (`(1775)=a`):**
  - Display `/api/v1/credits` account balance (`total_credits`, `total_usage`)
    when permitted, paired with `/api/v1/key` per-key limits and
    daily/weekly/monthly spend as fallback/companion.
  - Keep key verification (`wscred/openrouter_verify.go` `checkOpenRouter`)
    strictly anchored on `/api/v1/key` `2xx` status so a `/credits` `401` or
    `403` (for example on a non-management key) never marks a valid inference
    key as `rejected`.
  - Fix `werk` `LineUsage` (`werk/protocol.go`, `werk/agent.go`) to carry
    `Model` (`Response.model`) and persist compaction usage
    (`werk/compaction.go`), and aggregate per-model `CostUSD` and token counts
    via `wsmeta` SQLite deltas (`performance.md`: zero scanning of conversation
    transcript files on `/rail/usage` reads, and no per-event file-touch churn
    on Windows).
- **Workspace/account-ID gated `Codex / OpenAI` card unification (`(1776)=a`):**
  - Unify `werk` OpenAI (SIWC) and `codex` CLI usage cards **only** when
    workspace/account IDs match (`~/.codex/auth.json` `tokens.account_id` ==
    `werk` JWT workspace/account ID -> single `Codex / OpenAI` card).
  - When IDs mismatch or cannot be verified, render separate `Codex CLI` and
    `Werk (OpenAI)` cards with masked account labels. Never merge by `email` or
    `sub` alone due to the multi-workspace collision documented in
    `wscred/openai_store.go`.
  - Keep card identity and account labeling structured so the future
    `harness-account-switcher` (`261113-feat-ws-dashboard-harness-auth-slots`)
    can attach per-slot usage cleanly.
- **Phase 1 empirical SIWC spike before remote-quota wiring (`(1777)=a`):**
  - Run an empirical spike first against a real `werk` OpenAI SIWC token
    (`resource=https://api.openai.com/v1`, `scope=... chatgpt.tokens.use.direct`)
    to verify:
    (a) whether JWT claims carry `chatgpt_account_id` / `organization_id`, and
    (b) whether `api.openai.com/v1` or `chatgpt.com/backend-api/wham/usage`
    accepts it for remote 5h/7d quota.
  - Wire the remote quota probe if supported; otherwise fall back cleanly to
    local per-model token aggregation plus `wscred/openai_plan.go` limit/reset
    state (`BeginOpenAIPlanRequest`, `ErrOpenAIPlanPaused`).
  - All remote checks and storage must respect `background_consent`,
    `wscred.Store` refresh locking, and Type 5 credential isolation (raw
    credentials never enter `wsmeta` SQLite or `/rail/usage` payloads).
- **Backlog placement (`(1781)=a`):**
  - Capture to `todo/` backlog (deferred work, outside same-day office release
    scope).
- **Compatibility tier — T0 transparent:**
  - Additive `omitempty` `Model` field on `werk.LineUsage`, additive vendor/card
    entries in `/rail/usage` and `planUsageStore`, and additive SQLite usage
    delta rows/tables in `wsmeta`. Existing `"claude"`, `"codex"`, and `"agy"`
    card shapes and persisted history source names remain unchanged, and older
    JSON readers ignore unknown fields and vendor cards gracefully.

## Constraints

- **Performance & Windows AV (`ai-docs/manuals/performance.md`):**
  - Persist `werk` per-model usage aggregates as Storage Type 4 (`wsmeta`
    SQLite WAL deltas) with an in-memory cache; `/rail/usage` reads must serve
    from RAM with zero transcript-file scanning or per-read disk I/O.
  - Keep `wscred` credentials strictly in Storage Type 5 private files and
    honor `background_consent` before any background network probe.
- **Release compatibility (`ai-docs/manuals/release-compatibility.md`):**
  - Preserve T0 wire and store compatibility: additive JSON fields only,
    existing `plan-usage` / `wsmeta` readers continue working, and legacy
    persisted history identifiers (`via Codex CLI`, etc.) stay readable.
- **Dashboard visual design (`ai-docs/manuals/dashboard-style.md`):**
  - Style OpenRouter, `Werk (OpenAI)`, and unified `Codex / OpenAI` topbar
    cards and popovers with Werkschaft's borderless Material tonal surface
    ladder (`--wb-card`, `--wb-muted`), semantic tokens (`--wb-ok`,
    `--wb-warn`, `--wb-fail`), and `tabular-nums` alignment for balances,
    token totals, and costs.
- **Shipped-surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):**
  - Keep all error strings, status notices, and UI/i18n labels
    downstream-neutral without devenv-only references.

## Phases

### Phase 1: Empirical SIWC JWT Claims & Remote Quota Endpoint Spike

1. **Inspect live `werk` OpenAI SIWC token and endpoints:**
   - Capture claims from a real SIWC token (`resource=https://api.openai.com/v1`,
     `scope=... chatgpt.tokens.use.direct`) to verify whether `chatgpt_account_id`,
     `organization_id`, or equivalent workspace claims are present.
   - Probe whether `api.openai.com/v1` or `chatgpt.com/backend-api/wham/usage`
     accepts the SIWC access token for remote 5h/7d quota windows.
2. **Record spike verdict:**
   - Record the verified claim fields and endpoint acceptance in `### Result`
     to settle Phase 4's remote-probe vs local-fallback path before Phase 4
     implementation starts.

### Phase 2: `werk` `LineUsage.Model`, Compaction Usage, and `wsmeta` SQLite Deltas

1. **Emit `Model` on `LineUsage` and capture compaction usage:**
   - Add `Model string \`json:"model,omitempty"\`` to `werk.Line`
     (`werk/protocol.go`) and populate `Response.model` in `Agent.report`
     (`werk/agent.go`) and `compactModelProse` / `compactWithSummary`
     (`werk/compaction.go`).
2. **Aggregate per-model `CostUSD` and tokens via `wsmeta` SQLite deltas:**
   - Record per-model token counts and `CostUSD` deltas into `wsmeta` (Storage
     Type 4) on `LineUsage` ingestion, backed by an in-memory map so
     `/rail/usage` reads require zero file or transcript scans.
3. **Verification:**
   - Unit tests for `werk` `LineUsage.Model` emission (regular turns and
     compaction), `wsmeta` SQLite delta persistence/reload, and zero-transcript-scan
     `/rail/usage` read checks.

### Phase 3: OpenRouter `/credits` + `/key` Probe and Hybrid Topbar Card

*Depends on Phase 2 per-model `wsmeta` aggregation.*

1. **Hybrid OpenRouter probe in `wscred` / `wsdashboard`:**
   - Query `/api/v1/credits` (`total_credits`, `total_usage`) when permitted,
     alongside `/api/v1/key` limits and daily/weekly/monthly spend as
     fallback/companion.
   - Keep `checkOpenRouter` (`wscred/openrouter_verify.go`) strictly anchored
     on `/api/v1/key` `2xx` so a `/credits` `401`/`403` never classifies a valid
     key as `rejected`.
2. **Topbar card and popover:**
   - Merge remote balance/spend with Phase 2's local per-model OpenRouter
     tokens and `CostUSD` into a hybrid `/rail/usage` card and `assets/usage.js`
     popover following `ai-docs/manuals/dashboard-style.md` and `wsi18n`.
3. **Verification:**
   - Go tests covering `/credits` `401`/`403` with `/key` `2xx`, debounced
     background refresh, and frontend/browser tests for OpenRouter card and
     per-model popover rendering.

### Phase 4: Workspace-ID Matched `Codex / OpenAI` Unification vs `Werk (OpenAI)` Card

*Depends on Phase 1 spike result and Phase 2 per-model aggregation.*

1. **Workspace/account ID matching:**
   - Extract workspace/account ID from `~/.codex/auth.json` (`tokens.account_id`)
     and `werk` SIWC JWT claims (per Phase 1). Merge into a single
     `Codex / OpenAI` card **only** on exact workspace/account ID match;
     otherwise keep separate `Codex CLI` and `Werk (OpenAI)` cards with masked
     account labels (never merging by `email` or `sub` alone).
2. **Remote quota vs local fallback wiring:**
   - Wire remote 5h/7d quota probing if Phase 1 confirmed endpoint support;
     otherwise surface local per-model token aggregation paired with
     `wscred/openai_plan.go` limit/reset state, respecting `background_consent`,
     `wscred.Store` refresh locking, and Type 5 credential isolation.
3. **Verification:**
   - Go tests for matching vs mismatched workspace IDs, same-email
     multi-workspace separation, `background_consent=false` gating, and
     frontend card rendering tests.
