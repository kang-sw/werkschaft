# Plan: trim the dashboard audit UI

## Request (owner, 2026-11-23 11:49 and 11:51)

The recent server-span audit inspector work added UI the owner does not want.
Remove, from the dashboard frontend:

1. Every grouped "도구 호출 N회" (tool calls N, with "실패 N" failure count)
   frame in the conversation stream: the collapsible card that lists tool
   calls such as `whiteboard_update {...}` / `agenda_set {...}` with
   check/cross marks and durations. Screenshot:
   `C:\Users\user\.cache\ws@kang-sw-devenv\dashboard\sessions\renewaldevoteetranspirefencing\inbox\attachments\1d5de37ad1644b27-1.png`
2. The audit event search panel at the top: the "모든 감사 이벤트 검색..."
   search box and its filter chips (생각 / 훅 / 리마인더 / 서브...). Screenshot:
   `...\inbox\attachments\1d5de37ad1644b27-2.png` (same directory).
3. The harness badge in the top tab line (the orange "CL" chip after
   대화 / 파일 / terminal+). The owner says it is a no-op and came in with
   the same audit work. Screenshot: `...\inbox\attachments\afe701861f0b8319-1.png`.

Keep: clicking an individual command execution still opens its popup (the
audit/exec detail window). "That popup UX is enough."

## Outcome

- The three UI elements no longer render. Delete their rendering code, CSS,
  i18n keys used only by them, and tests asserting them; do not just hide
  them with CSS. Keep any server data/fields as they are (no Go route or
  payload change; compatibility T0, client only). If removing something
  requires a server change, stop and report instead.
- The per-command popup keeps working: verify it opens from an individual
  command execution after the change.
- Find the code with `git log` around the audit inspector commits
  (9655cb1c2, 72b381f6d, 84b3a0db8 and nearby) and under
  `agents-plugin-tool/internal/wsdashboard/assets/`. If an element turns out
  to predate the audit work and serves another live purpose (e.g. the CL
  badge carries real state), stop and report before deleting it.

## Verification

- Touched Node test files only, with `node --test <file>` (no full npm
  suites). Go tests (only if a Go test file or embedded asset list changes):
  w5090 only via
  `bash D:/sys_lock.sh remote w5090 run audit-ui-trim 30 --sync '<this worktree>' -- 'export PATH=$HOME/sdk/go/bin:$PATH; cd agents-plugin-tool && python3 scripts/generate-package-assets.py && go test ./internal/wsdashboard/... ./internal/wsi18n/...' > D:/Temp/audit-ui-trim/w5090.log 2>&1`, read back.
- Before/after LOOK captures of the three spots using the existing dashboard
  fixture/capture approach (headless browser against a fixture; never the
  owner's live dashboard or its login token). Save PNGs under
  `D:/Temp/audit-ui-trim/` and list their paths in the report.

## Constraints

- NO local `go test`/`go build`/Go tools, ws/ws-mcp exes, launcher or
  Electron on this PC. No network downloads.
- Temp files only under `D:/Temp/audit-ui-trim/`; delete all but the
  capture PNGs when done.
- Never print credentials, cookies, tokens or URLs with query strings.
- Commit on `impl/develop/audit-ui-trim` in this worktree with
  ws/git.commit, including this plan, with your own model's
  Co-Authored-By trailer. No push, no merge.

## Return boundary

Stop and report before changing any Go source or server payload, or before
deleting an element that serves a purpose beyond the audit UI.
