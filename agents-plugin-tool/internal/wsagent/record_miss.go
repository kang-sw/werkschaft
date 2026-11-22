package wsagent

import (
	"errors"
	"io/fs"
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
