package wsdashboard_test

import (
	"os"
	"os/exec"
	"testing"
)

// A running turn's summary is replaced on nearly every event; the open
// tool-call details inside it must be carried over, not rebuilt.
func TestConversationLiveCallGraft(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; run testdata/conv-live-call-graft.test.cjs locally")
	}
	out, err := exec.Command(node, "--test", "testdata/conv-live-call-graft.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("live call graft cases: %v\n%s", err, out)
	}
	t.Log(string(out))
}

// TestConversationLiveCallGraftBrowser holds the reader's scroll geometry
// constant across turn-rev updates in real Chromium layout.
func TestConversationLiveCallGraftBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run the live call graft check in cached Chromium")
	}
	out, err := exec.Command("node", "testdata/conv-live-call-graft.browser.cjs").CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium live call graft: %v", err)
	}
}
