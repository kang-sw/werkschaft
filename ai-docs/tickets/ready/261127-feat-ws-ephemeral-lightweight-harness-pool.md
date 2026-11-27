---
title: "공통 하네스 세션 미저장·ACP 워밍 캐시 경량 인스턴스 풀 및 동적 티어 라우팅"
related:
  261127-feat-ws-companion-layered-agents-and-naming: Tier 1 live companion reaction consumer replacing cold-spawn RunOneShot
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 7bb53dc107cc46c5
sage-review-completeness-reviewed: 7bb53dc107cc46c5
---

# Common Harness Ephemeral Non-Persistent / Warm-Cached Lightweight Instance Pool & Dynamic Tier Routing

## Background

Lightweight auxiliary LLM calls inside the daemon currently face two problems:

1. **Cold-spawn latency and harness coverage gap:** `companion_director.go` invokes `wsagent.RunOneShot` (`agents-plugin-tool/internal/wsagent/oneshot.go`) through the injected `DaemonOptions.CompanionOneShot` seam (`agents-plugin-tool/cmd/ws-mcp/dashboard.go#L218`, since wsdashboard cannot import wsagent), which spawns a fresh CLI process per call and currently only provides isolated one-shot support on `claude` (the only builtin registration declaring `OneShot`: `agents-plugin-tool/internal/wsagent/claude.go#L65`; a search for other `OneShot:` registrations in `wsagent` returned nothing).
2. **Session/store pollution risk:** Standard `wsagent.Runner` launches create `SessionDir` state, `wsmeta` SQLite rows, `wslive` transcript rings, `wsholder` recovery state, and harness-native session files on disk, triggering heavy antivirus (Windows AhnLab V3) file-touch overhead and polluting user session lists.

Meanwhile, two distinct consumers require ultra-low-latency, zero-pollution lightweight LLM invocations across all four supported harnesses (`werk`, `codex`, `claude`, `agy`):

- **Bounded-rotation warm callers:** e.g. the Tier 1 companion live reaction agent (`261127-feat-ws-companion-layered-agents-and-naming`), which benefits from keeping a warm context window across up to `N` turns/tokens before resetting.
- **Per-call clean one-shot callers:** e.g. `agent.spawn` dynamic tier routing, which needs an uncontaminated prompt/response turn with minimal startup delay and deterministic timeout fallback.

### Architecture Overview (Thin Wrapper over `wsagent.Backend` / `Process`)

`wsagent.Backend` (`Start(ctx, StartSpec) (Process, error)`) and `wsagent.Process` (`Events` / `Send` / `Interrupt` / `SetModel` / `SetEffort` / `Close`) already sit cleanly below `Runner` / `SessionDir` / `wsmeta` / `wslive` / `wsholder` (`agents-plugin-tool/internal/wsagent/backend.go`). Instead of introducing a parallel adapter layer:

- **Extend `StartSpec`** with `Ephemeral bool` and inline `SystemPrompt string`, relaxing the `MCPSession != nil` launch guards in `codex.go`, `agy.go`, and `werk_loop.go` when `Ephemeral: true` so ephemeral workers can run with zero ws-MCP attachment and zero session-dir/holder plumbing.
- **Add optional `conversationResetter` capability interface** (`Reset(ctx context.Context) error`, returning `ErrUnsupported` when unavailable so the pool falls back to process restart), following the existing `steerer` / `modeSetter` optional-interface pattern via `controlsFor` in `backend.go`.
- **Extend `internal/werk`:** skip `OpenStore` / file locks when `Config.Ephemeral` is true, and add `Agent.Reset()` clearing in-memory `transcript` / `journal` state.
- **Build `EphemeralPool`** as a thin wrapper over `wsagent.Backend` / `wsagent.Process` that owns exact-keyed slot caching, idle TTL eviction, reset/rotation policy, isolated temp-dir lifecycle (for `agy`), and structured JSON/timeout handling.

### Empirical Probe Results (Windows PC)

| Harness | Persistence / Disk Footprint | Warm Reset / Latency | Tool & MCP Isolation |
|---|---|---|---|
| `werk` (in-process) | 0 disk I/O when `OpenStore` is skipped | `Agent.Reset()` `< 1 ms` | `cfg.WS == nil` blocks all tools and MCP |
| `codex` (`0.170.0`) | 0 rollout `.jsonl` files with `thread/start` `ephemeral: true`; only `logs_2.sqlite-wal` mtime touches | Warm `thread/start #2` = `0.354 s` (`initialize` = `1.55 s`) | MCP disabled; shell / patch cannot be fully hidden (built-ins like `wait` / `exec` remain), so confined per `(1752)` |
| `claude` (`2.1.332`) | 0 `.jsonl` files with `--no-session-persistence` (empty `memory/` dir only) | `/clear` = `0.375 s` (`api_ms=0`) when `--disable-slash-commands` is omitted | Tools blocked via `--tools ""`, `--strict-mcp-config`, `--disallowedTools Skill` |
| `agy` (`1.5.15`) | No native non-persistence (`~458 KiB` `.db` + `brain/` remains after `session/close`) | Shared `agy acp` (without `--process-per-session`) `session/new #2` = `0.035 s` (`--process-per-session` is `2.78 s`) | `mcpServers: []` blocks user MCP (14 built-in tools remain); isolated `--gemini-dir` prevents polluting `~/.gemini` |

## Decisions

- **Four-harness default scope & thin pool wrapper — settled `(1731)=c`:**
  - **Chosen:** Enable all 4 harnesses (`werk`, `codex`, `claude`, `agy`) by default in Phase 1 via a thin `EphemeralPool` wrapper over `wsagent.Backend` / `wsagent.Process`.
  - **Exact Keying:** `EphemeralPool` NEVER returns an arbitrary warm agent; every acquisition specifies an explicit **`(harness, model, effort/tier)`** key (`AcquireSpec{Harness, Model, Effort, Mode}`) and looks up or starts that exact slot.
  - **`agy` Bounded Cleanup:** Because `agy` does not guarantee automatic LRU deletion of its `~458 KiB` `.db` files (plus `brain/` artifacts), `EphemeralPool` directly prunes/unlinks temporary session artifacts inside its isolated `--gemini-dir` via bounded LRU/rotation, idle eviction, and daemon shutdown cleanup so disk usage never grows unboundedly.
  - **Rejected alternatives:** Deferring `agy` or `codex` from Phase 1; building a separate process-management abstraction bypassing `wsagent.Backend`; sharing a generic unkeyed warm slot across different `(harness, model, effort/tier)` requests.
- **Lazy pool lifecycle & dual call modes — settled `(1732)=a`:**
  - **Chosen:** On-demand lazy startup + idle TTL eviction (default 1 slot per active `(harness, model, effort/tier)` key, 5–10 min idle TTL), supporting two call modes:
    1. **Pure one-shot:** `Reset` / clear on every call (for `agent.spawn` tier routing and stateless queries).
    2. **Bounded rotation:** Keep warm cache across up to `N` turns or token threshold before calling `Reset` (for companion live reactions).
  - **Rejected alternatives:** Eager startup of all harness slots at daemon boot (wastes memory and AV process-launch budget); purely cold per-call spawn (`RunOneShot`).
- **Shared contract & two-phase execution — settled `(1733)=a`:**
  - **Chosen:** Define both consumers' shared contract (`EphemeralRequest`, JSON response schema, timeout -> static fallback) from the start, executing in two sequential phases: Phase 1 (shared `EphemeralPool` + 4 harness extensions) -> Phase 2 (companion live reaction integration & `agent.spawn` dynamic tier router, opt-in default off per `(1753)`).
  - **Rejected alternatives:** Coupling only companion reactions first and retrofitting `agent.spawn` routing later with an incompatible request/fallback contract.
- **Residual-tool confinement for `codex` / `agy` — settled `(1752)=a`:**
  - **Chosen:** Accept the residual built-in tools on `codex` (`wait` / `exec`) and `agy` (14 built-ins) under explicit confinement: `codex` starts with the read-only sandbox and non-interactive approval policy via the existing `StartSpec.PermissionLevel` mapping; both `codex` and `agy` run in a pool-owned empty scratch cwd (never the user's repo root); the reply is parsed as JSON only and never executed. The earlier "MCP / shell / patch hideable" wording is withdrawn; MCP is disabled where the harness allows it, but residual built-ins are confined, not hidden.
  - **Explicitly supersedes** commit `06cbdf5a`'s all-tools-off requirement for these two harnesses.
  - **Rejected alternatives:** (b) additionally restricting the Phase 2 consumers to tool-free harnesses (`claude`, `werk`) by default; (c) dropping `codex` / `agy` from Phase 1 (would reverse `(1731)=c`).
- **Dynamic tier router default off — settled `(1753)=a`:**
  - **Chosen:** The `agent.spawn` dynamic tier router is opt-in and default off, enabled via `agent.tier_routing_dynamic`; ordinary spawns incur no extra model call or latency (T0).
  - **Rejected alternatives:** Default-on `auto` routing for every spawn.
- **Dynamic tier router contract — settled `(1754)` (technical):**
  - **Chosen:** Built on the existing tier grammar: settings `agent.tier_routing_dynamic` (bool, default off per `(1753)`) and `agent.tier_routing_dynamic_classifier` naming the classifier `(harness, tier)`, resolved through the static resolver (`mcp/tier_routing.go`, honouring `SpawnableHarness`); `PureOneShot` mode with a strict `Timeout`. Input: the spawn prompt/role and the tier targets allowed for that caller. Output: `{"tier": "<one of the allowed targets>"}`. A value outside the allowed set, invalid JSON, timeout, or error falls back to static resolution. `routedBinding` / `callerBinding` / caller-harness refusal checks remain the final authority. Config names stay downstream-generic per `shipped-surface-boundary`.
  - **Rejected alternatives:** A router-specific tier grammar (the sibling `261127-feat-ws-tier-routing-caller-target` ticket already changes tier grammar; a competing one is not acceptable).
- **Backlog scheduling — settled `(1751)=a`:**
  - **Chosen:** Capture in `todo/` backlog now; implementation scheduled sequentially after `v0.53.2` ships.
- **Compatibility tier — T0 transparent:**
  - New internal daemon package/pool, internal `wsagent` interface additions, and an opt-in (default off) routing hook with timeout -> static fallback. No wire protocol, `/mcp` hello, or persistent store format changes; older binaries and legacy static tier callers remain unaffected.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- **Performance & Windows AV (`ai-docs/manuals/performance.md`):**
  - Zero file I/O on the `werk` ephemeral path (`OpenStore` and file locks skipped when `Config.Ephemeral` is true).
  - Keep warm processes resident in memory until idle TTL so interactive reaction/routing calls avoid per-call OS process launches (Rule 6) and per-call disk writes.
  - Bound `agy` isolated `--gemini-dir` growth via whole-session/directory pruning in `EphemeralPool` on LRU rotation, idle eviction, and daemon shutdown rather than per-turn file scanning.
- **Release compatibility (`ai-docs/manuals/release-compatibility.md`):**
  - Preserve existing `wsagent.Backend`, `wsagent.Runner`, `wsagent.RunOneShot`, and static tier resolution behavior (`T0 transparent`). Dynamic tier routing must fall back cleanly to static tier resolution on timeout, error, or opt-out.
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):**
  - Any user-facing config keys, tool descriptions, or advisories added in Phase 2 must remain downstream-generic with zero devenv-specific citations.
- **Zero-pollution & tool isolation:**
  - Ephemeral launches must never write `wsmeta` rows, `wslive` rings, `wsholder` entries, or `SessionDir` transcripts, and must apply each harness's verified tool/MCP/persistence isolation flags (`--no-session-persistence` / `--tools ""` / `--strict-mcp-config` / `--disallowedTools Skill` on `claude`, `thread/start` `ephemeral: true` + read-only sandbox + scratch cwd on `codex` (residual built-ins confined per `(1752)`), `mcpServers: []` + scratch cwd + isolated `--gemini-dir` on `agy`, `cfg.WS == nil` + `Config.Ephemeral` on `werk`).

## Shared Contract & Interface Sketch

```go
// Optional Process capability (wsagent/backend.go), matching steerer/modeSetter:
type conversationResetter interface {
	Reset(ctx context.Context) error
}

// StartSpec extensions (wsagent/backend.go):
//   Ephemeral    bool
//   SystemPrompt string

type AcquireMode int // PureOneShot | BoundedRotation

type AcquireSpec struct {
	Harness      string
	Model        string
	Effort       string
	Mode         AcquireMode
	MaxTurns     int           // BoundedRotation rotation cap
	MaxTokens    int64         // BoundedRotation token cap
	SystemPrompt string
	IdleTTL      time.Duration // default 5–10m
}

type EphemeralRequest struct {
	Acquire        AcquireSpec
	Prompt         string
	ResponseSchema json.RawMessage // optional structured JSON output contract
	Timeout        time.Duration   // bounded wait; caller falls back on timeout
}
```

- On `Reset(ctx)` returning `ErrUnsupported` (or failing), `EphemeralPool` closes and restarts that exact `(harness, model, effort/tier)` slot cleanly.
- Callers such as `agent.spawn` dynamic tier routing supply a strict `Timeout` and JSON `ResponseSchema`, falling back deterministically to the existing static tier resolver whenever the ephemeral call times out, errors, or returns invalid JSON.

## Prior Decisions

- 06cbdf5a (2026-11-22, commit): "Rejected: opt-in codex via features.shell_tool=false. The live probe still showed exec, apply_patch and connector MCP tools, which violates the all-tools-off requirement." — bearing: superseded by `(1752)=a` (residual codex/agy built-ins accepted under explicit confinement)
- 06cbdf5a (2026-11-22, commit): "The executable is resolved with harnessCommand(Backend.Executable(), harness), the worker launch's resolver, so a Command override, the Windows cmd-shim unwrap and a PATH lookup behave the same." — bearing: constrains
- 8cd6f4a5 (2026-11-22, commit): "wsdashboard cannot import wsagent (wsagent imports wsdashboard), so the harness one-shot runner is injected as DaemonOptions.CompanionOneShot (same pattern as AgyCommand)." — bearing: constrains
- 261127-feat-ws-companion-layered-agents-and-naming (2026-11-27, Decisions): "Tier 1 live reaction agent ... uses warm caching + bounded rotation (cleanly resetting on turn count, elapsed time/idle TTL, ..." — bearing: supports
- 261127-feat-ws-tier-routing-caller-target (2026-11-27, Decisions): "Official `caller.*` / `caller.<size>` syntax — settled (1708)=a (T3): reserve `caller` as a first-class target keyword in `agent.tier_routing`" — bearing: constrains
- f4bef1cb (2026-11-26, commit): "Merged Phase 1 Tier Routing Settings page split, agent.tier_routing_expose toggle, agent.tier_routing_guidance prose key ... from impl/develop/tier-routing-page-and-expose" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsagent/backend.go, codex.go, agy.go, werk_loop.go, claude*.go, internal/werk, new EphemeralPool, wsdashboard/companion_director.go, mcp/tier_routing.go |
| scope.surface | cross-module | wsagent StartSpec and Process capability, werk Config, wsdashboard injection seam, mcp agent.spawn routing |
| scope.new_public_symbol | yes | EphemeralPool, AcquireSpec, EphemeralRequest, StartSpec.Ephemeral, werk Config.Ephemeral; none exist yet (search of agents-plugin-tool returned no Ephemeral match outside tests) |
| scope.new_type_contract | yes | conversationResetter Reset(ctx) error; AcquireSpec and EphemeralRequest structs |
| scope.test_surface | new-files | no pool tests exist; fake-backend tests for the pool are new; existing wsagent/werk tests cover launch guards |
| complexity.reuse_points | confirmed | wsagent Backend and Process, controlsFor optional-interface pattern (backend.go#L306-L325), runOneShot executable resolution (oneshot.go#L63-L77) |
| complexity.side_effect_risk | high | relaxes the MCPSession launch guards in codex.go#L120, agy.go#L270, werk_loop.go#L111 and adds an Ephemeral path through werk.OpenStore (werk/agent.go#L250) |
| risk.correctness | high | four harness-specific reset and isolation mechanisms rest on empirical probes the tree cannot confirm; warm slot concurrency and lifecycle |
| risk.fit | moderate | Process is single-goroutine by contract (backend.go#L178-L179) and the runner owns the conversation, so a pool outside Runner must serialize per slot |
| risk.test | high | Windows AV, latency, and real-CLI isolation claims cannot be covered by fake-backend tests alone |
| risk.security_or_contract | high | Phase 2 shipped-surface config keys and tool isolation: a missed flag leaks MCP or (on claude/werk) tools into a no-tools call; codex/agy residual built-ins are confined per `(1752)` |

## Phases

### Phase 1: Shared EphemeralPool and Four-Harness Backend Extensions

1. **Extend `wsagent.StartSpec` and `controlsFor` (`agents-plugin-tool/internal/wsagent/backend.go`):**
   - Add `Ephemeral bool` and inline `SystemPrompt string` to `StartSpec`.
   - Relax `MCPSession != nil` guards in `codex.go`, `agy.go`, and `werk_loop.go` when `spec.Ephemeral` is true.
   - Add `conversationResetter` (`Reset(ctx context.Context) error`) to `backend.go` and wire it through `processControls` / `controlsFor`.
   - Relaxing the start guards alone is not enough; the ephemeral path must also make every other `spec.MCPSession` / session-plumbing consumer safe when `Ephemeral` is true: `codexThreadConfig` (`codex.go`) and `agyHTTPServer` (`agy.go`) dereference `spec.MCPSession` unconditionally and must skip it (no nil panic); `validateWerkStart` must not require an MCP session (the pool sets `PermissionReadOnly` and a model on the ephemeral werk spec, since `werk.New` rejects a config with no valid authority and `Authority` has no empty case); the `startChild` / holder / audit path and `spec.Dir` handling must be explicit no-ops under `Ephemeral` (no session dir, no holder entry, no audit row).
2. **Extend all 4 harness backends for ephemeral + reset support:**
   - **`werk` (`internal/werk` + `wsagent/werk_loop.go`):** Skip `OpenStore` and file locks when `Config.Ephemeral` is true (zero disk I/O) by substituting an in-memory store implementation for the same store interface (`turnJournal`, `newWriter(store, ...)` and `Close` all use the store, so a nil store is not acceptable); implement `Agent.Reset()` clearing in-memory `transcript` / `journal`; enforce `cfg.WS == nil` for tool/MCP isolation.
   - **`codex` (`wsagent/codex.go`):** Start with `thread/start` `ephemeral: true`, MCP disabled, the read-only sandbox / non-interactive approval policy via the `StartSpec.PermissionLevel` mapping, and a pool-owned empty scratch cwd (residual built-ins confined per `(1752)`); implement `Reset(ctx)` via fresh warm `thread/start` on the resident `codex` process.
   - **`claude` (`wsagent/claude*.go`):** Launch with `--no-session-persistence`, `--tools ""`, `--strict-mcp-config`, `--disallowedTools Skill` (keeping slash commands enabled so `/clear` works in-stream); implement `Reset(ctx)` via `/clear`.
   - **`agy` (`wsagent/agy.go`):** Launch shared `agy acp` (without `--process-per-session`) using an isolated pool `--gemini-dir`, `mcpServers: []`, and a pool-owned empty scratch cwd (residual built-ins confined per `(1752)`); ACP permission requests in the ephemeral session are answered by the most restrictive existing `AgyPermissionModes` entry or an auto-deny, never left to stall until `Timeout`, and the real-CLI probe asserts this; implement `Reset(ctx)` via `session/new` (closing prior session). "Shared" means one long-lived `agy acp` process reused across sessions (not one process per session); process ownership is per pool slot, so each exact `(harness, model, effort/tier)` key owns its own `agy acp` process, and `Reset` rebinds the single-session `agyMapper` to the new session id. `--process-per-session` is only needed for client-configured HTTP MCP servers, which the ephemeral path drops.
3. **Implement `EphemeralPool`:**
   - Exact `(harness, model, effort/tier)` slot map (default 1 slot per active key, lazy start on demand, idle TTL eviction; defaults when the caller leaves a field zero: `IdleTTL` 5 min, `MaxTurns` 20, `MaxTokens` 0 = no token cap; these are documented package constants, and a consumer such as the companion rotation policy overrides them per call).
   - Concurrency: `Process` is single-goroutine by contract, so each slot serializes its turns. A same-key acquisition made while the slot is busy queues FIFO and waits up to its `Timeout`; it never opens a second process for the same key. `Reset` runs only between turns. On `Timeout` expiry the in-flight turn is `Interrupt`ed, the caller gets a timeout error (and falls back), and the slot is closed and restarted before reuse.
   - Support both `PureOneShot` (reset every call) and `BoundedRotation` (keep warm cache up to `MaxTurns`/`MaxTokens` before reset).
   - Shared `EphemeralRequest` execution with inline `SystemPrompt`, optional `ResponseSchema` JSON validation, per-call `Timeout`, and `ErrUnsupported` -> process-restart fallback.
   - Bounded LRU/rotation, idle-eviction, and daemon-shutdown cleanup of temporary `agy` `--gemini-dir` `.db` / `brain/` artifacts.
4. **Verification:**
   - Unit and fake-backend/process tests covering exact `(harness, model, effort/tier)` keying, zero `Runner`/`wsmeta`/`wslive`/`wsholder` side effects, `PureOneShot` vs `BoundedRotation`, `ErrUnsupported` process-restart fallback, idle TTL eviction, timeout fallback, `werk` zero-disk `OpenStore` skip + `Agent.Reset()`, per-slot serialization / same-key queueing, and `agy` isolated `--gemini-dir` bounded cleanup.
   - Env-gated real-CLI isolation probe test per harness (skipped by default; run on a machine with the CLI installed): after N pool calls plus a `Reset`, assert that no session / rollout / transcript files appear in the harness-native session location (`claude` no `.jsonl`; `codex` no rollout `.jsonl`; `agy` only inside the isolated `--gemini-dir`, then removed by pool cleanup), and that no tool or MCP call is reachable beyond the documented residual set. This is the check that confirms the empirical Windows probe claims the fake-backend tests cannot.

### Phase 2: Companion Reaction and agent.spawn Dynamic Tier Router Integration

*Depends on Phase 1 landing (`EphemeralPool` and all 4 harness extensions).*

1. **Companion live reaction consumer:**
   - Replace cold-spawn `wsagent.RunOneShot` in `wsdashboard/companion_director.go` with `EphemeralPool` in `BoundedRotation` mode across the configured/resolved harness, validating zero per-tick session/disk pollution.
2. **`agent.spawn` dynamic tier router (opt-in, default off):**
   - Add `agent.tier_routing_dynamic` (bool, default off) and `agent.tier_routing_dynamic_classifier` (`(harness, tier)` resolved via the static resolver) per `(1753)` / `(1754)`. The classifier prompt carries the spawn prompt/role and the caller's allowed tier targets; the `ResponseSchema` is `{"tier": "<one of the allowed targets>"}`, and an out-of-set value falls back to static resolution.
   - `mcp.Server` receives the pool by injection at daemon wiring (`mcp` already imports `wsagent`); the two `agent.tier_routing_dynamic*` keys are registered and surfaced following the existing `agent.tier_routing_*` pattern (including the Tier Routing Settings page split out in `f4bef1cb`).
   - Integrate `EphemeralPool` (`PureOneShot` mode, strict `Timeout`, JSON `ResponseSchema`) into `agent.spawn` tier routing (gated by `agent.tier_routing_dynamic`) so lightweight prompt classification can select an appropriate tier dynamically while falling back transparently (`T0`) to existing static tier resolution (`mcp/tier_routing.go`) on timeout, failure, or opt-out.
   - Wiring: replace the stateless `DaemonOptions.CompanionOneShot` func seam (`cmd/ws-mcp/dashboard.go`) with a pool-backed injected interface that can express `BoundedRotation` (wsdashboard cannot import wsagent, per 8cd6f4a5). The pool obtains each harness `Backend` through the same builtin registration and `harnessCommand` resolution as `RunOneShot` (so `Command` overrides, werk credentials and the Windows cmd-shim unwrap behave the same). The director keeps its rule that a tier resolving to a different harness is dropped. Ephemeral workers have no ws tools; the director's JSON reply parsing (`parseCompanionReply`) stays the contract. How the companion ticket's Tier 1 agent maps its `ws/companion.*` tool phrasing onto this JSON reply is reconciled in that ticket's Phase 1, not here.
3. **Verification:**
   - Integration tests for companion bounded-rotation reactions, `agent.spawn` dynamic tier selection + timeout/error -> static fallback parity, and Windows file-touch / latency sanity checks (`ai-docs/manuals/performance.md`).
   - Measurable pass conditions: (a) after 20 consecutive companion reactions through the pool, zero new `wsmeta` rows, zero new `SessionDir` / `wslive` / `wsholder` entries, and no new files in the harness-native session location (for `agy`, only inside the pool's isolated `--gemini-dir`); (b) a warm pool turn is measurably faster than a cold `RunOneShot` call on the same harness measured in the same test (relative comparison, no absolute threshold, since probe timings are machine-specific); (c) forced timeout, error, and invalid-JSON router calls each yield the exact static tier resolution a non-routed call would (parity assertion).
