# Werkschaft Tauri release packaging

The desktop release builds use the macOS arm64 and Windows x64 binaries and
metadata from the `ws-mcp-assets` workflow artifact. Each platform assembles a
wsflow package and validates its version, architecture, manifests, inventory,
and daemon hash without executing the daemon. The validated daemon version
sets the app and updater version. Use the checkout's numeric plugin version
or that version with `-canary.N`.

## Actions dry runs

Configure repository variable `WERKSCHAFT_EVAL_UPDATER_PUBLIC_KEY` and secret
`WERKSCHAFT_EVAL_UPDATER_PRIVATE_KEY` with the dedicated evaluation key pair.
Set secret `WERKSCHAFT_EVAL_UPDATER_KEY_PASSWORD` if that key has a password.
These values are the base64-encoded Tauri key contents. Applications built
with `tauri_dry_run` use the evaluation public key and fetch test releases
through `http://127.0.0.1:43137`. Feed JSON and the release API's asset URLs
still use their production GitHub addresses.

From the repository root, dispatch both channels on a remote branch with the
candidate workflow and code. Use `gh run list` to identify the two run IDs.

```sh
ref='<candidate-branch>'
core=$(jq -r '.plugin_version' agents-plugin-wsflow/runtime.json)
canary="$core-canary.1"
gh workflow run ws-mcp-release.yml --repo kang-sw/werkschaft --ref "$ref" -f tauri_dry_run=true -f version="$core"
gh workflow run ws-mcp-release.yml --repo kang-sw/werkschaft --ref "$ref" -f tauri_dry_run=true -f version="$canary"
gh run list --repo kang-sw/werkschaft --workflow ws-mcp-release.yml --branch "$ref" --event workflow_dispatch --limit 10
```

Watch and download each successful run. Load the evaluation public key into
the shell for verification.

```sh
stable_run='<stable-run-id>'
canary_run='<canary-run-id>'
export WERKSCHAFT_UPDATER_PUBLIC_KEY="$(gh variable get WERKSCHAFT_EVAL_UPDATER_PUBLIC_KEY --repo kang-sw/werkschaft --json value --jq .value)"
gh run watch "$stable_run" --repo kang-sw/werkschaft --exit-status
gh run watch "$canary_run" --repo kang-sw/werkschaft --exit-status
gh run download "$stable_run" --repo kang-sw/werkschaft -n werkschaft-tauri-assets -D tauri-stable
gh run download "$canary_run" --repo kang-sw/werkschaft -n werkschaft-tauri-assets -D tauri-canary
node agents-plugin-tool/desktop/scripts/release.cjs --verify --input tauri-stable --version "$core"
node agents-plugin-tool/desktop/scripts/release.cjs --verify --input tauri-canary --version "$canary"
```

Both runs retain `ws-mcp-assets` and the combined `werkschaft-tauri-assets`.
Both channels contain the three packages listed below, their `.sig` files,
and `werkschaft-tauri-update.json`. `tauri_dry_run=true` vetoes native and
Tauri publication, including a dispatch on a tag. Supply an explicit numeric
checkout version or that version with `-canary.N`. The workflow rejects the
default development version.

## Local signed packages

Use Node 24, Python 3, Rust 1.95.0, and the native build tools. macOS needs
Xcode Command Line Tools. Windows needs the x64 MSVC build tools, Windows SDK,
and `tar.exe`. Tauri can fetch the pinned NSIS and WiX tools and the WebView2
Evergreen bootstrapper on Windows. The embedded bootstrapper needs network
access on a machine that has to download the Evergreen runtime.

From the repository root, assemble the daemon using the binary and adjacent
`.metadata.json` from `ws-mcp-assets`. Supply fresh output directories. Load
`WERKSCHAFT_UPDATER_PUBLIC_KEY`, `TAURI_SIGNING_PRIVATE_KEY`, and, when needed,
`TAURI_SIGNING_PRIVATE_KEY_PASSWORD` through the shell environment. Use the
evaluation pair and `WERKSCHAFT_EVALUATION=1` for loopback test builds. Use
the production pair with `WERKSCHAFT_EVALUATION` unset for production builds.

On macOS arm64, put `ws-mcp-darwin-arm64` and its metadata under
`$PWD/ws-mcp-assets` and run the following.

```sh
output=$(mktemp -d)
node agents-plugin-tool/desktop/scripts/assemble-daemon-input.cjs \
  --binary "$PWD/ws-mcp-assets/ws-mcp-darwin-arm64" --out "$output/daemon" --platform darwin --package wsflow
cd agents-plugin-tool/desktop
npm ci --ignore-scripts --no-audit --no-fund
WERKSCHAFT_DAEMON_INPUT="$output/daemon" WERKSCHAFT_RELEASE_OUTPUT="$output/tauri-darwin-arm64" \
  node scripts/package.cjs --release
```

On Windows x64, put `ws-mcp-windows-amd64.exe` and its metadata under
`$PWD\ws-mcp-assets` and run the following from PowerShell.

```powershell
$output = Join-Path $env:TEMP ('werkschaft-release-' + [guid]::NewGuid())
New-Item -ItemType Directory $output | Out-Null
node agents-plugin-tool/desktop/scripts/assemble-daemon-input.cjs --binary "$PWD\ws-mcp-assets\ws-mcp-windows-amd64.exe" --out "$output\daemon" --platform win32 --package wsflow
Set-Location agents-plugin-tool/desktop
npm ci --ignore-scripts --no-audit --no-fund
$env:WERKSCHAFT_DAEMON_INPUT = "$output\daemon"
$env:WERKSCHAFT_RELEASE_OUTPUT = "$output\tauri-win32-x64"
node scripts/package.cjs --release
```

Put the two platform output directories under one `tauri-platforms` directory.
With the corresponding public key in `WERKSCHAFT_UPDATER_PUBLIC_KEY`, run
from the repository root.

```sh
version=$(jq -r '.version' tauri-platforms/tauri-darwin-arm64/platform.json)
node agents-plugin-tool/desktop/scripts/release.cjs --input tauri-platforms --output tauri-release --version "$version"
node agents-plugin-tool/desktop/scripts/release.cjs --verify --input tauri-release --version "$version"
```

For canary use the full `X.Y.Z-canary.N` version in both commands. Each feed
entry gives the signed asset's canonical URL, `.sig` contents, lowercase
SHA-256, and byte count. Verification checks those values and the Minisign
signature over the payload and its trusted version comment.

| Target in `werkschaft-tauri-update.json` | Asset with matching `.sig` |
| --- | --- |
| `darwin-aarch64` | `Werkschaft-darwin-arm64.app.tar.gz` |
| `windows-x86_64-nsis` | `Werkschaft-win32-x64-setup.exe` |
| `windows-x86_64-msi` | `Werkschaft-win32-x64.msi` |

Packaging extracts the Mac updater archive and verifies the plist,
`Contents/MacOS/Werkschaft`, and every embedded daemon file. The Windows app
executable is `werkschaft-desktop.exe`. WiX uses
the numeric core as `ProductVersion` for MSI. Its stock `MajorUpgrade` with
`AllowDowngrades="yes"` also replaces a different product at the same core.
The app inside the MSI, the updater signature, and the feed identify the full version.
[Tauri's WiX template](https://github.com/tauri-apps/tauri/blob/tauri-cli-v2.12.4/crates/tauri-bundler/src/bundle/windows/msi/main.wxs), [WiX 3 MajorUpgrade reference](https://docs.firegiant.com/wix3/xsd/wix/majorupgrade/)

## Publication gate

Configure the production updater key pair through the repository's Actions
settings:

| Kind | Name |
| --- | --- |
| Variable | `WERKSCHAFT_UPDATER_PUBLIC_KEY` |
| Secret | `TAURI_SIGNING_PRIVATE_KEY` |
| Optional secret | `TAURI_SIGNING_PRIVATE_KEY_PASSWORD` |

Stable `vX.Y.Z` tags publish native and Tauri assets together after the
combined payload is verified. Exact `vX.Y.Z-canary.N` tags add the signed
Tauri assets and feed to the native prerelease (`--latest=false`). Other tags
retain native-only behavior. Check the complete workflow and confirm
`releases/latest` resolves to a stable release. Follow the
[release procedure](../../ai-docs/ship/ws.md) for CI, approval, and tagging.

Updater signatures authenticate update payloads. Apple Developer ID signing
and notarization and Windows Authenticode signing remain deferred.
Gatekeeper or SmartScreen may prompt during native acceptance.

Existing Electron users must manually install Tauri from the release page.
Releases no longer include Electron migration ZIPs or desktop SHA256SUMS.
Tauri uses its own WebView profile and starts with default window settings.

## Native acceptance

Use isolated daemon caches and disposable Windows accounts or VM snapshots.
Download the combined stable and canary dry-run artifacts. Use a canary
with the next patch core for stable-to-canary opt-in and a lower-version
stable return. Prepare that core on a separate evaluation ref with
`agents-plugin-tool/scripts/bump-ws-version.sh <next-patch-core>`, and
dispatch its `tauri_dry_run` with `version=<next-patch-core>-canary.1`.
A same-core canary also exercises the MSI equal-core version replacement.

1. Install the stable NSIS and MSI payloads separately. Record the OS and
   Evergreen versions. Test one runtime-present VM and one supported VM
   without Evergreen. Confirm the embedded bootstrapper can install the
   runtime and that the user install opens on its first run.
2. Run the [evaluation feed](README.md#shell-updates). Confirm a scheduled
   stable check and the ordinary Settings check ignore canaries. Opt in,
   stage and restart into the canary. Confirm the running version and
   **Back to stable**. Pick stable, stage, restart, and confirm stable
   retains the same or newer running daemon. Check a draft saved through the
   restart, and record frame and origin refusal on the native transport.
3. Repeat with missing checksum, bad signature, changed bytes, and an
   interrupted transfer. Confirm refusal and retry against the valid feed.
   Stage an update, select the other channel, quit before downloading, and
   confirm reopen offers no superseded restart. Force an install or swap
   failure and verify retry or reinstall with the retained profile.
4. On disposable machines with old Electron installations, manually install
   the Tauri app or installer from the release page. Confirm first launch,
   connection, and default Tauri Settings in a fresh WebView profile. Record
   Gatekeeper or SmartScreen prompts and reinstall recovery on the authorized
   native test hosts.
