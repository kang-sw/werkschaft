package wsdashboard

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// postFullStop posts the full stop to d over a real connection with the
// given Authorization header ("" sends none), as the desktop shell does:
// no cookie and no Origin.
func postFullStop(t *testing.T, d *Daemon, authorization string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, endpointURL(d.Endpoint(), FullStopPath), nil)
	if err != nil {
		t.Fatal(err)
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func decodeFullStop(t *testing.T, body string) FullStopResult {
	t.Helper()
	var out FullStopResult
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("full stop body: %v\n%s", err, body)
	}
	return out
}

func assertRunning(t *testing.T, d *Daemon, why string) {
	t.Helper()
	select {
	case <-d.Done():
		t.Fatalf("the daemon stopped %s", why)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestFullStopRequiresTheEndpointToken pins the full stop's credential: the
// endpoint file's token as a bearer, exactly as /shutdown. A missing,
// wrong, or scheme-less token, or the LAN token, is answered 401
// unauthorized (never the guard's login_required, which the shell reads as
// a daemon without the route), and stops nothing; a read is not exempt.
func TestFullStopRequiresTheEndpointToken(t *testing.T) {
	holders := newFakeHolders()
	holders.listing = holderFixtureListing()
	f := startPageFixture(t, lanMode, withHolders(holders))
	ep := f.d.Endpoint()
	last := "0"
	if strings.HasSuffix(ep.Token, "0") {
		last = "1"
	}
	for name, header := range map[string]string{
		"missing":   "",
		"wrong":     "Bearer " + ep.Token[:len(ep.Token)-1] + last,
		"no scheme": ep.Token,
		"LAN token": "Bearer " + readLANToken(t, f.dir),
	} {
		code, body := postFullStop(t, f.d, header)
		if code != http.StatusUnauthorized || !strings.Contains(body, "unauthorized") || strings.Contains(body, "login_required") {
			t.Errorf("%s token: %d %q, want 401 unauthorized", name, code, strings.TrimSpace(body))
		}
	}
	req, _ := http.NewRequest(http.MethodGet, endpointURL(ep, FullStopPath), nil)
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// As for /shutdown, the exemption covers the POST only, so a read
	// meets the access guard.
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET full stop = %d, want the guard's 401", resp.StatusCode)
	}
	if got := holders.callOrder(); len(got) != 0 {
		t.Fatalf("a refused full stop called %v", got)
	}
	assertRunning(t, f.d, "on a refused full stop")
}

// TestFullStopEndsEveryHolderBeforeTheDaemon pins the complete full stop:
// every holder is stopped first, the answer is 202 with shutting_down and
// the stopped holders, and only then does the daemon close (and the holder
// host with it), removing its endpoint file.
func TestFullStopEndsEveryHolderBeforeTheDaemon(t *testing.T) {
	holders := newFakeHolders()
	holders.listing = holderFixtureListing()
	f := startPageFixture(t, withHolders(holders))
	<-holders.running

	code, body := postFullStop(t, f.d, "Bearer "+f.d.Endpoint().Token)
	if code != http.StatusAccepted {
		t.Fatalf("full stop = %d %q, want 202", code, strings.TrimSpace(body))
	}
	got := decodeFullStop(t, body)
	if !got.ShuttingDown || !slices.Equal(got.Stopped, []string{"hlost", "hjob", "hlive"}) || got.Remaining == nil || len(got.Remaining) != 0 {
		t.Fatalf("full stop answer = %+v, want shutting_down with all three stopped and an empty remaining list", got)
	}
	waitDone(t, f.d)
	if order := holders.callOrder(); !slices.Equal(order, []string{"stopall", "close"}) {
		t.Fatalf("calls = %v, want the holders stopped before the host closed", order)
	}
	if _, err := os.Stat(filepath.Join(f.dir, endpointFileName)); !os.IsNotExist(err) {
		t.Fatalf("endpoint file after the full stop: %v", err)
	}
}

// TestFullStopReportsRemainingHoldersAndKeepsTheDaemon pins the partial
// failure: a holder the stop could not end is answered 409 with its reason,
// shutting_down false and the others stopped; the daemon keeps running and
// listing it, and an ordinary /shutdown still stops only the daemon.
func TestFullStopReportsRemainingHoldersAndKeepsTheDaemon(t *testing.T) {
	holders := newFakeHolders()
	holders.listing = holderFixtureListing()
	holders.stopErr["hjob"] = wsholder.ErrStartTimeMismatch
	f := startPageFixture(t, withHolders(holders))
	local := viewer{peer: "127.0.0.1:50000", host: "127.0.0.1:" + strconv.Itoa(f.d.Endpoint().Port), localCookie: true}

	code, body := postFullStop(t, f.d, "Bearer "+f.d.Endpoint().Token)
	if code != http.StatusConflict {
		t.Fatalf("full stop with a holder left = %d %q, want 409", code, strings.TrimSpace(body))
	}
	got := decodeFullStop(t, body)
	if got.ShuttingDown || !slices.Equal(got.Stopped, []string{"hlost", "hlive"}) || len(got.Remaining) != 1 ||
		got.Remaining[0].ID != "hjob" || got.Remaining[0].PID != 4200 || !strings.Contains(got.Remaining[0].Error, "start time") {
		t.Fatalf("full stop answer = %+v, want hjob remaining with its reason", got)
	}
	assertRunning(t, f.d, "with a holder remaining")
	if list := decodeHolderList(t, serve(f.d, local, http.MethodGet, "/holders", "")); len(list.Holders) != 1 || list.Holders[0].ID != "hjob" {
		t.Errorf("holders after the incomplete stop = %+v, want hjob only", list.Holders)
	}
	if err := RequestShutdown(context.Background(), &http.Client{Timeout: 5 * time.Second}, f.d.Endpoint()); err != nil {
		t.Fatal(err)
	}
	waitDone(t, f.d)
	if order := holders.callOrder(); !slices.Equal(order, []string{"stopall", "close"}) {
		t.Fatalf("calls = %v, want the ordinary shutdown to only close the host", order)
	}
}

// TestOrdinaryShutdownLeavesTheHolders pins that /shutdown keeps its
// meaning beside the full stop: the daemon stops and its holders outlive it.
func TestOrdinaryShutdownLeavesTheHolders(t *testing.T) {
	holders := newFakeHolders()
	holders.listing = holderFixtureListing()
	f := startPageFixture(t, withHolders(holders))
	if err := RequestShutdown(context.Background(), &http.Client{Timeout: 5 * time.Second}, f.d.Endpoint()); err != nil {
		t.Fatal(err)
	}
	waitDone(t, f.d)
	if order := holders.callOrder(); !slices.Equal(order, []string{"close"}) {
		t.Fatalf("calls = %v, want only the host's close", order)
	}
	if got := holders.stops(); len(got) != 0 {
		t.Fatalf("an ordinary shutdown stopped %v", got)
	}
}

// TestFullStopWithoutAHolderHostStopsTheDaemon: a daemon that runs no
// holder host has none to stop, so the full stop is a complete one.
func TestFullStopWithoutAHolderHostStopsTheDaemon(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), "1.0.0")
	code, body := postFullStop(t, d, "Bearer "+d.Endpoint().Token)
	if code != http.StatusAccepted {
		t.Fatalf("full stop = %d %q, want 202", code, strings.TrimSpace(body))
	}
	if got := decodeFullStop(t, body); !got.ShuttingDown || got.Stopped == nil || len(got.Stopped) != 0 || got.Remaining == nil || len(got.Remaining) != 0 {
		t.Fatalf("full stop answer = %+v, want shutting_down with empty lists", got)
	}
	waitDone(t, d)
}

// TestFullStopOfAStoppingDaemonStopsNothing: once the daemon is closing, a
// full stop is answered 503 and its holders are left as they are, so the
// caller does not report a full quit that did not happen.
func TestFullStopOfAStoppingDaemonStopsNothing(t *testing.T) {
	holders := newFakeHolders()
	holders.listing = holderFixtureListing()
	f := startPageFixture(t, withHolders(holders))
	f.d.Close()
	req := httptest.NewRequest(http.MethodPost, FullStopPath, nil)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:" + strconv.Itoa(f.d.Endpoint().Port)
	req.Header.Set("Authorization", "Bearer "+f.d.Endpoint().Token)
	rec := httptest.NewRecorder()
	f.d.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "shutting_down") {
		t.Fatalf("full stop of a closing daemon = %d %q, want 503 shutting_down", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	if order := holders.callOrder(); !slices.Equal(order, []string{"close"}) {
		t.Fatalf("calls = %v, want no StopAll", order)
	}
}

// TestUnexemptMachinePostIsLoginRequired pins the refusal an older daemon
// gives the full stop: its access exemption names only /shutdown, so a
// bearer-only POST to a path outside the exemption meets the localhost
// guard's 401 login_required before any route. The desktop shell reads
// that answer (and a 404) as a daemon without the full stop, distinct from
// the route's own 401 unauthorized.
func TestUnexemptMachinePostIsLoginRequired(t *testing.T) {
	d := startTestDaemon(t, t.TempDir(), "1.0.0")
	req, _ := http.NewRequest(http.MethodPost, endpointURL(d.Endpoint(), "/shutdown/other"), nil)
	req.Header.Set("Authorization", "Bearer "+d.Endpoint().Token)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusUnauthorized || body["error"] != "login_required" {
		t.Fatalf("bearer-only POST outside the exemption = %d %v, want 401 login_required", resp.StatusCode, body)
	}
	assertRunning(t, d, "on a refused request")
}
