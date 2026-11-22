//go:build windows

package mcp

import (
	"os"
	"path/filepath"
	"syscall"
)

// holdGeneratedPathGrant is false on Windows: NTFS refuses a rename-replace
// (MoveFileEx MOVEFILE_REPLACE_EXISTING, used by Go os.Rename and Node
// fs.rename) over a file that any handle has open, FILE_SHARE_DELETE or not,
// and likewise a rename of a directory with an open handle beneath it. Holding
// the descriptor from path.generate to path.write would break a caller that
// saves the generated file by write-temp-then-rename, which worked when
// path.generate closed its reservation before returning. The grant records the
// file identity (volume serial and file index) instead, and path.write
// reopens the path and refuses a file whose identity differs.
const holdGeneratedPathGrant = false

// openGeneratedPathGrant opens the path.write descriptor with
// FILE_SHARE_DELETE, which os.OpenFile omits, so a concurrent delete-save does
// not hit a sharing violation during the brief open at grant and write time.
func openGeneratedPathGrant(path string) (*os.File, error) {
	namep, err := syscall.UTF16PtrFromString(extendedLengthPath(path))
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	handle, err := syscall.CreateFile(
		namep,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

// lstatGeneratedPath is the identity probe paired with
// openGeneratedPathGrant: it inspects the same extended-length path form the
// grant opens, so a path that needs the prefix compares like for like.
func lstatGeneratedPath(path string) (os.FileInfo, error) {
	return os.Lstat(extendedLengthPath(path))
}

// extendedLengthPath is path in the extended-length (\\?\ or \\?\UNC\) form
// when it is too long for the MAX_PATH-limited API, as os.OpenFile's
// internal fixLongPath produces; a raw CreateFile call does not get that
// normalization. A short path, an already-extended path, and a device path
// (\\.\) come back unchanged.
func extendedLengthPath(path string) string {
	if len(path) >= 4 && (path[:4] == `\??\` || (os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) && path[2] == '?' && os.IsPathSeparator(path[3]))) {
		return path
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return path
		}
		path = abs
	}
	// MAX_PATH (260) minus room for an 8.3 name, the bound os uses.
	if len(path) < 248 {
		return path
	}
	if len(path) >= 4 && os.IsPathSeparator(path[0]) && os.IsPathSeparator(path[1]) && path[2] == '.' && os.IsPathSeparator(path[3]) {
		return path
	}
	// GetFullPathName normalizes separators and . / .. elements, which the
	// extended-length form no longer resolves.
	p, err := syscall.UTF16FromString(path)
	if err != nil {
		return path
	}
	n := uint32(len(p))
	var buf []uint16
	for {
		buf = make([]uint16, n)
		n, err = syscall.GetFullPathName(&p[0], uint32(len(buf)), &buf[0], nil)
		if err != nil {
			return path
		}
		if n <= uint32(len(buf)) {
			buf = buf[:n]
			break
		}
	}
	full := syscall.UTF16ToString(buf)
	if len(full) >= 2 && full[0] == '\\' && full[1] == '\\' {
		return `\\?\UNC\` + full[2:]
	}
	return `\\?\` + full
}
