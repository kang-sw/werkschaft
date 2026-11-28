package wspiruntime

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestResolveAssetPinnedTable(t *testing.T) {
	cases := []struct {
		goos, goarch string
		filename     string
		sha256       string
		kind         ArchiveKind
		bin          string
	}{
		{"darwin", "arm64", "pi-darwin-arm64.tar.gz", "56b5e194d485ced555667b8ba3276157a8272e9ecde9e001e428af80985bdd9b", ArchiveTarGz, "pi"},
		{"darwin", "amd64", "pi-darwin-x64.tar.gz", "f997a088613514ac97d913aeea7642094d84fe6d46854ff7e64e2c12e536dc50", ArchiveTarGz, "pi"},
		{"darwin", "x64", "pi-darwin-x64.tar.gz", "f997a088613514ac97d913aeea7642094d84fe6d46854ff7e64e2c12e536dc50", ArchiveTarGz, "pi"},
		{"linux", "amd64", "pi-linux-x64.tar.gz", "811c6f2c9e7aaee252b5404252e556fb713aa209c1f7f924226aa77d4ef70cb5", ArchiveTarGz, "pi"},
		{"linux", "arm64", "pi-linux-arm64.tar.gz", "42805cd5615952fcbbfc9c56b04f8feb84395134952e169a6cdc8d7676975666", ArchiveTarGz, "pi"},
		{"windows", "amd64", "pi-windows-x64.zip", "e6adb8cb8c84eca14391ad24b363de9d2be98a89ca3ba3fdee1fa4de32b7f2e8", ArchiveZip, "pi.exe"},
		{"windows", "arm64", "pi-windows-arm64.zip", "e83404bc5a6003ac950683edd9b6cf499dac91ec890d8439a8c32528de534a77", ArchiveZip, "pi.exe"},
	}
	for _, tc := range cases {
		spec, err := ResolveAsset(tc.goos, tc.goarch)
		if err != nil {
			t.Fatalf("ResolveAsset(%s, %s): %v", tc.goos, tc.goarch, err)
		}
		if spec.Filename != tc.filename || spec.SHA256 != tc.sha256 || spec.ArchiveKind != tc.kind || spec.BinaryName != tc.bin {
			t.Errorf("ResolveAsset(%s, %s) = %+v", tc.goos, tc.goarch, spec)
		}
	}
	if _, err := ResolveAsset("freebsd", "amd64"); err == nil {
		t.Fatal("expected unsupported platform error for freebsd/amd64")
	}
}

type tarEntry struct {
	name     string
	typeflag byte
	mode     int64
	body     string
	linkname string
}

func buildTarGz(t *testing.T, entries []tarEntry) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		tf := e.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			if tf == tar.TypeDir {
				mode = 0o755
			} else {
				mode = 0o644
			}
		}
		hdr := &tar.Header{
			Name:     e.name,
			Typeflag: tf,
			Mode:     mode,
			Size:     int64(len(e.body)),
			Linkname: e.linkname,
		}
		if tf != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", e.name, err)
		}
		if tf == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("tar body %s: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

type zipEntry struct {
	name string
	mode os.FileMode
	body string
	dir  bool
}

func buildZip(t *testing.T, entries []zipEntry) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		hdr := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		mode := e.mode
		if mode == 0 {
			if e.dir {
				mode = os.ModeDir | 0o755
			} else {
				mode = 0o644
			}
		}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatalf("zip create %s: %v", e.name, err)
		}
		if !e.dir {
			if _, err := w.Write([]byte(e.body)); err != nil {
				t.Fatalf("zip write %s: %v", e.name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

func standardTarPayload(t *testing.T, marker string) ([]byte, string) {
	t.Helper()
	return buildTarGz(t, []tarEntry{
		{name: "pi/", typeflag: tar.TypeDir, mode: 0o755},
		{name: "pi/pi", typeflag: tar.TypeReg, mode: 0o755, body: "#!/bin/sh\necho " + marker + "\n"},
		{name: "pi/package.json", body: `{"name":"pi","version":"1.5.2","marker":"` + marker + `"}`},
		{name: "pi/photon_rs_bg.wasm", body: "WASM-BYTES"},
		{name: "pi/theme/", typeflag: tar.TypeDir},
		{name: "pi/theme/dark.json", body: `{"name":"dark"}`},
		{name: "pi/export-html/", typeflag: tar.TypeDir},
		{name: "pi/export-html/index.html", body: "<html></html>"},
		{name: "pi/assets/", typeflag: tar.TypeDir},
		{name: "pi/assets/icon.txt", body: "icon"},
		{name: "pi/native/", typeflag: tar.TypeDir},
		{name: "pi/native/addon.node", body: "addon"},
	})
}

func newTestManager(t *testing.T, cacheRoot, baseURL, goos, goarch string, spec AssetSpec) *Manager {
	t.Helper()
	m := NewManager(Options{
		CacheRoot: cacheRoot,
		BaseURL:   baseURL,
		GOOS:      goos,
		GOARCH:    goarch,
	})
	m.resolveAsset = func(string, string) (AssetSpec, error) {
		return spec, nil
	}
	return m
}

func TestInstallTarGzPreservesAssetsAndAtomicReady(t *testing.T) {
	archive, digest := standardTarPayload(t, "v1")
	spec := AssetSpec{
		GOOS:        "darwin",
		GOARCH:      "arm64",
		Filename:    "pi-darwin-arm64.tar.gz",
		SHA256:      digest,
		ArchiveKind: ArchiveTarGz,
		BinaryName:  "pi",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(digest + "  " + spec.Filename + "\n"))
		case "/" + spec.Filename:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cacheRoot := t.TempDir()
	resolvedCacheRoot, err := filepath.EvalSymlinks(cacheRoot)
	if err != nil {
		resolvedCacheRoot = cacheRoot
	}
	m := newTestManager(t, cacheRoot, srv.URL, "darwin", "arm64", spec)

	// Deterministic BinaryPath before install.
	wantBin := filepath.Join(resolvedCacheRoot, "runtimes", "pi", "v1.5.2-darwin-arm64", "pi")
	if got := m.BinaryPath(); got != wantBin {
		t.Fatalf("BinaryPath() = %q, want %q", got, wantBin)
	}
	if st := m.Status(); st.Installed {
		t.Fatalf("expected uninstalled before Install, got %+v", st)
	}

	// Verify .ws-ready already exists inside stagingDir at the moment of rename.
	readySeenBeforeSwap := false
	m.renameDir = func(oldpath, newpath string) error {
		if strings.Contains(filepath.Base(oldpath), ".tmp-") && newpath == m.InstallDir() {
			if _, err := os.Stat(filepath.Join(oldpath, readyMarkerName)); err != nil {
				t.Errorf(".ws-ready missing in stagingDir before swap: %v", err)
			} else {
				readySeenBeforeSwap = true
			}
		}
		return os.Rename(oldpath, newpath)
	}

	var progressCalls atomic.Int32
	if err := m.Install(context.Background(), func(Progress) { progressCalls.Add(1) }); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !readySeenBeforeSwap {
		t.Fatal("expected swap hook to observe .ws-ready in stagingDir")
	}
	if progressCalls.Load() == 0 {
		t.Fatal("expected progress callback invocations")
	}

	st := m.Status()
	if !st.Installed || st.Version != PinnedVersion || st.BinaryPath != wantBin {
		t.Fatalf("unexpected status after install: %+v", st)
	}

	// Verify binary permissions and sibling assets.
	binInfo, err := os.Stat(wantBin)
	if err != nil {
		t.Fatalf("stat binary: %v", err)
	}
	if runtime.GOOS != "windows" && binInfo.Mode().Perm()&0o111 == 0 {
		t.Errorf("binary mode %o is not executable", binInfo.Mode().Perm())
	}
	for _, rel := range []string{
		"package.json",
		"photon_rs_bg.wasm",
		"theme/dark.json",
		"export-html/index.html",
		"assets/icon.txt",
		"native/addon.node",
		readyMarkerName,
	} {
		if _, err := os.Stat(filepath.Join(m.InstallDir(), filepath.FromSlash(rel))); err != nil {
			t.Errorf("expected sibling asset %s: %v", rel, err)
		}
	}
}

func TestInstallZipPreservesAssets(t *testing.T) {
	archive, digest := buildZip(t, []zipEntry{
		{name: "pi/", dir: true},
		{name: "pi/pi.exe", mode: 0o755, body: "MZ-FAKE-PE"},
		{name: "pi/package.json", body: `{"name":"pi"}`},
		{name: "pi/photon_rs_bg.wasm", body: "WASM"},
		{name: "pi/theme/", dir: true},
		{name: "pi/theme/light.json", body: `{}`},
		{name: "pi/native/", dir: true},
		{name: "pi/native/addon.node", body: "dll"},
	})
	spec := AssetSpec{
		GOOS:        "windows",
		GOARCH:      "amd64",
		Filename:    "pi-windows-x64.zip",
		SHA256:      digest,
		ArchiveKind: ArchiveZip,
		BinaryName:  "pi.exe",
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(digest + " *" + spec.Filename + "\n"))
		case "/" + spec.Filename:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cacheRoot := t.TempDir()
	m := newTestManager(t, cacheRoot, srv.URL, "windows", "amd64", spec)
	if err := m.Install(context.Background(), nil); err != nil {
		t.Fatalf("zip Install: %v", err)
	}
	if !strings.HasSuffix(m.BinaryPath(), "pi.exe") {
		t.Fatalf("BinaryPath = %q, want pi.exe suffix", m.BinaryPath())
	}
	for _, rel := range []string{"pi.exe", "package.json", "photon_rs_bg.wasm", "theme/light.json", "native/addon.node", readyMarkerName} {
		if _, err := os.Stat(filepath.Join(m.InstallDir(), filepath.FromSlash(rel))); err != nil {
			t.Errorf("missing extracted zip file %s: %v", rel, err)
		}
	}
}

func TestReinstallOverPopulatedDirAndRollbackOnSwapFailure(t *testing.T) {
	v1Archive, v1Digest := standardTarPayload(t, "v1")
	v2Archive, v2Digest := standardTarPayload(t, "v2")

	var currentArchive []byte
	var currentDigest string
	var mu sync.Mutex
	setPayload := func(a []byte, d string) {
		mu.Lock()
		currentArchive, currentDigest = a, d
		mu.Unlock()
	}
	setPayload(v1Archive, v1Digest)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		a, d := currentArchive, currentDigest
		mu.Unlock()
		switch r.URL.Path {
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(d + "  pi-darwin-arm64.tar.gz\n"))
		case "/pi-darwin-arm64.tar.gz":
			_, _ = w.Write(a)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cacheRoot := t.TempDir()
	m := NewManager(Options{CacheRoot: cacheRoot, BaseURL: srv.URL, GOOS: "darwin", GOARCH: "arm64"})
	m.resolveAsset = func(string, string) (AssetSpec, error) {
		mu.Lock()
		d := currentDigest
		mu.Unlock()
		return AssetSpec{
			GOOS: "darwin", GOARCH: "arm64",
			Filename: "pi-darwin-arm64.tar.gz", SHA256: d,
			ArchiveKind: ArchiveTarGz, BinaryName: "pi",
		}, nil
	}

	if err := m.Install(context.Background(), nil); err != nil {
		t.Fatalf("v1 Install: %v", err)
	}
	// Plant stale leftovers to confirm cleanup after successful reinstall.
	staleTmp := m.InstallDir() + ".tmp-stale"
	staleOld := m.InstallDir() + ".old-stale"
	_ = os.MkdirAll(staleTmp, 0o755)
	_ = os.MkdirAll(staleOld, 0o755)

	// Reinstall v2 with swap failure -> must roll back to v1 cleanly.
	setPayload(v2Archive, v2Digest)
	m.renameDir = func(oldpath, newpath string) error {
		if strings.Contains(filepath.Base(oldpath), ".tmp-") && newpath == m.InstallDir() {
			return errors.New("simulated swap rename failure")
		}
		return os.Rename(oldpath, newpath)
	}
	if err := m.Install(context.Background(), nil); err == nil {
		t.Fatal("expected swap failure")
	}
	raw, err := os.ReadFile(filepath.Join(m.InstallDir(), "package.json"))
	if err != nil {
		t.Fatalf("rollback lost installDir: %v", err)
	}
	if !strings.Contains(string(raw), `"marker":"v1"`) {
		t.Fatalf("expected v1 after rollback, got %s", raw)
	}

	// Now allow swap -> v2 replaces v1 and cleans up .old-* and stale .tmp-*.
	m.renameDir = nil
	if err := m.Install(context.Background(), nil); err != nil {
		t.Fatalf("v2 reinstall: %v", err)
	}
	raw, err = os.ReadFile(filepath.Join(m.InstallDir(), "package.json"))
	if err != nil {
		t.Fatalf("read v2 package.json: %v", err)
	}
	if !strings.Contains(string(raw), `"marker":"v2"`) {
		t.Fatalf("expected v2 after reinstall, got %s", raw)
	}
	entries, _ := os.ReadDir(filepath.Dir(m.InstallDir()))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") || strings.Contains(e.Name(), ".old-") {
			t.Errorf("leftover temp/backup entry remained: %s", e.Name())
		}
	}
}

func TestSHA256MismatchRejection(t *testing.T) {
	archive, digest := standardTarPayload(t, "good")
	badDigest := strings.Repeat("a", 64)
	spec := AssetSpec{
		GOOS: "linux", GOARCH: "amd64",
		Filename: "pi-linux-x64.tar.gz", SHA256: digest,
		ArchiveKind: ArchiveTarGz, BinaryName: "pi",
	}

	t.Run("tampered SHA256SUMS", func(t *testing.T) {
		var archiveHits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/SHA256SUMS" {
				_, _ = w.Write([]byte(badDigest + "  " + spec.Filename + "\n"))
				return
			}
			archiveHits.Add(1)
			_, _ = w.Write(archive)
		}))
		defer srv.Close()

		m := newTestManager(t, t.TempDir(), srv.URL, "linux", "amd64", spec)
		if err := m.Install(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "mismatch") {
			t.Fatalf("expected SHA256SUMS mismatch error, got %v", err)
		}
		if archiveHits.Load() != 0 {
			t.Fatalf("archive should not have been downloaded on SHA256SUMS mismatch (hits=%d)", archiveHits.Load())
		}
	})

	t.Run("tampered archive bytes", func(t *testing.T) {
		tampered := append([]byte(nil), archive...)
		tampered[len(tampered)-1] ^= 0xff
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/SHA256SUMS" {
				_, _ = w.Write([]byte(digest + "  " + spec.Filename + "\n"))
				return
			}
			_, _ = w.Write(tampered)
		}))
		defer srv.Close()

		m := newTestManager(t, t.TempDir(), srv.URL, "linux", "amd64", spec)
		if err := m.Install(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
			t.Fatalf("expected archive SHA-256 mismatch error, got %v", err)
		}
		if m.Status().Installed {
			t.Fatal("tampered install should not mark runtime installed")
		}
	})

	t.Run("non-200 status", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusBadGateway)
		}))
		defer srv.Close()

		m := newTestManager(t, t.TempDir(), srv.URL, "linux", "amd64", spec)
		if err := m.Install(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "502") {
			t.Fatalf("expected HTTP 502 error, got %v", err)
		}
	})
}

func TestArchivePathTraversalAndSymlinkEscapeRejected(t *testing.T) {
	tarCases := []struct {
		name    string
		entries []tarEntry
	}{
		{"dotdot", []tarEntry{{name: "pi/../../evil.txt", body: "x"}}},
		{"outside prefix", []tarEntry{{name: "other/evil.txt", body: "x"}}},
		{"absolute path", []tarEntry{{name: "/etc/passwd", body: "x"}}},
		{"drive prefix", []tarEntry{{name: "C:/Windows/evil.dll", body: "x"}}},
		{"escaping symlink", []tarEntry{
			{name: "pi/pi", body: "ok", mode: 0o755},
			{name: "pi/link", typeflag: tar.TypeSymlink, linkname: "../../outside"},
		}},
		{"absolute symlink", []tarEntry{
			{name: "pi/pi", body: "ok", mode: 0o755},
			{name: "pi/link", typeflag: tar.TypeSymlink, linkname: "/etc/passwd"},
		}},
		{"escaping hardlink", []tarEntry{
			{name: "pi/pi", body: "ok", mode: 0o755},
			{name: "pi/hl", typeflag: tar.TypeLink, linkname: "../outside"},
		}},
		{"chained symlink dir traversal", []tarEntry{
			{name: "pi/pi", body: "ok", mode: 0o755},
			{name: "pi/a/b/s1", typeflag: tar.TypeSymlink, linkname: ".."},
			{name: "pi/a/b/s1/s2", typeflag: tar.TypeSymlink, linkname: "../.."},
			{name: "pi/a/b/s1/s2/evil.txt", body: "pwned"},
		}},
		{"forward-referenced chained symlink escape", []tarEntry{
			{name: "pi/pi", body: "ok", mode: 0o755},
			{name: "pi/x/y/z/s2", typeflag: tar.TypeSymlink, linkname: "s1/../.."},
			{name: "pi/x/y/z/s1", typeflag: tar.TypeSymlink, linkname: "../.."},
		}},
		{"hardlink via symlink parent", []tarEntry{
			{name: "pi/pi", body: "ok", mode: 0o755},
			{name: "pi/a/real.txt", body: "real"},
			{name: "pi/a/b/s1", typeflag: tar.TypeSymlink, linkname: ".."},
			{name: "pi/hl", typeflag: tar.TypeLink, linkname: "pi/a/b/s1/real.txt"},
		}},
		{"special fifo node", []tarEntry{
			{name: "pi/pipe", typeflag: tar.TypeFifo},
		}},
	}
	for _, tc := range tarCases {
		t.Run("tar/"+tc.name, func(t *testing.T) {
			data, _ := buildTarGz(t, tc.entries)
			tmp := t.TempDir()
			arch := filepath.Join(tmp, "bad.tar.gz")
			if err := os.WriteFile(arch, data, 0o600); err != nil {
				t.Fatal(err)
			}
			staging := filepath.Join(tmp, "stage")
			if err := os.MkdirAll(staging, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := extractArchive(arch, ArchiveTarGz, staging, "pi"); err == nil {
				t.Fatalf("expected error for unsafe tar case %s", tc.name)
			}
		})
	}

	zipCases := []struct {
		name    string
		entries []zipEntry
	}{
		{"dotdot", []zipEntry{{name: "pi/../../evil.txt", body: "x"}}},
		{"absolute", []zipEntry{{name: "/etc/evil", body: "x"}}},
		{"drive prefix", []zipEntry{{name: "C:\\evil.exe", body: "x"}}},
		{"escaping symlink", []zipEntry{
			{name: "pi/pi.exe", body: "ok"},
			{name: "pi/link", mode: os.ModeSymlink | 0o777, body: "../../outside"},
		}},
		{"chained symlink dir traversal", []zipEntry{
			{name: "pi/pi.exe", body: "ok"},
			{name: "pi/a/b/s1", mode: os.ModeSymlink | 0o777, body: ".."},
			{name: "pi/a/b/s1/s2", mode: os.ModeSymlink | 0o777, body: "../.."},
			{name: "pi/a/b/s1/s2/evil.txt", body: "pwned"},
		}},
		{"forward-referenced chained symlink escape", []zipEntry{
			{name: "pi/pi.exe", body: "ok"},
			{name: "pi/x/y/z/s2", mode: os.ModeSymlink | 0o777, body: "s1/../.."},
			{name: "pi/x/y/z/s1", mode: os.ModeSymlink | 0o777, body: "../.."},
		}},
	}
	for _, tc := range zipCases {
		t.Run("zip/"+tc.name, func(t *testing.T) {
			data, _ := buildZip(t, tc.entries)
			tmp := t.TempDir()
			arch := filepath.Join(tmp, "bad.zip")
			if err := os.WriteFile(arch, data, 0o600); err != nil {
				t.Fatal(err)
			}
			staging := filepath.Join(tmp, "stage")
			if err := os.MkdirAll(staging, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := extractArchive(arch, ArchiveZip, staging, "pi.exe"); err == nil {
				t.Fatalf("expected error for unsafe zip case %s", tc.name)
			}
		})
	}
}

func TestSingleFlightConcurrentInstall(t *testing.T) {
	archive, digest := standardTarPayload(t, "sf")
	spec := AssetSpec{
		GOOS: "darwin", GOARCH: "arm64",
		Filename: "pi-darwin-arm64.tar.gz", SHA256: digest,
		ArchiveKind: ArchiveTarGz, BinaryName: "pi",
	}

	started := make(chan struct{})
	release := make(chan struct{})
	var archiveDownloads atomic.Int32
	var startOnce sync.Once

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(digest + "  " + spec.Filename + "\n"))
		case "/" + spec.Filename:
			archiveDownloads.Add(1)
			startOnce.Do(func() { close(started) })
			<-release
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := newTestManager(t, t.TempDir(), srv.URL, "darwin", "arm64", spec)

	const callers = 5
	errs := make([]error, callers)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(idx int) {
			defer wg.Done()
			errs[idx] = m.Install(context.Background(), nil)
		}(i)
	}

	<-started
	if st := m.Status(); !st.Installing {
		t.Errorf("expected Status().Installing == true during in-flight install, got %+v", st)
	}
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d error: %v", i, err)
		}
	}
	if got := archiveDownloads.Load(); got != 1 {
		t.Fatalf("archive downloaded %d times across %d concurrent callers, want 1", got, callers)
	}
	if st := m.Status(); !st.Installed || st.Installing {
		t.Fatalf("unexpected post-install status: %+v", st)
	}
}

func TestSingleFlightWaiterCancellationAllowsNewInstall(t *testing.T) {
	archive, digest := standardTarPayload(t, "cancel-recover")
	spec := AssetSpec{
		GOOS: "darwin", GOARCH: "arm64",
		Filename: "pi-darwin-arm64.tar.gz", SHA256: digest,
		ArchiveKind: ArchiveTarGz, BinaryName: "pi",
	}

	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	var shaHits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SHA256SUMS":
			if shaHits.Add(1) == 1 {
				close(firstEntered)
				<-releaseFirst
			}
			_, _ = w.Write([]byte(digest + "  " + spec.Filename + "\n"))
		case "/" + spec.Filename:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := newTestManager(t, t.TempDir(), srv.URL, "darwin", "arm64", spec)
	// Delay teardown of the first cancelled install so caller 2 arrives while
	// caller 1's executeInstall is still unwinding.
	teardownEntered := make(chan struct{})
	releaseTeardown := make(chan struct{})
	var firstTeardownOnce sync.Once
	m.removeAll = func(path string) error {
		firstTeardownOnce.Do(func() {
			close(teardownEntered)
			<-releaseTeardown
		})
		return os.RemoveAll(path)
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	err1Ch := make(chan error, 1)
	go func() {
		err1Ch <- m.Install(ctx1, nil)
	}()

	<-firstEntered
	cancel1()
	if err1 := <-err1Ch; !errors.Is(err1, context.Canceled) {
		t.Fatalf("caller 1 error = %v, want context.Canceled", err1)
	}

	// Start caller 2 while caller 1's server handler / teardown is still active.
	err2Ch := make(chan error, 1)
	go func() {
		err2Ch <- m.Install(context.Background(), nil)
	}()

	close(releaseFirst)
	// Caller 1 may fail before staging dir removal; unblock teardown if reached.
	select {
	case <-teardownEntered:
		close(releaseTeardown)
	default:
		close(releaseTeardown)
	}

	if err2 := <-err2Ch; err2 != nil {
		t.Fatalf("caller 2 failed after caller 1 cancelled: %v", err2)
	}
	if st := m.Status(); !st.Installed || st.Installing {
		t.Fatalf("post-recovery status = %+v, want installed", st)
	}
}

func TestInMemoryReadinessCacheAndUninstall(t *testing.T) {
	archive, digest := standardTarPayload(t, "cache")
	spec := AssetSpec{
		GOOS: "darwin", GOARCH: "arm64",
		Filename: "pi-darwin-arm64.tar.gz", SHA256: digest,
		ArchiveKind: ArchiveTarGz, BinaryName: "pi",
	}
	var httpHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpHits.Add(1)
		switch r.URL.Path {
		case "/SHA256SUMS":
			_, _ = w.Write([]byte(digest + "  " + spec.Filename + "\n"))
		case "/" + spec.Filename:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	m := newTestManager(t, t.TempDir(), srv.URL, "darwin", "arm64", spec)
	var diskProbes atomic.Int32
	m.statHook = func() { diskProbes.Add(1) }

	// Repeated Status calls before install must probe disk only once.
	for i := 0; i < 5; i++ {
		if m.Status().Installed {
			t.Fatal("expected uninstalled")
		}
	}
	if got := diskProbes.Load(); got != 1 {
		t.Fatalf("disk probes = %d, want 1", got)
	}

	if err := m.EnsureInstalled(context.Background(), nil); err != nil {
		t.Fatalf("EnsureInstalled: %v", err)
	}
	hitsAfterInstall := httpHits.Load()

	// EnsureInstalled and Status must both serve from memory without extra probes or downloads.
	for i := 0; i < 5; i++ {
		if err := m.EnsureInstalled(context.Background(), nil); err != nil {
			t.Fatalf("EnsureInstalled repeat: %v", err)
		}
		if !m.Status().Installed {
			t.Fatal("expected installed from cache")
		}
	}
	if httpHits.Load() != hitsAfterInstall {
		t.Fatalf("EnsureInstalled hit HTTP again (%d -> %d)", hitsAfterInstall, httpHits.Load())
	}
	if diskProbes.Load() != 1 {
		t.Fatalf("Status after install probed disk again (probes=%d)", diskProbes.Load())
	}

	// Extract bridge so we also verify Uninstall removes bridge files.
	bridgePath, err := m.BridgePath()
	if err != nil {
		t.Fatalf("BridgePath: %v", err)
	}
	if _, err := os.Stat(bridgePath); err != nil {
		t.Fatalf("bridge missing: %v", err)
	}

	// Uninstall removes installDir + bridge and resets in-memory cache cleanly.
	if err := m.Uninstall(context.Background()); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if st := m.Status(); st.Installed {
		t.Fatalf("expected uninstalled after Uninstall, got %+v", st)
	}
	if diskProbes.Load() != 1 {
		t.Fatalf("Status after Uninstall should use updated cache without disk probe (probes=%d)", diskProbes.Load())
	}
	if _, err := os.Stat(m.InstallDir()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("installDir still exists after Uninstall: %v", err)
	}
	if _, err := os.Stat(bridgePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("bridge file still exists after Uninstall: %v", err)
	}

	// Explicit Invalidate forces one fresh disk probe.
	m.Invalidate()
	_ = m.Status()
	if diskProbes.Load() != 2 {
		t.Fatalf("expected 2 disk probes after Invalidate, got %d", diskProbes.Load())
	}
}

func TestBridgePathContentHashedAndIdempotent(t *testing.T) {
	m := NewManager(Options{CacheRoot: t.TempDir()})
	p1, err := m.BridgePath()
	if err != nil {
		t.Fatalf("BridgePath 1: %v", err)
	}
	p2, err := m.BridgePath()
	if err != nil {
		t.Fatalf("BridgePath 2: %v", err)
	}
	if p1 != p2 {
		t.Fatalf("BridgePath changed: %q vs %q", p1, p2)
	}
	base := filepath.Base(p1)
	if !strings.HasPrefix(base, "bridge-") || !strings.HasSuffix(base, ".mjs") || len(base) != len("bridge-")+16+len(".mjs") {
		t.Fatalf("unexpected content-hashed bridge filename: %q", base)
	}
	raw, err := os.ReadFile(p1)
	if err != nil {
		t.Fatalf("read extracted bridge: %v", err)
	}
	if !bytes.Equal(raw, BridgeSource()) {
		t.Fatal("extracted bridge content differs from embedded source")
	}
	src := string(raw)
	for _, needle := range []string{
		"WS_MCP_SESSION_URL",
		"WS_MCP_SESSION_TOKEN",
		"ws-pi-rpc-bridge",
		"after_provider_response",
		"provider_response",
		"registerMcpServer",
	} {
		if !strings.Contains(src, needle) {
			t.Errorf("bridge source missing %q", needle)
		}
	}
}
