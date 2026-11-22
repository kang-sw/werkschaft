---
title: "0.51.1 패치 배포를 막는 0.51.0 호환성 문제 수정"
---

# Fix the 0.51.0 compatibility blockers found by the 0.51.1 release sweep

## Background

The owner approved an overnight 0.51.1 patch release (whiteboard item (2179)=a,
2026-11-22). The release-gate sweep over `f800ba30d..a96ec0ccc` (`f800ba30d` is
tag `v0.51.0`) found changes that put a T3 boundary or a security failure
against the released 0.51.0. A patch release cannot ship those
(`AGENTS.md` `### Compatibility Tiers`). The sweep's findings file is the
evidence authority:
`review-paths/f3f484d9-01-findings.md` in the project cache (path relayed by
the review handler).

Blockers known when this ticket was opened (the findings file is
authoritative and may add more):

1. `dashboard.companion_mode` and the companion sidecar/lead-tool enum values
   shipped in 0.51.0 and were removed by the companion config reshape
   (`8c87e23da`). Existing `config.tune` calls are refused and an enabled pet
   defaults off on upgrade.
2. 0.51.0 cannot restore MCP for active cutover holders whose
   `Record.MCPURL` gains `#exec-cutover`.
3. A forced `worktree.release` through a 0.51.1 forwarder refuses when the
   0.51.0 daemon returns an unknown route with a child bound.
4. A 0.51.0 local preview can read 0.51.1's new credential file after a
   rollback (security).

## Decisions

- Blocker 3 (forced `worktree.release` against a v0.51.0 daemon): against an
  old daemon only, a forced release warns and proceeds as v0.51.0 itself did;
  against a current daemon the refusal from `6617d5657` stays. Owner (2182)=a.
  Rejected: closing children through the dashboard machine credential (moves an
  authority boundary); keeping the refusal (a T3 in a patch); stopping only
  the caller's own children (still refuses on others).
- Blocker 8 (agy cross-harness spawn and wake refused): ships in 0.51.1 as a
  recorded compliance exception to the patch tier rule, because Antigravity's
  consumer terms never allowed the blocked use. Owner (2181)=a. Rejected: a
  0.52.0 minor bump (locks out every previous-minor plugin session); deferring
  the restriction (keeps shipping a terms violation).

## Constraints

- Lower each blocker to T0–T2 with a source-cited verdict per
  `ai-docs/manuals/release-compatibility.md`; never ship a T3 in a patch.
- Do not remove the new design; keep old keys, values and shapes accepted as
  aliases where needed.
- `ai-docs/manuals/performance.md` applies to every change under
  `agents-plugin-tool/`.

## Phases

### Phase 1: Lower every sweep blocker to a patch-safe tier

One commit per blocker. Each carries tests for its upgrade/downgrade scenario
and its compatibility tier in `## AI Context`.

Verification: a test per scenario; focused package suites pass; the release
sweep's delta review clears the fixes.
