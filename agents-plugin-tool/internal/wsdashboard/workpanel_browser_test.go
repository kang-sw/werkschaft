package wsdashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsnote"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// workpanelNotes is the Workpanel fixture's notes source: four layers, one
// muted record, and a count of the forced (Refresh) reads.
type workpanelNotes struct {
	*pageFake
	mu        sync.Mutex
	refreshes int
}

func (s *workpanelNotes) ReadNotes(reg Registration, refresh bool) ([]NotesLayer, error) {
	s.mu.Lock()
	if refresh {
		s.refreshes++
	}
	s.mu.Unlock()
	at := time.Now().Add(-90 * time.Minute).UTC().Format(time.RFC3339)
	return []NotesLayer{
		{Layer: wsnote.LayerWorktree, Notes: []wsnote.Record{
			{Key: "wp.render-gate", Value: "Workpanel renders behind the gallery's sys page; keep the panels region single-owner.", Priority: 2, WrittenAt: at, Visible: true},
			{Key: "wp.scratch", Value: "muted scratch line", WrittenAt: at, Visible: false},
		}},
		{Layer: wsnote.LayerRepo, Notes: []wsnote.Record{
			{Key: "handoff", Value: "public repo handoff: the drawer's notes card retired into the Workpanel reference card.", Priority: 1, WrittenAt: at, Visible: true},
		}},
		{Layer: wsnote.LayerClone, Notes: []wsnote.Record{{Key: "clone.cache", Value: "clone-wide cache note", WrittenAt: at, Visible: true}}},
		{Layer: wsnote.LayerMachine, Notes: []wsnote.Record{{Key: "machine.path", Value: "machine-local toolchain path", WrittenAt: at, Visible: true}}},
	}, nil
}

// workpanelPublish renders livesession's working memory: a todo list with
// done of total finished, and an agenda tree whose first blob says agenda.
func workpanelPublish(t *testing.T, f *pageFixture, todo, agenda string, done, total int) {
	t.Helper()
	var rows []wswhiteboard.PanelRow
	for i := 0; i < total; i++ {
		state := "pending"
		if i < done {
			state = "done"
		}
		rows = append(rows, wswhiteboard.PanelRow{State: state, Text: fmt.Sprintf("%s step %02d", todo, i)})
	}
	tree := []wswhiteboard.TreeNode{
		wswhiteboard.JSONTree("agenda/", "implement", []byte(fmt.Sprintf(`{"phase":{"value":%q},"next":{"value":"Playwright spec"}}`, agenda))),
		wswhiteboard.JSONTree("agenda/", "review", []byte(`{"allocation":"lite","reviewer":"fresh"}`)),
	}
	view := wswhiteboard.BuildView(&wswhiteboard.State{Seq: 1, Topics: []wswhiteboard.TopicDoc{{Name: "Plan", Body: "Workpanel fixture board", Changed: 1}}}, wswhiteboard.ViewInput{Layout: wswhiteboard.LayoutFull, Panels: []wswhiteboard.Panel{
		{ID: "todo", Title: "Todo", Count: fmt.Sprintf("%d/%d", done, total), Rows: rows},
		{ID: "agenda", Title: "Agenda", Tree: tree},
	}})
	regions, err := wswhiteboard.RenderRegions(view)
	if err != nil {
		t.Fatal(err)
	}
	p := f.src.page("livesession")
	p.Board = regions
	p.Seq++
	f.src.setPage("livesession", p)
	if !f.d.index.Nudge("livesession", f.project) {
		t.Fatal("nudge refused")
	}
	f.d.index.Settle()
}

// TestWorkpanelBrowser drives the gallery's Workpanel system page (Graybox
// B) in Chromium against a disposable daemon, in both themes with the
// gallery column wide (720px, the column's maximum) and narrow (380px), and
// an empty session: the panels region adopted into the page and kept live
// by the keyed merge, the container-query switch between the two-column
// memory and the [Todo | Agenda] segment, no horizontal overflow, the Notes
// tab (layer chips, search, muted toggle, expand, Refresh's POST, the LAN
// repo-only hint), the Manuals tab read once on demand and opening the file
// preview, the approval wait on the rail slot and the drawer handle, and the
// drawer slimmed to its worktree and holder cards. WS_WORKPANEL_SHOTS=<dir>
// keeps the captures beside the approved mockup's presets.
func TestWorkpanelBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE for Chromium acceptance")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, key := range []string{"WS_CACHE_HOME", "WS_CONFIG_HOME", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(key, filepath.Join(root, key))
	}
	t.Setenv("WS_MCP_DASHBOARD_DEV_SOURCE", "")
	var notes *workpanelNotes
	f := startPageFixture(t, func(o *DaemonOptions) {
		o.Home = filepath.Join(root, "HOME")
		o.Worktrees = permUIWorktrees{}
		o.Holders = &stateFilterTerminalHost{newFakeHolders()}
		notes = &workpanelNotes{pageFake: o.Sessions.(*pageFake)}
		o.Sessions = notes
	})
	manuals := filepath.Join(f.project, "ai-docs", "manuals")
	if err := os.MkdirAll(manuals, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"dashboard-style.md": "---\nsummary: Dashboard visual tokens, density, and theme rules\n---\n# Dashboard style\n",
		"performance.md":     "---\nsummary: Hot-path rules: no polling, compositor-only animation\n---\n# Performance\n",
	} {
		if err := os.WriteFile(filepath.Join(manuals, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	snap := wslive.Snapshot{SchemaVersion: 1, Adapter: "pi", UpdatedAt: time.Now().UnixMilli(), HeartbeatMs: 3600000,
		Lead: wslive.Lead{Activity: wslive.Activity{Phase: wslive.PhaseThinking}},
		Agents: []wslive.Agent{
			{ID: "impl", Label: "implementer", Status: wslive.StatusRunning, Activity: wslive.Activity{Phase: wslive.PhaseThinking}},
		}}
	f.d.snapshots.SetLive(f.dir, "livesession", snap)
	var mu sync.Mutex
	todo, agenda, done := "initial", "spine fixed", 1
	workpanelPublish(t, f, todo, agenda, done, 4)
	updates := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		q := r.URL.Query()
		switch q.Get("op") {
		case "reset":
			todo, agenda, done = "initial", "spine fixed", 1
			workpanelPublish(t, f, todo, agenda, done, 4)
		case "todo":
			todo, done = q.Get("value"), done+1
			workpanelPublish(t, f, todo, agenda, done, 4)
		case "agenda":
			agenda = q.Get("value")
			workpanelPublish(t, f, todo, agenda, done, 4)
		case "permission", "clear":
			if q.Get("op") == "permission" {
				snap.Agents[0].Phase = wslive.PhasePermission
				snap.Agents[0].Permission = &wslive.Permission{ToolName: "Bash", ArgsPreview: "workpanel fixture approval"}
			} else {
				snap.Agents[0].Phase, snap.Agents[0].Permission = wslive.PhaseThinking, nil
			}
			f.d.snapshots.SetLive(f.dir, "livesession", snap)
			f.d.broker.wake(liveDep("livesession"))
		case "refreshes":
			notes.mu.Lock()
			n := notes.refreshes
			notes.mu.Unlock()
			fmt.Fprint(w, n)
			return
		default:
			http.Error(w, "unknown fixture update", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer updates.Close()
	shots := os.Getenv("WS_WORKPANEL_SHOTS")
	mockup, _ := filepath.Abs("../../../ai-docs/assets/mockups/261128/gallery-workpanel-b/index.html")
	if _, err := os.Stat(mockup); err != nil || shots == "" {
		mockup = ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/workpanel.browser.cjs", f.base, updates.URL, shots, mockup)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium workpanel: %v", err)
	}
}
