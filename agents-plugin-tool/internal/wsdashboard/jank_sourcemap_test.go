package wsdashboard

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"unicode/utf16"
)

func TestAssembleDashboardScriptsUTF16SourceMap(t *testing.T) {
	a, err := loadDashboardAssets()
	if err != nil {
		t.Fatal(err)
	}
	var sm scriptSourceMapData
	if err := json.Unmarshal([]byte(a.data.ScriptSourceMap), &sm); err != nil {
		t.Fatalf("unmarshal ScriptSourceMap: %v", err)
	}
	if len(sm.Files) != len(dashboardScriptFiles) {
		t.Fatalf("files len = %d, want %d", len(sm.Files), len(dashboardScriptFiles))
	}
	wantUTF16 := len(utf16.Encode([]rune(string(a.data.Script))))
	if sm.TotalChars != wantUTF16 {
		t.Fatalf("TotalChars = %d, want %d", sm.TotalChars, wantUTF16)
	}
	prevChar := 0
	prevEndLine := 0
	for i, f := range sm.Files {
		if f.File != dashboardScriptFiles[i] {
			t.Fatalf("file[%d] = %q, want %q", i, f.File, dashboardScriptFiles[i])
		}
		if f.StartChar != prevChar || f.EndChar <= f.StartChar {
			t.Fatalf("non-contiguous char span for %s: %+v (prevEnd=%d)", f.File, f, prevChar)
		}
		if f.StartLine < 1 || f.StartLine < prevEndLine || f.EndLine < f.StartLine {
			t.Fatalf("invalid line span for %s: %+v (prevEndLine=%d)", f.File, f, prevEndLine)
		}
		prevChar = f.EndChar
		prevEndLine = f.EndLine
	}
	if prevChar != sm.TotalChars {
		t.Fatalf("last EndChar = %d, want TotalChars = %d", prevChar, sm.TotalChars)
	}
	if sm.ScriptFile == "" || sm.ScriptFile != a.data.ScriptFile {
		t.Fatalf("sm.ScriptFile = %q, want %q", sm.ScriptFile, a.data.ScriptFile)
	}
	if a.data.FrameSourceMapFile == "" {
		t.Fatal("FrameSourceMapFile is empty")
	}
	frameBody, ok := a.files[a.data.FrameSourceMapFile]
	if !ok {
		t.Fatalf("missing %s in a.files", a.data.FrameSourceMapFile)
	}
	if !strings.HasPrefix(frameBody.body, "window.wsSourceMap=") {
		t.Fatalf("unexpected frame source map body prefix: %q", frameBody.body[:min(32, len(frameBody.body))])
	}
}

func TestAssembleDashboardScriptsNonASCIIBoundary(t *testing.T) {
	// Verify UTF-16 code unit counts (bmp 3-byte rune = 1 unit, astral emoji = 2 units)
	// and files missing trailing newlines stay exact across concatenation boundaries.
	orig := dashboardScriptFiles
	defer func() { dashboardScriptFiles = orig }()
	dashboardScriptFiles = []string{"a.js", "b.js", "c.js"}
	files := map[string]string{
		"a.js": "var x='…😀';\n", // 11 UTF-16 units: 7 ascii + 1 ('…') + 2 ('😀') + 2 ("';") + 1 ('\n') = 13? Verify below via utf16.Encode!
		"b.js": "var y=1;",       // No trailing newline
		"c.js": "var z=2;\n",     // Shares line 2 with b.js
	}
	raw, sm, err := assembleDashboardScripts(func(name string) ([]byte, error) {
		return []byte(files[name]), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != files["a.js"]+files["b.js"]+files["c.js"] {
		t.Fatalf("raw mismatch: %q", string(raw))
	}
	uA := len(utf16.Encode([]rune(files["a.js"])))
	uB := len(utf16.Encode([]rune(files["b.js"])))
	uC := len(utf16.Encode([]rune(files["c.js"])))
	if sm.TotalChars != uA+uB+uC || sm.TotalLines != 2 {
		t.Fatalf("totals = (%d chars, %d lines), want (%d, 2)", sm.TotalChars, sm.TotalLines, uA+uB+uC)
	}
	if sm.Files[0] != (scriptFileRange{File: "a.js", StartChar: 0, EndChar: uA, StartLine: 1, EndLine: 1}) {
		t.Fatalf("file 0 = %+v", sm.Files[0])
	}
	if sm.Files[1] != (scriptFileRange{File: "b.js", StartChar: uA, EndChar: uA + uB, StartLine: 2, EndLine: 2}) {
		t.Fatalf("file 1 = %+v", sm.Files[1])
	}
	if sm.Files[2] != (scriptFileRange{File: "c.js", StartChar: uA + uB, EndChar: uA + uB + uC, StartLine: 2, EndLine: 2}) {
		t.Fatalf("file 2 = %+v", sm.Files[2])
	}
}

func TestDevAssetsRecomputesSourceMap(t *testing.T) {
	baseA, err := loadDashboardAssets()
	if err != nil {
		t.Fatal(err)
	}
	dash, _ := devSource(t)
	editFile(t, dash("dashboard-jank.js"), "THRESHOLD_MS: STALL_THRESHOLD_MS,", "/* dev-assets-sourcemap-marker */\n    THRESHOLD_MS: STALL_THRESHOLD_MS,")
	devA, err := loadDashboardAssets()
	if err != nil {
		t.Fatal(err)
	}
	if devA.data.ScriptFile == baseA.data.ScriptFile || devA.data.FrameSourceMapFile == baseA.data.FrameSourceMapFile {
		t.Fatal("dev asset source map hash did not change after editing dashboard-jank.js")
	}
}

func TestSourceMapAndSysOpenPageDelivery(t *testing.T) {
	f := startPageFixture(t)
	a, err := loadDashboardAssets()
	if err != nil {
		t.Fatal(err)
	}

	// Standalone shell / index and standalone session include inline window.wsSourceMap before main script.
	for _, path := range []string{"/", "/s/livesession"} {
		res, err := testClient().Get(f.base + path)
		if err != nil {
			t.Fatal(err)
		}
		page := readBody(t, res)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("GET %s status = %d", path, res.StatusCode)
		}
		idxMap := strings.Index(page, "window.wsSourceMap =")
		idxScript := strings.Index(page, "var WsJank")
		if idxMap < 0 || idxScript <= idxMap {
			t.Fatalf("GET %s missing or out-of-order inline wsSourceMap (map=%d script=%d)", path, idxMap, idxScript)
		}
		if !strings.Contains(page, `data-sysopen="1"`) {
			t.Fatalf("GET %s missing data-sysopen on local viewer", path)
		}
	}

	// Framed session delivers external FrameSourceMapFile before ScriptFile.
	res, err := testClient().Get(f.base + "/s/livesession?frame=1")
	if err != nil {
		t.Fatal(err)
	}
	framePage := readBody(t, res)
	idxMapTag := strings.Index(framePage, `<script src="/s/`+a.data.FrameSourceMapFile+`"></script>`)
	idxMainTag := strings.Index(framePage, `<script src="/s/`+a.data.ScriptFile+`"></script>`)
	if idxMapTag < 0 || idxMainTag <= idxMapTag {
		t.Fatalf("framed session missing or out-of-order FrameSourceMapFile (map=%d main=%d)", idxMapTag, idxMainTag)
	}
}

func TestFrameScriptSourceMapSubFiles(t *testing.T) {
	a, err := loadDashboardAssets()
	if err != nil {
		t.Fatal(err)
	}
	var sm scriptSourceMapData
	if err := json.Unmarshal([]byte(a.data.ScriptSourceMap), &sm); err != nil {
		t.Fatal(err)
	}
	if sm.FrameScriptFile == "" || sm.FrameScriptFile != a.data.FrameScriptFile {
		t.Fatalf("sm.FrameScriptFile = %q, want %q", sm.FrameScriptFile, a.data.FrameScriptFile)
	}
	frameBody := a.files[a.data.FrameScriptFile].body
	if frameBody == "" {
		t.Fatal("empty FrameScriptFile body")
	}

	byName := map[string]scriptFileRange{}
	prevEndChar := 0
	prevEndLine := 1
	for _, r := range sm.FrameFiles {
		if r.StartChar < prevEndChar || r.EndChar <= r.StartChar {
			t.Fatalf("non-monotonic FrameFiles char range for %s: %+v (prevEnd=%d)", r.File, r, prevEndChar)
		}
		if r.StartLine < prevEndLine || r.EndLine < r.StartLine {
			t.Fatalf("non-monotonic FrameFiles line range for %s: %+v (prevEndLine=%d)", r.File, r, prevEndLine)
		}
		prevEndChar = r.EndChar
		prevEndLine = r.EndLine
		if _, dup := byName[r.File]; dup {
			t.Fatalf("duplicate FrameFiles entry %q", r.File)
		}
		byName[r.File] = r
	}

	expected := []string{
		"confirm.js", "answers.js", "form.js", "code.js", "pill.js", "wb/rail.js",
		"stream.js", "stream-view.js", "panel-diff.js", "card-find.js",
		"mermaid-init.js", "svg-view.js", "viewer.js", "toast.js", "hold-confirm.js",
		"tracker.js", "tracker-mount.js", "mention.js", "ws-input.js",
		"rail.js", "memo.js", "usage.js", "usage-charts.js", "usage-popover.js", "shell.js", "gallery.js", "cards.js",
		"overview.js", "preview.js", "panelcard.js", "files.js",
	}
	lines := strings.Split(frameBody, "\n")
	for _, name := range expected {
		r, ok := byName[name]
		if !ok {
			t.Fatalf("missing %q in sm.FrameFiles", name)
		}
		if r.EndLine > len(lines) {
			t.Fatalf("%s EndLine=%d exceeds total lines=%d", name, r.EndLine, len(lines))
		}
	}

	// Verify specific sub-file lines map directly to their source lines inside FrameScriptFile.
	cardsRange := byName["cards.js"]
	cardsFirstLine := lines[cardsRange.StartLine-1]
	if !strings.HasPrefix(cardsFirstLine, "/*") && !strings.Contains(cardsFirstLine, "use strict") && !strings.Contains(string(a.data.CardsScript), cardsFirstLine) {
		t.Fatalf("unexpected cards.js start line %q at line %d", cardsFirstLine, cardsRange.StartLine)
	}
	svRange := byName["stream-view.js"]
	svSlice := strings.Join(lines[svRange.StartLine-1:svRange.EndLine], "\n")
	if !strings.Contains(string(a.data.FormScript), svSlice) {
		t.Fatalf("stream-view.js mapped line slice not contained in FormScript")
	}
	if strings.Count(frameBody, svSlice) != 1 {
		t.Fatalf("stream-view.js slice appeared %d times in FrameScriptFile, want 1", strings.Count(frameBody, svSlice))
	}
}
