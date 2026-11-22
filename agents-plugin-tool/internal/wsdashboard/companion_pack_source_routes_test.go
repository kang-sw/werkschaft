package wsdashboard

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// companionSourceServer serves zip bodies by path over TLS and counts every
// request, so a test can prove when the daemon downloads.
type companionSourceServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	files map[string][]byte
	hits  int
}

func newCompanionSourceServer(t *testing.T) *companionSourceServer {
	t.Helper()
	s := &companionSourceServer{files: map[string][]byte{}}
	s.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.hits++
		data, ok := s.files[r.URL.Path]
		s.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *companionSourceServer) serve(path string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = data
}

func (s *companionSourceServer) requests() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits
}

// companionSourceFixture is a route fixture whose fetcher trusts srv and
// whose companion config is enabled with the given source list.
func companionSourceFixture(t *testing.T, srv *companionSourceServer, enabled bool, sources string) companionRouteFixture {
	t.Helper()
	f := newCompanionRouteFixture(t)
	client := companionFetchClient(srv.srv)
	f.d.companionFetch.tls, f.d.companionFetch.proxy, f.d.companionFetch.refuse = client.tls, client.proxy, client.refuse
	f.d.companion.snapshot.Store(&companionConfigSnapshot{wake: make(chan struct{}), config: CompanionConfig{
		Enabled: enabled, Director: "rules", Pack: "default", Preset: "werkschaft-d1", Sources: sources}})
	return f
}

func companionSourceBody(source string, ids ...string) []byte {
	raw, _ := json.Marshal(companionSourceRequest{Source: source, IDs: ids})
	return raw
}

func TestCompanionPackSourceRoutesGate(t *testing.T) {
	srv := newCompanionSourceServer(t)
	source := srv.srv.URL + "/packs.zip"
	srv.serve("/packs.zip", companionTestZip(t, companionTestBundle(t, "", "alpha")...))

	t.Run("untrusted", func(t *testing.T) {
		f := companionSourceFixture(t, srv, true, source)
		for _, path := range []string{companionSourcesRoute + "/fetch", companionSourcesRoute + "/install"} {
			r := authRequest("GET", path, "")
			r.AddCookie(f.cookie)
			r.Header.Set("Origin", companionTestOrigin)
			if w := authServe(f.d, r); w.Code != http.StatusMethodNotAllowed {
				t.Errorf("GET %s = %d, want 405", path, w.Code)
			}
			r = authRequest("POST", path, string(companionSourceBody(source, "alpha")))
			r.AddCookie(f.cookie)
			r.Header.Set("Origin", "https://example.invalid")
			if w := authServe(f.d, r); w.Code != http.StatusForbidden {
				t.Errorf("cross-origin %s = %d, want 403", path, w.Code)
			}
		}
	})
	t.Run("disabled companion fetches nothing", func(t *testing.T) {
		f := companionSourceFixture(t, srv, false, source)
		for _, body := range [][]byte{companionSourceBody(source), companionSourceBody(companionOfficialSource)} {
			if w := f.post(companionSourcesRoute+"/fetch", body); w.Code != http.StatusConflict {
				t.Errorf("fetch while disabled = %d %s, want 409", w.Code, w.Body)
			}
		}
		if w := f.post(companionSourcesRoute+"/install", companionSourceBody(source, "alpha")); w.Code != http.StatusConflict {
			t.Errorf("install while disabled = %d, want 409", w.Code)
		}
		// A local import whose marketplace lists an external pack downloads
		// nothing while the companion is off.
		external := companionTestZip(t, companionZipEntry{name: "marketplace.json", data: companionTestMarket(
			fmt.Sprintf(`{"id":"far","name":"Far","version":"1.0.0","url":%q,"sha256":%q}`, srv.srv.URL+"/far.zip", strings.Repeat("a", 64)))})
		w := f.post(companionPacksRoute+"/import?name=m.zip&id=far", external)
		result := companionRouteDecode[companionInstallResult](t, w)
		if far := companionListed(result, "far"); w.Code != http.StatusOK || far == nil || far.Error == nil ||
			!strings.Contains(far.Error.Message, "turn the companion on") {
			t.Errorf("import of an external pack while disabled = %d %s", w.Code, w.Body)
		}
	})
	t.Run("unlisted source and bad requests", func(t *testing.T) {
		f := companionSourceFixture(t, srv, true, source+" http://example.invalid/bad.zip")
		for _, tc := range []struct {
			route string
			body  []byte
		}{
			{"/fetch", companionSourceBody(srv.srv.URL + "/other.zip")},
			{"/fetch", companionSourceBody("http://example.invalid/bad.zip")},
			{"/fetch", []byte("not json")},
			{"/install", companionSourceBody(source)},
			{"/install", companionSourceBody(source, "Bad ID")},
		} {
			if w := f.post(companionSourcesRoute+tc.route, tc.body); w.Code != http.StatusBadRequest {
				t.Errorf("%s %s = %d %s, want 400", tc.route, tc.body, w.Code, w.Body)
			}
		}
	})
	t.Run("catalog and reload fetch nothing", func(t *testing.T) {
		f := companionSourceFixture(t, srv, true, source)
		r := authRequest("GET", companionPacksRoute, "")
		r.AddCookie(f.cookie)
		if w := authServe(f.d, r); w.Code != http.StatusOK {
			t.Fatalf("catalog = %d", w.Code)
		}
		if w := f.post(companionPacksRoute+"/reload", nil); w.Code != http.StatusOK {
			t.Fatalf("reload = %d %s", w.Code, w.Body)
		}
	})
	if n := srv.requests(); n != 0 {
		t.Fatalf("source server saw %d requests, want none", n)
	}
}

func TestCompanionPackSourceRoutesZip(t *testing.T) {
	srv := newCompanionSourceServer(t)
	source := srv.srv.URL + "/packs.zip"
	// The manifest's name claims to be official; a user source never is.
	bundle := companionTestBundle(t, "", "alpha", "beta")
	bundle[0].data = []byte(strings.Replace(string(bundle[0].data), `"name":"Fixture Packs"`, `"name":"Werkschaft official packs"`, 1))
	first := companionTestZip(t, bundle...)
	srv.serve("/packs.zip", first)
	f := companionSourceFixture(t, srv, true, source)

	w := f.post(companionSourcesRoute+"/fetch", companionSourceBody(source))
	listing := companionRouteDecode[companionInstallResult](t, w)
	if w.Code != http.StatusOK || listing.Listing.Kind != "zip" || listing.Listing.Official || listing.Listing.Source != source ||
		len(listing.Listing.Packs) != 2 || listing.Catalog != nil {
		t.Fatalf("fetch = %d %s", w.Code, w.Body)
	}
	for _, id := range []string{"alpha", "beta"} {
		if p := companionListed(listing, id); p.Status != companionStatusAvailable || p.Error != nil || p.Rev == "" {
			t.Fatalf("%s listed as %+v", id, p)
		}
	}
	if n := srv.requests(); n != 1 {
		t.Fatalf("fetch made %d requests, want 1", n)
	}

	// The install right after a listing uses the cached zip.
	w = f.post(companionSourcesRoute+"/install", companionSourceBody(source, "alpha"))
	installed := companionRouteDecode[companionInstallResult](t, w)
	alpha := companionRouteCatalogPack(installed.Catalog, "alpha")
	if w.Code != http.StatusOK || alpha == nil || alpha.Origin == nil || alpha.Origin.Kind != "zip" || alpha.Origin.Source != source ||
		alpha.Origin.ArchiveSHA256 != companionSum(first) || alpha.Origin.Official || companionRouteCatalogPack(installed.Catalog, "beta") != nil {
		t.Fatalf("install = %d %s", w.Code, w.Body)
	}
	if n := srv.requests(); n != 1 {
		t.Fatalf("install after a listing made %d requests in total, want the listing's 1", n)
	}

	// Every listing click downloads again: that is the update check.
	w = f.post(companionSourcesRoute+"/fetch", companionSourceBody(source))
	if p := companionListed(companionRouteDecode[companionInstallResult](t, w), "alpha"); p.Status != companionStatusInstalled {
		t.Fatalf("alpha after install listed as %+v", p)
	}
	changed := companionTestBundle(t, "", "alpha", "beta")
	changed[2].data = companionTestPNG(t, 9, 9) // alpha's stand.png
	srv.serve("/packs.zip", companionTestZip(t, changed...))
	w = f.post(companionSourcesRoute+"/fetch", companionSourceBody(source))
	if p := companionListed(companionRouteDecode[companionInstallResult](t, w), "alpha"); p.Status != companionStatusUpdate {
		t.Fatalf("alpha with changed content listed as %+v", p)
	}
	if n := srv.requests(); n != 3 {
		t.Fatalf("three listings made %d requests, want 3", n)
	}

	// An expired cache entry downloads again on install.
	f.d.companionSources.mu.Lock()
	f.d.companionSources.fetched = time.Now().Add(-companionSourceCacheTTL)
	f.d.companionSources.mu.Unlock()
	w = f.post(companionSourcesRoute+"/install", companionSourceBody(source, "alpha"))
	updated := companionRouteDecode[companionInstallResult](t, w)
	if p := companionListed(updated, "alpha"); w.Code != http.StatusOK || !p.Installed || p.Rev == alpha.Rev {
		t.Fatalf("update = %d %s", w.Code, w.Body)
	}
	if n := srv.requests(); n != 4 {
		t.Fatalf("install after expiry made %d requests in total, want 4", n)
	}

	// A failed download answers 502 with the error in the listing.
	srv.serve("/packs.zip", []byte("<html>share page</html>"))
	w = f.post(companionSourcesRoute+"/fetch", companionSourceBody(source))
	failed := companionRouteDecode[companionInstallResult](t, w)
	if w.Code != http.StatusBadGateway || len(failed.Listing.Errors) != 1 || !strings.Contains(failed.Listing.Errors[0].Message, "import it locally") {
		t.Fatalf("non-zip source = %d %s", w.Code, w.Body)
	}
}

func TestCompanionPackSourceRoutesOfficial(t *testing.T) {
	srv := newCompanionSourceServer(t)
	f := companionSourceFixture(t, srv, true, "")

	w := f.post(companionSourcesRoute+"/fetch", companionSourceBody(companionOfficialSource))
	listing := companionRouteDecode[companionInstallResult](t, w)
	if w.Code != http.StatusOK || !listing.Listing.Official || listing.Listing.Source != companionOfficialSource ||
		len(listing.Listing.Packs) != 1 || listing.Listing.Packs[0].ID != companionDefaultID ||
		listing.Listing.Packs[0].Status != companionStatusBuiltin || listing.Listing.Packs[0].Rev == "" {
		t.Fatalf("official listing = %d %s", w.Code, w.Body)
	}

	// A later release lists official packs by url and sha256.
	packZip := companionTestZip(t,
		companionZipEntry{name: "pack.json", data: companionTestManifest("extra", "stand.png", "")},
		companionZipEntry{name: "stand.png", data: companionTestPNG(t, 8, 8)})
	srv.serve("/extra.zip", packZip)
	saved := companionOfficialMarketplace
	t.Cleanup(func() { companionOfficialMarketplace = saved })
	companionOfficialMarketplace = []byte(fmt.Sprintf(`{"schema":"ws-companion-marketplace/v1","name":"Official","packs":[`+
		`{"id":"extra","name":"Extra","version":"1.0.0","url":%q,"sha256":%q}]}`, srv.srv.URL+"/extra.zip", companionSum(packZip)))

	w = f.post(companionSourcesRoute+"/fetch", companionSourceBody(companionOfficialSource))
	listing = companionRouteDecode[companionInstallResult](t, w)
	if extra := companionListed(listing, "extra"); w.Code != http.StatusOK || extra == nil || extra.Status != companionStatusAvailable {
		t.Fatalf("official listing with a url pack = %d %s", w.Code, w.Body)
	}
	if n := srv.requests(); n != 0 {
		t.Fatalf("an official listing made %d requests, want none", n)
	}
	w = f.post(companionSourcesRoute+"/install", companionSourceBody(companionOfficialSource, "extra"))
	installed := companionRouteDecode[companionInstallResult](t, w)
	extra := companionRouteCatalogPack(installed.Catalog, "extra")
	if w.Code != http.StatusOK || extra == nil || extra.Origin == nil || !extra.Origin.Official ||
		extra.Origin.Source != companionOfficialSource || extra.Origin.ArchiveSHA256 != companionSum(packZip) {
		t.Fatalf("official install = %d %s", w.Code, w.Body)
	}
	if n := srv.requests(); n != 1 {
		t.Fatalf("an official install made %d requests, want 1", n)
	}
	if p := companionListed(installed, companionDefaultID); p == nil || p.Status != companionStatusBuiltin {
		t.Fatalf("the built-in pack left the official listing: %s", w.Body)
	}
}
