# Plan: Expand `#hud-tooltip` Width to `#convo` & Enrich Metadata at Same Height

## Goal

Widen the top-right running-agent hover popover (`#hud-tooltip` in `wsdashboard`) to use the available conversation panel (`#convo`) width while keeping its vertical height compact (~same 5-row footprint as before), eliminating aggressive truncation and surfacing high-signal zero-I/O metadata already present in memory snapshots (`wslive.Agent` / `lookup.get(rid)` / `wsagent` session state).

Owner decision `(1615)=a`:
- Use `#convo` width (bounded cleanly inside `#convo` with 8px padding on each side, e.g. up to full `convoWidth - 16px` or a wide responsive width driven by `#convo` so titles, breadcrumbs, tasks, and single-line footer never needlessly wrap/clip).
- Preserve compact vertical height (merge the wrapped 2-line footer into 1 line, put tool counts / TPS / authority / branch or worktree onto existing horizontal rows instead of adding extra vertical blocks).
- Strictly **T0** (memory-only projection, zero file I/O, zero wire/schema breaks).

## Required Manuals to Read Before Editing

- `ai-docs/manuals/shipped-surface-boundary.md`
- `ai-docs/manuals/release-compatibility.md`
- `ai-docs/manuals/performance.md` (`## Frontend Minimal Updates`)
- `ai-docs/manuals/dashboard-style.md`

## Implementation Scope

### 1. Go Projection (`agents-plugin-tool/internal/wsdashboard/pageview_hud.go`, plus `pageview.go` if needed)
- Inspect `sessionLookup` (`lookup.get(rid)`), `wslive.Agent`, and `hudJSON` (and `tree_rows_stream_test.go` to understand the exact shell-invariance contract for `OwnRow` nodes).
- Project zero-I/O fields onto `HUDNode` (or read from `#r-tree` DOM for `OwnRow` if required by `tree_rows_stream_test.go` shell-invariance):
  - `Done int` (`json:"done,omitempty"`), `Failed int` (`json:"failed,omitempty"`), `OutputTps float64` (`json:"outputTps,omitempty"`) from `wslive.Agent` (or DOM `.am`/`.atps` for `OwnRow` if `hudJSON` must stay shell-invariant on tool count/TPS churn — check `tree_rows_stream_test.go` and pick the cleanest invariant-preserving design).
  - Session context from `lookup.get(rid)` (already called in `buildHUDRawForest` / `expandHUDLinked`, zero extra I/O):
    - `Branch string` (`json:"branch,omitempty"`) and/or `Dir string` (`json:"dir,omitempty"`, basename or worktree name when different from the viewing session's dir/branch)
    - `Authority string` / `PermissionLevel string` (`json:"authority,omitempty"`) and/or `Tier string` (`json:"tier,omitempty"`) if available on the in-memory `sessionLookup` entry / `wsagent` summary without adding disk reads.
  - Keep `HUDNode` additions optional (`omitempty`) and strictly T0.

### 2. Frontend Sizing & Layout (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-hud.js`, `dashboard-conversation.css`)
- **Responsive `#convo`-width sizing (`.hud-tooltip` & `hudPositionTooltip`)**:
  - Replace hardcoded `width: 252px` (`dashboard-conversation.css:411`) with a wide `#convo`-bounded layout:
    - For normal agent tooltips (`hudShowTooltip`), compute available width from `#convo` (or `#agisland` fallback): `avail = Math.max(252, Math.floor(box.right - box.left - pad * 2))`, and size `tt` to fill up to `#convo` width (e.g., `tt.style.width = avail + "px"` or `min(avail, maxWidth)` — user explicitly said `"대화 패널 폭을 다 써도 되는데. 높이는 그대로 두고요"`, so using full `box.right - box.left - 16` when `#convo` is available, clamped cleanly by `max-width: calc(100vw - 16px)`, gives maximum room and aligns neatly with `#convo`!).
    - For the small folded-count tooltip (`hudShowFoldedTooltip`), keep it compact (`width: auto` / `min-width: 220px; max-width: ...`) so a tiny "+5 finished agents" tooltip doesn't stretch awkwardly across the whole pane.
    - Keep `hudPositionTooltip` free of forced-reflow loops (single read of `anchor.getBoundingClientRect()` / `btn.getBoundingClientRect()`, set width/style, single read of `tt.getBoundingClientRect()`, set `left`/`top`).
- **Same-height enriched horizontal rows (`hudShowTooltip`)**:
  - **Row 1 (`.tt-head`)**: Harness icon + full `.tt-title` (now readable without 20-char truncation) + optional `.tt-native` + `.tt-pill` state badge on right.
  - **Row 2 (`.tt-meta`)**: Left cluster: `<code>model</code>` + `effort` + `· elapsed` + optional compact authority/branch/worktree chips (when present & non-redundant) + optional `+N deeper`. Right-aligned (`margin-left: auto`) cluster: `✓ <done>` (and `✗ <failed>` when > 0) + `<tps> t/s` (when > 0), all `tabular-nums`, `flex-wrap: nowrap` on wide viewports (graceful shrink/wrap only on very narrow viewports).
  - **Row 3 (`.tt-ctx`)**: Context `%` + wider progress bar + `used / window` text. If compaction is active or `compactions > 0`, fold the compact compaction indicator inline at the right end of `.tt-ctx` (or keep `.tt-cmp` only when `.tt-ctx` is absent) so compaction info never balloons card height when a context bar is shown!
  - **Row 4 (`.tt-chain` / `.tt-task`)**: Full-width `.tt-chain` (breadcrumb) and full-width `.tt-task` (current tool name + args preview / caption). If both exist, keep each to a crisp single line (now full `#convo` width so full tool args and paths are readable!); because the footer shrinks from 2 lines to 1 line, total height stays equal to or shorter than before.
  - **Row 5 (`.tt-hint` — single line)**: `display: flex; align-items: center; justify-content: space-between; gap: 12px; white-space: nowrap; overflow: hidden;` so `"클릭: 세션 이동 · /s/<replyId>"` stays on ONE line (never wrapping onto 2 lines like in the old 252px box), with optional short metadata (e.g., worktree/dir basename or `shortID`) right-aligned in muted mono.
- **Styling & Guards**:
  - Use existing `--wb-*` CSS custom properties per `ai-docs/manuals/dashboard-style.md` (both dark & light themes).
  - Respect `css_collision_test.go` (use `tt-*` or `d-*` class names; zero collisions with other modules).
  - Respect `frontend-idle.test.js` (no forbidden `:has()` selectors, no new running idle animations).
  - Respect the 500-line JS/CSS fragment check if any test asserts fragment line caps.

## Verification

1. `go vet ./...` inside `agents-plugin-tool/`.
2. `go test -race ./internal/wsdashboard/...` (including `pageview_hud_test.go`, `agents_island_test.go`, `tree_rows_stream_test.go`, `css_collision_test.go`, and `agent_popover_browser_test.go` if Playwright runs).
3. Local Node frontend tests: `node --test internal/wsdashboard/frontend-idle.test.js internal/wsdashboard/frontend-interaction.test.js` (and `agents-island.test.cjs`).
4. Add/extend unit tests covering:
   - `pageview_hud_test.go`: new `HUDNode` metadata fields (tool counts / TPS / branch / authority as projected) and `OwnRow` shell invariance.
   - `agents-island.test.cjs` (and/or `agent-popover.browser.cjs`): `#hud-tooltip` width scales with `#convo` width (vs compact folded tooltip), renders enriched row metadata, keeps single-line `.tt-hint`, and stays within `#convo` bounds.
5. Commit cleanly on `impl/develop/hud-tooltip-wide-info` (leaving the plan file unchanged) and report results.
