package wsdashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

type companionRouteFixture struct {
	d      *Daemon
	home   string
	cookie *http.Cookie
}

func newCompanionRouteFixture(t *testing.T) companionRouteFixture {
	t.Helper()
	home := t.TempDir()
	d := authFixture(t)
	d.config = &ConfigBackend{Options: wsconfig.Options{ConfigHome: home}}
	d.lan = true
	return companionRouteFixture{d: d, home: home, cookie: authCookie(t, d)}
}

// post sends an authenticated same-origin POST with body.
func (f companionRouteFixture) post(path string, body []byte) *httptest.ResponseRecorder {
	r := authRequest("POST", path, string(body))
	r.AddCookie(f.cookie)
	r.Header.Set("Origin", companionTestOrigin)
	return authServe(f.d, r)
}

func (f companionRouteFixture) packDir(id string) string {
	return filepath.Join(f.home, "companions", id)
}

func companionRouteDecode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %d %q: %v", w.Code, w.Body.String(), err)
	}
	return v
}

func companionRouteCatalogPack(c *companionCatalog, id string) *companionPackInfo {
	if c == nil {
		return nil
	}
	for i := range c.Packs {
		if c.Packs[i].ID == id {
			return &c.Packs[i]
		}
	}
	return nil
}

func TestCompanionPackRoutesRefuseUntrusted(t *testing.T) {
	f := newCompanionRouteFixture(t)
	body := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	token, err := f.d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		companionPacksRoute + "/validate?name=a.zip",
		companionPacksRoute + "/import?name=a.zip&id=alpha",
		companionPacksRoute + "/alpha/uninstall",
	} {
		send := func(method, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
			r := authRequest(method, path, string(body))
			if cookie != nil {
				r.AddCookie(cookie)
			}
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			return authServe(f.d, r)
		}
		if w := send("POST", companionTestOrigin, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("%s unauthenticated: %d", path, w.Code)
		}
		if w := send("POST", "http://evil.test", f.cookie); w.Code != http.StatusForbidden {
			t.Errorf("%s cross-origin: %d", path, w.Code)
		}
		r := authRequest("POST", path, string(body))
		r.Host, r.RemoteAddr = "192.0.2.1:43210", "192.0.2.2:1234"
		r.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
		r.Header.Set("Origin", "http://"+r.Host)
		if w := authServe(f.d, r); w.Code != http.StatusNotFound {
			t.Errorf("%s LAN: %d", path, w.Code)
		}
		w := send("GET", "", f.cookie)
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" ||
			w.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s GET: %d %+v", path, w.Code, w.Header())
		}
	}
	if f.d.companionPacks.active.Load() != nil {
		t.Fatal("a refused request activated the store")
	}
	if _, err := os.Stat(filepath.Join(f.home, "companions")); !os.IsNotExist(err) {
		t.Fatalf("a refused request wrote the pack directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.home, companionStagingName)); !os.IsNotExist(err) {
		t.Fatalf("a refused request wrote staging: %v", err)
	}
}

func TestCompanionPackValidateRoute(t *testing.T) {
	f := newCompanionRouteFixture(t)
	body := companionTestZip(t, companionTestBundle(t, "", "alpha", "beta")...)
	w := f.post(companionPacksRoute+"/validate?name=my%20packs.zip", body)
	if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("validate: %d %s %+v", w.Code, w.Body, w.Header())
	}
	result := companionRouteDecode[companionInstallResult](t, w)
	if result.Catalog != nil || result.Listing.Source != "my packs.zip" || result.Listing.Kind != "local" {
		t.Fatalf("listing header: %+v", result.Listing)
	}
	for _, id := range []string{"alpha", "beta"} {
		p := companionListed(result, id)
		if p == nil || p.Status != companionStatusAvailable || p.Rev == "" || p.Error != nil {
			t.Fatalf("%s: %+v", id, p)
		}
	}
	if _, err := os.Stat(filepath.Join(f.home, "companions")); !os.IsNotExist(err) {
		t.Fatalf("validate created the pack directory: %v", err)
	}

	// A marketplace-level failure is a 422 whose listing holds one error.
	bad := companionTestZip(t, companionZipEntry{name: "marketplace.json", data: []byte(`{"schema":"other"}`)})
	w = f.post(companionPacksRoute+"/validate?name=bad.zip", bad)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad marketplace: %d %s", w.Code, w.Body)
	}
	result = companionRouteDecode[companionInstallResult](t, w)
	if len(result.Listing.Errors) != 1 || result.Listing.Errors[0].Message == "" || result.Listing.Source != "bad.zip" ||
		result.Listing.Kind != "local" || result.Listing.Packs == nil || len(result.Listing.Packs) != 0 {
		t.Fatalf("bad marketplace listing: %+v", result.Listing)
	}
	if !strings.Contains(w.Body.String(), `"packs":[]`) {
		t.Fatalf("packs must be an empty array: %s", w.Body)
	}
}

func TestCompanionPackImportRoute(t *testing.T) {
	f := newCompanionRouteFixture(t)
	base := companionPacksRoute
	body := companionTestZip(t, companionTestBundle(t, "", "alpha", "beta")...)
	sum := sha256.Sum256(body)
	w := f.post(base+"/import?name=C:%5Cdownloads%5Cmy.zip&id=alpha&id=alpha", body)
	if w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	result := companionRouteDecode[companionInstallResult](t, w)
	if p := companionListed(result, "alpha"); p == nil || !p.Installed || p.Error != nil {
		t.Fatalf("alpha: %+v", p)
	}
	if p := companionListed(result, "beta"); p == nil || p.Installed || p.Status != companionStatusAvailable {
		t.Fatalf("beta: %+v", p)
	}
	if _, err := os.Stat(f.packDir("beta")); !os.IsNotExist(err) {
		t.Fatalf("unselected pack written: %v", err)
	}
	alpha := companionRouteCatalogPack(result.Catalog, "alpha")
	if alpha == nil || companionRouteCatalogPack(result.Catalog, "beta") != nil {
		t.Fatalf("catalog: %+v", result.Catalog)
	}
	if o := alpha.Origin; o == nil || o.Kind != "local" || o.Source != "my.zip" || o.ArchiveSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("origin: %+v", alpha.Origin)
	}
	oldRev := alpha.Rev

	// The same zip again is unchanged; a changed zip validates as an update
	// and then replaces the pack.
	changed := companionTestZip(t, companionTestZip2(t)...)
	w = f.post(base+"/validate?name=my.zip", changed)
	if p := companionListed(companionRouteDecode[companionInstallResult](t, w), "alpha"); w.Code != 200 || p == nil ||
		p.Status != companionStatusUpdate || p.Rev == oldRev {
		t.Fatalf("validate changed: %d %s", w.Code, w.Body)
	}
	if w = f.post(base+"/validate?name=my.zip", body); companionListed(companionRouteDecode[companionInstallResult](t, w), "alpha").Status != companionStatusInstalled {
		t.Fatalf("validate unchanged: %s", w.Body)
	}
	w = f.post(base+"/import?name=my.zip&id=alpha", changed)
	if w.Code != 200 {
		t.Fatalf("re-import: %d %s", w.Code, w.Body)
	}
	result = companionRouteDecode[companionInstallResult](t, w)
	if updated := companionRouteCatalogPack(result.Catalog, "alpha"); updated == nil || updated.Rev == oldRev {
		t.Fatalf("pack not updated: %+v", updated)
	}
}

// companionTestZip2 is the alpha bundle with a different raster, so its
// revision differs from companionTestBundle's.
func companionTestZip2(t *testing.T) []companionZipEntry {
	entries := companionTestBundle(t, "", "alpha", "beta")
	for i := range entries {
		if entries[i].name == "packs/alpha/stand.png" {
			entries[i].data = companionTestPNG(t, 9, 9)
		}
	}
	return entries
}

func TestCompanionPackImportRouteRejects(t *testing.T) {
	f := newCompanionRouteFixture(t)
	base := companionPacksRoute
	body := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	for _, q := range []string{"name=a.zip", "name=a.zip&id=", "name=a.zip&id=Bad%20ID", "name=a.zip&id=alpha&id=../x"} {
		if w := f.post(base+"/import?"+q, body); w.Code != http.StatusBadRequest {
			t.Errorf("%q: %d %s", q, w.Code, w.Body)
		}
	}
	w := f.post(base+"/import?name=a.zip&id=alpha", []byte("this is plain text, not an archive"))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("non-zip: %d %s", w.Code, w.Body)
	}
	result := companionRouteDecode[companionInstallResult](t, w)
	if len(result.Listing.Errors) != 1 || !strings.Contains(result.Listing.Errors[0].Message, "not a zip archive") ||
		!strings.Contains(result.Listing.Errors[0].Message, ".zip") {
		t.Fatalf("non-zip hint: %+v", result.Listing.Errors)
	}
	if w = f.post(base+"/validate?name=a.zip", nil); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("empty body: %d", w.Code)
	}

	old := companionUploadCap
	companionUploadCap = int64(len(body)) - 1
	t.Cleanup(func() { companionUploadCap = old })
	for _, path := range []string{base + "/import?name=a.zip&id=alpha", base + "/validate?name=a.zip"} {
		if w = f.post(path, body); w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("%s over cap: %d %s", path, w.Code, w.Body)
		}
	}
	companionUploadCap = int64(len(body))
	if w = f.post(base+"/validate?name=a.zip", body); w.Code != 200 {
		t.Errorf("at cap: %d", w.Code)
	}
	if _, err := os.Stat(filepath.Join(f.home, "companions")); !os.IsNotExist(err) {
		t.Fatalf("a rejected import wrote the pack directory: %v", err)
	}
}

func TestCompanionPackUninstallRoute(t *testing.T) {
	f := newCompanionRouteFixture(t)
	base := companionPacksRoute
	hand := companionTestPack(t, f.home, "hand", companionTestManifest("hand", "stand.webp", ""))
	body := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	if w := f.post(base+"/import?name=a.zip&id=alpha", body); w.Code != 200 {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	w := f.post(base+"/alpha/uninstall", nil)
	if w.Code != 200 {
		t.Fatalf("uninstall: %d %s", w.Code, w.Body)
	}
	cat := companionRouteDecode[companionCatalog](t, w)
	if companionRouteCatalogPack(&cat, "alpha") != nil || companionRouteCatalogPack(&cat, "hand") == nil {
		t.Fatalf("catalog after uninstall: %+v", cat)
	}
	if _, err := os.Stat(f.packDir("alpha")); !os.IsNotExist(err) {
		t.Fatalf("pack directory kept: %v", err)
	}
	for _, tc := range []struct {
		id   string
		code int
	}{{"hand", 409}, {"missing", 404}, {"alpha", 404}, {"default", 409}} {
		w = f.post(base+"/"+url.PathEscape(tc.id)+"/uninstall", nil)
		if w.Code != tc.code || companionRouteDecode[map[string]string](t, w)["error"] == "" {
			t.Errorf("uninstall %s: %d %s", tc.id, w.Code, w.Body)
		}
	}
	if got := companionDirFiles(t, hand); len(got) != 2 {
		t.Fatalf("hand-made pack touched: %v", got)
	}
}

func TestCompanionPackUploadName(t *testing.T) {
	long := strings.Repeat("é", 200)
	for _, tc := range []struct{ in, want string }{
		{"packs.zip", "packs.zip"},
		{"  spaced.zip  ", "spaced.zip"},
		{"C:\\Users\\me\\a.zip", "a.zip"},
		{"/tmp/x/b.zip", "b.zip"},
		{"", "upload.zip"},
		{"dir/", "upload.zip"},
		{"   ", "upload.zip"},
		{"bad\x00name.zip", "upload.zip"},
		{"tab\tname.zip", "upload.zip"},
		{"\xff\xfe.zip", "upload.zip"},
		{long, strings.Repeat("é", 128)},
	} {
		if got := companionUploadName(tc.in); got != tc.want {
			t.Errorf("%q: got %q, want %q", tc.in, got, tc.want)
		}
	}
}
