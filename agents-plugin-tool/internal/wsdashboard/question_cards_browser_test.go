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
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

func TestChatStreamQuestionCardsBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE for isolated Chromium acceptance")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var st *wswhiteboard.State
	apply := func(cmd string) error {
		_, probs := wswhiteboard.Apply(st, cmd, wswhiteboard.Env{Now: time.Date(2026, 11, 26, 14, 0, 0, 0, time.UTC)})
		if len(probs) != 0 {
			return fmt.Errorf("Apply problems: %v", probs)
		}
		return nil
	}
	reset := func() error {
		st = &wswhiteboard.State{}
		return apply(`<wb:topic name="design" label="Design Review">
			<wb:add class="policy"><wb:title>Choose chat stream capsule density and wrapping</wb:title><wb:option>Compact</wb:option><wb:option>Relaxed</wb:option></wb:add>
			<wb:add class="technical"><wb:title>Keep server WhiteBoardCard projection T0 unchanged</wb:title></wb:add>
			<wb:add class="announced"><wb:title>Reuse existing dark and light board palette tokens</wb:title></wb:add>
			<wb:add class="check"><wb:title>Verify keyboard focus and hover jump indicator</wb:title></wb:add>
			<wb:add class="policy"><wb:title>Already settled policy decision at initial load</wb:title><wb:option>A</wb:option><wb:option>B</wb:option></wb:add>
			<wb:add class="check"><wb:title>Already settled check verified</wb:title></wb:add>
			<wb:add class="technical"><wb:title>Already assumed technical default</wb:title></wb:add>
			<wb:add class="announced"><wb:title>Already withdrawn legacy option</wb:title></wb:add>
		</wb:topic>
		<wb:answer id="5" value="B">B</wb:answer>
		<wb:answer id="6" value="ok">ok</wb:answer>
		<wb:answer id="7" value="ok" assumed="true">default ok</wb:answer>
		<wb:patch id="8" status="withdrawn"/>`)
	}
	if err := reset(); err != nil {
		t.Fatal(err)
	}

	buildEntries := func() []ConvEntry {
		effects := []wswhiteboard.Effect{
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 1, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 2, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 3, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 4, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 5, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 6, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 7, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 8, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 999, Field: "title"},
		}
		dupEffects := []wswhiteboard.Effect{
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 1, Field: "title"},
			{Kind: "item", Topic: "design", Label: "Design Review", ID: 4, Field: "title"},
		}
		e1, _ := wslive.NewWhiteboardEffects(effects)
		e3, _ := wslive.NewWhiteboardEffects(dupEffects)
		return []ConvEntry{
			{RingEntry: e1, Key: "e-001", At: 1795615200000, AtRFC: "2026-11-26T14:00:00Z"},
			{RingEntry: wslive.RingEntry{Kind: wslive.RingReply, Text: "Follow-up turn referencing the same open questions."}, Key: "e-002", At: 1795615210000, AtRFC: "2026-11-26T14:00:10Z"},
			{RingEntry: e3, Key: "e-003", At: 1795615220000, AtRFC: "2026-11-26T14:00:20Z"},
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/s/qcards", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("reset") == "1" {
			if err := reset(); err != nil {
				http.Error(w, err.Error(), 500)
				return
			}
		}
		lang := r.URL.Query().Get("lang")
		if lang == "" {
			lang = "en"
		}
		theme := r.URL.Query().Get("theme")
		if theme == "" {
			theme = "dark"
		}
		historyOnly := r.URL.Query().Get("history") == "1"
		view := wswhiteboard.BuildView(st, wswhiteboard.ViewInput{Layout: wswhiteboard.LayoutFull, ReplyID: "qcards"})
		view.Lang = lang
		wb, err := wswhiteboard.RenderRegions(view)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		v := SessionView{
			ReplyID:           "qcards",
			Adapter:           !historyOnly,
			Live:              !historyOnly,
			SavedConversation: historyOnly,
			HasBoard:          !historyOnly,
			Seq:               st.Seq,
			Board:             wb.Board,
			Panels:            wb.Panels,
			Conversation:      ConversationView{Lang: lang, Entries: buildEntries()},
			Lang:              lang,
			Theme:             theme,
		}
		if historyOnly {
			v.Board = ""
			v.Panels = ""
		}
		regions, err := renderSessionRegions(v, nil)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		page, err := renderSessionPage(v, regions, false, "")
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	})
	mux.HandleFunc("/s/qcards/settle", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if err := apply(`<wb:answer id="1" value="A">A</wb:answer>`); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		lang := r.URL.Query().Get("lang")
		if lang == "" {
			lang = "en"
		}
		view := wswhiteboard.BuildView(st, wswhiteboard.ViewInput{Layout: wswhiteboard.LayoutFull, ReplyID: "qcards"})
		view.Lang = lang
		wb, err := wswhiteboard.RenderRegions(view)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, string(wb.Board))
	})
	mux.HandleFunc("/s/qcards/history", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"html":"","more":false}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	script, err := filepath.Abs("testdata/question-cards.browser.cjs")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, script)
	cmd.Env = append(os.Environ(), "QCARDS_BASE="+srv.URL)
	cmd.WaitDelay = 5 * time.Second
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("question cards browser: %v", err)
	}
}
