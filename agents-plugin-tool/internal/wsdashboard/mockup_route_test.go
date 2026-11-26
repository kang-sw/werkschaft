package wsdashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

type mockupFakeSource struct {
	*pageFake
	boardDir string
	instance uint64
	bundle   wswhiteboard.MockupBundle
	reads    atomic.Int32
	evictor  func(string)
}

func (m *mockupFakeSource) LookupMockup(reg Registration, entryID string) (MockupLookup, bool, error) {
	if reg.ReplyID != "boardsession" || entryID != m.bundle.EntryID {
		return MockupLookup{}, false, nil
	}
	return MockupLookup{
		BoardDir: m.boardDir,
		Instance: m.instance,
		EntryID:  m.bundle.EntryID,
		RootFile: m.bundle.RootFile,
		SHA256:   m.bundle.SHA256,
	}, true, nil
}

func (m *mockupFakeSource) ReadMockupBundle(reg Registration, entryID string) (wswhiteboard.MockupBundle, bool, error) {
	if reg.ReplyID != "boardsession" || entryID != m.bundle.EntryID {
		return wswhiteboard.MockupBundle{}, false, nil
	}
	m.reads.Add(1)
	return m.bundle, true, nil
}

func (m *mockupFakeSource) SetMockupEvictor(fn func(boardDir string)) {
	m.evictor = fn
}

func startMockupFixture(t *testing.T, lan bool) (*Daemon, string, *mockupFakeSource, string) {
	t.Helper()
	project := t.TempDir()
	mockDir := filepath.Join(project, "ai-docs", "assets", "mockups", "261126", "demo")
	if err := os.MkdirAll(filepath.Join(mockDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index.html":      `<!DOCTYPE html><html><head><link rel="stylesheet" href="app.css"></head><body><h1>Mock</h1><script src="app.js"></script></body></html>`,
		"app.css":         `body { color: #222; }`,
		"app.js":          `fetch('data/state.json');`,
		"data/state.json": `{"ok":true}`,
	}
	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(mockDir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bundle, err := wswhiteboard.CollectMockupBundle(project, "ai-docs/assets/mockups/261126/demo/index.html")
	if err != nil {
		t.Fatalf("CollectMockupBundle: %v", err)
	}
	bundle.EntryID = "e5"

	dir := t.TempDir()
	now := time.Now()
	register(t, dir, "boardsession", project, now)
	register(t, dir, "othersession", project, now)
	baseFake := newPageFake()
	baseFake.boards["boardsession"] = closedTopicsBoard(1)
	src := &mockupFakeSource{
		pageFake: baseFake,
		boardDir: filepath.Join(project, ".board"),
		instance: 1,
		bundle:   bundle,
	}
	d := startIndexedDaemon(t, dir, src, time.Hour, func(o *DaemonOptions) {
		o.noGitBadge = true
		o.LAN = lan
	})
	waitFor(t, "mockup fixture sessions", func() bool {
		_, ok1 := d.Index().Session("boardsession")
		_, ok2 := d.Index().Session("othersession")
		return d.Index().Loaded() && ok1 && ok2
	})
	return d, endpointURL(d.Endpoint(), ""), src, project
}

func TestBoardMockupRedirectAndFSMockServing(t *testing.T) {
	d, base, src, _ := startMockupFixture(t, false)
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	d.fsNow = func() time.Time { return now }

	// 1. /board/mockup redirects 302 no-store to /fs/mock/<cap>/index.html.
	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Second,
	}
	req, _ := http.NewRequest(http.MethodGet, base+"/s/boardsession/board/mockup?entry=e5", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.AddCookie(localhostTestCookie(t, d))
	resp, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("GET /board/mockup status = %d, want 302", resp.StatusCode)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", resp.Header.Get("Cache-Control"))
	}
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/s/boardsession/fs/mock/") || !strings.HasSuffix(loc, "/index.html") {
		t.Fatalf("unexpected Location %q", loc)
	}
	if src.reads.Load() != 0 {
		t.Fatalf("redirect minting must not read SQLite bundle, got %d reads", src.reads.Load())
	}

	// Cross-site request to /board/mockup is refused.
	badSiteReq, _ := http.NewRequest(http.MethodGet, base+"/s/boardsession/board/mockup?entry=e5", nil)
	badSiteReq.Header.Set("Sec-Fetch-Site", "cross-site")
	badSiteReq.AddCookie(localhostTestCookie(t, d))
	resp, _ = noFollow.Do(badSiteReq)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-site /board/mockup = %d, want 404", resp.StatusCode)
	}

	// 2. Fetch root HTML and sibling assets via /fs/mock/<cap>/...
	capPrefix := strings.TrimSuffix(loc, "index.html")
	fetch := func(path string, hdr map[string]string) (*http.Response, string) {
		r, _ := http.NewRequest(http.MethodGet, base+path, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		res, err := noFollow.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, string(b)
	}

	res, body := fetch(loc, nil)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "<h1>Mock</h1>") {
		t.Fatalf("GET root = %d %q", res.StatusCode, body)
	}
	csp := res.Header.Get("Content-Security-Policy")
	wantConnect := "connect-src " + base + capPrefix
	if !strings.Contains(csp, wantConnect) || !strings.Contains(csp, "sandbox allow-scripts") {
		t.Fatalf("unexpected CSP %q (want %q)", csp, wantConnect)
	}
	if res.Header.Get("X-Content-Type-Options") != "nosniff" || res.Header.Get("Vary") != "Origin" {
		t.Fatalf("missing security headers: nosniff=%q vary=%q", res.Header.Get("X-Content-Type-Options"), res.Header.Get("Vary"))
	}
	if src.reads.Load() != 1 {
		t.Fatalf("expected 1 ReadMockupBundle on cache miss, got %d", src.reads.Load())
	}

	// Sibling JSON fetch with Sec-Fetch-Dest: empty and Origin: null succeeds from LRU (no extra SQLite read).
	res, body = fetch(capPrefix+"data/state.json", map[string]string{
		"Origin":         "null",
		"Sec-Fetch-Dest": "empty",
	})
	if res.StatusCode != http.StatusOK || body != `{"ok":true}` {
		t.Fatalf("GET sibling json = %d %q", res.StatusCode, body)
	}
	if res.Header.Get("Access-Control-Allow-Origin") != "null" || res.Header.Get("Vary") != "Origin" {
		t.Fatalf("CORS headers mismatch: ACAO=%q Vary=%q", res.Header.Get("Access-Control-Allow-Origin"), res.Header.Get("Vary"))
	}
	res, body = fetch(capPrefix+"app.css", map[string]string{"Origin": "null", "Sec-Fetch-Dest": "style"})
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "color: #222") {
		t.Fatalf("GET app.css = %d %q", res.StatusCode, body)
	}
	if src.reads.Load() != 1 {
		t.Fatalf("LRU cache hit should not re-read SQLite, got %d reads", src.reads.Load())
	}

	// 3. Eviction via SetMockupEvictor forces fresh ReadMockupBundle.
	if src.evictor == nil {
		t.Fatal("expected daemon startup to wire SetMockupEvictor")
	}
	src.evictor(src.boardDir)
	res, _ = fetch(capPrefix+"app.js", nil)
	if res.StatusCode != http.StatusOK || src.reads.Load() != 2 {
		t.Fatalf("after eviction expected 200 and 2 reads, got %d and %d reads", res.StatusCode, src.reads.Load())
	}

	// 4. Security refusals on /fs/mock/: evil Origin, traversal, dot-path, wrong session, tampered cap, expired cap.
	for _, tc := range []struct {
		name string
		path string
		hdr  map[string]string
	}{
		{"evil origin", capPrefix + "data/state.json", map[string]string{"Origin": "https://evil.example"}},
		{"encoded traversal", capPrefix + "%2e%2e/secret.txt", nil},
		{"nested encoded traversal", capPrefix + "data/%2e%2e/index.html", nil},
		{"dot file", capPrefix + ".env", nil},
		{"wrong session", strings.Replace(loc, "/s/boardsession/", "/s/othersession/", 1), nil},
		{"tampered mac", strings.Replace(loc, "/index.html", "x/index.html", 1), nil},
		{"missing member", capPrefix + "nope.js", nil},
	} {
		r, _ := fetch(tc.path, tc.hdr)
		if r.StatusCode != http.StatusNotFound {
			t.Errorf("%s: got %d, want 404", tc.name, r.StatusCode)
		}
	}

	// Expired capability is refused.
	now = now.Add(fsCapabilityTTL + time.Second)
	res, _ = fetch(loc, nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("expired cap = %d, want 404", res.StatusCode)
	}
}

func TestFSMockLANAuthAndExistingCapRoutesUnchanged(t *testing.T) {
	d, base, _, project := startMockupFixture(t, true)
	token, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	// Seed a regular file in project root to verify /fs/cap/ stays unchanged.
	if err := os.WriteFile(filepath.Join(project, "plain.json"), []byte(`{"secret":1}`), 0o644); err != nil {
		t.Fatal(err)
	}

	noFollow := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       5 * time.Second,
	}

	// Unauthenticated LAN /board/mockup (non-loopback RemoteAddr) is denied.
	handler := d.Handler()
	unauthReq := httptest.NewRequest(http.MethodGet, base+"/s/boardsession/board/mockup?entry=e5", nil)
	unauthReq.RemoteAddr = "192.0.2.10:43210"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, unauthReq)
	if rec.Code == http.StatusFound || rec.Code == http.StatusOK {
		t.Fatalf("unauthenticated LAN /board/mockup should be blocked, got %d", rec.Code)
	}

	// Authenticated LAN /board/mockup redirects 302 to /fs/mock/<cap>/index.html.
	authReq := httptest.NewRequest(http.MethodGet, base+"/s/boardsession/board/mockup?entry=e5", nil)
	authReq.RemoteAddr = "192.0.2.10:43210"
	authReq.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authReq)
	if rec.Code != http.StatusFound {
		t.Fatalf("authenticated LAN /board/mockup = %d, want 302", rec.Code)
	}
	loc := rec.Header().Get("Location")

	// The redirected /fs/mock/ URL works WITHOUT cookie from the LAN client (previewCapability).
	mockReq := httptest.NewRequest(http.MethodGet, base+loc, nil)
	mockReq.RemoteAddr = "192.0.2.10:43210"
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, mockReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("cookie-less LAN /fs/mock/ = %d, want 200", rec.Code)
	}

	// Regression check: /fs/cap/ STILL refuses Sec-Fetch-Dest: empty and keeps connect-src 'none'.
	capURL := d.fsCapabilityURL("boardsession", project, "plain.json")
	capFetch, _ := http.NewRequest(http.MethodGet, base+capURL, nil)
	capFetch.Header.Set("Sec-Fetch-Dest", "empty")
	resp, err := noFollow.Do(capFetch)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/fs/cap/ must still refuse Sec-Fetch-Dest: empty, got %d", resp.StatusCode)
	}
	capNav, _ := http.NewRequest(http.MethodGet, base+capURL, nil)
	resp, err = noFollow.Do(capNav)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "connect-src 'none'") {
		t.Fatalf("/fs/cap/ CSP must keep connect-src 'none', got %q", resp.Header.Get("Content-Security-Policy"))
	}
}

func TestMockupLRUBoundedEviction(t *testing.T) {
	lru := newMockupLRU(100)
	k1 := mockupCacheKey{boardDir: "/b1", instance: 1, entryID: "e1", sha256: "a"}
	k2 := mockupCacheKey{boardDir: "/b1", instance: 1, entryID: "e2", sha256: "b"}
	k3 := mockupCacheKey{boardDir: "/b2", instance: 1, entryID: "e3", sha256: "c"}
	lru.Put(k1, &decodedMockup{size: 60})
	lru.Put(k2, &decodedMockup{size: 60})
	if _, ok := lru.Get(k1); ok {
		t.Fatal("expected k1 to be evicted when exceeding 100-byte cap")
	}
	if _, ok := lru.Get(k2); !ok {
		t.Fatal("expected k2 to remain cached")
	}
	lru.Put(k3, &decodedMockup{size: 30})
	lru.EvictBoard("/b1")
	if _, ok := lru.Get(k2); ok {
		t.Fatal("expected EvictBoard(/b1) to evict k2")
	}
	if _, ok := lru.Get(k3); !ok {
		t.Fatal("expected k3 in /b2 to survive EvictBoard(/b1)")
	}
}
