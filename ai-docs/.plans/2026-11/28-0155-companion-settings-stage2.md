# Plan: Track A Stage 2 — Reconcile `develop` (`8c36789a0`), Tests, Real-Browser Visual QA & Commit

## Context
You are completing **Stage 2** of Track A (`impl/develop/companion-settings-section`) in worktree `/Users/kang-sw/.ws-worktrees/devenv/appraisal-scallion-print`.

Stage 1 (`id:bankrollabacusrushbagginess`) already implemented the full Dashboard Settings `companion` section and retired the `#ws-companion-settings` floating modal, passing:
- `go test -count=1 ./internal/wsdashboard -run "TestAsset|TestCompanionConfig|TestCompanionPack|TestDashboardSettings"`
- `go test -count=1 ./internal/mcp -run TestDashboardSettingsUICatalog`
- `go test -count=1 ./internal/wsi18n`
- `node --check` on all 6 runtime JS files.

Those Stage 1 edits are currently **uncommitted** in this worktree (branch base `3ff780fa4`).

Meanwhile, `develop` advanced to `8c36789a0` with three additive merges:
1. `b033490a6` (Step 2 Co-Lead laptop typing): added `dashboard-companion-vrm-colead.js` (1 line in `pages.go` `companionScriptFiles` + `asset_source_test.go`) and updated `dashboard-companion-vrm.js`, `frontend-companion*.test.js`.
2. `a73301f88` (`261127-feat-ws-ephemeral-lightweight-harness-pool` Phase 2): added additive `agent.tier_routing_dynamic` and `agent.tier_routing_dynamic_classifier` keys to `config_registry.go`, `dashboard-settings-catalog.json`, `dashboard_settings_ui_catalog_test.go`, `frontend-settings.test.js`, and all 11 `internal/wsi18n/tables/*.json`.
3. `8c36789a0` (Step 3 laptop swap & dizzy easter egg): added 18 lines in `dashboard-companion-relay.js` forwarding `shCompanion.reset` -> `window.parent.WsCompanion.onSessionSwitch`, plus `dashboard-companion-vrm-colead.js`, `dashboard-companion-vrm.js`, `frontend-companion-vrm.test.js`.

## Required Steps

### 1. Reconcile with `develop` (`8c36789a0`) cleanly (zero data loss)
- First read `ai-docs/manuals/config-settings-ui.md`, `ai-docs/manuals/dashboard-style.md`, and `ai-docs/manuals/wsflow-mirroring.md` if needed for any conflict resolution.
- Preserve every Stage 1 change and cleanly integrate `develop` (`8c36789a0`) into `impl/develop/companion-settings-section`:
  - Recommended lossless sequence: stage & commit Stage 1's work on `impl/develop/companion-settings-section` (or create a clean checkpoint commit via `ws/git.commit` including the settled mockup `ai-docs/assets/mockups/261127/companion-settings-redesign/`), then run `git merge develop` in this worktree and resolve conflicts cleanly so both sides' additions survive intact.
  - **CRITICAL overlap checklist:**
    - `agents-plugin-tool/internal/wsdashboard/pages.go` & `asset_source_test.go`: MUST keep `dashboard-companion-vrm-colead.js` in `companionScriptFiles` AND keep `dashboard-settings-companion.js` / `dashboard-settings-companion.css` in Settings (and keep retired `dashboard-companion-settings.js` / `dashboard-companion-sources.js` removed).
    - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion-relay.js`: MUST keep BOTH Step 3's `onSessionSwitch` forwarding (`shCompanion.reset`) AND Stage 1's Settings/stage-action updates.
    - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-catalog.json`, `agents-plugin-tool/internal/mcp/dashboard_settings_ui_catalog_test.go`, and all 11 `agents-plugin-tool/internal/wsi18n/tables/*.json`: MUST keep BOTH `기능 1`'s additive `tier_routing` keys AND Stage 1's `companion` section catalog/i18n changes! Verify all 11 locale JSON files have identical key sets and `go test -count=1 ./internal/wsi18n` passes.
  - Run `python3 agents-plugin-tool/scripts/generate-package-assets.py` after reconciling and verify `python3 agents-plugin-tool/scripts/generate-package-assets.py --check` exits 0.
  - Keep protected files untouched (`dashboard-companion-vrm.js`, `dashboard-companion-vrm-colead.js`, `dashboard-rail.css`, `#rail-companion-slot` rules).

### 2. Update JS Unit Tests & Playwright Browser Tests
- Update `agents-plugin-tool/internal/wsdashboard/frontend-companion.test.js`, `frontend-companion-vrm.test.js`, and `frontend-settings.test.js` for:
  - Retired `#ws-companion-settings` modal (gear & quick menu route to `/settings?section=companion`).
  - `WsSettingsCompanion` section rendering, `?section=companion` deep-link, `requires_companion_agent_director` gating, disabled-but-editable pack management (`companion_enabled=false`), inline 2-step delete confirmation, Pack Sources preview/install, 2D vs VRM appearance switching, and `companion-packs-changed`/`companion-stage-action` postMessage bridge.
  - Preserved `tier_routing` tests in `frontend-settings.test.js`.
- Add/update Playwright browser verification (using cached Playwright under `~/.npm/_npx/*/node_modules/playwright`) covering:
  - Live `/settings?section=companion` in both Dark and Light themes (zero console/page errors, zero horizontal overflow `scrollWidth <= clientWidth`).
  - Left-rail companion gear / quick menu opening `/settings?section=companion` (no `#ws-companion-settings` modal).
  - Disabled-but-editable state when `companion_enabled=false`, inline 2-step delete confirmation, 2D fallback swatches vs VRM-active note, Advanced `.zip` accordion toggle, and Pack Sources trust notice.
  - Verify existing `TestCompanionVrmBrowser`, `TestCompanionVrmRailDockBrowser`, `TestCompanionVrmCoLeadBrowser`, and `TestCompanionVrmSwapDizzyBrowser` still pass!

### 3. Real-Browser Visual QA Screenshots (Mandatory)
- Capture 4 clean high-DPI Playwright screenshots into `/tmp/ws-companion-settings-qa/`:
  1. `01-companion-settings-dark-vrm.png` — Dark theme, Companion enabled + VRM active + installed packs (showing inline 2-step delete state) + Pack Sources.
  2. `02-companion-settings-light.png` — Light theme full section verifying contrast, borderless Material tonal hierarchy, and zero harsh `1px` borders.
  3. `03-companion-settings-disabled-2d.png` — `companion_enabled=false` disabled-but-editable banner + 2D fallback wardrobe/palette swatches + Advanced `.zip` accordion open.
  4. `04-gear-to-settings-flow.png` — Left-rail companion gear / quick menu -> `/settings?section=companion` handoff check.
- Visually inspect **all 4 screenshots** via `read_file`. Fix any visual, spacing, contrast, wrapping, border, or overflow defect and re-capture until crisp and faithful to the approved mockup (`ai-docs/assets/mockups/261127/companion-settings-redesign/index.html`) and `ai-docs/manuals/dashboard-style.md`.

### 4. Final Verification & Clean Commit
- Run full checks in one batch after final edits:
  - `node --test internal/wsdashboard/frontend-settings.test.js internal/wsdashboard/frontend-companion*.test.js`
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`
  - `go test -count=1 ./internal/wsi18n ./internal/mcp -run "TestDashboardSettings|TestConfig"`
  - `go test -v -count=1 ./internal/wsdashboard -run "TestAsset|TestConfig|TestCompanion|TestDashboardSettings"`
  - `GOOS=windows go vet ./...` (from `agents-plugin-tool/`)
- Privacy/IP hygiene check: zero third-party character/model/creator/company names and zero `.vrm`/`.png` files tracked in git.
- Commit cleanly on `impl/develop/companion-settings-section` via `ws/git.commit` (keep plan files unchanged; include tracked text mockup `ai-docs/assets/mockups/261127/companion-settings-redesign/`; note Compatibility tier: `T0 transparent` in `## AI Context`). Leave the worktree completely clean (`git status --short` empty).
