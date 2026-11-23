package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/kang-sw/devenv/internal/wsstate"
)

const dashboardLogCap = 8 << 20

// dashboardLog owns a bounded append log only while the daemon holds its
// singleton lock. The mutex serializes writes with opening, rotation and close.
type dashboardLog struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	size     int64
	fallback io.Writer
}

func openDashboardLog(dir string) *dashboardLog {
	return &dashboardLog{path: filepath.Join(dir, "dashboard.log"), fallback: os.Stderr}
}

func (l *dashboardLog) begin() func() {
	l.mu.Lock()
	defer l.mu.Unlock()
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err == nil {
		var info os.FileInfo
		info, err = f.Stat()
		if err == nil {
			l.size = info.Size()
			l.file = f
		} else {
			_ = f.Close()
		}
	}
	if err != nil {
		fmt.Fprintf(l.fallback, "dashboard: open log: %v\n", err)
	}
	return l.end
}

func (l *dashboardLog) end() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		if err := l.file.Close(); err != nil {
			fmt.Fprintf(l.fallback, "dashboard: close log: %v\n", err)
		}
		l.file = nil
	}
}

// rotate closes the Windows handle before replacing the retained generation.
func (l *dashboardLog) rotate() error {
	err := l.file.Close()
	l.file = nil
	if err != nil {
		return err
	}
	if err := wsstate.AtomicReplaceFile(l.path, l.path+".1"); err != nil {
		return err
	}
	l.file, err = os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	l.size = 0
	return err
}

func (l *dashboardLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	total := 0
	for len(p) > 0 {
		if l.file == nil {
			n, err := l.fallback.Write(p)
			return total + n, err
		}
		if l.size >= dashboardLogCap {
			if err := l.rotate(); err != nil {
				fmt.Fprintf(l.fallback, "dashboard: rotate log: %v\n", err)
				continue
			}
		}
		count := min(len(p), dashboardLogCap-int(l.size))
		n, err := l.file.Write(p[:count])
		l.size += int64(n)
		total += n
		p = p[n:]
		if err == nil && n != count {
			err = io.ErrShortWrite
		}
		if err != nil {
			fmt.Fprintf(l.fallback, "dashboard: write log: %v\n", err)
			_ = l.file.Close()
			l.file = nil
		}
	}
	return total, nil
}
