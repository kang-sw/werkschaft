//go:build windows

package wsdashboard

import (
	"os"
	"syscall"
	"testing"
)

// TestComposerRemovedWindowsDeletePending: ERROR_DELETE_PENDING (303) counts as
// removed, the way a filesystem without POSIX delete reports a name an open
// handle still holds; access denied (5) and a sharing violation (32) do not.
func TestComposerRemovedWindowsDeletePending(t *testing.T) {
	wrap := func(e syscall.Errno) error { return &os.PathError{Op: "remove", Path: "x.out", Err: e} }
	if !composerRemoved(wrap(303)) {
		t.Error("a delete-pending file did not count as removed")
	}
	for _, e := range []syscall.Errno{5, 32} {
		if composerRemoved(wrap(e)) {
			t.Errorf("errno %d counted as removed", e)
		}
	}
}
