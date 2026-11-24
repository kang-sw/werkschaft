# Settings Routing Graph Flex Fix

## Problem
Inside the floating settings modal (`#workflow-settings`, capped at `min(1280px, 100% - 32px)` × `min(960px, 100% - 32px)`), `:root[data-settings-modal] .dbody` overrides `.dbody` to `display: flex` without `flex: 1 1 0; min-height: 0;`, breaking the flex height constraint chain. Additionally, inside `.scontent` (`display: flex; flex-direction: column; overflow-y: auto;`), `.klist` and `[data-settings-view] .sgroup` (including `.sgroup.tr-card` with `overflow: hidden`) lack `flex: none;` / `flex-shrink: 0;`, causing Flexbox to shrink `.sgroup.tr-card` vertically instead of scrolling `.scontent`.

## Changes
1. `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings.css`:
   - Add `flex: 1 1 0; min-height: 0;` to `:root[data-settings-modal] .dbody`.
   - Add `flex: 1 1 0;` to `.scontent` (for `<= 820px` column flex `.sbody`) and `.scontent > * { flex: none; }`.
   - Add `flex: none;` to `.klist`, `.klist > *`, and `[data-settings-view] .sgroup`.
2. `agents-plugin-tool/internal/wsdashboard/settings_test.go`:
   - Add `TestSettingsModalFlexHeightChainAndNonShrinkingCards` verifying the CSS contract for `:root[data-settings-modal] .dbody`, `.klist`, and `[data-settings-view] .sgroup`.
3. `agents-plugin-tool/internal/wsdashboard/testdata/tier-routing.browser.cjs`:
   - Add a `1280x800` constrained-viewport assertion verifying `.tr-card` is not vertically clipped (`scrollHeight <= clientHeight + 2`), `.tr-frame` has `height >= 120`, and `.scontent` is scrollable (`scrollHeight > clientHeight`).
