package mcp

import (
	"time"

	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// parkedMailBackstop is the lost-wake poll of the parked set; it reads memory.
const parkedMailBackstop = 5 * time.Minute

// parkedMailWatch is one WatchParked call: a parked child's one-shot mail
// wake. It owns no goroutine and writes no file (no listening marker).
type parkedMailWatch struct {
	key, root string
	changed   func()
	peek      func() (wsmailbox.WaitResult, error)
}

// WatchParked calls changed once when the mailbox of key holds mail (also at
// once when it already does), until stop is called. Every parked key shares
// one goroutine, which wakes on the mailbox hub and on a slow backstop and
// peeks each key's queues in memory.
func (m *LeadMailbox) WatchParked(root, key string, changed func()) (stop func()) {
	w := &parkedMailWatch{key: key, root: root, changed: changed}
	m.pmu.Lock()
	if m.parkedW == nil {
		m.parkedW = map[*parkedMailWatch]struct{}{}
		m.pkick = make(chan struct{}, 1)
	}
	m.parkedW[w] = struct{}{}
	start := !m.pRunning
	m.pRunning = true
	m.pmu.Unlock()
	if start {
		go m.parkedLoop()
	}
	select {
	case m.pkick <- struct{}{}:
	default:
	}
	return func() {
		m.pmu.Lock()
		delete(m.parkedW, w)
		m.pmu.Unlock()
	}
}

func (m *LeadMailbox) parkedLoop() {
	sub := m.wake.Subscribe()
	defer sub.Close()
	tick := time.NewTicker(parkedMailBackstop)
	defer tick.Stop()
	for {
		m.pmu.Lock()
		if len(m.parkedW) == 0 {
			m.pRunning = false
			m.pmu.Unlock()
			return
		}
		m.pmu.Unlock()
		m.scanParked()
		select {
		case <-m.pkick:
		case <-sub.Hard():
		case <-sub.Nudge():
		case <-tick.C:
		}
	}
}

// scanParked peeks every parked key once and fires those holding mail.
func (m *LeadMailbox) scanParked() {
	m.pmu.Lock()
	watches := make([]*parkedMailWatch, 0, len(m.parkedW))
	for w := range m.parkedW {
		watches = append(watches, w)
	}
	m.pmu.Unlock()
	for _, w := range watches {
		if w.peek == nil {
			peek, err := wsmailbox.NewPeeker(wsmailbox.WaitTarget{SessionKey: w.key, Root: w.root, Paths: m.server.mailboxPaths()})
			if err != nil {
				continue
			}
			w.peek = peek
		}
		result, err := w.peek()
		if err != nil || !result.Woke() {
			continue
		}
		m.pmu.Lock()
		_, live := m.parkedW[w]
		delete(m.parkedW, w)
		m.pmu.Unlock()
		if live {
			w.changed()
		}
	}
}
