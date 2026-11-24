---
title: "터미널로 넘기기(HandOff) 백엔드 및 UI 장기 제거"
---

# 터미널로 넘기기(HandOff) 백엔드 및 UI 장기 제거

## Background

Explicit terminal handoff ("터미널로 넘기기" / `HandOff`) is being retired per owner decision `(1575)=a`.

Phase 1 (T0 transparent) has already landed on `develop`:
- Removed the `터미널로 넘기기` (`js.lead.handoff`) action button and click handler from `rpSessMake` in `agents-plugin-tool/internal/wsdashboard/assets/dashboard-leads.js` (hiding the entry point in both the head harness chip popover and rail row right-click menu).
- Added `Deprecated:` doc comments on `POST /agents/<id>/handoff`, `Runner.HandOff`, `terminalSession.terminalResume` (and `ClaudeBackend` / `AgyBackend` implementations), `Runner.resumeCommand`, and `State.ResumeCommand`.
- Updated `ai-docs/manuals/ws-mcp.md` to mark terminal handoff (`HandOff` / `terminalResume`) as deprecated and no longer required when implementing new harnesses.
- Kept passive `handedDraw` / `data-handed="1"` display, backend runtime behavior, i18n keys, and `.handed` CSS intact for T0 compatibility.

This ticket tracks Phase 2: the long-term removal of the deprecated backend route, harness interface method, state field, passive UI residue, i18n keys, and handoff-specific tests.

## Decisions

- **Compatibility tier (re-verify before executing Phase 2):**
  - `POST /agents/<id>/handoff` (`agents-plugin-tool/internal/wsagent/http.go:338-344`) is a dashboard HTTP control route, not an MCP tool. Once Phase 1 has shipped and no active UI calls it, removing the route only affects direct HTTP callers or pre-Phase-1 browser tabs still open against a newer daemon (which receive 404 without writing data).
  - Removing `State.ResumeCommand` (`resume_command` JSON field, `agents-plugin-tool/internal/wsagent/runner_state.go:126`) drops an omitted-when-empty presentation field; `WsRail.handedNotice` (`agents-plugin-tool/internal/wsdashboard/assets/rail.js:706-713`) already treats missing `resume_command` as `""`.
  - Before promoting this ticket to `ready/` and executing Phase 2, re-verify against `ai-docs/manuals/release-compatibility.md` whether any mixed-version caller still depends on `POST /agents/<id>/handoff` or `resume_command` (confirming T0/T1 vs any T3 minor-bump requirement).

## Constraints

### CRITICAL RETENTION BOUNDARY (MUST NOT DELETE even in Phase 2)

1. **Implicit terminal re-login & v0.51.x worker downgrade fence:**
   - Do **NOT** delete or alter `Record.HandedOffAt` (`handed_off_at`), `StatusHandedOff`, `conv.release()`, `HeldAgentWorker` (`agents-plugin-tool/internal/wsdashboard/registry.go:475-477`), or `agentRecordEnd.retired()` (`agents-plugin-tool/internal/wsdashboard/registry.go:455-457`).
   - These are required for:
     - Implicit terminal re-login when a terminal harness takes over a stopped hosted session (`relogin_session_key` -> `conv.release()`).
     - The v0.51.x worker downgrade fence (`agents-plugin-tool/cmd/ws-mcp/testdata/werk_0511_downgrade_test.go:38`, `wsdashboard/registry.go:455-457,475-477`).
   - Keep `TestReloginAfterAHandOffKeepsTheKey` (`agents-plugin-tool/internal/mcp/hosted_login_test.go:446`) and downgrade fence tests intact.

2. **Reverse `TakeOver` (terminal -> hosted):**
   - Do **NOT** delete or deprecate `TakeOver`: `HarnessTraits.TerminalTakeover`, `POST /agents/<id>/takeover`, `Runner.TakeOver`, `terminalSession.checkTerminalSession`, and `terminalSession.adoptTerminalSession` are intentionally separate and remain supported.

## Phases

### Phase 1: Remove deprecated terminal handoff backend, UI residue, i18n, and tests

Remove the deprecated explicit terminal handoff surface while strictly preserving the retention boundaries in `## Constraints`:

1. **Backend & harness contract (`agents-plugin-tool/internal/wsagent/`):**
   - Remove `POST /agents/<id>/handoff` route handling and doc comment in `http.go`, plus `st.ResumeCommand = ""` stripping in `view`.
   - Remove `Runner.HandOff` and `conv.handOff` in `handoff.go` (keeping `TakeOver`, `Reopen`, and implicit `conv.release()`).
   - Remove `terminalResume` from `terminalSession` in `backend_policy.go`, `Runner.resumeCommand`, `ClaudeBackend.terminalResume` in `claude_policy.go`, `AgyBackend.terminalResume` in `agy_policy.go`, and `resumeLine` if unused.
   - Remove `State.ResumeCommand` (`resume_command` JSON field) in `runner_state.go` and its population in `runner_state.go` and `runner_conv.go`.
2. **Dashboard passive UI & CSS (`agents-plugin-tool/internal/wsdashboard/assets/`):**
   - Remove `handedDraw`, `handedPage`, and `data-handed` wiring in `dashboard-leads.js` and `dashboard.tmpl` (and verify whether `PageView.HandedOff` is still needed or can be removed).
   - Remove `WsRail.handedNotice` in `rail.js`.
   - Remove `.handed` CSS rules in `dashboard.css` (lines 289-299).
3. **i18n (`agents-plugin-tool/internal/wsi18n/tables/`):**
   - Remove `js.lead.handoff`, `js.lead.handoff.note`, `js.lead.handoff.confirm.one`, `js.lead.handoff.confirm.other` (and `js.lead.handed*` if `handedDraw` is removed) across all 11 locales (`de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`), and remove their Phase 1 allowlist entries in `internal/wsi18n/keys_test.go`.
4. **Tests & docs:**
   - Remove or update handoff-specific Go and JS tests (`lead_takeover_test.go` handoff cases, `agy_lead_test.go` handoff case, `codex_admission_test.go` handoff check, `lead_http_test.go`, `pi_history_test.go`, `access_test.go`, `railscript_test.go`, `pages_stream_test.go`, `pageview_hud_test.go`, `backend_policy_test.go`).
   - Update `ai-docs/manuals/ws-mcp.md` to remove the deprecated `POST /agents/<id>/handoff` bullet while keeping `takeover`, `reopen`, and implicit terminal re-login documented.
