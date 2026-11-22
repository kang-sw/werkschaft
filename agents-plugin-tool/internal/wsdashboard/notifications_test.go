package wsdashboard

import (
	"os/exec"
	"testing"
)

// TestNotificationSelectionAndDelivery runs the rail's notification
// selection (finishes at full idle, new questions) and the shell page's
// delivery (Werkschaft native first, then the browser's or a toast) in node.
func TestNotificationSelectionAndDelivery(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	out, err := exec.Command(node, "testdata/notifications.test.cjs").CombinedOutput()
	if err != nil {
		t.Fatalf("notifications fixture: %v\n%s", err, out)
	}
	t.Log(string(out))
}
