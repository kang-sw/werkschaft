package wsweb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type staticResolver map[string][]string

func (r staticResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	list, ok := r[host]
	if !ok {
		return nil, fmt.Errorf("no DNS record for %s", host)
	}
	out := make([]net.IPAddr, 0, len(list))
	for _, raw := range list {
		out = append(out, net.IPAddr{IP: net.ParseIP(raw)})
	}
	return out, nil
}

// dialToTestServer routes any dialed public IP directly to srv's local address
// while verifying that Fetch pinned the expected public IP instead of re-resolving.
func dialToTestServer(t *testing.T, srv *httptest.Server, wantIP string) DialFunc {
	t.Helper()
	target := srv.Listener.Addr().String()
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, err
		}
		if wantIP != "" && host != wantIP {
			return nil, fmt.Errorf("dialed %q, want pinned IP %q", host, wantIP)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}
}

func TestFetchRejectsNonHTTPAndCredentials(t *testing.T) {
	badURLs := []string{
		"",
		"file:///etc/passwd",
		"ftp://example.com/file.txt",
		"gopher://example.com/",
		"data:text/plain,hello",
		"http://user:pass@example.com/",
		"https://token@example.com/secret",
		"https:///no-host",
		"https://example.com/\nheader-inject",
	}
	for _, raw := range badURLs {
		_, err := Fetch(context.Background(), raw, Options{
			Resolver: staticResolver{"example.com": {"93.184.216.34"}},
		})
		if !errors.Is(err, ErrInvalidURL) {
			t.Errorf("Fetch(%q) err = %v, want ErrInvalidURL", raw, err)
		}
	}
}

func TestFetchRefusesPrivateLoopbackAndLinkLocalAddresses(t *testing.T) {
	t.Run("literal private IPs", func(t *testing.T) {
		targets := []string{
			"http://127.0.0.1/",
			"http://127.0.0.5:8080/admin",
			"http://10.0.0.1/",
			"http://172.16.0.1/",
			"http://192.168.1.1/",
			"http://169.254.169.254/latest/meta-data/",
			"http://100.64.0.1/",
			"http://0.0.0.0/",
			"http://[::1]/",
			"http://[fe80::1]/",
			"http://[fc00::1]/",
			"http://[fd12:3456::1]/",
			"http://[::ffff:127.0.0.1]/",
			"http://[::ffff:169.254.169.254]/",
			"http://[64:ff9b::10.0.0.1]/",
		}
		for _, raw := range targets {
			_, err := Fetch(context.Background(), raw, Options{})
			if !errors.Is(err, ErrPrivateAddress) {
				t.Errorf("Fetch(%q) err = %v, want ErrPrivateAddress", raw, err)
			}
		}
	})

	t.Run("DNS resolving to private or mixed IPs", func(t *testing.T) {
		cases := map[string][]string{
			"loopback.test": {"127.0.0.1"},
			"meta.test":     {"169.254.169.254"},
			"lan.test":      {"10.1.2.3"},
			"cgnat.test":    {"100.64.10.2"},
			"ula.test":      {"fd00::1"},
			"v4mapped.test": {"::ffff:192.168.1.10"},
			"mixed.test":    {"93.184.216.34", "127.0.0.1"}, // any private record refuses host
		}
		for host, ips := range cases {
			dialed := false
			_, err := Fetch(context.Background(), "http://"+host+"/page", Options{
				Resolver: staticResolver{host: ips},
				DialContext: func(context.Context, string, string) (net.Conn, error) {
					dialed = true
					return nil, errors.New("must not dial")
				},
			})
			if !errors.Is(err, ErrPrivateAddress) {
				t.Errorf("host %s (%v): err = %v, want ErrPrivateAddress", host, ips, err)
			}
			if dialed {
				t.Errorf("host %s dialed despite private DNS result", host)
			}
		}
	})
}

func TestFetchRefusesRedirectToPrivateAddressAndCapsHops(t *testing.T) {
	t.Run("redirect to literal private IP refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
		}))
		defer srv.Close()

		_, err := Fetch(context.Background(), "http://public.example/start", Options{
			Resolver:    staticResolver{"public.example": {"93.184.216.34"}},
			DialContext: dialToTestServer(t, srv, "93.184.216.34"),
		})
		if !errors.Is(err, ErrPrivateAddress) {
			t.Fatalf("redirect to metadata IP: err = %v, want ErrPrivateAddress", err)
		}
	})

	t.Run("redirect to hostname resolving to loopback refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://internal.example/secret", http.StatusMovedPermanently)
		}))
		defer srv.Close()

		_, err := Fetch(context.Background(), "http://public.example/start", Options{
			Resolver: staticResolver{
				"public.example":   {"93.184.216.34"},
				"internal.example": {"127.0.0.1"},
			},
			DialContext: dialToTestServer(t, srv, "93.184.216.34"),
		})
		if !errors.Is(err, ErrPrivateAddress) {
			t.Fatalf("redirect to internal DNS: err = %v, want ErrPrivateAddress", err)
		}
	})

	t.Run("redirect to non-http scheme refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "file:///etc/passwd")
			w.WriteHeader(http.StatusFound)
		}))
		defer srv.Close()

		_, err := Fetch(context.Background(), "http://public.example/start", Options{
			Resolver:    staticResolver{"public.example": {"93.184.216.34"}},
			DialContext: dialToTestServer(t, srv, "93.184.216.34"),
		})
		if !errors.Is(err, ErrInvalidURL) {
			t.Fatalf("redirect to file://: err = %v, want ErrInvalidURL", err)
		}
	})

	t.Run("redirect hop limit enforced", func(t *testing.T) {
		hops := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hops++
			http.Redirect(w, r, fmt.Sprintf("/hop/%d", hops), http.StatusFound)
		}))
		defer srv.Close()

		_, err := Fetch(context.Background(), "http://public.example/hop/0", Options{
			MaxRedirects: 3,
			Resolver:     staticResolver{"public.example": {"93.184.216.34"}},
			DialContext:  dialToTestServer(t, srv, "93.184.216.34"),
		})
		if !errors.Is(err, ErrTooManyRedirects) {
			t.Fatalf("too many redirects: err = %v, want ErrTooManyRedirects", err)
		}
		if hops != 4 {
			t.Fatalf("hops = %d, want 4 (initial + 3 redirects before 4th rejected)", hops)
		}
	})

	t.Run("no cookies or auth sent across redirects", func(t *testing.T) {
		var seenCookies, seenAuth string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/login" {
				http.SetCookie(w, &http.Cookie{Name: "session", Value: "secret-cookie"})
				http.Redirect(w, r, "/target", http.StatusFound)
				return
			}
			seenCookies = r.Header.Get("Cookie")
			seenAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("clean"))
		}))
		defer srv.Close()

		res, err := Fetch(context.Background(), "http://public.example/login", Options{
			Resolver:    staticResolver{"public.example": {"93.184.216.34"}},
			DialContext: dialToTestServer(t, srv, "93.184.216.34"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if seenCookies != "" || seenAuth != "" {
			t.Fatalf("leaked credentials on redirect: Cookie=%q Authorization=%q", seenCookies, seenAuth)
		}
		if !strings.Contains(res.Text, "clean") {
			t.Fatalf("unexpected body: %q", res.Text)
		}
	})
}

func TestFetchSizeTimeCapsBinaryAndTruncation(t *testing.T) {
	t.Run("response size cap enforced", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(strings.Repeat("a", 2048)))
		}))
		defer srv.Close()

		_, err := Fetch(context.Background(), "http://public.example/big", Options{
			MaxResponseBytes: 1024,
			Resolver:         staticResolver{"public.example": {"93.184.216.34"}},
			DialContext:      dialToTestServer(t, srv, "93.184.216.34"),
		})
		if !errors.Is(err, ErrResponseTooLarge) {
			t.Fatalf("oversized response: err = %v, want ErrResponseTooLarge", err)
		}
	})

	t.Run("timeout cap enforced", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		defer func() {
			close(release)
			srv.Close()
		}()

		start := time.Now()
		_, err := Fetch(context.Background(), "http://public.example/slow", Options{
			Timeout:     80 * time.Millisecond,
			Resolver:    staticResolver{"public.example": {"93.184.216.34"}},
			DialContext: dialToTestServer(t, srv, "93.184.216.34"),
		})
		if err == nil {
			t.Fatal("expected timeout error, got nil")
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("timeout took %s, want ~80ms", elapsed)
		}
	})

	t.Run("binary media type and NUL bytes refused", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/png" {
				w.Header().Set("Content-Type", "image/png")
				_, _ = w.Write([]byte{0x89, 'P', 'N', 'G'})
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("hello\x00world"))
		}))
		defer srv.Close()

		opts := Options{
			Resolver:    staticResolver{"public.example": {"93.184.216.34"}},
			DialContext: dialToTestServer(t, srv, "93.184.216.34"),
		}
		if _, err := Fetch(context.Background(), "http://public.example/png", opts); !errors.Is(err, ErrBinaryContent) {
			t.Fatalf("image/png err = %v, want ErrBinaryContent", err)
		}
		if _, err := Fetch(context.Background(), "http://public.example/nul", opts); !errors.Is(err, ErrBinaryContent) {
			t.Fatalf("NUL text/plain err = %v, want ErrBinaryContent", err)
		}
	})

	t.Run("output truncated to MaxOutputBytes", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("HEAD-MARKER " + strings.Repeat("0123456789abcdef", 4000) + " TAIL-MARKER"))
		}))
		defer srv.Close()

		res, err := Fetch(context.Background(), "http://public.example/long", Options{
			MaxOutputBytes: DefaultMaxOutputBytes,
			Resolver:       staticResolver{"public.example": {"93.184.216.34"}},
			DialContext:    dialToTestServer(t, srv, "93.184.216.34"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if !res.Truncated {
			t.Fatal("expected Truncated=true")
		}
		if len(res.Text) > DefaultMaxOutputBytes {
			t.Fatalf("output len = %d, exceeds %d", len(res.Text), DefaultMaxOutputBytes)
		}
		if !strings.Contains(res.Text, "HEAD-MARKER") || !strings.Contains(res.Text, "TAIL-MARKER") || !strings.Contains(res.Text, "bytes truncated") {
			t.Fatalf("truncation should preserve head, tail, and marker; got len=%d", len(res.Text))
		}
	})
}

func TestFetchHTMLToReadableText(t *testing.T) {
	page := `<!doctype html>
<html>
<head>
  <title>  Example   Docs  </title>
  <style>body { color: red; }</style>
  <script>console.log("secret-script")</script>
</head>
<body>
  <main>
    <h1>Getting Started</h1>
    <p>Hello <b>world</b>, welcome to <a href="/docs">docs</a>.</p>
    <ul>
      <li>First item</li>
      <li>Second item</li>
    </ul>
    <table><tr><th>Key</th><th>Val</th></tr><tr><td>A</td><td>1</td></tr></table>
    <noscript>hidden-noscript</noscript>
    <svg><text>svg-noise</text></svg>
  </main>
</body>
</html>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(page))
	}))
	defer srv.Close()

	res, err := Fetch(context.Background(), "http://public.example/docs", Options{
		Resolver:    staticResolver{"public.example": {"93.184.216.34"}},
		DialContext: dialToTestServer(t, srv, "93.184.216.34"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Example Docs" {
		t.Fatalf("Title = %q, want %q", res.Title, "Example Docs")
	}
	for _, want := range []string{
		"URL: http://public.example/docs",
		"Status: 200 OK",
		"Title: Example Docs",
		"Getting Started",
		"Hello world, welcome to docs.",
		"- First item",
		"- Second item",
		"Key | Val",
		"A | 1",
	} {
		if !strings.Contains(res.Text, want) {
			t.Errorf("extracted text missing %q:\n%s", want, res.Text)
		}
	}
	for _, unwanted := range []string{"secret-script", "color: red", "hidden-noscript", "svg-noise", "<h1>"} {
		if strings.Contains(res.Text, unwanted) {
			t.Errorf("extracted text leaked %q:\n%s", unwanted, res.Text)
		}
	}
}
