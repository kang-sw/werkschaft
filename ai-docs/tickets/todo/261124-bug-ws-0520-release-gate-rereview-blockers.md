---
title: "Resolve ws 0.52 release-gate re-review blockers"
related:
  261124-bug-ws-0520-release-gate-blockers: "Original release-gate repair and governing decisions"
  261123-bug-ws-daemon-replace-gap-older-daemon: "Daemon minor-floor contract"
---

# Resolve ws 0.52 release-gate re-review blockers

## Background

The 0.52 release-gate re-review of `b4d28c185..ae485f20b` found remaining
permission, credential, and Pi history defects after `062ed8158`. It also
found caller-visible problems in the dashboard and launcher changes in
the same range. The release remains blocked on the items below.

## Constraints

Keep the approved 0.52 compatibility boundary and the matched v0.51.2
rollback contract. Record the cross-version effect of any new stored state
or admission behavior before settling the fix. Run provider-boundary
verification with synthetic credentials and local fixtures. The separate
pre-existing `git.search` credential hard-link issue remains outside this
release-gate ticket.

## Phases

### Phase 1: Close the re-review findings

- Keep scoped writes confined to their admitted destination through commit,
  including concurrent changes to file link identity. Keep the permission
  decision effective through exec and file dispatch when posture changes
  during an in-flight call. See `internal/wsfs/file_intent.go`,
  `internal/execjob/hosted_launch.go`, and `internal/wsagent/mcp_session.go`.
- Protect provider credentials across credential-store updates and daemon
  restarts. Verify operator provenance through inbox persistence and replay
  using daemon-owned state or an equivalent trust boundary. See
  `internal/wsdashboard/fs_provider_credentials.go` and
  `internal/wsagent/runner_conv.go`.
- Preserve Pi transcript history added during a matched v0.51.2 rollback
  when its pruner later removes the session directory. Keep archived history
  deleted after an explicit purge, including failures during cleanup. Count
  surviving Pi holders in worktree occupancy when startup was interrupted
  before the runner saved the holder ID. See
  `internal/wsagent/pi_history_archive.go` and `internal/wsagent/runner_startup.go`.
- Enforce the Responses output budget before allocating reconstructed JSON
  arguments. Surface the ChatGPT plan reconsent remedy when another werk
  provider is available. See `internal/werk/responses.go` and
  `internal/wsagent/werk.go`.
- Keep steady-state snap-deck navigation bounded when hydrating a topic
  updated in the background or traversing a board with many far drafts.
  Preserve detached history matches when the topic reattaches during a
  search. Accept docked-frame wheel deltas only from the active page. See
  `dashboard-board-anchor.js`, `stream.js`, and `cards.js`.
- Preserve folded topic geometry and accessible names on focused Overview
  Quick Look placeholders. Exercise the real composer send and exec
  acknowledgement reset paths independently. See `overview.js`,
  `dashboard-board.css`, and `composer-multiline.test.cjs`.
- Document the floor-aware launcher boundary and the usable matching
  v0.51.2 rollback procedure in `ai-docs/manuals/ws-mcp.md`.

Add regressions for the failure paths above and independently re-review
the new release-gate head.
