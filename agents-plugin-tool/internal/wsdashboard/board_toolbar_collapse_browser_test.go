package wsdashboard

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
	"golang.org/x/net/websocket"
)

// TestBoardToolbarCollapseBrowser sweeps board toolbar widths from 1600 down to
// 360 in both Korean and English on the real production session page, verifying
// that the toolbar collapses monotonically before wrapping to a second row and
// retains all labels at wide widths. Set WS_PLAYWRIGHT_MODULE to run and
// optionally WS_TOOLBAR_CAPTURES to write light-theme screenshots.
func TestBoardToolbarCollapseBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run isolated Chromium toolbar collapse regression")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	captures := os.Getenv("WS_TOOLBAR_CAPTURES")
	if captures != "" {
		if err := os.MkdirAll(captures, 0o755); err != nil {
			t.Fatal(err)
		}
		captures, err = filepath.Abs(captures)
		if err != nil {
			t.Fatal(err)
		}
	}

	buildBoard := func() (*wswhiteboard.State, error) {
		st := &wswhiteboard.State{}
		apply := func(cmd string) error {
			_, probs := wswhiteboard.Apply(st, cmd, wswhiteboard.Env{Now: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)})
			if len(probs) != 0 {
				return fmt.Errorf("apply: %v", probs)
			}
			return nil
		}
		for _, state := range []string{"researching", "discussing", "waiting", "implementing", "awaiting-confirmation"} {
			attrs := ""
			if state == "implementing" {
				attrs = ` owner="fixture-executor" eta_minutes="30"`
			}
			if err := apply(fmt.Sprintf(`<wb:topic name="%s-1" label="%s sample"><wb:body>Toolbar collapse fixture body for %s.</wb:body><wb:state value="%s"%s/></wb:topic>`, state, state, state, state, attrs)); err != nil {
				return nil, err
			}
		}
		if err := apply(`<wb:topic name="discussing-1"><wb:add class="check"><wb:title>Open check item</wb:title></wb:add></wb:topic>`); err != nil {
			return nil, err
		}
		for i := 0; i < 35; i++ {
			if err := apply(fmt.Sprintf(`<wb:topic name="closed-%02d"><wb:body>Closed topic %d.</wb:body><wb:close result="Done"/></wb:topic>`, i, i)); err != nil {
				return nil, err
			}
		}
		return st, nil
	}

	ring := []wslive.RingRecord{
		ringRecord(1, wslive.RingEntry{Kind: wslive.RingUser, Origin: wslive.OriginWhiteboard, Text: "Verify toolbar collapse across widths."}),
		ringRecord(2, wslive.RingEntry{Kind: wslive.RingReply, Text: "Checking single-row toolbar layout for Korean and English."}),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/s/tbcollapse", func(w http.ResponseWriter, r *http.Request) {
		lang := r.URL.Query().Get("lang")
		if lang != "en" {
			lang = "ko"
		}
		theme := r.URL.Query().Get("theme")
		if theme != "dark" {
			theme = "light"
		}
		st, err := buildBoard()
		if err != nil {
			t.Logf("buildBoard error: %v", err)
			http.Error(w, err.Error(), 500)
			return
		}
		wbView := wswhiteboard.BuildView(st, wswhiteboard.ViewInput{Layout: wswhiteboard.LayoutFull, ReplyID: "tbcollapse"})
		wbView.Lang = lang
		wbReg, err := wswhiteboard.RenderRegions(wbView)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		hView := historyView(ring, "", PageContent{}, sessionLookup{})
		v := SessionView{
			ReplyID:          "tbcollapse",
			Label:            "toolbar-collapse",
			Adapter:          true,
			Live:             true,
			HasBoard:         true,
			BoardIncarnation: "2026-11-22T00:00:00Z",
			Seq:              st.Seq,
			Board:            wbReg.Board,
			Panels:           wbReg.Panels,
			Conversation:     hView.Conversation,
			Lang:             lang,
			Theme:            theme,
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
	mux.Handle("/s/tbcollapse/events", websocket.Handler(func(c *websocket.Conn) {
		var msg string
		for websocket.Message.Receive(c, &msg) == nil {
		}
	}))
	mux.HandleFunc("/s/tbcollapse/terminals", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"terminals":[],"folders":{"worktree":"/work","root":"/repo"}}`)
	})
	mux.HandleFunc("/s/tbcollapse/", func(w http.ResponseWriter, r *http.Request) {
		if data, ct, ok := wswhiteboard.FrameAsset(path.Base(r.URL.Path)); ok {
			w.Header().Set("Content-Type", ct)
			w.Write(data)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if data, ct, ok := wswhiteboard.FrameAsset(path.Base(r.URL.Path)); ok {
			w.Header().Set("Content-Type", ct)
			w.Write(data)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "testdata/board-toolbar-collapse.browser.cjs", server.URL, captures)
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("board toolbar collapse browser: %v", err)
	}
}
