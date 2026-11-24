package wsdashboard

const (
	windowsNormalPriorityClass           = uint32(0x00000020)
	processInformationPowerThrottling    = uint32(4) // ProcessPowerThrottling
	processPowerThrottlingCurrentVersion = uint32(1)
	processPowerThrottlingExecutionSpeed = uint32(0x1)
)

// processPowerThrottlingState mirrors Win32 PROCESS_POWER_THROTTLING_STATE
// (three 32-bit unsigned fields, 12 bytes, no padding).
type processPowerThrottlingState struct {
	Version     uint32
	ControlMask uint32
	StateMask   uint32
}

// defaultPowerThrottlingOptOutState returns the Win32 state that opts a process
// out of EcoQoS (PROCESS_POWER_THROTTLING_EXECUTION_SPEED = 0).
func defaultPowerThrottlingOptOutState() processPowerThrottlingState {
	return processPowerThrottlingState{
		Version:     processPowerThrottlingCurrentVersion,
		ControlMask: processPowerThrottlingExecutionSpeed,
		StateMask:   0,
	}
}
