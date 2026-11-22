---
summary: Maintain the dashboard settings catalog, adapter declarations, and translations together so every supported setting remains inspectable.
---

# Dashboard Settings Catalog

Update the settings catalog when a key changes in the MCP registry or an
adapter manifest. Put the row in the section a user would look for it, choose
a control that matches its value and selectors, and add its label and
description to all dashboard locale tables. The drift test compares the
explicit catalog against the registry and all four embedded manifests in
both directions.

Use the daemon's packaged manifest and its selected ws or wsflow defaults
when inspecting an adapter. Run the package-assets generator after changing
a canonical manifest. Its check and package tests verify that a standalone
daemon has the same declarations without an installed adapter.

The page projects per-layer availability and scope restrictions from the
snapshot. The control needs both the exact selected override and the
effective value because a higher layer or harness bucket can win. Keep
labels and help text in the dashboard locale tables so MCP listing responses
retain their existing contract.

A repo edit becomes a draft proposal for the selected worktree's lead,
or a patch the user can copy. Profiles use the server-redacted snapshot for
the card and raw JSON. Prompt diffs compare the packaged seed with the
editor in memory. The planned Git Sync Layer tracks
`261119-feat-ws-config-git-sync`.

From the repository root, check generated assets and the registry and
translation references.

## Adding or Changing a Setting Key

Follow this checklist whenever a `wsconfig` key is added, renamed, or changes
type or selectors:

1. **Declare the key in its canonical source.**
   - Core keys: `agents-plugin-tool/internal/mcp/config_registry.go`
   - Adapter manifests:
     - `agents-plugin/config-manifest.json` (mirror in `agents-plugin-wsflow/config-manifest.json` per `ai-docs/manuals/wsflow-mirroring.md`)
     - `agents-plugin/agy-config-manifest.json`
     - `agents-plugin/dashboard-config-manifest.json` (mirror in `agents-plugin-wsflow/dashboard-config-manifest.json`)
     - `agents-plugin-pi/config-manifest.json`
   - After editing any canonical manifest, regenerate embedded daemon assets:
     `python3 agents-plugin-tool/scripts/generate-package-assets.py`

2. **Add or update the UI catalog row** in
   `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-catalog.json`:
   ```json
   {
     "key": "workflow.lang",
     "section": "workflow",
     "control": "text",
     "label": "js.settings.key.workflow.lang.label",
     "description": "js.settings.key.workflow.lang.description",
     "selectors": []
   }
   ```
   - **`key`**: exact key, or `<prefix>.*` for a wildcard family (for example `preference.*`, `agent.profile.*`).
   - **`section`**: one of `workflow`, `agents`, `compaction`, `prompts`, `profiles`, `claude`, `agy`, `dashboard`, `pi`. Keys starting with `claude.`, `agy.`, `dashboard.`, or `pi.` **must** use that adapter name as `section`.
   - **`control`**: enforced daemon-type mappings are `boolean` -> `toggle`, `integer` -> `number`, `enum` -> `choice`, `tier` -> `tier`; specialized controls are `text`, `textarea`, `threshold`, `prompt`, `profile` (for `agent.profile.*` / `worktree.profile.*` under `profiles`), and `tier_routing` (the `agent.tier_routing` graph editor in `dashboard-settings-tiers.js`, which draws and previews the graph through `/api/config/tier-routing` and saves through the ordinary set/reset routes; its catalog rule sets `Editable` so a structured key is not shown read-only). Add `"unit"` when displaying a numeric unit.
   - **`surface`** (optional): names the UI that renders the row when it is not the settings panel. The only allowed value is `"companion-modal"`; an absent field means the panel. The panel skips a row with a surface (`sectionEntries` in `dashboard-settings.js`) and the named UI renders it instead. The row still lives in this catalog with its section, control, and i18n strings, so the registry/manifest comparison and inspectability are unchanged. Do not use another `section` value to hide a row from the panel. The drift test (`dashboard_settings_ui_catalog_test.go`) rejects any other surface value, so a new surface needs its allowed value added there and its renderer. For `companion-modal`, the shell page embeds those rows (`companionModalRows` -> `window.wsCompanionModalRows`) because it loads no settings catalog, and keeps their label, description, and unit strings through `shellSettingsStrings` although it drops every other `js.settings.*` string; a new modal row also needs its key in `configFields` of `dashboard-companion-settings.js` (both enforced by tests). `dashboard.companion_sources` is the one modal row with no field of its own: the modal's row loop skips it and the Sources section (`dashboard-companion-sources.js`) renders it as a list editor that writes the whole value through `POST /api/config/set`.
   - **`selectors`**: must match the daemon entry's selectors exactly (for example `[]` or `["harness"]`).
   - **`label` / `description`**: distinct i18n IDs starting with `js.settings.key.` (convention: `js.settings.key.<key>.label` and `js.settings.key.<key>.description`).

3. **Add translations** for both `label` and `description` to every dashboard
   locale table under `agents-plugin-tool/internal/wsi18n/tables/` (`en.json`,
   `ko.json`, and any additional locale tables).

4. **Verify drift and asset parity** from the repository root:

```sh
python3 agents-plugin-tool/scripts/generate-package-assets.py --check
go -C agents-plugin-tool test ./internal/mcp ./internal/wsi18n ./internal/wspackage -run 'Settings|Manifest'
```
