# Plan: Track A Stage 1 — Dashboard Settings `companion` Section Implementation (Continuation)

## Context & Already-Completed Work in This Worktree
You are continuing Track A (`impl/develop/companion-settings-section`) in `/Users/kang-sw/.ws-worktrees/devenv/appraisal-scallion-print` (off `develop` `3ff780fa4`).
The previous worker already:
- Copied the 4 approved text mockup files into `ai-docs/assets/mockups/261127/companion-settings-redesign/` (`index.html`, `styles.css`, `app.js`, `mock-api.js`).
- Started Go edits in `companion_config.go`, `companion_pack_install_routes.go`, `companion_pack_source_routes.go`, and `pages.go` (run `git diff` first to inspect and finish them cleanly).

Full spec lives in `ai-docs/.plans/2026-11/28-0102-companion-settings-section.md`.

## Context-Budget Discipline (CRITICAL)
The previous worker hit 3 compactions in one turn by reading too many full manuals and doing implementation + browser QA all in one turn.
**Split into Stage 1 (THIS TURN) and Stage 2 (NEXT TURN):**
- **THIS TURN (Stage 1 — Implementation & Compile/Unit Checks ONLY):**
  1. Inspect current `git diff` and read ONLY the specific files you are editing (plus `ai-docs/assets/mockups/261127/companion-settings-redesign/` as visual/structure reference). Do NOT bulk-read unrelated manuals; the key rules are summarized below:
     - `dashboard-style.md`: ZERO `1px solid` / `1px dashed` borders on cards/rows/inputs/buttons/badges/dropzones; canonical `--wb-*` tokens ONLY (`--wb-bg`, `--wb-card`, `--wb-muted`, `--wb-accent`, `--wb-fail` — NEVER `--wb-danger`, `--wb-ok`, `--wb-warn`, `color-mix()` washes); support dark & light themes.
     - `config-settings-ui.md`: keep `dashboard-settings-catalog.json`, `dashboard_settings_ui_catalog_test.go`, `internal/wsi18n/tables/{en,ko}.json`, and `ai-docs/manuals/config-settings-ui.md` in sync (plus `agents-plugin/` & `agents-plugin-wsflow/` manifests if any manifest field changes).
     - `release-compatibility.md`: relaxing `companionSourceGate` when `companion_enabled == false` is T0 transparent.
  2. Complete ALL code edits cleanly:
     - Backend & catalog: verify `companionSourceGate` relaxation in `companion_pack_source_routes.go` / `companion_pack_install_routes.go`, update `dashboard-settings-catalog.json` (move the 10 companion keys from `"surface": "companion-modal"` into the native `companion` section), update i18n tables (`en.json`, `ko.json`), `companion_config.go`, `pages.go`, `dashboard.tmpl` (remove `companionModalRows` & obsolete `#ws-companion-settings` script includes, add `dashboard-settings-companion.js` to settings page).
     - Frontend Settings Companion section: create `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-companion.js`, wire `?section=companion` + sidebar nav item in `dashboard-settings.js`, add borderless tonal CSS in `dashboard-settings.css` (or `dashboard-settings-companion.css` if cleaner), implementing all 5 approved groups from the mockup (`기본 설정`, `아바타 · VRM 관리`, `팩 소스 및 스토어`, `2D 외형 커스터마이징`, `고급 · 팩 제작자`) with live `postMessage` sync back to `dashboard-shell.js` / `dashboard-lifecycle.js`.
     - Retire `#ws-companion-settings`: make `.ws-companion-gear` and context menu Settings item open Dashboard Settings to `?section=companion` via `dashboard-shell.js`; remove obsolete `#ws-companion-settings` modal plumbing (`dashboard-companion-settings.js`, `dashboard-companion-sources.js`, obsolete modal CSS rules in `dashboard-companion.css`, fixing any `--wb-danger` -> `--wb-fail`).
     - **DO NOT TOUCH**: `dashboard-companion-vrm.js`, `dashboard-rail.css`, or `#rail-companion-slot` rules.
  3. Run quick compile + Go unit test checks (`go test ./internal/mcp -run "TestDashboardSettings|TestConfig"` and `go test ./internal/wsdashboard -run "TestAsset|TestConfig|TestCompanionConfig|TestCompanionPack|TestCompanionSource"`).
  4. **DO NOT run Playwright browser tests or commit yet!** Stop cleanly right after Stage 1 compiles and unit tests pass, and report back a concise file list + remaining test/QA checklist so the lead triggers Stage 2 (JS/Playwright tests + visual QA screenshots + commit) cleanly!
