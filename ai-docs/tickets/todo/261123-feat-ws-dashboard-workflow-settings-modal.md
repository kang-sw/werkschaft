---
title: "워크플로 설정창 전 레이어 플로팅 모달 전환"
---

# Convert Workflow Settings Into a Floating Shell Modal Across All Layers

## Background

Today, opening Workflow Settings from `#gmenu` (`⚙ 워크플로 설정`) or any rail layer gear button (`global`, `project`, `repo`, `session`) calls `go("/settings?...")` and replaces the active session iframe in `#frames`.

Replacing the active session iframe has two harmful side effects:
- If the active session has open xterm terminal tabs, unsent drafts, open board cards, or support chats, navigating to Settings either prompts a leave warning or tears down the live session iframe.
- Even when the session iframe is cached, marking it `shown: false` suspends SSE/WebSocket updates, companion rendering, and doc frames until the user navigates back.

Converting Workflow Settings into a floating modal hosted by the dashboard shell allows inspecting and editing every config layer (`global`, `project`, `repo`, `session`) without interrupting the active session underneath.

## Decisions

- **Shell-hosted `<dialog>` modal with `/settings?frame=1` iframe (`(1505)=a`):** Host Workflow Settings inside a top-level `<dialog>` in the dashboard shell opened with `showModal()`, styled with `display: flex; flex-direction: column` (explicitly required for WKWebView layout compatibility), wrapping an iframe pointed at `/settings?frame=1&...`.
  - *Rejected:* Mounting Settings DOM directly inside the session document (risks CSS/JS collisions, bloats initial session bundle with `Settings*` assets, and complicates `.tr-modal` isolation); keeping `#frames` page replacement (disrupts live SSE/WS/xterm/drafts).
- **Zero disruption to active session:** Opening, switching layers inside, or closing the Settings modal must keep the underlying active session iframe `shown: true` with no reload, preserving SSE/WS streams, xterm PTYs, composer drafts, and scroll positions.
- **Unified entry points and in-place layer switching:** Keep existing entry points (`#gmenu` Workflow Settings and rail `global`/`project`/`repo`/`session` gear buttons) as well as internal settings layer switching. All of them open the shell `<dialog>` (if not open) and update only the modal iframe `src` in place rather than navigating `#frames`.
- **Dismissal, layered `Esc`, and draft persistence:**
  - Add a close (`×`) button in the settings header (`.shd-top`).
  - Support closing the modal via the `×` button, clicking the `<dialog>` backdrop, or pressing `Esc`.
  - Enforce layered `Esc` precedence: active inner controls inside `/settings` (`.tr-modal` tier-routing modal, open combobox dropdowns, or clearing an active search input) consume `Esc` first; only when no inner overlay/input consumes `Esc` does the outer Settings `<dialog>` close.
  - Preserve `repo` proposal drafts stored on `window.parent.wsSettingsRepoDrafts` across modal close and reopen.
- **URL preservation and direct cold-load `/settings` compatibility:**
  - Opening Settings from the dashboard UI keeps the current browser URL (e.g., `/s/...`) unchanged.
  - Direct cold-load navigation to `/settings?...` (bookmarks, reloads, LAN login redirect links) mounts the dashboard index shell behind the dialog and automatically opens the Settings modal for that query.
- **Compatibility tier: T0 (transparent).** Go `/api/config/*` JSON endpoints and config manifest/registry schemas are unchanged. Changes stay within dashboard templates, shell/settings JS & CSS, `/settings` cold-load shell rendering, and tests.

## Constraints

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
- **Verification environment:** On Windows, run Go tests only on remote `w5090` (never local `go test`); run targeted JS/Playwright tests locally via `node --test`.

## Phases

### Phase 1: Shell floating `<dialog>` modal for Workflow Settings across all layers

Implement the shell-hosted `<dialog>` modal for `/settings?frame=1` across all config layers and update tests.

**Goals:**
1. **Shell modal host (`dashboard.tmpl`, `dashboard-shell.css`, `shell.js`, `dashboard-lifecycle.js`, `dashboard-rail.js`):**
   - Add the top-level Settings `<dialog>` host (`showModal()`, `display: flex; flex-direction: column` for WKWebView) wrapping the `/settings?frame=1&...` iframe.
   - Route `#gmenu` Workflow Settings and rail layer gears (`global`, `project`, `repo`, `session`) to open the modal and swap the modal iframe `src` in place without calling `go("/settings?...")` or toggling the active session iframe's `shown` state.
   - Preserve the underlying browser URL while opening, switching layers, or closing from UI triggers.
   - Handle backdrop clicks, `postMessage` close requests from the settings iframe, and focus restoration when the modal closes.
2. **Settings iframe integration (`dashboard-settings.js`, `dashboard-settings.css`):**
   - Add a `×` close button in `.shd-top` (visible when framed in the modal) that notifies the parent shell to close the `<dialog>`.
   - Ensure internal layer switching updates the modal iframe in place without escaping to `#frames` navigation.
   - Implement layered `Esc` handling so `.tr-modal`, open comboboxes, and active search-input clear consume `Esc` before forwarding close to the parent `<dialog>`.
   - Keep `window.parent.wsSettingsRepoDrafts` intact across modal close/reopen.
3. **Direct `/settings?...` cold-load compatibility (`config_routes.go` and shell boot):**
   - When a browser loads `/settings?...` directly (non-`frame=1`), serve the dashboard index shell configured to auto-open the Settings `<dialog>` with the requested `/settings?frame=1&...` query so bookmarks, reloads, and LAN login links continue to work.
4. **Tests (`frontend-settings.test.js`, `settings*.browser.cjs`, `tier-routing.browser.cjs`, and Go route/asset tests):**
   - Update existing settings tests for the modal lifecycle and add assertions covering:
     - Opening and switching across `global`/`project`/`repo`/`session` layers keeps the underlying session iframe `shown: true` and preserves browser URL.
     - `×` button, backdrop click, and layered `Esc` (with `.tr-modal`, combobox, and search clear taking priority) work cleanly.
     - `wsSettingsRepoDrafts` persists across modal close and reopen.
     - Direct cold-load `/settings?...` renders the shell and auto-opens the Settings modal.

**Verification:**
- Remote `w5090` Go tests covering `internal/wsdashboard` config routes, shell rendering, and asset assembly (never local `go test` on Windows).
- Local targeted `node --test` covering `frontend-settings.test.js`, `settings*.browser.cjs`, and `tier-routing.browser.cjs`.
