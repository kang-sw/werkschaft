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
// error means the holder did not go and the job may still run. byUser marks
// the stop as the user's own (the HUD's): the first stop to set stopping
// decides it, and the job is marked stopped-by-user only where this stop's
// intent ends it as stopped (endLocked).
func (h *Hosted) stop(j *hostedJob, byUser bool) error {
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return nil
	}
	if !j.stopping {
		j.stopIntent = byUser
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
		// A launch not past LaunchWith pinned the job at its admission
		// (holder == ""); one past it pins on its recorded holder.
		if holder != "" {
			j.holderPinned = true
		}
		h.endLocked(j)
		row, rev := h.snapLocked(j)
		h.mu.Unlock()
		h.runningChanged()
		h.save(j, row, rev)
		if holder != "" {
			return h.retireHolder(j, holder)
		}
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
		j.stopping, j.stopIntent = false, false
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
	if err := h.stop(j, false); err != nil {
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
	running, open, unknown, spawned := !j.row.Terminal(), j.stdinOpen, j.stdinUnknown, j.row.PID != 0
	h.mu.Unlock()
	switch {
	case !running:
		return h.view(j, 0), fmt.Errorf("exec job %s is not running (%s)", id, j.row.State)
	case !spawned:
		// Its holder runs but the process has not started (an approval
		// may still be pending): the holder's no_child would read as an
		// ended process, so the write waits for the spawn.
		return h.view(j, 0), fmt.Errorf("exec job %s has not started its process yet; retry once it has", id)
	case unknown:
		return h.view(j, 0), fmt.Errorf("exec job %s's stdin state is not known yet: its holder is not attached; retry once it is", id)
	case !open:
		return h.view(j, 0), fmt.Errorf("exec job %s has no open stdin: it was given at launch, closed by eof, or the job is a legacy launch", id)
	}
	if err := h.opts.Holders.Input(holder, data, eof); err != nil {
		var opErr *wsholder.OpError
		var inErr *wsholder.InputError
		refused := errors.As(err, &opErr)
		accepted := 0
		if errors.As(err, &inErr) {
			accepted = inErr.Accepted
		}
		switch {
		case refused && (opErr.Code == "input_closed" || opErr.Code == "no_child"):
			// The child's stdin is gone (an eof closed it, or the child
			// ended before its exit reached this daemon): no resend helps.
			h.mu.Lock()
			j.stdinOpen = false
			h.mu.Unlock()
			cause := "an earlier eof closed it"
			if opErr.Code == "no_child" {
				cause = "the job's process has ended"
			}
			return h.view(j, 0), fmt.Errorf("exec job %s has no open stdin: %s (the holder took %d of %d bytes)", id, cause, accepted, len(data))
		case refused && opErr.Code == "input_overflow":
			// The child is not reading yet: the eof did not reach it and
			// stdin stays open; a retry resends only what the holder did
			// not take.
			return h.view(j, 0), fmt.Errorf("write to exec job %s: %w; stdin stays open: resend from byte %d of %d", id, err, accepted, len(data))
		case refused:
			return h.view(j, 0), fmt.Errorf("write to exec job %s: %w", id, err)
		case inErr != nil:
			// The connection failed mid-write: the holder may have taken
			// the chunk it was sent.
			return h.view(j, 0), fmt.Errorf("write to exec job %s: %w; check the job before resending from byte %d of %d", id, err, accepted, len(data))
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

// Retained returns the whole retained combined output of owner's job id,
// not the preview's last bytes: Start is where the ring's retention begins
// (nonzero once earlier output was evicted).
func (h *Hosted) Retained(owner, id string) (HostedView, Slice, error) {
	j, err := h.owned(owner, id)
	if err != nil {
		return HostedView{}, Slice{}, err
	}
	return h.view(j, 0), j.out.Retained(StreamCombined), nil
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

// RunningRows returns a copy of every running job's row, whoever owns it
// (the worktree.release check reads their Cwd). It reads memory only and
// leaves the output tails out; a nil h returns none.
func (h *Hosted) RunningRows() []HostedJobRow {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var rows []HostedJobRow
	for _, j := range h.jobs {
		if j.row.Terminal() {
			continue
		}
		row := j.row
		row.Tails = [3][]byte{}
		rows = append(rows, row)
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].ID < rows[b].ID })
	return rows
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
		if err := h.stop(j, false); err != nil {
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

// rootForms are root's spellings a job row may carry: as given and as
// resolvePath sees it (a temp directory under /var is /private/var on
// macOS; a junction is its target on Windows). The candidate side of every
// fence and live-job count, resolved by the same resolver as the job side
// (admittedPlaces) so both compare in one form. A file lookup, on the
// release and acquire paths only.
func rootForms(root string) []string {
	root = filepath.Clean(root)
	forms := []string{root}
	if real, err := resolvePath(root); err == nil && real != root {
		forms = append(forms, real)
	}
	return forms
}

// admittedPlaces are a job's root and working directory as resolvePath
// sees them, always filled when err is nil (equal to the recorded spelling
// where no link intervenes); realCwd is "" only for an empty cwd. A working
// directory reached through a link passed only a lexical containment check,
// so the process may run inside another worktree than either recorded path
// names. Taken once per job, at admission: a fence matches the job in
// either spelling with no file lookup of its own, and the process starts
// in the place admission checked. A path that cannot be resolved is an
// error, and the launch is refused: its spelling is unchecked, and could
// appear as a link into a worktree fenced before the process starts. So is
// a working directory whose resolved place has no ordinary spelling to
// start the process in (checkOrdinarySpelling); the root, never spawned
// in, needs none. One lookup when the two are the same path, two
// otherwise.
func admittedPlaces(root, cwd string) (realRoot, realCwd string, err error) {
	root = filepath.Clean(root)
	if realRoot, err = resolvePath(root); err != nil {
		return "", "", fmt.Errorf("exec: the worktree root %q cannot be resolved, so the launch is refused: %w", root, err)
	}
	switch {
	case cwd == "":
		return realRoot, "", nil
	case filepath.Clean(cwd) == root:
		realCwd = realRoot
	default:
		if realCwd, err = resolvePath(filepath.Clean(cwd)); err != nil {
			return "", "", fmt.Errorf("exec: the working directory %q cannot be resolved, so the launch is refused: %w", cwd, err)
		}
	}
	if err = checkOrdinarySpelling(realCwd); err != nil {
		return "", "", fmt.Errorf("exec: the working directory %q cannot be resolved, so the launch is refused: %w", cwd, err)
	}
	return realRoot, realCwd, nil
}

// restoredPlaces are admittedPlaces for a running row an older daemon
// stored without them: its process already runs, so a path that no longer
// resolves keeps only its recorded spelling ("" here).
func restoredPlaces(root, cwd string) []string {
	var places []string
	if real, err := resolvePath(filepath.Clean(root)); err == nil {
		places = append(places, real)
	}
	if cwd != "" {
		if real, err := resolvePath(filepath.Clean(cwd)); err == nil {
			places = append(places, real)
		}
	}
	return places
}

// placesIn reports whether one of a job's places lies in one of a
// worktree's forms.
func placesIn(forms, places []string) bool {
	for _, form := range forms {
		for _, p := range places {
			if within(form, p) {
				return true
			}
		}
	}
	return false
}

// jobIn reports whether j runs in one of a worktree's forms, by its
// recorded root and working directory or their resolved spellings (from
// admission, or from restore for a row an older daemon wrote).
func jobIn(forms []string, j *hostedJob) bool {
	return placesIn(forms, []string{j.row.Root, j.row.Cwd, j.row.RealRoot, j.row.RealCwd}) || placesIn(forms, j.real)
}

// fencedRootLocked is the fenced worktree j would run in, "" when none,
// and the reason its fence gives. Under mu.
func (h *Hosted) fencedRootLocked(j *hostedJob) (root, reason string) {
	for root, fence := range h.rootFences {
		if jobIn(fence.forms, j) {
			return root, fence.holds.reason()
		}
	}
	return "", ""
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
		if !j.row.Terminal() && jobIn(forms, j) {
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
	return h.FenceRootAs(root, FenceReleasing)
}

// FenceRootAs is FenceRoot whose refusal of a new job names reason
// (FenceReleasing, FenceAcquiring); while fences with different reasons
// overlap, the refusal names the latest one still held.
func (h *Hosted) FenceRootAs(root, reason string) (unfence func(), err error) {
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
	hold := &fenceHold{reason: reason}
	h.rootFences[root].holds = append(h.rootFences[root].holds, hold)
	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if f := h.rootFences[root]; f != nil {
				if f.holds = f.holds.drop(hold); len(f.holds) == 0 {
					delete(h.rootFences, root)
				}
			}
		})
	}, nil
}

// rootFence is one reserved worktree: its spellings and the reservations
// holding it, oldest first.
type rootFence struct {
	forms []string
	holds fenceHolds
}

// fenceHold is one fence on a worktree and why it was taken (the wording
// of the refusal it causes).
type fenceHold struct{ reason string }

// fenceHolds are the fences live on one worktree, oldest first.
type fenceHolds []*fenceHold

// reason is the latest live fence's reason, so a refusal never names a
// fence already lifted.
func (hs fenceHolds) reason() string {
	if len(hs) == 0 {
		return ""
	}
	return hs[len(hs)-1].reason
}

// drop removes hold, keeping the others in order.
func (hs fenceHolds) drop(hold *fenceHold) fenceHolds {
	for i, have := range hs {
		if have == hold {
			return append(hs[:i:i], hs[i+1:]...)
		}
	}
	return hs
}
