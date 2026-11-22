---
title: "컴패니언 T0 설정 키, 대시보드 설정 UI 카탈로그·번역 및 셸 실시간 반영"
parent: 261122-epic-werkschaft-lead-companion
related:
  261121-research-werkschaft-lead-companion-rig: settled opt-in config keys and v0.50.8 T0 audit
---

# Companion T0 Config Keys, Dashboard Settings UI & Live Shell Broadcast

## Background

The Werkschaft Lead Companion is strictly opt-in (`off` by default in both `ws` and `wsflow`) and must be inspectable and configurable from Dashboard Settings and `lead-tune` without breaking cross-version compatibility or requiring a full browser reload when toggled.

Today, `dashboard-config-manifest.json` declarations, `dashboard-settings-catalog.json`, `wsi18n` translation tables, embedded package assets, and drift tests are tightly coupled (`ai-docs/manuals/config-settings-ui.md`), and the daemon's settings `Changed` callback (`mcp/dashboard_settings_config.go`) currently handles only compaction tuning and workflow language updates.

## Decisions

- **Scalar `overrides` keys (verified T0 against `v0.50.8`):** Add four scalar string keys to both `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json` using existing manifest fields only:
  - `dashboard.companion_mode`: enum `off | visual | full`, default `"off"` (in both `ws` and `wsflow`).
  - `dashboard.companion_director`: enum `rules | sidecar | lead-tool`, default `"rules"`.
  - `dashboard.companion_pack`: string (`"default"` | `"auto"` | `<pack-id>`), default `"default"`.
  - `dashboard.companion_preset`: string (`"werkschaft-d1"` | `<preset-id>`), default `"werkschaft-d1"`.
- **Compatibility Tier Verdict — T0 (transparent):**
  - *Read path (`v0.50.8` reading new manifest/config):* `v0.50.8:agents-plugin-tool/internal/mcp/config_manifest.go:61-78,172-220` rejects unknown manifest struct fields (T3) but accepts arbitrary valid setting key entries conforming to the existing schema. `v0.50.8:agents-plugin-tool/internal/wsconfig/config.go:34-40,108-120` decodes all scalar string entries into `cfg.Overrides` (`map[string]string`).
  - *Write/reopen path (`v0.50.8` rewriting config, new binary reopening):* `v0.50.8:agents-plugin-tool/internal/wsconfig/config.go:745-767` mutates unrelated keys and serializes the entire `cfg.Overrides` map via `json.MarshalIndent(cfg)`, preserving unknown scalar override keys across writes so reopening with the new binary reads them intact.
  - *Rejected alternative:* Adding a dedicated top-level `"companion": { ... }` object in `config.json`. Rejected because `v0.50.8` lacks `preserve.go` and silently drops unknown top-level JSON members on write.
- **Live shell broadcast:** Extend the dashboard settings mutation / change notification path so updates to any `dashboard.companion_*` key immediately notify connected outer-shell clients (via the shell WebSocket stream or explicit settings-change event) instead of requiring manual refresh.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/config-settings-ui.md` — update catalog, adapter declarations, all `wsi18n` locale tables, and generated package assets together.
- `ai-docs/manuals/wsflow-mirroring.md` — keep `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json` key-identical (both default `off`) and keep wsflow text non-ws-aware.
- `ai-docs/manuals/release-compatibility.md` — keep all additions strictly within existing manifest struct fields and scalar `overrides`.
- `ai-docs/manuals/shipped-surface-boundary.md` — all manifest descriptions and UI strings must be downstream-neutral (zero devenv-specific paths, ticket stems, or repo names).
- `ai-docs/manuals/performance.md` — settings reads on interactive paths stay in memory; config writes touch disk only when values actually change.

## Prior Art

- Manifest declarations & parity tests: search `dashboard-config-manifest.json` and `dashboard_config_test.go` (note: update any explicit expected key-count assertion).
- Settings catalog & drift guard: search `dashboard-settings-catalog.json` and `dashboard_settings_ui_catalog_test.go`.
- Package asset generator: `agents-plugin-tool/scripts/generate-package-assets.py`.
- Locale tables & coverage guard: search `internal/wsi18n/tables/` and `keys_test.go`.
- Settings mutation & `Changed` callback: search `config_routes.go` (`TrustedWrite`) and `mcp/dashboard_settings_config.go`.

## Phases

### Phase 1: Declare T0 Companion Keys, Settings UI Entries & Live Broadcast

- **Goals:**
  1. Declare `dashboard.companion_mode`, `dashboard.companion_director`, `dashboard.companion_pack`, and `dashboard.companion_preset` in both `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json` with identical schema and safe `off`/`rules`/`default`/`werkschaft-d1` defaults.
  2. Run `python3 agents-plugin-tool/scripts/generate-package-assets.py` so embedded daemon manifests stay byte-synced.
  3. Add a dedicated Companion section/rows in `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-catalog.json` with appropriate controls (segmented/select for mode & director, text/select for pack & preset) and complete labels + help descriptions across all `internal/wsi18n/tables/*.json` locales.
  4. Expose the effective resolved `companion` config block in the shell bootstrap / settings snapshot and emit a lightweight live update event on the shell stream when any `dashboard.companion_*` key changes via `config_routes.go` or `ws/config.tune`.
  5. Update manifest parity, settings catalog drift, i18n coverage, and settings change-broadcast unit tests.
- **Deferred scope:** Pack discovery dropdown population from `<config-home>/companions/` (wired once `261122-feat-ws-companion-pack-store` lands) and actual canvas rendering (`261122-feat-ws-companion-shell-engine`).
- **Verification boundary:**
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`
  - `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/mcp ./internal/wsi18n ./internal/wspackage -run 'Settings|Manifest|Keys|Companion'`
  - Targeted `wsdashboard` config route & broadcast test verifying live update delivery and `TrustedWrite` enforcement.
  - `python3 -m unittest discover agents-plugin-wsflow/tests`
