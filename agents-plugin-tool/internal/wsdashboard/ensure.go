package wsdashboard

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gofrs/flock"
)

// Outcome is what Ensure found or did.
type Outcome string

const (
	// OutcomeRunning: a healthy daemon was already running and was left
	// alone: newer than this ws-mcp, or as new and in the LAN mode it asks
	// for.
	OutcomeRunning Outcome = "running"
	// OutcomeStarted: no reachable daemon was running; a new one is up.
	OutcomeStarted Outcome = "started"
	// OutcomeReplaced: an older daemon, an explicitly selected equal-core
	// daemon, or one as new in the other LAN mode was replaced.
	OutcomeReplaced Outcome = "replaced"
	// OutcomeUnavailable: no healthy daemon came up in time. After an
	// accepted replacement drain, the predecessor may already have stopped.
	OutcomeUnavailable Outcome = "unavailable"
)

// DefaultStartWait bounds how long Ensure waits for a started daemon's
// endpoint file: the successor's lock wait plus start-up.
const DefaultStartWait = SuccessorLockWait + 15*time.Second

// maxSpawns bounds the daemons one Ensure starts: the first, and one more to
// replace an older daemon that a concurrent older ws-mcp started and that won
// the lock race.
const maxSpawns = 2

// EnsureOptions configures Ensure.
type EnsureOptions struct {
	// Dir is the state directory (Dir's result).
	Dir string
	// Version is this ws-mcp's version; a running daemon older than it is
	// replaced.
	Version string
	// BuildTime and ContentHash describe this candidate; neither authorizes
	// replacing a healthy equal-numeric-core daemon on ordinary connection.
	BuildTime   int64
	ContentHash string
	// Replace requests one explicit equal-core replacement, even for the
	// same content. It never permits downgrading a newer numeric core.
	Replace bool
	// BelowFloor explicitly admits starting a candidate whose minor is lower
	// than dir's daemon-minor-floor and lowers the floor to that candidate's
	// minor under the singleton lock. It never drains a healthy newer daemon.
	BelowFloor bool
	// SpawnBeforeShutdown waits for a gated successor's readiness before drain.
	SpawnBeforeShutdown bool
	// JoinStarterLock serializes starters whose caller does not hold launcher-start.lock.
	JoinStarterLock bool
	// PreserveRunningLAN uses each fresh healthy observation, not an earlier probe.
	PreserveRunningLAN bool
	// DesktopStart limits the starter to one handoff of the shell's observed
	// daemon. A nil Predecessor authorizes a missing-only start.
	DesktopStart bool
	Predecessor  *DaemonIdentity
	// PrepareSuccessor creates a process gated on the returned admission
	// callback, returning only after the child finishes pre-lock initialization.
	PrepareSuccessor func(args []string) (admit func(bool) error, err error)
	// Port is the configured dashboard.port, passed to a started daemon as
	// its --port flag.
	Port int
	// LAN is the LAN mode this ws-mcp asks for, passed to a started daemon as
	// its --lan flag; a running daemon as new in the other mode is replaced.
	LAN bool
	// Spawn starts a detached daemon process with the given ws-mcp
	// arguments ("dashboard", "--port", N). It returns once the process is
	// started; it does not wait for the daemon to serve.
	Spawn func(args []string) error
	// HTTP is the client for /health and shutdown; nil uses a client with
	// DefaultHealthTimeout and no proxy.
	HTTP *http.Client
	// StartWait bounds the wait for a started daemon; zero means
	// DefaultStartWait.
	StartWait time.Duration
	// PollInterval is the endpoint-file poll period; zero means 50ms.
	PollInterval time.Duration
	// Log receives diagnostics; nil discards them.
	Log io.Writer
}

// DaemonIdentity binds a desktop handoff to a health observation without
// putting the endpoint bearer into the starter's arguments or input.
type DaemonIdentity struct {
	Port      int    `json:"port"`
	PID       int    `json:"pid"`
	StartedAt string `json:"started_at"`
	Version   string `json:"version"`
	TokenHash string `json:"token_sha256"`
}

func (id *DaemonIdentity) matches(ep Endpoint, info HealthInfo) bool {
	if id == nil || ep.Token == "" {
		return false
	}
	sum := sha256.Sum256([]byte(ep.Token))
	return ep.Port == id.Port && ep.PID == id.PID && info.StartedAt == id.StartedAt &&
		(info.Port == 0 || info.Port == ep.Port) && info.Version == id.Version &&
		hex.EncodeToString(sum[:]) == id.TokenHash
}

// DaemonArgs is the ws-mcp argument list that starts a daemon on port, in
// LAN mode when lan is set, reporting buildTime when it is known (non-zero).
func DaemonArgs(port int, lan bool, buildTime int64) []string {
	args := []string{"dashboard", "--port", strconv.Itoa(port)}
	if lan {
		args = append(args, "--lan")
	}
	if buildTime > 0 {
		args = append(args, "--build-time", strconv.FormatInt(buildTime, 10))
	}
	return args
}

func ensureDaemonArgs(port int, lan bool, buildTime int64, intent StartIntent, belowFloor bool) []string {
	args := DaemonArgs(port, lan, buildTime)
	if intent != "" {
		args = append(args, "--start-intent", string(intent))
	}
	if belowFloor {
		args = append(args, "--below-floor")
	}
	return args
}

func replaceIntent(info HealthInfo, opts EnsureOptions) StartIntent {
	running, _, runningOK := parseVersion(info.Version)
	candidate, _, candidateOK := parseVersion(opts.Version)
	if runningOK && candidateOK && running == candidate {
		return StartIntentReplace
	}
	if CompareVersions(info.Version, opts.Version) < 0 {
		return StartIntentUpgrade
	}
	return StartIntentReplace
}

// Ensure makes sure a daemon at least as new as opts.Version runs:
//
//   - A healthy newer daemon is left alone whatever its LAN mode, so an older
//     ws-mcp never replaces a newer daemon; one as new is left alone when its
//     LAN mode is opts.LAN, unless opts.Replace explicitly requests a swap.
//     Equal numeric cores ignore suffix, build time and content hash.
//   - A healthy older daemon, an explicitly selected equal-core daemon,
//     or one as new in the other LAN mode, gets a prepared successor before
//     the token-authenticated shutdown request when SpawnBeforeShutdown is
//     enabled. Admission then lets the successor wait for the singleton lock.
//   - An unreachable daemon (no endpoint file, a refused or unanswered
//     /health, an answer from another process) is never treated as dead:
//     a new daemon is started and the lock decides. When the recorded daemon
//     is alive but hung, the new daemon cannot take the lock and exits, and
//     Ensure reports OutcomeUnavailable.
//
// When a concurrently started older daemon wins the lock, Ensure replaces it
// once more; it starts at most maxSpawns daemons.
func Ensure(ctx context.Context, opts EnsureOptions) (Outcome, error) {
	if opts.Spawn == nil {
		return OutcomeUnavailable, errors.New("dashboard: no spawner")
	}
	client := opts.HTTP
	if client == nil {
		client = &http.Client{Timeout: DefaultHealthTimeout, Transport: &http.Transport{Proxy: nil}}
	}
	if opts.DesktopStart {
		direct := *client
		direct.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		client = &direct
	}
	logw := opts.Log
	if logw == nil {
		logw = io.Discard
	}
	if opts.JoinStarterLock {
		lock := flock.New(filepath.Join(opts.Dir, "launcher-start.lock"))
		ok, err := lock.TryLockContext(ctx, 50*time.Millisecond)
		if err != nil {
			return OutcomeUnavailable, fmt.Errorf("dashboard: launcher start lock: %v", err)
		}
		if !ok {
			return OutcomeUnavailable, errors.New("dashboard: launcher start lock: not acquired")
		}
		defer lock.Unlock() // Never unlink: concurrent waiters may already hold the inode.
	}
	replaced := false
	spawns := 0
	for {
		intent := StartIntentAbsence
		previous, readErr := ReadEndpoint(opts.Dir)
		if readErr == nil {
			info, err := Health(ctx, client, previous)
			if opts.DesktopStart && err == nil && !opts.Predecessor.matches(previous, info) {
				return OutcomeRunning, nil
			}
			if opts.DesktopStart && err != nil && opts.Predecessor != nil {
				return OutcomeUnavailable, errors.New("dashboard: observed predecessor health became uncertain")
			}
			if err == nil && opts.PreserveRunningLAN {
				opts.LAN = info.LAN
			}
			switch {
			case err == nil && (keepRunning(info, opts) || spawns >= maxSpawns):
				switch {
				case spawns == 0:
					return OutcomeRunning, nil
				case replaced:
					return OutcomeReplaced, nil
				}
				return OutcomeStarted, nil
			case err == nil:
				intent = replaceIntent(info, opts)
				if _, _, err := checkMinorFloorForIntent(opts.Dir, opts.Version, intent, opts.BelowFloor); err != nil {
					return OutcomeUnavailable, err
				}
				if opts.SpawnBeforeShutdown {
					if err := replaceWithPreparedSuccessor(ctx, client, opts, previous, info, intent, logw); err != nil {
						return OutcomeUnavailable, err
					}
					if !waitForSuccessor(ctx, client, opts, previous.Token, true, logw) {
						return OutcomeUnavailable, nil
					}
					return OutcomeReplaced, nil
				}
				if err := RequestShutdown(ctx, client, previous); err != nil {
					fmt.Fprintf(logw, "dashboard: older daemon %s refused shutdown: %v\n", info.Version, err)
					return OutcomeRunning, nil
				}
				fmt.Fprintf(logw, "dashboard: replacing daemon %s (lan %v, build %d) with %s (lan %v, build %d)\n", info.Version, info.LAN, info.BuildTime, opts.Version, opts.LAN, opts.BuildTime)
				replaced = true
			default:
				fmt.Fprintf(logw, "dashboard: recorded daemon unreachable (%v); starting one\n", err)
			}
		}
		if opts.DesktopStart && readErr != nil && opts.Predecessor != nil {
			return OutcomeUnavailable, errors.New("dashboard: observed predecessor endpoint became unavailable")
		}
		if spawns >= maxSpawns {
			return OutcomeUnavailable, nil
		}
		if intent == StartIntentAbsence {
			if _, _, err := checkMinorFloorForIntent(opts.Dir, opts.Version, intent, opts.BelowFloor); err != nil {
				return OutcomeUnavailable, err
			}
		}
		spawns++
		// An explicit request applies only to the initial daemon, not a
		// successor (including when the initial endpoint was absent).
		opts.Replace = false
		if err := opts.Spawn(ensureDaemonArgs(opts.Port, opts.LAN, opts.BuildTime, intent, opts.BelowFloor)); err != nil {
			return OutcomeUnavailable, fmt.Errorf("dashboard: start daemon: %w", err)
		}
		if !waitForSuccessor(ctx, client, opts, previous.Token, false, logw) {
			return OutcomeUnavailable, nil
		}
		if opts.DesktopStart {
			return OutcomeStarted, nil
		}
	}
}

func replaceWithPreparedSuccessor(ctx context.Context, client *http.Client, opts EnsureOptions, previous Endpoint, previousHealth HealthInfo, intent StartIntent, logw io.Writer) error {
	if opts.PrepareSuccessor == nil {
		return errors.New("dashboard: no successor preflight")
	}
	port := opts.Port
	if opts.DesktopStart {
		// Retain the dashboard origin and its webview-local drafts.
		port = previous.Port
	}
	admit, err := opts.PrepareSuccessor(ensureDaemonArgs(port, opts.LAN, opts.BuildTime, intent, opts.BelowFloor))
	if err != nil {
		return fmt.Errorf("dashboard: successor preflight: %w", err)
	}
	allowed := false
	defer func() {
		if !allowed {
			_ = admit(false)
		}
	}()
	// Process creation can block under antivirus. Recheck the endpoint and
	// REST identity immediately before draining the predecessor.
	current, err := ReadEndpoint(opts.Dir)
	if err != nil {
		return errors.New("dashboard: predecessor endpoint became unreadable")
	}
	health, err := Health(ctx, client, current)
	if err != nil || current != previous || health.StartedAt != previousHealth.StartedAt ||
		health.Version != previousHealth.Version || (opts.DesktopStart && !opts.Predecessor.matches(current, health)) {
		return errors.New("dashboard: predecessor changed before drain")
	}
	if rechecked, err := ReadEndpoint(opts.Dir); err != nil || rechecked != current {
		return errors.New("dashboard: predecessor endpoint changed before drain")
	}
	fmt.Fprintf(logw, "dashboard: successor %s ready; requesting shutdown of daemon %s\n", opts.Version, health.Version)
	if err := RequestShutdown(ctx, client, current); err != nil {
		return fmt.Errorf("dashboard: authenticated drain uncertain: %w", err)
	}
	allowed = true
	if err := admit(true); err != nil {
		return fmt.Errorf("dashboard: successor release after drain: %w", err)
	}
	return nil
}

// keepRunning reports whether the healthy daemon info describes may stay:
// it is newer than this ws-mcp, or as new and in the LAN mode opts asks for.
// Equal numeric cores stay regardless of suffix, build time or hash, unless
// an explicit replacement or the existing LAN-mode change requests a drain.
func keepRunning(info HealthInfo, opts EnsureOptions) bool {
	running, _, runningOK := parseVersion(info.Version)
	candidate, _, candidateOK := parseVersion(opts.Version)
	if runningOK && candidateOK && running == candidate {
		return info.LAN == opts.LAN && !opts.Replace
	}
	return CompareVersions(info.Version, opts.Version) >= 0
}

// waitForSuccessor polls the endpoint file until it names a healthy daemon
// other than the one carrying previousToken, or the start wait ends. A wait
// that ends unmet logs its last endpoint-read and health errors: a daemon can
// serve while its endpoint file stays unreadable to others (seen under heavy
// antivirus on Windows), and only these lines tell that apart from no daemon.
func waitForSuccessor(ctx context.Context, client *http.Client, opts EnsureOptions, previousToken string, prepared bool, logw io.Writer) bool {
	wait := opts.StartWait
	if wait <= 0 {
		wait = DefaultStartWait
	}
	poll := opts.PollInterval
	if poll <= 0 {
		poll = 50 * time.Millisecond
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var readErr error
	healthErr := errors.New("not attempted")
	unmet := func(reason string) bool {
		fmt.Fprintf(logw, "dashboard: successor wait ended (%s); last endpoint read: %v\n", reason, readErr)
		fmt.Fprintf(logw, "dashboard: successor wait ended (%s); last health check: %v\n", reason, healthErr)
		return false
	}
	for {
		ep, err := ReadEndpoint(opts.Dir)
		switch {
		case err != nil:
			readErr = err
		case ep.Token == "" || ep.Token == previousToken:
			readErr = errors.New("endpoint file names no successor yet")
		default:
			readErr = nil
			var info HealthInfo
			info, healthErr = Health(ctx, client, ep)
			if healthErr == nil {
				if !prepared || (info.Version == opts.Version && info.LAN == opts.LAN &&
					(opts.ContentHash == "" || info.ContentHash == opts.ContentHash)) {
					return true
				}
				healthErr = errors.New("healthy endpoint does not match the prepared successor")
			}
		}
		select {
		case <-ctx.Done():
			return unmet(ctx.Err().Error())
		case <-deadline.C:
			return unmet("timeout " + wait.String())
		case <-ticker.C:
		}
	}
}
