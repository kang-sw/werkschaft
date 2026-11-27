package wsdashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// lifecycleDragAgents serves /agents/<id> state plus /hide, /unhide, /close,
// and /reopen ({hidden?: bool}) for TestRailLifecycleDragBrowser.
type lifecycleDragAgents struct {
	dir        string
	failHideID string
	mu         sync.Mutex
	notify     func(replyID, project string)
	hasProc    map[string]bool
	posts      []string
}

func (a *lifecycleDragAgents) SessionMeta(replyID string) (AgentSessionMeta, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	proc, ok := a.hasProc[replyID]
	if !ok {
		return AgentSessionMeta{}, false
	}
	return AgentSessionMeta{HasProcess: proc, Backend: "claude"}, true
}

func (a *lifecycleDragAgents) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, AgentRoutePrefix+"/")
		id, action, _ := strings.Cut(rest, "/")
		if !ValidReplyID(id) {
			http.NotFound(w, r)
			return
		}
		state := func() {
			hidden, _ := ReadSessionHidden(a.dir, id)
			a.mu.Lock()
			proc := a.hasProc[id]
			a.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id":              id,
				"backend":         "claude",
				"status":          "running",
				"can_control":     true,
				"process":         proc,
				"hidden":          hidden,
				"model":           "claude-opus-5-5",
				"permission_mode": "auto",
			})
		}
		if r.Method == http.MethodGet && action == "" {
			state()
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "unknown agent route", http.StatusNotFound)
			return
		}
		switch action {
		case "hide", "unhide", "close":
			a.mu.Lock()
			a.posts = append(a.posts, id+"/"+action)
			notify := a.notify
			a.mu.Unlock()
			if action == "hide" && id == a.failHideID {
				http.Error(w, "the session was not archived: the holder did not stop", http.StatusInternalServerError)
				return
			}
			hidden := action == "hide"
			if err := WriteSessionHidden(a.dir, id, hidden); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			reg, err := ReadRegistration(a.dir, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			now := stampFor(time.Now())
			reg.HostStopped, reg.HostExitReason, reg.HeartbeatAt = true, "stop", now
			if action == "close" {
				reg.EndedAt = now
			} else {
				reg.EndedAt = ""
			}
			if err := WriteRegistration(a.dir, reg); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			a.mu.Lock()
			a.hasProc[id] = false
			a.mu.Unlock()
			if notify != nil {
				notify(id, reg.Project)
			}
			state()
		case "reopen":
			var body struct {
				Hidden bool `json:"hidden"`
			}
			if r.Body != nil {
				_ = json.NewDecoder(r.Body).Decode(&body)
			}
			a.mu.Lock()
			a.posts = append(a.posts, fmt.Sprintf("%s/reopen?hidden=%v", id, body.Hidden))
			notify := a.notify
			a.mu.Unlock()
			if err := WriteSessionHidden(a.dir, id, body.Hidden); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			reg, err := ReadRegistration(a.dir, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			reg.EndedAt = ""
			reg.HostStopped, reg.HostExitReason, reg.HeartbeatAt = true, "stop", stampFor(time.Now())
			if err := WriteRegistration(a.dir, reg); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			a.mu.Lock()
			a.hasProc[id] = false
			a.mu.Unlock()
			if notify != nil {
				notify(id, reg.Project)
			}
			state()
		default:
			http.Error(w, "unknown agent action", http.StatusNotFound)
		}
	})
}

func (a *lifecycleDragAgents) Start(notify func(replyID, project string)) {
	a.mu.Lock()
	a.notify = notify
	a.mu.Unlock()
}
func (a *lifecycleDragAgents) InboxChanged(string) {}
func (a *lifecycleDragAgents) Close()              {}

func initLifecycleGitRepo(t *testing.T, repo, soloWt string) {
	t.Helper()
	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM=")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	run(repo, "init")
	run(repo, "config", "user.name", "Test")
	run(repo, "config", "user.email", "test@example.com")
	run(repo, "commit", "--allow-empty", "-m", "init")
	run(repo, "worktree", "add", soloWt, "-b", "solo")
}

func seedLifecycleDragRail(t *testing.T, dir, devenv, soloWt, game string) {
	t.Helper()
	now := time.Now()
	old := 24 * time.Hour
	bgSnap := &wslive.Snapshot{
		SchemaVersion: 1,
		Adapter:       "claude",
		UpdatedAt:     now.UnixMilli(),
		HeartbeatMs:   30000,
		Lead:          wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseIdle}},
		Host:          &wslive.Host{BackgroundTasks: 1},
	}
	rows := []fourStateSession{
		{id: "starlead", project: devenv, label: "StarLead", harness: "claude", hosted: true, asleep: true},
		{id: "peer1", project: devenv, label: "Peer One", harness: "claude", hosted: true, asleep: true},
		{id: "peer2", project: devenv, label: "Peer Two", harness: "claude", hosted: true, asleep: true},
		{id: "kidsrow", project: devenv, label: "Parent Lead", harness: "claude", hosted: true, asleep: true},
		{id: "kidrow", project: devenv, label: "Child Session", harness: "claude", hosted: true, asleep: true},
		{id: "procrow", project: devenv, label: "Live Proc", harness: "claude", hosted: true},
		{id: "bgrow", project: devenv, label: "BG Lead", harness: "claude", hosted: true, snap: bgSnap},
		{id: "archfail", project: devenv, label: "Fail Arch", harness: "claude", hosted: true, asleep: true},
		{id: "termrow", project: devenv, label: "CLI Row", harness: "pi"},
		{id: "archidle", project: devenv, label: "Arch Idle", harness: "claude", hosted: true, asleep: true, hidden: true},
		{id: "delreopen", project: devenv, label: "Del Reopen", harness: "claude", hosted: true, endedAgo: old},
		{id: "delblocked", project: devenv, label: "Del Blocked", harness: "claude", hosted: false, endedAgo: old},
		{id: "solorow", project: soloWt, label: "Solo Lead", harness: "claude", hosted: true, asleep: true},
		{id: "gameone", project: game, label: "Game One", harness: "claude", hosted: true, asleep: true},
		{id: "gametwo", project: game, label: "Game Two", harness: "claude", hosted: true, asleep: true},
	}
	for i, s := range rows {
		at := now.Add(-time.Duration(len(rows)-i) * time.Minute)
		reg := Registration{
			ReplyID:      s.id,
			Project:      s.project,
			Harness:      s.harness,
			Hosted:       s.hosted,
			RegisteredAt: stampFor(at),
			HeartbeatAt:  stampFor(now),
		}
		if s.asleep {
			reg.HostStopped, reg.HostExitReason = true, "stop"
		}
		if s.endedAgo > 0 {
			ended := stampFor(now.Add(-s.endedAgo))
			reg.RegisteredAt, reg.HeartbeatAt, reg.EndedAt = ended, ended, ended
		}
		if err := WriteRegistration(dir, reg); err != nil {
			t.Fatal(err)
		}
		if err := writeSessionLabel(SessionDir(dir, s.id), s.label); err != nil {
			t.Fatal(err)
		}
		if s.hidden {
			if err := WriteSessionHidden(dir, s.id, true); err != nil {
				t.Fatal(err)
			}
		}
		if s.snap != nil {
			writeSnapshot(t, dir, s.id, s.snap)
		}
	}
}

// TestRailLifecycleDragBrowser verifies bidirectional drag-and-drop transitions
// across Active, Archived, and Deleted sections in Chromium, including empty
// 0/0 fold reveal, risk confirmation popovers, refusal hints, 500 error recovery,
// positioned restore, and atomic reopen-to-archive routing.
func TestRailLifecycleDragBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("WS_CACHE_HOME", t.TempDir())
	t.Setenv("WS_CONFIG_HOME", t.TempDir())
	SetDevAssets(nil)

	dir, root := t.TempDir(), t.TempDir()
	devenv := filepath.Join(root, "devenv")
	soloWt := filepath.Join(root, "devenv-solo")
	game := filepath.Join(root, "game")
	initLifecycleGitRepo(t, devenv, soloWt)
	if err := os.MkdirAll(filepath.Join(game, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	seedLifecycleDragRail(t, dir, devenv, soloWt, game)

	agents := &lifecycleDragAgents{
		dir:        dir,
		failHideID: "archfail",
		hasProc: map[string]bool{
			"starlead": false,
			"peer1":    false,
			"peer2":    false,
			"kidsrow":  false,
			"kidrow":   false,
			"procrow":  true,
			"bgrow":    false,
			"archfail": false,
			"archidle": false,
			"solorow":  false,
			"gameone":  false,
			"gametwo":  false,
		},
	}
	d := startIndexedDaemon(t, dir, newPageFake(), time.Hour, func(o *DaemonOptions) {
		o.Port = 0
		o.LAN = false
		o.bindHost = "127.0.0.1"
		o.noGitBadge = true
		o.Agents = agents
	})
	base := endpointURL(d.Endpoint(), "")
	if u, err := url.Parse(base); err != nil || u.Port() == "29417" {
		t.Fatal("fixture must use an ephemeral port, never the shared daemon port")
	}
	waitFor(t, "lifecycle drag rail sessions to be indexed", func() bool {
		s, ok := d.Index().Session("archidle")
		_, soloOK := d.Index().Session("solorow")
		return ok && s.Hidden && soloOK
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/rail-lifecycle-drag.browser.cjs", base)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium lifecycle drag acceptance: %v", err)
	}

	agents.mu.Lock()
	posts := append([]string(nil), agents.posts...)
	agents.mu.Unlock()
	joined := strings.Join(posts, ",")
	for _, want := range []string{
		"delreopen/reopen?hidden=true",
		"delreopen/reopen?hidden=false",
		"procrow/hide",
		"kidsrow/close",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("posts = %s; missing %s", joined, want)
		}
	}
	if hidden, _ := ReadSessionHidden(dir, "archfail"); hidden {
		t.Errorf("archfail must remain unarchived after 500 failure")
	}
	if strings.Contains(joined, "bgrow/hide") {
		t.Errorf("cancelled bgrow archive posted: %s", joined)
	}
	if reg, err := ReadRegistration(dir, "delreopen"); err != nil || reg.EndedAt != "" || !reg.HostStopped {
		t.Errorf("delreopen final registration = %+v, err=%v; want active asleep", reg, err)
	}
}
