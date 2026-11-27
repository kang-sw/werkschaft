package wsagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// An archived session.jsonl whose source is the same size and not newer than
// the archive copy is trusted without a byte comparison; a newer or resized
// source is still reported changed.
func TestRetiredPiJSONLSkipsCompareWhenUnchanged(t *testing.T) {
	source, archive := t.TempDir(), t.TempDir()
	for _, d := range []string{source, archive} {
		if err := os.MkdirAll(filepath.Join(d, "pi"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	from, to := filepath.Join(source, retiredPiSessionFile), filepath.Join(archive, retiredPiSessionFile)
	// Same size, different bytes: only the mtime shortcut can call this unchanged.
	if err := os.WriteFile(from, []byte("aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, []byte("bbbb"), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(from, old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(to, old.Add(time.Minute), old.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if changed, err := retiredPiFileChanged(source, archive, retiredPiSessionFile); err != nil || changed {
		t.Fatalf("unchanged archived file: changed=%t err=%v, want skip", changed, err)
	}
	// Source modified after the archive copy: compared, found different.
	if err := os.Chtimes(from, old.Add(time.Hour), old.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if changed, err := retiredPiFileChanged(source, archive, retiredPiSessionFile); err != nil || !changed {
		t.Fatalf("modified source: changed=%t err=%v, want changed", changed, err)
	}
	// Grown source is archived regardless of mtime.
	if err := os.WriteFile(from, []byte("aaaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(from, old, old); err != nil {
		t.Fatal(err)
	}
	if changed, err := retiredPiFileChanged(source, archive, retiredPiSessionFile); err != nil || !changed {
		t.Fatalf("grown source: changed=%t err=%v, want changed", changed, err)
	}
}
