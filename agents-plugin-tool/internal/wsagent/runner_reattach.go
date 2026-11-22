package wsagent

import (
	"encoding/json"
	"fmt"

	"github.com/kang-sw/devenv/internal/wslive"
)

// Reattaching to a process that kept running under its holder: rejoin, replay end, and recovery probes.

// reattach rejoins the process that kept running under the record's holder
// while no daemon ran. It runs before the holder manager's first scan, so
// the holder's whole replay reaches the process; checkReattach follows the
// scan.
func (c *conv) reattach() {
	rec := c.record()
	if err := c.r.admitWorkerRecord(rec); err != nil {
		// Never rejoined. The attach below only lets checkReattach run
		// the ownership match every held worker passes. Replay, events,
		// and the MCP bearer stay closed. A matching holder is then
		// ended (retireCrossHarnessWorker); a mismatched one is never
		// stopped. The record stays, stopped, either way.
		c.crossHarnessRejoin = true
		c.setStatus(func() { c.lastErr, c.wakeRefused = err.Error(), true })
		fmt.Fprintf(c.r.opts.Log, "agent %s: rejoin holder %s refused: %v\n", rec.ReplyID, rec.HolderID, err)
	}
	// A cross-harness rejoin never runs, so it reads no profile (startWorker
	// skips its config reads too).
	def := Profile{Backend: rec.Backend, Lead: rec.Lead}
	if !c.crossHarnessRejoin {
		if c.r.lifecycle(rec.Backend).NativeChildren && rec.ResolvedDefinition != nil {
			def = *rec.ResolvedDefinition
		} else if rp, err := c.r.resolve(rec.Profile, rec.Root); err == nil && rp.Definition.Backend == rec.Backend {
			// Best-effort mapping for a legacy held process is not evidence of
			// what it launched with. Keep its child ceiling unavailable until
			// an actual startWorker launch records the checked definition.
			def = rp.Definition
		}
	}
	var err error
	if rec.Lead {
		err = c.startLead(rec, def, true)
	} else {
		c.replaying = true
		err = c.startWorker(rec, def, true)
	}
	if err != nil {
		// The holder stays in the record: the next start ends it first.
		fmt.Fprintf(c.r.opts.Log, "agent %s: rejoin holder %s: %v\n", rec.ReplyID, rec.HolderID, err)
		if !rec.Lead {
			// A worker keeps replaying until checkReattach reads this after
			// the holder scan and ends the recovery (recoveryFailed drops
			// the open turn). The scan can take long, so the failure leaves
			// keep-awake membership now (wantAwake) instead of waiting for it.
			c.rejoinErr = err.Error()
			c.publishAwake(c.wantAwake())
		} else {
			// No process came back, so no replay will ever settle the turn
			// the record showed open: end the replay and drop that turn, or
			// the keep-awake membership it froze would hold the assertion
			// for good.
			c.replaying = false
			if c.turn {
				c.setStatus(func() { c.turn = false })
				c.live.applyQuiet(wslive.Event{Kind: wslive.KindTurnEnd})
			}
			// No replay will fold the recovery file's evidence: fold it
			// now (rows it showed running end unknown at its write time)
			// and let the file be replaced.
			if !c.finishAgyNativeReplay(false) {
				c.live.writeSnapshot()
			}
			c.publishAwake(c.wantAwake())
		}
	}
	// A process that outlived its checkout's release keeps running, but this
	// daemon gives it no new input there either: startConv settled that
	// before this rejoin.
}

// checkReattach tells a rejoined process where the holder's replay ends,
// once the holder is attached; a holder that is gone ended with its
// process while no daemon ran. Until one of the two is known the
// conversation submits nothing (replaying).
func (c *conv) checkReattach() {
	// Before the first scan an unlisted holder may be one not attached yet;
	// holdersScanned asks again after it.
	if c.replaying && c.proc == nil && c.rejoinErr != "" && c.r.scanDone() {
		// A worker rejoin that failed (reattach) has no process to replay
		// into. End the recovery so the start put off until the scan runs;
		// it ends the held process first (retireHolder).
		reason := "rejoin: " + c.rejoinErr
		c.rejoinErr = ""
		c.recoveryFailed(reason)
		return
	}
	if !c.replaying || c.replayAsked || c.proc == nil || !c.r.scanDone() {
		return
	}
	hp, ok := c.proc.(holderProcess)
	if !ok {
		return
	}
	if c.retiringHolder() {
		// The ownership match passed before the first, failed stop.
		c.retryRetirement(hp, true)
		return
	}
	id, _, _ := hp.HolderInfo()
	listed, attached := false, false
	for _, info := range c.r.opts.Holders.List().Holders {
		if info.ID == id {
			listed, attached = true, info.Attached
			if !attached {
				continue
			}
			if !c.rec.Lead {
				var meta holderSession
				// A cross-harness retirement stops the holder, so a holder already
				// rejected grants it no stop authority either.
				if json.Unmarshal(info.Session, &meta) != nil || !workerHolderMatches(c.record(), meta, info.Child) ||
					c.crossHarnessRejoin && c.r.holders.isRejected(id) {
					// Never stop a holder that failed the ownership check.
					c.r.holders.reject(id)
					hp.Detach()
					c.setStatus(func() { c.proc = nil })
					c.events = nil
					c.update(func(rec *Record) {
						rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
						if !rec.Lead {
							rec.HandedOffAt = ""
						}
					})
					c.recoveryFailed("held process association does not match the worker record")
					c.crossHarnessRetired()
					return
				}
				if c.retireDowngradedWorker(hp, true) {
					c.crossHarnessRetired()
					return
				}
				if c.crossHarnessRejoin {
					// Ownership matched: end the holder instead of rejoining it.
					c.retireCrossHarnessWorker(hp)
					return
				}
				rec := c.record()
				rp, resolveErr := c.r.resolve(rec.Profile, rec.Root)
				if resolveErr != nil {
					fmt.Fprintf(c.r.opts.Log, "agent %s: rejoin permission admission (advisory; applies on next launch): %v\n", c.rec.ReplyID, resolveErr)
				} else if _, _, err := c.r.childAdmission(rec, rp.Definition); err != nil {
					fmt.Fprintf(c.r.opts.Log, "agent %s: rejoin permission admission (advisory; applies on next launch): %v\n", c.rec.ReplyID, err)
				}
				c.r.restoreToken(c.record())
				c.restoreWorkerMCP()
				c.events = c.proc.Events()
				if gated, ok := c.proc.(interface{ allowHolderReplay() }); ok {
					gated.allowHolderReplay()
				}
			}
		}
	}
	if !listed {
		c.processGone()
		c.crossHarnessRetired()
		return
	}
	if !attached {
		return
	}
	// The holder answers on the connection its replayed records arrive on,
	// one after the other, so the answer may wait behind records this loop
	// has to take first: the question is asked off the loop.
	c.replayAsked = true
	go func() {
		st, err := c.r.opts.Holders.Status(id)
		if err != nil {
			// The next beat asks again.
			c.do(func() { c.replayAsked = false })
			return
		}
		hp.ReplayUntil(st.FirstSeq, st.NextSeq-1)
	}()
}

// replayDone ends a rejoin: the state the holder's records rebuilt is
// written, and the conversation takes input again. A replay that lost
// records (lost: the ring dropped records, even ones an earlier daemon
// applied; or a gap during it) and showed no turn boundary after them
// cannot tell whether a turn runs: the turn is taken as running, and
// nothing is submitted until the harness ends one (wrapHold), since a
// message sent into a running turn may be refused.
func (c *conv) replayDone(lost bool) {
	lost = lost || c.replayGap
	boundary := c.replayBoundary
	c.replaying, c.replayAsked, c.replayGap, c.replayBoundary = false, false, false, false
	// Agy native rows end their observation here, whatever the turn state
	// and before a worker's lost-turn probe returns early.
	c.finishAgyNativeReplay(false)
	c.queryRecoveryUsage()
	if !c.rec.Lead && c.rec.Parent != nil {
		if lost && c.rec.TurnOpen {
			c.wrapHold = true
			c.setStatus(func() { c.turn = true })
			c.probeWorkerRecovery()
			return
		}
		if c.rec.TurnOpen && !c.turn {
			status := TurnDone
			if c.replayTurnError != "" {
				status = TurnFailed
			}
			c.settleTurn(status, c.replayTurnError, c.turnMark)
		}
		c.unsteer()
		c.releaseHeld()
	}
	if lost {
		if boundary {
			fmt.Fprintf(c.r.opts.Log, "agent %s: records of holder %s left its ring before this daemon replayed them; the turn state is the one the records after them show\n", c.rec.ReplyID, c.rec.HolderID)
		} else {
			fmt.Fprintf(c.r.opts.Log, "agent %s: records of holder %s left its ring before this daemon replayed them, with no turn boundary after them; the turn is taken as running and nothing is submitted until the harness ends one\n", c.rec.ReplyID, c.rec.HolderID)
			c.wrapHold = true
			c.beginTurn(false)
			c.probeWrapHold()
		}
	}
	// A replay gap may have hidden an in-flight tool or approval.
	if !lost && (c.rec.Parent == nil || c.rec.TurnOpen) {
		c.armModelWait()
	}
	c.reconcileLeadPermission()
	// A rejoined cutover worker's process keeps the mode it ran in.
	c.holdWorkerCutoverMode(c.activeMode)
	c.rejectCutoverPending()
	c.allowAgyBypassPending()
	c.checkAction(false)
	c.live.writeSnapshot()
	c.r.notify(c.rec.ReplyID, c.rec.Root)
	// A message steered during the rejoin goes to the turn that still runs.
	c.steer()
	// A process whose ws-mcp route this daemon does not serve restarts
	// before it takes input; any other restart waits for its usual idle
	// point, as before a rejoin. A failed restart leaves no process: what
	// is held then starts one, as with no process at all.
	if c.mcpStale && c.restartIfDue() {
		return
	}
	if !c.turn {
		c.applySettings()
		c.kick()
		if !c.turn {
			c.armIdle()
		}
	}
}

// recoveryFailed settles once, ends the unrecoverable transport, and never
// launches another process on the strength of a failed probe.
func (c *conv) recoveryFailed(reason string) {
	text := "Worker recovery failed: " + reason
	c.setStatus(func() { c.turn = false; c.lastErr = text })
	if c.rec.TurnOpen {
		c.settleTurn(TurnFailed, text, c.turnMark)
	}
	hadProc := c.proc != nil
	c.stopProcess("")
	if !hadProc {
		// A failed rejoin has no process, so stopProcess returned before
		// processEnded; the state restored for the held process (an
		// internal compaction turn, steered reports) still needs that
		// end-of-process cleanup, or it discards or blocks the next turn.
		c.processEnded()
	}
	c.r.revokeToken(c.rec.ReplyID)
	if c.rec.Capability != "" || c.rec.MCPToken != "" {
		c.update(func(rec *Record) { rec.Capability = ""; rec.forgetMCP() })
	}
	c.replaying, c.wrapHold = false, false
}

func (c *conv) probeWorkerRecovery() {
	p := controlsFor(c.proc).recovery
	if p == nil {
		c.recoveryFailed("the held process cannot reconcile RPC messages")
		return
	}
	proc, startedAt := c.proc, c.rec.TurnStartedAt
	c.wrapTurnSeen = false
	go func() {
		running, err := p.TurnRunning()
		message, turnErr := "", ""
		var taken []string
		if err == nil && !running {
			message, turnErr, taken, err = p.recoveryReply(startedAt)
		}
		c.do(func() {
			if c.proc != proc || !c.wrapHold || c.wrapTurnSeen {
				return
			}
			if err != nil {
				c.recoveryFailed(err.Error())
				return
			}
			if running {
				return
			}
			for _, text := range taken {
				c.steerTaken(text)
			}
			// The idle RPC state and saved messages replace acknowledgements
			// lost from the bounded ring; unsteer clears any untaken queue.
			c.steerAnswers = c.steerSeq
			c.update(func(rec *Record) { rec.SteerAnswers = c.steerAnswers })
			c.reply = message
			c.wrapHold = false
			c.endTurn(turnErr, false)
		})
	}()
}

// probeWrapHold asks a harness that can tell (turnProber) whether a turn
// runs, so a rejoin's hold does not wait for a turn end an idle harness
// never sends. The ask runs off the loop, whose events it may wait behind;
// its answer comes back to the loop (wrapProbed).
func (c *conv) probeWrapHold() {
	tp := controlsFor(c.proc).turns
	if tp == nil {
		return
	}
	proc := c.proc
	c.wrapTurnSeen = false
	go func() {
		running, err := tp.TurnRunning()
		c.do(func() { c.wrapProbed(proc, running, err) })
	}()
}

// wrapProbed takes the harness's answer to probeWrapHold. An idle answer
// ends the held turn as the harness's own turn end would, submitting what
// waited; unless the hold ended already, the process is another, or a turn
// started since the ask (its start may follow the answer on the stream,
// and its end releases the hold). A running answer, or an ask that failed,
// keeps the hold until the harness ends a turn.
func (c *conv) wrapProbed(proc Process, running bool, err error) {
	if c.closed || proc != c.proc || !c.wrapHold {
		return
	}
	switch {
	case err != nil:
		fmt.Fprintf(c.r.opts.Log, "agent %s: ask the harness whether a turn runs: %v; the turn stays taken as running until the harness ends one\n", c.rec.ReplyID, err)
	case running:
		fmt.Fprintf(c.r.opts.Log, "agent %s: the harness reports a turn running; input waits for its end\n", c.rec.ReplyID)
	case c.wrapTurnSeen:
		fmt.Fprintf(c.r.opts.Log, "agent %s: the harness reports no turn running, but one started since; input waits for its end\n", c.rec.ReplyID)
	default:
		fmt.Fprintf(c.r.opts.Log, "agent %s: the harness reports no turn running; the held turn ends\n", c.rec.ReplyID)
		c.endTurn("", false)
	}
}

// lineEnd records that every event of holder record seq is applied. The
// sequence reaches the record at once when the line wrote a conversation
// entry, so a replay after a daemon restart does not write it again.
func (c *conv) lineEnd(seq uint64) {
	if seq <= c.seq {
		return
	}
	c.seq, c.seqDirty = seq, true
	if c.live.ringDirty {
		c.persistSeq()
	}
}

func (c *conv) persistSeq() {
	if !c.seqDirty {
		return
	}
	c.seqDirty, c.live.ringDirty = false, false
	c.update(func(rec *Record) { rec.HolderSeq = c.seq })
}
