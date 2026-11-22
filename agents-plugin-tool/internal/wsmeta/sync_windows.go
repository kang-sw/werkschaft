package wsmeta

import (
	"os"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// platformSyncDir makes the earlier entry changes of a directory durable.
// Windows has no directory fsync, so a write-through rename is the barrier.
// NTFS records every metadata change of a volume in one ordered log, and
// MoveFileEx(MOVEFILE_WRITE_THROUGH) returns only after the log has been
// flushed through its own record (wsstate.AtomicReplaceFile). That flush
// covers every earlier change, including the fence's rename or removal in
// this directory. A crash can leave a dot-named barrier file behind, which
// no bundled-file discovery reads.
func platformSyncDir(dir string) error {
	f, err := wsstate.CreateTemp(dir, ".wsmeta-barrier-*.tmp")
	if err != nil {
		return err
	}
	from := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(from)
		return err
	}
	to := filepath.Join(dir, filepath.Base(from)+".done")
	if err := wsstate.AtomicReplaceFile(from, to); err != nil {
		_ = os.Remove(from)
		return err
	}
	_ = os.Remove(to)
	return nil
}
