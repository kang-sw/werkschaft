---
title: 리뷰 스윕 권고 빈도 조정
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 717de70a2a75dbbb
sage-review-completeness-reviewed: 717de70a2a75dbbb
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

From Nov 10–23, 2026, `git log 6d8a8bbb8 --first-parent --merges` grouped by
committer date showed 28 to 292 first-parent merges per day before path
exclusions. The 2–3 sweeps per day goal is a calibration target for this repo's
pace.

The advisory reaches callers through these paths in
`agents-plugin-tool/internal/mcp/`.

- `workflow_manual.go#L516-L559` and `workflow_manual.go#L586-L620` inject the
  advisory in fresh and continued workflow manuals with a project root.
  Hosted lead manual rendering calls that renderer at `hosted_render.go:80`.
  `bootstrap_alarm.go:144-153` is the shared text-prepend helper.
- `session_state.go:1162` appends `review-watermark:` in the text response
  from `route.resolve_implement` (`handleEnterImplement`).
  `server_dispatch.go:1269` appends it after `tickets.close`.
- `session_state.go:1096` is in the dormant `handleEnter` helper.
  `workflow_manual.go:344` and `:372` belong to the separate
  `workflow_state` response, which renders only session state and notes.

## Decisions

- **(1438)=a.** Measure first-parent merges on `develop` and net changed lines
  since the review frontier. Apply the `ai-docs/` and generated-path
  exclusions defined in (1440) and (1492). Resolve the project's review track
  through the existing `ResolveTrackWith` path. `develop` is the declared track here
  (`AGENTS.md:154`, `agents-plugin-tool/internal/wsreview/track.go:11-31`).
  Supersedes the commit-count proxy from `b7815be41` in metric mode because
  all-ancestry commit counts include churn that does not represent reviewable
  changes. The legacy override still uses that proxy under (1443).
  Rejected (b): keep the all-ancestry commit count and only raise the threshold.
  That would still count non-reviewable churn.
- **(1439)=a.** Aim for 2–3 sweeps per day at this repo's current pace. Advise
  when first-parent merges reach **25** or net changed lines reach **20,000**,
  whichever happens first. Extend the existing `## Checkpoint Nudge` section
  in `ai-docs/_review.local.md` with the metric threshold keys.
  Rejected (b): 60 merges / 50,000 lines.
  Rejected (c): 5 merges / 5,000 lines / 50 commits.
- **(1440) confirmed.** Projects list generated-path exclusions under
  `## Checkpoint Nudge` in `ai-docs/_review.local.md`. Use those exclusions
  for both new metrics. Rejected: auto-detect `.gitattributes`
  `linguist-generated` paths or omit the generated-path filter.
- **(1441)=b.** The shipped defaults are **10 first-parent merges** or
  **5,000 net changed lines**. Devenv will opt in to **25 merges** or
  **20,000 lines** in its gitignored `ai-docs/_review.local.md`
  (`.gitignore#L16`).
  Rejected (a): ship 25 merges / 20,000 lines for all projects. That pair
  was calibrated for devenv's pace.
- **(1442)=a.** Use a single advisory tier. Once either applicable metric
  reaches its threshold, emit one message recommending a sweep.
  Supersedes the graduated advisory in
  `260824-feat-review-watermark-ledger` under Decisions. The advisory
  remains non-blocking.
  Rejected (b): add a second, higher tier with stronger wording.
- **(1443)=a.** With only a valid `staleness: N commits` threshold in local
  config, preserve the legacy all-ancestry commit-count trigger and present
  it with the single advisory wording. This preserves its original trigger
  point, including the effective cutoff at 20 when `N` is higher
  (`agents-plugin-tool/internal/wsreview/checkpoint.go:74-84`). Valid new
  threshold keys take precedence and select the merge and line metrics.
  Rejected (b): immediately apply the new shipped defaults to legacy-only
  overrides.
- **(1487).** In `## Checkpoint Nudge`, use `merges: <N>` and
  `lines: <N>` for positive decimal integer thresholds and one
  `exclude: <pathspec>` line per generated-path exclusion. Match each
  exclusion as a repo-relative, positive Git pathspec and pass it directly
  as an exclusion to both Git metrics. Git pathspecs let the two metrics
  apply identical matching. Rejected: a separate glob language and
  comma-separated exclusions, which would require extra matching or escaping
  rules.
- **(1488).** Count added and deleted text lines. Binary, mode-only, and
  gitlink-only changes contribute zero lines. Each can qualify a merge
  when it changes a non-excluded path relative to the merge's first parent.
  Rejected: using text numstat to decide merge eligibility or treating
  synthetic submodule SHA lines as changed source lines.
- **(1489).** Use the existing merge base of the clearing frontier head and
  review track as the exclusion boundary. Count merges on the track's
  first-parent chain that are outside the base's reachable ancestry, including
  when the base is on a side branch. An unresolved merge base gives no new
  advisory. Rejected: requiring the base itself to be on the first-parent chain
  or moving the boundary to a different commit on that chain.
- **(1492)=a.** Exclude all `ai-docs/` paths from both metrics. A mixed merge
  qualifies through any remaining eligible path. Measure lines with one net
  diff over the remaining paths so documentation edits never inflate that
  metric. Rejected: counting documentation lines attributed to mixed changes,
  which would require reconstructing those lines from commit history.
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
- Run advisory Git commands using local objects only. Suppress lazy fetching
  in partial clones, as the local Git search already does
  (`agents-plugin-tool/internal/mcp/git_search.go:675-677`). If objects needed
  to compute the metrics are unavailable, fail open without an advisory or a
  ledger write.
- Apply `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/ws-mcp.md`,
  `ai-docs/manuals/release-compatibility.md`, and `ai-docs/manuals/performance.md`.
  Keep downstream branch and generated-path policy project-configurable. Account
  for Git subprocess and file-touch costs on the emitting paths.
- Read `ai-docs/manuals/skill-authoring.md` and
  `ai-docs/manuals/wsflow-mirroring.md` before changing any shipped `rsrc` text.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 893c2eb09 (2026-11-22, commit): "Decision 10: reviewNudgeWanted keys on sessionEntry.isTopLevelLead(), so a keyless or unknown caller keeps the nudge." — bearing: constrains
- b76af960c (2026-11-10, commit): "TestHostedToolsRunGitOnTheConnectionPATH (project_tree, review.marker bootstrap, path.generate each log the connection's git shim; fails on the process git)" — bearing: constrains
- 257b9e6df (2026-08-30, commit): "Moved Read before ResolveTrack; only the behind-track staleness/size arms now require a resolved track." — bearing: constrains
- 9c36958a1 (2026-08-30, commit): "Bootstrap's own idempotency check (\"any entry exists\") is a different question from \"what's the frontier\", and this is a new read-side filter, not a ledger-format change." — bearing: constrains
- b7815be41 (2026-08-30, commit): The previous commit-count proxy was selected to avoid diff stats. Superseded in metric mode by (1438), with the legacy override retained under (1443).
- 260824-feat-review-watermark-ledger (2026-08-24, Decisions): The advisory used gentle and strong wording. Superseded by the single advisory tier under (1442). It remains non-blocking.

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/internal/wsreview/checkpoint.go`, `agents-plugin-tool/internal/wsreview/config.go`, the existing advisory tests, and `ai-docs/_review.local.md` |
| scope.surface | public-interface | New maintainer-facing Markdown keys in `ai-docs/_review.local.md`, with advisory text delivered through the existing MCP and hosted callers |
| scope.new_public_symbol | no | Phase 1 calls for no new Go export or MCP tool |
| scope.new_type_contract | yes | Two threshold values and a generated-path list extend the local Markdown config contract |
| scope.test_surface | existing | `agents-plugin-tool/internal/wsreview/checkpoint_test.go`, `agents-plugin-tool/internal/wsreview/config_test.go`, and `agents-plugin-tool/internal/mcp/review_watermark_checkpoint_test.go` |
| complexity.reuse_points | confirmed | `CheckpointNudgeWith` calls `Frontier` and `ResolveTrackWith` and accepts the Git runner used by existing callers (`agents-plugin-tool/internal/wsreview/checkpoint.go#L55-L73`) |
| complexity.side_effect_risk | moderate | The advisory reads the ledger and config, and additional Git work runs on manual rendering, route resolution, and ticket close |
| risk.correctness | high | Merge-parent path eligibility, net diff semantics, and legacy trigger precedence determine when every emitter advises a sweep |
| risk.fit | moderate | Interactive callers share the helper and the new filtered metrics must fit its existing Git and file-touch budget |
| risk.test | moderate | Coverage needs realistic merge graphs, partial config overrides, and parity across MCP and hosted rendering |
| risk.security_or_contract | moderate | Project-provided path exclusions reach Git and legacy config, caller eligibility, sweep range, and ledger semantics must remain compatible |

## Phases

### Phase 1: Measure reviewable changes and update the advisory

Implement in Codex. Reuse `CheckpointNudgeWith`,
the injected Git runner, `Frontier`, and `ResolveTrackWith`. Measure eligible
first-parent merges on the resolved review track and the net diff from the
frontier comparison base to the track tip. Use `<base>..<track-tip>` for the
candidate set and take only merges on the track's first-parent chain. Exclude
all `ai-docs/` paths and configured generated paths from each candidate merge's
delta against its first parent.
Keep a mixed merge eligible if it lands a reviewable path, including a binary,
mode or gitlink change, and count it once. Use the combined added and deleted
text line counts from the net diff, where changes reverted within the range
contribute zero lines. Apply the same path exclusions to that diff. Direct
commits can contribute to net lines.

Extend the existing local parser with `merges:`, `lines:`, and repeated
`exclude:` keys as defined in (1487). Use shipped defaults for thresholds
without a valid override in metric mode. Give valid new threshold keys
precedence over the legacy key, and retain the legacy trigger for legacy-only
overrides. Record the 25-merge / 20,000-line opt-in in devenv's local review
config in the development checkout that emits the advisory. Verify the
thresholds and explicit generated-path exclusions there, since the local file
is ignored by Git. The shared advisory uses one message in the applicable
units and carries the existing sweep range through the active emitters,
including hosted manual rendering.

#### Verification

- Update the Go fixtures for first-parent history with merged side-branch
  work, a comparison base off the first-parent chain, docs-only and
  generated-only merges, mixed changes, empty direct commits, reverted
  changes, and a non-`develop` review track. Verify that a mixed merge with
  reviewable text and `ai-docs/` edits counts as one merge and adds only
  eligible text lines. Verify that direct `ai-docs/` edits add no lines even in
  a range with qualifying changes. Verify that binary, mode-only, and
  gitlink-only deltas make otherwise eligible merges count without adding
  line totals.
- Cover shipped defaults and local overrides, repeated pathspec exclusions
  across both metrics, quiet below both thresholds, equality and independent
  triggering at each threshold, and one advisory when both thresholds are
  met. Cover the legacy-only trigger and new-key precedence, trailing
  non-clearing ledger entries, and baseline without a track. Verify that
  unavailable local objects in a partial clone cause no lazy fetch, advisory
  or ledger write. Assert consistent output through the active emitters.
- Compare Git subprocess counts per advisory before and after the change.
  Batch history work so the number of subprocesses does not grow with the
  number of merge candidates. Record the counts for both an ordinary range
  and a range containing many merges. On the same Windows baseline, record
  before and after file-operation counts and visible latency for the
  checkpoint actions under `ai-docs/manuals/performance.md#measuring`.

Run Go verification only on `w5090`, replacing `<who>`, `<min>`, and
`<worktree>` with the lock holder, minutes, and actual worktree. Record the
remote result in the phase result.

```bash
bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsreview/... ./internal/mcp/...'
```
