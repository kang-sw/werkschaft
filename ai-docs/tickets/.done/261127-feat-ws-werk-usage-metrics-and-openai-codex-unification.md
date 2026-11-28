---
title: "werk 사용량 지표: OpenRouter 잔고·모델별 집계 및 OpenAI/Codex 통합"
related:
  261120-feat-ws-dashboard-numbers-screen: shares long-horizon per-model SQLite usage telemetry discipline
  261122-feat-ws-dashboard-session-stats: shares per-model token/cost accounting and composer stats UX
  261113-feat-ws-dashboard-harness-auth-slots: future multi-slot account switcher integration (harness-account-switcher)
  261127-feat-ws-harness-account-switcher-and-slot-continuity: companion (soft ordering - owns the slot-aware home resolver and account-switch semantics that Phase 4 consumes; not a prerequisite)
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: b8d24c75c8c621fb
sage-review-completeness-reviewed: b8d24c75c8c621fb
completed: 2026-11-28
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
2. The `LineUsage` line emitted by `Agent.report` (`werk/agent.go`) does not
   set `Model` (`Response.model`); `werk.Line` already has a `Model` field
   (`agents-plugin-tool/internal/werk/protocol.go#L113`) that only the init and
   reasoning lines populate (`agents-plugin-tool/internal/werk/agent.go#L749-L768`), and `werk`
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

- **(1809) Account/slot key on per-model usage rows (technical, auto-confirmed).** Phase 2 `wsmeta` per-model delta rows carry an additive optional account/slot key (for OpenAI SIWC, the stable non-secret `ClientID` that `OpenAIAccountStatus` already exposes and the switcher uses; the salted match token of (1810) is a transient comparison value and never the persisted key), so an account switch (`261127-feat-ws-harness-account-switcher-and-slot-continuity` D12) never mixes spend from different accounts in one aggregate. The `Werk (OpenAI)` card and its masked label follow the active (or turn-pinned) account via the existing SSE path.
- **(1810) Werk-side account ID storage and exposure (technical, auto-confirmed).** The `chatgpt_account_id` extracted by `verifyIdentity` is stored in the account's Type 5 record only. `/rail/usage`, `wsmeta`, and logs receive a masked label plus a salted one-way match token computed in-process for the Codex CLI comparison; the raw ID never leaves `wscred`. Merge decisions compare the in-process IDs and are recomputed when the active account changes. The Codex CLI `auth.json` account ID is re-read only on a probe or a default-slot change and cached in memory, so `/rail/usage` reads stay zero-I/O.
- **(1811) Phase 4 completion boundary (technical, auto-confirmed).** Phase 4 is complete when its fixture/mock tests pass, including the local-fallback paths for a missing claim, `ErrOpenAIPlanPaused`, and `401`/`403`. Live SIWC dogfood after owner re-login is a recorded post-merge verification item appended as an Edition, not a completion gate. Until live verification, the remote probe stays conservative (attempted only when the claim is present).
- **(1812) Unified card identity, probe de-duplication, and token-first cost (technical, auto-confirmed).** A matched account yields one `Codex / OpenAI` card under a new additive vendor key, with the existing `"codex"` card shape and history source names unchanged for unmatched cases, and runs exactly one `wham/usage` probe. For OpenAI SIWC subscription traffic the popover shows token counts first and treats `CostUSD` as an estimate that may be zero. The Codex CLI account-ID read uses the slot-aware home resolver of `261127-feat-ws-harness-account-switcher-and-slot-continuity` (D10) when it has landed, otherwise it is default-home only and the card says so. Phase 2 writes batch `wsmeta` deltas in memory and flush on a debounce (and on shutdown) rather than per `LineUsage` event. Starting values, tunable in implementation: usage-delta flush debounce 30s; OpenRouter `/credits` + `/key` background refresh debounced to at most once per 10 minutes (matching the existing plan-usage probe debounce) and only under `background_consent`; the OpenRouter card payload carries balance (`total_credits`, `total_usage`), key limit and daily/weekly/monthly spend when present, and the per-model rows (model, tokens in/out, `CostUSD`), each field omitted when unavailable.

## Spike Findings

Phase 1 spike executed as a codebase audit plus Codex CLI token inspection; owner confirmed `(36)=a` to record it as completed and proceed with Phase 2 onward without blocking on a live SIWC re-login or a live OpenRouter key.

- **Codebase audit:** `wscred` Type 5 storage (`credentials.json`), `openAIState.Accounts`, `plan_usage_probe.go` (`wham/usage` probe), `openrouter_verify.go` (`/api/v1/key`), `werk/protocol.go` (`werk.Line` has a `Model` field at `#L113`, but the `LineUsage` line in `Agent.report` does not populate it), `werk/compaction.go` (`compactModelProse` emits no `LineUsage`), and `wsmeta` (no per-model `werk` table yet).
- **JWT claims:** the Codex CLI `access_token` JWT carries `https://api.openai.com/auth.chatgpt_account_id`; its `id_token` also carries `organizations`. `wscred/openai_oauth.go` requests `resource=https://api.openai.com/v1` and `scope=... chatgpt.tokens.use.direct`, and `verifyIdentity` currently discards the `https://api.openai.com/auth` claim object.
- **Local environment gaps:** the local `wscred` SIWC tokens were empty (`no_siwc_token`, cleared on refresh failure `2026-11-26`) and OpenRouter was unconfigured, so (a) whether a `werk` SIWC token itself carries the claim and (b) whether `wham/usage` accepts a SIWC token remain unverified live. Posture: Phase 2 proceeds now; Phase 3 verifies with HTTP fixtures/mocks (a live key is optional); Phase 4 implements the claim extraction and a conservative probe with automatic local fallback, and live SIWC dogfood verification follows after owner re-login.

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
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/)

## Prior Decisions

- 261122-feat-ws-provider-auth-layer (2026-11-25, Result): "Settings → Account is machine-wide. It manages OpenRouter verification, OpenAI sign-in and plan consent" — bearing: constrains
- 261122-feat-ws-provider-auth-layer (2026-11-22, Decisions): "First providers: OpenRouter by API key, then OpenAI by SIWC" — bearing: supports
- 261121-refactor-ws-daemon-metadata-sqlite (2026-11-27, Result): "latest/delta plan usage history ... MutateUsageHistory + ImportIssues"; wscred kept outside SQLite — bearing: constrains
- 261127-feat-ws-harness-account-switcher-and-slot-continuity (2026-11-27, Decisions): "D3 — Two-stage incremental rollout (`(1774)=a`)" werk OpenAI SIWC multi-account switching — bearing: constrains
- 261113-feat-ws-dashboard-harness-auth-slots (2026-11-27, commit 17b88838): extended with usage-toggle vs account UX and werk zero-restart ClientID switching — bearing: constrains
- 05398675 (2026-11-24, commit): "SIWC tiers require account background consent, with credential scope rechecked for every inference request." — bearing: constrains
- f9b3c792 (2026-11-13, commit): "Endpoint and headers checked against Orca's codex backend source ... no live Pi token call." (wham/usage probe) — bearing: supports
- 4f6a9327 (2026-11-23, commit): "Compaction is not implemented (Phase 2 lead lifecycle); the catalog states it unsupported." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | werk/protocol.go, werk/agent.go, werk/compaction.go, wscred/openrouter_verify.go, wscred/openai_oauth.go, wsdashboard/plan_usage*.go, wsdashboard/assets/usage.js, wsmeta |
| scope.surface | cross-module | werk, wscred, wsdashboard and wsmeta; additive /rail/usage JSON and wsmeta rows |
| scope.new_public_symbol | yes | new wsmeta per-model usage API and OpenRouter credits/key probe result types |
| scope.new_type_contract | yes | per-model usage delta rows, hybrid OpenRouter card payload, account-ID claim on the OpenAI identity |
| scope.test_surface | new-files | existing werk/agent_test.go, wscred/openai_test.go, wsdashboard/plan_usage_codex_test.go; new per-model wsmeta and OpenRouter probe tests |
| complexity.reuse_points | confirmed | planUsageStore and MutateUsageHistory in wsmeta, checkOpenRouter, plan_usage_probe.go wham/usage probe, Model field at werk/protocol.go#L113 |
| complexity.side_effect_risk | moderate | background network probes under background_consent and new batched, debounce-flushed SQLite writes fed by every usage line |
| risk.correctness | moderate | account-ID match gating the card merge and per-model cost aggregation |
| risk.fit | moderate | werk SIWC claim and wham/usage acceptance unverified live; local fallback required |
| risk.test | moderate | needs HTTP fixtures, wsmeta persistence and browser card tests; no live SIWC token locally |
| risk.security_or_contract | high | Type 5 credential isolation, /credits 401 must not reject keys, T0 additive wire and store compatibility |

## Phases

### Phase 1: Empirical SIWC JWT Claims & Remote Quota Endpoint Spike

*Closed as an audit spike (see `## Spike Findings`). The two live questions (werk SIWC claim presence, `wham/usage` acceptance of a SIWC token) remain unanswered and are explicitly not assumed true: Phase 4 treats the claim as unverified and falls back locally (1811). Phase 1 needs no further implementation.*

1. **Inspect live `werk` OpenAI SIWC token and endpoints:**
   - Done for the Codex CLI token (`https://api.openai.com/auth.chatgpt_account_id` present). The `werk` SIWC token claim check and the `wham/usage` acceptance probe are deferred to Phase 4 live dogfood after owner re-login.
2. **Record spike verdict:**
   - Recorded in `## Spike Findings`. Phase 4 takes the conservative path: remote probe attempted only when the claim is present and the probe is accepted, otherwise automatic local fallback.

### Result (83b6741c) - 2026-11-28

Closed as an audit spike prior to implementation (see `## Spike Findings`). Codex CLI token inspection confirmed `https://api.openai.com/auth.chatgpt_account_id`; live `werk` OpenAI SIWC token claim presence and `wham/usage` acceptance remain deferred to post-merge live dogfood verification per `(1811)`.

### Phase 2: `werk` `LineUsage.Model`, Compaction Usage, and `wsmeta` SQLite Deltas

1. **Emit `Model` on `LineUsage` and capture compaction usage:**
   - `werk.Line` already has `Model string \`json:"model,omitempty"\``
     (`agents-plugin-tool/internal/werk/protocol.go#L113`, shared with init and
     reasoning lines); populate it from `Response.model` on the `LineUsage`
     line in `Agent.report` (`werk/agent.go`, which does not set it at
     `agents-plugin-tool/internal/werk/agent.go#L749-L753`) and emit usage lines from `compactModelProse` /
     `compactWithSummary` (`werk/compaction.go`, which has no usage handling today).
2. **Aggregate per-model `CostUSD` and tokens via `wsmeta` SQLite deltas:**
   - Keyed by model plus the optional account/slot key (1809), batched and
     debounce-flushed (1812).
   - Record per-model token counts and `CostUSD` deltas into `wsmeta` (Storage
     Type 4) on `LineUsage` ingestion, backed by an in-memory map so
     `/rail/usage` reads require zero file or transcript scans.
3. **Verification:**
   - Unit tests for `werk` `LineUsage.Model` emission (regular turns and
     compaction), `wsmeta` SQLite delta persistence/reload, and zero-transcript-scan
     `/rail/usage` read checks.

### Result (757ca9cc) - 2026-11-28

- **Delivered (`757ca9cc`, follow-up fix `4a55bafc`):**
  - `werk/agent.go`, `werk/responses.go`, `werk/compaction.go`: populated `Line.Model` on `LineUsage` from `Response.Model` (falling back to the request model) and emitted `LineUsage` with `Compaction: true` from `compactModelProse`. Follow-up `4a55bafc` excludes out-of-band compaction `LineUsage` lines from advancing `wsagent/audit_werk.go`'s `RequestChain`.
  - `wsmeta/werk_usage.go`: added additive T0 table `werk_model_usage` (`SchemaVersion` remains 1), keyed by `(provider, account_key, model)` with `Snapshot.WerkModelUsage` and `AddWerkModelUsageDeltas` `(1809)`.
  - `wsagent/werk_loop.go` + `wsdashboard/plan_usage_werk.go`: wired `onUsage` callback carrying the turn-pinned `ClientID` for OpenAI SIWC into an in-memory map with 30s debounced SQLite delta flushing and shutdown flush on `close()`/`Daemon.Close`.
- **Verification:** `go test -race ./internal/werk/... ./internal/wsmeta/...`, `go test -race ./internal/wsagent -run 'TestWerk|TestLoop|TestUsage'`, and `go test -race ./internal/wsdashboard -run 'TestPlanUsage|TestAccount'` passed.

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
   - Go tests (HTTP fixtures/mocks; a live key is optional) covering `/credits` `401`/`403` with `/key` `2xx`, debounced
     background refresh, and frontend/browser tests for OpenRouter card and
     per-model popover rendering.

### Result (d5876de7) - 2026-11-28

- **Delivered (`3c5e1df5`, `d5876de7`, review fix `4a55bafc`):**
  - `wscred/openrouter_usage.go`: added `ProbeOpenRouterUsage` querying `/api/v1/key` and `/api/v1/credits`; `/credits` `401`/`403` sets `CreditsRestricted` without failing the probe or altering `checkOpenRouter` key verification.
  - `wsdashboard/plan_usage_openrouter.go`: added 10m-debounced viewer-driven refresh, generation-tracked cache invalidation on `accountChanged()`, and additive `/rail/usage` `openrouter` entry combining remote balance/limits/spend with Phase 2 per-model rows.
  - `assets/usage.js`, `usage.css`, `dashboard.css`, and all 11 `wsi18n/tables/*.json`: added compact handle metric (balance/remaining/daily/local-cost) and hybrid popover card with cost-first per-model table. Review fix `4a55bafc` changed the local-only cost handle label from `day` to `Cost`.
- **Deviation / clarification vs `(1812)`:** `wscred` defines `background_consent` exclusively on OpenAI SIWC ChatGPT-plan accounts (no consent field exists on OpenRouter API keys or Settings → Account). OpenRouter's `/credits` + `/key` refresh therefore uses the existing viewer-driven, 10m-debounced `planUsageStore.refresh` path (zero background polling when no dashboard viewer is connected, matching Codex CLI and AgY), while OpenAI SIWC strictly enforces `background_consent`.
- **Verification:** `node --check usage.js`, `node --test usage.test.js` (35/35 pass), `go test -race ./internal/wscred/... ./internal/wsi18n/...`, and `go test -race ./internal/wsdashboard -run 'TestPlanUsage|TestAccount'` passed with HTTP fixtures/mocks.

### Phase 4: Workspace-ID Matched `Codex / OpenAI` Unification vs `Werk (OpenAI)` Card

*Depends on Phase 1 spike result and Phase 2 per-model aggregation.*

1. **Workspace/account ID matching:**
   - Extend `verifyIdentity` (`wscred/openai_oauth.go`) to extract the
     `https://api.openai.com/auth.chatgpt_account_id` claim it currently discards.
   - Extract workspace/account ID from `~/.codex/auth.json` (`tokens.account_id`)
     and `werk` SIWC JWT claims (per Phase 1). Merge into a single
     `Codex / OpenAI` card **only** on exact workspace/account ID match;
     otherwise keep separate `Codex CLI` and `Werk (OpenAI)` cards with masked
     account labels (never merging by `email` or `sub` alone).
2. **Remote quota vs local fallback wiring:**
   - Implement a conservative `wham/usage` probe that automatically falls back
     to local aggregation on `ErrOpenAIPlanPaused`, a `401`/`403`, or a missing
     account claim; live SIWC dogfood verification follows owner re-login.
     Otherwise surface local per-model token aggregation paired with
     `wscred/openai_plan.go` limit/reset state, respecting `background_consent`,
     `wscred.Store` refresh locking, and Type 5 credential isolation.
3. **Verification:**
   - Go tests for matching vs mismatched workspace IDs, same-email
     multi-workspace separation, `background_consent=false` gating, and
     frontend card rendering tests.

### Result (d5876de7) - 2026-11-28

- **Delivered (`3c5e1df5`, `d5876de7`, review fix `4a55bafc`):**
  - `wscred/openai_oauth.go`, `wscred/openai_store.go`, `wscred/openai_usage.go`: extracted `https://api.openai.com/auth.chatgpt_account_id` (from `id_token`, falling back to `access_token` JWT) into Type 5 `openAIAccount.AccountID` only; exposed process-salted HMAC `AccountMatchToken`, `MaskOpenAIAccountLabel`, `ActiveOpenAIUsageIdentity`, and `ProbeOpenAIPlanUsage` (strictly gating network calls on `plan_enabled`, `background_consent`, `ErrOpenAIPlanPaused`, and claim presence, with automatic `auth_rejected` fallback on `401`/`403` without mutating saved credentials).
  - `wsdashboard/plan_usage_openai.go`, `plan_usage.go`, `plan_usage_probe.go`, `plan_usage_history.go`: consumed companion `261127-feat-ws-harness-account-switcher-and-slot-continuity` D10 slot-aware Codex home resolver (`configHome("CODEX_HOME", ".codex")`) and `invalidateSlots()` to cache the Codex CLI match token in `usageDetectMemo` (zero `/rail/usage` disk I/O). Exact match emits additive `codex_openai` (`Codex / OpenAI`) reusing the single Codex `wham/usage` probe and projecting `"codex"` history; mismatch or unverified claim keeps `"codex"` unchanged and emits separate `werk_openai` (`Werk (OpenAI)`) with masked label, token-first per-model rows filtered by active `ClientID`, and conservative `wham/usage` probe / local fallback. Review fix `4a55bafc` derives unified `werk.mode` from live window presence instead of hardcoding `"remote"`.
  - `assets/usage.js`, `usage.css`, `dashboard.css`, and all 11 `wsi18n/tables/*.json`: rendered unified `codex_openai` and separated `werk_openai` cards without topbar-only positioning assumptions.
- **Verification & post-merge follow-up `(1811)`:** All fixture/mock Go (`-race`), JS (`usage.test.js` 35/35), 11-locale `wsi18n`, `GOOS=windows go vet ./...`, and `go build ./cmd/ws-mcp` checks passed. Live SIWC dogfood verification after owner re-login remains a recorded post-merge verification item to be appended as an Edition per `(1811)`.


## Resolution (2026-11-28)

Implemented Phases 2–4 (following Phase 1 audit spike), addressed code-review-lite findings, and verified all Go (-race), JS (usage.test.js), 11-locale wsi18n, Windows vet, and ws-mcp build checks. Live SIWC dogfood after owner re-login remains a recorded post-merge Edition per (1811).
