package wsdashboard

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

const companionClipTestPack = "studio"

type companionClipFixture struct {
	companionRouteFixture
	dir string
}

// newCompanionClipFixture makes a hand-made local VRM pack "studio" with a
// legacy animation; extra is spliced into its manifest.
func newCompanionClipFixture(t *testing.T, extra string) companionClipFixture {
	t.Helper()
	f := newCompanionRouteFixture(t)
	manifest := companionTestVRMManifest(companionClipTestPack, "model.vrm", `,"animations":{"wave":"wave.vrma"}`+extra)
	dir := companionTestVRMPack(t, f.home, companionClipTestPack, manifest, syntheticVRM1GLB(), map[string][]byte{"wave.vrma": syntheticVRMAGLB()})
	return companionClipFixture{companionRouteFixture: f, dir: dir}
}

func (f companionClipFixture) opts() wsconfig.Options { return f.d.companionOptions() }

func (f companionClipFixture) pack(t *testing.T, id string) *companionPack {
	t.Helper()
	pack := f.d.companionPacks.snapshot(f.opts()).byID[id]
	if pack == nil {
		t.Fatalf("pack %s not loaded", id)
	}
	return pack
}

func (f companionClipFixture) rev(t *testing.T) string {
	return f.pack(t, companionClipTestPack).info.Rev
}

func (f companionClipFixture) file(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(f.dir, filepath.FromSlash(name)))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func (f companionClipFixture) absent(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(filepath.Join(f.dir, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists: %v", name, err)
	}
}

// author sends an authenticated same-origin write; rev "" omits If-Match.
func (f companionRouteFixture) author(method, path, rev string, body []byte) *httptest.ResponseRecorder {
	r := authRequest(method, path, string(body))
	r.AddCookie(f.cookie)
	r.Header.Set("Origin", companionTestOrigin)
	if rev != "" {
		r.Header.Set("If-Match", `"`+rev+`"`)
	}
	return authServe(f.d, r)
}

func companionClipPath(id, key string) string {
	return companionPacksRoute + "/" + id + "/clips/" + key
}

func companionTestPutBody(clip, entry string) []byte {
	if clip == "" {
		return []byte(`{"entry":` + entry + `}`)
	}
	return []byte(`{"clip":` + clip + `,"entry":` + entry + `}`)
}

// put writes key as a clip entry and fails the test unless it answers 200.
func (f companionClipFixture) put(t *testing.T, key, clip, entry string) companionClipResult {
	t.Helper()
	w := f.author("PUT", companionClipPath(companionClipTestPack, key), f.rev(t), companionTestPutBody(clip, entry))
	if w.Code != http.StatusOK {
		t.Fatalf("PUT %s: %d %s", key, w.Code, w.Body)
	}
	return companionRouteDecode[companionClipResult](t, w)
}

func companionTestHasAction(pack *companionPack, key string) bool {
	for _, e := range pack.catalog.actions {
		if e.Key == key {
			return true
		}
	}
	return false
}

func TestCompanionClipPutRoundTrip(t *testing.T) {
	f := newCompanionClipFixture(t, `,"future":{"keep":[1, 2]}`)
	before := f.file(t, companionManifestName)
	oldRev := f.rev(t)
	clip := companionTestClipJSON("wave2", true, `,"unknown_field":{"kept":true}`)
	result := f.put(t, "wave2", clip, companionTestEntryJSON("wave2", "clip", "Wave hello", true, ""))
	if result.Pack.Rev == oldRev || len(result.Warnings) != 0 {
		t.Fatalf("result: %+v", result)
	}
	stored := f.file(t, "clips/wave2.clip.json")
	if !bytes.Contains(stored, []byte(`"unknown_field": {`)) || !bytes.HasSuffix(stored, []byte("}\n")) {
		t.Fatalf("clip file:\n%s", stored)
	}
	manifest := string(f.file(t, companionManifestName))
	for _, kept := range []string{`"schema": "ws-companion-vrm/v1"`, `"animations": {"wave":"wave.vrma"}`, `"future": {"keep":[1, 2]}`, `"key": "wave2"`} {
		if !strings.Contains(manifest, kept) {
			t.Fatalf("manifest lost %s:\n%s", kept, manifest)
		}
	}
	if !strings.HasPrefix(manifest, "{\n  \"schema\"") || strings.Index(manifest, `"future"`) > strings.Index(manifest, `"actions"`) {
		t.Fatalf("top-level key order not kept:\n%s", manifest)
	}
	if bytes.Equal(before, []byte(manifest)) {
		t.Fatal("manifest not rewritten")
	}
	w := companionTestRequest(f.d, f.cookie, "GET", companionClipPath(companionClipTestPack, "wave2.clip.json"), "")
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), stored) || w.Header().Get("ETag") != `"`+result.Pack.Rev+`"` {
		t.Fatalf("GET after PUT: %d %v", w.Code, w.Header())
	}
	// A reload reads the same bytes back to the same revision and catalog.
	reloaded := f.d.companionPacks.reload(f.opts()).byID[companionClipTestPack]
	if reloaded.info.Rev != result.Pack.Rev || !companionTestHasAction(reloaded, "wave2") || len(reloaded.info.ClipWarnings) != 0 {
		t.Fatalf("reload: %+v", reloaded.info)
	}
	// An identical PUT changes nothing and touches no file.
	ops := 0
	f.d.companionPacks.trace = func(string) { ops++ }
	again := f.put(t, "wave2", clip, companionTestEntryJSON("wave2", "clip", "Wave hello", true, ""))
	f.d.companionPacks.trace = nil
	if again.Pack.Rev != result.Pack.Rev || ops != 0 {
		t.Fatalf("no-op PUT: rev %s ops %d", again.Pack.Rev, ops)
	}
	// Entry-only PUT (the clip is already loaded) rewrites pack.json only.
	f.put(t, "wave2", "", companionTestEntryJSON("wave2", "clip", "Wave again", false, ""))
	if !bytes.Equal(f.file(t, "clips/wave2.clip.json"), stored) || !strings.Contains(string(f.file(t, companionManifestName)), "Wave again") {
		t.Fatal("entry-only PUT")
	}
	// Calibration: written once, an equal body is a no-op, invalid is 400.
	cal := `{"props":{"mug":{"offset":[0,0.1,0],"scale":1.5}}}`
	if w := f.author("PUT", companionPacksRoute+"/studio/calibration", f.rev(t), []byte(cal)); w.Code != 200 {
		t.Fatalf("calibration: %d %s", w.Code, w.Body)
	}
	if !f.pack(t, companionClipTestPack).catalog.hasCalibration || !strings.Contains(string(f.file(t, companionManifestName)), `"calibration"`) {
		t.Fatal("calibration not stored")
	}
	rev := f.rev(t)
	if w := f.author("PUT", companionPacksRoute+"/studio/calibration", rev, []byte(cal)); w.Code != 200 || f.rev(t) != rev {
		t.Fatalf("equal calibration rewrote: %d", w.Code)
	}
	if w := f.author("PUT", companionPacksRoute+"/studio/calibration", rev, []byte(`{"props":{"mug":{"scale":9}}}`)); w.Code != 400 {
		t.Fatalf("invalid calibration: %d", w.Code)
	}
}

func TestCompanionClipWriteGate(t *testing.T) {
	f := newCompanionClipFixture(t, "")
	companionTestPack(t, f.home, "flat", companionTestManifest("flat", "stand.webp", ""))
	remote := companionTestVRMPack(t, f.home, "remote", companionTestVRMManifest("remote", "model.vrm", ""), syntheticVRM1GLB(), nil)
	companionTestWriteFile(t, remote, companionSidecarName, []byte(`{"source":"https://example.test/packs.git","kind":"git","commit":"`+strings.Repeat("a", 40)+`","version":"1","installedAt":"2026-11-28T00:00:00Z"}`))
	path := companionClipPath(companionClipTestPack, "wave2")
	body := companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "", false, ""))
	rev := f.rev(t)

	for _, tc := range []struct{ method, path, allow string }{
		{"PATCH", path, "GET, HEAD, PUT, DELETE"},
		{"POST", path, "GET, HEAD, PUT, DELETE"},
		{"GET", companionPacksRoute + "/studio/clips/import-vrma", "POST"},
		{"GET", companionPacksRoute + "/studio/calibration", "PUT"},
	} {
		w := f.author(tc.method, tc.path, rev, nil)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != tc.allow {
			t.Errorf("%s %s: %d %q", tc.method, tc.path, w.Code, w.Header().Get("Allow"))
		}
	}
	token, err := f.d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{path, companionPacksRoute + "/studio/clips/import-vrma?key=bow", companionPacksRoute + "/studio/calibration"} {
		method := "PUT"
		if strings.Contains(target, "import-vrma") {
			method = "POST"
		}
		r := authRequest(method, target, string(body))
		r.Host, r.RemoteAddr = "192.0.2.1:43210", "192.0.2.2:1234"
		r.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
		r.Header.Set("Origin", "http://"+r.Host)
		r.Header.Set("If-Match", `"`+rev+`"`)
		if w := authServe(f.d, r); w.Code != http.StatusNotFound {
			t.Errorf("LAN %s: %d", target, w.Code)
		}
		r = authRequest(method, target, string(body))
		r.AddCookie(f.cookie)
		r.Header.Set("Origin", "http://evil.test")
		r.Header.Set("If-Match", `"`+rev+`"`)
		if w := authServe(f.d, r); w.Code != http.StatusForbidden {
			t.Errorf("cross-origin %s: %d", target, w.Code)
		}
		if w := f.author(method, target, "", body); w.Code != http.StatusPreconditionRequired {
			t.Errorf("missing If-Match %s: %d", target, w.Code)
		}
	}
	r := authRequest("PUT", path, string(body))
	r.AddCookie(f.cookie)
	r.Header.Set("Origin", companionTestOrigin)
	r.Header.Set("If-Match", "*")
	if w := authServe(f.d, r); w.Code != http.StatusPreconditionRequired {
		t.Errorf("If-Match *: %d", w.Code)
	}
	w := f.author("PUT", path, "0123456789abcdef0123456789abcdef", body)
	if w.Code != http.StatusPreconditionFailed || w.Header().Get("X-Companion-Current-Rev") != rev {
		t.Fatalf("stale If-Match: %d %v", w.Code, w.Header())
	}
	defaultRev := f.pack(t, companionDefaultID).info.Rev
	for id, want := range map[string]int{companionDefaultID: 409, "flat": 409, "remote": 409, "nope": 404, "Bad": 404} {
		packRev := rev
		if p := f.d.companionPacks.snapshot(f.opts()).byID[id]; p != nil {
			packRev = p.info.Rev
		} else if id == companionDefaultID {
			packRev = defaultRev
		}
		w := f.author("PUT", companionClipPath(id, "wave2"), packRev, body)
		if w.Code != want {
			t.Errorf("pack %s: %d %s", id, w.Code, w.Body)
		}
		if id == "remote" && !strings.Contains(w.Body.String(), "import the .vrm as a local pack") {
			t.Errorf("remote message: %s", w.Body)
		}
	}
	for _, key := range []string{"con", "Idle", "a-b", "..%2Fx"} {
		if w := f.author("PUT", companionClipPath(companionClipTestPack, key), rev, body); w.Code != http.StatusBadRequest {
			t.Errorf("key %q: %d %s", key, w.Code, w.Body)
		}
	}
	if w := f.author("POST", companionPacksRoute+"/studio/clips/import-vrma?key=con", rev, syntheticVRMAGLB()); w.Code != 400 {
		t.Errorf("import key con: %d", w.Code)
	}
	if w := f.author("PUT", path, rev, bytes.Repeat([]byte(" "), companionClipFileCap+companionClipBodyOverhead+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over-cap body: %d", w.Code)
	}
	padded := companionTestClipJSON("wave2", false, `,"pad":"`+strings.Repeat("x", companionClipFileCap)+`"`)
	if w := f.author("PUT", path, rev, companionTestPutBody(padded, companionTestEntryJSON("wave2", "clip", "", false, ""))); w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("over-cap clip: %d", w.Code)
	}
	for name, bad := range map[string][]byte{
		"url key mismatch": companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("other", "clip", "", false, "")),
		"Cf description":   companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "a\u200bb", false, "")),
		"Zl description":   companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "a\u2028b", false, "")),
		"long description": companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", strings.Repeat("d", 121), false, "")),
		"bad clip":         companionTestPutBody(companionTestClipJSON("other", false, ""), companionTestEntryJSON("wave2", "clip", "", false, "")),
		"clip with vrma":   companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "vrma", "", false, "")),
		"no clip loaded":   companionTestPutBody("", companionTestEntryJSON("wave2", "clip", "", false, "")),
		"no entry":         []byte(`{"clip":` + companionTestClipJSON("wave2", false, "") + `}`),
		"not json":         []byte(`nope`),
	} {
		if w := f.author("PUT", path, rev, bad); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if f.rev(t) != rev {
		t.Fatal("a refused write changed the pack")
	}
	f.absent(t, "clips")
}

func TestCompanionClipSymlinkRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows; see the junction test")
	}
	f := newCompanionClipFixture(t, "")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(f.dir, "clips")); err != nil {
		t.Fatal(err)
	}
	body := companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "", false, ""))
	if w := f.author("PUT", companionClipPath(companionClipTestPack, "wave2"), f.rev(t), body); w.Code != http.StatusConflict {
		t.Fatalf("symlinked clips: %d %s", w.Code, w.Body)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote through the clips symlink: %v", entries)
	}

	// The pack directory itself swapped for a symlink after the scan.
	g := newCompanionClipFixture(t, "")
	rev := g.rev(t)
	moved := filepath.Join(t.TempDir(), "real")
	if err := os.Rename(g.dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, g.dir); err != nil {
		t.Fatal(err)
	}
	if w := g.author("PUT", companionClipPath(companionClipTestPack, "wave2"), rev, body); w.Code != http.StatusConflict {
		t.Fatalf("symlinked pack dir: %d %s", w.Code, w.Body)
	}
	if _, err := os.Stat(filepath.Join(moved, "clips")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("wrote through the pack symlink: %v", err)
	}
}

func TestCompanionClipDeleteOrder(t *testing.T) {
	f := newCompanionClipFixture(t, "")
	f.put(t, "wave2", companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "", false, ""))
	if w := f.author("DELETE", companionClipPath(companionClipTestPack, "nope"), f.rev(t), nil); w.Code != 404 {
		t.Fatalf("unknown key DELETE: %d", w.Code)
	}
	if w := f.author("DELETE", companionClipPath(companionClipTestPack, "idle"), f.rev(t), nil); w.Code != 404 {
		t.Fatalf("unknown sentinel DELETE: %d", w.Code)
	}
	var removed []string
	f.d.companionPacks.fault = func(op, name string) error {
		if op != "remove" {
			return nil
		}
		if strings.Contains(string(f.file(t, companionManifestName)), "wave2") {
			t.Errorf("%s unlinked before pack.json dropped it", name)
		}
		removed = append(removed, name)
		return errors.New("injected unlink failure")
	}
	w := f.author("DELETE", companionClipPath(companionClipTestPack, "wave2"), f.rev(t), nil)
	f.d.companionPacks.fault = nil
	if w.Code != 200 {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	result := companionRouteDecode[companionClipResult](t, w)
	if len(result.Warnings) != 2 || len(removed) != 2 {
		t.Fatalf("warnings %v removed %v", result.Warnings, removed)
	}
	f.file(t, "clips/wave2.clip.json") // the orphan stays
	pack := f.d.companionPacks.reload(f.opts()).byID[companionClipTestPack]
	if companionTestHasAction(pack, "wave2") || len(pack.clips) != 0 || len(pack.info.ClipWarnings) != 0 || pack.info.Rev != result.Pack.Rev {
		t.Fatalf("orphan affected the reload: %+v", pack.info)
	}
	// A clean DELETE removes the file.
	f.put(t, "nod", companionTestClipJSON("nod", false, ""), companionTestEntryJSON("nod", "clip", "", false, ""))
	if w := f.author("DELETE", companionClipPath(companionClipTestPack, "nod"), f.rev(t), nil); w.Code != 200 {
		t.Fatalf("DELETE nod: %d", w.Code)
	}
	f.absent(t, "clips/nod.clip.json")
}

// TestCompanionClipDuplicateRawKeys pins that writes clear a hand-edited
// duplicate actions key: the loader keeps only the first, so a DELETE that
// left the shadowed copy would revive it pointing at an unlinked file.
func TestCompanionClipDuplicateRawKeys(t *testing.T) {
	dup := `,"actions":[` + companionTestEntryJSON("wave2", "clip", "first", false, "") + `,` +
		companionTestEntryJSON("nod", "clip", "", false, "") + `,` +
		companionTestEntryJSON("wave2", "clip", "second", false, "") + `]`
	count := func(manifest []byte) int { return strings.Count(string(manifest), `"wave2"`) }

	f := newCompanionClipFixture(t, dup)
	companionTestWriteFile(t, f.dir, "clips/wave2.clip.json", []byte(companionTestClipJSON("wave2", false, "")))
	companionTestWriteFile(t, f.dir, "clips/nod.clip.json", []byte(companionTestClipJSON("nod", false, "")))
	if pack := f.pack(t, companionClipTestPack); companionTestWarned(pack.info.ClipWarnings, "duplicate key") != 1 {
		t.Fatalf("fixture: %v", pack.info.ClipWarnings)
	}
	if w := f.author("DELETE", companionClipPath(companionClipTestPack, "wave2"), f.rev(t), nil); w.Code != 200 {
		t.Fatalf("DELETE: %d %s", w.Code, w.Body)
	}
	if n := count(f.file(t, companionManifestName)); n != 0 {
		t.Fatalf("DELETE left %d wave2 entries", n)
	}
	if again := f.d.companionPacks.reload(f.opts()).byID[companionClipTestPack]; companionTestHasAction(again, "wave2") || !companionTestHasAction(again, "nod") {
		t.Fatalf("reload after DELETE: %+v", again.catalog.actions)
	}

	g := newCompanionClipFixture(t, dup)
	companionTestWriteFile(t, g.dir, "clips/wave2.clip.json", []byte(companionTestClipJSON("wave2", false, "")))
	companionTestWriteFile(t, g.dir, "clips/nod.clip.json", []byte(companionTestClipJSON("nod", false, "")))
	g.put(t, "wave2", "", companionTestEntryJSON("wave2", "clip", "first", false, ""))
	manifest := g.file(t, companionManifestName)
	if n := count(manifest); n != 1 || strings.Contains(string(manifest), "second") {
		t.Fatalf("PUT kept the shadowed duplicate:\n%s", manifest)
	}
	if again := g.d.companionPacks.reload(g.opts()).byID[companionClipTestPack]; companionTestWarned(again.info.ClipWarnings, "duplicate key") != 0 {
		t.Fatalf("duplicate survived: %v", again.info.ClipWarnings)
	}
}

func TestCompanionClipKeepsManifestMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	f := newCompanionClipFixture(t, "")
	if err := os.Chmod(filepath.Join(f.dir, companionManifestName), 0o600); err != nil {
		t.Fatal(err)
	}
	f.put(t, "wave2", companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "", false, ""))
	for _, name := range []string{companionManifestName, "clips/wave2.clip.json"} {
		info, err := os.Stat(filepath.Join(f.dir, filepath.FromSlash(name)))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode: %v %v", name, info.Mode(), err)
		}
	}
}

func TestCompanionClipImportVRMA(t *testing.T) {
	f := newCompanionClipFixture(t, "")
	vrma := syntheticVRMAGLB()
	importPath := func(q string) string { return companionPacksRoute + "/studio/clips/import-vrma?" + q }
	if w := f.author("POST", importPath("key=bow"), f.rev(t), vrma); w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	entry, ok := companionCatalogEntry(f.pack(t, companionClipTestPack), "bow")
	if !ok || entry.Kind != "vrma" || entry.Expose || entry.Description != "" || !entry.loaded || !bytes.Equal(f.file(t, "clips/bow.vrma"), vrma) {
		t.Fatalf("imported entry: %+v", entry)
	}
	if w := f.author("POST", importPath("key=bow"), f.rev(t), vrma); w.Code != http.StatusConflict {
		t.Fatalf("existing key without replace: %d", w.Code)
	}
	f.put(t, "bow", "", companionTestEntryJSON("bow", "vrma", "Bow politely", true, ""))
	if w := f.author("POST", importPath("key=bow&replace=1"), f.rev(t), vrma); w.Code != 200 {
		t.Fatalf("replace: %d %s", w.Code, w.Body)
	}
	if entry, _ = companionCatalogEntry(f.pack(t, companionClipTestPack), "bow"); entry.Expose || entry.Description != "Bow politely" {
		t.Fatalf("replace must reset expose and keep the description: %+v", entry)
	}
	if w := f.author("POST", importPath("key=bow&replace=1&expose=1"), f.rev(t), vrma); w.Code != 200 {
		t.Fatalf("replace+expose: %d", w.Code)
	}
	if entry, _ = companionCatalogEntry(f.pack(t, companionClipTestPack), "bow"); !entry.Expose {
		t.Fatal("expose=1 not applied")
	}
	if w := f.author("POST", importPath("key=idle"), f.rev(t), vrma); w.Code != 400 {
		t.Fatalf("sentinel without flag: %d", w.Code)
	}
	if w := f.author("POST", importPath("key=bow&sentinel=1&replace=1"), f.rev(t), vrma); w.Code != 400 {
		t.Fatalf("non-sentinel with flag: %d", w.Code)
	}
	if w := f.author("POST", importPath("key=idle&sentinel=1"), f.rev(t), vrma); w.Code != 200 {
		t.Fatalf("sentinel import: %d %s", w.Code, w.Body)
	}
	if e, ok := f.pack(t, companionClipTestPack).catalog.sentinels["idle"]; !ok || !e.loaded || !strings.Contains(string(f.file(t, companionManifestName)), `"sentinels"`) {
		t.Fatal("sentinel not stored")
	}
	if w := f.author("POST", importPath("key=wave3"), f.rev(t), []byte("not a glb")); w.Code != 400 {
		t.Fatalf("invalid vrma: %d", w.Code)
	}
	// A kind change removes the other file.
	f.put(t, "bow", companionTestClipJSON("bow", false, ""), companionTestEntryJSON("bow", "clip", "Bow", false, ""))
	f.absent(t, "clips/bow.vrma")
	f.file(t, "clips/bow.clip.json")
}

func TestCompanionClipWriteAtomicity(t *testing.T) {
	f := newCompanionClipFixture(t, `,"props":[{"id":"Mug","kind":"blob"}]`)
	v1 := companionTestClipJSON("wave2", false, "")
	f.put(t, "wave2", v1, companionTestEntryJSON("wave2", "clip", "one", false, ""))
	if !strings.Contains(string(f.file(t, companionManifestName)), `"props": [{"id":"Mug","kind":"blob"}]`) {
		t.Fatal("a malformed reserved field was not kept byte-for-byte")
	}
	clipBefore, manifestBefore := f.file(t, "clips/wave2.clip.json"), f.file(t, companionManifestName)
	packBefore := f.pack(t, companionClipTestPack)
	f.d.companionPacks.fault = func(op, name string) error {
		if op == "rename" && name == companionManifestName {
			return errors.New("injected pack.json replace failure")
		}
		return nil
	}
	w := f.author("PUT", companionClipPath(companionClipTestPack, "wave2"), packBefore.info.Rev,
		companionTestPutBody(companionTestClipJSON("wave2", true, ""), companionTestEntryJSON("wave2", "clip", "two", false, "")))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed replace: %d %s", w.Code, w.Body)
	}
	w = f.author("PUT", companionClipPath(companionClipTestPack, "nod"), packBefore.info.Rev,
		companionTestPutBody(companionTestClipJSON("nod", true, ""), companionTestEntryJSON("nod", "clip", "", false, "")))
	f.d.companionPacks.fault = nil
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("failed new key: %d", w.Code)
	}
	if !bytes.Equal(f.file(t, "clips/wave2.clip.json"), clipBefore) || !bytes.Equal(f.file(t, companionManifestName), manifestBefore) {
		t.Fatal("a failed pack.json replace left a changed file")
	}
	f.absent(t, "clips/nod.clip.json")
	if f.pack(t, companionClipTestPack) != packBefore {
		t.Fatal("a failed write swapped the snapshot")
	}
	entries, _ := os.ReadDir(filepath.Join(f.dir, "clips"))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
	top, _ := os.ReadDir(f.dir)
	for _, e := range top {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("pack.json temp left behind: %s", e.Name())
		}
	}
	// A clip-only change keeps pack.json untouched.
	renames := map[string]int{}
	f.d.companionPacks.fault = func(op, name string) error { renames[op+":"+name]++; return nil }
	f.put(t, "wave2", companionTestClipJSON("wave2", true, ""), companionTestEntryJSON("wave2", "clip", "one", false, ""))
	f.d.companionPacks.fault = nil
	if renames["rename:"+companionManifestName] != 0 || renames["rename:wave2.clip.json"] != 1 || !bytes.Equal(f.file(t, companionManifestName), manifestBefore) {
		t.Fatalf("unchanged entry rewrote pack.json: %v", renames)
	}
}

func TestCompanionClipDriftRefused(t *testing.T) {
	f := newCompanionClipFixture(t, "")
	rev := f.rev(t)
	edited := append(f.file(t, companionManifestName), '\n')
	companionTestWriteFile(t, f.dir, companionManifestName, edited)
	w := f.author("PUT", companionClipPath(companionClipTestPack, "wave2"), rev,
		companionTestPutBody(companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "", false, "")))
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), companionDriftMsg) {
		t.Fatalf("drift: %d %s", w.Code, w.Body)
	}
	if !bytes.Equal(f.file(t, companionManifestName), edited) {
		t.Fatal("drift write changed pack.json")
	}
	f.absent(t, "clips")
}

func TestCompanionClipFileOpBudget(t *testing.T) {
	f := newCompanionClipFixture(t, "")
	companionTestVRMPack(t, f.home, "other", companionTestVRMManifest("other", "model.vrm", ""), syntheticVRM1GLB(), nil)
	before := f.d.companionPacks.snapshot(f.opts())
	otherPtr, defaultPtr := before.byID["other"], before.byID[companionDefaultID]
	var mu sync.Mutex
	ops := map[string]int{}
	f.d.companionPacks.trace = func(op string) { mu.Lock(); ops[op]++; mu.Unlock() }
	total := func() int {
		n := 0
		for _, c := range ops {
			n += c
		}
		return n
	}
	f.put(t, "wave2", companionTestClipJSON("wave2", false, ""), companionTestEntryJSON("wave2", "clip", "one", false, ""))
	if n := total(); n > 16 || ops["read"] != 1 || ops["mkdir"] != 1 {
		t.Fatalf("first save: %d ops %v", n, ops)
	}
	clear(ops)
	f.put(t, "wave2", companionTestClipJSON("wave2", true, ""), companionTestEntryJSON("wave2", "clip", "two", false, ""))
	if n := total(); n > 14 || ops["read"] != 1 || ops["rename"] != 2 {
		t.Fatalf("steady save: %d ops %v", n, ops)
	}
	f.d.companionPacks.trace = nil
	after := f.d.companionPacks.snapshot(f.opts())
	if after == before || after.byID["other"] != otherPtr || after.byID[companionDefaultID] != defaultPtr {
		t.Fatal("the swap replaced other packs' pointers")
	}
	if len(after.packs) != len(before.packs) {
		t.Fatal("the swap changed the pack list")
	}
}

func TestCompanionClipConcurrentWrites(t *testing.T) {
	f := newCompanionClipFixture(t, "")
	bundle := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	// putRetry writes key, retrying on 412 with the reported revision. It runs
	// off the test goroutine, so it reports errors instead of calling t.Fatal.
	current := func() *companionPack { return f.d.companionPacks.snapshot(f.opts()).byID[companionClipTestPack] }
	putRetry := func(key string) error {
		pack := current()
		if pack == nil {
			return fmt.Errorf("%s: pack not loaded", key)
		}
		rev := pack.info.Rev
		body := companionTestPutBody(companionTestClipJSON(key, false, ""), companionTestEntryJSON(key, "clip", "", false, ""))
		for attempt := 0; attempt < 200; attempt++ {
			w := f.author("PUT", companionClipPath(companionClipTestPack, key), rev, body)
			switch w.Code {
			case http.StatusOK:
				if p := current(); p == nil || !companionTestHasAction(p, key) {
					return fmt.Errorf("%s missing from the snapshot right after its PUT", key)
				}
				return nil
			case http.StatusPreconditionFailed:
				rev = w.Header().Get("X-Companion-Current-Rev")
			default:
				return fmt.Errorf("%s: %d %s", key, w.Code, w.Body)
			}
		}
		return fmt.Errorf("%s: retries exhausted", key)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	stop := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				f.d.companionPacks.reload(f.opts())
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			if _, err := companionTestRun(&f.d.companionPacks, f.home, bundle, nil, false); err != nil {
				errs <- err
			}
		}
	}()
	var writers sync.WaitGroup
	for i := 0; i < 6; i++ {
		writers.Add(1)
		go func(i int) {
			defer writers.Done()
			if err := putRetry(fmt.Sprintf("k%d", i)); err != nil {
				errs <- err
			}
		}(i)
	}
	writers.Wait()
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	pack := f.pack(t, companionClipTestPack)
	for i := 0; i < 6; i++ {
		if !companionTestHasAction(pack, fmt.Sprintf("k%d", i)) {
			t.Fatalf("k%d lost", i)
		}
	}
	if again := f.d.companionPacks.reload(f.opts()).byID[companionClipTestPack]; again.info.Rev != pack.info.Rev {
		t.Fatalf("snapshot and disk disagree: %s vs %s", pack.info.Rev, again.info.Rev)
	}
	if f.pack(t, "alpha") == nil {
		t.Fatal("install did not land")
	}
}

func TestCompanionClipInstallRefusesAuthoredPack(t *testing.T) {
	home := t.TempDir()
	opts := wsconfig.Options{ConfigHome: home}
	store := &CompanionPackStore{}
	bundle := companionTestZip(t, companionTestBundle(t, "", "alpha", "beta")...)
	if _, err := companionTestRun(store, home, bundle, []string{"alpha"}, false); err != nil {
		t.Fatal(err)
	}
	// A local pack without authored content stays replaceable.
	if result, err := companionTestRun(store, home, bundle, []string{"alpha"}, false); err != nil {
		t.Fatal(err)
	} else if item := companionListed(result, "alpha"); item == nil || item.Error != nil {
		t.Fatalf("unauthored local alpha refused: %+v", item)
	}
	// alpha becomes a Studio-authored VRM pack that keeps its local sidecar.
	alpha := filepath.Join(home, "companions", "alpha")
	for _, name := range []string{"pack.json", "stand.png"} {
		if err := os.Remove(filepath.Join(alpha, name)); err != nil {
			t.Fatal(err)
		}
	}
	authored := companionTestVRMManifest("alpha", "model.vrm", `,"actions":[`+companionTestEntryJSON("wave2", "clip", "", false, "")+`]`)
	companionTestVRMPack(t, home, "alpha", authored, syntheticVRM1GLB(), nil)
	companionTestWriteFile(t, alpha, "clips/wave2.clip.json", []byte(companionTestClipJSON("wave2", false, "")))
	// beta is hand-made (no sidecar) and authored.
	companionTestVRMPack(t, home, "beta", companionTestVRMManifest("beta", "model.vrm", ""), syntheticVRM1GLB(), nil)
	pack := store.reload(opts).byID["alpha"]
	if pack == nil || pack.info.Origin == nil || pack.info.Origin.Kind != "local" || !pack.catalog.authored(pack.clips) {
		t.Fatalf("fixture: %+v", pack)
	}
	result, err := companionTestRun(store, home, bundle, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	item := companionListed(result, "alpha")
	if item == nil || item.Error == nil || !strings.Contains(item.Error.Message, "Studio-authored") {
		t.Fatalf("alpha listing: %+v", item)
	}
	if beta := companionListed(result, "beta"); beta == nil || beta.Status != companionStatusManual {
		t.Fatalf("beta listing: %+v", beta)
	}
	for _, id := range []string{"alpha", "beta"} {
		got, err := os.ReadFile(filepath.Join(home, "companions", id, "pack.json"))
		if err != nil || !strings.Contains(string(got), "ws-companion-vrm/v1") {
			t.Fatalf("%s replaced: %s %v", id, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(alpha, "clips", "wave2.clip.json")); err != nil {
		t.Fatal("authored clip lost")
	}
}
