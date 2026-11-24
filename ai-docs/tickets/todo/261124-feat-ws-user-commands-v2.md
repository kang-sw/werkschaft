---
title: 사용자 커맨드 v2 — 플로팅 팝업·직접 등록·인라인 오버라이드 및 실행/요청
related:
  261121-feat-ws-board-command-slots: builds on session user-command slots and composer exec HUD
---

# User Commands v2 — Floating Popover, User CRUD, and Inline Override Execution

## Background

The current session user-command UI renders agent-registered commands as an inline `.d-ucmd` chip bar inside `#composer`, which stays completely hidden until an agent calls `session.register_user_command`. Users cannot register their own reusable commands, cannot inspect the command or working directory before running it, cannot attach an additional prompt to ask the agent to execute or analyze the command, and cannot tweak a command or prompt inline for a one-off run or save the tweak back to the template.

User Commands v2 replaces the inline `.d-ucmd` chip bar with a single always-available composer toolbar icon that opens a borderless dark-tonal floating popover listing both agent-registered (`[에이전트]`) and user-created (`[사용자]`) commands as compact 3-line inline-editable cards. To prevent accidental shell execution, agent turns, or deletions, all three action buttons (`▶` Run Now, `↗` Ask Agent, and `×` Delete) require a 1.5s hold-to-confirm gesture via the shared `WsHoldConfirm` helper, while reversible actions (`💾` Save to Template when dirty, per-line `↺` Revert) remain single-click.

## Visual Reference

- **Approved interactive HTML mockup (`(1564)=a`, updated for `(1570)` hold-to-confirm on `▶`/`↗`/`×`):** `ai-docs/assets/mockups/261124/agent-registered-commands/user-commands-v2.html`
- **Visual design system manual (`(1567)=a`):** `ai-docs/manuals/dashboard-style.md`
- **Layout contract from approved mockup:**
  - Single composer toolbar icon button (always visible, including empty 0-command state) toggling a floating popover above `#composer`; removes the legacy inline `.d-ucmd` chip bar.
  - Each command row is a compact **3-line inline-editable unit** (no separate "편집" button or expandable override drawer):
    - **Line 1 (single line, `flex-wrap: nowrap`):** `[에이전트]` / `[사용자]` micro-pill, title, warm dirty indicator dot (`●`) when Line 2 or Line 3 differs from the saved baseline, quiet `[cwd]` pill, and right-aligned borderless `26×26` tonal icon buttons:
      - `▶` (Run Now) — **1.5s hold-to-confirm** (`WsHoldConfirm`, primary accent radial/bar fill, live header hint, quick-click reminder toast).
      - `↗` (Ask Agent) — **1.5s hold-to-confirm** (`WsHoldConfirm`, calm tonal accent fill, live header hint, quick-click reminder toast).
      - `💾` (Save to Template) — single-click, revealed **only** when Line 2 or Line 3 differs from the saved baseline; persists edits and clears dirty/revert states.
      - `×` (Delete) — **1.5s hold-to-confirm** (`WsHoldConfirm`, danger `#f0797e` fill, live header hint, quick-click reminder toast).
    - **Line 2:** Always-editable recessed `$ command` input with right-side `22×22` `↺` revert button (active/highlighted only when dirty). Pressing `Enter` does **not** immediately execute the command (preserving hold-to-confirm safety); it focuses the row's `▶` hold button and shows a subtle hold hint.
    - **Line 3:** Always-editable recessed prompt input with right-side `22×22` `↺` revert button (active/highlighted only when dirty). Pressing `Enter` does **not** immediately send an agent message; it focuses the row's `↗` hold button and shows a subtle hold hint.
  - Editing Line 2 or Line 3 updates dirty indicators (`●`, warm left accent, `↺`, Line-1 `💾`) in place without re-rendering the DOM or losing input focus.
  - Inline `+ 새 커맨드` form inside the popover (title, `cwd`, command, optional prompt) and clean empty state (`0개`) inviting creation.
- **Verification visual requirement (`(1573)=ok`):** Capture the rendered dashboard popover (default list, inline-dirty state, hold-in-progress state, creation form, and empty state) and compare side by side against `ai-docs/assets/mockups/261124/agent-registered-commands/user-commands-v2.html`.

## Decisions

- **Single toolbar icon + floating popover with 3-line inline-editable cards (`(1550)=a`, `(1564)=a`):** Replace the inline `.d-ucmd` chip strip with one always-visible composer icon opening a floating popover supporting user CRUD, dual actions (`▶` Run Now into Exec Job HUD and `↗` Ask Agent), inline one-off command/prompt overrides with per-line `↺` revert, conditional `💾` save-to-template, and `×` deletion.
  - Rejected: keeping the inline `.d-ucmd` chip bar inside `#composer`; separate "편집" button with an expandable override drawer; multi-button labeled action row that wraps to two lines.
- **Visual design language (`(1567)=a`, `(1573)=ok`):** Follow `ai-docs/manuals/dashboard-style.md` (borderless Material tonal surfaces, recessed `#16181c` inputs, `color-mix` hover washes, zero harsh outlines), reuse `hold-confirm.js` (`WsHoldConfirm`, 1500ms), synchronize all 11 `wsi18n` locale tables, and verify via side-by-side capture against `user-commands-v2.html`.
  - Rejected: heavy 1px high-contrast bordered cards, custom duplicate hold-timer logic, or omitting side-by-side visual verification.
- **User-created command persistence scope (`(1569)=a`):** Persist user-created commands (`source: "user"`) at **project-root (`root`) scope** using the empty `reply = ""` sentinel in `user_commands` so they survive session close/restart and appear across every session in the same repository/worktree root (`Reconcile` must never prune `reply == ""` rows). Keep agent-registered commands (`source: "agent"`) session-scoped `(reply, root)` as today.
  - Rejected: session-scoped `(reply, root)` lifecycle for user-created commands (option `b`), which would lose user-authored commands whenever a session closes.
- **`↗` Ask Agent submission & 1.5s hold-to-confirm on all firing actions (`(1570)=a` + owner directive):**
  - `↗` submits immediately via `POST /s/<replyID>/message` as a structured readable message block (command title, `cwd` when set, fenced shell command block, and optional user prompt text) without modifying or overwriting any active draft in `#composer`.
  - **All three triggering/destructive buttons — `▶` (Run Now), `↗` (Ask Agent), and `×` (Delete) — require a 1.5s hold-to-confirm gesture** (`WsHoldConfirm`, pointer hold or keyboard `Space`/`Enter` hold, immediate cancel on release/leave/blur, live popover header confirmation hint, and gentle reminder toast on quick click). Bare `Enter` inside Line 2 or Line 3 must not bypass hold confirmation (focus the corresponding hold button with a subtle hint instead).
  - Rejected: prefilling `#composer` without sending (`(1570)` option `b`); single-click immediate execution on `▶` or `↗`.
- **SQLite schema extension & Compatibility Tier `T1` (`(1571)=ok`):** Keep `storeSchemaVersion = 2` in `user-commands.db` and add defaulted columns (`prompt TEXT NOT NULL DEFAULT ''`, `source TEXT NOT NULL DEFAULT 'agent'`, `cwd TEXT NOT NULL DEFAULT ''`) via idempotent `ALTER TABLE ... ADD COLUMN` on store open.
  - Source-backed compatibility verdict (`ai-docs/manuals/release-compatibility.md`): **T1** (with a T2 caveat only on old-binary delete + re-put). Old binaries pass `storeMigrate` (`store.go:117-122`), read explicit 8 columns in `load()` (`store.go:173`) ignoring extra columns, and write explicit 8 columns in `put()` (`store.go:204-207`) where `ON CONFLICT DO UPDATE` updates only `label, description, line, revision` and preserves `prompt`, `source`, and `cwd`. Old binaries ignore `reply == ""` rows during session lookup (`scopeKey{reply, root}` with non-empty session reply).
  - Rejected: bumping `storeSchemaVersion` from `2` to `3`, which triggers `ErrStoreNewer` (`store.go:117-119`) and disables `userCmds` on old binaries (**T3**).
- **Backward-compatible REST & MCP API extension (`(1572)=ok`, Compatibility Tier `T0`):**
  - Extend existing `/s/<replyID>/user-commands/*` routes (all mutating/run routes gated by `localViewer(r)`):
    - `GET /s/<replyID>/user-commands` — returns merged view of session agent commands `(reply, root)` and project-root user commands `("", root)`, each carrying `id`, `label`, `description`, `line`, `prompt`, `cwd`, `source` (`"agent"` | `"user"`), `revision`.
    - `POST /s/<replyID>/user-commands` — create a user command (`source: "user"`, stored under `("", root)`, server-generated or validated slug `id`, `label`, `line`, optional `prompt`, `cwd`, `description`).
    - `PUT /s/<replyID>/user-commands/<id>` — update template fields (`label`, `line`, `prompt`, `cwd`, optional `description`) for either a `[사용자]` or `[에이전트]` command in its existing scope (`source` preserved; `revision` incremented).
    - `POST /s/<replyID>/user-commands/<id>/run` — accept optional JSON body `{ "line"?: string, "cwd"?: string }` for one-off inline overrides (falling back to the stored template's `line` and `cwd` when omitted), launching `execjob.ShellLaunch` under `execjob.OriginUser` + `UserApproved = true` into the Exec Job HUD (`.xgroup`). Validate `cwd` (empty = session/project root; relative paths resolved cleanly against root; reject missing/non-directory paths with a clear 400 error).
    - `POST /s/<replyID>/user-commands/<id>/remove` — keep existing route and allow deleting either a `[사용자]` or `[에이전트]` command from its scope.
    - Keep SSE event `"usercmd"` unchanged; broadcast on all create/update/remove mutations (for `reply == ""` root-scoped mutations, notify all active sessions sharing `root`).
  - Extend MCP `session.register_user_command` with optional `prompt` (string) and optional `cwd` (string) — **T0 transparent** for existing callers; `source` is always `"agent"`. Preserve `session.unregister_user_command` unchanged.
  - Rejected: introducing parallel `/user-commands-v2` endpoints or changing required MCP tool parameters.

## Constraints

- **Required manuals (read before editing matching paths):**
  - `ai-docs/manuals/dashboard-style.md`
  - `ai-docs/manuals/release-compatibility.md`
  - `ai-docs/manuals/performance.md`
  - `ai-docs/manuals/ws-mcp.md`
  - `ai-docs/manuals/shipped-surface-boundary.md`
- **File-touch and memory budget (`ai-docs/manuals/performance.md`):** Keep active command registry in memory (`wsusercmd.Registry`), persist only mutations to `user-commands.db` (SQLite WAL), and keep popover open, inline typing, dirty checks, and revert interactions completely free of disk I/O.
- **Security / Viewer boundary:** All mutating CRUD and direct shell execution routes remain gated by `localViewer(r)` and `execjob.OriginUser`, matching existing `!cmd` (`composer-exec`) posture.
- **Validation bounds:** Preserve `wsusercmd` bounds (`MaxPerScope = 32` per scope — up to 32 user commands per `root` and 32 agent commands per `(reply, root)`, `MaxLabel = 64`, `MaxDescription = 256`, `MaxLine = 4<<10`, valid ID pattern `^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`) and bound `prompt` (`<= 4<<10`) and `cwd` (`<= 512`).
- **i18n parity:** Add/update `js.ucmd.*` keys symmetrically across all 11 locale tables under `internal/wsi18n/tables/` (`de`, `en`, `es`, `fr`, `it`, `ja`, `ko`, `pt-BR`, `ru`, `zh-Hans`, `zh-Hant`).

## Prior Art

- SQLite store & in-memory registry: search `StoreName = "user-commands.db"` and `type Command struct` under `agents-plugin-tool/internal/wsusercmd/`.
- MCP tool registration & dispatch: search `registerUserCommandTool` under `agents-plugin-tool/internal/mcp/`.
- Dashboard HTTP routes, Exec Job HUD launch, and SSE `"usercmd"` broadcast: search `handleUserCommands` and `userCommandRun` under `agents-plugin-tool/internal/wsdashboard/` and `agents-plugin-tool/cmd/ws-mcp/`.
- Composer message submission (`POST /s/<replyID>/message`): search `handleInboxWrite` and `messageRequest` under `agents-plugin-tool/internal/wsdashboard/`.
- Frontend command module & shared hold-to-confirm helper: search `ucmdRender` in `dashboard-composer-ucmd.js` and `window.WsHoldConfirm` in `hold-confirm.js` under `agents-plugin-tool/internal/wsdashboard/assets/`.

## Phases

### Phase 1: Floating User Commands v2 Popover, Backend CRUD/Override API, and Verification

1. **Store & Registry (`internal/wsusercmd`):**
   - Keep `storeSchemaVersion = 2`; add idempotent `ALTER TABLE user_commands ADD COLUMN` migrations for `prompt TEXT NOT NULL DEFAULT ''`, `source TEXT NOT NULL DEFAULT 'agent'`, and `cwd TEXT NOT NULL DEFAULT ''` on store open (T1 compatible; test opening an existing v2 DB without those columns and verifying preservation).
   - Extend `Command` with `Prompt`, `Cwd`, and `Source` (`"agent"` | `"user"`, defaulting empty to `"agent"`).
   - Support project-root persistent scope `scopeKey{reply: "", root: root}` for user-created commands alongside session scope `scopeKey{reply, root}` for agent commands; ensure `Reconcile` skips `reply == ""` rows so user commands survive session close/restart, and provide merged list/lookup/update/delete operations across `(reply, root)` + `("", root)` (with clear handling if an ID exists in one scope).
2. **MCP & Dashboard HTTP/SSE (`internal/mcp`, `internal/wsdashboard`, `cmd/ws-mcp`):**
   - Add optional `prompt` and `cwd` arguments to `session.register_user_command` (T0).
   - Extend `/s/<replyID>/user-commands/*` with `POST` (create user command), `PUT /<id>` (update template in its scope), `POST /<id>/run` accepting optional `{ line?, cwd? }` one-off overrides with `cwd` resolution/validation under `localViewer(r)` + `execjob.OriginUser`, and `POST /<id>/remove` supporting both scopes.
   - Ensure `"usercmd"` SSE notifications reach the owning session for agent-command mutations and all active sessions on the same `root` for user-command (`reply == ""`) mutations.
3. **Frontend Popover, Hold-to-Confirm & Styling (`internal/wsdashboard/assets/`, `internal/wsi18n/tables/`):**
   - Replace the inline `.d-ucmd` chip bar with a single always-visible composer toolbar icon (with count badge when non-empty) toggling a floating popover above `#composer` matching `ai-docs/assets/mockups/261124/agent-registered-commands/user-commands-v2.html` and `ai-docs/manuals/dashboard-style.md` (keeping `.cfoot` layout clean when `.d-ucmd` is removed).
   - Render each command as a 3-line inline-editable card (Line 1: `[에이전트]`/`[사용자]` pill, title, dirty `●`, `[cwd]` pill, 26×26 icon buttons `▶`/`↗`/conditional `💾`/`×`; Line 2: recessed `$ command` input + `↺`; Line 3: recessed prompt input + `↺`), syncing dirty states in place without losing focus.
   - Wire `window.WsHoldConfirm` (`hold-confirm.js`, 1500ms) to **`▶` (Run Now), `↗` (Ask Agent), and `×` (Delete)** with distinct tonal radial/bar fills, live popover header confirmation hint, keyboard `Space`/`Enter` hold support, and quick-click reminder toast. Make `Enter` inside Line 2/Line 3 focus the corresponding hold button with a subtle hint rather than auto-firing.
   - Implement `↗` immediate submission via `POST /s/<replyID>/message` with a structured readable block (title, `cwd`, fenced command, optional prompt) without touching `#composer` draft text.
   - Implement the inline `+ 새 커맨드` creation form and empty state (`0개`), and add/sync all `js.ucmd.*` keys across all 11 locale tables in `internal/wsi18n/tables/`.
4. **Verification:**
   - **Remote `w5090` Go verification:** `go vet ./...`, `GOOS=windows go vet ./...`, `go test -race -count=1 ./internal/wsusercmd/... ./internal/mcp/... ./internal/wsdashboard/... ./internal/wsi18n/...` (including T1 migration/compat tests, root-scoped `Reconcile` preservation, REST CRUD/override/`localViewer` tests, MCP optional `prompt`/`cwd` tests, and 11-locale i18n parity).
   - **Local Node verification:** `node --test` suite covering popover toggle, 3-line inline dirty/revert without focus loss, `▶`/`↗`/`×` 1.5s hold-to-confirm (pointer + keyboard + cancel + quick-click toast), `Enter` non-firing focus guard on Line 2/3, `💾` template save, `↗` structured `POST /message` payload preserving `#composer` draft, `+ 새 커맨드`, and empty state.
   - **Visual side-by-side verification:** Render the live dashboard popover (list, dirty, hold-in-progress, create form, empty state) and compare side by side against `ai-docs/assets/mockups/261124/agent-registered-commands/user-commands-v2.html` and `ai-docs/manuals/dashboard-style.md`.
