---
title: "워크플로 설정창 전 레이어 플로팅 모달 전환"
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: ce9554445ed0ef75
sage-review-completeness-reviewed: ce9554445ed0ef75
completed: 2026-11-23
---

# Convert Workflow Settings Into a Floating Shell Modal Across All Layers

## Background

Today, opening Workflow Settings from `#gmenu` (`<a class="settings-page-link" href="/settings">` with `{{icon "gear"}}{{t "js.settings.title"}}`, calling `go("/settings")` via `agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl#L147` and `dashboard-lifecycle.js#L832-L843`) or any rail layer gear button (`global`, `project`, `repo`, `session` calling `go(window.WsShell.settingsHref(...))` to `/settings?...`, `dashboard.tmpl#L477,L615`, `dashboard-rail.js#L531-L539`) switches `#frames`'s active iframe to `/settings` (hiding cacheable session iframes or tearing down uncacheable ones via `shOpen` in `dashboard-lifecycle.js#L939-L949`).

Switching `#frames` away from the active session iframe has two harmful side effects:
- If the active session has open xterm terminal tabs, unsent drafts, open board cards, or support chats, navigating to Settings either prompts a leave warning or tears down the live session iframe.
- Even when the session iframe is cached, marking it `shown: false` stops its WebSocket stream (there is no SSE/`EventSource`, `agents-plugin-tool/internal/wsdashboard/stream_page_test.go#L64,L78-L79`; `syncMainStream` stops `mainStream` in `dashboard-stream.js#L380-L386`), pauses companion relay (`dashboard-companion-relay.js#L356`), and suspends doc frames (`dashboard-lifecycle.js#L1345`, `dashboard-frames.js#L191-L200`) until the user navigates back.

Converting Workflow Settings into a floating modal hosted by the dashboard shell allows inspecting and editing every config layer (`global`, `project`, `repo`, `session`) without interrupting the active session underneath.

## Decisions

- **Shell-hosted `<dialog>` modal with `/settings?frame=1` iframe (`(1505)=a`, superseding commit `ed920bb2`):** Host Workflow Settings inside a top-level `<dialog>` in the dashboard shell opened with `showModal()`, styled with `display: flex; flex-direction: column` (explicitly required for WKWebView layout compatibility), wrapping an iframe pointed at `/settings?frame=1&...`.
  - *Rejected:* Mounting Settings DOM directly inside the session document (risks CSS/JS collisions, bloats initial session bundle with `Settings*` assets, and complicates `.tr-modal` isolation); keeping `#frames` page replacement (from `ed920bb2`; disrupts live WebSocket/xterm/drafts).
- **Zero disruption to active session:** Opening, switching layers inside, or closing the Settings modal must keep the underlying active session iframe `shown: true` with no reload, preserving WebSocket streams, xterm PTYs, composer drafts, and scroll positions.
- **Unified entry points and in-place layer switching:** Keep existing entry points (`#gmenu` Workflow Settings and rail `global`/`project`/`repo`/`session` gear buttons) as well as internal settings layer switching. All of them open the shell `<dialog>` (if not open) and update only the modal iframe `src` in place rather than navigating `#frames`.
- **Dismissal, layered `Esc`, and silent draft persistence (`(1512)=a`, superseding commit `1a508ee8` leave-confirmation contract):**
  - Add a close (`×`) button in the settings header (`.shd-top`).
  - Support closing the modal via the `×` button, clicking the `<dialog>` backdrop, or pressing `Esc`.
  - Enforce layered `Esc` precedence: active inner controls inside `/settings` (`.tr-modal` tier-routing modal, open combobox dropdowns, or clearing an active search input) consume `Esc` first; only when no inner overlay/input consumes `Esc` does the outer Settings `<dialog>` close.
  - Preserve `repo` proposal drafts stored on `window.parent.wsSettingsRepoDrafts` across modal close/reopen and in-place layer switches. Because staged drafts are preserved non-destructively, both modal dismissal and in-place layer switching proceed silently without invoking `wsConfirm("js.draft.leave")` (explicitly superseding the leave-confirmation assertions in `settings.browser.cjs#L323-L326` and `#L355-L359`, commit `1a508ee8`).
  - *Rejected:* Invoking the parent shell's `wsConfirm("js.draft.leave")` prompt before closing the modal or switching away from `repo` with staged proposals (adds blocking friction without preventing data loss once drafts survive in `window.parent.wsSettingsRepoDrafts`).
- **URL preservation and direct cold-load `/settings` lifecycle (`(1513)=a`, superseding commit `ed920bb2` Back/Forward history stack):**
  - Opening Settings from the dashboard UI keeps the current browser URL (e.g., `/s/...`) unchanged and never pushes browser history entries.
  - Direct cold-load navigation to `/settings?...` (bookmarks, reloads, localhost `d.LoginLink` / `loginNavigation` redirect links preserving `/settings?...` in `agents-plugin-tool/internal/wsdashboard/localhost_auth.go#L170-L185,L258-L284`; LAN URLs in `access.go#L262` target `/?token=...`) mounts the dashboard index shell behind the dialog and automatically opens the Settings modal for that query.
  - While cold-loaded at `/settings?...` (`location.pathname === "/settings"`), internal layer switches (`scopeButton`, layer drawer, `#project=` hash) update `location` in place via `history.replaceState` so `page.reload()` stays on the selected layer (preserving `settings.browser.cjs#L265-L273` and `tier-routing.browser.cjs#L167,L353`).
  - Closing an auto-opened cold-load modal rewrites `location` to `/` via `history.replaceState` so a subsequent reload stays on the dashboard rather than reopening Settings.
  - Settings modal open, layer switch, and close never call `history.pushState`, explicitly superseding browser Back/Forward traversal between Settings layers (`settings.browser.cjs#L281-L282`, `ed920bb2`); on `popstate`, navigating away from `/settings` closes the modal and returning to `/settings?...` opens it.
  - *Rejected:* Leaving cold-load `location` static during internal layer switches (breaks reload persistence in `settings.browser.cjs#L265-L273` and `tier-routing.browser.cjs#L167,L353`); keeping a dual-mode `pushState` history stack only for cold-load sessions (unnecessary state-machine complexity for a modal overlay).
- **Modal-framed shell messaging, layout stripping, `#project=` source, and inert-rail/`Esc` guards (`(1514)`):** Treat the Settings modal iframe as a dedicated shell surface distinct from `#frames`:
  1. Add a dedicated `postMessage` branch in `dashboard-lifecycle.js` for the Settings modal iframe (rather than routing through `shEnt(e.source)` / `#frames` head/title/layout handlers, which would either drop messages or overwrite the active session header/state), and forward theme broadcasts to the modal iframe.
  2. In modal mode, hide `.top` and disable the 340px docked-rail layout column at `>=1600px` (`dashboard.tmpl#L627-L628`, `dashboard-settings.css#L938-L953`).
  3. Read `#project=` in `dashboard-settings.js#L170` from the modal iframe's own `location.hash` (or modal state) instead of `window.parent.location.hash` when modal-framed, because the parent URL stays on the underlying session.
  4. Suppress `Ctrl+\`` (`key: "rail"`) forwarding (`dashboard-settings.js#L149-L154`) while modal-framed (the shell `#rail` is inert behind `showModal()`), and guard document `keydown` `Escape` forwarding so open `.tr-modal` (`dashboard-settings-tiers.js#L568-L588`), open comboboxes, or active search inputs consume `Esc` before any close request reaches the parent `<dialog>`.
  - *Rejected:* Registering the Settings modal iframe inside `shFrames` / `shEnt` (causes head/title/layout messages to overwrite the active session) or reading `window.parent.location.hash` (stale session hash breaks `#project=` selection in modal mode).
- **Compatibility tier: T0 (transparent).** Go `/api/config/*` JSON endpoints and config manifest/registry schemas are unchanged. Changes stay within dashboard templates, shell/settings JS & CSS, `/settings` cold-load shell rendering, and tests.

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin/config-manifest.json, agents-plugin/agy-config-manifest.json, agents-plugin/dashboard-config-manifest.json, agents-plugin-pi/config-manifest.json, agents-plugin-tool/internal/wsdashboard/config_routes.go, agents-plugin-tool/internal/wsdashboard/config_proposals.go, agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)
- **Required manuals (read before editing):**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/config-settings-ui.md`
  - `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`)
  - `ai-docs/manuals/release-compatibility.md`
- **Invariants:**
  - `/api/config/*` routes and payload contracts remain unchanged.
  - `Settings*` JS/CSS assets remain lazy-loaded only inside the `/settings?frame=1` iframe and are not added to initial session page loads.
  - `.tr-modal` (tier-routing modal) and `window.parent.wsSettingsRepoDrafts` continue working identically inside the modal iframe.
  - WKWebView `<dialog>` sizing uses `display: flex; flex-direction: column` without clipped or collapsed iframe height.
- **Verification environment:** On Windows, run Go tests (including Go-launched Chromium Playwright wrappers for `testdata/settings*.browser.cjs` and `testdata/tier-routing.browser.cjs` with `WS_PLAYWRIGHT_MODULE` set in `agents-plugin-tool/internal/wsdashboard/config_settings_browser_test.go#L107`, `config_settings_production_browser_test.go#L92`, `settings_combobox_browser_test.go#L76`, `settings_test.go#L116`, and `tier_routing_browser_test.go#L121`) only on remote `w5090` (never local `go test`); run targeted `node:test` suites (`frontend-settings.test.js`) locally via `node --test`.

## Prior Decisions

- ed920bb2 (2026-11-21, commit): "Keep settings as a fresh dashboard shell frame and carry the exact selection and inspection IDs through frame navigation and browser history." — bearing: contradiction-candidate
- 1a508ee8 (2026-11-22, commit): "Test: settings.browser.cjs (TestSettingsInteractionBrowser). It stages a proposal, opens a rail session, confirms the leave dialog (나가기), waits for the session frame and for the Settings iframe" — bearing: constrains
- b5d74f83 (2026-11-22, commit): "Mounted fixed-position popup in input.closest('dialog') || document.body so it avoids table/scroll clipping and renders inside <dialog>.showModal() browser top layer" — bearing: supports
- 261122-feat-ws-companion-director (2026-11-22, Result): "The shell page embeds the modal rows instead of loading the settings catalog, because it loads no catalog and drops js.settings.*." — bearing: constrains
- 261116-feat-ws-dashboard-config-settings-ui (2026-11-21, Result): "Delivered in-memory worktree proposals with explicit reset operations, mailbox delivery to the active lead, and copy and manual selection fallback. No repo config file writes occurred." — bearing: constrains
- 261121-feat-ws-dashboard-inpage-dialogs (2026-11-21, Decisions): "Reuse one in-page HTML <dialog> confirmation helper across the ten browser confirmation sites. Use it for the draft decision before a destructive iframe swap in Phase 2." — bearing: supports
- 261115-feat-ws-dashboard-settings-in-usage-strip (2026-11-15, Result): "Moved Settings to the leftmost rail-footer gear; retained unrelated drawer cards, all preferences and the usage popover." — bearing: constrains
- 261113-feat-ws-dashboard-iframe-shell (2026-11-14, Result): "The shell dims its rail and header by the frame's modal backdrop alpha (layout.dim), so overlays read window-wide." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/assets/dashboard.tmpl, dashboard-shell.css, shell.js, dashboard-lifecycle.js, dashboard-rail.js, dashboard-settings.js, dashboard-settings.css, config_routes.go, frontend-settings.test.js, testdata/settings*.browser.cjs, testdata/tier-routing.browser.cjs |
| scope.surface | internal | changes stay within internal/wsdashboard routes, templates, frontend scripts/CSS, and tests; Go /api/config/* endpoints and manifest schemas unchanged |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/frontend-settings.test.js, testdata/settings*.browser.cjs, testdata/tier-routing.browser.cjs, shell_test.go, shell_frame_test.go, settings_test.go |
| complexity.reuse_points | confirmed | reuses /settings?frame=1 template/assets, dashboard-settings-combobox.js dialog mounting, dashboard-settings-tiers.js .tr-modal, window.parent.wsSettingsRepoDrafts, and WsShell postMessage bridge |
| complexity.side_effect_risk | moderate | modifies global shell navigation, iframe focus/Esc forwarding, direct /settings shell rendering, and repo proposal persistence across modal close/reopen |
| risk.correctness | moderate | must preserve layered Esc precedence, WKWebView <dialog> flex iframe layout, active session shown state, and direct cold-load /settings?... parity |
| risk.fit | moderate | must integrate cleanly with dashboard-lifecycle.js frame messaging, dashboard-rail.js gear entries, .tr-modal top-layer stacking, and window.wsNativeCloseReady |
| risk.test | moderate | UI-only node:test checks in frontend-settings.test.js cannot exercise real browser <dialog> showModal or WKWebView sizing; full flow requires Chromium Playwright wrappers on w5090 |
| risk.security_or_contract | moderate | changes /settings cold-load shell rendering and postMessage/Esc handling while preserving localhost login redirect and T0 same-origin /api/config/* contracts |

## Phases

### Phase 1: Shell floating `<dialog>` modal for Workflow Settings across all layers

Implement the shell-hosted `<dialog>` modal for `/settings?frame=1` across all config layers and update tests.

**Goals:**
1. **Shell modal host (`dashboard.tmpl`, `dashboard-shell.css`, `shell.js`, `dashboard-lifecycle.js`, `dashboard-rail.js`):**
   - Add the top-level Settings `<dialog>` host (`showModal()`, `display: flex; flex-direction: column` for WKWebView) wrapping the `/settings?frame=1&...` iframe.
   - Route `#gmenu` Workflow Settings and rail layer gears (`global`, `project`, `repo`, `session`) to open the modal and swap the modal iframe `src` in place without calling `go("/settings?...")` or toggling the active session iframe's `shown` state.
   - Preserve the underlying browser URL while opening, switching layers, or closing from UI triggers (never calling `history.pushState` for modal open/switch/close). When cold-loaded on `/settings?...` (`location.pathname === "/settings"`), sync internal layer switches via `history.replaceState`, reset `location` to `/` via `history.replaceState` on modal close, and in `popstate` close the modal when leaving `/settings` or open it when returning to `/settings?...`.
   - Add a dedicated `postMessage` branch in `dashboard-lifecycle.js` for the modal iframe (handling close requests and modal-scoped messages without touching the active session's header/title/layout state, and forwarding theme broadcasts to the modal iframe), handle backdrop clicks, and restore focus when the modal closes.
2. **Settings iframe integration (`dashboard-settings.js`, `dashboard-settings.css`, `dashboard-settings-extras.js`):**
   - Add a `×` close button in `.shd-top` (visible when framed in the modal) that notifies the parent shell to close the `<dialog>`.
   - Strip `.top` and disable the 340px docked-rail column (`>=1600px`) when modal-framed (`dashboard.tmpl#L627-L628`, `dashboard-settings.css#L938-L953`).
   - Read `#project=` from the modal iframe's own `location.hash` (or modal state) instead of `window.parent.location.hash` when modal-framed (`dashboard-settings.js#L170`), and ensure internal layer switching updates the modal iframe in place without escaping to `#frames` navigation.
   - Suppress `Ctrl+\`` (`key: "rail"`) forwarding while modal-framed (`dashboard-settings.js#L149-L154`), and implement layered `Esc` handling so open `.tr-modal` (`dashboard-settings-tiers.js#L568-L588`), open comboboxes, and active search-input clear consume `Esc` before forwarding close to the parent `<dialog>`.
   - Keep `window.parent.wsSettingsRepoDrafts` intact across modal close/reopen and in-place layer switches, allowing silent modal close and layer switching without leave confirmation (`(1512)=a`).
3. **Direct `/settings?...` cold-load compatibility (`config_routes.go` and shell boot):**
   - When a browser loads `/settings?...` directly (non-`frame=1`), serve the dashboard index shell configured to auto-open the Settings `<dialog>` with the requested `/settings?frame=1&...` query so bookmarks, reloads, and localhost login redirect links (`agents-plugin-tool/internal/wsdashboard/localhost_auth.go#L170-L185,L258-L284`) continue to work, following the `(1513)=a` `replaceState`-only lifecycle.
4. **Tests (`frontend-settings.test.js`, `settings*.browser.cjs`, `tier-routing.browser.cjs`, and Go route/asset tests):**
   - Update existing settings tests for the modal lifecycle and add assertions covering:
     - Opening and switching across `global`/`project`/`repo`/`session` layers keeps the underlying session iframe `shown: true` and preserves browser URL on UI-triggered opens.
     - `×` button, backdrop click, and layered `Esc` (with `.tr-modal`, combobox, and search clear taking priority) work cleanly.
     - Silent modal close and in-place layer switching with staged `repo` proposals preserve `window.parent.wsSettingsRepoDrafts` across close/reopen and layer switches (explicitly superseding the leave-confirmation assertions in `settings.browser.cjs#L323-L326,L355-L359` from `1a508ee8`).
     - Direct cold-load `/settings?...` renders the shell, auto-opens the Settings modal, syncs layer switches via `history.replaceState` so `page.reload()` preserves the selected layer (`settings.browser.cjs#L257-L273`, `tier-routing.browser.cjs#L167,L353` — splitting `settings.browser.cjs#L257-L273` so cold-load layer-switch `replaceState` + `page.reload()` starts from a direct `page.goto('/settings?...')` entry separately from UI-triggered rail-gear opens that keep the underlying URL unchanged), rewrites `location` to `/` via `history.replaceState` on modal close, and supersedes Settings-layer Back/Forward `pushState` assertions (`settings.browser.cjs#L281-L282`, `ed920bb2`).

**Verification:**
- Remote `w5090` Go tests covering `internal/wsdashboard` config routes, shell rendering, and asset assembly (never local `go test` on Windows).
- Local targeted `node --test` covering `agents-plugin-tool/internal/wsdashboard/frontend-settings.test.js` (`testdata/settings*.browser.cjs` and `testdata/tier-routing.browser.cjs` are standalone Playwright scripts launched by Go tests with daemon args and `WS_PLAYWRIGHT_MODULE`, not `node --test` suites).

### Result (9eaf78687) - 2026-11-23

Workflow Settings now opens in a shell-hosted dialog from the global menu and
all four rail layers. Settings navigation keeps the existing session iframe
shown and preserves the browser address. A direct `/settings` visit opens the
modal over the index and uses replacement history for layer changes and
dismissal. Close, backdrop, and layered Escape handling preserve staged repo
proposals in the parent window.

- Verified the dashboard build, route and asset assembly checks, 15 Settings
  Node tests, and ten targeted Go tests, including the five headless Chromium
  suites. The lead approved macOS verification because `w5090` was unavailable
  from this host. The eight known unrelated frontend fixture failures stayed
  outside this run.
- Captured and inspected `/tmp/settings-modal-shots/global-1200.png`,
  `/tmp/settings-modal-shots/global-760.png`,
  `/tmp/settings-modal-shots/rail-1200.png`, and
  `/tmp/settings-modal-shots/rail-760.png`. Chromium reached and opened the
  final category through the horizontal strip at 760px from both entry points.
- The production fixture's agy manifest count now includes the two keys added
  by `295e953b2`. The combobox fixture serves an empty tracker list for the
  newly mounted index behind Settings.
- Independent lite review reported no actionable findings. It inspected
  focus restoration and popstate statically.
