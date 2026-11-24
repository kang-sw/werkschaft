//go:build windows

package wsdashboard

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	kernel32PriorityDLL       = windows.NewLazySystemDLL("kernel32.dll")
	procSetProcessInformation = kernel32PriorityDLL.NewProc("SetProcessInformation")
)

// LiftProcessPriority performs a best-effort in-process opt-out of inherited
// low priority (BELOW_NORMAL_PRIORITY_CLASS) and EcoQoS execution-speed
// throttling (PROCESS_POWER_THROTTLING_EXECUTION_SPEED) at daemon startup.
// Failures are non-fatal and no file I/O is performed.
func LiftProcessPriority() {
	handle := windows.CurrentProcess()
	_ = windows.SetPriorityClass(handle, windows.NORMAL_PRIORITY_CLASS)
	if procSetProcessInformation.Find() == nil {
		state := defaultPowerThrottlingOptOutState()
		_, _, _ = procSetProcessInformation.Call(
			uintptr(handle),
			uintptr(processInformationPowerThrottling),
			uintptr(unsafe.Pointer(&state)),
			uintptr(uint32(unsafe.Sizeof(state))),
		)
	}
}
