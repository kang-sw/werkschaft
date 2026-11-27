---
title: "공통 하네스 세션 미저장·ACP 워밍 캐시 경량 인스턴스 풀 및 동적 티어 라우팅"
related:
  261127-feat-ws-companion-layered-agents-and-naming: Tier 1 live companion reaction consumer replacing cold-spawn RunOneShot
---

# Common Harness Ephemeral Non-Persistent / Warm-Cached Lightweight Instance Pool & Dynamic Tier Routing

## Background

Lightweight auxiliary LLM calls inside the daemon currently face two problems:

1. **Cold-spawn latency and harness coverage gap:** `companion_director.go` invokes `wsagent.RunOneShot` (`agents-plugin-tool/internal/wsagent/oneshot.go`), which spawns a fresh CLI process per call and currently only provides isolated one-shot support on select harnesses.
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
| `codex` (`0.170.0`) | 0 rollout `.jsonl` files with `thread/start` `ephemeral: true`; only `logs_2.sqlite-wal` mtime touches | Warm `thread/start #2` = `0.354 s` (`initialize` = `1.55 s`) | MCP / shell / patch hideable (some built-ins like `wait` / `exec` remain) |
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
  - **Chosen:** Define both consumers' shared contract (`EphemeralRequest`, JSON response schema, timeout -> static fallback) from the start, executing in two sequential phases: Phase 1 (shared `EphemeralPool` + 4 harness extensions) -> Phase 2 (companion live reaction integration & `agent.spawn` dynamic tier router `auto` / opt-in hook).
  - **Rejected alternatives:** Coupling only companion reactions first and retrofitting `agent.spawn` routing later with an incompatible request/fallback contract.
- **Backlog scheduling — settled `(1751)=a`:**
  - **Chosen:** Capture in `todo/` backlog now; implementation scheduled sequentially after `v0.53.2` ships.
- **Compatibility tier — T0 transparent:**
  - New internal daemon package/pool, internal `wsagent` interface additions, and an opt-in/`auto` routing hook with timeout -> static fallback. No wire protocol, `/mcp` hello, or persistent store format changes; older binaries and legacy static tier callers remain unaffected.

## Constraints

- **Performance & Windows AV (`ai-docs/manuals/performance.md`):**
  - Zero file I/O on the `werk` ephemeral path (`OpenStore` and file locks skipped when `Config.Ephemeral` is true).
  - Keep warm processes resident in memory until idle TTL so interactive reaction/routing calls avoid per-call OS process launches (Rule 6) and per-call disk writes.
  - Bound `agy` isolated `--gemini-dir` growth via whole-session/directory pruning in `EphemeralPool` on LRU rotation, idle eviction, and daemon shutdown rather than per-turn file scanning.
- **Release compatibility (`ai-docs/manuals/release-compatibility.md`):**
  - Preserve existing `wsagent.Backend`, `wsagent.Runner`, `wsagent.RunOneShot`, and static tier resolution behavior (`T0 transparent`). Dynamic tier routing must fall back cleanly to static tier resolution on timeout, error, or opt-out.
- **Shipped surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):**
  - Any user-facing config keys, tool descriptions, or advisories added in Phase 2 must remain downstream-generic with zero devenv-specific citations.
- **Zero-pollution & tool isolation:**
  - Ephemeral launches must never write `wsmeta` rows, `wslive` rings, `wsholder` entries, or `SessionDir` transcripts, and must apply each harness's verified tool/MCP/persistence isolation flags (`--no-session-persistence` / `--tools ""` / `--strict-mcp-config` / `--disallowedTools Skill` on `claude`, `thread/start` `ephemeral: true` on `codex`, `mcpServers: []` + isolated `--gemini-dir` on `agy`, `cfg.WS == nil` + `Config.Ephemeral` on `werk`).

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

## Phases

### Phase 1: Shared EphemeralPool and Four-Harness Backend Extensions

1. **Extend `wsagent.StartSpec` and `controlsFor` (`agents-plugin-tool/internal/wsagent/backend.go`):**
   - Add `Ephemeral bool` and inline `SystemPrompt string` to `StartSpec`.
   - Relax `MCPSession != nil` guards in `codex.go`, `agy.go`, and `werk_loop.go` when `spec.Ephemeral` is true.
   - Add `conversationResetter` (`Reset(ctx context.Context) error`) to `backend.go` and wire it through `processControls` / `controlsFor`.
2. **Extend all 4 harness backends for ephemeral + reset support:**
   - **`werk` (`internal/werk` + `wsagent/werk_loop.go`):** Skip `OpenStore` and file locks when `Config.Ephemeral` is true (zero disk I/O); implement `Agent.Reset()` clearing in-memory `transcript` / `journal`; enforce `cfg.WS == nil` for tool/MCP isolation.
   - **`codex` (`wsagent/codex.go`):** Start with `thread/start` `ephemeral: true` and hidden MCP/shell/patch settings; implement `Reset(ctx)` via fresh warm `thread/start` on the resident `codex` process.
   - **`claude` (`wsagent/claude*.go`):** Launch with `--no-session-persistence`, `--tools ""`, `--strict-mcp-config`, `--disallowedTools Skill` (keeping slash commands enabled so `/clear` works in-stream); implement `Reset(ctx)` via `/clear`.
   - **`agy` (`wsagent/agy.go`):** Launch shared `agy acp` (without `--process-per-session`) using an isolated pool `--gemini-dir` and `mcpServers: []`; implement `Reset(ctx)` via `session/new` (closing prior session).
3. **Implement `EphemeralPool`:**
   - Exact `(harness, model, effort/tier)` slot map (default 1 slot per active key, lazy start on demand, 5–10 min idle TTL eviction).
   - Support both `PureOneShot` (reset every call) and `BoundedRotation` (keep warm cache up to `MaxTurns`/`MaxTokens` before reset).
   - Shared `EphemeralRequest` execution with inline `SystemPrompt`, optional `ResponseSchema` JSON validation, per-call `Timeout`, and `ErrUnsupported` -> process-restart fallback.
   - Bounded LRU/rotation, idle-eviction, and daemon-shutdown cleanup of temporary `agy` `--gemini-dir` `.db` / `brain/` artifacts.
4. **Verification:**
   - Unit and fake-backend/process tests covering exact `(harness, model, effort/tier)` keying, zero `Runner`/`wsmeta`/`wslive`/`wsholder` side effects, `PureOneShot` vs `BoundedRotation`, `ErrUnsupported` process-restart fallback, idle TTL eviction, timeout fallback, `werk` zero-disk `OpenStore` skip + `Agent.Reset()`, and `agy` isolated `--gemini-dir` bounded cleanup.

### Phase 2: Companion Reaction and agent.spawn Dynamic Tier Router Integration

*Depends on Phase 1 landing (`EphemeralPool` and all 4 harness extensions).*

1. **Companion live reaction consumer:**
   - Replace cold-spawn `wsagent.RunOneShot` in `wsdashboard/companion_director.go` with `EphemeralPool` in `BoundedRotation` mode across the configured/resolved harness, validating zero per-tick session/disk pollution.
2. **`agent.spawn` dynamic tier router (`auto` / opt-in hook):**
   - Integrate `EphemeralPool` (`PureOneShot` mode, strict `Timeout`, JSON `ResponseSchema`) into `agent.spawn` tier routing (`auto` / opt-in config hook) so lightweight prompt classification can select an appropriate tier dynamically while falling back transparently (`T0`) to existing static tier resolution (`mcp/tier_routing.go`) on timeout, failure, or opt-out.
3. **Verification:**
   - Integration tests for companion bounded-rotation reactions, `agent.spawn` dynamic tier selection + timeout/error -> static fallback parity, and Windows file-touch / latency sanity checks (`ai-docs/manuals/performance.md`).
