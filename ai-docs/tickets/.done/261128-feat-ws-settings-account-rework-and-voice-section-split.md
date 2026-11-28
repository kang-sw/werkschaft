---
title: "계정 패널 목적별·인덴테이션 리워크 및 Settings 음성(Voice) 섹션 분리"
related:
  261125-feat-ws-desktop-native-os-stt: prerequisite — lands dashboard-settings-voice.js and Voice Providers UI inside Account first; this ticket promotes Voice to its own /settings?section=voice section and reworks Account
  261127-feat-ws-harness-account-switcher-and-slot-continuity: done — reworked UI surface (assets/dashboard-settings-account.js); it did not add an HTTP route for existing wscred.UpdateSlotLabel (no "slots/rename" case in account_routes.go#L106-L118, and UpdateSlotLabel is referenced only in wscred/account_slots.go#L787-L823 and account_slots_test.go#L90), so this ticket adds it
  261127-feat-ws-werk-usage-metrics-and-openai-codex-unification: companion track (plan usage popup rework stays separate)
blocked-by:
  - 261125-feat-ws-desktop-native-os-stt
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: f60a831f4c66fdfc
sage-review-completeness-reviewed: f60a831f4c66fdfc
completed: 2026-11-28
---

# Settings Account Panel Purpose/Indentation Rework and Dedicated Voice Section Split

## Background

`Settings → Account` (`agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-account.js`, currently 1,446 lines) accumulated every authentication and API credential surface as a flat list at identical visual depth: OpenRouter, OpenAI SIWC, OpenAI background usage consent, Codex slots, Claude slots, AgY single-account auth, Voice API keys (`openai_api`, `groq`, plus the incoming `261125-feat-ws-desktop-native-os-stt` Voice Providers / Powerline priority bar), and Local Werk Endpoints.

After inspecting the live dashboard, the owner confirmed two structural UX improvements and approved an interactive visual mockup (`(1820)=ok`):

1. **Purpose-based separation and Voice section split:** Move Voice Providers and Voice API settings out of `Settings → Account` into a dedicated **`Settings → Voice` (`/settings?section=voice`)** sidebar section grouped under `계정 · 인증` alongside `Account`. Inside `Account`, separate providers cleanly into two unmistakable purpose groups: **① External CLI Harness Accounts** (Claude Code, Codex CLI, Antigravity/AgY) and **② Built-in Werk Runtime & API** (OpenAI SIWC, OpenRouter, Local Werk Endpoints).
2. **Structural indentation showing ownership:** Express 3-level hierarchy using borderless Material tonal surface elevation and a muted `2px` left vertical stem (`--stem`) instead of flat sibling rows or heavy borders — placing saved slots, inline auth/key/endpoint drawers, and OpenAI SIWC's `background_consent` sub-setting visibly inside their parent provider's indented stem. Also expose inline slot renaming for non-default Codex/Claude slots via the already-implemented `wscred.AccountSlotRegistry.UpdateSlotLabel` method, and split the oversized `dashboard-settings-account.js` into focused, maintainable modules.

## Visual Reference

- Approved interactive mockup (owner `(1820)=ok`): `ai-docs/assets/mockups/261128/account-panel-rework/index.html` (`styles.css`, `app.js`, `mock-api.js`).
- **Authority scope of the mockup:** it is authoritative for the Account panel hierarchy, the sidebar `계정 · 인증` nav grouping, and the OpenRouter chip and Voice back-link affordances only. Its `voiceHtml()` body (`app.js`) is a simplified placeholder, illustrative only: it omits the native row, Powerline priority bar, Groq row and voice key inputs that `261125` ships and this ticket preserves. The `Settings → Voice` body is compared against the Voice Providers mockup that `261125` Phase 4 records, not against this mockup.
- **Contract note on mock-only route names:** `mock-api.js` used demo route names `openai/sign-out` and `key/recheck` for local simulation. Production code MUST continue using the existing real HTTP routes: `POST /api/account/sign-out` (`account_routes.go:143-144`, `account_signin.go:103-121`) and `POST /api/account/key` with `{"op": "recheck", "provider": "openrouter"}` (`account_routes.go:135-136, 211-250`).

## Decisions

- **Dedicated `Settings → Voice` section (`/settings?section=voice`):** Register `"voice"` in `dashboard-settings.js` (`sections`, `sectionLabels`, `sectionNotes`, `activeSection` query parsing, `selectSection`, `renderHeader`, `renderCounts`, `renderContent`) and render a lightweight sidebar nav group (`계정 · 인증`) pairing `account` and `voice` in `.snav`. Move the `dashboard-settings-voice.js` Voice Providers UI (landed by prerequisite `261125-feat-ws-desktop-native-os-stt`) out of `Settings → Account` into `Settings → Voice`.
  - **Supersedes `261125-feat-ws-desktop-native-os-stt` (1817=a):** that decision places Voice Providers in `/settings?section=account` with an `account.focus("voice")` route; this ticket reverses the placement per owner `(1820)=ok` (the Account panel was too crowded). Since `261125` lands first (`blocked-by`), this is a sequencing fact: this ticket moves what `261125` shipped, and the old `account.focus("voice")` path remains only as the legacy-link normalization below.
  - **Voice module contract is defined by what `261125` actually shipped:** the mount, gating and count contract for `dashboard-settings-voice.js` is unspecified until `261125` Phase 4 lands (no `window.WsSettingsVoice` global is promised there). The implementer reads the landed module and, where it exposes no mount hook, adds one (a `window.WsSettingsVoice` lifecycle mirroring `WsSettingsAccount`) without changing Voice behavior. The Voice sidebar entry is hidden when the module is absent, mirroring how `renderSections` gates `account` on `WsSettingsAccount`.
  - *Rejected:* Keeping Voice Providers appended to the bottom of `Settings → Account` (root cause of the cluttered account panel).
- **Backward-compatible deep-link normalization:** Update unconfigured composer mic / PTT navigation (`#cvox` / `dashboard-voice.js`) to open `/settings?section=voice` directly, AND normalize legacy `/settings?section=account&provider=voice` (both at startup query parsing in `dashboard-settings.js` and in `selectSection("account", "voice")`) to `activeSection = "voice"` so external or cached links never break.
  - *Rejected:* Breaking `/settings?section=account&provider=voice` silently or duplicating Voice controls in both sections.
- **Two purpose groups + 3-level borderless tonal indentation in `Settings → Account`:**
  - **Level 0 (Purpose Group tonal cards):** `① External CLI Harness Accounts` (`Claude Code`, `Codex CLI`, `Antigravity (AgY)`) and `② Built-in Werk Runtime & API` (`OpenAI (ChatGPT Plan · SIWC)`, `OpenRouter API`, `Local Werk Endpoints`), each with a concise 1-line purpose subtitle and zero boxed card borders.
  - **Level 1 (36px indented Provider block):** Provider monogram/icon, name, concise active status summary, and primary provider actions (`Sign in`, `Device code`, `Add existing`, `Edit key`, `Add endpoint`).
  - **Level 2 (indented child container with 2px muted tonal vertical stem `--stem`):**
    - Saved slots (`Default`, managed/custom slots) / OpenAI SIWC accounts / Local Werk Endpoints, with low-noise actions (`Use`, inline `Rename` for non-default slots, `Sign in again` when re-auth is needed, 1.5s hold-to-confirm delete).
    - Inline action drawers (OAuth Copy-link + Paste-code, Device-code `verify_url` + `user_code`, Add existing `config_dir` + `label`, OpenRouter key drawer, Local Endpoint add/edit form, AgY sign-in drawer) opening as recessed wells directly inside their parent provider's Level-2 stem.
    - OpenAI SIWC `background_consent` toggle rendered as a recessed sub-setting row directly inside OpenAI SIWC's Level-2 stem (disabled when no OpenAI SIWC account is linked), rather than floating as an unrelated sibling row.
    - OpenRouter row displays a clickable status chip jumping directly to `Settings → Voice` when Voice shares the top OpenRouter key (state source: the effective shared-key value as `261125` defines it, i.e. the explicit `dashboard.voice_openrouter_shared_key` setting from `261125` Phase 2, else its legacy `voice_backend == "openrouter"` fallback, combined with the top OpenRouter key being configured; reuse that effective-value rule, or the `capabilities` availability, rather than the raw key, and obtain the value however the landed `261125` code exposes it to `dashboard-settings.js`; no new `/api/account/status` field; the mockup's `voice_shared` field is mock-only) (and the Voice section retains a reverse link back to `Settings → Account` for managing the shared OpenRouter key).
  - *Rejected:* Flat sibling rows at uniform depth, or bordered accordion boxes that violate `ai-docs/manuals/dashboard-style.md`.
- **Additive `POST /api/account/slots/rename` endpoint (Compatibility Tier: T0):**
  - Expose `POST /api/account/slots/rename` in `account_routes.go` and `account_slots_routes.go` under the exact existing `/api/account/*` guards (`localViewer(r)`, `POST`, `sameOrigin(r)` / `crossSiteFetch(r)`, 72 KiB `DisallowUnknownFields` body limit), calling the already-existing `(*wscred.AccountSlotRegistry).UpdateSlotLabel(harness, slotID, label)` (`wscred/account_slots.go:788-823`).
  - Enforce a trimmed label length bound of 1–80 runes in `wscred` (`UpdateSlotLabel` and `AddSlot`, returning `ErrInvalidAccountSlot` -> HTTP `400 invalid_slot`) matching the UI `maxlength="80"`; `"default"` remains immutable (`ErrDefaultSlotImmutable` -> HTTP `400 default_slot_immutable`). Align the existing byte-length label check at sign-in start (`account_slot_signin.go`, `len(label) > 80`) to the same trimmed-rune bound so one label cannot pass one check and fail the other.
  - **Response and validation shape:** the rename handler responds `200` with the renamed slot (the `AccountSlot` JSON, as `slots/add` does via `writeJSON(w, http.StatusOK, slot)`), reading it back with `ResolveSlot` after `UpdateSlotLabel`; it rejects an unsupported harness or empty `slot_id` with `400 invalid_request` in the handler before calling the registry (`UpdateSlotLabel` would otherwise map an empty `slot_id` to `ErrDefaultSlotImmutable`, `account_slots.go` line 796). The UI updates from this slot plus the `account-changed` SSE refresh.
  - **Compatibility Tier verdict — T0 (transparent):**
    - *On-disk store:* Reuses the existing `AccountSlot.Label` (`json:"label"`) field in `account-slots.json` (`accountSlotsSchema = 1`, `wscred/account_slots.go:24, 29, 46-53`) with zero schema changes; older binaries read and write `label` identically.
    - *HTTP surface:* `POST /api/account/slots/rename` is strictly additive (`account_routes.go:106-118`); older binaries ignore it and never call it; existing `/api/account/slots/{add,remove,switch,sign-in/*}`, `/api/account/sign-out`, and `/api/account/key` payloads and responses are unchanged.
  - *Rejected:* Overloading `slots/add` for rename or storing labels client-side in `localStorage` (breaks cross-browser/desktop parity and `account-changed` SSE propagation).
- **Modular JS split of `dashboard-settings-account.js`:** Split the 1,446-line `dashboard-settings-account.js` during the rework into clean, focused modules registered in `pages.go` (`SettingsScript` / `SettingsCSS`), keeping every touched JS file well below the 1,000-line file-size advisory threshold while preserving the `window.WsSettingsAccount` (and `window.WsSettingsVoice`) public lifecycle hooks consumed by `dashboard-settings.js`.
  - *Rejected:* Growing `dashboard-settings-account.js` further beyond 1,500 lines.
- **Scope boundary:** Plan usage popover (`usage.js`) rework is intentionally out of scope for this ticket and stays untouched here and is left to a separate future ticket (to align with `261128-feat-ws-dashboard-ide-shell-overhaul` moving `.usage-toggle` to the bottom global status bar).

## Constraints

- Convention: ai-docs/manuals/shipped-surface-boundary.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/release-compatibility.md (declared for agents-plugin-tool/internal/)
- Convention: ai-docs/manuals/performance.md (declared for agents-plugin-tool/)
- Convention: ai-docs/manuals/config-settings-ui.md (declared for agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*)
- Convention: ai-docs/manuals/dashboard-style.md (declared for agents-plugin-tool/internal/wsdashboard/assets/, ai-docs/assets/mockups/)
- **Required manuals (from `AGENTS.md` `### Implementation Conventions`):**
  - `ai-docs/manuals/dashboard-style.md` — borderless Material tonal elevation, canonical `--wb-*` CSS tokens, zero harsh borders, light/dark parity, hover/focus accessibility, and responsive layout below 760px.
  - `ai-docs/manuals/config-settings-ui.md` — keep settings catalog, section registration, and all 11 `internal/wsi18n/tables/*.json` locale files synchronized with exact key parity and zero duplicate keys.
  - `ai-docs/manuals/performance.md` — Storage Type 5 private file for `account-slots.json` (no new file touches on read paths; `UpdateSlotLabel` writes only on explicit user rename mutation), minimal DOM updates without tearing down active inline editors/drawers during background `account-changed` SSE refreshes.
  - `ai-docs/manuals/release-compatibility.md` — maintain T0 additive compatibility; do not rename or alter existing `/api/account/*` paths or response envelopes.
  - `ai-docs/manuals/shipped-surface-boundary.md` — no devenv-only copy or paths in shipped UI strings or assets.
- **No live paid API calls or real OAuth browser popups in tests:** Exercise all Go/JS tests with `httptest.Server`, fake registries, and DOM unit tests (`node --test`).
- **Keep existing `261125` Voice functionality intact:** Moving Voice Providers from `Settings → Account` to `Settings → Voice` must preserve OS Native STT availability detection, OpenRouter shared-key toggle & modal, Powerline horizontal DnD priority bar (`dashboard.voice_backend_order`), OpenAI/Groq/Local voice keys, and `#cvox` / Spacebar PTT behavior without regression.

## Prior Decisions

- 261125-feat-ws-desktop-native-os-stt (2026-11-28, Decisions): "Voice Providers account section (1817=a): /settings?section=account gains one Voice Providers section ... in a dedicated module pair (dashboard-settings-voice.js + CSS) wired from dashboard-settings-account.js and pages.go" — bearing: supersedes the Account placement and `account.focus("voice")` routing (owner `(1820)=ok`); keeps the dedicated module pair
- 261127-feat-ws-harness-account-switcher-and-slot-continuity (2026-11-28, Result dcb83b49): "claude slot integration (internal/wsagent/claude.go, claude_slots.go, internal/wscred/slot_login.go, internal/wsdashboard/account_slot_signin.go, assets/dashboard-settings-account.js)" — bearing: constrains
- 261127-feat-ws-harness-account-switcher-and-slot-continuity (2026-11-27, D1): ".usage-toggle popover ... renders usage charts, per-harness active account slot switcher controls, and a shortcut link to /settings?section=account" — bearing: constrains
- dcb83b49 (2026-11-28, commit): "agy remains single-slot read-only in the topbar switcher, while /settings?section=account exposes /api/account/harness-auth/{status,start,complete,cancel,reverify} (gated by localViewer + sameOrigin" — bearing: constrains
- 261122-feat-ws-provider-auth-layer (2026-11-25, Result): "Settings → Account is machine-wide. It manages OpenRouter verification, OpenAI sign-in and plan consent, and named vLLM and Ollama endpoints." — bearing: constrains
- 261115-feat-ws-dashboard-external-stt-voice-typing (2026-11-15, Decisions): "Voice-only provider keys, fail closed: openai_api and groq registry entries are voice-only; not served to agent sessions through wscred.ServeToken" — bearing: constrains
- 1095ba52 (2026-11-28, commit): "Moves the 10 companion keys ... into the native Dashboard Settings companion section (dashboard-settings-companion.js/css) ... routes rail gear/quick-menu Settings links to /settings?secti" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | wscred/account_slots.go, wsdashboard/account_routes.go, account_slots_routes.go, pages.go, assets/dashboard-settings*.js/css, dashboard-voice.js, 11 wsi18n tables |
| scope.surface | cross-module | new HTTP route POST /api/account/slots/rename plus wscred label validation and dashboard assets |
| scope.new_public_symbol | no | none; additive route string only, UpdateSlotLabel already exported (wscred/account_slots.go#L787-L823) |
| scope.new_type_contract | yes | request body {harness, slot_id, label} and rename response envelope; new 1-80 rune label bound on UpdateSlotLabel/AddSlot |
| scope.test_surface | existing | wscred/account_slots_test.go, wsdashboard/account_slots_routes_test.go, account_routes_test.go, account_settings_browser_test.go; new JS tests also planned |
| complexity.reuse_points | confirmed | UpdateSlotLabel, handleAccountSlots* guards in account_routes.go#L86-L103, hold-confirm.js, existing settings sections |
| complexity.side_effect_risk | high | splits a 1,446-line JS file and moves Voice UI whose module dashboard-settings-voice.js does not exist yet (pending 261125-feat-ws-desktop-native-os-stt) |
| risk.correctness | high | legacy deep-link normalization, SSE refresh preserving open drawers, Voice move without regression |
| risk.fit | high | depends on unlanded 261125 module layout; Voice currently inline in dashboard-settings-account.js#L29-L62 |
| risk.test | moderate | needs headless browser and JS DOM checks across themes and widths |
| risk.security_or_contract | moderate | new mutating route must carry localViewer/sameOrigin/72 KiB guards; T0 additive |

## Phases

### Phase 1: Slot Rename Route, Dedicated Settings Voice Section, and Account Panel Hierarchy Rework

Work order and interim checkpoints: sub-heading 1 (backend) lands first with its `go test -race` green before the UI sub-headings use the route; sub-heading 2 (Voice split) and 3 (Account rework) follow, and sub-heading 4 closes with locale parity and the full verification. All Go and `node` commands run from `agents-plugin-tool/`. Starting requires `261125` landed through its Voice Providers UI and composer click routing (Phases 4-5), since sub-headings 2 and 3 move that code.

#### 1. Backend (`wscred` & `wsdashboard`)
- In `agents-plugin-tool/internal/wscred/account_slots.go`: enforce a 1–80 rune bound on trimmed `label` in `UpdateSlotLabel` (and max 80 runes in `AddSlot`), returning `ErrInvalidAccountSlot` on violation; keep `DefaultAccountSlotID` immutable (`ErrDefaultSlotImmutable`). Add unit tests in `account_slots_test.go`.
- In `agents-plugin-tool/internal/wsdashboard/account_routes.go` and `account_slots_routes.go`: add `POST /api/account/slots/rename` accepting `{harness, slot_id, label}` under the existing `localViewer` + `POST` + `sameOrigin` + 72 KiB `DisallowUnknownFields` guards; invoke `UpdateSlotLabel`, trigger `d.accountChanged()`, and return the renamed `AccountSlot` as `200` JSON (see the Decisions response shape, matching `slots/add`). Also align the sign-in start label check in `account_slot_signin.go` (`len(label) > 80`) to the same trimmed-rune bound. Add `-race` handler tests covering valid rename, a sign-in-start label at/over the bound, default-slot rejection, empty/overlong label rejection, unknown field rejection, and cross-origin/non-local rejection.
- In `agents-plugin-tool/internal/wsdashboard/pages.go`: register the split settings account/voice JS/CSS modules in `SettingsScript` and `SettingsCSS` in clean load order.

#### 2. Settings Navigation & Voice Section Split (`dashboard-settings.js`, `dashboard-settings.css`, `dashboard-settings-voice.*`, `dashboard-voice.js`)
- Register `"voice"` in `dashboard-settings.js` (`sections`, `sectionLabels`, `sectionNotes`, URL query parsing, `selectSection`, `renderHeader`, `renderCounts`, `renderContent`).
- Add lightweight sidebar nav group headers/dividers in `.snav` (`dashboard-settings.js`, `dashboard-settings.css`) pairing `account` and `voice` under `계정 · 인증` without breaking keyboard navigation or existing section badges.
- Mount `dashboard-settings-voice.js` as the body of `Settings → Voice` (`/settings?section=voice`) instead of appending it inside `Settings → Account`, preserving full `261125` Voice Providers functionality and adding a compact link back to `Settings → Account` for shared OpenRouter key management.
- Update `#cvox` / `dashboard-voice.js` unconfigured deep-links to `/settings?section=voice` and normalize legacy `/settings?section=account&provider=voice` to open `activeSection = "voice"`.

#### 3. Account Panel Hierarchy Rework & Modular JS Split (`dashboard-settings-account*.js`, `dashboard-settings-account.css`)
- Split `dashboard-settings-account.js` into focused, cohesive modules (each well under 1,000 lines; e.g., shared account request/DOM/hold helpers, External CLI Harness slots + AgY auth, Built-in Werk OpenAI SIWC + OpenRouter + Local Endpoints, and thin coordinator wiring `WsSettingsAccount`) while keeping `dashboard-settings.js` integration clean.
- Implement the approved visual hierarchy from `ai-docs/assets/mockups/261128/account-panel-rework/index.html`:
  - Two borderless Material tonal group cards (`① External CLI Harness Accounts` and `② Built-in Werk Runtime & API`) with 1-line purpose subtitles.
  - 36px indented Level-1 provider blocks with monogram/icon, concise status summary, and primary actions.
  - Level-2 indented child containers with a `2px` muted tonal vertical stem (`--stem`) holding saved slots/accounts/endpoints, inline recessed drawers (OAuth copy-link + paste-code, Device-code `verify_url` + `user_code`, Add existing folder, OpenRouter key, Local Endpoint editor, AgY login), and OpenAI SIWC's `background_consent` recessed sub-setting row (disabled when no OpenAI SIWC account is linked).
  - Non-default Codex/Claude slot inline rename (`Enter` to save via `POST /api/account/slots/rename`, `Esc` to cancel, validation error display; `Default` slot not renameable/deletable).
  - OpenRouter clickable status chip linking directly to `Settings → Voice` when shared OpenRouter voice mode is active.
  - 1.5s hold-to-confirm destructive actions (cancelling on pointer leave, `Esc`, blur, visibility change; supporting keyboard `Space`/`Enter`), low-noise hover/focus row actions, light/dark theme parity, and clean responsive behavior below `760px`.
  - Preserve open inline editors/drawers across background `account-changed` SSE refreshes (minimal DOM updates per `performance.md`).

#### 4. Localization & Verification
- Update all 11 locale tables under `agents-plugin-tool/internal/wsi18n/tables/*.json` with exact key parity and zero duplicate keys. New keys cover: the `계정 · 인증` sidebar group label, the Voice section label and note, the two Account purpose-group titles and subtitles, the rename affordance and its validation errors, and the OpenRouter chip and Voice back-link text; no such string is hard-coded in JS.
- Verification commands:
  - `go test -race ./internal/wscred/... ./internal/wsdashboard/... ./internal/wsi18n/...`
  - `GOOS=windows go vet ./...`
  - `go build -o /dev/null ./cmd/ws-mcp`
  - `node --check` on all new/modified dashboard JS assets
  - `node --test` on existing and new settings/voice/account JS unit tests, placed alongside the existing dashboard asset tests under `internal/wsdashboard/` (covering section routing, legacy `?section=account&provider=voice` normalization, inline slot rename, OpenAI consent disabled state, hold-to-confirm cancellation, and preservation of `261125` Voice Providers behavior)
  - Headless Chromium live/DOM render check: capture `Settings → Account` in both Light and Dark themes (desktop `1280px` and narrow `<760px`) and compare each result capture side by side against a capture of the approved mockup `ai-docs/assets/mockups/261128/account-panel-rework/index.html` in the same theme and width. Capture `Settings → Voice` in the same four configurations and compare it side by side against the Voice Providers mockup recorded by `261125` Phase 4 (not this mockup's placeholder Voice body); in this mockup compare only the sidebar nav grouping and the OpenRouter chip/back-link. Verify 0 console errors and 0 horizontal overflow throughout.

### Result (dcd23278) - 2026-11-28

#### What Landed
- **Backend (`617ecf14`):**
  - `agents-plugin-tool/internal/wscred/account_slots.go` (+ `account_slots_test.go`): enforced 1–80 trimmed rune bound (`maxAccountSlotLabelRunes = 80`) in `AddSlot` and `UpdateSlotLabel`, returning `ErrInvalidAccountSlot` on violation while keeping `DefaultAccountSlotID` immutable (`ErrDefaultSlotImmutable`).
  - `agents-plugin-tool/internal/wsdashboard/account_routes.go`, `account_slots_routes.go` (+ `account_slots_routes_test.go`): added T0 additive `POST /api/account/slots/rename` under `localViewer` + `POST` + `sameOrigin`/`crossSiteFetch` + 72 KiB `DisallowUnknownFields` guards; rejects unsupported harness or empty `slot_id` with `400 invalid_request`, invokes `UpdateSlotLabel` + `d.accountChanged()`, and returns `200` JSON `AccountSlot` via `ResolveSlot`.
  - `agents-plugin-tool/internal/wsdashboard/account_slot_signin.go`: aligned sign-in start label validation to the same trimmed 80-rune bound.
- **Settings Navigation, Voice Section Split & Account Panel Rework (`9831d2c9`, `dcd23278`):**
  - `agents-plugin-tool/internal/wsdashboard/pages.go`: registered the split account JS modules in `SettingsScript` dependency order.
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings.js` & `dashboard-settings.css`: registered `"voice"` section, added `.snav` `계정 · 인증` (`js.settings.nav.account_auth`) group pairing `account` and `voice` (gating `voice` on `WsSettingsVoice`), normalized legacy `?section=account&provider=voice` at startup and in `selectSection`, wired Account/Voice cross-links, and cleaned up both `voice.destroy()` and `account.destroy()` on `pagehide`.
  - `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings-voice.js` & `.css`, `dashboard-voice-ptt.js`: mounted Voice Providers independently in `Settings → Voice`, added the `Manage shared key in Account →` back-link chip (`onOpenAccount`), exposed `isSharedOpenRouterKey()`, fixed sequential `%s`/`%d` placeholder substitution in local `t()`, and updated `#cvox` unconfigured click deep-link to `/settings?section=voice`.
  - Split `dashboard-settings-account.js` (previously 1,446 lines) into 4 cohesive modules under 1,000 lines: `dashboard-settings-account.js` (160L), `dashboard-settings-account-shared.js` (234L), `dashboard-settings-account-harness.js` (623L), `dashboard-settings-account-runtime.js` (776L), plus `dashboard-settings-account.css` (487L) implementing borderless Material tonal group cards (`① External CLI Harness Accounts`, `② Built-in Werk Runtime & API`), 36px provider indentation, `2px` `--stem` Level-2 containers, inline non-default slot rename (`Enter` save / `Esc` cancel / focus return), OpenAI SIWC `background_consent` recessed sub-setting (disabled when signed out), OpenRouter→Voice clickable chip, 1.5s hold-to-confirm with full cancellation, SSE preservation of open drawers/editors, responsive `<760px` layout, and zero `:has()`.
  - `agents-plugin-tool/internal/wsi18n/tables/*.json`: updated all 11 locale tables (+26 new keys, -3 unused keys; exact 4,133-key parity, 0 duplicates).
  - Tests: added `frontend-settings-account-rework.test.js` (654L), updated `frontend-voice-phase5.test.js` and `testdata/account-settings.browser.cjs`.
- **Compatibility & File-Size Audit:**
  - Compatibility Tier: **T0 (transparent)** — `account-slots.json` schema unchanged (`accountSlotsSchema = 1`), `POST /api/account/slots/rename` strictly additive, existing `/api/account/*` routes and envelopes untouched.
  - Touched non-i18n files exceeding 1,000 lines: `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings.js` (1,896 lines, pre-existing) and `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings.css` (1,217 lines, pre-existing). All newly split account/voice modules are under 1,000 lines.

#### Verification
- `go test -race -count=1 ./internal/wscred/... ./internal/wsi18n/...`: PASS (`wscred` 5.28s, `wsi18n` 49.01s).
- `go test -race -run 'TestAccount|TestSettings|TestVoice|TestPage' ./internal/wsdashboard`: PASS (10.33s).
- 8-shard parallel compiled `-race` run across all 1,820 top-level tests in `./internal/wsdashboard`: 1,812/1,820 PASS (0 data races; 0 failures in any touched surface). The 8 failures are pre-existing unrelated `develop` failures (`TestConversationPresentationMergePreservesIdentityAndDisclosures`, `TestFreshSessionLoadClient`, `TestTrackerDrawerOverlay`, `TestFilesExplorerInstances`, `TestBoardKeepWiring`, `TestBoardGallery`, `TestBoardToolbarPlacementAndCounts`, `TestGalleryNoReservedColumnWithoutAPage`).
- `GOOS=windows go vet ./...` and `go build -o /dev/null ./cmd/ws-mcp`: PASS.
- `node --check` on all 7 new/modified dashboard JS files: PASS.
- `node --test internal/wsdashboard/frontend-settings.test.js internal/wsdashboard/frontend-settings-*.test.js internal/wsdashboard/frontend-voice-*.test.js`: 52/52 PASS.
- `TestAccountSettingsBrowser` (headless Chromium via Playwright): PASS (20.01s; 0 console errors, 0 page errors, 0 horizontal overflow across 1280px/390px and Dark/Light).
- `python3 scripts/generate-package-assets.py --check`: PASS.
- `python3 -m unittest discover -s agents-plugin-wsflow/tests -v`: PASS (22 tests, 2 Windows-only skipped).
- 11-locale parity & JS key audit: 11 locales, 4,133 keys each, 0 duplicates, 0 missing JS keys across all 7 touched JS files.
- `go test -v -run '^TestCommonCodeHasNoHarnessNameBranch$' ./...`: 0 new failures (only pre-existing `develop` failures in `cmd/ws-mcp/companion_runtime.go` and `internal/wsdashboard/plan_usage*.go`).
- Side-by-side visual comparisons (screenshots under `/Users/kang-sw/.gemini/antigravity-cli/brain/d80995db-cbb5-43e9-b46e-c204a20353b2/scratch/shots/`):
  - compared `account-1280-dark.png` vs `mockup-account-1280-dark.png` (+ `account-runtime-1280-dark.png` vs `mockup-account-runtime-1280-dark.png`): match
  - compared `account-1280-light.png` vs `mockup-account-1280-light.png` (+ `account-runtime-1280-light.png` vs `mockup-account-runtime-1280-light.png`): match
  - compared `account-390-dark.png` vs `mockup-account-390-dark.png`: match
  - compared `account-390-light.png` vs `mockup-account-390-light.png`: match
  - compared `voice-1280-dark.png` vs `mockup-voice-1280-dark.png`: match
  - compared `voice-1280-light.png` vs `mockup-voice-1280-light.png`: match
  - compared `voice-390-dark.png` vs `mockup-voice-390-dark.png`: match
  - compared `voice-390-light.png` vs `mockup-voice-390-light.png`: match

#### Review & Decisions
- `code-review-lite` (`/Users/kang-sw/.cache/ws@kang-sw-devenv/proj/17da6bdc@e5bc9667/review-paths/8baf1e23-01-findings.md`): `clean with 3 minor remaining` (0 Critical, 0 Important). Fixed 2 Minor items in `dcd23278` (call `account.destroy()` on `pagehide` in `dashboard-settings.js`; assert focus return to `[data-slot-rename]` after save and `Escape` in `frontend-settings-account-rework.test.js`); remaining Minor item (regex-based source checks for `dashboard-settings.js` section wiring alongside live Chromium execution in `account-settings.browser.cjs`) noted.
- Fixed pre-existing `%s` literal placeholder rendering in `dashboard-settings-voice.js` local `t()` so masked OpenRouter keys render properly (`...9f3c`), and mocked `/tracker/stores` + configured `dashboard.voice_backend_order = "native openrouter"` in `testdata/account-settings.browser.cjs` capture fixture so Chromium tests run with 0 console 404s and full 2-segment Powerline priority bar parity.
