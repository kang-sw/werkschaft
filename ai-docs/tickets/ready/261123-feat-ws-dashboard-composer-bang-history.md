---
title: "`!` 명령 히스토리 팝업과 퍼지 검색"
related:
  261123-feat-ws-composer-bang-terminal-look: Shares the composer's bang mode and command execution lifecycle.
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 1bfda5d7c2e43230
sage-review-completeness-reviewed: 1bfda5d7c2e43230
---

# Composer bang command history popup and fuzzy search

## Background

The owner requested on 2026-11-23 at 13:25–13:26 KST, paraphrased from Korean:

> When I run commands with `!`, show my previous commands in a popup. Fuzzy-match recent commands as I type, like Ctrl+R. Keep the recent list in browser-local storage and let me delete entries with an ×. Use Up/Down to navigate and Tab to complete the selected command into the input.

This feature adds recall to the existing composer bang mode. The related terminal ticket covers the run output.

## Decisions

- **(1461) Approved mockup.** The empty `!` prompt shows history newest-first with command text, exit-code chip when known, relative time and a per-row ×. A fuzzy query ranks matches by score, breaks ties by recency, highlights matched characters in amber, shows `N / M 일치`, and preselects the top match. Ctrl+R advances to the next match. The third state shows a no-match query and an armed clear-all confirmation. [Approved mockup](../../assets/mockups/261123/composer-bang-history/history.html)
- **Keyboard navigation (owner, 13:26).** Up/Down navigate rows. Tab fills the input with the selected command.
- **(1462)=b.** Enter on a selected row fills the input and closes the popup. In the default send mode, a second Enter runs the filled command. See (1476) for the alternate mode. Rejected alternative was immediate execution on the first Enter.
- **(1463)=a.** Scope history by worktree. Rejected alternative was one browser-wide list.
- **(1464)=a.** Keep up to 200 entries per worktree. Repeating a command moves its single entry to the top. Drop the oldest entries over the cap. Cap each stored entry at 2 KB. Rejected capacities were 50 and 500 entries.
- **(1465)=a.** Include `전체 지우기` with a two-step confirmation. The armed state expires after three seconds as shown in the mockup. Rejected alternative was per-row × only.
- **(1466)=a.** Open the popup automatically when `!` is typed and history exists. The first Esc closes the popup and the second Esc uses the existing bang-mode exit behavior. Rejected alternative was opening only on Up or Ctrl+R.
- **(1467)=a.** Show relative time and an exit-code chip. Record the exit code when the run ends and omit the chip when the code is unknown. Rejected alternative was command text only.
- **(1468), technically acknowledged.** Hide the popup when the worktree has no history. On a query with no matches, the configured send action runs the typed command as-is.
- **Storage and compatibility.** Use browser `localStorage` as requested by the owner. The new key and its data are client-only and the Go routes and payloads stay as they are. Compatibility is T0 because previous binaries never read or overwrite the new key.
- **(1476)=a.** Once the input is filled, or when the query has no matches, use the configured send action to execute. The default Enter mode follows the approved mockup. Render the hint for the active send chord. See (1486) for how Enter and the send chord handle an active selection.
- **(1477), acknowledged.** Record positively identified composer bang runs once per submission ID, including retries. A refusal or unknown attempt creates no history row. A retry that establishes a known run records it once. Use the distinctions in `cexecSend`, `composerRun` and commit `ce20146ad`.
- **(1478)=a.** Keep bounded exit-status tracking for a pending history row even after the user dismisses or clears its console card. Removing or clearing history stops tracking the affected rows.
- **(1479), acknowledged.** Bind history to the current worktree tag provided by the existing user-command list and revision wake (`rootTag`, `List` and `Revision` in `cmd/ws-mcp/usercmd.go`). The tag is a 32-bit hash with a nonzero collision risk. History stays unavailable until the binding is known, and users can still run commands. Rejected: the standalone `data-worktree` attribute is absent on composer pages, reply IDs would split one worktree's history by session, and a shared fallback would mix worktrees.
- **(1480)=a.** Apply history deletion and stale-completion guarantees across same-origin pages for the same worktree. A completion in one page cannot restore a row another page removed or cleared, or overwrite a newer run of the same command.
- **(1486)=a.** With a history row selected, plain Enter and the configured Ctrl+Enter or Cmd+Enter send chord both fill the selected command and close the popup. A subsequent send executes the filled command. This gives both keys the same selection behavior and preserves the separate execution action.

## Constraints

| paths | manual |
|---|---|
| `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/` | `ai-docs/manuals/shipped-surface-boundary.md` |
| `agents-plugin-tool/internal/` | `ai-docs/manuals/release-compatibility.md` |
| `agents-plugin-tool/` | `ai-docs/manuals/performance.md` |

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Assign frontend implementation to a native Claude worker per the owner's frontend rule.
- Follow Frontend Minimal Updates. Patch history rows by stable key and retain unaffected DOM nodes across keystrokes. Filter the in-memory list and persist history only on a mutation. The bounded per-worktree list is small mutable dashboard state (type 2 by shape), with browser `localStorage` as the owner-selected store.
- Keep local verification to `node --test` with explicit paths to touched test files. Set `WS_PLAYWRIGHT_MODULE` and `PLAYWRIGHT_BROWSERS_PATH` for browser tests. Local verification excludes `go test`, other Go tools, ws executables and Electron. Run any necessary Go work on `w5090`.

## Prior Decisions

- e49a9b386 (2026-11-23, commit): "Update the mockup to show Enter filling the selected command before a second Enter runs it." — bearing: supports
- 7d4394053 (2026-11-23, commit): "Shell mode = data-bang on #composer, set by bangSync (input handler and paintMentions) when ctext.value starts with `!`, the same test cexecSend uses. Send/draft/retry text logic is untouched." — bearing: constrains
- 11caef152 (2026-11-22, commit): "Only a session rendered as a hosted non-child composer sets data-cexec; the server still decides (a non-lead is refused after acceptance with a refused card)." — bearing: constrains
- ce20146ad (2026-11-22, commit): "Retry semantics: only a resend of the same text after an unresolved attempt sets retry; 4xx settles the attempt, a network failure or 5xx leaves it unresolved." — bearing: constrains
- 625708d53 (2026-11-12, commit): "mention.js was generalized (token/render/choose/exact/max options and group headings) instead of writing a second popup. A second popup would duplicate the listbox keyboard and aria handling." — bearing: supports
- 261109-feat-ws-dashboard-composer-file-mention (2026-11-09, Decisions): "Enter also picks (never sends); Esc closes only the popup (it arms Tab-out only when no popup is open)" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-exec.js`, `dashboard-composer-ucmd.js`, `dashboard-exec-hud.js`, `dashboard-composer-keyboard.js`, `dashboard-conversation.css`, and `agents-plugin-tool/internal/wsi18n/tables/` |
| scope.surface | internal | Dashboard client behavior and browser storage. The existing composer-exec routes and payloads stay as they are. |
| scope.new_public_symbol | no | None planned. |
| scope.new_type_contract | yes | The per-worktree `localStorage` entry format persists command text, recency and an optional exit code across reloads. |
| scope.test_surface | existing | `agents-plugin-tool/internal/wsdashboard/testdata/composer-bang.test.cjs` exercises bang mode, keyboard and console behavior in a static Chromium fixture. |
| complexity.reuse_points | confirmed | `dashboard-composer-ucmd.js` applies the user-command revision carrying the current worktree tag. `dashboard-composer-keyboard.js` owns send, popup and Escape keys. `cexecSend` and `cexecPoll` provide the submission ID and the card's `exec_id` and optional `exit_code`. The private exec HUD already reads jobs with IDs and launch directories from `execJobsList` (`internal/wsdashboard/exec_hud.go`). |
| complexity.side_effect_risk | moderate | Popup key handling shares the compose and send path, and the same worktree's persisted list can be open in multiple pages. |
| risk.correctness | high | A key-routing error can run a recalled command early, and a stale completion can revive a deleted row or replace a newer run's metadata. |
| risk.fit | moderate | The three mockup states have distinct selection, completion and clear-confirmation behavior to fit the existing composer. |
| risk.test | high | The current static fixture stubs requests. Reload, asynchronous completion ordering, keyboard dispatch and all three visual states need explicit coverage. |
| risk.security_or_contract | moderate | Browser storage persists raw shell text by worktree. The popup must retain the existing execution gates and require the confirmed send action. |

## Phases

### Phase 1: History popup, fuzzy search and local storage (frontend: Claude)

Integrate history with the composer bang flow in `agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-exec.js` and its editor key handling. Style the popup in `dashboard-conversation.css` and add translated labels and hints under `agents-plugin-tool/internal/wsi18n/tables/`. Scope storage by the existing worktree tag and carry the originating worktree and submission ID through admission and status handling. Use the existing command completion data for exit codes. Keep monitoring pending history rows independently of console visibility, with bounded tracking and cancellation when a row is removed. Apply mutations to the shared worktree history so a page's stale view cannot restore a deleted row, undo a clear, or overwrite metadata from a newer run of the same command. Keep over-limit commands runnable and skip persistence if an entry exceeds 2 KB when serialized in UTF-8, so recalling a command always restores its full text.

The user-command revision identifies the current worktree for opening the popup. To persist a run, match the composer card's `exec_id` to its job in the existing private `/s/<rid>/exec/jobs` view. A typed bang uses its cleaned launch root as the job working directory (`composerExec.Launch`, `resolveWorkingDir`); derive the same worktree tag from that directory as `rootTag` does from the root. Reuse the HUD list and its refresh path. Carry the verified origin for subsequent status updates, including when the active worktree changes. If the job is missing, pruned, or has no usable working directory, leave the run unrecorded until the origin can be verified within bounded tracking. The command and console keep their existing behavior.

Verify with targeted tests and visual evidence:

- Test persistent reload, sharing between two pages for the same worktree, isolation between worktrees, missing and changed bindings, repeat deduplication and promotion, oldest eviction at 200 entries, and the 2 KB limit with multibyte text.
- Test a known run, HTTP and card refusals, an unknown result, and a retry resolving a previous attempt without creating a second row. Test known and unknown exit codes, status arriving after console dismissal or clear, and cancellation when history is removed or cleared.
- Test binding changes before server admission, while the run is pending, and before a retry. Confirm the run appears only under its verified launch worktree. Cover a missing or pruned HUD job and tag parity with the producer for Windows and POSIX worktree paths.
- Test per-row removal, clear-all confirmation and timeout, and late completion after removal, clear, and a newer repeat run, including when the mutation and completion occur on different pages.
- Test fuzzy ranking, recency tie-breaking, matched character highlights, result counts and top-match preselection. Verify that search preserves the typed input and patches keyed rows without rebuilding the whole list.
- Test automatic opening with history, a hidden popup with empty history, Up/Down and Ctrl+R navigation, Tab completion, and the two Esc steps. With a row selected, test plain Enter in both send modes and each configured Ctrl/Cmd+Enter chord. Each fills and closes the popup without sending. Verify that the next configured send executes exactly once, and that a no-match query executes as typed on the configured send action.
- Produce a dashboard LOOK capture and compare it side by side with the approved mockup for all three states. Record the capture location and any visual differences in the phase result, including row metadata, amber highlights and match count, the Enter hints, and the armed clear-all control.
