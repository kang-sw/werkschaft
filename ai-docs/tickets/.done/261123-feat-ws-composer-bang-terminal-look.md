---
title: "대화창 ! 명령의 터미널형 표시: 셸 입력창, 콘솔 결과, 슬롯 키캡"
related:
  261121-feat-ws-board-command-slots: shipped composer `!`, command card, slot strip and exec HUD that this restyles
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: 1394877f00c880c1
sage-review-completeness-reviewed: 1394877f00c880c1
completed: 2026-11-23
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
- **(1493)=a (owner confirmed 2026-11-23, supersedes Phases 2–3 and the in-flow
  console/hint layout from (1400)).** Remove the in-flow `.d-cexec` inline
  console (26px header bar + scrollback card) and the bottom 22px `bash` hint
  row from the composer so toggling `!` mode and running `!` commands cause
  **zero vertical composer height growth**. Keep only the `!` shell-mode input
  styling (mono input, amber `!` via the CSS Custom Highlight API `ws-bang`,
  green Run button; dark theme palette unchanged and light theme keeps Solarized
  Light `--tm-*` from `8f3e2ceae` per `(1499)=a`), `@`/`/` popup suppression,
  first-Esc removes `!` / second-Esc arms Tab-out, and `$` command slot blocks.
  Unify running status, elapsed time, cwd, and live output into the existing
  out-of-flow top-right Exec HUD (`.cbadges` / `.xgroup` `.xchip` pills +
  `xpeek` overlay). Cancel Phases 2 and 3. Compatibility remains **T0**
  (client-only UI cleanup; server routes, payloads, and stored state are
  unchanged). Rejected: `(b)` migrating stderr/shell fields to `xpeek`; `(c)`
  keeping the in-flow `.d-cexec` console and Phases 2–3. Note: the owner also
  withdrew the Codex/Claude worker split on 2026-11-23 (14:24 KST), so `agy`
  workers implement remaining work.
- **(1499)=a (owner confirmed 2026-11-23).** Keep the Solarized Light `--tm-*`
  palette from `8f3e2ceae` (`dashboard-conversation.css#L117-L120`,
  `composer-bang.test.cjs#L190-L197`: base3/base2 `#fdf6e3` bg, base01 text,
  Solarized accents) for light-theme terminal surfaces (`data-bang` input and
  `.d-ucmd` slots) in Phase 4, while keeping the dark theme palette unchanged.
  Aligns `(1493)=a` and Phase 4 wording with `8f3e2ceae`. Rejected: `(b)`
  reverting light-theme `--tm-*` terminal surfaces back to dark `#131417`.
- **(1500) [announced, confirmed 2026-11-23].** Remove the `xpeek` "Jump
  to card" control (`xhCardFor` — including its callsite in `xhPeekRender`'s
  signature array at `dashboard-exec-hud-peek.js#L159` — and `act === "jump"`
  in `dashboard-exec-hud-peek.js#L224-L228,L720-L724,L777-L786`),
  `.cexec.xflash` (`dashboard-exec-hud.css#L164`), `js.xhud.pk.jump` /
  `js.xhud.pk.jump.none` across all 11 locale tables
  (`agents-plugin-tool/internal/wsi18n/tables/`), and the card-jump assertion in
  `testdata/exec-hud.browser.cjs#L440-L451`. Direct consequence of `(1493)=a`:
  once `.d-cexec` DOM cards are removed, no card target exists to jump to and
  `xpeek` itself is the unified output view.
- **(1501) [technical, confirmed 2026-11-23].** Headless bounded
  submission tracking and `noteWarn` toast feedback over removing `cexecPoll`
  or modifying Go payloads. After removing `.d-cexec` DOM cards and
  `sessionStorage` card restoration (`cexecCards`/`cexecBox`/`cexecSave`), keep
  `cexecList` on page load solely to prime `cexecToken`, and keep a lightweight
  headless bounded poll (`GET /s/<rid>/composer-exec/<submission_id>`) per
  accepted `202` submission from both `cexecSend` and `ucmdRun` until the
  server populates `exec_id` or settles the submission:
  - When `exec_id` arrives: invoke an in-memory admission hook
    (`submission_id`, `command`, `exec_id`, `origin`) for
    `261123-feat-ws-dashboard-composer-bang-history` (pinned in `(1505)`),
    trigger `.xgroup` Exec HUD refresh (`execWake` / `xhFetch`) so the `.xchip`
    pill appears promptly, and stop polling `composer-exec/<id>` (`.xgroup` +
    `xpeek` own live status, output, and exit code thereafter).
  - When `card.unknown === true` (initial `200` or `404` poll, for either
    `cexecSend` or `ucmdRun`) or post-`202` `phase === "refused"`: display the
    message via the existing floating `noteWarn` toast (`WsToast`, 0 px
    composer height change; keep `js.cexec.unknown` and `js.cexec.refused` in
    all 11 locale tables; leave the draft intact on `cexecSend` `unknown`), and
    stop polling.
  - Rejected: removing `cexecPoll` entirely (drops post-`202` non-lead refusal
    feedback and breaks `submission_id -> exec_id` correlation needed by
    `261123-feat-ws-dashboard-composer-bang-history`, since `/s/<rid>/exec/jobs`
    does not carry `submission_id`); adding `submission_id` to Go `execJobView`
    (violates `(1493)=a`'s client-only T0 scope).
- **(1505) [technical, auto-confirmed from Sage design & completeness review 2026-11-23].**
  Dispatch the in-memory admission hook via
  `window.dispatchEvent(new CustomEvent("ws:cexec-admitted", { detail: { submission_id, command, exec_id, origin } }))`
  with exact `origin` values `"cexec"` (typed `!` via `cexecSend`) and `"ucmd"`
  (registered slot click via `ucmdRun`), cap headless `cexecPoll` with an
  explicit upper poll-count / duration limit (20 polls or 30 s) so repeated
  transport/5xx errors or stuck states cannot spin indefinitely, and pass
  immediate `POST 200` retry responses carrying `exec_id` or
  `phase === "refused"` directly through the same headless admission hook /
  `.xgroup` HUD wake / `noteWarn` handler as `cexecPoll`. Rejected: an
  unbounded `cexecPoll` loop; omitting `POST 200` retry payloads from the hook;
  leaving the hook transport and `origin` strings unspecified across tickets.

## Constraints

Implementation Conventions rows from `AGENTS.md` matching the touched paths
(dashboard assets under `agents-plugin-tool/internal/wsdashboard/` and all 11
locale tables under `agents-plugin-tool/internal/wsi18n/tables/`; Phase 2 was
dropped by `(1493)=a`):

| paths | manual |
|---|---|
| `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/` | `ai-docs/manuals/shipped-surface-boundary.md` |
| `agents-plugin-tool/internal/` | `ai-docs/manuals/release-compatibility.md` |
| `agents-plugin-tool/` | `ai-docs/manuals/performance.md` |

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin/, agents-plugin-wsflow/, agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Read each manual before editing a covered file.
- i18n: if any label or placeholder changes (e.g. the `bash` badge, hint text,
  Run, console clear/expand), update the i18n tables for every locale, not only
  `en` and `ko`.
- Server `!` grammar, one-use approval, TrustedWrite routing and the slots'
  full-line display contract are not touched.
- Performance: `!`-mode paint is driven from `paintMentions`
  (`dashboard-composer-pickers.js#L47`) and paints `!` via the CSS Custom
  Highlight API `CSS.highlights.set("ws-bang", ...)`
  (`dashboard-composer-exec.js#L484-496`), not `.chl`; do not add per-keystroke
  layout work (a per-line PS2 `>` gutter in the textarea layer is costly and is
  omitted).

## Verification

- LOOK check: capture the running UI for each active state and compare side by
  side with the mockup (`agy` workers implement remaining work per `(1493)=a`,
  which withdrew the Codex/Claude split); text-only inspection does not count.
- Go tests run only on w5090. No local `go test`, no ws executables, no
  Electron.
- Run only the Node test files this change touches.
- Mockup states (legend of `overview.html`): ① idle, ② `!` typed, ③ short
  output done, ④ failure (stderr, exit != 0), ⑤ long output to file, ⑥ async
  run in progress, ⑦ command slot. Phase 1 covered ①, ②, ③, ④ (without stderr
  separation), ⑤, ⑥ without live tail, and ⑦; Phase 2 and Phase 3 were
  dropped by `(1493)=a`.
- Phase 1 test targets (`testdata/composer-bang.test.cjs`,
  `testdata/composer-exec.browser.cjs`, `testdata/composer-usercmd.browser.cjs`):
  first Esc removes the leading `!` and a second Esc arms Tab-exit; the removal
  keeps caret, `!` highlight (`CSS.highlights` `"ws-bang"` in
  `dashboard-composer-exec.js#L484-496`) and draft state consistent, as the
  Backspace path does, and does not disturb the send/retry text comparison;
  `@` and `/` popups stay closed in `!` mode; console collapse and clear; the
  failure, running and amber status colors.

## Prior Decisions

- 261123-feat-ws-dashboard-composer-bang-history (2026-11-23, Decisions (1493)=a alignment): "they read admission results (`exec_id`) from `cexecSend` and monitor job origin and exit status through the existing `.xgroup` Exec HUD (`/s/<rid>/exec/jobs`) list and refresh path." — bearing: constrains
- 0596b9076 (2026-11-23, commit): "cexecList now hands its failure cause to its own callback (done(list, why)) instead of writing a shared cexecWhy." — bearing: constrains
- 8f3e2ceae (2026-11-23, commit): "Owner feedback: light theme gets a subtle Solarized Light tint (base3/base2 bg, base01 text, Solarized accents); dark theme palette unchanged." — bearing: constrains
- 7d4394053 (2026-11-23, commit): "Amber `!` is painted with the CSS Custom Highlight registry (`ws-bang`) over the contenteditable editor, not through `.chl`: the composer always attaches the capsule editor, `.chl` is hidden then" — bearing: constrains
- 0e918e11f (2026-11-23, commit): "JS: ucmdCompare orders a revision against the shown one; ucmdApply(list, post) differs for a read and a write answer; ucmdRefresh funnels a wake or a dropped answer into one read" — bearing: constrains
- 444c57b29 (2026-11-23, commit): "Event-driven only: no initial list fetch. An old daemon never sends execjobs, so the page asks for nothing and logs no 404; a 404 on the list (LAN viewer, closed lead) marks the page absent" — bearing: constrains
- ce20146ad (2026-11-22, commit): "Retry semantics: only a resend of the same text after an unresolved attempt sets retry; 4xx settles the attempt, a network failure or 5xx leaves it unresolved." — bearing: constrains
- 11caef152 (2026-11-22, commit): "Only a session rendered as a hosted non-child composer sets data-cexec; the server still decides (a non-lead is refused after acceptance with a refused card)." — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wsdashboard/assets/dashboard-composer-exec.js, dashboard-composer-ucmd.js, dashboard-exec-hud.js, dashboard-exec-hud-peek.js, dashboard-conversation.css, dashboard-exec-hud.css, agents-plugin-tool/internal/wsi18n/tables/, testdata/composer-bang.test.cjs |
| scope.surface | internal | client-only JS, CSS, i18n keys, and test cleanup; server routes, payloads, and stored state unchanged per (1493)=a |
| scope.new_public_symbol | no | none |
| scope.new_type_contract | no | none |
| scope.test_surface | existing | agents-plugin-tool/internal/wsdashboard/testdata/composer-bang.test.cjs, composer-exec.browser.cjs, composer-usercmd.browser.cjs, exec-hud.browser.cjs |
| complexity.reuse_points | confirmed | bangSync/bangMode/cexecSend/cexecList (dashboard-composer-exec.js#L312-514), execWake/xhFetch/.xgroup (dashboard-exec-hud.js#L161-222,L533-575), ::highlight(ws-bang)/[data-bang] (dashboard-conversation.css#L188-199), Escape/tabOut (dashboard-composer-keyboard.js#L34-58) |
| complexity.side_effect_risk | moderate | removing .d-cexec and .cbang affects ucmdContainer/ucmdRun in dashboard-composer-ucmd.js#L43,L197-200, xhCardFor/jump in dashboard-exec-hud-peek.js#L224-228,L720-724,L777-786, and async refusal/unknown UX |
| risk.correctness | moderate | must remove console/hint DOM without breaking cexecSend retry/admission tokens, exec_id delivery for bang-history, Esc/bangLeave, or per-read cexecList error reporting |
| risk.fit | moderate | 0 px vertical composer growth in dark and light themes while keeping Solarized Light `--tm-*` in light theme (`dashboard-conversation.css#L120`, `(1499)=a`) and non-lead refusal/unknown feedback via `noteWarn` toast (`(1501)`) |
| risk.test | moderate | composer-bang.test.cjs runs via node --test locally, whereas Go-driven Playwright suites (composer-exec.browser.cjs, composer-usercmd.browser.cjs, exec-hud.browser.cjs) and LOOK checks require w5090 |
| risk.security_or_contract | moderate | must preserve ! grammar, attachments/LAN/non-lead refusal behavior, and private cexecList admission token handling without leaking shell runs into transcript |

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

### Phase 2: Additive server fields [dropped]

Dropped by owner decision `(1493)=a` (2026-11-23): command status, elapsed time,
cwd, and live output are unified into the existing top-right Exec HUD
(`.xgroup` + `xpeek`), so additive inline-console card fields are no longer
needed.

### Phase 3: Render the new fields [dropped]

Dropped by owner decision `(1493)=a` (2026-11-23): the in-flow `.d-cexec`
console is removed in Phase 4.

### Phase 4: Remove inline `.d-cexec` console and bottom 22px `bash` hint row

Remove the in-flow `.d-cexec` console UI (header bar, collapse toggle, scrollback
card strip) and the bottom 22px `bash` mode hint row from the composer so that
entering `!` mode and executing `!` commands produce **0 px vertical composer
height growth**.

Keep intact:
- `!` shell-mode input styling (`data-bang` mono input, amber `!` via the
  CSS Custom Highlight API `ws-bang` (`dashboard-composer-exec.js#L484-496`,
  `dashboard-conversation.css#L188`), green Run button; dark terminal surfaces
  in dark theme and the Solarized Light `--tm-*` palette from `8f3e2ceae` in
  light theme per `(1499)=a`).
- `@` mention and `/` command popup suppression while in `!` mode, and the Esc
  rule (first Esc removes leading `!`, second Esc arms Tab-out).
- Terminal-styled `$` command slot blocks (`.d-ucmd`, dark in dark theme and
  Solarized Light in light theme per `(1499)=a`) with keycap button and full
  stored line.
- `cexecSend` and `ucmdRun` HTTP submission, retry semantics, admission-token
  priming (`cexecList` on load without restoring `.d-cexec` DOM cards), and
  non-console refusal/unknown feedback per `(1501)` and `(1505)`: keep a
  lightweight headless bounded poll (`GET /s/<rid>/composer-exec/<submission_id>`,
  capped at 20 polls or 30 s so repeated errors or stuck states stop cleanly)
  per accepted `202` submission (from both `cexecSend` and `ucmdRun`), and pass
  immediate `POST 200` retry payloads returning `exec_id` or
  `phase === "refused"` directly through the same headless handler. Once
  `exec_id` is populated, dispatch
  `window.dispatchEvent(new CustomEvent("ws:cexec-admitted", { detail: { submission_id, command, exec_id, origin } }))`
  with `origin: "cexec"` (`cexecSend`) or `origin: "ucmd"` (`ucmdRun`) for
  `261123-feat-ws-dashboard-composer-bang-history`, trigger the existing
  `.xgroup` Exec HUD refresh (`execWake` / `xhFetch`) so the running `.xchip`
  pill appears promptly in `.cbadges` (inside `.sbwrap`,
  `dashboard.tmpl#L26,279`) and clicking it opens `xpeek`, and stop polling
  `composer-exec/<id>`. Surface `card.unknown === true` (initial `200` or `404`
  poll, keeping the draft on `cexecSend`) and `phase === "refused"` (immediate
  `POST 200` or post-`202` poll) via the floating `noteWarn` toast (`WsToast`,
  0 px composer height change), retaining `js.cexec.unknown` and
  `js.cexec.refused` across all 11 locales.
- Remove unused `.d-cexec` / `bash`-hint DOM markup, `cexecCards` / `cexecBox`
  / `sessionStorage` card restoration, `ucmdContainer`'s `cexecBox` anchor
  reference (`dashboard-composer-ucmd.js#L43`), `xpeek` "Jump to card"
  (`xhCardFor` — including its callsite inside `xhPeekRender`'s signature array
  at `dashboard-exec-hud-peek.js#L159` — and `act === "jump"` in
  `dashboard-exec-hud-peek.js#L224-L228,L720-L724,L777-L786`), `.cexec.xflash`
  (`dashboard-exec-hud.css#L164`), orphaned `.d-cexec` / `bash`-hint /
  `js.xhud.pk.jump*` CSS rules and i18n keys across all 11 locale tables
  (`agents-plugin-tool/internal/wsi18n/tables/`), and the card-jump assertion in
  `testdata/exec-hud.browser.cjs#L440-L451` per `(1500)`. Migrate
  `testdata/composer-exec.browser.cjs` and `testdata/composer-usercmd.browser.cjs`
  (alongside `testdata/exec-hud.browser.cjs`) from `.d-cexec .cexec` card
  assertions to `.xgroup` / `xpeek` / `noteWarn` / `ws:cexec-admitted`
  assertions.

#### Verification

- Run touched standalone Node tests with `node --test`
  (`testdata/composer-bang.test.cjs`), and run Go-driven Playwright fixtures
  (`testdata/composer-exec.browser.cjs`, `testdata/composer-usercmd.browser.cjs`,
  `testdata/exec-hud.browser.cjs`) via `go test` on `w5090`: verify `#composer`
  height changes by `0 px` when entering/leaving `!` mode and when running a `!`
  command; verify `!` shell-mode input (dark in dark theme, Solarized Light in
  light theme per `(1499)=a`) / amber `!` / green Run / `@`+`/` suppression /
  Esc rule / `$` slot blocks / headless bounded `cexecPoll` (including poll cap
  and immediate `POST 200` retry payloads) emitting `ws:cexec-admitted` with
  `origin: "cexec"|"ucmd"` + `.xgroup` HUD refresh / `noteWarn` toast for
  `unknown` and `refused` (for both `cexecSend` and `ucmdRun`) / removal of
  `xpeek` Jump-to-card remain working.
- Capture a before/after LOOK of idle, `!` typed, and active `!` run (with
  `.xgroup` pill visible in `.cbadges`) in dark and light themes to confirm zero
  vertical composer growth.
- Run Go tests on `w5090` (covers embedded asset generation, i18n tables, and Go-driven Playwright fixtures):
  ```bash
  bash D:/sys_lock.sh remote w5090 run <who> <min> --sync '<worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/... ./internal/wsi18n/...'
  ```

### Result (40daceb) - 2026-11-23

Landed (40daceb): removed the in-flow `.d-cexec` console DOM, `.cbang` bottom hint row, `sessionStorage` card restoration, `ucmdContainer`'s `cexecBox` anchor (`dashboard-composer-ucmd.js`), `xpeek` Jump-to-card (`xhCardFor`, `act === "jump"`, `.cexec.xflash`), orphaned CSS rules, and 24 orphaned `js.cexec.*` / `js.xhud.pk.jump*` i18n keys across all 11 locale tables per `(1493)=a`, `(1499)=a`, `(1500)`, `(1501)`, and `(1505)`. Kept `!` shell-mode input styling (`data-bang`, `ws-bang`, green Run, `@`/`/` suppression, Esc rule), `$` `.d-ucmd` slot blocks, dark `#131417` and Solarized Light `--tm-*` (`#fdf6e3`) surfaces, `cexecPrime()` (`cexecList` on load without restoring cards), and bounded headless submission polling (`CEXEC_POLL_MAX = 20`, `CEXEC_POLL_MS = 30000`) routing both `cexecSend` and `ucmdRun` (`202` + immediate `POST 200` retries), dispatching `ws:cexec-admitted` (`origin: "cexec"|"ucmd"`), calling `execWake()` / `xhFetch()`, and surfacing `unknown` / `refused` via `noteWarn` (`WsToast`). Migrated `composer-bang.test.cjs`, `composer-exec.browser.cjs`, `composer-usercmd.browser.cjs`, and `exec-hud.browser.cjs` to headless HUD + toast + `0 px` height growth assertions.

Verification:
- `node --test agents-plugin-tool/internal/wsdashboard/testdata/composer-bang.test.cjs`: 9/9 pass (`68911.5ms`).
- Remote `w5090` (`python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/... ./internal/wsi18n/...`): `ok github.com/kang-sw/devenv/internal/wsdashboard 341.208s`, `ok github.com/kang-sw/devenv/internal/wsi18n 3.089s`.
- Before/after Playwright LOOK captures (`D:/Temp/composer-bang-p4-look/`): `#composer` height remained `193px` (`0 px` delta vs `221px` `!` / `297.8px` active before) across `idle`, `!` typed, and active `!` run in both dark (`rgb(19, 20, 23)`) and Solarized Light (`rgb(253, 246, 227)`).
- `code-review-lite` (`agy.medium`, `58a4049..40daceb`): clean with 2 minor test-coverage observations remaining (poll cap 5xx vs running stop condition, direct `ucmdRun` `unknown`/`refused` slot-click path).
