package wsmeta

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/kang-sw/devenv/internal/wsstate"
)

// dirGeneration identifies one incarnation of a session directory: volume
// serial, file index and creation time. On NTFS the file index carries the
// MFT record's sequence number, which changes when a record is reused, so a
// directory pruned and recreated under the same reply-id reads as another
// generation even when name tunneling preserves its creation time.
func dirGeneration(path string, info os.FileInfo) string {
	if p, err := windows.UTF16PtrFromString(path); err == nil {
		wsstate.CountFileIO("open")
		h, err := windows.CreateFile(p, windows.FILE_READ_ATTRIBUTES,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
			windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err == nil {
			var fi windows.ByHandleFileInformation
			err = windows.GetFileInformationByHandle(h, &fi)
			_ = windows.CloseHandle(h)
			if err == nil {
				return fmt.Sprintf("id:%d:%d:%d:ctime:%d:%d", fi.VolumeSerialNumber, fi.FileIndexHigh, fi.FileIndexLow,
					fi.CreationTime.HighDateTime, fi.CreationTime.LowDateTime)
			}
		}
	}
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok && d != nil {
		return fmt.Sprintf("ctime:%d:%d", d.CreationTime.HighDateTime, d.CreationTime.LowDateTime)
	}
	return "mtime:" + info.ModTime().UTC().Format("20060102T150405.000000000")
}
