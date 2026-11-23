---
title: 리뷰 스윕 권고 빈도 조정
---

# Tune the review-sweep advisory cadence

## Background

The review-watermark advisory fires too often on `develop`. `CheckpointNudgeWith`
reads the clearing frontier, resolves the review track, and counts all reachable
commits with `git rev-list --count <merge-base>..<track>`. This includes merges,
side-branch ancestry, ticket edits, generated churn, and empty commits. The
hard-coded strong threshold of 20 commits is checked first. The soft threshold
defaults to 10 and can be overridden by `staleness: N commits` under
`## Checkpoint Nudge` in `ai-docs/_review.local.md`.
Evidence: `agents-plugin-tool/internal/wsreview/checkpoint.go:57-108`
(the strong constant is at line 19) and
`agents-plugin-tool/internal/wsreview/config.go:10-58`.

The owner's Nov 10–23 calibration reported that every sampled day crossed both
10 and 20 commits, 5 merges, and 5,000 changed lines. A single net diff per
first-parent day still showed at least 18,887 changed lines after excluding
`ai-docs/` and generated paths for that calibration. Among the tested cutoffs,
only 50 first-parent commits reduced the number of days hit. As a cross-check,
`git log develop --first-parent --merges` grouped by committer date showed 28
to 292 merges per day across the same dates as of `6d8a8bbb8`. The 2–3 sweeps
per day goal is a calibration target for this repo's pace.

The advisory reaches callers through these paths in
`agents-plugin-tool/internal/mcp/`.

- `workflow_manual.go:554` and `:613` inject the advisory in fresh and
  continued workflow manuals. Hosted lead manual rendering calls that renderer
  at `hosted_render.go:80`. `bootstrap_alarm.go:144-153` is the shared
  text-prepend helper.
- `session_state.go:1162` appends `review-watermark:` in the text response
  from `route.resolve_implement` (`handleEnterImplement`).
  `server_dispatch.go:1269` appends it after `tickets.close`.
- `session_state.go:1096` is in the dormant `handleEnter` helper.
  `workflow_manual.go:344` and `:372` belong to the separate
  `workflow_state` response, which renders only session state and notes.

## Decisions

- **(1438)=a.** Measure first-parent merges on `develop` and net changed lines
  since the review frontier. Exclude `ai-docs/`-only commits and generated
  paths from the advisory metrics. Resolve the project's review track through
  the existing `ResolveTrackWith` path. `develop` is the declared track here
  (`AGENTS.md:154`, `agents-plugin-tool/internal/wsreview/track.go:11-31`).
  Rejected (b): keep the all-ancestry commit count and only raise the threshold.
  That would still count non-reviewable churn.
- **(1439)=a.** Aim for 2–3 sweeps per day at this repo's current pace. Advise
  when first-parent merges reach **25** or net changed lines reach **20,000**,
  whichever happens first. Extend the existing `## Checkpoint Nudge` section
  in `ai-docs/_review.local.md` with the metric threshold keys.
  Rejected (b): 60 merges / 50,000 lines (roughly once a day).
  Rejected (c): 5 merges / 5,000 lines / 50 commits (roughly every 1–2 hours).
- **(1440) confirmed.** Projects list generated-path exclusions under
  `## Checkpoint Nudge` in `ai-docs/_review.local.md`. Use those exclusions
  for both new metrics. Rejected: auto-detect `.gitattributes`
  `linguist-generated` paths or omit the generated-path filter.
- **(1441)=b.** The shipped defaults are **10 first-parent merges** or
  **5,000 net changed lines**. Devenv opts in to **25 merges** or
  **20,000 lines** in its `ai-docs/_review.local.md`.
  Rejected (a): ship 25 merges / 20,000 lines for all projects. That pair
  was calibrated for devenv's pace.
- **(1442)=a.** Use a single advisory tier. Once either applicable metric
  reaches its threshold, emit one message recommending a sweep.
  Rejected (b): add a second, higher tier with stronger wording.
- **(1443)=a.** With only a valid `staleness: N commits` threshold in local
  config, preserve the legacy all-ancestry commit-count trigger and present
  it with the single advisory wording. This preserves its original trigger
  point, including the effective cutoff at 20 when `N` is higher
  (`agents-plugin-tool/internal/wsreview/checkpoint.go:74-84`). Valid new
  threshold keys take precedence and select the merge and line metrics.
  Rejected (b): immediately apply the new shipped defaults to legacy-only
  overrides.
- **Compatibility: T0.** Maintainers can add keys to the optional local
  Markdown file. The current binary reads the file and searches only for the
  known section and `staleness` line, using its default when that line is
  absent or invalid (`agents-plugin-tool/internal/wsreview/config.go:17-58`).
  It ignores the added keys and performs no rewrite. The new reader accepts
  existing files, applies the precedence in (1443), and sees the
  added keys after switching back from the old binary. Keep the advisory
  read-only and preserve MCP calls and the ledger format
  (`agents-plugin-tool/internal/wsreview/checkpoint.go:21-29,57-87`).
  Use the local file extension for tuning. A new config-manifest field can
  hit the older decoder's unknown-field rejection, a T3 boundary
  (`agents-plugin-tool/internal/mcp/config_manifest.go:180-185`,
  `ai-docs/manuals/release-compatibility.md:116`).

## Constraints

- Backend implementation is owned by Codex. Scope is Go under
  `agents-plugin-tool/internal/wsreview/` and the existing MCP emitters.
- Keep the advisory read-only and best effort. Preserve the no-ledger baseline
  prompt, including on a trackless repo, the clearing frontier selection, the
  suggested sweep range, and existing caller eligibility and response formats.
- Apply `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/release-compatibility.md`, and `ai-docs/manuals/performance.md`.
  Keep downstream branch and generated-path policy project-configurable. Account
  for Git subprocess and file-touch costs on the emitting paths.
- Read `ai-docs/manuals/skill-authoring.md` and
  `ai-docs/manuals/wsflow-mirroring.md` before changing any shipped `rsrc` text.

## Phases

### Phase 1: Measure reviewable changes and update the advisory

Implement in Codex. Reuse `CheckpointNudgeWith`,
the injected Git runner, `Frontier`, and `ResolveTrackWith`. Measure eligible
first-parent merges on the resolved review track and the net diff from the
frontier comparison base to the track tip. Use the combined added and deleted
line counts from the net diff, where changes reverted within the range
contribute zero lines. Apply the exclusions to both metrics, including the
first-parent contribution of each candidate merge. Keep mixed merges eligible
if they land a reviewable path. Direct commits can contribute to net lines.
Count each eligible first-parent merge once.

Extend the existing local parser with the two metric thresholds and the
project's generated-path exclude list. Use shipped defaults for thresholds
without a valid override in metric mode. Give valid new threshold keys
precedence over the legacy key, and retain the legacy trigger for legacy-only
overrides. Record the 25-merge / 20,000-line opt-in in devenv's local review
config during implementation. The shared advisory uses one message in the
applicable units and carries the existing sweep range through the active
emitters, including hosted manual rendering.

Update the Go fixtures for first-parent history with merged side-branch work,
docs-only and generated-only changes, mixed changes, empty direct commits,
reverted changes, and a non-`develop` review track. Cover shipped defaults
and local overrides, quiet below both thresholds, equality and independent
triggering at each threshold, and one advisory when both thresholds are met.
Cover the legacy-only trigger and new-key precedence, trailing non-clearing
ledger entries, baseline without a track, and failure without ledger writes.
Assert consistent output through the active emitters.

Run Go verification only on `w5090`, replacing `<who>`, `<min>`, and
`<worktree>` with the lock holder, minutes, and actual worktree. Record the
remote result in the phase result.

```bash
bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsreview/... ./internal/mcp/...'
```
