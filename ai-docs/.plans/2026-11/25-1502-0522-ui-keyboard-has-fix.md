# Plan: Fix 0.52.2 UI Keyboard Latency Regression (`0c9eed204` `:has()` Rules + `xhTickText` Layout Skip + Guard)

## Goal
Eliminate the 6-second keyboard typing latency regression introduced on `develop` (measured in `Trace-20261125T143528.json.gz`: ~107ms/keystroke `UpdateLayoutTree(8,153 elems) + Layout(577/9,204)` caused by `0c9eed204`'s sibling `:has(+ .gap-range:empty)` and remaining sibling/root `:has()` selectors), skip redundant 1-second forced layout ticks in `dashboard-exec-hud.js`, and add a static regression test in `frontend-idle.test.js` preventing sibling/root/`:empty` `:has()` selectors from re-entering dashboard/whiteboard stylesheets. Strictly T0-compatible, on branch `impl/develop/0522-ui-keyboard-has-fix`.

## Required Reading Before Editing
1. `AGENTS.md`
2. `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`)
3. `ai-docs/manuals/dashboard-style.md`
4. `ai-docs/manuals/shipped-surface-boundary.md`
5. Git commits `e828db7b7` (prior Blink sibling `:has(+ ...)` fix in `page-panel-diff.css`) and `0c9eed204` (commit that introduced the `page-stream.css` regression).

## Scope of Changes (Owner Decision `(1602)=a`)

### 1. Eliminate Sibling `:has(+ ...)` / `:has(...:empty)` and Document-Root `:has(...)` Selectors
Inspect ALL `agents-plugin-tool/internal/wswhiteboard/assets/*.css` and `agents-plugin-tool/internal/wsdashboard/assets/*.css` for `:has(` usage and replace every sibling/root/high-ancestor/`:empty` `:has()` selector with explicit classes or `data-*` attributes maintained by the owning SSR template and JS code:

1. **`agents-plugin-tool/internal/wswhiteboard/assets/page-stream.css` (around L83–94 — primary `0c9eed204` regression):**
   - Replace `.gap-sep:has(+ .gap-range:empty)` and the `.history-gap:has([data-load=...])` rules with explicit classes or `data-*` attributes set by the history-gap renderer (inspect ALL Go SSR templates and client JS in `wswhiteboard/` — e.g. `stream.js`, templates, view-model builders — that render or mutate `.history-gap`, `.gap-sep`, `.gap-range`, `[data-load]`).
   - Preserve exact visual behavior in all cases (load-up only, load-down only, both directions, empty `.gap-range` hides `.gap-sep`).
2. **`agents-plugin-tool/internal/wsdashboard/assets/dashboard-companion.css` (around L198):**
   - Replace `.rail[data-companion-dock="unregistered"] .rlist > :has(+ .rws-unreg)` without any sibling `:has(+)` (e.g., style `.rws-unreg` directly or toggle an explicit class on the preceding item in the rail renderer, preserving exact visual spacing/dividers).
3. **Document-wide / high-ancestor `:has(...)` selectors across `wsdashboard/assets/*.css`:**
   - `dashboard-layout.css` (around L178–180, L231–237, L444): `body:has(#toolbar-files[data-max][data-docked])`, `body:has(.fpane[data-max])`, `body:has(.bcard[data-kind="explorer"][data-max])`
   - `dashboard-drawer.css` (around L90): `:root[data-shell]:has(.settings-pop:not([hidden])) .frames`
   - `dashboard-account-consent.css` (around L17–22): `:root:has(> body > .dwrap > .account-banner:not([hidden]))`
   - `dashboard-rail.css` (around L95, L191–205): `.dash:has(...)`, `.dbody:has(...)`
   - Check every other `:has(` across both `wsdashboard/assets/*.css` and `wswhiteboard/assets/*.css`; replace any remaining sibling (`+`/`~`), `:empty`, or root/body/shell/dash/dbody `:has(...)` rules.
   - For every replacement, verify BOTH initial SSR state (if an element can start shown/open/maximized on page load) AND every JS mutation site that toggles `[data-max]`, `[data-docked]`, `[hidden]`, etc., keeping the toggled `data-*`/class strictly in sync (including teardown/close/disconnect paths).

### 2. Skip Forced Layout on 1-Second `xhTickText` Clock Ticks
- **File:** `agents-plugin-tool/internal/wsdashboard/assets/dashboard-exec-hud.js` (around L215–275: `xhRender`, `xhTickText`, `xhLayout`, `xhLayoutNow`)
- **Fix:**
  - When the 1-second timer runs `xhTickText()` purely to update elapsed-time text on unchanged chips, do NOT invoke `xhLayout()` / `xhLayoutNow()` (which previously forced synchronous layout every second).
  - Verify elapsed-time labels use `font-variant-numeric: tabular-nums` (add if missing in CSS) so text updates don't jitter; continue calling `xhLayout()` whenever chip membership/count/visibility/state changes or on container resize.

### 3. Static CSS Regression Guard Test
- **File:** `agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js` (and inspect existing tests there to match style)
- **Fix:**
  - Add a static guard test scanning ALL `*.css` files under `agents-plugin-tool/internal/wsdashboard/assets/` and `agents-plugin-tool/internal/wswhiteboard/assets/` (stripping CSS comments before matching) and failing with an actionable message citing `ai-docs/manuals/performance.md` if any selector contains:
    - sibling `:has(` (`:has(\s*[+~]`),
    - `:has(` combined with `:empty`, or
    - high-ancestor `:has(` on `:root`, `html`, `body`, `.dash`, `.dbody`, `.dwrap`, `.dgrid`.
  - Add/update JS unit tests covering the new explicit attribute/class sync or `xhTickText` non-layout behavior where existing Node test harnesses exist.

## Out of Scope (Explicitly Deferred per `(1602)=a`)
- Do NOT add `contain: strict` or `content-visibility: auto` to `#convo` (Rank 2).
- Do NOT refactor `freeApplyOffWindow` in `dashboard-board-anchor.js` (Rank 3).

## Verification Requirements (STRICT Windows AV Host Constraints)
- **NEVER run `go`, `go test`, `go build`, `go vet`, `gofmt`, or raw `ws-mcp.exe` locally on Windows!** (AhnLab V3 AV rule).
- Run Node tests locally (Node is allowed if installed; check with `node --test`):
  - `node --test agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js`
  - Any other affected `*.test.js` / `test-*.cjs` files under `wsdashboard` or `wswhiteboard`.
- Run ALL Go formatting, vet, and `-race` tests on remote `w5090` via `sys_lock.sh`:
  ```bash
  D:/sys_lock.sh remote w5090 run 0522-ui-has 20 \
    --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/porthole-diabetic-banjo' \
    -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && \
        test -z "$(gofmt -l internal/wsdashboard internal/wswhiteboard)" && \
        go vet ./internal/wsdashboard ./internal/wswhiteboard && \
        go test -race ./internal/wswhiteboard ./internal/wsdashboard ./internal/wsi18n ./internal/wsrsrc'
  ```
- Keep any temporary files under `D:/Temp/<purpose>/` and delete them before returning.

## Git & Return Rules
- Commit cleanly on `impl/develop/0522-ui-keyboard-has-fix` using `ws/git.commit(expected_branch: "impl/develop/0522-ui-keyboard-has-fix", paths: [...], title: ..., ai_context: [...])`.
- Include this plan file (`ai-docs/.plans/2026-11/25-1502-0522-ui-keyboard-has-fix.md`) in the commit's `paths`.
- **NO `Co-Authored-By` trailer** (agy worker policy).
- Do **NOT** merge to `develop`, do **NOT** push to `origin`, and do **NOT** release the worktree — end with the `delegate-report` block containing commit SHA(s), changed files, and verbatim Node + `w5090` Go verification output.
