package mcp

import (
	"testing"
	"time"
)

// waitForDashboardValidation is only for direct presentation-source reads that
// bypass the daemon Index's signal/invalidation path. External watched inputs
// (ticket listings, image stats, watch stamps) may retain a value for one
// second; allow 100 ms scheduling margin, not waitFor's longer
// asynchronous-operation deadline. Do not use this for board read-your-writes,
// mutation admission or nudge tests. DashboardSessionSource.now controls domain
// time, not these cache clocks.
func waitForDashboardValidation(t *testing.T) {
	t.Helper()
	time.Sleep(time.Second + 100*time.Millisecond)
}

// waitForFileCacheValidation waits out wsdashboard.FileCache's 5 s fallback
// validation interval (plus 100 ms margin) for isolated direct reads of parsed
// presentation files (record parts, snapshots) that receive no daemon signal.
func waitForFileCacheValidation(t *testing.T) {
	t.Helper()
	time.Sleep(5*time.Second + 100*time.Millisecond)
}
