# Plan: Fix `#tkdrawer[open]` Shell-Frame Overlay Contract Regression (`0522-tkdrawer-overlay-fix`)

## Context & Problem
The 0.52.2 release-gate review (`C:\Users\user\.cache\ws@kang-sw-devenv\proj\de04b4ac\review-paths\2002f3f6-01-findings.md`) passed all of `af9578df1..b4a3e5e84` and 3 of 4 items in `3ba7e1889`, but flagged 1 Important regression in `3ba7e1889` (`6d1ba1f03`):

- `shFrameInit()` (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-lifecycle.js:1436`) added `#tkdrawer[open]` to its `popover` query so the shell sets `:root[data-shell][data-fpopover]` and lifts `.frames` (`position: fixed; inset: 0`) to `z-index: 60` (`var(--z-popovers)`) over `.dwrap` (`z-index: 1`, which owns `.top` and `#rail`).
- Because an `<iframe>` captures all pointer events across its full `inset: 0` viewport box in the parent document even over transparent areas, and `#tkdrawer` currently has **no outside-click dismiss handler** (`tracker-mount.js:125-132` returns early on `if (!own(t)) return;`), opening `#tkdrawer` leaves the visible shell `.top` header and `#rail` controls as an unresponsive pointer dead zone.
- Additionally, pressing `Ctrl+Backquote` inside the iframe calls `railKey()` -> `shPost("key", { key: "rail" })` (`dashboard-lifecycle.js:1481,1316`), which opens the shell's `.rail-open` rail + memo pad + `.rscrim` (`memo.css:11-14`) inside `.dwrap` (`z-index: 1`) trapped **behind** the still-lifted `.frames` (`z-index: 60`).
- Minor review finding #6 (`tracker-render.js:167`): no unit test directly exercises `tracker-render.js` syncing/removing the `open` boolean attribute on `#tkdrawer`.

## Scope & Implementation Steps

1. **Read relevant manuals & source first:**
   - Read `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`), `ai-docs/manuals/dashboard-style.md`, and `ai-docs/manuals/shipped-surface-boundary.md`.
   - Inspect `agents-plugin-tool/internal/wsdashboard/assets/dashboard-drawer.js` (lines 500–520, `#drawer` outside-click dismiss pattern), `agents-plugin-tool/internal/wsdashboard/assets/tracker-mount.js` (lines 110–160 and how `#tkdrawer` open/close/fold works), `agents-plugin-tool/internal/wsdashboard/assets/tracker-render.js` (lines 155–190), `agents-plugin-tool/internal/wsdashboard/assets/tracker-state.js` / `tracker-nav.js` (or whichever file exposes `fold()`/`toggle()`/`close()`), and `agents-plugin-tool/internal/wsdashboard/assets/dashboard-lifecycle.js` (lines 1290–1520, `shFrameInit`, `railKey`, shell message handling).

2. **Fix 1 — Outside-click dismiss handler for `#tkdrawer` (`tracker-mount.js`):**
   - Align `#tkdrawer` with `data-fpopover`'s transient overlay contract (matching `#drawer` in `dashboard-drawer.js:505-510`).
   - When `#tkdrawer` is open (`ui.open` / `#tkdrawer[open]`) and a click (or pointerdown/click — match `dashboard-drawer.js` carefully so drag/resize handles and tracker sub-overlays are never broken!) lands outside `#tkdrawer` and outside every tracker-owned overlay/trigger element (`#tkdrawer`, `#tkclip`, `#tkhandles`, `#tkmenu`, `#tkcap`, `#tkwin`, `#tkdlg`, `#tkpm`, `#tkorged`, `#tkqpop`, plus check for any other `#tk*` popovers/triggers like `#tkqs` or `.tk-handle` in `tracker-mount.js` / `own(t)`!), fold/close `#tkdrawer` cleanly (using the exact same state path as `.tk-fold` / `Escape` so `ui.open = false`, `#tkdrawer.removeAttribute("open")`, and `shFrameInit`'s MutationObserver lowers `.frames` immediately).
   - Be careful:
     - Do NOT break clicking inside `#tkdrawer`, dragging/resizing `#tkhandles`, or interacting with tracker dialogs/popups (`#tkmenu`, `#tkcap`, `#tkwin`, `#tkdlg`, `#tkpm`, `#tkorged`, `#tkqpop`, `#tkclip`, or any detached modals).
     - Do NOT break the click that OPENS `#tkdrawer` (e.g., clicking `#tkclip` / `#tkqs` / drawer trigger).
     - Do NOT add heavy per-event DOM scans or forced reflows (follow `performance.md`).

3. **Fix 2 — Collapse open `#tkdrawer` on `Ctrl+Backquote` / rail-open handoff (`dashboard-lifecycle.js`):**
   - In `dashboard-lifecycle.js` (`railKey()` around line 1481, and any shell->frame message that opens `.rail-open`/memo if applicable), if `#tkdrawer` is currently open when `Ctrl+Backquote` is pressed, collapse/close `#tkdrawer` (via the tracker close API / hook, or clicking `.tk-fold` / dispatching the tracker fold action so tracker state `ui.open` stays in sync, NOT merely stripping the DOM attribute out of sync with `ui.open`!) BEFORE (or together with) posting `shPost("key", { key: "rail" })`, so `data-fpopover` clears immediately and `.rail-open` + memo pad are never trapped behind `.frames`.
   - Also check the parent-shell side (`dashboard-lifecycle.js` where `Ctrl+Backquote` is handled directly on the shell document or when `railOpen` toggles to true): if the user presses `Ctrl+Backquote` while focus is in the shell (or opens `.rail-open`), make sure any active frame's `#tkdrawer` is also closed or cannot stay lifted over `.rail-open`!

4. **Fix 3 — Regression tests (also resolves Minor finding #6):**
   - Add direct unit test coverage for `tracker-render.js` toggling/removing the `open` boolean attribute on `#tkdrawer` when `ui.open` changes (and not redundantly mutating when unchanged).
   - Add unit test coverage for `#tkdrawer` outside-click dismissal (dismisses on outside click, preserves open state on clicks inside `#tkdrawer` and every tracker overlay `#tkclip`, `#tkhandles`, `#tkmenu`, `#tkcap`, `#tkwin`, `#tkdlg`, `#tkpm`, `#tkorged`, `#tkqpop`).
   - Add unit test coverage for `Ctrl+Backquote` (`railKey()`) collapsing an open `#tkdrawer` before/when posting `shPost("key", { key: "rail" })` so `data-fpopover` clears.
   - Keep `frontend-idle.test.js` CSS `:has()` guard and `shell-post-layout.test.cjs` green.

## Constraints & Verification
- **T0 compatibility only.** Pure frontend JS + tests.
- **No `Co-Authored-By` trailer** in commits (Pi/agy worker policy).
- **Windows AV rule (CRITICAL):** NEVER run local `go`, `go test`, `go build`, `go vet`, or `gofmt` on this Windows machine. Run Node tests locally (`node --test`), and run Go verification on remote `w5090`:
  ```bash
  bash D:/sys_lock.sh remote w5090 run 0522-tkdrawer-fix 12 --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/tassel-sneak-enjoyably' -- 'export PATH=$HOME/sdk/go/bin:$HOME/.local/node/bin:$PATH; cd agents-plugin-tool && go vet ./internal/wsdashboard && go test -race ./internal/wsdashboard -run "TestShell|TestTracker|TestDrawer|TestPinCol|TestFrontend"'
  ```
- Commit cleanly on `impl/develop/0522-tkdrawer-overlay-fix` including this unchanged plan file (`ai-docs/.plans/2026-11/25-1655-0522-tkdrawer-overlay-fix.md`), do NOT merge or push, and report verbatim Node and `w5090` verification output.
