// exec_hud.go is the dashboard side of the chat page's exec job HUD: the open
// hosted lead's own hosted exec jobs, listed, read and stopped through three
// localhost-only routes, plus the opaque revision the page stream sends when
// the job set changes. Commands, working directories and output never ride
// the stream or a region (every viewer shares those): the page learns that
// something changed and refetches the list here.
package wsdashboard

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/execjob"
)

// ExecJobsService is the dashboard's narrow dependency on the daemon's hosted
// exec jobs for the HUD, bound by the command layer (SetExecJobs). The owner
// is the conversation the page is for: the service refuses another
// conversation's job as not found, so a forged job id never reaches it. All of
// it reads memory only.
type ExecJobsService interface {
	// Open is the live authority check (as ComposerExecService.Open): the
	// open generation of replyID when it is an open hosted lead right now.
	Open(replyID string) (gen uint64, err error)
	// Jobs lists the owner's own jobs, with no output tail and no cap but
	// the retained jobs.
	Jobs(owner string) []execjob.HostedSummary
	// ReadOutput is one bounded read of the owner's job id's merged output
	// from combined offset from.
	ReadOutput(owner, id string, from int64, limit int) (execjob.HostedOutput, error)
	// StopByUser is the user's stop: it ends the job, marks it stopped-by-user
	// and tells the owner (execjob.Hosted.StopByUser).
	StopByUser(owner, id string) (execjob.HostedSummary, error)
}

// SetExecJobs binds the service the HUD routes run on; before it they answer
// 404, which the page reads as "no HUD".
func (d *Daemon) SetExecJobs(svc ExecJobsService) {
	if svc == nil {
		d.execHUD.Store(nil)
		return
	}
	d.execHUD.Store(&svc)
}

// execJobID bounds a job id the routes accept: exec_<words> or a legacy
// exec-<digits>-<hex> key, nothing that could name a path.
var execJobID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,96}$`)

// execJobView is a job as the HUD reads it.
type execJobView struct {
	ID            string `json:"id"`
	Tool          string `json:"tool"`
	Command       string `json:"command"`
	Cwd           string `json:"cwd,omitempty"`
	State         string `json:"state"`
	Error         string `json:"error,omitempty"`
	Origin        string `json:"origin"`
	ExitCode      *int   `json:"exit_code,omitempty"`
	Signal        *int   `json:"signal,omitempty"`
	StartedAt     int64  `json:"started_at,omitempty"`
	EndedAt       int64  `json:"ended_at,omitempty"`
	Written       int64  `json:"written"`
	StoppedByUser bool   `json:"stopped_by_user,omitempty"`
	StopNotice    string `json:"stop_notice,omitempty"`
}

func execJobViewOf(s execjob.HostedSummary) execJobView {
	return execJobView{
		ID: s.ID, Tool: s.Tool, Command: s.Command, Cwd: s.Cwd, State: s.State, Error: s.Error, Origin: s.Origin,
		ExitCode: s.ExitCode, Signal: s.Signal, StartedAt: s.StartedAt, EndedAt: s.EndedAt, Written: s.Written,
		StoppedByUser: s.StoppedByUser, StopNotice: s.StopNotice,
	}
}

// execSegmentView is one stream-attributed run of output text. Stream is
// stdout, stderr, or combined for a run restored after a daemon restart.
type execSegmentView struct {
	Stream string `json:"stream"`
	Text   string `json:"text"`
}

// execOutputView is one output read: the runs from From to Next (combined
// offsets), the job's totals, and Gap when output before the asked offset
// was dropped.
type execOutputView struct {
	Job      execJobView       `json:"job"`
	From     int64             `json:"from"`
	Next     int64             `json:"next"`
	Written  int64             `json:"written"`
	Evicted  int64             `json:"evicted"`
	Gap      bool              `json:"gap,omitempty"`
	Segments []execSegmentView `json:"segments"`
}

// handleExecJobs serves /s/<reply-id>/exec/...: the job list, one job's output
// and the user's stop of it. It is localhost-only like every route that
// carries execution authority or shows a command: a LAN viewer gets 404 (the
// HUD never mounts), a write needs the daemon's own Origin, a read a
// non-cross-site fetch. The session must be an open hosted lead right now.
func (d *Daemon) handleExecJobs(w http.ResponseWriter, r *http.Request, replyID, rest string) {
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
	if s, ok := d.index.Session(replyID); !ok || !s.Hosted {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	svcp := d.execHUD.Load()
	if svcp == nil {
		http.NotFound(w, r)
		return
	}
	svc := *svcp
	// The live authority, for reads as for writes: a closed lead's jobs are
	// no longer served.
	if _, err := svc.Open(replyID); err != nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}
	id, tail, _ := strings.Cut(rest, "/")
	switch {
	case rest == "jobs" && !write:
		d.execJobsList(w, svc, replyID)
	case id != "jobs" && execJobID.MatchString(id) && tail == "output" && !write:
		execJobOutput(w, r, svc, replyID, id)
	case id != "jobs" && execJobID.MatchString(id) && tail == "stop" && write:
		execJobStop(w, svc, replyID, id)
	default:
		http.NotFound(w, r)
	}
}

func (d *Daemon) execJobsList(w http.ResponseWriter, svc ExecJobsService, replyID string) {
	// The revision is read before the list: a change that lands between the
	// two moves the revision on, so the page refetches once more and never
	// holds a list older than the revision it believes.
	rev := d.execRev.Load()
	jobs := svc.Jobs(replyID)
	views := make([]execJobView, 0, len(jobs))
	for _, s := range jobs {
		views = append(views, execJobViewOf(s))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"rev": rev, "jobs": views})
}

func execJobOutput(w http.ResponseWriter, r *http.Request, svc ExecJobsService, replyID, id string) {
	var from int64
	if v := r.URL.Query().Get("from"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			http.Error(w, "bad offset", http.StatusBadRequest)
			return
		}
		from = n
	}
	out, err := svc.ReadOutput(replyID, id, from, execjob.HostedOutputLimit)
	if err != nil {
		execJobError(w, err)
		return
	}
	view := execOutputView{Job: execJobViewOf(out.Job), From: out.From, Next: out.Next, Written: out.Written, Evicted: out.Evicted, Gap: out.Gap, Segments: make([]execSegmentView, 0, len(out.Segments))}
	for _, seg := range out.Segments {
		text, _ := execjob.DecodeText(seg.Data)
		view.Segments = append(view.Segments, execSegmentView{Stream: seg.Stream.String(), Text: text})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, view)
}

// execJobStop stops the job (or retries a failed notice of a job the user
// already stopped) and answers the job with the notice outcome.
func execJobStop(w http.ResponseWriter, svc ExecJobsService, replyID, id string) {
	sum, err := svc.StopByUser(replyID, id)
	if err != nil {
		var status int
		if errors.Is(err, execjob.ErrExecNotFound) {
			status = http.StatusNotFound
		} else {
			status = http.StatusBadGateway
		}
		// The job may still run (its holder did not go): say so, with its view.
		if sum.ID != "" && status != http.StatusNotFound {
			w.Header().Set("Cache-Control", "no-store")
			writeJSON(w, status, map[string]any{"error": err.Error(), "job": execJobViewOf(sum)})
			return
		}
		execJobError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"job": execJobViewOf(sum)})
}

func execJobError(w http.ResponseWriter, err error) {
	if errors.Is(err, execjob.ErrExecNotFound) {
		http.Error(w, "unknown job", http.StatusNotFound)
		return
	}
	http.Error(w, fmt.Sprint(err), http.StatusInternalServerError)
}
