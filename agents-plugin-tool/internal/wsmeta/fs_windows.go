package wsmeta

import (
	"errors"
	"io/fs"

	"golang.org/x/sys/windows"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// platformFileSystem names the file system of the volume that holds dir,
// following any reparse point to the directory itself, so a mounted folder
// reports the volume it mounts.
func platformFileSystem(dir string) (string, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	wsstate.CountFileIO("open")
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return "", fs.ErrNotExist
		}
		return "", err
	}
	defer windows.CloseHandle(h)
	name := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformationByHandle(h, nil, 0, nil, nil, nil, &name[0], uint32(len(name))); err != nil {
		return "", err
	}
	return windows.UTF16ToString(name), nil
}
