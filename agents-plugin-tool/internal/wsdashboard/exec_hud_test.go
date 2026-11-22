package wsdashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
)

// execJobsFake is an ExecJobsService over a fixed set of jobs, recording the
// owner every call named.
type execJobsFake struct {
	mu     sync.Mutex
	open   error
	jobs   map[string][]execjob.HostedSummary // by owner
	out    map[string]execjob.HostedOutput
	stopFn func(owner, id string) (execjob.HostedSummary, error)
	owners []string
	froms  []int64
	limits []int
}

func (f *execJobsFake) Open(string) (uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return 4, f.open
}

func (f *execJobsFake) Jobs(owner string) []execjob.HostedSummary {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owners = append(f.owners, owner)
	return append([]execjob.HostedSummary(nil), f.jobs[owner]...)
}

func (f *execJobsFake) ReadOutput(owner, id string, from int64, limit int) (execjob.HostedOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owners = append(f.owners, owner)
	f.froms = append(f.froms, from)
	f.limits = append(f.limits, limit)
	for _, s := range f.jobs[owner] {
		if s.ID == id {
			return f.out[id], nil
		}
	}
	return execjob.HostedOutput{}, execjob.ErrExecNotFound
}

func (f *execJobsFake) StopByUser(owner, id string) (execjob.HostedSummary, error) {
	f.mu.Lock()
	f.owners = append(f.owners, owner)
	fn := f.stopFn
	jobs := f.jobs[owner]
	f.mu.Unlock()
	if fn != nil {
		return fn(owner, id)
	}
	for _, s := range jobs {
		if s.ID == id {
			s.State, s.StoppedByUser, s.StopNotice = execjob.HostedStopped, true, execjob.StopNoticeSent
			return s, nil
		}
	}
	return execjob.HostedSummary{}, execjob.ErrExecNotFound
}

func startHUDFixture(t *testing.T) (*composerFixture, *execJobsFake) {
	t.Helper()
	f := startComposerFixture(t)
	zero := 0
	svc := &execJobsFake{
		jobs: map[string][]execjob.HostedSummary{
			"livesession": {
				{ID: "exec_a-b-c", Tool: "exec.shell", Command: "npm run dev <b>", Cwd: "web/", State: execjob.HostedRunning, Origin: execjob.OriginAgent, StartedAt: 100, Written: 12},
				{ID: "exec_d-e-f", Tool: "exec.shell", Command: "go test", State: execjob.HostedExited, ExitCode: &zero, Origin: execjob.OriginUser, StartedAt: 50, EndedAt: 90, Written: 3},
			},
			"boardsession": {{ID: "exec_other-job-x", Command: "secret", State: execjob.HostedRunning}},
		},
		out: map[string]execjob.HostedOutput{
			"exec_a-b-c": {
				Job:  execjob.HostedSummary{ID: "exec_a-b-c", State: execjob.HostedRunning, Origin: execjob.OriginAgent},
				From: 0, Next: 12, Written: 12, Evicted: 0,
				Segments: []execjob.Segment{{Stream: execjob.StreamStdout, Data: []byte("ready <i>\n")}, {Stream: execjob.StreamStderr, Data: []byte("w\xffarn")}},
			},
		},
	}
	f.d.SetExecJobs(svc)
	return f, svc
}

func hudGet(f *composerFixture, path string) *httptest.ResponseRecorder {
	return composerHTTP(f.d, http.MethodGet, path, "", "", "127.0.0.1:1234", nil)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// TestExecHUDRoutesAreLocalhostOnlyAndOwnerScoped: list, output and stop
// answer only a local viewer for an open hosted lead, name the page's own
// conversation as the owner (never anything the browser sends), and refuse
// another conversation's job as unknown.
func TestExecHUDRoutesAreLocalhostOnlyAndOwnerScoped(t *testing.T) {
	f, svc := startHUDFixture(t)
	post := func(path, origin, remote string) *httptest.ResponseRecorder {
		return composerHTTP(f.d, http.MethodPost, path, "", origin, remote, nil)
	}
	// The list.
	res := hudGet(f, "/s/livesession/exec/jobs")
	if res.Code != 200 || res.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("list = %d %s", res.Code, res.Body)
	}
	var list struct {
		Rev  int64
		Jobs []execJobView
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil || len(list.Jobs) != 2 {
		t.Fatalf("list body = %s, %v", res.Body, err)
	}
	if j := list.Jobs[0]; j.ID != "exec_a-b-c" || j.Command != "npm run dev <b>" || j.Origin != execjob.OriginAgent || j.Written != 12 || j.State != execjob.HostedRunning {
		t.Fatalf("job = %+v", j)
	}
	if strings.Contains(res.Body.String(), "secret") {
		t.Fatalf("another conversation's job is listed: %s", res.Body)
	}
	// LAN peers, missing or foreign origins, wrong methods, unknown sessions.
	for _, c := range []struct {
		name, method, path, origin, remote string
		code                               int
	}{
		{"LAN list", http.MethodGet, "/s/livesession/exec/jobs", "", "192.0.2.1:9", 401},
		{"LAN stop", http.MethodPost, "/s/livesession/exec/exec_a-b-c/stop", "http://127.0.0.1", "192.0.2.1:9", 401},
		{"stop without origin", http.MethodPost, "/s/livesession/exec/exec_a-b-c/stop", "", "127.0.0.1:1", 403},
		{"stop cross-origin", http.MethodPost, "/s/livesession/exec/exec_a-b-c/stop", "http://evil.test", "127.0.0.1:1", 403},
		{"stop by GET", http.MethodGet, "/s/livesession/exec/exec_a-b-c/stop", "", "127.0.0.1:1", 404},
		{"list by POST", http.MethodPost, "/s/livesession/exec/jobs", "http://127.0.0.1", "127.0.0.1:1", 404},
		{"output by POST", http.MethodPost, "/s/livesession/exec/exec_a-b-c/output", "http://127.0.0.1", "127.0.0.1:1", 404},
		{"read with a foreign origin", http.MethodGet, "/s/livesession/exec/jobs", "http://evil.test", "127.0.0.1:1", 405},
		{"non-hosted session", http.MethodGet, "/s/boardsession/exec/jobs", "", "127.0.0.1:1", 404},
		{"unknown session", http.MethodGet, "/s/nosuchsession/exec/jobs", "", "127.0.0.1:1", 404},
		{"job named jobs", http.MethodPost, "/s/livesession/exec/jobs/stop", "http://127.0.0.1", "127.0.0.1:1", 404},
		{"bad job id", http.MethodPost, "/s/livesession/exec/..%2fx/stop", "http://127.0.0.1", "127.0.0.1:1", 404},
		{"unknown child", http.MethodPost, "/s/livesession/exec/exec_a-b-c/kill", "http://127.0.0.1", "127.0.0.1:1", 404},
	} {
		if res := composerHTTP(f.d, c.method, c.path, "", c.origin, c.remote, nil); res.Code != c.code {
			t.Fatalf("%s: %d %s", c.name, res.Code, res.Body)
		}
	}
	if res := composerHTTP(f.d, http.MethodGet, "/s/livesession/exec/jobs", "", "", "127.0.0.1:1", map[string]string{"Sec-Fetch-Site": "cross-site"}); res.Code != 405 {
		t.Fatalf("cross-site read = %d", res.Code)
	}
	// Stop: the owner is the page's conversation; a job of another owner, or
	// one that does not exist, is unknown and nothing of it is reported.
	res = post("/s/livesession/exec/exec_a-b-c/stop", "http://127.0.0.1", "127.0.0.1:1")
	var stopped struct{ Job execJobView }
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &stopped) != nil || !stopped.Job.StoppedByUser || stopped.Job.StopNotice != "sent" || stopped.Job.State != "stopped" {
		t.Fatalf("stop = %d %s", res.Code, res.Body)
	}
	if res := post("/s/livesession/exec/exec_other-job-x/stop", "http://127.0.0.1", "127.0.0.1:1"); res.Code != 404 {
		t.Fatalf("another owner's job = %d %s", res.Code, res.Body)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	for _, o := range svc.owners {
		if o != "livesession" {
			t.Fatalf("a call named owner %q", o)
		}
	}
}

// TestExecHUDRoutesFollowTheOpenAuthority: a closed or unbound service is
// unknown, so a page reads it as "no HUD".
func TestExecHUDRoutesFollowTheOpenAuthority(t *testing.T) {
	f, svc := startHUDFixture(t)
	svc.mu.Lock()
	svc.open = errors.New("closed")
	svc.mu.Unlock()
	for _, p := range []string{"/s/livesession/exec/jobs", "/s/livesession/exec/exec_a-b-c/output"} {
		if res := hudGet(f, p); res.Code != 404 {
			t.Fatalf("closed lead %s = %d", p, res.Code)
		}
	}
	if res := composerHTTP(f.d, http.MethodPost, "/s/livesession/exec/exec_a-b-c/stop", "", "http://127.0.0.1", "127.0.0.1:1", nil); res.Code != 404 {
		t.Fatalf("stop of a closed lead = %d", res.Code)
	}
	f.d.SetExecJobs(nil)
	if res := hudGet(f, "/s/livesession/exec/jobs"); res.Code != 404 {
		t.Fatalf("unbound service = %d, want 404 (no HUD)", res.Code)
	}
}

// TestExecHUDOutputRouteReadsByOffset: the offset reaches the service, the
// read is bounded by the service's limit, segments keep their stream and are
// decoded as text (invalid bytes replaced), and markup stays text.
func TestExecHUDOutputRouteReadsByOffset(t *testing.T) {
	f, svc := startHUDFixture(t)
	res := hudGet(f, "/s/livesession/exec/exec_a-b-c/output?from=7")
	var out execOutputView
	if res.Code != 200 || json.Unmarshal(res.Body.Bytes(), &out) != nil {
		t.Fatalf("output = %d %s", res.Code, res.Body)
	}
	if out.Next != 12 || out.Written != 12 || out.Job.ID != "exec_a-b-c" || len(out.Segments) != 2 ||
		out.Segments[0] != (execSegmentView{Stream: "stdout", Text: "ready <i>\n"}) ||
		out.Segments[1] != (execSegmentView{Stream: "stderr", Text: "w�arn"}) {
		t.Fatalf("output = %+v", out)
	}
	svc.mu.Lock()
	froms, limits := svc.froms, svc.limits
	svc.mu.Unlock()
	if len(froms) != 1 || froms[0] != 7 || limits[0] != execjob.HostedOutputLimit {
		t.Fatalf("service saw from %v limit %v", froms, limits)
	}
	for _, q := range []string{"from=-1", "from=abc", "from=99999999999999999999"} {
		if res := hudGet(f, "/s/livesession/exec/exec_a-b-c/output?"+q); res.Code != 400 {
			t.Fatalf("%s = %d", q, res.Code)
		}
	}
	if res := hudGet(f, "/s/livesession/exec/exec_nope/output"); res.Code != 404 {
		t.Fatalf("unknown job = %d", res.Code)
	}
	if res := hudGet(f, "/s/livesession/exec/exec_other-job-x/output"); res.Code != 404 {
		t.Fatalf("another owner's job = %d", res.Code)
	}
}

// TestExecHUDStopFailureReportsTheJob: a stop whose holder did not go is an
// error that still names the job, which may run on.
func TestExecHUDStopFailureReportsTheJob(t *testing.T) {
	f, svc := startHUDFixture(t)
	svc.mu.Lock()
	svc.stopFn = func(owner, id string) (execjob.HostedSummary, error) {
		return execjob.HostedSummary{ID: id, State: execjob.HostedRunning}, errors.New("stop exec job: holder unreachable")
	}
	svc.mu.Unlock()
	res := composerHTTP(f.d, http.MethodPost, "/s/livesession/exec/exec_a-b-c/stop", "", "http://127.0.0.1", "127.0.0.1:1", nil)
	var body struct {
		Error string
		Job   execJobView
	}
	if res.Code != http.StatusBadGateway || json.Unmarshal(res.Body.Bytes(), &body) != nil || !strings.Contains(body.Error, "holder unreachable") || body.Job.State != "running" {
		t.Fatalf("stop = %d %s", res.Code, res.Body)
	}
}

// TestExecJobsEventCarriesOnlyARevision: a session stream sends the execjobs
// event on connect and after every ExecJobsChanged, as {"rev":N} and nothing
// else, with no tracker bound; no region or event of the stream holds a job's
// command, working directory or output.
func TestExecJobsEventCarriesOnlyARevision(t *testing.T) {
	f, svc := startHUDFixture(t)
	if f.d.tracker != nil {
		t.Fatal("the fixture binds a tracker; this test pins the event as not tracker-gated")
	}
	events := openStream(t, f.base+"/s/livesession/events")
	var all []sseEvent
	rev := func(want string) {
		t.Helper()
		deadline := time.After(5 * time.Second)
		for {
			select {
			case ev, ok := <-events:
				if !ok {
					t.Fatal("stream closed")
				}
				all = append(all, ev)
				if ev.name == "execjobs" {
					if ev.data != want {
						t.Fatalf("execjobs data = %s, want %s", ev.data, want)
					}
					return
				}
			case <-deadline:
				t.Fatalf("no execjobs event %s; got %+v", want, all)
			}
		}
	}
	start := f.d.execRev.Load()
	rev(`{"rev":` + itoa(start) + `}`)
	f.d.ExecJobsChanged()
	rev(`{"rev":` + itoa(start+1) + `}`)
	f.d.ExecJobsChanged()
	f.d.ExecJobsChanged()
	// A burst may coalesce, but the last revision arrives.
	last := start + 3
	for {
		select {
		case ev := <-events:
			all = append(all, ev)
			if ev.name == "execjobs" && ev.data == `{"rev":`+itoa(last)+`}` {
				goto done
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the burst's last revision %d never arrived: %+v", last, all)
		}
	}
done:
	for _, ev := range all {
		for _, secret := range []string{"npm run dev", "go test", "web/", "ready"} {
			if strings.Contains(ev.data, secret) {
				t.Fatalf("event %s leaks %q: %s", ev.name, secret, ev.data)
			}
		}
	}
	// The list reports the revision it was read at, so a page can tell
	// whether it is behind the last event.
	res := hudGet(f, "/s/livesession/exec/jobs")
	var list struct{ Rev int64 }
	if json.Unmarshal(res.Body.Bytes(), &list) != nil || list.Rev != last {
		t.Fatalf("list rev = %d, want %d (%s)", list.Rev, last, res.Body)
	}
	_ = svc
}

// TestExecJobsEventRidesTheWebSocketFraming: the WebSocket framing of the
// stream sends the same revision event (both framings share the sink).
func TestExecJobsEventRidesTheWebSocketFraming(t *testing.T) {
	f, _ := startHUDFixture(t)
	ws := openWS(t, f.base, "/s/livesession/events", f.base, true).events
	want := `{"rev":` + itoa(f.d.execRev.Load()) + `}`
	f.d.ExecJobsChanged()
	next := `{"rev":` + itoa(f.d.execRev.Load()) + `}`
	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for !seen[next] {
		select {
		case ev, ok := <-ws:
			if !ok {
				t.Fatal("stream closed")
			}
			if ev.name == "execjobs" {
				seen[ev.data] = true
			}
		case <-deadline:
			t.Fatalf("no execjobs event %s (connect revision %s); saw %v", next, want, seen)
		}
	}
}

// TestExecHUDOutputKeepsACharacterSplitAcrossInterleavedStreams: the euro
// sign's bytes arrive on stdout around a stderr write; each stream's own
// bytes join, so it decodes whole instead of as replacement characters.
func TestExecHUDOutputKeepsACharacterSplitAcrossInterleavedStreams(t *testing.T) {
	segs := []execjob.Segment{
		{Stream: execjob.StreamStdout, Data: []byte("a\xe2\x82")},
		{Stream: execjob.StreamStderr, Data: []byte("warn\n")},
		{Stream: execjob.StreamStdout, Data: []byte("\xacb")},
	}
	got := decodeExecSegments(segs)
	want := []execSegmentView{{Stream: "stdout", Text: "a"}, {Stream: "stderr", Text: "warn\n"}, {Stream: "stdout", Text: "€b"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("segments = %+v, want %+v", got, want)
	}
	// Incomplete at the window's end, or invalid, is replaced as before.
	tail := decodeExecSegments([]execjob.Segment{{Stream: execjob.StreamStdout, Data: []byte("x\xe2\x82")}, {Stream: execjob.StreamStderr, Data: []byte("\xff")}})
	if len(tail) != 3 || tail[0].Text != "x" || tail[1].Text != "�" || tail[2].Text != "��" {
		t.Fatalf("tail = %+v", tail)
	}
}
