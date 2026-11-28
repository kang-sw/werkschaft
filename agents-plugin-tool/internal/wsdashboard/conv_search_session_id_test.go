package wsdashboard

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConvSearchRejectsInvalidSessionID(t *testing.T) {
	d, err := StartDaemon(DaemonOptions{
		Dir:      t.TempDir(),
		Home:     t.TempDir(),
		Port:     0,
		Version:  "test",
		Sessions: fakeConvSessionSource{},
		Agents:   &fakeConvAgentHost{},
		LockWait: 200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	defer d.Close()

	for _, id := range []string{"../escape", "..", "a/b", `a\b`, "bad id", "../../etc/passwd"} {
		_, err := d.ConvSearch(context.Background(), ConvSearchRequest{SessionID: id, Query: "x"})
		if err == nil || !strings.Contains(err.Error(), "invalid session_id") {
			t.Errorf("ConvSearch session_id %q -> %v, want invalid session_id", id, err)
		}
	}
}
