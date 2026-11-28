// runner_startup.go holds Runner construction and startup recovery.
package wsagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func NewRunner(opts Options) *Runner {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Timer == nil {
		opts.Timer = func(d time.Duration) (<-chan time.Time, func()) {
			t := time.NewTimer(d)
			return t.C, func() { t.Stop() }
		}
	}
	if opts.AgyModelWaitBackoff == nil {
		opts.AgyModelWaitBackoff = defaultAgyModelWaitBackoff
	}
	if opts.Log == nil {
		opts.Log = io.Discard
	}
	if opts.heartbeat <= 0 {
		opts.heartbeat = wslive.HeartbeatInterval
	}
	if opts.Trusted == nil {
		opts.Trusted = wsdashboard.TrustedWrite
	}
	if opts.Alive == nil {
		opts.Alive = wsstate.ProcessAlive
	}
	if opts.Backends != nil && (opts.AccountSlots != nil || opts.PiRuntime != nil) {
		var cloned map[string]BackendRegistration
		ensureCloned := func() {
			if cloned == nil {
				cloned = make(map[string]BackendRegistration, len(opts.Backends))
				for k, v := range opts.Backends {
					cloned[k] = v
				}
			}
		}
		for name, reg := range opts.Backends {
			changed := false
			if opts.AccountSlots != nil {
				if binder, ok := reg.Backend.(slotHomeBinder); ok {
					harness := name
					slots := opts.AccountSlots
					explicitHome := opts.ExplicitHome
					reg.Backend = binder.withSlotHomeResolver(func() string {
						return wscred.ResolveHarnessHomeFromRegistry(slots, harness, "", explicitHome, nil)
					})
					changed = true
				}
			}
			if opts.PiRuntime != nil {
				if binder, ok := reg.Backend.(piRuntimeBinder); ok {
					reg.Backend = binder.withPiRuntime(opts.PiRuntime)
					changed = true
				}
			}
			if changed {
				ensureCloned()
				cloned[name] = reg
			}
		}
		if cloned != nil {
			opts.Backends = cloned
		}
	}
	r := &Runner{opts: opts, versions: loadHarnessVersions(opts.Dir, opts.Log), convs: map[string]*conv{}, closed: map[string]Record{}, quit: make(chan struct{}), scanned: make(chan struct{}), rejoined: make(chan struct{}), startupLoaded: make(chan struct{}), notify: func(string, string) {}}
	r.awake = newKeepAwake(opts)
	if opts.Holders != nil {
		// Before the manager's first scan, so a predecessor's holders are
		// seen attaching.
		opts.Holders.AddSink(&r.holders)
	}
	return r
}

// SetLiveHost binds the daemon's RAM projection before Start.
func (r *Runner) SetLiveHost(host wsdashboard.LiveHost) { r.opts.LiveHost = host }

// SetAuditSink binds the daemon's capture sink before Start.
func (r *Runner) SetAuditSink(sink wsaudit.Sink) { r.opts.AuditSink = sink }

// Start loads every conversation record from the session registry, with no
// process (lazy resume), and starts serving them. notify is called after
// each write to a conversation's stores, so the daemon re-reads it. The
// daemon calls Start once it holds its lock.
func (r *Runner) Start(notify func(replyID, project string)) {
	r.startWithServing(notify, nil)
	r.hydrateClosedRows()
}

// StartForPublication waits for held-session recovery before the daemon
// publishes its endpoint. Deferred processless kicks and closed presentations
// run once it accepts requests.
func (r *Runner) StartForPublication(notify func(replyID, project string), serving <-chan struct{}) {
	r.startWithServing(notify, serving)
	<-r.rejoined
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		select {
		case <-serving:
			r.kickAfterServing()
			r.hydrateClosedRows()
		case <-r.quit:
		}
	}()
}

// Retained name check: retained holder descriptors (TestRetiredPiRealHolderSurvivesStartup).
func (r *Runner) startWithServing(notify func(replyID, project string), serving <-chan struct{}) {
	r.reloadProviderConfig()
	r.started.Store(true)
	startupLoaded := make(chan struct{})
	r.startupMu.Lock()
	r.startupLoaded = startupLoaded
	r.serving = serving
	r.startupMu.Unlock()
	if notify != nil {
		r.notify = notify
	}
	private := r.openDurableAgentRecords()
	r.restoreRetiredPiHistory()
	var all, open []Record
	if r.meta != nil {
		privateIDs := make([]string, 0, len(private))
		for id := range private {
			privateIDs = append(privateIDs, id)
		}
		slices.Sort(privateIDs)
		for _, id := range privateIDs {
			all = append(all, private[id])
		}
		if r.metaSnap != nil {
			recordIDs := make([]string, 0, len(r.metaSnap.Records))
			for id := range r.metaSnap.Records {
				if _, ok := private[id]; ok {
					continue
				}
				recordIDs = append(recordIDs, id)
			}
			slices.Sort(recordIDs)
			for _, id := range recordIDs {
				ar := r.metaSnap.Records[id]
				rec, err := decodeRecord("sqlite agent record", id, ar.JSON)
				if err != nil {
					fmt.Fprintf(r.opts.Log, "agent: skip %s: %v\n", id, err)
					continue
				}
				if r.durableProcess(rec.Backend) {
					rec = durableHistoryProjection(rec)
				}
				all = append(all, rec)
			}
		}
	} else {
		entries, _ := wsstate.ReadDir(filepath.Join(r.opts.Dir, "sessions"))
		for _, e := range entries {
			if !e.IsDir() || !wsdashboard.ValidReplyID(e.Name()) {
				continue
			}
			dir := wsdashboard.SessionDir(r.opts.Dir, e.Name())
			if rec, ok := private[e.Name()]; ok {
				r.restoreDurableProjection(dir, rec)
				all = append(all, rec)
				continue
			}
			rec, err := readRecord(dir)
			if err != nil {
				if !errors.Is(err, os.ErrNotExist) {
					fmt.Fprintf(r.opts.Log, "agent: skip %s: %v\n", e.Name(), err)
				}
				continue
			}
			// The history projection is never an input to the durable scheduler.
			if r.durableProcess(rec.Backend) {
				rec = durableHistoryProjection(rec)
			}
			all = append(all, rec)
		}
	}
	// From the full set, before any record is loaded or rejoined, so a
	// child read before its parent is admitted from that parent.
	r.resolveSpawnerBackends(all)
	dead := map[string]*admissionRefusal{}
	bindings := map[string]resolvedAgentBinding{}
	retiredPiHolders := map[string]bool{}
	for _, rec := range all {
		if rec.Backend == BackendPi {
			if rec.HolderID != "" {
				retiredPiHolders[rec.HolderID] = true
				r.rememberRetiredPiBinding(rec)
			}
			if err := r.archiveRetiredPiHistory(rec); err != nil {
				fmt.Fprintf(r.opts.Log, "agent %s: archive retired Pi history: %v\n", rec.ReplyID, err)
			} else if rec.ClosedAt == "" && !rec.handedOff() && r.closeAtStart(rec) {
				continue
			}
			// Even if the record could not be written, never rejoin a Pi process.
			r.loadClosed(rec)
			continue
		}
		if rec.ClosedAt != "" || rec.handedOff() {
			// A handed-off session is a terminal harness's: nothing here
			// writes its row, watches its mail, or starts a process for it.
			r.loadClosed(rec)
			r.restoreRetainedReleaseOccupancy(rec)
		} else {
			open = append(open, rec)
			binding := r.resolveAgentBinding(rec)
			bindings[rec.ReplyID] = binding
			// A checkout removed with its release marker still ends the
			// binding: the gone path refuses it like a recorded release.
			if refusal := r.deadBindingRefusal(rec, binding.ended()); refusal != nil {
				dead[rec.ReplyID] = refusal
			}
		}
	}
	open = r.expireBeforeServing(open, dead)
	orphans := r.orphansAtStart(open)
	startup := make([]*conv, 0, len(open))
	cold := r.coldParkable(open, dead, orphans)
	for _, rec := range open {
		binding := bindings[rec.ReplyID]
		refusal := dead[rec.ReplyID]
		if cold[rec.ReplyID] {
			// A waiting child with nothing pending holds no loop: its mail
			// or a send rehydrates it (unpark).
			r.loadParked(rec)
			continue
		}
		// A cross-harness worker must finish the existing rejoin and
		// retirement path, including settlement of a cut-off turn.
		if refusal != nil && r.admitWorkerRecord(rec) == nil {
			r.closeDeadAtStart(rec, refusal, binding)
			continue
		}
		if refusal == nil && orphans[rec.ReplyID] && r.closeAtStart(rec) {
			continue
		}
		// A lead whose process may still run under its holder rejoins
		// it there, before the scan below replays the holder's records.
		// A turn the last daemon's exit cut off settles there too.
		startup = append(startup, r.startConvWith(rec, nil, false, nil, &binding))
	}
	close(startupLoaded)
	for _, c := range startup {
		c.do(func() {
			if refusal := dead[c.rec.ReplyID]; refusal != nil && c.proc == nil && c.rec.HolderID == "" {
				c.closeDeadAfterRefusal(refusal)
				return
			}
			if r.durableProcess(c.rec.Backend) {
				c.kick()
				c.releaseHeld()
				return
			}
			// Re-run only startup work whose earlier signal may have been
			// consumed by the all-records-loaded barrier. A held inbox send on
			// a processless conversation remains lazy until a new wake, as it
			// did before restart restore existed.
			if c.proc != nil || len(c.rec.Queued) > 0 || len(c.rec.Mail) > 0 || len(c.rec.Reports) > 0 {
				c.kick()
			}
		})
	}
	// All records are loaded now, including children sorted after their lead.
	for _, c := range startup {
		c.do(func() {
			// Release a persisted HeldTurn whose background work ended
			// with the previous daemon (open children still hold it).
			if c.rec.Parent != nil && c.rec.HolderID == "" && !r.durableProcess(c.rec.Backend) {
				c.releaseHeld()
			}
			c.spawnDirty = true
			c.live.writeSnapshot()
		})
	}
	// A child loaded before its parent found no parent to report to.
	r.deliverPending()
	r.startParkSweeper()
	if r.opts.Holders == nil {
		// No holder to wait for.
		r.markScanned()
		r.finishMCPRejoin()
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.opts.Holders.Scan()
		r.markScanned()
		r.holdersScanned(retiredPiHolders)
		r.finishMCPRejoin()
	}()
}

func (r *Runner) startupReady() bool {
	if r == nil {
		return true
	}
	r.startupMu.RLock()
	startupLoaded := r.startupLoaded
	r.startupMu.RUnlock()
	if startupLoaded == nil {
		return true
	}
	select {
	case <-startupLoaded:
		return true
	default:
		return false
	}
}

// publicationReady reports whether new process launches may dial the daemon's
// published endpoint. Plain Start sets no serving channel, so it is true at
// once; StartForPublication gates processless launches until serving closes.
func (r *Runner) publicationReady() bool {
	if r == nil {
		return true
	}
	r.startupMu.RLock()
	serving := r.serving
	r.startupMu.RUnlock()
	if serving == nil {
		return true
	}
	select {
	case <-serving:
		return true
	default:
		return false
	}
}

// kickAfterServing wakes conversations whose processless start kick was
// deferred until the daemon published its endpoint and started accepting.
func (r *Runner) kickAfterServing() {
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	for _, c := range convs {
		select {
		case <-r.quit:
			return
		default:
		}
		if c.kickDeferred.Swap(false) {
			c.signalInbox()
		}
	}
}

// expireBeforeServing applies the purge guard before any open record starts
// a loop. A descendant that could not be deleted still protects its ancestors.
func (r *Runner) expireBeforeServing(open []Record, dead map[string]*admissionRefusal) []Record {
	expired := r.expiredAtStart(open, dead)
	if len(expired) == 0 {
		return open
	}
	startupOpen := make(map[string]Record, len(open))
	r.mu.Lock()
	for _, rec := range open {
		startupOpen[rec.ReplyID] = rec
		r.indexChildLocked(rec)
	}
	r.mu.Unlock()
	for _, id := range expired {
		rec := startupOpen[id]
		r.mu.Lock()
		retain := r.hasLiveDescendantsLocked(safeStopParent(id, rec.Lead), startupOpen)
		r.mu.Unlock()
		if retain || !r.deleteSessionRecord(rec) {
			continue
		}
		delete(startupOpen, id)
		r.mu.Lock()
		r.unindexChildLocked(rec)
		r.mu.Unlock()
	}
	return slices.DeleteFunc(open, func(rec Record) bool {
		_, keep := startupOpen[rec.ReplyID]
		return !keep
	})
}

// loadClosed lists closed (or handed-off) record rec at Start.
func (r *Runner) loadClosed(rec Record) {
	closure := rec.DeadBindingClosure
	dead := rec.ClosedAt != "" && closure != nil && closure.Reason != ""
	if dead && rec.HolderID != "" {
		// A retained release closure can reach disk while its holder still
		// owns the running process (an older daemon detached it, or its stop
		// failed). Keep the generic orphan sweep off it: the complete startup
		// inventory ends it through finalizeDeadHolders.
		r.holders.reject(rec.HolderID)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed[rec.ReplyID] = rec
	r.indexChildLocked(rec)
	if dead {
		r.rememberDeadRefusalLocked(rec.ReplyID, &admissionRefusal{reason: closure.Reason, checkout: closure.Checkout})
	}
}

// Hydration reads retained evidence outside the runner lock. A concurrent
// reopen, close, or forget owns any newer presentation for the same ID.
func (r *Runner) hydrateClosedRows() {
	r.mu.Lock()
	records := make([]Record, 0, len(r.closed))
	for id, rec := range r.closed {
		if _, hydrated := r.closedRows[id]; !hydrated {
			records = append(records, rec)
		}
	}
	r.mu.Unlock()
	for _, rec := range records {
		select {
		case <-r.quit:
			return
		default:
		}
		st := r.closedState(rec)
		r.mu.Lock()
		current, stillClosed := r.closed[rec.ReplyID]
		_, hydrated := r.closedRows[rec.ReplyID]
		publish := stillClosed && !hydrated && current.OpenGen == rec.OpenGen &&
			current.ClosedAt == rec.ClosedAt && current.HandedOffAt == rec.HandedOffAt
		if publish {
			if r.closedRows == nil {
				r.closedRows = make(map[string]State, len(records))
			}
			r.closedRows[rec.ReplyID] = st
		}
		r.mu.Unlock()
		if publish {
			r.wakeSpawnParents(st)
		}
	}
}

// closeAtStart closes open record rec at Start, with no loop started, and
// reports whether it did.
func (r *Runner) closeAtStart(rec Record) bool {
	now := r.opts.Now()
	// An orphan's cut-off turn ends here, as close ends a running one.
	stampClosed(&rec, now)
	if err := r.persistAgentRecord(wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID), rec); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: record write: %v\n", rec.ReplyID, err)
		return false
	}
	if err := wsdashboard.WriteRegistration(r.opts.Dir, registration(rec, now)); err != nil {
		fmt.Fprintf(r.opts.Log, "agent %s: registration write: %v\n", rec.ReplyID, err)
	}
	r.notify(rec.ReplyID, rec.Root)
	r.loadClosed(rec)
	return true
}

// orphansAtStart picks the open spawned children a daemon start closes
// instead of serving (closeAtStart): one whose conversation parent is
// closed or gone, so no report of it reaches one, or that descends from
// one. A finished child is not closed: it sleeps (agent_tools.go).
func (r *Runner) orphansAtStart(open []Record) map[string]bool {
	isOpen := map[string]bool{}
	for _, rec := range open {
		isOpen[rec.ReplyID] = true
	}
	done := map[string]bool{}
	stays := func(id string) bool { return isOpen[id] && !done[id] }
	for changed := true; changed; {
		changed = false
		for _, rec := range open {
			id := rec.ReplyID
			// Legacy direct non-Pi children keep their existing next-launch
			// admission refusal when a parent disappears. Held and durable
			// workers end during startup when their conversation parent is gone.
			recoverable := rec.HolderID != "" || r.durableProcess(rec.Backend)
			if done[id] || rec.Lead || rec.Parent == nil || !recoverable {
				continue
			}
			if rec.Parent.Kind == ParentConversation && !stays(rec.Parent.ReplyID) {
				done[id], changed = true, true
			}
		}
	}
	return done
}

// markScanned closes scanned, once however often it is called.
func (r *Runner) markScanned() {
	r.scanOnce.Do(func() { close(r.scanned) })
}

// holdersScanned follows the first holder scan of this daemon: each
// rejoined process learns where its replay ends (or that its holder is
// gone), and a lead holder no conversation runs under is ended, so no
// second process of a session stays behind a crash between a spawn and its
// record.
// Retained name check: retained holder descriptors (TestRetiredPiRealHolderSurvivesStartup).
func (r *Runner) holdersScanned(retiredPiHolders map[string]bool) {
	// Listed before the conversations are taken and asked: a conversation
	// opened after the listing launched no holder in it, and a holder a
	// start launched meanwhile is in its record by the time the
	// conversation's loop answers.
	deadHolders := r.deadHolderRecords()
	inventory := r.opts.Holders.List()
	listed := inventory.Holders
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	owned := map[string]bool{}
	for _, c := range convs {
		c.do(func() {
			c.checkReattach()
			if c.hidden {
				c.stopProcess("")
				if err := c.retireHolder(); err != nil {
					c.setStatus(func() { c.lastErr = "archive process: " + err.Error() })
				}
			}
			if c.proc == nil {
				// A start kick put off until the scan (a rejoin that failed).
				// During StartForPublication, never start a fresh harness CLI
				// before endpoint publication (<-serving) and finishMCPRejoin;
				// defer pending kicks until kickAfterServing wakes them.
				if r.publicationReady() {
					c.kick()
				} else if c.hasPendingKick() && !c.deferKickIfUnpublished() {
					c.kick()
				}
			}
			if id := c.rec.HolderID; id != "" {
				owned[id] = true
			}
		})
	}
	select {
	case <-r.quit:
		// The daemon closes: a loop that ended on it let its process go
		// under its holder for the next daemon (letGo) and answered nothing
		// above, so its holder would read as no conversation's. The next
		// daemon's first scan sweeps instead.
		return
	default:
	}
	if !inventory.Incomplete {
		r.finalizeDeadHolders(deadHolders, listed)
	}
	for _, info := range listed {
		var meta holderSession
		if json.Unmarshal(info.Session, &meta) != nil || retiredPiHolders[info.ID] || meta.retiredPi() {
			// Pi history is closed, but its holders retain ownership of their processes.
			continue
		}
		// Older lead descriptors recorded only the profile name. A custom
		// profile could have launched Pi, and current config cannot establish
		// the backend that originally launched an orphaned holder.
		if meta.Kind == holderKindLead && meta.Backend == "" {
			continue
		}
		if (meta.Kind != holderKindLead && meta.Kind != holderKindAgentWorker) || owned[info.ID] || r.holders.isRejected(info.ID) {
			continue
		}
		fmt.Fprintf(r.opts.Log, "agent: ending conversation holder %s of %s: no open conversation runs under it\n", info.ID, meta.ReplyID)
		if err := r.opts.Holders.Stop(info.ID); err != nil {
			fmt.Fprintf(r.opts.Log, "agent: stop holder %s: %v\n", info.ID, err)
		}
	}
}

// Retained name check: retained holder descriptors (TestRetiredPiRealHolderSurvivesStartup).
func (meta holderSession) retiredPi() bool {
	return meta.Kind == holderKindPiWorker || meta.Backend == BackendPi
}

// scanDone reports whether the holder manager's first scan of this daemon
// returned. Before it the manager lists only the holders attached so far,
// so a holder a record names that is missing from the listing may still
// run: nothing may take it for gone, or start a second process beside it.
func (r *Runner) scanDone() bool {
	select {
	case <-r.scanned:
		return true
	default:
		return false
	}
}

// Close ends every conversation's process and stops the runner; the
// conversations stay open on disk and resume lazily under the next daemon.
// A lead conversation's process is let go, not ended: it runs on under its
// session holder, and the next daemon rejoins it.

// resolveSpawnerBackends derives the spawner stamp of each record made before
// it (Record.SpawnerBackend) on a backend registered with a cross-harness
// refusal: its parent's record backend, found by Parent.ReplyID among all
// the records read at startup. A record whose parent has no record keeps no
// stamp, so admitWorkerRecord refuses it as cross-harness. The one derived
// value then drives every admission of the record: rejoin classification,
// holder retirement, wake, and the stop retry. It is kept in memory and
// rides the record's next write.
func (r *Runner) resolveSpawnerBackends(all []Record) {
	backends := make(map[string]string, len(all))
	for _, rec := range all {
		backends[rec.ReplyID] = rec.Backend
	}
	for i := range all {
		rec := &all[i]
		if rec.Lead || rec.Parent == nil || rec.SpawnerBackend != "" || r.opts.Backends[rec.Backend].crossHarnessRefusal == "" {
			continue
		}
		rec.SpawnerBackend = backends[rec.Parent.ReplyID]
	}
}
