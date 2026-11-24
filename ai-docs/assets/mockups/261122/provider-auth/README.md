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
mockup reproduces the live `/settings` "에이전트 · 티어" section from
`dashboard-settings.css`: `.srow` rows, origin pills, `.btn`, `.seg`, `.tg`,
and the 1120px and 820px breakpoints. It adds a provider-credential group,
replaces the `agent.werk_endpoints` textarea row with a structured list, and
adds a dashboard-wide consent banner. The tier matrix below them is an
abridged copy of the existing picker and is unchanged.

All visible strings are Korean. The implementation adds them as
`js.settings.*` i18n keys in every `wsi18n/tables/*.json` locale table, as
`ai-docs/manuals/config-settings-ui.md` requires, and declares the rows in
`dashboard-settings-catalog.json`.

## States covered

| Group | States |
|---|---|
| OpenRouter | not set (`미설정`); key entry; prefix warning (key does not start with `sk-or-`, saving still allowed); verifying with OpenRouter (`확인 중`); rejected by OpenRouter (invalid key, nothing saved); format rejected by the daemon (422 from `validateKey`); daemon not answering (drain or restart, with retry); saved and verified (`OpenRouter에서 유효한 키로 확인했습니다`); key set (`키 설정됨 sk-or-...a1b2`); replace entry; remove with a 1.5 s hold-to-confirm |
| OpenAI (Sign in with ChatGPT) | signed out; viewed from another device (sign-in button disabled with a one-line hint); browser flow pending (spinner and cancel, 5-minute timeout); sign-in failed or timed out; signed in with the verified email; signed in without an email; plan usage not granted (sign in again to grant it); refresh refused, so sign-in is needed again; sign out with hold-to-confirm, which deletes the account record |
| Background consent | dashboard-wide request banner with 허용 / 허용 안 함 / close (×) and a link to the OpenAI row; not yet answered (banner closed); allowed; denied or revoked. The settled answer is a toggle in a sub-row under the OpenAI row. It is disabled, with its value kept, while the account needs sign-in again or plan usage is off |
| Local endpoints | none; list (name with `local/<name>`, base URL, kind, and the backend mask `...9f3c` for a key); add; edit with a changed URL (the old key is deleted on save); validation errors (name pattern, URL scheme and query); delete with hold-to-confirm, which also deletes the stored key; read-only "저장 형식(JSON) 보기" |
| Tier links | every provider row has `Werk 티어에서 … 모델 선택 →`. It scrolls to the existing tier matrix, keeps the Werk tab selected, and highlights the tiers that use that provider |
| Theme | dark and light, in both files |

Checked with headless Chromium (cached Playwright 1.60):

- Both files at 1280px and 390px, in dark and light, through every state.
- No console errors, and the page never scrolls sideways. The snav and tier
  table keep the live page's own horizontal scrollers.
- The interactions work: hold-to-remove, the save → verify → saved flow,
  closing the banner, and the banner and tier links.
- `board.html` also loads inside a sandboxed `srcdoc` iframe with the
  whiteboard's sandbox flags (`allow-scripts allow-popups
  allow-popups-to-escape-sandbox`, `wswhiteboard/render.go:702`) and a dark
  root. It follows the host theme and collapses at an 820px frame width.

Screenshots are not committed.

## Settled (lead, 2026-11-24)

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
   backend gap 8 an implementation item. The same reasoning covers a URL edit
   (derived here, see open questions): the key is bound to the old URL and can
   never be used again, so the mockup says it is deleted on save.
8. **The backend's mask is shown as-is** (`sk-or-...a1b2`, or `...9f3c` for
   an endpoint key without a prefix), consistent with `credential show`.

## Design notes

- **Secrets.** Key inputs are `type=password` with `autocomplete=off`. They
  are always empty when opened, including on replace, and cleared after a
  successful save. While the key is being verified, the input and the save
  button are disabled. After a daemon-down failure, the typed value stays in
  the field for a retry. That value came from the user, never from the store.
- **The stored format is unchanged.** The endpoint list reads and writes the
  same `agent.werk_endpoints` JSON object (`{name: {base_url, kind}}`)
  through the ordinary `/api/config/set` route. The JSON disclosure shows
  exactly that value, which never contains keys. An endpoint key is stored
  separately in the credential store under `local/<name>`, bound to the
  base URL.
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
   (`/api/v1/key`) and store the key only on success.
7. **Endpoint keys are bound to the URL and are never cleaned up**
   (settlement 7, now an implementation item). `Provider.keyID` hashes the
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
    dashboard, as `werk.go:220` already does.

## Open questions

1. **OpenRouter unreachable during verification** (network error, 5xx, 429).
   Should the daemon refuse to save and offer a retry, or save the key marked
   "not verified"? The mockup shows only the valid and invalid outcomes.
2. **Old key on a URL edit.** The mockup deletes the old URL's key on save,
   extending settlement 7. Please confirm, or keep the key until the
   endpoint is deleted.
