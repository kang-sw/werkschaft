//go:build windows

package wsholder

import (
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWindowsDetachedCreationFlagsIncludeNormalPriority(t *testing.T) {
	base := uint32(windows.CREATE_NO_WINDOW | syscall.CREATE_NEW_PROCESS_GROUP | windows.NORMAL_PRIORITY_CLASS)
	if got := windowsDetachedCreationFlags(false); got != base {
		t.Fatalf("windowsDetachedCreationFlags(false) = %#x, want %#x", got, base)
	}
	wantBreakaway := base | windows.CREATE_BREAKAWAY_FROM_JOB
	if got := windowsDetachedCreationFlags(true); got != wantBreakaway {
		t.Fatalf("windowsDetachedCreationFlags(true) = %#x, want %#x", got, wantBreakaway)
	}
}
