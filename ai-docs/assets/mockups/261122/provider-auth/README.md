# Provider auth: dashboard settings mockup (ticket 261122-feat-ws-provider-auth-layer, Phase 3)

Two files render the same mockup:

- `mockup.html` is a standalone page. Open it in a browser; `?theme=light`
  or `?theme=dark` picks the starting theme (dark by default).
- `board.html` is the whiteboard panel fragment, for `wb:panel src`. It has
  no doctype, `html`, `head`, or `body`, and no external resources. Every CSS
  rule is scoped under the `.pa-mk` wrapper, and the inline script works only
  inside that wrapper. Its tokens follow the panel frame's root `data-theme`,
  or the OS theme when the root has none. Its breakpoints are container
  queries, so it collapses by the panel's width, not the viewport's.

`mockup.html` contains `board.html` byte for byte between the
`<!-- board.html: begin -->` and `<!-- board.html: end -->` lines. Edit that
section and copy it out. This check prints nothing when the two match:

```sh
diff <(sed '1,/board.html: begin/d;/board.html: end/,$d' mockup.html) board.html
```

The dashed bar at the top switches states and is not part of the product.
Its theme buttons (페이지 따름 / 다크 / 라이트) set the wrapper's own theme. The
mockup reproduces the live `/settings` page from `dashboard.tmpl` and
`dashboard-settings.css`: the header with its target pill and precedence
(scope) strip, the search and filter bar, the section nav, `.srow` rows,
origin pills, `.btn`, `.seg`, `.tg`, and the 1120px and 820px breakpoints.

v4 adds a new settings section, **계정** (Account), next to
"에이전트 · 티어" in the nav. It holds the provider credentials (OpenRouter,
and OpenAI with the background-use consent toggle) and the structured editor
for local endpoints, which replaces the `agent.werk_endpoints` textarea row.
The section has no scope switcher (settlement 11). "에이전트 · 티어" keeps
only the tier picker, an abridged copy of the existing one. Where it needs a
provider, it links to 계정 (`계정에서 관리`). The dashboard-wide consent banner
links to the OpenAI row in 계정. Use the `설정 섹션` and `선택된 스코프` groups in
the bar to compare the two sections under each scope.

All visible strings are Korean. The implementation adds them as
`js.settings.*` i18n keys in every `wsi18n/tables/*.json` locale table, as
`ai-docs/manuals/config-settings-ui.md` requires. That includes the new
section's label and note. The rows are declared in
`dashboard-settings-catalog.json` (gap 12).

## States covered

| Group | States |
|---|---|
| Settings section | 계정: the header shows the fixed label `이 머신 전체`, with no precedence strip and no filter bar; the nav count is the number of items that need attention. 에이전트 · 티어: the live header with the precedence strip, the filter bar, and the tier picker |
| Selected scope | builtin, global, repo, project, session (synced is the live page's disabled "준비 중" node). In 에이전트 · 티어 the target pill, strip, and the repo, builtin, or project notice follow the scope. 계정 looks the same under every scope |
| OpenRouter | not set (`미설정`); key entry; prefix warning (key does not start with `sk-or-`, saving still allowed); verifying with OpenRouter (`확인 중`); rejected by OpenRouter (invalid key, nothing saved); format rejected by the daemon (422 from `validateKey`); daemon not answering (drain or restart, with retry); saved and verified (`OpenRouter에서 유효한 키로 확인했습니다`); saved but not verified (`저장됨 · 확인 못 함`, with the reason and `다시 확인`, which re-runs the check); key set (`키 설정됨 sk-or-...a1b2`); replace entry; remove with a 1.5 s hold-to-confirm |
| OpenAI (Sign in with ChatGPT) | signed out; viewed from another device (sign-in button disabled with a one-line hint); browser flow pending (spinner and cancel, 5-minute timeout); sign-in failed or timed out; signed in with the verified email; signed in without an email; plan usage not granted (sign in again to grant it); refresh refused, so sign-in is needed again; sign out with hold-to-confirm, which deletes the account record |
| Background consent | dashboard-wide request banner with 허용 / 허용 안 함 / close (×) and a link to the OpenAI row in 계정 (`계정에서 OpenAI 보기`, which opens 계정 from any section); not yet answered (banner closed); allowed; denied or revoked. The settled answer is a toggle in a sub-row under the OpenAI row. It is disabled, with its value kept, while the account needs sign-in again or plan usage is off |
| Local endpoints | none; list (name with `local/<name>`, base URL, kind, and the backend mask `...9f3c` for a key); add; edit with a changed URL (the key field is empty and warns that the stored key is deleted on save); validation errors (name pattern, URL scheme and query); delete with hold-to-confirm, which also deletes the stored key; read-only "저장 형식(JSON) 보기" |
| Tier links | every provider row in 계정 has `Werk 티어에서 … 모델 선택 →`. It opens 에이전트 · 티어, scrolls to the tier matrix, keeps the Werk tab selected, and highlights the tiers that use that provider |
| 계정 links | in 에이전트 · 티어, a note above the matrix links to 계정. A tier whose provider is not ready (no OpenRouter key; OpenAI signed out, plan usage off, or sign-in needed; endpoint `ollama-box` missing) gets a one-line warning under its model with `계정에서 관리 →`, which opens 계정 and highlights that row |
| Theme | dark and light, in both files |

Checked with headless Chromium (cached Playwright 1.60):

- Both files at 1280px and 390px, in dark and light, through every state of
  every group, once with 계정 open and once with 에이전트 · 티어 open.
- No console errors, and the page never scrolls sideways. The snav and tier
  table keep the live page's own horizontal scrollers.
- In every 계정 render, the precedence strip, the filter bar, and every scope
  button are absent, and the target pill reads `이 머신 전체` and contains no
  control. In every 에이전트 · 티어 render, the strip is shown, and neither a
  provider row, the endpoint list, nor the text `agent.werk_endpoints`
  appears.
- The interactions work: hold-to-remove, the save → verify → saved flow,
  closing the banner, a scope click in 에이전트 · 티어 (계정 still shows its
  fixed label afterwards), and the cross-section links (tier links from 계정,
  `계정에서 관리` from a tier row and from the note, and the banner link).
- `board.html` also loads inside a sandboxed `srcdoc` iframe with the
  whiteboard's sandbox flags (`allow-scripts allow-popups
  allow-popups-to-escape-sandbox`, `wswhiteboard/render.go:702`), at frame
  widths 1200px and 820px with a dark root and 390px with a light root. It
  follows the host theme and collapses at an 820px frame width.

Screenshots are not committed.

## Settled (lead, 2026-11-24)

Items 1 to 8 settled the first round of questions, and items 9 and 10 the
second. Items 11 and 12 record the owner's v4 decisions.

1. **Consent banner: 허용 / 허용 안 함 plus a close (×). No "나중에".**
   Closing hides the banner until the next refused background request raises
   it again. That leaves the answer unset rather than recording a refusal.
   It also keeps one exit that is not a decision, without adding a reminder
   timer.
2. **The banner is dashboard-wide and links to the OpenAI row.** The refusal
   happens outside `/settings` (a worker or a cron turn), so the prompt has
   to be visible wherever the user is. The link leads to the toggle where the
   answer can be changed later. The banner is inline rather than a modal:
   the refused session has already failed, so nothing is waiting, and an
   express-consent answer should be deliberate rather than a reflex to a
   blocking dialog.
3. **No account switcher in Phase 3.** The ticket's Phase 3 scope is sign-in
   and sign-out only, so the UI shows the single active account even though
   the backend can store several registrations (`openai_store.go:37-42`).
4. **Sign-out deletes the account record.** Tokens and the consent answer go
   together, because Decision 4 stores consent with the credential. No remote
   revoke is offered unless the backend gains one. The sign-out tooltip says
   that it deletes the account record and the background-use answer.
5. **The prefix mismatch stays a soft warning. Saving verifies the key with
   OpenRouter's key-info endpoint.** The UI shows valid or invalid, and no
   credit in Phase 3. A key that does not start with `sk-or-` may still be
   genuine, but a key OpenRouter refuses is never stored. Credit display is
   out of scope.
6. **A non-loopback viewer gets a disabled Sign in button and a one-line
   hint.** The loopback PKCE flow opens the browser on the daemon's machine,
   so starting it from another device would open a window nobody sees. The
   other sign-in buttons (the plan-disabled and re-sign-in states) are
   disabled the same way.
7. **Deleting a local endpoint also deletes its stored key.** This makes
   backend gap 7 an implementation item.
8. **The backend's mask is shown as-is** (`sk-or-...a1b2`, or `...9f3c` for
   an endpoint key without a prefix), consistent with `credential show`.
9. **When OpenRouter cannot be reached, the key is saved and marked
   unverified.** A network error, a 5xx, or a 429 from the key-info endpoint
   saves the key and shows `저장됨 · 확인 못 함` with a short reason
   (`연결 실패` for a network error, `일시 오류` for a 5xx or 429) and a
   `다시 확인` button. Only a definitive rejection (401 or 403) refuses to
   save, which is the existing `OpenRouter 거부` state. A moment offline or a
   rate limit must not block setup. The prefix check is soft anyway, and the
   first real call reports an auth failure regardless. A transient failure is
   not evidence that the key is bad.
10. **Changing an endpoint's URL deletes the old URL's key, as an announced
    default.** The key ID is the SHA-256 of the base URL, so the old key no
    longer applies. Keeping it would leave an orphan secret on disk, and
    carrying it over would send a secret to a host the user never entered it
    for. Once the URL differs from the stored one, the key field stays empty
    and says, before save, `URL이 바뀌면 저장된 키가 삭제됩니다. 새 URL용 키를
    다시 입력하세요.` Saving deletes the old key.
11. **Credentials and local endpoints live in their own 계정 section, and it
    is global only** (owner, v4; decision (53)=a). 계정 sits next to
    "에이전트 · 티어" in the settings nav. It holds OpenRouter, OpenAI with
    the background-use toggle, and the endpoint editor. "에이전트 · 티어" keeps
    only the tier picker and links to 계정 where a provider matters. 계정
    has no scope switcher, and nothing in it says it is per-project. The live
    header draws the target pill and the precedence strip
    (builtin → synced → global → repo → project → session) for every section
    (`dashboard.tmpl`, `renderHeader` in `dashboard-settings.js:266`). Of the
    two options, the mockup uses **the fixed label**. In 계정, the existing
    target pill reads `이 머신 전체` and holds no control. The strip and the
    search and filter bar are not drawn. The reasons:
    - The target pill is already the live page's non-interactive answer to
      "where does an edit go". 계정 keeps that answer with honest text.
      Hiding the pill as well would leave the one section without an answer
      and make the header change shape between sections.
    - The strip is removed, not disabled. Six disabled scope nodes would
      suggest that the values are layered and only locked here.
    - The filter bar goes too. Its filters (overridden in this scope,
      shadowed, editable) only make sense for layered keys.
    The scope selected in the URL stays as it was, so another section opens
    at the same scope. 계정 shows no project note and no repo or builtin
    banner, and it stays editable when the page is open at the built-in
    scope, where every other section is read-only.
12. **`agent.werk_endpoints` is hidden from agent tools** (owner, v4). It
    remains the stored config value, but only 계정 and the CLI edit it.
    config.list, config.get, config.tune, and lead-tune neither show it nor
    change it. No generic config row and nothing in "에이전트 · 티어" renders
    it. The read-only JSON disclosure in 계정 is the only place the raw value
    appears. The reasons:
    - Changing where worker traffic goes is an owner-level action.
    - Hiding the key does not close a leak. Endpoint keys are bound to the
      URL (settlement 10), so pointing an endpoint elsewhere never sends a
      stored key there. The point is who may decide, not secrecy.
    - The key is new in 0.52, so no existing setup or script loses anything.

## Design notes

- **Secrets.** Key inputs are `type=password` with `autocomplete=off`. They
  are always empty when opened, including on replace, and cleared after a
  successful save. While the key is being verified, the input and the save
  button are disabled. After a daemon-down failure, the typed value stays in
  the field for a retry. That value came from the user, never from the store.
- **The stored format is unchanged.** The endpoint list reads and writes the
  same `agent.werk_endpoints` JSON object (`{name: {base_url, kind}}`) at
  global scope through the dashboard's ordinary `/api/config/set` route.
  That is a session-authenticated browser route, not an agent tool, so
  settlement 12 does not affect it. The JSON disclosure shows exactly that
  value, which never contains keys. An endpoint key is stored separately in
  the credential store under `local/<name>`, bound to the base URL.
- **Where the agents section mentions a provider, it links to 계정.** A note
  above the tier matrix always links there. A tier row gets a warning and
  `계정에서 관리 →` only while its provider is not usable, so a healthy setup
  adds no noise to the existing picker. The warning does not mention
  background consent, because consent depends on who starts the session,
  not on the tier.
- **The 계정 nav count shows what needs attention** (no OpenRouter key, an
  OpenAI account that needs sign-in or plan approval, a pending consent
  request) in the warning tint. 계정 has no per-scope overrides, so the
  other sections' override count would always be empty there.
- **Hold-to-confirm.** Remove, sign-out, and endpoint delete use the
  dashboard's `WsHoldConfirm` behavior: a radial fill on 28px icon buttons
  over 1.5 s, cancelled by Escape, pointer leave, blur, or a visibility
  change. A Space or Enter hold works too. Revoking consent is one click,
  because turning it off is the safe direction.
- No new visual language: only the live settings primitives are used, plus
  an `ok` tint on the origin pill for "set/signed in" and the
  primary-tinted banner.

## Backend facts the UI needs that do not exist yet

1. **No browser-facing credential routes.** The daemon registers only
   `/auth/provider/change`, gated by the endpoint-file token
   (`daemon_http.go:60`, `credential_routes.go:187`), and the harness token
   route. The page needs session-authenticated routes for:
   - status (`Store.Status`, `store.go:404`)
   - set and remove (`Store.Apply`, `store.go:437`)
   - consent (`SetOpenAIBackgroundConsent`, `openai_store.go:180`)
2. **No dashboard sign-in route with progress and cancel.**
   `Store.SignInOpenAI` blocks for up to 5 minutes (`openai_store.go:102`,
   `openai_oauth.go:32`). It is reachable only through the offline CLI path
   (`credential_routes.go:88-111`). The pending, cancel, and failed states
   need three things: a start call, a status poll or live-feed event, and
   cancellation through the context.
3. **The status route must say whether the viewer is on loopback**
   (settlement 6). The daemon can judge the request's peer with the existing
   `loopbackPeer` (`access.go:227`), which the token route already uses
   (`credential_routes.go:136`).
4. **No sign-out** (settlement 4). `Store.Apply` refuses non-API-key
   providers (`store.go:444`), and nothing deletes an OpenAI account record.
   A sign-out must delete the record, consent included, and clear `Active`.
5. **No consent-request signal** (settlements 1 and 2). A background refusal
   is returned only as error text to the session (`werk.go:246`,
   `cron_seams.go:160`). Nothing records that a prompt is pending, or which
   session, tier, or model asked. The banner also needs a dashboard-wide
   event so it can appear on any view. Status does distinguish "never
   answered" (`nil`) from "denied" (`false`) through `BackgroundConsent *bool`
   (`openai_store.go:72`).
6. **No key verification on save** (settlement 5). `validateKey` checks only
   for an empty key, the length, and the character set (`store.go:558`).
   `KeyPrefix` is used only for masking (`provider.go:71`, `provider.go:97`).
   The daemon, not the browser, should call OpenRouter's key-info endpoint
   (`/api/v1/key`). It refuses to store only on 401 or 403 and stores the key
   unverified on a network error, 5xx, or 429 (settlement 9). The stored
   record needs a verified flag and timestamp, and the status route must
   report `verified`, `unverified` (with the reason), or, for the save
   response, `rejected` (never stored). A re-check call for a stored key is
   needed for `다시 확인`.
7. **Endpoint keys are bound to the URL and are never cleaned up**
   (settlements 7 and 10, now implementation items). `Provider.keyID` hashes the
   base URL (`local_endpoints.go:69`). Deleting an endpoint, renaming it, or
   changing its URL in config must also remove the old key entry. Nothing
   does that today.
8. **No plan label.** `OpenAIAccountStatus` (`openai_store.go:66-73`) has no
   plan name. The email is kept only when verified (`openai_store.go:117`),
   hence the "로그인됨" state without an email.
9. **Plan pause is not in Status.** The usage-limit pause lives only in
   daemon memory (`store.go:141`, `openai_plan.go:45-50`). The mockup has no
   "paused" state for it.
10. **Error texts still point at the CLI.** `credential.go:97`,
    `credential.go:116`, `credential.go:118` and `werk.go:146` say to run
    `credential set` or `credential sign-in`. They should mention the
    dashboard's 계정 section, as `werk.go:220` already mentions the dashboard.
11. **Registry: `agent.werk_endpoints` must be hidden from agent tools**
    (settlement 12). Its registry entry (`config_registry.go:434`) is an
    ordinary `config.tune` knob with `RequiresLeadAuthority`. No flag means
    "not an agent knob" today. `NoAgentVisible` only decides what the
    agentless wsflow catalog keeps (`config_registry.go:65`,
    `server_config.go:305`). The entry needs a new flag that:
    - removes the key from config.list, config.get, and the lead-tune
      catalog;
    - makes config.tune refuse to write or reset it.
    The dashboard reads the key through the `wsconfigservice` projection.
    That projection must keep it, because the 계정 editor and the catalog
    drift test (`dashboard_settings_ui_catalog_test.go`) both use it. The
    tier provider field's description (`config_registry.go:651`) tells
    agents to pick `local/<name> from agent.werk_endpoints`. It should name
    the dashboard's 계정 section instead of the hidden key.
12. **Dashboard settings: the catalog needs a new `account` section.**
    - Add `account` to the allowed `section` values in
      `ai-docs/manuals/config-settings-ui.md`.
    - Add it to `sections` in `dashboard-settings.js:24`, after `agents`, and
      add its label and section note to the i18n tables in every locale
      (ko: `계정`).
    - Move the `agent.werk_endpoints` row (`dashboard-settings-catalog.json:2`)
      from `section: "agents", control: "textarea"` to `section: "account"`
      with a new specialized control for the structured editor. The control
      list in the manual and the drift test gain it, and no generic row can
      render the value.
    - In `renderHeader` (`dashboard-settings.js:266`), when 계정 is active,
      hide `[data-settings-precedence]` and the filter bar, and give the
      target pill the fixed label.
    - In `renderContent` (`dashboard-settings.js:1428-1437`), skip the
      project note and the repo and builtin banners for 계정. The section
      always reads and writes at global scope, whatever scope the URL
      selects.
    - The credential rows are not config keys. They render from the
      credential status route (gap 1), not from catalog rows.
    - The 계정 nav count shows the items that need attention, not the
      per-scope override count.
13. **The CLI cannot edit endpoints yet.** Settlement 12 leaves 계정 and the
    CLI as the only writers, but `ws-mcp config` offers only `list` and the
    tier `tune` (`hosted_cli.go:268-300`), and `ws-mcp credential` handles
    only keys (`credential_cli.go:28`). Settled here as a technical choice:
    add `ws-mcp credential endpoint <set|remove> <name> [--base-url <url>
    --kind vllm|ollama]` under `credential`. The owner manages endpoints next
    to the keys, and the command must do the same URL-change and delete key
    cleanup as gap 7. `ws-mcp config list` is an owner-run CLI, so it may
    keep showing the value.

## Open questions

None. Both questions from the second round are settled (items 9 and 10).
The v4 scope presentation (fixed label, settlement 11) and the CLI command
shape (gap 13) were technical choices made during the revision. Change
either if the owner prefers otherwise.
