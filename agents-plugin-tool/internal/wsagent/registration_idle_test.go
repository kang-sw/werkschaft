package wsagent

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

func TestRegistrationHeartbeatFailureAndLiveness(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "blocked")
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1000, 0).UTC()
	r := NewRunner(Options{Dir: dir, Now: func() time.Time { return now }, Log: io.Discard})
	c := &conv{r: r, rec: Record{ReplyID: "idle", Root: base, Backend: "pi", CreatedAt: now.Format(time.RFC3339), Lead: true}}
	if c.writeRegistration() || !c.lastRegAt().IsZero() {
		t.Fatal("failed registration advanced the heartbeat")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if !c.writeRegistration() || !c.lastRegAt().Equal(now) {
		t.Fatal("registration retry failed")
	}
	now = now.Add(time.Second)
	if !c.writeRegistration() {
		t.Fatal("unchanged registration failed")
	}
	row, err := wsdashboard.ReadRegistration(dir, "idle")
	if err != nil || row.HeartbeatAt != c.lastRegAt().UTC().Format(time.RFC3339) || c.lastRegAt().Equal(now) {
		t.Fatalf("unchanged write moved the heartbeat: %+v, %v", row, err)
	}
	c.rec.HostPID = 123
	if !c.writeRegistration() || !c.lastRegAt().Equal(now) {
		t.Fatal("semantic registration change was skipped")
	}
	// Missing, unreadable and altered same-owner rows must be repaired even
	// inside the heartbeat interval; success is a real spawn reservation.
	path := filepath.Join(wsdashboard.SessionDir(dir, "idle"), "registration.json")
	for _, broken := range []string{"missing", "unreadable", "changed"} {
		now = now.Add(time.Second)
		switch broken {
		case "missing":
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		case "unreadable":
			if err := os.WriteFile(path, []byte("not json"), 0600); err != nil {
				t.Fatal(err)
			}
		case "changed":
			row, err := wsdashboard.ReadRegistration(dir, "idle")
			if err != nil {
				t.Fatal(err)
			}
			row.HostPID = 999
			if err := wsdashboard.WriteRegistration(dir, row); err != nil {
				t.Fatal(err)
			}
		}
		if !c.writeRegistration() || !c.lastRegAt().Equal(now) {
			t.Fatalf("%s row not repaired", broken)
		}
		row, err := wsdashboard.ReadRegistration(dir, "idle")
		if err != nil || row.HostPID != 123 || !row.Hosted {
			t.Fatalf("%s reservation: %+v %v", broken, row, err)
		}
	}
	// An idle heartbeat interval rewrites nothing: liveness is memory.
	before, err := wsdashboard.ReadRegistration(dir, "idle")
	if err != nil {
		t.Fatal(err)
	}
	for range 4 {
		now = now.Add(wsdashboard.HeartbeatInterval)
		if !c.writeRegistration() {
			t.Fatal("unchanged write failed")
		}
		row, err := wsdashboard.ReadRegistration(dir, "idle")
		if err != nil || row.HeartbeatAt != before.HeartbeatAt {
			t.Fatalf("idle interval rewrote the row: %+v, %v", row, err)
		}
	}
	row, err = wsdashboard.ReadRegistration(dir, "idle")
	if err != nil {
		t.Fatal(err)
	}
	row.Hosted, row.Agent, row.HostPID = false, false, 0
	if err := wsdashboard.WriteRegistration(dir, row); err != nil {
		t.Fatal(err)
	}
	if c.writeRegistration() || !c.isForeign() {
		t.Fatal("terminal handoff was overwritten")
	}
}
