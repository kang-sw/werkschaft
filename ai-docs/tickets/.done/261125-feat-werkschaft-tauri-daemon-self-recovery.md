---
title: "Werkschaft Tauri 데몬 자기 회복(Self-Recovery) 4계층 구현"
related:
  261121-feat-werkschaft-tauri-shell-return: "Tauri shell baseline; decision (2055) partially relaxed by (1585)=a"
  261122-feat-werkschaft-tray-notifications-full-quit: "Tray menu lifecycle and no-holder/no-daemon naming invariant"
  261123-chore-werkschaft-electron-retirement: "Electron retired; Tauri is the sole desktop shell"
  261122-chore-werkschaft-tauri-deferred-verification: "Deferred packaged macOS/Windows Tauri verification rows"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 8b5b321650f20e26
sage-review-completeness-reviewed: 8b5b321650f20e26
completed: 2026-11-25
---

# Four-layer Tauri daemon self-recovery, offline update bridge, and CI regression gate

## Background

During the `v0.52.0` release, an NSIS upgrade left stale `rsrc/`/`skills/` files inside `$INSTDIR\daemon`, causing `daemon_package::validate()` to reject the bundled daemon with the opaque static error `"Invalid daemon package"`. Because Werkschaft's Tauri updater bridge previously accepted `werkschaft:update:*` messages only from the connected dashboard origin (`pinned_url().is_some()`), users whose bundled daemon failed to validate or launch were stranded on an offline error screen with no self-update path, no fallback to the last known good (LKG) daemon, and no persistent shell log on Windows (`windows_subsystem = "windows"` drops `eprintln!`).

The hotfix `v0.52.1` patched the NSIS installer hook (`installer-hooks.nsh`), but the Tauri shell still lacks systemic recovery when a future release ships a broken bundled daemon or fails to start. This ticket implements a four-layer self-recovery architecture inside `agents-plugin-tool/desktop/` and wires Rust unit/regression tests into CI so no broken daemon package can brick downstream Tauri installs.

## Decisions

- **(1584)=a [policy, owner-confirmed] Full four-layer self-recovery package.** Implement all four layers in `agents-plugin-tool/desktop/`: (1) daemon-independent self-update recovery via both offline UI and native menus/tray, (2) in-place Last-Known-Good (LKG) staged-daemon fallback with safe GC, (3) structured `DaemonPackageError` diagnostics, cross-platform `daemon-start` stderr capture, persistent `shell.log`, and redesigned dark offline recovery UI, and (4) residual-file/upgrade/security Rust tests plus packaging and CI `cargo test` gates.
  - Rejected: Layer 1 only (offline self-update without LKG survival or persistent diagnostics); Layers 1–3 without Layer 4 CI regression protection.
- **(1585)=a [policy, owner-confirmed] Dual offline self-update entry points (partially relaxing `(2055)`).** Expose self-update actions both on the local offline recovery page (`ui/index.html`) AND in the native `Connection` submenu and system tray menu (`Check for Updates…` / dynamically updated `Restart to Update (vX.Y.Z)`). This partially relaxes `261121-feat-werkschaft-tauri-shell-return` decision `(2055)` (which kept update actions only in dashboard Settings) so users can update even when the dashboard cannot load or the window is hidden, while preserving `withGlobalTauri: false` and `no-native-authority` (`local: false`, `permissions: []`).
  - Rejected: Local recovery page only (unreachable when the window is hidden or blank); native menu/tray only (less discoverable on the failure screen).
- **(1590)=a [policy, owner-confirmed] Respect `daemon-minor-floor` for automatic LKG fallback; keep `daemon_start.go` write boundary unchanged.** Automatic LKG fallback selects only health-verified staged daemons whose version is `>= daemon-minor-floor`. Keep the `updateMinorFloor` call in `agents-plugin-tool/internal/wsdashboard/daemon_start.go` (`daemon_start.go:288-292`; the function itself is defined in `agents-plugin-tool/internal/wsdashboard/minor_floor.go#L186`; executed after lock, listen, `MigrateLegacy`, `wsauditstore.Open`, and `wsmeta.Open`/`migrate`, and before `writeEndpoint`/`Serve`) unchanged — pre-migration launch crashes already never raise the floor. When a newer-minor daemon fails after the floor has already advanced and no eligible `>= floor` LKG exists, block automatic cross-minor downgrade and display the minor-floor blocked offline recovery view directing the user to install a hotfix update or inspect logs.
  - Rejected: Moving or tightening `daemon_start.go`'s floor-write boundary in this ticket; adding a user-confirmed `--below-floor` forced rollback override.
- **(1591)=a [policy, owner-confirmed] Run Tauri Rust tests in both release and PR/develop CI.** Add `cargo test --locked --features custom-protocol` to both `.github/workflows/werkschaft-tauri-build.yml` and path-filtered `.github/workflows/ws-mcp-ci.yml` (triggered on changes under `agents-plugin-tool/desktop/**` and the workflow files on macOS and Windows runners, matching `main.rs` platform gates).
  - Rejected: Release workflow only (`werkschaft-tauri-build.yml` alone); manual/local verification only.
- **(1592)=a & (1593)=ok [policy & visual check, owner-confirmed] Approved dark-theme offline recovery UI mockup.** The visual reference for `ui/index.html`, the minor-floor blocked state, the LKG fallback toast/title badge, and the `Connection`/tray update menu labels is locked at `ai-docs/assets/mockups/261125/tauri-daemon-self-recovery/recovery-ui.html`. Verification must compare a rendered capture of the implemented UI against this mockup side by side.
- **(1594) [technical, auto-confirmed] LKG fallback applies only when no daemon is running.** The fallback launches an LKG stage only on the "no daemon running" start path (`running.is_none()`, `replace=false`). Bundled `select()`/`launch()`/`settle()` failures on the attach, `Intent::Replace`, and `Intent::Connect`/`Recover` paths that already have a running daemon keep their current outcomes (attach to the running daemon, or `refuse(...)` while keeping it); the fallback never replaces or stops a running daemon, preserving the `261113-feat-werkschaft-thin-tauri-shell` and `261115-feat-ws-daemon-release-auto-update` rules.
  - Rejected: Wiring LKG at every bundled failure point (could swap a running daemon for an older stage).
- **(1595) [technical, auto-confirmed] Stage GC protects by binary hash and by live use, not by path.** `last-running-executable.json`'s `executable` is the daemon's content-addressed copy under `<cache>/dashboard/bin/`, never inside a `daemon-<rand>/` stage, so a path match never fires. GC protects every stage whose `binary_sha256` equals the hash of that executable (several stages may share one hash), plus the stage of the currently discovered/running daemon (matched by endpoint version plus the `binary_sha256` of `<cache>/dashboard/last-running-executable.json`, which the singleton daemon writes before `writeEndpoint`, so a discoverable endpoint's hash is already current; the starter child's pid is not the daemon's pid and cannot be used), plus the stage just launched while its `settle()` is pending or timed out. Only then does it apply the newest-`N=2`-LKG retention and best-effort cleanup.
  - Rejected: Path matching against `last-running-executable.json`; deleting "failed/unverified" stages without checking live use (a late-healthy or still-running daemon on macOS would lose its `WS_RSRC_ROOT`/`WS_SKILLS_ROOT`/`CLAUDE_PLUGIN_ROOT` directory).
- **(1596) [technical, auto-confirmed] LKG is recorded only for the stage whose own process became the daemon.** After `settle()`, record the stage only when the settled daemon corresponds to the launched stage (matching endpoint version and the `last-running-executable.json` `binary_sha256` per `(1595)`, and not the pre-existing `running` daemon nor an `attach_winner` from a concurrent starter). Every shell-started launch creates a fresh `daemon-<rand>` path, so `lkg-index.json` is rewritten on each such launch whose verified stage path/version differs from the recorded entry; the zero-write guarantee applies to attach-only launches and to relaunches of an already-recorded stage.
  - Rejected: Recording any healthy daemon that `settle()` discovers.
- **(1597) [technical, auto-confirmed] Recovery gate and update UI cover `/index.html` only.** `/python-missing.html` receives no `recovery.js` or update card, so the disconnected gate authorizes `/index.html` only; `/python-missing.html` keeps its current behavior (and the no-Python path errors before `select()`, so it also gets no LKG fallback).
  - Rejected: Authorizing `/python-missing.html` for update/recovery messages without a UI that uses them (widens the bridge for no gain).
- **(1598) [technical, auto-confirmed] Retry, LKG ordering, and floor comparison.** `werkschaft:recovery:retry` re-runs the full connect sequence (select/validate, launch, settle, then LKG fallback per `(1594)`) and clears the previously recorded failure state before the attempt. "Newest" LKG means highest version (ties broken by most recent verification time). `lkg-index.json` stores, per entry, the stage directory name, version, `binary_sha256`, and verification timestamp. The `daemon-minor-floor` comparison uses the numeric major.minor pair only (matching Go's `MinorVersion`), reads `<cache>/dashboard/daemon-minor-floor`, and fails closed (automatic fallback blocked, minor-floor view shown) when the file exists but is malformed or unreadable, matching Go's `ReadMinorFloor`.
  - Rejected: Retry that only re-attempts bundled launch; ordering by verification time alone; three-part version comparison against the floor.
- **Compatibility tier: T0 (transparent).**
  - Existing daemon and updater contracts (`endpoint.json`, `/health`, `daemon-minor-floor`, `last-running-executable.json`, `WsToast`, `werkschaft-tauri-update.json`) are reused without changing any MCP tool, `/mcp` hello, shared daemon store, or tracker schema.
  - Readers/writers by direction:
    - Old shell / old `ws-mcp` reading new state: `<app_local_data_dir>/daemon-stages/lkg-index.json` and `<app_local_data_dir>/logs/shell.log` are shell-private files outside any `daemon-<rand>/` stage directory and outside `ws-mcp`'s cache/store directories; old shells and `ws-mcp` ignore them completely (`daemon_package.rs` `validate()` inspects only inside `daemon-<rand>/`).
    - New shell reading old state: when `lkg-index.json` is absent or empty (fresh upgrade from `0.52.0`/`0.52.1`), the new shell proceeds directly through normal bundled-daemon launch and records the first verified LKG entry upon `settle()`; if bundled launch fails with no LKG, it renders the offline recovery view cleanly.
    - Existing cross-minor daemon refusal remains T3 and is preserved untouched by `(1590)=a`.

## Constraints

- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`, `agents-plugin-tool/internal/wswhiteboard/assets/`, `ai-docs/assets/mockups/`)
- **Security boundary (`no-native-authority`):** Preserve `withGlobalTauri: false`, `no-native-authority` (`local: false`, `permissions: []`), and `script-src 'self'` in `tauri.conf.json`. Existing security tests (`pages_receive_no_native_command_permissions`, `only_the_title_strip_webview_has_permissions`) must continue passing without relaxing capability grants.
- **Zero new Cargo crates:** Implement all four layers using existing `agents-plugin-tool/desktop/Cargo.toml` dependencies (`tauri-plugin-updater`, `tauri-plugin-dialog`, `rfd`, `clipboard-win`/`arboard`, `open`, `serde_json`, `sha2`, etc.) without adding new crates.
- **Storage types and Windows AV file-touch budget (`ai-docs/manuals/performance.md`):**
  - `lkg-index.json` (`<app_local_data_dir>/daemon-stages/lkg-index.json`): **Type 5** small pointer/index file. Write ONLY when the `settle()`-verified staged daemon version changes (zero writes on steady-state happy-path launches). NEVER write metadata inside `daemon-<rand>/` (would break `daemon_package::validate()`).
  - `shell.log` (`<app_local_data_dir>/logs/shell.log`): **Type 1** held append-only file handle with 256 KiB × 2 rotation (no open/close per log line).
  - LKG fallback launch: re-validate `binary_sha256` and manifests **in place** without re-copying staged files through Windows antivirus (`AhnLab V3`).
- **Windows multi-webview quirk:** On Windows, the custom title strip is a sibling webview so `app.get_webview_window("main")` returns `None`; all update-state pushes and script evaluations targeting the main page MUST use `chrome::get(app, MAIN_WINDOW)` (`get_webview("main")`).
- **Tray naming invariant:** Tray menu items must NOT contain `"holder"` or `"daemon"` (preserving `menu_items_map_to_the_two_lifetimes` in `tray.rs`).
- **Visual parity:** The redesigned `ui/index.html` (`recovery.css` + `recovery.js`, `script-src 'self'`) must match the approved dark-theme mockup at `ai-docs/assets/mockups/261125/tauri-daemon-self-recovery/recovery-ui.html` (borderless Material `#141518`/`#1e2024` surface ladder, zero idle animations, zero `1px solid` card outlines) and be verified by side-by-side comparison.
- **No Go daemon/store changes:** Per `(1590)=a`, do not modify `agents-plugin-tool/internal/wsdashboard/daemon_start.go` or any `ws-mcp` protocol/store code.

## Prior Art

- `agents-plugin-tool/desktop/src/updates.rs`, `main.rs`, `windows.rs`, `macos/webview_delegate.rs`, `navigation.rs`: existing native `postMessage` update bridge (`window.chrome.webview` on Windows, `window.webkit.messageHandlers.werkschaftUpdate` on macOS) and `Destination::{Dashboard, Local}` origin classifier.
- `agents-plugin-tool/desktop/src/daemon.rs`, `daemon_package.rs`, `shell.rs`: existing bundled-daemon staging, `validate()`, `select()`, `launch()`, `settle()`, and `Connection::show_refusal()` / `daemon-notice.js` sticky `WsToast.show` notification pipeline.
- `agents-plugin-tool/desktop/src/close.rs` and `tray.rs`: existing `close::drafts_saved`, `close::confirm`/`close::inform` native dialogs, and tray menu construction.
- `agents-plugin-tool/desktop/scripts/test-package.cjs` and `installer-hooks.nsh`: existing packaging verification script and NSIS `$INSTDIR\daemon` cleanup hook.

## Prior Decisions

- 261115-feat-ws-dev-daemon-accepted-build-recovery (2026-11-15, Decisions): "(706)=a: keep dead-daemon recovery separate from replacing a running daemon. Automatically restart a dead daemon using the last-known-good build, never an unaccepted newer candidate." — bearing: supports
- 261115-feat-ws-dev-daemon-accepted-build-recovery (2026-11-15, Decisions): "(709)=a: a candidate earns last-known-good status after post-start checks covering hello, session lookup, whiteboard read and mailbox receive, followed by approximately 30 minutes of quiet observation" — bearing: contradiction-candidate
- 261115-feat-ws-daemon-release-auto-update (2026-11-15, Decisions): "Shell daemon rule gains one exception. ... the shell attaches, starts a daemon only when none runs, never replaces or stops one" — bearing: constrains
- 261113-feat-werkschaft-thin-tauri-shell (2026-11-13, Decisions): "the shell attaches to whatever daemon is running, starts its beside-exe daemon only when none is, and never replaces or stops a running daemon." — bearing: constrains
- 261123-chore-werkschaft-electron-retirement (2026-11-25, Result): "Removed the Electron source tree, build and publish jobs ... The Tauri workflow handles exact stable and canary tags." — bearing: supports
- 93710e79 (commit): "Root cause confirmed on an affected install: replicating daemon_package.rs validate() found exactly one failure, the unlisted .pi.md (plus a stale bin/ws-pi-payload.py ...)" — bearing: supports
- b0c14a9c (2026-11-21, commit): "Owner answer (2070)=a requires the first launcher run to install its exact bundled version, including downgrades, and later runs to preserve Tauri self-updates." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/desktop/src (updates.rs, main.rs, windows.rs, macos/webview_delegate.rs, tray.rs, navigation.rs, daemon.rs, daemon_package.rs, shell.rs), desktop/ui, scripts/test-package.cjs, two workflows |
| scope.surface | cross-module | postMessage update bridge authorization, native menu/tray, daemon staging, CI workflows; no exported Go symbol changes |
| scope.new_public_symbol | yes | DaemonPackageError enum, lkg-index.json and shell.log file formats, werkschaft:recovery:* message enum |
| scope.new_type_contract | yes | DaemonPackageError replaces Result<_, &'static str> at daemon_package.rs#L198 and L326; daemon.rs#L130 select signature |
| scope.test_surface | existing | in-crate tests in main.rs#L472, tray.rs#L87, daemon_package.rs#L471, daemon.rs#L479; scripts/test-package.cjs; new ui assets need capture checks |
| complexity.reuse_points | confirmed | Connection::show_refusal shell.rs#L195, daemon-notice.js, close::confirm/inform, Destination classifier navigation.rs#L12-L39, Controller::publish updates.rs#L367 |
| complexity.side_effect_risk | high | changes the postMessage authorization gate, daemon launch/fallback path, and stage GC that can delete installed daemon files on Windows |
| risk.correctness | high | LKG selection, minor-floor guard, and GC protection of last-running-executable are subtle and cannot be exercised end to end locally |
| risk.fit | moderate | four layers across Rust, JS/CSS, and CI in one phase; Windows multi-webview quirk applies |
| risk.test | high | native menu, tray, and WebView behavior plus macOS and Windows CI runners; no cargo step exists yet in either workflow |
| risk.security_or_contract | high | widens the Local-origin update bridge while tauri.conf.json keeps withGlobalTauri false and script-src self; the window.url and epoch checks carry the security boundary |

Scope note: decision `(709)=a` of `261115-feat-ws-dev-daemon-accepted-build-recovery` (post-start checks plus about 30 minutes of quiet observation before a build counts as last known good) governs the dev daemon's accepted-build recovery, not the Tauri-bundled daemon. This ticket's LKG criterion for the bundled daemon is the existing `settle()` health check, per `(1584)=a`; it does not alter or reverse `(709)=a`.

## Phases

### Phase 1: Four-layer Tauri daemon self-recovery, offline recovery UI, and CI regression gate

Deliver all four self-recovery layers in `agents-plugin-tool/desktop/` and wire CI regression gates.

#### Layer 1 — Daemon-independent self-update recovery (`updates.rs`, `main.rs`, `windows.rs`, `macos/webview_delegate.rs`, `tray.rs`, `navigation.rs`)

- **Mutually exclusive `postMessage` authorization gate:**
  - When `pinned_url().is_some()` (connected): ONLY top-frame `Destination::Dashboard` is authorized for `werkschaft:update:*`; `Destination::Local` is rejected.
  - When `pinned_url().is_none()` (disconnected/offline): ONLY `main_frame == true` AND `destination(source, None) == Destination::Local` (exact `/index.html` only per `(1597)`, no query string, no fragment, no subframe) AND `window.url() == Destination::Local` AND `epoch == window_epoch()` AND `!closing` is authorized for:
    - `werkschaft:update:*` (check, download/install, restart, channel status)
    - Fixed enum `werkschaft:recovery:{retry,open-logs,copy-diagnostics}` (no arbitrary path/command payloads).
- **State publishing & install path:**
  - Update `Controller::publish()` so when `pinned_url().is_none()` it does not early-return; instead push update state to `LOCAL_ORIGIN` via `chrome::get(app, MAIN_WINDOW)`.
  - Add a disconnected `Destination::Local` branch to `Controller::install()` that skips dashboard `draft_ready` (no dashboard loaded) while preserving `connection.closing` and epoch checks.
  - When invoked from native menus or tray while connected, check `close::drafts_saved` across `chrome::all(app)` before applying an update.
  - `Controller::install` currently takes a `WebviewWindow` and `close::release_document`; the menu/tray-initiated and hidden-window install branches must obtain the main webview through `chrome::get(app, MAIN_WINDOW)` (Windows `get_webview_window("main")` is `None`) and adapt those two call sites accordingly.
- **Native `Connection` submenu & Tray menu entry points (`(1585)=a`):**
  - Add `Check for Updates…` inside the existing `Connection` submenu (keeping Windows `strip.html` and top-level `accelerator::MENUS` unchanged) and in the Tray menu (preserving the no-`"holder"`/no-`"daemon"` invariant).
  - Store `MenuItem<Wry>` handles in app state so menu labels dynamically update to `Restart to Update (vX.Y.Z)` when an update is ready.
  - When the window is hidden or disconnected/blank, surface check results and install confirmation through native `close::confirm`/`close::inform` dialogs; for dev/unbundled builds (`pubkey` empty or `Platform::installed().is_none()`), disable or show an informative notice cleanly without panicking.

#### Layer 2 — Health-verified LKG staged-daemon fallback & safe GC (`daemon.rs`, `daemon_package.rs`, `shell.rs`)

- **LKG index (`<app_local_data_dir>/daemon-stages/lkg-index.json`, Type 5):**
  - Record a staged daemon directory as LKG ONLY after `settle()` (health check) succeeds for the stage that was actually launched (`(1596)`), and rewrite `lkg-index.json` only when the verified stage version/path changes. Entry fields and ordering follow `(1598)`. Never write files inside `daemon-<rand>/`.
- **In-place LKG fallback with `daemon-minor-floor` guard (`(1590)=a`, scoped by `(1594)`):**
  - Only on the no-daemon-running start path: if bundled `select()` (including `validate()`) or bundled `launch()`/`settle()` fails, read `daemon-minor-floor` (when present; numeric major.minor, fail closed per `(1598)`) and select the newest health-verified LKG stage whose version satisfies `version >= daemon-minor-floor`.
  - Re-validate `binary_sha256` and manifests in place (no file recopy) and launch it.
  - If no eligible `>= floor` LKG exists (either no LKG recorded or all LKG stages are below `daemon-minor-floor`), transition to the offline recovery view with the structured reason (including explicit minor-floor block context when applicable).
- **Fallback running notice & automatic hotfix check:**
  - When running on an LKG fallback daemon, display a sticky warning toast via existing `Connection::show_refusal()` / `daemon-notice.js` (`WsToast.show` warn tone) plus window title badge (`Werkschaft — Safe Fallback vX.Y.Z`) explaining that the bundled daemon failed and LKG `vX.Y.Z` is running, and immediately trigger a background update check.
- **Safe stage GC:**
  - Retain the newest `N = 2` health-verified LKG stages AND always protect the stages named in `(1595)` (hash match with `<cache>/dashboard/last-running-executable.json`, the running/discovered daemon's stage, and a just-launched pending stage).
  - Clean up only failed, unverified, or older stages that are not protected, on a best-effort basis, ignoring Windows `ERROR_SHARING_VIOLATION` / `AccessDenied`.

#### Layer 3 — Structured error diagnostics, persistent `shell.log`, and dark offline recovery UI (`daemon_package.rs`, `daemon.rs`, `ui/index.html`, `ui/recovery.js`, `ui/recovery.css`)

- **Structured `DaemonPackageError`:**
  - Replace the single opaque `"Invalid daemon package"` string with a structured enum capturing failure variant and offending relative path (e.g., `UnexpectedFile { tree, path }`, `HashMismatch { path }`, `MissingManifest`, `InvalidMetadata`, `SymlinkRejected`) with token/sensitive-path redaction.
- **Cross-platform `daemon-start` stderr capture:**
  - Extend the bounded 16 KiB redacted `daemon-start` stderr capture from Windows-only to macOS as well; include exit code, redacted stderr tail, and `daemon-minor-floor` rejection status in the structured diagnostic payload.
- **Persistent `shell.log` (`<app_local_data_dir>/logs/shell.log`, Type 1):**
  - Keep a single held append-only file handle with 256 KiB × 2 rotation so startup, staging, validation, LKG fallback, and updater diagnostics survive on Windows (`windows_subsystem = "windows"`).
- **Typed diagnostic payload to the Local page:**
  - `Connection.refusal` (`Option<(usize, &'static str)>`) and `show_local(reason: &str)` carry only a title string. Add a typed payload (failure variant, offending path, bundled version, LKG status and version, minor-floor block flag, exit code, redacted stderr tail, `shell.log` path) delivered to the Local page by `eval` through `chrome::get(app, MAIN_WINDOW)`; the page URL stays exactly `/index.html` with no query string or fragment. The same payload drives the fallback toast/title badge version text.
- **Offline recovery UI (`ui/index.html` + `ui/recovery.js` + `ui/recovery.css`):**
  - Strictly comply with `script-src 'self'` (no inline scripts) and `ai-docs/manuals/dashboard-style.md` (borderless Material `#141518` / `#1e2024` / `#2b2d32` tonal ladder, zero `1px solid` card borders, zero idle animations).
  - Match the approved mockup at `ai-docs/assets/mockups/261125/tauri-daemon-self-recovery/recovery-ui.html`:
    - Structured failure card (bundled version, error variant, offending path, LKG status, and minor-floor blocked variant when applicable).
    - Live Self-Update Recovery Card (checking / update available `Download & Install` / static progress bar / `Restart to Update (vX.Y.Z)` / dev-build notice).
    - Action bar: `Retry`, `Copy Diagnostics` (copies redacted diagnostic JSON/text to clipboard), `Open Logs` (opens `<app_local_data_dir>/logs/`).
    - Collapsible `<details>` diagnostic preview showing exit code, redacted `daemon-start` stderr tail, and `shell.log` path.

#### Layer 4 — Regression tests, packaging verification, and CI gates

- **Rust regression tests (`cargo test --locked --features custom-protocol`):**
  - Residual file in `rsrc/` or `skills/` → structured `DaemonPackageError::UnexpectedFile { tree, path }` diagnostic + automatic in-place LKG fallback + fallback notice trigger.
  - Bundled daemon `launch()` / `settle()` failure → LKG fallback + failed stage GC (while preserving `N=2` LKG and the `(1595)` protected stages: hash-matched `last-running-executable.json`, running daemon's stage, pending launch).
  - With a daemon already running, bundled `select()`/`launch()`/`settle()` failure never launches an LKG stage (`(1594)`); LKG is not recorded for an `attach_winner`/pre-existing daemon (`(1596)`); malformed `daemon-minor-floor` blocks fallback (`(1598)`); `retry` re-runs the full sequence and clears failure state.
  - Native `Connection` submenu and tray: `Check for Updates…` → `Restart to Update (vX.Y.Z)` label transition on the stored `MenuItem` handles, plus a new `tray.rs` `Action` variant for the update item with `menu_items_map_to_the_two_lifetimes` still passing.
  - `daemon-minor-floor` > LKG version → automatic fallback blocked and minor-floor blocked diagnostic emitted (`(1590)=a`).
  - `Destination::Local` mutual-exclusion `postMessage` gate security tests (connected blocks `Local`; disconnected rejects query/fragment/subframe/stale-epoch/closing and non-enum recovery actions) + existing `pages_receive_no_native_command_permissions`, `only_the_title_strip_webview_has_permissions`, and `menu_items_map_to_the_two_lifetimes` invariants passing.
  - `lkg-index.json` Type 5 no-rewrite on unchanged version and `shell.log` Type 1 256 KiB × 2 rotation tests.
- **Packaging verification (`agents-plugin-tool/desktop/scripts/test-package.cjs`):**
  - Verify `installer-hooks.nsh` is wired in Tauri bundle config and contains `RMDir /r "$INSTDIR\daemon"` so stale daemon files are wiped on NSIS upgrades (already asserted at `agents-plugin-tool/desktop/scripts/test-package.cjs#L75-L80`; this item only needs to keep that assertion passing).
- **CI workflows (`(1591)=a`):**
  - Add `cargo test --locked --features custom-protocol` to `.github/workflows/werkschaft-tauri-build.yml` (macOS and Windows).
  - Add a path-filtered Tauri Rust test job (`cargo test --locked --features custom-protocol`) to `.github/workflows/ws-mcp-ci.yml` triggered when `agents-plugin-tool/desktop/**` or the workflow changes. `ws-mcp-ci.yml` already triggers pull requests on all of `agents-plugin-tool/**`, so the desktop job needs a job-level path condition rather than a workflow-level filter. The job must install the pinned Rust toolchain (1.95.0) and provide whatever resources `tauri::generate_context!` needs on the macOS and Windows runners; confirm how a path-skipped job interacts with the release gate before relying on it.

#### Verification

- Run `cargo test --locked --features custom-protocol` on both macOS and Windows (via CI or target runner) and `node agents-plugin-tool/desktop/scripts/test-package.cjs`.
- Verify zero new Cargo crates were added in `agents-plugin-tool/desktop/Cargo.toml` / `Cargo.lock`.
- Visually capture the rendered offline recovery UI (`ui/index.html` primary failure state, minor-floor blocked state, and LKG fallback toast/title badge) and compare side by side against `ai-docs/assets/mockups/261125/tauri-daemon-self-recovery/recovery-ui.html`.
- Verify `dashboard-style.md` compliance (dark tonal surfaces, no `1px solid` card boxes, no idle animations, `script-src 'self'` with zero CSP console errors).

### Result (e7da5be6a) - 2026-11-25

Landed in `e5dfa9a25` (recovery page), `9e1e9cfa5` (Rust layers 1–4 + CI), `e7da5be6a` (review follow-ups).

- **Layer 1.** `updates::authorized` returns `Origin::{Dashboard, Local}` and is mutually exclusive: pinned → top-frame dashboard only; unpinned → exactly `local_view("/index.html")` (query, fragment, `/python-missing.html`, the other platform's origin all refused). `admit` adds the `window.url()` re-check, window epoch, and `closing`; `route` sends `werkschaft:recovery:{retry,open-logs,copy-diagnostics}` (exact-match enum) only from `Origin::Local`. Windows registers the message handler for main always (`updates::bridged`, 40-byte cap); macOS adds a `werkschaftRecovery` script handler. `publish` pushes to the recovery page while disconnected and relabels the stored `MenuItem` handles (`UpdateItems`); `install` takes `Requester::{Dashboard, Local, Native}` (Local skips `draft_ready`; Native checks `close::drafts_saved` across `chrome::all` only while connected). `Check for Updates…` sits in the `Connection` submenu (after a separator) and in the tray after `Show Werkschaft` (`tray::Action::CheckUpdates`); menu/tray flows answer through native `close::confirm`/`close::inform`, and builds without an updater say "Updates unavailable".
- **Layer 2.** `lkg.rs`: `lkg-index.json` (schema 1; stage, version, `binary_sha256`, `verified_at`), at most one entry per version, `RETAIN = 2`, rewritten (tmp + rename) only when the set changes; `Floor` reads `<cache>/dashboard/daemon-minor-floor` as major.minor and fails closed; `collect` keeps the recorded stages, digest matches with `last-running-executable.json`, the running daemon's version, and the just-launched stage, renaming a stage to `<name>.gc` before removal so a Windows stage in use is skipped whole. `daemon.rs`: records only when the stage's own process became the daemon (spawned, not the incumbent, version + last-running digest match); `fall_back` runs only on the no-daemon path (select, stage, or launch/settle failure), tries eligible stages newest first with in-place `Candidate::reopen`, and reports `LkgState::{None, Blocked, Invalid, Failed, Running}`. A fallback attach shows a sticky `WsToast` warn notice with a `Check for Updates` action, the `Werkschaft — Safe Fallback vX.Y.Z` title badge, and starts a background update check.
- **Layer 3.** `DaemonPackageError` (`InvalidRoot`, `MissingFile`, `SymlinkRejected`, `UnsafePath`, `MissingManifest`, `UnexpectedFile { tree, path }`, `HashMismatch`, `InvalidMetadata`, `Unavailable`) with package-relative paths replaces `"Invalid daemon package"` in `validate()`/`select()`. `daemon-start` stderr is piped on macOS too and read to EOF keeping a 16 KiB tail (redacted) with the exit code. `shell_log.rs`: one held append handle, 256 KiB × 2 (`shell.log`, `shell.log.1`). `diagnostics::Diagnostics` (camelCase payload) reaches `/index.html` by evaluation (`recovery::push`); `ui/index.html` + `recovery.js` + `recovery.css` render the idle, failure, floor-blocked, and update-card states and the diagnostics `<details>`.
- **Layer 4.** New Rust tests cover every rule above (109 tests total). `werkschaft-tauri-build.yml` runs `cargo test --locked --features custom-protocol` on both package legs. `ws-mcp-ci.yml` adds `desktop change detection` + `cargo test (desktop, macos-latest|windows-latest)`; release-gate interaction checked: `ws-mcp-release.yml` accepts only the two long legs as skipped, so `desktop-changes` answers true outside pull requests and the ship flow's dispatch run always runs the desktop job; it skips only on PRs that leave the shell and both workflows untouched.

Verification:

- `cargo +nightly test --locked --features custom-protocol` (Windows; local stable is 1.94 < `rust-version` 1.95): 109 passed, 0 warnings. macOS compile and tests are left to the new CI desktop job (no macOS host here).
- `node --test scripts/test-package.cjs`: 4 passed (NSIS `RMDir /r "$INSTDIR\daemon"` assertion intact). `node --test scripts/test-daemon-notice.cjs`: skipped (no Playwright here).
- `Cargo.toml`, `Cargo.lock`, `tauri.conf.json`: unchanged (zero new crates; `pages_receive_no_native_command_permissions` and `only_the_title_strip_webview_has_permissions` pass).
- Visual: headless-Chrome captures of the failure (UnexpectedFile + update ready), floor-blocked, idle, downloading, and error states compared side by side with the mockup's Sections 1–2: match (one state per card instead of the mockup's combined state preview; the floor-blocked action row sits below the nested update card). The fallback toast compared with Section 3A: mismatch by design — the ticket requires the existing `WsToast` warn pipeline, whose surface is inverted and single-text, and recovery actions are refused on the dashboard, so the toast offers `Check for Updates` + dismiss but no `Open Logs`. Native menu and tray rows are not capturable.
- CSP: the shipped page served with the production CSP header rendered fully (scripts and styles applied); a control inline script was blocked. No `1px solid`, animation, transition, inline style, or inline script in the page files.

Decisions: a no-daemon `stage()` failure also falls back (superset of select/launch/settle); collection also keeps any stage of the running daemon's version (superset of version + digest); tray keeps its existing labels and adds the update item after `Show Werkschaft` (the mockup's `Open`/`Hide`/`Quit` wording would break the tray label tests); menu/tray update flows always use native dialogs.
