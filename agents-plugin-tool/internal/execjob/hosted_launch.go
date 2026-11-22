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
	// The program resolves before the approval, so the prompt names the
	// binary that starts.
	argv := append([]string(nil), req.Argv...)
	bin, err := lookProgram(argv[0], req.Environ)
	if err != nil {
		return HostedView{}, err
	}
	argv[0] = bin
	call := approvalCall(req, bin)
	explicit, err := h.opts.Sessions.Approve(ctx, owner.ID, call)
	if err != nil {
		return HostedView{}, err
	}
	id, err := h.newID(req.Legacy)
	if err != nil {
		return HostedView{}, err
	}
	j := &hostedJob{done: make(chan struct{}), out: h.rings.NewJob(), stdinOpen: !req.Legacy && !req.StdinSet, starting: true}
	j.row = HostedJobRow{ID: id, Legacy: req.Legacy, Owner: owner.ID, Gen: owner.Gen, Tool: req.Tool, Root: filepath.Clean(req.Root), Cwd: req.Cwd, Argv: argv, Command: req.Command, Shell: req.Shell, State: HostedRunning, StdinBytes: int64(len(req.Stdin)), Notify: req.Async}
	if err := h.admit(j); err != nil {
		return HostedView{}, err
	}
	session, _ := json.Marshal(hostedHolderSession{Kind: HolderKindExec, ExecID: id, Owner: owner.ID})
	holder, err := h.opts.Holders.LaunchWith(wsholder.LaunchSpec{Session: session, RingBytes: hostedHolderRing, ExitCopy: h.exitCopyPath(id)})
	if err != nil {
		return h.failStart(j, "", fmt.Errorf("start the job's holder: %w", err))
	}
	h.mu.Lock()
	if j.row.Terminal() {
		// Stopped (its owner closing) while the holder launched: the
		// process never starts.
		h.mu.Unlock()
		go h.stopHolder(holder)
		return h.view(j, OutcomeDone), errClosedBeforeStart
	}
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
	st, err := h.opts.Holders.Spawn(holder, wsholder.SpawnSpec{Argv: argv, Cwd: req.Cwd, Env: environMap(req.Environ), Mode: "pipe", Input: req.Stdin, InputEOF: req.Legacy || req.StdinSet})
	close(spawned)
	if err != nil {
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
	window := time.NewTimer(h.opts.Window)
	defer window.Stop()
	select {
	case <-j.done:
		return h.view(j, OutcomeDone), nil
	case <-window.C:
		return h.convert(j), nil
	case <-drain:
		return h.convert(j), nil
	case <-ctx.Done():
		h.stop(j)
		return h.view(j, OutcomeDone), ctx.Err()
	}
}

// convert makes j async when it still runs; an end that won the race is an
// OutcomeDone.
func (h *Hosted) convert(j *hostedJob) HostedView {
	h.mu.Lock()
	if j.row.Terminal() {
		h.mu.Unlock()
		return h.view(j, OutcomeDone)
	}
	j.row.Notify = true
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
	return h.view(j, OutcomeConverted)
}

// admit registers j, refusing a new job once the running ones fill the job
// cap, and drops the oldest finished jobs over it.
func (h *Hosted) admit(j *hostedJob) error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return errors.New("exec: the daemon is shutting down")
	}
	if h.ownerFences[j.row.Owner] {
		h.mu.Unlock()
		return errors.New("exec: the owning session is closing")
	}
	if root := h.fencedRootLocked(j.row); root != "" {
		h.mu.Unlock()
		return fmt.Errorf("exec: the worktree %s is being released or removed", root)
	}
	running := 0
	var finished []*hostedJob
	for _, have := range h.jobs {
		switch {
		case !have.row.Terminal():
			running++
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
	h.jobs[j.row.ID] = j
	var dropped []*hostedJob
	var ids []string
	if over := len(h.jobs) - HostedMaxJobs; over > 0 {
		sort.Slice(finished, func(a, b int) bool { return finished[a].row.EndedAt < finished[b].row.EndedAt })
		for _, old := range finished[:min(over, len(finished))] {
			delete(h.jobs, old.row.ID)
			h.rings.Release(old.out)
			dropped = append(dropped, old)
			ids = append(ids, old.row.ID)
		}
	}
	h.mu.Unlock()
	h.runningChanged()
	if len(dropped) > 0 {
		h.forget(dropped, ids)
	}
	return nil
}

// errClosedBeforeStart answers a launch whose owner closed (or whose job
// was stopped) before its process started.
var errClosedBeforeStart = errors.New("exec: the owning session closed before the job started")

// failStart ends j before its process ran, stopping its holder. A job a
// stop already ended keeps that end.
func (h *Hosted) failStart(j *hostedJob, holder string, cause error) (HostedView, error) {
	if holder != "" {
		go h.stopHolder(holder)
	}
	h.mu.Lock()
	if j.row.Terminal() {
		if holder != "" {
			delete(h.byHolder, holder)
		}
		h.mu.Unlock()
		return h.view(j, OutcomeDone), cause
	}
	if holder != "" {
		delete(h.byHolder, holder)
	}
	j.row.State, j.row.Error = HostedFailed, cause.Error()
	j.row.Notify = false
	if j.row.StartedAt == 0 {
		j.row.StartedAt = h.opts.Now().UnixMilli()
	}
	h.endLocked(j)
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.runningChanged()
	h.save(j, row, rev)
	return h.view(j, OutcomeDone), cause
}

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
			key, err := wskey.Generate()
			if err != nil {
				return "", err
			}
			id = "exec_" + key
		}
		h.mu.Lock()
		_, taken := h.jobs[id]
		h.mu.Unlock()
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
