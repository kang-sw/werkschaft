package wsagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmeta"
)

var _ wsdashboard.AgentHistoryPurger = (*Runner)(nil)

// loadRetiredPiPurges runs once at startup if the runner has Pi history.
// Reuse the daemon's held metadata connection where it already has one.
func (r *Runner) loadRetiredPiPurges() error {
	if r.piPurges != nil || r.piPurgeLoadErr != nil {
		return r.piPurgeLoadErr
	}
	if r.piPurgeStore == nil {
		r.piPurgeStore = r.durableRecords
	}
	if r.piPurgeStore == nil {
		r.piPurgeStore, r.piPurgeLoadErr = wsmeta.Open(r.opts.Dir, wsmeta.Options{})
		if r.piPurgeLoadErr != nil {
			return r.piPurgeLoadErr
		}
	}
	r.piPurges, r.piPurgeLoadErr = r.piPurgeStore.RetiredPiPurges(context.Background())
	return r.piPurgeLoadErr
}

// PrepareWorktreeHistoryPurge persists closures that need the released
// checkout's marker and removes retired Pi archives before checkout deletion.
// Failed archive deletion remains pending
// even if an older daemon prunes the associated registry row before retry.
func (r *Runner) PrepareWorktreeHistoryPurge(worktree string, ended map[string]bool) error {
	if err := r.persistRetainedReleaseClosures(worktree, ended); err != nil {
		return err
	}
	r.piPurgeMu.Lock()
	defer r.piPurgeMu.Unlock()
	if r.piPurgeLoadErr != nil {
		r.piPurgeLoadErr = nil
		if err := r.loadRetiredPiPurges(); err != nil {
			return err
		}
	}
	for id := range ended {
		if !r.piArchiveIDs[id] {
			continue
		}
		if _, pending := r.piPurges[id]; pending {
			continue
		}
		if err := r.piPurgeStore.BeginRetiredPiPurge(context.Background(), id, worktree); err != nil {
			return fmt.Errorf("session %s: commit retired Pi deletion: %w", id, err)
		}
		r.piPurges[id] = worktree
	}
	remove := r.opts.removeRetiredPiArchive
	if remove == nil {
		remove = os.RemoveAll
	}
	for id, bound := range r.piPurges {
		if !sameWorktreePath(worktree, bound) {
			continue
		}
		archive := filepath.Join(r.opts.Dir, retiredPiHistoryDir, id)
		if err := errors.Join(remove(archive+stagedPiHistorySuffix), remove(archive+previousPiHistorySuffix), remove(archive)); err != nil {
			return fmt.Errorf("session %s: remove retired Pi archive: %w", id, err)
		}
	}
	return nil
}

// CompleteWorktreeHistoryPurge clears deletion state after the dashboard
// removes the checkout and its registry directories.
func (r *Runner) CompleteWorktreeHistoryPurge(worktree string) {
	r.piPurgeMu.Lock()
	defer r.piPurgeMu.Unlock()
	for id, bound := range r.piPurges {
		if sameWorktreePath(worktree, bound) {
			r.finishRetiredPiPurge(id)
		}
	}
}

func (r *Runner) finishRetiredPiPurge(id string) {
	if _, err := os.Lstat(wsdashboard.SessionDir(r.opts.Dir, id)); !errors.Is(err, os.ErrNotExist) {
		return
	}
	if err := r.piPurgeStore.FinishRetiredPiPurge(context.Background(), id); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: finish retired Pi deletion: %v\n", id, err)
		return
	}
	delete(r.piPurges, id)
	delete(r.piArchiveIDs, id)
}
