package mcp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// TestFSReadsTheComposerOutputArtifact: the dashboard's composer output file
// (wsdashboard.ComposerArtifact) is readable by the harness's fs.read for
// hostile output: invalid UTF-8 and NUL bytes beyond the 1 MiB cap. fs.read
// refuses a file with a NUL in its head as binary and reads whole files within
// its scan cap, so the artifact is UTF-8 text without NUL and within the cap;
// what the reader returns is its own line- and budget-bounded view of it.
func TestFSReadsTheComposerOutputArtifact(t *testing.T) {
	dir := t.TempDir()
	cases := map[string][]byte{
		"invalid": bytes.Repeat([]byte{0xff}, 900000),
		"nul":     bytes.Repeat([]byte("line\x00with nul\n"), 90000),
		"mixed":   append(bytes.Repeat([]byte{0, 0xff, 'a'}, 400000), '\n'),
	}
	for name, raw := range cases {
		text, dropped, replaced := wsdashboard.ComposerArtifact(raw)
		if !replaced || len(text) > 1<<20 {
			t.Fatalf("%s: artifact %d bytes, replaced=%v", name, len(text), replaced)
		}
		if dropped < 0 || dropped > len(raw) {
			t.Fatalf("%s: dropped %d of %d", name, dropped, len(raw))
		}
		path := filepath.Join(dir, name+".out")
		if err := os.WriteFile(path, text, 0o600); err != nil {
			t.Fatal(err)
		}
		got, err := fsRead(path, 0, fsReadDefaultLines)
		if errors.Is(err, errFSBinary) || err != nil {
			t.Fatalf("%s: fs.read refused the artifact: %v", name, err)
		}
		if !strings.Contains(got, "1\t") {
			t.Fatalf("%s: fs.read returned no lines: %.80q", name, got)
		}
		// A later window of the file is reachable by offset.
		if _, err := fsRead(path, 1, fsReadDefaultLines); err != nil {
			t.Fatalf("%s: offset read: %v", name, err)
		}
	}
	// The raw bytes, as a NUL-bearing file, are what fs.read would refuse.
	raw := filepath.Join(dir, "raw.out")
	if err := os.WriteFile(raw, cases["nul"][:1<<20], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fsRead(raw, 0, 10); !errors.Is(err, errFSBinary) {
		t.Fatalf("the raw NUL output was not refused as binary: %v", err)
	}
}
