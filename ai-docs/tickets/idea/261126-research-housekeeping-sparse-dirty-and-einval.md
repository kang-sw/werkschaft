---
title: "housekeeping sparse 워크트리 ' D' 오탐 및 Windows Invalid argument 조사"
related:
  261126-bug-win-worktree-remove-cwd-lock-and-async-trash: sibling bug ticket for Windows worktree.remove CWD locks and async trash
---

# Investigate `housekeeping` sparse-checkout `' D'` false dirty state and Windows `Invalid argument` on `worktree.remove`

## Background

During a Windows pooled worktree cleanup in `InspectTGV_AIDriven` (20 idle worktrees), one `housekeeping` sparse-checkout worktree (`knee-canopy-excitable`, 84 B on disk containing only `.git`, `HEAD` reachable from `strata`) failed removal in two distinct ways:

1. `ws/worktree.remove` refused it as dirty because `git status` reported ~3,729 tracked files as `' D'` (unstaged deletions), even though `housekeeping` is defined as cone sparse checkout of `["ai-docs"]`.
2. Direct `git worktree remove --force --force` failed with `failed to delete ...: Invalid argument` (`EINVAL`) for both the checkout directory and its admin metadata, leaving the worktree still registered even after `git worktree prune`.

This research ticket tracks reproducing and pinpointing the root cause before promoting an actionable fix to `ready/`.

## Code Audit Observations

- **Profile definition:** `builtinWorktreeProfiles` (`internal/mcp/worktree_profile.go:221-226`) defines `housekeeping` with `{"sparse":{"mode":"cone","paths":["ai-docs"]}}`.
- **Dirty check:** `worktree_remove.go:71-80` (as well as `worktree.release` and `worktree.list`) calls `wsgit.PorcelainPaths` (`internal/wsgit/worktree.go:21-41`), which runs `git -C <wtPath> --no-optional-locks status --porcelain -z` and collects every status entry regardless of XY code (`' D'` counts as dirty). `worktree_remove.go:79` joins all dirty paths without truncation (unlike `releaseHolderCheck` in `worktree_tools.go:969-971`, which truncates after 5).
- **`core.longPaths` inconsistency:** `worktreeHygiene` (`internal/mcp/worktree_tools.go:839-846`) passes `-c core.longPaths=true` to `reset --hard` and `clean -ffdx`, whereas `worktree add` (line 614), `applyDeclaredSparse` (line 829), `switch` (lines 652, 695), `checkout --detach` (line 856), `PorcelainPaths` (`wsgit/worktree.go:22`), and `git worktree remove` (`worktree_remove.go:144`) do not.
- **Expected Git sparse behavior:** Under active cone sparse checkout, hidden tracked entries should carry Git's `SKIP_WORKTREE` index bit so `git status` does not report absent non-`ai-docs` files as `' D'`. Seeing 3,729 `' D'` entries indicates `SKIP_WORKTREE` was absent on those index entries or `core.sparseCheckout` / `config.worktree` was inactive or corrupted on that checkout.

## Live Diagnostic Evidence (`InspectTGV_AIDriven`, `knee-canopy-excitable`, 2026-11-26)

Read-only inspection reported by peer lead (`git version 2.48.1.windows.1`):

- **Main repo config:** `extensions.worktreeconfig=true`, `core.ignorecase=true`, `core.symlinks=false`, `core.autocrlf=true`, `core.fscache=true`, `core.longPaths` unset, Git LFS filters configured.
- **Worktree config (`<common>/worktrees/knee-canopy-excitable/config.worktree`):** `core.sparseCheckout=true`, `core.sparseCheckoutCone=true`, `index.sparse=false`.
- **Sparse patterns (`info/sparse-checkout`):** `"/*"`, `"!/*/"`, `"/ai-docs/"` (`git sparse-checkout list` -> `ai-docs`).
- **Key finding — empty index (`"D "` staged deletions, NOT `" D"` unstaged):**
  - `git ls-files` and `git ls-files -v` print **nothing** (the worktree index has 0 entries; there are no `S`/`H` skip-worktree bits at all), while `HEAD` (`1fd09654`, detached, reachable from `strata`) has 3,729 files.
  - `git status --porcelain` therefore reports all 3,729 entries as **`"D "` (staged deletion in column 1)**, e.g. `D  .clang-format`, `D  AGENTS.md` — not a `SKIP_WORKTREE` sparse mismatch.
- **Disk & admin dir state:**
  - Checkout directory contains only `.git` (84 B); `ai-docs/` is absent.
  - Admin directory contains `info/`, `logs/`, `refs/`, `commondir`, `config.worktree`, `gitdir`, `HEAD`, `ws-worktree-profile.json` (65 B), and a leftover 0-byte **`ws-worktree-release.lock`** file (no `index` file or an empty index).
- **Removal failure:** `git worktree remove --force --force` failed with `failed to delete ...: Invalid argument` on both the checkout dir and admin dir; `git worktree prune` kept it registered because the dir still exists.
- **Likely root cause:** An interrupted/aborted provisioning (`worktree add --detach --no-checkout` starts with an empty index) or interrupted `worktree.release` (evidenced by leftover `ws-worktree-release.lock`) left the worktree with an empty index and no checked-out files. `ws`'s `PorcelainPaths` dirty check then treats the 3,729 `"D "` entries as uncommitted user edits and refuses `worktree.remove`.

## Outcome Ledger

### Verified Findings
- `builtinWorktreeProfiles` defines `housekeeping` as cone sparse `["ai-docs"]` (`internal/mcp/worktree_profile.go:221-226`).
- `wsgit.PorcelainPaths` (`internal/wsgit/worktree.go:21-41`) treats all `git status --porcelain -z` entries as dirty, and `worktree_remove.go:79` concatenates all dirty paths untruncated into the error string.
- `worktreeHygiene` passes `-c core.longPaths=true`, while `worktree add`, `applyDeclaredSparse`, `switch`, `checkout --detach`, `PorcelainPaths`, and `worktree.remove` omit it.
- Live diagnostic on `InspectTGV_AIDriven`'s `knee-canopy-excitable` (`git 2.48.1.windows.1`) proved the index is **completely empty** (`ls-files` empty, `status --porcelain` emits 3,729 `"D "` staged deletions, not `" D"`), `config.worktree` and `info/sparse-checkout` are valid cone `ai-docs`, `ai-docs/` was never materialized (or wiped), and a 0-byte `ws-worktree-release.lock` remains in the admin dir.

### Confirmed Decisions
- Track this empty-index / `"D "` / `Invalid argument` investigation separately from `261126-bug-win-worktree-remove-cwd-lock-and-async-trash` (`(1631)=a`) so the confirmed 18/20 CWD-lock and large-tree timeout fixes are not blocked.

### Proposals
- Detect empty-index / aborted-checkout pooled worktrees (e.g., no `index` file or 0-entry index with only `.git` on disk / interrupted `ws-worktree-release.lock`) so `worktree.remove` does not misclassify an aborted `worktree add --no-checkout` or interrupted release as user dirty work.
- Truncate dirty-path lists in `worktree_remove.go:79` to match `releaseHolderCheck` (cap at 5 paths + total count).
- Pass `-c core.longPaths=true` consistently across all pooled worktree Git operations (`worktree add`, `sparse-checkout`, `switch`, `checkout --detach`, `PorcelainPaths`, `worktree remove`).
- Audit `worktree.release` and `acquire` error/crash paths around `ws-worktree-release.lock` and `--no-checkout` to see how a worktree can be left with an empty index and lingering lock file without quarantine.

### Open Questions
- Which exact failure path in `worktree_tools.go` (`acquire` `--no-checkout` vs `release` holding `ws-worktree-release.lock`) leaves an unquarantined worktree with an empty index and `.git`-only working directory?
- Does `ws-worktree-release.lock` (or an open handle in the admin dir) explain why `git worktree remove --force --force` returned `Invalid argument` on both the checkout dir and the admin dir on Windows?

### Rejected Alternatives
- Blindly ignoring `' D'` or `'D '` entries in `PorcelainPaths` without distinguishing an empty/aborted checkout from genuine staged user deletions.
