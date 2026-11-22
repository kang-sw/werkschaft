package wsdashboard

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsnetguard"
)

// companionFetchClient returns a fetcher that trusts srv's certificate, uses
// no proxy (the machine running the tests may set one), and allows loopback
// so the httptest server is reachable.
func companionFetchClient(srv *httptest.Server) *companionFetcher {
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return &companionFetcher{
		tls:    &tls.Config{RootCAs: pool},
		proxy:  func(*http.Request) (*url.URL, error) { return nil, nil },
		refuse: func(a netip.Addr) bool { return !a.IsLoopback() && wsnetguard.RefuseLocal(a) },
	}
}

func companionFetchServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *companionFetcher) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	return srv, companionFetchClient(srv)
}

// companionFetchPaths records request URIs in arrival order.
type companionFetchPaths struct {
	mu   sync.Mutex
	list []string
}

func (p *companionFetchPaths) add(r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.list = append(p.list, r.URL.RequestURI())
}

func (p *companionFetchPaths) get() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.list...)
}

func companionPkt(payload string) string { return fmt.Sprintf("%04x%s", len(payload)+4, payload) }

func companionSum(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func companionFetchLower(t *testing.T, cap *int64, value int64) {
	t.Helper()
	old := *cap
	*cap = value
	t.Cleanup(func() { *cap = old })
}

// Object names of the ref advertisement fixture.
var (
	companionRefHead      = strings.Repeat("1", 40) // HEAD and refs/heads/main
	companionRefDev       = strings.Repeat("2", 40)
	companionRefTagObject = strings.Repeat("3", 40) // the annotated tag object of v1
	companionRefTagCommit = strings.Repeat("4", 40) // its peeled commit
	companionRefLight     = strings.Repeat("5", 40)
	companionRefBothHead  = strings.Repeat("6", 40)
	companionRefBothTag   = strings.Repeat("7", 40)
)

const companionServiceLines = "001e# service=git-upload-pack\n0000"

func companionRefsFixture() string {
	return companionServiceLines +
		companionPkt(companionRefHead+" HEAD\x00multi_ack side-band-64k symref=HEAD:refs/heads/main agent=fixture/1\n") +
		companionPkt(companionRefHead+" refs/heads/main\n") +
		companionPkt(companionRefDev+" refs/heads/dev\n") +
		companionPkt(companionRefBothHead+" refs/heads/both\n") +
		companionPkt(companionRefTagObject+" refs/tags/v1\n") +
		companionPkt(companionRefTagCommit+" refs/tags/v1^{}\n") +
		companionPkt(companionRefLight+" refs/tags/light\n") +
		companionPkt(companionRefBothTag+" refs/tags/both\n") +
		"0000"
}

// TestCompanionPackFetchSourceParse covers source classification and the
// URL rules of each kind.
func TestCompanionPackFetchSourceParse(t *testing.T) {
	cases := []struct {
		name, raw string
		want      companionSource
		err       string
	}{
		{name: "http refused", raw: "http://example.invalid/packs.zip", err: "https"},
		{name: "userinfo refused", raw: "https://user:secret@example.invalid/packs.zip", err: "credentials"},
		{name: "too long", raw: "https://example.invalid/" + strings.Repeat("a", companionURLMax), err: "exceeds"},
		{name: "zip with fragment", raw: "https://example.invalid/packs.zip#v1", err: "fragment"},
		{name: "zip with empty fragment", raw: "https://example.invalid/packs.zip#", err: "fragment"},
		{name: "git with query", raw: "https://example.invalid/team/repo?tab=files", err: "query"},
		{name: "git with empty query", raw: "https://example.invalid/team/repo?", err: "query"},
		{name: "git ref with space", raw: "https://example.invalid/team/repo#feature%20x", err: "spaces"},
		{name: "git ref with control", raw: "https://example.invalid/team/repo#%01", err: "control"},
		{name: "git ref too long", raw: "https://example.invalid/team/repo#" + strings.Repeat("r", companionRefMax+1), err: "exceeds"},
		{name: "git default HEAD", raw: "https://example.invalid/team/repo",
			want: companionSource{raw: "https://example.invalid/team/repo", kind: "git", url: "https://example.invalid/team/repo",
				site: "https://example.invalid", path: "/team/repo", ref: "HEAD"}},
		{name: "git empty fragment is HEAD", raw: "https://example.invalid/team/repo#",
			want: companionSource{raw: "https://example.invalid/team/repo#", kind: "git", url: "https://example.invalid/team/repo",
				site: "https://example.invalid", path: "/team/repo", ref: "HEAD"}},
		{name: "git ref keeps .git", raw: "https://example.invalid/team/repo.git#v1.2",
			want: companionSource{raw: "https://example.invalid/team/repo.git#v1.2", kind: "git", url: "https://example.invalid/team/repo.git",
				site: "https://example.invalid", path: "/team/repo.git", ref: "v1.2"}},
		{name: "git trailing slash trimmed", raw: "  https://example.invalid:8443/team/repo/  ",
			want: companionSource{raw: "https://example.invalid:8443/team/repo/", kind: "git", url: "https://example.invalid:8443/team/repo",
				site: "https://example.invalid:8443", path: "/team/repo", ref: "HEAD"}},
		{name: "git at host root", raw: "https://example.invalid/",
			want: companionSource{raw: "https://example.invalid/", kind: "git", url: "https://example.invalid",
				site: "https://example.invalid", path: "", ref: "HEAD"}},
		{name: "zip case-insensitive", raw: "https://example.invalid/Packs.ZIP",
			want: companionSource{raw: "https://example.invalid/Packs.ZIP", kind: "zip", url: "https://example.invalid/Packs.ZIP"}},
		{name: "zip keeps its query", raw: "https://example.invalid/files/packs.zip?download=1",
			want: companionSource{raw: "https://example.invalid/files/packs.zip?download=1", kind: "zip", url: "https://example.invalid/files/packs.zip?download=1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCompanionSource(tc.raw)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("parse(%q) = %+v, %v; want an error mentioning %q", tc.raw, got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("parse(%q) = %+v, %v; want %+v", tc.raw, got, err, tc.want)
			}
		})
	}
}

// TestCompanionPackFetchRefs parses raw ref advertisements and resolves refs
// against them.
func TestCompanionPackFetchRefs(t *testing.T) {
	zero := strings.Repeat("0", 40)
	empty := companionServiceLines + companionPkt(zero+" capabilities^{}\x00multi_ack agent=fixture/1\n") + "0000"
	cases := []struct {
		name, raw, ref, want string
		bad                  bool   // a malformed advertisement
		err                  string // a resolution error
	}{
		{name: "HEAD", raw: companionRefsFixture(), ref: "HEAD", want: companionRefHead},
		{name: "branch", raw: companionRefsFixture(), ref: "dev", want: companionRefDev},
		{name: "annotated tag peeled", raw: companionRefsFixture(), ref: "v1", want: companionRefTagCommit},
		{name: "lightweight tag", raw: companionRefsFixture(), ref: "light", want: companionRefLight},
		{name: "branch before tag", raw: companionRefsFixture(), ref: "both", want: companionRefBothHead},
		{name: "40-hex as given", raw: companionRefsFixture(), ref: strings.Repeat("Ab", 20), want: strings.Repeat("ab", 20)},
		{name: "40-hex on an empty repository", raw: empty, ref: strings.Repeat("9", 40), want: strings.Repeat("9", 40)},
		{name: "missing ref", raw: companionRefsFixture(), ref: "absent", err: "no branch or tag"},
		{name: "full ref name is not a short name", raw: companionRefsFixture(), ref: "refs/heads/main", err: "no branch or tag"},
		{name: "empty repository", raw: empty, ref: "HEAD", err: "empty"},
		{name: "empty repository without capabilities", raw: companionServiceLines + "0000", ref: "HEAD", err: "empty"},
		{name: "no HEAD", raw: companionServiceLines + companionPkt(companionRefDev+" refs/heads/dev\x00agent=fixture/1\n") + "0000", ref: "HEAD", err: "default branch"},
		{name: "length not hex", raw: companionServiceLines + "00zz" + companionRefHead + " HEAD\n0000", bad: true},
		{name: "length below the header", raw: companionServiceLines + "0003" + "0000", bad: true},
		{name: "length over the maximum", raw: companionServiceLines + "fff1" + strings.Repeat("x", 0xfff1-4) + "0000", bad: true},
		{name: "truncated line", raw: companionServiceLines + "0040" + companionRefHead, bad: true},
		{name: "truncated header", raw: companionServiceLines + "00", bad: true},
		{name: "v2 delimiter", raw: companionServiceLines + companionPkt("version 2\n") + "0001" + "0000", bad: true},
		{name: "v2 response end", raw: companionServiceLines + "0002", bad: true},
		{name: "missing service line", raw: companionPkt(companionRefHead+" HEAD\x00agent=fixture/1\n") + "0000", bad: true},
		{name: "service line without flush", raw: "001e# service=git-upload-pack\n" + companionPkt(companionRefHead+" HEAD\n") + "0000", bad: true},
		{name: "no final flush", raw: companionServiceLines + companionPkt(companionRefHead+" HEAD\n"), bad: true},
		{name: "data after the final flush", raw: companionRefsFixture() + companionPkt(companionRefDev+" refs/heads/late\n"), bad: true},
		{name: "capabilities after the first line", raw: companionServiceLines + companionPkt(companionRefHead+" HEAD\n") + companionPkt(companionRefDev+" refs/heads/dev\x00agent=fixture/1\n") + "0000", bad: true},
		{name: "uppercase object name", raw: companionServiceLines + companionPkt(strings.Repeat("A", 40)+" HEAD\n") + "0000", bad: true},
		{name: "sha-256 object names", raw: companionServiceLines + companionPkt(strings.Repeat("a", 64)+" HEAD\x00object-format=sha256\n") + "0000", bad: true},
		{name: "empty", raw: "", bad: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			refs, err := parseCompanionRefs([]byte(tc.raw))
			if tc.bad {
				if !errors.Is(err, errCompanionBadRefs) {
					t.Fatalf("parse = %v, %v; want a malformed-advertisement error", refs, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			got, err := resolveCompanionRef(refs, tc.ref)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("resolve(%q) = %q, %v; want an error mentioning %q", tc.ref, got, err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("resolve(%q) = %q, %v; want %q", tc.ref, got, err, tc.want)
			}
		})
	}
}

// TestCompanionPackFetchGit resolves a git source through a served ref
// advertisement and walks the archive URL patterns in order.
func TestCompanionPackFetchGit(t *testing.T) {
	archive := companionTestZip(t, companionTestBundle(t, "repo-0123/", "alpha")...)
	refsURI := "/team/repo.git/info/refs?service=git-upload-pack"
	first := func(sha string) string { return "/team/repo/archive/" + sha + ".zip" }
	second := func(sha string) string { return "/team/repo/-/archive/" + sha + "/repo-" + sha + ".zip" }
	cases := []struct {
		name          string
		source        string
		commit        string
		first, second string // "zip", "html", or "404"
		want          []string
		fail          bool
	}{
		{name: "first pattern 404 then second zip", source: "/team/repo.git", commit: companionRefHead, first: "404", second: "zip",
			want: []string{refsURI, first(companionRefHead), second(companionRefHead)}},
		{name: "first pattern web page then second zip", source: "/team/repo.git/", commit: companionRefHead, first: "html", second: "zip",
			want: []string{refsURI, first(companionRefHead), second(companionRefHead)}},
		{name: "first pattern wins for a tag", source: "/team/repo.git#v1", commit: companionRefTagCommit, first: "zip", second: "404",
			want: []string{refsURI, first(companionRefTagCommit)}},
		{name: "both patterns fail", source: "/team/repo.git", commit: companionRefHead, first: "404", second: "html", fail: true,
			want: []string{refsURI, first(companionRefHead), second(companionRefHead)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var paths companionFetchPaths
			var protocol []string
			answer := func(w http.ResponseWriter, mode string) {
				switch mode {
				case "zip":
					w.Write(archive)
				case "html":
					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte("<!doctype html><p>sign in to download</p>"))
				default:
					http.Error(w, "not found", http.StatusNotFound)
				}
			}
			srv, f := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
				paths.add(r)
				switch r.URL.Path {
				case "/team/repo.git/info/refs":
					paths.mu.Lock()
					protocol = append(protocol, r.Header.Get("Git-Protocol"))
					paths.mu.Unlock()
					w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement; charset=binary")
					io.WriteString(w, companionRefsFixture())
				case first(tc.commit):
					answer(w, tc.first)
				case second(tc.commit):
					answer(w, tc.second)
				default:
					http.NotFound(w, r)
				}
			})
			got, err := f.fetchSource(context.Background(), srv.URL+tc.source)
			if paths := paths.get(); !reflect.DeepEqual(paths, tc.want) {
				t.Errorf("requested %q, want %q", paths, tc.want)
			}
			if len(protocol) != 1 || protocol[0] != "" {
				t.Errorf("Git-Protocol headers = %q, want one request without it", protocol)
			}
			if tc.fail {
				if !errors.Is(err, errCompanionNoArchive) || !strings.Contains(err.Error(), "zip download URL as a zip source") {
					t.Fatalf("err = %v, want the zip-source hint", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			ref := "HEAD"
			if _, frag, ok := strings.Cut(tc.source, "#"); ok {
				ref = frag
			}
			want := companionInstallOrigin{Source: srv.URL + tc.source, Kind: "git", Ref: ref, Commit: tc.commit}
			if got.origin != want || !bytes.Equal(got.data, archive) {
				t.Fatalf("origin = %+v (data %d bytes), want %+v with the archive", got.origin, len(got.data), want)
			}
		})
	}

	t.Run("host root tries only the first pattern", func(t *testing.T) {
		var paths companionFetchPaths
		srv, f := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
			paths.add(r)
			if r.URL.Path == "/info/refs" {
				w.Header().Set("Content-Type", "application/x-git-upload-pack-advertisement")
				io.WriteString(w, companionRefsFixture())
				return
			}
			http.NotFound(w, r)
		})
		_, err := f.fetchSource(context.Background(), srv.URL+"/")
		want := []string{"/info/refs?service=git-upload-pack", "/archive/" + companionRefHead + ".zip"}
		if got := paths.get(); !reflect.DeepEqual(got, want) || !errors.Is(err, errCompanionNoArchive) {
			t.Fatalf("requested %q, err %v; want %q and the zip-source hint", got, err, want)
		}
	})

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		detail  string
	}{
		{"wrong content type", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			io.WriteString(w, companionRefsFixture())
		}, "ref advertisement"},
		{"missing repository", func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		}, "status 404"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, f := companionFetchServer(t, tc.handler)
			_, err := f.fetchSource(context.Background(), srv.URL+"/team/repo")
			if !errors.Is(err, errCompanionNotGit) || !strings.Contains(err.Error(), "not a git repository readable over HTTPS") ||
				!strings.Contains(err.Error(), tc.detail) {
				t.Fatalf("err = %v, want the not-a-git-repository hint mentioning %q", err, tc.detail)
			}
		})
	}
}

// TestCompanionPackFetchZipSource downloads a zip source and refuses a
// response that is not a zip with the local-import hint.
func TestCompanionPackFetchZipSource(t *testing.T) {
	archive := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	srv, f := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/packs.zip":
			w.Write(archive)
		case "/share.zip":
			w.Header().Set("Content-Type", "text/html")
			io.WriteString(w, "<!doctype html><p>open in the browser to download</p>")
		default:
			http.NotFound(w, r)
		}
	})
	got, err := f.fetchSource(context.Background(), srv.URL+"/packs.zip?download=1")
	if err != nil {
		t.Fatal(err)
	}
	want := companionInstallOrigin{Source: srv.URL + "/packs.zip?download=1", Kind: "zip", ArchiveSHA256: companionSum(archive)}
	if got.origin != want || !bytes.Equal(got.data, archive) {
		t.Fatalf("origin = %+v, want %+v", got.origin, want)
	}

	_, err = f.fetchSource(context.Background(), srv.URL+"/share.zip")
	if !errors.Is(err, errCompanionNotZip) || !strings.Contains(err.Error(), "download the file and import it locally") {
		t.Fatalf("web page err = %v, want the local-import hint wrapping errCompanionNotZip", err)
	}
	_, err = f.fetchSource(context.Background(), srv.URL+"/gone.zip")
	if err == nil || errors.Is(err, errCompanionNotZip) || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("missing zip err = %v, want a status failure", err)
	}
}

// TestCompanionPackFetchPack checks the sha256 of an external pack zip before
// opening it.
func TestCompanionPackFetchPack(t *testing.T) {
	packZip := companionTestZip(t,
		companionZipEntry{name: "far-0123/"},
		companionZipEntry{name: "far-0123/pack.json", data: companionTestManifest("far", "stand.png", "")},
		companionZipEntry{name: "far-0123/stand.png", data: companionTestPNG(t, 8, 8)})
	page := []byte("<!doctype html><p>not a zip</p>")
	srv, f := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/far.zip":
			w.Write(packZip)
		case "/page.zip":
			w.Write(page)
		default:
			http.NotFound(w, r)
		}
	})
	fetch := func(path, sum string) (*companionArchive, error) {
		return f.fetchPack(context.Background(), companionMarketplaceEntry{ID: "far", URL: srv.URL + path, SHA256: sum}, newCompanionInflate())
	}

	// A web page is reported as a hash mismatch, not as a non-zip, so the
	// hash is checked before the zip is opened.
	_, err := fetch("/page.zip", companionSum(packZip))
	if !errors.Is(err, errCompanionPackSHA256) || errors.Is(err, errCompanionNotZip) || !strings.Contains(err.Error(), companionSum(page)) {
		t.Fatalf("mismatch err = %v, want a sha256 mismatch naming the downloaded hash", err)
	}
	if _, err := fetch("/page.zip", companionSum(page)); !errors.Is(err, errCompanionNotZip) {
		t.Fatalf("matching non-zip err = %v, want errCompanionNotZip from the open", err)
	}
	if _, err := fetch("/gone.zip", companionSum(packZip)); err == nil || !strings.Contains(err.Error(), "status 404") {
		t.Fatalf("missing pack err = %v, want a status failure", err)
	}
	archive, err := fetch("/far.zip", companionSum(packZip))
	if err != nil {
		t.Fatal(err)
	}
	if prefix, ok := archive.root(companionManifestName); !ok || prefix != "far-0123/" {
		t.Fatalf("pack root = %q, %v; want far-0123/", prefix, ok)
	}
}

// TestCompanionPackFetchRedirects follows at most five https redirects and
// sends neither Referer nor Cookie after one.
func TestCompanionPackFetchRedirects(t *testing.T) {
	archive := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	var mu sync.Mutex
	seen := map[string]http.Header{}
	srv, f := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path] = r.Header.Clone()
		mu.Unlock()
		switch {
		case strings.HasPrefix(r.URL.Path, "/hop/"):
			n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/hop/"), ".zip"))
			if err != nil {
				http.NotFound(w, r)
			} else if n > 0 {
				http.Redirect(w, r, fmt.Sprintf("/hop/%d.zip", n-1), http.StatusFound)
			} else {
				w.Write(archive)
			}
		case r.URL.Path == "/plain.zip":
			http.Redirect(w, r, "http://example.invalid/plain.zip", http.StatusFound)
		case r.URL.Path == "/creds.zip":
			http.Redirect(w, r, "https://user:secret@"+r.Host+"/hop/0.zip", http.StatusFound)
		case r.URL.Path == "/cookie.zip":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "fixture", Path: "/"})
			http.Redirect(w, r, "/landing.zip", http.StatusTemporaryRedirect)
		case r.URL.Path == "/landing.zip":
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	if _, err := f.fetchSource(ctx, srv.URL+"/hop/5.zip"); err != nil {
		t.Fatalf("five redirects: %v", err)
	}
	if _, err := f.fetchSource(ctx, srv.URL+"/hop/6.zip"); !errors.Is(err, errCompanionRedirectCount) || !strings.Contains(err.Error(), "more than 5") {
		t.Fatalf("six redirects err = %v, want a redirect-count refusal", err)
	}
	if _, err := f.fetchSource(ctx, srv.URL+"/plain.zip"); !errors.Is(err, errCompanionRedirectScheme) || !strings.Contains(err.Error(), "not https") {
		t.Fatalf("http redirect err = %v, want a scheme refusal", err)
	}
	if _, err := f.fetchSource(ctx, srv.URL+"/creds.zip"); !errors.Is(err, errCompanionRedirectCredentials) {
		t.Fatalf("credential redirect err = %v, want a credentials refusal", err)
	}
	if _, err := f.fetchSource(ctx, srv.URL+"/cookie.zip"); err != nil {
		t.Fatalf("cookie redirect: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if ua := seen["/cookie.zip"].Get("User-Agent"); ua != companionFetchUserAgent {
		t.Errorf("first hop User-Agent = %q, want %q", ua, companionFetchUserAgent)
	}
	landing := seen["/landing.zip"]
	if landing == nil {
		t.Fatal("the redirect target was never requested")
	}
	if landing.Get("Referer") != "" || landing.Get("Cookie") != "" || landing.Get("User-Agent") != companionFetchUserAgent {
		t.Errorf("after a redirect: Referer %q, Cookie %q, User-Agent %q; want no Referer or Cookie and the constant User-Agent",
			landing.Get("Referer"), landing.Get("Cookie"), landing.Get("User-Agent"))
	}
}

// TestCompanionPackFetchCaps enforces each response cap on a declared
// Content-Length, before any body byte arrives, and on a chunked body.
func TestCompanionPackFetchCaps(t *testing.T) {
	sourceZip := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	packZip := companionTestZip(t,
		companionZipEntry{name: "pack.json", data: companionTestManifest("far", "stand.png", "")},
		companionZipEntry{name: "stand.png", data: companionTestPNG(t, 8, 8)})
	refs := []byte(companionRefsFixture())
	cases := []struct {
		name        string
		cap         *int64
		path        string
		body        []byte
		contentType string
		fetch       func(f *companionFetcher, base string) error
		withinCap   func(err error) bool
	}{
		{"ref advertisement", &companionRefsCap, "/team/repo/info/refs", refs, companionGitAdvertType,
			func(f *companionFetcher, base string) error {
				_, err := f.fetchSource(context.Background(), base+"/team/repo")
				return err
			},
			// Within the cap the refs parse and the archive patterns 404.
			func(err error) bool { return errors.Is(err, errCompanionNoArchive) }},
		{"source zip", &companionSourceCap, "/packs.zip", sourceZip, "application/zip",
			func(f *companionFetcher, base string) error {
				_, err := f.fetchSource(context.Background(), base+"/packs.zip")
				return err
			},
			func(err error) bool { return err == nil }},
		{"pack zip", &companionPackZipCap, "/far.zip", packZip, "application/zip",
			func(f *companionFetcher, base string) error {
				_, err := f.fetchPack(context.Background(),
					companionMarketplaceEntry{ID: "far", URL: base + "/far.zip", SHA256: companionSum(packZip)}, newCompanionInflate())
				return err
			},
			func(err error) bool { return err == nil }},
	}
	for _, tc := range cases {
		for _, mode := range []string{"declared", "chunked", "plain"} {
			t.Run(tc.name+"/"+mode, func(t *testing.T) {
				srv, f := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != tc.path {
						http.NotFound(w, r)
						return
					}
					w.Header().Set("Content-Type", tc.contentType)
					switch mode {
					case "declared":
						// Headers only: a fetcher that waited for the body
						// would run into its timeout instead.
						w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
						<-r.Context().Done()
					case "chunked":
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
						w.Write(tc.body)
					default:
						w.Header().Set("Content-Length", strconv.Itoa(len(tc.body)))
						w.Write(tc.body)
					}
				})
				f.timeout = 10 * time.Second
				companionFetchLower(t, tc.cap, int64(len(tc.body))-1)
				if mode != "plain" {
					err := tc.fetch(f, srv.URL)
					if !errors.Is(err, errCompanionTooLarge) || !strings.Contains(err.Error(), "limit") {
						t.Fatalf("over the cap err = %v, want a size refusal", err)
					}
				}
				if mode == "declared" {
					return
				}
				*tc.cap = int64(len(tc.body))
				if err := tc.fetch(f, srv.URL); !tc.withinCap(err) {
					t.Fatalf("at the cap err = %v", err)
				}
			})
		}
	}
}

// TestCompanionPackFetchTimeout bounds a call by the fetcher's timeout, both
// while waiting for headers and while reading a stalled body, and reports a
// canceled caller context as a cancellation.
func TestCompanionPackFetchTimeout(t *testing.T) {
	srv, f := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stall.zip" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("PK\x03\x04"))
			w.(http.Flusher).Flush()
		}
		<-r.Context().Done()
	})
	f.timeout = 200 * time.Millisecond
	for _, path := range []string{"/slow.zip", "/stall.zip"} {
		start := time.Now()
		_, err := f.fetchSource(context.Background(), srv.URL+path)
		if err == nil || !strings.Contains(err.Error(), "did not finish within 200ms") {
			t.Fatalf("%s err = %v, want a timeout", path, err)
		}
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("%s took %v, want about the timeout", path, elapsed)
		}
	}

	f2 := companionFetchClient(srv)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)
	if _, err := f2.fetchSource(ctx, srv.URL+"/slow.zip"); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("canceled caller err = %v, want a cancellation", err)
	}
}

// TestCompanionPackFetchLoopbackRefused dials an httptest server on loopback
// under the default policy: the dial is refused and the server never sees a
// request.
func TestCompanionPackFetchLoopbackRefused(t *testing.T) {
	var hits int
	var mu sync.Mutex
	srv, f := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
	})
	f.refuse = nil
	host := netip.MustParseAddrPort(srv.Listener.Addr().String()).Addr().String()
	_, err := f.fetchSource(context.Background(), srv.URL+"/packs.zip")
	var refused *wsnetguard.RefusedError
	if !errors.As(err, &refused) || !strings.Contains(err.Error(), host) {
		t.Fatalf("err = %v, want a dial refusal naming %s", err, host)
	}
	_, err = f.fetchPack(context.Background(), companionMarketplaceEntry{ID: "far", URL: srv.URL + "/far.zip", SHA256: strings.Repeat("0", 64)}, newCompanionInflate())
	if !errors.As(err, &refused) {
		t.Fatalf("pack err = %v, want a dial refusal", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if hits != 0 {
		t.Fatalf("the server saw %d requests, want none", hits)
	}
}

// TestCompanionPackFetchProxy sends a request through an environment-style
// CONNECT proxy: the proxy makes the connection, so a loopback target is
// reachable under the default policy, while the same fetch without a proxy is
// refused.
func TestCompanionPackFetchProxy(t *testing.T) {
	archive := companionTestZip(t, companionTestBundle(t, "", "alpha")...)
	target, _ := companionFetchServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/via-proxy.zip" {
			http.NotFound(w, r)
			return
		}
		w.Write(archive)
	})
	var mu sync.Mutex
	var tunnels []string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT only", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		tunnels = append(tunnels, r.Host)
		mu.Unlock()
		upstream, err := net.Dial("tcp", r.Host)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		conn, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		io.WriteString(conn, "HTTP/1.1 200 Connection established\r\n\r\n")
		go func() {
			io.Copy(upstream, buffered)
			upstream.Close()
		}()
		go func() {
			io.Copy(conn, upstream)
			conn.Close()
		}()
	}))
	t.Cleanup(proxy.Close)
	proxyURL, err := url.Parse(proxy.URL)
	if err != nil {
		t.Fatal(err)
	}

	f := companionFetchClient(target)
	f.refuse = nil
	f.proxy = func(*http.Request) (*url.URL, error) { return proxyURL, nil }
	got, err := f.fetchSource(context.Background(), target.URL+"/via-proxy.zip")
	if err != nil {
		t.Fatalf("proxied fetch: %v", err)
	}
	if !bytes.Equal(got.data, archive) {
		t.Fatal("proxied fetch returned other bytes")
	}
	mu.Lock()
	if want := []string{target.Listener.Addr().String()}; !reflect.DeepEqual(tunnels, want) {
		t.Errorf("tunnels = %q, want %q", tunnels, want)
	}
	mu.Unlock()

	direct := companionFetchClient(target)
	direct.refuse = nil
	var refused *wsnetguard.RefusedError
	if _, err := direct.fetchSource(context.Background(), target.URL+"/via-proxy.zip"); !errors.As(err, &refused) {
		t.Fatalf("direct fetch err = %v, want a dial refusal", err)
	}
}
