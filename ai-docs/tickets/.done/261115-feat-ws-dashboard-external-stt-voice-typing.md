---
title: "외부 전사 프로바이더를 통한 dashboard 음성 입력"
parent: 261125-epic-ws-voice-chat
related:
  261125-feat-ws-desktop-native-os-stt: follow-up OS native STT backend behind the same voice input interface
  261125-feat-ws-meeting-minutes-voice-mode: consumer of shared microphone permissions, wscred API-key providers, and voice capture foundation
sage-review-design: completed
sage-review-completeness: completed
sage-review-design-reviewed: b54ccb65ecca0724
sage-review-completeness-reviewed: b54ccb65ecca0724
completed: 2026-11-25
---

# Dashboard voice typing through an external transcription provider

## Background

Add push-to-talk transcription at the dashboard composer through a daemon-mediated external provider, behind a clean **common voice input interface** so OS native STT (`261125-feat-ws-desktop-native-os-stt`) and Meeting Minutes Mode (`261125-feat-ws-meeting-minutes-voice-mode`) can plug into the same frontend/daemon contract.

This updates the original 2026-11-15 capture (owner approvals `(872)=a`, `(877)=a`, `(878)=b`, `(885)=c`) to align with:
- Owner decision `(04)=a` (2026-11-25): ship the common voice input interface + external STT first, with OS native STT as a separate child ticket behind the same interface.
- Desktop shell transition: Werkschaft retired the Electron shell and now ships a **Tauri v2** shell (`agents-plugin-tool/desktop/`).

## Decisions

- **Common voice input interface (`04=a`, 2026-11-25):** Define a provider-agnostic frontend voice input controller (`WsVoiceInput`) and daemon voice-backend contract (capability discovery, start/stop capture, partial-text stream event, final transcript commit, error/retry) so Track 1a (external STT) and Track 1b (OS native STT via Tauri bridge) share the exact same composer caret-capsule UX and settings selector. The contract shape is pinned here because the native STT and Meeting Minutes tickets bind to it:
  - `GET /api/voice/capabilities` returns `{backends:[{id, label, kind:"external"|"native", available, reason?, models:[{id, streaming, language_field}]}], selected}` and never returns key material. `language_field` is `"language"` or `"languages"`.
  - `POST /api/voice/transcribe` takes multipart `audio`, `backend`, `model`, `language?`. It answers `application/json` `{text}` for final-only models, or `text/event-stream` with normalized `transcript.text.delta` (`{delta}`), `transcript.text.done` (`{text}`) and `error` (`{code}`) events for streaming models.
  - Frontend `WsVoiceInput` keeps a registry of backends `{id, available(), start(ctx), stop() -> Promise<{text}>, cancel(), onPartial(cb), retry?()}` and a controller state machine `idle | recording | transcribing | error`. The native STT ticket registers a `kind:"native"` backend with the same shape; Meeting Minutes reuses only capability discovery and the mic permission.
- **External STT transport & endpoint (`(872)=a`):** Use one OpenAI-compatible multipart transcription client: `POST {base}/audio/transcriptions`, with an audio file, model selection and language selection. Apply the provider/model-specific fields and streaming boundaries documented below; shared endpoint shape does not imply identical capabilities.
- **Daemon-mediated credentials (`wscred`):** Keep provider API keys in `wscred` (`<cacheRoot>/dashboard/auth/credentials.json`, owner-only permissions). Reuse existing `openrouter` (`KindAPIKey`) and custom local endpoints (`ItemWerkEndpoints`), and add `KindAPIKey` provider entries for OpenAI Platform (`openai_api`, distinct from `ProviderOpenAI` ChatGPT Plan OAuth `KindSIWC`) and Groq (`groq`) in `wscred`. The settings API returns masked values only and never exposes keys to LAN viewers. Audio travels `renderer -> local daemon -> provider`. Browser-direct calls and CORS workarounds are rejected.
- **Push-to-talk capture & codec negotiation:** Record with `MediaRecorder` preferring `audio/webm;codecs=opus` with safe fallback negotiation for `WKWebView` / Safari (e.g., `audio/mp4` or `AudioWorklet` WAV/PCM16 if `MediaRecorder` lacks webm/opus support), matching filename extension to MIME content type on upload. Stopping recording uploads the audio and inserts the completed transcription at the composer caret.
- **Caret-anchored dim pulsing capsule & streaming preview (`(877)=a`, `(885)=c`):** Anchor a dim, pulsing capsule at the composer caret and **lock composer interaction** while recording/transcribing is active. Supported OpenAI streaming models (`stream=true`, SSE `transcript.text.delta` / `transcript.text.done`) render partial text inside the capsule; OpenRouter, Groq, and non-streaming endpoints show the pulsing capsule without partial text. Commit only the completed transcript at the caret; never commit partial text. Always clean up the lock and capsule on both success and failure so the composer cannot become stuck.
  - *Rejected:* preserving concurrent composer edits around an unlocked preview or clearing it on edit and inserting elsewhere.
- **Memory-only last-recording retry (`(878)=b`):** Keep only the last recording in memory for a post-failure resend button. Never write audio to disk; discard on a new recording or window close.
- **Hidden-window lifecycle (`(11)=a`, 2026-11-25):** Composer voice typing is a foreground interaction. On window hide, minimize or tray (the webview survives X-close under `261122-feat-werkschaft-tray-notifications-full-quit`, so no unload fires), an active recording is cancelled and the mic released, the capsule and composer lock are dropped, and the in-memory last recording is discarded. This keeps the hidden-window idle-CPU budget of `261123-bug-ws-dashboard-webview-idle-cpu`. Meeting Minutes' hidden-window behavior is decided by its own ticket and epic `261125-epic-ws-voice-chat` (10).
  - The hide signal is the same one that drives `syncPageVisibility` and `data-animation-paused` in the dashboard assets (search those names), not a bare `visibilitychange` listener, because whether WebView2 reports `document.hidden` on minimize or tray is unconfirmed in `261123-bug-ws-dashboard-webview-idle-cpu` (its fallback is a shell-relayed window event). Embedded `?frame=1` composers receive the same signal.
  - *Rejected:* continuing composer capture while the window is hidden.
- **Secure origin & Tauri v2 microphone permissions:**
  - The voice capability-discovery response is the single gate for showing the microphone control. The voice routes follow the `/api/account/*` guard (loopback peer and Host, dashboard viewer cookie, dashboard Origin on mutations), so LAN viewers and any non-loopback origin get no capability and no mic control. There is no standalone `https://` clause.
  - **Tauri v2 macOS (`agents-plugin-tool/desktop/`):** `tauri.conf.json` has no macOS Info.plist or entitlements configuration today. Supply `NSMicrophoneUsageDescription` through the Tauri v2 bundler's Info.plist merge (confirm the exact mechanism against the Tauri v2 schema at implementation; do not assume a `bundle.macOS.infoPlist` key) and add the `com.apple.security.device.audio-input` entitlement unconditionally, since signed hardened-runtime releases silently deny the mic without it. Add a pinned-origin `WKUIDelegate` media-capture permission handler (`webView:requestMediaCapturePermissionForOrigin:initiatedByFrame:type:decisionHandler:`) in `desktop/src/macos/` that grants microphone access strictly for the pinned local daemon origin and denies all other origins. `macos/webview_delegate.rs` is the navigation delegate only and no `WKUIDelegate` is installed today, so the new handler must forward to or wrap wry's own UI delegate the way `NavigationDelegate` keeps `original`. Verify the purpose string on the built bundle (extend the existing `plutil` Info.plist check in `desktop/scripts/package.cjs`), because `cargo test` does not see the bundle.
  - **Tauri v2 Windows (`agents-plugin-tool/desktop/src/windows.rs`):** Extend `add_PermissionRequested` (which currently gates `CLIPBOARD_READ`) to handle `COREWEBVIEW2_PERMISSION_KIND_MICROPHONE`: allow only when user-initiated on the pinned `Destination::Dashboard` origin with `SetSavesInProfile(false)`, denying all other origins.
  - Preserve `withGlobalTauri: false` and `"no-native-authority"` renderer isolation.
- **Voice-only provider keys, fail closed:** The new `openai_api` and `groq` registry entries are voice-only. They are not served to agent sessions through `wscred.ServeToken` (which today refuses only `ProviderOpenAI`), not admitted as a werk lead provider through `WerkBackend.werkProvider` in `wsagent/werk.go`, and not listed as agent credentials by `cmd/ws-mcp/credential_cli.go`. `/api/account/status` reports them in their own voice-key group instead of `LocalKeys`, which today holds custom local endpoints. Users enter and clear these keys through a dashboard voice settings control that reuses the masked `/api/account/*` key-write path. Regression tests cover each refusal.
- **Capture gesture and single recorder:** `getUserMedia` runs synchronously inside the push-to-talk user gesture, before any async capability fetch, so user activation does not lapse (the Windows handler allows only `IsUserInitiated` requests). Exactly one recorder is active per microphone across all composers in the page, including embedded `?frame=1` composers; a second start is refused while one is active.
- **Settings keys and defaults:** The dashboard manifest gains `dashboard.voice_backend`, `dashboard.voice_model` and `dashboard.voice_language`, following the existing `dashboard.*` keys. `voice_backend` defaults to unset; with no selected and usable backend (no configured key, or no available native backend) the capability response reports none and the mic control is hidden. `voice_model` defaults to the selected provider's documented default model and `voice_language` to auto-detect (omitted).
- **Compatibility tier: T0 additive** for new daemon routes, `wscred` API-key provider entries, settings manifest keys, and dashboard UI behavior. No existing API, protocol, or store lifecycle is replaced.

### Provider evidence and field boundaries

Verified official-doc research (2026-11-15, `developers.openai.com`, `console.groq.com`, `openrouter.ai`):

- **OpenAI:** Bearer-authenticated multipart `POST https://api.openai.com/v1/audio/transcriptions` with `file` and `model` [O1]. Models: `gpt-transcribe`, `gpt-4o-transcribe`, `gpt-4o-mini-transcribe`, `whisper-1`, `gpt-4o-transcribe-diarize` [O1][O2] (`gpt-transcribe` recommended; legacy models deprecated with 2027-02-26 shutdown).
  - `gpt-transcribe` takes the `languages` array instead of `language` (never send both) [O2]; other OpenAI models take a single `language` [O1][O2].
  - `stream=true` returns SSE (`data: {json}` with `transcript.text.delta` carrying `delta` and `transcript.text.done` carrying final `text`) on `gpt-transcribe`, `gpt-4o-transcribe`, `gpt-4o-mini-transcribe`, and `gpt-4o-transcribe-diarize`; `whisper-1` ignores `stream` [O1][O2].
  - 25 MB max upload; filename extension must match MIME content type [O1].
- **Groq:** Bearer-authenticated multipart `POST https://api.groq.com/openai/v1/audio/transcriptions` (`whisper-large-v3-turbo`, `whisper-large-v3`) [G1][G2]. Single ISO-639-1 `language`; `response_format` `json`/`text`/`verbose_json`; final-only (no `stream` field); 25 MB free / 100 MB dev.
- **OpenRouter:** Bearer-authenticated multipart `POST https://openrouter.ai/api/v1/audio/transcriptions` [R1][R2]. Single `language` (auto-detect when omitted); final-only single JSON (`response_format` `json` or `verbose_json` only — `text`/`srt`/`vtt` return HTTP 400); `prompt` ignored; 25 MB limit; 60s upstream timeout.
- **Custom local endpoints (`ItemWerkEndpoints`):** OpenAI-compatible `POST {base}/audio/transcriptions` (final-only unless explicitly streaming-capable).

[O1]: https://developers.openai.com/api/reference/resources/audio/subresources/transcriptions/methods/create
[O2]: https://developers.openai.com/api/docs/guides/speech-to-text
[G1]: https://console.groq.com/docs/speech-to-text
[G2]: https://console.groq.com/docs/api-reference
[R1]: https://openrouter.ai/docs/api/api-reference/stt/create-transcription.md
[R2]: https://openrouter.ai/docs/guides/overview/multimodal/stt.md

### Implementation-time technical verification

Settle during implementation via unit fixtures / documented fallbacks (and obtain explicit owner approval before any live paid external API probe):
- Recorder codec/MIME fallback matrix across Chromium, Safari/WKWebView (`audio/mp4` vs `audio/webm` vs PCM WAV), ensuring uploaded extension matches `Content-Type`.
- Exact OpenAI SSE framing (`[DONE]` sentinel handling) and multipart encoding of `languages` array for `gpt-transcribe`.
- Graceful timeout/error handling (including OpenRouter 60s upstream timeout) surfacing the in-memory resend button cleanly.

## Constraints

- **Applicable manuals (from `AGENTS.md` `### Implementation Conventions`):**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/release-compatibility.md` (T0 additive)
  - `ai-docs/manuals/performance.md` (zero audio disk writes; compositor-only pulse animation using `opacity`/`transform`; idle screens frame-free; `prefers-reduced-motion` honored)
  - `ai-docs/manuals/config-settings-ui.md` (keep settings catalog, manifests, `wsflow` mirrors, and i18n translations aligned)
  - `ai-docs/manuals/wsflow-mirroring.md` (if `dashboard-config-manifest.json` changes, mirror identically in `agents-plugin-wsflow/`)
  - `ai-docs/manuals/dashboard-style.md` (borderless tonal Material capsule & mic button, canonical `--wb-*` tokens, no `:has()` selectors)
  - `ai-docs/manuals/ws-mcp.md` (if MCP config registry touches settings)
- **Outside scope for this ticket:** OS native STT (`261125-feat-ws-desktop-native-os-stt`), Realtime WebSocket/WebRTC (`261125-research-ws-live-voice-gpt-realtime`), Meeting Minutes diarization (`261125-feat-ws-meeting-minutes-voice-mode`), >25 MB audio splitting, OS keychain integration, and unapproved paid API calls.
- **Strict Tauri v2 isolation:** never loosen `withGlobalTauri: false` or `"no-native-authority"`; restrict microphone permission grants strictly to the pinned daemon origin.
- Convention: `ai-docs/manuals/shipped-surface-boundary.md` (declared for `agents-plugin/`, `agents-plugin-wsflow/`, `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/wsflow-mirroring.md` (declared for `agents-plugin/rsrc/`, `agents-plugin/skills/`, `agents-plugin/hooks/`, `agents-plugin-wsflow/`)
- Convention: `ai-docs/manuals/ws-mcp.md` (declared for `agents-plugin-tool/internal/mcp/`)
- Convention: `ai-docs/manuals/release-compatibility.md` (declared for `agents-plugin-tool/internal/`)
- Convention: `ai-docs/manuals/performance.md` (declared for `agents-plugin-tool/`)
- Convention: `ai-docs/manuals/config-settings-ui.md` (declared for `agents-plugin-tool/internal/mcp/config_registry.go`, `agents-plugin/config-manifest.json`, `agents-plugin/agy-config-manifest.json`, `agents-plugin/dashboard-config-manifest.json`, `agents-plugin-tool/internal/wsdashboard/config_routes.go`, `agents-plugin-tool/internal/wsdashboard/config_proposals.go`, `agents-plugin-tool/internal/wsdashboard/assets/dashboard-settings*`)
- Convention: `ai-docs/manuals/dashboard-style.md` (declared for `agents-plugin-tool/internal/wsdashboard/assets/`, `agents-plugin-tool/internal/wswhiteboard/assets/`, `ai-docs/assets/mockups/`)

## Prior Decisions

- 261125-epic-ws-voice-chat (2026-11-25, Cross-Child Decisions): "Common voice input abstraction (04=a). Frontend capture/UX and backend transcription/diarization sit behind a provider-agnostic voice input contract" — bearing: supports
- 261125-feat-ws-desktop-native-os-stt (2026-11-25, Decisions): "Plug into WsVoiceInput (04=a): Expose OS native dictation as a selectable backend in the same settings/provider switcher and composer caret-capsule UX established by 261115" — bearing: constrains
- 261125-feat-ws-meeting-minutes-voice-mode (2026-11-25, Decisions): "Start with chunked batch diarization via OpenAI gpt-4o-transcribe-diarize over VAD-aligned 15-30s windows (reusing the wscred OpenAI Platform API key and Tauri/browser mic permissions from 261115)" — bearing: constrains
- 261122-feat-ws-provider-auth-layer (2026-11-25, Result): "The browser API uses the dashboard viewer cookie and requires a loopback peer and Host. Mutations also require the dashboard Origin. Responses carry masks and fixed error codes." — bearing: constrains
- 261125-feat-werkschaft-tauri-daemon-self-recovery (2026-11-25, commit): "Implemented all four Tauri daemon self-recovery layers ... with 109/109 Rust tests passing on Windows, 4/4 test-package.cjs passing, production CSP check passing" — bearing: constrains
- 061bce25 (2026-11-25, commit): "wsT(\"js.bgexec.\" + emptyKey) hid keys from internal/wsi18n/keys_test.go TestEveryEnglishKeyIsUsed; selecting full quoted literals restores static detection" — bearing: constrains
- 261123-bug-ws-dashboard-card-note-typing-lag (2026-11-24, commit): "in-place unchanged-anchor upsert() fast path and single-render anchor-change fallback in capsules.js, plus form.js grow() skip for hidden .wsc-backing textareas." — bearing: constrains
- 261116-bug-ws-dashboard-composer-autosize-forced-layout (2026-11-16, commit): "261116-bug-ws-dashboard-composer-autosize-forced-layout: Phase 1 merged; live typing/IME acceptance pending" — bearing: constrains

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | agents-plugin-tool/internal/wscred/, agents-plugin-tool/internal/wsdashboard/, agents-plugin-tool/internal/wsdashboard/assets/, agents-plugin-tool/internal/mcp/config_registry.go, agents-plugin-tool/desktop/tauri.conf.json, agents-plugin-tool/desktop/src/macos/, agents-plugin-tool/desktop/src/windows.rs, agents-plugin/dashboard-config-manifest.json, agents-plugin-wsflow/dashboard-config-manifest.json |
| scope.surface | cross-module | new wscred ProviderID entries (openai_api, groq), loopback HTTP routes for voice capability discovery and POST /api/voice/transcribe, dashboard-config-manifest.json keys, Tauri v2 macOS/Windows permission hooks, and frontend WsVoiceInput interface |
| scope.new_public_symbol | yes | wscred ProviderID constants for openai_api and groq, window.WsVoiceInput controller, and POST /api/voice/transcribe route |
| scope.new_type_contract | yes | WsVoiceInput provider-agnostic frontend controller and daemon voice capability/transcription HTTP+SSE contract consumed by 261125-feat-ws-desktop-native-os-stt and 261125-feat-ws-meeting-minutes-voice-mode |
| scope.test_surface | new-files | existing Go tests in internal/wscred, internal/wsdashboard, internal/mcp, internal/wsi18n, existing Node frontend-idle.test.js, existing Rust cargo tests in agents-plugin-tool/desktop/, plus new voice transcription Go/JS/Rust tests |
| complexity.reuse_points | confirmed | reuses wscred.Store and KindAPIKey registry (provider.go, store.go, local_endpoints.go), /api/account/* loopback/origin guards (account_routes.go), WebView2 add_PermissionRequested (windows.rs#L145-L178), WKWebView delegate wiring (macos/webview_delegate.rs), and WsCapsules (capsules.js#L441, dashboard-composer.js#L8) |
| complexity.side_effect_risk | high | touches credential storage and masking, external multipart/SSE HTTP proxying, native macOS WKUIDelegate and Windows WebView2 microphone permission hooks, and composer input locking across desktop and browser |
| risk.correctness | high | provider-specific multipart/SSE wire rules (OpenAI languages vs language and SSE delta/done vs OpenRouter/Groq final-only), MediaRecorder codec fallback across WKWebView/WebView2/browsers, and composer lock/capsule cleanup on all error paths |
| risk.fit | high | establishes the shared WsVoiceInput interface and daemon/permission foundation that 261125-feat-ws-desktop-native-os-stt and 261125-feat-ws-meeting-minutes-voice-mode build on |
| risk.test | moderate | requires mocked HTTP/SSE provider fixtures, Node DOM/state-machine and frontend-idle.test.js assertions, and cross-platform macOS/Windows Rust permission gating tests without live paid API calls |
| risk.security_or_contract | high | handles API keys in wscred, enforces loopback-only/LAN-hidden audio and key routes, gates native OS microphone permissions strictly to the pinned daemon origin while preserving withGlobalTauri: false and no-native-authority, and guarantees zero audio disk writes |

## Phases

### Phase 1: Common voice input interface, Tauri v2 mic permissions, and daemon-mediated external STT

Implement the full push-to-talk composer voice typing path over the shared voice input interface:

1. **Daemon `wscred` & transcription backend (`internal/wscred/`, `internal/wsdashboard/`, `internal/mcp/config_registry.go`):**
   - Add `openai_api` and `groq` `KindAPIKey` entries to `wscred` (masked in `/api/account/*`, loopback-only, unknown-field-preserving) alongside existing `openrouter` and custom local endpoints.
   - Add loopback-only daemon routes for voice capability discovery and `POST /api/voice/transcribe` (multipart upload -> provider-specific `POST {base}/audio/transcriptions` preserving OpenAI `gpt-transcribe` `languages` vs single `language` split, OpenAI SSE `stream=true` relay -> `transcript.text.delta`/`done`, and OpenRouter/Groq final-only JSON).
   - Wire dashboard settings catalog (`dashboard-config-manifest.json` + `agents-plugin-wsflow/` mirror + i18n locales) for `dashboard.voice_backend`, `dashboard.voice_model` and `dashboard.voice_language`, plus the masked voice-key entry control.
   - Apply the voice-only key fail-closed rules from Decisions (`ServeToken`, `werkProvider`, `credential_cli.go`, `/api/account/status` grouping).
   - **Done when:** the routes, key store, settings manifest with its wsflow mirror and i18n keys, and the fail-closed refusals all exist together, and the Go tests in item 4 pass against mock providers.

2. **Tauri v2 scoped microphone permissions (`agents-plugin-tool/desktop/`):**
   - macOS: add `NSMicrophoneUsageDescription` (via the Info.plist mechanism in Decisions) and the `com.apple.security.device.audio-input` entitlement, plus a pinned-origin `WKUIDelegate` media-capture permission handler denying non-daemon origins.
   - Windows: handle `COREWEBVIEW2_PERMISSION_KIND_MICROPHONE` in `desktop/src/windows.rs` (`add_PermissionRequested`) allowing only user-initiated grants on `Destination::Dashboard` with `SetSavesInProfile(false)`.
   - **Done when:** the origin-and-kind decision is a pure function covered by Rust unit tests on both platforms, the macOS delegate forwards non-microphone requests to wry's own UI delegate, and the macOS bundle carries the purpose string and audio-input entitlement.

3. **Frontend common voice input interface & composer UX (`internal/wsdashboard/assets/`):**
   - Implement `WsVoiceInput` interface supporting pluggable backends (external daemon STT now; Tauri OS native STT in `261125-feat-ws-desktop-native-os-stt`).
   - Add push-to-talk mic control on the composer (shown only when capability discovery reports a usable backend), codec-negotiated `MediaRecorder` capture, caret-anchored dim pulsing capsule (compositor-only animation, paused when idle / reduced motion), composer interaction lock while active, SSE partial preview for supported OpenAI models, final-only insertion at caret via `WsCapsules`-compatible edit path, and memory-only last-recording resend button on failure (discarded on new recording, window unload, or window hide/minimize/tray, where an active recording is also cancelled and the mic released).
   - **Done when:** the mic control, capsule, lock, partial preview, final insertion and resend all run through `WsVoiceInput` with no provider-specific branch in the composer, and the Node tests in item 4 pass.

4. **Verification:**
   - Go unit tests (`go test ./internal/wscred/... ./internal/wsdashboard/... ./internal/mcp/...`) with mock HTTP/SSE providers verifying `languages` vs `language`, OpenAI SSE delta/done streaming, OpenRouter/Groq final-only handling, masked key responses, non-loopback peer/Host/Origin rejection by the shared guard with an empty capability response, and zero disk writes of audio payloads.
   - Node JS unit tests (including `frontend-idle.test.js`) verifying `WsVoiceInput` state machine, caret capsule locking/unlocking on success and failure, partial vs final insertion, memory-only resend and discard, the mic control shown only when capability discovery reports a usable backend, and zero `:has()` regressions.
   - Tauri desktop Rust tests (`cargo test` in `agents-plugin-tool/desktop/`) verifying the pure macOS/Windows origin-and-kind permission decision (daemon origin + microphone granted; other origins, other kinds and non-user-initiated Windows requests denied). The WKWebView and WebView2 delegate wiring itself is not unit-testable; cover it by the pure decision function plus a manual packaged-app check. The `Info.plist` purpose string and the audio-input entitlement are verified on the built bundle by the `plutil` check in `desktop/scripts/package.cjs`, not by `cargo test`.
   - Frontend acceptance assertions: the capsule is anchored at the caret offset; the composer lock releases on success, provider error and abort; the resend button discards its audio on a new recording and on unload; a hide/minimize/tray (visibility change) cancels an active recording, releases the mic, drops the lock and discards the last recording; a second concurrent recorder start is refused.
   - Isolated fixture verification only (never port `29417`; obtain explicit owner approval before any live paid provider smoke call).

### Result (251cc857) - 2026-11-25

- **Landed:**
  - **Daemon external STT & voice-only credentials (`507890e8`):**
    - Added `wscred.ProviderOpenAIAPI` (`openai_api`) and `wscred.ProviderGroq` (`groq`) marked `VoiceOnly: true`, rejected by `wsagent` werk runtime and `ws-mcp` credential CLI while supported in Account API key CRUD with masked/never-echoed responses (`internal/wscred/provider.go`, `route.go`, `internal/wsagent/werk.go`, `cmd/ws-mcp/credential_cli.go`, `internal/wsdashboard/account_routes.go`).
    - Added daemon routes `GET /api/voice/capabilities` and `POST /api/voice/transcribe` under the existing loopback + Host + Origin viewer boundary (returning empty capabilities to non-loopback/unauthenticated viewers), supporting OpenAI Platform (`gpt-transcribe` `languages` vs `gpt-4o-*`/`whisper-1` `language` + SSE `transcript.text.delta`/`transcript.text.done` streaming), Groq (`whisper-large-v3-turbo`/`whisper-large-v3`), OpenRouter (`chat/completions` `input_audio`), and custom local OpenAI-compatible endpoints (`local/<name>`), with a 25 MiB in-memory cap and zero audio disk writes (`internal/wsdashboard/voice_routes.go`, `daemon.go`, `daemon_http.go`).
    - Added global settings `dashboard.voice_backend`, `dashboard.voice_model`, and `dashboard.voice_language` to `agents-plugin/dashboard-config-manifest.json` (mirrored to `agents-plugin-wsflow/dashboard-config-manifest.json`), `dashboard-settings-catalog.json`, `dashboard-settings-account.js` (grouped Voice keys section), and all 11 locale tables (`internal/wsi18n/tables/*.json`).
  - **Tauri v2 scoped desktop microphone permissions (`822dac8f`):**
    - Added pure cross-platform origin+kind policy functions and unit tests in `agents-plugin-tool/desktop/src/permissions.rs`.
    - Added `Info.plist` (`NSMicrophoneUsageDescription`), `entitlements.plist` (`com.apple.security.device.audio-input`), and `bundle.macOS` config in `tauri.conf.json` while keeping `withGlobalTauri: false` and `no-native-authority` untouched.
    - Wired `WKUIDelegate` (`macos/webview_delegate.rs`, `macos.rs`) and WebView2 `add_PermissionRequested` (`windows.rs`, `SetSavesInProfile(false)`, fail-closed `DENY`) through `permissions.rs`, and added macOS plist/entitlement verification checks to `desktop/scripts/package.cjs` and `test-package.cjs`.
  - **Common `WsVoiceInput` interface & composer UI (`01ac0567`, `251cc857`):**
    - Added provider-agnostic `window.WsVoiceInput` registry, controller, external daemon backend (synchronous `getUserMedia` inside click gesture, codec negotiation + PCM16 WAV fallback, SSE delta/done parser rejecting incomplete EOF streams with `stream_incomplete`, memory-only `lastAudio` resend/discard, cross-frame single-recorder lock on `window.top`), and composer integration (`dashboard-voice.js`, `dashboard-voice.css`, `dashboard.tmpl`, `sprite.svg`, `pages.go`, `dashboard-composer-delivery.js`, 13 `js.voice.*` keys across all 11 locales, `frontend-voice.test.js`, `frontend-idle.test.js`, `asset_source_test.go`, and updated `internal/mcp/testdata/settings-description-digests.json`).
- **Verification:**
  - `python3 agents-plugin-tool/scripts/generate-package-assets.py --check`: pass
  - `python3 -m unittest discover agents-plugin-wsflow/tests`: pass (22 tests, 2 skipped)
  - `node --test agents-plugin-tool/internal/wsdashboard/frontend-voice.test.js agents-plugin-tool/internal/wsdashboard/frontend-idle.test.js agents-plugin-tool/internal/wsdashboard/frontend-settings.test.js agents-plugin-tool/internal/wsdashboard/frontend-interaction.test.js agents-plugin-tool/desktop/scripts/test-package.cjs`: pass (52/52)
  - `PATH="$HOME/.cargo/bin:$PATH" cargo test --manifest-path agents-plugin-tool/desktop/Cargo.toml`: pass (129/129, 0 warnings)
  - `go -C agents-plugin-tool test ./internal/wscred/... ./internal/wsi18n/... ./internal/wspackage/... ./internal/wsagent/... ./cmd/ws-mcp/...`: pass
  - `go -C agents-plugin-tool test -run 'TestVoice|TestAccount|TestDashboard|TestPage|TestFrame|TestAsset|TestUserMessageComposerSendsBrowserOffset|TestComposerCarriesWebPicks|TestSelectionComposerDraftTextOnlyAndLostBytes' ./internal/wsdashboard`: pass (full `./internal/wsdashboard` hit two pre-existing failures on `develop` `0f306a07` unrelated to voice typing: `TestSessionPageOffersSystemOpenToLocalViewersOnly` from `dashboard-conversation.css:892` and `TestSelectionComposerSendSnapshotsAndCancel` `convoVisible is not defined`)
  - `go -C agents-plugin-tool test ./internal/mcp -timeout 45m`: full suite ran with only `TestSettingsDescriptionsMatchTheLocaleReviewFixture` failing on the 3 new manifest keys; after regenerating `settings-description-digests.json`, `go -C agents-plugin-tool test ./internal/mcp -run 'TestSettingsDescriptionsMatchTheLocaleReviewFixture|TestDashboardSettingsUICatalog' -count=1` passed.
  - `code-review-lite`: found 1 Critical (`discoverCapabilities` parsing daemon `selected` object) and 1 Important (incomplete SSE stream EOF committing partial text); both fixed and regression-tested in `251cc857`.
- **Decisions:**
  - Appended `dashboard-voice.js` after `dashboard-lifecycle.js` in `dashboardScriptFiles` so its standalone IIFE runs after `WsCapsules` attaches and stays outside `selection_composer_test.go`'s composer fragment slice.
  - Maintained T0 additive compatibility (new additive `/api/voice/*` routes, additive `dashboard.voice_*` settings keys, additive `VoiceOnly` providers).


## Resolution (2026-11-25)

Implemented Phase 1 end-to-end: daemon-mediated external STT (`/api/voice/capabilities`, `/api/voice/transcribe`) with voice-only `wscred` keys (`openai_api`, `groq`), OpenAI/Groq/OpenRouter/local endpoints, global `dashboard.voice_*` settings & 11-locale i18n, Tauri v2 scoped macOS/Windows microphone permissions, provider-agnostic `window.WsVoiceInput` + push-to-talk composer UI/capsule/lock/resend, full unit/integration test suites, and `code-review-lite` remediation.
