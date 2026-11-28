package wsdashboard

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// registryIO counts the registry.go file operations of one kind.
func registryIO(op string) int64 {
	var n int64
	for site, count := range wsstate.FileIOCounters() {
		if strings.HasPrefix(site, "file_io_site:wsdashboard/registry.go:") && strings.HasSuffix(site, ":"+op) {
			n += count
		}
	}
	return n
}

// readCounted reads replyID's row and reports the registry stats and file
// reads it made.
func readCounted(t *testing.T, dir, replyID string) (reg Registration, stats, reads int64, err error) {
	t.Helper()
	stats0, reads0 := registryIO("stat"), registryIO("read_file")
	reg, err = ReadRegistration(dir, replyID)
	return reg, registryIO("stat") - stats0, registryIO("read_file") - reads0, err
}

func backdateRegistration(t *testing.T, path string, at time.Time) {
	t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
}

// TestReadRegistrationCacheServesUnchangedRowsByStat pins the parsed-row
// cache: an unchanged settled row costs one stat and no read, while every
// replacement (local or another process's, even one keeping size and mtime),
// a fresh write inside the racy window, and a delete are seen at once.
func TestReadRegistrationCacheServesUnchangedRowsByStat(t *testing.T) {
	dir := t.TempDir()
	code := 3
	row := Registration{ReplyID: "cachedrow", Project: "/p1", Hosted: true, HostPID: 4242, HostExitCode: &code}
	if err := WriteRegistration(dir, row); err != nil {
		t.Fatal(err)
	}
	path := RegistrationPath(dir, row.ReplyID)
	old := time.Now().Add(-time.Hour).Truncate(time.Second)
	backdateRegistration(t, path, old)

	if reg, stats, reads, err := readCounted(t, dir, row.ReplyID); err != nil || reg.Project != "/p1" || stats != 1 || reads != 1 {
		t.Fatalf("cold read: %+v %v stats=%d reads=%d, want /p1, 1 stat, 1 read", reg, err, stats, reads)
	}
	for i := range 3 {
		reg, stats, reads, err := readCounted(t, dir, row.ReplyID)
		if err != nil || reg.Project != "/p1" || reg.HostPID != 4242 || stats != 1 || reads != 0 {
			t.Fatalf("warm read %d: %+v %v stats=%d reads=%d, want 1 stat, 0 reads", i, reg, err, stats, reads)
		}
		// A caller's write through a pointer field never reaches the cache.
		*reg.HostExitCode = 99
	}
	if reg, _ := ReadRegistration(dir, row.ReplyID); reg.HostExitCode == nil || *reg.HostExitCode != 3 {
		t.Fatalf("cached row aliased a caller's copy: %+v", reg)
	}

	// Another process replaces the file whole with a same-size row and the
	// same mtime: only the file identity differs.
	other := row
	other.Project = "/p2"
	tmpDir := t.TempDir()
	if err := WriteRegistration(tmpDir, other); err != nil {
		t.Fatal(err)
	}
	external := RegistrationPath(tmpDir, other.ReplyID)
	if a, b := mustStat(t, external), mustStat(t, path); a.Size() != b.Size() {
		t.Fatalf("fixture sizes differ: %d vs %d", a.Size(), b.Size())
	}
	backdateRegistration(t, external, old)
	if err := os.Rename(external, path); err != nil {
		t.Fatal(err)
	}
	if reg, _, reads, err := readCounted(t, dir, row.ReplyID); err != nil || reg.Project != "/p2" || reads != 1 {
		t.Fatalf("external replace: %+v %v reads=%d, want /p2 read once", reg, err, reads)
	}
	if reg, _, reads, err := readCounted(t, dir, row.ReplyID); err != nil || reg.Project != "/p2" || reads != 0 {
		t.Fatalf("settled external replace: %+v %v reads=%d, want cached /p2", reg, err, reads)
	}

	// A local write is fresh, inside the racy window: read every time.
	local := row
	local.Project = "/p3"
	if err := WriteRegistration(dir, local); err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		if reg, _, reads, err := readCounted(t, dir, row.ReplyID); err != nil || reg.Project != "/p3" || reads != 1 {
			t.Fatalf("fresh local write read %d: %+v %v reads=%d, want /p3 read each time", i, reg, err, reads)
		}
	}

	// A delete by another process surfaces os.ErrNotExist, cached or not.
	backdateRegistration(t, path, old)
	if _, err := ReadRegistration(dir, row.ReplyID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, reads, err := readCounted(t, dir, row.ReplyID); !errors.Is(err, os.ErrNotExist) || reads != 0 {
		t.Fatalf("external delete: err=%v reads=%d, want os.ErrNotExist without a read", err, reads)
	}

	// A local remove after a cached read surfaces it too.
	if err := WriteRegistration(dir, row); err != nil {
		t.Fatal(err)
	}
	backdateRegistration(t, path, old)
	if _, err := ReadRegistration(dir, row.ReplyID); err != nil {
		t.Fatal(err)
	}
	if err := RemoveRegistration(dir, row.ReplyID); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegistration(dir, row.ReplyID); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local remove: err=%v, want os.ErrNotExist", err)
	}

	// A row that stops parsing is reported, never served from the cache.
	if err := WriteRegistration(dir, row); err != nil {
		t.Fatal(err)
	}
	backdateRegistration(t, path, old)
	if _, err := ReadRegistration(dir, row.ReplyID); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "corrupt.tmp"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(filepath.Dir(path), "corrupt.tmp"), path); err != nil {
		t.Fatal(err)
	}
	backdateRegistration(t, path, old)
	if _, err := ReadRegistration(dir, row.ReplyID); err == nil {
		t.Fatal("corrupt replacement served the cached row")
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
