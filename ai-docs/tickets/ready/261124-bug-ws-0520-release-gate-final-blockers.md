---
title: 0.52 최종 게이트 차단 결함 수정 (권한·자격 증명·출처·cron·Pi 점유·agy 감사)
related:
  261124-bug-ws-0520-release-gate-rereview-blockers: previous repair (merged 247f6c724); its Decisions stay authoritative except where this ticket refines C1, C6 and C7
  261124-bug-ws-0520-rereview-dashboard-fixes: scoped snap-deck navigation contract
  261122-feat-ws-provider-auth-layer: credential and background consent contracts (Decision 4, background consent for plan tiers; ai-docs/tickets/ready/261122-feat-ws-provider-auth-layer.md#L79-L84)
  261124-bug-ws-0520-final-dashboard-fixes: the dashboard UI findings of the same re-review (sent-state index, settings cleanup, browser reds), run in parallel by the UI/UX session
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 5d5605fdebfe79d1
sage-review-completeness-reviewed: 5d5605fdebfe79d1
---

# Fix the remaining ws 0.52 release-gate blockers

## Background

The 0.52 release-gate re-review of `cee60ac89..247f6c724` (findings in the
review cache, `35bb2392-01-findings.md`) recorded `block`: four Critical and
five Important product defects in the integrated repairs and feature merges.
This ticket owns Critical 1-4 and Important 1, 4 and 5. Important 2 and 3
(sent-state index, settings cleanup) are in
`261124-bug-ws-0520-final-dashboard-fixes`. The release stays blocked until
both land and a re-review clears.

## Decisions

- Every item below is fixed here; none is carried. Compatibility stays inside
  the approved 0.52 T3 boundary (owner (27)=a); a fix that changes a
  cross-version surface beyond it states its tier in the commit and goes to
  the lead first.
- C1 (refines the previous C1): the staging file never holds new bytes with
  access wider than the destination's. Unix: the staging file is created
  `0600` (exclusive create; under a directory default ACL the `0600` group
  bits mask every inherited named entry), and before any byte is written it
  receives the destination's permission metadata read from the verified
  destination handle: mode bits, owner, group, and an extended access ACL
  when the destination carries one. The metadata is rechecked against the
  destination before publication. When any of it cannot be reproduced (an
  owner change needs privilege, the group cannot be set, or the platform
  cannot copy the destination's extended ACL), the replacement fails closed
  with a clear error instead of publishing. A brand-new file (no prior
  destination) keeps today's mode. Rejected: copying mode bits alone (a
  different group or inherited ACL makes the same mode grant other users
  access).
  Windows: the staging file is created with the destination's security
  descriptor (DACL and its protection flag, read from the destination handle)
  passed at create time, so it is never narrowed afterwards; the effective
  DACL is verified before publication and a mismatch aborts the write. When
  the destination's owner or integrity label cannot be preserved without
  extra privileges, the write fails closed with a clear error. Rejected:
  relying on the directory's inherited ACL (publication discards a stricter
  destination DACL); writing before the mode is applied (the reviewed
  exposure window); narrowing the DACL after create (handles opened before
  the narrowing keep their access); silently publishing with a changed owner
  or label (a quiet permission change).
- C6 (refines the previous C6): credential loading, path protection and
  opened-file protection resolve the credential file the same way, following
  the final link as the loader does. A linked layout is protected at the link
  path and at the resolved target's identity, and keeps loading. One shared
  read-only follow-to-regular identity helper serves the committed and legacy
  credential names for both the loader and the guard; a resolution error is
  surfaced, never dropped (the guard must not silently lose an identity).
  Scrub and staging stay no-follow. The Git guard refuses worktree-content
  commands when the final credential name is itself a link, because a
  non-regular reparse point on Windows can read as no link. Rejected:
  refusing linked layouts at load (breaks existing layouts); scoping the Git
  refusal to the affected worktrees only (needs per-worktree link
  resolution for a rare layout; fail closed is simpler).
- C7 (refines the previous C7): model file-tool writes are refused for the
  whole daemon state directory (`wsdashboard.Dir`, the `dashboard` directory
  under the cache root), by path and identity. That covers the durable
  metadata store (`metadata.sqlite` and its `-wal`, `-shm` and `-journal`
  sidecars), session state, and every other daemon-owned store there; nothing
  in it is a legitimate model write target. Identity checks cover
  `metadata.sqlite` and its sidecars, and the trusted session-state files
  the daemon reads back as authority (durable Werk inputs and session
  records), so an external hard link cannot bypass
  the path check (the root-plus-identity pattern of the existing denied-path
  guard). When the state directory cannot be resolved, model writes the guard
  would cover fail closed instead of skipping the guard. Project-scoped workflow paths
  outside it (for example generated review and prompt paths) are unaffected.
  Reads are unchanged. Rejected: listing individual stores (misses sidecars
  and future stores); refusing reads too (outside this finding; exec remains
  the accepted residual, as for git.search).
- Cron consent: while background consent is off, an automated cron prompt
  is not steered into a running durable Werk turn admitted as interactive on
  a ChatGPT plan tier; it is held and delivered as its own background-admitted
  entry after that turn, so background consent applies to it. With background
  consent on, the owner-directed mid-turn steer delivery (`bebb8ef4`,
  reaffirmed by `a475a5d46`) is unchanged, because consent already allows
  automated inference on that account. This covers the input batching path as
  well as the active steer: a batch admitted into such a turn never mixes
  scheduled entries with operator entries; scheduled entries stay queued and
  are admitted later as a standalone background turn, using the existing
  scheduled/operator flags (no new persisted format). Steering into other
  turns keeps the current behavior. Rejected: carrying per-steer provenance
  through inference admission (more moving parts in the provider path for
  the same outcome); blocking only the active-steer path (batching still
  mixes cron text into the operator turn); holding cron prompts whenever
  such a turn runs, consent on or off (reverses the owner-directed mid-turn
  delivery without a consent reason).
- Dashboard deletion (C5 follow-up): the dashboard checkout deletion preview
  and final authorization use the same live Pi descriptor occupancy evidence
  as MCP release and reuse, at preview, at final authorization, and again
  before the archive purge. `wsdashboard` reaches that evidence through a
  narrow interface the runner implements (it cannot import `wsagent`).
- agy audit replay: reattachment restores the recorded manual compaction's
  effect on the usage baseline at its original position, so the original cut
  stays the only one. The cut's anchor is recovered from the retained cut
  already in the store (its source key carries boundary and counter) through
  an internal recovery path that distinguishes a cold, not-yet-loaded store
  from an empty one; no persisted format change.
- agy audit thought bound: the thought builder is bounded before accumulation
  and before redaction: the remaining budget is checked before each append.
  A thought that exceeds the bound drops its body and records one marker
  with a distinct capture-cap loss reason; further chunks are ignored until
  the next boundary. Rejected: keeping a truncated or redacted excerpt (a
  secret split at the cut would slip past redaction).

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- `ai-docs/manuals/performance.md`, `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/ws-mcp.md`, and `ai-docs/manuals/shipped-surface-boundary.md`
  apply (paths under `agents-plugin-tool/`).
- Windows verification on the native Windows test host for C1 (staging DACL
  and publication) and C6 (linked layout): a source build per
  `ai-docs/manuals/windows-dogfood.md`, run in a fresh per-run scratch folder
  with its state and config overrides inside that folder, touching no other
  process or user state on the host (the host's machine-local details are in
  the lead's untracked local manual, not this ticket).
- Synthetic credentials and provider fixtures only; no live provider calls.
- The baseline Chromium failures and the dashboard UI findings belong to
  `261124-bug-ws-0520-final-dashboard-fixes`.

## Prior Decisions

- 0267af9f (2026-11-24, commit): "Workdir-write must never truncate an inode linked outside its admitted path." — bearing: constrains
- 11b6fd53 (2026-11-24, commit): "Set operator origin at the trusted input boundary and carry it through inbox claims, durable queues, and active-turn replay." — bearing: constrains
- 261124-bug-ws-0520-release-gate-rereview-blockers (2026-11-24, commit 247f6c72): "The release lead's terms: impl branch, codex workers, Windows runs for C1/C6/git.search" — bearing: constrains
- 261124-bug-ws-0520-release-gate-blockers (2026-11-24, commit 062ed815): "Live OpenRouter, SIWC and vLLM checks remain publish gates outside this ticket" — bearing: constrains
- 261119-feat-ws-audit-capture-harness-adapters (2026-11-24, Result d452c4e2a): "Performance: zero extra file I/O (stays purely in memory on the ACP stream and holder tee; never tails Antigravity's local transcript.jsonl)" — bearing: constrains
- bebb8ef4 (2026-11-24, commit): "Owner-directed T0 hotfix to deliver ws/cron prompts mid-turn as steered inbox entries rather than holding them until turn end." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | Phases 1–3 name wsfs, wsdashboard and wsagent sources plus ai-docs/manuals/ws-mcp.md. |
| scope.surface | cross-module | Model file protection feeds MCP mutation admission; runner occupancy feeds dashboard deletion; cron input reaches durable provider admission. |
| scope.new_public_symbol | no | Phases repair existing boundaries; no new public entry point is specified. |
| scope.new_type_contract | no | Decisions retain the approved 0.52 boundary; no new wire or persisted shape is specified. |
| scope.test_surface | existing | wsfs/file_intent_test.go, wsagent/cron_seams_test.go, audit_capture_test.go, audit_agy_test.go and wsdashboard/railworktreedelete_test.go exist under agents-plugin-tool/internal/. |
| complexity.reuse_points | confirmed | Read commitReplacement (wsfs/file_intent.go:226), NewProviderCredentialProtection (wsdashboard/fs_provider_credentials.go:39), NewModelFileWriteProtection (:15), mapKept (wsagent/audit_capture.go:784) and agy thought assembly (audit_agy.go:99-111). |
| complexity.side_effect_risk | high | Replacement publication changes platform permissions; admission changes queued cron delivery; deletion can remove a checkout. |
| risk.correctness | high | Preserve restricted destinations, credential identity, durable provenance, holder occupancy and replay compaction position across independent repairs. |
| risk.fit | moderate | Cron exception must preserve steering in other turns; directory protection must leave project-scoped generated paths usable. |
| risk.test | high | Windows DACL and linked-layout checks require runtime verification; replay timing and descriptor-only occupancy need dedicated regressions. |
| risk.security_or_contract | high | Four Critical findings concern file permissions, credential disclosure, model-writable operator provenance and background-consent bypass. |

## Phases

### Phase 1: Enforce filesystem and provider boundaries

- **C1** `internal/wsfs/file_intent.go:234`, `internal/wsfs/file_intent_unix.go:68`,
  `internal/wsfs/file_intent_windows.go`. Test: a private destination in a
  shared directory; the staging file's effective access (Unix mode, owner,
  group and access ACL, including under a directory default ACL; Windows
  DACL) is never wider than the destination's while it holds bytes, and the
  published file keeps the destination's restrictions; on Unix a
  destination whose group or extended ACL cannot be reproduced aborts
  without publishing; on Windows a DACL mismatch or an
  owner/label that cannot be preserved aborts without publishing.
- **C6** `internal/wsdashboard/fs_provider_credentials.go:55`. Test: a
  synthetic linked credential layout loads, and model file-tool reads and
  writes through the link path and the target path are refused; the
  regular-generation scrub and retirement tests still pass; a Git
  worktree-content command is refused while the credential name is a link.
- **C7** `internal/wsdashboard/fs_model_write_protection.go:15`,
  `internal/wsagent/runner_durable.go:28`. Test: through the production model
  mutation boundary, writes to the real metadata store and its sidecars, and
  to another daemon-state file, are refused, including through an external
  hard link to the metadata store and to a trusted session-state file;
  writes outside the daemon state
  directory still work; an unresolvable state directory fails closed. The runbook in `ai-docs/manuals/ws-mcp.md` states the
  protected directory.
- **Cron consent** `internal/wsagent/cron_seams.go:100`. Test: with background
  consent off, a cron prompt arriving during an active operator plan turn is
  not admitted into that turn and is not sent as an interactive inference;
  it is delivered after the turn as its own background entry; an operator
  entry and a cron entry queued together are not batched into one
  interactive turn while consent is off; with consent on, mid-turn steer
  delivery is unchanged; operator input and cron delivery into other turns
  are unchanged.

### Phase 2: Dashboard deletion occupancy

- **Dashboard deletion** `internal/wsdashboard/railworktreedelete.go:465`.
  Test: preview and final deletion refuse a checkout with an active legacy Pi
  descriptor without a saved holder ID (including a holder that appears
  between preview and purge), and allow it after the holder exits.

### Phase 3: agy audit

- **Replay baseline** `internal/wsagent/audit_capture.go:784`. Test: restart
  after the recorded manual cut and before the next usage sample; exactly one
  cut remains, at the original position.
- **Thought bound** `internal/wsagent/audit_agy.go:94`. Test: a long thought
  carried across finalized intervals never grows the builder past the bound,
  and is recorded as one capture-cap loss marker without body text.

All code paths are relative to `agents-plugin-tool/`.

Verification: each named regression; full `go test ./internal/mcp -timeout
45m`; `./internal/wsagent`, `./internal/wsfs`, `./internal/wscred -race`,
`./internal/wscron`, `./cmd/ws-mcp`; focused `./internal/wsdashboard` tests for
C6, C7 and deletion; the Windows host runs above. The lead then re-reviews
`41e81115c..<fix head>`.
