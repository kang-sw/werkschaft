package wsdashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

func bexpPost(t *testing.T, url, origin, fetchSite string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if fetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", fetchSite)
	}
	resp, err := testClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, string(body)
}

func TestBoardExportRouteWritesArchiveAndMailboxMetadata(t *testing.T) {
	f := startPageFixture(t)
	now := time.Date(2026, 11, 27, 14, 15, 0, 0, time.Local)
	f.d.boardExportNow = func() time.Time { return now }
	f.src.mu.Lock()
	f.src.boards["boardsession"] = closedTopicsBoard(1)
	f.src.mu.Unlock()

	code, hdr, raw := bexpPost(t, f.base+"/s/boardsession/board-export", f.base, "same-origin")
	if code != http.StatusOK {
		t.Fatalf("POST board-export = %d, want 200: %s", code, raw)
	}
	if got := hdr.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	var res1 boardExportResponse
	if err := json.Unmarshal([]byte(raw), &res1); err != nil {
		t.Fatalf("decode JSON: %v (%s)", err, raw)
	}
	if !filepath.IsAbs(res1.Path) {
		t.Errorf("path %q is not absolute", res1.Path)
	}
	exportRoot := filepath.Join(f.dir, boardExportDir)
	rel, err := filepath.Rel(exportRoot, res1.Path)
	if err != nil || strings.HasPrefix(rel, "..") {
		t.Errorf("path %q not inside %q (rel=%q, err=%v)", res1.Path, exportRoot, rel, err)
	}
	if filepath.Base(res1.Path) != res1.Filename {
		t.Errorf("filename %q != basename(%q)", res1.Filename, res1.Path)
	}
	gotBytes, err := os.ReadFile(res1.Path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	if string(gotBytes) != "PK-zip-of-boardsession" {
		t.Errorf("exported zip = %q, want %q", gotBytes, "PK-zip-of-boardsession")
	}
	if res1.ReplyID != "boardsession" || res1.Mailbox.ID != "id:boardsession" || res1.Mailbox.ReplyID != "boardsession" {
		t.Errorf("unexpected mailbox ids: %+v", res1)
	}
	if res1.Mailbox.Dir != f.project {
		t.Errorf("mailbox.dir = %q, want %q", res1.Mailbox.Dir, f.project)
	}

	// Second export in the same minute gets a collision-free suffix without overwriting.
	code, _, raw2 := bexpPost(t, f.base+"/s/boardsession/board-export", f.base, "same-origin")
	if code != http.StatusOK {
		t.Fatalf("second POST board-export = %d: %s", code, raw2)
	}
	var res2 boardExportResponse
	if err := json.Unmarshal([]byte(raw2), &res2); err != nil {
		t.Fatal(err)
	}
	if res2.Path == res1.Path || !strings.HasSuffix(res2.Filename, "_2.zip") {
		t.Errorf("second export path = %q (%q), want _2.zip distinct from %q", res2.Path, res2.Filename, res1.Path)
	}
	if _, err := os.Stat(res1.Path); err != nil {
		t.Errorf("first export removed unexpectedly: %v", err)
	}
	if _, err := os.Stat(res2.Path); err != nil {
		t.Errorf("second export missing: %v", err)
	}

	// Legacy GET /board.zip download route remains completely intact (T0).
	getCode, getBody := get(t, f.base+"/s/boardsession/board.zip")
	if getCode != http.StatusOK || getBody != "PK-zip-of-boardsession" {
		t.Errorf("GET board.zip = %d %q, want 200 PK-zip-of-boardsession", getCode, getBody)
	}
}

func TestBoardExportRouteGuards(t *testing.T) {
	f := startPageFixture(t)
	f.src.mu.Lock()
	f.src.boards["boardsession"] = closedTopicsBoard(1)
	f.src.mu.Unlock()

	// Wrong HTTP method: GET answers 405.
	if code, _ := get(t, f.base+"/s/boardsession/board-export"); code != http.StatusMethodNotAllowed {
		t.Errorf("GET board-export = %d, want 405", code)
	}

	// Missing, opaque, or foreign Origin -> 403.
	for _, origin := range []string{"", "null", "http://evil.example", "http://127.0.0.1:" + strconv.Itoa(f.d.Endpoint().Port+1)} {
		if code, _, msg := bexpPost(t, f.base+"/s/boardsession/board-export", origin, "same-origin"); code != http.StatusForbidden {
			t.Errorf("Origin %q = %d, want 403 (%s)", origin, code, msg)
		}
	}

	// Cross-site Sec-Fetch-Site -> 403 even with own Origin.
	if code, _, msg := bexpPost(t, f.base+"/s/boardsession/board-export", f.base, "cross-site"); code != http.StatusForbidden {
		t.Errorf("Sec-Fetch-Site=cross-site = %d, want 403 (%s)", code, msg)
	}

	// Remote/non-local caller -> 404 (TrustedWrite masks existence from remote peers).
	remoteReq := httptest.NewRequest(http.MethodPost, f.base+"/s/boardsession/board-export", strings.NewReader("{}"))
	remoteReq.RemoteAddr = "203.0.113.9:54321"
	remoteReq.Header.Set("Origin", f.base)
	remoteRec := httptest.NewRecorder()
	f.d.handleBoardExport(remoteRec, remoteReq, "boardsession")
	if remoteRec.Code != http.StatusNotFound {
		t.Errorf("non-local caller = %d, want 404", remoteRec.Code)
	}

	// Host anchor, unknown session, and session without a board -> 404.
	for _, rid := range []string{LocalBrowseAnchor, "nosuchsession", "livesession"} {
		if code, _, msg := bexpPost(t, f.base+"/s/"+rid+"/board-export", f.base, "same-origin"); code != http.StatusNotFound {
			t.Errorf("rid %q = %d, want 404 (%s)", rid, code, msg)
		}
	}
}

func TestBoardExportPrunesStaleAndOversizedKeepingLatest(t *testing.T) {
	f := startPageFixture(t)
	now := time.Date(2026, 11, 27, 15, 0, 0, 0, time.Local)
	f.d.boardExportNow = func() time.Time { return now }
	f.d.boardExportMaxBytes = 15 // smaller than the newly exported 22-byte archive
	f.src.mu.Lock()
	f.src.boards["boardsession"] = closedTopicsBoard(1)
	f.src.mu.Unlock()

	exportDir := filepath.Join(f.dir, boardExportDir)
	if err := os.MkdirAll(exportDir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeAged := func(name string, payload string, age time.Duration) string {
		p := filepath.Join(exportDir, name)
		if err := os.WriteFile(p, []byte(payload), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := now.Add(-age)
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
		return p
	}
	expiredPath := writeAged("old-expired.zip", "1234567890", 8*24*time.Hour)
	olderOverCapPath := writeAged("recent-older.zip", "1234567890", 2*time.Hour)
	nonZipPath := writeAged("note.txt", "keep-me", 30*24*time.Hour)

	code, _, raw := bexpPost(t, f.base+"/s/boardsession/board-export", f.base, "")
	if code != http.StatusOK {
		t.Fatalf("POST board-export = %d: %s", code, raw)
	}
	var res boardExportResponse
	if err := json.Unmarshal([]byte(raw), &res); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(expiredPath); !os.IsNotExist(err) {
		t.Errorf("expired zip should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(olderOverCapPath); !os.IsNotExist(err) {
		t.Errorf("over-cap older zip should be removed, stat err = %v", err)
	}
	if _, err := os.Stat(nonZipPath); err != nil {
		t.Errorf("non-zip file must not be pruned: %v", err)
	}
	if _, err := os.Stat(res.Path); err != nil {
		t.Errorf("newly exported file %q must never be pruned: %v", res.Path, err)
	}
}

func TestBoardExportMailboxRefParityWithSessionView(t *testing.T) {
	worktreeDir := filepath.Join(t.TempDir(), "wt")
	repoDir := filepath.Join(t.TempDir(), "repo")
	if runtime.GOOS == "windows" {
		worktreeDir = filepath.Clean(worktreeDir)
		repoDir = filepath.Clean(repoDir)
	}

	cases := []struct {
		name     string
		project  string
		topLevel string
		branch   string
		detached bool
		harness  string
		label    string
		wantRoot string
	}{
		{
			name:     "same root omits root and escapes label",
			project:  repoDir,
			topLevel: repoDir,
			branch:   "develop",
			harness:  "claude",
			label:    `Lead <alpha & "beta">`,
			wantRoot: "",
		},
		{
			name:     "worktree differs from root and empty label self-closes",
			project:  worktreeDir,
			topLevel: repoDir,
			branch:   "impl/develop/foo",
			harness:  "codex",
			label:    "",
			wantRoot: repoDir,
		},
		{
			name:     "detached head omits branch",
			project:  repoDir,
			topLevel: repoDir,
			branch:   "HEAD",
			detached: true,
			harness:  "pi",
			label:    "worker",
			wantRoot: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := startPageFixture(t)
			now := time.Now()
			rid := "paritysession"
			reg := Registration{
				SchemaVersion: 1,
				ReplyID:       rid,
				Project:       tc.project,
				Harness:       tc.harness,
				HeartbeatAt:   now.UTC().Format(time.RFC3339),
			}
			f.d.index.mu.Lock()
			f.d.index.sessions[rid] = &indexEntry{session: Session{Registration: reg, Label: tc.label, HasBoard: true}}
			f.d.index.mu.Unlock()
			if f.d.git == nil {
				f.d.git = newGitWatcher(nil, nil, nil)
			}
			f.d.git.mu.Lock()
			f.d.git.roots[gitKey(tc.project)] = &gitRoot{
				badge: &GitBadge{
					State:    gitOK,
					Root:     tc.project,
					TopLevel: tc.topLevel,
					Repo:     wslive.Repo{Branch: tc.branch, Detached: tc.detached},
				},
			}
			f.d.git.mu.Unlock()
			f.src.mu.Lock()
			f.src.boards[rid] = closedTopicsBoard(1)
			f.src.mu.Unlock()

			code, _, raw := bexpPost(t, f.base+"/s/"+rid+"/board-export", f.base, "same-origin")
			if code != http.StatusOK {
				t.Fatalf("POST board-export = %d: %s", code, raw)
			}
			var res boardExportResponse
			if err := json.Unmarshal([]byte(raw), &res); err != nil {
				t.Fatal(err)
			}
			sv := SessionView{
				ReplyID: rid,
				Project: tc.project,
				Harness: tc.harness,
				Label:   tc.label,
				Git: &GitBadge{
					State:    gitOK,
					TopLevel: tc.topLevel,
					Repo:     wslive.Repo{Branch: tc.branch, Detached: tc.detached},
				},
			}
			if want := sv.MailboxRef(); res.MailboxRef != want {
				t.Errorf("mailbox_ref = %q, want SessionView.MailboxRef %q", res.MailboxRef, want)
			}
			if res.Mailbox.Root != tc.wantRoot {
				t.Errorf("mailbox.root = %q, want %q", res.Mailbox.Root, tc.wantRoot)
			}
			wantBranch := tc.branch
			if tc.detached {
				wantBranch = ""
			}
			if res.Mailbox.Branch != wantBranch {
				t.Errorf("mailbox.branch = %q, want %q", res.Mailbox.Branch, wantBranch)
			}
		})
	}
}
