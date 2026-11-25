# Plan: Fix Dashboard Release-Gate Review Findings (2 & 4)

## Scope

Address the two Important release-gate findings in `agents-plugin-tool/internal/wsdashboard/` from review findings `d7697a3d-01-findings.md`:

1. **Finding 2 (`voice_routes.go`) — `resolveVoiceTarget` model fallback on backend override**
   - Only apply global `settings.Model` when `model == ""` AND `backend == settings.Backend`.
   - When a request overrides `backend` to a different provider (cloud preset such as `groq` or `local/<name>`) with an empty `model`, fall through to that backend's default (`spec.DefaultModel` or `"whisper-1"`).
   - Add unit tests in `voice_routes_test.go` covering omitted backend, same backend, cloud/local backend override, explicit model override, and empty global backend with stale global model.

2. **Finding 4 (`frontend-perf-switch-scroll.test.js`) — Execute real production code instead of source-text regex checks**
   - Refactor `'shell reveals frame early on ready+head without waiting for iframe load and stays idempotent on load'` to execute `shell.js`, `dashboard-lifecycle.js` shell glue (`shAdd`/`shOpen`/`shShow`/`shShellInit`/message handler), `dashboard.js` (`shStaged`/`shUnstage`), and `dashboard-frames.js` (`mountFrame`/`mountFrames`/`mountFramesPaced`/`mountSlice`) in the Node VM harness, asserting real DOM/state transitions, `postMessage` counts, and staged frame deferral/unstage mounting.
   - Refactor the `dashboard-rail.js` scroll-gating check (lines 798-800) to execute real `boardScrolling()`, `railGate`, `railRelease()`, `ws-board-scroll-idle` listener, and shell `window.wsBoardScrollIdle` against `WsRail.swapGate`, asserting swaps are held during active scroll (both direct and shell-framed) and flushed once scroll becomes idle.

## Verification

- `cd agents-plugin-tool && go vet ./internal/wsdashboard/... && go test -v ./internal/wsdashboard -run 'TestVoice'`
- `node --test agents-plugin-tool/internal/wsdashboard/frontend-perf-switch-scroll.test.js`
- `node --test agents-plugin-tool/internal/wsdashboard/testdata/*.test.cjs agents-plugin-tool/internal/wsdashboard/*.test.js`
