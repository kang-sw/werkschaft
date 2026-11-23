# Plan: drop the title-only "whiteboard update confirmed" card in the conversation stream

## Request (owner, 2026-11-23 10:28)

In the dashboard conversation stream, the result of a `whiteboard.update`
tool call shows a small header label "화이트보드 업데이트 확정됨" (whiteboard
update confirmed) followed by the topic card(s). Sometimes an extra card
appears between them that holds ONLY the bold title "화이트보드 업데이트
확정됨" and nothing else. It was not there before. The owner wants that
empty title-only card gone; the small label is enough.

Screenshots (view them):
- present: `C:\Users\user\.cache\ws@kang-sw-devenv\dashboard\sessions\renewaldevoteetranspirefencing\inbox\attachments\8ac5943376c97981-1.png`
- absent: `C:\Users\user\.cache\ws@kang-sw-devenv\dashboard\sessions\renewaldevoteetranspirefencing\inbox\attachments\8ac5943376c97981-2.png`

Lead hypothesis (unverified): the card appears when the update carries a
root-level command outside any `wb:topic` group that renders no topic content,
e.g. `<wb:floor id="1411"/>` (present case), whereas the absent case had only
`<wb:answer>` at the root plus a topic group. Confirm the real trigger from
the renderer source before fixing.

## Outcome

- Find where the conversation stream renders a whiteboard-update tool result
  (search `agents-plugin-tool/internal/wsdashboard/assets/` and
  `internal/wswhiteboard/` for the confirmed-title string / its i18n key in
  `internal/wsi18n/tables/`). Make the stream never render a card whose only
  content is that title; the small header label stays. Keep every card that
  has real content unchanged.
- Fix the root cause (the empty group/section is produced), not by CSS-hiding.
- Add or extend a test in the existing Node test for that renderer, covering
  an update with a root-level-only command (e.g. `wb:floor`) producing no
  empty card. Run only touched Node test files with `node --test`. If the
  change touches Go (unlikely), Go tests run only on w5090 (see Constraints).
- Compatibility: client-only, T0. State it in the commit AI Context.

## Verification

- `node --test` on the touched test file(s) passes.
- Visual: if a quick before/after capture is feasible without starting the
  shared daemon or the Werkschaft app, include it; otherwise state that the
  owner verifies live after the next daemon update.

## Constraints

- Read `ai-docs/manuals/performance.md`, `shipped-surface-boundary.md`,
  `release-compatibility.md` before editing under `agents-plugin-tool/`.
- NO local `go test`, Go tools, gofmt, ws/ws-mcp exes, launcher, Electron.
  Go only on w5090 via
  `bash D:/sys_lock.sh remote w5090 run wb-confirm-card 20 --sync '<this worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 ../scripts/generate-package-assets.py && go test ./internal/wsdashboard/...'`
  (adjust the generator path if it lives elsewhere), stdout redirected to
  `D:/Temp/wb-confirm-card/` and read back, then delete that dir.
- Do NOT start, stop, rebuild or swap the shared daemon. Never print
  credentials or board URLs.
- Commit on `impl/develop/wb-confirm-card` in this worktree only, ending with
  your own model's Co-Authored-By trailer. No push, no merge.

## Return boundary

Stop and report before changing whiteboard command semantics, MCP results or
schemas, or anything beyond the stream renderer, its i18n string use, and its
test.
