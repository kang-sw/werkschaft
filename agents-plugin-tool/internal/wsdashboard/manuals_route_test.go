package wsdashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type manualsBody struct {
	Manuals []manualRow `json:"manuals"`
}

func writeManual(t *testing.T, project, name, body string) {
	t.Helper()
	dir := filepath.Join(project, "ai-docs", "manuals")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestManualsRoute(t *testing.T) {
	f := startPageFixture(t, lanMode)
	local := viewer{peer: "127.0.0.1:3456", host: "localhost", localCookie: true}
	lan := viewer{peer: "192.168.1.23:3456", host: "192.168.1.2", cookie: readLANToken(t, f.dir)}
	const url = "/s/livesession/manuals"

	// A project without ai-docs/manuals/ answers an empty list, not an error.
	for _, v := range []viewer{local, lan} {
		rec := serve(f.d, v, http.MethodGet, url, "")
		if rec.Code != http.StatusOK || rec.Body.String() != "{\"manuals\":[]}\n" {
			t.Fatalf("missing dir: %d %q", rec.Code, rec.Body.String())
		}
	}

	writeManual(t, f.project, "performance.md", "---\nsummary: Hot path <rules>\n---\n# Perf\n")
	writeManual(t, f.project, "alpha.md", "# no frontmatter\n")
	writeManual(t, f.project, "notes.txt", "ignored\n")
	if err := os.MkdirAll(filepath.Join(f.project, "ai-docs", "manuals", "nested.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeManual(t, f.project, "SECRET", "x")
	want := []manualRow{
		{Path: "ai-docs/manuals/alpha.md", Stem: "alpha", Summary: ""},
		{Path: "ai-docs/manuals/performance.md", Stem: "performance", Summary: "Hot path <rules>"},
	}

	// The page render must not scan the directory: the manuals written above
	// stay out of the served page.
	page := serve(f.d, local, http.MethodGet, "/s/livesession?frame=1", "")
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), "performance.md") || strings.Contains(page.Body.String(), "Hot path") {
		t.Fatalf("page render listed manuals: %d", page.Code)
	}

	for _, v := range []viewer{local, lan} {
		rec := serve(f.d, v, http.MethodGet, url, "")
		var got manualsBody
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || !reflect.DeepEqual(got.Manuals, want) {
			t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
		}
		for h, val := range map[string]string{
			"Cache-Control":          "no-store",
			"X-Content-Type-Options": "nosniff",
			"Content-Type":           "application/json",
		} {
			if rec.Header().Get(h) != val {
				t.Fatalf("%s = %q, want %q", h, rec.Header().Get(h), val)
			}
		}
		if strings.Contains(rec.Body.String(), f.project) {
			t.Fatalf("response leaks the absolute project path: %s", rec.Body.String())
		}
	}
	// Contract fields: every row carries exactly path, stem, and summary.
	var raw struct {
		Manuals []map[string]any `json:"manuals"`
	}
	rec := serve(f.d, local, http.MethodGet, url, "")
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil || len(raw.Manuals) != 2 {
		t.Fatalf("raw: %v %s", err, rec.Body.String())
	}
	for _, row := range raw.Manuals {
		if len(row) != 3 || row["path"] == nil || row["stem"] == nil || row["summary"] == nil {
			t.Fatalf("row fields = %v", row)
		}
	}
	if rec := serve(f.d, local, http.MethodHead, url, ""); rec.Code != http.StatusOK {
		t.Fatalf("HEAD = %d", rec.Code)
	}

	// The listing is on demand, never cached: a new manual shows on the next read.
	writeManual(t, f.project, "zeta.md", "---\nsummary: later\n---\n")
	rec = serve(f.d, local, http.MethodGet, url, "")
	if !strings.Contains(rec.Body.String(), `"stem":"zeta"`) {
		t.Fatalf("new manual not listed: %s", rec.Body.String())
	}

	// Path traversal in the session segment or a root query cannot move the
	// listing off the registered project.
	other := t.TempDir()
	writeManual(t, other, "elsewhere.md", "---\nsummary: other project\n---\n")
	for _, tc := range []struct {
		method, path string
		v            viewer
		status       int
	}{
		{http.MethodGet, "/s/nosuchsession/manuals", local, http.StatusNotFound},
		{http.MethodGet, "/s/" + LocalBrowseAnchor + "/manuals", local, http.StatusNotFound},
		{http.MethodGet, "/s/livesession%2F..%2F..%2Fmanuals", local, http.StatusNotFound},
		{http.MethodGet, "/s/livesession/manuals/extra", local, http.StatusNotFound},
		{http.MethodPost, url, local, http.StatusMethodNotAllowed},
		{http.MethodPut, url, local, http.StatusMethodNotAllowed},
		{http.MethodDelete, url, local, http.StatusMethodNotAllowed},
		{http.MethodGet, url, viewer{peer: lan.peer, host: lan.host}, http.StatusUnauthorized},
	} {
		rec := serve(f.d, tc.v, tc.method, tc.path, "")
		if rec.Code != tc.status {
			t.Fatalf("%s %s = %d, want %d: %s", tc.method, tc.path, rec.Code, tc.status, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "elsewhere") || strings.Contains(rec.Body.String(), "performance") {
			t.Fatalf("refused request leaked a listing: %s", rec.Body.String())
		}
	}
	rec = serve(f.d, local, http.MethodGet, url+"?root="+other, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "elsewhere") || !strings.Contains(rec.Body.String(), `"stem":"performance"`) {
		t.Fatalf("root query moved the listing: %d %s", rec.Code, rec.Body.String())
	}

	// A read failure is a bare 500 that names no path.
	if err := os.RemoveAll(filepath.Join(f.project, "ai-docs", "manuals")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.project, "ai-docs", "manuals"), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = serve(f.d, local, http.MethodGet, url, "")
	if rec.Code != http.StatusInternalServerError || rec.Body.String() != "manuals read failed\n" {
		t.Fatalf("failure = %d %q", rec.Code, rec.Body.String())
	}
}
