package wsdashboard

import "testing"

func TestWbUpdateVirtualStorePerf(t *testing.T) {
	runNodeTestSuite(t, "testdata/wbupdate-virtualstore-perf.test.cjs")
}
