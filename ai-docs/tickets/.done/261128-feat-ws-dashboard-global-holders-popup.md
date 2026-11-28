---
title: "대시보드 전역 하단 상태바 Holders 모니터·프로세스 트리 리소스 트레이스 팝업 및 #p-holders/#drhandle 최종 은퇴"
blocked-by:
  - 261128-feat-ws-dashboard-ide-shell-overhaul
  - 261128-feat-ws-dashboard-gallery-git-page
  - 261128-feat-ws-dashboard-gallery-workpanel-b
related:
  261128-feat-ws-dashboard-always-open-gallery-and-ticketmap: Track 2A always-open #bgal and Ticket Map infrastructure (prerequisite of Track 2B/2C)
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 2fba117ed66dfd43
sage-review-completeness-reviewed: 2fba117ed66dfd43
completed: 2026-11-28
---

# Global Bottom-Bar Holders Monitor, Process-Tree Resource Trace Popup, and Final #p-holders / #drhandle Retirement

## Background

Today the Werkschaft dashboard hosts `#p-holders` (`hdLoad` / `hdDraw` / `hdStop` in `assets/dashboard-git.js`, `#p-holders` in `assets/dashboard.tmpl`) inside the per-session top-right `#drhandle` / `#drbody` / `#drcards` drawer. That placement has three structural problems:

1. **Scope mismatch:** Session holders (`wsholder.Manager`, `GET /holders`), host-owned terminals (`GET /s/host0/terminals`), and durable in-process sessions (`durable.*`) are **daemon-global**, not per-session. Unattached holders, start failures, and host terminals are invisible from Home (`/`) or Settings (`/settings`), and `page_routes.go` previously gated `view.Holders` on `d.holders != nil` alone even though `daemon_http.go` and `holders.go` also serve `d.durableSessionHost()`.
2. **Last obstacle to legacy drawer retirement:** Track 1 (`261128-feat-ws-dashboard-ide-shell-overhaul`) introduces the 26px global bottom status bar (`#botbar`, `#slot-bot-right`), upward `.usage-toggle` / `#gmenu` popovers, and `data-fdim` modal dimming. Track 2B (`261128-feat-ws-dashboard-gallery-git-page`) retires `#ghandle` / `#gbody` / `#p-worktrees`, and Track 2C (`261128-feat-ws-dashboard-gallery-workpanel-b`) retires `#p-todo`, `#p-agenda`, and `#p-notes` while relocating `drawerWait` into `dashboard-workpanel.js`. Only `#p-holders` keeps `#drhandle` / `#drbody` / `#drcards` alive.
3. **Lack of process-tree visibility and safe Stop confirmation:** `#p-holders` hides healthy attached holders, shows no parent-lead to child-agent (`agent.spawn`) or OS subprocess (`PID -> PPID`) hierarchy or memory/CPU pressure, and stops unattached holders on a single accidental click without confirmation.

Migrating Holders into a compact trigger at the far bottom-right of the global status bar with an upward diagnostic popup (`(1717)=a`, `(1741)=a`, `(1742)=a`, `(1743)=ok`) solves all three problems and completes the final retirement of `#p-holders`, `#drhandle`, `#drbody`, and `#drcards` while preserving `#sbody` / `#shandle` (`Ctrl+K` quick search) and unframed (`Frame: false`) renders (`(1724)`).

## Decisions

- **Compatibility tier: T0 transparent.**
  - **Phase 1** adds zero new wire fields or on-disk stores: it extends `shellPageData` (`page_regions.go:83`) with `Holders bool` (set in `serveShellStatus` at `page_routes.go:290`), updates `view.Holders` / shell `Holders` visibility (`page_routes.go:263`) to `d.holders != nil || d.durableSessionHost() != nil` (matching `daemon_http.go:105-108` and `holders.go:88-95`), adds `shell.js` (`KINDS` at `shell.js:71-106`) `postMessage` bridge types for orphan terminals, and updates dashboard templates, CSS, JS, i18n tables, and tests.
  - **Phase 2** adds:
    1. A read-only HTTP endpoint `GET /holders/metrics` backed purely by an in-memory sample ring (zero on-disk persistence). Old clients ignore it; a new frontend calling an older daemon degrades gracefully to static holder rows without RSS/CPU/sparklines.
    2. Optional additive JSON fields `label` (`omitempty`, already defined in `wsagent/holder.go:123` and populated for leads in `runner_lead_start.go:54`, omitted for workers in `runner_process.go:307`) and `parent_reply_id` (`omitempty`) on `wsagent.holderSession` (`wsagent/holder.go:118-126`). Verified against `workerHolderMatches` (`wsagent/worker_rejoin_test.go:56-90`): ownership matching compares only `Kind`, `Backend`, `ReplyID`, `Cwd`, `Capability`, and `ChildStatus.PID`, ignoring `Label` and unknown JSON fields (`ParentReplyID`). Older `wsholder` descriptors lacking `parent_reply_id` deserialize with `""` and fall back to live `wsagent` registry parent lookup in `holders.go` or flat root rendering.
  - No MCP tool schemas, `/mcp` hello payloads, tracker wire formats, or on-disk stores change.

- **Approved Visual & Interaction Reference (`(1741)=a`, `(1743)=ok`).**
  - Visual layout, tonal surfaces, typography, header chips, 2-layer tree grid, sparklines, and status footer follow the approved v2 mockup: `ai-docs/assets/mockups/261128/global-holders-popup/index.html` (with sibling `styles.css` and `app.js`).

- **Far Bottom-Right Compact Trigger & Status States (`(1717)=a`, `(1743)=ok`):**
  - Place the Holders trigger button at the **far bottom-right end** of `.bot-right` — specifically **to the right of `.usage-toggle`** inside `#slot-bot-right` / `.bot-right` (updating Track 1's bottom-right slot order so Holders is the rightmost element on the status bar).
  - Render as a compact `20px` pill: status dot + layers icon + **total running OS holder process count** (`h.pid > 0`, excluding `durable.*` `werk` in-process sessions, which still appear inside the popup with an `인프로세스` / in-process badge). Never render a long `"Holders"` text label in the bar at any viewport width.
  - States:
    - `ok`: muted tonal surface + `--wb-ok` dot + running count (when `running > 0` and no alert).
    - `warn`: `--wb-warn` tinted surface + `--wb-warn` dot + running count when any `!h.attached`, `h.restart_safe === false`, `list.incomplete === true`, or `list.failures.length > 0`.
    - `zero`: muted surface + dim dot + `0`.
  - Tooltip (`title` / `aria-label`) carries the breakdown (`running`, `lost`, `warn`, `failures`).
  - *Rejected:* Placing the trigger left of `.usage-toggle`; showing a text label `"Holders"` in `#botbar`; counting in-process `durable.*` sessions in the OS process pill count.

- **Upward Popover Coordination & Modal Dimming (`railPopOpen`, `data-fdim`):**
  - Anchor the popup (`width: 440px; max-width: calc(100vw - 16px); max-height: min(560px, calc(100vh - 72px))`) **4px above `#botbar`**, right-aligned to the far-right trigger and clamped within the viewport (`8px` inset).
  - Register with `railPopOpen` / `WsRail.popPlace` (`dashboard-rail-popovers.js` `railPopPlace` at `#L420`, its `WsRail.popPlace` call at `#L444`, `railPopClose` at `#L464`, `railPopOpen` at `#L688`; `popPlace` itself is defined in `rail.js#L890`) so the Holders popup, `.usage-toggle` popover, and `#gear` / `#gmenu` are mutually exclusive, and `railPopClose` (`:466`) cancels any active Hold-to-Confirm (`WsHoldConfirm.cancelAll()`).
  - Extend `data-fdim` click-blocking and dimming (`dashboard-shell.css:33,43`) to the trigger and popup whenever an iframe modal opens.

- **Popup Sections, Summary Counts, and `Listing.Incomplete` Banner:**
  - Header summary chips keep counts separate (`실행 N · 미연결 M · 주의 K · 실패 F` or `모두 정상`), where `lost` = `!h.attached`, `warn` = `(attached && restart_safe === false) + (incomplete ? 1 : 0)`, and `fails` = `failures.length`.
  - Surface `list.incomplete === true` as an explicit warning banner (`role="alert"`) at the top of `#hdlist`.
  - Group **all** holders rather than hiding healthy ones:
    1. **주의 필요 (Attention Required):** unattached holders (`!h.attached`), attached holders with `restart_safe === false`, and `list.failures` rows.
    2. **실행 중 프로세스 트리 (Running Process Tree):** attached healthy holders (root leads, child-agent holders, standalone `exec` holders; rendered flat in Phase 1, nested in Phase 2) plus `durable.*` `werk` sessions marked with an in-process badge and excluded from the OS process count.
    3. **터미널 (Terminals):** daemon-global `host0` terminals (`GET /s/host0/terminals`) and active-session orphan terminals.

- **Hold-to-Confirm Holder Stop & Inline Terminal Confirm (`(1742)=a`):**
  - **Holder Stop (`POST /holders/<id>/stop`):** Uses **Hold-to-Confirm** (`1.5s` hold with left-to-right `--wb-fail` fill bar via existing `window.WsHoldConfirm` in `assets/hold-confirm.js`), supporting pointer down/leave/up and Space/Enter keyboard hold, cancelling automatically on pointer leave, `blur`, `visibilitychange`, or `Escape`. Plain click does not stop; it writes the hold-to-confirm hint into `#hdnote`. Stop remains restricted to unattached stoppable holders (`!h.attached && list.can_stop`, matching `dashboard-git.js:235` and `holders.go:96,101`).
  - **Terminal Stop (`POST /s/<rid>/terminals/<id>/close`):** Exited terminals stop directly; running terminals toggle an inline `종료` / `취소` confirm strip (`.hdconfirm`, `role="alertdialog"`) inside the row rather than opening a nested modal `wsConfirm` over the popup.
  - *Rejected:* Single-click holder Stop; nested `window.confirm` / `wsConfirm` modal over the upward popover.

- **Shell Terminals & `postMessage` Bridge (`shell.js`, `terminal.js`):**
  - Outer shell queries daemon-global `GET /s/host0/terminals` directly (`dashboard-git.js:152` pattern), without requiring `window.WsTerm` in the shell document.
  - Extend `KINDS` in `assets/shell.js` (`shell.js:71-106`) with host-validated `postMessage` messages between the shell and the active session iframe (`#frames > iframe.sframe.on` when a session `/s/<rid>` with `data-term` is active): requesting/receiving orphan terminals (`WsTerm.list`) and sending `ws:term-attach` (`WsTerm.attach(t)`).
  - When no session frame is active (Home `/`, Settings `/settings`, or a frame without `data-term`), disable `Attach` buttons with tooltip (`세션 프레임 활성화 시 연결 가능`, i18n key) and omit session orphans gracefully.

- **Zero-Polling Idle Contract & On-Demand Live Metrics Trace (`performance.md`, `(1743)=ok`):**
  - **Closed popup (`performance.md` Rule 1 & Frontend Minimal Updates):** Zero `setInterval` polling and zero OS process/memory syscalls while the popup is closed. Initial shell boot fetches `GET /holders` once to seed the trigger badge; updates thereafter happen on popup open, manual Refresh (`#hp-refresh`), after Stop actions, and optionally via a change-only SSE summary event if `wsholder.Manager` already observes a holder attach/exit/start-failure transition in memory (no extra disk/OS scan).
  - **Open popup trace (`GET /holders/metrics`, Phase 2):** Polled every `1.5s–2s` **only while the popup is open, unpaused (`#hp-pause`), and `document.visibilityState === "visible"`** (plus manual Refresh). Immediately stops polling on popup close, pause, or tab hide. Server retains an in-memory ring (~20–24 samples per holder/PID, expiring after idle) with zero disk I/O.
  - **Keyed DOM updates (`performance.md` Frontend Minimal Updates #5):** Live trace ticks patch only `[data-k="<id>:rss"]`, `[data-k="<id>:cpu"]`, `[data-k="<id>:spark"]`, and `#hp-agg` in place without rebuilding `.tnode` DOM rows, so active Hold-to-Confirm pointer/keyboard holds, focus, and tree expand/collapse states remain unbroken.

- **2-Layer Process-Tree Hierarchy & `CGO_ENABLED=0` Cross-Platform Collectors (Phase 2):**
  - **Logical agent layer:** Populate `Label: rec.Label` and additive `ParentReplyID` (from `rec.Parent.ReplyID` when `rec.Parent != nil`, `wsagent/record.go:151`) in `wsagent/runner_process.go:307` (`holderSession` in `wsagent/holder.go:118-126`). In `holders.go` / metrics assembly, also consult live `d.agents` parent relationships by `reply_id` so already-running holders whose descriptor predates `parent_reply_id` still nest under their parent lead holder.
  - **OS descendant layer (`PID -> PPID`):** Walk OS descendant processes under each holder's `PID` (and `Child.PID`) to sum tree RSS & CPU% and render expandable `.tn-row.os` rows on demand.
  - **Cross-platform OS collectors (`CGO_ENABLED=0` mandatory per `scripts/dev.py:146`, `agents-plugin-tool/scripts/build-release-assets.sh:33` (the script is under `agents-plugin-tool/`, not the repo-root `scripts/`); `performance.md` Rule 6 "Spawn a process only when no in-process route exists" governs the subprocess choice: Windows and Linux have an in-process route, so they spawn nothing; Darwin uses a single `ps` per open-popup poll, the owner-approved Rule 6 exception recorded in the `(1744)=a` Decision above):**
    - **Windows (`//go:build windows`, 100% in-process, zero subprocess spawns):** `windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)` + `Process32First`/`Process32Next` (reusing the pattern in `execjob/process_windows.go:121-144` and `wsstate/process_parent_windows.go:12-26`), `windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, ...)` + `windows.GetProcessTimes` (`wsholder/proc_windows.go:12-22`), and `windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")` (`PROCESS_MEMORY_COUNTERS_EX.WorkingSetSize`).
    - **Linux (`//go:build linux`, 100% in-process, zero subprocess spawns):** Read `/proc/<pid>/stat` (`comm`, `ppid`, `utime`, `stime`, `starttime`, `rss * os.Getpagesize()`, following `wsholder/proc_linux.go:21-38`).
    - **macOS / Darwin (`//go:build darwin`, `CGO_ENABLED=0`):** Single `ps -axo pid=,ppid=,rss=,cputime=,comm=` invocation per open-popup poll (matching `execjob/process_unix.go:61`), avoiding deprecated `syscall(2)` and paying no Windows AV spawn cost.

- **macOS `ps` spawn — owner-approved `performance.md` Rule 6 exception (`(1744)=a`, Rule 8):**
  - Rule: Rule 6, "Spawn a process only when no in-process route exists".
  - Reason: with `CGO_ENABLED=0` on Darwin, the in-process `kern.proc.all` sysctl (`golang.org/x/sys/unix` `SysctlKinfoProcSlice`) exposes `Xrssize` as `int16`, which saturates on large processes, so it yields no accurate RSS/CPU field. Windows uses native Win32 APIs and Linux reads `/proc`, both with zero spawns.
  - Scope: exactly one `ps -axo pid=,ppid=,rss=,cputime=,comm=` per ~1.5–2s metrics poll, only while the Holders popup is open, visible, and unpaused; never while it is closed, paused, or hidden.
  - Measured cost: the Phase 2 `### Result` records the observed `ps` execution time and cost.
  - *Rejected:* in-process sysctl with degraded RSS/CPU accuracy; macOS tree without RSS/CPU/sparkline.

- **Auto-confirmed technical decisions (sage-review settlement; owner directive `pref.ticket-settlement-autonomy`):**
  - **`GET /holders/metrics` schema & join key (Phase 2):** Response is `{ "ts": <unix ms>, "interval_ms": <int>, "supported": <bool>, "holders": { "<holder id>": { "pid": <int>, "rss": <bytes>, "cpu": <percent, one decimal>, "spark": { "rss": [<=24 bytes], "cpu": [<=24 percent] }, "procs": [ { "pid", "ppid", "name", "rss", "cpu" } ] } } }`. The join key to a `GET /holders` row is the holder `id` (the same id the stop route uses); `procs` is the flat OS-descendant list under the holder `PID` / `Child.PID` (the client derives nesting by `ppid`). `supported: false` (unsupported platform or collector failure) makes the client keep static holder rows. The ring expires an entry after ~30s without a metrics request. *Rejected:* joining by `pid` (PID reuse) or `reply_id` (absent for exec holders).
  - **Live reconcile during trace:** Each metrics response also carries the current `procs` set, so the client reconciles by stable key: patch existing `[data-k]` cells in place; **add** a `.tn-row.os` / child row for a new key and **remove** a row whose key vanished; a metrics key with no holder row in the DOM triggers one coalesced `GET /holders` re-read (at most one per 5s) instead of being dropped; a row that currently owns an active Hold-to-Confirm hold or keyboard focus is never removed or re-ordered until the hold ends, only marked exited (`.gone`). *Rejected:* patch-only updates (leaves stale and missing rows until manual Refresh).
  - **Route dispatch & gate:** `handleHolders` (`holders.go`) treats any non-empty remainder as `<id>/stop`; `/holders/metrics` is matched by an explicit branch before the id parse (or its own mux entry beside `daemon_http.go`'s `/holders` registration). It uses the **stricter `localViewer` gate used by Stop**, because it exposes OS process names and PIDs of arbitrary descendants; non-local (LAN-token) viewers get `404`, as the Stop route does ("as if the route did not exist"), and the client degrades to static rows without RSS/CPU/sparkline. `holders_test.go` covers `/holders/metrics` routing, the viewer gate, `no-store` / `nosniff` headers, and a method check (`GET` only).
  - **Closed-popup trigger freshness (zero polling preserved):** besides boot seed, open, manual Refresh, and post-Stop, re-fetch `GET /holders` once on `visibilitychange` to visible and on window `focus`, rate-limited to one fetch per 10s, and after shell frame navigation. No `setInterval`; no OS metrics reads. The SSE summary event stays optional.
  - **Bottom-bar identifiers are provisional:** `#botbar`, `#slot-bot-right`, `.bot-right` are this ticket's working names; the implementer reads the landed `261128-feat-ws-dashboard-ide-shell-overhaul` DOM (3-column bar, right cell holding `.usage-toggle`) and mounts the trigger as the rightmost element of that right cell, adapting ids/classes in code and tests to the landed names. Likewise, `file:line` anchors in this ticket are search hints, not edit targets.
  - **Phase 1 tree is flat:** `parent_reply_id` and the `d.agents` parent lookup land in Phase 2, so Phase 1 renders attached holders flat (root, child-agent, and `exec` holders in one list) with the in-process badge for `durable.*`. Phase 2 adds nesting; the `d.agents` lookup needs a new optional parent accessor on the `AgentHost` interface (absent host or accessor → flat rendering).
  - **Legacy cards not yet retired by Track 2A:** before deleting `#drcards` / `#drbody` / `#drhandle`, Phase 1 step 3 greps for remaining `#p-*` cards under `#drcards` (e.g. `#p-tickets`, `#p-mailbox` if `261128-feat-ws-dashboard-always-open-gallery-and-ticketmap` has not landed). Any remaining card keeps its host container and the minimum drawer wiring; only `#p-holders` and the `#drhandle` coupling are removed in that case, and the Result records what stayed and why. If none remain, the full retirement list applies.
  - **Unframed pages:** the Result lists which routes still render unframed (`Frame: false`) and therefore have no Holders surface, per `(1724)`.
  - **Phase 2 collector tests:** unit-test the Darwin `ps` output parser, the Linux `/proc/<pid>/stat` parser (including `comm` containing spaces/parentheses), subtree aggregation, and the ring (capacity 24, idle expiry) with fixtures, plus `GOOS=windows go vet`.

- **Final `#p-holders` & `#drhandle` / `#drbody` / `#drcards` Retirement — Preservation Audit:**
  - **Retire:** `#p-holders`, `#drbody`, `#drcards`, `#drhandle` (`dashboard.tmpl:172,190`), `cardTab` / `panelTab` / `drawerTab` / `drEntries`, `drFill`, `drBadge` / `drSwap`, `DR_OPEN` (`ws.dashboard.drawer`), `"panels"` sheet mode in `dashboard-drawer.js`, `#drhandle` / `#drfold` `.wait` coupling in `drawerWait` (already moved to `dashboard-workpanel.js` by Track 2C), and `.hdcard` / `#drbody` / `#drhandle` CSS rules in `dashboard-drawer.css`.
  - **Preserve (critical audit):**
    - `#drawer`, `#drclip`, `#drsheet`, `#drfold` (search mode), `#sbody`, and `#shandle` (`Ctrl+K` quick-search sheet in `dashboard-drawer.js:21-41`) **MUST stay intact** so `Ctrl+K` quick search and `TestSessionOmitsDuplicateSearchButtonAndIndexKeepsIt` (`shell_search_test.go:65-100`, which keys `#tkqs` omission on `#sbody` presence) keep working without regression.
    - Keep `#thandle` / `thandleSync` / `drRoom()` (`--shr` / `--thr` offsets and `drRoom._shr` / `drRoom._thr` caches pinned by `testdata/ticketmap-hier.test.cjs`) intact unless Track 2A has already retired `#thandle`, removing only the `#drhandle` (`w`, `--drhw`, `--drw`) terms from `drRoom()`.
    - **Unframed standalone renders (`Frame: false` in `page_regions.go:229,287`):** Per `(1724)`, unframed standalone pages (`/`, `/s/<rid>`, `/w/...` without `?frame=1`) have no `#botbar` and keep their existing in-page controls; removing `#p-holders` from `drcards` simply removes the legacy drawer card without adding `#botbar` to unframed pages, and shell-only `holders-pop` scripts tolerate absent `#botbar` / trigger elements without JS errors.

## Constraints

- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`, `ai-docs/assets/mockups/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin-tool/`)
- **Visual & theme parity (`dashboard-style.md`):** Material tonal surfaces (`--wb-bg`, `--wb-card`, `--wb-muted`), `color-mix()` semantic washes, `font-variant-numeric: tabular-nums`, Dark & Light theme parity, `0px` horizontal overflow (`scrollWidth <= clientWidth`), and no boxy `1px` row outlines.
- **Performance & AV budget (`performance.md`):**
  - `CGO_ENABLED=0` across all platforms (a build constraint from `scripts/dev.py:146` and the release script, not a `performance.md` rule) (`scripts/dev.py:146`, `agents-plugin-tool/scripts/build-release-assets.sh:33`).
  - Zero subprocess spawns in Windows and Linux metrics collectors; zero `setInterval` polling and zero OS metrics reads while the popup is closed; zero disk I/O for metrics history; keyed in-place DOM patching so live ticks never reset active Hold-to-Confirm states.
- **11-locale i18n parity:** Add/update all new and revised `js.hd.*` / `js.holders.*` keys across every table in `agents-plugin-tool/internal/wsi18n/tables/` (`de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`).
- **Downstream-first shipped text (`shipped-surface-boundary.md`):** No repository-specific strings, paths, or assumptions in shipped Go/JS/CSS/i18n assets.
- **Modular asset hygiene:** Isolate new global Holders popup CSS/JS cleanly (e.g., `dashboard-holders.css` / `dashboard-holders.js` registered in `pages.go`) rather than further bloating `dashboard-git.js` or `dashboard-rail-popovers.js`.

## Prior Art

- `ai-docs/assets/mockups/261128/global-holders-popup/index.html` (`styles.css`, `app.js`) — approved v2 visual & interactive reference (Dark/Light, Wide/Compact, Warning+Tree / Healthy+Tree / Empty / No Session Frame / Incomplete Scan).
- `agents-plugin-tool/internal/wsdashboard/holders.go` (`HolderHost`, `DurableSessionHost`, `durableHolderInfo`, `handleHolders`) & `holders_test.go`.
- `agents-plugin-tool/internal/wsdashboard/assets/hold-confirm.js` (`window.WsHoldConfirm`, `DEFAULT_DURATION = 1500` at `hold-confirm.js#L2`).
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-rail-popovers.js` (`railPopOpen`, `railPopClose`, `railPopPlace`, `WsRail.popPlace`).
- `agents-plugin-tool/internal/wsdashboard/assets/shell.js` (`KINDS`, `framed`, frame message routing) & `assets/terminal.js` (`WsTerm.list`, `WsTerm.attach`).
- `agents-plugin-tool/internal/execjob/process_windows.go` (`CreateToolhelp32Snapshot`), `internal/wsholder/proc_windows.go` (`GetProcessTimes`), and `internal/wsholder/proc_linux.go` (`/proc/<pid>/stat`).

## Prior Decisions

- b31e61ba (2026-11-28, commit): "Legacy retirement is transitional: Phase 1 moves #r-panels into the Workpanel (drops drawer todo/agenda), Phase 2 retires only the Notes card; #drhandle plus a slimmed sheet stay live for #p-worktrees ... and #p-holders" — bearing: supports
- 261128-feat-ws-dashboard-gallery-workpanel-b (2026-11-28, Decisions): "Legacy Retirement ... Phase 2 retires the Todo, Agenda, and Notes (#p-notes) cards fro[m the drawer]" — bearing: constrains
- 261128-feat-ws-dashboard-always-open-gallery-and-ticketmap (2026-11-28, Decisions): "Retire #thandle: Remove the top-right Ticket Map handle button (#thandle and thandleSync in dashboard-drawer.js, .d-tm-handle / --thr room reservation in drRoom())" — bearing: constrains
- 261128-feat-ws-dashboard-control-mcp-api (2026-11-28, Decisions): "(1728)=a & (1742) — Shared top-level shell 1-Click Confirmation Gate" — bearing: constrains
- 30f2b221 (2026-11-24, commit): "Replaced live hosted rail hover archive button with conditional .drstop ... and replaced modal confirmations in rpSessMake with a reusable 1.5s horizontal hold-to-confirm" — bearing: supports
- 6d1ba1f0 (2026-11-25, commit): "left hold-confirm.js DEFAULT_DURATION (1500ms) and destructive session/rail/account/bg-exec holds untouched." — bearing: supports
- 261110-feat-ws-dashboard-session-holder (2026-11-10, Result): "GET /holders and POST /holders/<id>/stop; the drawer's holders card with its strings in all 11 language tables." — bearing: supports
- 1496fe20 (2026-11-10, commit): "No confirmation step on Stop: the page has no existing confirm pattern for a destructive button" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsdashboard/page_routes.go, page_regions.go, pages.go, assets/dashboard.tmpl, dashboard-drawer.js/css, dashboard-git.js, shell.js, terminal.js, new dashboard-holders.js/css, wsi18n/tables (11), wsagent/holder.go, runner_process.go, new wsholder collectors |
| scope.surface | cross-module | wsdashboard, wsagent (holderSession fields), wsholder collectors, wsi18n tables; new GET /holders/metrics route |
| scope.new_public_symbol | yes | GET /holders/metrics; holderSession.ParentReplyID; shellPageData.Holders (absent today at page_regions.go#L83-L93) |
| scope.new_type_contract | yes | holderSession ParentReplyID json parent_reply_id omitempty; metrics response schema; shell.js KINDS postMessage types |
| scope.test_surface | new-files | existing wsdashboard/holders_test.go, drawer_height_test.go, pages_render_test.go, shell_search_test.go, frontend-drawer.test.js, testdata/holders-anchor.test.cjs; new collector and Playwright coverage needed |
| complexity.reuse_points | confirmed | hold-confirm.js WsHoldConfirm, railPopOpen/railPopPlace, holders.go handleHolders, wsholder/proc_*.go, execjob/process_windows.go |
| complexity.side_effect_risk | high | retires drawer shell (#drhandle) while Ctrl+K #sbody/#shandle and drRoom() must survive; drawer JS touches drhandle at many sites in dashboard-drawer.js |
| risk.correctness | high | cross-platform process-tree collectors and keyed in-place DOM patching under live holds |
| risk.fit | high | three blocked-by tickets (all ready, not landed) supply #botbar and slot order this ticket mounts into; no #botbar in dashboard.tmpl today |
| risk.test | high | needs Playwright across theme x width, Windows/Linux/Darwin collector builds |
| risk.security_or_contract | moderate | new read-only metrics endpoint exposing process info and additive wire field; stop route unchanged |

## Phases

Two sequential phases: Phase 1 delivers the far bottom-right Holders trigger, upward popup, Hold-to-Confirm Stop, terminal `postMessage` bridge, visibility fix, and final `#p-holders` / `#drhandle` retirement against the existing `GET /holders` contract; Phase 2 adds the `CGO_ENABLED=0` cross-platform process-tree RSS/CPU% collector, `GET /holders/metrics` endpoint, `wsagent` parent/label propagation, and live expandable process-tree sparkline UI.

### Phase 1: Far Bottom-Right Trigger, Upward Popup, Hold-to-Confirm, Shell Terminal Bridge, and #p-holders / #drhandle Retirement

1. **Visibility & Shell Template Plumbing (`page_routes.go`, `page_regions.go`, `dashboard.tmpl`, `pages.go`):**
   - Add `Holders bool` to `shellPageData` (`page_regions.go:83`), set `page.Holders = d.holders != nil || d.durableSessionHost() != nil` in `serveShellStatus` (`page_routes.go:290`), and update `view.Holders` (`page_routes.go:263`) to `d.holders != nil || d.durableSessionHost() != nil` so durable-only sessions also surface Holders.
   - Mount the compact Holders trigger button at the far bottom-right of `#botbar` (to the right of `.usage-toggle` inside `.bot-right` / `#slot-bot-right`) when `.Holders` is true, and include the `#holders-pop` upward popover container (or lazy-mount it via `railPopOpen`) participating in `data-fdim` (`dashboard-shell.css`). Keep unframed (`Frame: false`) renders safe without `#botbar` per `(1724)`.
   - Register modular `dashboard-holders.css` and `dashboard-holders.js` in `pages.go`.
2. **Global Holders Controller, Popover Coordination, and Hold-to-Confirm (`dashboard-holders.js`, `dashboard-holders.css`, `dashboard-rail-popovers.js`, `shell.js`, `terminal.js`):**
   - Seed the compact trigger badge on shell load via one `GET /holders` call (no `setInterval` background polling; show total running OS holders `h.pid > 0`, excluding `durable.*`), updating state (`ok` / `warn` / `zero`), breakdown tooltip, and `incomplete` banner.
   - Integrate `#holders-pop` into `railPopOpen` / `railPopClose` / `WsRail.popPlace` (right-aligned, 4px above `#botbar`, mutually exclusive with `.usage-toggle` and `#gmenu`, cancelling active holds on close).
   - Render all holders grouped into **주의 필요** (`!h.attached`, `restart_safe === false`, `failures`), **실행 중 프로세스 트리** (attached running holders + `durable.*` `werk` in-process row), and **터미널** (`GET /s/host0/terminals` globally + active session iframe orphans).
   - Wire `window.WsHoldConfirm` (1.5s hold, Space/Enter keyboard support, cancel on leave/blur/tab-hide/Esc, plain click hint in `#hdnote`) for `!h.attached && can_stop` holders posting to `POST /holders/<id>/stop`; wire inline `.hdconfirm` (`종료`/`취소`) for live terminals and direct stop for exited terminals posting to `POST /s/<rid>/terminals/<id>/close`.
   - Extend `shell.js` `KINDS` and `terminal.js` / frame messaging so active session frames bridge orphan terminals and `Attach`; disable `Attach` with tooltip when on Home/Settings/non-terminal frames.
   - Add all required i18n keys across all 11 locale JSONs in `internal/wsi18n/tables/`.
3. **Final `#p-holders` & `#drhandle` / `#drbody` / `#drcards` Retirement + Preservation (`dashboard.tmpl`, `dashboard-drawer.js`, `dashboard-drawer.css`, `dashboard-git.js`):**
   - Remove `#p-holders`, `#drbody`, `#drcards`, `#drhandle`, `cardTab` / `panelTab` / `drEntries`, `drFill`, `drBadge` / `drSwap`, `DR_OPEN` (`ws.dashboard.drawer`), and `"panels"` drawer mode while preserving `#drawer`, `#drclip`, `#drsheet`, `#sbody`, `#shandle` (`Ctrl+K`), `#drfold` (search mode), and `#thandle` / `drRoom()` (`--shr`, `--thr`) without regression.
   - Clean up legacy `#p-holders` / `#drbody` / `#drhandle` rules in `dashboard-drawer.css` and `hdLoad` / `hdDraw` / `hdStop` from `dashboard-git.js`.
   - Update affected Go, Node, and Playwright tests (`holders_test.go`, `holders-anchor.test.cjs`, `frontend-drawer.test.js`, `drawer_height_test.go`, `pages_render_test.go`, `shell_search_test.go`, `shell-topbar.browser.cjs`, `ticketmap-hier.test.cjs`, `confirmation.test.cjs`, etc.) and add Playwright coverage across Dark/Light × Wide/Compact (`1440px` / `1180px`+) and scenarios (`warning`, `healthy`, `empty`, `noframe`, `incomplete`).

Verification: `cd agents-plugin-tool && go test -count=1 ./internal/wsdashboard/... ./internal/wsi18n/...`, `GOOS=windows go vet ./...`, `node --test agents-plugin-tool/internal/wsdashboard/*.test.js agents-plugin-tool/internal/wsdashboard/testdata/*.test.cjs`, and Playwright verification confirming far-right trigger placement, upward popover mutual exclusion, `data-fdim`, Hold-to-Confirm 1.5s hold & cancel, terminal `Attach` enabled/disabled states, `Ctrl+K` `#shandle` preservation, and `0px` horizontal overflow.

### Result (8c8cfd589) - 2026-11-28

**What landed**
- The shell's `#botbar` mounts a compact Holders trigger (`#holders-trigger`) at the far right of `.bot-right`, after `.usage-toggle`. It opens the upward `#holders-pop` as rail popover kind `holders`:
  - placed by `railPopOpen`/`railPopPlace`, right-aligned to the trigger above the bar;
  - mutually exclusive with the usage popover and `#gmenu`;
  - `railPopClose` cancels active holds via `WsHoldConfirm.cancelAll()`.
- Visibility: `view.Holders` and `shellPageData.Holders` are true when `d.holders != nil || d.durableSessionHost() != nil`.
  - Unframed routes (`/`, `/s/<rid>`, `/w/...` without `?frame=1`) have no Holders surface. The legacy drawer card is gone, so there is nothing to show there (`(1724)` keeps unframed renders safe).
- `dashboard-holders.js`/`.css` are registered in `pages.go`; `asset_source_test.go` pins them.
  - One `GET /holders` seeds the badge on shell load (running OS holders, `pid > 0`, excluding `durable.*`); there is no background poll.
  - It re-reads on popup open and on tab return.
  - Trigger state is `ok`/`warn`/`zero`, with a breakdown tooltip and an incomplete banner.
- The popup sections are Needs attention, Running, and Terminals (`GET /s/host0/terminals` plus active-frame orphans via the shell `KINDS` bridge).
  - Unattached stoppable holders use a 1.5s `WsHoldConfirm` red-fill hold (bar mode). It supports Space/Enter and cancels on leave, blur, tab hide, or Esc. A plain click explains the hold in `#hdnote`.
  - A live terminal asks inline (`term-yes`/`term-no`, focus moves to confirm, Esc dismisses and then closes); an exited terminal closes at once.
  - Attach is disabled with a tooltip outside a terminal frame.
- Retired: `#p-holders`, `#drhandle`, `#drbody`, `#drcards`, `cardTab`/`panelTab`/`drEntries`, `drFill`, `drBadge`/`drSwap`, `DR_OPEN`, the `"panels"` drawer mode, the legacy drawer CSS, and `hdLoad`/`hdDraw`/`hdStop` in `dashboard-git.js`.
  - Kept: `#drawer`, `#drclip`, `#drsheet`, `#sbody`, `#shandle` (Ctrl+K), `#drfold`, and `#thandle`/`drRoom()`.
  - Anchor references were audited and rewired, including TicketMap's header room (now `tk-room`).
- i18n keys were added to all 11 `wsi18n/tables/*.json`.
- Tests:
  - `holders-anchor.test.cjs` became `holders-popup.test.cjs`;
  - new Playwright `holders-popup.browser.cjs` (`TestHoldersPopupBrowser`) covers Dark/Light × Wide 1440/Compact 1180 and the scenarios `warning`, `healthy`, `empty`, `noframe`, `incomplete`;
  - drawer, topbar, ticketmap, workpanel and confirmation tests were updated.

**Verification**
- `TestHoldersPopupBrowser`, `TestTicketMapBrowser`, `TestTicketMapDock`, `TestWorkpanelBrowser`, `TestShellTopbarBrowser`, `TestIdeShellPhase3Browser`: pass.
- `ide-shell-phase3.browser.cjs` read the accent bar from `::after` while the CSS draws it with `::before`. That drift existed at base, and the test now reads `::before`.
- i18n: 11 tables, valid JSON, 0 duplicate keys, key sets identical to `en`.
- `go test ./internal/wsi18n/...`: pass. `GOOS=windows go vet ./...`: clean.
- Node, wsdashboard `*.test.js` + `testdata/*.test.cjs`: 25 failures.
  - These are the same 25 test names that fail at base `86bdc5eec`; the comparison used a `git archive` export of base `internal/`.
  - Breakdown: shell-lifecycle (13), board-home-ack (4), composer-multiline `.cfoot` pin, session-load, shell-post-layout, and frontend CustomEvent / `:has()` / transition pins.
- Go wsdashboard failures that exist at base and are unrelated: TestTickTimesKeepsItsSetsUntilThePageChanges, TestTabLabelCount, TestUserMessageClockAndMinuteRefresh, TestBoardKeepWiring, TestBoardToolbarPlacementAndCounts, TestFilesExplorerInstances, TestConversationPresentationMergePreservesIdentityAndDisclosures, TestSearchDrawerSplitOwnership, TestFreshSessionLoadClient, TestShellSwitchesFramesThroughItsGlue, TestShellPostLayout, TestSessionURLServesTheShellAroundItsFramedPage.
- Visual comparison: `compared /tmp/claude/hdshots/live-dark-wide-warning.png vs /tmp/claude/hdshots/mockup-dark-wide-warning.png: match` and `compared /tmp/claude/hdshots/live-light-compact-warning.png vs /tmp/claude/hdshots/mockup-light-compact-warning.png: match`. This covers the trigger, the popup frame, the sections, and the hold and confirm controls.

### Phase 2: Cross-Platform Process-Tree RSS & CPU% Trace (`GET /holders/metrics`) and Expandable 2-Layer Tree UI

1. **Logical Agent Hierarchy Metadata (`wsagent/holder.go`, `wsagent/runner_process.go`, `holders.go`):**
   - Add additive `ParentReplyID string \`json:"parent_reply_id,omitempty"\`` to `holderSession` (`wsagent/holder.go:118-126`) and populate `Label: rec.Label` + `ParentReplyID` (when `rec.Parent != nil`) in `wsagent/runner_process.go:307`.
   - Verify `workerHolderMatches` (`wsagent/worker_rejoin_test.go`) continues to pass for both legacy and enriched descriptors.
   - In `holders.go` (or metrics builder), also resolve live parent links from `d.agents` when an already-running holder descriptor lacks `parent_reply_id`.
2. **`CGO_ENABLED=0` Cross-Platform Process-Tree Collectors & `GET /holders/metrics` (`internal/wsholder` / `internal/wsdashboard`):**
   - Implement OS process snapshot & resource collectors behind build tags:
     - `windows`: `CreateToolhelp32Snapshot` + `OpenProcess` + `GetProcessTimes` + `psapi.dll!GetProcessMemoryInfo` (`WorkingSetSize`) — zero subprocess spawns.
     - `linux`: `/proc/<pid>/stat` (`ppid`, `utime`, `stime`, `rss * os.Getpagesize()`) — zero subprocess spawns.
     - `darwin`: single `ps -axo pid=,ppid=,rss=,cputime=,comm=` per sample — `CGO_ENABLED=0`; owner-approved Rule 6 exception (`(1744)=a`), and the Result records the observed `ps` execution time and cost.
   - Build an in-memory bounded sample ring (~20–24 points per holder/PID, expiring on idle, zero disk I/O) computing per-node and subtree-aggregated RSS (MB/GB), CPU%, and sparkline series, served at `GET /holders/metrics` (`Cache-Control: no-store`, `X-Content-Type-Options: nosniff`).
3. **Expandable 2-Layer Tree UI, Keyed Metric Updates, and On-Demand Trace Loop (`dashboard-holders.js`, `dashboard-holders.css`):**
   - Render the 4-column tree grid (Process, RSS, CPU, inline `<svg>` sparkline + Stop) with expandable parent-lead -> child-agent -> `.tn-row.os` subprocess rows, aggregate RSS/CPU strip (`#hp-agg`), live trace status pill (`1.5s 트레이스`), and Pause/Resume (`#hp-pause`) toggle matching `ai-docs/assets/mockups/261128/global-holders-popup/index.html`.
   - Run the `GET /holders/metrics` timer **only while the popup is open, unpaused, and `document.visibilityState === "visible"`** (strictly zero timers/syscalls when closed).
   - Update metric cells (`[data-k="<id>:rss"]`, `[data-k="<id>:cpu"]`, `[data-k="<id>:spark"]`, `#hp-agg`) in place by stable key so active Hold-to-Confirm holds, focus, and row expansion are never disturbed.

Verification:
- `cd agents-plugin-tool && CGO_ENABLED=0 go test -count=1 ./internal/wsdashboard/... ./internal/wsagent/... ./internal/wsholder/...`
- `cd agents-plugin-tool && CGO_ENABLED=0 GOOS=windows go vet ./...` and `GOOS=linux go vet ./...`
- `node --test agents-plugin-tool/internal/wsdashboard/*.test.js agents-plugin-tool/internal/wsdashboard/testdata/*.test.cjs`
- Playwright verification (Dark & Light × Wide `1440px` & Compact `1180px`) comparing live popup capture against `ai-docs/assets/mockups/261128/global-holders-popup/index.html`: 2-layer lead -> child-agent -> OS subprocess expansion, live sparkline updates without interrupting an active 1.5s Hold-to-Confirm hold, Pause/Resume & close stopping all metric requests, and `0px` horizontal overflow.

### Result (41269621f) - 2026-11-28

**What landed**
- The backend landed in 8c8cfd589 and the UI in 41269621f.
- Hierarchy:
  - `holderSession` gains an additive `parent_reply_id`; `runner_process.go` writes `Label` and `ParentReplyID`.
  - `workerHolderMatches` passes for legacy and enriched descriptors.
  - For a running holder whose descriptor lacks the link, `holder_parents.go` (`AgentParentHost`) resolves it from live `d.agents`. `GET /holders` carries it as an additive `parents` map (holder id → parent reply id).
- Collector: a new package `internal/wsproctree` (not `wsholder`; it is a stand-alone process-table snapshot), all `CGO_ENABLED=0`.
  - windows: Toolhelp32 + `OpenProcess`/`GetProcessTimes` + `psapi` `WorkingSetSize`.
  - linux: `/proc/<pid>/stat`.
  - darwin: one `ps -axo pid=,ppid=,rss=,cputime=,comm=` per sample (`(1744)=a`).
  - Other OSes report unsupported.
- `GET /holders/metrics` (`holders_metrics.go`) answers `{ts, interval_ms:1500, supported, holders:{id:{pid, rss, cpu, spark:{rss[],cpu[]}, procs:[…]}}}` with `no-store` and `nosniff`.
  - Each holder's `rss`/`cpu` aggregates its OS subtree (the holder process and its descendants). Each `procs` entry is per node, capped at 64.
  - The ring is in memory: 24 points per holder; an ask within 1s reuses the last snapshot; history drops after 30s idle.
  - Gated to `localViewer` (404 otherwise); GET only (405).
- UI (`dashboard-holders.js`/`.css`):
  - Holders nest within a section into a 2-layer tree: lead → spawned agent holders (by `parent_reply_id` or `parents`, cycle-safe) → OS process rows (depth-first by ppid).
  - Chevrons start open only for holders with spawned holders, as in the mockup, and keep the user's choice across re-renders.
  - Columns: RSS / CPU / a two-line sparkline (CPU bold, RSS faint).
  - The strip shows aggregate totals and a live `1.5s trace` pill, with a pause/resume button.
- Trace loop:
  - It is a `setTimeout` chain (no `setInterval`) and runs only while the popup is open, the document is visible, and the user hasn't paused. Close, pause, tab hide, or a 404/405/`supported:false` answer stops it, and an unsupported daemon hides the pill and pause.
  - Cells patch in place by `data-k`, so holds, focus, and expansion survive ticks.
  - A tick that lacks a rendered holder, or names a new one, re-reads `/holders` once per new difference, at most every 5s.
- i18n: 13 `js.holders.*` keys were added to all 11 tables.

**Measured darwin `ps` cost (`(1744)`)** — Apple M3 Max, about 969 processes:
- `BenchmarkSnapshot`: 38.2 ms/op wall (fork+exec+parse), 1.27 MB and 5734 allocs per op.
- `/usr/bin/time -l ps -axo …`: 0.03 s real, 0.02 s sys, about 3.9 MB peak RSS.
- At the 1.5s ask period this is roughly 1.5–2.5% of one core, and only while the popup is open, visible and unpaused. With the popup closed there are no spawns or timers.

**Decisions and cosmetic deviations**
- IDs keep Phase 1's `hd-` prefix (`#hd-agg`, `#hd-pause`, `#hd-trace`) rather than `#hp-*`, and OS rows are `.hd-row.hd-os` rather than `.tn-row.os`.
- The kind badge heads the meta line, as in the mockup, so names keep the column width.
- A lead row shows its own OS subtree. Spawned agent holders are separate rows rather than being summed into the lead. As a result the lead's figure is smaller than the mockup's illustrative 612 MB.
- The aggregate totals wrap under the chips instead of sharing their line.

**Verification**
- `CGO_ENABLED=0 go test -count=1 ./internal/wsi18n/... ./internal/wsagent/... ./internal/wsholder/... ./internal/wsproctree/...`: all ok.
- Targeted wsdashboard tests (`TestDashboardAssetSource|TestHolders|TestSessionPagesCarryScriptHooks|TestPanelRows|TestDrawer|TestSearchPalette|TestDashboardConfirmation|TestI18n|TestPagesRender`): ok, including `holders_metrics_test.go` and `holder_parents_test.go`.
- The full wsdashboard package shows only the base-existing failures listed in Phase 1's Result.
- `gofmt -l` on the new packages and files: clean. `GOOS=windows go vet ./...` and `GOOS=linux go vet ./...`: clean.
- `node --test testdata/holders-popup.test.cjs`: 11/11. The full node set (`*.test.js` + `testdata/*.test.cjs`) has 25 failures, the same 25 names as base `86bdc5eec`.
- i18n: 11 tables × 4330 keys, 0 duplicates, all equal to `en`.
- `TestHoldersPopupBrowser`: pass, across 9 runs. It covers:
  - workers nested through both link sources;
  - chevron open, fold and refold;
  - OS rows naming their parent;
  - metric cells, sparkline and totals;
  - pause stopping reads for 2.2s and resume asking at once;
  - close stopping reads for 2.2s;
  - a tick during the 1.5s hold that moves the sparkline and keeps the same held button;
  - 0px horizontal overflow with the deepest rows open.
- Visual comparison:
  - `compared /tmp/claude/hdshots/live-dark-wide-warning.png vs /tmp/claude/hdshots/mockup-dark-wide-warning.png: match`;
  - `compared /tmp/claude/hdshots/live-light-compact-warning.png vs /tmp/claude/hdshots/mockup-light-compact-warning.png: match`.
  - The tree, metric columns, sparklines, pill, pause and totals match. The two cosmetic deviations above were noted.

#### Edition (80093231a) - 2026-11-28

Lite code review of `86bdc5eec..41269621f` found 0 Critical, 1 Important and 3 Minor issues.
- **Important, fixed:** `panel-rows.browser.cjs` still dereferenced the retired `#drbody`. It now asserts that `#drbody` is null and that `#r-panels` sits outside `#drawer`. `TestPanelRowsBrowser` passes.
- **Minor, fixed:**
  - Attach drew a missing `#i-link` icon, so the symbol was added to `sprite.svg`.
  - A re-render during an in-flight holder stop could offer a second stop. `hd.stopping` now keeps the guard per holder, and `holders-popup.test.cjs` covers it (11/11).
- **Minor, not changed (design-level, follow-up):** a holder whose process is an OS descendant of another holder's process is counted in both holders' subtree totals.
- After the fixes, `TestHoldersPopupBrowser`, `TestIdeShellPhase3Browser`, `TestPanelRowsBrowser` and `TestTicketMapBrowser` pass.
