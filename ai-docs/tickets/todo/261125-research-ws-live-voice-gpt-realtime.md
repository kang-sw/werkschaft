---
title: "라이브 음성 채팅 모드: OpenAI GPT Realtime API 결합 리서치"
parent: 261125-epic-ws-voice-chat
---

# Live Voice Chat Mode: OpenAI GPT Realtime API Integration Research

## Background

The owner wants to explore a live, bidirectional voice chat mode (e.g., integrating OpenAI's GPT Realtime API) as Track 2 of `261125-epic-ws-voice-chat`, but explicitly chose (`05`) to scaffold this research ticket and freeze it in a dedicated topic until Track 1 (STT voice typing) and higher-priority features settle.

## Initial Codebase & Architecture Grounding (2026-11-25)

### Credential & Provider Boundaries
- `agents-plugin-tool/internal/wscred/provider.go` registers `openrouter` (`KindAPIKey`), `openai` (`KindSIWC` — ChatGPT Plan OAuth), and custom local endpoints (`local_endpoints.go`).
- OpenRouter supports batch `POST /api/v1/audio/transcriptions` (final JSON only) but does **not** proxy OpenAI's WebSocket/WebRTC Realtime API.
- ChatGPT Plan OAuth (`ProviderOpenAI`) is scoped for ChatGPT plan inference (`chatgpt.tokens.use.direct`), not OpenAI Platform API billing (`sk-...`). Live Realtime sessions require an OpenAI Platform API key (`KindAPIKey` in `wscred`).

### Transport & Daemon Constraints
- `agents-plugin-tool/go.mod` currently has no Go WebSocket client dependency.
- OpenAI Realtime supports WebRTC (`RTCPeerConnection` from the browser/webview) authenticated by a short-lived ephemeral client secret minted server-side by the daemon (`POST /v1/realtime/sessions` or `/v1/realtime/client_secrets`).
- WebRTC with daemon-minted ephemeral secrets keeps the long-lived Platform API key strictly inside `wscred` on the daemon, avoids adding Go audio-proxy / WebSocket complexity, and leverages built-in browser/webview acoustic echo cancellation (AEC), jitter buffering, and Opus transport.
- Tauri v2 macOS (`WKWebView`) and Windows (`WebView2`) WebRTC + microphone behavior under strict renderer isolation (`withGlobalTauri: false`, pinned daemon origin) requires platform verification when this research resumes.

### Candidate Agent Integration Models
1. **Voice Frontend Bridge (Recommended hypothesis):** The Realtime model handles low-latency spoken dialogue, interruption (barge-in), and conversational pacing, calling scoped local daemon tools (read whiteboard, inspect status/tickets, enqueue or steer messages to the active hosted lead) while existing coding harnesses (`agy`, `claude`, `codex`, `werk`) continue to execute real work.
2. **Standalone Voice Harness:** The Realtime model runs directly as a hosted lead session with full workflow tool exposure.

## Outcome Ledger

### Verified Findings
- `wscred.ProviderOpenAI` is ChatGPT Plan OAuth (`KindSIWC`), not an OpenAI Platform API key (`agents-plugin-tool/internal/wscred/openai_oauth.go:25-33`, `provider.go:70-73`).
- OpenRouter does not proxy OpenAI Realtime API; it supports batch final-only `audio/transcriptions` only.
- `agents-plugin-tool/go.mod` carries no WebSocket library; WebRTC with a daemon-minted ephemeral secret avoids Go WebSocket proxying while keeping the Platform API key in `wscred`.
- Werkschaft desktop is Tauri v2 (`agents-plugin-tool/desktop/tauri.conf.json`), with `withGlobalTauri: false` and no existing `WKUIDelegate` / WebView2 microphone permission hooks yet (shared prerequisite with Track 1a `261115-feat-ws-dashboard-external-stt-voice-typing`).

### Confirmed Decisions
- `(05)` (2026-11-25): Scaffold and park this research ticket under `261125-epic-ws-voice-chat`; do not implement live voice chat or run paid Realtime API probes until the owner explicitly unfreezes this topic.

### Proposals
- Prefer the **Voice Frontend Bridge** model over a standalone Realtime coding harness, using WebRTC + daemon-minted ephemeral secrets.

### Open Questions
- Voice Frontend Bridge vs Standalone Voice Harness: latency, tool-calling reliability, cost per minute, and barge-in UX when delegating to an existing hosted lead.
- Exact `WKWebView` (macOS Tauri v2) and `WebView2` (Windows Tauri v2) WebRTC `RTCPeerConnection` + AEC stability on `http://127.0.0.1` loopback origins.
- Tool surface exposed to the Realtime voice layer (read-only status/whiteboard vs write/message injection into the lead) and explicit user cost/session-duration guardrails.
- Whether alternative realtime speech providers (e.g., Gemini Live API) should share the same transport/bridge abstraction.

### Rejected Alternatives
- Proxying raw real-time PCM/Opus audio through OpenRouter (unsupported by OpenRouter).
- Exposing a long-lived OpenAI Platform API key directly to renderer JavaScript (violates `wscred` secret hygiene and LAN-safety invariants).
