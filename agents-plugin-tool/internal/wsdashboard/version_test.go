package wsdashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCompareVersions(t *testing.T) {
	for _, tc := range []struct {
		a, b string
		want int
	}{
		{"0.48.10", "0.48.10", 0},
		{"0.48.10", "0.48.9", 1},
		{"0.48.10", "0.49.0", -1},
		{"1.0.0", "0.99.99", 1},
		{"v0.48.10", "0.48.10", 0},
		{"0.48.10-dev", "0.48.10", 1},
		{"0.48.10-dev", "0.48.11", -1},
		{"0.48.10-dev", "0.48.9", 1},
		{"0.48.10-dev", "0.48.10-dev", 0},
		{"0.48.10-alpha", "0.48.10-beta", -1},
		{"garbage", "0.0.1", -1},
		{"", "0.0.1", -1},
		{"garbage", "other", 0},
		{"1.2", "1.2.0", -1},
	} {
		if got := CompareVersions(tc.a, tc.b); got != tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
		if got := CompareVersions(tc.b, tc.a); got != -tc.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d (antisymmetry)", tc.b, tc.a, got, -tc.want)
		}
	}
}

// TestStageExecutableCopiesAndPrunes: the daemon's binary copy is
// content-addressed and reused for the same content; staging leaves the
// other copies alone, and the background prune removes only expired
// ws-mcp-dashboard-<sha16> builds (and their sidecars) beyond the pin set
// and the newest five. Foreign files, directories and symlinks always stay.
func TestStageExecutableCopiesAndPrunes(t *testing.T) {
	src := filepath.Join(t.TempDir(), "ws-mcp.exe")
	if err := os.WriteFile(src, []byte("binary-v1"), 0o755); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(t.TempDir(), "bin")
	first, err := StageExecutable(src, binDir)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(first) != binDir || filepath.Ext(first) != ".exe" {
		t.Fatalf("staged path = %s", first)
	}
	if body, _ := os.ReadFile(first); string(body) != "binary-v1" {
		t.Fatalf("staged content = %q", body)
	}
	again, err := StageExecutable(src, binDir)
	if err != nil || again != first {
		t.Fatalf("restage = %s, %v; want %s", again, err, first)
	}
	if err := os.WriteFile(src, []byte("binary-v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	second, err := StageExecutable(src, binDir)
	if err != nil || second == first {
		t.Fatalf("stage v2 = %s, %v", second, err)
	}
	entries, _ := os.ReadDir(binDir)
	if len(entries) != 2 {
		t.Fatalf("bin dir holds %v, want the v1 copy kept beside v2 (the running daemon may be v1)", entries)
	}
	dir := filepath.Dir(binDir)
	ago := func(path string, age time.Duration) {
		t.Helper()
		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	day := 24 * time.Hour
	// Nothing is expired yet, and the newest-five rule keeps both copies.
	PruneStagedExecutables(dir, second)
	for _, path := range []string{first, second} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the prune removed %s: %v", path, err)
		}
	}

	// Foreign entries beside the builds: another launcher's staging in flight
	// (a temp file and a not-yet-hashed name), unknown metadata, a directory
	// and a symlink. None is disposable, however old.
	tmp := filepath.Join(binDir, ".ws-mcp-dashboard-other.123.tmp")
	young := filepath.Join(binDir, "ws-mcp-dashboard-young.exe")
	metadata := filepath.Join(binDir, "other.metadata.json")
	foreign := filepath.Join(binDir, "electron-package")
	link := filepath.Join(binDir, "ws-mcp-dashboard-0123456789abcdef.exe")
	for _, path := range []string{tmp, young, metadata} {
		if err := os.WriteFile(path, []byte("in flight"), 0o755); err != nil {
			t.Fatal(err)
		}
		ago(path, 60*day)
	}
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	ago(foreign, 60*day)
	linked := os.Symlink(first, link) == nil

	// Six expired builds, each with a launcher sidecar, older than v1 (8 days).
	ago(first, 8*day)
	if err := os.WriteFile(first+".metadata.json", []byte(`{"accepted":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ago(first+".metadata.json", 8*day)
	var fakes []string
	for i := 0; i < 6; i++ {
		path := filepath.Join(binDir, fmt.Sprintf("ws-mcp-dashboard-%016x.exe", 0xf0+i))
		if err := os.WriteFile(path, []byte("old build"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path+".metadata.json", []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		ago(path, time.Duration(9+i)*day)
		ago(path+".metadata.json", time.Duration(9+i)*day)
		fakes = append(fakes, path)
	}
	PruneStagedExecutables(dir, second)
	// Newest five: second, first (8d), and the 9/10/11-day builds. The 12, 13
	// and 14-day builds are expired and beyond them: gone with their sidecars.
	for i, path := range fakes {
		want := i < 3
		for _, p := range []string{path, path + ".metadata.json"} {
			if _, err := os.Stat(p); (err == nil) != want {
				t.Errorf("%s present = %v, want %v", filepath.Base(p), err == nil, want)
			}
		}
	}
	for _, path := range []string{first, first + ".metadata.json", second, tmp, young, metadata, foreign} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the prune removed %s: %v", path, err)
		}
	}
	if linked {
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("prune removed symlink: %v", err)
		}
	}

	// Beyond the newest five and a month old, v1 survives while the restart
	// record names it: the pin set outranks age.
	ago(first, 40*day)
	for i := 0; i < 2; i++ { // two newer builds push v1 past the newest five
		path := filepath.Join(binDir, fmt.Sprintf("ws-mcp-dashboard-%016x.exe", 0xe0+i))
		if err := os.WriteFile(path, []byte("newer build"), 0o755); err != nil {
			t.Fatal(err)
		}
		ago(path, time.Duration(2+i)*day)
	}
	record, err := json.Marshal(runningExecutable{1, first, "1.0.0", "", 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, RunningExecutableFile), record, 0o600); err != nil {
		t.Fatal(err)
	}
	PruneStagedExecutables(dir, second)
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("the build recorded for restart was pruned: %v", err)
	}
	// Without the record only the running daemon's own build is safe.
	if err := os.Remove(filepath.Join(dir, RunningExecutableFile)); err != nil {
		t.Fatal(err)
	}
	PruneStagedExecutables(dir, second)
	if _, err := os.Stat(first); !os.IsNotExist(err) {
		t.Fatalf("an unpinned expired build beyond the newest five survived: %v", err)
	}
	if _, err := os.Stat(first + ".metadata.json"); !os.IsNotExist(err) {
		t.Fatalf("the pruned build's sidecar survived: %v", err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatalf("the running build was pruned: %v", err)
	}
}

func TestExecutableContentHashIsTheFileSHA256(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ws-mcp")
	if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := ExecutableContentHash(path)
	if want, _ := fileSHA256(path); len(got) != 64 || got != want {
		t.Fatalf("ExecutableContentHash = %q, want %q", got, want)
	}
	if ExecutableContentHash(filepath.Join(t.TempDir(), "absent")) != "" {
		t.Fatal("a missing file must hash to the empty (unknown) string")
	}
}

func TestDaemonExecutableIsRetainedOnEveryPlatform(t *testing.T) {
	src := filepath.Join(t.TempDir(), "ws-mcp")
	if err := os.WriteFile(src, []byte("daemon"), 0o755); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	got, err := DaemonExecutable(src, dir)
	if err != nil || filepath.Dir(got) != filepath.Join(dir, stagedBinDir) {
		t.Fatalf("DaemonExecutable = %s, %v", got, err)
	}
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(got); err != nil || string(data) != "daemon" {
		t.Fatalf("retained executable = %q, %v", data, err)
	}
}
