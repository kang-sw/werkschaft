package wsdashboard

import (
	"bytes"
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
	"github.com/kang-sw/devenv/internal/wslive"
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

// execTreeFake extends execJobsFake with ExecTreeService for testing
// /s/<reply-id>/exec/bg/... routes across a 3-level hierarchy.
type execTreeFake struct {
	*execJobsFake
	trees map[string][]ExecTreeNode
}

func (f *execTreeFake) OpenTree(replyID string) error {
	_, err := f.Open(replyID)
	return err
}

func (f *execTreeFake) Subtree(replyID string) []ExecTreeNode {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ExecTreeNode(nil), f.trees[replyID]...)
}

func (f *execTreeFake) AgentJobsForOwners(owners map[string]bool) []execjob.HostedSummary {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []execjob.HostedSummary
	for owner := range owners {
		for _, j := range f.jobs[owner] {
			if j.Origin == execjob.OriginUser {
				continue
			}
			j.Owner = owner
			out = append(out, j)
		}
	}
	// Mirror execjob.Hosted.JobsForOwners order: running newest first, then finished newest first.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			a, b := out[i], out[j]
			swap := false
			at, bt := a.Terminal(), b.Terminal()
			switch {
			case at != bt:
				swap = at && !bt
			case !at:
				swap = b.StartedAt > a.StartedAt || (b.StartedAt == a.StartedAt && b.ID < a.ID)
			default:
				swap = b.EndedAt > a.EndedAt || (b.EndedAt == a.EndedAt && b.ID < a.ID)
			}
			if swap {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func (f *execTreeFake) ReadAgentOutput(owners map[string]bool, id string, from int64, limit int) (execjob.HostedOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.froms = append(f.froms, from)
	f.limits = append(f.limits, limit)
	for owner := range owners {
		for _, j := range f.jobs[owner] {
			if j.ID == id && j.Origin != execjob.OriginUser {
				out := f.out[id]
				out.Job.Owner = owner
				return out, nil
			}
		}
	}
	return execjob.HostedOutput{}, execjob.ErrExecNotFound
}

func (f *execTreeFake) StopAgentByUser(owners map[string]bool, id string) (execjob.HostedSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for owner := range owners {
		for _, j := range f.jobs[owner] {
			if j.ID == id && j.Origin != execjob.OriginUser {
				j.Owner = owner
				j.State = execjob.HostedStopped
				j.StoppedByUser = true
				j.StopNotice = execjob.StopNoticeSent
				return j, nil
			}
		}
	}
	return execjob.HostedSummary{}, execjob.ErrExecNotFound
}

// TestExecBgTreeRoutesHierarchyIsolationAndSeparation verifies:
//   - 3-level hierarchy (Lead -> Child -> Grandchild) recursive listing,
//     owner_depth (0/1/2), owner_path, running-first sorting, and strict
//     exclusion of origin=="user" jobs.
//   - Cross-session isolation: Lead A cannot list/tail/stop Lead B's jobs;
//     Child A1 cannot list/tail/stop Parent Lead A's jobs.
//   - Non-controllable viewer (remoteAddr != loopback) gets 403 on POST stop
//     and cannot read tree exec jobs.
func TestExecBgTreeRoutesHierarchyIsolationAndSeparation(t *testing.T) {
	f := startComposerFixture(t)
	// Register a child worker session in the dashboard index so /s/childa1/... resolves.
	childReg, err := ReadRegistration(f.dir, "livesession")
	if err != nil {
		t.Fatal(err)
	}
	childReg.ReplyID = "childa1"
	childReg.Hosted = true
	if err := WriteRegistration(f.dir, childReg); err != nil {
		t.Fatal(err)
	}
	f.d.Index().Refresh()
	waitFor(t, "childa1 registration", func() bool {
		s, ok := f.d.Index().Session("childa1")
		return ok && s.Hosted
	})

	zero := 0
	base := &execJobsFake{
		jobs: map[string][]execjob.HostedSummary{
			"livesession": {
				{ID: "exec_lead_run", Tool: "exec.shell", Command: "go test ./...", State: execjob.HostedRunning, Origin: execjob.OriginAgent, StartedAt: 100, Written: 20},
				{ID: "exec_lead_user", Tool: "exec.shell", Command: "! ls", State: execjob.HostedRunning, Origin: execjob.OriginUser, StartedAt: 300, Written: 5},
			},
			"childa1": {
				{ID: "exec_child_done", Tool: "exec.shell", Command: "npm run build", State: execjob.HostedExited, ExitCode: &zero, Origin: execjob.OriginAgent, StartedAt: 50, EndedAt: 90, Written: 10},
			},
			"granda2": {
				{ID: "exec_grand_run", Tool: "exec.shell", Command: "node verify.mjs", State: execjob.HostedRunning, Origin: execjob.OriginAgent, StartedAt: 200, Written: 30},
			},
			"boardsession": {
				{ID: "exec_peer_run", Tool: "exec.shell", Command: "secret-peer", State: execjob.HostedRunning, Origin: execjob.OriginAgent, StartedAt: 400, Written: 1},
			},
		},
		out: map[string]execjob.HostedOutput{
			"exec_lead_run":  {Job: execjob.HostedSummary{ID: "exec_lead_run", State: execjob.HostedRunning, Origin: execjob.OriginAgent}, Segments: []execjob.Segment{{Stream: execjob.StreamStdout, Data: []byte("lead-out")}}, Next: 8},
			"exec_grand_run": {Job: execjob.HostedSummary{ID: "exec_grand_run", State: execjob.HostedRunning, Origin: execjob.OriginAgent}, Segments: []execjob.Segment{{Stream: execjob.StreamStdout, Data: []byte("grand-out")}}, Next: 9},
			"exec_peer_run":  {Job: execjob.HostedSummary{ID: "exec_peer_run", State: execjob.HostedRunning, Origin: execjob.OriginAgent}, Segments: []execjob.Segment{{Stream: execjob.StreamStdout, Data: []byte("peer-out")}}, Next: 8},
		},
	}
	svc := &execTreeFake{
		execJobsFake: base,
		trees: map[string][]ExecTreeNode{
			"livesession": {
				{ID: "livesession", Label: "root-lead", Depth: 0},
				{ID: "childa1", Label: "quota-p1-p2", Depth: 1, Path: []string{"quota-p1-p2"}},
				{ID: "granda2", Label: "verify-chromium", Depth: 2, Path: []string{"quota-p1-p2", "verify-chromium"}},
			},
			"childa1": {
				{ID: "childa1", Label: "quota-p1-p2", Depth: 0},
				{ID: "granda2", Label: "verify-chromium", Depth: 1, Path: []string{"verify-chromium"}},
			},
			"boardsession": {
				{ID: "boardsession", Label: "peer-lead", Depth: 0},
			},
		},
	}
	f.d.SetExecJobs(svc)
	post := func(path string) *httptest.ResponseRecorder {
		return composerHTTP(f.d, http.MethodPost, path, "", "http://127.0.0.1", "127.0.0.1:1234", nil)
	}

	// 1. Lead A lists its 3-level subtree: 2 running (grand newest, then lead) + 1 finished (child); user job excluded.
	res := hudGet(f, "/s/livesession/exec/bg/jobs")
	if res.Code != http.StatusOK {
		t.Fatalf("GET bg/jobs status = %d: %s", res.Code, res.Body)
	}
	var list struct {
		Rev               int64         `json:"rev"`
		CanControl        bool          `json:"can_control"`
		SelfRunning       int           `json:"self_running"`
		DescendantRunning int           `json:"descendant_running"`
		TotalRunning      int           `json:"total_running"`
		TotalFinished     int           `json:"total_finished"`
		Jobs              []execJobView `json:"jobs"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode bg/jobs: %v (%s)", err, res.Body)
	}
	if !list.CanControl || list.SelfRunning != 1 || list.DescendantRunning != 1 || list.TotalRunning != 2 || list.TotalFinished != 1 {
		t.Fatalf("counts = %+v", list)
	}
	if len(list.Jobs) != 3 {
		t.Fatalf("jobs len = %d, want 3: %+v", len(list.Jobs), list.Jobs)
	}
	// Order: running newest first (grand StartedAt=200, lead StartedAt=100), then finished (child EndedAt=90).
	if list.Jobs[0].ID != "exec_grand_run" || list.Jobs[0].OwnerID != "granda2" || list.Jobs[0].OwnerDepth == nil || *list.Jobs[0].OwnerDepth != 2 || !reflect.DeepEqual(list.Jobs[0].OwnerPath, []string{"quota-p1-p2", "verify-chromium"}) {
		t.Fatalf("job[0] = %+v", list.Jobs[0])
	}
	if list.Jobs[1].ID != "exec_lead_run" || list.Jobs[1].OwnerID != "livesession" || list.Jobs[1].OwnerDepth == nil || *list.Jobs[1].OwnerDepth != 0 || len(list.Jobs[1].OwnerPath) != 0 {
		t.Fatalf("job[1] = %+v", list.Jobs[1])
	}
	if list.Jobs[2].ID != "exec_child_done" || list.Jobs[2].OwnerID != "childa1" || list.Jobs[2].OwnerDepth == nil || *list.Jobs[2].OwnerDepth != 1 || !reflect.DeepEqual(list.Jobs[2].OwnerPath, []string{"quota-p1-p2"}) {
		t.Fatalf("job[2] = %+v", list.Jobs[2])
	}

	// 2. Lead A can tail & stop grandchild's job, with owner metadata enriched.
	res = hudGet(f, "/s/livesession/exec/bg/exec_grand_run/output?from=0&limit=4096")
	if res.Code != http.StatusOK {
		t.Fatalf("GET grandchild output status = %d: %s", res.Code, res.Body)
	}
	var out execOutputView
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode output: %v", err)
	}
	if out.Job.OwnerID != "granda2" || out.Job.OwnerDepth == nil || *out.Job.OwnerDepth != 2 || len(out.Segments) != 1 || out.Segments[0].Text != "grand-out" {
		t.Fatalf("grandchild output = %+v", out)
	}
	if gotLimit := svc.limits[len(svc.limits)-1]; gotLimit != 4096 {
		t.Fatalf("limit forwarded = %d, want 4096", gotLimit)
	}

	res = post("/s/livesession/exec/bg/exec_grand_run/stop")
	if res.Code != http.StatusOK {
		t.Fatalf("POST stop grandchild status = %d: %s", res.Code, res.Body)
	}
	var stopped struct{ Job execJobView }
	if err := json.Unmarshal(res.Body.Bytes(), &stopped); err != nil || stopped.Job.State != execjob.HostedStopped || stopped.Job.OwnerID != "granda2" {
		t.Fatalf("stopped = %+v, err = %v", stopped, err)
	}

	// 3. Cross-session isolation: Lead A cannot tail/stop peer Lead B's job or its own user-origin job.
	for _, forbidden := range []string{"exec_peer_run", "exec_lead_user"} {
		if r := hudGet(f, "/s/livesession/exec/bg/"+forbidden+"/output"); r.Code != http.StatusNotFound {
			t.Fatalf("GET %s from lead A = %d, want 404", forbidden, r.Code)
		}
		if r := post("/s/livesession/exec/bg/" + forbidden + "/stop"); r.Code != http.StatusNotFound {
			t.Fatalf("POST stop %s from lead A = %d, want 404", forbidden, r.Code)
		}
	}

	// 4. Child A1 cannot tail/stop Parent Lead A's job, but can tail its own grandchild's job.
	if r := hudGet(f, "/s/childa1/exec/bg/exec_lead_run/output"); r.Code != http.StatusNotFound {
		t.Fatalf("Child A1 GET parent job = %d, want 404", r.Code)
	}
	if r := post("/s/childa1/exec/bg/exec_lead_run/stop"); r.Code != http.StatusNotFound {
		t.Fatalf("Child A1 POST stop parent job = %d, want 404", r.Code)
	}
	if r := hudGet(f, "/s/childa1/exec/bg/exec_grand_run/output"); r.Code != http.StatusOK {
		t.Fatalf("Child A1 GET grandchild job = %d, want 200", r.Code)
	}

	// 5. Non-controllable viewers: LAN peer gets 401; missing/foreign Origin on POST stop gets 403.
	if r := composerHTTP(f.d, http.MethodPost, "/s/livesession/exec/bg/exec_lead_run/stop", "", "http://127.0.0.1", "192.0.2.9:4321", nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("LAN POST stop = %d, want 401", r.Code)
	}
	if r := composerHTTP(f.d, http.MethodGet, "/s/livesession/exec/bg/jobs", "", "", "192.0.2.9:4321", nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("LAN GET bg/jobs = %d, want 401", r.Code)
	}
	if r := composerHTTP(f.d, http.MethodPost, "/s/livesession/exec/bg/exec_lead_run/stop", "", "http://evil.test", "127.0.0.1:1234", nil); r.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST stop = %d, want 403", r.Code)
	}
}

// TestBgExecLiveMarkupOnConvLive verifies that Host.BgExecCount/Self/Desc
// render as additive data-bg-exec* attributes on r-convlive when non-zero
// and are omitted when zero.
func TestBgExecLiveMarkupOnConvLive(t *testing.T) {
	now := time.Now()
	v := buildSessionView(pageInputs{
		Session: Session{Registration: Registration{ReplyID: "lead1", Hosted: true}},
		Now:     now,
		Snapshot: &wslive.Snapshot{
			Adapter:   "claude",
			UpdatedAt: now.UnixMilli(),
			Host:      &wslive.Host{BgExecCount: 3, BgExecSelf: 1, BgExecDesc: 2},
		},
	})
	if v.BgExecCount != 3 || v.BgExecSelf != 1 || v.BgExecDesc != 2 {
		t.Fatalf("SessionView bg exec = %d/%d/%d, want 3/1/2", v.BgExecCount, v.BgExecSelf, v.BgExecDesc)
	}
	vZero := buildSessionView(pageInputs{
		Session: Session{Registration: Registration{ReplyID: "lead1", Hosted: true}},
		Now:     now,
		Snapshot: &wslive.Snapshot{
			Adapter:   "claude",
			UpdatedAt: now.UnixMilli(),
		},
	})
	if vZero.BgExecCount != 0 || vZero.BgExecSelf != 0 || vZero.BgExecDesc != 0 {
		t.Fatalf("SessionView zero bg exec = %d/%d/%d, want 0/0/0", vZero.BgExecCount, vZero.BgExecSelf, vZero.BgExecDesc)
	}
	assets, err := loadDashboardAssets()
	if err != nil {
		t.Fatalf("loadDashboardAssets: %v", err)
	}
	var nonZeroBuf bytes.Buffer
	if err := assets.tmpl.ExecuteTemplate(&nonZeroBuf, "r-convlive", v); err != nil {
		t.Fatalf("render r-convlive non-zero: %v", err)
	}
	nonZeroHTML := nonZeroBuf.String()
	for _, want := range []string{`data-bg-exec="3"`, `data-bg-exec-self="1"`, `data-bg-exec-desc="2"`} {
		if !strings.Contains(nonZeroHTML, want) {
			t.Fatalf("r-convlive non-zero HTML missing %s: %s", want, nonZeroHTML)
		}
	}
	var zeroBuf bytes.Buffer
	if err := assets.tmpl.ExecuteTemplate(&zeroBuf, "r-convlive", vZero); err != nil {
		t.Fatalf("render r-convlive zero: %v", err)
	}
	zeroHTML := zeroBuf.String()
	for _, forbidden := range []string{"data-bg-exec=", "data-bg-exec-self=", "data-bg-exec-desc="} {
		if strings.Contains(zeroHTML, forbidden) {
			t.Fatalf("r-convlive zero HTML unexpectedly contains %s: %s", forbidden, zeroHTML)
		}
	}
}
