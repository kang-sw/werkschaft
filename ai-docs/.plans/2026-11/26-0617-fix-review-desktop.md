# Plan: Fix Desktop Release-Gate Review Findings

## Goal
Resolve the two Important release-gate review findings in `agents-plugin-tool/desktop/` from `d7697a3d-01-findings.md` on `impl/develop/fix-review-desktop`.

## Scope
1. **Finding 1 (`agents-plugin-tool/desktop/src/daemon.rs`)**
   - Extract `run_starter_with_timeout(command, input, spawned, timeout)` so `run_starter` retains its 42-second budget while tests can exercise timeout behavior in milliseconds.
   - When `stdin.write_all(input)` fails (`!wrote_identity`, e.g., `BrokenPipe` on immediate crash), poll briefly (up to `timeout.min(500ms)`) for the child's exit status before falling back to `child.kill()` + `child.wait()`.
   - On all failure paths (including `!wrote_identity` and timeout), capture `exit_code` from `child.try_wait()` / `child.wait()` and drain the stderr-tail channel with a bounded `recv_timeout(500ms)`.
   - Add unit tests in `daemon.rs` covering immediate crash-on-launch (non-zero exit with stderr before reading stdin) and timeout (child writes stderr and sleeps past deadline).

2. **Finding 3 (`agents-plugin-tool/desktop/scripts/recovery.test.cjs`)**
   - Add a standalone `node --test` DOM-harness test suite with zero Playwright dependency covering `agents-plugin-tool/desktop/ui/recovery.js`:
     - `window.__werkschaftRecovery(payload)` across idle, failure (package error vs. start failure, LKG states), readable/unreadable floor-blocked, diagnostics `<details>`, and `#update` DOM relocation.
     - `window.__werkschaftRecoveryNotice(message)` show/hide/clear behavior.
     - The 8-state self-update card state machine (`unavailable`, `available`, `downloading`, `ready`, `error`, `download-error`, `apply-error`, `unchecked`, default), bridge binding, and button actions.
     - `[data-act]` click dispatch gated on `navigator.userActivation?.isActive` over WebView2 and WebKit transports.
     - `WAIT_MS` timeout fallback behavior.

## Verification
- `cargo test --manifest-path agents-plugin-tool/desktop/Cargo.toml`
- `node --test agents-plugin-tool/desktop/scripts/recovery.test.cjs`
