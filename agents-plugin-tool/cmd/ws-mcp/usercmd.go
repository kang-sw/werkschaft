package main

import (
	"fmt"
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
	CronKnown(id string) bool
}

// userCommands binds the user commands' registry to the open hosted leads. It
// is both the dashboard's service (wsdashboard.UserCommandService) and the MCP
// tools' (mcp.UserCommandHost): the agent's register and unregister tools, the
// page's list, Run and remove control all go through the one registry. The
// lead's bound worktree is read from the runner's memory on every call (no key
// record, no file), so no read is cached.
type userCommands struct {
	reg    *wsusercmd.Registry
	runner userCommandRunner
}

// openUserCommands opens the registry in the daemon's state directory, drops
// the commands of conversations the runner no longer knows, and returns the
// binding. onChange is the dashboard's refresh signal.
func openUserCommands(dir string, runner userCommandRunner, onChange func(string), logw io.Writer) (*userCommands, error) {
	reg, err := wsusercmd.Open(filepath.Join(dir, wsusercmd.StoreName), onChange)
	if err != nil {
		return nil, err
	}
	u := &userCommands{reg: reg, runner: runner}
	if err := reg.Reconcile(runner.CronKnown); err != nil {
		fmt.Fprintf(logw, "dashboard: user commands: drop commands of unknown conversations: %v\n", err)
	}
	return u, nil
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
// strip refetches when the lead's worktree changes (the registry's own revision
// is per conversation).
func rootTag(root string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(filepath.Clean(root)))
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

// List is the lead's commands in its bound worktree and the revision they carry
// (the page's list and its refetch): one registry snapshot, so the revision
// always belongs to the list.
func (u *userCommands) List(replyID string) ([]wsusercmd.Command, string, error) {
	root, err := u.root(replyID)
	if err != nil {
		return nil, "", err
	}
	cmds, rev := u.reg.Snapshot(replyID, root)
	return cmds, rev + "." + rootTag(root), nil
}

// Get returns one command of the lead's bound worktree and that worktree (a
// click runs the stored line only when this read still holds the displayed
// revision, and only in the worktree it was read for).
func (u *userCommands) Get(replyID, id string) (wsusercmd.Command, string, bool, error) {
	root, err := u.root(replyID)
	if err != nil {
		return wsusercmd.Command{}, "", false, err
	}
	c, ok := u.reg.Get(replyID, root, id)
	return c, root, ok, nil
}

// RemoveIf withdraws a command only while it has the revision the user was
// shown (the user's remove control): wsusercmd.ErrStale when it changed,
// wsusercmd.ErrUnknown when it is gone.
func (u *userCommands) RemoveIf(replyID, id string, rev int64) error {
	root, err := u.root(replyID)
	if err != nil {
		return err
	}
	return u.reg.RemoveIf(replyID, root, id, rev)
}

// Revision is the opaque revision of the lead's commands and worktree.
func (u *userCommands) Revision(replyID string) string {
	rev := u.reg.Revision(replyID)
	if root, err := u.root(replyID); err == nil {
		rev += "." + rootTag(root)
	}
	return rev
}

// Lead refuses a conversation that is not an open hosted lead (the MCP tools'
// authority check, read from the runner's memory).
func (u *userCommands) Lead(conversation string) error {
	_, err := u.runner.ComposerOpen(conversation)
	return err
}

// Register adds or replaces a command in the lead's bound worktree.
func (u *userCommands) Register(conversation string, in wsusercmd.Input) (wsusercmd.Command, wsusercmd.Outcome, error) {
	root, err := u.root(conversation)
	if err != nil {
		return wsusercmd.Command{}, 0, err
	}
	return u.reg.Set(conversation, root, in)
}

// Unregister withdraws a command of the lead's bound worktree, by ID: the
// agent's own tool keeps its by-ID semantics.
func (u *userCommands) Unregister(conversation, id string) error {
	root, err := u.root(conversation)
	if err != nil {
		return err
	}
	return u.reg.Remove(conversation, root, id)
}

// Commands lists the lead's commands for the agent (the tool's no-argument
// read).
func (u *userCommands) Commands(conversation string) ([]wsusercmd.Command, error) {
	cmds, _, err := u.List(conversation)
	return cmds, err
}
