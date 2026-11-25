---
title: "Electron 데스크톱 셸 은퇴: 빌드·배포 중단과 코드 제거"
related:
  261121-feat-werkschaft-tauri-shell-return: "adopted the Tauri shell; its Constraints required a separate owner decision for Electron retirement"
  261122-chore-werkschaft-tauri-deferred-verification: "carries the Electron-to-Tauri migration verification cases"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 713a9307b8b974c2
sage-review-completeness-reviewed: 713a9307b8b974c2
completed: 2026-11-25
---

# Retire the Electron desktop shell: stop building and shipping, remove code

## Background

The owner is dogfooding the Tauri shell and it runs well. The Tauri shell was
adopted at `b25b9f368`. The release workflow still carries the Electron
build route and the Electron source tree remains; this ticket removes both.

## Decisions

- **(1396)=a, owner, 2026-11-23: retire Electron.** The owner is dogfooding
  Tauri successfully. This supersedes the guard in
  `.done/261121-feat-werkschaft-tauri-shell-return.md` Constraints: "Retirement
  or deletion of `agents-plugin-tool/desktop-electron/`, and removal of shipped
  functionality, require a separate owner decision."
- **(5101)=b, owner, 2026-11-24: stop shipping Electron builds now; remaining
  Electron installs reinstall manually.** Rejected: (a) keep shipping Electron
  through a migration window so the Electron updater can swap in Tauri
  payloads. The release workflow already routes to Tauri
  (`WERKSCHAFT_TAURI_RELEASE=true`, so the Electron `desktop-build` job was
  skipped for v0.52.0); this ticket removes the dead route and the code.
- **Technical: the Electron-only `desktop_dry_run` workflow input is removed
  with the `desktop-build` job; desktop verification uses the existing
  `tauri_dry_run=true` input.** Rejected: keeping `desktop_dry_run` as an
  alias, because after removal it would select no job and silently verify
  nothing. `ai-docs/ship/ws.md` "Desktop dry run before shipping" is updated
  to the `tauri_dry_run` command.
- **Consequence of (5101)=b (announced, auto-confirmed from Sage design
  review): stable Tauri releases stop producing the Electron-compatible
  migration assets.** Retire `legacyName` and its producers and checks:
  the macOS migration ZIP (`desktop/scripts/package.cjs` `releaseMac`
  legacy branch, l.178), the Windows migration shim and ZIP
  (`releaseWindows` legacy branch, l.196, and `desktop/migration/`), the
  legacy entries in `desktop/scripts/release.cjs` (`inspectAssets`,
  `assetFiles`, `describeRelease`, `verifyRelease`, and the
  `Werkschaft-desktop-SHA256SUMS` file they produce), and their
  `test-release.cjs` cases. This supersedes `3c4d85a6` for the legacy
  migration assets only; the signed Tauri packages, their `.sig` files, and
  the updater feed are unchanged. Rejected: keeping the migration ZIPs in
  stable releases, which would keep automatic Electron migration alive
  against (5101)=b.
- Evidence for the current route: the v0.52.0 release run (2026-11-25) shows
  the Electron `Desktop (...)` and `Verify and publish desktop assets` jobs
  skipped and the `Tauri desktop` jobs publishing the desktop assets.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
- Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
- `.github/workflows/ws-mcp-release.yml` has a `desktop-build` job (l.210)
  that packages Electron. It is gated by `tauri_route` (set from the
  `WERKSCHAFT_TAURI_RELEASE` variable, l.73-95); `tauri_route=stable` skips
  tag-release builds, but `desktop_dry_run=true` still selects Electron
  unless `tauri_dry_run=true` (l.215).
- `.github/workflows/werkschaft-tauri-build.yml` (l.80) runs
  `node ../desktop-electron/scripts/assemble-daemon-input.cjs`. That script
  must be relocated out of `agents-plugin-tool/desktop-electron/` before the
  directory is deleted. `ws-mcp-release.yml` (l.281) uses it too.
- No Electron-to-Tauri automatic migration is kept ((5101)=b); the Tauri
  updater and the Tauri release assets (`werkschaft-tauri-build.yml`, the
  `Tauri desktop` jobs of `ws-mcp-release.yml`) must keep working unchanged.
- Modules outside `desktop-electron/` still `require` code inside it:
  `agents-plugin-tool/desktop/scripts/release.cjs:6` (`update`),
  `agents-plugin-tool/desktop/scripts/package.cjs:6,120` (`daemon-stage`,
  `daemon-selection`), and the browser test
  `agents-plugin-tool/internal/wsdashboard/testdata/rail-navigation.browser.cjs:6-7`
  (`policy`, preload). `internal/wsrsrc/ci_contract_test.go:221-222` asserts
  the Electron release asset paths. Docs `agents-plugin-tool/desktop/README.md`
  and `desktop/RELEASE.md` reference `assemble-daemon-input.cjs`.
- Removing shipped functionality: removal is limited to the Electron shell,
  its build/release jobs, and Electron-only docs and tests. No MCP tool,
  daemon behavior, or dashboard behavior changes.

## Prior Decisions

- 261115-feat-werkschaft-electron-shell (2026-11-23, Resolution): "Superseded by the Tauri shell (261121-feat-werkschaft-tauri-shell-return, adopted (2140)=b); owner retired Electron (1396)=a on 2026-11-23." — bearing: supports
- 261115-feat-werkschaft-electron-daemon-override (2026-11-23, Resolution): "Phase 4 packaged acceptance is waived because the owner retired Electron in favour of Tauri ((1396)=a, 2026-11-23)." — bearing: supports
- 261121-feat-werkschaft-tauri-shell-return (2026-11-21, Decisions): "Supersede the settings part of (2066): migrate no Electron shell settings." — bearing: constrains
- 1aace4c5 (2026-11-22, commit): "Electron retirement and OS signing/notarization stay separate decisions per the ticket." — bearing: constrains
- 424ff4e7 (2026-11-16, commit): "Reuse ws-mcp-assets rather than rebuild binaries in desktop jobs so native and bundled daemon metadata have one source of truth." — bearing: constrains
- 3c4d85a6 (2026-11-21, commit): "The pinned Tauri signer signs the final filenames with the exact daemon version after bundling with OS signing disabled." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Both release workflows, desktop-electron/, desktop/scripts/, ci_contract_test.go, rail-navigation.browser.cjs, and desktop/release docs |
| scope.surface | cross-module | Tauri packaging imports Electron modules; release workflow and Go browser fixtures also consume that tree |
| scope.new_public_symbol | no | Phase 1 relocates existing modules and removes the Electron route; no new public symbol is proposed |
| scope.new_type_contract | no | Phase 1 preserves daemon, dashboard, MCP, and Tauri updater behavior; no new type or signature is proposed |
| scope.test_surface | existing | internal/wsrsrc/ci_contract_test.go and internal/wsdashboard/rail_navigation_test.go; existing desktop Node tests |
| complexity.reuse_points | confirmed | Read assemble-daemon-input.cjs, update.js, daemon-stage.js, daemon-selection.js, and policy.js; verified Tauri and browser fixture imports |
| complexity.side_effect_risk | high | Deleting desktop-electron/ breaks Tauri packaging and browser fixtures unless all shared dependencies move first |
| risk.correctness | high | Cross-platform release routing, shared packaging modules, and updater artifacts must survive Electron removal |
| risk.fit | moderate | Owner approved Electron retirement, while the Tauri-only behavior boundary excludes daemon and dashboard changes |
| risk.test | high | The original Contract/Rail filter excludes CI release tests, and desktop_dry_run selects the job being removed |
| risk.security_or_contract | moderate | Relocating policy/preload fixtures and packaging validation must preserve navigation and package-validation contracts |

## Phases

### Phase 1: Retire the Electron shell

- Move every module that the Tauri route or tests still use out of
  `agents-plugin-tool/desktop-electron/` (at least
  `scripts/assemble-daemon-input.cjs`, `update`, `daemon-stage`,
  `daemon-selection`, and whatever `rail-navigation.browser.cjs` needs) into
  `agents-plugin-tool/desktop/` (or the location the Tauri side already uses),
  and repoint every caller: `werkschaft-tauri-build.yml:80`,
  `desktop/scripts/release.cjs`, `desktop/scripts/package.cjs`, the browser
  test, and the docs.
- Remove the `desktop_dry_run` workflow input and every condition that reads it, the Electron `desktop-build` job, its `Verify and publish desktop
  assets` job, and the `tauri_route` branch that selects Electron from
  `.github/workflows/ws-mcp-release.yml`; update
  `internal/wsrsrc/ci_contract_test.go` to the Tauri-only contract.
- Retire the legacy migration assets per the Decisions entry above
  (`legacyName`, the macOS and Windows migration branches,
  `desktop/migration/`, the desktop SHA256SUMS file, their tests and the
  workflow steps that upload or check them). Include
  `desktop/scripts/evaluation-feed.py` (l.38 requires both migration ZIPs and
  `Werkschaft-desktop-SHA256SUMS` for stable files): it must accept a
  Tauri-only stable artifact (feed manifest, signed Tauri packages and their
  `.sig` files) so the `desktop/README.md` smoke command (l.200) still starts
  the feed; verify that with a stable fixture lacking the retired files.
- Delete `agents-plugin-tool/desktop-electron/` and Electron-only docs;
  update docs that describe the Electron shell (`ai-docs/manuals/`, desktop
  README/RELEASE, `ai-docs/ship/ws.md` Build section) to the Tauri-only
  route. In `ai-docs/ship/ws.md`, also update the canary procedure (step 5
  near l.343): it waits for the removed `desktop-release` job and requires
  the Electron ZIPs and `Werkschaft-desktop-SHA256SUMS`; it must watch the
  `Tauri desktop` jobs and verify the Tauri assets and updater feed instead.
- Verification: `go test ./internal/wsrsrc ./internal/wsdashboard -run
  'Contract|CIRelease|Rail'` with `WS_TEST_EMBEDDED_PACKAGES=1`,
  `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH` set to the existing
  Playwright install (otherwise `TestRailNavigationBrowser` skips,
  `rail_navigation_test.go:118`, and the relocated fixture is never loaded);
  confirm in `-v` output that it ran
  (`internal/wsrsrc/ci_contract_test.go#L86-L146` names the CI release tests);
  the moved Node scripts'
  own tests (`node --test` where present); `actionlint` or a YAML parse of
  both workflows. A Tauri dry run
  (`gh workflow run ws-mcp-release.yml --ref <branch> -f tauri_dry_run=true
  -f version=<checkout plugin version or that version with -canary.N>`, per
  `desktop/RELEASE.md`; the default `0.52.0-dev` is rejected) checking the
  `werkschaft-tauri-assets` artifact is the lead's step after merge, not the
  worker's.

### Result (bdbcd72c4) - 2026-11-25

- Removed the Electron source tree, build and publish jobs, dispatch input,
  and automatic migration assets. Existing Electron installations use manual
  Tauri reinstall. The Tauri workflow handles exact stable and canary tags.
  Stable publication combines native and desktop assets, and canary
  publication attaches the same signed Tauri assets to the native prerelease.
- Moved the shared daemon assembly, staging, and validation scripts and their
  contract tests into `desktop/scripts/`. Kept exact release version validation
  at the platform manifest boundary in `a9aa2d654`. The rail Chromium fixture
  now loads the actual Tauri updater bridge over a stub native transport.
  Updated desktop docs, operating manuals, and the shipping dry-run and
  canary verification steps.
- Generated embedded packages and passed `go build ./...` and
  `go vet ./internal/wsrsrc ./internal/wsdashboard`. With
  `WS_TEST_EMBEDDED_PACKAGES=1` and the cached Playwright module and browser,
  `go test -count=1 -v ./internal/wsrsrc ./internal/wsdashboard -run 'Contract|CIRelease|Rail'`
  passed 246 tests and subtests. The release cases and
  `TestRailNavigationBrowser` ran, including browser and Tauri bridge
  navigation in both themes. The release cases passed again after the last
  workflow edit.
- `node --test --test-reporter=dot scripts/*.test.js scripts/test-*.cjs` from
  `desktop/` passed 40 tests. `python3 scripts/test-evaluation-feed.py`
  passed three tests, including CLI startup with a seven-file Tauri-only
  stable fixture and HTTP and fault checks. Ruby Psych parsed both workflows.
  Local documentation link targets and `git diff --check` passed.
- Independent lite review of `b98630ccf..bdbcd72c4` found no actionable
  findings. The lead owns the remote Tauri dry run after merge. Native app
  launches were excluded from local verification.
