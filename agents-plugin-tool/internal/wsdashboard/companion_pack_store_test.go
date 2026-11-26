package wsdashboard

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func companionTestManifest(id, file, extra string) []byte {
	return []byte(fmt.Sprintf(`{"schema":"ws-companion/v1","id":%q,"name":%q,"canvas":[512,512],"poses":{"stand":{"layers":[{"file":%q}]}},"extension":{"future":true}%s}`, id, id, file, extra))
}
func companionTestPack(t *testing.T, home, id string, manifest []byte) string {
	t.Helper()
	dir := filepath.Join(home, "companions", id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stand.webp"), []byte("raster"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}
func companionTestError(s *companionSnapshot, id string) bool {
	for _, e := range s.errors {
		if e.ID == id && e.Path != "" && e.Message != "" {
			return true
		}
	}
	return false
}

func TestCompanionPackStoreCatalogAndResolution(t *testing.T) {
	home := t.TempDir()
	raw := companionTestManifest("alpha", "stand.webp", `,"autoMatch":{"harnesses":["fixture-harness"],"models":["needle"]}`)
	companionTestPack(t, home, "alpha", raw)
	companionTestPack(t, home, "beta", companionTestManifest("beta", "stand.webp",
		`,"autoMatch":{"harnesses":["other-harness"],"models":["NEEDLE","boost"]}`))
	for _, id := range []string{"default", "auto", "Bad"} {
		companionTestPack(t, home, id, companionTestManifest(id, "stand.webp", ""))
	}
	companionTestPack(t, home, "unsafe", companionTestManifest("unsafe", "../evil.png", ""))
	companionTestPack(t, home, "svg", companionTestManifest("svg", "evil.svg", ""))
	s := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: home})
	if s.byID["default"] == nil || s.byID["default"].info.Source != "embedded" || len(s.byID["default"].assets) == 0 {
		t.Fatal("embedded default missing")
	}
	if p := s.byID["alpha"]; p == nil || string(p.manifest) != string(raw) {
		t.Fatal("minimal raw manifest not preserved")
	}
	for _, id := range []string{"default", "auto", "Bad", "unsafe", "svg"} {
		if !companionTestError(s, id) {
			t.Errorf("missing error for %s: %+v", id, s.errors)
		}
	}
	for _, tc := range []struct{ h, m, id, by string }{
		{"", "", "default", "default"},
		{"fixture-harness-variant", "unrelated", "default", "default"},
		{"FIXTURE-HARNESS", "", "alpha", "harness"},
		{"", "family-Needle-1", "alpha", "model"},
		{"fixture-harness", "family-boost-1", "beta", "model"},
		{"OTHER-HARNESS", "family-needle-1", "beta", "model"},
	} {
		got := s.catalog(tc.h, tc.m).ResolvedAuto
		if got.PackID != tc.id || got.MatchedBy != tc.by {
			t.Errorf("%+v: %+v", tc, got)
		}
	}
}
func TestCompanionPackConfigHome(t *testing.T) {
	env, explicit := t.TempDir(), t.TempDir()
	companionTestPack(t, env, "env", companionTestManifest("env", "stand.webp", ""))
	companionTestPack(t, explicit, "explicit", companionTestManifest("explicit", "stand.webp", ""))
	t.Setenv("WS_CONFIG_HOME", env)
	for _, tc := range []struct{ home, want, absent string }{{explicit, "explicit", "env"}, {"", "env", "explicit"}} {
		s := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: tc.home})
		if s.byID[tc.want] == nil || s.byID[tc.absent] != nil {
			t.Fatalf("home %q: %+v", tc.home, s.packs)
		}
	}
}
func TestCompanionPackSymlinks(t *testing.T) {
	home := t.TempDir()
	external := t.TempDir()
	companionTestPack(t, external, "outside", companionTestManifest("outside", "stand.webp", ""))
	os.MkdirAll(filepath.Join(home, "companions"), 0755)
	if err := os.Symlink(filepath.Join(external, "companions", "outside"), filepath.Join(home, "companions", "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	dir := companionTestPack(t, home, "assetlink", companionTestManifest("assetlink", "stand.webp", ""))
	os.Remove(filepath.Join(dir, "stand.webp"))
	if err := os.Symlink(filepath.Join(external, "companions", "outside", "stand.webp"), filepath.Join(dir, "stand.webp")); err != nil {
		t.Fatal(err)
	}
	s := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: home})
	for _, id := range []string{"linked", "assetlink"} {
		if s.byID[id] != nil || !companionTestError(s, id) {
			t.Errorf("symlink %s: %+v", id, s.errors)
		}
	}
	rootHome := t.TempDir()
	if err := os.Symlink(filepath.Join(external, "companions"), filepath.Join(rootHome, "companions")); err != nil {
		t.Fatal(err)
	}
	s = new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: rootHome})
	if s.byID["outside"] != nil || len(s.errors) == 0 {
		t.Fatalf("root symlink accepted: %+v", s)
	}
}

func TestCompanionPackPinnedDirectories(t *testing.T) {
	for _, tc := range []struct {
		name, beforeOpen string
		moveRoot         bool
	}{
		{"root replaced before pack open", "one", true},
		{"pack replaced before manifest open", "pack.json", false},
		{"pack replaced before asset open", "stand.webp", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			raw := companionTestManifest("one", "stand.webp", "")
			original := companionTestPack(t, home, "one", raw)
			replacement := companionTestPack(t, t.TempDir(), "one",
				companionTestManifest("one", "stand.webp", `,"replacement":true`))
			if err := os.WriteFile(filepath.Join(replacement, "stand.webp"), []byte("replacement"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.moveRoot {
				original, replacement = filepath.Dir(original), filepath.Dir(replacement)
			}

			// The lookup hook runs after the parent opened and before its
			// child opens. The same lexical path now reaches different bytes.
			swapped := false
			store := &CompanionPackStore{open: func(parent companionPackFile, name string, directory bool) (companionPackFile, error) {
				if name == tc.beforeOpen {
					if parent == nil || swapped {
						t.Fatalf("unexpected lookup of %q", name)
					}
					if err := os.Rename(original, original+"-held"); err != nil {
						t.Fatalf("rename open directory: %v", err)
					}
					if err := os.Rename(replacement, original); err != nil {
						t.Fatalf("replace open directory path: %v", err)
					}
					swapped = true
				}
				return openCompanionPackEntry(parent, name, directory)
			}}
			snapshot := store.snapshot(wsconfig.Options{ConfigHome: home})
			pack := snapshot.byID["one"]
			if !swapped || pack == nil || string(pack.manifest) != string(raw) || string(pack.assets["stand.webp"]) != "raster" {
				t.Fatalf("open directory did not keep its original pack: swapped=%t, pack=%v, errors=%v", swapped, pack, snapshot.errors)
			}
		})
	}
}

func TestCompanionPackLimits(t *testing.T) {
	t.Run("files and count", func(t *testing.T) {
		home := t.TempDir()
		dir := companionTestPack(t, home, "a-bigmanifest", companionTestManifest("a-bigmanifest", "stand.webp", ""))
		os.WriteFile(filepath.Join(dir, "pack.json"), make([]byte, companionManifestCap+1), 0600)
		dir = companionTestPack(t, home, "b-bigasset", companionTestManifest("b-bigasset", "stand.webp", ""))
		os.WriteFile(filepath.Join(dir, "stand.webp"), make([]byte, companionAssetCap+1), 0600)
		for i := 0; i < 32; i++ {
			id := fmt.Sprintf("p%02d", i)
			companionTestPack(t, home, id, companionTestManifest(id, "stand.webp", ""))
		}
		s := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: home})
		for _, id := range []string{"a-bigmanifest", "b-bigasset", "p30", "p31"} {
			if !companionTestError(s, id) {
				t.Errorf("missing limit error %s", id)
			}
		}
		if s.byID["default"] == nil || s.byID["p29"] == nil {
			t.Fatal("earlier packs missing")
		}
	})
	t.Run("128 MiB catalog budget", func(t *testing.T) {
		home := t.TempDir()
		layers := make([]string, 8)
		for i := range layers {
			layers[i] = fmt.Sprintf(`{"file":"%d.webp"}`, i)
		}
		for n := 0; n < 9; n++ {
			id := fmt.Sprintf("p%d", n)
			raw := []byte(fmt.Sprintf(`{"schema":"ws-companion/v1","id":%q,"name":%q,"canvas":[512,512],"poses":{"stand":{"layers":[%s]}}}`, id, id, strings.Join(layers, ",")))
			dir := companionTestPack(t, home, id, raw)
			for i := range layers {
				f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%d.webp", i)))
				if err != nil {
					t.Fatal(err)
				}
				if err = f.Truncate(companionAssetCap - 1024); err != nil {
					t.Fatal(err)
				}
				if err = f.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
		s := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: home})
		for n := 0; n < 8; n++ {
			id := fmt.Sprintf("p%d", n)
			if s.byID[id] == nil {
				t.Fatalf("earlier pack %s rejected: %+v", id, s.errors)
			}
		}
		if s.byID["p8"] != nil || !companionTestError(s, "p8") || s.byID["default"] == nil {
			t.Fatalf("cache budget: %+v", s.errors)
		}
	})
	t.Run("pack and cache", func(t *testing.T) {
		// Use a bounded reader to exercise size accounting without writing 64 MiB to disk.
		raw := companionTestManifest("large", "stand.webp", "")
		asset := make([]byte, companionAssetCap)
		read := func(name string, limit int) ([]byte, error) {
			var b []byte
			if name == "pack.json" {
				b = raw
			} else {
				b = asset
			}
			if len(b) > limit {
				return nil, errCompanionFileLimit
			}
			return b, nil
		}
		if _, _, err := loadCompanionPack("large", "local", len(raw)+len(asset)-1, read); err == nil || !strings.Contains(err.Error(), "cache") {
			t.Fatalf("cache limit: %v", err)
		}
		layers := make([]string, 9)
		for i := range layers {
			layers[i] = fmt.Sprintf(`{"file":"%d.webp"}`, i)
		}
		raw = []byte(fmt.Sprintf(`{"schema":"ws-companion/v1","id":"large","name":"large","canvas":[512,512],"poses":{"stand":{"layers":[%s]}}}`, strings.Join(layers, ",")))
		if _, _, err := loadCompanionPack("large", "local", companionCacheCap, read); err == nil || !strings.Contains(err.Error(), "pack byte") {
			t.Fatalf("pack limit: %v", err)
		}
	})
}

type companionCountingFile struct {
	companionPackFile
	stats, dirs *int
}

func (f *companionCountingFile) Stat() (fs.FileInfo, error) {
	*f.stats++
	return f.companionPackFile.Stat()
}
func (f *companionCountingFile) ReadDir(n int) ([]fs.DirEntry, error) {
	*f.dirs++
	return f.companionPackFile.ReadDir(n)
}
func TestCompanionPackIOCounts(t *testing.T) {
	home := t.TempDir()
	companionTestPack(t, home, "one", companionTestManifest("one", "stand.webp", ""))
	d := authFixture(t)
	d.config = &ConfigBackend{Options: wsconfig.Options{ConfigHome: home}}
	cookie := authCookie(t, d)
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(filepath.Join(d.dir, localhostSecretFile), old, old); err != nil {
		t.Fatal(err)
	}
	opens, stats, dirs := 0, 0, 0
	d.companionPacks.open = func(p companionPackFile, n string, d bool) (companionPackFile, error) {
		opens++
		f, err := openCompanionPackEntry(p, n, d)
		if err != nil {
			return nil, err
		}
		return &companionCountingFile{f, &stats, &dirs}, nil
	}
	if d.companionPacks.active.Load() != nil || opens != 0 {
		t.Fatal("pack store loaded before a request")
	}
	beforeStats, beforeReads := companionAuthIOCounts()
	if w := companionTestRequest(d, cookie, http.MethodGet, companionPacksRoute, ""); w.Code != http.StatusOK {
		t.Fatalf("cold catalog: %d %s", w.Code, w.Body)
	}
	// The fifth open is the install-sidecar lookup, absent for a hand-made pack.
	if opens != 5 || stats != 4 || dirs != 1 {
		t.Fatalf("cold opens=%d stats=%d readdir=%d", opens, stats, dirs)
	}
	afterStats, afterReads := companionAuthIOCounts()
	if afterStats-beforeStats != 1 || afterReads-beforeReads != 1 {
		t.Fatalf("cold auth stats=%d reads=%d", afterStats-beforeStats, afterReads-beforeReads)
	}
	opens, stats, dirs = 0, 0, 0
	for _, suffix := range []string{"", "/one/pack.json", "/one/assets/stand.webp"} {
		if w := companionTestRequest(d, cookie, http.MethodGet, companionPacksRoute+suffix, ""); w.Code != http.StatusOK {
			t.Fatalf("warm %s: %d %s", suffix, w.Code, w.Body)
		}
	}
	if opens != 0 || stats != 0 || dirs != 0 {
		t.Fatalf("warm opens=%d stats=%d readdir=%d", opens, stats, dirs)
	}
	warmStats, warmReads := companionAuthIOCounts()
	if warmStats-afterStats != 3 || warmReads-afterReads != 0 {
		t.Fatalf("warm auth stats=%d reads=%d", warmStats-afterStats, warmReads-afterReads)
	}
	t.Log("pack store cold: 5 opens (one a missing sidecar), 4 stats, 1 ReadDir. Warm: zero on catalog, manifest and asset.")
	t.Log("auth credential cold: 1 stat and 1 file read. Warm: 1 stat per request and zero file reads.")
}

func companionAuthIOCounts() (stats, reads int64) {
	for site, n := range wsstate.FileIOCounters() {
		if !strings.HasPrefix(site, "file_io_site:wsstate/read_cache.go:") {
			continue
		}
		switch {
		case strings.HasSuffix(site, ":stat"):
			stats += n
		case strings.HasSuffix(site, ":read_file"):
			reads += n
		}
	}
	return stats, reads
}
