---
title: "1000행 초과·다중 책임 혼재 소스 파일 전수 조사 및 책임별 분할"
related:
  261126-bug-win-worktree-remove-cwd-lock-and-async-trash: triggered runner_conv.go size_hard_limit advisory
---

# Audit and Split Oversized Multi-Responsibility Source Files (>1000 Lines)

## Background

Recent merges into `develop` surfaced repeated `ws-size-guard` advisories on files that grew past 1000–2000 lines as distinct responsibilities accumulated in a single module:

- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-delivery.js` (1019 lines, soft limit on merge `93fca1dc6`)
- `agents-plugin-tool/internal/wsdashboard/assets/dashboard-board-anchor.js` (2214 lines, hard limit on merge `7dcd594e7`)
- `agents-plugin-tool/internal/wsagent/runner_conv.go` (2367 lines, hard limit on merge `efa163ab6`)

Rather than silencing `.gitattributes` limits ad hoc or mixing structural refactors into unrelated feature/bug branches, this ticket scaffolds a dedicated cleanup pass to audit the entire source tree for source files exceeding 1000 lines with mixed responsibilities and split them along clean module boundaries.

> Scaffolding status: created in `todo/` as an initial placeholder. The full-tree line-count/responsibility inventory and concrete split plan will be populated before promotion to `ready/`.

## Constraints

- **Behavior-preserving refactor (T0):** No intended changes to external behavior, MCP/HTTP wire contracts, storage formats, DOM/CSS public hooks, or asset load ordering.
- **Responsibility-first splitting:** Split only files where genuinely distinct responsibilities are tangled together. Do not mechanically slice cohesive single-responsibility files, generated tables, or locale dictionaries merely to satisfy a line count; document justified exclusions during the audit.
- **Asset bundle & package parity:** Any frontend JS/CSS split must update asset registration/load order (e.g., `internal/wsdashboard/pages.go` and corresponding Node/browser tests) without changing runtime behavior.
- **Implementation conventions:** Before promoting to `ready/`, populate matching `AGENTS.md` `### Implementation Conventions` manuals for every directory covered by the finalized split list.

## Phases

### Phase 1: Full-tree >1000-line audit and responsibility-based file splits

1. **Audit (pre-ready fact population):** Scan non-generated source files across `agents-plugin-tool/`, `agents-plugin/`, and `agents-plugin-wsflow/` exceeding 1000 lines, inspect each candidate's responsibilities, and separate actionable split targets from justified cohesive exclusions. Include the three already-flagged candidates (`dashboard-composer-delivery.js`, `dashboard-board-anchor.js`, `runner_conv.go`) plus any other oversized multi-responsibility files discovered.
2. **Split by responsibility:** Decompose each confirmed target into focused sibling files (same package / script bundle) with clean naming, minimal diff noise, and preserved invariants.
3. **Verification:** Run full affected Go tests/vet (`-race`, remote `w5090`), Windows cross-build, and local Node frontend test suites to confirm zero behavioral regressions and clean `ws-size-guard` status on all split files.
