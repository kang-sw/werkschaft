---
title: "운영 데몬에서 감사 실시간 캡처가 연결되지 않음"
related:
  261119-feat-ws-audit-capture-harness-adapters: provides the hosted-session adapters and holder audit channel
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
result, hosted Claude leads and Codex workers produce zero audit events,
and the dashboard audit inspector shows `No audit events recorded`
(`agents-plugin-tool/internal/wsi18n/tables/en.json:3264`).
`CHANGELOG.md:9–12` documents the missing wiring.

The production `wsholder.Options` also omits `AuditChannel`
(`agents-plugin-tool/cmd/ws-mcp/dashboard.go:107–112`). The manager offers
the audit channel only when that option is enabled
(`agents-plugin-tool/internal/wsholder/manager_scan.go:197–204`), so
Claude lead lines filtered out of the holder ring never reach capture.

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

## Phases

### Phase 1: Wire audit sink and holder channel in the production daemon

Bind the store returned by `(*wsdashboard.Daemon).AuditSink`
(`agents-plugin-tool/internal/wsdashboard/audit.go:71–78`) to the hosted
runner after the daemon opens the store and before the runner starts or
reattaches hosted sessions. Preserve dashboard startup when the store is
unavailable. Enable `AuditChannel` on the production holder manager so
capable Claude lead holders tee ring-skipped lines into capture. Reuse
the existing capture policy, adapters, store, and audit routes.

Add a regression test that uses the production daemon wiring, feeds a
kept event through a hosted session, and reads the expected event for
that session from the real audit store. The test must fail when the
production sink binding is missing. Replace the “not wired” note in
`CHANGELOG.md` with a line recording that hosted live capture is enabled.

Run Go tests only on `w5090`. Substitute the lock owner, time in minutes,
and worktree in the command, and create the local log directory before
redirecting stdout. Keep `go test`, `go build`, other Go tools, and ws
executables off the local PC.

```bash
bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; python3 scripts/generate-package-assets.py && cd agents-plugin-tool && go test ./internal/wsdashboard/... ./internal/wsagent/... ./cmd/ws-mcp/...' > D:/Temp/ws-audit-live-capture/go-test.log
```
