// runner_conversation.go holds conversation lifecycle and registration.
package wsagent

import (
	"fmt"
	"slices"
	"sync"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

func (r *Runner) Close() {
	r.once.Do(func() { close(r.quit) })
	r.wg.Wait()
	r.piPurgeMu.Lock()
	if r.piPurgeStore != nil && r.piPurgeStore != r.durableRecords {
		if err := r.piPurgeStore.Close(); err != nil {
			fmt.Fprintf(r.opts.Log, "agent: close retired Pi purge store: %v\n", err)
		}
	}
	r.piPurgeMu.Unlock()
	if r.durableRecords != nil {
		if err := r.durableRecords.Close(); err != nil {
			fmt.Fprintf(r.opts.Log, "agent: close durable records: %v\n", err)
		}
	}
	// Every loop has removed its membership; drain the backend so no late
	// acquisition outlives the runner.
	r.awake.Close()
}

// InboxChanged tells the runner a conversation's inbox changed from the
// session page (a message, cancel, steer, or interrupt), so it delivers or
// interrupts as for its own routes. It returns at once: the loop does the
// work (inboxChanged). An unknown reply-id is ignored.
func (r *Runner) InboxChanged(replyID string) {
	if c := r.conv(replyID); c != nil {
		c.signalInbox()
	}
}

func (r *Runner) conv(replyID string) *conv {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.convs[replyID]
}

// summaryLock returns replyID's summary writer mutex, the same one for
// every conversation generation of the session. Only the map guard is
// taken, and it is released before the caller locks the mutex.
func (r *Runner) summaryLock(replyID string) *sync.Mutex {
	r.summaryMu.Lock()
	defer r.summaryMu.Unlock()
	if r.summaryLocks == nil {
		r.summaryLocks = map[string]*sync.Mutex{}
	}
	l := r.summaryLocks[replyID]
	if l == nil {
		l = &sync.Mutex{}
		r.summaryLocks[replyID] = l
	}
	return l
}

// setSummaryOwner makes c replyID's summary writer generation. It waits
// for a writer of the session that holds the summary lock (a ring or
// snapshot write, or a heartbeat sweep), so the owner never changes under
// one.
func (r *Runner) setSummaryOwner(replyID string, c *conv) {
	l := r.summaryLock(replyID)
	l.Lock()
	defer l.Unlock()
	r.summaryMu.Lock()
	defer r.summaryMu.Unlock()
	if r.summaryOwners == nil {
		r.summaryOwners = map[string]*conv{}
	}
	r.summaryOwners[replyID] = c
}

// summaryOwner reads replyID's summary writer generation, unpinned: it can
// change once this returns. Use lockSummaryOwner to decide a write on it.
func (r *Runner) summaryOwner(replyID string) *conv {
	r.summaryMu.Lock()
	defer r.summaryMu.Unlock()
	return r.summaryOwners[replyID]
}

// startConv starts serving rec. It writes the registration and the
// snapshot, and tells the daemon, before it takes r.mu to list the
// conversation, so no other conversation's Get, List, or act waits on
// those writes; r.mu is not held. seed, when not empty, is the command
// list the first snapshot shows (lastCommands); a process's own list
// replaces it.
func (r *Runner) startConv(rec Record, seed []wslive.SlashCommand) *conv {
	return r.startConvWith(rec, seed, false, nil, nil)
}

func (r *Runner) startConvWith(rec Record, seed []wslive.SlashCommand, spawnPending bool, claimWidth func(), loaded *resolvedAgentBinding) *conv {
	dir := wsdashboard.SessionDir(r.opts.Dir, rec.ReplyID)
	c := &conv{
		r:            r,
		dir:          dir,
		inbox:        wsinbox.Dir(dir),
		rec:          rec,
		live:         newLiveWriter(dir, rec.WSKey, rec.Backend, r.opts.Now, r.opts.Log),
		reqs:         make(chan func()),
		inboxC:       make(chan struct{}, 1),
		spawnC:       make(chan struct{}, 1),
		spawnDirty:   true,
		spawnPending: spawnPending,
		mailC:        make(chan struct{}, 1),
		done:         make(chan struct{}),
		settleC:      make(chan struct{}),
	}
	c.refreshAmbient()
	// Its root and effective working directory never change after this:
	// resolved once here, they serve every occupancy scan (boundConvs) from
	// memory.
	binding := resolvedAgentBinding{}
	if loaded != nil {
		binding = *loaded
	} else {
		binding = r.resolveAgentBinding(rec)
	}
	c.canonRoot, c.canonCwd = binding.root, binding.cwd
	c.checkouts, c.releasedCheckout = binding.checkouts, binding.released
	// Derived from the record's immutable backend on every load, with no
	// stored field, and before any profile or config read.
	refusal := r.admitWorkerRecord(rec)
	if !rec.Lead && refusal == nil {
		if level, source, err := r.currentWorkerPermission(rec); err == nil {
			c.workerPermissionLevel = level
			c.workerPermissionSource = source
		} else {
			c.lastErr = appendDiagnostic(c.lastErr, "worker permission selection: "+err.Error())
		}
	}
	if loaded != nil && rec.PendingCompaction != nil {
		// Restore a rotated lead's carry-forward compaction handoff before
		// the loop or any inbox/mail signal can kick. canKick's scan/replay
		// gates order delivery after holder reconciliation.
		c.restorePendingCompaction(rec.PendingCompaction)
	} else if loaded != nil && r.opts.RestoreBundle != nil {
		// Install the marker before the loop or any inbox/mail signal can
		// kick. canKick's scan/replay gates then order delivery after holder
		// reconciliation without admitting sessions spawned during the scan.
		c.compactNote = restartRestoreMarker
	}
	c.refreshHeld()
	c.live.coverage = c.snapshotCoverage
	if rec.Lead {
		c.leadCompactionMode(rec)
		_, _, _ = refreshPermissionSelection(dir)
		r.setSummaryOwner(rec.ReplyID, c)
		c.live.owner = c.lockSummaryWriter
		c.live.ownerRAM = c.lockSummaryWriterRAM
	}
	if rec.Lead {
		hidden, err := wsdashboard.ReadSessionHidden(r.opts.Dir, rec.ReplyID)
		if err != nil {
			// Fail closed before any startup inbox/mail/replay signal can kick.
			hidden = true
			c.lastErr = "read session visibility: " + err.Error()
			fmt.Fprintf(r.opts.Log, "agent %s: %s\n", rec.ReplyID, c.lastErr)
		}
		c.hidden = hidden
	}
	c.live.wake = func() {
		if slot := c.live.currentSlot(); slot != nil {
			slot.RingAppended()
		} else {
			r.notify(rec.ReplyID, rec.Root)
		}
	}
	c.live.beforeSnapshot = c.seedSpawnRows
	c.observations = c.live.st.ObservedResponses("")
	own := c.live.st.Snapshot(r.opts.Now().UnixMilli())
	if own.Lead.Observed != nil {
		observed := own.Lead.Observed.Current
		c.observed = &observed
	}
	if own.Lead.OutputRate != nil {
		rate := own.Lead.OutputRate.OutputTps
		c.outputTps = &rate
	}
	if usage := own.Usage; usage != nil && usage.Context != nil && usage.Context.Tokens != nil {
		tokens := float64(*usage.Context.Tokens)
		c.contextTokens = &tokens
	}
	if usage := own.Usage; usage != nil && usage.Context != nil && usage.Context.Window > 0 {
		c.contextWindow = usage.Context.Window
		c.contextCompaction = cloneUsageCompaction(usage.Context.Compaction)
	}
	if seed == nil && rec.Lead {
		// A rejoined lead's list is its snapshot's, which an earlier daemon
		// may have written without this runner's playbook commands.
		if merged := r.withPlaybookCommands(rec, own.Commands); !slices.Equal(merged, own.Commands) {
			seed = merged
		}
	}
	if len(seed) > 0 {
		c.live.applyQuiet(wslive.Event{Kind: wslive.KindCommands, Commands: seed})
	}
	c.sends, c.drained, c.turnMark, c.voided = rec.Sends, rec.Drained, rec.TurnMark, rec.Voided
	c.heldRes, c.holding = rec.HeldTurn, rec.HeldTurn != nil
	if c.heldRes != nil {
		c.heldRes.mark = rec.TurnMark
	}
	c.steered = slices.Clone(rec.Steered)
	for i := range c.steered {
		c.steered[i].Mark = rec.SteerMarks[c.steered[i].Seq]
	}
	c.steerSeq, c.steerAnswers = rec.SteerSeq, rec.SteerAnswers
	if last := rec.LastTurn; last != nil {
		// Persisted coverage keeps a wait for queued input from taking a
		// prior settlement. Legacy records retain their zero-count behavior.
		c.settled, c.settledReport = last.Result, last.ID
		c.settled.n, c.settled.mark = 1, last.Mark
		if !rec.TurnOpen && (last.Result.Status == TurnFailed || last.Result.Status == TurnInterrupted) {
			c.lastErr = last.Result.Error
		}
	}
	if refusal != nil {
		// A cross-harness worker shows why it does not run,
		// whatever its last turn says (interrupted settles a turn left
		// open with this reason too).
		c.lastErr = refusal.Error()
	}
	c.writeRegistration()
	// A row a terminal login took while no daemon ran: the loop lets the
	// session go before it takes anything else (settle), and nothing below
	// starts for it.
	foreign := c.isForeign()
	if !foreign {
		if r.opts.LiveHost != nil {
			if slot, ok := r.opts.LiveHost.OpenHostedSlot(dir); ok {
				c.live.setSlot(slot)
				if live, ok := slot.(wsdashboard.InProcessLiveSlot); ok {
					live.KeepAliveInProcess()
				}
				if folder, ok := slot.(wsdashboard.ObservedFoldSlot); ok && c.live.observedSeeded {
					folder.FoldsObserved()
				}
			}
		}
		if rec.Lead {
			// Bind after the slot is fixed: summary retries can wake off-loop.
			c.live.summaryStore().SetWake(c.live.wake)
		}
		// A rejoin keeps the recovery file an earlier daemon left until its
		// replay folds that file's evidence (finishAgyNativeReplay): this
		// write, and any before then, reaches the live slot alone.
		c.live.diskHold = rec.HolderID != ""
		c.live.writeSnapshot()
	}
	if rec.HolderID != "" && !foreign {
		c.compactInternalTurn = c.r.internalPrompt(rec)
		if rec.Parent != nil && (rec.TurnOpen || c.compactInternalTurn) {
			c.turn = true
			c.live.applyQuiet(wslive.Event{Kind: wslive.KindTurnStart})
		}
		// Rejoined before the loop runs: what the loop takes first (waiting
		// mail, a message from the page) would otherwise start a second
		// process, and end this one to do it.
		c.reattach()
	}
	r.mu.Lock()
	r.convs[rec.ReplyID] = c
	r.indexChildLocked(rec)
	delete(r.closed, rec.ReplyID)
	delete(r.closedRows, rec.ReplyID)
	if claimWidth != nil {
		claimWidth()
	}
	r.wg.Add(1)
	go c.loop()
	r.mu.Unlock()
	// Do not expose a previous LastTurn while startup still reconciles the
	// later TurnOpen. The loop reconciles before it accepts this request.
	c.do(func() {})
	if (rec.Lead || rec.Parent != nil) && r.opts.Mail != nil && !foreign {
		// Mail that waited while no daemon ran is found by the first check.
		// A spawned child is watched asleep too: mail to its address wakes
		// it.
		c.stopMail = r.opts.Mail.Watch(rec.Root, rec.WSKey, c.signalMail)
		c.signalMail()
	}
	if (len(rec.Queued) > 0 || len(rec.Reports) > 0) && !foreign {
		// Drained input an earlier daemon queued: nothing else would send
		// it before the next message. A rejoin's replay end sends it
		// instead (kick waits for it).
		c.signalInbox()
	}
	return c
}

// requestError is an error a route answers with its status.
