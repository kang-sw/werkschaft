package wsdashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// hudBrowserJob is one job of the browser fixture: the HUD's list fields plus
// the output the fake serves for it.
type hudBrowserJob struct {
	ID            string `json:"id"`
	Tool          string `json:"tool"`
	Command       string `json:"command"`
	Cwd           string `json:"cwd"`
	State         string `json:"state"`
	Origin        string `json:"origin"`
	ExitCode      *int   `json:"exit_code"`
	Signal        *int   `json:"signal"`
	StartedAt     int64  `json:"started_at"`
	EndedAt       int64  `json:"ended_at"`
	StoppedByUser bool   `json:"stopped_by_user"`
	StopNotice    string `json:"stop_notice"`
	Async         bool   `json:"async"`
	Evicted       int64  `json:"evicted"`
	segs          []execjob.Segment
	written       int64
}

// hudBrowserSvc is an ExecJobsService the browser test drives through a small
// control server: it sets the job list, appends output, bumps the revision and
// chooses how a stop's notice lands. It counts every call so the test can
// assert what the page asked for.
type hudBrowserSvc struct {
	mu        sync.Mutex
	jobs      []*hudBrowserJob
	notice    string // outcome of an agent job's stop: sent, failed-once, closed, pending
	failedOne map[string]bool
	stopMs    int
	counts    map[string]int
	d         *Daemon
	lastFrom  map[string]int64
	// fromLog lists every offset an output read asked for, per job; outDelayMs
	// holds an answer back after its snapshot is taken (a slow response), and
	// inflight/maxInflight count the reads under way per job.
	fromLog     map[string][]int64
	outDelayMs  int
	inflight    map[string]int
	maxInflight map[string]int
	// stopGate holds every stop's answer back after its snapshot is taken, and
	// listGate does the same for the job list (answers that arrive late, after
	// newer ones), until the gate is released (25 s at most); held counts the
	// answers waiting at a gate, their snapshots already taken. outGone makes an
	// output read answer 404 for the listed jobs, and outHang makes it never answer
	// (until released or 25 s).
	// stopBlock holds a stop before it takes effect (the job is still running while
	// it waits). quit is closed when the test ends, and releases every wait at once.
	// manual: no automatic revision is published (a stop's end, /patch); the script
	// publishes each one with /bump, which answers with the revision it made.
	manual    bool
	stopGate  chan struct{}
	stopBlock chan struct{}
	listGate  chan struct{}
	quit      chan struct{}
	held      map[string]int
	outGone   map[string]bool
	outHang   chan struct{}
}

// waitGate waits at a gate: the snapshot is taken, the answer is held.
func (s *hudBrowserSvc) waitGate(kind string, gate chan struct{}) {
	s.mu.Lock()
	s.held[kind]++
	s.mu.Unlock()
	select {
	case <-gate:
	case <-s.quit:
	case <-time.After(25 * time.Second):
	}
	s.mu.Lock()
	s.held[kind]--
	s.mu.Unlock()
}

func (s *hudBrowserSvc) Open(string) (uint64, error) { return 4, nil }

func (s *hudBrowserSvc) summary(j *hudBrowserJob) execjob.HostedSummary {
	return execjob.HostedSummary{ID: j.ID, Tool: j.Tool, Command: j.Command, Cwd: j.Cwd, State: j.State, Origin: j.Origin, ExitCode: j.ExitCode, Signal: j.Signal,
		StartedAt: j.StartedAt, EndedAt: j.EndedAt, Written: j.written, StoppedByUser: j.StoppedByUser, StopNotice: j.StopNotice, Async: j.Async}
}

func (s *hudBrowserSvc) Jobs(owner string) []execjob.HostedSummary {
	s.mu.Lock()
	s.counts["list"]++
	var out []execjob.HostedSummary
	for _, j := range s.jobs {
		out = append(out, s.summary(j))
	}
	gate := s.listGate
	s.mu.Unlock()
	// The snapshot is taken: a held answer reaches the page after the job moved on.
	if gate != nil {
		s.waitGate("list", gate)
	}
	return out
}

func (s *hudBrowserSvc) find(id string) *hudBrowserJob {
	for _, j := range s.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

func (s *hudBrowserSvc) ReadOutput(owner, id string, from int64, limit int) (execjob.HostedOutput, error) {
	s.mu.Lock()
	s.inflight[id]++
	if s.inflight[id] > s.maxInflight[id] {
		s.maxInflight[id] = s.inflight[id]
	}
	out, err := s.readLocked(id, from, limit)
	if s.outGone[id] {
		out, err = execjob.HostedOutput{}, execjob.ErrExecNotFound
	}
	delay, hang := s.outDelayMs, s.outHang
	s.mu.Unlock()
	if hang != nil {
		select {
		case <-hang:
		case <-s.quit:
		case <-time.After(25 * time.Second):
		}
	}
	// The snapshot is taken: a slow answer reaches the page after the job moved on.
	if delay > 0 {
		time.Sleep(time.Duration(delay) * time.Millisecond)
	}
	s.mu.Lock()
	s.inflight[id]--
	s.mu.Unlock()
	return out, err
}

func (s *hudBrowserSvc) readLocked(id string, from int64, limit int) (execjob.HostedOutput, error) {
	s.counts["output"]++
	s.counts["output:"+id]++
	j := s.find(id)
	if j == nil {
		return execjob.HostedOutput{}, execjob.ErrExecNotFound
	}
	if s.lastFrom == nil {
		s.lastFrom = map[string]int64{}
	}
	s.lastFrom[id] = from
	s.fromLog[id] = append(s.fromLog[id], from)
	out := execjob.HostedOutput{Job: s.summary(j), Written: j.written, Evicted: j.Evicted}
	if from < j.Evicted {
		out.Gap = true
		from = j.Evicted
	}
	if from > j.written {
		from = j.written
	}
	out.From, out.Next = from, from
	var pos int64
	for _, seg := range j.segs {
		end := pos + int64(len(seg.Data))
		if end > from && out.Next-from < int64(limit) {
			lo := int64(0)
			if from > pos {
				lo = from - pos
			}
			data := seg.Data[lo:]
			if room := int64(limit) - (out.Next - from); int64(len(data)) > room {
				data = data[:room]
			}
			out.Segments = append(out.Segments, execjob.Segment{Stream: seg.Stream, Data: append([]byte(nil), data...)})
			out.Next += int64(len(data))
		}
		pos = end
	}
	return out, nil
}

func (s *hudBrowserSvc) StopByUser(owner, id string) (execjob.HostedSummary, error) {
	s.mu.Lock()
	s.counts["stop"]++
	s.counts["stop:"+id]++
	ms, block, manual := s.stopMs, s.stopBlock, s.manual
	s.mu.Unlock()
	if block != nil {
		s.waitGate("stopblock", block)
	}
	if ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	j := s.find(id)
	if j == nil {
		return execjob.HostedSummary{}, execjob.ErrExecNotFound
	}
	if j.State == execjob.HostedRunning {
		j.State, j.StoppedByUser, j.EndedAt = execjob.HostedStopped, true, time.Now().UnixMilli()
	}
	// Only a job this stop ended is marked and owes its owner a notice.
	if j.Origin != execjob.OriginUser && j.StoppedByUser {
		switch s.notice {
		case "failed-once":
			if !s.failedOne[id] {
				s.failedOne[id] = true
				j.StopNotice = execjob.StopNoticeFailed
			} else {
				j.StopNotice = execjob.StopNoticeSent
			}
		case "closed":
			j.StopNotice = execjob.StopNoticeClosed
		case "pending":
			j.StopNotice = execjob.StopNoticePending
		default:
			j.StopNotice = execjob.StopNoticeSent
		}
	}
	sum := s.summary(j)
	d, gate := s.d, s.stopGate
	if gate != nil {
		s.mu.Unlock()
		s.waitGate("stop", gate)
		s.mu.Lock()
	}
	if !manual {
		go d.ExecJobsChanged()
	}
	return sum, nil
}

// TestExecHUDBrowser drives the exec job HUD in Chromium against the real
// session page, the real routes and the real execjobs stream event (only the
// job service is a fake): the strip's folding and its priority layout, the
// tooltip, the peek, the stop flow, the pin and fade timers, escaping, the
// absence of a HUD against a daemon without the route, keyboard, reduced
// motion, and the performance measurements, in ko and en, light and dark.
func TestExecHUDBrowser(t *testing.T) {
	runExecHUDBrowser(t, "testdata/exec-hud.browser.cjs", "WS_EXEC_HUD_SCRIPT")
}

// TestExecHUDReviewBrowser drives the review-fix scenarios against the same
// fixture: the keyboard, the drawer's Escape, serialized and stale-guarded
// output reads, long lines, the folded strip's bounds and dismissal, the toast
// and the copy label.
func TestExecHUDReviewBrowser(t *testing.T) {
	runExecHUDBrowser(t, "testdata/exec-hud-review.browser.cjs", "WS_EXEC_HUD_REVIEW_SCRIPT")
}

func runExecHUDBrowser(t *testing.T, script, scriptEnv string) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	shots := os.Getenv("WS_EXEC_HUD_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_EXEC_HUD_SHOTS must be absolute")
	}
	mockup := ""
	if shots != "" {
		abs, err := filepath.Abs(filepath.Join("..", "..", "..", "ai-docs", "assets", "mockups", "261122", "exec-job-hud", "index.html"))
		if err != nil {
			t.Fatal(err)
		}
		mockup = abs
	}
	f := startComposerFixture(t)
	// The composer's `!echo hi` runs as the job the HUD lists for it, so the
	// peek's "Jump to card" finds the card by the job's id.
	zero := 0
	f.svc.mu.Lock()
	f.svc.launch = func(req ComposerLaunch) (ComposerJob, error) {
		return ComposerJob{ExecID: "exec_hi", Gen: 4, State: "exited", ExitCode: &zero, Command: req.Line, Written: 3, Output: []byte("hi\n")}, nil
	}
	f.svc.mu.Unlock()
	svc := &hudBrowserSvc{counts: map[string]int{}, failedOne: map[string]bool{}, notice: "sent", d: f.d, fromLog: map[string][]int64{}, inflight: map[string]int{}, maxInflight: map[string]int{}, outGone: map[string]bool{}, held: map[string]int{}, quit: make(chan struct{})}
	t.Cleanup(func() { close(svc.quit) })
	f.d.SetExecJobs(svc)
	ctl := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(v)
	}
	ctl.HandleFunc("/jobs", func(w http.ResponseWriter, r *http.Request) {
		var in []*hudBrowserJob
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		svc.mu.Lock()
		svc.jobs = in
		svc.failedOne = map[string]bool{}
		svc.mu.Unlock()
		reply(w, "ok")
	})
	ctl.HandleFunc("/out", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Stream, Text string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		svc.mu.Lock()
		if j := svc.find(in.ID); j != nil {
			st := execjob.StreamStdout
			if in.Stream == "stderr" {
				st = execjob.StreamStderr
			}
			j.segs = append(j.segs, execjob.Segment{Stream: st, Data: []byte(in.Text)})
			j.written += int64(len(in.Text))
		}
		svc.mu.Unlock()
		reply(w, "ok")
	})
	ctl.HandleFunc("/set", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Notice     string   `json:"notice"`
			StopMs     int      `json:"stopMs"`
			OutDelayMs int      `json:"outDelayMs"`
			StopHold   bool     `json:"stopHold"`
			StopBlock  bool     `json:"stopBlock"`
			ListHold   bool     `json:"listHold"`
			Gone       []string `json:"outGone"`
			Hang       bool     `json:"outHang"`
			Unbind     bool     `json:"unbind"`
			Bind       bool     `json:"bind"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		svc.mu.Lock()
		if in.Notice != "" {
			svc.notice = in.Notice
		}
		svc.stopMs = in.StopMs
		svc.outDelayMs = in.OutDelayMs
		// A hold stays until a /set without it: that releases every answer held.
		if in.StopHold && svc.stopGate == nil {
			svc.stopGate = make(chan struct{})
		} else if !in.StopHold && svc.stopGate != nil {
			close(svc.stopGate)
			svc.stopGate = nil
		}
		if in.StopBlock && svc.stopBlock == nil {
			svc.stopBlock = make(chan struct{})
		} else if !in.StopBlock && svc.stopBlock != nil {
			close(svc.stopBlock)
			svc.stopBlock = nil
		}
		if in.ListHold && svc.listGate == nil {
			svc.listGate = make(chan struct{})
		} else if !in.ListHold && svc.listGate != nil {
			close(svc.listGate)
			svc.listGate = nil
		}
		svc.outGone = map[string]bool{}
		for _, id := range in.Gone {
			svc.outGone[id] = true
		}
		if in.Hang && svc.outHang == nil {
			svc.outHang = make(chan struct{})
		} else if !in.Hang && svc.outHang != nil {
			close(svc.outHang)
			svc.outHang = nil
		}
		svc.mu.Unlock()
		if in.Unbind {
			f.d.SetExecJobs(nil)
		}
		if in.Bind {
			f.d.SetExecJobs(svc)
		}
		reply(w, "ok")
	})
	// /patch changes one job in place (its output stays) and bumps the revision.
	ctl.HandleFunc("/patch", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ID         string `json:"id"`
			State      string `json:"state"`
			StopNotice string `json:"stop_notice"`
			ExitCode   *int   `json:"exit_code"`
			EndedAt    int64  `json:"ended_at"`
			Stopped    bool   `json:"stopped_by_user"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		svc.mu.Lock()
		j := svc.find(in.ID)
		if j != nil {
			if in.State != "" {
				j.State = in.State
			}
			if in.State != "" && in.State != execjob.HostedRunning && in.EndedAt == 0 {
				j.EndedAt = time.Now().UnixMilli()
			}
			if in.EndedAt != 0 {
				j.EndedAt = in.EndedAt
			}
			j.StopNotice = in.StopNotice
			j.ExitCode = in.ExitCode
			j.StoppedByUser = in.Stopped
		}
		manual := svc.manual
		svc.mu.Unlock()
		if !manual {
			f.d.ExecJobsChanged()
		}
		reply(w, "ok")
	})
	ctl.HandleFunc("/bump", func(w http.ResponseWriter, r *http.Request) {
		f.d.ExecJobsChanged()
		reply(w, f.d.execRev.Load())
	})
	// /manual switches the controlled-wake mode on or off (see manual).
	ctl.HandleFunc("/manual", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			On bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		svc.mu.Lock()
		svc.manual = in.On
		svc.mu.Unlock()
		reply(w, "ok")
	})
	ctl.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		reply(w, map[string]any{"counts": svc.counts, "from": svc.lastFrom, "fromLog": svc.fromLog, "maxInflight": svc.maxInflight, "inflight": svc.inflight, "held": svc.held})
	})
	ctl.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		svc.counts = map[string]int{}
		svc.fromLog = map[string][]int64{}
		svc.maxInflight = map[string]int{}
		svc.mu.Unlock()
		reply(w, "ok")
	})
	ctlSrv := httptest.NewServer(ctl)
	defer ctlSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	// The run outlasts the fixture session's registration heartbeat: keep it
	// alive, as a running ws-mcp does.
	beat := time.NewTicker(5 * time.Second)
	defer beat.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-beat.C:
				if reg, err := ReadRegistration(f.dir, "livesession"); err == nil {
					reg.HeartbeatAt = stampFor(time.Now())
					_ = WriteRegistration(f.dir, reg)
				}
				snap := &wslive.Snapshot{SchemaVersion: 1, ReplyID: "livesession", Adapter: "pi", UpdatedAt: time.Now().UnixMilli(), HeartbeatMs: 30000,
					Lead: wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseReplying}, Reply: &wslive.Reply{Text: "streamed <partial> reply"}}}
				if raw, err := json.Marshal(snap); err == nil {
					path := wslive.SnapshotPath(SessionDir(f.dir, "livesession"))
					if os.WriteFile(path+".tmp", raw, 0o644) == nil {
						_ = wsstate.AtomicReplaceFile(path+".tmp", path)
					}
				}
			}
		}
	}()
	if d := os.Getenv(scriptEnv); d != "" {
		script = d
	}
	out, err := exec.CommandContext(ctx, "node", script, f.base, ctlSrv.URL, shots, mockup).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium exec HUD acceptance: %v", err)
	}
}
