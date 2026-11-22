package wsmeta

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/windows"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// dirGeneration identifies one incarnation of a session directory: volume
// serial, file index and creation time. On NTFS the file index carries the
// MFT record's sequence number, which changes when a record is reused. So a
// directory pruned and recreated under the same reply-id reads as another
// generation, even when name tunneling preserves its creation time.
//
// A failed handle lookup (a sharing violation, a scanner's open) is returned
// as inconclusive, never answered with a weaker creation-time token. That
// token would read as another incarnation and discard the session's journal
// rows.
func dirGeneration(path string, _ os.FileInfo) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	wsstate.CountFileIO("open")
	h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) || errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return "", fs.ErrNotExist
		}
		return "", err
	}
	defer windows.CloseHandle(h)
	var fi windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &fi); err != nil {
		return "", err
	}
	return fmt.Sprintf("id:%d:%d:%d:ctime:%d:%d", fi.VolumeSerialNumber, fi.FileIndexHigh, fi.FileIndexLow,
		fi.CreationTime.HighDateTime, fi.CreationTime.LowDateTime), nil
}
