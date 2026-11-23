---
title: "대화창 ! 명령의 터미널형 표시: 셸 입력창, 콘솔 결과, 슬롯 키캡"
related:
  261121-feat-ws-board-command-slots: shipped composer `!`, command card, slot strip and exec HUD that this restyles
---

# Composer `!` terminal look: shell input, console results, slot keycaps

## Background

`261121-feat-ws-board-command-slots` shipped the composer `!` exec: a message
whose first character is `!` runs as a bash command, the result shows in the
composer's card strip (`.d-cexec`), registered commands show in the slot strip
(`.d-ucmd`), and running jobs show as an exec HUD chip. It works, but nothing
looks like a terminal, so the user cannot tell at a glance that `!` switched
the composer into shell mode.

This ticket changes only what is shown. The server's first-byte `!` grammar,
the leading-space-means-prompt rule, and the rule that a slot shows its stored
line in full all stay as shipped.

Visual reference (tracked, three directions A/B/C, an element matrix tagged
client-only / needs-new-server-field / touches-existing-logic, and a combined
preview):
`ai-docs/assets/mockups/261123/composer-bang-terminal/overview.html`.

## Decisions

- **Owner decision (whiteboard (1400)=a, 2026-11-23): build the recommended
  combination**, client-only stage first:
  - **A's composer.** On a leading `!` the input becomes a dark terminal input:
    mono font, amber `!` (painted through the existing mention layer `.chl`,
    the `!` stays in the text), a `bash` mode badge with an Esc/Backspace hint,
    and a green Run button in place of Send.
  - **B's console-window results.** The result strip becomes one
    terminal-styled scrollback of runs. Each run is prompt line, output, then a
    status line `✓ 0 · bytes · time`. The console collapses to one line when
    idle.
  - **C's slot keycaps.** Slots keep their structure (label button,
    description, `x`, the full stored line) but the command line is the main
    text, on a terminal block with a `$` prompt and a green keycap.
  - Terminal surfaces stay dark in the light theme too.
- **Rejected:**
  - C's prompt-capsule input. Removing `!` from the text would reopen the
    four-round-reviewed send/draft/retry logic for a purely visual gain.
  - A alone (leaves results and slots un-terminal).
  - B-centred (moves the input into the console window; larger layout change).
- **Compatibility: T0 for Phase 1** (client-only: no wire, schema, or stored
  state changes). Phase 2 is expected to be additive (an old client ignores
  new fields); that is a hypothesis for Phase 2's own evidence to confirm, not
  a settled verdict.

## Constraints

Implementation Conventions rows from `AGENTS.md` matching the touched paths
(dashboard assets under `agents-plugin-tool/internal/wsdashboard/`; Phase 2
also touches Go under `agents-plugin-tool/internal/`):

| paths | manual |
|---|---|
| `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/` | `ai-docs/manuals/shipped-surface-boundary.md` |
| `agents-plugin-tool/internal/` | `ai-docs/manuals/release-compatibility.md` |
| `agents-plugin-tool/` | `ai-docs/manuals/performance.md` |

- Read each manual before editing a covered file.
- i18n: if any label or placeholder changes (e.g. the `bash` badge, hint text,
  Run, console clear/expand), update the i18n tables for every locale, not only
  `en` and `ko`.
- Server `!` grammar, one-use approval, TrustedWrite routing and the slots'
  full-line display contract are not touched.
- Performance: the `!`-mode paint reuses the existing mention layer; do not add
  per-keystroke layout work (a per-line PS2 `>` gutter in the textarea layer
  is costly and is omitted).

## Verification

- LOOK check: capture the running UI for each matrix state and compare
  side by side with the mockup. UI that must match a mockup goes to a native
  Claude worker given the mockup path; text-only inspection does not count.
- Go tests (Phase 2) run only on w5090. No local `go test`, no ws executables,
  no Electron.
- Run only the Node test files this change touches.

## Phases

### Phase 1: Client-only terminal look

Uses only data the cards already carry: exit code, byte count, job ID, state,
`file`/`file_url`/`truncated`, and the registered slot fields.

- Composer in `!` mode: dark input, mono font, amber `!`, `bash` badge with
  Esc/Backspace hint, green Run button. Leaving `!` mode (removing the leading
  `!`) restores the normal look.
- Disable the `@` mention and `/` command popups while in `!` mode.
- Esc rule: the first Esc in `!` mode removes the leading `!` (leaves shell
  mode); a second Esc keeps today's behaviour (prepares Tab-exit). See (1404).
- Results: one console scrollback with prompt line, output block, and status
  line `✓ 0 · bytes · time`-shaped using existing data only (exit code, bytes,
  job ID; the receive-time stamp is client-side). Failure colors the status
  line and `$` red; running is blue; signal/stop/lost stay amber chips. The
  file link renders as an output line. Collapses to one line when idle; clear
  closes all cards using the existing close.
- Slots: keycap button, command line as main text on a dark `$` block, full
  stored line shown without truncation.
- Elements needing a not-yet-existing field are not drawn in this phase.
- Light theme: terminal surfaces stay dark.

Deferred: everything in Phase 2. Verification boundary: LOOK check plus touched
Node tests.

### Phase 2: Additive server fields

Depends on Phase 1's landed Result. Add additive card/list fields; each element
switches on when its field exists and stays hidden otherwise:

- start and end times (elapsed time in the status line, running elapsed)
- separate stdout/stderr (red stderr lines with a left rule)
- a short output tail (preview lines for long output, live tail while running)
- cwd and shell name (console title, e.g. `bash ~/work/demo`)

Compatibility: expected additive (old client ignores the new fields; an old
daemon simply omits them and the client leaves the elements hidden). To be
confirmed by this phase's own evidence under
`ai-docs/manuals/release-compatibility.md` before it is called T0/additive.

## Open Decision Queue

- (1403) [open] [technical] One ticket with two phases over two tickets - Phase 2 only switches on elements Phase 1 already lays out, so one contract keeps the element list in one place.
- (1404) [open] [technical] In `!` mode the first Esc removes the leading `!` (leaves shell mode) and a second Esc keeps today's Esc behaviour (prepare Tab-exit) over making Esc always leave the composer - Esc is already bound to the Tab-exit preparation, so overloading it once keeps that binding reachable.
