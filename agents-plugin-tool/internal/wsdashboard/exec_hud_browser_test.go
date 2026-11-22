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
}

func (s *hudBrowserSvc) Open(string) (uint64, error) { return 4, nil }

func (s *hudBrowserSvc) summary(j *hudBrowserJob) execjob.HostedSummary {
	return execjob.HostedSummary{ID: j.ID, Tool: j.Tool, Command: j.Command, Cwd: j.Cwd, State: j.State, Origin: j.Origin, ExitCode: j.ExitCode, Signal: j.Signal,
		StartedAt: j.StartedAt, EndedAt: j.EndedAt, Written: j.written, StoppedByUser: j.StoppedByUser, StopNotice: j.StopNotice}
}

func (s *hudBrowserSvc) Jobs(owner string) []execjob.HostedSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts["list"]++
	var out []execjob.HostedSummary
	for _, j := range s.jobs {
		out = append(out, s.summary(j))
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
	defer s.mu.Unlock()
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
	ms := s.stopMs
	s.mu.Unlock()
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
	if j.Origin != execjob.OriginUser {
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
	d := s.d
	go d.ExecJobsChanged()
	return sum, nil
}

// TestExecHUDBrowser drives the exec job HUD in Chromium against the real
// session page, the real routes and the real execjobs stream event (only the
// job service is a fake): the strip's folding and its priority layout, the
// tooltip, the peek, the stop flow, the pin and fade timers, escaping, the
// absence of a HUD against a daemon without the route, keyboard, reduced
// motion, and the performance measurements, in ko and en, light and dark.
func TestExecHUDBrowser(t *testing.T) {
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
	svc := &hudBrowserSvc{counts: map[string]int{}, failedOne: map[string]bool{}, notice: "sent", d: f.d}
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
			Notice string `json:"notice"`
			StopMs int    `json:"stopMs"`
			Unbind bool   `json:"unbind"`
			Bind   bool   `json:"bind"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		svc.mu.Lock()
		if in.Notice != "" {
			svc.notice = in.Notice
		}
		svc.stopMs = in.StopMs
		svc.mu.Unlock()
		if in.Unbind {
			f.d.SetExecJobs(nil)
		}
		if in.Bind {
			f.d.SetExecJobs(svc)
		}
		reply(w, "ok")
	})
	ctl.HandleFunc("/bump", func(w http.ResponseWriter, r *http.Request) { f.d.ExecJobsChanged(); reply(w, "ok") })
	ctl.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		reply(w, map[string]any{"counts": svc.counts, "from": svc.lastFrom})
	})
	ctl.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		svc.counts = map[string]int{}
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
	script := "testdata/exec-hud.browser.cjs"
	if d := os.Getenv("WS_EXEC_HUD_SCRIPT"); d != "" {
		script = d
	}
	out, err := exec.CommandContext(ctx, "node", script, f.base, ctlSrv.URL, shots, mockup).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium exec HUD acceptance: %v", err)
	}
}
