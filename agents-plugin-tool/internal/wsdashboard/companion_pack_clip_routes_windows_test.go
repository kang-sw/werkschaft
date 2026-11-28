//go:build windows

package wsdashboard

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCompanionClipJunctionRefused runs on a Windows runner: a junction at
// clips/ or in place of the pack directory is a reparse point the pinned
// write path refuses, and nothing is written through it.
func TestCompanionClipJunctionRefused(t *testing.T) {
	junction := func(link, target string) {
		t.Helper()
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
			t.Skipf("mklink /J unavailable: %v %s", err, out)
		}
	}
	body := companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "", false, ""))

	f := newCompanionClipFixture(t, "")
	outside := t.TempDir()
	junction(filepath.Join(f.dir, "clips"), outside)
	if w := f.author("PUT", companionClipPath(companionClipTestPack, "wave2"), f.rev(t), body); w.Code != http.StatusConflict {
		t.Fatalf("junction clips: %d %s", w.Code, w.Body)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote through the clips junction: %v", entries)
	}

	g := newCompanionClipFixture(t, "")
	rev := g.rev(t)
	moved := filepath.Join(t.TempDir(), "real")
	if err := os.Rename(g.dir, moved); err != nil {
		t.Fatal(err)
	}
	junction(g.dir, moved)
	if w := g.author("PUT", companionClipPath(companionClipTestPack, "wave2"), rev, body); w.Code != http.StatusConflict {
		t.Fatalf("junction pack dir: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(moved, "clips")); !os.IsNotExist(err) {
		t.Fatalf("wrote through the pack junction: %v", err)
	}
}
