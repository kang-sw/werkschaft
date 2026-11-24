# Plan: `settings-tier-harness-ui`

## Decisions
- **(1522)=a (T0 patch-safe, UI-only removal of `default` bucket):**
  - Remove `"default"` from the `agents.tier` harness selector UI in the Workflow Settings modal.
  - Keep `"default"` in `config_registry.go` (`agentsTierHarnessEnum`) and `wsconfig` backend fallback (`config.resolve_agent` / `resolveAliasMapping`) for full T0 compatibility.
  - Render a conditional cleanup banner below the tier card whenever any stored `"default"` override exists in `global` or `project` scope, allowing one-click reset via `POST /api/config/reset`; hide the banner completely when none exist.
- **(1523)=a (4-harness full-width tab cards + honest unconfigured `werk` display):**
  - Upgrade the `agents.tier` harness selector to full-width 4-harness tab cards (`claude`, `codex`, `agy`, `werk`) with `.hb[data-h]` harness badge, override/status pill, compact model summary, and active harness context bar, defaulting to the current session's harness on modal open.
  - Fix unconfigured `werk` display in `wsconfig.InspectTier` + `wsconfigservice` snapshot + settings UI so unconfigured `werk` rows honestly show as unconfigured ("Unconfigured" / "미설정") instead of inheriting `codex` from `default`.

## Changes
1. **Backend inspection & snapshot (T0):**
   - `agents-plugin-tool/internal/wsconfig/snapshot.go`: in `InspectTier`, return unconfigured (empty `Default`/`Value`, `Bucket: ""`, `Scope: ScopeBuiltin`) for `werk` when no explicit `aliases["werk"]` override exists in `project` or `global`.
   - `agents-plugin-tool/internal/wsconfigservice/snapshot.go`: mark the `builtin` layer `present: false` when `tier.Default` is empty.
   - `agents-plugin-tool/internal/wsdashboard/config_routes.go`: add optional `session_harness` (`omitempty`) to `configSnapshot` from `resolved.session.Harness`.
2. **Settings UI (`dashboard-settings.js`, `dashboard-settings.css`):**
   - Full-width 4-harness tab cards (`claude`, `codex`, `agy`, `werk`), excluding `"default"`.
   - Default selected tab from `snapshot.session_harness` or parent shell active session badge (`.rail .rs.cur .hb[data-h]`), falling back to `claude`.
   - Minimal DOM updates on tab switch and tier row save/reset.
   - Honest unconfigured `werk` row styling (dashed `.is-unset` model input, placeholder, `.origin-pill.unset` "Unconfigured" pill, honest layer drawer).
   - Conditional `.tier-legacy` cleanup banner for stored `"default"` overrides across scopes with clear button.
3. **Translations (`agents-plugin-tool/internal/wsi18n/tables/*.json`):**
   - Add all new `js.settings.tier.*` keys across all 11 locale tables.
4. **Tests & Verification:**
   - Add unit tests in `wsconfig/settings_compat_test.go` and `wsdashboard/config_routes_test.go`.
   - Update `testdata/settings-combobox.browser.cjs` selector for `aria-selected="true"` on tier harness tabs.
   - Run Go tests on remote `w5090` (`./internal/wsconfig/... ./internal/wsconfigservice/... ./internal/wsdashboard/... ./internal/wsi18n/... ./internal/mcp/...`) and `node --check` on `dashboard-settings.js`.
