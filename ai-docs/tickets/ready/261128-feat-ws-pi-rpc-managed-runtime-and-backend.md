---
title: 관리형 Pi 런타임 다운로더 및 pi-rpc wsagent 백엔드
related:
  261115-feat-ws-permission-profile: constrains permission-mode truthfulness (do not fabricate native modes when a harness has none)
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 41d482b4bc812638
sage-review-completeness-reviewed: 41d482b4bc812638
---

# Managed Pi Runtime Downloader and pi-rpc wsagent Backend

## Background

`ws` previously retired its old in-process/bundled Pi harness (`BackendPi = "pi"`) along with `pi.*` config keys and `ws-pi-bridge`, retaining only archival history and compatibility stubs. Users still want first-class third-party model/provider access through upstream Pi (`earendil-works/pi`, `@mariozechner/pi-coding-agent`) without maintaining a global Node/npm toolchain or resurrecting the retired `"pi"` backend surface.

This is **Ticket 1 of 2** for the third-party Pi RPC bridge:

- **Ticket 1 (this ticket):** Managed Pi standalone runtime downloader (`v1.5.2`), embedded `.mjs` MCP/provider-response bridge, and the `pi-rpc` `wsagent` backend with registration, `wsconfig` / multi-slot `PI_CODING_AGENT_DIR` wiring, and `fs_denied` / `fs_outside` credential protection.
- **Ticket 2 (follow-up, out of scope here):** `/api/account/pi-rpc/*` HTTP routes, ephemeral PTY `pi auth login` / `logout`, `Settings -> Account` UI, New Session picker/badges, and 11-locale i18n.

Splitting runtime + backend into Ticket 1 keeps the diff tightly scoped (soft target: under ~2,500 lines across two sequentially dependent phases; do not cut test coverage if tests push slightly over — report any overrun cleanly): Phase 1 builds the standalone runtime manager and embedded bridge; Phase 2 builds the `pi-rpc` `wsagent` backend and platform wiring on top of Phase 1.

## Decisions

### Confirmed Owner Policy Decisions

- **(1840)=b Managed standalone runtime distribution:** Download a pinned upstream `earendil-works/pi` standalone release (`v1.5.2`) on demand with SHA-256 verification and launch it with a thin embedded `.mjs` bridge extension (`-ne -e builtin:mcp -e <cached-ws-bridge.mjs>`).
  - *Rejected:* Bundling npm/Node dependencies; falling back to a user-managed global `pi` binary on `PATH`.
- **(1841)=a Single credential ownership & unfiltered providers:** Pi's own `auth.json` under `PI_CODING_AGENT_DIR` (default `~/.pi/agent`) is the sole credential owner; credentials are never mirrored into `wscred` provider stores. Authentication is CLI-driven (`pi auth login` / `logout`, exposed in Ticket 2), and **all** providers exposed by the `pi` CLI are allowed unfiltered (zero provider allowlist/blocklist).
  - *Rejected:* Mirroring Pi credentials into `wscred` stores; filtering or blocking providers.
- **(1842)=a T0 transparent new backend ID (`pi-rpc`):** Register a brand-new backend ID `BackendPiRPC = "pi-rpc"` and leave retired `BackendPi = "pi"`, `errPiRetired`, `retired_pi_tiers.go`, `pi_history_archive.go`, `retired_pi_purges`, and `"ws-pi-bridge"` completely untouched.
  - *Rejected:* Reusing `"pi"` (would collide with retirement guards, archival history, and `config_manifest.go` retired-key filtering).

### Auto-Confirmed Technical Decisions

- **Identifiers & adapter-owned placement:** Define `BackendPiRPC = "pi-rpc"`, `LeadProfilePiRPC = "lead-pi-rpc"`, `AgentWorkerProfile(BackendPiRPC)` (`"agent-worker-pi-rpc"`), and `BridgeClientName = "ws-pi-rpc-bridge"`. Place constants, `//go:embed instructions/agent-worker-pi-rpc.md`, and `init()` registration into `builtinInstructionTexts` inside adapter-owned `agents-plugin-tool/internal/wsagent/pirpc.go` (matching `werk.go:19-52`). Because `harness_name_scan_test.go:259` `adapterOwned` already matches `^internal/wsagent/(claude|codex|agy|pi|werk)[a-z0-9_]*\.go$`, all `pirpc*.go` files are adapter-owned; update `harnessExact`/`harnessAlt`/`backendIdent` in `harness_name_scan_test.go:269-271` to cover `pi-rpc` / `BackendPiRPC`, and add `classExternal` allowlist rows for `fs_outside.go` and `fs_denied.go`. Never introduce `pi.*` config keys (`internal/mcp/config_manifest.go:196` ignores retired `pi.*` prefixes).
  - *Rejected:* Declaring `BackendPiRPC` in shared `profile.go`/`builtin.go` (would add unnecessary `(1881)` allowlist churn); using `pi.*` config keys.
- **Install-implies-enabled & explicit install trigger (zero manifest churn):** Do not add a `dashboard.pi_rpc_enabled` config key or modify `config-manifest.json`. Installing the managed Pi runtime makes `pi-rpc` usable; uninstalling hides availability cleanly through `ChoiceLister.Executable()` and `supportBackendAvailability` (`internal/wsagent/support.go:381-409`). Launching `pi-rpc` while the runtime is absent **never** auto-downloads: it fails cleanly via `choiceExecutableError` / `Start` returning a clear "managed Pi runtime is not installed" error; `Manager.Install` / `EnsureInstalled` is invoked only by explicit user install triggers (Ticket 2) or tests.
  - *Rejected:* Separate enablement flag in `config-manifest.json` / `dashboard-config-manifest.json`; silent auto-download inside `Start`.
- **Dedicated runtime package (`internal/wspiruntime`) & shared runner seam:** Place the standalone runtime downloader, SHA-256 verifier, install lock, in-memory readiness cache, and embedded `pirpc_bridge.mjs` extractor in `agents-plugin-tool/internal/wspiruntime/`. Wire a shared `*wspiruntime.Manager` into `PiRPCBackend` via a runner binder interface (`piRuntimeBinder` / `WithPiRuntime` matching `slotHomeBinder` at `runner_startup.go:46-71` and `WithProviderCredentials` at `werk.go:123`) so the daemon, `PiRPCBackend`, and Ticket 2's HTTP routes share one readiness cache and single-flight lock; when unbound (`Runtime == nil`), fall back safely to constructing a manager on demand rather than freezing `WS_CACHE_HOME` inside `builtinRegistrations`'s `sync.OnceValue`.
  - *Rejected:* Putting downloader/archive extraction directly in `internal/wsagent`; instantiating a fresh `Manager` per method call in production or freezing `WS_CACHE_HOME` in a package singleton.
- **Pinned upstream release (`v1.5.2`, live-verified 2026-11-28):**
  - Base URL: `https://github.com/earendil-works/pi/releases/download/v1.5.2/`
  - Verbatim `SHA256SUMS` table (defense-in-depth: verify **both** the fetched `SHA256SUMS` entry AND the hardcoded pinned digest table, and hash the downloaded archive bytes against them):
    - `pi-darwin-arm64.tar.gz`: `56b5e194d485ced555667b8ba3276157a8272e9ecde9e001e428af80985bdd9b`
    - `pi-darwin-x64.tar.gz`: `f997a088613514ac97d913aeea7642094d84fe6d46854ff7e64e2c12e536dc50`
    - `pi-linux-x64.tar.gz`: `811c6f2c9e7aaee252b5404252e556fb713aa209c1f7f924226aa77d4ef70cb5`
    - `pi-linux-arm64.tar.gz`: `42805cd5615952fcbbfc9c56b04f8feb84395134952e169a6cdc8d7676975666`
    - `pi-windows-x64.zip`: `e6adb8cb8c84eca14391ad24b363de9d2be98a89ca3ba3fdee1fa4de32b7f2e8`
    - `pi-windows-arm64.zip`: `e83404bc5a6003ac950683edd9b6cf499dac91ec890d8439a8c32528de534a77`
  - Archive layout (live-verified on `pi-darwin-arm64.tar.gz`): single top-level `pi/` directory containing Bun-compiled executable (`pi` / `pi.exe`) plus required sibling assets (`package.json`, `photon_rs_bg.wasm`, `theme/`, `export-html/`, `assets/`, `native/`). Strip the single `pi/` prefix cleanly into `<wsstate.CacheRoot>/runtimes/pi/v1.5.2-<goos>-<goarch>/` so the executable sits at `<installDir>/pi` (or `pi.exe`) with all sibling assets preserved alongside it.
- **Verified Pi `v1.5.2` CLI flags, isolation & `ProcessExternal` ExecCutover boundary:**
  - Mandatory bridge flags: `--mode rpc -ne -e builtin:mcp -e <cached-ws-bridge.mjs>` (live smoke-tested: `-ne` disables `builtin:mcp`, which `pi.registerMcpServer` requires, so `-e builtin:mcp` must precede `-e <cached-ws-bridge.mjs>`).
  - Isolation flags: `-ns -np --no-themes -na -nc`. **Never** pass `-nt` (`--no-tools` strips MCP tools too).
  - **`ProcessExternal` `LifecyclePolicy`, truthful `PermissionPolicy` & `ExecCutover` (code-verified against `server_fs_write.go:20-41`, `permission_gate.go:70-79`, `permission.go:289-296`, `runner_events.go:1396`, and `261115-feat-ws-permission-profile`):**
    - `PiRPCBackend` runs as `ProcessExternal` under `wsholder`, so it MUST set `LifecyclePolicy{ComposedLeadInstructions: true, LeadHTTPMCP: true, ExecReadTools: true, RestartLeadOnPermissionChange: true}` (matching `CodexBackend`, `codex.go:50-54`, plus the permission-restart flag below) and MUST **NOT** set `FileWriteTools` or `ReadOnlyNoExec`. `internal/mcp/server_fs_write.go:20-41` requires a durable `s.fileWrites` session and `mcpframe.ToolCallIDMeta` (only `ProcessDurable` `bindDurableMCP` supplies these), and `internal/mcp/permission_gate.go:70-79` `ReadOnlyNoExec` blocks even read-only allowlist `exec.*` calls on non-durable sessions.
    - **Truthful permission policy (no fabricated native modes):** Pi `v1.5.2` has no native permission mode or `set_permission_mode` RPC — it only switches launch-time CLI flags (`-nbt` vs `--exclude-tools bash`). Per `261115-feat-ws-permission-profile` (Decisions 1, 5, 6, 9: report enforcement truthfully and never fabricate a native mode when the harness has none), `PiRPCBackend` sets `PermissionLevels: LeadPermissionLevels()`, `PermissionModes: nil`, and `Permission: PermissionPolicy{LegacyDefault: PermissionWorkdirWrite}` (`NativeModes: nil`, `LegacyModes: nil`, `LiveModeProjection: false`). Add runtime-only `LifecyclePolicy.RestartLeadOnPermissionChange bool` (checked alongside `durableProcess` at `runner_lead_start.go:260`, `permission.go:290`, and `runner_events.go:1396`, zero harness-name branching): default leads cleanly derive `PermissionWorkdirWrite` (never `""`, avoiding HTTP 400 "unresolved"), `PermissionPolicy.quality` truthfully reports `"advisory"`, `reconcileLeadPermission` (`permission.go:396`) stays silent on holder reattach, and live lead permission changes (`setPermissionLevel`) restart the process at the idle/turn boundary to re-launch with updated `-nbt` / `--exclude-tools bash` flags.
    - Set `PlaybookCommands: true` (matching `AgyBackend` and `WerkBackend`) because `-ne -ns -np` runs Pi without ws plugin skills, so hosted leads surface playbooks via dashboard commands and ws MCP server terminology.
    - Set `ExecCutover: &ExecCutoverSupport{}` (`Since: ""`, `NoNativeTools: false`) and emit `Event.Harness = &HarnessReport{Version: wspiruntime.PinnedVersion}` on process start. Because the runtime is strictly ws-managed and SHA-256 pinned to `v1.5.2` (zero unverified global `pi` fallback), empty `Since` prevents `exec_cutover.go:101` (`seen.Version == ""`) from leaving the very first launch after install outside exec cutover, while `NoNativeTools: false` preserves the `exec.native_tool_removal.pi-rpc` rollout switch.
    - Tool removal flags when `spec.ExecCutover` is true: pass `--exclude-tools bash` for leads and `PermissionWorkdirWrite` / `PermissionFull` workers (keeping Pi's native `read`/`edit`/`write` while routing shell execution through ws `exec.*` and `fs.read`/`fs.grep`/`fs.list`); pass `-nbt` (`--no-builtin-tools`) for `PermissionReadOnly` leads and workers so Pi's native `read`/`edit`/`write`/`bash` are all removed and reads route exclusively through ws `fs.read`/`fs.grep`/`fs.list` (protected by `wsdashboard/fs_denied.go`) and allowlisted read-only `exec.*`.
    - *Rejected:* Setting `FileWriteTools: true` / `ReadOnlyNoExec: true` on `ProcessExternal` (breaks ws file writes and read-only `exec.*` allowlist); passing `--exclude-tools bash,edit,write` without durable `fs.edit`/`fs.write` (leaves `PermissionWorkdirWrite` sessions with no edit/write tool); setting `Since: "1.5.2"` (leaves the first post-install session outside cutover); copying `codexPermissionPolicy()` or fabricating synthetic `PermissionModes` (breaks `setPermissionLevel` with HTTP 400 or falsely reports `"partial"` enforcement when `ExecCutover` is off, violating `261115-feat-ws-permission-profile`).
- **Canonical HTTP MCP env names (`WS_MCP_SESSION_URL` / `WS_MCP_SESSION_TOKEN`):** Pass `EnvMCPSessionURL` (`WS_MCP_SESSION_URL`, `internal/wsagent/mcp_session.go:42`) and `EnvMCPSessionToken` (`WS_MCP_SESSION_TOKEN`, `mcp_session.go:44`) in child env alongside `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0`, and `PI_CODING_AGENT_DIR=<resolvedSlotDir>`. Both canonical env vars are already scrubbed from child tool subprocesses by `internal/mcp/process_env.go:510`. Scrub parent harness env prefixes (`wsharnessenv.Drop`), never pass tokens in argv or on disk, and never set `PI_OFFLINE=1` on live turns.
  - *Rejected:* Introducing a new `WS_MCP_HTTP_URL` env name (not in `process_env.go:510` scrub list and duplicates `EnvMCPSessionURL`).
- **Multi-slot `PI_CODING_AGENT_DIR` wiring & `planUsageStore` isolation:** Add `"pi-rpc"` to `internal/wscred/account_slots.go` (`SupportedSlotHarness`, `harnessHomeSpec` -> `("PI_CODING_AGENT_DIR", ".pi/agent")`, `HarnessForConfigEnv`, and `ActiveSlots()`). Keep `SlotLoginHarnesses()` (`"codex"`, `"claude"`) unchanged because Pi authentication is per-provider via `pi auth login`. Implement `slotHomeBinder` on `PiRPCBackend` (`runner_startup.go:49-70`) so `PI_CODING_AGENT_DIR` resolves through the D10 active-slot chain. Guard `planUsageStore.classifyActiveSlotsLocked` (`internal/wsdashboard/plan_usage.go:191-198`) so `"pi-rpc"` in `ActiveSlots()` can never fall through into `gptHarness` (skip any harness registered in `s.harnesses` without `SeparateFiveHourHistory`, and restrict the empty-traits fallback loop to `wscred.SlotLoginHarnesses()` — zero new harness-name literals in `plan_usage.go`).
- **Explicit-only tier configuration & backend-preserving `wsconfig` writes (matching `werk`):** Because `pi-rpc` supports arbitrary user-authenticated providers with no hardcoded default model, treat `"pi-rpc"` in `internal/wsconfig/snapshot.go` (`InspectTier`) as explicit-only (no fallback to `default`/`codex` models) and implement `workerSetup.workerTierGap` on `PiRPCBackend` (matching `werk.go:325`). In `internal/wsconfig/config.go`, keep `"pi-rpc"` bucket writes (`setAgentsTierForHarness`, `config.go:381-452`) bound to `"pi-rpc"` without calling `InferBackend(model)` (which would misclassify `openrouter/anthropic/claude-3.7-sonnet` as `"claude"` or `openai/gpt-*` as `"codex"`) and without seeding `effective.Agents.Tiers[tier]` Codex defaults on effort-only edits; likewise default omitted `mapping.Backend` on a `"pi-rpc"` bucket hit in `ResolveAgentForHarnessConfig` (`config.go:488-504`) and `ResolveAgentTierForHarness` (`config.go:532-534`) to `"pi-rpc"`. Model values use `<provider>/<modelId>` split on the first `/` via `strings.Cut` (`wsconfig.LaunchModelPattern` at `internal/wsconfig/tier_routing.go:52` already permits `/` and `:`, including nested `openrouter/anthropic/claude-3.7-sonnet`).
- **Compatibility Tier — T0 Transparent:** Per `ai-docs/manuals/release-compatibility.md`, every touched cross-version surface is strictly T0:
  1. `account-slots.json` (`internal/wscred/account_slots.go:322-377`): `decodeAccountSlotsDocument` (`account_slots.go:322-361`) preserves unknown harness keys in `doc.active` and unknown `AccountSlot` rows in `doc.slots` across read/write cycles; `accountSlotsDocument.encode` (`account_slots.go:364-377`) re-emits them verbatim; `syncDenyDirsLocked` (`account_slots.go:491-502`) protects every slot's `ConfigDir` regardless of harness name. Old binaries keep functioning and do not drop `"pi-rpc"` slots.
  2. `wsagent` records, profiles & registrations (`internal/wsagent/registration.go:204`, `internal/wsagent/record.go`): `"pi-rpc"` is a new backend identifier; old binaries ignore unregistered backends cleanly without mutating records, and all retired `"pi"` surfaces remain untouched.
  3. `wsconfig` (`internal/wsconfig/config.go:763-789`, `scope.go:294-327`, `snapshot.go:41-61`): additive `"pi-rpc"` harness selector and bucket default-scope entries; old binaries ignore unknown `agents.model_aliases[tier]["pi-rpc"]` keys and unknown bucket overrides on read.
  4. `harness-versions.jsonl` (`internal/wsagent/harness_versions.go:64-70`): append-only JSONL keyed by `backend`; old binaries parse and ignore `"pi-rpc"` entries.
  5. Runtime cache (`<wsstate.CacheRoot>/runtimes/pi/`): new cache subdirectory invisible to old binaries.
  6. MCP wire & `/mcp` hello (`internal/mcp/server_harness.go:155-174`): zero tool schema or hello changes; `ConnectionPolicy.BridgeClientName = "ws-pi-rpc-bridge"` and `HarnessTraits.ReplayClientName = "ws-pi-rpc-bridge"` plug into existing registration-driven detection.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- **File-touch budget (`performance.md` Rule 6):** Keep runtime readiness and bridge-path state cached in memory after first resolution (zero `stat()` polling on interactive paths), use single-flight locking, atomic staging + Windows AV retry on directory replacement, and `wsstate.AtomicReplaceFile` for `.ws-ready` and bridge files.
- **Harness-name isolation (`(1881)`):** `go test ./internal/wsagent -run 'TestCommonCodeHasNoHarnessNameBranch|TestHarnessNameScan'` (`harness_name_scan_test.go:555`) must pass with zero un-allowlisted harness names in common code.
- **No `pi.*` config keys:** Never register or read `pi.*` keys; `internal/mcp/config_manifest.go:196` ignores retired `pi.*` prefixes.
- **Retired `"pi"` untouched:** Do not modify `BackendPi`, `errPiRetired`, `retired_pi_tiers.go`, `pi_history_archive.go`, `retired_pi_purges`, or `"ws-pi-bridge"` compatibility paths.
- **Scope boundary (Ticket 1 only):** Do not add HTTP routes (`/api/account/pi-rpc/*`), PTY login/logout helpers, dashboard UI/badges, or i18n keys in this ticket (all belong to Ticket 2). Aim for a soft diff budget under ~2,500 lines; keep production code surgical and report any test-driven overrun rather than dropping coverage.

## Prior Art

- `agents-plugin-tool/internal/wsagent/codex.go` — `ProcessExternal` backend over `startChild`, `MCPSession`, `ExecCutover`, `ChoiceLister`, `slotHomeBinder`, `LifecyclePolicy{ComposedLeadInstructions: true, LeadHTTPMCP: true, ExecReadTools: true}`.
- `agents-plugin-tool/internal/wsagent/werk.go` — adapter-owned constants, `//go:embed instructions/agent-worker-werk.md`, `init()` registration into `builtinInstructionTexts`, `PlaybookCommands: true`, `ToolNameSchemeSanitize`, `workerSetup.workerTierGap`.
- `agents-plugin-tool/internal/wsagent/stream.go` — `startChild`, `streamProcess`, holder reattach, 64 MiB `maxLineBytes` (`stream.go:410`).
- `agents-plugin-tool/internal/wsstate/paths.go` (`CacheRoot`) and `agents-plugin-tool/internal/wsstate/replace_file_{unix,windows}.go` (`AtomicReplaceFile` with Windows transient-error retry).
- `agents-plugin-tool/internal/wscred/account_slots.go` — D10 slot-aware home resolution and unknown-harness preservation.
- `agents-plugin-tool/internal/wsdashboard/fs_outside.go` & `fs_denied.go` — provider credential deny paths.

## Prior Decisions

- 29fa10e6 (2026-11-24, commit): "Pi data and holder descriptors predate this change. The runner preserves their history and guards against stopping existing Pi processes." — bearing: constrains
- 5ec600a2 (2026-11-24, commit): "Pi remains retired for launch and rejoin. Archived history, bridge recognition, and startup protection for legacy holders remain readable or respected at the corresponding boundary." — bearing: constrains
- 8ba912d6 (2026-11-24, commit): "Decision 2 keeps the ws-pi-bridge client identity. Matching it does not expose runtime execution or add Pi to the backend registry." — bearing: constrains
- d0aef569 (2026-11-23, commit): "Work that adds a harness difference after this must route it through BackendRegistration policy data or Backend interfaces, or extend harnessNameAllowlist in its own diff under the (1881) rule." — bearing: supports
- e56aef79 (2026-11-23, commit): "T0 additions only: registration, config bucket, catalog profile, agent.tiers Unavailable line for werk, hidden subcommand; ... Tier models start empty; do not add a default (Decision 8)." — bearing: supports
- 261127-feat-ws-harness-account-switcher-and-slot-continuity (2026-11-28, commit 97e7fedb): "Kept common wsagent/wsdashboard code free of literal harness-name branching (verified by TestCommonCodeHasNoHarnessNameBranch) via AccountSlotRegistry.ActiveSlots() and HarnessTraitSet." — bearing: supports
- 8c96c4a6 (2026-11-24, commit): "Updated wsconfig.InspectTier (UI inspection only, not config.resolve_agent) ... so unconfigured werk tiers honestly render as unconfigured instead of inheriting codex from default." — bearing: supports
- 261118-refactor-ws-harness-name-branches (2026-11-21, commit 2c2ee347): "Keep the in-memory descriptor floor behind the adapter method, preserve stored Profile and Record shapes, and preserve existing config precedence and selector order." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | new internal/wspiruntime package; internal/wsagent pirpc*.go, registration.go, backend_policy.go, runner_startup.go, runner_lead_start.go, permission.go, runner_events.go; internal/wsconfig config.go scope.go snapshot.go; internal/wscred/account_slots.go; internal/wsdashboard fs_outside.go fs_denied.go plan_usage.go |
| scope.surface | cross-module | wsagent, wsconfig, wscred, wsdashboard plus a new wspiruntime package consumed by wsagent |
| scope.new_public_symbol | yes | BackendPiRPC, LeadProfilePiRPC, PiRPCBackend, wspiruntime.Manager, NewManager, PinnedVersion, BridgePath |
| scope.new_type_contract | yes | PiRPCBackend registration and lifecycle policy, wspiruntime.AssetSpec, RuntimeStatus and Options |
| scope.test_surface | new-files | wspiruntime tests and internal/wsagent/pirpc_test.go are new; existing registration_test.go, backend_policy_test.go, wsdashboard/fs_denied_test.go#L91 TestFSLocalDeniedProviderCredentials and harness_name_scan_test.go are extended |
| complexity.reuse_points | confirmed | startChild at wsagent/stream.go#L129, slotHomeBinder at wsagent/backend_policy.go#L253, werk.go registration and embed pattern, wsstate.AtomicReplaceFile and CacheRoot all read in tree |
| complexity.side_effect_risk | high | downloads and executes an external binary, writes the user cache root, edits shared slot, config and credential-deny tables |
| risk.correctness | high | Pi v1.5.2 CLI flags, RPC command set, archive layout and digests are external and not checkable from the tree; zip and tar extraction safety is new code |
| risk.fit | moderate | adapter-owned placement follows werk.go and codex.go precedent, but the harness-name scan allowlist and the exec cutover contract need coordinated edits |
| risk.test | high | live Pi behavior cannot run offline so fake-binary tests carry the proof; Windows atomic-move retry paths are hard to exercise |
| risk.security_or_contract | high | SHA-256 pinned download and archive path traversal defense, credential deny paths for auth.json, token passing via env only, T0 compatibility claims over persisted account-slots and records |

## Phases

### Phase 1: Managed Pi Standalone Runtime Downloader & Embedded `.mjs` Bridge

Create `agents-plugin-tool/internal/wspiruntime/` to manage the pinned standalone Pi `v1.5.2` runtime and embedded `.mjs` bridge extension.

#### Deliverables

1. **Pinned Release Manifest (`manifest.go`):**
   - Export `PinnedVersion = "1.5.2"`, `ReleaseTag = "v1.5.2"`, `DefaultBaseURL = "https://github.com/earendil-works/pi/releases/download/v1.5.2/"`, and the verbatim 6-platform asset/SHA-256 table (`darwin/arm64`, `darwin/x64`, `linux/x64`, `linux/arm64`, `windows/x64`, `windows/arm64`).
   - Resolve `(goos, goarch)` -> `AssetSpec{Filename, SHA256, ArchiveKind (tar.gz | zip), BinaryName ("pi" | "pi.exe")}`, returning an explicit unsupported-platform error for other targets.

2. **Runtime Manager (`manager.go`, `archive.go`):**
   - `Manager` constructed via `NewManager(Options)` supporting injectable `CacheRoot`, `BaseURL`, `HTTPClient`, `GOOS`, `GOARCH` (defaults to `wsstate.CacheRoot(wsstate.Options{})`, `DefaultBaseURL`, bounded `http.Client`, `runtime.GOOS`, `runtime.GOARCH`).
   - Deterministic install layout:
     - Runtime root: `<CacheRoot>/runtimes/pi/`
     - Versioned install dir: `<CacheRoot>/runtimes/pi/v1.5.2-<goos>-<goarch>/`
     - Deterministic binary path (`BinaryPath() string`): `<installDir>/pi` (or `pi.exe`), returned deterministically even before install so `ChoiceLister.Executable()` integrates with `support.go:choiceExecutableError` without reporting an uninstalled runtime as available.
     - Readiness marker: `<installDir>/.ws-ready` (JSON with `version`, `goos`, `goarch`, `sha256`, written via `wsstate.AtomicReplaceFile`).
   - **In-memory readiness cache (`performance.md` Rule 6):**
     - `Status() RuntimeStatus` (`Installed`, `Version`, `BinaryPath`, `InstallDir`, `Installing`, `Error`) checks disk once on first call, caches the result in memory, and refreshes only on `Install`, `Uninstall`, or explicit `Invalidate()`. Zero `stat()` polling on interactive paths.
   - **Defense-in-depth download & atomic install (`Install(ctx, onProgress)` / `EnsureInstalled(ctx, onProgress)`):**
     - Invoked only by explicit install triggers (Ticket 2) or tests; launching `pi-rpc` while uninstalled never triggers an implicit download.
     - In-process single-flight coordination (mutex + broadcast/shared in-flight call) plus cross-process lockfile under `<CacheRoot>/runtimes/pi/.install.lock` so concurrent installs never corrupt the target directory.
     - Fetch `SHA256SUMS` from `<BaseURL>SHA256SUMS`, parse the asset's digest, and verify it equals the hardcoded pinned digest before accepting the archive; stream-download the archive while computing SHA-256 (reporting bounded progress via optional callback) and fail loudly on any mismatch or non-200 response.
     - Extract into a sibling temp staging directory (`v1.5.2-<goos>-<goarch>.tmp-<rand>`), stripping the single top-level `pi/` archive directory so the Bun executable and all required sibling assets (`package.json`, `photon_rs_bg.wasm`, `theme/`, `export-html/`, `assets/`, `native/`) land directly inside the staging directory.
     - Reject any archive entry containing `..`, absolute paths, drive prefixes, symlink/hardlink targets escaping the staging root, or unsupported special device nodes (zip-slip & tar-traversal defense). Preserve executable permissions (`0o755` on the `pi` binary on Unix).
     - **Torn-readiness-free directory swap:** Write `.ws-ready` inside the staging directory via `wsstate.AtomicReplaceFile` **before** swapping staging into `<installDir>` so `<installDir>/pi` never appears without `.ws-ready`. Because neither POSIX `os.Rename` nor Windows `MoveFileExW` can atomically overwrite an existing non-empty directory, reinstall/repair over an existing `<installDir>` moves the prior directory aside to a sibling backup (`v1.5.2-<goos>-<goarch>.old-<rand>`) under the install lock with Windows AV transient-retry backoff (matching `wsstate/replace_file_windows.go` patterns), renames staging into `<installDir>` with retry (attempting rollback of `.old-<rand>` on failure), removes `.old-<rand>` best-effort on success, prunes stale `.tmp-*`/`.old-*` leftovers, and updates the in-memory cache. Clean up staging dir on any error.
   - **Uninstall (`Uninstall(ctx)`):**
     - Under the install lock, remove `<installDir>` (and stale `.tmp-*`/`.old-*`/bridge artifacts as appropriate) and invalidate the in-memory cache cleanly.

3. **Embedded `.mjs` Bridge (`pirpc_bridge.mjs`, `bridge.go`):**
   - `//go:embed pirpc_bridge.mjs` with `BridgePath() (string, error)` extracting once to content-hashed `<CacheRoot>/runtimes/pi/bridge-<sha256[:16]>.mjs` via `wsstate.AtomicReplaceFile` and memoizing the path in memory.
   - `pirpc_bridge.mjs` behavior (thin, zero external npm dependencies):
     - Reads `process.env.WS_MCP_SESSION_URL` and `process.env.WS_MCP_SESSION_TOKEN`; when both are non-empty, registers the daemon's ws MCP server via `pi.registerMcpServer` (`name: "ws"`, HTTP transport to `WS_MCP_SESSION_URL`, `Authorization: Bearer <token>`, `exposure: "direct"`, `clientInfo: { name: "ws-pi-rpc-bridge" }` or matching Pi 1.5.2 `builtin:mcp` option shape).
     - Registers `pi.on("after_provider_response", ...)` to detect HTTP `429` / `529` responses (plus `retry-after` / rate-limit headers when present) and emit a fire-and-forget `ctx.ui.notify(JSON.stringify({ ws_pi_event: "provider_response", status, retry_after, provider, model }), "warning")` without blocking or mutating provider responses.

4. **Phase 1 Verification:**
   - Offline `httptest.Server` unit tests covering: `.tar.gz` and `.zip` installs preserving executable + sibling files (`package.json`, `photon_rs_bg.wasm`, `theme/`, `native/`), `.ws-ready` present atomically with the swapped directory, reinstall/repair over an existing populated `<installDir>` (including rollback when swap fails and cleanup of `.old-*`), SHA-256 mismatch rejection (tampered `SHA256SUMS` and tampered archive bytes), zip-slip and tar path-traversal / symlink-escape rejection, concurrent single-flight install, in-memory readiness cache (verifying repeated `Status()` calls do not re-stat disk), `Uninstall()` + cache reset, and content-hashed `BridgePath()` extraction idempotency.

### Phase 2: `pi-rpc` `wsagent` Backend, Registration, Config/Slot Wiring & Credential Protection

Build the `pi-rpc` `wsagent` backend on top of Phase 1's `internal/wspiruntime`, register it in the builtin floor, and wire `wsconfig`, multi-slot `PI_CODING_AGENT_DIR`, and `fs_denied` / `fs_outside` protection.

#### Deliverables

1. **Adapter-Owned `pi-rpc` Backend (`internal/wsagent/pirpc.go`, `pirpc_process.go`, `pirpc_mapper.go`, `instructions/agent-worker-pi-rpc.md`):**
   - `pirpc.go`:
     - Constants `BackendPiRPC = "pi-rpc"`, `LeadProfilePiRPC = "lead-pi-rpc"`, `pirpcBridgeClientName = "ws-pi-rpc-bridge"`, `//go:embed instructions/agent-worker-pi-rpc.md`, and `init()` storing into `builtinInstructionTexts[AgentWorkerProfile(BackendPiRPC)]`.
     - `PiRPCBackend` struct with optional `Runtime *wspiruntime.Manager`, `Command string` (test override), and `ResolveHome func() string` (wired by `slotHomeBinder` via `withSlotHomeResolver`). Implement `piRuntimeBinder` (`withPiRuntime(*wspiruntime.Manager) Backend`) and export `WithPiRuntime(registrations, mgr)` (matching `WithProviderCredentials` at `werk.go:123`) so `NewRunner` / daemon startup shares one `*wspiruntime.Manager` across backend and Ticket 2 HTTP routes; when `b.Runtime == nil`, construct a manager on demand without caching it in `builtinRegistrations`'s `sync.OnceValue`.
     - `registration() (string, BackendRegistration)`:
       - `ProcessKind: ProcessExternal`, `LeadProfile: LeadProfilePiRPC`, `PermissionLevels: LeadPermissionLevels()`, `PermissionModes: nil`, `Permission: PermissionPolicy{LegacyDefault: PermissionWorkdirWrite}` (truthful enforcement, zero fabricated native modes), `Catalog: PiRPCCatalog()`.
       - `WorkerCapable: true`, `WorkerProfile: AgentWorkerProfile(BackendPiRPC)`, `WorkerHTTPMCP: true`, `WorkerAutoApprove: true`.
       - `ManualHarness: BackendPiRPC`, `PlaybookCommands: true`, `ToolNameScheme: ToolNameSchemeSanitize`.
       - `Lifecycle: LifecyclePolicy{ComposedLeadInstructions: true, LeadHTTPMCP: true, ExecReadTools: true, RestartLeadOnPermissionChange: true}` (no `FileWriteTools`, no `ReadOnlyNoExec`).
       - `ExecCutover: &ExecCutoverSupport{}` (`Since: ""`, `NoNativeTools: false`).
       - `Connection: ConnectionPolicy{BridgeClientName: pirpcBridgeClientName}`.
       - `Dashboard: wsdashboard.HarnessTraits{ReplayClientName: pirpcBridgeClientName}`.
       - `Profiles`: `agentWorkerBuiltinProfile(BackendPiRPC, nil)` and `leadBuiltinProfile(LeadProfilePiRPC, BackendPiRPC, "")`.
     - `ChoiceLister`: `Executable()` returns `b.Command` if set else `runtime.BinaryPath()`; `Choices(ctx)` returns cached model/thinking-level choices from the last opened session per resolved `PI_CODING_AGENT_DIR` (matching `codexChoices` pattern) with on-demand RPC probe fallback (`get_available_models` + `get_available_thinking_levels`) when installed and cache is cold.
     - `workerSetup`: `workerTierGap(tiers)` reports when no `pi-rpc` tier model is configured; `workerCredentialGap()` and `workerProviderGap()` return `""` (unfiltered CLI-owned auth; runtime install absence is already reported via `choiceExecutableError`).
   - `pirpc_process.go` & `pirpc_mapper.go`:
     - Subprocess launch via `startChild` (`internal/wsagent/stream.go:129`, 64 MiB `maxLineBytes`) under `wsholder`, with holder reattach support (`spec.Holder != nil && spec.Holder.id != ""`). If the managed runtime binary is missing (and `b.Command == ""`), `Start` fails immediately with a clear "managed Pi runtime is not installed" error without auto-downloading.
     - CLI arguments:
       - Live-verified base flags: `--mode rpc -ne -e builtin:mcp -e <bridgePath> -ns -np --no-themes -na -nc`.
       - Tool cutover: when `spec.ExecCutover` is true, pass `-nbt` (`--no-builtin-tools`) if `spec.PermissionLevel == PermissionReadOnly`, else `--exclude-tools bash`. Never pass `-nt`.
       - Instructions/system prompt & session persistence: compose `spec.Instructions` + `spec.SystemPrompt` and wire non-ephemeral session/resume flags plus `--no-session` for ephemeral sessions (verify exact flag spelling and RPC payload field names once against the pinned `v1.5.2` binary via `pi --help` / smoke check before pinning the fake-binary test fixtures; if the implementation environment cannot execute the pinned binary offline, explicitly flag those unverified flag/payload assumptions in the implementation result for first-connect verification; never leak tokens into argv or disk).
     - Child environment:
       - Scrub parent harness vars via `wsharnessenv.Drop(spec.Env)` + drop `CLAUDE`, `CODEX_`, `PI_`, `ORCA_`, `WS_PI_` (preserving `PI_CODING_AGENT_DIR` only when not overridden by slot binding).
       - Set `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0`, `PI_CODING_AGENT_DIR=<resolvedSlotDir>`.
       - When `spec.MCPSession != nil`, set `EnvMCPSessionURL` (`WS_MCP_SESSION_URL`) and `EnvMCPSessionToken` (`WS_MCP_SESSION_TOKEN`). Never set `PI_OFFLINE=1` on live turns; never place tokens in argv or disk.
     - Strict LF-only (`0x0A`) JSONL framing on stdin/stdout.
     - Concrete `wsagent` process-control mapping (`internal/wsagent/backend.go:253-317`):
       - `Process`: `Send` -> `prompt` (or `follow_up` when queued), `Interrupt` -> `abort`, `SetModel` -> `set_model`, `SetEffort` -> `set_thinking_level`, `Close`, `Events`.
       - `processControls`: `steer` (`steerer`: `Steer` -> `steer`), `reports` (`reportSteerer`: `ClearSteered` -> `clear_queue`, enabled on workers), `commands` (`commandRunner`: `RunCommand` mapping `/compact` -> `compact`), `turns` (`turnProber`: `TurnRunning` -> `get_state`); `mode` (`modeSetter`) intentionally `nil` because live lead permission switches restart the process via `LifecyclePolicy.RestartLeadOnPermissionChange`.
       - Session metadata RPCs: `get_session_stats`, `get_available_models`, `get_available_thinking_levels`.
     - Model ID parsing: split `<provider>/<modelId>` on the **first** `/` via `strings.Cut` so nested IDs (e.g., `openrouter/anthropic/claude-3.7-sonnet`) pass `provider = "openrouter"`, `modelId = "anthropic/claude-3.7-sonnet"` losslessly to `set_model`.
     - Event mapping (`pirpc_mapper.go`):
       - Emit `Event.Harness = &HarnessReport{Version: wspiruntime.PinnedVersion}` on process open.
       - Map streaming text/reasoning/tool events, usage/stats from `get_session_stats`, and `agent_settled` as the turn completion boundary.
       - Intercept `ctx.ui.notify` JSON payloads carrying `ws_pi_event: "provider_response"` for HTTP 429/529 retry/quota hints and surface structured `Event.Notice` / retry hint metadata instead of raw JSON noise.
   - `instructions/agent-worker-pi-rpc.md`: concise worker instructions matching `agent-worker-codex.md` / `agent-worker-werk.md` (directing child work through `agent.spawn` and `mailbox.send`).

2. **Registration, Common Permission-Restart Seam, Config, Account Slots & Credential Protection Wiring:**
   - `agents-plugin-tool/internal/wsagent/registration.go`: append `PiRPCBackend{}` to `builtinRegistrations` (`registration.go:205`).
   - `agents-plugin-tool/internal/wsagent/backend_policy.go`, `runner_startup.go`, `runner_lead_start.go`, `permission.go`, `runner_events.go`:
     - Add runtime-only `LifecyclePolicy.RestartLeadOnPermissionChange bool` (`backend_policy.go`) and `piRuntimeBinder` / `Options.PiRuntime` wiring (`runner_startup.go:46-71`).
     - Propagate `spec.PermissionLevel = level` at `runner_lead_start.go:260` when `durable || life.RestartLeadOnPermissionChange`, set `c.dirtyPermission` at `permission.go:290` when `durableProcess || RestartLeadOnPermissionChange`, and restart in `applySettings` at `runner_events.go:1396` when `permission && (!rec.Lead || durableProcess || RestartLeadOnPermissionChange)`.
   - `agents-plugin-tool/internal/wsconfig/config.go`:
     - Add `"pi-rpc"` to `normalizedHarness` (`config.go:763`) and `aliasTargetKey` error text (`config.go:788`).
     - In `setAgentsTierForHarness` (`config.go:381-452`): when `key == "pi-rpc"`, skip `effective.Agents.Tiers[tier]` Codex fallback seeding and keep `mapping.Backend` bound to `"pi-rpc"` (do not call `InferBackend(model)` to overwrite `"pi-rpc"` with `"claude"`/`"codex"`).
     - In `ResolveAgentForHarnessConfig` (`config.go:488-504`) and `ResolveAgentTierForHarness` (`config.go:532-534`): when the winning alias bucket is `"pi-rpc"` and `mapping.Backend == ""`, default `backend = "pi-rpc"` instead of `InferBackend(model)`.
   - `agents-plugin-tool/internal/wsconfig/scope.go`: add `"pi-rpc"` to the five harness-bucket `RegisterDefaultScope` slices (`scope.go:294, 300, 306, 312, 325`).
   - `agents-plugin-tool/internal/wsconfig/snapshot.go`: handle `harness == "pi-rpc"` in `InspectTier` (`snapshot.go:41-61`) as explicit-only (no fallback to `default`/`codex`, no `WerkDefaultProvider` injection).
   - `agents-plugin-tool/internal/wscred/account_slots.go`: add `"pi-rpc"` to `SupportedSlotHarness` (`121`), `harnessHomeSpec` (`157` -> `"PI_CODING_AGENT_DIR"`, `".pi/agent"`), `HarnessForConfigEnv` (`170` -> `"PI_CODING_AGENT_DIR"` -> `"pi-rpc"`), and `ActiveSlots()` (`616`). Keep `SlotLoginHarnesses()` unchanged (`"codex"`, `"claude"`).
   - `agents-plugin-tool/internal/wsdashboard/plan_usage.go`: guard `classifyActiveSlotsLocked` (`191-198`) so registered harnesses without `SeparateFiveHourHistory` are skipped and the empty-traits fallback checks `slices.Contains(wscred.SlotLoginHarnesses(), h)`, preventing `"pi-rpc"` slots from ever being classified as `gptHarness`.
   - `agents-plugin-tool/internal/wsdashboard/fs_outside.go`: add `".pi/agent/auth.json"` to `outsideDenied` (`107-120`).
   - `agents-plugin-tool/internal/wsdashboard/fs_denied.go`: add `{"PI_CODING_AGENT_DIR", "auth.json"}` to `deniedBasePaths` (`85-95`).
   - `agents-plugin-tool/internal/wsagent/harness_name_scan_test.go`: extend `harnessExact`, `harnessAlt`, `backendIdent` (`269-271`) with `pi-rpc` / `BackendPiRPC`, and add `classExternal` allowlist entries for `internal/wsdashboard/fs_outside.go` (`".pi/agent/auth.json"`) and `internal/wsdashboard/fs_denied.go` (`"PI_CODING_AGENT_DIR"`).
   - `agents-plugin-tool/internal/wsagent/registration_test.go` & `backend_policy_test.go`: update `TestBuiltinBackendRegistrationDefaults` (5 built-in backends including `BackendPiRPC`) and add `BackendPiRPC` row to `TestBuiltinBackendPolicyTable`.

3. **Phase 2 Verification:**
   - Fake-binary Pi RPC integration tests (`internal/wsagent/pirpc_test.go`):
     - Launch args verification: `-ne -e builtin:mcp -e <bridge> -ns -np --no-themes -na -nc`, `--exclude-tools bash` for workspace-write/lead cutover, `-nbt` for `PermissionReadOnly` cutover, absence of `-nt`, and uninstalled fail-closed error (no auto-download).
     - Live lead permission-change test: default lead derives `PermissionWorkdirWrite` (`"advisory"`, no HTTP 400), `SetPermissionLevel` between `read-only` and `workdir-write` restarts the `pi-rpc` process to flip `-nbt` vs `--exclude-tools bash`, and holder reattach emits no failed permission reconciliation.
     - Shared runtime binder test: `WithPiRuntime` / `Options.PiRuntime` injects a shared `*wspiruntime.Manager` without freezing `WS_CACHE_HOME`.
     - Env verification: `PI_SKIP_VERSION_CHECK=1`, `PI_TELEMETRY=0`, `PI_CODING_AGENT_DIR` slot binding, `WS_MCP_SESSION_URL` & `WS_MCP_SESSION_TOKEN` present in env and absent from argv, `PI_OFFLINE` unset, parent harness vars scrubbed.
     - Protocol round-trip over LF-only JSONL: `prompt` -> streaming deltas -> `agent_settled`, `steer`, `follow_up`, `abort`, `clear_queue`, `compact`, `get_state` (`TurnRunning`), `get_session_stats`, `get_available_models`, `set_model` with nested `openrouter/anthropic/claude-3.7-sonnet`, `set_thinking_level`, `get_available_thinking_levels`, and `ws_pi_event: "provider_response"` 429/529 warning interception.
     - MCP client detection test verifying `clientInfo.name == "ws-pi-rpc-bridge"` resolves to `BackendPiRPC` while legacy `"ws-pi-bridge"` still resolves to `BackendPi`.
     - Slot, usage & credential tests: `internal/wscred` D10 resolution + T0 round-trip preservation for `"pi-rpc"` slots; `internal/wsdashboard` `TestFSLocalDeniedProviderCredentials` covering `.pi/agent/auth.json` and `PI_CODING_AGENT_DIR/auth.json`, plus a `planUsageStore.classifyActiveSlotsLocked` regression test proving `"pi-rpc"` never leaks into `gptHarness` even when `"codex"` is absent; `internal/wsconfig` tests verifying `"pi-rpc"` tier writes with `openrouter/anthropic/claude-3.7-sonnet` and `openai/gpt-5` preserve `Backend: "pi-rpc"`, effort-only edits on unset `"pi-rpc"` tiers do not seed Codex defaults, bucket default scopes work, and `InspectTier` stays explicit-only.
     - Full regression check: `go test ./internal/wspiruntime/... ./internal/wsagent/... ./internal/wsconfig/... ./internal/wscred/... ./internal/wsdashboard/... ./internal/mcp/...` including `TestCommonCodeHasNoHarnessNameBranch` (`harness_name_scan_test.go:555`) with the `TestHarnessNameScan*` helpers and the retired `BackendPi` test suite.
