package wsholder

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"sync"
	"time"
)

// Sink receives every event of every attached holder, in order per holder,
// on that holder's reader goroutine. Records carry their sequence numbers:
// after a daemon restart the whole ring is replayed, so a consumer drops
// the records it already holds.
type Sink interface {
	HolderEvent(id string, ev Event)
}

// Options configures a Manager.
type Options struct {
	// Dir is the holders directory (DirName under the dashboard state
	// directory); it is created when absent.
	Dir string
	// Python is the interpreter that runs a holder (PythonEnv's value);
	// empty means python3 on PATH.
	Python string
	// Log receives diagnostics; nil discards them.
	Log io.Writer
	// RescanInterval is Run's period; zero means DefaultRescanInterval.
	RescanInterval time.Duration
	// Sink receives the attached holders' events; nil drops them. AddSink
	// adds another.
	Sink Sink
	// RingBytes is the size of the output ring a new holder keeps; zero is
	// the holder's own default.
	RingBytes int
	// OnExit persists a stopped holder's exit before its directory is removed.
	// An error retains the directory for a later scan to retry.
	OnExit func(Descriptor, Exit) error
	// AuditChannel offers every attach the holder audit channel
	// (AuditVersion): a holder that speaks it then tees its ring-skipped
	// lines to the sinks as EventAudit and EventAuditFence events. A holder
	// that predates the channel ignores the offer and attaches as before.
	AuditChannel bool

	// holderArgs are extra holder arguments, so a test shortens its
	// periods. startTimeout bounds the wait for a started holder's
	// descriptor; zero means defaultStartTimeout. The rest replace OS
	// calls: lookPath (exec.LookPath), start (startDetached), breakaway
	// (breakawayApplies), procStart (processStartTime), procTicks
	// (processStartTicks), alive (processAlive), and kill (killHolder).
	holderArgs   []string
	startTimeout time.Duration
	lookPath     func(string) (string, error)
	start        func(exe string, args []string, dir string, breakaway bool) (*exec.Cmd, error)
	breakaway    *bool
	procStart    func(pid int) (time.Time, error)
	procTicks    func(pid int) (uint64, error)
	alive        func(pid int) bool
	kill         func(pid int) error
}

const (
	defaultStartTimeout = 20 * time.Second
	// startTimeTolerance is how far a holder's recorded start may sit from
	// the OS creation time of its PID for a guarded kill to proceed.
	startTimeTolerance = 2 * time.Second
	// stopWait bounds the wait for a holder to go after a stop or a kill.
	stopWait = 20 * time.Second
	// maxFailures and failureAge bound the start failures a listing keeps.
	maxFailures = 5
	failureAge  = time.Hour
)

var (
	// ErrUnknownHolder is Stop's answer for an id no holder has.
	ErrUnknownHolder = errors.New("unknown holder")
	// ErrUnknownHolderIncomplete is ErrUnknownHolder whose answer came from
	// an incomplete census (Listing.Incomplete): the last scan could not
	// read every registration, so the holder may run unindexed. It is
	// decided under the same lock as the index lookup, so a scan that
	// indexes the holder right after cannot turn it into proof of absence.
	// errors.Is(err, ErrUnknownHolder) also holds for it.
	ErrUnknownHolderIncomplete = fmt.Errorf("%w, and the holder census is incomplete: it may still run", ErrUnknownHolder)
	// ErrStartTimeMismatch refuses a guarded kill: the process under the
	// holder's PID is not the one that wrote the descriptor.
	ErrStartTimeMismatch = errors.New("the process under the holder's PID does not match its recorded start time; not killed")
	// ErrStopping refuses a launch once StopAll has begun.
	ErrStopping = errors.New("every session holder is being stopped; no new one starts")
)

// Info is one holder as the daemon sees it.
type Info struct {
	ID  string `json:"id"`
	PID int    `json:"pid,omitempty"`
	// Attached reports that this daemon holds the holder's attach slot;
	// Cause says why not otherwise.
	Attached bool  `json:"attached"`
	Cause    Cause `json:"cause,omitempty"`
	// Protocol is the version the holder reports.
	Protocol int `json:"protocol"`
	// StartedAt is the holder's start in epoch milliseconds.
	StartedAt int64 `json:"started_at"`
	// RestartSafe is false for a holder that may end with the daemon's
	// host (Descriptor.RestartSafe).
	RestartSafe bool            `json:"restart_safe"`
	Session     json.RawMessage `json:"session,omitempty"`
	// Child is the child's last known status, for an attached holder.
	Child *ChildStatus `json:"child,omitempty"`
	// Lost counts the records a replay reported gone from the ring.
	Lost uint64 `json:"lost,omitempty"`
}

// Exit is the optional exit.json written by a holder before it ends.
type Exit struct {
	ExitCode  *int   `json:"exit_code"`
	Signal    *int   `json:"signal"`
	Reason    string `json:"reason"`
	StoppedAt string `json:"stopped_at"`
}

// StartFailure is a holder that could not be started.
type StartFailure struct {
	// At is the failure's time in epoch milliseconds.
	At      int64           `json:"at"`
	Message string          `json:"message"`
	Session json.RawMessage `json:"session,omitempty"`
}

// Listing is every holder the manager knows and its recent start failures.
type Listing struct {
	Holders  []Info         `json:"holders"`
	Failures []StartFailure `json:"failures"`
	// Incomplete: the last scan could not read the whole holders directory
	// (the directory itself, or a holder's descriptor), so a running holder
	// may be missing from Holders.
	Incomplete bool `json:"incomplete,omitempty"`
}

type entry struct {
	desc   Descriptor
	client *Client
	cause  Cause
	// lastSeq is the last record sequence seen from this holder, kept in
	// memory only: a reattach in this process continues after it, and a
	// new daemon process replays the whole ring.
	lastSeq uint64
	lost    uint64
	child   ChildStatus
	// audit is the attached connection's EventAuditAttached when it
	// negotiated the audit channel (AuditAttach).
	audit Event
}

// Manager finds, starts, attaches to, and stops session holders for one
// holders directory. The daemon never ends a live holder on its own: only
// Stop does.
type Manager struct {
	opts Options
	// opMu serializes scans, launches, and stops, which attach and detach.
	opMu sync.Mutex
	mu   sync.Mutex
	// entries are the known holders by id.
	entries map[string]*entry
	// sinks receive the attached holders' events (Options.Sink, AddSink).
	sinks    []Sink
	failures []StartFailure
	// incomplete: the last scan could not read every holder's registration
	// (Listing.Incomplete).
	incomplete bool
	closed     bool
	// stopping refuses launches from the start of a StopAll; it stays set
	// after a complete one, whose caller closes the manager.
	stopping bool
	stop     chan struct{}
	stopOnce sync.Once
}

// New returns a manager for opts.Dir. It touches nothing until Scan, Run,
// or Launch.
func New(opts Options) *Manager {
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.RescanInterval <= 0 {
		opts.RescanInterval = DefaultRescanInterval
	}
	if opts.startTimeout <= 0 {
		opts.startTimeout = defaultStartTimeout
	}
	if opts.lookPath == nil {
		opts.lookPath = exec.LookPath
	}
	if opts.start == nil {
		opts.start = startDetached
	}
	if opts.breakaway == nil {
		applies := breakawayApplies
		opts.breakaway = &applies
	}
	if opts.procStart == nil {
		opts.procStart = processStartTime
	}
	if opts.procTicks == nil {
		opts.procTicks = processStartTicks
	}
	if opts.alive == nil {
		opts.alive = processAlive
	}
	if opts.kill == nil {
		opts.kill = killHolder
	}
	m := &Manager{opts: opts, entries: map[string]*entry{}, stop: make(chan struct{})}
	if opts.Sink != nil {
		m.sinks = []Sink{opts.Sink}
	}
	return m
}

// AddSink adds a receiver of the attached holders' events, after the ones
// already there. A consumer that needs the manager itself (to answer an
// event with an operation) adds itself here before the first Scan or Run,
// so it misses no event.
func (m *Manager) AddSink(sink Sink) {
	m.mu.Lock()
	m.sinks = append(m.sinks[:len(m.sinks):len(m.sinks)], sink)
	m.mu.Unlock()
}

func (m *Manager) logf(format string, args ...any) {
	fmt.Fprintf(m.opts.Log, "holders: "+format+"\n", args...)
}

// Run scans at once, removes the staged scripts no live holder runs, and
// then rescans every RescanInterval until Close.
func (m *Manager) Run() {
	m.Scan()
	m.PruneScripts()
	ticker := time.NewTicker(m.opts.RescanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.Scan()
		}
	}
}

// Close ends the rescans and every attach connection. The holders and
// their children keep running for the next daemon.
func (m *Manager) Close() {
	m.stopOnce.Do(func() { close(m.stop) })
	m.mu.Lock()
	m.closed = true
	var clients []*Client
	for _, e := range m.entries {
		if e.client != nil {
			clients = append(clients, e.client)
		}
	}
	m.mu.Unlock()
	for _, c := range clients {
		c.Close()
	}
}

// Client returns the attach connection to holder id, nil when unattached.
func (m *Manager) Client(id string) *Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := m.entries[id]; e != nil {
		return e.client
	}
	return nil
}

// List returns the known holders, ordered by start, and the recent start
// failures, newest first.
func (m *Manager) List() Listing {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := Listing{Holders: []Info{}, Failures: []StartFailure{}, Incomplete: m.incomplete}
	for _, e := range m.entries {
		out.Holders = append(out.Holders, e.info())
	}
	sort.Slice(out.Holders, func(i, j int) bool {
		a, b := out.Holders[i], out.Holders[j]
		if a.StartedAt != b.StartedAt {
			return a.StartedAt < b.StartedAt
		}
		return a.ID < b.ID
	})
	cutoff := time.Now().Add(-failureAge).UnixMilli()
	for i := len(m.failures) - 1; i >= 0; i-- {
		if m.failures[i].At >= cutoff {
			out.Failures = append(out.Failures, m.failures[i])
		}
	}
	return out
}

// info is e as a listing reports it; the caller holds mu.
func (e *entry) info() Info {
	info := Info{
		ID:          e.desc.ID,
		PID:         e.desc.PID,
		Attached:    e.client != nil,
		Protocol:    e.desc.Protocol,
		StartedAt:   int64(e.desc.StartedAt * 1000),
		RestartSafe: e.desc.RestartSafe,
		Session:     e.desc.Session,
		Lost:        e.lost,
	}
	if info.Attached {
		child := e.child
		info.Child = &child
	} else {
		info.Cause = e.cause
		if info.Cause == "" {
			info.Cause = CauseNoAnswer
		}
	}
	return info
}
