package wsdashboard

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
	"github.com/kang-sw/devenv/internal/wscred"
)

// Fixture values: shaped like real ones, but never real.
const (
	credFixtureKey   = "sk-or-v1-fixture-key-0001"
	credFixtureKey2  = "sk-or-v1-fixture-key-0002"
	tokenPermitted   = "fixture-token-permitted"
	tokenWorker      = "fixture-token-worker"
	tokenNotAdmitted = "fixture-token-plain"
)

// credentialAgents is an agent host whose sessions are a fixed token table.
type credentialAgents struct {
	sessions map[string]CredentialSession
}

func (credentialAgents) Handler() http.Handler               { return http.NotFoundHandler() }
func (credentialAgents) Start(func(replyID, project string)) {}
func (credentialAgents) InboxChanged(string)                 {}
func (credentialAgents) Close()                              {}
func (a credentialAgents) ResolveCredentialToken(token string) (CredentialSession, bool) {
	s, ok := a.sessions[token]
	return s, ok
}

func testCredentialAgents() credentialAgents {
	return credentialAgents{sessions: map[string]CredentialSession{
		tokenPermitted:   {Conversation: "lead", Harness: "test-builtin", Lead: true, Permitted: true},
		tokenWorker:      {Conversation: "worker", Harness: "test-builtin", Permitted: true},
		tokenNotAdmitted: {Conversation: "other", Harness: "claude"},
	}}
}

// spyStore records the requests the token route makes of its store.
type spyStore struct {
	*wscred.Store
	mu   sync.Mutex
	reqs []wscred.Request
}

func (s *spyStore) Credential(ctx context.Context, req wscred.Request) (wscred.Credential, error) {
	s.mu.Lock()
	s.reqs = append(s.reqs, req)
	s.mu.Unlock()
	return s.Store.Credential(ctx, req)
}

type credentialDaemon struct {
	d        *Daemon
	dir      string
	credPath string
	log      *syncBuffer
	base     string
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func startCredentialDaemon(t *testing.T, dir, credPath string, source credentialStore) credentialDaemon {
	t.Helper()
	logw := &syncBuffer{}
	opts := DaemonOptions{Dir: dir, Home: t.TempDir(), Port: 0, Version: "1.0.0", LockWait: 200 * time.Millisecond,
		Log: logw, Agents: testCredentialAgents(), credentialSource: source}
	if source == nil {
		opts.Credentials = wscred.NewStore(credPath)
	}
	d, err := StartDaemon(opts)
	if err != nil {
		t.Fatalf("StartDaemon: %v", err)
	}
	t.Cleanup(d.Close)
	return credentialDaemon{d: d, dir: dir, credPath: credPath, log: logw, base: "http://127.0.0.1:" + strconv.Itoa(d.Endpoint().Port)}
}

// plainClient carries no dashboard login cookie: the routes are machine
// routes, admitted by their own credentials alone.
func plainClient() *http.Client {
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{Proxy: nil}}
}

func routeCredential(t *testing.T, cd credentialDaemon, token string) (wscred.Credential, error) {
	t.Helper()
	return wscred.NewRouteClient(cd.base, wscred.NewSecret(token), plainClient()).Credential(context.Background(), wscred.Request{Provider: wscred.ProviderOpenRouter})
}

func postRaw(t *testing.T, url string, headers map[string]string, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := plainClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// TestProviderTokenRouteAdmitsOnlyPermittedSessions: the route answers a
// session whose backend registration admits provider credentials, and
// refuses every other bearer without the credential in the refusal.
func TestProviderTokenRouteAdmitsOnlyPermittedSessions(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(t.TempDir(), "auth", wscred.FileName)
	spy := &spyStore{Store: wscred.NewStore(credPath)}
	if _, err := spy.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(credFixtureKey)}); err != nil {
		t.Fatal(err)
	}
	cd := startCredentialDaemon(t, dir, credPath, spy)

	cred, err := routeCredential(t, cd, tokenPermitted)
	if err != nil || cred.Secret.Reveal() != credFixtureKey {
		t.Fatalf("permitted lead = %+v, %v", cred, err)
	}
	if _, err := routeCredential(t, cd, tokenWorker); err != nil {
		t.Fatalf("permitted worker = %v", err)
	}
	spy.mu.Lock()
	reqs := append([]wscred.Request(nil), spy.reqs...)
	spy.mu.Unlock()
	if len(reqs) != 2 || !reqs[0].AgentSession || reqs[0].Background || !reqs[1].AgentSession || !reqs[1].Background {
		t.Fatalf("requester fields the route derived = %+v", reqs)
	}
	if _, err := routeCredential(t, cd, tokenNotAdmitted); !errors.Is(err, wscred.ErrNotPermitted) {
		t.Fatalf("a session of a harness without the capability = %v", err)
	}
	if _, err := routeCredential(t, cd, "fixture-token-unknown"); !errors.Is(err, wscred.ErrUnauthorized) {
		t.Fatalf("an unknown token = %v", err)
	}
	url := cd.base + wscred.TokenRoutePath
	body := `{"provider":"openrouter"}`
	for name, headers := range map[string]map[string]string{
		"no bearer":    {},
		"not admitted": {"Authorization": "Bearer " + tokenNotAdmitted},
		"browser":      {"Authorization": "Bearer " + tokenPermitted, "Origin": cd.base},
		"endpoint":     {"Authorization": "Bearer " + cd.d.Endpoint().Token},
	} {
		code, got := postRaw(t, url, headers, body)
		if code == http.StatusOK || strings.Contains(got, "fixture-key") || strings.Contains(got, "fixture-token") {
			t.Errorf("%s = %d %s", name, code, got)
		}
	}
	// Another machine never reaches the route, even with a session token.
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1"+wscred.TokenRoutePath, strings.NewReader(body))
	req.RemoteAddr = "192.0.2.1:50000"
	req.Header.Set("Authorization", "Bearer "+tokenPermitted)
	rec := httptest.NewRecorder()
	cd.d.Handler().ServeHTTP(rec, req)
	if rec.Code == http.StatusOK || strings.Contains(rec.Body.String(), "fixture-key") {
		t.Fatalf("a foreign peer = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(cd.log.String(), "fixture") {
		t.Fatalf("the daemon log carries a credential or token: %s", cd.log.String())
	}
}

// TestCredentialChangeAgainstRunningDaemonIsServedByTheDaemon: with a daemon
// running, a CLI change goes to the daemon, which writes the file, and the
// next token request sees it.
func TestCredentialChangeAgainstRunningDaemonIsServedByTheDaemon(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(t.TempDir(), "auth", wscred.FileName)
	cd := startCredentialDaemon(t, dir, credPath, nil)
	beforeOfflineCredentialWrite = func() { t.Error("the CLI wrote the file beside a running daemon") }
	t.Cleanup(func() { beforeOfflineCredentialWrite = nil })
	// The CLI's own idea of the file is never touched on this path.
	cliPath := filepath.Join(t.TempDir(), "cli", wscred.FileName)
	ctx := context.Background()

	if _, err := routeCredential(t, cd, tokenPermitted); !errors.Is(err, wscred.ErrNotConfigured) {
		t.Fatalf("before set = %v", err)
	}
	res, served, err := ApplyCredentialChange(ctx, dir, cliPath, wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(credFixtureKey)})
	if err != nil || served != CredentialServedByDaemon || !res.Changed || res.Status.Masked != "sk-or-...0001" {
		t.Fatalf("set = %+v, %q, %v", res, served, err)
	}
	if cred, err := routeCredential(t, cd, tokenPermitted); err != nil || cred.Secret.Reveal() != credFixtureKey {
		t.Fatalf("after set = %+v, %v", cred, err)
	}
	if raw, err := os.ReadFile(credPath); err != nil || !strings.Contains(string(raw), credFixtureKey) {
		t.Fatalf("the daemon did not write its file: %v", err)
	}
	if _, err := os.Stat(cliPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the CLI wrote its own file: %v", err)
	}

	res, served, err = ApplyCredentialChange(ctx, dir, cliPath, wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(credFixtureKey2)})
	if err != nil || served != CredentialServedByDaemon || !res.Changed {
		t.Fatalf("replace = %+v, %q, %v", res, served, err)
	}
	if cred, _ := routeCredential(t, cd, tokenPermitted); cred.Secret.Reveal() != credFixtureKey2 {
		t.Fatal("the next token request did not see the replaced key")
	}
	res, served, err = ApplyCredentialChange(ctx, dir, cliPath, wscred.Change{Op: wscred.OpRemove, Provider: wscred.ProviderOpenRouter})
	if err != nil || served != CredentialServedByDaemon || !res.Changed || res.Status.Configured {
		t.Fatalf("remove = %+v, %q, %v", res, served, err)
	}
	if _, err := routeCredential(t, cd, tokenPermitted); !errors.Is(err, wscred.ErrNotConfigured) {
		t.Fatalf("after remove = %v", err)
	}
	// A refused change comes back without the key.
	_, _, err = ApplyCredentialChange(ctx, dir, cliPath, wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret("sk-or-v1 fixture spaced")})
	if err == nil || strings.Contains(err.Error(), "fixture") {
		t.Fatalf("refused change = %v", err)
	}
	if strings.Contains(cd.log.String(), "fixture") {
		t.Fatalf("the daemon log carries a credential: %s", cd.log.String())
	}
}

func TestCredentialChangeRouteNeedsTheEndpointToken(t *testing.T) {
	dir := t.TempDir()
	cd := startCredentialDaemon(t, dir, filepath.Join(t.TempDir(), "auth", wscred.FileName), nil)
	url := cd.base + credentialChangePath
	body := `{"op":"set","provider":"openrouter","api_key":"` + credFixtureKey + `"}`
	for name, headers := range map[string]map[string]string{
		"no bearer":     {},
		"session token": {"Authorization": "Bearer " + tokenPermitted},
		"browser":       {"Authorization": "Bearer " + cd.d.Endpoint().Token, "Origin": cd.base},
	} {
		if code, got := postRaw(t, url, headers, body); code == http.StatusOK || strings.Contains(got, "fixture") {
			t.Errorf("%s = %d %s", name, code, got)
		}
	}
	if _, err := routeCredential(t, cd, tokenPermitted); !errors.Is(err, wscred.ErrNotConfigured) {
		t.Fatalf("a refused change took effect: %v", err)
	}
	code, got := postRaw(t, url, map[string]string{"Authorization": "Bearer " + cd.d.Endpoint().Token}, body)
	if code != http.StatusOK || strings.Contains(got, credFixtureKey) || !strings.Contains(got, `"masked":"sk-or-...0001"`) {
		t.Fatalf("authenticated change = %d %s", code, got)
	}
}

// TestOfflineCredentialChangeTakesTheDaemonLock: with no daemon running, the
// CLI writes the file itself while holding the daemon's lock, so no daemon
// starts mid-write; the next daemon reads the file at its first use.
func TestOfflineCredentialChangeTakesTheDaemonLock(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(t.TempDir(), "auth", wscred.FileName)
	var hookRan bool
	beforeOfflineCredentialWrite = func() {
		hookRan = true
		if d, err := StartDaemon(DaemonOptions{Dir: dir, Version: "1.0.0", LockWait: 100 * time.Millisecond}); !errors.Is(err, ErrLocked) {
			if d != nil {
				d.Close()
			}
			t.Errorf("a daemon start during the offline write = %v, want ErrLocked", err)
		}
	}
	t.Cleanup(func() { beforeOfflineCredentialWrite = nil })
	res, served, err := ApplyCredentialChange(context.Background(), dir, credPath, wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(credFixtureKey)})
	if err != nil || served != CredentialServedByFile || !res.Changed || !hookRan {
		t.Fatalf("offline set = %+v, %q, %v (hook ran %v)", res, served, err, hookRan)
	}
	beforeOfflineCredentialWrite = nil
	// The lock is released: a daemon starts, and serves the file's key.
	cd := startCredentialDaemon(t, dir, credPath, nil)
	if cred, err := routeCredential(t, cd, tokenPermitted); err != nil || cred.Secret.Reveal() != credFixtureKey {
		t.Fatalf("the next daemon = %+v, %v", cred, err)
	}
}

// TestCredentialChangeToADaemonWithoutTheRoute: a running daemon that
// predates the change route is named, not waited out; one that holds the
// lock and never answers fails at the deadline.
// fakeLockHolder serves /health as the daemon the endpoint file names and
// change for every other path, standing in for a daemon that holds the lock.
func fakeLockHolder(t *testing.T, dir string, change http.HandlerFunc) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			writeJSON(w, http.StatusOK, HealthInfo{Service: healthService, PID: os.Getpid()})
			return
		}
		change(w, r)
	}))
	t.Cleanup(srv.Close)
	port, _ := strconv.Atoi(srv.URL[strings.LastIndex(srv.URL, ":")+1:])
	if err := writeEndpoint(dir, Endpoint{SchemaVersion: endpointSchemaVersion, Port: port, PID: os.Getpid(), Token: "fixture-endpoint-token"}); err != nil {
		t.Fatal(err)
	}
}

func TestCredentialChangeToADaemonWithoutTheRoute(t *testing.T) {
	restore := credentialChangeWait
	credentialChangeWait = 300 * time.Millisecond
	t.Cleanup(func() { credentialChangeWait = restore })
	dir := t.TempDir()
	holder := flock.New(filepath.Join(dir, lockFileName))
	if ok, err := holder.TryLock(); !ok || err != nil {
		t.Fatalf("hold the lock: %v %v", ok, err)
	}
	t.Cleanup(func() { _ = holder.Unlock() })
	beforeOfflineCredentialWrite = func() { t.Error("the CLI wrote the file while a daemon held the lock") }
	t.Cleanup(func() { beforeOfflineCredentialWrite = nil })
	change := wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(credFixtureKey)}
	credPath := filepath.Join(t.TempDir(), wscred.FileName)

	if _, _, err := ApplyCredentialChange(context.Background(), dir, credPath, change); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("a silent lock holder = %v", err)
	}

	// A stale endpoint file whose port another process took: the key and
	// the endpoint token are never sent to it.
	var strangerMu sync.Mutex
	var strangerSaw []string
	stranger := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		strangerMu.Lock()
		strangerSaw = append(strangerSaw, r.URL.Path+" "+r.Header.Get("Authorization")+" "+string(body))
		strangerMu.Unlock()
		writeJSON(w, http.StatusOK, map[string]string{"service": "something-else"})
	}))
	t.Cleanup(stranger.Close)
	strangerPort, _ := strconv.Atoi(stranger.URL[strings.LastIndex(stranger.URL, ":")+1:])
	if err := writeEndpoint(dir, Endpoint{SchemaVersion: endpointSchemaVersion, Port: strangerPort, PID: os.Getpid(), Token: "fixture-endpoint-token"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ApplyCredentialChange(context.Background(), dir, credPath, change); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("a stranger on the recorded port = %v", err)
	}
	strangerMu.Lock()
	if len(strangerSaw) == 0 {
		t.Error("the stranger was never asked who it is")
	}
	for _, seen := range strangerSaw {
		if strings.Contains(seen, credFixtureKey) || strings.Contains(seen, "fixture-endpoint-token") || strings.Contains(seen, credentialChangePath) {
			t.Errorf("the stranger received the change: %q", seen)
		}
	}
	strangerMu.Unlock()

	// An older daemon answers its access guard's login_required.
	fakeLockHolder(t, dir, func(w http.ResponseWriter, r *http.Request) { loginRequired(w) })
	started := time.Now()
	if _, _, err := ApplyCredentialChange(context.Background(), dir, credPath, change); err == nil || !strings.Contains(err.Error(), "does not take credential changes") {
		t.Fatalf("an older daemon = %v", err)
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatal("an older daemon was waited out instead of named")
	}

	// A daemon without a credential store says so at once; a draining one
	// is waited out.
	fakeLockHolder(t, dir, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, credentialChangeRefusal{Error: "this daemon holds no provider credentials"})
	})
	started = time.Now()
	if _, _, err := ApplyCredentialChange(context.Background(), dir, credPath, change); err == nil || !strings.Contains(err.Error(), "holds no provider credentials") {
		t.Fatalf("a daemon without a store = %v", err)
	}
	if time.Since(started) > 250*time.Millisecond {
		t.Fatal("a daemon without a store was waited out instead of named")
	}
	fakeLockHolder(t, dir, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "shutting_down"})
	})
	if _, _, err := ApplyCredentialChange(context.Background(), dir, credPath, change); err == nil || !strings.Contains(err.Error(), "did not answer") {
		t.Fatalf("a draining daemon = %v", err)
	}
}

// gatedStore holds its first change at a gate inside the store's critical
// section, the way a slow write would; its Close waits for that change the
// way the real store's does (wscred tests the real fence).
type gatedStore struct {
	*wscred.Store
	mu         sync.Mutex
	held       bool
	entered    chan struct{}
	gate       chan struct{}
	closing    chan struct{}
	lockHeld   func() bool
	heldAtLand []bool
}

func (g *gatedStore) Apply(c wscred.Change) (wscred.ChangeResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.held {
		g.held = true
		close(g.entered)
		<-g.gate
	}
	res, err := g.Store.Apply(c)
	if err == nil && res.Changed {
		g.heldAtLand = append(g.heldAtLand, g.lockHeld())
	}
	return res, err
}

func (g *gatedStore) Close() {
	close(g.closing)
	g.mu.Lock()
	defer g.mu.Unlock()
	g.Store.Close()
}

// TestDaemonShutdownFencesTheCredentialWriter: a change in flight when the
// daemon stops lands before the daemon releases its lock, and a later one is
// refused; no daemon write lands after another process may own the file.
func TestDaemonShutdownFencesTheCredentialWriter(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(t.TempDir(), "auth", wscred.FileName)
	lockHeld := func() bool {
		probe := flock.New(filepath.Join(dir, lockFileName))
		ok, err := probe.TryLock()
		if ok {
			_ = probe.Unlock()
		}
		return !ok || err != nil
	}
	g := &gatedStore{Store: wscred.NewStore(credPath), entered: make(chan struct{}), gate: make(chan struct{}), closing: make(chan struct{}), lockHeld: lockHeld}
	cd := startCredentialDaemon(t, dir, credPath, g)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sent := make(chan error, 1)
	go func() {
		_, _, err := ApplyCredentialChange(ctx, dir, filepath.Join(t.TempDir(), wscred.FileName), wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(credFixtureKey)})
		sent <- err
	}()
	<-g.entered
	stopped := make(chan struct{})
	go func() {
		cd.d.Close()
		close(stopped)
	}()
	select {
	case <-g.closing:
	case <-stopped:
		t.Fatal("the daemon stopped without closing its credential store")
	}
	if !lockHeld() {
		t.Fatal("the daemon released its lock while a credential change was in flight")
	}
	close(g.gate)
	<-stopped
	if len(g.heldAtLand) != 1 || !g.heldAtLand[0] {
		t.Fatalf("the change in flight landed with the lock held = %v", g.heldAtLand)
	}
	if lockHeld() {
		t.Fatal("the stopped daemon still holds its lock")
	}
	before, err := os.ReadFile(credPath)
	if err != nil || !strings.Contains(string(before), credFixtureKey) {
		t.Fatalf("the change in flight did not land: %v", err)
	}
	if _, err := g.Apply(wscred.Change{Op: wscred.OpRemove, Provider: wscred.ProviderOpenRouter}); !errors.Is(err, wscred.ErrStoreClosed) {
		t.Fatalf("a change after the daemon stopped = %v", err)
	}
	if after, _ := os.ReadFile(credPath); string(after) != string(before) {
		t.Fatal("a refused change rewrote the file after the lock was released")
	}
	cancel()
	<-sent
}

// TestChangeRouteOnAClosedStoreAsksTheSenderToRetry: a change that reaches
// a stopping daemon after its store closed is answered as a drain, so the
// sender retries against the successor instead of failing.
func TestChangeRouteOnAClosedStoreAsksTheSenderToRetry(t *testing.T) {
	dir := t.TempDir()
	credPath := filepath.Join(t.TempDir(), "auth", wscred.FileName)
	store := &spyStore{Store: wscred.NewStore(credPath)}
	cd := startCredentialDaemon(t, dir, credPath, store)
	store.Close()
	ep, err := ReadEndpoint(dir)
	if err != nil {
		t.Fatal(err)
	}
	code, body := postRaw(t, cd.base+credentialChangePath, map[string]string{"Authorization": "Bearer " + ep.Token, "Content-Type": "application/json"}, `{"op":"remove","provider":"openrouter"}`)
	if code != http.StatusServiceUnavailable || !strings.Contains(body, "shutting_down") {
		t.Fatalf("change on a closed store = %d %s", code, body)
	}
}
