package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sync"
	"time"

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

// userCommandRootTTL is how long a lead's bound worktree, read once from the
// runner, serves the list and page reads: the read goes to the lead's key
// record, so the interactive paths (page load, the list refetch) reuse it
// instead of touching that file each time. A click, a removal and every tool
// call read it afresh.
const userCommandRootTTL = 10 * time.Second

// userCommands binds the user commands' registry to the open hosted leads. It
// is both the dashboard's service (wsdashboard.UserCommandService) and the MCP
// tools' (mcp.UserCommandHost): the agent's register and unregister tools, the
// page's list, Run and remove control all go through the one registry.
type userCommands struct {
	reg    *wsusercmd.Registry
	runner userCommandRunner
	now    func() time.Time

	mu    sync.Mutex
	roots map[string]userCommandRoot
}

type userCommandRoot struct {
	root string
	gen  uint64
	at   time.Time
}

// openUserCommands opens the registry in the daemon's state directory, drops
// the commands of conversations the runner no longer knows, and returns the
// binding. onChange is the dashboard's refresh signal.
func openUserCommands(dir string, runner userCommandRunner, onChange func(string), logw io.Writer) (*userCommands, error) {
	reg, err := wsusercmd.Open(filepath.Join(dir, wsusercmd.StoreName), onChange)
	if err != nil {
		return nil, err
	}
	u := &userCommands{reg: reg, runner: runner, now: time.Now, roots: map[string]userCommandRoot{}}
	if err := reg.Reconcile(runner.CronKnown); err != nil {
		fmt.Fprintf(logw, "dashboard: user commands: drop commands of unknown conversations: %v\n", err)
	}
	return u, nil
}

// Close releases the registry's store.
func (u *userCommands) Close() error { return u.reg.Close() }

// root is the worktree the open lead is bound to. fresh reads it from the
// runner; otherwise a read under the TTL, for the same open generation, is
// reused.
func (u *userCommands) root(replyID string, fresh bool) (string, error) {
	gen, err := u.runner.ComposerOpen(replyID)
	if err != nil {
		return "", err
	}
	now := u.now()
	u.mu.Lock()
	cached, ok := u.roots[replyID]
	u.mu.Unlock()
	if !fresh && ok && cached.gen == gen && now.Sub(cached.at) < userCommandRootTTL {
		return cached.root, nil
	}
	owner, root, err := u.runner.ComposerRoot(replyID)
	if err != nil {
		return "", err
	}
	u.mu.Lock()
	u.roots[replyID] = userCommandRoot{root: root, gen: owner.Gen, at: now}
	u.mu.Unlock()
	return root, nil
}

// List is the lead's commands in its bound worktree and their revision (the
// page's list and its refetch: memory only while the root is cached).
func (u *userCommands) List(replyID string, fresh bool) ([]wsusercmd.Command, string, error) {
	root, err := u.root(replyID, fresh)
	if err != nil {
		return nil, "", err
	}
	return u.reg.List(replyID, root), u.reg.Revision(replyID), nil
}

// Get returns one command of the lead's bound worktree, read afresh (a click
// runs the stored line only when this read still holds the displayed revision).
func (u *userCommands) Get(replyID, id string) (wsusercmd.Command, bool, error) {
	root, err := u.root(replyID, true)
	if err != nil {
		return wsusercmd.Command{}, false, err
	}
	c, ok := u.reg.Get(replyID, root, id)
	return c, ok, nil
}

// Remove withdraws a command, as the user's remove control and the agent's
// unregister tool both do.
func (u *userCommands) Remove(replyID, id string) (bool, error) {
	root, err := u.root(replyID, true)
	if err != nil {
		return false, err
	}
	switch err := u.reg.Remove(replyID, root, id); {
	case err == nil:
		return true, nil
	case errors.Is(err, wsusercmd.ErrUnknown):
		return false, nil
	default:
		return false, err
	}
}

// Revision is the opaque revision of the lead's commands.
func (u *userCommands) Revision(replyID string) string { return u.reg.Revision(replyID) }

// Lead refuses a conversation that is not an open hosted lead (the MCP tools'
// authority check, read from the runner's memory).
func (u *userCommands) Lead(conversation string) error {
	_, err := u.runner.ComposerOpen(conversation)
	return err
}

// Register adds or replaces a command in the lead's bound worktree.
func (u *userCommands) Register(conversation string, in wsusercmd.Input) (wsusercmd.Command, wsusercmd.Outcome, error) {
	root, err := u.root(conversation, true)
	if err != nil {
		return wsusercmd.Command{}, 0, err
	}
	return u.reg.Set(conversation, root, in)
}

// Unregister withdraws a command of the lead's bound worktree.
func (u *userCommands) Unregister(conversation, id string) error {
	ok, err := u.Remove(conversation, id)
	if err != nil {
		return err
	}
	if !ok {
		return wsusercmd.ErrUnknown
	}
	return nil
}

// Commands lists the lead's commands for the agent (the tool's no-argument
// read).
func (u *userCommands) Commands(conversation string) ([]wsusercmd.Command, error) {
	cmds, _, err := u.List(conversation, true)
	return cmds, err
}
