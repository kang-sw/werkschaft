package wsdashboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Messenger presence: each open Companion Messenger (one per dashboard tab)
// reports open/close (and heartbeats while open) so the companion keeps the
// phone in hand while the user is looking at the conversation, and gets one
// close notification when the last Messenger closes mid-hold. Presence is
// memory only; a tab that disappears without a close beacon ages out after
// companionMessengerPresenceTTL.
const (
	companionMessengerPresenceRoute = "/api/companion/messenger/presence"

	// companionMessengerPresenceTTL bounds how long an open report without a
	// heartbeat keeps the Messenger counted as open. The client heartbeats
	// every 60 s while open.
	companionMessengerPresenceTTL = 3 * time.Minute
	// companionMessengerCloseDebounce absorbs a close immediately followed by
	// a reopen (a reload, a tab switch) before the companion is told.
	companionMessengerCloseDebounce = 2500 * time.Millisecond

	companionMessengerPresenceMaxClients = 64
	companionMessengerPresenceBodyLimit  = 1 << 10
	companionMessengerClientMaxLen       = 64
)

// messengerOpenLocked reports whether any Messenger client reported within
// the TTL. It is read-only: only syncMessengerOpenLocked (the presence
// setter and the deadline prune) expires entries and moves the cached
// snapshot flag, so the open-to-closed transition is seen exactly once.
func (s *CompanionPhoneStore) messengerOpenLocked(now time.Time) bool {
	for _, at := range s.presence {
		if now.Sub(at) <= companionMessengerPresenceTTL {
			return true
		}
	}
	return false
}

// syncMessengerOpenLocked expires stale presence entries and updates the
// cached snapshot flag, returning it.
func (s *CompanionPhoneStore) syncMessengerOpenLocked(now time.Time) bool {
	for id, at := range s.presence {
		if now.Sub(at) > companionMessengerPresenceTTL {
			delete(s.presence, id)
		}
	}
	s.messengerOpen = len(s.presence) > 0
	return s.messengerOpen
}

// holdingPhoneLocked reports whether a session holds the phone in hand.
func (s *CompanionPhoneStore) holdingPhoneLocked() bool {
	return s.posture == CompanionPosturePhone && s.leaseOwner != ""
}

// SetMessengerPresence records client's Messenger as open (or refreshes its
// heartbeat) or closed. It reports whether the Messenger as a whole just
// closed or opened, and the phone holder ("" when the phone is on the desk).
func (s *CompanionPhoneStore) SetMessengerPresence(client string, open bool) (closedNow, openedNow bool, holder string) {
	client = normalizeMessengerClient(client)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	now := s.clock()
	wasOpen := s.messengerOpen
	s.syncMessengerOpenLocked(now)
	if open {
		if s.presence == nil {
			s.presence = make(map[string]time.Time)
		}
		if _, known := s.presence[client]; !known && len(s.presence) >= companionMessengerPresenceMaxClients {
			s.dropOldestPresenceLocked()
		}
		s.presence[client] = now
	} else {
		delete(s.presence, client)
	}
	nowOpen := s.syncMessengerOpenLocked(now)
	if wasOpen != nowOpen {
		s.publishLocked()
	}
	if s.holdingPhoneLocked() {
		holder = s.leaseOwner
	}
	return wasOpen && !nowOpen, !wasOpen && nowOpen, holder
}

func (s *CompanionPhoneStore) dropOldestPresenceLocked() {
	var oldestID string
	var oldest time.Time
	for id, at := range s.presence {
		if oldestID == "" || at.Before(oldest) {
			oldestID, oldest = id, at
		}
	}
	delete(s.presence, oldestID)
}

// pruneMessengerPresence expires stale presence as of now. It reports
// whether that closed the Messenger, and the phone holder.
func (s *CompanionPhoneStore) pruneMessengerPresence(now time.Time) (closedNow bool, holder string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	wasOpen := s.messengerOpen
	if s.syncMessengerOpenLocked(now) || !wasOpen {
		return false, ""
	}
	s.publishLocked()
	if s.holdingPhoneLocked() {
		holder = s.leaseOwner
	}
	return true, holder
}

// MessengerOpen reports whether at least one Messenger client is open.
func (s *CompanionPhoneStore) MessengerOpen() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	return s.messengerOpenLocked(s.clock())
}

// closedWhileHeldBy reports whether the Messenger is closed and holder still
// has the phone in hand: the condition the close notification acts on.
func (s *CompanionPhoneStore) closedWhileHeldBy(holder string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()
	return holder != "" && !s.messengerOpenLocked(s.clock()) && s.holdingPhoneLocked() && s.leaseOwner == holder
}

func normalizeMessengerClient(client string) string {
	client = strings.TrimSpace(client)
	if client == "" {
		return "default"
	}
	if len(client) > companionMessengerClientMaxLen {
		client = client[:companionMessengerClientMaxLen]
	}
	return client
}

// companionPresenceDebounce owns the pending close notification. gen
// invalidates a timer that a reopen or a newer close superseded.
type companionPresenceDebounce struct {
	mu    sync.Mutex
	timer *time.Timer
	gen   uint64
	// delay overrides companionMessengerCloseDebounce when > 0 (test seam).
	delay time.Duration
}

func (p *companionPresenceDebounce) cancel() {
	p.mu.Lock()
	p.gen++
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
	p.mu.Unlock()
}

// SetCompanionMessengerClosed registers the callback that gets one debounced
// call (with the phone holder) when the last Messenger closes while a
// session holds the phone.
func (d *Daemon) SetCompanionMessengerClosed(fn func(holder string)) {
	if fn == nil {
		d.companionMessengerClosed.Store(nil)
		return
	}
	d.companionMessengerClosed.Store(&fn)
}

// SetCompanionMessengerCloseDebounce overrides the close debounce (test seam;
// zero restores the default).
func (d *Daemon) SetCompanionMessengerCloseDebounce(delay time.Duration) {
	d.companionPresence.mu.Lock()
	d.companionPresence.delay = delay
	d.companionPresence.mu.Unlock()
}

// CompanionMessengerOpen reports whether at least one Messenger is open.
func (d *Daemon) CompanionMessengerOpen() bool {
	return d.companionPhone.MessengerOpen()
}

// CompanionMessengerPresence records a Messenger client's open/close report
// and returns whether any Messenger is open afterwards. A close that leaves
// no Messenger open while a session holds the phone schedules the debounced
// close notification; an open cancels a pending one.
func (d *Daemon) CompanionMessengerPresence(client string, open bool) bool {
	closedNow, openedNow, holder := d.companionPhone.SetMessengerPresence(client, open)
	if openedNow {
		d.companionPresence.cancel()
	}
	if closedNow && holder != "" {
		d.scheduleCompanionMessengerClosed(holder)
	}
	return d.companionPhone.MessengerOpen()
}

func (d *Daemon) pruneCompanionMessengerPresence(now time.Time) {
	if closedNow, holder := d.companionPhone.pruneMessengerPresence(now); closedNow && holder != "" {
		d.scheduleCompanionMessengerClosed(holder)
	}
}

func (d *Daemon) scheduleCompanionMessengerClosed(holder string) {
	p := &d.companionPresence
	p.mu.Lock()
	defer p.mu.Unlock()
	p.gen++
	gen := p.gen
	if p.timer != nil {
		p.timer.Stop()
	}
	delay := p.delay
	if delay <= 0 {
		delay = companionMessengerCloseDebounce
	}
	p.timer = time.AfterFunc(delay, func() {
		p.mu.Lock()
		current := p.gen == gen
		if current {
			p.timer = nil
		}
		p.mu.Unlock()
		if !current {
			return
		}
		select {
		case <-d.stop:
			return
		default:
		}
		if !d.companionPhone.closedWhileHeldBy(holder) {
			return
		}
		if fn := d.companionMessengerClosed.Load(); fn != nil && *fn != nil {
			(*fn)(holder)
		}
	})
}

func (d *Daemon) handleCompanionMessengerPresence(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	local, own := TrustedWrite(r)
	if !local {
		http.NotFound(w, r)
		return
	}
	if !own {
		http.Error(w, "same-origin request required", http.StatusForbidden)
		return
	}
	// navigator.sendBeacon posts text/plain, so the body is decoded as JSON
	// whatever its declared content type.
	r.Body = http.MaxBytesReader(w, r.Body, companionMessengerPresenceBodyLimit)
	var body struct {
		Open   bool   `json:"open"`
		Client string `json:"client"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid messenger presence payload", http.StatusBadRequest)
		return
	}
	open := d.CompanionMessengerPresence(body.Client, body.Open)
	writeJSON(w, http.StatusOK, map[string]any{"open": open})
}
