package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

func TestWhiteboardMockupEndToEndMCPAndDashboardSource(t *testing.T) {
	f := newWhiteboardFixture(t)
	mockDir := filepath.Join(f.root, "ai-docs", "assets", "mockups", "261126", "e2e")
	if err := os.MkdirAll(mockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mockDir, "index.html"), []byte(`<!DOCTYPE html><html><head><link rel="stylesheet" href="app.css"></head><body><h1>E2E</h1></body></html>`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mockDir, "app.css"), []byte(`h1 { color: teal; }`), 0o644); err != nil {
		t.Fatal(err)
	}

	resp := f.call(t, "whiteboard.update", map[string]any{
		"session_key": f.key,
		"commands":    `<wb:topic name="Design"><wb:panel name="e2e" recap="E2E mockup" src="ai-docs/assets/mockups/261126/e2e/index.html" /></wb:topic>`,
	})
	if isToolError(resp) {
		t.Fatalf("whiteboard.update failed: %s", toolText(t, resp))
	}

	src := NewDashboardSessionSource(f.s.sessions.boards)
	var evicted string
	src.SetMockupEvictor(func(boardDir string) { evicted = boardDir })
	reg := wsdashboard.Registration{Project: f.root, ReplyID: wsmailbox.ReplyID(f.key)}

	page, ok, err := src.ReadPage(reg)
	if err != nil || !ok || !page.HasBoard {
		t.Fatalf("ReadPage = (ok=%v hasBoard=%v err=%v)", ok, page.HasBoard, err)
	}
	boardHTML := string(page.Board.Board)
	if !strings.Contains(boardHTML, `data-mockup-src="/s/`+reg.ReplyID+`/board/mockup?entry=e1"`) {
		t.Fatalf("rendered board missing stable mockup src: %s", boardHTML)
	}
	if strings.Contains(boardHTML, "/fs/mock/") || strings.Contains(boardHTML, "allow-same-origin") {
		t.Fatalf("rendered board leaked /fs/mock/ or allow-same-origin")
	}

	meta, found, err := src.LookupMockup(reg, "e1")
	if err != nil || !found || meta.RootFile != "index.html" || meta.SHA256 == "" {
		t.Fatalf("LookupMockup = (%+v, %v, %v)", meta, found, err)
	}
	bundle, found, err := src.ReadMockupBundle(reg, "e1")
	if err != nil || !found || bundle.SHA256 != meta.SHA256 {
		t.Fatalf("ReadMockupBundle = (%+v, %v, %v)", bundle, found, err)
	}

	archiveBytes, ok, err := src.BoardArchive(reg)
	if err != nil || !ok || len(archiveBytes) == 0 {
		t.Fatalf("BoardArchive = (len=%d ok=%v err=%v)", len(archiveBytes), ok, err)
	}
	zipPath := filepath.Join(t.TempDir(), "export.zip")
	if err := os.WriteFile(zipPath, archiveBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	loadResp := f.call(t, "whiteboard.load", map[string]any{
		"session_key": f.key,
		"path":        zipPath,
	})
	if isToolError(loadResp) {
		t.Fatalf("whiteboard.load failed: %s", toolText(t, loadResp))
	}
	if evicted == "" {
		t.Fatalf("expected whiteboard.load to trigger SetMockupEvictor")
	}
	meta2, found, err := src.LookupMockup(reg, "e1")
	if err != nil || !found || meta2.SHA256 != meta.SHA256 {
		t.Fatalf("LookupMockup after load = (%+v, %v, %v)", meta2, found, err)
	}
}
