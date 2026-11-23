---
title: "`!` 명령 히스토리 팝업과 퍼지 검색"
related:
  261123-feat-ws-dashboard-composer-bang-terminal: Shares the composer's bang mode and command execution lifecycle.
---

# Composer bang command history popup and fuzzy search

## Background

The owner requested on 2026-11-23 at 13:25–13:26 KST, paraphrased from Korean:

> When I run commands with `!`, show my previous commands in a popup. Fuzzy-match recent commands as I type, like Ctrl+R. Keep the recent list in browser-local storage and let me delete entries with an ×. Use Up/Down to navigate and Tab to complete the selected command into the input.

This feature adds recall to the existing composer bang mode. The related terminal ticket covers the run output.

## Decisions

- **(1461) Approved mockup.** The empty `!` prompt shows history newest-first with command text, exit-code chip when known, relative time and a per-row ×. A fuzzy query ranks matches by score, breaks ties by recency, highlights matched characters in amber, shows `N / M 일치`, and preselects the top match. Ctrl+R advances to the next match. The third state shows a no-match query and an armed clear-all confirmation. [Approved mockup](../../assets/mockups/261123/composer-bang-history/history.html)
- **Keyboard navigation (owner, 13:26).** Up/Down navigate rows. Tab fills the input with the selected command.
- **(1462)=b.** Enter on a selected row fills the input and closes the popup. A second Enter runs the filled command. Rejected alternative was immediate execution on the first Enter.
- **(1463)=a.** Scope history by worktree. Rejected alternative was one browser-wide list.
- **(1464)=a.** Keep up to 200 entries per worktree. Repeating a command moves its single entry to the top. Drop the oldest entries over the cap. Cap each stored entry at 2 KB. Rejected capacities were 50 and 500 entries.
- **(1465)=a.** Include `전체 지우기` with a two-step confirmation. The armed state expires after three seconds as shown in the mockup. Rejected alternative was per-row × only.
- **(1466)=a.** Open the popup automatically when `!` is typed and history exists. The first Esc closes the popup and the second Esc uses the existing bang-mode exit behavior. Rejected alternative was opening only on Up or Ctrl+R.
- **(1467)=a.** Show relative time and an exit-code chip. Record the exit code when the run ends and omit the chip when the code is unknown. Rejected alternative was command text only.
- **(1468), technically acknowledged.** Hide the popup when the worktree has no history. Enter on a query with no matches runs the typed command as-is.
- **Storage and compatibility.** Use browser `localStorage` as requested by the owner. The new key and its data are client-only and the Go routes and payloads stay as they are. Compatibility is T0 because previous binaries never read or overwrite the new key.

## Constraints

| paths | manual |
|---|---|
| `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/` | `ai-docs/manuals/shipped-surface-boundary.md` |
| `agents-plugin-tool/internal/` | `ai-docs/manuals/release-compatibility.md` |
| `agents-plugin-tool/` | `ai-docs/manuals/performance.md` |

- Assign frontend implementation to a native Claude worker per the owner's frontend rule.
- Follow Frontend Minimal Updates. Patch history rows by stable key and retain unaffected DOM nodes across keystrokes. Filter the in-memory list and persist history only on a mutation. The bounded per-worktree list is small mutable dashboard state (type 2 by shape), with browser `localStorage` as the owner-selected store.
- Keep local verification to `node --test` with explicit paths to touched test files. Set `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH` for browser tests. Local verification excludes `go test`, other Go tools, ws executables and Electron. Run any necessary Go work on `w5090`.

## Phases

### Phase 1: History popup, fuzzy search and local storage (frontend: Claude)

Integrate history with the composer bang flow in `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-exec.js` and its editor key handling. Style the popup in `dashboard-conversation.css` and add translated labels and hints under `agents-plugin-tool/internal/wsi18n/tables/`. Use the current worktree identity for the storage key and the existing command completion data for exit codes. Associate completions with the originating worktree and run so a late completion cannot restore a deleted row or overwrite metadata from a newer run of the same command. Keep over-limit commands runnable and skip persistence if an entry exceeds 2 KB when serialized in UTF-8, so recalling a command always restores its full text.

Verify with targeted tests and visual evidence:

- Test persistent reload, per-worktree key isolation, repeat deduplication and promotion, oldest eviction at 200 entries, and the 2 KB limit with multibyte text. Cover per-row removal, clear-all confirmation and timeout, late completion following removal or a repeat run, and known versus unknown exit codes.
- Test fuzzy ranking, recency tie-breaking, matched character highlights, result counts and top-match preselection. Verify that search preserves the typed input and patches keyed rows without rebuilding the whole list.
- Test automatic opening with history, a hidden popup with empty history, Up/Down and Ctrl+R navigation, Tab completion, first Enter filling and second Enter running exactly once, no-match Enter running the typed query, and the two Esc steps.
- Produce a dashboard LOOK capture and compare it side by side with the approved mockup for all three states. Record the capture location and any visual differences in the phase result, including row metadata, amber highlights and match count, the Enter hints, and the armed clear-all control.
