---
title: "읽기 전용 exec의 외부 프로그램 실행 옵션 차단"
related:
  261123-bug-ws-exec-cutover-worker-authority: "Read-only exec policy and repository configuration trust boundary"
sage-review-design: required
sage-review-completeness: required
completed: 2026-11-23
---

# Deny external-executable options in the read-only exec policy

## Background

The v0.51.2 release re-sweep found that ripgrep's `--hostname-bin` option
can run a caller-selected program. ripgrep 14.1.1 resolves and starts the
helper in `crates/core/flags/hiargs.rs`. Both hosted launch forms share
`checkReadOnlyArgv`, so the missing check applies to both. The base already
refuses `--pre`, `--pre-glob`, `-z`, and `--search-zip`.

## Decisions

- T0. Tighten internal launch admission. MCP arguments, replies, and storage
  formats stay compatible with older callers.
- Audit explicit command options. Project-owned Git configuration and
  attributes remain inside the trust boundary established in
  `261123-bug-ws-exec-cutover-worker-authority`.

## Constraints

- Follow `ai-docs/manuals/shipped-surface-boundary.md`,
  `ai-docs/manuals/release-compatibility.md`, and
  `ai-docs/manuals/performance.md` for the native tooling change. Admission
  checks use the parsed arguments and add zero file operations or processes.
- Scope the implementation to `internal/execjob`. Use w5090 for Go checks
  and reason about unsafe options from upstream source and documentation.
- Keep working-directory admission under a separate owner decision.

## Phases

### Phase 1: Deny external-executable options in the read-only allowlist

Refuse helper and write options in the shared argument checker. Verify
option spellings and ordinary reads with table-driven policy checks, and run
gofmt, go vet, and both Go package suites on w5090.

### Result (fa8c08590) - 2026-11-23

Both hosted launch forms use the stricter argument check. Each tested flag
is evaluated in memory with no command launch.

- `rg` refuses `--hostname-bin VALUE` and `--hostname-bin=VALUE`. The
  tests pin the existing separated and equals forms of `--pre` and
  `--pre-glob`, and `-z`, short clusters, and `--search-zip`.
- `file` refuses `-z` and `-Z`, including short clusters, and
  `--uncompress` and `--uncompress-noreport`, including long
  abbreviations. Blocking decompression directly also covers the external
  fallback where sandboxing is disabled or unavailable.
- `diff` refuses `-l`, its short clusters, and `--paginate` and its
  long abbreviations.
- `tree` refuses `-R`, its short clusters, and clusters after an attached
  depth such as `-L1R`.
- Git refuses `--show-signature` and its abbreviations. It rejects `%G`
  placeholders with any spacing modifier in `--pretty`, `--format`, and
  `shortlog --group`. For `for-each-ref` it rejects signature atoms in
  `--format` and `--sort`, including dereference and sort modifiers.
  Value checks cover both equals and separate values. It also refuses
  `--submodule=diff` and long option abbreviations with that value.

| Command | Verdict |
|---|---|
| `git` | Global config overrides and alias subcommands are refused. Existing checks cover output, external diff, pager, text conversion, filters, index refresh, and remerge writes. Added signature verifier and inline submodule diff checks. Project-owned configuration and attributes are trusted. |
| `ls` | Listing and formatting only. |
| `cat` | Input copying and text formatting only. |
| `head` | Input selection only. |
| `tail` | Input selection and following only. |
| `wc` | Counting only. |
| `grep` | Pattern matching and input selection only. |
| `rg` | Added both `--hostname-bin` forms. Existing checks cover preprocessing and external decompression. `RIPGREP_CONFIG_PATH` is removed and caller environment overrides are refused. |
| `cut` | Field and character selection only. |
| `tr` | Stdin transformation only. |
| `nl` | Input numbering and formatting only. |
| `diff` | Added refusal for external pagination via `-l` and `--paginate`. |
| `cmp` | Input comparison only. |
| `comm` | Input comparison and column selection only. |
| `stat` | Metadata reads and formatting only. |
| `file` | Added refusal for decompression via `-z`, `-Z`, `--uncompress`, and `--uncompress-noreport`. Compilation and access-time restoration were already refused. |
| `pwd` | Working-directory reporting only. |
| `echo` | Formatting and stdout only. |
| `basename` | Path string transformation only. |
| `dirname` | Path string transformation only. |
| `realpath` | Path resolution and formatting only. |
| `du` | Filesystem traversal, reads, and accounting only. |
| `tree` | Added refusal for `-R`, which writes per-directory HTML. Explicit output was already refused. |
| `which` | Executable lookup only. |
| `jq` | Filters and modules read or transform data and emit results. |
| `find` | Existing checks refuse `-exec`, `-execdir`, `-ok`, `-okdir`, `-delete`, `-fprint*`, and `-fls`. |
| `sort` | Existing checks refuse output, compressor, temporary-directory options, and native Windows slash options. Standard sorting may use scratch files in the inherited temporary directory. |
| `uniq` | Existing stdin-only argument check prevents an output operand. |
| `sed` | Existing check allows only line-address printing scripts and read operands. |

Upstream references for changed behavior include [ripgrep flags](https://raw.githubusercontent.com/BurntSushi/ripgrep/master/crates/core/flags/defs.rs),
[GNU diff options](https://www.gnu.org/software/diffutils/manual/html_node/diff-Options.html),
[file options](https://github.com/file/file/blob/master/doc/file.man),
[tree options](https://gitlab.com/OldManProgrammer/unix-tree/-/raw/master/doc/tree.1),
[Git pretty formats](https://git-scm.com/docs/pretty-formats),
[Git ref formatting](https://raw.githubusercontent.com/git/git/master/ref-filter.c),
and [Git submodule diff](https://raw.githubusercontent.com/git/git/master/submodule.c).

**Forwarded finding.** An existing in-tree symlink or junction can resolve a
working directory into an unrelated repository outside the project.
`execjob.go:660` checks the spelling, `hosted_ops.go:443` resolves the
target, and `hosted_launch.go:200` starts the child in that target. The
existing Windows fixture at `resolve_path_windows_test.go:264` expects
that admission for ordinary launches. The owner approved a separate repair
for read-only admission under decision (1474)=a.

w5090 generated the embedded package assets and passed gofmt and vet.
The final argument-policy suite finished with 171 passes and zero failures
including subtests. In the earlier two-package run, execjob had 171 passes
and zero failures, and ws-mcp had 294 passes, zero failures, and two skips.
None of the three known process-lifecycle failures or the known audit-order
flake appeared. The follow-up combined run also verifies both packages.
