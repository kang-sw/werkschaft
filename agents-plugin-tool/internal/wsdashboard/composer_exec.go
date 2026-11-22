// composer_exec.go is the dashboard side of the chat composer's `!` command:
// a user typing `!<bash line>` in an open hosted lead's chat runs the line as
// that lead's exec job (the command layer binds the service), and the result
// reaches the lead's context passively. The route owns the grammar, the
// submission-ID claims, the private result card and the output file; it
// never routes the line through the inbox.
package wsdashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/execjob"
)

// Composer exec bounds (the product contract's fixed constants).
const (
	// ComposerWindow is how long a composer run is waited for before it
	// converts to async.
	ComposerWindow = 10 * time.Second
	// ComposerInlineMax is the largest raw combined output attached inline.
	ComposerInlineMax = 4096
	// composerFileMax, composerFileAge and composerFilesTotal bound the
	// output files: per run, by age, and across files.
	composerFileMax    = 1 << 20
	composerFileAge    = 7 * 24 * time.Hour
	composerFilesTotal = 100 << 20
	// composerOutputDir names the output files' directory under the state
	// directory.
	composerOutputDir = "composer-exec"
	// composerKeepCards bounds the cards one session keeps in memory.
	composerKeepCards = 64
	// composerBodyLimit bounds a composer-exec request body.
	composerBodyLimit = 256 << 10
)

// ComposerLaunch is one `!` line's launch for an open hosted lead.
type ComposerLaunch struct {
	ReplyID string
	Line    string
	// Window is the wait before conversion.
	Window time.Duration
}

// ComposerJob is a composer run's job as the card and the receipt read it.
// Output is the whole retained combined output once the job ended (nil
// while it runs).
type ComposerJob struct {
	ExecID   string
	Gen      uint64
	State    string // running, exited, stopped, failed or lost
	ExitCode *int
	Signal   *int
	Error    string
	Command  string
	// Async: the job converted (or started) as an async job.
	Async     bool
	StartedAt int64
	EndedAt   int64
	// Written is the combined output's raw byte count; Start the offset the
	// retained Output begins at (nonzero once earlier output was evicted).
	Written int64
	Start   int64
	Output  []byte
}

// Terminal reports whether the job ended.
func (j ComposerJob) Terminal() bool { return j.State != "running" && j.State != "" }

// ComposerExecService is the dashboard's narrow dependency on the daemon's
// hosted exec jobs and the agent runner, bound by the command layer.
// Everything about owner, generation, worktree and environment comes from
// server-side runner state; none of it from the browser.
type ComposerExecService interface {
	// Launch runs req.Line for lead req.ReplyID under that lead's own job
	// owner, bound worktree and environment, as the user's own one-use
	// approval, and waits through req.Window; drain ends the wait without
	// stopping the job (a daemon replacement). A job that converts hands its
	// id to the lead's pending input before it can owe a notice. The
	// returned job is empty when nothing was admitted.
	Launch(ctx context.Context, drain <-chan struct{}, req ComposerLaunch) (ComposerJob, error)
	// Job returns the lead's job execID (its output once ended), refusing
	// another owner's.
	Job(replyID, execID string) (ComposerJob, error)
	// Hand puts a receipt of job execID into the lead's pending input
	// under open generation gen, for its next accepted send; it starts no
	// turn.
	Hand(replyID string, gen uint64, execID, text string) error
}

// composerExec is the daemon's composer state: the bound service, the claims
// and the output files.
type composerExec struct {
	svc atomic.Pointer[ComposerExecService]

	mu   sync.Mutex
	subs map[string]*composerSub
	// order keeps each session's claims oldest first (composerKeepCards).
	order map[string][]string
}

// SetComposerExec binds the service the composer route runs on; before it
// the route answers unavailable.
func (d *Daemon) SetComposerExec(svc ComposerExecService) {
	if svc == nil {
		d.composer.svc.Store(nil)
		return
	}
	d.composer.svc.Store(&svc)
}

// composerSub is one claimed submission: the submission ID maps to exactly
// one job. Guarded by mu.
type composerSub struct {
	mu      sync.Mutex
	reply   string
	id      string
	line    string
	created time.Time

	phase string // running, async, done, refused
	job   ComposerJob
	// err is a refusal or a failed start's reason.
	err string
	// finalized: the result was classified (inline text or file) once.
	finalized bool
	inline    string
	replaced  bool
	file      string // the output file's path once it exists
	fileErr   string
	// handed: the receipt went to the lead's pending input.
	handed bool
}

var composerSubmissionID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// composerCard is a submission's private projection for its page.
type composerCard struct {
	ID      string `json:"id"`
	Command string `json:"command"`
	// Phase is running, async (converted; still running or ended since),
	// done or refused.
	Phase       string `json:"phase"`
	ExecID      string `json:"exec_id,omitempty"`
	State       string `json:"state,omitempty"`
	ExitCode    *int   `json:"exit_code,omitempty"`
	Signal      *int   `json:"signal,omitempty"`
	Error       string `json:"error,omitempty"`
	Async       bool   `json:"async,omitempty"`
	Output      string `json:"output,omitempty"`
	OutputBytes int64  `json:"output_bytes"`
	// Replaced: invalid UTF-8 bytes were decoded as U+FFFD.
	Replaced bool `json:"replaced,omitempty"`
	// File is the output file's path and FileURL its owner-checked route,
	// shown only once the file exists; FileError says why a large result
	// has none.
	File      string `json:"file,omitempty"`
	FileURL   string `json:"file_url,omitempty"`
	FileError string `json:"file_error,omitempty"`
	// Truncated is how many leading bytes the retention dropped.
	Truncated int64 `json:"truncated,omitempty"`
	StartedAt int64 `json:"started_at,omitempty"`
	EndedAt   int64 `json:"ended_at,omitempty"`
}

func (s *composerSub) cardLocked() composerCard {
	c := composerCard{ID: s.id, Command: s.line, Phase: s.phase, Error: s.err, Async: s.job.Async, OutputBytes: s.job.Written}
	c.ExecID, c.State, c.ExitCode, c.Signal = s.job.ExecID, s.job.State, s.job.ExitCode, s.job.Signal
	c.StartedAt, c.EndedAt = s.job.StartedAt, s.job.EndedAt
	if s.job.Error != "" && c.Error == "" {
		c.Error = s.job.Error
	}
	if s.finalized {
		c.Output, c.Replaced, c.File, c.FileError = s.inline, s.replaced, s.file, s.fileErr
		if s.file != "" {
			c.FileURL = "/s/" + s.reply + "/composer-exec/" + s.id + "/output"
		}
		c.Truncated = s.job.Start
	}
	return c
}

// composerRoute serves /s/<reply-id>/composer-exec and its children. It is
// localhost-only: the same gate as every write that carries execution
// authority (TrustedWrite), plus the daemon's own Origin for writes, and a
// non-cross-site fetch for reads. An unknown daemon route would fail closed
// in the client; this one never falls back to the message route.
func (d *Daemon) handleComposerExec(w http.ResponseWriter, r *http.Request, replyID, rest string) {
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	rest = strings.TrimPrefix(rest, "/")
	write := r.Method == http.MethodPost
	if write && !own {
		http.Error(w, "cross-origin request refused", http.StatusForbidden)
		return
	}
	if !write && (r.Method != http.MethodGet && r.Method != http.MethodHead || crossSiteFetch(r) || (r.Header.Get("Origin") != "" && !own)) {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if d.index == nil || !ValidReplyID(replyID) {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	s, ok := d.index.Session(replyID)
	if !ok || !s.Hosted {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	svcp := d.composer.svc.Load()
	if svcp == nil {
		http.Error(w, "dashboard commands are unavailable", http.StatusServiceUnavailable)
		return
	}
	svc := *svcp
	switch {
	case rest == "" && write:
		d.composerSubmit(w, r, svc, replyID)
	case rest == "" && !write:
		d.composerList(w, svc, replyID)
	case !write:
		id, tail, _ := strings.Cut(rest, "/")
		switch tail {
		case "":
			d.composerShow(w, svc, replyID, id)
		case "output":
			d.composerOutput(w, r, replyID, id)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// composerRequest is the composer-exec body: the raw first-character
// candidate text and its submission ID. Attachment fields are accepted only
// to be refused.
type composerRequest struct {
	SubmissionID string            `json:"submission_id"`
	Text         string            `json:"text"`
	Images       []json.RawMessage `json:"images"`
	Refs         []json.RawMessage `json:"refs"`
}

// composerLine applies the `!` grammar to a raw composer text: the first
// byte only (a leading space is an ordinary prompt), exactly one byte
// stripped, a payload that is not blank.
func composerLine(text string) (line string, status int, msg string) {
	if !strings.HasPrefix(text, "!") {
		return "", http.StatusBadRequest, "not a command: a command starts with ! as its first character"
	}
	line = text[1:]
	if strings.TrimSpace(line) == "" {
		return "", http.StatusBadRequest, "nothing to run after !"
	}
	if !utf8.ValidString(line) {
		return "", http.StatusBadRequest, "the command is not valid text"
	}
	return line, 0, ""
}

func (d *Daemon) composerSubmit(w http.ResponseWriter, r *http.Request, svc ComposerExecService, replyID string) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, composerBodyLimit+1))
	if err != nil || len(raw) > composerBodyLimit {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	var req composerRequest
	if json.Unmarshal(raw, &req) != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	if !composerSubmissionID.MatchString(req.SubmissionID) {
		http.Error(w, "a submission id is required", http.StatusBadRequest)
		return
	}
	if len(req.Images)+len(req.Refs) > 0 {
		http.Error(w, "a command takes no attachments: remove them or send it as a message", http.StatusBadRequest)
		return
	}
	line, status, msg := composerLine(req.Text)
	if status != 0 {
		http.Error(w, msg, status)
		return
	}
	sub, fresh := d.composerClaim(replyID, req.SubmissionID, line)
	if !fresh {
		// A retry after a lost response: the claim maps to its one job.
		sub.mu.Lock()
		same := sub.line == line
		card := sub.cardLocked()
		sub.mu.Unlock()
		if !same {
			http.Error(w, "this submission id was used for another command", http.StatusConflict)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, card)
		return
	}
	// The run happens after the acknowledgement, under the daemon's own
	// lifetime: closing the tab does not cancel a submitted command.
	go d.composerRun(svc, sub)
	sub.mu.Lock()
	card := sub.cardLocked()
	sub.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusAccepted, card)
}

// composerClaim claims submission id of session replyID for line, or returns
// the claim an earlier request made (fresh false).
func (d *Daemon) composerClaim(replyID, id, line string) (*composerSub, bool) {
	key := replyID + "\x00" + id
	c := &d.composer
	c.mu.Lock()
	defer c.mu.Unlock()
	if sub, ok := c.subs[key]; ok {
		return sub, false
	}
	if c.subs == nil {
		c.subs, c.order = map[string]*composerSub{}, map[string][]string{}
	}
	sub := &composerSub{reply: replyID, id: id, line: line, created: time.Now(), phase: "running"}
	c.subs[key] = sub
	c.order[replyID] = append(c.order[replyID], key)
	// Keep the newest cards; an unfinished one is never dropped.
	for len(c.order[replyID]) > composerKeepCards {
		oldest := c.order[replyID][0]
		old := c.subs[oldest]
		old.mu.Lock()
		active := old.phase == "running" || (old.phase == "async" && !old.finalized)
		old.mu.Unlock()
		if active {
			break
		}
		delete(c.subs, oldest)
		c.order[replyID] = c.order[replyID][1:]
	}
	return sub, true
}

func (d *Daemon) composerFind(replyID, id string) *composerSub {
	c := &d.composer
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subs[replyID+"\x00"+id]
}

// composerRun launches the claimed submission and settles its card; a run
// that ended inside the window is classified and handed to the lead.
func (d *Daemon) composerRun(svc ComposerExecService, sub *composerSub) {
	job, err := svc.Launch(context.Background(), d.stop, ComposerLaunch{ReplyID: sub.reply, Line: sub.line, Window: ComposerWindow})
	sub.mu.Lock()
	if job.ExecID == "" {
		sub.phase, sub.err = "refused", composerErrorText(err)
		sub.mu.Unlock()
		return
	}
	sub.job = job
	if err != nil {
		sub.err = composerErrorText(err)
	}
	switch {
	case job.Terminal():
		sub.phase = "done"
		d.composerFinalizeLocked(sub, true, svc)
	default:
		sub.phase, sub.job.Async = "async", true
	}
	sub.mu.Unlock()
}

func composerErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimPrefix(err.Error(), "exec: ")
}

// composerFinalizeLocked classifies a terminal job's retained output once:
// 4096 raw combined bytes or fewer inline, more as a private file. hand
// sends the receipt to the lead (a run that ended inside its window; an
// async job's end is the ordinary exec notice's). sub.mu is held.
func (d *Daemon) composerFinalizeLocked(sub *composerSub, hand bool, svc ComposerExecService) {
	if sub.finalized {
		return
	}
	job := sub.job
	sub.finalized = true
	// The count is the raw combined bytes the job wrote, never a decoded or
	// tail length.
	if job.Written <= ComposerInlineMax {
		text, replaced := decodeComposerText(job.Output)
		sub.inline, sub.replaced = text, replaced
	} else {
		data := job.Output
		if len(data) > composerFileMax {
			data = data[len(data)-composerFileMax:]
			sub.job.Start += int64(len(job.Output) - len(data))
		}
		text, replaced := decodeComposerText(data)
		sub.replaced = replaced
		path, err := d.composerWriteFile(sub.reply, sub.id, []byte(text))
		if err != nil {
			sub.fileErr = "the output file could not be written: " + err.Error()
			// The card still shows the tail, as the exec result does.
			tail := job.Output
			if len(tail) > ComposerInlineMax {
				tail = tail[len(tail)-ComposerInlineMax:]
			}
			sub.inline, _ = decodeComposerText(tail)
		} else {
			sub.file = path
		}
	}
	if hand && !sub.handed {
		if err := svc.Hand(sub.reply, sub.job.Gen, sub.job.ExecID, composerReceipt(sub)); err != nil {
			fmt.Fprintf(d.log, "dashboard: composer result for %s not handed: %v\n", sub.reply, err)
		} else {
			sub.handed = true
		}
	}
}

// decodeComposerText decodes output bytes for display, each invalid UTF-8
// byte as U+FFFD (the exec reply's decoding).
func decodeComposerText(b []byte) (string, bool) { return execjob.DecodeText(b) }

// composerReceipt is the text the lead's next turn carries for a finished
// composer run: the command, how it ended, and its output inline or as a
// file path.
func composerReceipt(sub *composerSub) string {
	job := sub.job
	var b strings.Builder
	fmt.Fprintf(&b, "[user ran from the dashboard] exec_id %s %s", job.ExecID, composerEnd(job))
	if job.StartedAt > 0 && job.EndedAt >= job.StartedAt {
		fmt.Fprintf(&b, " after %s", (time.Duration(job.EndedAt-job.StartedAt) * time.Millisecond).Round(100*time.Millisecond))
	}
	b.WriteString(".\ncommand: " + sub.line + "\n")
	switch {
	case sub.file != "":
		fmt.Fprintf(&b, "output (%d bytes) is in the file: %s\n", job.Written, sub.file)
		if job.Start > 0 {
			fmt.Fprintf(&b, "the file starts at byte %d of the output; earlier bytes were not retained.\n", job.Start)
		}
	case sub.fileErr != "":
		fmt.Fprintf(&b, "%s; output tail (%d bytes of %d):\n%s\n", sub.fileErr, len(sub.inline), job.Written, sub.inline)
	case job.Written == 0:
		b.WriteString("no output.\n")
	default:
		fmt.Fprintf(&b, "output:\n%s", sub.inline)
		if !strings.HasSuffix(sub.inline, "\n") {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// ComposerAsyncReceipt is the receipt of a composer run that converted to
// async: only the job's id (its completion arrives as the ordinary exec
// notice).
func ComposerAsyncReceipt(line, execID string) string {
	return fmt.Sprintf("[user ran from the dashboard] exec_id %s is still running after %s; its completion arrives as a message (exec.tail exec_id %s reads its output).\ncommand: %s", execID, ComposerWindow, execID, line)
}

func composerEnd(job ComposerJob) string {
	switch job.State {
	case "stopped":
		return "was stopped"
	case "failed":
		return "failed to start: " + job.Error
	case "lost":
		return "was lost: " + job.Error
	}
	switch {
	case job.ExitCode != nil:
		return fmt.Sprintf("exited with code %d", *job.ExitCode)
	case job.Signal != nil:
		return fmt.Sprintf("was ended by signal %d", *job.Signal)
	}
	return "ended"
}

// composerRefresh brings an async submission's card up to date from its job
// and finalizes it once the job ended.
func (d *Daemon) composerRefresh(svc ComposerExecService, sub *composerSub) {
	sub.mu.Lock()
	defer sub.mu.Unlock()
	if sub.phase != "async" || sub.finalized || sub.job.ExecID == "" {
		return
	}
	job, err := svc.Job(sub.reply, sub.job.ExecID)
	if err != nil {
		sub.err = "the job is no longer queryable: " + composerErrorText(err)
		return
	}
	sub.err = ""
	job.Async = true
	sub.job = job
	if job.Terminal() {
		sub.phase = "done"
		sub.job.Async = true
		d.composerFinalizeLocked(sub, false, svc)
	}
}

func (d *Daemon) composerShow(w http.ResponseWriter, svc ComposerExecService, replyID, id string) {
	sub := d.composerFind(replyID, id)
	if sub == nil {
		// Unknown to this daemon (a restart, or an id it never saw): the
		// card says so and nothing re-runs.
		http.Error(w, "unknown submission", http.StatusNotFound)
		return
	}
	d.composerRefresh(svc, sub)
	sub.mu.Lock()
	card := sub.cardLocked()
	sub.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, card)
}

func (d *Daemon) composerList(w http.ResponseWriter, svc ComposerExecService, replyID string) {
	c := &d.composer
	c.mu.Lock()
	var subs []*composerSub
	for _, key := range c.order[replyID] {
		subs = append(subs, c.subs[key])
	}
	c.mu.Unlock()
	cards := make([]composerCard, 0, len(subs))
	for _, sub := range subs {
		d.composerRefresh(svc, sub)
		sub.mu.Lock()
		cards = append(cards, sub.cardLocked())
		sub.mu.Unlock()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"cards": cards})
}

// composerOutput serves a submission's output file. The submission ID is the
// key; the browser never names a path, and a claim of another session is not
// found.
func (d *Daemon) composerOutput(w http.ResponseWriter, r *http.Request, replyID, id string) {
	sub := d.composerFind(replyID, id)
	if sub == nil {
		http.Error(w, "unknown submission", http.StatusNotFound)
		return
	}
	sub.mu.Lock()
	path := sub.file
	sub.mu.Unlock()
	if path == "" {
		http.Error(w, "no output file", http.StatusNotFound)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		http.Error(w, "the output file is gone (retention removed it)", http.StatusGone)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Disposition", "inline")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, f)
}

// composerFilePath is the output file of a submission: derived from the
// session and submission ids, never from browser input.
func (d *Daemon) composerFilePath(replyID, id string) string {
	sum := sha256.Sum256([]byte(replyID + "\x00" + id))
	return filepath.Join(d.dir, composerOutputDir, hex.EncodeToString(sum[:12])+".out")
}

// composerWriteFile writes data as the submission's output file, atomically,
// then applies the retention. It runs on the submission's own goroutine, off
// the page and send paths.
func (d *Daemon) composerWriteFile(replyID, id string, data []byte) (string, error) {
	path := d.composerFilePath(replyID, id)
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	composerPrune(dir, time.Now())
	return path, nil
}

// composerPrune deletes whole output files: those older than the age
// bound, then the oldest until the total fits.
func composerPrune(dir string, now time.Time) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type file struct {
		path string
		mod  time.Time
		size int64
	}
	var files []file
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".out") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		path := filepath.Join(dir, e.Name())
		if now.Sub(info.ModTime()) > composerFileAge {
			_ = os.Remove(path)
			continue
		}
		files = append(files, file{path, info.ModTime(), info.Size()})
		total += info.Size()
	}
	sort.Slice(files, func(a, b int) bool { return files[a].mod.Before(files[b].mod) })
	for _, f := range files {
		if total <= composerFilesTotal {
			break
		}
		if os.Remove(f.path) == nil {
			total -= f.size
		}
	}
}
