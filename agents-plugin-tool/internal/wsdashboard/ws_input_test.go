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

func TestWsInputMigratedSurfaces(t *testing.T) {
	a, err := loadDashboardAssets()
	if err != nil {
		t.Fatalf("loadDashboardAssets: %v", err)
	}
	script := string(a.data.Script)
	checks := []struct {
		name   string
		needle string
	}{
		{"main composer attach", "window.WsInput.attach(ctext"},
		{"board note attach", "window.WsInput.attach(note"},
		{"selection popover attach", "window.WsInput.attach(mnote"},
		{"close-request reason attach", "WsInput.attach(input"},
		{"support chat attach", "window.WsInput.attach(c.ta"},
	}
	for _, tc := range checks {
		if !strings.Contains(script, tc.needle) {
			t.Errorf("dashboard Script missing %s (%q)", tc.name, tc.needle)
		}
	}
	if !strings.Contains(string(a.data.MemoScript), "window.WsInput.attach(area") {
		t.Errorf("MemoScript missing WsInput.attach wiring")
	}
}
