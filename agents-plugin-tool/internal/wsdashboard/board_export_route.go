// Board export route writes a session's board zip to a local export folder
// for handoff prompts and prunes old archives asynchronously-free under a lock.

package wsdashboard

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsmailbox"
)

const (
	boardExportDir              = "board-exports"
	boardExportFileAge          = 7 * 24 * time.Hour
	boardExportFilesTotal int64 = 100 << 20
)

type boardExportMailbox struct {
	ID      string `json:"id"`
	ReplyID string `json:"reply_id"`
	Dir     string `json:"dir"`
	Root    string `json:"root,omitempty"`
	Branch  string `json:"branch,omitempty"`
	Harness string `json:"harness,omitempty"`
	Label   string `json:"label,omitempty"`
}

type boardExportResponse struct {
	Path       string             `json:"path"`
	Filename   string             `json:"filename"`
	ReplyID    string             `json:"reply_id"`
	MailboxRef string             `json:"mailbox_ref"`
	Mailbox    boardExportMailbox `json:"mailbox"`
}

// handleBoardExport serves POST /s/<reply-id>/board-export for same-origin
// localhost callers: it writes the session's board zip under
// <d.dir>/board-exports/, prunes stale/oversized old exports, and returns the
// absolute path alongside session mailbox metadata matching SessionView.MailboxRef.
func (d *Daemon) handleBoardExport(w http.ResponseWriter, r *http.Request, replyID string) {
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !own || crossSiteFetch(r) {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	s, root, branch, data, status, msg := d.resolveBoardArchive(replyID)
	if status != http.StatusOK {
		http.Error(w, msg, status)
		return
	}
	if d.dir == "" {
		http.Error(w, "export directory unconfigured", http.StatusInternalServerError)
		return
	}
	fullPath, fileName, err := d.writeBoardExport(s, branch, data)
	if err != nil {
		http.Error(w, "export failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	open := mailboxOpen(s.ReplyID, s.Project, root, branch, s.Harness)
	mref := mailboxRef(open, s.Label)
	a, b := filepath.Clean(root), filepath.Clean(s.Project)
	if root == "" || a == b || (runtime.GOOS == "windows" && strings.EqualFold(a, b)) {
		root = ""
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, boardExportResponse{
		Path:       fullPath,
		Filename:   fileName,
		ReplyID:    s.ReplyID,
		MailboxRef: mref,
		Mailbox: boardExportMailbox{
			ID:      wsmailbox.ReplyIDPrefix + s.ReplyID,
			ReplyID: s.ReplyID,
			Dir:     s.Project,
			Root:    root,
			Branch:  branch,
			Harness: s.Harness,
			Label:   s.Label,
		},
	})
}

func (d *Daemon) writeBoardExport(s Session, branch string, data []byte) (fullPath, fileName string, err error) {
	d.boardExportMu.Lock()
	defer d.boardExportMu.Unlock()

	now := time.Now()
	if d.boardExportNow != nil {
		now = d.boardExportNow()
	}
	dir := filepath.Join(d.dir, boardExportDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", "", err
	}
	name, err := boardExportUniqueName(dir, boardArchiveName(s, branch, now))
	if err != nil {
		return "", "", err
	}
	dest := filepath.Join(dir, name)
	tmp, err := os.CreateTemp(dir, ".board-export-*.tmp")
	if err != nil {
		return "", "", err
	}
	tmpPath := tmp.Name()
	kept := false
	defer func() {
		if !kept {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", "", err
	}
	_ = os.Chmod(tmpPath, 0o600)
	if err := os.Rename(tmpPath, dest); err != nil {
		return "", "", err
	}
	kept = true

	maxBytes := d.boardExportMaxBytes
	if maxBytes <= 0 {
		maxBytes = boardExportFilesTotal
	}
	boardExportPruneWith(dir, now, maxBytes, name, os.Remove, func(path string, rerr error) {
		if d.log != nil {
			fmt.Fprintf(d.log, "dashboard: board export %s not removed: %v\n", filepath.Base(path), rerr)
		}
	})

	if abs, err := filepath.Abs(dest); err == nil {
		dest = abs
	}
	return dest, name, nil
}

func boardExportUniqueName(dir, base string) (string, error) {
	stem := strings.TrimSuffix(base, ".zip")
	for i := 1; i <= 10000; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s_%d.zip", stem, i)
		}
		_, err := os.Stat(filepath.Join(dir, candidate))
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("too many board export collisions for %s", base)
}

func boardExportPruneWith(dir string, now time.Time, maxBytes int64, keepName string, remove func(string) error, fail func(path string, err error)) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		name string
		mod  time.Time
		size int64
	}
	var files []file
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".zip") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.Name() != keepName && now.Sub(info.ModTime()) > boardExportFileAge {
			path := filepath.Join(dir, e.Name())
			if rerr := remove(path); composerRemoved(rerr) {
				continue
			} else if fail != nil {
				fail(path, rerr)
			}
		}
		files = append(files, file{name: e.Name(), mod: info.ModTime(), size: info.Size()})
		total += info.Size()
	}
	if total <= maxBytes {
		return
	}
	sort.Slice(files, func(a, b int) bool { return files[a].mod.Before(files[b].mod) })
	for i := range files {
		if total <= maxBytes {
			break
		}
		if files[i].name == keepName {
			continue
		}
		path := filepath.Join(dir, files[i].name)
		if rerr := remove(path); composerRemoved(rerr) {
			total -= files[i].size
		} else if fail != nil {
			fail(path, rerr)
		}
	}
}
