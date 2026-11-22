package execjob

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsgit"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wskey"
)

// Launch approves req for its owner, starts it under a new holder, and
// waits through the execution window, which starts with the process: an
// end inside it answers OutcomeDone, otherwise the job goes on as async and
// owes its owner one completion notice. ctx's end inside the window (the
// caller's interrupt) stops the job; drain's (a daemon replacement)
// converts it instead.
func (h *Hosted) Launch(ctx context.Context, drain <-chan struct{}, req HostedLaunch) (HostedView, error) {
	if len(req.Argv) == 0 || strings.TrimSpace(req.Argv[0]) == "" {
		return HostedView{}, errors.New("nothing to run")
	}
	owner, err := h.opts.Sessions.Owner(req.Owner)
	if err != nil {
		return HostedView{}, fmt.Errorf("exec: the owning session cannot take a job: %w", err)
	}
	if req.CheckGen && owner.Gen != req.ExpectGen {
		return HostedView{}, errors.New("exec: the owning session reopened since this request was made")
	}
	// The job's resolved places, so a worktree fence matches it however
	// its working directory was spelled and the process starts where
	// admission checked; once per launch, stored with the row. A place that
	// cannot be resolved refuses the launch (admittedPlaces), before
	// anything asks for approval.
	realRoot, realCwd, err := admittedPlaces(req.Root, req.Cwd)
	if err != nil {
		return HostedView{}, err
	}
	// The program resolves before the approval, so the prompt names the
	// binary that starts.
	argv := append([]string(nil), req.Argv...)
	bin, lookErr := lookProgram(argv[0], req.Environ)
	if lookErr != nil && !req.Legacy {
		return HostedView{}, lookErr
	}
	// A legacy launch of a missing program answers as the file-backed
	// launch does: a failed record under a durable key. Nothing runs, so
	// nothing asks for approval.
	var call HostedCall
	explicit := false
	if lookErr == nil {
		argv[0] = bin
		call = approvalCall(req, bin)
		if req.UserApproved {
			// The user's own request is this invocation's approval.
			explicit = true
		} else if explicit, err = h.opts.Sessions.Approve(ctx, owner.ID, call); err != nil {
			return HostedView{}, err
		}
	}
	var id string
	var j *hostedJob
	for attempt := 1; ; attempt++ {
		if id, err = h.newID(req.Legacy); err != nil {
			return HostedView{}, err
		}
		// A job that will launch a holder is pinned from its admission: a
		// stop that ends it before or while LaunchWith runs leaves a holder
		// this job does not record yet, and the job must not be pruned (nor
		// its id drawn again) until that holder is confirmed gone.
		j = &hostedJob{done: make(chan struct{}), out: h.rings.NewJob(), stdinOpen: !req.Legacy && !req.StdinSet, starting: true, holderPinned: lookErr == nil}
		j.row = HostedJobRow{ID: id, Legacy: req.Legacy, Owner: owner.ID, Gen: owner.Gen, Tool: req.Tool, Root: filepath.Clean(req.Root), Cwd: req.Cwd, RealRoot: realRoot, RealCwd: realCwd, Argv: argv, Command: req.Command, Shell: req.Shell, State: HostedRunning, StdinBytes: int64(len(req.Stdin)), Notify: req.Async}
		err = h.admit(j)
		if err == nil {
			break
		}
		h.rings.Release(j.out)
		// Another launch drew the same id and was admitted first.
		if !errors.Is(err, errIDTaken) || attempt == 16 {
			return HostedView{}, err
		}
	}
	// Registered: a close from here on stops this job (StopOwner). A close
	// and revival that completed between the owner read and the admission
	// (the fence lifted again) left the job on a generation nobody asked for:
	// read the live generation once more now, against the one the request was
	// made under (the owner read's, unless the request names its own).
	want := owner.Gen
	if req.CheckGen {
		want = req.ExpectGen
	}
	var recheck error
	if now, err := h.opts.Sessions.Owner(req.Owner); err != nil {
		recheck = fmt.Errorf("exec: the owning session cannot take a job: %w", err)
	} else if now.Gen != want {
		recheck = errors.New("exec: the owning session reopened since this request was made")
	}
	if recheck != nil {
		// No holder launched yet: nothing is left to retire.
		h.mu.Lock()
		j.holderPinned = false
		h.mu.Unlock()
		return h.failStart(j, "", recheck)
	}
	if lookErr != nil {
		return h.failStart(j, "", lookErr)
	}
	session, _ := json.Marshal(hostedHolderSession{Kind: HolderKindExec, ExecID: id, Owner: owner.ID})
	holder, err := h.opts.Holders.LaunchWith(wsholder.LaunchSpec{Session: session, RingBytes: hostedHolderRing, ExitCopy: h.exitCopyPath(id)})
	if err != nil {
		if holder != "" {
			// The holder registered but could not be attached: it runs, and
			// may write the exit copy at this job's id's path, so the job
			// stays pinned until that holder is confirmed gone.
			view, failed := h.failStart(j, "", fmt.Errorf("start the job's holder: %w", err))
			go h.retireHolder(j, holder)
			return view, failed
		}
		h.mu.Lock()
		j.holderPinned = false
		h.mu.Unlock()
		return h.failStart(j, "", fmt.Errorf("start the job's holder: %w", err))
	}
	h.mu.Lock()
	if j.row.Terminal() {
		// Stopped (its owner closing) while the holder launched: the
		// process never starts, and the job stays pinned until its holder
		// is gone.
		h.mu.Unlock()
		go h.retireHolder(j, holder)
		return h.view(j, OutcomeDone), errClosedBeforeStart
	}
	// The job records its holder now: its end and stop handle that holder.
	j.holderPinned = false
	j.row.HolderID = holder
	h.byHolder[holder] = j
	h.mu.Unlock()
	// The live mode decides the start: an allow by role or mode that no
	// longer holds asks the user now.
	for !explicit {
		ok, err := h.opts.Sessions.Immediate(owner.ID)
		if err != nil {
			return h.failStart(j, holder, err)
		}
		if ok {
			break
		}
		if explicit, err = h.opts.Sessions.Approve(ctx, owner.ID, call); err != nil {
			return h.failStart(j, holder, err)
		}
	}
	h.mu.Lock()
	if j.stopping || h.ownerFences[owner.ID] {
		// Its owner closed (or the job was stopped) while it waited for
		// approval: the process never starts. A stop already ended the job
		// and its holder (stop); a fence alone fails the start here.
		if j.row.Terminal() {
			h.mu.Unlock()
			return h.view(j, OutcomeDone), errClosedBeforeStart
		}
		h.mu.Unlock()
		return h.failStart(j, holder, errClosedBeforeStart)
	}
	// From here a stop waits for the hand-over and then ends the process.
	j.starting = false
	spawned := make(chan struct{})
	j.spawned = spawned
	j.row.StartedAt = h.opts.Now().UnixMilli()
	h.mu.Unlock()
	// realCwd is in the ordinary spelling, never the extended-length form:
	// CreateProcess refuses \\?\ as a current directory. A Windows cwd past
	// MAX_PATH fails at process start, as it did before admission resolved
	// it (the path reached the same CreateProcess).
	st, err := h.opts.Holders.Spawn(holder, wsholder.SpawnSpec{Argv: argv, Cwd: realCwd, Env: environMap(req.Environ), Mode: "pipe", Input: req.Stdin, InputEOF: req.Legacy || req.StdinSet})
	close(spawned)
	if err != nil {
		if st.PID != 0 {
			// The child started and only its remaining input failed.
			h.mu.Lock()
			j.row.PID = st.PID
			h.mu.Unlock()
		}
		return h.failStart(j, holder, fmt.Errorf("start %s: %w", argv[0], err))
	}
	h.mu.Lock()
	j.row.PID = st.PID
	if !j.row.Terminal() {
		row, rev := h.snapLocked(j)
		h.mu.Unlock()
		h.save(j, row, rev)
	} else {
		h.mu.Unlock()
	}
	if req.Async {
		return h.view(j, OutcomeAsync), nil
	}
	wait := h.opts.Window
	if req.Window > 0 {
		wait = req.Window
	}
	window := time.NewTimer(wait)
	defer window.Stop()
	select {
	case <-j.done:
		return h.view(j, OutcomeDone), nil
	case <-window.C:
		return h.convert(j, req.OnConvert), nil
	case <-drain:
		return h.convert(j, req.OnConvert), nil
	case <-ctx.Done():
		h.stop(j)
		return h.view(j, OutcomeDone), ctx.Err()
	}
}

// convert makes j async when it still runs; an end that won the race is an
// OutcomeDone. The outcome is committed once, under h.mu: a job found running
// there is converted for good (it owes its notice from that moment), whatever
// happens after, and one found ended is done and owes none. The caller's
// onConvert then publishes the job's id with the completion notice held back
// (publishing), so an end that follows can only order its notice after the id,
// and the converted view is the one taken at the commit.
func (h *Hosted) convert(j *hostedJob, onConvert func(HostedView)) HostedView {
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return h.view(j, OutcomeDone)
	}
	j.row.Notify = true
	j.publishing = onConvert != nil
	v := HostedView{HostedJobRow: h.rowLocked(j), Outcome: OutcomeConverted}
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
	if onConvert != nil {
		func() {
			defer func() {
				h.mu.Lock()
				j.publishing = false
				h.mu.Unlock()
				// An end that came while the id was being published left its
				// notice pending; it goes now.
				h.deliver(j)
			}()
			onConvert(v)
		}()
	}
	return v
}

// admit registers j, refusing a new job once the running ones and the
// dropped ones whose row delete keeps failing fill the job cap, and drops
// the oldest finished jobs over it. It retries the failed deletes first.
func (h *Hosted) admit(j *hostedJob) error {
	h.mu.Lock()
	var retry []string
	var claim uint64
	if !h.closed {
		retry, claim = h.claimRetriesLocked()
	}
	h.mu.Unlock()
	h.forget(nil, retry, claim)
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return errors.New("exec: the daemon is shutting down")
	}
	if h.ownerFences[j.row.Owner] {
		h.mu.Unlock()
		return errors.New("exec: the owning session is closing")
	}
	if root, reason := h.fencedRootLocked(j); root != "" {
		h.mu.Unlock()
		return fmt.Errorf("exec: the worktree %s is %s", root, reason)
	}
	// newID checked the id without holding h.mu through here: a launch
	// that drew the same id may have been admitted since.
	_, taken := h.jobs[j.row.ID]
	_, pending := h.deleting[j.row.ID]
	if taken || pending || h.retiring[j.row.ID] > 0 {
		h.mu.Unlock()
		return errIDTaken
	}
	running, held := 0, 0
	var finished []*hostedJob
	for _, have := range h.jobs {
		switch {
		case !have.row.Terminal():
			running++
		case have.holderPinned || h.retiring[have.row.ID] > 0:
			// A holder of it may still write its exit copy: it stays, and
			// counts toward the cap, until that holder is gone.
			held++
		case have.row.Notice == NoticePending || have.delivering:
			// Its notice is still owed; it stays until delivered.
		default:
			finished = append(finished, have)
		}
	}
	if running >= HostedMaxJobs {
		h.mu.Unlock()
		return fmt.Errorf("exec: %d jobs already run; stop one first", running)
	}
	orphans := 0
	for id := range h.retiring {
		if _, ok := h.jobs[id]; !ok {
			// A predecessor's holder no job owns holds its id the same way.
			orphans++
		}
	}
	held += orphans
	// Every id that stays taken fills the cap together: the running jobs,
	// the dropped jobs whose row delete keeps failing, and the ended jobs
	// (or bare ids) whose holders are still being stopped.
	if pending := len(h.deleting); running+pending+held >= HostedMaxJobs {
		h.mu.Unlock()
		var waits []string
		if pending > 0 {
			waits = append(waits, fmt.Sprintf("%d finished jobs' records could not be deleted from the job store", pending))
		}
		if held > 0 {
			waits = append(waits, fmt.Sprintf("%d ended jobs wait for their holders to stop", held))
		}
		return fmt.Errorf("exec: %d jobs run and %s; retry later, or stop a job", running, strings.Join(waits, " and "))
	}
	h.jobs[j.row.ID] = j
	var dropped []*hostedJob
	var ids []string
	// The dropped jobs' tombstones are this prune's from the start: no
	// retry deletes their rows before forget has fenced their writes.
	drop := h.claimLocked()
	// The bare reserved ids take room under the cap as job rows do.
	if over := len(h.jobs) + orphans - HostedMaxJobs; over > 0 {
		sort.Slice(finished, func(a, b int) bool { return finished[a].row.EndedAt < finished[b].row.EndedAt })
		// The batch fits the tombstones' room under the job cap, so even a
		// failed bulk delete parks no more than the cap; jobs left over
		// go at a later launch.
		room := max(HostedMaxJobs-len(h.deleting), 0)
		for _, old := range finished[:min(over, len(finished), room)] {
			delete(h.jobs, old.row.ID)
			if h.byHolder[old.row.HolderID] == old {
				delete(h.byHolder, old.row.HolderID)
			}
			h.deleting[old.row.ID] = &tombstone{holder: old.row.HolderID, claim: drop}
			h.rings.Release(old.out)
			dropped = append(dropped, old)
			ids = append(ids, old.row.ID)
		}
	}
	h.mu.Unlock()
	// Deferred, so a panicking OnRunning still fences the dropped jobs
	// and settles their claim.
	defer h.forget(dropped, ids, drop)
	h.runningChanged()
	return nil
}

// errClosedBeforeStart answers a launch whose owner closed (or whose job
// was stopped) before its process started.
var errClosedBeforeStart = errors.New("exec: the owning session closed before the job started")

// failStart ends j, whose process did not start as asked, and its holder.
// The holder is stopped first, because a child may run under it (a spawn
// that failed only on its remaining input): until that stop is confirmed
// the job stays running, so its owner's close and the running counts still
// see it, and a later stop retries the holder. A job a stop already ended
// keeps that end.
func (h *Hosted) failStart(j *hostedJob, holder string, cause error) (HostedView, error) {
	h.mu.Lock()
	if j.row.Terminal() {
		if holder != "" {
			delete(h.byHolder, holder)
		}
		h.mu.Unlock()
		if holder != "" {
			go h.stopHolder(holder)
		}
		return h.view(j, OutcomeDone), cause
	}
	j.row.Notify = false
	j.stopping = true
	h.mu.Unlock()
	if holder != "" {
		if err := h.opts.Holders.Stop(holder); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
			h.mu.Lock()
			if !j.row.Terminal() {
				j.stopping = false
				j.row.Error = cause.Error() + "; its holder did not stop, so the job may still run: " + err.Error()
				row, rev := h.snapLocked(j)
				h.mu.Unlock()
				h.save(j, row, rev)
				return h.view(j, 0), fmt.Errorf("%w; its holder did not stop, so the job may still run: %v", cause, err)
			}
			h.mu.Unlock()
		}
	}
	h.mu.Lock()
	if holder != "" {
		delete(h.byHolder, holder)
	}
	// An exit record the stop brought ended the job as stopped; the start's
	// failure is still its story.
	wasEnded := j.row.Terminal()
	j.row.State, j.row.Error = HostedFailed, cause.Error()
	if j.row.StartedAt == 0 {
		j.row.StartedAt = h.opts.Now().UnixMilli()
	}
	if !wasEnded {
		h.endLocked(j)
	}
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	if !wasEnded {
		h.runningChanged()
	}
	h.save(j, row, rev)
	return h.view(j, OutcomeDone), cause
}

// errIDTaken: admit found the job's id taken; the launch draws again.
var errIDTaken = errors.New("exec: the drawn exec id is taken")

// newExecKey draws a three-word exec key (a test seam).
var newExecKey = wskey.Generate

// newID draws an unused exec id: three words for the new tools; for a
// legacy launch the long legacy key form, whose 64 random bits keep it
// apart from the file-backed jobs' keys without opening their store.
func (h *Hosted) newID(legacy bool) (string, error) {
	for i := 0; i < 16; i++ {
		var id string
		if legacy {
			var b [8]byte
			if _, err := rand.Read(b[:]); err != nil {
				return "", err
			}
			id = "exec-" + strconv.FormatInt(h.opts.Now().UnixMilli(), 10) + "-" + hex.EncodeToString(b[:])
		} else {
			key, err := newExecKey()
			if err != nil {
				return "", err
			}
			id = "exec_" + key
		}
		// An id whose dropped row is still being deleted stays reserved:
		// reusing it would overwrite that row and its exit copy. So does
		// one a predecessor's holder still being stopped runs under.
		h.mu.Lock()
		_, taken := h.jobs[id]
		_, pending := h.deleting[id]
		retiring := h.retiring[id] > 0
		h.mu.Unlock()
		taken = taken || pending || retiring
		if !taken {
			return id, nil
		}
	}
	return "", errors.New("exec: could not draw an unused exec id")
}

// lookProgram resolves a bare program name on the job's PATH, not the
// daemon's.
func lookProgram(name string, environ []string) (string, error) {
	if strings.ContainsAny(name, `/\`) {
		return name, nil
	}
	bin, err := wsgit.LookPathEnv(name, environ)
	if err != nil {
		return "", fmt.Errorf("exec: %s is not on the job's PATH: %w", name, err)
	}
	return bin, nil
}

// approvalCall is req as its approval shows it, starting program.
func approvalCall(req HostedLaunch, program string) HostedCall {
	call := HostedCall{Tool: req.Tool, Command: req.Display, Cwd: req.Cwd, Program: program, StdinBytes: len(req.Stdin)}
	for name, value := range req.Env {
		call.Env = append(call.Env, name+"="+value)
	}
	sort.Strings(call.Env)
	head := req.Stdin
	if len(head) > HostedStdinPreviewBytes {
		head = head[:HostedStdinPreviewBytes]
		// Cut at a character's start, not inside one.
		for i := len(head) - 1; i >= 0 && i >= len(head)-utf8.UTFMax; i-- {
			if utf8.RuneStart(head[i]) {
				if !utf8.FullRune(head[i:]) {
					head = head[:i]
				}
				break
			}
		}
	}
	call.Stdin = strings.ToValidUTF8(string(head), "�")
	return call
}

// environMap is environ as the holder's whole child environment; a later
// entry of a name wins, as in an overlay.
func environMap(environ []string) map[string]string {
	out := make(map[string]string, len(environ))
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok && name != "" {
			if goruntime.GOOS == "windows" {
				for have := range out {
					if strings.EqualFold(have, name) && have != name {
						delete(out, have)
					}
				}
			}
			out[name] = value
		}
	}
	return out
}

// ShellLaunch prepares the hosted launch of one bash line: cwd resolved
// inside root (cwdArg empty is root itself), environ overlaid with overlay,
// and the shell chosen by ShellArgv. exec.shell and the dashboard composer
// both build their launch here, so the shell, the working directory and the
// environment are one decision. The caller sets what is its own (Async,
// Stdin, Window, approval).
func ShellLaunch(owner, tool, root, cwdArg, line string, environ []string, overlay map[string]string) (HostedLaunch, error) {
	cwd, err := ResolveWorkingDir(root, cwdArg)
	if err != nil {
		return HostedLaunch{}, err
	}
	if strings.TrimSpace(line) == "" {
		return HostedLaunch{}, errors.New(tool + ": cmd is required")
	}
	environ = OverlayEnv(environ, overlay)
	argv, err := ShellArgv(line, environ)
	if err != nil {
		return HostedLaunch{}, err
	}
	return HostedLaunch{Owner: owner, Tool: tool, Root: root, Cwd: cwd, Environ: environ, Env: overlay, Argv: argv, Display: line, Command: line, Shell: argv[0]}, nil
}

// ShellArgv is the new exec.shell form's argv for line: Bash on the job's
// PATH (Git for Windows' bash on Windows), else /bin/sh on POSIX.
func ShellArgv(line string, environ []string) ([]string, error) {
	if goruntime.GOOS == "windows" {
		if git, err := wsgit.LookPathEnv("git", environ); err == nil {
			// <Git>\cmd\git.exe or <Git>\bin\git.exe -> <Git>\bin\bash.exe
			bash := filepath.Join(filepath.Dir(filepath.Dir(git)), "bin", "bash.exe")
			if info, statErr := os.Stat(bash); statErr == nil && !info.IsDir() {
				return []string{bash, "-c", line}, nil
			}
		}
		if bash, err := wsgit.LookPathEnv("bash", environ); err == nil {
			return []string{bash, "-c", line}, nil
		}
		return nil, errors.New("exec.shell: no Git Bash found (install Git for Windows, or use exec.run with another shell)")
	}
	if bash, err := wsgit.LookPathEnv("bash", environ); err == nil {
		return []string{bash, "-c", line}, nil
	}
	return []string{"/bin/sh", "-c", line}, nil
}
