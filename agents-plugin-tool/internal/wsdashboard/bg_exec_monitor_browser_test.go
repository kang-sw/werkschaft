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

type bgExecBrowserJob struct {
	Owner         string `json:"owner"`
	ID            string `json:"id"`
	Tool          string `json:"tool"`
	Command       string `json:"command"`
	Cwd           string `json:"cwd"`
	State         string `json:"state"`
	Origin        string `json:"origin"`
	ExitCode      *int   `json:"exit_code"`
	StartedAt     int64  `json:"started_at"`
	EndedAt       int64  `json:"ended_at"`
	StoppedByUser bool   `json:"stopped_by_user"`
	StopNotice    string `json:"stop_notice"`
	segs          []execjob.Segment
	written       int64
}

type bgExecBrowserSvc struct {
	mu     sync.Mutex
	d      *Daemon
	jobs   []*bgExecBrowserJob
	trees  map[string][]ExecTreeNode
	counts map[string]int
}

func (s *bgExecBrowserSvc) Open(string) (uint64, error) { return 4, nil }
func (s *bgExecBrowserSvc) OpenTree(string) error       { return nil }

func (s *bgExecBrowserSvc) Subtree(replyID string) []ExecTreeNode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ExecTreeNode(nil), s.trees[replyID]...)
}

func (s *bgExecBrowserSvc) summary(j *bgExecBrowserJob) execjob.HostedSummary {
	return execjob.HostedSummary{
		ID:            j.ID,
		Tool:          j.Tool,
		Command:       j.Command,
		Cwd:           j.Cwd,
		State:         j.State,
		Origin:        j.Origin,
		Owner:         j.Owner,
		ExitCode:      j.ExitCode,
		StartedAt:     j.StartedAt,
		EndedAt:       j.EndedAt,
		Written:       j.written,
		StoppedByUser: j.StoppedByUser,
		StopNotice:    j.StopNotice,
	}
}

func (s *bgExecBrowserSvc) Jobs(owner string) []execjob.HostedSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts["hudList"]++
	var out []execjob.HostedSummary
	for _, j := range s.jobs {
		if j.Owner == owner {
			out = append(out, s.summary(j))
		}
	}
	return out
}

func (s *bgExecBrowserSvc) ReadOutput(owner, id string, from int64, limit int) (execjob.HostedOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts["hudOutput"]++
	return s.readLocked(map[string]bool{owner: true}, id, from, limit, false)
}

func (s *bgExecBrowserSvc) StopByUser(owner, id string) (execjob.HostedSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts["hudStop"]++
	return s.stopLocked(map[string]bool{owner: true}, id, false)
}

func (s *bgExecBrowserSvc) AgentJobsForOwners(owners map[string]bool) []execjob.HostedSummary {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts["bgList"]++
	var out []execjob.HostedSummary
	for _, j := range s.jobs {
		if !owners[j.Owner] || j.Origin == execjob.OriginUser {
			continue
		}
		out = append(out, s.summary(j))
	}
	for i := 0; i < len(out); i++ {
		for k := i + 1; k < len(out); k++ {
			a, b := out[i], out[k]
			at, bt := a.Terminal(), b.Terminal()
			swap := false
			switch {
			case at != bt:
				swap = at && !bt
			case !at:
				swap = b.StartedAt > a.StartedAt || (b.StartedAt == a.StartedAt && b.ID < a.ID)
			default:
				swap = b.EndedAt > a.EndedAt || (b.EndedAt == a.EndedAt && b.ID < a.ID)
			}
			if swap {
				out[i], out[k] = out[k], out[i]
			}
		}
	}
	return out
}

func (s *bgExecBrowserSvc) ReadAgentOutput(owners map[string]bool, id string, from int64, limit int) (execjob.HostedOutput, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.counts["bgOutput"]++
	s.counts["bgOutput:"+id]++
	return s.readLocked(owners, id, from, limit, true)
}

func (s *bgExecBrowserSvc) StopAgentByUser(owners map[string]bool, id string) (execjob.HostedSummary, error) {
	s.mu.Lock()
	s.counts["bgStop"]++
	s.counts["bgStop:"+id]++
	sum, err := s.stopLocked(owners, id, true)
	d := s.d
	s.mu.Unlock()
	if err == nil && d != nil {
		go d.ExecJobsChanged()
	}
	return sum, err
}

func (s *bgExecBrowserSvc) readLocked(owners map[string]bool, id string, from int64, limit int, agentOnly bool) (execjob.HostedOutput, error) {
	var j *bgExecBrowserJob
	for _, cand := range s.jobs {
		if cand.ID == id && owners[cand.Owner] && (!agentOnly || cand.Origin != execjob.OriginUser) {
			j = cand
			break
		}
	}
	if j == nil {
		return execjob.HostedOutput{}, execjob.ErrExecNotFound
	}
	if from > j.written {
		from = j.written
	}
	out := execjob.HostedOutput{Job: s.summary(j), From: from, Next: from, Written: j.written}
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

func (s *bgExecBrowserSvc) stopLocked(owners map[string]bool, id string, agentOnly bool) (execjob.HostedSummary, error) {
	for _, j := range s.jobs {
		if j.ID == id && owners[j.Owner] && (!agentOnly || j.Origin != execjob.OriginUser) {
			if j.State == execjob.HostedRunning {
				j.State = execjob.HostedStopped
				j.StoppedByUser = true
				j.EndedAt = time.Now().UnixMilli()
				j.StopNotice = execjob.StopNoticeSent
			}
			return s.summary(j), nil
		}
	}
	return execjob.HostedSummary{}, execjob.ErrExecNotFound
}

// TestBgExecMonitorBrowser verifies the interactive #bgbadge + C-variant
// background exec monitor popover in Chromium: strict separation from the user
// `!` exec HUD, zero polling while closed, 3-level hierarchy breadcrumbs,
// docked live tail with stdout/stderr and XSS escaping, 1.5s WsHoldConfirm
// stop, read-only can_control=false hiding, narrow 480px layout, and dark/light
// theme parity.
func TestBgExecMonitorBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	shots := os.Getenv("WS_BG_EXEC_SHOTS")
	if shots != "" && !filepath.IsAbs(shots) {
		t.Fatal("WS_BG_EXEC_SHOTS must be absolute")
	}
	f := startComposerFixture(t)
	now := time.Now().UnixMilli()
	zero := 0

	leadJob := &bgExecBrowserJob{
		Owner: "livesession", ID: "exec_lead_run", Tool: "exec.shell",
		Command: "go test ./internal/wsdashboard -run TestBgExec -v", Cwd: "agents-plugin-tool",
		State: execjob.HostedRunning, Origin: execjob.OriginAgent, StartedAt: now - 35000,
		segs: []execjob.Segment{
			{Stream: execjob.StreamStdout, Data: []byte("=== RUN   TestBgExecMonitor\n")},
			{Stream: execjob.StreamStdout, Data: []byte("connecting session stream...\n")},
		},
	}
	leadJob.written = int64(len(leadJob.segs[0].Data) + len(leadJob.segs[1].Data))

	l1Job := &bgExecBrowserJob{
		Owner: "childa1", ID: "exec_l1_run", Tool: "exec.shell",
		Command: "pnpm --filter web build --watch", Cwd: "apps/web",
		State: execjob.HostedRunning, Origin: execjob.OriginAgent, StartedAt: now - 18000,
		segs: []execjob.Segment{
			{Stream: execjob.StreamStdout, Data: []byte("[vite] building for development...\n")},
			{Stream: execjob.StreamStderr, Data: []byte("warn: chunk size exceeds 500 kB\n")},
		},
	}
	l1Job.written = int64(len(l1Job.segs[0].Data) + len(l1Job.segs[1].Data))

	l2Job := &bgExecBrowserJob{
		Owner: "granda2", ID: "exec_l2_run", Tool: "exec.shell",
		Command: "node scripts/verify-layout.mjs --strict", Cwd: "e2e",
		State: execjob.HostedRunning, Origin: execjob.OriginAgent, StartedAt: now - 6000,
		segs: []execjob.Segment{
			{Stream: execjob.StreamStdout, Data: []byte("checking viewport 1180x820 <script>alert(1)</script>\n")},
			{Stream: execjob.StreamStderr, Data: []byte("stderr: retrying font metrics check\n")},
			{Stream: execjob.StreamStdout, Data: []byte("ok: 0 horizontal overflow detected\n")},
		},
	}
	l2Job.written = int64(len(l2Job.segs[0].Data) + len(l2Job.segs[1].Data) + len(l2Job.segs[2].Data))

	l1Done := &bgExecBrowserJob{
		Owner: "childa1", ID: "exec_l1_done", Tool: "exec.shell",
		Command: "golangci-lint run ./...", Cwd: "agents-plugin-tool",
		State: execjob.HostedExited, ExitCode: &zero, Origin: execjob.OriginAgent,
		StartedAt: now - 120000, EndedAt: now - 45000,
		segs: []execjob.Segment{
			{Stream: execjob.StreamStdout, Data: []byte("0 issues.\n")},
		},
	}
	l1Done.written = int64(len(l1Done.segs[0].Data))

	userJob := &bgExecBrowserJob{
		Owner: "livesession", ID: "exec_user_cmd", Tool: "exec.shell",
		Command: "npm run dev", Cwd: "web",
		State: execjob.HostedRunning, Origin: execjob.OriginUser, StartedAt: now - 12000,
		segs: []execjob.Segment{
			{Stream: execjob.StreamStdout, Data: []byte("ready on http://localhost:5173\n")},
		},
	}
	userJob.written = int64(len(userJob.segs[0].Data))

	svc := &bgExecBrowserSvc{
		d:      f.d,
		counts: map[string]int{},
		jobs:   []*bgExecBrowserJob{leadJob, l1Job, l2Job, l1Done, userJob},
		trees: map[string][]ExecTreeNode{
			"livesession": {
				{ID: "livesession", Label: "root-lead", Depth: 0},
				{ID: "childa1", Label: "worker-alpha", Depth: 1, Path: []string{"worker-alpha"}},
				{ID: "granda2", Label: "verify-e2e", Depth: 2, Path: []string{"worker-alpha", "verify-e2e"}},
			},
		},
	}
	f.d.SetExecJobs(svc)

	writeSnap := func() {
		svc.mu.Lock()
		selfRun, descRun := 0, 0
		for _, j := range svc.jobs {
			if j.Origin == execjob.OriginUser || j.State != execjob.HostedRunning {
				continue
			}
			if j.Owner == "livesession" {
				selfRun++
			} else {
				descRun++
			}
		}
		svc.mu.Unlock()
		snap := &wslive.Snapshot{
			SchemaVersion: 1,
			ReplyID:       "livesession",
			Adapter:       "pi",
			UpdatedAt:     time.Now().UnixMilli(),
			HeartbeatMs:   30000,
			Lead: wslive.Lead{
				Activity: wslive.Activity{Phase: wslive.PhaseReplying},
				Reply:    &wslive.Reply{Text: "Running background checks across worker tree."},
			},
			Host: &wslive.Host{
				BgExecCount: selfRun + descRun,
				BgExecSelf:  selfRun,
				BgExecDesc:  descRun,
			},
		}
		if raw, err := json.Marshal(snap); err == nil {
			path := wslive.SnapshotPath(SessionDir(f.dir, "livesession"))
			if os.WriteFile(path+".tmp", raw, 0o644) == nil {
				_ = wsstate.AtomicReplaceFile(path+".tmp", path)
			}
		}
	}
	writeSnap()

	ctl := http.NewServeMux()
	reply := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_ = json.NewEncoder(w).Encode(v)
	}
	ctl.HandleFunc("/stats", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		cp := map[string]int{}
		for k, v := range svc.counts {
			cp[k] = v
		}
		svc.mu.Unlock()
		reply(w, map[string]any{"counts": cp})
	})
	ctl.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		svc.counts = map[string]int{}
		svc.mu.Unlock()
		reply(w, "ok")
	})
	ctl.HandleFunc("/append", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ ID, Stream, Text string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		svc.mu.Lock()
		for _, j := range svc.jobs {
			if j.ID == in.ID {
				st := execjob.StreamStdout
				if in.Stream == "stderr" {
					st = execjob.StreamStderr
				}
				j.segs = append(j.segs, execjob.Segment{Stream: st, Data: []byte(in.Text)})
				j.written += int64(len(in.Text))
			}
		}
		svc.mu.Unlock()
		reply(w, "ok")
	})
	ctl.HandleFunc("/peer-job", func(w http.ResponseWriter, r *http.Request) {
		svc.mu.Lock()
		svc.jobs = append(svc.jobs, &bgExecBrowserJob{
			Owner: "otherlead", ID: "exec_peer", Tool: "exec.shell",
			State: execjob.HostedRunning, Origin: execjob.OriginAgent,
		})
		svc.mu.Unlock()
		f.d.ExecJobsChanged()
		reply(w, "ok")
	})
	ctl.HandleFunc("/syncsnap", func(w http.ResponseWriter, r *http.Request) {
		writeSnap()
		f.d.ExecJobsChanged()
		reply(w, "ok")
	})
	ctlSrv := httptest.NewServer(ctl)
	defer ctlSrv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	beat := time.NewTicker(3 * time.Second)
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
				writeSnap()
			}
		}
	}()

	script := "testdata/bg-exec-monitor.browser.cjs"
	out, err := exec.CommandContext(ctx, "node", script, f.base, ctlSrv.URL, shots).CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium bg-exec monitor acceptance: %v", err)
	}
}
