package wsmeta

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// platformEntryName returns the long name of the entry name spells in dir.
// NTFS gives an entry an 8.3 short name next to its long one, and either
// spelling opens it, so a path component may name an entry under a name
// that compares unequal to its long name. A missing entry returns name
// unchanged; the caller's own lookup decides absence.
func platformEntryName(dir, name string) (string, error) {
	if strings.ContainsAny(name, "*?") {
		return name, nil
	}
	p, err := windows.UTF16PtrFromString(filepath.Join(dir, name))
	if err != nil {
		return "", err
	}
	wsstate.CountFileIO("stat")
	var fd windows.Win32finddata
	h, err := windows.FindFirstFile(p, &fd)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return name, nil
		}
		return "", err
	}
	_ = windows.FindClose(h)
	return windows.UTF16ToString(fd.FileName[:]), nil
}
