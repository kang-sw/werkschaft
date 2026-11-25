# Plan: 0.52.2 Small UI Polish Batch (4 Owner-Reported UX Fixes)

## Goal
Land four small, surgical, strictly T0-compatible dashboard UX bug fixes cleanly on branch `impl/develop/0522-ui-polish-batch` (based on `develop` `f4c9d4a2e`):
1. **Session popover hold-button height stability** (`hold-button-height`): prevent `.rpop .spact` press-and-hold buttons (`중지`/`보관`/`삭제`) from shrinking vertically when entering the armed `"놓으면 실행"` state and causing `pointerleave` misses.
2. **3-column tracker drawer z-index / shell frame lift** (`tracker-drawer-zindex`): prevent the left session `#rail` from painting over the pulled-down top-right `#tkdrawer` in 3-column mode (`>= 1600px`).
3. **User Command hold delay `1.5s` -> `1.0s`** (`ucmd-hold-delay`): shorten `.ucmd-ibtn.is-hold` hold confirmation from `1500ms` to `1000ms` (with all 11 locale strings and ucmd tests updated, keeping global `1500ms` destructive holds unchanged).
4. **Tracker Timeline (Gantt) left label single-line wrapping fix** (`tracker-timeline-wrap`, Owner Decision `(1604)=a`): stop `.tk .glabel` from breaking icons, titles, and priority badges across 3–4 lines.

## Required Reading Before Editing
1. `AGENTS.md`
2. `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates` — zero forced reflows, zero extra `MutationObserver` noise, zero forbidden `:has()` selectors per `frontend-idle.test.js`)
3. `ai-docs/manuals/dashboard-style.md`
4. `ai-docs/manuals/shipped-surface-boundary.md`

---

## Scope of Changes

### 1. Session Popover Hold-Button Height Invariance (`hold-button-height`)
- **Files:**
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-leads.js` (`rpSessMake`, `actBtn`, `drawStopNote`, `drawArchiveNote`, `drawCloseNote`, `bindHold`, `P.draw` around L981–1220)
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-rail.css` (`.rpop .spact`, `.rpop .spact .gs` around L725–755)
  - `agents-plugin-tool/internal/wsdashboard/testdata/hold-confirm.test.cjs`
- **Fix (zero-reflow CSS Grid 1/1 stacking):**
  - Inside `.rpop .spact .gs` on hold-bound buttons (`stop`, `archive`, `close` — keep non-hold `restart` clean), stack two child spans (e.g. `.gs-idle` updated by `draw*Note`, and `.gs-ready` populated with `wsT("js.hold.release")`) in the same `grid-area: 1 / 1` cell (`display: grid` on `.rpop .spact .gs`).
  - Toggle visibility/opacity purely via CSS on `.rpop .spact.d-hold-ready` (no `display: none`, no text swap in `onReady`/`onCancel`, no `getBoundingClientRect()`/`minHeight` reads/writes). Set `aria-hidden` cleanly so screen readers only announce the active label (or update `aria-hidden` on `onReady`/`onCancel` without mutating text/layout).
  - Verify button height is always `max(idle, ready)` in every locale/width so holding on the bottom line of a 3-line Korean description never shrinks the button out from under the pointer.
  - Add/update tests in `testdata/hold-confirm.test.cjs` verifying `.gs-idle`/`.gs-ready` structure, zero text-content swap on `onReady`, and ready state visual toggle.

### 2. 3-Column Mode Tracker Drawer Occlusion (`tracker-drawer-zindex`)
- **Files:**
  - `agents-plugin-tool/internal/wsdashboard/assets/tracker-render.js` (around L167 where `#tkdrawer` `.open` class is toggled)
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-lifecycle.js` (`shFrameInit`, around L1401–1454)
  - `agents-plugin-tool/internal/wsdashboard/assets/memo.css` (around L13: `.rail-open .rail { z-index: 45; }` — inspect standalone `>= 1600px` overlap against `.tk.tk-drawer` `z-index: 40`; keep `hud.browser.cjs:235-236` invariant `agisland/hud-tooltip z-index > 4 and < drawer` intact!)
  - Tests: `agents-plugin-tool/internal/wsdashboard/shell_frame_test.go`, `agents-plugin-tool/internal/wsdashboard/testdata/shell-post-layout.test.cjs` (and verify `shell_search_test.go`, `pincol_width_test.go`, `drawer_height_test.go`, `hud.browser.cjs` stay green).
- **Fix (zero extra `MutationObserver` overhead):**
  - Do **NOT** add `"class"` to the `document.body` `MutationObserver` in `dashboard-lifecycle.js:1454` (that would fire on every class toggle in the page).
  - Instead, in `tracker-render.js:167`, sync the `open` boolean attribute (`el.toggleAttribute("open", isOpen)` or `setAttribute`/`removeAttribute`) alongside `.open` on `#tkdrawer` (and ensure initial closed/open mount state matches).
  - In `dashboard-lifecycle.js:1435`, include `#tkdrawer[open]` in `shFrameInit`'s `popover` selector (alongside `.rpop:not([hidden]), .lmenu`). Because the existing `MutationObserver` at `:1454` ALREADY watches `attributeFilter: ["open", "hidden"]`, toggling `#tkdrawer[open]` immediately lifts `.frames` via `data-fpopover` (`z-index: 60` > `.dwrap` `z-index: 1`) with zero extra observer overhead, and restores `.frames` (`z-index: 0`) when `#tkdrawer` closes.
  - Check standalone `.rail-open .rail` (`memo.css:13`, `z-index: 45` vs `.tk.tk-drawer` `z-index: 40`): if `.rail-open` can coexist with `#tkdrawer.open` in standalone mode, make sure `#tkdrawer` stacks cleanly above `#rail` without breaking mobile drawer or `hud.browser.cjs` z-index assertions.
  - Add/update test coverage in `shell-post-layout.test.cjs` (and `shell_frame_test.go` if appropriate) verifying `#tkdrawer[open]` lifts `popover` (`data-fpopover`) and closing lowers it.

### 3. User Command Hold Delay `1.5s` -> `1.0s` (`ucmd-hold-delay`)
- **Files:**
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-ucmd.js` (`L3` comment, `L16` `UCMD_HOLD_MS = 1000`)
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-ucmd.css` (`L571` fallback `1000ms`, comments at `L3, L542, L582, L598, L628`)
  - `agents-plugin-tool/internal/wsi18n/tables/*.json` (ALL 11 locale files: `de.json`, `en.json`, `es.json`, `fr.json`, `it.json`, `ja.json`, `ko.json`, `pt-BR.json`, `ru.json`, `zh-Hans.json`, `zh-Hant.json`) — update the 8 `js.ucmd.*` keys (`js.ucmd.run.title`, `js.ucmd.remove`, `js.ucmd.sub.default`, `js.ucmd.ask.title`, `js.ucmd.enter.hint`, `js.ucmd.hold.early.run`, `js.ucmd.hold.early.ask`, `js.ucmd.hold.early.del`) from `1.5` / `1,5` to natural 1-second phrasing in each locale (e.g., `1s`, `1초`, `1秒`, `1 с`, etc. — check each locale's existing phrasing carefully for singular/grammar correctness!).
  - Tests:
    - `agents-plugin-tool/internal/wsdashboard/testdata/composer-bang.test.cjs` (`L251` `t.ms === 1000`, comment `L241`)
    - `agents-plugin-tool/internal/wsdashboard/testdata/composer-usercmd.browser.cjs` (`L10-L11` `TEXT.ko.earlyRun`/`TEXT.en.earlyRun`, `L41` `holdButton` default `1100`, comments)
    - `agents-plugin-tool/internal/wsdashboard/testdata/composer-usercmd-v2.test.cjs` (`L323`, `L496`, `L509`, `L522`, `L535`, `L645`, `L655`, `L662`, `L671` and comments)
- **Constraint:** Do **NOT** modify `hold-confirm.js` `DEFAULT_DURATION = 1500` or destructive session/rail/account/bg-exec hold durations.

### 4. Tracker Timeline Left Label Wrapping Fix (`tracker-timeline-wrap`, Owner Decision `(1604)=a`)
- **Files:**
  - `agents-plugin-tool/internal/wsdashboard/assets/tracker.css` (around L822–825, `.grow-axis`, `.grow-row`, `.glabel`, `.glabel .title`, `.glabel > .muted`, `.glabel .chip.hid`)
  - `agents-plugin-tool/internal/wsdashboard/assets/tracker-window.js` (around L159, `ganttBody`) and/or `tracker-view-helpers.js` (around L140–143, `titleRef`)
  - Add CSS/DOM unit test assertion (in an existing tracker test file under `internal/wsdashboard/`).
- **Fix (matches list view `.ir-main`, uniform 1-line Gantt rows):**
  - Change `.tk .grow-axis, .tk .grow-row` (`tracker.css:822`) left column width from `minmax(180px, 34%) 1fr` to `minmax(220px, 38%) 1fr`.
  - Change `.tk .glabel` (`tracker.css:823`) from `flex-wrap: wrap` to `flex-wrap: nowrap; overflow: hidden;` and cap deep nesting indent (`padding-left: calc(min(var(--ind, 0), 5) * 14px)`).
  - Add `.tk .glabel .title { font-weight: 600; color: var(--wb-fg); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; flex: 1 1 0; }` (preserving `.tk .grow-row.ctx .glabel .title` at `:825`).
  - Keep `.chip.hid`, `.tsub`, `.tw`, `.sti`, `.pri`, `.gmore` non-shrinking (`flex: none`), and ensure `.glabel > .muted` (`whoTag`) elides cleanly (`flex: 0 1 auto; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;`) and `.tmore` stays single-line (`white-space: nowrap`).
  - Add `data-tip` (`tipAttr(it.title)`) on the Gantt `<a class="title">` (either via optional param on `titleRef` or directly in `ganttBody`) so hovering/focusing truncated titles shows `#tktip` immediately without interfering with `.tw`/`.sti`/`.pri`/`.gmore` tooltips.

---

## Verification Requirements (STRICT Windows AV Host Constraints)
- **NEVER run `go`, `go test`, `go build`, `go vet`, `gofmt`, or raw `ws-mcp.exe` locally on Windows!** (AhnLab V3 AV rule).
- Run local Node tests (`node --test`):
  - `agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js` (verify static `:has()` guard STILL passes!)
  - `agents-plugin-tool/internal/wsdashboard/testdata/hold-confirm.test.cjs`
  - `agents-plugin-tool/internal/wsdashboard/testdata/shell-post-layout.test.cjs`
  - `agents-plugin-tool/internal/wsdashboard/testdata/composer-usercmd-v2.test.cjs`
  - Any other runnable `.test.js` / `.test.cjs` under `wsdashboard`.
- Run Go vet, `gofmt`, `wsi18n` tests, and targeted + comprehensive `wsdashboard` `-race` tests on remote `w5090` via `sys_lock.sh`:
  ```bash
  D:/sys_lock.sh remote w5090 run 0522-ui-polish 20 \
    --sync 'D:/Workspace/Repos/.ws-worktrees/devenv/tassel-neutron-chunk' \
    -- 'export PATH=$HOME/sdk/go/bin:$HOME/.local/node/bin:$PATH; cd agents-plugin-tool && \
        test -z "$(gofmt -l internal/wsdashboard internal/wsi18n)" && \
        go vet ./internal/wsdashboard ./internal/wsi18n && \
        go test -race ./internal/wsi18n && \
        go test -race ./internal/wsdashboard -run "TestHoldConfirm|TestShell|TestTracker|TestPinCol|TestDrawer|TestComposer|TestRail|TestFrontend|TestUserCmd|TestUcmd"'
  ```
- Note: Check `gofmt -l` specifically on every Go file you touch (do not reformat unrelated pre-existing files). Delete any temporary files under `D:/Temp/<purpose>/`.

## Git & Return Rules
- Commit cleanly on `impl/develop/0522-ui-polish-batch` using `ws/git.commit(expected_branch: "impl/develop/0522-ui-polish-batch", paths: [...], title: ..., ai_context: [...])`.
- Include this plan file (`ai-docs/.plans/2026-11/25-1609-0522-ui-polish-batch.md`) in the commit's `paths`.
- **NO `Co-Authored-By` trailer** (agy worker policy).
- Do **NOT** merge to `develop`, do **NOT** push to `origin`, and do **NOT** release the worktree — end with the `delegate-report` block containing commit SHA(s), changed files, and verbatim local Node + remote `w5090` verification output.
