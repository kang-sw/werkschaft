package wsdashboard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

func TestBoardPanelFullscreenBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium acceptance")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	SetDevAssets(nil)
	f := startPageFixture(t)
	writeSnapshot(t, f.dir, "livesession", &wslive.Snapshot{
		SchemaVersion: 1, Adapter: "pi", UpdatedAt: time.Now().UnixMilli(), HeartbeatMs: 3600000,
	})
	st := &wswhiteboard.State{Seq: 20}
	for i := 1; i <= st.Seq; i++ {
		topic := wswhiteboard.TopicDoc{Name: fmt.Sprintf("Topic %02d", i), Body: "Topic body text", Changed: i}
		if i == 10 {
			topic.Entries = []wswhiteboard.Entry{
				{ID: "e1", Kind: wswhiteboard.EntryPanel, PanelID: "p1", Recap: "First panel", Text: "<p>First panel document</p>"},
				{ID: "e2", Kind: wswhiteboard.EntryPanel, PanelID: "p2", Recap: "Second panel", Text: "<p>Second panel document</p>"},
			}
		}
		st.Topics = append(st.Topics, topic)
	}
	view := wswhiteboard.BuildView(st, wswhiteboard.ViewInput{Layout: wswhiteboard.LayoutFull, ReplyID: "livesession"})
	regions, err := wswhiteboard.RenderRegions(view)
	if err != nil {
		t.Fatal(err)
	}
	f.src.setPage("livesession", PageContent{HasBoard: true, Seq: st.Seq, Board: regions})
	f.d.Index().Refresh()

	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/board-panel-fullscreen.browser.cjs", f.base)
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("Chromium panel fullscreen acceptance: %v", err)
	}
}
