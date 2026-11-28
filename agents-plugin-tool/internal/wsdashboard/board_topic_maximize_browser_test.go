package wsdashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
	"golang.org/x/net/websocket"
)

// TestBoardTopicMaximizeBrowser verifies that maximizing and restoring a tall
// topic card (and a panel inside it) from mid-scroll when its free-canvas Y has
// drifted away from FREE_RUNWAY_MID (50000) never leaves #bcol blank, preserves
// the pre-maximize reading offset (including across settle/SSE updates while
// maximized), and never pollutes freeHeightCache with the fixed sheet height.
func TestBoardTopicMaximizeBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium topic maximize regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	st := &wswhiteboard.State{Seq: 24}
	tallPara := strings.Repeat("<p>Tall topic body paragraph keeps the card much taller than the viewport so mid-scroll maximize and restore exercises drifted free-canvas coordinates.</p>", 42)
	normalPara := strings.Repeat("<p>Standard topic body paragraph for free-canvas spacing.</p>", 6)
	for i := 0; i < 24; i++ {
		body := normalPara
		if i == 2 || i == 10 {
			body = tallPara
		}
		topic := wswhiteboard.TopicDoc{
			Name:    fmt.Sprintf("max-%02d", i),
			Body:    body,
			Changed: 100 - i,
		}
		if i == 10 {
			topic.Entries = []wswhiteboard.Entry{
				{ID: "e1", Kind: wswhiteboard.EntryPanel, PanelID: "p-mid", Recap: "Mid-topic panel", Text: "<p>Mid-topic panel body content</p>"},
			}
		}
		st.Topics = append(st.Topics, topic)
	}
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Review board."}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingReply, Text: "Ready."}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/s/wbmax", func(w http.ResponseWriter, r *http.Request) {
		wbView := wswhiteboard.BuildView(st, wswhiteboard.ViewInput{Layout: wswhiteboard.LayoutFull, ReplyID: "wbmax"})
		wbView.Lang = "en"
		wbReg, err := wswhiteboard.RenderRegions(wbView)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		hView := historyView(ring, "", PageContent{}, sessionLookup{})
		v := SessionView{
			ReplyID:          "wbmax",
			Label:            "board-topic-maximize",
			Adapter:          true,
			Live:             true,
			HasBoard:         true,
			BoardIncarnation: "2026-11-28T00:00:00Z",
			Seq:              st.Seq,
			Board:            wbReg.Board,
			Panels:           wbReg.Panels,
			Conversation:     hView.Conversation,
			Lang:             "en",
			Theme:            "dark",
		}
		regions, err := renderSessionRegions(v, nil)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		page, err := renderSessionPage(v, regions, true, "xterm.js")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	})
	mux.Handle("/s/wbmax/events", websocket.Handler(func(c *websocket.Conn) {
		var msg string
		for websocket.Message.Receive(c, &msg) == nil {
		}
	}))
	mux.HandleFunc("/s/wbmax/terminals", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"terminals":[],"folders":{"worktree":"/work","root":"/repo"}}`)
	})
	serveFrameAsset := func(w http.ResponseWriter, r *http.Request) {
		if data, ct, ok := wswhiteboard.FrameAsset(path.Base(r.URL.Path)); ok {
			w.Header().Set("Content-Type", ct)
			w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}
	mux.HandleFunc("/s/wbmax/", serveFrameAsset)
	mux.HandleFunc("/", serveFrameAsset)

	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "testdata/board-topic-maximize.browser.cjs", server.URL).CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("board topic maximize browser: %v", err)
	}
}
