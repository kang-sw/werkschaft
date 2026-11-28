//go:build windows

package wsproctree

import (
	"errors"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const supported = true

var procGetProcessMemoryInfo = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")

// processMemoryCounters is PROCESS_MEMORY_COUNTERS.
type processMemoryCounters struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
}

// snapshot lists every process through Toolhelp32, keeps roots' subtrees,
// and reads only those processes' times and working sets, in process.
func snapshot(roots []int) ([]Proc, error) {
	h, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(h)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(h, &entry); err != nil {
		return nil, err
	}
	var all []Proc
	for {
		all = append(all, Proc{PID: int(entry.ProcessID), PPID: int(entry.ParentProcessID), Name: windows.UTF16ToString(entry.ExeFile[:])})
		if err := windows.Process32Next(h, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				break
			}
			return nil, err
		}
	}
	kept := keep(all, roots)
	for i := range kept {
		kept[i].RSS, kept[i].CPU = usage(kept[i].PID)
	}
	return kept, nil
}

// usage is pid's working set and CPU time; zeros when it cannot be opened.
func usage(pid int) (uint64, time.Duration) {
	ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return 0, 0
	}
	defer windows.CloseHandle(ph)
	var cpu time.Duration
	var creation, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(ph, &creation, &exit, &kernel, &user) == nil {
		cpu = time.Duration(filetimeTicks(kernel)+filetimeTicks(user)) * 100
	}
	var mem processMemoryCounters
	mem.CB = uint32(unsafe.Sizeof(mem))
	var rss uint64
	if r, _, _ := procGetProcessMemoryInfo.Call(uintptr(ph), uintptr(unsafe.Pointer(&mem)), uintptr(mem.CB)); r != 0 {
		rss = uint64(mem.WorkingSetSize)
	}
	return rss, cpu
}

// filetimeTicks is a FILETIME duration in 100ns ticks.
func filetimeTicks(f windows.Filetime) int64 {
	return int64(f.HighDateTime)<<32 | int64(f.LowDateTime)
}
