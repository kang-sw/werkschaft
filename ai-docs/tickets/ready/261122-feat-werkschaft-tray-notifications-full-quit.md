---
title: "데스크톱 트레이 상주, 네이티브 알림, 완전 종료"
related:
  261121-feat-werkschaft-tauri-shell-return: "Phase 4 adoption decision and desktop shell prerequisite"
blocked-by: 261121-feat-werkschaft-tauri-shell-return#phase4
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: a1a6eb85ad11f88c
sage-review-completeness-reviewed: a1a6eb85ad11f88c
---

# Werkschaft tray residency, native notifications, and full quit

## Background

The owner asks for a simpler desktop lifecycle and notifications:

> tauri 앱 트레이에 숨기기 + tauri 네이티브 알람 등 구현이 가능할까요? 데스크톱 앱을 끄더라도 기본적으로는 트레이로 가게 하고, 트레이에 우클릭했을 때 "닫기"와 "완전 종료(모든 홀더와 데몬 끄기 = 모든 비동기 세션 종료)"가 되게요. 지금 수명이 앱 수명 / 데몬 수명 / 홀더 수명으로 삼중화되어서 좀 혼란스럽긴 한데, 앱 표시 쪽에서는 앱 자체 수명하고 백그라운드 데몬 수명으로 이원화하는 게 개념적으로는 더 다루기 편할 듯합니다.

Owner decision (2084)=a: capture a separate todo ticket now; implement only after `261121-feat-werkschaft-tauri-shell-return` Phase 4's Result records the owner decision adopting Tauri. The phase Result alone is insufficient if it does not adopt Tauri; revisit this ticket if the decision differs.

Evidence at `develop`: `agents-plugin-tool/desktop/src/main.rs#L340-L345` intercepts the main window's `CloseRequested` and calls `close::request`; `agents-plugin-tool/desktop/src/close.rs#L19-L55` checks draft readiness or asks for confirmation before destroying the window (local pages are destroyed directly). `agents-plugin-tool/desktop/Cargo.toml` pins Tauri 2.12.5 with `devtools`, no tray feature, and no notification plugin. The prerequisite's Phase 2 requires the daemon to survive shell exit and holder identities to survive daemon handoff.

Evidence at `develop`: `agents-plugin-tool/internal/wsdashboard/daemon_http.go#L155-L170` authenticates `/shutdown` and calls `d.Close()` for the daemon; `agents-plugin-tool/internal/wsholder/manager_test.go#L168-L173` documents holders and their children surviving daemon restart. `agents-plugin-tool/internal/wsdashboard/holders.go#L46-L72` exposes an individual holder stop. A search of `agents-plugin-tool` for all-holder shutdown and stop paths found no all-holder stop path. Verify the current baseline again before implementation.

## Decisions

- Owner decisions (2026-11-22 01:08 KST):
  - (2085)=a: Tray "닫기" exits only the desktop app; the daemon and holders continue running. Rejected: stopping the daemon or holders on "닫기".
  - (2086)=a: Window X-close hides the app to the tray. This supersedes the Tauri prerequisite's Phase 1 close behavior and its Phase 4 Windows check for this subsequent feature; Phase 2's daemon-survival requirement remains. Rejected: exiting the app on X-close.
  - (2087)=a: Check for unsent drafts and confirm only on real app exit via "닫기" or "완전 종료", not on hide. Preserve draft recovery and make check or exit failures recoverable. Rejected: prompting on hide.
  - (2088)=a: "완전 종료" confirms only when sessions are running and displays their count. Cancellation leaves the app, daemon and holders running; an incomplete stop must be reported rather than represented as a successful full quit. Rejected: unconditional full-quit confirmation.
  - (2089) confirmed (technical): Add a full-stop route alongside the existing bearer-authenticated `/shutdown`; stop all holders before the daemon and report remaining holders on partial failure. Keep ordinary `/shutdown` and daemon replacement behavior. Establish the directional compatibility tier from evidence; T0 is a proposal, not a settled verdict.
  - (2090)=a, with owner refinement: Notify on a question to the user and on session completion only once the session is fully idle: "정확히는, 모든 자식이나 백그라운드 태스크가 종료되어서 완전 idle이 된 시점이요." Do not notify for each child completion. Use native notifications inside the app without duplicate browser notifications. Rejected: per-child completion notifications and duplicate browser delivery.
  - (2091) confirmed (technical): Measure macOS unsigned-app notification permission and Windows portable AppUserModelID in the Phase 4 package build; fall back to browser notifications if either platform's native notification path fails.
  - (2092)=a: Tray left-click restores and focuses the window; on macOS follow menu-bar convention and open the menu. Rejected: leaving the window unrestored on other platforms.
  - (2093)=a: Implement this feature for Tauri only; Electron is slated for retirement and is not updated. Rejected: Electron parity.
- (2142) auto-confirmed (technical): The full-stop route is a new path, `POST /shutdown/all`, bearer-authenticated with the endpoint token exactly like `/shutdown`; the Tauri shell already holds that token (`agents-plugin-tool/desktop/src/endpoint.rs` sends it as a bearer). When the running daemon answers 404 (an older daemon without the route), the shell reports that full quit is unsupported by the running daemon and stops nothing, rather than falling back to `/shutdown`. An older daemon actually answers a bearer-only request to the new path with `401 login_required` from its local cookie guard before routing, because its access exemption names only `/shutdown` (`agents-plugin-tool/internal/wsdashboard/localhost_auth.go#L91-L111`, `agents-plugin-tool/internal/wsdashboard/access.go#L123-L139`); the new daemon adds `/shutdown/all` to that exemption, and the shell recognizes the old daemon's `login_required` response (and a 404) as unsupported while still reporting a genuine bearer failure (`unauthorized`) as an authentication error. Both cases stop nothing; tests cover both responses. Rejected: a query flag on `/shutdown`, because an older daemon would ignore the flag, stop only itself and answer success, which (2088) forbids reporting as a full quit; and the browser `localViewer` + `sameOrigin` gate of `POST /holders/<id>/stop`, because the caller is the native shell, not a dashboard page.
- (2143) auto-confirmed (announced): For this feature the owner-confirmed (2089) supersedes 6395b28c6's "without opening a new IPC or HTTP surface", which governed the Tauri shell-return work only; no other new surface is added. Citation: (2089) answer `ok` on the whiteboard, 2026-11-22.
- (2144)=a (owner, design-review gap): The full-quit confirmation counts every holder full quit would stop - agent sessions including children, and terminal holders - and counts a session whose status is unavailable as running. Zero means nothing is lost. Rejected: counting only sessions with an active turn or known background work (an idle live session or a busy terminal would die uncounted); showing separate working and idle counts.
- (2145)=a (owner, design-review gap): Full quit stops the holders and the daemon running at that moment. An MCP client still alive outside the holders may afterwards start a fresh empty daemon through the launcher's normal recovery (`agents-plugin/bin/ws-mcp-launcher.py#L1821-L1887`, `#L2080-L2096`); when the shell observes a successor daemon after the stop, its result says so instead of hiding it. No launcher change. Rejected: keeping the backend stopped until an explicit restart, which changes the shipped launcher's cross-version behavior.
- (2146)=a (owner, design-review gap): For a session whose adapter reports an unknown background-task count (Codex today, `agents-plugin-tool/internal/wsagent/codex_mapper.go#L284-L290`), treat it as fully idle once its turn ended and no ws-known child or background task remains; document that such sessions may alert early. Rejected: requiring a known zero, which would never alert for those sessions. Before alerting, count every ws-known live exec job (`261117-feat-ws-exec-tools`) as well. Apply this fallback wherever an adapter still reports an unknown count, including sessions inside the exec cutover: removing the native shell is not evidence of a known zero (`codex_mapper.go#L284-L290` still emits -1), and agy keeps an advisory-only native subagent path. Retire the fallback for an adapter only when evidence shows its background activity is completely observed.
- Present two user-facing lifecycle tiers: the app and the background daemon. Holders are a daemon implementation detail, not a third user-facing lifetime.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Apply `ai-docs/manuals/shipped-surface-boundary.md` for `agents-plugin-tool/`, `ai-docs/manuals/performance.md` for `agents-plugin-tool/`, `ai-docs/manuals/release-compatibility.md` for `agents-plugin-tool/internal/`, and `ai-docs/manuals/ws-mcp.md` if `agents-plugin-tool/internal/mcp/` is touched, per AGENTS.md Implementation Conventions. Select a storage type before adding persisted data; account for interactive file operations and Windows antivirus costs.
- A new route and any other cross-version surface require a directional compatibility verdict backed by old/new readers, writers, and recovery evidence. T0 is only a proposal in (2089); T3 requires explicit owner approval and a minor bump; T4 cannot ship.
- Preserve the existing authenticated and origin boundaries; design and test the authorization, ordering, race and failure semantics of the full-stop route. Test destructive lifecycle operations only against isolated, owned test processes and profiles.

## Prior Decisions

- a53f2ba82 (2026-11-22, commit): "Closing the main window still checks draft recovery and leaves other windows usable." — bearing: constrains
- 6395b28c6 (2026-11-21, commit): "Keep control in the native shell and make the dashboard close-readiness hook optional. Reuse the current daemon and browser recovery contracts without opening a new IPC or HTTP surface." — bearing: superseded for this feature by (2089) and (2143)
- fc9e3677e (2026-11-10, commit): "The stop route is gated by localViewer then sameOrigin, in the system-open route's shape" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/desktop/src/main.rs, agents-plugin-tool/desktop/src/close.rs, agents-plugin-tool/internal/wsdashboard/daemon_http.go, agents-plugin-tool/internal/wsdashboard/holders.go |
| scope.surface | public-interface | Phase 1 adds an authenticated full-stop HTTP route |
| scope.new_public_symbol | yes | Phase 1 adds `POST /shutdown/all` (decision (2142)) |
| scope.new_type_contract | yes | Phase 1 requires a response reporting remaining holders on partial failure |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/holders_test.go and agents-plugin-tool/internal/wsholder/manager_test.go; phases require additional verification |
| complexity.reuse_points | confirmed | close::request, handleShutdown, and holder List and Stop exist in the cited files |
| complexity.side_effect_risk | high | Full quit terminates running holders and daemon processes |
| risk.correctness | high | Concurrent starts and partial stops must not be reported as successful full quit |
| risk.fit | high | Tray, draft recovery, idle events, and notification fallback must work across both packaged platforms |
| risk.test | high | Process lifecycle races and platform notification delivery require isolated and packaged platform verification |
| risk.security_or_contract | high | New destructive route needs authorization, origin, and cross-version compatibility evidence |

## Phases

The prerequisite is satisfied: its Phase 4 Result (closed in 9c5a761a8) records the owner's Tauri adoption, merged at `1aace4c5e`. Later phases depend on the preceding phase's landed Result.

### Phase 1: Full-stop daemon contract

Implement the authenticated full-stop route: discover and stop all holders before stopping the daemon, and report remaining holders on partial failure. Design for concurrent starts and stop races. Test authorization and origin boundaries, discovery, termination, daemon shutdown, running sessions, races, partial failures and the evidenced directional compatibility tier. Preserve ordinary `/shutdown`, daemon replacement and surviving holders.

### Result (e9d961574) - 2026-11-22

`POST /shutdown/all` takes `/shutdown`'s endpoint bearer and joins its
loopback-only cookie exemption
(`agents-plugin-tool/internal/wsdashboard/daemon_http.go:45,205-238`,
`localhost_auth.go:104`). It calls `wsholder.Manager.StopAll`
(`agents-plugin-tool/internal/wsholder/manager.go:818-918`). StopAll refuses
launches with `ErrStopping` from its call, including a launch queued behind a
running one. A launch already running finishes and its holder is stopped.
Each round rescans the holders directory and stops every holder not yet
tried in parallel, using the protocol stop or the start-time-guarded kill.
The survivors are then read from the directory and the process table, not
from manager memory. The route closes the daemon only when no holder process
remains. That success answers `202 {"shutting_down": true, "stopped": [...],
"remaining": []}`. Otherwise it answers `409` with each remaining holder and
the reason, lifts the launch refusal, and keeps serving. A daemon that is
already stopping answers `503 {"error": "shutting_down"}` and stops nothing.
Plain `/shutdown`, daemon replacement, and holders that outlive an ordinary
stop are unchanged.

- Compatibility T0, by direction. An old caller (the old shell, Electron, an
  `Ensure` drain) still sends only `/shutdown`, which keeps its behavior
  (`daemon_http.go:162-174`, `TestOrdinaryShutdownLeavesTheHolders`). A new
  caller reaching an older daemon is gated: that daemon's exemption names
  only `/shutdown` (`dd73e50ec:agents-plugin-tool/internal/wsdashboard/localhost_auth.go:104`),
  so the guard answers a bearer-only `POST /shutdown/all` with
  `401 {"error":"login_required"}` before routing (`access.go:124-139`,
  `localhost_auth.go:136-148`). Nothing is stopped or written. The new shell
  reports that as unsupported, distinct from the route's own
  `401 unauthorized`. No store or holder protocol changes. Holder exits are
  persisted through the existing `OnExit` path of the individual stop.
- Partial-stop and race semantics: an incomplete stop never closes the
  daemon. A `Close` that begins during StopAll can end the process before it
  answers, so callers treat a lost answer as an incomplete stop.
- Verification: `go test ./internal/wsholder -run StopAll` passes against
  real isolated holders. It covers every holder ended with its running child,
  including unscanned, unattachable, and predecessor holders; a refused
  guarded kill reported remaining; and a running launch stopped while a
  queued one is refused. `go test ./internal/wsdashboard -run
  'FullStop|OrdinaryShutdown|UnexemptMachinePost|Shutdown|NonLoopbackHost|LocalhostAuthExemptions|Holder|LANMode|Guard'`
  passes for bearer, LAN token, non-loopback Host, ordering, 409, 503,
  no-holder-host, and older-daemon response shape cases. The full
  `wsholder` and `wsagent` suites pass. `TestWideExplorerRouting`,
  `TestShellStreamsTheRailAndItsFramesTheirRegions`,
  `TestProxyWhiteboardLinkHasNoLoginCode`, and
  `TestWhiteboardConstructionBeforeDashboardBind` fail identically on base
  `dd73e50ec` in this environment. `ai-docs/manuals/ws-mcp.md` documents the
  route.
- Performance: the full stop reads the holders directory and descriptors
  once per round (at most three) plus once for survivors. It runs only for
  this destructive request, never on an interactive path, and adds no
  persisted data.

### Phase 2: Tray and desktop lifecycle

Build on Phase 1 for Tauri: X hides to tray without a draft prompt; "닫기" exits only the app; "완전 종료" stops holders and daemon. Check unsent drafts on real exit, and confirm full quit only for running sessions, showing their count. Left-click restores and focuses except on macOS, where it opens the menu. Verify hide and restore, both exit actions, draft recovery and check failures, zero and nonzero session counts (agent sessions, children and terminals per (2144)), cancellation, incomplete-stop reporting, the unsupported response of an older daemon per (2142), and the reported successor daemon per (2145) against an isolated daemon; expose only the agreed two user-facing tiers.

### Result (279dd4d4f) - 2026-11-22

The main window's close button hides it to the tray with no draft check.
The tray menu has Show Werkschaft, Close Werkschaft, and Quit Werkschaft and
Stop the Background (`agents-plugin-tool/desktop/src/tray.rs`). Close exits
only the app and is also what the app menu's, Dock's, or system's quit does.
`ExitRequested` with no code is prevented and routed to Close. The app's own
exits, the updater restart, and the Windows rebuild proceed as before
(`close.rs`, `exit_request`). Both exits run the existing draft-readiness
check and confirm when it fails. A full quit counts every holder
`GET /holders` lists, read with the existing browser credential: agent
sessions, subagent workers, and terminals per (2144). An unreadable count is
never taken as zero. It confirms once, combining count and draft lines, only
when something would be lost. It then posts the full stop, waits for the
daemon to exit, and reports an observed successor daemon before exiting
(2145). Incomplete (409), unsupported (404 or `401 login_required`),
unauthorized, already stopping, and unknown outcomes show what happened and
keep the app open. They release the document and clear the exit flag.
`Connection.closing` stays set throughout, so the shell never restarts the
daemon it is stopping. A left click restores and focuses the window on
Windows and opens the menu on macOS (2092). Labels name only the app and the
background.

- Decisions: the labels are English like the rest of the native shell, which
  has no i18n. macOS keeps its Dock icon while hidden, and Reopen restores
  the window. The tray needs only Tauri's `tray-icon` feature, whose crate
  was already locked.
- Verification: `cargo test --locked --features custom-protocol` passes
  (54 at `5e8fb3b6f`). Its tests cover response classification, bearer-only
  requests after the identity recheck, holder counting, confirmation and
  report wording, exit routing, and tray click mapping. The isolated
  end-to-end run used
  `WERKSCHAFT_E2E_WS_MCP=/tmp/ws-mcp-e2e-new WERKSCHAFT_E2E_OLD_WS_MCP=/tmp/ws-mcp-e2e-old cargo test --locked --features custom-protocol full_stop_e2e -- --ignored --test-threads=1`
  and passed both tests. Each test starts real daemons in a scratch HOME,
  `WS_CACHE_HOME`, `WS_CONFIG_HOME`, and port 0. The new daemon stopped two
  terminal holders and itself, then a successor daemon was observed. A
  daemon built from `dd73e50ec` answered unsupported, and its holder and the
  daemon stayed alive. No test process outlived the run. Clippy reports only
  warnings in untouched code. No GUI was launched; Phase 4 runs the
  packaged app.
- Performance: an exit's identity rechecks read the endpoint file a bounded
  number of times. After the full stop, the endpoint is polled every 200 ms
  only while the daemon no longer answers health, for at most 20 s. Nothing
  is persisted.

### Phase 3: Native notifications

Implement native notifications for user questions and session completion at full idle after all children and background tasks finish. Map native failure to browser fallback without duplicate delivery. Verify questions, child completion while work remains, final idle, a session with an unknown background-task count per (2146), permissions, denied or failed native delivery and fallback.

### Phase 4: Packaged macOS and Windows verification

Exercise the complete Tauri lifecycle and notification flow on packaged macOS and Windows builds, including running session counts and cancellation, draft recovery, daemon and holder termination, full-idle event selection and duplicate-free fallback. Measure notification permission on unsigned macOS and AppUserModelID on portable Windows; verify browser fallback if the native path fails. Record platform versions, signing and launch conditions, evidence and remaining limitations. Electron is outside this feature's scope.
