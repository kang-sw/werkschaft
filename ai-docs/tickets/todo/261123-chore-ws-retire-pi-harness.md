---
title: "Pi 하네스 철거: Pi 백엔드, 확장 패키지, 번들 payload 제거"
parent: 261114-epic-ws-common-session-foundation
related:
  261122-feat-ws-builtin-agent-harness: "werk replaces Pi; its Decision 1 moved Pi's retirement here and its Decision 9 (pi.* to werk.*) is carried out here"
  261119-feat-ws-audit-capture-harness-adapters: "its Phase 4 (Pi adapter) is dropped by this ticket"
  261114-feat-ws-live-reasoning-summary: "Pi transport of reasoning summaries retires with the extension"
  261122-feat-ws-web-tools: "Pi's web tools retire without waiting for werk's replacement (Decision 6)"
---

# Retire the Pi harness

## Background

Owner direction (2026-11-23, board (05)=b, note verbatim): "네 요즘 pi 아예 안
쓰기도 합니다." Pi is removed now, independently of the werk harness's phases,
instead of after the parity and A/B gate the werk ticket used to hold (its
Decision 1, revised the same day). Work that ran on Pi moves to the Codex and
Claude tiers until werk lands.

Footprint (read-only survey at `b3ee18816`, 2026-11-23):

- `agents-plugin-pi/`: about 42k source lines, 50k test and fixture lines,
  46k generated `dist/` lines, build scripts, launcher and forwarder shims,
  mirrored resources, and workflow ZIPs for ws and wsflow.
- Go daemon: 11 Pi-specific production files (about 2.6k lines) and 16 test
  files (about 4.1k) under `internal/wsagent` (`pi*.go`); about 174 explicit
  Pi lines in shared `wsagent` files (registration, profiles, admission,
  launch, holder recovery, session state, handoff, support, compaction).
- MCP and config: `internal/mcp/pi_tools.go`, `pi_forward.go`, `pi_live.go`
  (about 1.1k lines) and six Pi-named tests; about 91 Pi lines in shared MCP
  files; the `agents-plugin-pi/config-manifest.json` overlay (15 `pi.*` keys)
  loaded by `internal/wspackage/package.go`; `dashboard.pi_spawn_enabled` in
  the dashboard manifest.
- Dashboard: about 62 Pi lines in 15 Go files and 28 in eight JS/CSS/template
  files (lead composer and permission display, rail launch, HUD labels,
  settings, session revival, plan usage).
- Distribution and docs: the root `package.json` (Pi's git-install entry),
  the prepared Pi payload inside the ws and wsflow packages (about 47.7k text
  lines, 7.9 MB of ZIPs), `install.sh` and `scripts/install-claude-plugin.ps1`
  staging, `bump-ws-version.sh`, CI and release workflows,
  `generate-package-assets.py`, `build-release-assets.sh`, README, AGENTS.md,
  `ai-docs/ship/ws.md`, and manuals (about 143 references in eight files).
- Hidden couplings: tracker support conversations can select a Pi profile
  (`wsagent/support.go`, `support.json`); the dashboard plan-usage probe falls
  back to Pi's stored `openai-codex` OAuth credential
  (`wsdashboard/plan_usage_probe.go`); the companion director can run a
  `pi -p` one-shot (`wsdashboard/companion_director.go`, falls back to
  Claude); `session.hire` accepts harness `pi`; the `WS_MCP_PI_TOKEN` and
  `WS_MCP_PI_ENDPOINT` forwarding is also used by generic workers;
  `PiTurnReporter` and `ChildCheck` → `mcp.PiReady` naming.
- Release-compatibility surfaces: six legacy `pi.*` MCP tools, hidden from
  discovery but callable by older sessions (`internal/mcp/server_inventory.go`);
  `agent.spawn` Pi tiers; `session.hire.harness`; the `ws-pi-bridge` client
  identity; `pi.*` config keys; `agents.tier` harness `pi`; tier routing nodes
  and bundles naming `pi`; `agent.profile.*` with backend `pi`; agent records,
  native Pi session JSONL, sidecars, legacy `pi-worker` holder descriptors,
  the Pi probe cache and plan-usage history.

## Decisions

1. **Remove Pi now, on its own schedule (owner (05)=b).** Not gated on any
   werk phase. Rejected: retiring after werk's parity and A/B gate.
2. **Old data stays readable; nothing resumes on Pi (announced (19), from
   (05)=b and the owner's note that Pi is no longer used).** Agent records,
   Pi session JSONL and transcripts stay readable through the existing
   readers; the `ws-pi-bridge` client-name recognition stays. A record or held
   process whose backend is `pi` is shown as ended and cannot be resumed,
   woken or handed off. The daemon never terminates a live Pi holder it finds
   after the upgrade; the holder ends on its own. Rejected: keeping a Pi
   resume path for continuity.
3. **`pi.*` tier names (announced (20), from werk Decision 9).** Until werk
   has merged, a `pi.<tier>` call (direct, through `agent.tier_aliases`, or
   from a tier routing node) is refused with a reason naming the Codex and
   Claude tiers. Once werk is on develop, `pi.<tier>` resolves to
   `werk.<tier>` only, runs werk's availability check, and is refused with
   werk's actionable reason when werk is unavailable; it never borrows another
   backend's tier (the resolver returns a backend with the model,
   `wsconfig/config.go`). A user's configured `pi.<tier>` model is copied into
   an unset `werk.<tier>` only when the model ID matches an OpenRouter
   model-list ID exactly. Stored values that name `pi` (tier routing nodes and
   bundles, `agents.tier` harness `pi`, profiles with backend `pi`) are still
   accepted on read, so no stored value is refused. Rejected: refusing the
   `pi` spelling as unknown (an old value would fail its whole family);
   falling back to the default tier chain.
4. **Legacy `pi.*` MCP handlers stay until the next minor (announced (21),
   from the parent epic's (54)=b).** They remain callable and hidden; a
   handler whose work needed the removed backend answers a clear error
   instead. `session.hire` with harness `pi` is refused with a reason.
5. **Config keys retire without migration (technical (22)).** The 15 `pi.*`
   keys and `dashboard.pi_spawn_enabled` leave the manifests, the overlay
   loader, and the settings UI; stored values are ignored by the new binary
   and still read by an older one (T0). Rejected: migrating them to werk keys
   (no werk key has the same meaning).
6. **Pi-only capabilities retire with Pi (announced (23), from (05)=b).**
   Pi's web tools, its reasoning-summary transport, its execute gateway, and
   its goal loop go without a replacement in this ticket; werk's own web tools
   and summaries follow on their tickets.
7. **Couplings are cut to the remaining paths (technical (24)).** Support
   conversations offer only the remaining harnesses, and a stored Pi
   selection falls back to the default with a notice. The plan-usage probe
   keeps its Codex `auth.json` source and drops the Pi credential fallback.
   The companion director's one-shot uses the remaining one-shot backends
   (Claude today). The MCP token and endpoint forwarding keeps its behavior
   for generic workers; its `PI`-named environment variables are renamed only
   if no running cross-version reader depends on them, otherwise they keep
   their names with a comment. Rejected: leaving Pi branches as dead code.
8. **The package is deleted, not archived (technical (25)).**
   `agents-plugin-pi/`, the root `package.json` Pi entry, and the prepared Pi
   payload in both plugin packages are deleted; git history keeps them.
   Rejected: moving them under `ai-docs/.old/` (no reader would use them).
9. **Pi-only tickets close as dropped (announced (26), from the werk
   ticket's former Phase 3).** The open `idea/` and `todo/` tickets whose
   subject exists only in Pi are dropped with a pointer to this ticket;
   `261119-feat-ws-audit-capture-harness-adapters` Phase 4 (Pi adapter) is
   marked dropped there.
10. **Compatibility tiers.** Removals are new-binary behavior with no
    persisted shape change: old data is read (Decision 2), stored `pi`
    values are accepted (Decision 3), retired keys are ignored (Decision 5),
    legacy MCP handlers stay (Decision 4). Expected tier T0 for every
    surface; each phase's Result states the tier per removed surface with
    source evidence, per `ai-docs/manuals/release-compatibility.md`.

## Constraints

- Manuals: `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/wsflow-mirroring.md` (shared playbooks and wsflow shims
  that mention Pi), `ai-docs/manuals/config-settings-ui.md` (retired
  settings rows), `ai-docs/manuals/performance.md`,
  `ai-docs/manuals/ws-mcp.md` (its Pi sections).
- Shipped playbooks and conventions lose Pi-specific branches without
  gaining any rule specific to this repository (Architecture Rule 4).
- Release coordination: the release owner sequences when the removal reaches
  develop; tell them before merging.
- Out of scope: the werk harness itself; removing the legacy `pi.*` MCP
  handlers (next minor); the Python session holder.

## Phases

### Phase 1: Daemon, MCP and dashboard

- Remove the Pi backend registration and the `wsagent` Pi launch, process,
  event, probe, lead, policy and rejoin code; keep the Pi session JSONL
  reader and the `ws-pi-bridge` recognition (Decision 2).
- Tier handling per Decision 3 (the pre-werk refusal; the werk redirect is
  added in the change that merges werk if this phase lands first).
- MCP: `pi_live.go` and `pi_forward.go` paths that need the backend go;
  legacy handlers per Decision 4; `session.hire` refusal.
- Config per Decision 5; couplings per Decision 7.
- Dashboard: Pi leaves the lead composer, rail launch, HUD labels and
  badges, settings and session revival; ended Pi sessions still open as
  history.
- Verification: `go build`, `go vet` (darwin and `GOOS=windows`), the
  `wsagent`, `mcp`, `wsconfig`, `wsdashboard` suites; tests for a stored
  record, tier routing graph, profile and `support.json` that name `pi`
  (read without error, refused or redirected as Decision 3 says); a legacy
  `pi.*` handler call; the dashboard frontend tests touched.

### Phase 2: Package, distribution and docs

Depends on Phase 1.

- Delete `agents-plugin-pi/`, the root `package.json` Pi entry, and the
  prepared Pi payload in `agents-plugin/` and `agents-plugin-wsflow/`.
- Installers, version bump, CI, release and asset scripts stop building,
  staging or validating Pi.
- Docs: README, AGENTS.md (package list and Pi references), `ai-docs/ship/`,
  manuals, shared playbooks and their wsflow mirrors.
- Tickets per Decision 9.
- Verification: CI workflow dry run or `act`-free local equivalents of the
  changed scripts; `./install.sh update` on this machine; the package tests
  of ws and wsflow (runtime contract and skill-shim drift); `git grep -i
  '\bpi\b'` over shipped paths reviewed, with each remaining hit justified in
  the Result.
