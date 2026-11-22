package wsmeta

import (
	"errors"
	"fmt"
	"io/fs"

	"golang.org/x/sys/windows"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// entryIdentity is the volume serial and file index of the entry at p (a
// reparse point is not followed). A rename within the volume keeps both, and
// on NTFS, the only file system the cutover accepts (barrier.go), the index
// carries the MFT record's sequence number, so a reused record reads as
// another file. The creation time is left out: NTFS tunneling can carry a
// deleted name's creation time onto a file renamed to that name, as happens
// when a re-fence retires an old witness and moves the new original there
// (re-review 80ffd114).
func entryIdentity(p string) (string, error) {
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return "", err
	}
	wsstate.CountFileIO("open")
	h, err := windows.CreateFile(u, windows.FILE_READ_ATTRIBUTES,
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
	return fmt.Sprintf("id:%d:%d:%d", fi.VolumeSerialNumber, fi.FileIndexHigh, fi.FileIndexLow), nil
}
