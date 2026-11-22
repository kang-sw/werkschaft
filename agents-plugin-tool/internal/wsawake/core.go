// Package wsawake holds one daemon-wide idle-system-sleep assertion while
// hosted turns run and the user has opted in (agent.keep_awake_during_turns).
//
// Core is the desired-state reconciler: callers publish turn membership and
// the setting in memory, and one worker goroutine owns every Backend call, so
// process launch, OS calls and waits never run on a caller's goroutine.
package wsawake

import (
	"fmt"
	"io"
	"sync"
)

// Backend takes and drops the platform's idle-system-sleep assertion. Core
// calls it from one goroutine only, never concurrently, and alternates
// Acquire and Release; Release follows only a successful Acquire.
type Backend interface {
	// Acquire takes the assertion. An error means none is held.
	Acquire() error
	// Release drops the assertion. It must be safe after the assertion was
	// lost on its own (a helper that exited, say); an error is logged and
	// the assertion counts as dropped.
	Release() error
}

// closingBackend is a Backend holding a resource of its own (the Windows
// dedicated thread). Core closes it when its worker exits, after the final
// Release.
type closingBackend interface {
	Close() error
}

// Core keeps the assertion held exactly while the setting is enabled and at
// least one member (a live turn, keyed by its conversation instance) exists.
// Every method is safe from any goroutine, and on a nil Core (a no-op), and
// returns without waiting on the backend, except Close, which drains it.
type Core struct {
	backend Backend
	log     io.Writer

	mu      sync.Mutex
	enabled bool
	members map[any]struct{}
	closed  bool
	// failed is set when an Acquire failed while eligible, and cleared at the
	// next transition into eligibility: one attempt per transition. epoch
	// counts those transitions, so a failure of an attempt made before the
	// latest one does not suppress the newer attempt.
	failed bool
	epoch  uint64
	// started is set once the worker runs; wake (one slot) tells it the
	// desired state may have changed, and done closes when it exits.
	started bool
	wake    chan struct{}
	done    chan struct{}
}

// New returns a Core over backend; log receives diagnostics (nil discards).
func New(backend Backend, log io.Writer) *Core {
	if log == nil {
		log = io.Discard
	}
	return &Core{backend: backend, log: log, members: map[any]struct{}{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
}

// eligibleLocked reports whether the assertion is wanted, ignoring a failed
// attempt.
func (c *Core) eligibleLocked() bool {
	return c.enabled && len(c.members) > 0 && !c.closed
}

func (c *Core) wantLocked() bool { return c.eligibleLocked() && !c.failed }

// change applies fn under the lock and signals the worker when the desired
// state may differ. A transition into eligibility re-arms a failed attempt.
func (c *Core) change(fn func()) {
	c.mu.Lock()
	before := c.eligibleLocked()
	fn()
	after := c.eligibleLocked()
	if after && !before {
		c.failed = false
		c.epoch++
	}
	start := !c.started && c.wantLocked()
	if start {
		c.started = true
	}
	running := c.started
	c.mu.Unlock()
	if start {
		go c.run()
		return
	}
	if running {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// SetMember adds (live) or removes key's membership. Repeating a value is a
// no-op, so duplicate or late reports cannot change the count.
func (c *Core) SetMember(key any, live bool) {
	if c == nil {
		return
	}
	c.change(func() {
		if c.closed {
			return
		}
		if live {
			c.members[key] = struct{}{}
		} else {
			delete(c.members, key)
		}
	})
}

// SetEnabled publishes the setting's current value.
func (c *Core) SetEnabled(enabled bool) {
	if c == nil {
		return
	}
	c.change(func() { c.enabled = enabled })
}

// Members is the number of live members.
func (c *Core) Members() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.members)
}

// Enabled reports the published setting.
func (c *Core) Enabled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled
}

// Close drops every membership, releases a held assertion and waits for the
// worker to exit, so no acquisition outlives it. Later calls change nothing.
func (c *Core) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.closed {
		started := c.started
		c.mu.Unlock()
		if started {
			<-c.done
		}
		return
	}
	c.closed = true
	c.members = map[any]struct{}{}
	started := c.started
	c.mu.Unlock()
	if !started {
		return
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
	<-c.done
}

// run is the only goroutine that calls the backend. It converges the held
// state on the desired one, re-reading after every call, so a change made
// while a call ran is applied next.
func (c *Core) run() {
	defer close(c.done)
	held := false
	for {
		c.mu.Lock()
		want, closed, epoch := c.wantLocked(), c.closed, c.epoch
		c.mu.Unlock()
		switch {
		case want && !held:
			if err := c.backend.Acquire(); err != nil {
				fmt.Fprintf(c.log, "keep-awake: take the idle-sleep assertion: %v\n", err)
				c.mu.Lock()
				// Only this attempt failed: a later transition into
				// eligibility tries again (change clears failed).
				if c.epoch == epoch {
					c.failed = true
				}
				c.mu.Unlock()
			} else {
				held = true
			}
			continue
		case !want && held:
			if err := c.backend.Release(); err != nil {
				fmt.Fprintf(c.log, "keep-awake: drop the idle-sleep assertion: %v\n", err)
			}
			held = false
			continue
		}
		if closed {
			if b, ok := c.backend.(closingBackend); ok {
				if err := b.Close(); err != nil {
					fmt.Fprintf(c.log, "keep-awake: close the idle-sleep backend: %v\n", err)
				}
			}
			return
		}
		<-c.wake
	}
}
