package execjob

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// stop ends j's process group through its holder and waits for the end.
func (h *Hosted) stop(j *hostedJob) {
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return
	}
	j.stopping = true
	holder := j.row.HolderID
	h.mu.Unlock()
	err := h.opts.Holders.Stop(holder)
	if err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		h.logf("stop job %s: %v", j.row.ID, err)
	}
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return
	}
	if err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		// The holder did not go: the job may still run.
		j.stopping = false
		h.mu.Unlock()
		return
	}
	// The holder is gone and its exit record did not arrive first.
	j.row.State = HostedStopped
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.runningChanged()
	h.save(j, row, rev)
}

// Stop stops owner's job id and returns its view after the end.
func (h *Hosted) Stop(owner, id string) (HostedView, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, err
	}
	h.stop(j)
	return h.view(j, 0), nil
}

// Write writes data to owner's running job id's stdin, closing it after
// with eof.
func (h *Hosted) Write(owner, id string, data []byte, eof bool) (HostedView, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, err
	}
	j.writeMu.Lock()
	defer j.writeMu.Unlock()
	h.mu.Lock()
	unknown, holder := j.stdinUnknown, j.row.HolderID
	h.mu.Unlock()
	if unknown {
		// A holder that attached after Start reports its stdin now.
		for _, info := range h.opts.Holders.List().Holders {
			if info.ID == holder {
				h.mu.Lock()
				stdinFromHolderLocked(j, info)
				h.mu.Unlock()
			}
		}
	}
	h.mu.Lock()
	running, open, unknown := !j.row.Terminal(), j.stdinOpen, j.stdinUnknown
	h.mu.Unlock()
	switch {
	case !running:
		return h.view(j, 0), fmt.Errorf("exec job %s is not running (%s)", id, j.row.State)
	case unknown:
		return h.view(j, 0), fmt.Errorf("exec job %s's stdin state is not known yet: its holder is not attached; retry once it is", id)
	case !open:
		return h.view(j, 0), fmt.Errorf("exec job %s has no open stdin: it was given at launch, closed by eof, or the job is a legacy launch", id)
	}
	if err := h.opts.Holders.Input(holder, data, eof); err != nil {
		var opErr *wsholder.OpError
		if errors.As(err, &opErr) && opErr.Code == "input_closed" {
			h.mu.Lock()
			j.stdinOpen = false
			h.mu.Unlock()
			return h.view(j, 0), fmt.Errorf("exec job %s has no open stdin: an earlier eof closed it", id)
		}
		// The eof did not reach the child: stdin stays open. A retry
		// resends only what the holder did not take.
		var inErr *wsholder.InputError
		if errors.As(err, &inErr) {
			if inErr.Refused {
				return h.view(j, 0), fmt.Errorf("write to exec job %s: %w; stdin stays open: resend from byte %d of %d", id, err, inErr.Accepted, len(data))
			}
			return h.view(j, 0), fmt.Errorf("write to exec job %s: %w; check the job before resending from byte %d of %d", id, err, inErr.Accepted, len(data))
		}
		return h.view(j, 0), fmt.Errorf("write to exec job %s: %w", id, err)
	}
	if eof {
		h.mu.Lock()
		j.stdinOpen = false
		h.mu.Unlock()
	}
	return h.view(j, 0), nil
}

// Get returns owner's job id.
func (h *Hosted) Get(owner, id string) (HostedView, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, err
	}
	return h.view(j, 0), nil
}

// Tail returns the last max bytes of owner's job id's combined output.
func (h *Hosted) Tail(owner, id string, max int) (HostedView, Slice, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, Slice{}, err
	}
	if max <= 0 || max > HostedPreviewBytes {
		max = HostedPreviewBytes
	}
	return h.view(j, 0), j.out.Tail(StreamCombined, max), nil
}

// List returns owner's jobs: the running ones, newest first, then the
// finished ones, last ended first, HostedListMax at most, and how many
// more there were.
func (h *Hosted) List(owner string) ([]HostedView, int) {
	h.mu.Lock()
	var mine []*hostedJob
	for _, j := range h.jobs {
		if j.row.Owner == owner {
			mine = append(mine, j)
		}
	}
	h.mu.Unlock()
	views := make([]HostedView, 0, len(mine))
	for _, j := range mine {
		views = append(views, h.view(j, 0))
	}
	sort.Slice(views, func(a, b int) bool {
		ra, rb := views[a].HostedJobRow, views[b].HostedJobRow
		if ra.Terminal() != rb.Terminal() {
			return !ra.Terminal()
		}
		if !ra.Terminal() {
			return ra.StartedAt > rb.StartedAt
		}
		return ra.EndedAt > rb.EndedAt
	})
	more := 0
	if len(views) > HostedListMax {
		more = len(views) - HostedListMax
		views = views[:HostedListMax]
	}
	return views, more
}

// LiveCount counts owner's running jobs.
func (h *Hosted) LiveCount(owner string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, j := range h.jobs {
		if j.row.Owner == owner && !j.row.Terminal() {
			n++
		}
	}
	return n
}

// Output returns job id's output for a reader of its own (legacy readers).
func (h *Hosted) output(id string) *JobOutput {
	h.mu.Lock()
	defer h.mu.Unlock()
	if j := h.jobs[id]; j != nil {
		return j.out
	}
	return nil
}

// owned returns job id when owner owns it.
func (h *Hosted) owned(owner, id string) (*hostedJob, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	j := h.jobs[strings.TrimSpace(id)]
	if j == nil || owner == "" || j.row.Owner != owner {
		return nil, fmt.Errorf("%w: %s", ErrExecNotFound, id)
	}
	return j, nil
}

func (h *Hosted) view(j *hostedJob, outcome HostedOutcome) HostedView {
	h.mu.Lock()
	defer h.mu.Unlock()
	return HostedView{HostedJobRow: h.rowLocked(j), Outcome: outcome}
}
