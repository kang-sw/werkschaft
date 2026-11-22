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
   - **`control`**: enforced daemon-type mappings are `boolean` -> `toggle`, `integer` -> `number`, `enum` -> `choice`, `tier` -> `tier`; specialized controls are `text`, `textarea`, `threshold`, `prompt`, and `profile` (for `agent.profile.*` / `worktree.profile.*` under `profiles`). Add `"unit"` when displaying a numeric unit.
   - **`selectors`**: must match the daemon entry's selectors exactly (for example `[]` or `["harness"]`).
   - **`label` / `description`**: distinct i18n IDs starting with `js.settings.key.` (convention: `js.settings.key.<key>.label` and `js.settings.key.<key>.description`).

3. **Add translations** for both `label` and `description` to every dashboard
   locale table under `agents-plugin-tool/internal/wsi18n/tables/` (`en.json`,
   `ko.json`, and any additional locale tables).

4. **Verify drift and asset parity** from the repository root:

```sh
python3 agents-plugin-tool/scripts/generate-package-assets.py --check
go -C agents-plugin-tool test ./internal/mcp ./internal/wsi18n ./internal/wspackage -run 'Settings|Manifest|Keys'
```
