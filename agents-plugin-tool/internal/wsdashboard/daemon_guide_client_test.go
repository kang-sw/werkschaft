package wsdashboard

import "testing"

func TestDaemonGuideClientContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/daemon-guide.test.cjs")
}
