package wsagent

import (
	"errors"
	"fmt"
	"os"
	"sort"
)

// ComposerTarget is what a dashboard-run command (the composer's `!`) needs
// from an open hosted lead, read from the runner's own state and never from
// the browser: the owner and its open generation, the worktree root the
// lead is bound to, and the environment its harness runs under.
type ComposerTarget struct {
	Owner   ExecOwner
	Root    string
	Environ []string
}

// ErrNotComposerTarget refuses a conversation a dashboard command cannot
// run for: not an open hosted lead.
var ErrNotComposerTarget = errors.New("only an open hosted lead takes a dashboard command")

// ComposerOpen is the live authority check of the dashboard's private
// command routes: it returns the open generation of id when id is an open,
// not handed-off hosted lead, and refuses otherwise. It reads memory only.
func (r *Runner) ComposerOpen(id string) (uint64, error) {
	owner, err := r.ExecOwner(id)
	if err != nil {
		return 0, err
	}
	if !owner.Lead {
		return 0, ErrNotComposerTarget
	}
	c, err := r.open(id)
	if err != nil {
		return 0, err
	}
	c.mu.Lock()
	handedOff := c.rec.handedOff()
	c.mu.Unlock()
	if handedOff {
		return 0, ErrNotComposerTarget
	}
	return owner.Gen, nil
}

// ComposerTarget returns the open hosted lead id's target. A closed,
// handed-off, unknown or non-lead conversation is refused.
func (r *Runner) ComposerTarget(id string) (ComposerTarget, error) {
	owner, err := r.ExecOwner(id)
	if err != nil {
		return ComposerTarget{}, err
	}
	if !owner.Lead {
		return ComposerTarget{}, ErrNotComposerTarget
	}
	c, err := r.open(id)
	if err != nil {
		return ComposerTarget{}, err
	}
	c.mu.Lock()
	root, key, handedOff := c.rec.Root, c.rec.WSKey, c.rec.handedOff()
	c.mu.Unlock()
	if handedOff {
		return ComposerTarget{}, ErrNotComposerTarget
	}
	// The key's own root is the bound worktree, as the lead's ws tools see it.
	if roots, ok := r.opts.Keys.(interface{ Root(string) (string, bool) }); ok && key != "" {
		if bound, found := roots.Root(key); found && bound != "" {
			root = bound
		}
	}
	if root == "" {
		return ComposerTarget{}, errors.New("the lead has no bound worktree")
	}
	return ComposerTarget{Owner: owner, Root: root, Environ: r.composerEnviron(id)}, nil
}

// composerEnviron is the environment the lead's ws-mcp connection carries
// (its harness's, with the entry's overlay), the one exec.shell would run
// under; the daemon's own when the conversation has no token yet.
func (r *Runner) composerEnviron(id string) []string {
	r.mu.Lock()
	t := r.mcpTokens[r.mcpTokenOf[id]]
	r.mu.Unlock()
	if t == nil {
		return os.Environ()
	}
	select {
	case <-t.ready:
	default:
		return os.Environ()
	}
	env := make([]string, 0, len(t.hello.Env))
	for name, value := range t.hello.Env {
		env = append(env, name+"="+value)
	}
	sort.Strings(env)
	return env
}

// PassiveHand puts the receipt text of exec job execID into the pending
// input of open conversation id under generation gen (a later put for the
// same job replaces it), for the next accepted send to carry. It starts no
// turn and writes nothing. A conversation reopened since gen, or closed,
// refuses it.
func (r *Runner) PassiveHand(id string, gen uint64, execID, text string) error {
	c, err := r.open(id)
	if err != nil {
		return err
	}
	// The check and the put share one hold, so a close or revival cannot fall
	// between them (passive.mu nests under c.mu and calls nothing).
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return endedError(c.rec)
	}
	if c.rec.OpenGen != gen {
		return fmt.Errorf("the conversation reopened since this command was submitted")
	}
	c.passive.put(gen, execID, text)
	return nil
}
