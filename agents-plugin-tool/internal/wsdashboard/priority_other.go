//go:build !windows

package wsdashboard

// LiftProcessPriority is a Windows-only best-effort opt-out of inherited low
// priority and EcoQoS throttling; macOS uses launchd ProcessType Interactive
// instead, and other OSes need no action here.
func LiftProcessPriority() {}
