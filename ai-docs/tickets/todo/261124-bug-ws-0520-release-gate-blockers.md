---
title: 0.52 배포 게이트 차단 결함 수정 (werk·Pi 은퇴·인증)
related:
  261123-chore-ws-retire-pi-harness: source of the Pi-retirement findings; its Decisions 1-10 and owner answer (27)=a stay authoritative
  261122-feat-ws-builtin-agent-harness: source of the werk findings; its Decision 5 (worktree-scoped writes) is the violated contract
  261122-feat-ws-provider-auth-layer: source of the auth findings; its Decision 3 and redaction constraint are the violated contracts
  261124-chore-ws-remove-legacy-pi-handlers: the six hidden pi.* handlers stay until 0.53; not in scope here
---

# 0.52 release-gate blocker fixes (werk, Pi retirement, auth)

## Background

The ws 0.52.0 release-gate sweep over `b4d28c185..d70c307f1` (the
track/harness merge `d70c307f1`: Pi harness retirement, the in-daemon werk
harness, provider auth) returned `fix` with 7 Critical and 18 Important
findings. The review frontier stays at `b4d28c185` until this ticket lands
and a re-review clears it. Each item below is a source-confirmed finding
with its location at `d70c307f1`, the required behavior, and the regression
the fix must add. The finding list is the contract; it was produced by
twelve Codex reviewers (Correctness, Fit, Test across werk, Pi retirement,
auth, docs/packaging) and consolidated by the gate handler.

Out of scope: the six hidden `pi.*` handlers (removed at 0.53 by
`261124-chore-ws-remove-legacy-pi-handlers`), the 0.52 version bump itself
(the release lead does it at the release cut), and the owner's live provider
checks (OpenRouter, Sign in with ChatGPT, vLLM), which gate publish
separately.

## Decisions

- Every Critical and Important below is fixed in this ticket; none is
  carried as a follow-up. Rejected: shipping 0.52 with accepted Criticals —
  they are permission, data-loss, and credential defects on new surfaces.
- Each fix follows the remedy named in its item; where an item offers two
  remedies (C1: confine the mutation to the permitted directory entry, or
  refuse multiply linked targets under scoped authority), the implementer
  picks the simpler one that keeps the stated regression passing and
  records the choice in the commit's `## AI Context`.
- Compatibility: these fixes stay inside the already-approved 0.52 T3
  boundary (owner (27)=a). A fix that changes a cross-version surface beyond
  that boundary states its tier in the commit and goes to the lead first.

## Constraints

- `ai-docs/manuals/performance.md`, `ai-docs/manuals/release-compatibility.md`,
  `ai-docs/manuals/ws-mcp.md`, and `ai-docs/manuals/shipped-surface-boundary.md`
  apply (paths under `agents-plugin-tool/internal/`).
- Windows verification is required for C1 (hard links), I4 (line endings),
  and any file-identity check, on the Windows test host recipe.
- No live provider calls in tests; use local fixtures and synthetic secrets.

## Phases

### Phase 1: Code fixes (Critical and Important)

Critical:

- **C1 Scoped writes escape through hard links.** `internal/wsfs/file_intent.go:168`
  truncates and writes the existing inode after `files.go:126` validates the
  path; an out-of-worktree hard-link alias sees the change (werk Decision 5).
  Fix per the Decisions remedy. Test: `fs.write` and `fs.edit` under
  workdir-write leave an external hard-link alias unchanged.
- **C2 Active-turn close wedges the reopened werk lead.** `internal/wsagent/durable_close.go:92`
  clears the scheduler turn and cancels the host; `internal/werk/durable_turn.go:96`
  leaves the journal turn open; reopen via `handoff.go:252` then refuses
  every new turn at `durable_turn.go:65`. Persist and reconcile an
  intentional-close disposition while keeping recovery on ordinary daemon
  replacement. Test: close during an active request, reopen, a new turn
  succeeds in the same conversation; daemon replacement still recovers.
- **C3 Exec runs after a posture downgrade without the now-required approval.**
  `internal/execjob/hosted_launch.go:72` treats full-access auto-approval as
  an explicit grant, skipping the late posture check at line 176; the bind
  check at `internal/wsagent/mcp_session.go:225` accepts either write-capable
  authority. Track grant origin through dispatch and revalidate current
  authority; apply the same to file approval vs MCP file authority. Test:
  full-to-workspace-write downgrade during launch requires approval.
- **C4 Pi retirement puts open history on the 7-day purge.** `internal/wsagent/runner_startup.go:132`
  writes a closed record; the sweep then deletes the directory (ws
  transcript ring, daemon-owned `pi/session.jsonl`) after retention.
  Preserve or archive that history with a readable recovery path before any
  prune. Test: retire, restart, prune after seven days, read back.
- **C5 Surviving Pi holders vanish from pooled-worktree occupancy.**
  `internal/wsagent/runner_startup.go:126` indexes a live holder only as
  closed; `agent_spawn.go:318` checks active conversations only, so release
  and acquire miss the bound process. Track surviving holder bindings in the
  release and reuse guards until the holder exits; history still shows
  ended. Test: bound checkout refused before holder exit, allowed after.
- **C6 Daemon file tools expose provider secrets.** `internal/mcp/server_exec_cutover.go:36`
  admits daemon reads at read-only authority; `server_fs.go:83` and the
  read/search handlers lack the dashboard's protected-state checks, so the
  credential store (OAuth tokens, API keys) is readable, writable outside
  the store path, and leaked in edit output (auth Decision 3, redaction
  constraint). Apply the credential-file protections to the model-facing
  file boundary and its filesystem identity checks. Test: read-only read,
  search, and authorized write/edit paths with synthetic secrets.
- **C7 Automatic nudges spend the ChatGPT plan without background consent.**
  `internal/wsagent/runner_conv.go:1856` treats an inbox entry with no event
  ID or cron metadata as interactive; `internal/wsdashboard/ack_check.go:292`
  produces that shape; the bit bypasses `internal/wscred/openai_store.go:277`.
  Require positive operator provenance for the interactive flag, including
  queued and recovered input. Test: runner-to-credential cases for operator
  input, automatic nudge, and recovery (the durable test double must carry
  the flag).

Important:

- **I1** `internal/wsagent/handoff.go:305` Reopen lacks the new-start
  transition fence and can race past Full Quit. Apply the fence; test a
  concurrent Reopen.
- **I2** `internal/wsagent/werk_skills.go:89` bundled-only isolation still
  loads installed Claude/Codex skill roots. Respect the selection or exclude
  it from werk's creation contract; test both selections.
- **I3** `internal/wsagent/werk_mapper.go:202` a reopened mapper starts cost
  at zero and overwrites the displayed total. Reconstruct accumulated usage
  or publish deltas; test after idle wake and after daemon replacement.
- **I4** `internal/wsfs/files.go:193` exact edit rewrites unrelated mixed
  line endings and the diff misses it. Preserve bytes outside the edit; add
  a mixed LF/CRLF fixture.
- **I6** `internal/wsagent/retired_pi_tiers.go:68` legacy tier resolution
  refetches the OpenRouter catalog per binding. Share one snapshot; add a
  request-count fixture.
- **I7** `internal/mcp/tier_routing.go:162` preview/config resolve a legacy
  binding that spawn refuses (harness-wide vs provider check). Use the
  spawn provider check on every resolution surface; test with a second
  provider active and no OpenRouter key.
- **I8** `internal/werk/responses.go:173` the ChatGPT plan-usage limit code
  is retried as a generic 429. Handle it first, pause plan use, surface the
  usage-settings recovery; test it apart from a transient rate limit.
- **I9** `internal/wsagent/werk.go:151` signed-in-without-plan collapses to
  the sign-in prompt. Carry the provider reason through admission and
  surface the reconsent remedy; test in the picker/tier path.
- **I10** `internal/wscred/openai_test.go:212` the negative signature
  fixture only tests key lookup. Keep it and add an invalid signature with a
  resolvable key ID.
- **I15** `agents-plugin-tool/scripts/bump-ws-version.sh:107` global
  replacement would rewrite the historical `v0.51.2` rollback baseline in
  `ws-mcp.md:150`. Limit replacement to current-version references.
- **I16** `internal/wsagent/durable_close.go:94` a staged inbox claim
  survives Close and is delivered after Reopen. Reconcile staged claims as
  discarded at the close boundary; test Close/Reopen with an interrupted
  claim.
- **I17** `internal/wsdashboard/session_hire.go:35` owner hire approval
  drops the selected werk provider (defaults to OpenRouter). Carry the
  provider through approval, persistence, and create; nondefault-provider
  replay test.
- **I18** `internal/werk/responses.go:447` unbounded streamed events and
  retained fragments can exhaust the daemon. Bound decoded event and
  aggregate response size; refuse oversized responses before tool dispatch;
  local fixtures for both.

All paths above are under `agents-plugin-tool/` unless shown otherwise.

### Phase 2: 0.52 docs and release procedure

Depends on Phase 1 (C4's recovery path and the close/Full Quit behavior are
documented as fixed).

- **I5** `ai-docs/manuals/ws-mcp.md:953` document the werk creation
  contract: provider selector, required model, defaults, in-daemon identity
  binding.
- **I11** `CHANGELOG.md:3` add the 0.52 entry: Pi retirement and werk,
  execution and distribution changes, plugin update plus session restart,
  retained history, the 0.53 handler schedule.
- **I12** `ai-docs/manuals/ws-mcp.md:846` add the 0.52 T3 upgrade and
  recovery procedure: old launcher refusal, versioned hello guard,
  update-and-restart sequence, affected Pi calls.
- **I13** `ai-docs/ship/ws.md:13` record that this cut must be 0.52.0 (not a
  patch increment) and that the owner's live-provider checks precede
  publish.
- **I14** `ai-docs/manuals/ws-mcp.md:1540` Full Quit and replacement text:
  durable sessions count separately and can 409 with no OS holder; document
  admission fencing, survivor IDs, holder listing.
- Minor, same pass: `ws-mcp.md:146` provider setup and token-route wording
  (named local endpoint selector and key form);
  `release-compatibility.md:103` cite the launcher health refusal and the
  legacy Pi initialization pin; `internal/mcp/config_registry.go:456` and
  `server_config.go:440` use `werk.large` in alias examples.

Verification for the ticket: each item's named regression passes; full
`go test ./internal/mcp -timeout 45m` (the gate's run timed out at 10m),
`./internal/wsagent`, `./internal/werk`, `./internal/wsfs`,
`./internal/execjob`, `./internal/wscred -race`, `./cmd/ws-mcp`; Windows host
run for C1, I4 and C6 identity checks. The lead then re-reviews
`b4d28c185..<fix head>`.
