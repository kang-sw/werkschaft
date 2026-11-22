package wsawake

import (
	"io"
	"os"
)

// Platform returns this operating system's backend: the system caffeinate
// helper on macOS, the thread execution state on Windows, and a no-op that
// notes once in log that it holds nothing elsewhere. Constructing it makes
// no OS call; the first Acquire does.
func Platform(log io.Writer) Backend {
	if log == nil {
		log = io.Discard
	}
	return platformBackend(log, os.Getpid())
}
