package mcp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

const whiteboardBoardIdle = 30 * time.Minute

// WhiteboardStores shares held boards across the daemon's MCP connections,
// dashboard and nudge scheduler. Each board has its own admission lock.
type WhiteboardStores struct {
	mu     sync.Mutex
	slots  map[string]*whiteboardSlot
	closed bool
	once   sync.Once
	err    error

	idle          time.Duration
	options       wswhiteboard.BoardDBOptions
	changed       func(root, key string)
	onLoadArchive func(dir string)
}

type whiteboardSlot struct {
	mu    sync.Mutex
	board *wswhiteboard.BoardDB
	// rev mirrors board's revision (0 while no board is held), republished at
	// the end of every admitted use so Revision reads it without slot.mu or
	// any I/O.
	rev atomic.Uint64

	// Guarded by WhiteboardStores.mu, including while a caller waits on mu.
	users int
	due   time.Time
	timer *time.Timer
}

func NewWhiteboardStores(log io.Writer) *WhiteboardStores {
	s := &WhiteboardStores{slots: make(map[string]*whiteboardSlot), idle: whiteboardBoardIdle}
	if log != nil {
		s.options.Logf = func(format string, args ...any) {
			fmt.Fprintf(log, format+"\n", args...)
		}
	}
	s.options.OnLoadArchive = func(dir string) {
		s.mu.Lock()
		fn := s.onLoadArchive
		s.mu.Unlock()
		if fn != nil {
			fn(dir)
		}
	}
	return s
}

// SetChanged installs the daemon's in-process notification before it starts
// serving. The callback runs outside the board lock.
func (s *WhiteboardStores) SetChanged(fn func(root, key string)) { s.changed = fn }

// SetOnLoadArchive installs a callback invoked after any board's LoadArchive
// commits, used by the dashboard to evict cached mockup bundles for that board.
func (s *WhiteboardStores) SetOnLoadArchive(fn func(dir string)) {
	s.mu.Lock()
	s.onLoadArchive = fn
	s.mu.Unlock()
}

// peekHeld runs fn on the board root/key already holds, renewing its idle
// lease as an active use does, and never opens, imports, or creates one:
// false when no board is held.
func (s *WhiteboardStores) peekHeld(root, key string, fn func(*wswhiteboard.BoardDB)) bool {
	dir := whiteboardBoardDir(root, key)
	s.mu.Lock()
	slot := s.slots[dir]
	if s.closed || slot == nil {
		s.mu.Unlock()
		return false
	}
	slot.users++
	s.mu.Unlock()
	slot.mu.Lock()
	defer slot.mu.Unlock()
	board := slot.board
	if board != nil {
		fn(board)
	}
	s.release(dir, slot, board != nil)
	return board != nil
}

func (s *WhiteboardStores) use(root, key string, legacy *wswhiteboard.State, activity bool, fn func(*whiteboardSlot) error) error {
	return s.useLegacy(root, key, func() (*wswhiteboard.State, error) { return legacy, nil }, activity, fn)
}

func (s *WhiteboardStores) useLegacy(root, key string, legacy func() (*wswhiteboard.State, error), activity bool, fn func(*whiteboardSlot) error) error {
	return s.useDirectory(whiteboardBoardDir(root, key), root, key, legacy, activity, fn)
}

func (s *WhiteboardStores) useDirectory(dir, root, key string, legacy legacyBoardReader, activity bool, fn func(*whiteboardSlot) error) error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return errWhiteboardDraining
	}
	slot := s.slots[dir]
	if slot == nil {
		slot = new(whiteboardSlot)
		s.slots[dir] = slot
	}
	slot.users++
	s.mu.Unlock()

	slot.mu.Lock()
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	var changed bool
	defer func() {
		s.release(dir, slot, activity || changed)
		slot.mu.Unlock()
		if changed && s.changed != nil {
			s.changed(root, key)
		}
	}()
	if closed {
		return errWhiteboardDraining
	}
	if slot.board == nil {
		fromLegacy := false
		if legacy != nil {
			read := legacy
			legacy = func() (*wswhiteboard.State, error) {
				state, err := read()
				fromLegacy = state != nil && err == nil
				return state, err
			}
		}
		board, err := wswhiteboard.OpenBoardDBLazy(dir, legacy, s.options)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		slot.board = board
		changed = fromLegacy && board != nil
	}
	var revision uint64
	if slot.board != nil {
		revision = slot.board.Revision()
	}
	slot.rev.Store(revision)
	err := fn(slot)
	var after uint64
	if slot.board != nil {
		after = slot.board.Revision()
	}
	slot.rev.Store(after)
	changed = changed || slot.board != nil && after != revision
	return err
}

// Revision is the revision of the board root/key already holds in memory, 0
// when none is held. It never opens, imports or creates a board, takes no
// board lock and does no I/O: it reads the mirror republished after each
// admitted use. A caller compares two reads to learn whether the board changed
// in between; a board opened in between also reads as changed.
func (s *WhiteboardStores) Revision(root, key string) uint64 {
	dir := whiteboardBoardDir(root, key)
	s.mu.Lock()
	slot := s.slots[dir]
	s.mu.Unlock()
	if slot == nil {
		return 0
	}
	return slot.rev.Load()
}

var (
	errWhiteboardOwner    = errors.New("whiteboard calls require the ws dashboard daemon, which owns the board")
	errWhiteboardDraining = errors.New("whiteboard board owner is draining")
)

func (s *WhiteboardStores) release(dir string, slot *whiteboardSlot, activity bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	slot.users--
	if s.closed {
		return
	}
	if slot.board == nil && slot.users == 0 {
		delete(s.slots, dir)
		if slot.timer != nil {
			slot.timer.Stop()
		}
		return
	}
	if activity || slot.due.IsZero() {
		slot.due = time.Now().Add(s.idle)
	}
	if slot.users != 0 {
		return
	}
	delay := max(0, time.Until(slot.due))
	if slot.timer != nil {
		slot.timer.Reset(delay)
	} else {
		slot.timer = time.AfterFunc(delay, func() { s.expire(dir, slot) })
	}
}

func (s *WhiteboardStores) expire(dir string, slot *whiteboardSlot) {
	slot.mu.Lock()
	defer slot.mu.Unlock()
	s.mu.Lock()
	if s.closed || s.slots[dir] != slot || slot.users != 0 {
		s.mu.Unlock()
		return
	}
	if delay := time.Until(slot.due); delay > 0 {
		slot.timer.Reset(delay)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	if err := slot.close(); err != nil && s.options.Logf != nil {
		s.options.Logf("whiteboard: idle close %s: %v", dir, err)
	}

	// Keep the admission lock through SQLite close. If a caller arrived
	// meanwhile, it reopens in this slot once the exclusive handle is gone.
	s.mu.Lock()
	if slot.users == 0 && !s.closed {
		delete(s.slots, dir)
	}
	slot.due = time.Time{}
	s.mu.Unlock()
}

func (s *whiteboardSlot) close() error {
	if s.board == nil {
		return nil
	}
	err := s.board.Close()
	s.board = nil
	s.rev.Store(0)
	return err
}

// Close refuses queued and future operations, finishes admitted operations,
// and releases every SQLite handle before the daemon releases its ownership.
func (s *WhiteboardStores) Close() error {
	s.once.Do(func() {
		s.mu.Lock()
		s.closed = true
		slots := s.slots
		s.slots = nil
		for _, slot := range slots {
			if slot.timer != nil {
				slot.timer.Stop()
			}
		}
		s.mu.Unlock()
		for dir, slot := range slots {
			slot.mu.Lock()
			if err := slot.close(); err != nil {
				s.err = errors.Join(s.err, fmt.Errorf("close %s: %w", dir, err))
			}
			slot.mu.Unlock()
		}
	})
	return s.err
}
