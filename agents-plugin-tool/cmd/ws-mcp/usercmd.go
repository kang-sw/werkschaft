package main

import (
	"hash/fnv"
	"io"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsusercmd"
)

// userCommandRunner is the runner side the user commands read: the live
// authority of an open hosted lead and the worktree it is bound to.
type userCommandRunner interface {
	ComposerOpen(id string) (uint64, error)
	ComposerRoot(id string) (wsagent.ExecOwner, string, error)
}

// userCommands binds the user commands' registry to the open hosted leads. It
// is both the dashboard's service (wsdashboard.UserCommandService) and the MCP
// tools' (mcp.UserCommandHost): the agent's register and unregister tools, the
// page's list, CRUD, Run and remove control all go through the one registry.
// The lead's bound worktree is read from the runner's memory on every call (no
// key record, no file), so no read is cached.
type userCommands struct {
	reg    *wsusercmd.Registry
	runner userCommandRunner
}

// openUserCommands opens the registry in the daemon's state directory and
// returns the binding. Every command, agent- or user-created, is kept per
// worktree and outlives the conversation that registered it.
func openUserCommands(dir string, runner userCommandRunner, onChange func(string), logw io.Writer) (*userCommands, error) {
	reg, err := wsusercmd.Open(filepath.Join(dir, wsusercmd.StoreName), onChange)
	if err != nil {
		return nil, err
	}
	return &userCommands{reg: reg, runner: runner}, nil
}

// Close releases the registry's store.
func (u *userCommands) Close() error { return u.reg.Close() }

// root is the worktree the open lead is bound to (a refusal when it is not an
// open hosted lead).
func (u *userCommands) root(replyID string) (string, error) {
	_, root, err := u.runner.ComposerRoot(replyID)
	return root, err
}

// rootTag is a short stable tag of a worktree, appended to the revision so the
// popover refetches when the lead's worktree changes (and bangHistTag keys
// local shell history per worktree).
func rootTag(root string) string {
	h := fnv.New32a()
	root = filepath.Clean(root)
	if runtime.GOOS == "windows" {
		// Same scope key as the registry's: Windows paths compare without case.
		root = strings.ToLower(root)
	}
	_, _ = h.Write([]byte(root))
	return strconv.FormatUint(uint64(h.Sum32()), 36)
}

// sameRoot reports whether two worktree paths name the same scope.
func sameRoot(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// List is the lead's merged commands in its bound worktree and the combined
// revision they carry: one registry snapshot, so the revision always belongs
// to the list.
func (u *userCommands) List(replyID string) ([]wsusercmd.Command, string, error) {
	root, err := u.root(replyID)
	if err != nil {
		return nil, "", err
	}
	cmds, rev := u.reg.Snapshot(replyID, root)
	return cmds, rev + "." + rootTag(root), nil
}

// Get returns one scope-qualified command of the lead's bound worktree and
// that worktree.
func (u *userCommands) Get(replyID, source, id string) (wsusercmd.Command, string, bool, error) {
	root, err := u.root(replyID)
	if err != nil {
		return wsusercmd.Command{}, "", false, err
	}
	c, ok := u.reg.GetBySource(replyID, root, source, id)
	return c, root, ok, nil
}

// Create creates a project-root-persistent user command in the lead's bound
// worktree after enforcing the synchronous worktree-root cwd fence.
func (u *userCommands) Create(replyID string, in wsusercmd.Input) (wsusercmd.Command, error) {
	root, err := u.root(replyID)
	if err != nil {
		return wsusercmd.Command{}, err
	}
	if in.Cwd, err = wsusercmd.ValidateWorktreeCwd(root, in.Cwd); err != nil {
		return wsusercmd.Command{}, err
	}
	return u.reg.CreateUser(root, in)
}

// PatchIf updates a command ("agent" or "user") only while it has revision
// rev, enforcing the synchronous worktree-root cwd fence when cwd is patched.
func (u *userCommands) PatchIf(replyID, source, id string, rev int64, patch wsusercmd.Patch) (wsusercmd.Command, error) {
	root, err := u.root(replyID)
	if err != nil {
		return wsusercmd.Command{}, err
	}
	if patch.Cwd != nil {
		checked, err := wsusercmd.ValidateWorktreeCwd(root, *patch.Cwd)
		if err != nil {
			return wsusercmd.Command{}, err
		}
		patch.Cwd = &checked
	}
	return u.reg.PatchIf(replyID, root, source, id, rev, patch)
}

// RemoveIf withdraws a command ("agent" or "user") only while it has the
// revision the user was shown: wsusercmd.ErrStale when it changed,
// wsusercmd.ErrUnknown when it is gone.
func (u *userCommands) RemoveIf(replyID, source, id string, rev int64) error {
	root, err := u.root(replyID)
	if err != nil {
		return err
	}
	return u.reg.RemoveIfSource(replyID, root, source, id, rev)
}

// Revision is the combined opaque revision of the lead's agent commands, its
// worktree's user commands, and its worktree tag (epoch.N.rootTag).
func (u *userCommands) Revision(replyID string) string {
	if root, err := u.root(replyID); err == nil {
		return u.reg.RevisionFor(replyID, root) + "." + rootTag(root)
	}
	return u.reg.Revision(replyID)
}

// Lead refuses a conversation that is not an open hosted lead (the MCP tools'
// authority check, read from the runner's memory).
func (u *userCommands) Lead(conversation string) error {
	_, err := u.runner.ComposerOpen(conversation)
	return err
}

// Register adds or replaces an agent command in the lead's bound worktree
// after enforcing the synchronous worktree-root cwd fence.
func (u *userCommands) Register(conversation string, in wsusercmd.Input) (wsusercmd.Command, wsusercmd.Outcome, error) {
	root, err := u.root(conversation)
	if err != nil {
		return wsusercmd.Command{}, 0, err
	}
	if in.Cwd, err = wsusercmd.ValidateWorktreeCwd(root, in.Cwd); err != nil {
		return wsusercmd.Command{}, 0, err
	}
	return u.reg.Set(conversation, root, in)
}

// Unregister withdraws an agent command of the lead's bound worktree, by ID:
// the agent's own tool keeps its by-ID semantics and never removes user scope.
func (u *userCommands) Unregister(conversation, id string) error {
	root, err := u.root(conversation)
	if err != nil {
		return err
	}
	return u.reg.Remove(conversation, root, id)
}

// Commands lists the lead's merged commands for the agent.
func (u *userCommands) Commands(conversation string) ([]wsusercmd.Command, error) {
	cmds, _, err := u.List(conversation)
	return cmds, err
}
