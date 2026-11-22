package wsdashboard

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// companionZipEntry describes one entry of a generated test archive.
type companionZipEntry struct {
	name   string
	data   []byte
	method uint16      // zip.Deflate unless store is set
	store  bool        // write with zip.Store
	mode   fs.FileMode // 0 selects a regular 0644 file
	flags  uint16      // raw general-purpose flags (0x1 marks encryption)
}

func companionTestZip(t testing.TB, entries ...companionZipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		if e.flags != 0 || (e.method != 0 && e.method != zip.Store && e.method != zip.Deflate) {
			// CreateRaw keeps flags and an unregistered method verbatim; the
			// payload is written as is.
			fh := &zip.FileHeader{Name: e.name, Method: e.method, Flags: e.flags,
				CRC32: crc32.ChecksumIEEE(e.data), CompressedSize64: uint64(len(e.data)), UncompressedSize64: uint64(len(e.data))}
			out, err := w.CreateRaw(fh)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := out.Write(e.data); err != nil {
				t.Fatal(err)
			}
			continue
		}
		fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.store {
			fh.Method = zip.Store
		}
		if e.method != 0 {
			fh.Method = e.method
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		if strings.HasSuffix(e.name, "/") {
			mode = fs.ModeDir | 0o755
			fh.Method = zip.Store
		}
		fh.SetMode(mode)
		out, err := w.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		if len(e.data) > 0 {
			if _, err := out.Write(e.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func companionTestPNG(t testing.TB, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7)
	}
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// companionTestWebP returns a lossless WebP header carrying the given size;
// the pipeline reads only the header.
func companionTestWebP(width, height int) []byte {
	bits := uint32(width-1) | uint32(height-1)<<14
	chunk := []byte{0x2f, byte(bits), byte(bits >> 8), byte(bits >> 16), byte(bits >> 24), 0, 0, 0}
	out := []byte("RIFF\x00\x00\x00\x00WEBPVP8L")
	out = binary.LittleEndian.AppendUint32(out, uint32(len(chunk)))
	out = append(out, chunk...)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(out)-8))
	return out
}

func companionTestMarket(entries ...string) []byte {
	return []byte(fmt.Sprintf(`{"schema":"ws-companion-marketplace/v1","name":"Fixture Packs","owner":{"name":"Fixture Author","url":"https://example.invalid/"},"packs":[%s],"future":{"ignored":true}}`, strings.Join(entries, ",")))
}

func companionTestEntry(id, dir string) string {
	return fmt.Sprintf(`{"id":%q,"name":"Pack %s","version":"1.0.0","author":"Fixture Author","license":"CC0-1.0","path":%q}`, id, id, dir)
}

// companionTestBundle is a self-contained marketplace with one valid pack per
// ID under packs/<id>/, each referencing stand.png.
func companionTestBundle(t testing.TB, prefix string, ids ...string) []companionZipEntry {
	var listed []string
	var entries []companionZipEntry
	for _, id := range ids {
		listed = append(listed, companionTestEntry(id, "packs/"+id))
		entries = append(entries,
			companionZipEntry{name: prefix + "packs/" + id + "/pack.json", data: companionTestManifest(id, "stand.png", "")},
			companionZipEntry{name: prefix + "packs/" + id + "/stand.png", data: companionTestPNG(t, 8, 8)})
	}
	return append([]companionZipEntry{{name: prefix + "marketplace.json", data: companionTestMarket(listed...)}}, entries...)
}

func companionTestInstallOrigin() companionInstallOrigin {
	return companionInstallOrigin{Source: "fixture.zip", Kind: "local", ArchiveSHA256: strings.Repeat("ab", 32)}
}

// companionTestRun opens data and runs an install or dry run of ids (nil
// selects every listed pack).
func companionTestRun(store *CompanionPackStore, home string, data []byte, ids []string, dryRun bool) (companionInstallResult, error) {
	return companionTestRunInflate(store, home, data, ids, dryRun, newCompanionInflate())
}

func companionTestRunInflate(store *CompanionPackStore, home string, data []byte, ids []string, dryRun bool, inflate *companionInflate) (companionInstallResult, error) {
	archive, err := openCompanionArchive(data, inflate)
	if err != nil {
		return companionInstallResult{}, err
	}
	market, err := archive.marketplace()
	if err != nil {
		return companionInstallResult{}, err
	}
	return store.install(wsconfig.Options{ConfigHome: home}, companionInstallPlan{archive: archive, market: market, origin: companionTestInstallOrigin()}, ids, dryRun)
}

func companionListed(result companionInstallResult, id string) *companionListingPack {
	for i := range result.Listing.Packs {
		if result.Listing.Packs[i].ID == id {
			return &result.Listing.Packs[i]
		}
	}
	return nil
}

func companionDirFiles(t testing.TB, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names
}

func TestCompanionPackInstallRejectsHostileArchives(t *testing.T) {
	png8 := companionTestPNG(t, 8, 8)
	pack := func(files ...companionZipEntry) []companionZipEntry {
		base := []companionZipEntry{
			{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
			{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "stand.png", "")},
		}
		return append(base, files...)
	}
	stand := companionZipEntry{name: "packs/alpha/stand.png", data: png8}
	manyLayers := func(n int, file func(int) string) []byte {
		layers := make([]string, n)
		for i := range layers {
			layers[i] = fmt.Sprintf(`{"file":%q}`, file(i))
		}
		return []byte(fmt.Sprintf(`{"schema":"ws-companion/v1","id":"alpha","name":"alpha","canvas":[512,512],"poses":{"stand":{"layers":[%s]}}}`, strings.Join(layers, ",")))
	}
	var tooMany []companionZipEntry
	tooMany = append(tooMany, pack(stand)...)
	for i := 0; len(tooMany) <= companionZipEntryMax; i++ {
		tooMany = append(tooMany, companionZipEntry{name: fmt.Sprintf("extra/%d.txt", i), store: true})
	}
	packTooMany := pack(stand)
	for i := 0; i < companionPackEntryMax; i++ {
		packTooMany = append(packTooMany, companionZipEntry{name: fmt.Sprintf("packs/alpha/sub%d/", i)})
	}
	bigAsset := make([]byte, companionAssetCap+1)
	var packCap []companionZipEntry
	packCap = append(packCap, companionZipEntry{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
		companionZipEntry{name: "packs/alpha/pack.json", data: manyLayers(9, func(i int) string { return fmt.Sprintf("l%d.png", i) })})
	for i := 0; i < 9; i++ {
		packCap = append(packCap, companionZipEntry{name: fmt.Sprintf("packs/alpha/l%d.png", i), data: make([]byte, companionAssetCap-16), store: true})
	}
	twoIDs := func(market []byte) []companionZipEntry {
		return []companionZipEntry{{name: "marketplace.json", data: market},
			{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "stand.png", "")}, stand}
	}

	cases := []struct {
		name    string
		entries []companionZipEntry
	}{
		{"traversal", pack(stand, companionZipEntry{name: "packs/alpha/../../evil.png", data: png8})},
		{"absolute path", pack(stand, companionZipEntry{name: "/etc/evil.png", data: png8})},
		{"backslash", pack(stand, companionZipEntry{name: `packs\alpha\evil.png`, data: png8})},
		{"drive letter", pack(stand, companionZipEntry{name: "C:/evil.png", data: png8})},
		{"non-clean name", pack(stand, companionZipEntry{name: "packs/alpha/./evil.png", data: png8})},
		{"duplicate names", pack(stand, stand)},
		{"case collision", pack(stand, companionZipEntry{name: "packs/alpha/Stand.png", data: png8})},
		{"symlink entry", pack(companionZipEntry{name: "packs/alpha/stand.png", data: []byte("/etc/passwd"), mode: fs.ModeSymlink | 0o777})},
		{"nested directory file", pack(stand, companionZipEntry{name: "packs/alpha/sub/evil.png", data: png8})},
		{"unknown file", pack(stand, companionZipEntry{name: "packs/alpha/readme.txt", data: []byte("hi")})},
		{"unreferenced raster", pack(stand, companionZipEntry{name: "packs/alpha/extra.png", data: png8})},
		{"svg file", pack(stand, companionZipEntry{name: "packs/alpha/evil.svg", data: []byte("<svg/>")})},
		{"svg reference", []companionZipEntry{{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
			{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "evil.svg", "")}}},
		{"extension format mismatch", pack(companionZipEntry{name: "packs/alpha/stand.png", data: companionTestWebP(8, 8)})},
		{"webp with png bytes", []companionZipEntry{{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
			{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "stand.webp", "")}, {name: "packs/alpha/stand.webp", data: png8}}},
		{"oversized png dimensions", pack(companionZipEntry{name: "packs/alpha/stand.png", data: companionTestPNG(t, companionImageSideMax+1, 1)})},
		{"oversized webp dimensions", []companionZipEntry{{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
			{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "stand.webp", "")},
			{name: "packs/alpha/stand.webp", data: companionTestWebP(16, companionImageSideMax+1)}}},
		{"archive entry count", tooMany},
		{"pack entry count", packTooMany},
		{"asset byte cap", pack(companionZipEntry{name: "packs/alpha/stand.png", data: bigAsset, store: true})},
		{"manifest byte cap", []companionZipEntry{{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
			{name: "packs/alpha/pack.json", data: append(companionTestManifest("alpha", "stand.png", ""), bytes.Repeat([]byte(" "), companionManifestCap)...), store: true}, stand}},
		{"pack byte cap", packCap},
		{"ratio bomb", pack(companionZipEntry{name: "packs/alpha/stand.png", data: make([]byte, 1<<20)})},
		{"encrypted entry", pack(stand, companionZipEntry{name: "packs/alpha/other.png", data: png8, flags: 0x1})},
		{"unsupported method", pack(stand, companionZipEntry{name: "packs/alpha/other.png", data: png8, method: 12})},
		{"bad marketplace schema", []companionZipEntry{{name: "marketplace.json", data: bytes.Replace(companionTestMarket(companionTestEntry("alpha", "packs/alpha")), []byte("/v1"), []byte("/v2"), 1)},
			{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "stand.png", "")}, stand}},
		{"marketplace missing", []companionZipEntry{{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "stand.png", "")}, stand}},
		{"id mismatch", []companionZipEntry{{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
			{name: "packs/alpha/pack.json", data: companionTestManifest("beta", "stand.png", "")}, stand}},
		{"reserved id default", twoIDs(companionTestMarket(companionTestEntry("default", "packs/alpha")))},
		{"reserved id auto", twoIDs(companionTestMarket(companionTestEntry("auto", "packs/alpha")))},
		{"duplicate id", twoIDs(companionTestMarket(companionTestEntry("alpha", "packs/alpha"), companionTestEntry("alpha", "packs/alpha")))},
		{"device name id", []companionZipEntry{{name: "marketplace.json", data: companionTestMarket(companionTestEntry("com1", "packs/com1"))},
			{name: "packs/com1/pack.json", data: companionTestManifest("com1", "stand.png", "")}, {name: "packs/com1/stand.png", data: png8}}},
		{"device name file", []companionZipEntry{{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
			{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "AUX.png", "")}, {name: "packs/alpha/AUX.png", data: png8}}},
		{"device name file with extension chain", []companionZipEntry{{name: "marketplace.json", data: companionTestMarket(companionTestEntry("alpha", "packs/alpha"))},
			{name: "packs/alpha/pack.json", data: companionTestManifest("alpha", "nul.x.webp", "")}, {name: "packs/alpha/nul.x.webp", data: companionTestWebP(8, 8)}}},
		{"path escaping archive", twoIDs(companionTestMarket(companionTestEntry("alpha", "../packs/alpha")))},
		{"url without sha256", twoIDs(companionTestMarket(`{"id":"alpha","name":"A","version":"1","url":"https://example.invalid/a.zip"}`))},
		{"http url", twoIDs(companionTestMarket(`{"id":"alpha","name":"A","version":"1","url":"http://example.invalid/a.zip","sha256":"` + strings.Repeat("0", 64) + `"}`))},
		{"control character name", twoIDs(companionTestMarket(`{"id":"alpha","name":"A\u0007","version":"1","path":"packs/alpha"}`))},
		{"overlong version", twoIDs(companionTestMarket(`{"id":"alpha","name":"A","version":"` + strings.Repeat("9", 33) + `","path":"packs/alpha"}`))},
	}
	// Each case must fail for its own rule, not an earlier coincidence.
	wants := map[string]string{
		"traversal": ".. segments", "absolute path": "absolute", "backslash": "backslashes",
		"drive letter": "drive-letter", "non-clean name": "clean paths", "duplicate names": "duplicate entry",
		"case collision": "only by case", "symlink entry": "symbolic links", "nested directory file": "nested directories",
		"unknown file": "only pack.json", "unreferenced raster": "not referenced", "svg file": "only pack.json",
		"svg reference": "plain .webp or .png", "extension format mismatch": "not a PNG", "webp with png bytes": "not a WebP",
		"oversized png dimensions": "dimensions", "oversized webp dimensions": "dimensions",
		"archive entry count": "more than 4096 entries", "pack entry count": "more than 256 entries",
		"asset byte cap": "file byte limit", "manifest byte cap": "file byte limit", "pack byte cap": "pack byte limit",
		"ratio bomb": "compression ratio", "encrypted entry": "encrypted", "unsupported method": "stored or deflated",
		"bad marketplace schema": "schema must be", "marketplace missing": "marketplace.json is missing",
		"id mismatch": "id must match", "reserved id default": "reserved", "reserved id auto": "reserved",
		"duplicate id": "more than once", "device name id": "reserved device name", "device name file": "reserved device name",
		"device name file with extension chain": "reserved device name", "path escaping archive": "clean relative directory",
		"url without sha256": "sha256", "http url": "https", "control character name": "control characters",
		"overlong version": "version exceeds",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			store := new(CompanionPackStore)
			data := companionTestZip(t, tc.entries...)
			for _, dryRun := range []bool{true, false} {
				result, err := companionTestRun(store, home, data, nil, dryRun)
				message := ""
				if err != nil {
					message = err.Error()
				} else if len(result.Listing.Packs) > 0 && result.Listing.Packs[0].Error != nil {
					message = result.Listing.Packs[0].Error.Message
				}
				if want := wants[tc.name]; want == "" || !strings.Contains(message, want) {
					t.Errorf("dryRun=%t: rejected for %q, want %q", dryRun, message, want)
				}
				if err == nil {
					for _, p := range result.Listing.Packs {
						if p.Error == nil || p.Installed {
							t.Fatalf("dryRun=%t accepted %s: %+v", dryRun, p.ID, result.Listing.Packs)
						}
					}
					if len(result.Listing.Packs) == 0 {
						t.Fatalf("dryRun=%t: no rejection reported", dryRun)
					}
				}
			}
			if _, err := os.Stat(filepath.Join(home, "companions")); !os.IsNotExist(err) {
				t.Fatalf("a rejected archive created the pack root: %v", err)
			}
		})
	}
}

func TestCompanionPackInstallOperationAndCacheBudgets(t *testing.T) {
	t.Run("install operation total", func(t *testing.T) {
		home := t.TempDir()
		data := companionTestZip(t, companionTestBundle(t, "", "alpha", "beta")...)
		// Leave room for the marketplace and the first pack only.
		archive, err := openCompanionArchive(data, newCompanionInflate())
		if err != nil {
			t.Fatal(err)
		}
		probe, err := archive.marketplace()
		if err != nil || len(probe.Packs) != 2 {
			t.Fatalf("probe: %v", err)
		}
		used := companionOperationCap - archive.inflate.left
		perPack := len(companionTestManifest("alpha", "stand.png", "")) + len(companionTestPNG(t, 8, 8))
		result, err := companionTestRunInflate(new(CompanionPackStore), home, data, nil, false,
			&companionInflate{left: used + perPack + perPack/2})
		if err != nil {
			t.Fatal(err)
		}
		if a, b := companionListed(result, "alpha"), companionListed(result, "beta"); a.Error != nil || b.Error == nil ||
			!strings.Contains(b.Error.Message, "operation") {
			t.Fatalf("operation cap: %+v", result.Listing.Packs)
		}
	})
	t.Run("remaining cache budget", func(t *testing.T) {
		home := t.TempDir()
		store := new(CompanionPackStore)
		// A later-sorting installed pack holds nearly the whole cache: a new
		// earlier ID would push it out of the catalog, so it is refused.
		big := &companionPack{info: companionPackInfo{ID: "zeta", Source: "local"}, size: companionCacheCap - 64}
		store.active.Store(&companionSnapshot{packs: []companionPackInfo{big.info}, byID: map[string]*companionPack{"zeta": big}})
		result, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if p := companionListed(result, "alpha"); p.Error == nil || !strings.Contains(p.Error.Message, "cache") || p.Installed {
			t.Fatalf("cache budget: %+v", p)
		}
	})
}

func TestCompanionPackInstallAcceptance(t *testing.T) {
	t.Run("self-contained bundle", func(t *testing.T) {
		home := t.TempDir()
		store := new(CompanionPackStore)
		data := companionTestZip(t, companionTestBundle(t, "", "alpha", "beta")...)
		preview, err := companionTestRun(store, home, data, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range preview.Listing.Packs {
			if p.Error != nil || p.Status != companionStatusAvailable || p.Rev == "" || p.Installed {
				t.Fatalf("preview: %+v", p)
			}
		}
		if _, err := os.Stat(filepath.Join(home, "companions")); !os.IsNotExist(err) {
			t.Fatal("a dry run wrote the pack root")
		}
		result, err := companionTestRun(store, home, data, []string{"beta"}, false)
		if err != nil {
			t.Fatal(err)
		}
		if b := companionListed(result, "beta"); !b.Installed || b.Error != nil || companionListed(result, "alpha").Installed {
			t.Fatalf("selected install: %+v", result.Listing.Packs)
		}
		if result.Catalog == nil {
			t.Fatal("install returned no catalog")
		}
		var origin *companionSidecar
		for _, p := range result.Catalog.Packs {
			if p.ID == "beta" {
				origin = p.Origin
			}
		}
		if origin == nil || origin.Kind != "local" || origin.Source != "fixture.zip" || origin.Version != "1.0.0" ||
			origin.PackSHA256 != companionListed(result, "beta").Rev || origin.InstalledAt == "" || origin.Official {
			t.Fatalf("origin: %+v", origin)
		}
		if got := companionDirFiles(t, filepath.Join(home, "companions", "beta")); strings.Join(got, ",") != ".ws-install.json,pack.json,stand.png" {
			t.Fatalf("installed files: %v", got)
		}
		raw, _ := os.ReadFile(filepath.Join(home, "companions", "beta", "pack.json"))
		if !bytes.Equal(raw, companionTestManifest("beta", "stand.png", "")) {
			t.Fatal("pack.json bytes changed")
		}
		if got := companionDirFiles(t, filepath.Join(home, companionStagingName)); len(got) != 0 {
			t.Fatalf("staging left behind: %v", got)
		}
	})
	t.Run("host archive with directory entries and extra files", func(t *testing.T) {
		home := t.TempDir()
		prefix := "fixture-repo-0123abc/"
		entries := []companionZipEntry{{name: prefix}, {name: prefix + "packs/"}, {name: prefix + "packs/alpha/"},
			{name: prefix + "packs/alpha/unused-dir/"}, {name: prefix + "README.md", data: []byte("# readme")},
			{name: prefix + "LICENSE", data: []byte("license")}, {name: prefix + "src/"},
			{name: prefix + "src/art-source.kra", data: []byte("not decompressed"), method: 99, store: true}}
		// The unsupported method on an ignored entry still fails the archive:
		// archive-level rules cover every entry.
		data := companionTestZip(t, append(entries, companionTestBundle(t, prefix, "alpha")...)...)
		if _, err := companionTestRun(new(CompanionPackStore), home, data, nil, false); err == nil {
			t.Fatal("unsupported method on an ignored entry accepted")
		}
		entries[len(entries)-1] = companionZipEntry{name: prefix + "src/art-source.kra", data: bytes.Repeat([]byte("x"), 1<<20)}
		data = companionTestZip(t, append(entries, companionTestBundle(t, prefix, "alpha")...)...)
		inflate := newCompanionInflate()
		result, err := companionTestRunInflate(new(CompanionPackStore), home, data, nil, false, inflate)
		if err != nil {
			t.Fatal(err)
		}
		if a := companionListed(result, "alpha"); a == nil || !a.Installed {
			t.Fatalf("host archive: %+v", result.Listing)
		}
		// The ratio-bomb-shaped extra file was never decompressed.
		if used := companionOperationCap - inflate.left; used > 64<<10 {
			t.Fatalf("ignored entries decompressed: %d bytes", used)
		}
	})
	t.Run("update over a sidecar directory", func(t *testing.T) {
		home := t.TempDir()
		store := new(CompanionPackStore)
		if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, false); err != nil {
			t.Fatal(err)
		}
		before := store.snapshot(wsconfig.Options{ConfigHome: home}).byID["alpha"].info.Rev
		entries := companionTestBundle(t, "", "alpha")
		entries[2].data = companionTestPNG(t, 9, 9)
		changed := companionTestZip(t, entries...)
		preview, err := companionTestRun(store, home, changed, nil, true)
		if err != nil || companionListed(preview, "alpha").Status != companionStatusUpdate {
			t.Fatalf("update preview: %v %+v", err, preview.Listing.Packs)
		}
		same, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, true)
		if err != nil || companionListed(same, "alpha").Status != companionStatusInstalled {
			t.Fatalf("unchanged preview: %v %+v", err, same.Listing.Packs)
		}
		result, err := companionTestRun(store, home, changed, nil, false)
		if err != nil || !companionListed(result, "alpha").Installed {
			t.Fatalf("update: %v %+v", err, result.Listing.Packs)
		}
		after := store.snapshot(wsconfig.Options{ConfigHome: home}).byID["alpha"]
		if after == nil || after.info.Rev == before || after.info.Origin == nil || after.info.Origin.PackSHA256 != after.info.Rev {
			t.Fatalf("updated pack: %+v", after)
		}
	})
	t.Run("hand-made directory refused", func(t *testing.T) {
		home := t.TempDir()
		dir := companionTestPack(t, home, "alpha", companionTestManifest("alpha", "stand.webp", ""))
		store := new(CompanionPackStore)
		result, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if p := companionListed(result, "alpha"); p.Error == nil || p.Installed || p.Status != companionStatusManual {
			t.Fatalf("hand-made overwrite: %+v", p)
		}
		if got := companionDirFiles(t, dir); strings.Join(got, ",") != "pack.json,stand.webp" {
			t.Fatalf("hand-made directory changed: %v", got)
		}
		if _, err := store.uninstall(wsconfig.Options{ConfigHome: home}, "alpha"); err == nil {
			t.Fatal("uninstall removed a hand-made pack")
		}
		if _, err := os.Stat(dir); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("local pack cap", func(t *testing.T) {
		home := t.TempDir()
		for i := 0; i < companionMaxPacks-1; i++ {
			companionTestPack(t, home, fmt.Sprintf("p%02d", i), companionTestManifest(fmt.Sprintf("p%02d", i), "stand.webp", ""))
		}
		// An invalid directory with a valid ID still counts, as the scan's
		// visited count does.
		if err := os.MkdirAll(filepath.Join(home, "companions", "broken"), 0o755); err != nil {
			t.Fatal(err)
		}
		store := new(CompanionPackStore)
		result, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, false)
		if err != nil {
			t.Fatal(err)
		}
		if p := companionListed(result, "alpha"); p.Error == nil || !strings.Contains(p.Error.Message, "limit") {
			t.Fatalf("local pack cap: %+v", p)
		}
	})
	t.Run("concurrent installs of one ID serialize", func(t *testing.T) {
		home := t.TempDir()
		store := new(CompanionPackStore)
		data := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
		var wg sync.WaitGroup
		errs := make(chan string, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, err := companionTestRun(store, home, data, nil, false)
				if err != nil {
					errs <- err.Error()
				} else if p := companionListed(result, "alpha"); p.Error != nil {
					errs <- p.Error.Message
				}
			}()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Error(e)
		}
		if s := store.reload(wsconfig.Options{ConfigHome: home}); s.byID["alpha"] == nil || len(s.errors) != 0 {
			t.Fatalf("after concurrent installs: %+v", s.errors)
		}
		if got := companionDirFiles(t, filepath.Join(home, "companions")); strings.Join(got, ",") != "alpha" {
			t.Fatalf("pack root: %v", got)
		}
	})
}

func TestCompanionPackInstallCrashSafety(t *testing.T) {
	home := t.TempDir()
	leftover := filepath.Join(home, companionStagingName, "run-crashed", "alpha")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "pack.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := new(CompanionPackStore)
	if s := store.snapshot(wsconfig.Options{ConfigHome: home}); len(s.errors) != 0 {
		t.Fatalf("leftover staging reported: %+v", s.errors)
	}
	if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "beta")...), nil, false); err != nil {
		t.Fatal(err)
	}
	if got := companionDirFiles(t, filepath.Join(home, companionStagingName)); len(got) != 0 {
		t.Fatalf("leftover staging kept: %v", got)
	}
}

func TestCompanionPackUninstall(t *testing.T) {
	home := t.TempDir()
	opts := wsconfig.Options{ConfigHome: home}
	store := new(CompanionPackStore)
	if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, false); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"default", "auto", "missing", "../x"} {
		if _, err := store.uninstall(opts, id); err == nil {
			t.Errorf("uninstall %q accepted", id)
		}
	}
	s, err := store.uninstall(opts, "alpha")
	if err != nil || s.byID["alpha"] != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "companions", "alpha")); !os.IsNotExist(err) {
		t.Fatalf("pack directory kept: %v", err)
	}
	if got := companionDirFiles(t, filepath.Join(home, companionStagingName)); len(got) != 0 {
		t.Fatalf("staging left behind: %v", got)
	}
}

func TestCompanionPackSidecarOrigin(t *testing.T) {
	home := t.TempDir()
	good := companionTestPack(t, home, "good", companionTestManifest("good", "stand.webp", ""))
	bad := companionTestPack(t, home, "bad", companionTestManifest("bad", "stand.webp", ""))
	sidecar := companionSidecar{Source: "https://example.invalid/packs.git", Kind: "git", Ref: "main",
		Commit: strings.Repeat("a", 40), PackSHA256: strings.Repeat("b", 32), Version: "1.2.3", InstalledAt: "2026-01-01T00:00:00Z"}
	raw, _ := json.Marshal(sidecar)
	if err := os.WriteFile(filepath.Join(good, companionSidecarName), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{`{"kind":"svn","source":"x"}`} {
		if err := os.WriteFile(filepath.Join(bad, companionSidecarName), []byte(malformed), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: home})
	if p := s.byID["good"]; p == nil || p.info.Origin == nil || *p.info.Origin != sidecar {
		t.Fatalf("origin: %+v", p)
	}
	if p := s.byID["bad"]; p == nil || p.info.Origin != nil {
		t.Fatalf("malformed sidecar: %+v", p)
	}
	for _, raw := range []string{
		`{"source":"x","kind":"zip","commit":"short"}`, `{"source":"","kind":"zip"}`, `{"source":"x","kind":"zip","official":"yes"}`,
		`{"source":"x\u0000","kind":"zip"}`, `{"source":"x","kind":"zip","packSha256":"` + strings.Repeat("g", 32) + `"}`, `[]`,
	} {
		if parseCompanionSidecar([]byte(raw)) != nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestCompanionPackRasterHeaders(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		ok   bool
	}{
		{"a.png", companionTestPNG(t, 4096, 1), true},
		{"a.webp", companionTestWebP(4096, 4096), true},
		{"a.webp", companionTestWebP(4097, 1), false},
		{"a.webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 \x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"), false},
		{"a.png", []byte("\x89PNG\r\n\x1a\n"), false},
		{"a.webp", nil, false},
	} {
		if err := companionCheckRaster(tc.name, tc.data); (err == nil) != tc.ok {
			t.Errorf("%s %d bytes: %v", tc.name, len(tc.data), err)
		}
	}
	vp8 := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8 \x0a\x00\x00\x00\x00\x00\x00\x9d\x01\x2a"), 0x10, 0x00, 0x20, 0x00)
	if w, h, err := companionWebPSize(vp8); err != nil || w != 16 || h != 32 {
		t.Fatalf("VP8: %d %d %v", w, h, err)
	}
	vp8x := append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00"), 0x0f, 0x00, 0x00, 0x1f, 0x00, 0x00)
	if w, h, err := companionWebPSize(vp8x); err != nil || w != 16 || h != 32 {
		t.Fatalf("VP8X: %d %d %v", w, h, err)
	}
	// Width-1 and height-1 span three bytes each; the second byte must count.
	vp8x = append([]byte("RIFF\x00\x00\x00\x00WEBPVP8X\x0a\x00\x00\x00\x00\x00\x00\x00"), 0xff, 0x01, 0x00, 0xff, 0x0f, 0x00)
	if w, h, err := companionWebPSize(vp8x); err != nil || w != 512 || h != 4096 {
		t.Fatalf("VP8X wide: %d %d %v", w, h, err)
	}
}

func TestCompanionPackInstallFileOperations(t *testing.T) {
	home := t.TempDir()
	opts := wsconfig.Options{ConfigHome: home}
	ops := map[string]int{}
	opens := 0
	store := &CompanionPackStore{
		trace: func(op string) { ops[op]++ },
		open: func(p companionPackFile, n string, d bool) (companionPackFile, error) {
			opens++
			return openCompanionPackEntry(p, n, d)
		},
	}
	store.snapshot(opts)
	reset := func() (map[string]int, int) {
		got, n := ops, opens
		ops, opens = map[string]int{}, 0
		return got, n
	}
	reset()
	data := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	if _, err := companionTestRun(store, home, data, nil, false); err != nil {
		t.Fatal(err)
	}
	install, installOpens := reset()
	t.Logf("one local import (1 pack, 1 raster): installer %v, scan opens %d", install, installOpens)
	if install["rename"] != 1 || install["create"] != 3 || installOpens != 5 {
		t.Fatalf("install ops: %v, scan opens %d", install, installOpens)
	}
	for i := 0; i < 3; i++ {
		store.snapshot(opts)
	}
	if warm, warmOpens := reset(); len(warm) != 0 || warmOpens != 0 {
		t.Fatalf("warm reads touched files: %v %d", warm, warmOpens)
	}
	if _, err := store.uninstall(opts, "alpha"); err != nil {
		t.Fatal(err)
	}
	removal, removalOpens := reset()
	t.Logf("one uninstall: installer %v, scan opens %d", removal, removalOpens)
	if removal["rename"] != 1 || removal["create"] != 0 {
		t.Fatalf("uninstall ops: %v", removal)
	}
}

// TestCompanionPackInstallExternalEntry covers a url entry through the
// plan's external hook: a dry run lists it without a download, an install
// fetches and checks it, and the listing status follows the entry's sha256.
func TestCompanionPackInstallExternalEntry(t *testing.T) {
	home := t.TempDir()
	store := &CompanionPackStore{}
	opts := wsconfig.Options{ConfigHome: home}
	packZip := companionTestZip(t,
		companionZipEntry{name: "far-0123/"},
		companionZipEntry{name: "far-0123/pack.json", data: companionTestManifest("far", "stand.png", "")},
		companionZipEntry{name: "far-0123/stand.png", data: companionTestPNG(t, 8, 8)})
	sum := strings.Repeat("c1", 32)
	market := func(sha string) []byte {
		return companionTestZip(t,
			companionZipEntry{name: "marketplace.json", data: companionTestMarket(
				companionTestEntry("near", "packs/near"),
				fmt.Sprintf(`{"id":"far","name":"Far","version":"2.0.0","url":"https://example.invalid/far.zip","sha256":%q}`, sha))},
			companionZipEntry{name: "packs/near/pack.json", data: companionTestManifest("near", "stand.png", "")},
			companionZipEntry{name: "packs/near/stand.png", data: companionTestPNG(t, 8, 8)})
	}
	var fetched []string
	run := func(data []byte, ids []string, dryRun bool, external func(companionMarketplaceEntry, *companionInflate) (*companionArchive, error)) companionInstallResult {
		t.Helper()
		inflate := newCompanionInflate()
		archive, err := openCompanionArchive(data, inflate)
		if err != nil {
			t.Fatal(err)
		}
		m, err := archive.marketplace()
		if err != nil {
			t.Fatal(err)
		}
		origin := companionInstallOrigin{Source: "https://example.invalid/packs.zip", Kind: "zip", ArchiveSHA256: strings.Repeat("ab", 32)}
		result, err := store.install(opts, companionInstallPlan{archive: archive, market: m, origin: origin, external: external}, ids, dryRun)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	refuse := func(entry companionMarketplaceEntry, _ *companionInflate) (*companionArchive, error) {
		t.Fatalf("dry run downloaded %s", entry.URL)
		return nil, nil
	}
	serve := func(entry companionMarketplaceEntry, inflate *companionInflate) (*companionArchive, error) {
		fetched = append(fetched, entry.URL)
		return openCompanionArchive(packZip, inflate)
	}

	listing := run(market(sum), nil, true, refuse)
	if far := companionListed(listing, "far"); far == nil || far.Status != companionStatusAvailable || far.Error != nil || far.Rev != "" {
		t.Fatalf("dry-run url entry = %+v, want available with no download", far)
	}
	if near := companionListed(listing, "near"); near == nil || near.Error != nil || near.Rev == "" {
		t.Fatalf("dry-run path entry = %+v, want validated", near)
	}

	installed := run(market(sum), []string{"far"}, false, serve)
	if far := companionListed(installed, "far"); far == nil || !far.Installed || far.Error != nil {
		t.Fatalf("install url entry = %+v", far)
	}
	if len(fetched) != 1 || fetched[0] != "https://example.invalid/far.zip" {
		t.Fatalf("fetched = %v, want one download of the entry URL", fetched)
	}
	origin := store.snapshot(opts).byID["far"].info.Origin
	if origin == nil || origin.ArchiveSHA256 != sum || origin.Kind != "zip" || origin.Version != "2.0.0" {
		t.Fatalf("far sidecar = %+v, want the entry sha256 as archiveSha256", origin)
	}

	if far := companionListed(run(market(sum), nil, true, refuse), "far"); far.Status != companionStatusInstalled {
		t.Fatalf("unchanged url entry status = %q, want installed", far.Status)
	}
	if far := companionListed(run(market(strings.Repeat("d2", 32)), nil, true, refuse), "far"); far.Status != companionStatusUpdate {
		t.Fatalf("changed url entry status = %q, want update", far.Status)
	}
	if far := companionListed(run(market(sum), []string{"far"}, false, nil), "far"); far.Error == nil ||
		!strings.Contains(far.Error.Message, "network source") {
		t.Fatalf("url entry without a fetcher = %+v, want a refusal", far)
	}
}

// TestCompanionPackInstallPaperDollFiles pins the install allow-list to the
// validator: every raster the paper-doll sections reference (outfit columns
// and frames, fills, attachments and their columns, eye sets) installs, and
// an unreferenced raster beside them still fails the pack.
func TestCompanionPackInstallPaperDollFiles(t *testing.T) {
	png8 := companionTestPNG(t, 8, 8)
	bundle := func(extra ...string) []byte {
		entries := []companionZipEntry{
			{name: "marketplace.json", data: companionTestMarket(companionTestEntry("kit", "packs/kit"))},
			{name: "packs/kit/pack.json", data: []byte(companionKitFixture)},
		}
		for _, name := range append(append([]string{}, companionKitFiles...), extra...) {
			entries = append(entries, companionZipEntry{name: "packs/kit/" + name, data: png8})
		}
		return companionTestZip(t, entries...)
	}
	home := t.TempDir()
	store := new(CompanionPackStore)
	result, err := companionTestRun(store, home, bundle(), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if kit := companionListed(result, "kit"); kit == nil || !kit.Installed || kit.Error != nil {
		t.Fatalf("paper-doll pack: %+v", kit)
	}
	want := append([]string{".ws-install.json", "pack.json"}, companionKitFiles...)
	sort.Strings(want)
	if got := companionDirFiles(t, filepath.Join(home, "companions", "kit")); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("installed files = %v, want %v", got, want)
	}

	other := t.TempDir()
	result, err = companionTestRun(new(CompanionPackStore), other, bundle("unused.png"), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if kit := companionListed(result, "kit"); kit == nil || kit.Installed || kit.Error == nil ||
		!strings.HasSuffix(kit.Error.Path, "unused.png") || !strings.Contains(kit.Error.Message, "not referenced") {
		t.Fatalf("unreferenced raster beside paper-doll files = %+v, want the pack refused", kit)
	}
	if _, err := os.Stat(filepath.Join(other, "companions", "kit")); !os.IsNotExist(err) {
		t.Fatal("a refused pack left a directory")
	}
}

func TestCompanionPackInstallHandMadeNameCaseFold(t *testing.T) {
	home := t.TempDir()
	// A hand-made "Foo" is invalid to the scan (IDs are lower case) but on a
	// case-insensitive volume it is the directory "foo" names.
	dir := filepath.Join(home, "companions", "Foo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mine.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	store := new(CompanionPackStore)
	result, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "foo")...), nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if p := companionListed(result, "foo"); p.Error == nil || p.Installed {
		t.Fatalf("case-variant hand-made directory not refused: %+v", p)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "mine.txt")); err != nil || string(got) != "keep" {
		t.Fatalf("hand-made pack changed: %q %v", got, err)
	}
	if got := companionDirFiles(t, filepath.Join(home, "companions")); strings.Join(got, ",") != "Foo" {
		t.Fatalf("pack root: %v", got)
	}
}

func TestCompanionPackInstallStrandedPreviousPack(t *testing.T) {
	home := t.TempDir()
	opts := wsconfig.Options{ConfigHome: home}
	store := new(CompanionPackStore)
	if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, false); err != nil {
		t.Fatal(err)
	}
	before := store.snapshot(opts).byID["alpha"].info.Rev
	entries := companionTestBundle(t, "", "alpha")
	entries[2].data = companionTestPNG(t, 9, 9)
	changed := companionTestZip(t, entries...)

	// The staged pack cannot move in and the previous pack cannot move back.
	target := filepath.Join(home, "companions", "alpha")
	store.rename = func(oldPath, newPath string) error {
		if newPath == target {
			return errors.New("injected rename failure")
		}
		return os.Rename(oldPath, newPath)
	}
	result, err := companionTestRun(store, home, changed, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	p := companionListed(result, "alpha")
	if p.Installed || p.Error == nil || !strings.Contains(p.Error.Message, "could not be restored") {
		t.Fatalf("stranded rollback not surfaced: %+v", p)
	}
	stranded, _ := filepath.Glob(filepath.Join(home, companionStagingName, "run-*", ".alpha", companionManifestName))
	if len(stranded) != 1 {
		t.Fatalf("previous pack deleted with the run: %v", stranded)
	}

	// The next operation restores it instead of deleting it.
	store.rename = nil
	if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "beta")...), nil, false); err != nil {
		t.Fatal(err)
	}
	s := store.snapshot(opts)
	if s.byID["alpha"] == nil || s.byID["alpha"].info.Rev != before || s.byID["beta"] == nil {
		t.Fatalf("previous pack not restored: %+v", s.packs)
	}
	if got := companionDirFiles(t, filepath.Join(home, companionStagingName)); len(got) != 0 {
		t.Fatalf("staging left behind: %v", got)
	}
}

func TestCompanionPackInstallRecoversCrashBetweenRenames(t *testing.T) {
	for _, op := range []string{"install", "uninstall"} {
		t.Run(op, func(t *testing.T) {
			home := t.TempDir()
			opts := wsconfig.Options{ConfigHome: home}
			store := new(CompanionPackStore)
			if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, false); err != nil {
				t.Fatal(err)
			}
			// A crash after the moved-aside rename leaves the only copy in staging.
			run := filepath.Join(home, companionStagingName, "run-crashed")
			if err := os.MkdirAll(run, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(home, "companions", "alpha"), filepath.Join(run, ".alpha")); err != nil {
				t.Fatal(err)
			}
			store = new(CompanionPackStore)
			switch op {
			case "install":
				if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "beta")...), nil, false); err != nil {
					t.Fatal(err)
				}
			default:
				if _, err := store.uninstall(opts, "alpha"); err != nil {
					t.Fatal(err)
				}
			}
			s := store.snapshot(opts)
			if (op == "install") != (s.byID["alpha"] != nil) {
				t.Fatalf("%s: alpha presence wrong: %+v", op, s.packs)
			}
			if got := companionDirFiles(t, filepath.Join(home, companionStagingName)); len(got) != 0 {
				t.Fatalf("staging left behind: %v", got)
			}
		})
	}
}

func TestCompanionPackInstallRefusesLinkedPackRoot(t *testing.T) {
	home := t.TempDir()
	opts := wsconfig.Options{ConfigHome: home}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "companions")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	store := new(CompanionPackStore)
	if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, false); err == nil {
		t.Fatal("install through a linked pack root accepted")
	}
	if _, err := companionTestRun(store, home, companionTestZip(t, companionTestBundle(t, "", "alpha")...), nil, true); err == nil {
		t.Fatal("dry run through a linked pack root accepted")
	}
	if _, err := store.uninstall(opts, "alpha"); err == nil {
		t.Fatal("uninstall through a linked pack root accepted")
	}
	if got := companionDirFiles(t, outside); len(got) != 0 {
		t.Fatalf("wrote through the link: %v", got)
	}
}

func TestCompanionArchiveChargesBytesOfFailedRead(t *testing.T) {
	data := companionTestZip(t, companionZipEntry{name: "big.bin", data: bytes.Repeat([]byte{7}, 100<<10), store: true})
	inflate := &companionInflate{left: 1 << 20}
	archive, err := openCompanionArchive(data, inflate)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := archive.read(archive.files["big.bin"], 10<<10); err == nil {
		t.Fatal("over-limit entry accepted")
	}
	if used := (1 << 20) - inflate.left; used < 10<<10 {
		t.Fatalf("failed read charged %d bytes, want at least the %d it produced", used, 10<<10)
	}
}
