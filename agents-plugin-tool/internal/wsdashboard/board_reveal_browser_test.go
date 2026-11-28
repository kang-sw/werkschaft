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

// TestBoardRevealBrowser loads a framed whiteboard session page hidden, then
// shows it the way the shell does on a session switch (shown:true), including
// a board swap held while hidden, and checks that topic cards fill the middle
// of #bcol without any user scroll. Set WS_PLAYWRIGHT_MODULE to run.
func TestBoardRevealBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run isolated Chromium board reveal regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	st := &wswhiteboard.State{}
	for i := 0; i < 30; i++ {
		cmd := fmt.Sprintf(`<wb:topic name="reveal-%02d" label="Reveal %02d"><wb:body>%s</wb:body><wb:state value="discussing"/></wb:topic>`,
			i, i, strings.Repeat("Reveal fixture paragraph keeps each card tall enough to need scrolling. ", 12))
		if _, probs := wswhiteboard.Apply(st, cmd, wswhiteboard.Env{Now: time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC)}); len(probs) != 0 {
			t.Fatalf("apply %d: %v", i, probs)
		}
	}
	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Reveal the board."}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingReply, Text: "Revealed."}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/s/wbreveal", func(w http.ResponseWriter, r *http.Request) {
		wbView := wswhiteboard.BuildView(st, wswhiteboard.ViewInput{Layout: wswhiteboard.LayoutFull, ReplyID: "wbreveal"})
		wbView.Lang = "en"
		wbReg, err := wswhiteboard.RenderRegions(wbView)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		hView := historyView(ring, "", PageContent{}, sessionLookup{})
		v := SessionView{
			ReplyID:          "wbreveal",
			Label:            "board-reveal",
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
	mux.Handle("/s/wbreveal/events", websocket.Handler(func(c *websocket.Conn) {
		var msg string
		for websocket.Message.Receive(c, &msg) == nil {
		}
	}))
	mux.HandleFunc("/s/wbreveal/terminals", func(w http.ResponseWriter, r *http.Request) {
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
	mux.HandleFunc("/s/wbreveal/", serveFrameAsset)
	mux.HandleFunc("/", serveFrameAsset)

	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, node, "testdata/board-reveal.browser.cjs", server.URL).CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("board reveal browser: %v", err)
	}
}
