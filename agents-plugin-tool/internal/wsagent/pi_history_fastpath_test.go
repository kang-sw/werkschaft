package wsagent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Distinct archived session.jsonl files of equal size are still byte-compared
// even when the source mtime is older than the archive copy (a restored or
// replaced file must not leave stale archived history); identical bytes and
// hard-linked same-file copies still report unchanged.
func TestRetiredPiJSONLComparesDistinctSameSizeFilesRegardlessOfMtime(t *testing.T) {
	source, archive := t.TempDir(), t.TempDir()
	for _, d := range []string{source, archive} {
		if err := os.MkdirAll(filepath.Join(d, "pi"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	from, to := filepath.Join(source, retiredPiSessionFile), filepath.Join(archive, retiredPiSessionFile)
	// Same size, different bytes, older source mtime: must still detect change.
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
	if changed, err := retiredPiFileChanged(source, archive, retiredPiSessionFile); err != nil || !changed {
		t.Fatalf("distinct same-size different-bytes file with older mtime: changed=%t err=%v, want changed", changed, err)
	}
	// Identical bytes across distinct files: unchanged.
	if err := os.WriteFile(to, []byte("aaaa"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := retiredPiFileChanged(source, archive, retiredPiSessionFile); err != nil || changed {
		t.Fatalf("distinct identical-bytes file: changed=%t err=%v, want unchanged", changed, err)
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
	// Same-file hard link skips byte comparison and reports unchanged.
	if err := os.Remove(to); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(from, to); err == nil {
		if changed, err := retiredPiFileChanged(source, archive, retiredPiSessionFile); err != nil || changed {
			t.Fatalf("hard-linked same file: changed=%t err=%v, want unchanged", changed, err)
		}
	}
}
