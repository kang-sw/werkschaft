package wsdashboard

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

const companionTestOrigin = "http://127.0.0.1:43210"

func companionTestRequest(d *Daemon, cookie *http.Cookie, method, path, origin string) *httptest.ResponseRecorder {
	r := authRequest(method, path, "")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	return authServe(d, r)
}

func TestCompanionPackHTTP(t *testing.T) {
	home := t.TempDir()
	manifest := companionTestManifest("one", "stand.webp", "")
	dir := companionTestPack(t, home, "one", manifest)
	if err := os.WriteFile(filepath.Join(dir, "hidden.png"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := authFixture(t)
	d.config = &ConfigBackend{Options: wsconfig.Options{ConfigHome: home}}
	d.lan = true
	cookie := authCookie(t, d)
	base := companionPacksRoute
	if w := companionTestRequest(d, nil, "GET", base, ""); w.Code != 401 {
		t.Fatalf("unauth read: %d", w.Code)
	}
	if w := companionTestRequest(d, nil, "POST", base+"/reload", companionTestOrigin); w.Code != 401 {
		t.Fatalf("unauth reload: %d", w.Code)
	}
	if w := companionTestRequest(d, cookie, "POST", base+"/reload", "http://evil.test"); w.Code != 403 {
		t.Fatalf("cross-origin reload: %d", w.Code)
	}
	token, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	lanRequest := func(method, path string) *httptest.ResponseRecorder {
		r := authRequest(method, path, "")
		r.Host, r.RemoteAddr = "192.0.2.1:43210", "192.0.2.2:1234"
		r.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
		r.Header.Set("Origin", "http://"+r.Host)
		return authServe(d, r)
	}
	if w := lanRequest("POST", base+"/reload"); w.Code != http.StatusNotFound {
		t.Fatalf("LAN reload: %d", w.Code)
	}
	if d.companionPacks.active.Load() != nil {
		t.Fatal("unauthorized request activated the store")
	}
	if w := lanRequest("GET", base); w.Code != http.StatusOK {
		t.Fatalf("LAN read: %d", w.Code)
	}
	w := companionTestRequest(d, cookie, "GET", base, "")
	var cat companionCatalog
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &cat) != nil {
		t.Fatalf("catalog %d %s", w.Code, w.Body.String())
	}
	rev := ""
	for _, p := range cat.Packs {
		if p.ID == "one" {
			rev = p.Rev
		}
	}
	if rev == "" {
		t.Fatal("local pack missing")
	}
	if w := companionTestRequest(d, cookie, "GET", base+"/one/pack.json", ""); w.Body.String() != string(manifest) {
		t.Fatalf("manifest body changed: %s", w.Body)
	}
	if w := companionTestRequest(d, cookie, "GET", base+"/default/assets/stand.png", ""); w.Code != 200 ||
		w.Header().Get("Content-Type") != "image/png" || w.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
		t.Fatalf("embedded PNG: %d %+v", w.Code, w.Header())
	}
	for _, tc := range []struct{ path, typ string }{{"/one/pack.json", "application/json"}, {"/one/assets/stand.webp", "image/webp"}} {
		for _, query := range []string{"", "?rev=" + rev} {
			w = companionTestRequest(d, cookie, "GET", base+tc.path+query, "")
			cache := "private, no-cache"
			if query != "" {
				cache = "private, max-age=31536000, immutable"
			}
			if w.Code != 200 || w.Header().Get("Content-Type") != tc.typ || w.Header().Get("ETag") != `"`+rev+`"` || w.Header().Get("Cache-Control") != cache || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("%s%s: %d %+v", tc.path, query, w.Code, w.Header())
			}
			if tc.typ == "image/webp" && w.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" {
				t.Fatal("missing raster CSP")
			}
			r := authRequest("GET", base+tc.path+query, "")
			r.AddCookie(cookie)
			r.Header.Set("If-None-Match", `"`+rev+`"`)
			if w = authServe(d, r); w.Code != 304 {
				t.Fatalf("etag: %d", w.Code)
			}
		}
		w = companionTestRequest(d, cookie, "GET", base+tc.path+"?rev=unknown", "")
		if w.Code != 409 || w.Header().Get("X-Companion-Current-Rev") != rev || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("unknown rev: %d %+v", w.Code, w.Header())
		}
	}
	for _, path := range []string{"/one/assets/hidden.png", "/one/assets/%2e%2e%2fstand.webp", "/one/assets/stand.svg", "/missing/pack.json"} {
		if w = companionTestRequest(d, cookie, "GET", base+path, ""); w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "stand.webp"), []byte("new raster"), 0o600); err != nil {
		t.Fatal(err)
	}
	w = companionTestRequest(d, cookie, "POST", base+"/reload", companionTestOrigin)
	if w.Code != 200 {
		t.Fatalf("reload %d %s", w.Code, w.Body.String())
	}
	w = companionTestRequest(d, cookie, "GET", base+"/one/assets/stand.webp?rev="+rev, "")
	r := authRequest("GET", base+"/one/assets/stand.webp?rev="+rev, "")
	r.AddCookie(cookie)
	r.Header.Set("If-None-Match", `"`+rev+`"`)
	if stale := authServe(d, r); stale.Code != http.StatusConflict {
		t.Fatalf("stale matching ETag: %d", stale.Code)
	}
	newRev := w.Header().Get("X-Companion-Current-Rev")
	if w.Code != 409 || newRev == "" || newRev == rev {
		t.Fatalf("stale %d %s", w.Code, newRev)
	}
	if w = companionTestRequest(d, cookie, "GET", base+"/one/assets/stand.webp?rev="+newRev, ""); w.Code != 200 || w.Body.String() != "new raster" {
		t.Fatalf("new revision: %d %s", w.Code, w.Body.String())
	}
	if err := os.Remove(filepath.Join(dir, "stand.webp")); err != nil {
		t.Fatal(err)
	}
	if w := companionTestRequest(d, cookie, "POST", base+"/reload", companionTestOrigin); w.Code != 200 {
		t.Fatalf("reload after removing asset: %d %s", w.Code, w.Body)
	}
	if w = companionTestRequest(d, cookie, "GET", base+"/one/assets/stand.webp?rev="+newRev, ""); w.Code != 404 {
		t.Fatalf("removed file: %d", w.Code)
	}
	if err := os.Remove(filepath.Join(dir, "hidden.png")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "pack.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if w := companionTestRequest(d, cookie, "POST", base+"/reload", companionTestOrigin); w.Code != 200 {
		t.Fatalf("reload after removing pack: %d %s", w.Code, w.Body)
	}
	if w = companionTestRequest(d, cookie, "GET", base+"/one/pack.json?rev="+newRev, ""); w.Code != 404 {
		t.Fatalf("removed pack: %d", w.Code)
	}
}

func TestCompanionPackConcurrentReload(t *testing.T) {
	home := t.TempDir()
	dir := companionTestPack(t, home, "one", companionTestManifest("one", "stand.webp", ""))
	d := authFixture(t)
	d.config = &ConfigBackend{Options: wsconfig.Options{ConfigHome: home}}
	cookie := authCookie(t, d)
	base := companionPacksRoute
	w := companionTestRequest(d, cookie, "GET", base+"/one/pack.json", "")
	old := w.Header().Get("ETag")
	if w.Code != 200 || len(old) < 2 {
		t.Fatalf("initial: %d", w.Code)
	}
	if err := os.WriteFile(filepath.Join(dir, "stand.webp"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	d.companionPacks.open = func(p companionPackFile, n string, dir bool) (companionPackFile, error) {
		if p == nil {
			close(entered)
			<-release
		}
		return openCompanionPackEntry(p, n, dir)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- companionTestRequest(d, cookie, "POST", base+"/reload", companionTestOrigin) }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("reload did not begin")
	}
	read := make(chan *httptest.ResponseRecorder, 1)
	go func() { read <- companionTestRequest(d, cookie, "GET", base+"/one/pack.json", "") }()
	select {
	case got := <-read:
		if got.Code != 200 || got.Header().Get("ETag") != old {
			t.Errorf("warm reader: %d", got.Code)
		}
	case <-time.After(5 * time.Second):
		t.Error("warm reader blocked behind reload")
	}
	close(release)
	if got := <-done; got.Code != 200 {
		t.Fatalf("reload: %d", got.Code)
	}
	rev := old[1 : len(old)-1]
	if got := companionTestRequest(d, cookie, "GET", base+"/one/assets/stand.webp?rev="+rev, ""); got.Code != 409 || got.Header().Get("X-Companion-Current-Rev") == "" {
		t.Fatalf("stale after reload: %d", got.Code)
	}
}
