package wsdashboard

import "testing"

func TestSessionReentryBurstClientContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/session-reentry-burst.test.cjs")
}
