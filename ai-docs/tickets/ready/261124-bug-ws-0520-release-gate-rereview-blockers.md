---
title: 0.52 게이트 재리뷰 차단 결함 수정 (권한·자격 증명·Pi 기록·git.search)
related:
  261124-bug-ws-0520-release-gate-blockers: original release-gate repair (merged 062ed8158); its Decisions stay authoritative except where this ticket refines C4
  261123-bug-ws-daemon-replace-gap-older-daemon: daemon minor-floor contract (I9-runbook item)
  261122-feat-ws-provider-auth-layer: its Decision 3 and redaction constraint are the credential contracts C6, C7 and the git.search item protect
  261124-chore-ws-remove-legacy-pi-handlers: the six hidden pi.* handlers stay until 0.53; not in scope
  261124-bug-ws-0520-rereview-dashboard-fixes: the dashboard findings of the same re-review, run in parallel by the UI/UX session
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: e427bdcec26223dc
sage-review-completeness-reviewed: e427bdcec26223dc
---

# 0.52 release-gate re-review blockers (permissions, credentials, Pi history, git.search)

## Background

The 0.52 release-gate re-review of `b4d28c185..ae485f20b` (findings in the
review cache, `0f55934a-01-findings.md`) recorded `block`: six Critical and
ten Important findings after the first repair at `062ed8158`. The prefix
`b4d28c185..cee60ac89` cleared. This ticket owns the non-dashboard findings
(C1, C3, C4, C5, C6, C7, I9, I18, the Pi purge revival, the floor runbook),
plus the pre-existing `git.search` credential hard-link exposure, which
shares C6's credential-identity design. The dashboard findings (snap-deck
work, detached history reply, doc-frame wheel, Overview geometry and focus,
composer reset tests) are in `261124-bug-ws-0520-rereview-dashboard-fixes`.

## Decisions

- Every item below is fixed here; none is carried. Compatibility stays
  inside the approved 0.52 T3 boundary (owner (27)=a); a fix that changes a
  cross-version surface beyond it states its tier in the commit and goes to
  the lead first.
- C1: scoped writes commit through a replacement directory entry (write a
  sibling temporary file, then rename it over the destination) with the
  existing recovery and destination checks, so a concurrent link change can
  never route the mutation into a shared inode. Rejected: sampling the link
  count before an in-place write (the race the re-review found).
- C3: privilege-sensitive effect admission (the hosted-exec holder spawn and
  the file mutation's prepare/apply) runs under the same guard as posture
  transitions, with the final authority check inside it. An operator grant
  is awaited outside the guard and re-checked inside it at dispatch.
  Rejected: a check followed by admission without that guard.
- C4 (refines the first ticket's C4 decision): 0.52 preserves all Pi history
  that existed when 0.52 last ran, across a matched 0.51.2 rollback prune.
  History that 0.51.2 itself writes while it runs after a rollback follows
  0.51.2's own retention, like any other data the old version owns; the
  runbook and `release-compatibility.md` state this. Rejected: preventing
  the old pruner from removing old-version writes (needs 0.52 to steer
  0.51.2's retention, which it cannot do without fragile record tricks).
- Pi purge: an explicit purge deletes the archive before it reports success;
  if the archive deletion fails, the purge returns a retryable error and
  startup does not restore an archive the purge was deleting (a committed
  deletion state). Rejected: logging the archive error and reporting success.
- C5: worktree occupancy also counts live holder descriptors (backend Pi,
  cwd inside the worktree) that have no saved holder ID in a runner record,
  until the holder exits. History still shows the conversation as ended.
- C6: after a credential-store update returns, no previous file generation
  retains credential bytes, and while the update runs the retiring
  generation stays inside the protected boundary, and the current name
  always holds a valid committed file. The update writes the new content to
  a sibling temporary file, adds a store-owned retiring hard link to the
  current file inside the protected store directory, renames the temporary
  file over the current name (the existing atomic replacement), then
  truncates and removes the retiring link, so a pre-existing alias ends up
  seeing an empty file. If the rename fails, the update removes the retiring
  link without truncating and returns the error; the current file is
  untouched. During the window the retiring link is a protected regular
  credential file: the model-facing guard refuses it by path and identity,
  and the git.search link count includes it (a worktree-reading request
  during an update is refused; accepted, the window is short). Startup
  recovery: a leftover retiring link whose identity matches the current
  file is only removed (the rename never happened); one whose identity
  differs is truncated and removed. The model-facing guard keeps protecting
  the current generation by identity, across restarts. Rejected: renaming
  the current file away before publishing (leaves the current name empty on
  failure); scrubbing after publishing with no protected name for the
  retired generation (an unprotected generation and no crash recovery);
  persisting a list of
  retired identities (inode reuse makes it a false refusal source and it
  adds file I/O); in-place credential writes (loses crash atomicity). The
  implementer verifies the hard link, the rename over the current name, and
  the truncate of the retiring link on Windows (open handles, share-delete) and records the mechanism in
  `## AI Context`.
- C7: operator provenance comes only from daemon-owned state that the
  model-facing file tools cannot write: the session state the runner trusts
  for the interactive flag joins the protected-state set the C6 guard
  enforces (read-only for the model; writes refused), and the flag survives
  queue and replay only through that state. Rejected: signing the flag (the
  key would sit in the same writable state or be lost on restart).
- git.search (pre-existing; folded in because it depends on C6): before a
  worktree-reading request runs, refuse it when any protected regular
  credential file (not directory, including C6's retiring file) has more
  than one link. With C6's scrub, retired generations hold no secret bytes,
  so counting the files currently in the store is sufficient.
  Worktree-reading requests: `grep` with neither `--cached` nor a tree-ish
  (includes `--untracked`); every `blame`, with or without a revision,
  because blame always loads the worktree `.mailmap`;
  `ls-files` worktree modes (`--eol`, `-m`, `-d`, `-o`, `-k`, `-i`); and
  `log`/`show` while mailmap is enabled (Git's default, or the allowed
  mailmap flags), because the worktree `.mailmap` feeds their output. Index
  and revision reads without mailmap keep their behavior. The refusal names
  neither the alias nor any content and says how to remove the extra link.
  Rejected: per-path checks of reported paths (pathless, count and context
  output carry no filename); a worktree scan for multiply linked files
  (performance manual). Residual race accepted: creating an alias during a
  request takes exec, which can read the credential file directly.

## Constraints

- `ai-docs/manuals/performance.md`, `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/ws-mcp.md`, and `ai-docs/manuals/shipped-surface-boundary.md`
  apply (paths under `agents-plugin-tool/`).
- Windows verification on the Windows test host recipe for C1 (rename over
  destination), C6 (hold-and-rename scrub), and git.search link counts
  (Windows link count comes from the file handle; the C1 helper in
  `internal/wsfs/file_intent_windows.go` is package-private today).
- Synthetic credentials and local fixtures only; no live provider calls.

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/ws-mcp.md (declared for agents-plugin-tool/internal/mcp/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 261124-bug-ws-0520-release-gate-blockers (2026-11-24, commit 062ed815): "Live OpenRouter, SIWC and vLLM checks remain publish gates outside this ticket; the git.search credential hard-link finding is reported separately." — bearing: constrains
- 7b77fad5 (2026-11-24, commit): "C1 uses the approved multiply linked target refusal. It keeps the existing pinned file commit path and leaves both hard-link aliases untouched under workdir-write." — bearing: supports
- bffb02ba (2026-11-24, commit): "Check protected identities on the pinned mutation handles before reading or writing, and resnapshot after recording to cover a credential identity introduced during the callback." — bearing: constrains
- e4329847 (2026-11-24, commit): "The transcript ring stays type 1 and the raw Pi JSONL stays a type 5 file. Backup and recovery touch files on startup, and the normal prune adds no file reads." — bearing: constrains
- 11b6fd53 (2026-11-24, commit): "Set operator origin at the trusted input boundary and carry it through inbox claims, durable queues, and active-turn replay. A queued legacy interactive bit was inferred and cannot qualify." — bearing: constrains
- 22bc12a6 (2026-11-24, commit): "Stored daemon-minor-floor as a plain major.minor file beside dashboard.lock and last-port (storage type 5)" — bearing: constrains
- 05398675 (2026-11-24, commit): "SIWC tiers require account background consent, with credential scope rechecked for every inference request." — bearing: constrains
- 1e31337a (2026-11-24, commit): "Reuse the stateless Responses adapter and in-process credential source. Preserve the hosted default and keep provider identity separate from the model slug in saved tiers." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsfs/file_intent.go, internal/execjob/hosted_launch.go, internal/wsagent/pi_history_archive.go, internal/wscred/private_file.go, internal/mcp/git_search.go; ai-docs/manuals/ws-mcp.md and release-compatibility.md |
| scope.surface | cross-module | File admission, posture transitions, credential replacement, runner recovery, Git search and provider picker span package boundaries |
| scope.new_public_symbol | unknown | Phase 1 settles behavior but does not name the exported guard or link-count interface needed between wsagent, execjob, wsfs and mcp |
| scope.new_type_contract | yes | Pi purge adds a committed deletion state recovery must respect; C3 shares effect admission with posture transitions; C7 expands protected state |
| scope.test_surface | existing | agents-plugin-tool/internal/mcp/git_search_test.go, internal/wsfs/file_intent_test.go, internal/werk/responses_test.go; original repair Result names existing runner and credential regressions |
| complexity.reuse_points | confirmed | Read commitFile's pinned handle and link check, archiveRetiredPiHistory/restoreRetiredPiHistory/discardRetiredPiHistory, writePrivateFile, ProviderCredentialProtection, hostedChoices and runGitSearch |
| complexity.side_effect_risk | high | Rename-based file mutation, retired credential inode scrubbing and committed history deletion affect durable state and recovery |
| risk.correctness | high | Concurrent authority downgrade and link creation, rollback retention, interrupted purge and orphan holder occupancy require coordinated boundaries |
| risk.fit | moderate | Preserve existing pinned destination checks, credential atomic replacement, the approved 0.52 boundary and refined C4 rollback contract |
| risk.test | high | Concurrency and restart regressions, Windows share-delete/link counts, mixed-provider picker and encoded argument expansion need distinct fixtures |
| risk.security_or_contract | high | Scoped writes, approval admission, credential aliases and operator provenance control filesystem authority and ChatGPT plan consent |

## Phases

### Phase 1: Code fixes

- **C1** `internal/wsfs/file_intent.go:203`. Test: a link added between
  admission and commit leaves the external alias unchanged.
- **C3** `internal/execjob/hosted_launch.go:222/263`,
  `internal/wsagent/mcp_session.go:218`. Test: a downgrade racing each
  interval (exec spawn, file prepare/apply) requires approval; an operator
  grant obtained before a downgrade is re-checked.
- **C4** `internal/wsagent/pi_history_archive.go:96`. Test: archive, apply
  the 0.51.2 prune rule, reopen 0.52, read back; docs state the rollback-era
  retention.
- **Pi purge** `internal/wsagent/pi_history_archive.go:76`. Test: archive
  deletion failure returns a retryable error and a restart does not restore
  the history; a retry completes the purge.
- **C5** `internal/wsagent/runner_startup.go:130`. Test: a live Pi holder
  descriptor without a saved ID blocks release and reuse of its checkout
  until the holder exits.
- **C6** `internal/wsdashboard/fs_provider_credentials.go:67`,
  `internal/wscred/private_file.go:42`. Test: an alias to the previous
  generation reads empty after an update and after a restart; current
  and retiring generation reads and writes stay refused (a hook pauses the
  update between the link and the scrub); a failed rename leaves the old
  current file intact; a crash at each step of the sequence recovers to one
  valid current file holding the old or new content and no retiring link.
- **C7** `internal/wsagent/runner_conv.go:1856`. Test: a model file-tool
  write to the trusted session state is refused; operator input, automatic
  nudge, and replay keep the right flag.
- **I9** `internal/wsagent/werk.go:207`, `werk_models.go:138`,
  `internal/wsdashboard/assets/dashboard-leads.js:374`. Test: OpenRouter or a
  local endpoint available plus a plan-less ChatGPT sign-in keeps the
  reconsent remedy in the picker.
- **I18** `internal/werk/responses.go:738`. Test: argument-delta expansion
  past a small local limit is refused before the large allocation.
- **git.search** `internal/mcp/git_search.go:221,744,760-772`. Tests
  (synthetic store, tracked and untracked alias): every worktree-reading
  form refused before Git runs (`grep` plain, `-c`, `-l`, `-h`, `-C`,
  `--untracked`; `blame` with and without a revision; `ls-files --eol`,
  `-m`; `log`
  with mailmap); refusal text has no alias path or secret byte; index and
  revision reads without mailmap still run (including the historical
  control at `git_search_test.go:323-325`); requests run normally once the
  alias is removed.

All paths are under `agents-plugin-tool/` unless shown otherwise.

### Phase 2: Docs

Depends on Phase 1.

- Floor runbook `ai-docs/manuals/ws-mcp.md:1759`: the matched v0.51.2
  rollback steps (its launcher cold-starts its own binary after the newer
  daemon stops) and `--below-floor` scoped to floor-aware candidates.
- C4 rollback-era retention in the Pi retention section of `ws-mcp.md` and
  the rollback row of `release-compatibility.md`.
- A read-through confirms the C6 scrub, C7 protected state, and git.search
  refusal text match the final behavior.

Verification: each named regression; full `go test ./internal/mcp -timeout
45m`; `./internal/wsagent`, `./internal/werk`, `./internal/wsfs`,
`./internal/execjob`, `./internal/wscred -race`, `./cmd/ws-mcp`; the
Windows host runs above. The lead then re-reviews `cee60ac89..<fix head>`.
