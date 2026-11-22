//go:build !darwin && !windows

package wsawake

import (
	"fmt"
	"io"
	"runtime"
	"sync"
)

func platformBackend(log io.Writer, _ int) Backend {
	return &unsupported{log: log}
}

// unsupported holds nothing: idle-sleep prevention is implemented on macOS
// and Windows only. Its first Acquire notes that once in the log.
type unsupported struct {
	log  io.Writer
	once sync.Once
}

func (b *unsupported) Acquire() error {
	b.once.Do(func() {
		fmt.Fprintf(b.log, "keep-awake: agent.keep_awake_during_turns has no effect on %s (macOS and Windows only)\n", runtime.GOOS)
	})
	return nil
}

func (b *unsupported) Release() error { return nil }
