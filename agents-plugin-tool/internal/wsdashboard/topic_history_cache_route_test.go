package wsdashboard

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// TestTopicHistoryRouteRevalidatesAndCompresses pins the history route's
// transport: a weak ETag over the exact answer with a revalidate-always
// policy, 304 with no body for a matching If-None-Match, a fresh 200 once
// the board changes, and a gzip body (decoding to the identical answer)
// only for a client that accepts it.
func TestTopicHistoryRouteRevalidatesAndCompresses(t *testing.T) {
	f := startPageFixture(t)
	st := closedTopicsBoard(35)
	st.Topics[0].Entries = []wswhiteboard.Entry{{ID: "e1", Kind: wswhiteboard.EntryAgent, Text: strings.Repeat("c1 history line ", 200), At: "2026-11-17T00:00:00Z"}}
	f.src.mu.Lock()
	f.src.boards["boardsession"] = st
	f.src.mu.Unlock()
	url := f.base + "/s/boardsession/topic?name=c1&name=c2"
	do := func(header map[string]string) (*http.Response, []byte) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		client := testClient()
		client.Timeout = 0
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatal(err)
		}
		return resp, body
	}

	plain, body := do(map[string]string{"Accept-Encoding": "identity"})
	etag := plain.Header.Get("ETag")
	if plain.StatusCode != http.StatusOK || !strings.HasPrefix(etag, `W/"`) || plain.Header.Get("Cache-Control") != "private, no-cache" || plain.Header.Get("Content-Encoding") != "" || !bytes.Contains(body, []byte("c1 history line")) {
		t.Fatalf("plain answer: %d etag %q cache %q encoding %q", plain.StatusCode, etag, plain.Header.Get("Cache-Control"), plain.Header.Get("Content-Encoding"))
	}

	again, empty := do(map[string]string{"If-None-Match": etag, "Accept-Encoding": "identity"})
	if again.StatusCode != http.StatusNotModified || len(empty) != 0 || again.Header.Get("ETag") != etag {
		t.Fatalf("revalidation = %d, %d bytes, etag %q", again.StatusCode, len(empty), again.Header.Get("ETag"))
	}
	if other, _ := do(map[string]string{"If-None-Match": `W/"stale"`}); other.StatusCode != http.StatusOK {
		t.Fatalf("a stale validator = %d, want 200", other.StatusCode)
	}

	zipped, raw := do(map[string]string{"Accept-Encoding": "br, gzip;q=0.8"})
	if zipped.Header.Get("Content-Encoding") != "gzip" || zipped.Header.Get("ETag") != etag || !strings.Contains(zipped.Header.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("gzip answer headers: %v", zipped.Header)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(zr)
	if err != nil || !bytes.Equal(decoded, body) {
		t.Fatalf("gzip body does not decode to the plain answer: %v", err)
	}
	if len(raw) >= len(body) {
		t.Errorf("gzip answer %d bytes, plain %d", len(raw), len(body))
	}
	if refused, _ := do(map[string]string{"Accept-Encoding": "gzip;q=0, *"}); refused.Header.Get("Content-Encoding") != "" {
		t.Error("gzip;q=0 still compressed")
	}

	f.src.mu.Lock()
	st.Topics[0].Entries[0].Text = "changed history"
	f.src.mu.Unlock()
	if changed, _ := do(map[string]string{"If-None-Match": etag}); changed.StatusCode != http.StatusOK || changed.Header.Get("ETag") == etag {
		t.Fatalf("a changed board answered %d with etag %q", changed.StatusCode, changed.Header.Get("ETag"))
	}
}

func TestAcceptsGzip(t *testing.T) {
	for header, want := range map[string]bool{
		"":                     false,
		"identity":             false,
		"gzip":                 true,
		"GZIP":                 true,
		"br, gzip;q=0.5":       true,
		"gzip;q=0":             false,
		"gzip; q=0.0, br":      false,
		"x-gzip":               true,
		"*":                    true,
		"*;q=0":                false,
		"gzip;q=0, *":          false,
		"deflate, *;q=0.1":     true,
		"br;q=1, identity;q=0": false,
	} {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %t, want %t", header, got, want)
		}
	}
}
