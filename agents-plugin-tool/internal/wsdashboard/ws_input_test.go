package wsdashboard

import (
	"os/exec"
	"strings"
	"testing"
)

func TestWsInputComponent(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; run node --test testdata/ws-input.test.cjs locally")
	}
	out, err := exec.Command(node, "--test", "testdata/ws-input.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("ws-input.test.cjs failed:\n%s", string(out))
	}
}

func TestWsInputInMentionScript(t *testing.T) {
	a, err := loadDashboardAssets()
	if err != nil {
		t.Fatalf("loadDashboardAssets: %v", err)
	}
	if !strings.Contains(string(a.data.MentionScript), "window.WsInput =") {
		t.Fatalf("MentionScript missing window.WsInput export")
	}
	if !strings.Contains(string(a.data.MentionScript), "window.WsMdEditor =") {
		t.Fatalf("MentionScript missing window.WsMdEditor fallback export")
	}
}
