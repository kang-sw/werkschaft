# Plan: Dashboard Settings `companion` Section Redesign (Retire `#ws-companion-settings`)

## Goal
Port the owner-approved interactive mockup (`/Users/kang-sw/devenv/ai-docs/assets/mockups/261127/companion-settings-redesign/`) into production Dashboard Settings as the dedicated `companion` (`컴패니언`) section, retiring the cramped floating `#ws-companion-settings` dialog and fixing all `dashboard-style.md` / discoverability / disabled-state accessibility flaws (owner decisions `140=a`, `143=ok`).

## Worktree & Branch
- Worktree: `/Users/kang-sw/.ws-worktrees/devenv/appraisal-scallion-print`
- Branch: `impl/develop/companion-settings-section` (branched cleanly off `develop` `3ff780fa4` which already includes VRM ground-zero reset `a933d8d8` and Step 1 left-rail dock `3ff780fa4`)
- Never touch `/Users/kang-sw/devenv` (root `develop` hub). Never run `git worktree add`.
- **DO NOT TOUCH** `dashboard-companion-vrm.js`, `dashboard-rail.css`, or `#rail-companion-slot` layout rules (Step 2 is editing VRM poses in a parallel branch).

## Required Manuals to Read Before Editing
- `ai-docs/manuals/config-settings-ui.md`
- `ai-docs/manuals/dashboard-style.md`
- `ai-docs/manuals/performance.md`
- `ai-docs/manuals/shipped-surface-boundary.md`
- `ai-docs/manuals/release-compatibility.md`
- `ai-docs/manuals/wsflow-mirroring.md` (if any `agents-plugin/` manifest is touched)

## Reference Approved Mockup (Also Track Its Text Files)
- Inspect `/Users/kang-sw/devenv/ai-docs/assets/mockups/261127/companion-settings-redesign/` (`index.html`, `styles.css`, `app.js`, `mock-api.js`).
- Copy those 4 text files into `ai-docs/assets/mockups/261127/companion-settings-redesign/` inside this worktree and commit them as part of this branch (per `/lead-mockup` Step 5; never copy PNGs or dotfiles).

## Deliverables

### 1. Dashboard Settings `companion` Section (`dashboard-settings.js`, new `dashboard-settings-companion.js`, CSS, i18n)
- Support `?section=companion` deep-linking in `dashboard-settings.js` (alongside `account`, `tier_routing`, etc.) and render the `컴패니언` (`Companion`) nav item in the Settings sidebar.
- Keep code modular: put the Companion section renderer in a dedicated file (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-companion.js`, wired cleanly in `pages.go` and verified by `asset_source_test.go`) rather than bloating `dashboard-settings.js`.
- Follow `ai-docs/manuals/dashboard-style.md` strictly:
  - ZERO `1px solid` or `1px dashed` borders on cards, rows, inputs, buttons, badges, or dropzones.
  - Use canonical `--wb-*` tonal tokens (`--wb-bg`, `--wb-card`, `--wb-muted`, `--wb-accent`, `--wb-fail` — NEVER undefined `--wb-danger`, `--wb-ok`, `--wb-warn`, `color-mix()` washes).
  - Support both dark & light themes and both Korean (`ko`) & English (`en`) via the Dashboard Settings i18n system (`config-settings-ui.md`).
- Implement all 5 approved groups matching the mockup:
  1. **기본 설정 (Core & Director)**:
     - `dashboard.companion_enabled` (`.tg`): when OFF, show calm status pill ("비활성화됨 — 설정은 계속 편집 가능") and keep ALL pack/VRM/source controls fully usable!
     - `dashboard.companion_director` (`.seg`: `rules` / `agent`).
     - `dashboard.companion_agent_cooldown_seconds` and `dashboard.companion_agent_hourly_cap`: visibly coupled via `enabled_when` (active only when `director === "agent"`).
     - `크기·위치 초기화` (`Reset size` / `Return home` action row) wired to notify the live companion stage (via `postMessage` to parent dashboard shell / `localStorage` reset).
  2. **아바타 · VRM 관리 (VRM-First Hero)**:
     - Active Avatar summary card (`dashboard.companion_pack`, `3D VRM` vs `2D` soft pill, `persona.name` + `persona.voice`).
     - Borderless tonal `.vrm` drag-and-drop dropzone + styled upload button (no raw `<input type="file">`), wired to real `/api/companion/import-vrm` (verify exact route paths in `companion_pack_source_routes.go`!).
     - Conditional Official 3D VRM download banner (shown when no VRM pack is installed yet).
     - Installed packs list (`적용`, `업데이트 확인`, inline two-step `삭제 → [확인] [취소]` — NO `window.confirm`).
  3. **팩 소스 및 스토어 (Pack Sources)**:
     - Official registry + custom URL sources (`dashboard.companion_sources`), inline URL add bar with origin preview, expandable source catalog cards (`설치` / `업데이트` / `교체` / `설치됨`), and a single calm inline third-party trust notice.
  4. **2D 외형 커스터마이징 (Contextual Legacy 2D)**:
     - Auto-hidden with a concise VRoid Studio `.vrm` note when a `3D VRM` pack is active.
     - Auto-revealed with clean borderless tonal controls (`preset`, `outfit`, `hair`, `eyes`, `attachments`, `palette` + `색상 초기화`) when a `2D` pack is active.
  5. **고급 · 팩 제작자 (Secondary Collapsible)**:
     - Clean tonal disclosure (no ugly browser-default `<details>` styling) containing `.zip` bundle import (styled button, no raw file input) + authoring schema/guide links (`/api/companion/authoring/*`).
- Propagate live companion changes (pack switch, VRM import, uninstall, wardrobe change, reset size/home) from the Settings iframe back to the parent dashboard shell (`dashboard-shell.js` / `dashboard-lifecycle.js`) so the live companion updates immediately without requiring a full page reload.

### 2. Backend & Catalog Alignment
- **Relax `companionSourceGate`** in `companion_pack_source_routes.go` (and its tests): allow pack/VRM/source management APIs to work even when `dashboard.companion_enabled == false` so users can install and configure `.vrm` avatars before turning display on. (Compatibility tier: T0 transparent — server becomes more permissive; old binaries unaffected.)
- **Update catalog & docs**:
  - Update `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-catalog.json` so the 10 companion keys previously marked `"surface": "companion-modal"` now live natively under the `companion` Dashboard Settings section (verify whether any `agents-plugin/` or `agents-plugin-wsflow/` manifest needs matching updates; keep `wsflow` mirrored if touched!).
  - Update `agents-plugin-tool/internal/mcp/dashboard_settings_ui_catalog_test.go`, `companion_config.go`, `companion_config_test.go`, and `ai-docs/manuals/config-settings-ui.md` cleanly.

### 3. Retire Floating `#ws-companion-settings` Modal Cleanly
- Clicking `.ws-companion-gear` (or the context menu's Settings item in `dashboard-companion-menu.js`) opens Dashboard Settings directly to `?section=companion` via the dashboard shell's settings opener (inspect `dashboard-shell.js`).
- Remove `#ws-companion-settings` modal plumbing (`dashboard-companion-settings.js`, `dashboard-companion-sources.js`, `companionModalRows` injection in `dashboard.tmpl` / `pages.go` / `companion_config.go`, and obsolete modal CSS rules in `dashboard-companion.css`, fixing any remaining `--wb-danger` -> `--wb-fail`).
- Keep changes to `dashboard-companion-stage.js` and `dashboard-companion.css` strictly surgical (preserve all Step 1 `#rail-companion-slot` behavior intact!).

## Verification & Visual QA
1. Run Go tests:
   - `cd agents-plugin-tool && go test ./internal/mcp -run "TestDashboardSettings|TestConfig"`
   - `cd agents-plugin-tool && go test -v -count=1 ./internal/wsdashboard -run "TestCompanion|TestSettings|TestAsset|TestConfig"`
2. Run JS unit tests:
   - `node --test internal/wsdashboard/frontend-settings*.test.js internal/wsdashboard/frontend-companion*.test.js`
3. Update & run Playwright browser tests (`companion_settings_browser_test.go`, `companion_sources_browser_test.go`, and their `.browser.cjs` scripts) against the real Dashboard Settings `companion` section!
4. Capture real headless Chromium screenshots of the **live rendered Dashboard Settings → Companion section** into `/tmp/ws-companion-settings-qa/` (dark/light, VRM vs 2D, `companion_enabled=false`) and visually inspect EVERY PNG with `read_file` before committing! (Never place PNGs in the git repo.)
5. Strict privacy/IP hygiene: never reference any third-party character, model, filename, creator, or company name anywhere.
6. Commit cleanly on `impl/develop/companion-settings-section` via `ws/git.commit` with `## AI Context`, and return commit hash, test outputs, visual QA observations, and `/tmp/ws-companion-settings-qa/*.png` paths.
