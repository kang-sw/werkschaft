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
	// composerFenceMax bounds the replay fence: the submission ids this
	// daemon remembers having admitted, whatever became of their cards.
	composerFenceMax = 4096
	// composerReceiptCommand bounds the command text a receipt repeats.
	composerReceiptCommand = 1024
)

// The background loop's default periods: settling the active async cards,
// and the output files' retention (first after the start, then on a period; a
// write also asks for one at once). A composerExec's own fields, when set
// before the service is bound, replace them (tests).
const (
	composerSettleEvery = time.Second
	composerSweepFirst  = time.Minute
	composerSweepEvery  = 30 * time.Minute
)

// ComposerLaunch is one `!` line's launch for an open hosted lead.
type ComposerLaunch struct {
	ReplyID string
	// Gen is the open generation the route read (Open) before it
	// acknowledged the submission: the launch is refused when the lead is on
	// another one by the time it is admitted.
	Gen  uint64
	Line string
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
	// Open is the live authority check: the open generation of replyID when
	// it is an open hosted lead right now, an error otherwise. It reads
	// memory only; every private route asks it.
	Open(replyID string) (gen uint64, err error)
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
	// order keeps each session's cards oldest first (composerKeepCards).
	order map[string][]string
	// fence is the replay fence: every submission id admitted, newest
	// composerFenceMax of them, independent of the cards. An id in it never
	// launches again in this daemon, whatever became of its card; an id it
	// does not hold (after a restart, or older than the bound) launches only
	// as a fresh submission, never as a retry.
	fence  map[string]struct{}
	fenceQ []string

	// wg counts the goroutines that run or finalize submissions and the
	// background loop; Close waits for them. loopOnce starts the loop; wake
	// asks it to settle active cards; kick asks it for a retention sweep.
	wg       sync.WaitGroup
	loopOnce sync.Once
	wake     chan struct{}
	kick     chan struct{}
	// settleEvery, sweepFirst and sweepEvery are the loop's periods; zero
	// takes the default.
	settleEvery, sweepFirst, sweepEvery time.Duration
}

// SetComposerExec binds the service the composer route runs on; before it
// the route answers unavailable.
func (d *Daemon) SetComposerExec(svc ComposerExecService) {
	if svc == nil {
		d.composer.svc.Store(nil)
		return
	}
	d.composer.svc.Store(&svc)
	d.composer.loopOnce.Do(func() {
		c := &d.composer
		c.wake, c.kick = make(chan struct{}, 1), make(chan struct{}, 1)
		for _, p := range []struct {
			v   *time.Duration
			def time.Duration
		}{{&c.settleEvery, composerSettleEvery}, {&c.sweepFirst, composerSweepFirst}, {&c.sweepEvery, composerSweepEvery}} {
			if *p.v <= 0 {
				*p.v = p.def
			}
		}
		c.wg.Add(1)
		go d.composerLoop()
	})
}

// composerWait waits for the composer's goroutines (Close, after d.stop).
func (d *Daemon) composerWait() { d.composer.wg.Wait() }

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
	// finalizing: a goroutine is classifying the ended job (and writing its
	// output file) outside mu; finalized: the result was classified once.
	finalizing bool
	finalized  bool
	inline     string
	replaced   bool
	file       string // the output file's path once it exists
	fileAt     time.Time
	fileErr    string
	// handed: the receipt went to the lead's pending input.
	handed bool
	// gen is the open generation the submission was admitted under.
	gen uint64
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
	// Unknown: this daemon cannot say what became of the submission (it
	// restarted, or forgot it); nothing was or will be run for it.
	Unknown   bool  `json:"unknown,omitempty"`
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
	// The live authority, for reads as for writes: a closed lead keeps its
	// registration (and its index flag), but its commands, results and files
	// are no longer served.
	gen, err := svc.Open(replyID)
	if err != nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	switch {
	case rest == "" && write:
		d.composerSubmit(w, r, svc, replyID, gen)
	case rest == "" && !write:
		d.composerList(w, replyID)
	case !write:
		id, tail, _ := strings.Cut(rest, "/")
		switch tail {
		case "":
			d.composerShow(w, replyID, id)
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
	SubmissionID string `json:"submission_id"`
	Text         string `json:"text"`
	// Retry marks a resend of an attempt whose outcome the browser never
	// learned (it recorded the attempt before sending). A retry never starts
	// a command: it maps to the card of the one admission, or reports the
	// outcome unknown.
	Retry  bool              `json:"retry"`
	Images []json.RawMessage `json:"images"`
	Refs   []json.RawMessage `json:"refs"`
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

func (d *Daemon) composerSubmit(w http.ResponseWriter, r *http.Request, svc ComposerExecService, replyID string, gen uint64) {
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
	sub, admission := d.composerAdmit(replyID, req.SubmissionID, line, gen, req.Retry)
	w.Header().Set("Cache-Control", "no-store")
	switch admission {
	case composerFresh:
		// The run happens after the acknowledgement, under the daemon's own
		// lifetime: closing the tab does not cancel a submitted command. It
		// carries the generation read above, not whatever is open later.
		d.composer.wg.Add(1)
		go func() {
			defer d.composer.wg.Done()
			d.composerRun(svc, sub)
		}()
		sub.mu.Lock()
		card := sub.cardLocked()
		sub.mu.Unlock()
		writeJSON(w, http.StatusAccepted, card)
	case composerKnown:
		// A resend after a lost response, or a duplicate: the admission maps
		// to its one job.
		sub.mu.Lock()
		same := sub.line == line
		card := sub.cardLocked()
		sub.mu.Unlock()
		if !same {
			http.Error(w, "this submission id was used for another command", http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusOK, card)
	default:
		// Not known to this daemon (a restart, or an id its replay fence
		// forgot cards of): nothing is run; the card says so and the user
		// sends again deliberately.
		writeJSON(w, http.StatusOK, composerCard{ID: req.SubmissionID, Command: line, Phase: "done", Unknown: true})
	}
}

// composerAdmission is how a submission id met the daemon.
type composerAdmission int

const (
	// composerFresh: admitted now; its command runs.
	composerFresh composerAdmission = iota
	// composerKnown: an earlier admission's card is returned.
	composerKnown
	// composerUnknown: the id cannot be admitted again and has no card (a
	// retry of an unknown attempt, or an id the fence holds whose card was
	// evicted).
	composerUnknown
)

// composerAdmit admits submission id of session replyID for line under open
// generation gen. Fresh and retry differ: only a fresh submission whose id
// neither a card nor the replay fence holds is admitted and runs; a retry
// maps to its card, or reports the outcome unknown, and never launches, so a
// forgotten id (an evicted card, a restarted daemon) cannot run a command
// twice.
func (d *Daemon) composerAdmit(replyID, id, line string, gen uint64, retry bool) (*composerSub, composerAdmission) {
	key := replyID + "\x00" + id
	c := &d.composer
	c.mu.Lock()
	defer c.mu.Unlock()
	if sub, ok := c.subs[key]; ok {
		return sub, composerKnown
	}
	if _, ok := c.fence[key]; ok || retry {
		return nil, composerUnknown
	}
	if c.subs == nil {
		c.subs, c.order, c.fence = map[string]*composerSub{}, map[string][]string{}, map[string]struct{}{}
	}
	c.fence[key] = struct{}{}
	c.fenceQ = append(c.fenceQ, key)
	if len(c.fenceQ) > composerFenceMax {
		delete(c.fence, c.fenceQ[0])
		c.fenceQ = c.fenceQ[1:]
	}
	sub := &composerSub{reply: replyID, id: id, line: line, created: time.Now(), phase: "running", gen: gen}
	c.subs[key] = sub
	c.order[replyID] = append(c.order[replyID], key)
	// Keep the newest cards; an unfinished one is never dropped, and one
	// that is still active does not hold the finished ones behind it. The
	// fence outlives the card.
	for len(c.order[replyID]) > composerKeepCards {
		drop := -1
		for i, key := range c.order[replyID][:len(c.order[replyID])-composerKeepCards] {
			old := c.subs[key]
			old.mu.Lock()
			active := old.phase == "running" || old.finalizing || (old.phase == "async" && !old.finalized)
			old.mu.Unlock()
			if !active {
				drop = i
				break
			}
		}
		if drop < 0 {
			break
		}
		delete(c.subs, c.order[replyID][drop])
		c.order[replyID] = append(c.order[replyID][:drop:drop], c.order[replyID][drop+1:]...)
	}
	return sub, composerFresh
}

func (d *Daemon) composerFind(replyID, id string) *composerSub {
	c := &d.composer
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.subs[replyID+"\x00"+id]
}

// composerRun launches the admitted submission and settles its card; a run
// that ended inside the window is classified and handed to the lead. A run
// that converted is the ordinary exec notice's from then on: its card settles
// in the background and the dashboard hands nothing more, whatever state the
// job reached since.
func (d *Daemon) composerRun(svc ComposerExecService, sub *composerSub) {
	job, err := svc.Launch(context.Background(), d.stop, ComposerLaunch{ReplyID: sub.reply, Gen: sub.gen, Line: sub.line, Window: ComposerWindow})
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
	if job.Async || !job.Terminal() {
		sub.phase, sub.job.Async = "async", true
		sub.job.Output = nil
		sub.mu.Unlock()
		select {
		case d.composer.wake <- struct{}{}:
		default:
		}
		return
	}
	sub.finalizing = true
	sub.mu.Unlock()
	// This is the submission's own goroutine; polls of the card do not wait
	// for the file write (the card reads as running until it lands).
	d.composerFinalize(sub, job, true, svc)
}

func composerErrorText(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimPrefix(err.Error(), "exec: ")
}

// composerFileHook, when set, sees every file operation the composer makes
// by name (a test seam: the interactive paths must show none, the
// finalization and the sweep exactly the ones documented).
var composerFileHook atomic.Pointer[func(op string)]

func composerFileOp(op string) {
	if p := composerFileHook.Load(); p != nil {
		(*p)(op)
	}
}

// composerText is output bytes as the card, the receipt and the file show
// them: UTF-8 text, each invalid byte as U+FFFD (the exec reply's decoding)
// and each NUL byte as U+2400, so a harness's text reader (it refuses a file
// with a NUL in its head as binary) takes the artifact. replaced reports any
// substitution.
func composerText(b []byte) (string, bool) {
	text, replaced := execjob.DecodeText(b)
	if strings.IndexByte(text, 0) >= 0 {
		text, replaced = strings.ReplaceAll(text, "\x00", "\u2400"), true
	}
	return text, replaced
}

// composerArtifactBytes is what composerText will make of raw, in bytes: a
// valid rune its own width, an invalid byte or a NUL three.
func composerArtifactBytes(raw []byte) int {
	n := 0
	for len(raw) > 0 {
		r, size := utf8.DecodeRune(raw)
		switch {
		case r == utf8.RuneError && size == 1, r == 0:
			n += 3
		default:
			n += size
		}
		raw = raw[size:]
	}
	return n
}

// composerFileWindow is the newest part of raw whose artifact text fits max
// bytes (so the file stays within the cap after the substitutions), cut on a
// rune boundary, and how many leading raw bytes it dropped.
func composerFileWindow(raw []byte, max int) ([]byte, int) {
	size := composerArtifactBytes(raw)
	i := 0
	for size > max && i < len(raw) {
		r, n := utf8.DecodeRune(raw[i:])
		w := n
		if (r == utf8.RuneError && n == 1) || r == 0 {
			w = 3
		}
		size -= w
		i += n
	}
	return raw[i:], i
}

// ComposerArtifact is the output file's content for raw combined output: the
// newest range of raw whose text fits the per-file cap, as composerText shows
// it, with how many leading raw bytes it dropped and whether any byte was
// substituted. Exported so the harness-side reader's limits can be checked
// against the real representation.
func ComposerArtifact(raw []byte) (text []byte, dropped int, replaced bool) {
	window, dropped := composerFileWindow(raw, composerFileMax)
	s, replaced := composerText(window)
	return []byte(s), dropped, replaced
}

// composerFinalize classifies an ended job's retained output once: 4096 raw
// combined bytes or fewer inline, more as a private file. The file write runs
// here, with no lock held, so a card poll or another submission never waits
// on it; the result is then applied under sub.mu, and the whole snapshot is
// dropped (the hosted ring is the one retained copy). hand sends the receipt
// to the lead (a run that ended inside its window; a converted job's end is
// the ordinary exec notice's). sub.finalizing was set by the caller.
func (d *Daemon) composerFinalize(sub *composerSub, job ComposerJob, hand bool, svc ComposerExecService) {
	var inline, file, fileErr string
	var replaced bool
	var fileAt time.Time
	start := job.Start
	// The count is the raw combined bytes the job wrote, never a decoded or
	// tail length.
	if job.Written > 0 && len(job.Output) == 0 {
		// The retained output could not be read back: no empty file stands in
		// for it.
		fileErr = "the output is no longer retained"
	} else if job.Written <= ComposerInlineMax {
		inline, replaced = composerText(job.Output)
	} else {
		// The file holds the newest raw range whose text fits the cap; Start
		// says how many leading bytes of the output it lacks.
		text, dropped, rep := ComposerArtifact(job.Output)
		start += int64(dropped)
		replaced = rep
		path, err := d.composerWriteFile(sub.reply, sub.id, text)
		fileAt = time.Now()
		if err != nil {
			fileErr = "the output file could not be written: " + err.Error()
			// The card still shows the tail, as the exec result does.
			tail := job.Output
			if len(tail) > ComposerInlineMax {
				tail = tail[len(tail)-ComposerInlineMax:]
			}
			inline, _ = composerText(tail)
		} else {
			file = path
			// Retention is the background loop's: this write only asks.
			select {
			case d.composer.kick <- struct{}{}:
			default:
			}
		}
	}
	sub.mu.Lock()
	if sub.finalized {
		sub.mu.Unlock()
		return
	}
	async := sub.job.Async
	sub.job = job
	sub.job.Async, sub.job.Start, sub.job.Output = async, start, nil
	sub.inline, sub.replaced, sub.file, sub.fileErr = inline, replaced, file, fileErr
	if file != "" {
		sub.fileAt = fileAt
	}
	sub.phase, sub.finalized, sub.finalizing = "done", true, false
	receipt, doHand := "", hand && !sub.handed
	if doHand {
		receipt = composerReceipt(sub)
	}
	execID, gen := sub.job.ExecID, sub.gen
	sub.mu.Unlock()
	if !doHand {
		return
	}
	if err := svc.Hand(sub.reply, gen, execID, receipt); err != nil {
		fmt.Fprintf(d.log, "dashboard: composer result for %s not handed: %v\n", sub.reply, err)
		return
	}
	sub.mu.Lock()
	sub.handed = true
	sub.mu.Unlock()
}

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
	b.WriteString(".\ncommand: " + composerCut(sub.line, composerReceiptCommand) + "\n")
	switch {
	case sub.file != "":
		fmt.Fprintf(&b, "output (%d bytes) is in the file: %s\n", job.Written, sub.file)
		if job.Start > 0 {
			fmt.Fprintf(&b, "the file starts at byte %d of the output; earlier bytes were not retained.\n", job.Start)
		}
		if sub.replaced {
			b.WriteString("the file is UTF-8 text: invalid bytes appear as U+FFFD and NUL bytes as U+2400.\n")
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

// composerCut is text cut to max bytes on a rune boundary, marked.
func composerCut(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return fmt.Sprintf("%s …[command cut at %d of %d bytes]", text[:cut], cut, len(text))
}

// ComposerAsyncReceipt is the receipt of a composer run that converted to
// async: the job's id only (its completion arrives as the ordinary exec
// notice, which carries the command).
func ComposerAsyncReceipt(execID string) string {
	return fmt.Sprintf("[user ran from the dashboard] exec_id %s ran past %s and continues as an async job; its completion arrives as a message (exec.tail exec_id %s reads its output).", execID, ComposerWindow, execID)
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

// composerSettle brings every active converted submission's card up to date
// from its job and finalizes the ones that ended, whether or not a page
// polls; a job its service no longer knows (pruned) retires its card as
// finished. It reports whether any card is still active. Memory reads only
// (the file write of a finalization runs on its own goroutine).
func (d *Daemon) composerSettle() bool {
	svcp := d.composer.svc.Load()
	if svcp == nil {
		return false
	}
	svc := *svcp
	c := &d.composer
	c.mu.Lock()
	var active []*composerSub
	for _, subs := range c.order {
		for _, key := range subs {
			active = append(active, c.subs[key])
		}
	}
	c.mu.Unlock()
	more := false
	for _, sub := range active {
		sub.mu.Lock()
		skip := sub.phase != "async" || sub.finalized || sub.finalizing || sub.job.ExecID == ""
		reply, execID := sub.reply, sub.job.ExecID
		sub.mu.Unlock()
		if skip {
			sub.mu.Lock()
			more = more || sub.phase == "running" || sub.finalizing
			sub.mu.Unlock()
			continue
		}
		job, err := svc.Job(reply, execID)
		sub.mu.Lock()
		switch {
		case sub.finalized || sub.finalizing:
		case err != nil:
			// Pruned or no longer ours: the card ends here, unfinalized
			// output and all (nothing is left to read it from).
			sub.err = "the job is no longer queryable: " + composerErrorText(err)
			sub.phase, sub.finalized = "done", true
		case !job.Terminal():
			job.Async = true
			sub.job = job
			more = true
		default:
			job.Async = true
			sub.finalizing = true
			c.wg.Add(1)
			go func() {
				defer c.wg.Done()
				d.composerFinalize(sub, job, false, svc)
			}()
			more = true
		}
		sub.mu.Unlock()
	}
	return more
}

// composerShow and composerList answer from memory only: the background loop
// keeps the cards current, so a page poll never queries a job, writes or
// reads a file.
func (d *Daemon) composerShow(w http.ResponseWriter, replyID, id string) {
	sub := d.composerFind(replyID, id)
	if sub == nil {
		// Unknown to this daemon (a restart, or an id it never saw): the
		// card says so and nothing re-runs.
		http.Error(w, "unknown submission", http.StatusNotFound)
		return
	}
	sub.mu.Lock()
	card := sub.cardLocked()
	sub.mu.Unlock()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, card)
}

func (d *Daemon) composerList(w http.ResponseWriter, replyID string) {
	c := &d.composer
	c.mu.Lock()
	var subs []*composerSub
	for _, key := range c.order[replyID] {
		subs = append(subs, c.subs[key])
	}
	c.mu.Unlock()
	cards := make([]composerCard, 0, len(subs))
	for _, sub := range subs {
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
	composerFileOp("open")
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

// composerWriteFile writes data as the submission's output file, atomically.
// It runs on the finalizing goroutine, off the page and send paths; the
// retention is the background loop's.
func (d *Daemon) composerWriteFile(replyID, id string, data []byte) (string, error) {
	path := d.composerFilePath(replyID, id)
	dir := filepath.Dir(path)
	composerFileOp("mkdir")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	composerFileOp("createtemp")
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return "", err
	}
	composerFileOp("write")
	_, werr := tmp.Write(data)
	composerFileOp("close")
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	composerFileOp("chmod")
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	composerFileOp("rename")
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}

// composerLoop is the composer's one background goroutine: it settles active
// converted cards, and it runs the output files' retention (at start, on a
// period, and when a write asks), so neither depends on a page being open
// and both stay off the card, acknowledgement and send paths. It idles with
// no timer running while no card is active.
func (d *Daemon) composerLoop() {
	c := &d.composer
	defer c.wg.Done()
	sweep := time.NewTimer(c.sweepFirst)
	defer sweep.Stop()
	settle := time.NewTimer(time.Hour)
	settle.Stop()
	defer settle.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-c.wake:
			settle.Reset(c.settleEvery)
		case <-settle.C:
			if d.composerSettle() {
				settle.Reset(c.settleEvery)
			}
		case <-c.kick:
			d.composerSweep()
		case <-sweep.C:
			d.composerSweep()
			sweep.Reset(c.sweepEvery)
		}
	}
}

// composerSweep applies the retention to the output directory and publishes
// the absence of every file a card still shows that is no longer there
// (removed by the retention, or by anyone else), so the card stops offering
// it. Only the loop calls it, so two sweeps never race.
func (d *Daemon) composerSweep() {
	dir := filepath.Join(d.dir, composerOutputDir)
	scanned := time.Now()
	present, ok := composerPrune(dir, scanned)
	if !ok {
		return
	}
	c := &d.composer
	c.mu.Lock()
	var subs []*composerSub
	for _, sub := range c.subs {
		subs = append(subs, sub)
	}
	c.mu.Unlock()
	for _, sub := range subs {
		sub.mu.Lock()
		// A file written after the scan began is not judged by it.
		if sub.file != "" && sub.fileAt.Before(scanned) {
			if _, ok := present[filepath.Base(sub.file)]; !ok {
				sub.file, sub.fileErr = "", "the output file was removed (retention keeps 7 days and 100 MiB)"
			}
		}
		sub.mu.Unlock()
	}
}

// composerPrune deletes whole output files: those older than the age bound,
// then the oldest until the total fits. It returns the names that remain. A
// file that is already gone counts as removed, never as still taking room.
func composerPrune(dir string, now time.Time) (map[string]struct{}, bool) {
	present := map[string]struct{}{}
	composerFileOp("readdir")
	entries, err := os.ReadDir(dir)
	if err != nil {
		// No directory yet holds no file; any other failure says nothing.
		return present, errors.Is(err, os.ErrNotExist)
	}
	type file struct {
		name string
		mod  time.Time
		size int64
	}
	var files []file
	var total int64
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".out") {
			continue
		}
		composerFileOp("stat")
		info, err := e.Info()
		if err != nil {
			continue
		}
		if now.Sub(info.ModTime()) > composerFileAge {
			composerFileOp("remove")
			if err := os.Remove(filepath.Join(dir, e.Name())); err == nil || errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
		files = append(files, file{e.Name(), info.ModTime(), info.Size()})
		total += info.Size()
	}
	sort.Slice(files, func(a, b int) bool { return files[a].mod.Before(files[b].mod) })
	for i := range files {
		if total <= composerFilesTotal {
			break
		}
		composerFileOp("remove")
		err := os.Remove(filepath.Join(dir, files[i].name))
		if err == nil || errors.Is(err, os.ErrNotExist) {
			total -= files[i].size
			files[i].size = -1
		}
	}
	for _, f := range files {
		if f.size >= 0 {
			present[f.name] = struct{}{}
		}
	}
	return present, true
}
