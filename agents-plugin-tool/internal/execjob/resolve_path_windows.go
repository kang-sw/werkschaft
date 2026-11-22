//go:build windows

package execjob

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

// resolvePath is p as the kernel names the directory (or file) it opens:
// symlinks and directory junctions (mount points) resolved, the one form
// the worktree fence compares a job's places and a worktree in. Since Go
// 1.23 filepath.EvalSymlinks keeps a junction as spelled, or fails below
// one, so a job could enter a worktree through a junction while every
// path it records lies outside. One open and close; p must exist. The
// open takes p as win32Path gives it: a deep worktree or working directory
// in the extended-length form, so it resolves on a host whose Win32 calls
// stay MAX_PATH-limited, and any other as given. The result comes back in
// the ordinary spelling, or is an error where that spelling would name
// another directory (ordinarySpelling); rootForms then keeps only the
// candidate's recorded spelling, and admission refuses the job.
func resolvePath(p string) (string, error) {
	name, err := windows.UTF16PtrFromString(win32Path(p))
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
	return ordinarySpelling(final)
}

// ordinarySpelling is the final path final without its extended-length
// prefix, provided that spelling names the same directory: the ordinary
// spelling is what spawn hands CreateProcess, which applies the Win32
// rules (a trailing dot or space dropped, . and .. resolved) the prefixed
// form skips. A directory only extended syntax reaches, such as a literal
// `dual.` beside `dual` opened through \\?\ or a junction, has no such
// spelling: the process would start in `dual` while the fence tracks
// `dual.`, so it is errNoOrdinarySpelling and admission refuses the job.
// GetFullPathName is a string operation, so the check opens nothing.
func ordinarySpelling(final string) (string, error) {
	stripped := filepath.Clean(stripExtendedPrefix(final))
	full, err := fullPathName(stripped)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", errNoOrdinarySpelling, final, err)
	}
	if filepath.Clean(full) != stripped {
		return "", fmt.Errorf("%w: %q reads as %q", errNoOrdinarySpelling, final, full)
	}
	return stripped, nil
}

// errNoOrdinarySpelling marks a resolved directory whose ordinary Win32
// spelling names another directory, or none.
var errNoOrdinarySpelling = errors.New("the resolved directory has no ordinary Win32 spelling")

// win32Path is p as a raw CreateFile must take it to open what an
// ordinary Win32 open of p opens. Go's os package lengthens a long path
// itself (fixLongPath); a raw call does not. internal/mcp's
// extendedLengthPath does the same for its raw CreateFile, and this
// mirrors it (execjob cannot import mcp, which imports execjob):
//   - an extended (\\?\, \??\) or device (\\.\) path, in either separator,
//     comes back unchanged;
//   - otherwise p is normalized by the native GetFullPathName, which
//     applies the Win32 rules the prefixed form skips (a trailing dot or
//     space dropped, separators and . / .. resolved), so `checkout.` names
//     `checkout` as an unprefixed open and CreateProcess would;
//   - only a normalized path of 248 characters or more (MAX_PATH less room
//     for an 8.3 name, os's bound) gains \\?\ or \\?\UNC\; a shorter p comes
//     back as given, opened exactly as an ordinary call opens it.
func win32Path(p string) string {
	if extendedOrDevicePath(p) {
		return p
	}
	full, err := fullPathName(p)
	if err != nil || len(full) < 248 || extendedOrDevicePath(full) {
		return p
	}
	if len(full) >= 2 && full[0] == '\\' && full[1] == '\\' {
		return `\\?\UNC\` + full[2:]
	}
	return `\\?\` + full
}

// extendedOrDevicePath reports whether p starts with \\?\, \\.\ (either
// separator in each place) or \??\.
func extendedOrDevicePath(p string) bool {
	if len(p) < 4 {
		return false
	}
	if p[:4] == `\??\` {
		return true
	}
	sep := func(c byte) bool { return c == '\\' || c == '/' }
	return sep(p[0]) && sep(p[1]) && (p[2] == '?' || p[2] == '.') && sep(p[3])
}

// fullPathName is windows.GetFullPathName of p.
func fullPathName(p string) (string, error) {
	name, err := windows.UTF16FromString(p)
	if err != nil {
		return "", err
	}
	buf := make([]uint16, windows.MAX_PATH)
	for {
		n, err := windows.GetFullPathName(&name[0], uint32(len(buf)), &buf[0], nil)
		if err != nil {
			return "", err
		}
		if int(n) < len(buf) {
			return windows.UTF16ToString(buf[:n]), nil
		}
		buf = make([]uint16, n)
	}
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
