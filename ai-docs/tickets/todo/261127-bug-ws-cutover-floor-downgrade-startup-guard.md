---
title: "SQLite cutover 이후 구버전 데몬 기동 차단 가드 및 호환성 경계 보강"
related:
  261112-feat-ws-mixed-version-compat-guards: sibling (mixed-version skew detection, store write refusal, and compatibility enforcement)
---

# Block Legacy Daemon Startup After Irreversible SQLite Cutover and Harden Compatibility Boundaries

## Background

On 2026-11-27, during a `0.53.2-dev` dashboard daemon exit, Werkschaft auto-launched a bundled `v0.53.1` daemon (`pid 10924`) against the shared `<daemon dir>` state after SQLite cutover had already completed (`metadata.sqlite` with `import_state='complete'` and `cutover_started='1'`).

### Root Mechanism

1. **Irreversible cutover fencing (`v0.53.2`):** SQLite cutover (`645c62d87f5a7bb4a7e2c819290c846dc7a4359e`, `feat(wsmeta): add live CRUD methods and cutover orchestrator`) fenced all 1,817 on-disk `sessions/<id>/permission.json` files with `{"cutover_fenced":true,"permission_level":"!cutover-fenced"}` (`wsmeta/cutover.go:30,38,156-160`, `PermissionCutoverFencedLevel = "!cutover-fenced"`).
2. **`Major.Minor`-only floor check admitted the downgraded patch:** `daemon-minor-floor` (`22bc12a63fa38225fafaae0236fb3503e1545f1d`, `minor_floor.go:169`, `CheckMinorFloor`) compares only `Major.Minor` (`0.53 == 0.53`). Because `daemon-minor-floor = "0.53"`, `v0.53.1` passed the floor check despite lacking SQLite metadata support.
3. **Destructive half-broken startup in `v0.53.1`:**
   - `v0.53.1` (pre-SQLite, no `runner_meta.go`) read the fenced `permission.json` via `permission_store.go:143-145` and rejected `"!cutover-fenced"` (`level.rank() == 0` -> `invalid permission_level`).
   - Startup logged advisory rejoin errors on 9 sessions (6 leads + 3 workers), **ended 16 live holders** (`ending conversation holder ...: no open conversation runs under it`), and failed fresh starts (`start: invalid permission_level`) on 3 leads (`sedativegeneticsdroolhalved`, `eaterybotanicalsnifffreeness`, `unwelcomemutilatoroldbony`) until `v0.53.2` resumed.

## Decisions

- **Fail-closed startup guard on incompatible store/cutover state — owner decision `(1814)=a` (2026-11-27):**
  - When on-disk store state or a completed cutover makes an older daemon binary unable to operate meaningfully, startup **must block cleanly before touching or terminating live holders** ("네 애초에 기동해도 의미 없는 상황이 되면 그건 차단이 맞겠네요 ..").
- **Compatibility Tier — T3 (old-version boundary, `ai-docs/manuals/release-compatibility.md:56-62`):**
  - Irreversible store fencing (`!cutover-fenced`) is a **T3** surface because an older binary's reads/starts fail and its holder cleanup destroys active state.
  - Because `daemon-minor-floor` and `/mcp` hello currently gate only on `Major.Minor`, shipping a patch-level cutover (`0.53.2` over `0.53.1`) bypassed the floor check and admitted an older daemon into a destructive half-broken state.
  - Full T3 boundary analysis is required before settling the exact floor/generation/schema enforcement mechanism for `ready/` promotion.

## Constraints

- **Release compatibility (`ai-docs/manuals/release-compatibility.md`, declared for `agents-plugin-tool/internal/`):**
  - T3 boundaries require a minor bump and explicit owner approval (`Release Rules` #2). Merely writing a marker or schema field that an already-shipped older binary ignores does not protect data; enforcement must account for both post-fix binary guards and release-gate/minor-bump discipline for any future irreversible fence.
  - Preserve existing `Ensure` drain safety: a refused candidate successor must fail before requesting drain or killing an incumbent daemon or live holders.
- **Minimal-I/O startup budget (`ai-docs/manuals/performance.md`, declared for `agents-plugin-tool/`):**
  - Target machines are corporate Windows PCs with heavy antivirus (AhnLab V3) where every file touch pays 10–100x syscall overhead.
  - Keep startup floor/generation verification minimal-I/O (single bounded read or existing startup SQLite/floor check under the daemon lock; zero per-request or polling re-reads).
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`, declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`):**
  - Startup guards, error payloads, and Werkschaft preflight checks ship downstream and must not depend on devenv-only paths or repository-local conventions.

## Phases

### Phase 1: Cutover-aware / generation-or-semver startup floor guard in `ws-mcp` daemon

Harden `ws-mcp` daemon startup and replacement preflight so a candidate binary that cannot support the current store generation or cutover state refuses immediately before any side effects:

1. **Pre-side-effect cutover/floor guard:**
   - Before holder cleanup, session rejoin, `metadata.sqlite` mutation, or taking over the dashboard listener, verify that the candidate daemon binary supports the store's cutover generation / full semver floor (e.g., store format/cutover floor check alongside `daemon-minor-floor`).
   - Fail fast with a structured, actionable version-mismatch error so a stale older binary never enters startup recovery or terminates live holders.
2. **Holder termination root-cause investigation (`buildAgentRecordFence` / `agent.json`):**
   - Investigate the `buildAgentRecordFence` / `agent.json` interaction that caused `v0.53.1` to emit `ending conversation holder ...: no open conversation runs under it` across 16 live holders, and ensure any related fence/metadata invariants are covered so holder liveness is never misclassified during version boundaries.
3. **T0–T3 compatibility & release-gate enforcement:**
   - Analyze and enforce both sides of the compatibility boundary:
     - *(a) Runtime forward guard:* how post-fix binaries record and enforce a store generation / semver floor so future older post-fix binaries refuse cleanly against newer cutovers (keeping startup file touches minimal per `performance.md`).
     - *(b) Release-time boundary rule:* enforce minor-bump / release-gate checks whenever an irreversible T3 store fence is introduced, acknowledging that an already-shipped older binary cannot run newly added check code.
4. **Verification:**
   - Unit/integration tests simulating an older binary (or lower generation/semver capability) attempting startup (`absence`, `upgrade`, `replace`) against a cutover-completed store, proving zero holder terminations, zero session/store writes, and an immediate actionable version-mismatch error.

### Phase 2: Werkschaft desktop preflight & bundled daemon version alignment

Depends on Phase 1's structured version-mismatch contract.

1. **Desktop auto-restart preflight:**
   - Prevent Werkschaft desktop auto-restart from silently launching an older bundled daemon binary against a newer cutover/floor store (compare bundled daemon version/generation against the store floor or retained running executable before spawning).
2. **Actionable UI surfacing:**
   - Catch and surface Phase 1's structured version-mismatch error clearly in the Werkschaft UI with upgrade guidance rather than looping auto-restarts or leaving sessions stranded.
3. **Verification:**
   - Desktop preflight tests covering stale bundled daemon vs. higher store floor/cutover generation, verifying auto-restart suppression and structured error presentation.

## Notes

Design considerations to settle during `/lead-discuss` before promoting to `ready/`:

- **Floor & generation mechanism:** Whether to extend `daemon-minor-floor` to full semver / store-generation floor (or introduce a separate cutover/store-generation floor check alongside `metadata.sqlite` `import_state`/`cutover_started`), balancing `performance.md` file-touch budget against raw launcher readability.
- **Release-gate policy:** How `lead-ship` / release tests should detect or enforce the `Minor + explicit owner approval` rule whenever an irreversible T3 fence (such as `!cutover-fenced`) is introduced, since pre-cutover shipped binaries (like `v0.53.1`) cannot retroactively enforce new floor rules.
- **Werkschaft bundled binary precedence:** How Werkschaft reconciles its bundled `ws-mcp` version with `last-running-executable.json` and store floor metadata on auto-restart when a newer dev or plugin daemon previously owned the cache root.
