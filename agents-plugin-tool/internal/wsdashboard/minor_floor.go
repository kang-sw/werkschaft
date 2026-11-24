package wsdashboard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// MinorFloorFile is the plain-text file beside dashboard.lock and last-port
// holding the highest daemon minor that has started in this state directory
// (storage type 5 in performance.md, read raw by the Python launchers).
const MinorFloorFile = "daemon-minor-floor"

// ErrBelowMinorFloor is wrapped by MinorFloorError when a candidate daemon's
// minor is lower than the state directory's recorded floor.
var ErrBelowMinorFloor = errors.New("daemon candidate is below the state directory minor floor")

// StartIntent records why Ensure started or replaced a daemon so StartDaemon
// applies the same minor-floor admission rule under the singleton lock.
type StartIntent string

const (
	// StartIntentAbsence starts a daemon when no healthy daemon answered.
	StartIntentAbsence StartIntent = "absence"
	// StartIntentUpgrade replaces a healthy daemon running an older version.
	StartIntentUpgrade StartIntent = "upgrade"
	// StartIntentReplace explicitly replaces a healthy equal-core daemon
	// (--replace or a LAN-mode change).
	StartIntentReplace StartIntent = "replace"
)

// ParseStartIntent validates a CLI --start-intent value. Empty defaults to
// StartIntentAbsence (for direct manual `ws-mcp dashboard` runs).
func ParseStartIntent(raw string) (StartIntent, error) {
	switch StartIntent(strings.TrimSpace(raw)) {
	case "", StartIntentAbsence:
		return StartIntentAbsence, nil
	case StartIntentUpgrade:
		return StartIntentUpgrade, nil
	case StartIntentReplace:
		return StartIntentReplace, nil
	default:
		return "", fmt.Errorf("invalid start intent %q (want absence, upgrade, or replace)", raw)
	}
}

// MinorVersion is a numeric major.minor pair stored in daemon-minor-floor.
type MinorVersion struct {
	Major int
	Minor int
}

// String formats m as "major.minor".
func (m MinorVersion) String() string {
	return fmt.Sprintf("%d.%d", m.Major, m.Minor)
}

// Compare orders m against other, returning -1, 0, or 1.
func (m MinorVersion) Compare(other MinorVersion) int {
	if m.Major != other.Major {
		return sign(m.Major - other.Major)
	}
	return sign(m.Minor - other.Minor)
}

// MinorFloorError reports a candidate refused by daemon-minor-floor. Its
// message follows the existing "update this plugin" error shape.
type MinorFloorError struct {
	Floor     MinorVersion
	Candidate string
	Intent    StartIntent
}

func (e *MinorFloorError) Error() string {
	return fmt.Sprintf(
		"the shared daemon state directory requires minor %s or newer, a newer minor than this plugin (%s); update this plugin and restart this session (or run daemon-start --below-floor to roll back)",
		e.Floor, e.Candidate,
	)
}

func (e *MinorFloorError) Unwrap() error { return ErrBelowMinorFloor }

// ParseMinorFloor parses a daemon-minor-floor file's content: one numeric
// major.minor pair with optional surrounding whitespace.
func ParseMinorFloor(raw string) (MinorVersion, error) {
	trimmed := strings.TrimSpace(raw)
	parts := strings.Split(trimmed, ".")
	if len(parts) != 2 {
		return MinorVersion{}, fmt.Errorf("malformed %s: %q", MinorFloorFile, trimmed)
	}
	major, ok1 := parseUnsignedDecimal(parts[0])
	minor, ok2 := parseUnsignedDecimal(parts[1])
	if !ok1 || !ok2 {
		return MinorVersion{}, fmt.Errorf("malformed %s: %q", MinorFloorFile, trimmed)
	}
	return MinorVersion{Major: major, Minor: minor}, nil
}

func parseUnsignedDecimal(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

func parseMinorFromVersion(v string) (MinorVersion, bool) {
	core, _, ok := parseVersion(v)
	if !ok {
		return MinorVersion{}, false
	}
	return MinorVersion{Major: core[0], Minor: core[1]}, true
}

// ReadMinorFloor reads dir/daemon-minor-floor. An absent file returns
// (zero, false, nil) as legacy state. An unreadable or malformed existing file
// returns an error and never admits.
func ReadMinorFloor(dir string) (MinorVersion, bool, error) {
	raw, err := os.ReadFile(filepath.Join(dir, MinorFloorFile))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return MinorVersion{}, false, nil
		}
		return MinorVersion{}, true, fmt.Errorf("read %s: %w", MinorFloorFile, err)
	}
	floor, err := ParseMinorFloor(string(raw))
	if err != nil {
		return MinorVersion{}, true, err
	}
	return floor, true, nil
}

// CheckMinorFloor checks whether candidateVersion is admitted by dir's minor
// floor for the given start intent. Both Ensure (before any drain or spawn)
// and StartDaemon (under the singleton lock, before side effects) call this
// rule so their admission decisions agree for every intent.
func CheckMinorFloor(dir, candidateVersion string, intent StartIntent, belowFloor bool) error {
	_, _, err := checkMinorFloorForIntent(dir, candidateVersion, intent, belowFloor)
	return err
}

func checkMinorFloorForIntent(dir, candidateVersion string, intent StartIntent, belowFloor bool) (MinorVersion, bool, error) {
	parsedIntent, err := ParseStartIntent(string(intent))
	if err != nil {
		return MinorVersion{}, false, err
	}
	floor, exists, err := ReadMinorFloor(dir)
	if err != nil {
		return MinorVersion{}, exists, err
	}
	if !exists {
		return MinorVersion{}, false, nil
	}
	candidate, ok := parseMinorFromVersion(candidateVersion)
	if !ok {
		return floor, true, fmt.Errorf("cannot verify candidate version %q against minor floor %s", candidateVersion, floor)
	}
	if candidate.Compare(floor) < 0 && !belowFloor {
		return floor, true, &MinorFloorError{
			Floor:     floor,
			Candidate: candidateVersion,
			Intent:    parsedIntent,
		}
	}
	return floor, true, nil
}

// updateMinorFloor updates dir/daemon-minor-floor under the singleton lock,
// after startup initialization and before endpoint publication:
//   - when belowFloor is set and the candidate is below an existing floor, it
//     lowers the floor to the candidate's minor before the advance check;
//   - when the floor is absent or lower than the candidate's minor, it writes
//     the candidate's minor atomically;
//   - when the floor already equals the candidate's minor, it performs no write.
func updateMinorFloor(dir, candidateVersion string, floor MinorVersion, hasFloor bool, belowFloor bool) error {
	candidate, ok := parseMinorFromVersion(candidateVersion)
	if !ok {
		return nil
	}
	if hasFloor {
		cmp := candidate.Compare(floor)
		if cmp == 0 {
			return nil
		}
		if cmp < 0 && !belowFloor {
			// Guarded by checkMinorFloorForIntent on lock entry; refuse defensively.
			return &MinorFloorError{Floor: floor, Candidate: candidateVersion}
		}
	}
	return writePrivateFile(dir, MinorFloorFile, []byte(candidate.String()+"\n"))
}
