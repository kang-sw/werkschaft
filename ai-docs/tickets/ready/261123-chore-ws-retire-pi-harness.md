---
title: "Pi 하네스 철거: Pi 백엔드, 확장 패키지, 번들 payload 제거"
parent: 261114-epic-ws-common-session-foundation
related:
  261122-feat-ws-builtin-agent-harness: "werk replaces Pi; its Decision 1 moved Pi's retirement here and its Decision 9 (pi.* to werk.*) is carried out here"
  261119-feat-ws-audit-capture-harness-adapters: "its Phase 4 (Pi adapter) is dropped by this ticket"
  261114-feat-ws-live-reasoning-summary: "Pi transport of reasoning summaries retires with the extension"
  261122-feat-ws-web-tools: "Pi's web tools retire without waiting for werk's replacement (Decision 6)"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 14ca4dcc4c18ea66
sage-review-completeness-reviewed: 14ca4dcc4c18ea66
---

# Retire the Pi harness

## Background

Owner direction (2026-11-23, board (05)=b, note verbatim): "네 요즘 pi 아예 안
쓰기도 합니다." Pi is removed now, independently of the werk harness's phases,
instead of after the parity and A/B gate the werk ticket used to hold (its
Decision 1, revised the same day; revision recorded in `ceff96b7f`, pending 261122-feat-ws-builtin-agent-harness). Implementation lands on the integration track with werk Phase 1 already
available; the track reaches develop as a whole only after the werk ticket is
fully implemented, with its last phase in execution order completed. Tell the
release owner before that merge (technical (29), refined by (33)).

Footprint (read-only survey at `b3ee18816`, 2026-11-23; Go paths below are relative to `agents-plugin-tool/`):

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
  staging, `agents-plugin-tool/scripts/bump-ws-version.sh`, CI and release workflows,
  `agents-plugin-tool/scripts/generate-package-assets.py`,
  `agents-plugin-tool/scripts/build-release-assets.sh`, README, AGENTS.md,
  `ai-docs/ship/ws.md`, and manuals (about 143 references in eight files).
- Hidden couplings: tracker support conversations can select a Pi profile
  (`wsagent/support.go`, `support.json`); the dashboard plan-usage probe falls
  back to Pi's stored `openai-codex` OAuth credential
  (`wsdashboard/plan_usage_probe.go`); the companion director can run a
  `pi -p` one-shot (`internal/wsdashboard/companion_director.go` dispatches through the
  one-shot registry; `internal/wsagent/pi.go#L75-L80` supplies Pi's arguments); `session.hire` accepts harness `pi`; the `WS_MCP_PI_TOKEN` and
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
3. **`pi.*` tier names (announced (20), clarified by technical (29)).**
   `pi.<tier>` resolves to `werk.<tier>` only, runs werk's availability check,
   and is refused with werk's actionable reason when werk is unavailable;
   it never borrows another backend's tier. This ticket implements the alias
   in Phase 1 on the integration track where werk Phase 1 already exists.
   A user's configured `pi.<tier>` model is copied into an unset
   `werk.<tier>` only when the model ID matches an OpenRouter model-list ID
   exactly. Stored values that name `pi` (tier routing nodes and bundles,
   `agents.tier` harness `pi`, profiles with backend `pi`) are still accepted
   on read, so no stored value is refused. Rejected: refusing the `pi`
   spelling as unknown; falling back to the default tier chain.
4. **Legacy `pi.*` MCP handlers stay through 0.52 (announced (21),
   from the parent epic's (54)=b; release timing owner (27)=a).** They remain callable and hidden; a
   handler whose work needed the removed backend answers a clear error
   instead. Authorized `pi.list` and transcript history reads keep working
   independently of the removed runtime and dashboard knob (technical (31)).
   `session.hire` with harness `pi` is refused with a reason.
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
   The lead revises `261119-feat-ws-audit-capture-harness-adapters` Phase 4
   (Pi adapter) as dropped before Phase 1 starts, per technical (32).
10. **Compatibility tiers (technical (30); owner (27)=a).**
    Only tier calls that still work through the werk alias are T0. A previously
    working Pi caller without a werk OpenRouter key or with a Pi model ID that
    is not an OpenRouter model ID can receive a refusal; those tier refusals
    are T3 (`internal/wsagent/werk.go:149–175` on the integration track;
    technical (33)). Legacy `pi.*`
    bridge handlers needing the backend, `session.hire` harness `pi`, and
    `config.tune` writes of retired `pi.*` keys or
    `dashboard.pi_spawn_enabled` are T3 because working calls
    become refusals. Owner (27)=a explicitly approves the T3 boundary
    and minor bump to 0.52. The integration track merges to develop at 0.52;
    legacy `pi.*` bridge handlers stay through 0.52 and are removed in 0.53. Old history reads, stored `pi` values, and ignored
    retired settings retain their compatibility paths. Each phase's Result
    states the tier per surface with old/new reader/writer and recovery source
    evidence per `ai-docs/manuals/release-compatibility.md`.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: ai-docs/manuals/skill-authoring.md (declared for `agents-plugin/rsrc/`, `agents-plugin/skills/`, `agents-plugin-wsflow/rsrc/`, `agents-plugin-wsflow/skills/`, `agents-plugin-tool/internal/wsdoc/conventions/`)
- Convention: ai-docs/manuals/wsflow-mirroring.md (declared for `agents-plugin/rsrc/`, `agents-plugin/skills/`, `agents-plugin/hooks/`, `agents-plugin-wsflow/`)
- Convention: ai-docs/manuals/ws-mcp.md (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: ai-docs/manuals/release-compatibility.md (declared for `agents-plugin-tool/internal/`)
- Convention: ai-docs/manuals/performance.md (declared for `agents-plugin-tool/`)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for `agents-plugin-tool/internal/mcp/config_registry.go`, `agents-plugin/config-manifest.json`, `agents-plugin/agy-config-manifest.json`, `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-pi/config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/config_routes.go`, `agents-plugin-tool/internal/wsdashboard/config_proposals.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)

- Manuals: `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/wsflow-mirroring.md` (shared playbooks and wsflow shims
  that mention Pi), `ai-docs/manuals/config-settings-ui.md` (retired
  settings rows), `ai-docs/manuals/performance.md`,
  `ai-docs/manuals/ws-mcp.md` (its Pi sections).
- Shipped playbooks and conventions lose Pi-specific branches without
  gaining any rule specific to this repository (Architecture Rule 4).
- Integration landing (technical (29)): implement on the track carrying werk
  Phase 1. The release owner sequences the complete werk + retirement track's
  arrival on develop only after the werk ticket is fully implemented (its
  last phase in execution order completes); tell the release owner before
  that merge. Owner (27)=a approves the T3/minor boundary at 0.52,
  including this track's merge to develop; handlers are retained through 0.52
  and removed in 0.53.
- Related-ticket prerequisite (technical (32)): Phase 1 starts only after the
  related-ticket revision commit is on develop and merged into the
  integration track (revision commit: `c7fd8c1a2`, on develop via `31e1755b3`;
  merged into the integration track via `37588484e`),
  revising the active Pi parity/A-B gate and runnable Pi criteria in:
  `261114-epic-ws-common-session-foundation`,
  `261122-feat-ws-web-tools`,
  `261119-feat-ws-audit-capture-harness-adapters` (drop Phase 4),
  `261114-feat-ws-live-reasoning-summary`,
  `261115-feat-ws-claude-reasoning-summary-producer`,
  `261115-feat-ws-whiteboard-agent-card`,
  `261115-feat-ws-worker-profile-session-spawn`,
  `261115-feat-ws-session-hire-approval`,
  `261116-feat-ws-subagent-worktree-lease`, and
  `261115-feat-ws-permission-profile` (remaining Pi regressions).
  These are lead-owned contract revisions, not prerequisites to implement the
  other tickets' features. Historical Results remain unchanged.
- Out of scope: the werk harness itself; removing the legacy `pi.*` MCP
  handlers (0.53); the Python session holder.

## Prior Decisions

- 0fdff051 (2026-11-23, commit): "Owner (05)=b: Pi retires now, independently of the werk harness phases." — bearing: supports
- ceff96b7f (2026-11-23, commit): "Pi retires on its own ticket." — bearing: supports
- 261114-epic-ws-common-session-foundation (2026-11-22, Cross-Child Decisions): "pi.* hidden from tools/list (T0) supersedes its alias (owner 19:16, (54)=b)" — bearing: constrains
- f2d70d5b (2026-11-22, commit): "PiReadyKnownNeverRunsThePiProbe: zero probes, the record read once per process, and the process probe wins once it has run." — bearing: constrains
- 261117-feat-ws-harness-capability-catalog (2026-11-17, Result): "This result feeds the Pi extension retirement parity criterion; it approves no removal." — bearing: supports (Decision 1 here supplies the later removal authority).

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Both phases span daemon packages, plugin distributions, installers and manuals. |
| scope.surface | public-interface | Legacy pi.* MCP handlers, session.hire.harness, agent.spawn tiers and config manifests are caller-visible. |
| scope.new_public_symbol | no | Removal plus compatibility handling; no new public symbol requested. |
| scope.new_type_contract | no | Decisions 2–5 preserve stored spellings and handlers; no new persisted shape requested. |
| scope.test_surface | existing | agents-plugin-tool/internal/wsagent/pi_worker_recovery_test.go, internal/mcp/pi_tools_test.go, internal/wsagent/support_tiers_test.go and wsflow package tests. |
| complexity.reuse_points | confirmed | internal/mcp/pi_tools.go retains legacy dispatch; internal/wspackage/package.go loads the Pi overlay; internal/wsagent/pi.go registers OneShot support. Go paths relative to agents-plugin-tool/. |
| complexity.side_effect_risk | high | Retirement changes live-holder recovery and launch admission while retaining old history and generic MCP token forwarding. |
| risk.correctness | high | Stored pi tiers, profiles, routing bundles and support selections must remain readable while runtime execution changes. |
| risk.fit | moderate | Deletion spans common backend registrations and mirrored shipped resources; repository-specific rules must stay outside shipped text. |
| risk.test | high | Phase 1 requires cross-version stored-data and live-holder coverage; Phase 2 spans generated payloads, installers and Windows verification. |
| risk.security_or_contract | high | Legacy call refusals are T3 approved at 0.52 by owner (27)=a; working werk tier aliases are T0, while unavailable-key/model cases are T3; forwarding tokens still serve generic workers. |

## Phases

### Phase 1: Daemon, MCP and dashboard

- Remove the Pi backend registration and the `wsagent` Pi launch, process,
  event, probe, lead, policy and rejoin code; keep the Pi session JSONL
  reader and the `ws-pi-bridge` recognition (Decision 2).
- Implement the werk tier alias and exact-ID model handling per Decision 3.
- MCP: `pi_live.go` and `pi_forward.go` paths that need the backend go;
  legacy handlers per Decision 4; `session.hire` refusal. Split authorized
  `pi.list` and transcript reads before the runtime/retired dashboard-knob
  check (`internal/mcp/pi_tools.go:142–199,242–253`); test `pi.list` and
  `pi.transcript` independently still succeed with ownership checks after
  retirement, while execution calls receive the retirement refusal (technical (31)).
- Config per Decision 5; couplings per Decision 7.
- Dashboard: Pi leaves the lead composer, rail launch, HUD labels and
  badges, settings and session revival; ended Pi sessions still open as
  history.
- Verification: `go build`, `go vet` (darwin and `GOOS=windows`), the
  `wsagent`, `mcp`, `wsconfig`, `wsdashboard` suites; tests for a stored
  record, tier routing graph, profile and `support.json` that name `pi`
  (read without error, redirected as Decision 3 says); a legacy
  `pi.*` handler call; the dashboard frontend tests touched.
- Upgrade/restart verification: recover a live Pi holder and assert its history
  opens, its record is shown as ended, its process is left alive, and resume,
  wake and handoff are refused (Decision 2; technical (28)). Add a dedicated
  startup sweep guard and test (`internal/wsagent/runner_startup.go:354–358`)
  for a live legacy Pi holder with no open conversation record as well as a
  record-backed holder: neither process may be terminated. The holder does
  not idle out a live child (`internal/wsholder/holder.py:157–159`), so safety must hold without
  waiting for it to exit (technical (31)).

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
