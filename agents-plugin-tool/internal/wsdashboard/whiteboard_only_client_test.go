package wsdashboard

import "testing"

func TestWhiteboardOnlyClientContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/whiteboard-only.test.cjs")
}
