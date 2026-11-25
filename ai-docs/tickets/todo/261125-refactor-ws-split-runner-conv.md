---
title: "wsagent/runner_conv.go 분할 리팩터링 (2,340줄 파일 크기 권고 대응)"
related:
  261125-feat-ws-exec-completion-chat-row: "edits wsagent/runner_conv.go; sequence or rebase around it"
---

# Split wsagent/runner_conv.go below the file size limit

## Background

`agents-plugin-tool/internal/wsagent/runner_conv.go` grew to 2,340 lines,
reaching the hard-limit file size advisory during the `0.52.1`/`0.52.2`
release gate (`handoff-0521-office-261125`).

Per owner decision `(1588)=a` (2026-11-25), splitting `runner_conv.go` is
deferred out of the `0.52.2` release gate into this dedicated follow-up
refactor ticket. Two cohesive extraction seams are already identified in
`runner_conv.go`:

- **AGY native replay staging/settlement (~250 lines, `runner_conv.go:1541-1790`):**
  `stageAgyNativeReplay`, `finishAgyNativeReplay`, `resolveAgyNativeReplay`,
  and `settleAgyNative`.
- **Registration & foreign-row ownership (~210 lines, `runner_conv.go:1218-1430`):**
  `release`, `dropHeld`, and ownership helpers.

## Decisions

- **(1588)=a, owner, 2026-11-25: defer splitting `agents-plugin-tool/internal/wsagent/runner_conv.go` out of the `0.52.2` release gate into a dedicated follow-up refactor ticket.**
  - Rejected: performing an ~460+ line file split inline during the release gate.
- **Behavior-preserving pure refactor (technical):**
  - Extract cohesive method/helper groups into sibling files within `package wsagent` without altering conversation replay, turn settlement, registration, ownership, store layout, or protocol behavior.
- **Compatibility tier: T0 transparent.**
  - Pure internal file organization within `internal/wsagent`; no protocol, store, MCP tool, HTTP route, or config/rsrc schema changes.

## Constraints

- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- **Behavior-preserving pure refactor:** no protocol, store, or API changes; preserve exact runtime, locking, and file-touch behavior.
- **Sequencing:** `261125-feat-ws-exec-completion-chat-row` also touches `wsagent/runner_conv.go`; re-verify line ranges and remaining size before promoting to `ready/`.

## Phases

### Phase 1: Split wsagent/runner_conv.go along cohesive seams

- Extract AGY native replay staging/settlement (`stageAgyNativeReplay`, `finishAgyNativeReplay`, `resolveAgyNativeReplay`, `settleAgyNative`, ~250 lines at `runner_conv.go:1541-1790`) into a cohesive sibling file in `agents-plugin-tool/internal/wsagent/`.
- Extract registration & foreign-row ownership (`release`, `dropHeld`, and ownership helpers, ~210 lines at `runner_conv.go:1218-1430`) into a cohesive sibling file in `agents-plugin-tool/internal/wsagent/`.
- Re-check remaining file size and cohesive clusters at `ready/` promotion so every resulting file stays well under the size advisory without changing external or internal contracts.
- Verification: `go build ./...`, `go vet ./internal/wsagent/...`, and `go test -race ./internal/wsagent/...` pass with no behavioral changes or test-contract relaxations.
