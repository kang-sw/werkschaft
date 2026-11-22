package execjob

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// stop ends j's process group through its holder and waits for the end. A
// job whose process was not handed to its holder yet ends at once (Launch
// then never spawns it); one whose hand-over runs is waited for first. An
// error means the holder did not go and the job may still run.
func (h *Hosted) stop(j *hostedJob) error {
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return nil
	}
	j.stopping = true
	if j.starting {
		holder := j.row.HolderID
		if holder != "" {
			delete(h.byHolder, holder)
		}
		j.row.State = HostedStopped
		if j.row.StartedAt == 0 {
			j.row.StartedAt = h.opts.Now().UnixMilli()
		}
		h.endLocked(j)
		row, rev := h.snapLocked(j)
		h.mu.Unlock()
		if holder != "" {
			h.stopHolder(holder)
		}
		h.runningChanged()
		h.save(j, row, rev)
		return nil
	}
	if wait := j.spawned; wait != nil {
		h.mu.Unlock()
		<-wait
		h.mu.Lock()
		if j.row.Terminal() {
			h.mu.Unlock()
			return nil
		}
	}
	holder := j.row.HolderID
	h.mu.Unlock()
	err := h.opts.Holders.Stop(holder)
	if err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		h.logf("stop job %s: %v", j.row.ID, err)
	}
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return nil
	}
	if err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
		// The holder did not go: the job may still run.
		j.stopping = false
		h.mu.Unlock()
		return fmt.Errorf("stop exec job %s: %w", j.row.ID, err)
	}
	// The holder is gone and its exit record did not arrive first.
	j.row.State = HostedStopped
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.runningChanged()
	h.save(j, row, rev)
	return nil
}

// Stop stops owner's job id and returns its view after the end.
func (h *Hosted) Stop(owner, id string) (HostedView, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, err
	}
	if err := h.stop(j); err != nil {
		return h.view(j, 0), err
	}
	return h.view(j, 0), nil
}

// Write writes data to owner's running job id's stdin, closing it after
// with eof.
func (h *Hosted) Write(owner, id string, data []byte, eof bool) (HostedView, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, err
	}
	h.mu.Lock()
	running, open, holder := !j.row.Terminal(), j.stdinOpen, j.row.HolderID
	if running && open && eof {
		j.stdinOpen = false
	}
	h.mu.Unlock()
	switch {
	case !running:
		return h.view(j, 0), fmt.Errorf("exec job %s is not running (%s)", id, j.row.State)
	case !open:
		return h.view(j, 0), fmt.Errorf("exec job %s has no open stdin: it was given at launch, closed by eof, or the job is a legacy launch", id)
	}
	if err := h.opts.Holders.Input(holder, data, eof); err != nil {
		return h.view(j, 0), fmt.Errorf("write to exec job %s: %w", id, err)
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

// StopOwner stops every running job of owner, each with its whole process
// group, and returns once they ended. A stop sends no completion notice.
// The error names each job whose holder did not go (it may still run).
func (h *Hosted) StopOwner(owner string) error {
	if h == nil || owner == "" {
		return nil
	}
	h.mu.Lock()
	var jobs []*hostedJob
	for _, j := range h.jobs {
		if j.row.Owner == owner && !j.row.Terminal() {
			jobs = append(jobs, j)
		}
	}
	h.mu.Unlock()
	var errs []error
	for _, j := range jobs {
		if err := h.stop(j); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// FenceOwner refuses owner's new jobs until UnfenceOwner; a job already
// past admission but not started fails its start.
func (h *Hosted) FenceOwner(owner string) {
	if h == nil || owner == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ownerFences == nil {
		h.ownerFences = map[string]bool{}
	}
	h.ownerFences[owner] = true
}

// UnfenceOwner lets owner own new jobs again.
func (h *Hosted) UnfenceOwner(owner string) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.ownerFences, owner)
}

// within reports whether path is root or lies beneath it.
func within(root, path string) bool {
	if path == "" {
		return false
	}
	rel, err := filepath.Rel(root, filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// rootForms are root's spellings a job row may carry: as given and with
// its symlinks resolved (a temp directory under /var is /private/var on
// macOS). A file lookup, on the release path only.
func rootForms(root string) []string {
	root = filepath.Clean(root)
	forms := []string{root}
	if real, err := filepath.EvalSymlinks(root); err == nil && real != root {
		forms = append(forms, real)
	}
	return forms
}

// inRoot reports whether row's job runs in one of a worktree's forms.
func inRoot(forms []string, row HostedJobRow) bool {
	for _, root := range forms {
		if within(root, row.Root) || within(root, row.Cwd) {
			return true
		}
	}
	return false
}

// fencedRootLocked is the fenced worktree row's job would run in, "" when
// none. Under mu.
func (h *Hosted) fencedRootLocked(row HostedJobRow) string {
	for root, fence := range h.rootFences {
		if inRoot(fence.forms, row) {
			return root
		}
	}
	return ""
}

// LiveIn counts the running jobs whose root or working directory lies in
// worktree root.
func (h *Hosted) LiveIn(root string) int {
	if h == nil {
		return 0
	}
	forms := rootForms(root)
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.liveInLocked(forms)
}

func (h *Hosted) liveInLocked(forms []string) int {
	n := 0
	for _, j := range h.jobs {
		if !j.row.Terminal() && inRoot(forms, j.row) {
			n++
		}
	}
	return n
}

// FenceRoot reserves worktree root for a release or removal: it refuses
// while a job runs there, and otherwise refuses new jobs there until the
// returned unfence runs. One lock orders it with job admission, so no job
// slips in between the check and the release.
func (h *Hosted) FenceRoot(root string) (unfence func(), err error) {
	if h == nil {
		return func() {}, nil
	}
	forms := rootForms(root)
	root = forms[0]
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := h.liveInLocked(forms); n > 0 {
		return nil, fmt.Errorf("%d exec job(s) still run in it; stop them with exec.stop (exec.list shows them), or let them finish, first", n)
	}
	if h.rootFences == nil {
		h.rootFences = map[string]*rootFence{}
	}
	if h.rootFences[root] == nil {
		h.rootFences[root] = &rootFence{forms: forms}
	}
	h.rootFences[root].holders++
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if f := h.rootFences[root]; f != nil {
				if f.holders--; f.holders <= 0 {
					delete(h.rootFences, root)
				}
			}
		})
	}, nil
}

// rootFence is one reserved worktree: its spellings and how many
// reservations hold it.
type rootFence struct {
	forms   []string
	holders int
}
