package wsagent

import (
	"errors"
	"io/fs"
	"os"
	"time"
)

// recordMissBudget bounds how long a record read retries a not-found while
// its session directory exists: long enough to ride out a rename-over, short
// enough that a never-written record reads not-found promptly.
const recordMissBudget = 250 * time.Millisecond

// isRecordMiss reports whether a failed record read is worth retrying as a
// transient rename-over miss: a not-found error, inside the miss budget,
// while the session directory still exists. dirExists runs only when the
// cheaper conditions hold, so a deleted session costs no extra stat.
func isRecordMiss(err error, withinBudget bool, dirExists func() bool) bool {
	return errors.Is(err, fs.ErrNotExist) && withinBudget && dirExists()
}

// readRecordRetrying reads sessionDir's agent.json through readOnce, retrying
// on transient (sharing/access) errors for up to two seconds and on a
// record-miss (see isRecordMiss) within recordMissBudget. Platform-neutral so
// the retry policy is testable everywhere; only Windows wires it into
// readRecordFile, POSIX keeps a single read. afterFailedRead, when non-nil,
// runs after each failed attempt that will be retried, before the back-off
// sleep (a test seam).
func readRecordRetrying(sessionDir string, readOnce func(string) ([]byte, error), transient func(error) bool, afterFailedRead func()) ([]byte, error) {
	path := recordPath(sessionDir)
	deadline := time.Now().Add(2 * time.Second)
	missDeadline := time.Now().Add(recordMissBudget)
	backoff := 5 * time.Millisecond
	for {
		raw, err := readOnce(path)
		if err == nil {
			return raw, nil
		}
		now := time.Now()
		retry := false
		switch {
		case transient(err):
			retry = !now.After(deadline)
		case isRecordMiss(err, now.Before(missDeadline), func() bool {
			_, serr := os.Stat(sessionDir)
			return serr == nil
		}):
			retry = true
		}
		if !retry {
			return raw, err
		}
		if afterFailedRead != nil {
			afterFailedRead()
		}
		time.Sleep(backoff)
		if backoff < 200*time.Millisecond {
			backoff *= 2
		}
	}
}
