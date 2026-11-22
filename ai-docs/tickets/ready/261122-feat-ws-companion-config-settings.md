---
title: "컴패니언 T0 설정 키, 대시보드 설정 UI 카탈로그·번역 및 셸 실시간 반영"
parent: 261122-epic-werkschaft-lead-companion
related:
  261121-research-werkschaft-lead-companion-rig: settled opt-in config keys and v0.50.8 T0 audit
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 75a60323dec32694
sage-review-completeness-reviewed: 75a60323dec32694
---

# Companion T0 Config Keys, Dashboard Settings UI & Live Shell Broadcast

## Background

The Werkschaft Lead Companion is strictly opt-in (`off` by default in both `ws` and `wsflow`) and must be inspectable and configurable from Dashboard Settings and `lead-tune` without breaking cross-version compatibility or requiring a full browser reload when toggled.

Today, `dashboard-config-manifest.json` declarations, `dashboard-settings-catalog.json`, `wsi18n` translation tables, embedded package assets, and drift tests are tightly coupled (`ai-docs/manuals/config-settings-ui.md`), and the daemon's settings `Changed` callback (`mcp/dashboard_settings_config.go`) currently handles only compaction tuning and workflow language updates.

## Decisions

- **Scalar `overrides` keys — `global`-only scope (verified T0 against `v0.50.8`):** Add four scalar string keys to both `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json` using existing manifest fields only (`key`, `type`, `enum`/`pattern`, `default`, `default_scope: "global"`, `description`):
  - `dashboard.companion_mode`: enum `off | visual | full`, default `"off"` (in both `ws` and `wsflow`).
  - `dashboard.companion_director`: enum `rules | sidecar | lead-tool`, default `"rules"`.
  - `dashboard.companion_pack`: string matching `^[a-z0-9][a-z0-9_-]{0,63}$` (`"default"` | `"auto"` | `<pack-id>`), default `"default"`.
  - `dashboard.companion_preset`: string matching `^[a-z0-9][a-z0-9_-]{0,63}$` (`"werkschaft-d1"` | `<preset-id>`), default `"werkschaft-d1"`.
- **Global-only effective scope & selector semantics for app-scoped shells:** All four `dashboard.companion_*` keys resolve exclusively at **`global` scope** via compiled-in global-only registration (same pattern as `dashboard.lan_enabled` and `dashboard.mailbox_undelivered_minutes` in `dashboard_config.go` — project, repo, and session writes are refused and ignored). Rationale: the companion is mounted once as an app-scoped singleton on the persistent outer shell spanning all workspaces/sessions served by the daemon, so every connected shell shares one authoritative global effective configuration.
  - The global `dashboard.companion_pack` selector (`"default"`, `"auto"`, or `<pack-id>`) is stored and broadcast unchanged; `"default"` and `"auto"` are reserved selector IDs (rejected as local BYO directory names by `261122-feat-ws-companion-pack-store`). When set to `"auto"`, each connected shell resolves the active pack per its own visible session via `261122-feat-ws-companion-pack-store`'s catalog contract (`?harness=&model=`), falling back to `"default"` on sessionless routes (root, `/settings`).
  - *Rejected alternative:* Supporting per-project or per-session companion overrides. Rejected because the outer shell is app-scoped across sessions and mixed-scope mutations would force per-shell context tracking and divergent live broadcasts across tabs.
- **Compatibility Tier Verdict — T0 (transparent):**
  - *Read/write scalar preservation (`v0.50.8` <-> new binary):* `v0.50.8:agents-plugin-tool/internal/mcp/config_manifest.go:61-78,172-220` rejects unknown manifest struct fields (T3) but accepts arbitrary valid setting key entries conforming to the existing schema (`key`, `type`, `enum`, `pattern`, `default`, `default_scope`, `description`). `v0.50.8:agents-plugin-tool/internal/wsconfig/config.go:34-40,108-120` decodes all scalar string entries into `cfg.Overrides` (`map[string]string`), and `:745-767` serializes the full `cfg.Overrides` map on unrelated writes so unknown scalar keys survive and reopen intact in the new binary.
  - *Mixed-version scope nuance (`v0.50.8` explicit non-global writes):* In `v0.50.8`, manifest `default_scope` applies when scope is omitted (`config_manifest.go:406-424`), while non-global rejection is enforced only for compiled-in registrations (`wsconfig/resolver.go:168-174,238-244`, `dashboard_config.go:26-34`). A `v0.50.8` MCP server loading the new manifest may therefore acknowledge an explicit project-scope write that the new daemon ignores in favor of `global` scope. Scalar preservation remains T0, and tests cover ignoring non-global overrides cleanly.
  - *Dashboard snapshot & live-event path:* The new `companion` snapshot field and shell live-update event are additive JSON/stream payloads consumed strictly by the matching dashboard frontend embedded in the same daemon binary; any stale/older browser tab ignores unknown JSON keys and unrecognized stream event names without error.
  - *Rejected alternative:* Adding a dedicated top-level `"companion": { ... }` object in `config.json`. Rejected because `v0.50.8` lacks `preserve.go` and silently drops unknown top-level JSON members on write.
- **Live shell broadcast (in-process + separate stdio `ws-mcp` + reconnect resync):**
  - Extend both the daemon settings mutation path (`config_routes.go`, `mcp/dashboard_settings_config.go`) AND the MCP `ws/config.tune` / reset commit path (`server_dispatch.go`) so changes to any `dashboard.companion_*` key — whether made via Dashboard Settings or a separate stdio `ws-mcp` process (`cmd/ws-mcp/main.go`) — trigger a lightweight local daemon notification that recomputes the resolved global `companion` object and broadcasts it to all connected outer-shell rail WebSocket streams.
  - Emit the current resolved global `companion` object immediately on every shell rail stream initial connect and reconnect so tabs that missed an update while disconnected resync automatically without requiring a page reload.

## Constraints

Required manuals (per `AGENTS.md` `### Implementation Conventions`):
- `ai-docs/manuals/config-settings-ui.md` — update catalog, adapter declarations, all `wsi18n` locale tables, and generated package assets together.
- `ai-docs/manuals/wsflow-mirroring.md` — keep `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json` key-identical (both default `off`) and keep wsflow text non-ws-aware.
- `ai-docs/manuals/release-compatibility.md` — keep all additions strictly within existing manifest struct fields and scalar `overrides`.
- `ai-docs/manuals/shipped-surface-boundary.md` — all manifest descriptions and UI strings must be downstream-neutral (zero devenv-specific paths, ticket stems, or repo names).
- `ai-docs/manuals/performance.md` — settings reads on interactive paths stay in memory; config writes touch disk only when values actually change.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for `agents-plugin/rsrc/`, `agents-plugin/skills/`, `agents-plugin/hooks/`, `agents-plugin-wsflow/`)
- Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
- Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for `agents-plugin-tool/internal/mcp/config_registry.go`, `agents-plugin/config-manifest.json`, `agents-plugin/agy-config-manifest.json`, `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-pi/config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/config_routes.go`, `agents-plugin-tool/internal/wsdashboard/config_proposals.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)

## Prior Art

- Manifest declarations & parity tests: search `dashboard-config-manifest.json` and `dashboard_config_test.go` (note: update any explicit expected key-count assertion).
- Settings catalog & drift guard: search `dashboard-settings-catalog.json` and `dashboard_settings_ui_catalog_test.go`.
- Package asset generator: `agents-plugin-tool/scripts/generate-package-assets.py`.
- Locale tables & coverage guard: search `internal/wsi18n/tables/` and `keys_test.go`.
- Settings mutation & `Changed` callback: search `config_routes.go` (`TrustedWrite`) and `mcp/dashboard_settings_config.go`.

## Prior Decisions

- 261122-feat-ws-companion-shell-engine (2026-11-22, Decisions): "Toggling `off <-> visual/full` mounts/tears down cleanly via the live settings broadcast from `261122-feat-ws-companion-config-settings`." — bearing: supports
- 5bb4a1564 (2026-11-21, commit): "Owner confirmed (70)=ok on v2 interactive mockup and (75)=a restricting /settings and /api/config/snapshot to local/control-authorized callers" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Both dashboard manifests, dashboard-settings-catalog.json, locale tables and settings notification paths |
| scope.surface | public-interface | Four config keys and shell settings snapshot and live event |
| scope.new_public_symbol | yes | Four dashboard.companion_* configuration keys |
| scope.new_type_contract | yes | Effective companion snapshot block and live update event |
| scope.test_surface | existing | dashboard_config_test.go, dashboard_settings_ui_catalog_test.go, keys_test.go and config_routes_test.go |
| complexity.reuse_points | confirmed | DashboardConfigBackend Changed callback and config_routes.go mutation path |
| complexity.side_effect_risk | moderate | Settings mutations and live shell notifications span multiple entry points |
| risk.correctness | high | Effective scoped values must stay synchronized for both mutation paths |
| risk.fit | moderate | App-wide shell state must align with scoped configuration and downstream shell engine |
| risk.test | moderate | Cross-entry-point broadcast and locale coverage require integration checks |
| risk.security_or_contract | high | Snapshot access and cross-version configuration preservation affect public contracts |

## Phases

### Phase 1: Declare T0 Companion Keys, Settings UI Entries & Live Broadcast

- **Goals:**
  1. Declare `dashboard.companion_mode`, `dashboard.companion_director`, `dashboard.companion_pack`, and `dashboard.companion_preset` in both `agents-plugin/dashboard-config-manifest.json` and `agents-plugin-wsflow/dashboard-config-manifest.json` with identical schema and safe `off`/`rules`/`default`/`werkschaft-d1` defaults.
  2. Run `python3 agents-plugin-tool/scripts/generate-package-assets.py` so embedded daemon manifests stay byte-synced.
  3. Add a dedicated Companion section/rows in `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-catalog.json` with appropriate controls (segmented/select for mode & director, text/select for pack & preset) and complete labels + help descriptions across all `internal/wsi18n/tables/*.json` locales.
  4. Register the four `dashboard.companion_*` keys as global-only in the compiled-in registry (`dashboard_config.go` pattern), expose the resolved global `companion` config block in the shell bootstrap / settings snapshot, emit the current `companion` state on every shell rail stream connect/reconnect, and deliver live broadcast events on both `config_routes.go` mutations and separate-process `ws/config.tune` sets/resets (`server_dispatch.go` -> daemon handoff).
  5. Update manifest parity, settings catalog drift, i18n coverage, mixed-version non-global override ignore behavior, separate stdio `ws/config.tune` set/reset propagation, and shell stream reconnect-resync unit tests.
- **Deferred scope:** Pack discovery dropdown population from `<config-home>/companions/` (wired once `261122-feat-ws-companion-pack-store` lands) and actual canvas rendering (`261122-feat-ws-companion-shell-engine`).
- **Verification boundary:**
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`
  - `GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local go -C agents-plugin-tool test ./internal/mcp ./internal/wsi18n ./internal/wspackage -run 'Settings|Manifest|Keys|Companion'`
  - Targeted `wsdashboard` & `mcp` tests verifying `TrustedWrite` enforcement, global-only enforcement (ignoring non-global overrides written by older binaries), separate stdio `ws/config.tune` set and reset daemon handoff, and shell stream connect/reconnect resync after a missed change.
  - `python3 -m unittest discover agents-plugin-wsflow/tests`
