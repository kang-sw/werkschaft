---
title: "데스크톱 OS 네이티브 STT 백엔드 (macOS SFSpeechRecognizer · Windows WinRT)"
parent: 261125-epic-ws-voice-chat
blocked-by: 261115-feat-ws-dashboard-external-stt-voice-typing
---

# Desktop OS Native STT Backend (macOS SFSpeechRecognizer and Windows WinRT)

## Background

`261115-feat-ws-dashboard-external-stt-voice-typing` introduces the common `WsVoiceInput` interface and daemon-mediated external STT providers. To provide zero-cost, low-latency, offline-capable voice typing inside the Werkschaft Tauri v2 desktop app without requiring paid API keys, this ticket adds an OS native speech-to-text backend behind the same `WsVoiceInput` contract (owner decision `04=a`, 2026-11-25).

## Decisions

- **Plug into `WsVoiceInput` (`04=a`):** Expose OS native dictation as a selectable backend in the same settings/provider switcher and composer caret-capsule UX established by `261115-feat-ws-dashboard-external-stt-voice-typing`. Partial recognition hypotheses stream into the dimmed caret capsule and only finalized text is committed at the caret on stop.
- **macOS (`SFSpeechRecognizer` via `objc2`):** Implement a native bridge in `agents-plugin-tool/desktop/src/macos/` using `objc2-speech` / `objc2-av-foundation` (consistent with existing `objc2` + `WKScriptMessageHandler` bridges `werkschaftUpdate`/`werkschaftNotify`), adding `NSSpeechRecognitionUsageDescription` to `bundle.macOS.infoPlist` and preferring on-device recognition (`requiresOnDeviceRecognition`) when available for the requested locale.
- **Windows (WinRT `Windows.Media.SpeechRecognition`):** Implement a native bridge in `agents-plugin-tool/desktop/src/windows.rs` using the pinned `windows = "=0.62.2"` crate, checking installed recognizer language availability and surfacing clear actionable status when the required OS language pack or Windows speech privacy setting is disabled.
- **Strict origin & renderer isolation:** Keep `withGlobalTauri: false` and `"no-native-authority"`. Restrict the native STT JS bridge strictly to the pinned local daemon dashboard origin. Browser-only (non-Tauri) sessions hide the OS-native backend option and cleanly fall back to external STT providers.
- **Compatibility tier: T0 additive.**

## Constraints

- **Applicable manuals (from `AGENTS.md` `### Implementation Conventions`):**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/release-compatibility.md` (T0 additive)
  - `ai-docs/manuals/performance.md` (zero audio written to disk; stop audio engine and release mic handles immediately when dictation stops or window closes/hides)
  - `ai-docs/manuals/config-settings-ui.md` & `ai-docs/manuals/wsflow-mirroring.md` (if settings options change)
- **Do not use browser Web Speech API (`webkitSpeechRecognition`)** as the primary backend (unreliable in embedded `WKWebView`/`WebView2` and lacks offline/privacy guarantees).

## Phases

### Phase 1: Native macOS & Windows STT bridges behind `WsVoiceInput`

1. **macOS native STT bridge (`agents-plugin-tool/desktop/src/macos/`):**
   - Add `NSSpeechRecognitionUsageDescription` to macOS `Info.plist`.
   - Implement start/stop/cancel recognition with `AVAudioEngine` + `SFSpeechAudioBufferRecognitionRequest` + `SFSpeechRecognizer`, streaming partial/final/error events over a pinned-origin `WKScriptMessageHandler` bridge.
   - Always tear down audio taps and release the microphone on stop, error, navigation, or app suspend.

2. **Windows native STT bridge (`agents-plugin-tool/desktop/src/windows.rs`):**
   - Implement WinRT `SpeechRecognizer` start/stop/cancel over a pinned-origin WebView2 message bridge, surfacing language-pack/privacy unavailability clearly in UI status.

3. **Frontend adapter & settings wiring (`internal/wsdashboard/assets/`):**
   - Register the `os-native` backend in `WsVoiceInput` when running inside the Tauri desktop shell with bridge availability reported, reusing the exact caret capsule, lock, and commit lifecycle from `261115`.

4. **Verification:**
   - Rust tests (`cargo test` in `agents-plugin-tool/desktop/`) covering origin gating, session lifecycle teardown, and capability reporting (`GOOS=windows` / cross-platform compilation checks + macOS/Windows smoke verification).
   - Node JS unit tests verifying `WsVoiceInput` backend selection, partial/final event handling, browser fallback (hiding OS native option outside Tauri), and cleanup on error/cancel.
