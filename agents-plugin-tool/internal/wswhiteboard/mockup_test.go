package wswhiteboard

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeMockupFixture(t *testing.T, root, relDir string, files map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(root, filepath.FromSlash(relDir))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for rel, content := range files {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, content, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestPanelClassificationMatrixAndMockupCapture(t *testing.T) {
	root := t.TempDir()
	writeMockupFixture(t, root, "ai-docs/assets/mockups/261126/demo", map[string][]byte{
		"index.html":       []byte("<!DOCTYPE html><html><head><link rel=\"stylesheet\" href=\"app.css\"></head><body><h1>Demo</h1><script src=\"app.js\"></script></body></html>"),
		"app.css":          []byte("body { color: #111; }"),
		"app.js":           []byte("fetch('data/items.json');"),
		"data/items.json":  []byte(`{"items":[1,2,3]}`),
		"ignored.bak":      []byte("non-allowlisted regular file should be skipped"),
	})
	writeMockupFixture(t, root, "panels", map[string][]byte{
		"frag.html":    []byte("<div class=\"card\">Fragment only</div>"),
		"bad-full.svg": []byte("<!DOCTYPE html><html><body>not svg</body></html>"),
		"bad-full.md":  []byte("<!DOCTYPE html><html><body>not md</body></html>"),
	})

	state := &State{}
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	env := Env{Now: now, SourceRoot: root}

	// 1. Full HTML document via src -> captured as mockup snapshot + Markdown fallback.
	out, probs := Apply(state, `<wb:topic name="Design">
		<wb:panel name="demo" recap="Interactive demo mockup" src="ai-docs/assets/mockups/261126/demo/index.html" />
	</wb:topic>`, env)
	if len(probs) != 0 || out.Accepted != 1 {
		t.Fatalf("mockup panel failed: accepted=%d probs=%v", out.Accepted, probs)
	}
	if len(out.MockupBundles) != 1 {
		t.Fatalf("expected 1 mockup bundle, got %d", len(out.MockupBundles))
	}
	bundle := out.MockupBundles[0]
	if bundle.RootFile != "index.html" || bundle.FileCount != 4 {
		t.Fatalf("unexpected bundle metadata: %+v", bundle)
	}
	files, _, err := DecodeMockupZip(bundle.Zip)
	if err != nil {
		t.Fatalf("DecodeMockupZip: %v", err)
	}
	if _, skipped := files["ignored.bak"]; skipped {
		t.Fatalf("non-allowlisted regular file must be skipped")
	}
	for _, want := range []string{"index.html", "app.css", "app.js", "data/items.json"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("missing bundled file %q", want)
		}
	}
	var entry Entry
	for _, e := range state.Topics[0].Entries {
		if e.Kind == EntryPanel {
			entry = e
		}
	}
	if entry.Mockup == nil || entry.Mockup.Root != "index.html" || entry.Mockup.SHA256 != bundle.SHA256 {
		t.Fatalf("entry.Mockup = %+v, want root=index.html sha=%s", entry.Mockup, bundle.SHA256)
	}
	if bundle.EntryID != entry.ID {
		t.Fatalf("bundle.EntryID = %q, want %q", bundle.EntryID, entry.ID)
	}
	if !strings.Contains(entry.Text, "index.html") {
		t.Fatalf("expected readable Markdown fallback in entry.Text, got %q", entry.Text)
	}

	// 2. HTML fragment via src stays an ordinary fragment panel.
	out, probs = Apply(state, `<wb:topic name="Design">
		<wb:panel name="frag" recap="Plain fragment" src="panels/frag.html" />
	</wb:topic>`, env)
	if len(probs) != 0 || len(out.MockupBundles) != 0 {
		t.Fatalf("fragment panel should not capture bundle: probs=%v bundles=%d", probs, len(out.MockupBundles))
	}
	for _, e := range state.Topics[0].Entries {
		if e.PanelName == "frag" && e.Mockup != nil {
			t.Fatalf("fragment panel unexpectedly marked as mockup")
		}
	}

	// 3. .svg, .md, and inline full HTML documents remain refused.
	for _, tc := range []struct {
		name string
		cmd  string
	}{
		{"svg full doc", `<wb:topic name="Design"><wb:panel name="badsvg" recap="r" src="panels/bad-full.svg" /></wb:topic>`},
		{"md full doc", `<wb:topic name="Design"><wb:panel name="badmd" recap="r" src="panels/bad-full.md" /></wb:topic>`},
		{"inline full doc", `<wb:topic name="Design"><wb:panel name="badin" recap="r"><!DOCTYPE html><html><body>hi</body></html></wb:panel></wb:topic>`},
	} {
		out, probs := Apply(state, tc.cmd, env)
		if len(probs) == 0 || out.Accepted != 0 || len(out.MockupBundles) != 0 {
			t.Errorf("%s: expected refusal, got accepted=%d probs=%v bundles=%d", tc.name, out.Accepted, probs, len(out.MockupBundles))
		}
	}

	// 4. wb:change patch on a mockup panel is refused.
	out, probs = Apply(state, `<wb:topic name="Design">
		<wb:panel name="demo" recap="Patch attempt"><wb:change><wb:old>index.html</wb:old><wb:new>other.html</wb:new></wb:change></wb:panel>
	</wb:topic>`, env)
	if len(probs) == 0 || out.Accepted != 0 {
		t.Fatalf("expected wb:change on mockup panel to fail, got accepted=%d probs=%v", out.Accepted, probs)
	}
	if !strings.Contains(strings.Join(probs, " "), "mockup snapshot") {
		t.Fatalf("unexpected wb:change error: %v", probs)
	}

	// 5. Re-posting mockup by same name creates new version and skips diffPanel.
	writeMockupFixture(t, root, "ai-docs/assets/mockups/261126/demo", map[string][]byte{
		"index.html": []byte("<!DOCTYPE html><html><body><h1>Demo v2</h1></body></html>"),
	})
	out, probs = Apply(state, `<wb:topic name="Design">
		<wb:panel name="demo" recap="Updated demo v2" src="ai-docs/assets/mockups/261126/demo/index.html" />
	</wb:topic>`, Env{Now: now.Add(time.Minute), SourceRoot: root})
	if len(probs) != 0 || len(out.MockupBundles) != 1 {
		t.Fatalf("re-post mockup failed: probs=%v bundles=%d", probs, len(out.MockupBundles))
	}
	view := BuildView(state, ViewInput{Layout: LayoutFull})
	for _, run := range view.Topics[0].Stream.Runs {
		if run.Entry.Kind == EntryPanel && run.Entry.PanelName == "demo" && !run.Entry.Historical {
			if run.Entry.Diff != nil {
				t.Fatalf("expected mockup panel re-post to skip diffPanel, got %+v", run.Entry.Diff)
			}
			if run.Entry.Version != 2 {
				t.Fatalf("expected version 2, got %d", run.Entry.Version)
			}
		}
	}
}

func TestCollectMockupBundleSecurityAndCaps(t *testing.T) {
	t.Run("deterministic zip and sha256", func(t *testing.T) {
		root := t.TempDir()
		writeMockupFixture(t, root, "mock/a", map[string][]byte{
			"index.html": []byte("<!DOCTYPE html><html><body>A</body></html>"),
			"b.css":      []byte("body{}"),
			"a.js":       []byte("console.log(1);"),
		})
		b1, err := CollectMockupBundle(root, "mock/a/index.html")
		if err != nil {
			t.Fatal(err)
		}
		b2, err := CollectMockupBundle(root, "mock/a/index.html")
		if err != nil {
			t.Fatal(err)
		}
		if b1.SHA256 != b2.SHA256 || !bytes.Equal(b1.Zip, b2.Zip) {
			t.Fatalf("CollectMockupBundle not deterministic: %s vs %s", b1.SHA256, b2.SHA256)
		}
	})

	t.Run("refuses src directly in session root", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("<!DOCTYPE html><html></html>"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := CollectMockupBundle(root, "index.html"); err == nil {
			t.Fatal("expected error for root-level mockup")
		}
	})

	t.Run("refuses dot files and dot dirs", func(t *testing.T) {
		for _, bad := range []string{".env", ".DS_Store", ".git/config", "sub/.hidden.js"} {
			root := t.TempDir()
			writeMockupFixture(t, root, "mock/dot", map[string][]byte{
				"index.html": []byte("<!DOCTYPE html><html></html>"),
				bad:          []byte("secret"),
			})
			if _, err := CollectMockupBundle(root, "mock/dot/index.html"); err == nil {
				t.Errorf("expected dot-named %q to be refused", bad)
			}
		}
		root := t.TempDir()
		writeMockupFixture(t, root, ".hidden/mock", map[string][]byte{
			"index.html": []byte("<!DOCTYPE html><html></html>"),
		})
		if _, err := CollectMockupBundle(root, ".hidden/mock/index.html"); err == nil {
			t.Error("expected dot-named ancestor folder to be refused")
		}
	})

	t.Run("refuses symlinks", func(t *testing.T) {
		root := t.TempDir()
		outside := filepath.Join(t.TempDir(), "secret.txt")
		if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
			t.Fatal(err)
		}
		dir := writeMockupFixture(t, root, "mock/sym", map[string][]byte{
			"index.html": []byte("<!DOCTYPE html><html></html>"),
		})
		if err := os.Symlink(outside, filepath.Join(dir, "leak.txt")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if _, err := CollectMockupBundle(root, "mock/sym/index.html"); err == nil {
			t.Fatal("expected symlink inside mockup folder to be refused")
		}
	})

	t.Run("non-allowlisted files uncounted toward file and byte caps", func(t *testing.T) {
		root := t.TempDir()
		files := map[string][]byte{
			"index.html": []byte("<!DOCTYPE html><html></html>"),
		}
		// Add 300 non-allowlisted files (> 256 limit) and a 9 MiB non-allowlisted file (> 8 MiB limit).
		for i := 0; i < 260; i++ {
			files[filepath.ToSlash(filepath.Join("raw", strings.Repeat("a", 1)+string(rune('0'+i%10))+string(rune('a'+(i/10)%26))+string(rune('a'+(i/260)%26))+".bak"))] = []byte("x")
		}
		files["huge.bin"] = bytes.Repeat([]byte("Z"), int(MockupMaxBundleBytes)+1024)
		writeMockupFixture(t, root, "mock/uncounted", files)
		bundle, err := CollectMockupBundle(root, "mock/uncounted/index.html")
		if err != nil {
			t.Fatalf("non-allowlisted files must not trigger caps: %v", err)
		}
		if bundle.FileCount != 1 {
			t.Fatalf("FileCount = %d, want 1", bundle.FileCount)
		}
	})

	t.Run("enforces file count cap", func(t *testing.T) {
		root := t.TempDir()
		files := map[string][]byte{
			"index.html": []byte("<!DOCTYPE html><html></html>"),
		}
		for i := 0; i < MockupMaxFiles; i++ {
			files[filepath.ToSlash(filepath.Join("f", "a"+string(rune('a'+i%26))+string(rune('a'+(i/26)%26))+".txt"))] = []byte("1")
		}
		writeMockupFixture(t, root, "mock/many", files)
		if _, err := CollectMockupBundle(root, "mock/many/index.html"); err == nil {
			t.Fatal("expected file count cap error")
		}
	})

	t.Run("enforces byte size cap", func(t *testing.T) {
		root := t.TempDir()
		writeMockupFixture(t, root, "mock/big", map[string][]byte{
			"index.html": []byte("<!DOCTYPE html><html></html>"),
			"big.js":     bytes.Repeat([]byte("A"), int(MockupMaxBundleBytes)),
		})
		if _, err := CollectMockupBundle(root, "mock/big/index.html"); err == nil {
			t.Fatal("expected byte size cap error")
		}
	})
}

func TestBoardDBMockupPersistenceArchiveAndFallback(t *testing.T) {
	root := t.TempDir()
	writeMockupFixture(t, root, "ai-docs/assets/mockups/261126/card", map[string][]byte{
		"index.html": []byte("<!DOCTYPE html><html><body>Card v1</body></html>"),
		"style.css":  []byte(".card{padding:8px}"),
	})

	boardDir := filepath.Join(t.TempDir(), "board")
	state := &State{}
	env := Env{Now: time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC), SourceRoot: root}
	out, probs := Apply(state, `<wb:topic name="UI"><wb:panel name="card" recap="Card mockup" src="ai-docs/assets/mockups/261126/card/index.html" /></wb:topic>`, env)
	if len(probs) != 0 || len(out.MockupBundles) != 1 {
		t.Fatalf("Apply failed: probs=%v bundles=%d", probs, len(out.MockupBundles))
	}
	entryID := out.MockupBundles[0].EntryID
	wantSHA := out.MockupBundles[0].SHA256

	var mockupReads atomic.Int32
	db, err := CreateBoardDBWithBundles(boardDir, state, out.MockupBundles, BoardDBOptions{
		Observe: func(stage string) {
			if stage == "mockup-read" {
				mockupReads.Add(1)
			}
		},
	})
	if err != nil {
		t.Fatalf("CreateBoardDBWithBundles: %v", err)
	}

	// In-memory lookup must not touch SQLite.
	rootFile, sha, ok := db.LookupMockupMeta(entryID)
	if !ok || rootFile != "index.html" || sha != wantSHA {
		t.Fatalf("LookupMockupMeta = (%q,%q,%v)", rootFile, sha, ok)
	}
	if mockupReads.Load() != 0 {
		t.Fatalf("LookupMockupMeta must not query SQLite, got %d reads", mockupReads.Load())
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopening populates mockup index without reading blobs.
	mockupReads.Store(0)
	var evictedDir string
	db, err = OpenBoardDB(boardDir, nil, BoardDBOptions{
		Observe: func(stage string) {
			if stage == "mockup-read" {
				mockupReads.Add(1)
			}
		},
		OnLoadArchive: func(dir string) { evictedDir = dir },
	})
	if err != nil {
		t.Fatalf("OpenBoardDB: %v", err)
	}
	defer db.Close()
	if mockupReads.Load() != 0 {
		t.Fatalf("OpenBoardDB must not read bundle blobs, got %d", mockupReads.Load())
	}
	got, found, err := db.ReadMockupBundle(entryID)
	if err != nil || !found || got.SHA256 != wantSHA {
		t.Fatalf("ReadMockupBundle = (%+v, %v, %v)", got, found, err)
	}
	if mockupReads.Load() != 1 {
		t.Fatalf("expected 1 mockup-read, got %d", mockupReads.Load())
	}

	// Export archive includes _mockups/<entryID>.zip and round-trips through LoadNewBoardDB & LoadArchive.
	archiveBytes, err := db.ExportArchive()
	if err != nil {
		t.Fatalf("ExportArchive: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		t.Fatal(err)
	}
	foundMockupZip := false
	for _, f := range zr.File {
		if f.Name == "_mockups/"+entryID+".zip" {
			foundMockupZip = true
		}
	}
	if !foundMockupZip {
		t.Fatalf("ExportArchive missing _mockups/%s.zip", entryID)
	}

	// Add orphan and sha-mismatched zip entries into a copy of the archive to verify graceful skip.
	var patched bytes.Buffer
	zw := zip.NewWriter(&patched)
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		_ = rc.Close()
		w, err := zw.Create(f.Name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(b.Bytes())
	}
	// Orphan entry (no e999 in board).
	ow, _ := zw.Create("_mockups/e999.zip")
	_, _ = ow.Write([]byte("not-even-a-zip"))
	_ = zw.Close()

	zipPath := filepath.Join(t.TempDir(), "board.zip")
	if err := os.WriteFile(zipPath, patched.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	// LoadNewBoardDB imports matching bundle and skips orphan.
	loadedDir := filepath.Join(t.TempDir(), "loaded")
	loaded, _, err := LoadNewBoardDB(loadedDir, zipPath, BoardDBOptions{})
	if err != nil {
		t.Fatalf("LoadNewBoardDB: %v", err)
	}
	defer loaded.Close()
	if _, sha2, ok := loaded.LookupMockupMeta(entryID); !ok || sha2 != wantSHA {
		t.Fatalf("loaded board missing mockup %s: ok=%v sha=%q", entryID, ok, sha2)
	}
	if _, _, ok := loaded.LookupMockupMeta("e999"); ok {
		t.Fatalf("orphan e999 must not be imported")
	}

	// LoadArchive triggers OnLoadArchive and writes backup containing _mockups/<entryID>.zip.
	backupDir, _, err := db.LoadArchive(zipPath, time.Date(2026, 11, 26, 12, 30, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadArchive: %v", err)
	}
	if evictedDir != boardDir {
		t.Fatalf("OnLoadArchive dir = %q, want %q", evictedDir, boardDir)
	}
	if _, err := os.Stat(filepath.Join(backupDir, "_mockups", entryID+".zip")); err != nil {
		t.Fatalf("backup missing _mockups/%s.zip: %v", entryID, err)
	}
	// Opening the legacy backup directory directly recovers the mockup bundle.
	bakDB, err := OpenBoardDB(backupDir, nil, BoardDBOptions{})
	if err != nil {
		t.Fatalf("OpenBoardDB(backupDir): %v", err)
	}
	defer bakDB.Close()
	if _, shaBak, ok := bakDB.LookupMockupMeta(entryID); !ok || shaBak != wantSHA {
		t.Fatalf("backup recovery missing mockup: ok=%v sha=%q", ok, shaBak)
	}

	// ExportArchive fails loudly when total uncompressed bytes exceed archiveMaxBytes.
	prevCap := archiveMaxBytes
	archiveMaxBytes = 32
	defer func() { archiveMaxBytes = prevCap }()
	if _, err := db.ExportArchive(); err == nil {
		t.Fatal("expected ExportArchive to fail when exceeding archiveMaxBytes")
	}
}

func TestMockupPanelRenderingAndFallback(t *testing.T) {
	sha := hex.EncodeToString(sha256.New().Sum(nil))
	state := &State{
		Seq:          1,
		EntrySeq:     1,
		PanelCounter: 1,
		Topics: []TopicDoc{{
			Name: "Design",
			Entries: []Entry{{
				ID:        "e1",
				Kind:      EntryPanel,
				PanelID:   "p1",
				PanelName: "demo",
				Recap:     "Mockup recap",
				Text:      "Interactive HTML mockup snapshot from `ai-docs/assets/mockups/261126/demo/index.html` (`index.html`).",
				At:        "2026-11-26T12:00:00Z",
				Mockup:    &MockupMeta{Root: "index.html", SHA256: sha},
			}},
		}},
	}

	// Entry JSON round-trip preserves Mockup without duplicating into unknown.
	raw, err := json.Marshal(state.Topics[0].Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	var roundTripped Entry
	if err := json.Unmarshal(raw, &roundTripped); err != nil {
		t.Fatal(err)
	}
	if roundTripped.Mockup == nil || roundTripped.Mockup.Root != "index.html" || roundTripped.Mockup.SHA256 != sha || len(roundTripped.unknown) != 0 {
		t.Fatalf("Entry JSON round-trip mismatch: %+v unknown=%v", roundTripped.Mockup, roundTripped.unknown)
	}

	// Available bundle -> renders .mockup-wrap placeholder with stable /board/mockup URL and strict sandbox.
	regAvailable, err := RenderRegions(BuildView(state, ViewInput{
		Layout: LayoutFull,
		MockupSrc: func(entryID, gotSHA string) string {
			if entryID == "e1" && gotSHA == sha {
				return "/s/rid1/board/mockup?entry=e1"
			}
			return ""
		},
	}))
	if err != nil {
		t.Fatal(err)
	}
	htmlAvailable := string(regAvailable.Board)
	for _, want := range []string{
		`class="mockup-wrap"`,
		`class="doc-frame mockup-frame"`,
		`data-doc-key="entry:e1"`,
		`data-mockup-src="/s/rid1/board/mockup?entry=e1"`,
		`data-mockup-sha="` + sha + `"`,
		`data-sandbox="allow-scripts allow-popups allow-popups-to-escape-sandbox"`,
		`data-fallback-text="Interactive HTML mockup snapshot`,
	} {
		if !strings.Contains(htmlAvailable, want) {
			t.Errorf("rendered mockup HTML missing %q", want)
		}
	}
	for _, forbid := range []string{"allow-same-origin", "/fs/mock/"} {
		if strings.Contains(htmlAvailable, forbid) {
			t.Errorf("rendered mockup HTML must not contain %q", forbid)
		}
	}

	// Missing bundle -> renders fallback note + Markdown text.
	regMissing, err := RenderRegions(BuildView(state, ViewInput{
		Layout:    LayoutFull,
		MockupSrc: func(string, string) string { return "" },
	}))
	if err != nil {
		t.Fatal(err)
	}
	htmlMissing := string(regMissing.Board)
	if !strings.Contains(htmlMissing, "Mockup snapshot unavailable.") || strings.Contains(htmlMissing, "data-mockup-src=") {
		t.Fatalf("missing bundle should render fallback note without mockup iframe: %s", htmlMissing)
	}
}

func TestMockupMaxCapIncompressibleArchiveRoundTrip(t *testing.T) {
	// 256 incompressible allowlisted files with ~100-char nested paths totaling
	// exactly MockupMaxBundleBytes uncompressed exceed 64 KiB of zip container
	// overhead; export, LoadNewBoardDB, LoadArchive, and backup recovery must
	// all accept the valid bundle.
	root := t.TempDir()
	indexHTML := []byte("<!DOCTYPE html><html><body>max-cap</body></html>")
	rem := int(MockupMaxBundleBytes) - len(indexHTML)
	const otherCount = MockupMaxFiles - 1
	perFile := rem / otherCount
	extra := rem % otherCount

	files := map[string][]byte{
		"index.html": indexHTML,
	}
	var x uint64 = 0x9e3779b97f4a7c15
	nextBytes := func(n int) []byte {
		buf := make([]byte, n)
		for i := 0; i < n; i++ {
			x ^= x << 13
			x ^= x >> 7
			x ^= x << 17
			buf[i] = byte(x)
		}
		return buf
	}
	nestedPrefix := "assets/nested-component-directory-with-long-name/sub-segment-for-header-overhead/file-"
	for i := 0; i < otherCount; i++ {
		sz := perFile
		if i == 0 {
			sz += extra
		}
		rel := nestedPrefix + strings.Repeat("0", 3-len(string(rune('0'+i%10)))) +
			string([]byte{byte('a' + (i/26)%26), byte('a' + i%26)}) + ".png"
		files[rel] = nextBytes(sz)
	}
	writeMockupFixture(t, root, "mock/maxcap", files)

	state := &State{}
	outcome, problems := Apply(state, `<wb:topic name="MaxCap"><wb:panel name="m" recap="MaxCap" src="mock/maxcap/index.html" /></wb:topic>`, Env{
		Now:        time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
		SourceRoot: root,
	})
	if len(problems) > 0 {
		t.Fatalf("Apply problems: %v", problems)
	}
	boardDir := filepath.Join(t.TempDir(), "board")
	db, err := CreateBoardDBWithBundles(boardDir, state, outcome.MockupBundles, BoardDBOptions{})
	if err != nil {
		t.Fatalf("CreateBoardDBWithBundles: %v", err)
	}
	defer db.Close()
	if len(outcome.MockupBundles) != 1 || outcome.MockupBundles[0].FileCount != MockupMaxFiles || outcome.MockupBundles[0].ByteSize != MockupMaxBundleBytes {
		t.Fatalf("unexpected bundle stats: %+v", outcome.MockupBundles)
	}
	if int64(len(outcome.MockupBundles[0].Zip)) <= MockupMaxBundleBytes+(64<<10) {
		t.Fatalf("expected incompressible 256-file zip (%d bytes) to exceed 8MiB+64KiB so the regression is genuinely exercised", len(outcome.MockupBundles[0].Zip))
	}

	archiveBytes, err := db.ExportArchive()
	if err != nil {
		t.Fatalf("ExportArchive: %v", err)
	}
	zipPath := filepath.Join(t.TempDir(), "maxcap.zip")
	if err := os.WriteFile(zipPath, archiveBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	// LoadNewBoardDB round-trip.
	freshDir := filepath.Join(t.TempDir(), "fresh")
	db2, _, err := LoadNewBoardDB(freshDir, zipPath, BoardDBOptions{})
	if err != nil {
		t.Fatalf("LoadNewBoardDB: %v", err)
	}
	defer db2.Close()
	got, ok, err := db2.ReadMockupBundle("e1")
	if err != nil || !ok || got.SHA256 != outcome.MockupBundles[0].SHA256 {
		t.Fatalf("LoadNewBoardDB ReadMockupBundle = (%+v, %v, %v)", got, ok, err)
	}

	// LoadArchive round-trip + backup folder recovery via OpenBoardDB.
	backupDir, _, err := db2.LoadArchive(zipPath, time.Date(2026, 11, 26, 13, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("LoadArchive: %v", err)
	}
	if backupDir == "" {
		t.Fatal("expected backup dir from LoadArchive")
	}
	backupDB, err := OpenBoardDB(backupDir, nil, BoardDBOptions{})
	if err != nil {
		t.Fatalf("OpenBoardDB(backupDir): %v", err)
	}
	defer backupDB.Close()
	gotBackup, ok, err := backupDB.ReadMockupBundle("e1")
	if err != nil || !ok || gotBackup.SHA256 != outcome.MockupBundles[0].SHA256 {
		t.Fatalf("backup OpenBoardDB ReadMockupBundle = (%+v, %v, %v)", gotBackup, ok, err)
	}
}
