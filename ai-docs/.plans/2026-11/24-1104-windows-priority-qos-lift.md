# Plan: Windows Two-Layer Priority + EcoQoS Opt-Out (macOS Parity)

## Goal
Implement owner decision `(1533)=a` (compatibility tier **T0 transparent**): escape inherited `BELOW_NORMAL_PRIORITY_CLASS` and `PROCESS_POWER_THROTTLING_EXECUTION_SPEED` (EcoQoS) across both the dashboard daemon layer and the session holder layer on Windows, matching macOS's two-layer QoS lift (`launchd` `ProcessType=Interactive` + `holder.py` `lift_qos_default()`).

## Compatibility & Performance
- **Tier:** T0 transparent. Pure in-process Win32 API calls and `CreationFlags` additions; zero protocol, wire, schema, manifest, or stored-file changes.
- **File I/O:** Zero additional file touches (startup-only in-process `kernel32.dll` calls; `SetProcessInformation` resolved via `LazyDLL` with `Find() == nil` guard).

## Changes
1. **Daemon layer (Go):**
   - `agents-plugin-tool/internal/wsdashboard/spawn_windows.go`: add `windows.NORMAL_PRIORITY_CLASS` (`0x00000020`) to detached daemon `CreationFlags` while preserving breakaway / job-lifetime behavior.
   - `agents-plugin-tool/internal/wsdashboard/priority.go`, `priority_windows.go`, `priority_other.go`: add `LiftProcessPriority()` calling `SetPriorityClass(CurrentProcess, NORMAL_PRIORITY_CLASS)` and `SetProcessInformation(CurrentProcess, ProcessPowerThrottling=4, &PROCESS_POWER_THROTTLING_STATE{Version: 1, ControlMask: 1, StateMask: 0}, 12)` on Windows (no-op elsewhere).
   - `agents-plugin-tool/cmd/ws-mcp/dashboard.go`: invoke `wsdashboard.LiftProcessPriority()` once at daemon startup before spawning holders or children.
2. **Holder layer (Go + Python):**
   - `agents-plugin-tool/internal/wsholder/spawn_windows.go`: include `windows.NORMAL_PRIORITY_CLASS` (`0x00000020`) in `CreationFlags` via testable `windowsDetachedCreationFlags(breakaway)`.
   - `agents-plugin-tool/internal/wsholder/holder.py`:
     - Add `_clear_windows_eco_qos(handle, k32=None)` using `ctypes.WinDLL("kernel32", use_last_error=True)` with explicit 64-bit-safe `argtypes`/`restype` and `SetProcessInformation` availability guard.
     - Extend `lift_qos_default()` with a `sys.platform == "win32"` branch setting `NORMAL_PRIORITY_CLASS` and clearing EcoQoS.
     - Update `PipeChild.__init__` to include `NORMAL_PRIORITY_CLASS` in `creationflags` and call `_clear_windows_eco_qos(proc._handle)`.
     - Update `ConPtyChild.__init__` to include `NORMAL_PRIORITY_CLASS` in `dwCreationFlags` (`0x00080000 | 0x00000400 | 0x00000020`) and call `_clear_windows_eco_qos(info.hProcess, k32=k32)`.
3. **Tests:**
   - Go (`internal/wsdashboard/priority_test.go`, `spawn_electron_windows_test.go`, `internal/wsholder/spawn_windows_test.go`, `holder_qos_test.go`): struct layout/constants, daemon/holder creation flags, embedded `holder.py` parity.
   - Python (`internal/wsholder/tests/test_holder.py`): `lift_qos_default()` Win32 branch with mocked `kernel32`, `PipeChild` `creationflags` + EcoQoS clear, `ConPtyChild` `dwCreationFlags` + EcoQoS clear.

## Verification
- Remote Go verification on `w5090` (`GOOS=windows go vet`, `GOOS=windows go build ./cmd/ws-mcp`, `GOOS=windows go test -c`, and `go test ./internal/wsholder ./internal/wsdashboard`).
- Local Python verification (`python -m py_compile` and `python -m unittest`).
