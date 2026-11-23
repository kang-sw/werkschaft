# Plan: frontend minimal-update rules in performance.md

## Request (owner (1413)=a, 2026-11-23 10:55)

The webview idle-CPU investigation confirmed that small, local UI changes were
dragging the whole main-thread pipeline (style, layout, paint, layerize) every
frame or every message. Add a frontend "minimal update" section to
`ai-docs/manuals/performance.md`, next to its existing file-touch rules, plus
ONE line in `AGENTS.md` `## Code Standards` next to item 6 (File-touch budget)
pointing at it.

## Draft rules (refine wording; keep each rule tied to evidence)

1. An idle screen produces no frames. Perpetual animations move only
   `transform`/`opacity`; never animate values inside gradients, filters or
   shadows (main-thread repaint every frame).
2. Contain invalidation to the element: moving or frequently changing elements
   get their own layer or layout/paint/style containment so ancestors are not
   re-laid-out or repainted.
3. Pause animations of invisible elements and while the window is hidden.
4. Per update, finish all DOM writes, then read layout; never interleave.
   Cache layout probes (`getComputedStyle`, `getClientRects`,
   `checkVisibility`, `offsetWidth`) once per frame/pass; never measure per row
   or per item in a loop.
5. Small changes patch only the changed element by key; do not rebuild a
   subtree with `innerHTML`.
6. Measure: as with file operations per action, count DOM operations and
   forced layouts per message/frame in tests.

## Evidence to cite (short, in the manual's evidence style)

- Ticket `261123-bug-ws-dashboard-webview-idle-cpu` Phase 1 Result (read it in
  `ai-docs/tickets/ready/`): conic-gradient spinners animated an angle custom
  property (main-thread repaint per frame) -> static gradient on a `::before`
  rotated by `transform` with `contain: paint`; box-shadow halos -> static
  shadow plus opacity pulse. Baseline idle trace: Paint 604 ms / Layerize
  443 ms / UpdateLayoutTree 354 ms in 5.6 s idle.
- Phase 2 (branch `impl/develop/pout-mop-gravy`, commits 925424c05..670ea087f,
  not yet merged; cite by ticket stem, not hash): one probe snapshot per read
  pass cut forced flushes in the board scroll capture from 7 to 2 on the
  counting fixture.
- Ticket `261123-bug-ws-dashboard-xterm-measure-storm` (todo): xterm DOM
  renderer re-measured glyph width via `offsetWidth` per row, ~1.5k forced
  style recalcs per refresh, 36 s of a 50 s trace, ~1 frame per 5-6 s.

## Verification

Docs only: read the diff back. Read `ai-docs/manuals/skill-authoring.md`
only if the edit touches shipped text (it should not; both files are
repo-local).

## Constraints

- Root checkout `D:\Workspace\Repos\devenv`, branch develop. Other agents
  commit ticket files on develop concurrently: stage only performance.md,
  AGENTS.md and this plan, via ws/git.commit (expected_branch develop), no
  Co-Authored-By trailer. Then `git fetch origin`, merge origin/develop if it
  moved (no rebase/force; STOP on conflict), `git push origin develop`.
- Keep the manual's existing tone and density; one short section, no
  duplication of the existing file-touch rules. AGENTS.md gets exactly one
  added line.

## Return boundary

Stop and report before editing any other file.
