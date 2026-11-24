package wsdashboard

import (
	"testing"
	"unsafe"
)

func TestPowerThrottlingStateLayoutAndDefaults(t *testing.T) {
	var state processPowerThrottlingState
	if got := unsafe.Sizeof(state); got != 12 {
		t.Fatalf("sizeof(processPowerThrottlingState) = %d, want 12", got)
	}
	if got := unsafe.Offsetof(state.Version); got != 0 {
		t.Fatalf("offsetof(Version) = %d, want 0", got)
	}
	if got := unsafe.Offsetof(state.ControlMask); got != 4 {
		t.Fatalf("offsetof(ControlMask) = %d, want 4", got)
	}
	if got := unsafe.Offsetof(state.StateMask); got != 8 {
		t.Fatalf("offsetof(StateMask) = %d, want 8", got)
	}
	if windowsNormalPriorityClass != 0x00000020 {
		t.Fatalf("windowsNormalPriorityClass = %#x, want 0x20", windowsNormalPriorityClass)
	}
	if processInformationPowerThrottling != 4 {
		t.Fatalf("processInformationPowerThrottling = %d, want 4", processInformationPowerThrottling)
	}

	optOut := defaultPowerThrottlingOptOutState()
	if optOut.Version != 1 || optOut.ControlMask != 1 || optOut.StateMask != 0 {
		t.Fatalf("defaultPowerThrottlingOptOutState() = %+v, want {Version:1 ControlMask:1 StateMask:0}", optOut)
	}
}

func TestLiftProcessPriorityDoesNotPanic(t *testing.T) {
	LiftProcessPriority()
}
