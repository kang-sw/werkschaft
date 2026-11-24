# Provider auth: dashboard settings mockup (ticket 261122-feat-ws-provider-auth-layer, Phase 3)

`mockup.html` is a self-contained page with inline CSS and JS and no external
resources. Open it directly in a browser. The dashed bar at the top switches
states and is not part of the product. `?theme=light` or the theme buttons
switch between the light and dark themes the live page supports. The mockup
reproduces the live `/settings` "에이전트 · 티어" section from
`dashboard-settings.css`: `.srow` rows, origin pills, `.btn`, `.seg`, `.tg`,
and the 1120px and 820px breakpoints. It adds one provider-credential group
and replaces the `agent.werk_endpoints` textarea row with a structured list.
The tier matrix below them is an abridged copy of the existing picker and is
unchanged.

All visible strings are Korean. The implementation adds them as
`js.settings.*` i18n keys in every `wsi18n/tables/*.json` locale table, as
`ai-docs/manuals/config-settings-ui.md` requires, and declares the rows in
`dashboard-settings-catalog.json`.

## States covered

Each group switches independently, so combinations can be reviewed together.

| Group | States |
|---|---|
| OpenRouter | not set (`미설정`); key entry; prefix warning (key does not start with `sk-or-`, saving is still allowed); daemon rejected the format (422 from `validateKey`); daemon not answering (drain or restart, with retry); key set (`키 설정됨 …a1b2`); replace entry; remove with a 1.5 s hold-to-confirm (the trash icon in the key-set state) |
| OpenAI (Sign in with ChatGPT) | signed out; browser flow pending (spinner and cancel, with a note that the browser opens on the daemon's machine and the flow times out after 5 minutes); sign-in failed or timed out; signed in with the verified email; signed in without an email; signed in but plan usage not granted (`요금제 사용 꺼짐`, sign in again to grant it); refresh refused, so sign-in is needed again; sign out with hold-to-confirm |
| Background consent | request banner (allow or deny); not yet answered; allowed; denied or revoked. The settled toggle sits in a sub-row under the OpenAI row. It is disabled, with its value kept, while the account needs sign-in again or plan usage is off |
| Local endpoints | none; list (name with `local/<name>`, base URL, kind, and key status showing at most the last 4 characters); add form; edit with a changed URL (warns that the old key stays bound to the old URL); validation errors (name pattern, URL scheme or query); remove with hold-to-confirm; read-only "저장 형식(JSON) 보기" |
| Tier links | every provider row has `Werk 티어에서 … 모델 선택 →`. It scrolls to the existing tier matrix, keeps the Werk tab selected, and highlights the tiers that use that provider |
| Theme | dark and light |

Checked with headless Chromium (cached Playwright 1.60) at 1280px and 390px
widths in both themes, for every state: no console errors and no horizontal
page overflow. The tier table keeps the live page's inner horizontal scroller
at narrow widths. Screenshots are not committed.

## Design choices

- **Consent prompt as an inline banner, not a modal.** By the time the prompt
  shows, the worker or cron turn has already failed with an actionable error
  (Decision 4, `werk.go:246`, `cron_seams.go:160`), so nothing is waiting on
  the answer. A modal would interrupt unrelated work and push the user toward
  a reflexive "allow". An express-consent answer should be deliberate, so the
  banner stays until it is answered and names the session, tier, model, and
  account involved. The implementation would put the same banner in the
  dashboard shell above every view, so it can be seen outside `/settings`
  too. The settled answer is then the toggle under the OpenAI row.
- **Secrets.** Key inputs are `type=password` with `autocomplete=off`. They
  are always empty when opened, including on replace. They are cleared after
  a successful save. The page shows a key only as `키 설정됨` with at most the
  last 4 characters (`…a1b2`). `wscred.Mask` returns `sk-or-...a1b2`, and the
  UI drops the prefix. After a failed save (daemon down), the typed value
  stays in the field so the user can retry. That value came from the user,
  not from the store.
- **The stored format is unchanged.** The endpoint list reads and writes the
  same `agent.werk_endpoints` JSON object (`{name: {base_url, kind}}`)
  through the ordinary `/api/config/set` route. The JSON disclosure shows
  exactly that value, which never contains keys. A key is stored separately
  in the credential store under `local/<name>`, bound to the base URL.
- **Hold-to-confirm.** Remove, sign-out, and endpoint delete use the
  dashboard's `WsHoldConfirm` behavior: a radial fill on 28px icon buttons
  over 1.5 s, cancelled by Escape, pointer leave, blur, or a visibility
  change. A Space or Enter hold works too. Revoking consent is one click,
  because turning it off is the safe direction.
- No new visual language: only the live settings primitives are used, plus
  an `ok` tint on the origin pill for "set/signed in".

## Backend facts the UI needs that do not exist yet

1. **No browser-facing credential routes.** The daemon registers only
   `/auth/provider/change`, gated by the endpoint-file token
   (`daemon_http.go:60`, `credential_routes.go:187`), and the harness token
   route. The page needs session-authenticated routes for:
   - status (wrapping `Store.Status`, `store.go:404`)
   - set and remove (`Store.Apply`, `store.go:437`)
   - consent (`SetOpenAIBackgroundConsent`, `openai_store.go:180`)
2. **No dashboard sign-in route with progress and cancel.**
   `Store.SignInOpenAI` blocks for up to 5 minutes (`openai_store.go:102`,
   `openai_oauth.go:32`). It is reachable only through the offline CLI path
   (`credential_routes.go:88-111`), whose comment says dashboard sign-in "uses
   the running daemon's Store directly", but no route does that. The pending,
   cancel, and failed states need three things: a start call, a status poll or
   live-feed event, and cancellation through the context.
3. **No sign-out.** `Store.Apply` refuses non-API-key providers
   (`store.go:444`), and nothing clears an OpenAI account's tokens.
4. **No plan label.** `OpenAIAccountStatus` (`openai_store.go:66-73`) holds
   only the client ID, the email, and the active, signed-in, plan-enabled, and
   consent flags. There is no plan name such as Plus or Pro. The email is
   kept only when verified (`openai_store.go:117`), which is why the mockup
   has a "로그인됨" state without an email.
5. **No consent-request signal.** A background refusal is returned only as
   error text to the session (`werk.go:246`, `cron_seams.go:160`). Nothing
   records that a prompt is pending, or which session, tier, or model asked,
   so the banner has nothing to show. Status does distinguish "never answered"
   (`nil`) from "denied" (`false`) through `BackgroundConsent *bool`
   (`openai_store.go:72`). That is enough to decide whether to ask, but not
   when to ask.
6. **Plan pause is not in Status.** The usage-limit pause lives only in
   daemon memory (`store.go:141`, `openai_plan.go:46-50`). The mockup has no
   "paused" state for it.
7. **Prefix is a client-side hint only.** `validateKey` checks only for an
   empty key, the length, and the character set (`store.go:558`).
   `KeyPrefix` is used only for masking (`provider.go:71`, `provider.go:97`).
   Nothing checks the key against OpenRouter (`/api/v1/key`) on save, so the
   only "rejected" state is a format rejection.
8. **Endpoint keys are bound to the URL and can be orphaned.**
   `Provider.keyID` hashes the base URL (`local_endpoints.go:69`). A changed
   URL shows "no key", which the edit warning covers. Removing or renaming an
   endpoint in config leaves its key entry in the credential file, and no
   cleanup exists.
9. **Error texts still point at the CLI.** `credential.go:97`,
   `credential.go:116`, `credential.go:118` and `werk.go:146` say to run
   `credential set` or `credential sign-in`. After Phase 3 they should
   mention the dashboard, as `werk.go:220` already does.

## Open questions for the owner

1. **The consent banner's "later" option.** The banner has no dismiss
   button: it stays until "허용" or "허용 안 함" is chosen. Should a "나중에"
   option exist? If so, should the next refused background session raise it
   again?
2. **Where the banner appears.** Should it be shell-wide (proposed), or
   limited to `/settings` plus an attention count on the rail?
3. **Multiple OpenAI accounts.** The backend keeps several registrations and
   an active one (`openai_store.go:37-42`). The mockup shows only the active
   account. Is an account switcher wanted in Phase 3, or later?
4. **Sign-out semantics.** Should it drop only the tokens and keep the
   registration (a returning sign-in reuses the client ID), or delete the
   account record including the consent answer? Should it also revoke the
   grant at OpenAI?
5. **Prefix warning.** Keep it soft, as now (saving is allowed), or block a
   key without `sk-or-` from being saved? Also, should saving verify the key
   against `/api/v1/key` and show credit or free-tier remaining?
6. **Remote dashboard viewers.** The browser flow opens on the daemon's
   machine. When the dashboard is viewed from another device, should the
   sign-in button be disabled, or should the pending note be the only signal?
7. **Endpoint delete.** Should deleting an endpoint also delete its stored
   key (needs the cleanup from gap 8)?
8. **Masked form.** Should the display show `…a1b2` (proposed) or the
   backend's `sk-or-...a1b2`?
