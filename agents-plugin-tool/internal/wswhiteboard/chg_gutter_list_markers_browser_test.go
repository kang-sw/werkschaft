package wswhiteboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsi18n"
)

func TestChgGutterListMarkersBrowser(t *testing.T) {
	if os.Getenv("WS_PLAYWRIGHT_MODULE") == "" {
		t.Skip("set WS_PLAYWRIGHT_MODULE to run Chromium/WebKit acceptance")
	}
	now := time.Date(2026, 11, 22, 9, 45, 0, 0, time.UTC)

	var ol11, ol15 strings.Builder
	for i := 1; i <= 15; i++ {
		line := strconv.Itoa(i) + ". List item " + strconv.Itoa(i) + " with enough detail to show alignment\n"
		if i <= 11 {
			ol11.WriteString(line)
		}
		ol15.WriteString(line)
	}

	matrixBody := "Intro paragraph.\n\n" +
		"1. One digit unchanged\n2. One digit changed\n\n" +
		"12. Two digits 12 changed\n13. Two digits 13 changed\n\n" +
		"100. Three digits 100 changed\n101. Three digits 101 changed\n\n" +
		"1. Parent ordered item\n   - Nested unordered item\n   - Nested second\n\n" +
		"- Parent bullet item\n  1. Nested ordered 1\n  2. Nested ordered 2\n\n" +
		"- [ ] Unchecked task item\n- [x] Checked task item\n\n" +
		"1. [ ] Ordered task item\n"

	st := &State{
		Seq: 2,
		Topics: []TopicDoc{
			{
				Name:      "ol-15",
				Changed:   2,
				ChangedAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
				Body:      ol15.String(),
				Marks:     diffBodies(ol11.String(), ol15.String()),
			},
			{
				Name:      "lists-matrix",
				Changed:   2,
				ChangedAt: now.Add(-5 * time.Minute).Format(time.RFC3339),
				Body:      matrixBody,
				Marks: []BlockMark{
					{Block: 0, Kind: MarkNew},
					{Block: 2, Kind: MarkNew},
					{Block: 3, Kind: MarkNew},
					{Block: 4, Kind: MarkNew},
					{Block: 5, Kind: MarkNew},
					{Block: 6, Kind: MarkNew},
					{Block: 7, Kind: MarkNew},
					{Block: 8, Kind: MarkNew},
					{Block: 9, Kind: MarkNew},
					{Block: 10, Kind: MarkNew},
					{Block: 11, Kind: MarkNew},
					{Block: 12, Kind: MarkNew},
					{Block: 13, Kind: MarkNew},
					{Block: 14, Kind: MarkNew},
					{Block: 15, Kind: MarkNew},
				},
			},
		},
	}

	view := BuildView(st, ViewInput{Layout: LayoutFull, ReplyID: "chg-gutter"})
	view.Lang = "ko"
	for i := range view.Topics {
		view.Topics[i].BodyUpdatedAt = now.Add(-5 * time.Minute).Format(time.RFC3339)
	}
	clock := `<script>Date.now = function () { return ` + strconv.FormatInt(now.UnixMilli(), 10) + `; };</script>`
	regions, err := RenderRegions(view)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := PageAssets()
	if err != nil {
		t.Fatal(err)
	}
	board := strings.ReplaceAll(string(regions.Board), "&lt;head&gt;", "&lt;head&gt;"+strings.ReplaceAll(strings.ReplaceAll(clock, "<", "&lt;"), ">", "&gt;"))
	page := `<!DOCTYPE html><html><head><meta charset="utf-8">` + clock + `<style>` + string(assets.CSS) + `</style></head><body>` +
		string(assets.Sprite) + `<main>` + board + `</main><script>` + string(wsi18n.Script(view.Lang)) +
		`</script><script>` + string(assets.AnswersScript) + `</script><script>(function(){` + string(assets.FormScript) +
		`})();</script></body></html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(page))
			return
		}
		if data, kind, ok := FrameAsset(strings.TrimPrefix(r.URL.Path, "/")); ok {
			w.Header().Set("Content-Type", kind)
			_, _ = w.Write(data)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", "testdata/chg-gutter-list-markers.browser.cjs",
		srv.URL, os.Getenv("WS_CHG_GUTTER_SHOTS"), os.Getenv("WS_CHG_GUTTER_PHASE"))
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatalf("chg gutter browser test: %v", err)
	}
}
