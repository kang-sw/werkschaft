---
title: "ws 관리 컨텍스트 압축 (하네스 독립 wscompact + 외부 하네스 세션 로테이션 + 조건부 tools/list 스키마)"
parent: 261114-epic-ws-common-session-foundation
related:
  - 261117-feat-ws-hosted-compaction-lifecycle
  - 261119-feat-ws-new-session-compaction-segment-chain
  - 261117-feat-ws-harness-capability-catalog
  - 261122-feat-ws-builtin-agent-harness
---

# ws-Managed Compaction (Harness-Agnostic wscompact, External-Harness Session Rotation, and Conditional tools/list Schema)

## Background

Today, hosted leads split compaction across two incompatible worlds:

1. **Builtin `werk` leads** perform ws-managed self-compaction: the lead passes 7 structured carry-forward prose fields plus optional `reread` paths to `ws/compact(lead_summary)` (`agents-plugin-tool/internal/wscompact/summary.go:10-83`), and `agents-plugin-tool/internal/werk/compaction.go:62-126` plus `agents-plugin-tool/internal/werk/lead_compaction.go:10-72` assemble a deterministic markdown checkpoint (carry-forward prose, bounded `reread` file contents up to 40 KiB, recent `## Dialog` tail up to 40 KiB, active ticket/playbook references, and live `CompactionState` children/exec-job snapshot), commit an explicit `wsaudit.KindCompaction` checkpoint (`agents-plugin-tool/internal/wsagent/werk_loop.go:244-261`), and reset the transcript to `[checkpoint]` in place.
2. **External hosted leads (`claude`, `codex`, `agy`)** are currently blocked from using `lead_summary` by the `leadSummaryCompactor` type gate in `agents-plugin-tool/internal/wsagent/compaction.go:117-123` (`"lead_summary is only available for a builtin lead compacting itself"`). They must rely on each harness's opaque native `/compact` command (`wsagent/compaction.go:455-462`), which cannot preserve ws-structured carry-forward state or `reread` files, behaves inconsistently across harnesses, and on `agy` emits no explicit compaction event (forcing estimated `usage_drop` inference in `agents-plugin-tool/internal/wsagent/audit_agy.go:121-127,258-300`).
3. Meanwhile, `agents-plugin-tool/internal/mcp/compaction.go:59-67` unconditionally attaches `wscompact.LeadSummarySchema()` to the `compact` tool definition in `tools/list` for every session—costing ~669 bytes of extra schema tokens even on sessions using native compaction—while the shared checkpoint assembly logic remains trapped inside `internal/werk/`.

This ticket lifts the checkpoint assembly engine out of `internal/werk/` into a harness-agnostic `internal/wscompact/` package, extends hosted-lead ws-managed compaction to `claude`, `codex`, and `agy` via native session rotation (preserving ws session identity, ring history, audit stream, and children), adds a two-stage fallback for triggers without `lead_summary`, and gates `tools/list` exposure of `lead_summary` per session under a single T0-compatible server acceptance contract.

## Decisions

### 1. Single Server Acceptance Contract + Conditional `tools/list` Schema View (Owner-Confirmed `(1537)=a`)

- **Server acceptance contract (always permissive, never knob-gated):** `handleCompact` (`agents-plugin-tool/internal/mcp/compaction.go:73-113`) and `precheckToolArguments` (`agents-plugin-tool/internal/mcp/tool_arguments.go:55-65,84-155`) ALWAYS keep the canonical `compact` argument schema (including `lead_summary`) so the server unconditionally accepts both:
  - No-arg / summary-less calls (`{session_key, ...}` without `lead_summary`), and
  - Complete 7-field `wscompact.LeadSummary` (+ optional `reread`, `<= 12` paths) validated by `wscompact.ParseLeadSummary` (`agents-plugin-tool/internal/wscompact/summary.go:54-83`), rejecting partial structs or unknown fields (`additionalProperties: false`).
  - Note: because `precheckToolArguments` looks up schemas from the static `tools()` table (`mcp/tool_arguments.go:55-65`) rather than the per-session `filteredTools()` output (`mcp/server_inventory.go:74-118`), projecting `lead_summary` out only in `filteredTools()` preserves the single server acceptance contract cleanly without breaking precheck.
- **Conditional `tools/list` projection per session:** `Server.filteredTools()` (`agents-plugin-tool/internal/mcp/server_inventory.go:74-118`, served by `mcp/server.go:421-430`) projects the `compact` tool schema per session based on the resolved `lead-tune` compaction mode knob for that session's harness:
  - When ws-managed compaction is **off** (`native` mode on external harnesses): strip `lead_summary` from the advertised `compact` `inputSchema` (lightweight no-arg view: `656 B` on `ws` / `660 B` on `wsflow` in Go `encoding/json`, saving `669 B`).
  - When ws-managed compaction is **on** (`ws` mode on external harnesses, or `werk`): advertise the full structured 7-field `lead_summary` + `reread` schema (`1325 B` on `ws` / `1329 B` on `wsflow`, well within `toolSchemaByteCap = 1450 B`). Update `wscompact.LeadSummarySchema()` description (`wscompact/summary.go:35-51`) from `"Builtin lead self-compaction prose..."` to harness-agnostic wording while keeping total serialized size well below `1450 B`.
- **Mid-session knob toggle safety:** Toggling the compaction mode knob mid-session never breaks an in-flight session because the server always accepts both no-arg and `lead_summary` calls; the updated `tools/list` schema view takes effect on the next session start or MCP reconnect. No `/mcp` `ProxyHello` wire change is required because `mcp.Server` already has `ServerOptions.Harness`, `ServerOptions.Conversation`, and `s.configResolver()` (`mcp/process_env.go:111-115`).

### 2. Two-Stage Fallback on External Harnesses Triggered Without `lead_summary` (Owner-Confirmed `(1538)=a`)

When a hosted external-harness lead (`claude`, `codex`, `agy`) has ws-managed compaction enabled and compaction is triggered **without** a `lead_summary`—namely:
- Automatic hard-threshold crossing (`stageHardCompaction` / `steerCompaction`, `wsagent/compaction.go:546-630`), or
- Dashboard `/compact` command (`wsdashboard/inbox_routes.go:489` -> `wsagent/runner_conv.go:1990` / `compactIdle`), or
- A no-arg `ws/compact` MCP call without `lead_summary` on a ws-managed external lead—

the runner executes a **two-stage fallback**:

1. **Stage 1 (1-shot `lead_summary` steer):** Reuse the existing hard compaction advisory steer/nudge mechanism (`wsagent/compaction.go:589-630,683-688`, updating the gate in `stageCompactionNudge` from `durableProcess(rec.Backend)` to effective ws-managed mode) to request a single bounded turn instructing the lead to save durable state to its agenda and call `ws/compact` with `lead_summary` (all 7 fields + optional `reread`).
   - If the lead calls `ws/compact(lead_summary)` during that turn, the idle boundary executes the ws-managed checkpoint + native session rotation (Section 4) and clears the fallback state.
2. **Stage 2 (native `/compact` fallback):** If the Stage 1 turn finishes without queuing `ws/compact(lead_summary)` or errors/aborts, fall back to the harness's native `/compact` path (`c.runCommand(wsinbox.Command{Name: "compact"})`, `wsagent/compaction.go:455-462`) so context pressure is always relieved rather than looping or stranding the session.

### 3. Phase 1 Scope and Opt-In Default (Owner-Confirmed `(1539)=a`)

- **In scope (Phase 1):** All hosted leads across `claude`, `codex`, `agy`, and `werk` unified under one ws-managed compaction engine, controlled by a harness-applicable core config knob under `compaction.*` (pattern-consistent with `compaction.lead_advisory` / `compaction.lead_hard` in `wsconfig/compaction.go:11-15` and `mcp/compaction.go:14-42`, e.g., `compaction.lead_mode` with values `native` | `ws`, `HarnessApplicable: true`, wired into `notifyCompactionTune` / `compactionConfigGen`).
- **Default posture (opt-in for dogfooding):** Defaults to `native` on external harnesses (`claude`, `codex`, `agy`) and `ws` on `werk` (where `werk` always uses ws-managed compaction).
- **Explicitly deferred out of Phase 1:**
  - Worker `lead_summary` forwarding across the child token-forward path (`agents-plugin-tool/internal/wsagent/children.go:298-308` currently forwards only `Target` and `Resume` and calls `r.Compact(...)`); workers stay on their existing compaction path in Phase 1.
  - Flipping the external-harness default from `native` to `ws` (follows after dogfooding validates real-world rotation quality across `claude`/`codex`/`agy`).

### 4. Context Reset Primitive: Native Session Rotation (Announced Default)

Instead of sending harness-specific `/clear` commands, ws-managed compaction on external hosted leads resets model context at the idle boundary via **native session rotation**:

1. **Quiescent checkpoint assembly & commit:** At the hosted idle boundary (`compactIdle`, `wsagent/compaction.go:417-538`), assemble the markdown checkpoint via the shared `internal/wscompact/` engine (carry-forward prose + bounded `reread` files from `wsdashboard.ReadWorktreeTextFiles` + recent `## Dialog` tail from the RAM-pinned `wslive` ring + active ticket/playbook refs + `r.compactionState(rec.ReplyID)` children/exec-job snapshot).
2. **Explicit audit checkpoint:** Record an explicit, non-estimated `wsaudit.KindCompaction` checkpoint via `auditTap.commitCheckpoint` (`wsagent/audit_capture.go:289-327`)—matching `werk_loop.go:244-261`—so all harnesses including `agy` emit explicit checkpoints instead of falling back to `audit_agy.go:258-300` estimated `usage_drop` inference.
3. **Rotate native session ID while preserving ws identity:**
   - Stop/retire the current harness process/holder cleanly (`retireHolder`, `wsagent/runner_lead_start.go:323-344`).
   - Clear only the harness-native session binding (`rec.SessionID = ""`, plus any harness-specific native session handle such as `rec.SessionFile` if applicable) while preserving all ws-owned session identity and history: `rec.ReplyID`, `rec.WSKey`, `ring/` (`wslive`), `wsaudit` segment log, children/mail bindings, worktree/cwd, and permissions.
   - Start a fresh native session via `startLead` (`wsagent/runner_lead_start.go:21-318`):
     - **`claude`:** omits `--resume` when `spec.SessionID == ""` (`wsagent/claude.go:184-186`).
     - **`codex`:** sends `thread/start` instead of `thread/resume` when `spec.SessionID == ""` (`wsagent/codex.go:255-264`).
     - **`agy`:** sends `session/new` instead of `session/resume` when `spec.SessionID == ""` (`wsagent/agy.go:451-458`).
     - **`werk`:** unchanged in-place transcript replacement `a.transcript = []json.RawMessage{userItem(checkpoint.Summary)}` (`werk/compaction.go:119-124`).
4. **Restore delivery & continuation:** Deliver the assembled checkpoint + `RestoreCompaction` session-state bundle into the fresh session's first turn (reusing `queueRestore` / `renderPendingNote`, `wsagent/compaction.go:882-933`, and `compactEvent` lifecycle accounting `CompactStart`/`CompactEnd`/`CompactFailed`, `cmpCount`, `cmpLast`, `wsagent/compaction.go:774-834`), starting one continuation turn when `resume` is true (default true for self-compaction).
5. **Failure safety:** If checkpoint assembly or rotation fails before the fresh session is established, emit `CompactFailed` without incrementing `cmpCount`, preserve the prior `rec.SessionID` (or leave recovery clean), and surface the error rather than silently dropping session state.

### 5. Shared Harness-Agnostic `internal/wscompact/` Engine

Lift the shared checkpoint assembly primitives out of `internal/werk/` into `internal/wscompact/` so `werk` and `wsagent` call one implementation with zero duplicated formatting logic:

- Move from `agents-plugin-tool/internal/werk/compaction.go:18-26,186-343` and `agents-plugin-tool/internal/werk/lead_compaction.go:10-72` into `agents-plugin-tool/internal/wscompact/`:
  - `CompactionState` (`ChildrenInFlight`, `ChildrenFinished`, `ExecJobs`) — unifying the duplicate struct currently defined in both `werk/compaction.go:21-25` and `wsagent/compaction_snapshot.go:11-15`.
  - `reread` smallest-first bin-packing under `RereadBytes = 40 << 10`, `<file path=...>` inlining with `</file>` escaping, and deferred file listing (`werk/lead_compaction.go:10-72`).
  - Ticket/playbook reference extraction (`ticketRefRE`, `skillRE`), bounded `## Dialog` tail assembly (`DialogBytes = 40 << 10`), section writer (`writeCompactSection`), and deterministic markdown checkpoint renderer (`summary` / `BuildCheckpoint`).
- Provide two lightweight dialog-source adapters feeding the shared renderer:
  - `werk` continues feeding from its transcript segment replay (`werk/compaction.go:62-97`).
  - `wsagent` (for external harnesses) feeds from the RAM-pinned `wslive` ring (`wslive.ReadRing` / `wslive.ReadRingPage`, `internal/wslive/ring.go:165-197`, mapping `RingUser`, `RingReply`, `RingTools`, `RingMail`, `RingCompact` boundaries since the last compaction).

### 6. Compatibility Tier: **T0 Transparent**

Per `ai-docs/manuals/release-compatibility.md`, every cross-version surface touched by this change is **T0 transparent** (no minor bump, no T3 boundary, no `/mcp` hello change, no on-disk schema break):

| Surface | New writer / server -> Old reader / client | Old writer / server -> New reader / client | Source citations |
|---|---|---|---|
| `ws/compact` MCP tool call & precheck | New server keeps `lead_summary` in canonical `compactTool()` (`mcp/compaction.go:59-67`) and `precheckToolArguments` (`mcp/tool_arguments.go:55-65,84-155`); old callers sending `{session_key}` without `lead_summary` continue to pass validation and dispatch identically. | Old server accepts `{session_key}` without `lead_summary` (`mcp/compaction.go:73-113`); external-harness callers on an old server only see/use no-arg compact (or receive existing `leadSummaryCompactor` error if they explicitly pass `lead_summary`). | `mcp/compaction.go:59-113`, `mcp/tool_arguments.go:55-65,84-155`, `wsagent/compaction.go:117-123` |
| Conditional `tools/list` schema projection | Per-session `filteredTools()` (`mcp/server_inventory.go:74-118`) only omits/includes the optional `lead_summary` property on `compact`; JSON-RPC `tools/list` shape is unchanged and both variants stay under `toolSchemaByteCap = 1450 B`. | Old server always emits `compact` with `lead_summary`; new clients/harnesses accept standard `tools/list` definitions unchanged. | `mcp/server.go:421-430`, `mcp/server_inventory.go:74-118`, `mcp/tool_schema_size_test.go:18` |
| New `compaction.*` config key in `Config.Overrides` | Stored as a plain string entry in `Config.Overrides map[string]string` (`wsconfig/config.go:40`). Older binaries unmarshal `Config.Overrides` cleanly (`wsconfig/config.go:112-125`), ignore unknown override keys during resolution, and preserve unknown members via `preserveConfigMembers` (`wsconfig/preserve.go:14-46`) when editing other keys. | Absent key in configs written by older binaries resolves cleanly to the built-in defaults (`native` on external harnesses, `ws` on `werk`). | `wsconfig/config.go:34-48,112-125`, `wsconfig/preserve.go:14-46`, `wsconfig/compaction.go:11-15,66-109` |
| `/mcp` `ProxyHello` | Unchanged — no new hello field is introduced; `mcp.Server` resolves the knob via existing `ServerOptions.Harness`, `ServerOptions.Conversation`, and `s.configResolver()`. | Unchanged. | `mcp/process_env.go:111-115`, `wsagent/mcp_session.go:438-465` |
| `agent.json` (`Record`), `ring/` (`wslive`), `wsaudit` | Rotation mutates only existing `Record.SessionID` (already cleared/rotated across restarts). Explicit `wsaudit.KindCompaction` checkpoints use the existing `commitCheckpoint` envelope already written by `werk` (`werk_loop.go:244-261`, `audit_capture.go:289-327`); old readers read it unchanged without treating it as estimated. | Existing `agent.json`, `ring/`, and `wsaudit` files written by old binaries remain 100% readable by the new binary. | `wsagent/audit_capture.go:269-327`, `wsagent/werk_loop.go:244-261`, `wsagent/runner_lead_start.go:102,305-312` |

## Constraints

- **Required Implementation Convention manuals (read before editing matching paths per `AGENTS.md`):**
  - `ai-docs/manuals/release-compatibility.md` (all `agents-plugin-tool/internal/` changes — T0 verification and bidirectional compatibility tests).
  - `ai-docs/manuals/config-settings-ui.md` (new `compaction.*` key in `internal/mcp/config_registry.go`, `internal/wsconfig/compaction.go`, `internal/wsdashboard/assets/dashboard-settings-catalog.json`, all 11 locale files under `internal/wsi18n/tables/*.json`, and settings drift/i18n tests).
  - `ai-docs/manuals/ws-mcp.md` (`internal/mcp/` tool surface, schema, and dispatch conventions).
  - `ai-docs/manuals/performance.md` (Windows/AV file-touch budget and storage-type discipline).
  - `ai-docs/manuals/shipped-surface-boundary.md` (no repo-specific assumptions in shipped tool descriptions, nudges, or checkpoint templates).
  - `ai-docs/manuals/wsflow-mirroring.md` (mandatory parity check + `generate-package-assets.py` regeneration if any adapter manifest under `agents-plugin/` or `agents-plugin-wsflow/` is touched; note that core `compaction.*` keys live in `config_registry.go` + `dashboard-settings-catalog.json` rather than adapter manifests, while `TestListedToolSchemasFitByteCap` checks both `ws` and `wsflow` surfaces).
- **Tool schema byte cap (`toolSchemaByteCap = 1450 B`):**
  - Verified against `agents-plugin-tool/internal/mcp/tool_schema_size_test.go:18,45-55,72-106` (which marshals `{name, description, inputSchema}` with Go's `encoding/json` HTML escaping where `<` and `>` cost 6 B each):
    - `compact` WITH `lead_summary`: **1325 B** (`ws`) / **1329 B** (`wsflow`) — fits with ~121 B headroom (even with `antigravity/alwaysLoad` `_meta`, `1365 B` / `1369 B`, still under `1450 B`).
    - `compact` WITHOUT `lead_summary`: **656 B** (`ws`) / **660 B** (`wsflow`) — saves **669 B**.
  - Any description edits to `compactTool()` or `wscompact.LeadSummarySchema()` MUST keep both `ws` and `wsflow` variants under `1450 B` and pass `TestListedToolSchemasFitByteCap` for both schema views.
  - Note: `agy` `DirectTools` (`agents-plugin-tool/internal/wsagent/agy.go:69-76`) currently omits `"compact"` (reaches it through `CallFallback: true`); Phase 1 does not need to expand `agy.DirectTools` unless separately decided, while keeping `compact` under `1450 B` guarantees `agy` compatibility either way.
- **Windows / AV file-touch budget (`ai-docs/manuals/performance.md`):**
  - Keep all compaction orchestration, two-stage fallback flags, and config generation tracking in memory on `conv` (`wsagent/runner_conv.go:122-160`).
  - Zero per-turn config disk polling: reload the new `compaction.*` mode only when `wsagent.compactionConfigGen` increments (`mcp/compaction.go:14-42`, `wsagent/compaction.go:193-231`), matching existing `compaction.lead_advisory` / `compaction.lead_hard`.
  - Read `## Dialog` tail from the RAM-pinned `wslive` ring (`wslive/ring.go:165-197`) — zero directory walk / zero per-entry file opens for active hosted leads.
  - Read `reread` worktree files only at the quiescent compaction boundary, strictly bounded by `maxRereads = 12` and `RereadBytes = 40 << 10` (`wscompact/summary.go:12`, `werk/lead_compaction.go:10`).
  - Do not add new persisted JSON files or per-observation disk writes; reuse existing `agent.json` update, `wsaudit` checkpoint append, and `wslive` ring append.
- **Verification environment:** Go build/vet/test suites for `agents-plugin-tool` MUST run on the remote Linux build host (`w5090`), never locally on Windows.

## Phases

### Phase 1: Shared `wscompact` Engine, Hosted-Lead Session Rotation, Two-Stage Fallback, and Conditional `tools/list` Schema

Deliver the complete, opt-in Phase 1 ws-managed compaction stack for hosted leads (`claude`, `codex`, `agy`, `werk`):

1. **Lift shared checkpoint engine into `internal/wscompact/`:**
   - Move `CompactionState`, `reread` bin-packing & `<file>` inlining (`werk/lead_compaction.go:10-72`), ticket/playbook ref extraction, bounded `## Dialog` rendering, and markdown checkpoint assembly (`werk/compaction.go:18-26,186-343`) into `internal/wscompact/`.
   - Refactor `internal/werk/compaction.go` and `internal/wsagent/compaction_snapshot.go` to consume `wscompact` directly with zero behavioral regression on `werk` leads.
   - Add a `wslive` ring-to-dialog adapter so external hosted leads build `## Dialog` (since the last `RingCompact` boundary, bounded to `40 KiB`) from the RAM-pinned ring (`wslive.ReadRing` / `wslive.ReadRingPage`).
   - Update `wscompact.LeadSummarySchema()` description (`wscompact/summary.go:35-51`) to harness-agnostic wording while preserving all 7 required fields, `reread` limits, and the `< 1450 B` `compact` schema budget.

2. **Add harness-applicable lead compaction mode config & UI parity:**
   - Register the new core `compaction.*` lead-mode knob (defaulting to `native` on external harnesses and `ws` on `werk`, `HarnessApplicable: true`, `NoAgentVisible: true`) in `internal/wsconfig/compaction.go` and `internal/mcp/config_registry.go` (`mcp/compaction.go:14-42`), wired to `notifyCompactionTune` so changes bump `compactionConfigGen`.
   - Add the corresponding row in `internal/wsdashboard/assets/dashboard-settings-catalog.json` (under `"section": "compaction"`, `"selectors": ["harness"]`) and translations across all 11 `internal/wsi18n/tables/*.json` locale files per `ai-docs/manuals/config-settings-ui.md`.
   - Ensure `ws` / `wsflow` package parity tests and settings catalog drift tests pass (no adapter manifest edits unless required; if touched, mirror per `wsflow-mirroring.md` and run `generate-package-assets.py`).

3. **Single server acceptance contract + conditional `tools/list` projection:**
   - Keep `lead_summary` in canonical `compactTool()` (`mcp/compaction.go:59-67`) so `precheckToolArguments` (`mcp/tool_arguments.go:55-65,84-155`) and `handleCompact` (`mcp/compaction.go:73-113`) always accept both `{session_key}` and `{session_key, lead_summary}` (and reject partial `lead_summary` structs) regardless of knob state.
   - In `Server.filteredTools()` (`mcp/server_inventory.go:74-118`), clone and strip `lead_summary` from the advertised `compact` schema when the session's resolved compaction mode is `native` (off), and advertise the full `lead_summary` schema when mode is `ws` (on) or `werk`.
   - Lift the `leadSummaryCompactor`-only restriction in `Runner.CompactWithSummary` (`wsagent/compaction.go:117-123`) so hosted external leads (`claude`, `codex`, `agy`) accept `lead_summary` while keeping descendants/workers on the existing path (deferring `children.go:298-308`).

4. **External-harness native session rotation + explicit audit checkpoint:**
   - At `compactIdle` (`wsagent/compaction.go:417-538`), when a hosted external lead executes ws-managed compaction with `lead_summary`:
     - Assemble the checkpoint via `wscompact` (`lead_summary` prose + `wsdashboard.ReadWorktreeTextFiles` rereads + `wslive` ring dialog tail + `r.compactionState(rec.ReplyID)` + active ticket/playbook refs).
     - Emit `CompactStart`, commit an explicit non-estimated `wsaudit.KindCompaction` checkpoint (`audit_capture.go:289-327`), retire the running holder (`retireHolder`), clear `rec.SessionID` (preserving `ReplyID`, `WSKey`, `ring/`, `wsaudit`, children, and permissions), and start a fresh native session via `startLead` (`claude` omits `--resume`, `codex` calls `thread/start`, `agy` calls `session/new`).
     - Queue the checkpoint + `RestoreCompaction` bundle into the fresh session's first turn (`queueRestore` / `renderPendingNote`) and emit `CompactEnd` (continuing automatically when `resume` is true); on failure, emit `CompactFailed` cleanly without corrupting `cmpCount` or session identity.

5. **Two-stage fallback for summary-less triggers on external ws-managed leads:**
   - Update `stageCompactionNudge` (`wsagent/compaction.go:683-688`) to ask for `lead_summary` whenever the session's effective mode is ws-managed (not just `durableProcess`).
   - When hard threshold crossing (`stageHardCompaction` / `steerCompaction`, `wsagent/compaction.go:546-630`), dashboard `/compact` (`wsdashboard/inbox_routes.go:489` -> `runner_conv.go:1990`), or a no-arg `ws/compact` call triggers compaction on a ws-managed external lead without `lead_summary`:
     - Stage 1: inject the 1-shot advisory steer asking the lead to save agenda and call `ws/compact(lead_summary)`.
     - Stage 2: if that turn completes without queuing `ws/compact(lead_summary)` or ends in error/abort, automatically fall back to native `runCommand(wsinbox.Command{Name: "compact"})` (`wsagent/compaction.go:455-462`).

6. **Verification (on remote `w5090`):**
   - Unit tests in `internal/wscompact/`: checkpoint assembly, `reread` 40 KiB bin-packing & `</file>` escaping, `## Dialog` 40 KiB truncation, ticket/playbook extraction, `CompactionState` rendering, and `ParseLeadSummary` partial-struct rejection.
   - Unit & integration tests in `internal/mcp/`: conditional `tools/list` schema exposure (`native` strips `lead_summary`, `ws`/`werk` includes it), single acceptance contract across mid-session knob toggles (both `{}` and complete `lead_summary` accepted; partial rejected), `TestListedToolSchemasFitByteCap` passing on `ws` and `wsflow` for both schema views, and T0 `Config.Overrides` compatibility tests.
   - Unit & fixture tests in `internal/wsagent/`: native session rotation on `claude` (no `--resume`), `codex` (`thread/start`), `agy` (`session/new`), and regression check on `werk` (in-place transcript reset); preservation of `ReplyID`/`WSKey`/`ring/`/children; explicit non-estimated `wsaudit.KindCompaction` checkpoint emission (including `agy`); two-stage fallback (Stage 1 succeeds via `ws/compact(lead_summary)` vs Stage 1 no-op/error falls back to native `/compact` for both hard-threshold and dashboard `/compact` triggers); zero extra per-turn config disk reads.
   - Settings catalog, i18n completeness across all 11 `internal/wsi18n/tables/*.json` files, and `ws`/`wsflow` package drift tests.
