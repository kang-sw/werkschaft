package wsfs

import (
	"os"
	"runtime"

	"golang.org/x/sys/windows"
)

// HasMultipleLinks reports whether the opened file has another directory entry.
func HasMultipleLinks(file *os.File, _ os.FileInfo) (bool, error) {
	var info windows.ByHandleFileInformation
	err := windows.GetFileInformationByHandle(windows.Handle(file.Fd()), &info)
	runtime.KeepAlive(file)
	return info.NumberOfLinks > 1, err
}
