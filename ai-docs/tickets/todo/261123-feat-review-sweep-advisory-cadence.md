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
  in `ai-docs/_review.local.md` with the metric threshold keys. Shipped defaults
  are open in (1441). Rejected (b): 60 merges / 50,000 lines (roughly once a
  day). Rejected (c): 5 merges / 5,000 lines / 50 commits (roughly every
  1–2 hours).
- **Compatibility: T0.** Maintainers can add keys to the optional local
  Markdown file. The current binary reads the file and searches only for the
  known section and `staleness` line, using its default when that line is
  absent or invalid (`agents-plugin-tool/internal/wsreview/config.go:17-58`).
  It ignores the added keys and performs no rewrite. The new reader accepts
  existing files, applies the precedence selected in (1443), and sees the
  added keys after switching back from the old binary. Keep the advisory
  read-only and preserve MCP calls and the ledger format
  (`agents-plugin-tool/internal/wsreview/checkpoint.go:21-29,57-87`).
  Use the local file extension for tuning. A new config-manifest field can
  hit the older decoder's unknown-field rejection, a T3 boundary
  (`agents-plugin-tool/internal/mcp/config_manifest.go:180-185`,
  `ai-docs/manuals/release-compatibility.md:116`).

## Open Decision Queue

Last ID: (1443)

(1440) [open] [technical] Identify generated paths through project-declared exclusions in `## Checkpoint Nudge`.
- Prefer an explicit exclude list in `ai-docs/_review.local.md` over `.gitattributes` `linguist-generated` detection or omitting the filter. A local list makes the advisory scope explicit for each project and avoids coupling review scope to language statistics. This repo already marks some generated paths in `.gitattributes:12-21`, but the shipped implementation needs a generic mechanism.

(1441) [open] [policy] Choose the merge and line thresholds for downstream projects without local overrides.
- Context: 25 merges / 20,000 lines was calibrated for devenv. Downstream projects land work at different rates.
- Alternative a: Ship 25 merges / 20,000 lines as the default pair for every project.
- Alternative b: Keep a conservative shipped default policy, agree its thresholds before promotion, and opt devenv into 25 merges / 20,000 lines through `ai-docs/_review.local.md`.
> Recommendation b. A local opt-in lets this repo use its measured cadence without making that cadence the default for downstream projects.

(1442) [open] [policy] Decide how the hard-coded 20-commit strong advisory tier should carry over.
- Context: The current strong tier runs first and can override the configured soft threshold. Its commit units do not match the new merge and line metrics.
- Alternative a: Use one advisory tier for the new metrics and handle any legacy mode under (1443).
- Alternative b: Keep stronger wording only at separately defined merge or line thresholds above the first advisory.
> Recommendation a. The first threshold already asks for a sweep, and the calibration gives no basis for a second urgency level.

(1443) [open] [policy] Decide how the new reader honors an existing `staleness: N commits` override.
- Context: Some projects may already rely on a specific commit threshold. Extending the local file leaves that setting readable by an older binary.
- Alternative a: Preserve legacy advisory behavior when a valid old key is the only explicit threshold setting. Let valid new threshold keys take precedence when present.
- Alternative b: Switch existing files to the new metric policy immediately. An old key alone would use the new shipped defaults.
> Recommendation a. Projects keep their explicit cadence until they opt into the new thresholds.

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

Implement in Codex after settling (1440)–(1443). Reuse `CheckpointNudgeWith`,
the injected Git runner, `Frontier`, and `ResolveTrackWith`. Measure eligible
first-parent merges on the resolved review track and the net diff from the
frontier comparison base to the track tip. Use the combined added and deleted
line counts from the net diff, where changes reverted within the range
contribute zero lines. Apply the exclusions to both metrics, including the
first-parent contribution of each candidate merge. Keep mixed merges eligible
if they land a reviewable path. Direct commits can contribute to net lines.
Count each eligible first-parent merge once.

Extend the existing local parser with the settled keys, fallback, and tier
behavior. The shared advisory reports the threshold reached in merge or line
units and carries the existing sweep range. Use the existing active emitters
for that result, including hosted manual rendering.

Update the Go fixtures for first-parent history with merged side-branch work,
docs-only and generated-only changes, mixed changes, empty direct commits,
reverted changes, and a non-`develop` review track. Cover quiet below both
thresholds, equality and independent triggering at each threshold, config
fallback and precedence, the settled strong-tier behavior, trailing non-clearing
ledger entries, baseline without a track, and failure without ledger writes.
Assert consistent output through the active emitters.

Run Go verification only on `w5090`, replacing `<who>`, `<min>`, and
`<worktree>` with the lock holder, minutes, and actual worktree. Record the
remote result in the phase result.

```bash
bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsreview/... ./internal/mcp/...'
```
