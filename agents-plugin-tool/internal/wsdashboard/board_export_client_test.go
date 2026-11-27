package wsdashboard

import "testing"

func TestBoardExportHandoffContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/board-export-handoff.test.cjs")
}
