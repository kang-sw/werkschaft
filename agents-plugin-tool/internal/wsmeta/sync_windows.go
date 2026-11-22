package wsmeta

import (
	"os"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// platformSyncDir makes the earlier entry changes of a directory durable.
// Windows has no directory fsync. Every rename the migration makes is
// already write-through (wsstate.AtomicReplaceFile), which MoveFileEx
// documents as on disk when it returns, and a deletion is such a rename. So
// this barrier is relied on alone only where the change came from an
// interrupted attempt or another process: a re-applied removal whose rename
// an earlier attempt made, and an original already absent. There it rests
// on NTFS keeping one ordered metadata log per volume: the write-through
// rename below returns only after the log is flushed through its own
// record, which covers every earlier change in the directory. That ordering
// is file-system behavior, not the MoveFileEx contract, and is not verified
// for the volume (re-review e33c561c, pending the lead's decision). A crash
// can leave a dot-named barrier file behind, which no bundled-file
// discovery reads.
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
