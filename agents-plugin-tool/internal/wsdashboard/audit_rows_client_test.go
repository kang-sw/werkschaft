package wsdashboard

import (
	"os/exec"
	"testing"
)

func TestAuditRowsClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed; run node --test frontend-audit-rows.test.js locally")
	}
	out, err := exec.Command(node, "--test", "frontend-audit-rows.test.js").CombinedOutput()
	if err != nil {
		t.Fatalf("audit rows client: %v\n%s", err, out)
	}
	t.Log(string(out))
}
