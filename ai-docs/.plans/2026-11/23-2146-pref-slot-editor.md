# Dashboard editor for user-preference slots (`preference.*`)

## Problem

Preference slots (`preference.<slot>`, ticket 261112, merged 2e284baa3) are
settable only through `config.tune`. The dashboard Settings prompts section
edits only the legacy `prompt.UserPreferenceSection` key
(`agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-extras.js`
around line 91). The `preference.*` family is registered
(`agents-plugin-tool/internal/mcp/dashboard_settings_config.go` around line
129; `dashboard-settings.js` specFor family match), but there is no list,
add, hide, or reset UI.

## Approved reference

Owner-approved mockup (5106/5108 = a):
`ai-docs/assets/mockups/261123/settings-preference-slots/preference-slots.html`
(state A: global scope with the legacy fallback; state B: repo scope with a
hidden slot and a repo-only slot). Match its structure and behavior:

- Replaces the `prompt.UserPreferenceSection` card; reached through a new
  first chip "사용자 선호 슬롯 `preference.*`" in the prompt picker, before the
  `prompt.DelegationSection` / `prompt.SeedSection` chips.
- Slot list in manual order (`default` first, then alphabetical), number
  badge per row, source-layer chip per row.
- Hide = write an empty string at this layer (tombstone; drops the slot from
  the manual). Reset = remove this layer's value (falls back to the next
  layer; "슬롯 삭제" when no layer below has it, disabled "값 없음" when this
  layer holds none). A two-item legend explains the difference.
- Hidden row: struck-through masked value, layer chain, "복원" = remove the
  empty value.
- Inherited slots: dashed muted text area; editing creates an override at the
  current layer.
- Add form: fixed `preference.` prefix, live checks matching
  `ValidPreferenceKey`, target-layer segmented control defaulting to the
  current scope, empty text refused.
- Repo scope goes through the existing edit-proposal flow (button
  "편집 제안 초안에 추가").
- Live manual preview (labels only in the preview; hidden slots as a dashed
  placeholder; the draft in the add form is excluded).
- Legacy notice when `prompt.UserPreferenceSection` is set and no
  `preference.default` exists at that layer: the legacy value shows as a
  read-only `default` row tagged "레거시 대체값".
- **Owner decision (5108=a):** "default 슬롯으로 옮기기" writes
  `preference.default` at the same layer with the legacy value AND clears the
  legacy `prompt.UserPreferenceSection` at that layer.
- Layout collapses by panel width (the settings view is now a floating modal,
  merged 60377a909).

Use the dashboard's existing settings styles and components; the mockup's
inline CSS is a visual reference, not code to paste.

## Steps

1. Read the existing settings data flow (config routes, proposals, the
   prompt-override section) and reuse it. Prefer the existing config read and
   write endpoints. If listing slots per layer needs new server data, add it
   as a T0 addition only: new optional response fields, with the existing
   shape unchanged. Old binaries must ignore them.
2. Implement the editor and its picker chip; remove the standalone legacy
   card only by replacing it with the slot editor as described.
3. Tests: `frontend-settings.test.js` (node:test) for ordering, add-name
   validation, hide vs reset, legacy move (writes default + clears legacy);
   extend the Chromium settings browser suite for the editor in global and
   repo scopes.
4. Capture headless Chromium screenshots at 1200 and 760 of states matching
   mockup A and B. Save them to `/tmp/pref-slot-editor-shots/` and never
   commit PNGs. Compare them side by side with the mockup and list any
   deviations in the report.

## Constraints

- Read before editing: `ai-docs/manuals/config-settings-ui.md`,
  `ai-docs/manuals/performance.md` (frontend section),
  `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`.
- Compatibility: T0 only. Do not change the config store's shape or a tool's
  arguments. If T1+ seems necessary, stop and report.
- File size: keep each asset within its `.gitattributes` size guard. If a file
  would exceed it, put the editor in a new asset file. Never split an
  existing file.
- Never launch Electron. Never stop, replace, or rerun the running daemon.
  Create no extra worktrees.
- Browser tests env: `WS_PLAYWRIGHT_MODULE=$(ls -d ~/.npm/_npx/*/node_modules/playwright|head -1)`
  and `PLAYWRIGHT_BROWSERS_PATH=$HOME/Library/Caches/ms-playwright`.

## Verification

From `agents-plugin-tool/`:

- `node --test internal/wsdashboard/frontend-settings.test.js`
- `go test ./internal/wsdashboard -run 'Settings|Config|TierRouting' -count=1`
  (browser env)
- `go test ./internal/mcp -run 'Preference|DashboardSettings' -count=1`
- `GOOS=windows go vet ./internal/wsdashboard ./internal/mcp`
- Report the verbatim results, the screenshot paths, and the deviations from
  the mockup.
