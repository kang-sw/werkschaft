---
title: "음성 채팅 Epic: 공통 음성 입력, OS 네이티브 STT, 라이브 리서치, 회의록 모드"
sage-review-design: completed
related:
  261114-epic-ws-common-session-foundation: planned voice extension consumer
sage-review-design-reviewed: 9873cf87fbd04df5
---

# Voice Chat Epic: Common Voice Input, OS Native STT, Live Voice Research, and Meeting Minutes Mode

## Scope

Introduce voice input and voice-driven workflows across the Werkschaft Tauri v2 desktop shell and browser dashboard over one shared voice input and credential foundation:

1. **Track 1a (First Implementation Target):** Common voice input interface + daemon-mediated external STT voice typing at the composer (`261115-feat-ws-dashboard-external-stt-voice-typing`), updated from the retired Electron shell to Tauri v2 (macOS/Windows pinned-origin microphone permissions) and backed by `wscred` API-key storage.
2. **Track 1b (Second Backend):** Free/offline-capable OS native STT backend (`261125-feat-ws-desktop-native-os-stt`: macOS `SFSpeechRecognizer` and Windows WinRT `SpeechRecognition`) behind the same common voice input interface.
3. **Track 2 (Parked Research):** Live low-latency voice chat integration (`261125-research-ws-live-voice-gpt-realtime`: OpenAI GPT Realtime API via WebRTC + daemon-minted ephemeral secrets vs standalone harness), scaffolded and frozen until owner resumes.
4. **Track 3 (Meeting Minutes Mode):** Dedicated lead-session "Meeting Minutes Mode" toggle (`261125-feat-ws-meeting-minutes-voice-mode`) that immediately activates continuous diarized voice capture, displays a zero-token live transcript strip via SSE, and delivers turn-gated coalesced `<speak who="...">...</speak>` batches with a meeting-minutes system/envelope prompt so the lead continuously maintains the whiteboard.

## Non-Scope

- Replacing existing text coding harnesses (`agy`, `claude`, `codex`, `werk`) with an audio-only harness.
- Writing raw user audio recordings to disk (all audio buffers across Track 1 and Track 3 stay in memory only).
- Exposing provider API keys to browser JS, LAN HTTP origins, or logs (`wscred` redaction and loopback gating remain mandatory).
- Unapproved paid external API smoke tests during implementation/verification (require explicit owner approval per paid-ops directive).

## Child Tickets

- `261115-feat-ws-dashboard-external-stt-voice-typing` — Track 1a (`todo/`, first promotion target): common voice input interface, Tauri v2 macOS/Windows scoped microphone permissions, `wscred` Platform/Groq API-key support, and daemon-mediated push-to-talk external STT at the composer.
- `261125-feat-ws-desktop-native-os-stt` — Track 1b (`todo/`, `blocked-by: 261115-feat-ws-dashboard-external-stt-voice-typing`): OS native STT backend (`SFSpeechRecognizer` on macOS, WinRT `SpeechRecognition` on Windows) plugging into the common voice input interface.
- `261125-research-ws-live-voice-gpt-realtime` — Track 2 (`todo/`, research, parked): OpenAI GPT Realtime API live voice chat investigation (WebRTC ephemeral secret + voice frontend bridge vs standalone harness).
- `261125-feat-ws-meeting-minutes-voice-mode` — Track 3 (`todo/`, `blocked-by: 261115-feat-ws-dashboard-external-stt-voice-typing`): lead-session Meeting Minutes Mode toggle with continuous chunked diarization (`gpt-4o-transcribe-diarize` MVP behind diarizer interface), SSE zero-token live transcript strip, and turn-gated `<speak who="...">...</speak>` whiteboard-update loop.

## Cross-Child Decisions

1. **Common voice input abstraction (`04=a`).** The `WsVoiceInput` contract covers single-utterance push-to-talk at the composer: external STT (OpenAI, OpenRouter, Groq, custom local endpoints) and OS native STT (macOS/Windows Tauri bridge) share one composer UI state machine, permission model, and capability discovery surface. Meeting Minutes Mode (Track 3) is not a `WsVoiceInput` backend: it is a continuous, chunked, diarized capture path that reuses only the shared permission model, `wscred` API-key providers, and capability discovery surface, and owns its own toggle, VAD chunking, diarizer interface, and SSE strip. Track 1a must not reserve continuous-mode semantics in `WsVoiceInput`.
   - *Rejected:* requiring `261115` to reserve a continuous/chunked capability in `WsVoiceInput` for Track 3 to plug into (couples the first-promotion child to an unlanded consumer's needs).
2. **Tauri v2 pinned-origin microphone gating.** Keep `withGlobalTauri: false` and `"no-native-authority"` intact; grant microphone (and speech recognition on macOS) strictly for the pinned local daemon origin via native macOS `WKUIDelegate` + `Info.plist` purpose strings and Windows `WebView2` `COREWEBVIEW2_PERMISSION_KIND_MICROPHONE` with `SetSavesInProfile(false)`. Browser microphone capture is enabled only in a secure context on loopback (`http://127.0.0.1`) or `https://`, matching `261115`; LAN HTTP origins never expose microphone capture.
3. **Credential separation in `wscred`.** `wscred.ProviderOpenAI` is ChatGPT Plan OAuth (`KindSIWC`), not Platform API billing. OpenAI Platform STT/diarization/Realtime and Groq use dedicated `KindAPIKey` entries in `wscred` alongside existing `openrouter` and custom local endpoints (`ItemWerkEndpoints`), always masked in settings and loopback-only.
4. **Memory-only audio hygiene.** Neither composer voice typing (`(878)=b`) nor Meeting Minutes Mode writes recorded audio chunks to disk. Consequence for Track 3: each diarized chunk is discarded right after transcription and no voice enrollment or retained reference audio exists, so speaker labels are best-effort per chunk (the diarizer assigns labels per request and they may drift across chunks). A speaker alias maps the label as emitted and is not a stable person identity; Track 3 must not claim cross-chunk speaker identity.
5. **Turn-gated lead delivery (`06=b`).** Continuous voice workflows run inside an ordinary lead session (not a custom scribe authority bypass) and never inject per-second transcripts via mid-turn `Steer` (which aborts turns on `replacesTurn: true` harnesses such as `agy`). Instead, live transcripts stream directly to the UI over SSE at zero token cost, while coalesced `<speak who="...">...</speak>` deltas enter the lead's inbox only on idle/turn-gated boundaries. Zero token cost applies to the SSE strip only: each delivered batch starts a lead turn, so Track 3 planning must bound delivery cadence (a minimum interval or turn-rate cap); the existing turn-aware, never-merged inbox delivery precedent in `wsagent` (`Runner.DeliverCron`, `cron_seams.go`) is the reuse candidate to evaluate.
6. **Capture lifecycle on window hide (`11=a`, 2026-11-25).** Because `261122-feat-werkschaft-tray-notifications-full-quit` (decision 2086=a) hides the window to the tray on X-close, the webview survives with no unload, so "window close" no longer ends capture on its own. Composer voice typing (Track 1a, 1b) stops capture, releases the microphone, drops any composer lock, and discards the in-memory recording on window hide, minimize, or tray hide. Meeting Minutes Mode (Track 3) keeps capturing while hidden, minimized, or in the tray, with a persistent recording indicator (tray/dock) so mic use stays visible, and is an explicit exception to the hidden-window idle-CPU budget (under 0.5%, `261123-bug-ws-dashboard-webview-idle-cpu`) only while the Meeting Minutes toggle is ON. Child tickets align their lifecycle wording to this invariant when they are planned or promoted. Two child-planning obligations follow: Track 3 explicitly owns the persistent recording indicator, which needs a pinned-origin page-to-native bridge (the renderer has no native authority) and coordination with the tray-icon code of `261122-feat-werkschaft-tray-notifications-full-quit`; and Tracks 1a/1b detect hide/minimize through whatever hidden-window signal `261123-bug-ws-dashboard-webview-idle-cpu` lands, or an equivalent native-window-event-driven hook, and must not rely on `visibilitychange` alone since WebView2 may not flip `document.hidden` on minimize or occlusion.
   - *Rejected:* stopping all tracks including Meeting Minutes on hide with a "capture paused" state on restore (defeats the main background-meeting use case); keeping Meeting Minutes capture only behind a user "keep recording in background" opt-in (extra setting with no gain over a visible recording indicator).
7. **Compatibility tier: T0 additive** across all child tickets unless a future child explicitly revises a shared store or protocol boundary.

## Prior Decisions

- 261115-feat-ws-dashboard-external-stt-voice-typing (2026-11-15, Decisions): "Common voice input interface (04=a, 2026-11-25): Define a provider-agnostic frontend voice input controller (WsVoiceInput) and daemon voice-backend contract" — bearing: supports
- 261115-feat-ws-dashboard-external-stt-voice-typing (2026-11-15, Decisions): "Browser microphone control is enabled only on secure loopback; LAN HTTP origins hide or disable it." — bearing: constrains
- 261125-feat-ws-desktop-native-os-stt (2026-11-25, Decisions): "Keep withGlobalTauri: false and no-native-authority. Restrict the native STT JS bridge strictly to the pinned local daemon dashboard origin." — bearing: constrains
- 261125-feat-ws-meeting-minutes-voice-mode (2026-11-25, Decisions): "MVP diarization backend behind abstractable interface (06=b): chunked batch diarization via OpenAI gpt-4o-transcribe-diarize over VAD-aligned 15-30s windows" — bearing: supports
- 261125-research-ws-live-voice-gpt-realtime (2026-11-25, Confirmed Decisions): "(05): Scaffold and park this research ticket under 261125-epic-ws-voice-chat; do not implement live voice chat or run paid Realtime API probes until the owner explicitly unfreezes" — bearing: constrains
- 6153e895 (2026-11-24, commit): "Verification: go test ./internal/wscred and go test -race ./internal/wscred passed. Fake keys and transports only" — bearing: supports

## Route Facts

| fact | value | evidence |
|---|---|---|
| scope.span | multi-file | epic coordinating four child tickets (261115, 261125-feat-ws-desktop-native-os-stt, 261125-research-ws-live-voice-gpt-realtime, 261125-feat-ws-meeting-minutes-voice-mode); names no source paths itself |
| scope.surface | cross-module | children span daemon voice backend, wscred, Tauri desktop shell, dashboard composer |
| scope.new_public_symbol | yes | WsVoiceInput interface introduced by child 261115 |
| scope.new_type_contract | yes | provider-agnostic voice backend contract (capability discovery, partial/final transcript events) in child 261115 |
| scope.test_surface | unknown | epic has no phases; tests belong to children (261115 route facts: new-files) |
| complexity.reuse_points | confirmed | wscred provider registry agents-plugin-tool/internal/wscred/provider.go#L35-L72 (KindAPIKey, KindSIWC), tauri.conf.json withGlobalTauri false at agents-plugin-tool/desktop/tauri.conf.json#L11 |
| complexity.side_effect_risk | high | microphone permission, credential storage, and Tauri native authority boundaries across children |
| risk.correctness | high | continuous diarized capture and turn-gated delivery are stateful and timing sensitive |
| risk.fit | moderate | additive T0 over existing wscred, Tauri shell, and SSE; children sequenced behind 261115 |
| risk.test | high | native OS STT, mic permissions, and paid-provider paths are hard to test without live APIs |
| risk.security_or_contract | high | API keys must stay out of browser JS and logs; mic gating by pinned origin |

## Completion Criteria

- Done: Track 1a (`261115`), Track 1b (`261125-feat-ws-desktop-native-os-stt`), and Track 3 (`261125-feat-ws-meeting-minutes-voice-mode`) are in `.done/`, and Track 2 research (`261125-research-ws-live-voice-gpt-realtime`) has reached a recorded disposition (child ticket spawned or explicitly closed/deferred).
- Dropped: Owner explicitly abandons voice input and meeting-minutes capabilities for Werkschaft.
- Deferred: Track 2 live voice chat implementation stays deferred behind its parked research ticket until explicitly resumed by the owner; streaming WebSocket diarizers (Deepgram/AssemblyAI/Speechmatics) and local on-device diarization (pyannote/sherpa-onnx) are deferred follow-up backends behind Track 3's diarizer interface.
