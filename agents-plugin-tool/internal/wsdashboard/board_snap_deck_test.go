package wsdashboard

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func runNodeTestSuite(t *testing.T, script string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node not installed; run node --test %s locally", script)
	}
	out, err := exec.Command(node, "--test", filepath.FromSlash(script)).CombinedOutput()
	if err != nil {
		t.Fatalf("%s failed: %v\n%s", script, err, out)
	}
	t.Log(string(out))
}

func TestBoardAnchorContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/board-anchor.test.cjs")
}

func TestBoardDialAndPreviewContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/board-dial-and-preview.test.cjs")
}

func TestBoardFreeCanvasContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/board-free-canvas.test.cjs")
}

func TestBoardVirtualizeContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/board-virtualize.test.cjs")
}

func TestTitleClickScrollTopContracts(t *testing.T) {
	runNodeTestSuite(t, "testdata/title-click-scroll-top.test.cjs")
}
