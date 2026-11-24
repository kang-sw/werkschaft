# Werkschaft Tauri shell

Werkschaft opens the dashboard at its verified `http://127.0.0.1:<port>/`
origin. It discovers the endpoint under `WS_CACHE_HOME` or the default ws
cache root and starts the bundled daemon when needed. A newer numeric core
replaces an older daemon, including across minors. **Connection → Replace
daemon (same core)** also allows an equal-core swap. **Connection → Retry**
(`Cmd+R` on macOS, `Ctrl+R` on Windows) repeats discovery and connection.
Update and restart plugins on the previous minor after a cross-minor swap.

macOS requires Python 3 for daemon start and replacement. Windows prefers an
independent daemon process and retries inside the containing Windows job if
the OS reports access denied for breakaway. A job-lifetime daemon ends when
its containing job is torn down.

Werkschaft has two lifetimes: the app, and the background (the daemon and
every session it runs). The window's close button hides the app to the tray
without asking about drafts. In the tray menu, **Close Werkschaft** (also the
app menu's or the system's quit) exits the app and keeps the background
running. **Quit Werkschaft and Stop the Background…** stops every session and
exec job, then the daemon. It asks first when any are running, with their
count.
Both exits ask first when a draft may not be saved. A full quit that cannot
stop everything says what remains and keeps the app open. So does one against
an older daemon without the full stop, which stops nothing. When another
client starts a new background right after, the app reports it. Left-clicking
the tray icon shows the window on Windows and opens the menu on macOS.

The main window notifies through the OS, also while hidden in the tray: when a
session waits on an approval, when a board question opens, and when a
top-level session is fully idle (its turn ended and none of its children,
background tasks, or exec jobs still run). A child finishing under a busy
parent does not notify. A hosted session's own exec jobs keep it busy until
they end. Jobs a terminal session runs through ws are counted per worktree,
not per session: a session that worked notifies only once every such job in
its worktree has ended, and a session that sat idle while another session's
job ran does not notify. A session whose harness does not report its
background tasks (Codex) may notify as soon as its turn ends. Other windows
stay silent. When the OS refuses or fails a notification, the dashboard shows
its own (the browser notification, or a toast) instead, never both. Clicking
an OS notification does not open the session yet.

For development, set `WERKSCHAFT_DAEMON_ROOT` to an absolute path to a prepared
candidate directory before launching Tauri. It has the same layout as the
bundled input and needs the current daemon starter. A development candidate
may differ from its runtime contract by patch version. The override belongs
to the Tauri launch environment.

## Build and run

Use Rust 1.95 and the native toolchain (Xcode Command Line Tools on macOS,
MSVC on Windows). Windows runs on Microsoft Edge WebView2 Evergreen, version
122 or later (`ICoreWebView2_22`, which the page boundary requires); with an
older runtime the app says to update it and does not open the dashboard.

```sh
cargo test --locked --features custom-protocol
cargo build --release --locked --features custom-protocol
```

For a Phase 2 packaged build, first build a daemon for the target platform
with the current generated inventory embedded and matching version metadata.
For example, from the repository root on macOS arm64:

```sh
cd agents-plugin-tool
python3 scripts/generate-package-assets.py
go build -trimpath -ldflags '-s -w -X main.version=0.51.0-dev' -o /tmp/ws-mcp-darwin-arm64 ./cmd/ws-mcp
python3 scripts/write-runtime-metadata.py /tmp/ws-mcp-darwin-arm64 --version 0.51.0-dev
node desktop/scripts/assemble-daemon-input.cjs --binary /tmp/ws-mcp-darwin-arm64 --out /tmp/werkschaft-daemon-input --platform darwin
cd desktop
npm ci --ignore-scripts --no-audit --no-fund
WERKSCHAFT_DAEMON_INPUT=/tmp/werkschaft-daemon-input npm run build:package
```

Use the checkout's current plugin version in both build and metadata, with
the same suffix. The assembler requires a fresh output path. The packaging
entry point validates the full package, binary architecture and embedded
inventory, then maps every file to `resource_dir/daemon`. It validates the
resulting app at `target/release/bundle/macos/Werkschaft.app`.

On Windows x64, from `agents-plugin-tool` in PowerShell:

```powershell
python scripts/generate-package-assets.py
go build -trimpath -ldflags '-s -w -X main.version=0.51.0-dev' -o "$env:TEMP\ws-mcp-windows-amd64.exe" ./cmd/ws-mcp
python scripts/write-runtime-metadata.py "$env:TEMP\ws-mcp-windows-amd64.exe" --version 0.51.0-dev
node desktop/scripts/assemble-daemon-input.cjs --binary "$env:TEMP\ws-mcp-windows-amd64.exe" --out "$env:TEMP\werkschaft-daemon-input" --platform win32
cd desktop
npm ci --ignore-scripts --no-audit --no-fund
$env:WERKSCHAFT_DAEMON_INPUT = "$env:TEMP\werkschaft-daemon-input"
npm run build:package
```

The Windows smoke payload is `target/release/phase2-smoke/`, with the
executable and `daemon/` together. Remove an earlier smoke output before
rebuilding. Use `build:package` to prepare the validated resource layout
for smoke tests. See [Release packaging](RELEASE.md) for signed installers,
updates, and workflow artifacts.

Run the packaging checks with `node --test scripts/test-package.cjs`.
Run the notice browser test with `WS_PLAYWRIGHT_MODULE` pointing to
Playwright and `node --test scripts/test-daemon-notice.cjs`.
Run the theme bridge test with `node --test scripts/test-theme-bridge.cjs`.
Run the title strip test with `node --test scripts/test-strip.cjs`.

`scripts/build-slot.py` remains the bounded debug build runner from the
attach-only research. Its process-group regressions can run without Cargo:

```sh
python3 -m unittest discover -s scripts -p test_build_slot.py -v
```

## Shell boundary

- The endpoint bearer stays native. The app health-checks the recorded daemon,
  requests a machine-authenticated browser cookie, and loads the dashboard
  login gate when cookie setup fails. **Copy Browser Login Link to Clipboard**
  mints a path-scoped link and writes it directly to the system clipboard.
- The app allows its connection and Python guide only as top-level bundled
  documents. Those pages and the dashboard have no Tauri IPC permissions; the
  one page with any is the Windows title strip, below. Approved dashboard
  frames stay on the pinned origin or use blank/srcdoc documents. External
  HTTP(S) links open in the system browser from top-level links and popups.
  Other schemes are blocked. Popup requests never create a second webview.
- Downloads are accepted only from the pinned dashboard origin and saved to the
  user's Downloads folder under the server's file name, cleaned to one path
  component and numbered `name (1).ext` rather than overwriting; a native
  notification reports the end. macOS needs the navigation delegate to hand
  WebKit's download to Wry; on Windows the same handler replaces WebView2's
  default download UI (Wry already suppresses it).
- The renderer uses the dashboard's existing draft storage. The native close
  asks the pinned top-level renderer whether it recovered the draft and shows
  the native **Stay / Close** prompt if recovery fails or the renderer is too
  old to answer. Native status and recovery diagnostics contain fixed messages.
- The View menu provides zoom and release DevTools
  (`Cmd+Option+I` on macOS, `Ctrl+Shift+I` on Windows, or `F12`).
  Starting a second instance restores the original window.
- On Windows the main window has no system title bar or menu bar. One strip
  (34 logical pixels, the bundled `ui/strip.html`) across its top holds the
  icon, the window title, the menu labels (File, Edit, Window, Help,
  Connection, View) and the minimize, maximize and close buttons; the dashboard
  fills the rest. The strip is its own child webview, labelled `main-strip`,
  and it is the only page with Tauri IPC. The `title-strip` capability grants
  it, by webview label and not by window, five fixed commands and no plugin or
  core permission: `strip_popup` (show one of the existing native menus at a
  label), `strip_minimize`, `strip_toggle_maximize`, `strip_close` and
  `strip_drag`. Each command also refuses any other webview. The menu items,
  accelerators and actions are the same ones the macOS menu bar has. The
  shell pushes state into the strip with fixed scripts (maximized, focused,
  title, theme, and the maximize button's hover, which the shell hit-tests so
  Windows 11 can offer its snap layouts there), and the page sends only the
  five commands.
  The close button takes the window's usual close, which hides the app to the
  tray. The strip's webview cannot navigate away from its page, opens no
  windows, and has DevTools, context menus and the browser's keys off. Page
  zoom changes only the dashboard's webview. Secondary windows keep the system
  title bar and menu bar.
- The strip follows the dashboard's theme. The dashboard's page gets a small
  injected bridge (`theme-bridge.js`, installed like the notify bridge in the
  main window's pinned top frame only) that posts the resolved theme, the
  pinned `data-theme` or else the OS preference, as the exact text
  `werkschaft:theme:light` or `werkschaft:theme:dark` through
  `chrome.webview.postMessage`. The shell accepts it only from the pinned
  dashboard origin's top frame in the current window generation, and parses
  nothing else out of it. Until the dashboard reports, the strip follows the OS
  preference.
- Without a menu bar, **F10** and a lone **Alt** open the File menu and
  **Alt+F/E/W/H/C/V** open File, Edit, Window, Help, Connection and View, from
  the dashboard or the strip. The shortcuts the menu items carry still work
  from the focused webview as before, and **Ctrl+M** minimizes, as the Window
  menu's item does. The maximize button offers the Windows 11 snap layouts: it
  is a native hit-test region, not part of the strip page.

## Shell updates

Signed installations provide the App update controls in Settings. Ordinary
checks look for stable releases. **Update to canary** looks for a newer
canary when clicked. **Back to stable** appears on canary builds and asks
before downloading the current stable build, including a downgrade.
The installed build supplies the channel and version.

The shell checks the artifact size, SHA-256, and version-bound signature
before offering a restart. Restart waits for the dashboard to save its draft.
Use the existing Connection menu for explicit daemon replacement. The
shared daemon lifecycle resumes on shell relaunch under the usual version
ordering.

If download or verification fails, check again from Settings and retry.
If installation fails, reopen Werkschaft and retry the staged update or
reinstall from the release page. Existing Electron users must manually install
Tauri from the release page. Tauri uses its own WebView profile and starts
with default window settings.

On a disposable smoke host, point the signed evaluation builds at the
downloaded dry-run artifacts with:

```sh
python3 agents-plugin-tool/desktop/scripts/evaluation-feed.py --stable tauri-stable --canary tauri-canary
```

Use `--fault missing-sha`, `--fault bad-signature`, `--fault bad-bytes`,
or `--fault interrupted` for refusal and retry tests. For stable to canary
and lower-stable return in one run, use a canary from an evaluation branch
with the next patch core in the same minor.
