//go:build windows

package execjob

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// resolvePath is p as the kernel names the directory (or file) it opens:
// symlinks and directory junctions (mount points) resolved, the one form
// the worktree fence compares a job's places and a worktree in. Since Go
// 1.23 filepath.EvalSymlinks keeps a junction as spelled, or fails below
// one, so a job could enter a worktree through a junction while every
// path it records lies outside. One open and close; p must exist.
func resolvePath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	// Access 0 with backup semantics opens a directory for its name alone.
	h, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(h)
	final, err := finalPathByHandle(h)
	if err != nil {
		return "", err
	}
	return filepath.Clean(stripExtendedPrefix(final)), nil
}

// finalPathByHandle is h's normalized DOS-volume path, which carries the
// extended-length prefix.
func finalPathByHandle(h windows.Handle) (string, error) {
	// FILE_NAME_NORMALIZED | VOLUME_NAME_DOS, both 0; x/sys names neither.
	const normalizedDOSName = 0x0
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFinalPathNameByHandle(h, &buf[0], uint32(len(buf)), normalizedDOSName)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n+1)
	}
}

// stripExtendedPrefix turns \\?\C:\x into C:\x and \\?\UNC\host\share\x
// into \\host\share\x, the spellings a recorded path carries.
func stripExtendedPrefix(p string) string {
	if rest, ok := strings.CutPrefix(p, `\\?\UNC\`); ok {
		return `\\` + rest
	}
	return strings.TrimPrefix(p, `\\?\`)
}
