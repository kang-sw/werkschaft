---
title: "회의록 작성 모드: 화자 구분 실시간 음성 입력 및 화이트보드 자동 갱신"
parent: 261125-epic-ws-voice-chat
blocked-by: 261115-feat-ws-dashboard-external-stt-voice-typing
---

# Meeting Minutes Mode: Continuous Speaker-Diarized Voice Input and Live Whiteboard Updates

## Background

During live discussions or meetings, the user wants to open a dedicated hosted lead session, toggle on a **"Meeting Minutes Mode" (`회의록 모드`)**, and have the session immediately start continuous speaker-diarized voice capture while the lead listens to incoming utterances and continuously organizes notes, decisions, and action items onto the whiteboard (owner decision `06=b`, 2026-11-25).

## Decisions

- **Dedicated lead session with Meeting Minutes toggle (`06=b`, 2026-11-25):** Rather than introducing a separate scribe sub-agent or bypassing lead-only `whiteboard.update` authority, Meeting Minutes Mode is a session-level toggle on any hosted lead session. Toggling it ON immediately starts continuous voice capture and enables Meeting Minutes framing for that session; toggling it OFF cleanly stops capture, flushes any final pending transcript chunk, and ends the mode.
- **Mode-scoped system/envelope instruction (`06=b`):** While Meeting Minutes Mode is active, each delivered transcript batch carries a concise mode prompt informing the lead that:
  - It is operating in Meeting Minutes Mode and should capture/organize meeting state incrementally on the whiteboard (`wb:panel`/`wb:change`/`wb:log`/`wb:add`) rather than emitting verbose plain-text chat replies.
  - Spoken input arrives incrementally in chunks and may contain mid-sentence cuts, ASR typos, or occasional speaker-label drift — reconcile gently without interrupting the meeting or asking unnecessary clarifying questions unless explicitly addressed.
- **Speaker-tagged message format (`06=b`):** Deliver coalesced diarized utterances to the lead wrapped in structured speaker tags:
  ```xml
  <speak who="speaker_1">...</speak>
  <speak who="speaker_2">...</speak>
  ```
  Support optional lightweight in-UI speaker alias renaming (e.g., mapping `speaker_1` -> `Alice`) so future chunks and UI display use human-readable names without requiring voice enrollment.
- **MVP diarization backend behind abstractable interface (`06=b`):** Start with chunked batch diarization via OpenAI `gpt-4o-transcribe-diarize` over VAD-aligned 15–30s windows (reusing the `wscred` OpenAI Platform API key and Tauri/browser mic permissions from `261115-feat-ws-dashboard-external-stt-voice-typing`), behind a clean daemon diarizer interface so stateful streaming diarizers (e.g., Deepgram/AssemblyAI) can be added later without changing the session pipeline.
- **Turn-gated anti-thrash delivery & zero-token live transcript strip (auto-confirmed technical):**
  - Keep the rolling diarized transcript in a daemon in-memory buffer pushed directly to the dashboard UI via SSE (instant zero-token visibility for the user while speaking).
  - Never inject per-second transcripts via mid-turn `Steer` (which aborts turns on `replacesTurn: true` harnesses such as `agy` and blows up token usage). Instead, coalesce finalized `<speak who="...">` segments and enqueue them to the lead's inbox only on a **turn-gated cadence** (when the previous lead turn is idle, or every 15–30s / N finalized utterances once idle).
- **Memory-only audio hygiene:** Discard each recorded audio chunk from memory immediately after transcription; never write raw meeting audio to disk (consistent with `261115` `(878)=b`).
- **Compatibility tier: T0 additive.**

## Constraints

- **Applicable manuals (from `AGENTS.md` `### Implementation Conventions`):**
  - `ai-docs/manuals/shipped-surface-boundary.md`
  - `ai-docs/manuals/release-compatibility.md` (T0 additive)
  - `ai-docs/manuals/performance.md` (memory-first rolling transcript buffer, zero audio disk writes, idle-safe UI animations, no `:has()` selectors)
  - `ai-docs/manuals/dashboard-style.md` (borderless tonal live-transcript strip and mode toggle chip using `--wb-*` tokens)
- **Secure loopback only:** Meeting Minutes voice capture obeys the same secure-loopback / Tauri pinned-origin microphone gating as `261115`.
- **Paid API guard:** Never run live paid `gpt-4o-transcribe-diarize` API calls in automated tests or verification without explicit owner approval.

## Phases

### Phase 1: Lead-session Meeting Minutes Mode toggle, chunked diarization, SSE transcript strip, and turn-gated `<speak>` delivery

1. **Daemon diarization & turn-gated batcher (`internal/wsdashboard/`, `internal/wsagent/`):**
   - Define a daemon diarizer interface and implement the MVP `gpt-4o-transcribe-diarize` chunk adapter (reusing `wscred` `openai_api` key).
   - Add session-scoped Meeting Minutes state (toggle on/off, speaker alias map, in-memory rolling transcript ring, SSE push to session viewers).
   - Implement the turn-gated coalescer: accumulate finalized `<speak who="...">...</speak>` utterances while the lead is busy, and deliver one coalesced batch (with the Meeting Minutes system/envelope prompt) only when the lead is idle (never mid-turn `Steer`).

2. **Dashboard UI & continuous VAD chunk capture (`internal/wsdashboard/assets/`):**
   - Add the Meeting Minutes Mode toggle control in the session header/composer area (loopback/Tauri only).
   - On toggle ON, start continuous mic capture with client-side silence/VAD chunk segmentation (15–30s windows, hard cap to avoid oversized uploads), uploading chunks to the daemon and discarding audio buffers immediately in memory.
   - Render a compact, collapsible live diarized transcript strip fed by SSE (zero LLM tokens) with inline speaker alias rename and recording/paused/error indicators.

3. **Verification:**
   - Go unit tests (`go test ./internal/wsdashboard/... ./internal/wsagent/...`) with mocked diarizer responses verifying `<speak who="...">` formatting, speaker alias substitution, Meeting Minutes prompt framing, turn-gated coalescing (confirming zero mid-turn `Steer` calls and single coalesced delivery after busy turn completes), final flush on toggle OFF, and zero audio disk writes.
   - Node JS unit tests (including `frontend-idle.test.js`) verifying VAD chunk segmentation/teardown on toggle OFF or unload, SSE transcript strip rendering, speaker alias updates, secure-origin gating, and zero `:has()` regressions.
   - Isolated fixture verification only (never port `29417`; live paid API verification only with explicit owner approval).
