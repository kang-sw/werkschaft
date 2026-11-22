// agent_spawn.go holds agent spawn, child control, and listing.
package wsagent

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

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsgit"
)

// AgentSpawn starts a child of caller (agent.spawn).
func (r *Runner) AgentSpawn(caller AgentCaller, req AgentSpawnRequest) (AgentSpawnResult, error) {
	const tool = "agent.spawn"
	if strings.TrimSpace(req.Prompt) == "" {
		return AgentSpawnResult{}, badRequest("%s: prompt is required", tool)
	}
	level := PermissionLevel(strings.TrimSpace(req.Authority))
	if level == "" {
		return AgentSpawnResult{}, badRequest("%s: authority is required: read-only, workdir-write, or full", tool)
	}
	if level.rank() == 0 {
		return AgentSpawnResult{}, badRequest("%s: authority must be read-only, workdir-write, or full; got %q", tool, req.Authority)
	}
	if req.SystemPrompt != "" && req.SystemPromptPath != "" {
		return AgentSpawnResult{}, badRequest("%s: give system_prompt or system_prompt_path, not both", tool)
	}
	if strings.TrimSpace(req.Cwd) != "" && strings.TrimSpace(req.Worktree) != "" {
		return AgentSpawnResult{}, badRequest("%s: give cwd or worktree, not both", tool)
	}
	depth := 0
	if h := caller.holder; h != nil {
		d, ok := r.depth(*h)
		if !ok {
			return AgentSpawnResult{}, &requestError{status: http.StatusForbidden, msg: fmt.Sprintf("%s: this conversation's spawn chain cannot be traced to its lead, so its depth is unknown and it spawns no subagent", tool)}
		}
		depth = d
	}
	if depth+1 > MaxSpawnDepth {
		return AgentSpawnResult{}, depthRefusal(tool, depth)
	}
	harness, bare, mapping, err := r.resolveTier(caller, req.Tier)
	if err != nil {
		return AgentSpawnResult{}, err
	}
	if h := caller.holder; h != nil {
		if h.Tier == "" {
			return AgentSpawnResult{}, &requestError{status: http.StatusForbidden, msg: fmt.Sprintf("%s: this conversation has no recorded tier, so no tier cap for its subagents can be set; it spawns none", tool)}
		}
		if tierRank(bare) > tierRank(h.Tier) {
			return AgentSpawnResult{}, &requestError{status: http.StatusForbidden, msg: fmt.Sprintf("%s: tier %q is above this conversation's own tier %q, which caps its subagents' tier; pick %q or lower", tool, bare, h.Tier, h.Tier)}
		}
	}
	ceiling, err := r.agentCeiling(caller.Parent, true)
	if err != nil {
		return AgentSpawnResult{}, &requestError{status: http.StatusForbidden, msg: fmt.Sprintf("%s: permission admission: %v", tool, err)}
	}
	if level.rank() > ceiling.rank() {
		return AgentSpawnResult{}, &requestError{status: http.StatusForbidden, msg: fmt.Sprintf("%s: authority %s is above this session's own level %s, which caps its children; pick %s or lower", tool, level, ceiling, ceiling)}
	}
	system := req.SystemPrompt
	if path := strings.TrimSpace(req.SystemPromptPath); path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(caller.base(), path)
		}
		raw, err := readBounded(path, maxSystemPrompt)
		if err != nil {
			return AgentSpawnResult{}, badRequest("%s: system_prompt_path: %v", tool, err)
		}
		system = string(raw)
	}
	root, cwd := caller.Root, strings.TrimSpace(req.Cwd)
	if wt := strings.TrimSpace(req.Worktree); wt != "" {
		if !filepath.IsAbs(wt) {
			wt = filepath.Join(caller.base(), wt)
		}
		wt = filepath.Clean(wt)
		if _, err := os.Lstat(filepath.Join(wt, ".git")); err != nil {
			return AgentSpawnResult{}, badRequest("%s: worktree %s is not a git worktree root (no .git entry)", tool, wt)
		}
		// An early check for the refusal that names re-acquiring; the create
		// below holds the reservation itself until the child is registered.
		unlockWorktree, err := wsgit.ReserveWorktreeSpawn(wt)
		if err != nil {
			if errors.Is(err, wsgit.ErrWorktreeReleasing) {
				return AgentSpawnResult{}, conflict("%s: worktree %s is currently being released; wait for release to finish and re-acquire before spawning", tool, wt)
			}
			return AgentSpawnResult{}, badRequest("%s: reserve worktree %s: %v", tool, wt, err)
		}
		unlockWorktree()
		root, cwd = wt, wt
	} else if cwd == "" {
		cwd = caller.base()
	} else if !filepath.IsAbs(cwd) {
		cwd = filepath.Join(caller.base(), cwd)
	}
	label := req.Label
	if strings.TrimSpace(label) == "" {
		label = req.Prompt
	}
	parent := caller.Parent
	claimWidth, releaseWidth, err := r.reserveSpawnWidth(parent)
	if err != nil {
		return AgentSpawnResult{}, err
	}
	defer releaseWidth()
	st, err := r.Spawn(SpawnSpec{
		Profile:            r.opts.Backends[harness].WorkerProfile,
		Root:               root,
		Cwd:                cwd,
		Model:              mapping.Model,
		Effort:             mapping.Effort,
		Tier:               bare,
		Prompt:             req.Prompt,
		Label:              label,
		Parent:             &parent,
		PermissionLevel:    level,
		delegatedAuthority: true,
		SystemPrompt:       system,
		claimWidth:         claimWidth,
	})
	if err != nil {
		return AgentSpawnResult{}, err
	}
	// A close of a holder that ran meanwhile listed its descendants before
	// this child existed: the child closes with it here.
	if h := caller.holder; h != nil {
		if c := r.conv(h.ReplyID); c == nil || c.isClosed() {
			_, _ = r.CloseConversation(st.ID)
			return AgentSpawnResult{}, errClosed
		}
	}
	return AgentSpawnResult{Address: AgentAddress(st.ID), Status: AgentStatus(st)}, nil
}

// reserveBoundWorktrees takes the spawn reservation of every worktree root at
// or above each of paths, once per root, until the returned unlock runs.
// worktree.release counts a conversation as bound when its canonical root
// equals the worktree or its canonical cwd lies anywhere within it
// (ActiveChildrenInWorktree), so every conversation create reserves each
// worktree that could count it: the walk starts from the canonical path, so
// a symlink from outside still reserves the worktree it resolves into, and it
// does not stop at a nested repo or submodule, whose enclosing worktree counts
// the conversation too. A path outside any worktree reserves nothing. A
// reservation that cannot be taken refuses like a release in progress
// (ErrWorktreeReleasing) does: an unreserved create could slip in beside a
// release that already holds the lock (boundReservationRefusal).
func reserveBoundWorktrees(paths ...string) (func(), error) {
	var unlocks []func()
	unlockAll := func() {
		for i := len(unlocks) - 1; i >= 0; i-- {
			unlocks[i]()
		}
	}
	seen := map[string]bool{}
	for _, path := range paths {
		for _, dir := range enclosingWorktreeRoots(path) {
			if seen[dir] {
				continue
			}
			seen[dir] = true
			unlock, err := wsgit.ReserveWorktreeSpawn(dir)
			if err != nil {
				unlockAll()
				if errors.Is(err, wsgit.ErrWorktreeReleasing) {
					return nil, err
				}
				return nil, fmt.Errorf("could not take the release reservation of worktree %s: %w", dir, err)
			}
			unlocks = append(unlocks, unlock)
		}
	}
	return unlockAll, nil
}

// boundReservationRefusal is the conflict a create answers when
// reserveBoundWorktrees refuses it.
func boundReservationRefusal(err error) error {
	if errors.Is(err, wsgit.ErrWorktreeReleasing) {
		return conflict("%v; wait for the release to finish before opening a conversation there", err)
	}
	return conflict("%v; no conversation opens there unreserved, since a concurrent worktree.release could miss it; try again", err)
}

// enclosingWorktreeRoots returns every directory at or above path's
// canonical form (canonicalWorktreePath, the form release compares) that
// holds a .git entry, innermost first.
func enclosingWorktreeRoots(path string) []string {
	path = canonicalWorktreePath(path)
	if path == "" {
		return nil
	}
	var roots []string
	for dir := path; ; {
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
			roots = append(roots, dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return roots
		}
		dir = parent
	}
}

func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if info.Size() > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return os.ReadFile(path)
}

// ownAgent is address's conversation id when it is caller's direct child,
// else an ownership error.
func (r *Runner) ownAgent(tool string, caller AgentCaller, address string) (string, error) {
	id := agentID(address)
	if rec, ok := r.recordOf(id); ok && rec.Parent != nil && *rec.Parent == caller.Parent {
		return id, nil
	}
	return "", &requestError{status: http.StatusNotFound, msg: fmt.Sprintf("%s: %s is not an agent this session spawned (agent.list lists them)", tool, address)}
}

// AgentInterrupt interrupts caller's child's running turn and hands back
// the messages it had not taken yet; the child stays.
func (r *Runner) AgentInterrupt(caller AgentCaller, address string) (AgentInterruptResult, error) {
	id, err := r.ownAgent("agent.interrupt", caller, address)
	if err != nil {
		return AgentInterruptResult{}, err
	}
	st, returned, err := r.Interrupt(id)
	if err != nil {
		return AgentInterruptResult{}, err
	}
	if returned == nil {
		returned = []string{}
	}
	return AgentInterruptResult{Address: AgentAddress(id), Status: AgentStatus(st), Returned: returned}, nil
}

// BoundWorktreeChild identifies a non-closed conversation bound to a worktree.
type BoundWorktreeChild struct {
	Address string `json:"address"`
	State   string `json:"state"`
	Cwd     string `json:"cwd,omitempty"`
}

// ActiveChildrenInWorktree returns every non-closed conversation in the runner
// whose canonical root equals worktree or whose canonical working directory is
// worktree or within it, oldest first then by address. Reads live state from
// memory without touching disk records.
func (r *Runner) ActiveChildrenInWorktree(worktree string) []BoundWorktreeChild {
	target := canonicalWorktreePath(worktree)
	if target == "" {
		return nil
	}
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	type match struct {
		createdAt string
		child     BoundWorktreeChild
	}
	var matches []match
	for _, c := range convs {
		st := c.memoryState()
		if st.Status == StatusClosed || st.Status == StatusHandedOff {
			continue
		}
		convRoot := canonicalWorktreePath(st.Root)
		convCwd := canonicalWorktreePath(st.Cwd)
		if !sameWorktreePath(convRoot, target) && !worktreePathContains(target, convCwd) {
			continue
		}
		matches = append(matches, match{
			createdAt: st.CreatedAt,
			child: BoundWorktreeChild{
				Address: AgentAddress(st.ID),
				State:   AgentStatus(st),
				Cwd:     st.Cwd,
			},
		})
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].createdAt != matches[j].createdAt {
			return matches[i].createdAt < matches[j].createdAt
		}
		return matches[i].child.Address < matches[j].child.Address
	})
	out := make([]BoundWorktreeChild, len(matches))
	for i, m := range matches {
		out[i] = m.child
	}
	return out
}

func canonicalWorktreePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	p = filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(resolved)
	}
	return p
}

func sameWorktreePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func worktreePathContains(root, path string) bool {
	if root == "" || path == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	if root == path {
		return true
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

// AgentList lists caller's direct children, oldest first.
func (r *Runner) AgentList(caller AgentCaller) []AgentChild {
	states := r.Children(caller.Parent)
	out := make([]AgentChild, 0, len(states))
	for _, st := range states {
		rec, _ := r.recordOf(st.ID)
		activity := &AgentChildActivity{
			LastActiveAt:   rec.LastActiveAt,
			AwaitingReport: st.Status != StatusClosed && st.Status != StatusHandedOff && rec.TurnOpen || len(rec.PendingReports) > 0,
		}
		if closed, err := time.Parse(time.RFC3339, rec.ClosedAt); err == nil && closed.After(lastActive(rec)) {
			activity.LastActiveAt = rec.ClosedAt
		}
		child := AgentChild{
			Address:   AgentAddress(st.ID),
			Label:     rec.Label,
			Tier:      st.Backend,
			Authority: childLevel(wsdashboard.SessionDir(r.opts.Dir, st.ID)),
			State:     AgentStatus(st),
			Model:     st.Model,
			Effort:    st.Effort,
			Cwd:       rec.workDir(),
			Held:      st.Held,
			Error:     st.Error,
			CreatedAt: st.CreatedAt,
			Activity:  activity,
		}
		if rec.Tier != "" {
			child.Tier = st.Backend + "." + rec.Tier
		}
		if last := rec.LastTurn; last != nil {
			child.LastReport = reportSummary(last.Result)
			activity.AwaitingReport = activity.AwaitingReport || rec.ReportID != "" && rec.ReportID != rec.LastReportedTurn
			// A settled interruption may leave its reason in State.Error.
			activity.SettledInterruptError = last.Result.Status == TurnInterrupted && last.Result.Error == st.Error
		}
		out = append(out, child)
	}
	return out
}
