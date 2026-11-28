---
title: "컴패니언 인터랙티브 가이드 — ws/daemon.highlight 스포트라이트 + ws/daemon.monitor 구조적 UI 이벤트 모니터링"
related:
  261128-feat-ws-dashboard-control-mcp-api: prerequisite (done) — ws/daemon.{curl,tabs,dom,ui,screenshot}, tab registry, bridge ops, 1-click banner this ticket extends
  261127-feat-ws-companion-layered-agents-and-naming: prerequisite (done) — companion-body / companion-work dual-brain markers and gates this ticket splits
  261122-epic-werkschaft-lead-companion: umbrella companion epic (context only)
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: e9bdcd6e86f9a1a7
sage-review-completeness-reviewed: e9bdcd6e86f9a1a7
completed: 2026-11-28
---

# Companion Interactive Guide — `ws/daemon.highlight` + `ws/daemon.monitor`

## Background

The dashboard-control surface (`ws/daemon.tabs`, `ws/daemon.dom`,
`ws/daemon.ui`, `ws/daemon.curl`, `ws/daemon.screenshot`) lets an opted-in
lead or the Tier-2 `companion-work` brain inspect and drive the local
dashboard. Three gaps block in-character tutorial guidance:

1. **No way to point at something.** An agent can find a control with
   `ws/daemon.dom` but cannot visually show it to the user; `ws/daemon.ui`
   only navigates or mutates.
2. **No way to follow along.** An agent cannot learn that the user switched a
   tab, opened settings, or clicked the thing it pointed at, short of polling
   `ws/daemon.dom` (token-expensive and laggy).
3. **The Tier-1 `companion-body` is fully blocked** from every `ws/daemon.*`
   tool (`agents-plugin-tool/internal/mcp/daemon_tools.go:175-177`,
   `hosted_render.go:84`), so any guidance needs a `companion.delegate_work`
   round-trip to the hidden work brain — too slow for a live tutorial.

This ticket adds a non-destructive visual overlay tool, a bounded structural
UI-event monitor that wakes the owning agent with compact turns, and a
dual-brain authority split that gives `companion-body` the non-destructive
guide toolset while keeping every mutating control behind `companion-work` and
the 1-click banner.

## Decisions

- **(1773)=a — Dual-brain authority split.**
  - `companion-body` gets direct access to the **non-destructive guide
    toolset**: `ws/daemon.tabs`, `ws/daemon.dom`, `ws/daemon.highlight`,
    `ws/daemon.monitor`. No `delegate_work` round-trip is needed for guidance.
  - `ws/daemon.curl`, `ws/daemon.ui` (and `ws/daemon.screenshot`, which is not
    in the guide set) stay denied for `companion-body`. `curl`/`ui` remain
    `companion-work` (Tier-2) or opted-in-lead tools, and their mutating paths
    keep the in-handler 1-click banner unchanged (`daemon_dom_ui.go:648-666`,
    `:711-730`; `daemon_curl.go:276`). Zero relaxation of mutation safety.
  - Opted-in dashboard-control leads (`dashboard_control_access`) and
    `companion-work` also get `ws/daemon.highlight` and `ws/daemon.monitor`
    (they already pass the `daemon.` prefix gate, `daemon_tools.go:34-36`).
  - Rejected: routing body guidance through `companion.delegate_work` (latency
    of a second model turn per pointer); granting body the whole
    `ws/daemon.*` set (would expose `curl`/`ui` mutation without the Tier-2
    approval posture).
  - **Supersedes** `261128-feat-ws-dashboard-control-mcp-api` caller scope
    ("Worker subagents, Tier-1 `companion-body`, and remote/untrusted viewers
    are strictly blocked") **for the four guide tools only**, by owner
    decision `(1773)=a`. Workers and remote viewers stay blocked, and body
    stays blocked for `curl`/`ui`/`screenshot`.
- **(1774)=a — Standalone T0-transparent ticket.** Additive MCP tools, an
  additive bridge op, additive WebSocket push/frame event names, additive
  optional host interface. No existing tool, argument, schema, store shape, or
  event changes meaning (grading in `## Constraints`).
- **Highlight is non-destructive → no 1-click banner.** It only paints an
  overlay in the local shell; it never clicks, fills, or navigates.
  `companionWorkApprovalGate` already returns early for every `daemon.*` tool
  (`server_inventory.go:248-250`), so no new gate code is needed.
- **Monitor events are structural only.** A strict 6-name allowlist; never
  keystrokes, chat drafts, form values, or any element text.
- **Monitor is memory-only, one per owning session.** No persisted state; the
  owner is the calling hosted conversation's reply id. `start` on an active
  monitor replaces it (new `monitor_id`, fresh TTL and counters, no final
  notice for the replaced one).
- **Turn delivery reuses the system-mail wake path.** The daemon appends one
  system envelope to the owner's mailbox queue and wakes the mail waiters
  (same pattern as `config_proposals.go:109-115` and `session_hire.go:401-417`);
  wsagent's mail watcher claims it and submits it as a turn or steers it into
  a running turn (`wsagent/runner_conv.go:584-639`). Rejected: a new
  wsagent-specific delivery API (more surface, no gain).
- **`route_changed` is derived server-side** from the existing tab heartbeat
  (`daemon_bridge.go:311-328` already detects route / reply-id changes), so
  it works with Phase 1 alone and the Phase 2 client never emits
  `route_changed` itself (no double counting).
- **Default tool output is compact text; `format: "json"` returns the
  structured object** (`ai-docs/manuals/ws-mcp.md` `## Tool Output
  Convention`). The older daemon tools keep their JSON default unchanged.
- **Companion gaze hook is a DOM event, not a stage edit.** Phase 2 dispatches
  `ws:guide-highlight-change` on the shell `document`; the companion
  stage/VRM files are owned by a parallel track and are not edited here.

## Constraints

- **Compatibility tier: T0** (`ai-docs/manuals/release-compatibility.md`
  `### Tools and Protocol Mapping`). Directional evidence:
  - New tools: only the serving daemon lists and dispatches them; an older
    daemon never lists them, and the unknown-tool refusal
    (`server_dispatch.go:1752-1753`) writes nothing.
  - New bridge op `guide.highlight` sent to a cached pre-Phase-2 shell: the
    shell replies `invalid_request` for unknown ops
    (`assets/dashboard-daemon-bridge.js:858`). The server maps that to
    `guide_unavailable` (reason `client_unsupported`). No write.
  - New push event `daemon-guide-monitor` sent to an older client: the stream
    dispatcher only calls named handlers (`assets/dashboard-stream.js:290`),
    so it is ignored.
  - New client frames (`guide-monitor-event`, `guide-monitor-stop`) sent to
    an older daemon: `handleFrame` drops every event other than
    `tab-heartbeat` / `visibility` (`daemon_bridge.go:259-263`).
  - Mailbox: an ordinary `Envelope{Label, Content, SentAt}` system append
    (`wsmailbox/memstore.go:349-358`); no store shape change.
  - New `DaemonGuideHost` interface is optional (type assertion), so
    `DaemonControlHost` (`daemon_tools.go:25-32`) and its implementers and
    fakes are unchanged.
  - Permission widening for `companion-body` is additive. Refusing `curl`/`ui`
    for body is today's behavior.
- **Performance** (`ai-docs/manuals/performance.md`): the monitor registry is
  memory-only (Rule 1); ingestion does no file I/O; the only file touch is one
  mailbox append per delivered turn, bounded by the 12/minute cap (Rule 2/5).
  The Phase 2 overlay keeps idle screens frame-free: no continuous rAF loop,
  rect updates only on scroll/resize/mutation events coalesced per frame,
  motion only via `transform`/`opacity`, paused while hidden
  (`## Frontend Minimal Updates`).
- **Shipped surface** (`ai-docs/manuals/shipped-surface-boundary.md`): tool
  descriptions, prompt lines, and delivered turn text name no ticket,
  commit, or repository path.
- **MCP conventions** (`ai-docs/manuals/ws-mcp.md`): tool output convention
  above; new tools registered through `daemonControlTools()` (appended at
  `server_schemas.go:1315`) and dispatched at `server_dispatch.go:1729-1730`.
- **Dashboard style** (`ai-docs/manuals/dashboard-style.md`): the Phase 2
  card and stop chip use the canonical tonal tokens, borderless surfaces, and
  compact pill pattern (`### Compact Controls & Pills`).
- **wsflow mirroring** (`ai-docs/manuals/wsflow-mirroring.md`): both
  `agents-plugin/runtime.json` and `agents-plugin-wsflow/runtime.json` gain the
  two tool entries, then
  `python3 agents-plugin-tool/scripts/generate-package-assets.py` regenerates
  the embedded copies and `--check` passes.
- **File-size discipline:** every new Go/JS/CSS file stays under 1,000 lines.
  New Go logic goes in dedicated files (`internal/mcp/daemon_guide.go`,
  `internal/wsdashboard/daemon_guide.go`, `daemon_guide_monitor.go`), not in
  `wsagent/runner_conv.go`. Edits to existing files are small hooks.
- **Parallel-track boundary:** do not edit `dashboard-companion-vrm*.js`,
  `dashboard-companion-stage.js`, or `dashboard-companion.css`.

## Prior Art

- Bridge request/reply with nonce correlation and per-tab push:
  `SendDaemonBridgeRequest` (`daemon_bridge.go:540-627`),
  `handleDaemonBridgeReply` (`:641-727`).
- Ref targeting and staleness: `lookupRef` (`daemon_dom_ui.go:211-257`),
  generation/ref pattern `g<gen>-r<n>` (`:126`), exclusion of
  `data-ws-dom-exclude` / confirm-banner nodes (`:259-270`), secret redaction
  `redactDaemonSecrets` (used at `:303-367`).
- Shell/frame bridge op routing: `handleDaemonBridgeRequest`
  (`assets/dashboard-daemon-bridge.js:569-859`), frame-side
  `handleDaemonFrameBridgeReq` (`:397-462`), `dcCallFrame` postMessage RPC
  (`:477-509`), exclusion check `dcIsExcluded` (`:33`).
- Tab heartbeat frame and push channel over `/rail/events`:
  `sendDaemonHeartbeat` (`assets/dashboard-stream.js:520-538`), the stream event
  map (`:588`), server stream wiring (`sse.go:576-582`, `:776-796`).
- Companion gates: `companionBodyBaseAllows` (`server_inventory.go:49-66`),
  `companionBodyPhonePostureAllows` (`:75-85`), `companionMarkerAllowsTool`
  (`:92-112`), `sessionToolAccess` daemon branch (`:436-443`),
  `daemonControlListed` / `daemonControlCallRefusal`
  (`daemon_tools.go:144-188`), agy direct-tool check
  (`server_exec_cutover.go:40`), hosted render grant (`hosted_render.go:81-87`).
- Existing tests to extend: `internal/mcp/daemon_tools_test.go`
  (`TestDaemonControlBlocksWorkersCompanionBodyAndNonHosted` :232,
  `TestDaemonControlCompanionWorkAutoGrantAndIsolation` :462 — its body
  manual assertion at :615-627 changes), `internal/wsdashboard/daemon_bridge_test.go`,
  `daemon_control_browser_test.go` (Playwright pattern gated on
  `WS_PLAYWRIGHT_MODULE`).

## Prior Decisions

- 261128-feat-ws-dashboard-control-mcp-api (2026-11-28, Decisions "Caller scope"): "Worker subagents, Tier-1 `companion-body`, and remote/untrusted viewers are strictly blocked." — bearing: superseded for the four non-destructive guide tools only, by owner `(1773)=a` (see `## Decisions`). Still constrains `curl`/`ui`/`screenshot`, workers, and remote viewers.
- 261128-feat-ws-dashboard-control-mcp-api (2026-11-28, Decisions): `POST /api/daemon/bridge-reply` enforces `TrustedWrite` plus single-use `(request_id, nonce)` — bearing: constrains (the `guide.highlight` reply rides the same endpoint unchanged; monitor frames ride the already-local-viewer-only `/rail/events` bridge stream, `sse.go:577`).
- 261127-feat-ws-companion-layered-agents-and-naming (2026-11-27, Decisions, Rejected): "letting Tier-1 directly execute daemon/UI mutations without delegating to Tier-2" — bearing: constrains (highlight/monitor are non-mutating; `curl`/`ui` stay Tier-2-only).
- 261127-feat-ws-companion-layered-agents-and-naming (2026-11-27, `(1750)`/`(1742)`): companion-work dispatch gate with mutating `ws/daemon.curl`/`ws/daemon.ui` classified behind the 1-click banner — bearing: constrains (unchanged; highlight/monitor add no gate because they are non-mutating, and `companionWorkApprovalGate` already defers every `daemon.*` tool to its handler, `server_inventory.go:248-250`).
- 261127-feat-ws-companion-layered-agents-and-naming (2026-11-27, `(1740)`, D8): phone-posture strict allowlist; work tools are posture-locked while the body holds the phone — bearing: constrains (guide tools are posture-locked at phone posture).

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phase 1: mcp/daemon_tools.go, mcp/daemon_guide.go (new), mcp/server_inventory.go, mcp/server_dispatch.go, mcp/server_exec_cutover.go, mcp/hosted_render.go, wsdashboard/daemon_guide.go + daemon_guide_monitor.go (new), wsdashboard/daemon_bridge.go, daemon_runtime.go, companion_soul_prompt.go, cmd/ws-mcp/companion_runtime.go, both runtime.json. Phase 2: assets/dashboard-daemon-guide.{js,css} (new), dashboard-daemon-bridge.js, dashboard-stream.js, shell page asset list, 11 wsi18n tables |
| scope.surface | cross-module | MCP tool surface + daemon bridge + dashboard shell assets + companion prompts |
| scope.new_public_symbol | yes | wsdashboard: DaemonHighlightRequest/Response, DaemonMonitorRequest/Status, (*Daemon).DaemonHighlight/DaemonMonitor; mcp: DaemonGuideHost |
| scope.new_type_contract | yes | two MCP tool schemas, bridge op `guide.highlight`, push event `daemon-guide-monitor`, frames `guide-monitor-event`/`guide-monitor-stop`, delivered turn payload, DOM event `ws:guide-highlight-change` |
| scope.test_surface | new-files | extends mcp/daemon_tools_test.go plus new mcp/daemon_guide_test.go, new wsdashboard/daemon_guide_test.go, Phase 2 browser test |
| complexity.reuse_points | confirmed | SendDaemonBridgeRequest, lookupRef, redactDaemonSecrets, mailbox PostSystem + mailWake, heartbeat route detection, dcCallFrame |
| complexity.side_effect_risk | moderate | permission split touches shared gates (companion marker, hosted render, discovery) |
| risk.correctness | moderate | timer-driven coalesce/burst/TTL logic; must be race-free under -race |
| risk.fit | low | additive hooks on existing bridge and gates |
| risk.test | moderate | timers need an injected clock; browser overlay needs Playwright |
| risk.security_or_contract | high | widens companion-body tool access; must keep curl/ui/screenshot denied and keep the monitor content-free |

## Phases

### Phase 1: Backend tools, authority split, monitor registry

#### 1. `ws/daemon.highlight` (MCP + daemon)

Input schema (added to `daemonControlTools()`):

```text
session_key  string   (standard)
action       enum     "show" | "clear" | "clear_all"          (required)
id           string   highlight slot id, ^[A-Za-z0-9_-]{1,32}$, default "default"
tab_id       string   optional explicit tab (default: most recently focused visible tab)
target       object   show only; exactly one of:
               ref        string   daemon.dom ref handle (g<gen>-r<n>)
               selector   string   CSS selector, <=256 bytes, no control chars
               text       string   visible text / accessible name, <=200 runes
             plus optional generation (integer) asserted like daemon.ui
comment      string   optional guide text, <=280 runes (longer is rejected)
style        enum     "spotlight" | "pulse" | "ring", default "spotlight"
format       string   "json" for the structured object; default compact text
```

Server rules (`wsdashboard/daemon_guide.go`, `(*Daemon).DaemonHighlight`):

- `show` requires `target` with exactly one of ref/selector/text, else
  `invalid_request`. `clear` uses `id` (default `"default"`); `clear_all`
  takes no `id`/`target`/`comment`/`style` (rejected if present, so no input
  is silently ignored).
- `ref` goes through `lookupRef(tab_id, generation, ref)`, giving the same
  `stale_ref` / `ref_not_found` / `denied`(`excluded_element`) errors as
  `daemon.ui`; the resolved tab is pinned for the bridge call.
- `selector` mentioning `daemon-confirm` or `data-ws-dom-exclude`
  (case-insensitive) → `denied` / `excluded_element`. The client also refuses
  any matched element inside an excluded subtree.
- `comment` passes through `redactDaemonSecrets`; the client renders it as
  text only (never `innerHTML`).
- Bridge op `guide.highlight` with params
  `{action, id, style, comment, target:{kind, ref, generation, selector, text}}`
  via `SendDaemonBridgeRequest(..., daemonBridgeOpTimeout)`. No confirmation, no
  `CompanionPause`.
- Bridge error `invalid_request` → `DaemonControlError{Code:"guide_unavailable",
  Reason:"client_unsupported"}` (pre-Phase-2 shell). Other client codes pass
  through (`target_not_found`, `target_excluded`, `too_many_highlights`,
  `invalid_request` with a client reason).
- Response `DaemonHighlightResponse`:
  `{ok, action, tab_id, id, style, target_kind, scope("shell"|"frame"|"settings"),
  visible, rect{x,y,width,height} (top-level viewport CSS px), cleared}`.
  `clear` / `clear_all` are idempotent: clearing a missing id or an empty tab
  returns `ok` with `cleared: 0`, never an error.
  Compact text example: `highlight show id=default tab=tab-1 scope=frame visible rect=120,88 240x32`.

#### 2. `ws/daemon.monitor` (MCP + daemon registry)

Input schema:

```text
session_key  string   (standard)
action       enum     "start" | "stop" | "status"              (required)
events       array    subset of: tab_switched, section_changed, modal_opened,
                      modal_closed, route_changed, highlight_clicked
                      (default: all 6; unknown names → invalid_request)
ttl_sec      integer  default 600, clamped to [60, 900]
debounce_ms  integer  default 400, clamped to [300, 2000]
format       string   "json" for the structured object; default compact text
```

Status object `DaemonMonitorStatus`:
`{active, monitor_id, events[], ttl_sec, debounce_ms, started_at, expires_at,
remaining_sec, delivered, coalesced, suppressed, stop_reason}`.
`stop` returns the final status (`stop_reason: "stopped_by_agent"`); `status`
with no monitor returns `{active:false}` plus the last `stop_reason` when one
is remembered. `stop` with no active monitor is idempotent: it returns the
same inactive status, never an error.

Registry (`wsdashboard/daemon_guide_monitor.go`), keyed by owner reply id
(`Server.conversation`):

- **One monitor per owner**, at most **8 active daemon-wide**
  (`too_many_monitors` beyond that).
- **Event sources:**
  1. Server-derived `route_changed` from the tab heartbeat route / reply-id
     change detection (`daemon_bridge.go:311-328`), via a hook invoked after
     the bridge lock is released (`handleFrame` captures the change under the
     lock and runs a deferred hook after unlocking). A tab's first non-empty
     route is not a change and emits nothing.
  2. Client frame on the same `/rail/events` WebSocket (each ≤1 KiB,
     `wsMaxFrame`):
     `{"event":"guide-monitor-event","tab_id","monitor_event","route","section","modal","highlight_id"}`.
     Only the 5 client-side names (`tab_switched`, `section_changed`,
     `modal_opened`, `modal_closed`, `highlight_clicked`) are accepted from
     clients. A client `route_changed` is dropped (server-derived only).
     Fields are clamped: `route` ≤256 bytes, `section`/`modal`/`highlight_id`
     ≤64 bytes, with control characters stripped. `route` also passes through
     `redactDaemonSecrets` before delivery (as `daemon.dom` does for its
     route). Frames from unregistered streams are dropped.
  3. Client frame `{"event":"guide-monitor-stop","tab_id","monitor_id"}` →
     stop with `stopped_by_user` and one final notice.
- **Coalesce:** the first matching event opens a batch window of
  `debounce_ms` (fixed window from the first event, so latency stays
  bounded). Events inside the window merge: the latest event's fields win,
  `events` keeps distinct names in arrival order, `coalesced_count` counts raw
  events.
- **Burst cap:** at most **12 delivered turns per rolling 60 s per monitor**.
  An over-cap batch is not delivered; its raw events are added to
  `suppressed_count` and to a per-name `suppressed_events` map, and a single
  summary delivery is scheduled for when the oldest delivery in the window
  ages out. That summary counts toward the cap. Its payload is
  `{"monitor_id","event":"events_suppressed","suppressed_count","suppressed_events":{name:count},"remaining_sec"}`.
  A regular delivery that goes out first absorbs the pending suppressed
  counts instead (fields below), so no summary is sent twice. Final notices
  (`monitor_stopped`) bypass the cap: they are one-shot and end the monitor.
- **Delivered turn** (one system envelope, label `guide-monitor`):

  ```text
  [ws/daemon.monitor] structural UI event (follow up with ws/daemon.dom or ws/daemon.highlight if useful; call ws/daemon.monitor action=stop when the guide ends)
  {"monitor_id":"gm-…","event":"tab_switched","events":["tab_switched","modal_opened"],"tab_id":"tab-…","route":"/s/…","section":"","modal":"settings","highlight_id":"","coalesced_count":3,"remaining_sec":512}
  ```

  `suppressed_count` / `suppressed_events` appear only when non-zero. Final
  notice: `{"monitor_id":"gm-…","event":"monitor_stopped","reason":"stopped_by_user"|"expired"}`.
- **Teardown:** TTL timer (`expired`, final notice delivered); stop chip
  (`stopped_by_user`, final notice); agent `stop` (no notice, the tool result
  acknowledges); owner session no longer live (`session_closed`, no notice),
  checked at every delivery (not at ingest, which stays I/O-free) and by a
  30 s sweep that runs only while a monitor is active. An archived (hidden)
  owner whose registration still reads live is accepted: its mail queues
  unclaimed and the TTL, 8-monitor cap and 12/min cap bound the cost; `Daemon.Close` (`daemon_stopped`, no notice). Every
  teardown stops all of that monitor's timers.
- **Tab push:** on every start/stop/expiry, push `daemon-guide-monitor`
  `{active, monitors:[{monitor_id, events, expires_in_ms}]}` to every
  registered tab (non-blocking enqueue, `daemon_bridge.go:339-346`), and to a
  tab when it first registers while monitors are active, so the Phase 2 stop
  chip and client event hooks follow the registry.
- **Testability:** the registry takes an injected clock/timer source, delivery
  function, and liveness function. Production defaults: real timers, mailbox
  `PostSystem` + `mailWake.Wake()`, registration liveness
  (`ReadRegistration` + `regLive`, `index.go:826-831`).

#### 3. Authority split (MCP)

- New `isDaemonGuideTool(name)`: `daemon.tabs`, `daemon.dom`,
  `daemon.highlight`, `daemon.monitor` (`internal/mcp/daemon_guide.go`).
- `companionBodyBaseAllows` admits `isDaemonGuideTool` names.
  `companionBodyPhonePostureAllows` is unchanged, so guide tools are
  posture-locked (hands busy) while the body holds the phone, like every other
  work tool.
- `daemonControlCallRefusal`: an open top-level `companion-body` record is
  allowed exactly the guide tools (regardless of `DashboardControlAccess`).
  Every other `daemon.*` tool is refused with a message naming the guide set.
  The `leadRender` branch also admits guide tools for a body render.
- `daemonControlListed()` becomes per-tool `daemonControlListedFor(name)` at
  its two call sites (`server_inventory.go:338`, `server_exec_cutover.go:40`).
- `hostedRenderServer` (`hosted_render.go:81-87`) records a body-guide grant
  (new `Server.dashboardGuideEffective` bool, alongside
  `dashboardControlEffective`) for an open top-level companion-body record, so an agy body manual lists
  `daemon_tabs`/`daemon_dom`/`daemon_highlight`/`daemon_monitor` as
  `[direct]` and never `daemon_curl`/`daemon_ui`/`daemon_screenshot`.
- Optional `DaemonGuideHost` interface
  (`DaemonHighlight`, `DaemonMonitor`) asserted on `s.daemonControl`; a host
  without it answers `guide_unavailable`.
- Dispatch cases for the two tools at `server_dispatch.go:1729-1730`; handler
  routing in `handleDaemonTool` (`daemon_tools.go:194-218`).
- Prompts: the companion-body launch prompt (`companion_soul_prompt.go:702-711`)
  gains a short guide-tools paragraph (laptop posture only; highlight before
  explaining; start a monitor for multi-step tours and stop it when done;
  `curl`/`ui` still go through `delegate_work`). The companion-work prompt
  (`cmd/ws-mcp/companion_runtime.go:913`) lists the two new tools.
- `runtime.json` (ws + wsflow): add `daemon.highlight`, `daemon.monitor`, then
  regenerate the package assets.

#### 4. Phase 1 verification

- Unit tests (`-race`): highlight argument validation and error mapping
  (`guide_unavailable`, ref staleness, excluded selector); monitor
  start/stop/status/replace and clamps; coalescing into one delivery with the
  correct `coalesced_count`; burst cap at 12/min with the suppressed summary;
  TTL expiry plus final notice; stop-chip frame plus final notice; liveness
  teardown with no notice; server-derived `route_changed` from heartbeats;
  client `route_changed` and non-allowlisted names dropped; content fields
  clamped; `Daemon.Close` teardown; the 8-monitor cap.
- Permission tests: body sees and calls tabs/dom/highlight/monitor; body is
  refused curl/ui/screenshot (listing, discovery, call); body guide tools are
  posture-locked at phone posture; body agy manual lists exactly the 4 guide
  aliases; companion-work and opted-in leads see all 7; workers never see any;
  highlight/monitor never trigger `RequestDaemonConfirmation`.
- `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`,
  `go test -race -count=1 ./internal/mcp/... ./internal/wsdashboard/... ./internal/wsagent/...`
  (or targeted `-run` covering daemon/companion/guide/permission tests plus a
  full package compile), `GOOS=windows go vet ./...`.

### Result (5fce5315d) - 2026-11-28

Phase 1 landed as planned. It adds `ws/daemon.highlight` (argument checks, then a `guide.highlight` bridge op) and `ws/daemon.monitor` (a daemon-side registry). The registry supports start/stop/status/replace with clamps, coalesced deliveries, the 12/min burst cap with a suppressed summary, and TTL and stop-chip final notices. It also covers liveness teardown, `route_changed` derived on the server from heartbeats, the client allowlist, and `Daemon.Close` teardown. The companion-body authority split exposes only the 4 guide aliases (tabs/dom/highlight/monitor), locked to its posture; companion-work and opted-in leads see all 7. The tools are also added to both `runtime.json` files.

One addition beyond the plan: the dashboard's `invalid_request:<reason>` bridge errors (for example `invalid_selector`) map to `invalid_request` with that `Reason`. A bare `invalid_request` from an older client maps to `guide_unavailable/client_unsupported`. The original worker made further deviations:
- Liveness is checked at delivery, sweep and final notice, not at ingest, so ingest stays I/O-free.
- The new tools default to compact text.
- A monitor sees events from every dashboard tab, not one bound tab.
- Final notices bypass the 12/min cap.
- Routes are passed through `redactDaemonSecrets` at delivery (covered by `TestGuideMonitorDeliveryRedactsSecrets`).

Verification:
- `go test -race -count=1 -timeout 45m ./cmd/ws-mcp/...` ok.
- Targeted `-race -run 'Guide|Highlight|Monitor|DaemonTool|Companion' ./internal/mcp/ ./cmd/ws-mcp/`: 38/38 PASS.
- `go build ./...`, `go vet ./...`, `GOOS=windows go vet ./...` ok.
- `python3 scripts/generate-package-assets.py --check` rc 0.
- `ws/tickets.verify` PASS.

Pre-existing failures, not from this branch:
- `internal/mcp`: `TestSessionKeyResolvesRoot` has a `-race` data race on `s.intake` in `ServeStdio` (`server.go:254` vs `:356`), because `session_auth_test.go` runs two concurrent `ServeStdio` on one server.
- `internal/wsdashboard`: 11 node-suite tests fail: TestShellPostLayout, TestSessionURLServesTheShellAroundItsFramedPage, TestBoardKeepWiring, TestBoardToolbarPlacementAndCounts, TestBoardGallery, TestConversationPresentationMergePreservesIdentityAndDisclosures, TestFilesExplorerInstances, TestGalleryNoReservedColumnWithoutAPage, TestSearchDrawerSplitOwnership, TestFreshSessionLoadClient, TestShellSwitchesFramesThroughItsGlue. Their inputs (assets/testdata/templates/`pages.go`) are identical to merge-base `b01986791`.

This session took over a wedged worker. The implementation was complete but uncommitted, and it was reviewed before being committed.

### Phase 2: Shell overlay, structural hooks, stop chip, E2E

Builds on the Phase 1 bridge op, push event, and frames.

- **New assets** `assets/dashboard-daemon-guide.js` + `dashboard-daemon-guide.css`
  (each <1,000 lines), loaded by the top-level local shell next to
  `dashboard-daemon-bridge.js`. `dashboard-daemon-bridge.js` delegates op
  `guide.highlight` to `window.WsDaemonGuide` (a small hook; the file stays
  <1,000 lines); framed pages answer a frame-side resolve op through the
  existing `dcCallFrame` protocol.
- **Target resolution:** `ref` resolves against shell refs or the owning frame
  (`dcRemoteRefTargets`). `selector` queries the shell, then the active session
  frame, then the settings frame, taking the first visible match. `text`
  matches visible elements by accessible name or direct text (exact,
  case-insensitive; then substring). Matches inside `dcIsExcluded` subtrees or
  the confirm banner → `target_excluded`. Not found → `target_not_found`. Max 8
  concurrent slots per tab → `too_many_highlights`. `show` on an existing `id`
  replaces that slot.
- **Cross-iframe projection:** a frame target's `getBoundingClientRect()` is
  offset by its iframe element's rect plus `clientLeft/clientTop`, giving
  top-level viewport coordinates. The spotlight (dimmed backdrop with a cutout)
  and the comment card render in a shell-level fixed layer above the frames, so
  nothing is clipped by an iframe.
- **Tracking:** scroll (capture) and resize listeners on the shell window and
  the target frame's window, plus a `ResizeObserver` on the target, update the
  rect in at most one rAF per change. No continuous loop. The highlight hides
  (`visible:false`) while the target is detached or off-screen and clears when
  the frame navigates away.
- **Dismissal:** a `×` button on the card and `ESC` clear the slot immediately.
  `highlight_clicked` fires (via the client frame) when the user clicks the
  highlighted target or the card body.
- **Structural hooks** emit `guide-monitor-event` frames only while a
  `daemon-guide-monitor` push says a monitor is active and the event is in its
  set: `tab_switched` (session tab / ctab change), `section_changed` (settings
  section), `modal_opened` / `modal_closed` (settings modal, `dialog[open]`),
  `highlight_clicked`. A ~300 ms client-side debounce runs before sending.
  Never `route_changed`, never text content.
- **Stop chip** in the shell chrome while any monitor is active:
  `가이드 모니터링 중 · 중단` (new wsi18n key in all 11 tables:
  de/en/es/fr/it/ja/ko/pt-BR/ru/zh-Hans/zh-Hant; en "Guide monitoring · Stop",
  other locales translated in the same meaning). One click sends
  `guide-monitor-stop` and hides the chip optimistically. Style follows
  `dashboard-style.md` compact pill.
- **Companion gaze hook:** `document.dispatchEvent(new CustomEvent("ws:guide-highlight-change", {detail:{id, visible, style, rect:{x,y,width,height}, has_comment}}))`
  on show, move (rAF-coalesced), and clear. No companion stage/VRM file is
  edited.
- **E2E (Playwright, gated on `WS_PLAYWRIGHT_MODULE` like
  `daemon_control_browser_test.go`):** spotlight and card on a shell target and
  on a session-frame target with the correct projected rect, following scroll
  and resize; `×` and `ESC` dismissal; `highlight_clicked`, `tab_switched`, and
  modal events reach a fake delivery sink coalesced; the stop chip appears,
  stops the monitor, and delivers `stopped_by_user`; `ws:guide-highlight-change`
  fires; an excluded target is refused. Verify idle frame-freedom (no rAF while
  static).

### Result (901bc5816) - 2026-11-28

Phase 2 landed in 901bc5816, with review fixes in 47386310b. The overlay is in the new modules `assets/dashboard-daemon-guide.js` (658 lines) and `dashboard-daemon-guide.css` (119 lines), loaded right after the daemon bridge and confirm assets.

What it does:
- Ref/selector/text resolution across the shell, the session frame and the settings frame.
- Spotlight and pulse styles, with a text-only comment card in a `data-ws-dom-exclude` fixed layer.
- Cross-iframe projection, clipped to the frame viewport.
- Tracking through scroll/resize listeners, a ResizeObserver and a throttled MutationObserver, with one rAF site and nothing scheduled while idle. It pauses while the page is hidden.
- `×` and ESC dismissal, `highlight_clicked` on a target or card click, the 8-slot cap, replace by id, and the `ws:guide-highlight-change` gaze event.
- Structural monitor hooks (`tab_switched`, `section_changed`, `modal_opened/closed`, `highlight_clicked`) with a 300 ms debounce, gated on the `daemon-guide-monitor` push.
- The stop chip (`js.dc.guide.monitor_stop`) plus `js.dc.guide.dismiss`, in all 11 locales.

Wiring: `dashboard-daemon-bridge.js` delegates `guide.highlight`; `dashboard-stream.js` relays `daemon-guide-monitor` and resets it on drop.

Deviations from the plan:
- Frame and settings refs resolve through a same-origin `window.wsDaemonGuideRef(gen, ref)` call, not a `dcCallFrame` op, because an Element cannot cross postMessage.
- While `#workflow-settings` is open, the overlay layer moves into that modal, because `showModal` makes the rest of the document inert.
- Mutation-driven re-measure uses a 120 ms timer rather than rAF and writes styles only on change. A detached selector/text target re-resolves at most once per second, with one trailing retry.
- `tab_switched` carries the new tab value in `section`.
- Text targets never match user-editable content (contenteditable, textarea, select, non-button inputs), so found/not-found answers cannot probe drafts.

Review: code-review-lite (claude.medium, read-only) found 0 Critical, 0 Important and 5 Minor. Four are fixed in 47386310b:
- stop-chip send result;
- editable text probes;
- hidden-tab pause and re-resolve backoff;
- re-check before delivering a batch built before a stop.

The fifth (an optional vm unit test of the chip) is covered end to end by Playwright instead.

Verification. Branch merged with develop 9bfb17422 (d3ca4fe45); develop e2ac20320 only adds whiteboard docs and merge-trees cleanly.
- `node --test testdata/daemon-guide.test.cjs`: 10/10.
- `WS_PLAYWRIGHT_MODULE=… go test -run TestDaemonGuideBrowser ./internal/wsdashboard/`: PASS in real Chromium. It covers shell and frame spotlights with projected rects following scroll and resize, `×`/ESC, refusals and the cap, editable-text probes, idle rAF freedom, coalesced monitor deliveries, and chip → `stopped_by_user`.
- `go test ./internal/wsi18n/...` ok.
- `GOOS=windows go vet ./...` ok.
- `generate-package-assets.py --check` rc 0.
- `go test -race ./cmd/ws-mcp/...` ok.
- `TestDashboardAssetSourceAssembly`'s hard-coded bundle list now includes the guide modules.

Remaining `-race` failures, none from this branch (none of their inputs are touched here):
- `internal/wsdashboard`, 9 from the Phase 1 list: TestShellPostLayout, TestSessionURLServesTheShellAroundItsFramedPage, TestBoardKeepWiring, TestBoardToolbarPlacementAndCounts, TestConversationPresentationMergePreservesIdentityAndDisclosures, TestFilesExplorerInstances, TestSearchDrawerSplitOwnership, TestFreshSessionLoadClient, TestShellSwitchesFramesThroughItsGlue. The develop merge fixed TestBoardGallery and TestGalleryNoReservedColumnWithoutAPage.
- `internal/wsdashboard`, 3 introduced by develop's 578066dfe (`docHidden` is undefined in source snippets taken from `assets/dashboard.js`): TestTickTimesKeepsItsSetsUntilThePageChanges, TestTabLabelCount, TestUserMessageClockAndMinuteRefresh.
- `internal/mcp`:
  - The `s.intake` ServeStdio race hits TestSessionKeyResolvesRoot, TestMailboxRecvConcurrentCallsNeverDoubleDeliver and TestServeStdioDoesNotBlockToolsListBehindLongCall.
  - TestDashboardSettingsCatalog expects 28 dashboard keys; develop's 049b88cce made it 30.
  - TestServeStdioConfigShow fails on macOS: the response path is `/var/folders/…` but the test expects the canonical `/private/var/…`.


## Resolution (2026-11-28)

Phase 1 (ws/daemon.highlight, ws/daemon.monitor, guide monitor registry, companion-body 4-alias guide subset) and Phase 2 (dashboard-daemon-guide.js/.css overlay, cross-iframe projection, structural monitor hooks, stop chip, 11-locale i18n, Node 10/10 & real Chromium Playwright TestDaemonGuideBrowser PASS, code-review-lite 0 Critical/Important) implemented and verified on impl/develop/companion-interactive-guide (335f9f635). T0 transparent.
