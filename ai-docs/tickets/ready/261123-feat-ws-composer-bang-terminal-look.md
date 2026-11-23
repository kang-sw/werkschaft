---
title: "대화창 ! 명령의 터미널형 표시: 셸 입력창, 콘솔 결과, 슬롯 키캡"
related:
  261121-feat-ws-board-command-slots: shipped composer `!`, command card, slot strip and exec HUD that this restyles
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 72e66d63c8e25ff0
sage-review-completeness-reviewed: 72e66d63c8e25ff0
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
- **One ticket over two (owner-acknowledged 2026-11-23, was (1403)).** The
  server fields and the client elements that switch on for them share one
  contract, so the element list lives in one place. Rejected: two tickets (the
  element list would be split across two contracts).
- **Esc in `!` mode (owner-acknowledged 2026-11-23, was (1404)).** The first
  Esc removes the leading `!` (leaves shell mode); a second Esc keeps today's
  behaviour (prepare Tab-exit). Rejected: making Esc always leave the composer
  - Esc is already bound to the Tab-exit preparation, so overloading it once
  keeps that binding reachable.
- **Work split (owner rule 2026-11-23).** Tickets are split frontend/backend and
  as much work as possible goes to the backend part, which Codex implements;
  Claude implements only frontend code. Phase 1 stays pure client rendering so
  it can ship first (T0); everything that is not rendering (new fields, display
  data, formatting data) lives in the backend phase, and the later frontend
  phase only renders those fields.

- **Times (technical, from review 2026-11-23).** The exec card JSON already
  carries `started_at` and `ended_at` (ms, `composerCard` in
  `agents-plugin-tool/internal/wsdashboard/composer_exec.go`); the dashboard JS
  does not read them yet. Phase 1 draws no time on the status line (a client
  receive-time stamp would show a wrong duration, and display data that is not
  rendering belongs to the backend phase under the work-split rule). Phase 2
  adds only what is missing (a server-derived duration and a running-elapsed
  base); Phase 3 renders it. Rejected: a client receive-time stamp in Phase 1;
  client-side `ended_at - started_at` in Phase 1 (duration derivation is
  display data owned by the backend phase).

## Constraints

Implementation Conventions rows from `AGENTS.md` matching the touched paths
(dashboard assets under `agents-plugin-tool/internal/wsdashboard/`; Phase 2
also touches Go under `agents-plugin-tool/internal/`):

| paths | manual |
|---|---|
| `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/` | `ai-docs/manuals/shipped-surface-boundary.md` |
| `agents-plugin-tool/internal/` | `ai-docs/manuals/release-compatibility.md` |
| `agents-plugin-tool/` | `ai-docs/manuals/performance.md` |

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
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
- Go tests (Phase 2, backend) run only on w5090. No local `go test`, no ws executables,
  no Electron.
- Run only the Node test files this change touches.
- Mockup states (legend of `overview.html`): ① idle, ② `!` typed, ③ short
  output done, ④ failure (stderr, exit != 0), ⑤ long output to file, ⑥ async
  run in progress, ⑦ command slot. Phase 1 covers ①, ②, ③, ④ (without stderr
  separation), ⑤, ⑥ without live tail, and ⑦; Phase 3 adds the field-dependent
  parts of ③-⑥ and the ② title.
- Phase 1 test targets (touched Node/browser fixtures, for example
  `testdata/composer-exec.browser.cjs` and `testdata/composer-usercmd.browser.cjs`):
  first Esc removes the leading `!` and a second Esc arms Tab-exit; the removal
  keeps caret, mention paint (`.chl`) and draft state consistent, as the
  Backspace path does, and does not disturb the send/retry text comparison;
  `@` and `/` popups stay closed in `!` mode; console collapse and clear; the
  failure, running and amber status colors.

## Prior Decisions

- 261121-feat-ws-board-command-slots (2026-11-22, Result): "Performance, measured on macOS (the probes under `WS_COMPOSER_PERF=1`, `composer_exec_perf_test.go` and `TestComposerExecConversionProbe`; p50 / p95)" — bearing: constrains
- 11caef15 (2026-11-22, commit): "A LAN viewer typing `!` fails closed (route refused, draft kept, hint to start with a space) rather than sending a command line as a prompt." — bearing: constrains
- 11caef15 (2026-11-22, commit): "Only a session rendered as a hosted non-child composer sets data-cexec; the server still decides (a non-lead is refused after acceptance with a refused card)." — bearing: constrains
- ce20146a (2026-11-22, commit): "Retry semantics: only a resend of the same text after an unresolved attempt sets retry; 4xx settles the attempt, a network failure or 5xx leaves it unresolved." — bearing: constrains
- 0e918e11 (2026-11-23, commit): "JS: ucmdCompare orders a revision against the shown one; ucmdApply(list, post) differs for a read and a write answer; ucmdRefresh funnels a wake or a dropped answer into one read" — bearing: constrains
- 7d13b55d (2026-05-21, commit): "Captured transcript rendering direction: exec items use terminal-output style, agent items render action-unit blocks" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wsdashboard/assets/dashboard-composer-exec.js, dashboard-composer-ucmd.js, dashboard-composer-keyboard.js, dashboard-conversation.css, dashboard-exec-hud.css; Phase 2 Go under agents-plugin-tool/internal/ |
| scope.surface | cross-module | Phase 2 adds card/list fields consumed by dashboard JS; Phase 1 is client-only |
| scope.new_public_symbol | unknown | Phase 2 chooses the field names and records them in its Result; none are fixed yet |
| scope.new_type_contract | yes | Phase 2 additive card/list fields (times, stream segments, tail, cwd/shell) |
| scope.test_surface | existing | testdata/composer-exec.browser.cjs, testdata/composer-usercmd.browser.cjs, composer_exec_test.go, composer_usercmd_test.go |
| complexity.reuse_points | confirmed | mention layer .chl (dashboard-conversation.css#L482), .d-cexec, .d-ucmd, dashboard-composer-keyboard.js Escape/tabOut handler |
| complexity.side_effect_risk | moderate | Esc handler shares the Tab-out arming; the mention and slash popups are disabled in bang mode |
| risk.correctness | moderate | first-Esc-removes-bang versus Tab-out arming, plus send/draft/retry logic that depends on the leading bang |
| risk.fit | moderate | the mockup must be matched across a light theme with dark terminal surfaces |
| risk.test | moderate | LOOK check needs the running UI and cannot run locally; Go tests run only on w5090 |
| risk.security_or_contract | moderate | Phase 2 adds wire fields (additive hypothesis, unconfirmed); approval and TrustedWrite routing untouched |

## Phases

### Phase 1: Client-only terminal look (frontend: Claude)

Uses only data the cards already carry: exit code, byte count, job ID, state,
`file`/`file_url`/`truncated`, and the registered slot fields.

- Composer in `!` mode: dark input, mono font, amber `!`, `bash` badge with
  Esc/Backspace hint, green Run button. Leaving `!` mode (removing the leading
  `!`) restores the normal look.
- Disable the `@` mention and `/` command popups while in `!` mode.
- Esc rule: the first Esc in `!` mode removes the leading `!` (leaves shell
  mode); a second Esc keeps today's behaviour (prepares Tab-exit). See Decisions.
- Results: one console scrollback with prompt line, output block, and status
  line `✓ 0 · bytes` using existing data only (exit code, bytes, job ID; byte
  formatting stays in the client, as today); no
  time is drawn until Phase 3 (see the Times decision). Failure colors the
  status line and `$` red; running is blue; signal/stop/lost stay amber chips.
  The file link renders as an output line. "Idle" means no running card: the
  console collapses to one line then. The per-card close (`cexec-x`,
  `cexecDismiss`) stays on each run; clear is a console-level action that
  closes every card through that same existing close.
- Slots: keycap button, command line as main text on a dark `$` block, full
  stored line shown without truncation.
- Elements needing a not-yet-existing field are not drawn in this phase.
- Light theme: terminal surfaces stay dark.

Deferred: everything in Phases 2 and 3. Verification boundary: LOOK check plus
touched Node tests. This phase has no backend dependency and ships first (T0).

### Result (e2e3d30) - 2026-11-23

Landed (7d4394053, e2e3d3094): `!` shell mode (`data-bang`, amber `!` via the CSS
Highlight API `ws-bang`, `bash` badge + Esc/Backspace hint, green Run), `@`/`/`
popups off in `!` mode, Esc rule (first removes `!`, second arms Tab-out), one
dark scrollback console (prompt line, output, `✓ exit 0 · bytes · job` status,
folds to one line when idle/restored, console-level clear through `cexec-x`),
dark `$` slot blocks with keycap button and full stored line, terminal surfaces
dark in light theme, strings in all 11 locales (lite review caught missing
zh-Hans/zh-Hant; fixed).

Verification: `node --test testdata/composer-bang.test.cjs` 5/5 pass. LOOK
compared en/ko x dark/light captures vs the mockup (match; minor: no glyph in
bash badge, slots are rows, extra Collapse toggle, no cwd/time/red stderr/caret =
Phase 3). Captures: `D:/Temp/composer-bang-look/`. Go and the Go-run browser
fixtures were not run (hard rule); `composer-exec.browser.cjs` reload waits were
changed to `state: 'attached'` because restored history is folded.

Decisions: the idle fold applies to restored history and user toggle; a fresh or
active run keeps the console open. Restore never folds over a run added before
the restore answer arrived (review finding).

### Phase 2: Additive server fields (backend: Codex)

Self-contained; independent of Phase 1 and may land before or after it. Owns all
non-rendering work: the server adds additive card fields derived server-side, so
the client only renders them. The field set is exactly the list below, tied to
the Phase 3 elements; nothing else is added.

- duration and a running-elapsed base (`started_at`/`ended_at` already exist on
  the card, so only the derived values are new)
- separate stdout/stderr (stream-tagged segments)
- a short output tail (preview lines for long output, live tail while running)
- cwd and shell name (e.g. `bash ~/work/demo`; Git Bash on Windows)

This phase picks the exact field names, types and absent-field semantics and
records them in its Result; Phase 1 does not depend on them and Phase 3 reads
that Result. Done when each field above is emitted, and omitted (not zero or
empty) when it does not apply, with Go tests for both paths.

Constraints: fields are additive only; the first-byte `!` grammar, approval and
TrustedWrite routing are unchanged. Applies the Constraints rows above
(release-compatibility, performance, shipped-surface-boundary).

Compatibility: expected additive (old client ignores the new fields; an old
daemon simply omits them). To be confirmed by this phase's own evidence under
`ai-docs/manuals/release-compatibility.md` (new -> old -> new cycle) before it
is called additive/T0.

Verification: Go tests for the new fields and the absent-field path, run only on
w5090 (no local `go test`, no ws executables).

### Phase 3: Render the new fields (frontend: Claude)

Blocked on Phase 1's and Phase 2's landed Results. Pure client rendering in the
element positions Phase 1 already laid out; each element switches on when its
field exists and stays hidden otherwise (an old daemon leaves them hidden):

- duration/elapsed in the status line, from the Phase 2 derived fields (the
  Phase 1 status line has no time)
- red stderr lines with a left rule from the stream-tagged segments
- preview lines for long output and live tail while running
- console title with shell name and cwd

Sequencing note: `261115-feat-ws-whiteboard-agent-card` Phase 2 embeds the same
conversation/composer shell, so whichever lands second checks that `!`-mode
styling and the exec console still work inside the embedded card.

No server code and no derivation logic beyond display. Verification: LOOK check
against the mockup states ③–⑥ and ② (title) plus touched Node tests.
