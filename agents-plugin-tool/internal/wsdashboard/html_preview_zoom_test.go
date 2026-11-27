package wsdashboard

import "testing"

func TestHTMLPreviewZoomContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/html-preview-zoom.test.cjs")
}
