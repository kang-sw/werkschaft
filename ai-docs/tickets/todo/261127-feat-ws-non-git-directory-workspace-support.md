---
title: "비-Git 일반 폴더 워크스페이스 일급 지원"
---

# Non-Git Directory First-Class Workspace Support

## Background

Today `ws` assumes every workspace is a Git repository. `wsstate.Manager.Resolve`
(`agents-plugin-tool/internal/wsstate/paths.go`) delegates directly to
`gitIdentity(m.opts.Env, repoPath)`, which fails outside a Git repository.
Because `playbook.render`, `note.*`, `mailbox.*`, `path.generate`, `tickets.*`,
`workflow_manual`, and `workflow_state` rely on `wsstate.Manager` or Git-backed
scopes, plain (non-Git) directories registered in the dashboard cannot launch
sessions (`.rnew` is omitted when `!ws.Repo` in `dashboard.tmpl`), fail during
tool calls, or leave partially edited ticket files on disk when `scopedGitMove`
rejects a non-Git root (`agents-plugin-tool/internal/wsdoc/tickets_mutate.go`).

Making non-Git folders first-class workspaces lets users run sessions,
whiteboards, notes, mailbox, delegation, and ticket tracking in plain folders
(such as research folders or scratch workspaces) while keeping Git-only
primitives explicit and preserving seamless promotion when `.rgit` runs
`git init`.

## Decisions

*(Settled on whiteboard `(1746..1750)=a`)*

- **First-class non-Git folder workspace support (`(1746)=a`):**
  - Enable dashboard `.rnew` / session-launch arguments, session-key minting,
    and `wsstate` path-hash fallback in plain directories so `playbook.render`,
    `lead-delegate`, whiteboard, notes, and mailbox work without `.git`.
  - Git-only primitives (`git.*`, `worktree.*`, `lead-review` diff mode,
    `lead-ship`) return explicit unsupported notices for plain directories
    rather than opaque `git` failures.
- **Canonical path-hash `wsstate` key and `.rgit` promotion migration (`(1747)=a`):**
  - When `gitIdentity` detects a plain directory (non-repository), derive the
    `wsstate` `ProjectKey` and `WorktreeKey` from a hash of the canonicalized
    folder path (`EvalSymlinks`, `filepath.Clean`, Windows case normalization).
  - When dashboard `.rgit` (`handleWorkspaceGitInit` in
    `wsdashboard/railgitinit.go`) promotes a plain directory to a Git repository
    via `git init`, migrate (or alias-inherit) existing non-Git `wsstate`
    (notes, cached artifacts, session/metadata records) into the new Git root
    key so nothing is orphaned.
- **Note/mailbox layer mapping and `note.query()` symmetry (`(1748)=a`):**
  - Map the canonical folder path hash to the `worktree` layer (including
    `@worktree` mailbox scope) and fall back `clone` to the same root or an
    explicitly inactive layer cleanly.
  - Fix the asymmetry where multi-layer `note.query()` (`searchNoteLayers` in
    `mcp/note_tools.go`) fails hard on `LayerClone` resolution while
    `wsnote.ComputeIn` (`wsnote/inject.go`) silently ignores layer errors.
- **Plain-FS `tickets.move` / `tickets.close` fallback and partial-overwrite fix (`(1749)=a`):**
  - Add an `os.Rename`-based plain-filesystem fallback for `tickets.move` and
    `tickets.close` in non-Git directories (`guardOccupiedRoot` in
    `mcp/occupied_root.go` applies to Git repositories only; `tickets.acquire`
    and `tickets.release` return explicit no-index guidance).
  - Fix the existing partial-overwrite bug in `wsdoc/tickets_mutate.go` where
    `TicketsClose` / `TicketsMove` write frontmatter, `## Resolution`, or
    sage-review posture to disk before `scopedGitMove` / `atomicGitMove` fails
    when `scope == nil` or no Git repository exists. Pre-validate moveability
    before any disk write or route non-Git directories cleanly to the plain-FS
    rename path.
- **`project_mode: plain`, Git-nudge suppression, and playbook branches (`(1750)=a`):**
  - Emit `project_mode: plain` in `workflow_manual` and `workflow_state`
    (`mcp/workflow_manual.go` and session-state rendering) for plain-directory
    sessions.
  - Suppress Git-only nudges in plain mode: `wsreview.CheckpointNudgeWith`,
    `reviewTrackNudge`, `occupiedRootAnnouncement`, and Git-only `"gitignored"`
    guidance in `noteOversizeChallengeFor` (`mcp/note_tools.go`) and
    `manualsAuthoringGuidance` (`mcp/manuals_announcement.go`).
  - Add explicit non-Git branches (skipping branch, commit, and worktree steps)
    in `lead-workflow-manual`, `lead-delegate`, and `lead-bootstrap` playbooks,
    keeping `agents-plugin-wsflow/` mirrored and downstream-neutral.
- **Compatibility tier — T0 transparent:**
  - Additive fallback for non-Git directories only. Existing Git repositories
    retain identical `wsstate` keys, `gitIdentity` resolution, and Git-backed
    `tickets.*` / `note.*` behavior; `project_mode` is additive metadata that
    older clients ignore safely.

## Constraints

- **Performance & Windows AV (`ai-docs/manuals/performance.md`):**
  - Cache non-Git canonical path-hash resolution in memory alongside
    `gitIdentities` (`wsstate/paths.go`) without spawning redundant `git`
    subprocesses on repeated tool calls, and keep interactive paths free of
    unnecessary disk I/O.
- **Release compatibility (`ai-docs/manuals/release-compatibility.md`):**
  - Preserve T0 compatibility across shared stores, MCP responses, and
    `wsstate` directory layouts; existing Git project keys and persisted data
    must remain unchanged.
- **Shipped-surface boundary (`ai-docs/manuals/shipped-surface-boundary.md`):**
  - All new/edited playbook prose, MCP unsupported notices, and manual/nudge
    variants under `agents-plugin/`, `agents-plugin-wsflow/`, and
    `agents-plugin-tool/` must be downstream-neutral and cite zero devenv-only
    files, tickets, or repo layout paths.
- **Skill authoring (`ai-docs/manuals/skill-authoring.md`):**
  - Keep non-Git branches in `lead-workflow-manual`, `lead-delegate`, and
    `lead-bootstrap` concise, scoped (Layer 3 prose only), and failure-cited.
- **wsflow mirroring (`ai-docs/manuals/wsflow-mirroring.md`):**
  - Keep `agents-plugin-wsflow/` skill shims and `rsrc/` product-mode
    rendering synchronized with shared playbooks and covered by wsflow bundle
    tests.
- **MCP contract (`ai-docs/manuals/ws-mcp.md`):**
  - Preserve argument prechecks, `session_key` binding, and clear fail-loud
    vs advisory behavior across all MCP handlers.
- **Dashboard visual design (`ai-docs/manuals/dashboard-style.md`):**
  - Keep `.rnew` and `.rgit` rail controls on plain-folder rows consistent with
    Werkschaft's borderless Material tonal surface tokens and compact icon
    button sizing.

## Phases

### Phase 1: `wsstate` Path-Hash Fallback, Dashboard `.rnew`, and `.rgit` Migration

1. **Canonical path-hash fallback in `wsstate`:**
   - Update `wsstate.Manager.Resolve` / `Ensure` (`wsstate/paths.go`) so
     plain directories (explicit Git non-repository result, distinct from
     corrupt/permission failures) resolve deterministically via a canonicalized
     folder path hash (`EvalSymlinks`, `filepath.Clean`, Windows case folding)
     and record plain-directory metadata.
2. **Dashboard `.rnew` and `.rgit` migration:**
   - Enable `.rnew` session creation and plain-folder workspace args in
     `wsdashboard` (`dashboard.tmpl` and workspace/session launch paths).
   - Update `handleWorkspaceGitInit` (`wsdashboard/railgitinit.go`) so
     promoting a plain directory via `.rgit` migrates or alias-inherits
     existing non-Git `wsstate` (notes, generated artifacts, session/metadata
     entries) into the new Git root key cleanly.
3. **Verification:**
   - Unit and integration tests for `wsstate` plain-folder key stability,
     symlink/case normalization, `.rnew` session launch on plain folders, and
     `.rgit` promotion preserving prior `wsstate`.

### Phase 2: `worktree` / `@worktree` Note & Mailbox Scopes and `note.query()` Symmetry

*Depends on Phase 1 `wsstate` plain-folder resolution.*

1. **Layer and mailbox scope mapping:**
   - Map plain-folder `wsstate` keys to `wsnote.LayerWorktree` and the
     `@worktree` mailbox scope, and handle `wsnote.LayerClone` cleanly (mapped
     to the same root or explicitly inactive without erroring multi-layer
     queries).
2. **`note.query()` symmetry and Git-only MCP notices:**
   - Update `searchNoteLayers` (`mcp/note_tools.go`) so multi-layer
     `note.query()` never fails asymmetrically on plain directories while
     `wsnote.ComputeIn` (`wsnote/inject.go`) degrades cleanly.
   - Return explicit unsupported notices from `git.*` and `worktree.*` MCP
     handlers when invoked in a plain directory.
3. **Verification:**
   - Tests for `note.write` / `note.query` (single-layer and multi-layer),
     `wsnote.ComputeIn`, `@worktree` mailbox send/recv, and `git.*` /
     `worktree.*` unsupported notices in plain directories.

### Phase 3: Plain-FS `tickets.move` / `tickets.close` Fallback and Partial-Overwrite Fix

*Depends on Phase 1 plain-directory detection.*

1. **Pre-write validation and plain-FS fallback in `wsdoc/tickets_mutate.go`:**
   - Fix the partial-overwrite bug in `TicketsClose` / `TicketsMove` where
     `writeFrontmatterField`, `appendResolution`, or sage-review frontmatter
     edits land on disk before `scopedGitMove` / `atomicGitMove` fails.
   - Route non-Git directories to an `os.Rename`-based move/close path (creating
     the destination status directory when needed) after all status/pre-flight
     checks pass.
2. **Lease and occupied-root handling:**
   - Limit `guardOccupiedRoot` (`mcp/occupied_root.go`) to Git repositories;
     return explicit no-index guidance from `tickets.acquire` and
     `tickets.release` in plain directories.
3. **Verification:**
   - Regression tests proving failed moves never leave partial disk mutations,
     plus plain-directory `tickets.move`, `tickets.close`, `tickets.acquire`,
     and `tickets.release` coverage.

### Phase 4: `project_mode: plain`, Nudge Suppression, Playbooks, and wsflow Mirroring

*Depends on Phases 1–3.*

1. **`project_mode: plain` and Git-nudge suppression:**
   - Emit `project_mode: plain` in `workflow_manual` and `workflow_state`
     (`mcp/workflow_manual.go` and session-state output) when the workspace is a
     plain directory.
   - Suppress `wsreview.CheckpointNudgeWith`, `reviewTrackNudge`,
     `occupiedRootAnnouncement`, and Git-only `"gitignored"` phrasing in
     `noteOversizeChallengeFor` (`mcp/note_tools.go`) and
     `manualsAuthoringGuidance` (`mcp/manuals_announcement.go`) in plain mode.
2. **Playbook non-Git branches and `wsflow` mirroring:**
   - Add concise non-Git branches (skipping branch, commit, and worktree steps;
     returning explicit unsupported notices for `lead-review` diff mode and
     `lead-ship`) in `lead-workflow-manual`, `lead-delegate`, and
     `lead-bootstrap` playbooks under `agents-plugin/rsrc/`.
   - Verify downstream-neutrality (`shipped-surface-boundary.md`),
     `skill-authoring.md` compliance, and `agents-plugin-wsflow/` product-mode
     mirroring and tests.
3. **Verification:**
   - MCP tests for `workflow_manual` / `workflow_state` `project_mode: plain`
     and suppressed nudges, plus `ws` and `wsflow` playbook/bundle verification
     tests.
