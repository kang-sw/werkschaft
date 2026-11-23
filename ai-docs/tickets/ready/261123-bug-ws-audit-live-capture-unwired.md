---
title: "운영 데몬에서 감사 실시간 캡처가 연결되지 않음"
related:
  261119-feat-ws-audit-capture-harness-adapters: provides the hosted-session adapters and holder audit channel
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: cfa3bee590957a60
sage-review-completeness-reviewed: cfa3bee590957a60
---

# Live audit capture is unwired in the production daemon

## Background

The production dashboard daemon opens the audit store but never passes it
to the hosted-session runner. `newAgentRunner` leaves
`wsagent.Options.AuditSink` nil
(`agents-plugin-tool/cmd/ws-mcp/dashboard.go:116,453–514`). `StartDaemon`
opens the store and starts the runner without binding the sink
(`agents-plugin-tool/internal/wsdashboard/daemon_start.go:236–247`).
`startBackend` creates a capture tap only when the sink is set
(`agents-plugin-tool/internal/wsagent/runner_create.go:396–410`). As a
result, new output from hosted Claude leads and Codex workers produces
zero audit events through this path. For an empty session, the dashboard
audit inspector shows `No audit events recorded`
(`agents-plugin-tool/internal/wsdashboard/assets/dashboard-audit.js#L325`,
`agents-plugin-tool/internal/wsi18n/tables/en.json:3264`).
`CHANGELOG.md#L10-L12` documents the missing wiring, and its search entry
says search finds nothing until live capture is enabled
(`CHANGELOG.md#L37-L44`).

The production `wsholder.Options` also omits `AuditChannel`
(`agents-plugin-tool/cmd/ws-mcp/dashboard.go:107–112`). The manager offers
the audit channel only when that option is enabled
(`agents-plugin-tool/internal/wsholder/manager_scan.go:197–204`), so
Claude lead lines filtered out of the holder ring never reach capture.

The related adapter ticket is in `ready/`. Claude capture (Phase 1),
Codex capture (Phase 2), and store-integrated restart and replay
verification (Phase 5) have Results. The agy and Pi adapters (Phases 3
and 4) remain open. The stream's `auditSource` is implemented by
`(*claudeMapper).newAudit` and `(*codexMapper).newAudit` in
`agents-plugin-tool/internal/wsagent/`.

## Decisions

- **Release (1419)=b, owner-confirmed 2026-11-23.** Fix this now and ship
  it in v0.51.2. Merge the fix before the `71e875ecf..develop` re-review.
  The owner rejected deferring to the next release and putting the fix on
  hold.
- **Implementation.** One backend phase owned by a Codex worker. The
  existing dashboard audit reader and inspector display events once the
  daemon supplies them.
- **Compatibility tier T0.** The v0.51.2 capture path feeds the existing
  writer, which appends the same per-session `seg-*.jsonl` records under
  `<cache root>/dashboard/audit/`
  (`agents-plugin-tool/internal/wsdashboard/audit.go:47–60`,
  `agents-plugin-tool/internal/wsauditstore/ring.go:25–43`,
  `agents-plugin-tool/internal/wsauditstore/store_writer.go:214–254`, and
  `agents-plugin-tool/internal/wsauditstore/ring_write.go:186–248`). The
  v0.51.1 tag has the same writer and reader. On reopen, both versions
  recover retained events by replaying those segment records
  (`agents-plugin-tool/internal/wsauditstore/ring_recover.go:165–187,292–338`
  at v0.51.1 and on `develop`). The holder continues to negotiate the
  existing optional attach offer. A holder that predates the offer
  attaches normally
  (`agents-plugin-tool/internal/wsholder/manager.go:43–47`).

## Constraints

- **File-touch budget.** Reuse the Type 1 append-only audit segment ring
  and its in-memory state. Keep the capture handoff nonblocking and disk
  I/O off the interactive path. Steady-state writes append deltas through
  the existing writer with zero per-event rereads or rewrites. Report file
  operations per kept event and distinguish steady-state appends from
  segment opening and rotation. See `AGENTS.md` Code Standard 6 and
  `ai-docs/manuals/performance.md` under `Rules` (1–5), `Storage Types`
  and `Measuring`.
- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)

## Prior Decisions

- 261123-bug-ws-audit-search-empty-budget-page (2026-11-23, Result): "Sweep Important 6 (CHANGELOG claimed live audit capture) was fixed by the lead in fcecba92a, outside this branch." — bearing: supports
- d05aba26ba (2026-11-22, commit): "Index.OnRemove runs under the index lock; auditSessionRemoved only enqueues a non-blocking Touch" — bearing: constrains
- 94a00415b (2026-11-22, commit): "Production wiring is intentionally off: cmd/ws-mcp sets neither Options.AuditSink nor wsholder Options.AuditChannel until the audit store lands (ring-store / Phase 5)." — bearing: supports
- 798e456fb (2026-11-22, commit): "Attach handoff contract: Manager.AuditAttach(id) returns the current connection's EventAuditAttached (Next, Conn) while that client is live" — bearing: constrains
- ae60db30b (2026-11-22, commit): "Audit envelope Phase 1: new stdlib-only internal/wsaudit package (envelope, 11 kinds, model-seen cut, capture policy gate over normalized events, sink interface); unwired, so no daemon swap needed." — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/cmd/ws-mcp/dashboard.go`, `agents-plugin-tool/internal/wsdashboard/daemon_start.go`, runner wiring and regression coverage, `CHANGELOG.md`, and `ai-docs/manuals/ws-mcp.md` |
| scope.surface | cross-module | daemon startup binds the store to the runner and enables the holder manager's existing attach capability |
| scope.new_public_symbol | unknown | Phase 1 specifies when the sink binds and leaves the Go wiring interface to implementation |
| scope.new_type_contract | unknown | Phase 1 reuses `wsaudit.SourcedSink` and leaves the startup handoff signature to implementation |
| scope.test_surface | existing | `cmd/ws-mcp/agent_mcp_test.go`, `internal/wsagent/audit_store_test.go`, `TestAuditRealHolderAttachReachesCapture`, and `internal/wsdashboard/audit_test.go` exercise adjacent seams; Phase 1 checks production wiring and startup without a store |
| complexity.reuse_points | confirmed | `Daemon.AuditSink`, `wsagent.Options.AuditSink`, `wsholder.Options.AuditChannel`, and the existing capture, store and read paths |
| complexity.side_effect_risk | high | enabling capture activates bounded persistent writes and holder audit traffic on hosted sessions |
| risk.correctness | high | the bind must precede hosted start and reattach while a failed store open still permits startup |
| risk.fit | moderate | the runner is constructed in the command package and started inside `StartDaemon` |
| risk.test | high | existing fixtures inject capture or explicitly enable the holder offer; production coverage must detect either missing binding and check startup without a store |
| risk.security_or_contract | high | live capture begins persisting tool bodies through the existing masking path and must preserve older holder attach behavior |

## Phases

### Phase 1: Wire audit sink and holder channel in the production daemon

Bind the store returned by `(*wsdashboard.Daemon).AuditSink`
(`agents-plugin-tool/internal/wsdashboard/audit.go:71–78`) to the hosted
runner after the daemon opens the store and before the runner starts or
reattaches hosted sessions. Preserve dashboard startup when the store is
unavailable. Enable `AuditChannel` on the production holder manager so
capable Claude lead holders tee ring-skipped lines into capture. Reuse
the existing capture policy, adapters, store, and audit routes.

Verification:

- Feed a kept event through a hosted session using the production daemon
  wiring. Read the expected event for that session from the real audit
  store and verify that removing the production sink binding fails the test.
- With the production holder configuration and a capable Claude holder,
  assert that the stored thinking body includes a delta delivered only by
  the audit channel. Use `TestAuditRealHolderAttachReachesCapture` as the
  fixture reference. Disabling the production holder offer must fail this
  check.
- Exercise production startup with an audit path the store cannot open.
  Assert that the daemon starts and can run a hosted session without an
  audit sink.

Update the live capture and search caveats in `CHANGELOG.md#L10-L12` and
`CHANGELOG.md#L37-L44` to reflect that hosted live capture is enabled for
the existing Claude and Codex adapters. Update
`ai-docs/manuals/ws-mcp.md#L1477-L1479` to describe the production sink
binding when the store opens and the existing optional holder audit
offer for capable Claude holders.

Run Go tests only on `w5090`. Substitute the lock owner, time in minutes,
and worktree in the command, and create the local log directory before
redirecting stdout. Keep `go test`, `go build`, other Go tools, and ws
executables off the local PC. The asset generator is at
`agents-plugin-tool/scripts/generate-package-assets.py` in the synced checkout.

```bash
bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/... ./internal/wsagent/... ./cmd/ws-mcp/...' > D:/Temp/ws-audit-live-capture/go-test.log
```
