package wsagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"time"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

func (c *conv) handle(ev Event) {
	if c.r.durableProcess(c.rec.Backend) && (ev.TurnID != "" || ev.TurnStart || ev.TurnEnd) {
		if c.rec.DurableTurn == nil || c.rec.DurableTurn.ID != ev.TurnID {
			return
		}
	}
	quiet := ev.Replay
	c.eventSeq = ev.Seq
	if ev.Seq > 0 && !ev.Replay {
		c.lineOpen = ev.Seq
		if ev.LineEnd {
			c.lineOpen = 0
		}
	}
	if !quiet && !c.replaying && (ev.TurnStart || ev.Live != nil || ev.ToolBoundary) {
		c.notifyCompanionActivity()
	}
	switch {
	case ev.LineEnd:
		c.lineEnd(ev.Seq)
	case ev.ReplayDone:
		c.replayDone(ev.ReplayLost)
	case ev.acpReplay != nil:
		if c.replaying {
			c.modelWaitPending = ev.acpReplay.pendingTools
			if ev.acpReplay.promptOpen {
				// The prompt can still be waiting for its first content.
				c.beginTurn(true)
			}
		}
	case ev.ReplayGap:
		c.summaryGap = true
		if c.replaying {
			c.replayGap, c.replayBoundary = true, false
		}
	case ev.SessionID != "":
		if ev.SessionID != c.rec.SessionID {
			c.update(func(rec *Record) { rec.SessionID = ev.SessionID })
		}
	case ev.ReasoningSummary != nil:
		c.summaryProjection(ev, quiet)
	case ev.Harness != nil:
		c.harnessReported(*ev.Harness, quiet)
	case ev.Live != nil:
		if !quiet && ev.compactCommandDone {
			c.compactCommand = false
			c.stopCompactWatchdog()
		}
		if ev.Live.Kind == wslive.KindCommands {
			// The list is offered without the commands of unsupported items
			// and with the lead's playbook commands; the dashboard runs only
			// a command the list holds.
			live := *ev.Live
			c.commands = slices.Clone(live.Commands)
			live.Commands = c.r.withPlaybookCommands(c.record(), allowedCommands(live.Commands, c.caps))
			ev.Live = &live
		}
		if !quiet && ev.Live.Kind == wslive.KindCompact && ev.Live.Phase == wslive.CompactStart {
			// The requested compaction started: its own turn holds kick
			// until it ends.
			c.holdForLever(false)
		}
		if ev.Live.Kind == wslive.KindReply && ev.Live.Final && !(quiet && c.rec.Parent != nil && ev.Seq <= c.rec.TurnStartSeq) {
			c.reply = ev.Live.Text
		}
		if quiet && c.replaying && ev.Live.Kind == wslive.KindCompact {
			c.replayCompactionSeq(*ev.Live, ev.Seq)
		} else if !quiet {
			c.compactEvent(*ev.Live)
		}
		if ev.LiveCoalescesTurnEnd {
			c.applyCoalesced(*ev.Live, quiet)
		} else {
			c.apply(*ev.Live, quiet)
		}
		if !quiet && ev.Live.Kind == wslive.KindStatus {
			c.checkUsageDropCompaction(*ev.Live)
			if !ev.FinalUsage && !ev.LiveCoalescesTurnEnd {
				c.steerCompaction(false)
			}
			// Pi can report usage after settlement. A released binding gets
			// no compaction turn or nudge.
			if c.canKick() && c.released() == "" && !c.compactIdle() && c.compactionNudgeReady() {
				c.kickInput()
			}
		}
		if !quiet && ev.ToolBoundary {
			c.escalateHardOverflow()
			c.steerCompaction(true)
		}
		if !quiet && ev.Live.Kind == wslive.KindCompact && ev.Live.Phase != wslive.CompactStart {
			if !c.compactCommand {
				c.stopCompactWatchdog()
			}
			c.kick()
			c.releaseHeld()
			if !c.turn && !c.compacting && !c.compactCommand {
				c.armIdle()
			}
		}
	case ev.Card != nil:
		if !quiet {
			c.live.card(*ev.Card)
		}
	case ev.UI != nil:
		if !quiet {
			c.live.ui(*ev.UI)
		}
	case ev.TrackerWrite:
		if f := c.r.opts.TrackerChanged; f != nil && !quiet {
			f()
		}
	case ev.Fatal != nil:
		if quiet {
			return
		}
		if c.replaying && c.rec.Parent != nil {
			c.processExited()
			return
		}
		c.setStatus(func() { c.lastErr = ev.Fatal.Error() })
		fmt.Fprintf(c.r.opts.Log, "agent %s: %v\n", c.rec.ReplyID, ev.Fatal)
		c.stopProcess("")
		if !c.closed && !c.quitting && c.rec.CompanionRole != "" && c.r.opts.OnCompanionClose != nil {
			c.r.opts.OnCompanionClose(c.rec.CompanionRole, c.rec.ReplyID, c.rec.WSKey, "crash")
		}
	case ev.TurnStart:
		c.summaryStart(ev)
		if quiet && c.rec.Parent != nil && ev.Seq <= c.rec.TurnStartSeq {
			return
		}
		c.replayBoundary = c.replayBoundary || c.replaying
		c.wrapTurnSeen = c.wrapTurnSeen || (c.wrapHold && !quiet)
		c.beginTurn(quiet)
	case ev.TurnEnd:
		if quiet && c.rec.Parent != nil && ev.Seq <= c.rec.TurnStartSeq {
			return
		}
		c.replayBoundary = c.replayBoundary || c.replaying
		if !quiet {
			if c.interceptModelWaitTurnEnd(ev) {
				return
			}
			if override := c.modelWaitTurnError(ev); override != "" {
				ev.TurnError = override
			}
			c.disarmModelWait()
			// Every turn end lifts an earlier hold; a lever turn arms anew.
			c.holdForLever(ev.CompactRequested && !c.interruptAsked)
			c.interruptAsked = false
		}
		if ev.ModelFailure != "" {
			ev.TurnError = agyTerminalFailureError(ev.ModelFailure, ev.TurnError)
			ev.TurnReason = "error"
		}
		c.summaryReason = ev.TurnReason
		c.live.summaryNaturalEnd = true
		c.endTurn(ev.TurnError, quiet)
		c.live.summaryNaturalEnd = false
		c.live.summarySource, c.summaryReason, c.summaryGap = "", "", false
		if !quiet && !c.replaying && c.slot2aVerifyPending {
			c.slot2aVerifyPending = false
			turnErrVal := ev.TurnErrorVal
			if turnErrVal == nil && ev.TurnError != "" {
				turnErrVal = errors.New(ev.TurnError)
			}
			if turnErrVal != nil && !c.slot2aFallbackRetried && c.isSlot2aTurnRejection(turnErrVal) {
				c.slot2aFallbackRetried = true
				if err := c.fallbackSlot2aTo2b(turnErrVal); err != nil {
					fmt.Fprintf(c.r.opts.Log, "agent %s: slot 2b fallback after turn rejection failed: %v\n", c.rec.ReplyID, err)
				}
			} else {
				c.slot2aFallbackRetried = false
			}
		}
	case ev.ModelActivity != nil:
		if !quiet {
			c.onModelActivity(*ev.ModelActivity, ev.ModelContent)
		}
	case ev.ModelFailureUpdate != nil:
		if !quiet {
			c.onModelFailure(*ev.ModelFailureUpdate)
		}
	case ev.Approval != nil:
		c.addApproval(*ev.Approval, quiet)
	case ev.ApprovalDone != "":
		c.dropApproval(ev.ApprovalDone, quiet)
		c.forgetAnswered(ev.ApprovalDone)
	case ev.PermissionMode != "":
		if ev.PermissionMode != c.activeMode {
			c.setStatus(func() { c.activeMode = ev.PermissionMode })
			rec := c.record()
			if rec.Lead && !quiet {
				if level, _, err := c.r.leadPermission(rec, c.dir); err == nil {
					c.permissionNudge(level, false, ev.PermissionMode)
				}
			}
			c.changed(quiet)
		}
		// The mode the harness runs in is the session's own: its next
		// launch resumes in it. A change of the user's that waits to be
		// applied stands until it is. Under agy bypass the launch mode is
		// native default whatever agy reports, and bypass holds only with
		// that mode on record (agyBypass), so a report leaves it. A flag
		// left stale by an older writer goes, so that a report of
		// native default does not revive it without the user.
		rec := c.record()
		// Codex reports its effective defaults even when the launch omitted a
		// permission choice. Keep that report in activeMode for projection, but
		// do not turn an observation into a persisted next-launch override.
		omittedMode := c.r.permissionPolicy(rec.Backend).LiveModeProjection && rec.PermissionMode == ""
		if !rec.Lead && c.cutoverSupport() != nil {
			if !quiet && !c.replaying {
				c.holdWorkerCutoverMode(ev.PermissionMode)
			}
			break
		}
		if mode := c.cutoverLaunchMode(); mode != "" {
			// Under the cutover the process runs in its launch mode only:
			// a reported move is undone, and the saved preference stays.
			if !quiet && ev.PermissionMode != mode && !c.dirtyMode {
				c.dirtyMode, c.reconcileMode = true, true
				if !c.turn {
					c.applySettings()
				}
			}
			break
		}
		if rec.Lead && !quiet && !c.dirtyMode && !rec.agyBypass() && !omittedMode && ev.PermissionMode != rec.PermissionMode && slices.Contains(permissionModes(rec.Backend, []map[string]BackendRegistration{c.r.opts.Backends}), ev.PermissionMode) {
			c.update(func(rec *Record) { rec.PermissionMode, rec.AgyBypass = ev.PermissionMode, false })
			c.changed(quiet)
		}
	case ev.HarnessState != "":
		c.harnessState = ev.HarnessState
		c.checkAction(quiet)
	case ev.SteerAnswer != nil:
		if ev.Seq > 0 && ev.Seq <= c.rec.SteerAnswerSeq {
			return
		}
		c.steerAnswered(ev.SteerAnswer.Refusal)
		c.update(func(rec *Record) { rec.SteerAnswers, rec.SteerAnswerSeq = c.steerAnswers, ev.Seq })
	case ev.compactSteerReply != nil:
		if !quiet {
			c.compactSteerAnswered(*ev.compactSteerReply)
		}
	case ev.durableSteerID != "":
		c.acceptDurableSteer(ev.durableSteerID)
	case ev.UserTaken != "":
		c.steerTaken(ev.UserTaken)
	case ev.CommandError != "":
		settledCompact := false
		if !quiet && c.compactCommand {
			c.compactCommand = false
			c.stopCompactWatchdog()
			settledCompact = true
		}
		if settledCompact {
			c.publishCompaction(false)
		}
		if !quiet && c.compacting {
			c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
			c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed, Text: ev.CommandError})
			c.stopCompactWatchdog()
			settledCompact = true
		}
		// Replay rebuilds lifecycle state, not transient command diagnostics.
		if !quiet {
			c.setStatus(func() { c.lastErr = ev.CommandError })
			c.changed(false)
			if settledCompact {
				c.kick()
				c.releaseHeld()
				if !c.turn && !c.compacting && !c.compactCommand {
					c.armIdle()
				}
			}
		}
	case ev.Notice != "":
		if !quiet {
			fmt.Fprintf(c.r.opts.Log, "agent %s: %s\n", c.rec.ReplyID, ev.Notice)
		}
	case ev.UnrequestedCommand != "":
		c.unrequestedCommand(ev.UnrequestedCommand, quiet)
	case ev.UnrequestedSubagent != "":
		c.unrequestedSubagent(ev.UnrequestedSubagent, quiet)
	case ev.BackgroundTasks != nil:
		if n := *ev.BackgroundTasks; n != c.bgTasks || !sameCount(c.background, ev.Background) || !sameCount(c.monitors, ev.Monitors) {
			prev := c.bgTasks
			c.setStatus(func() {
				c.bgTasks = n
				c.background, c.monitors = copyCount(ev.Background), copyCount(ev.Monitors)
			})
			c.syncHost(quiet)
			c.changed(quiet)
			if !quiet {
				if n > 0 {
					c.stopBgGrace()
				} else if n == 0 && !c.turn {
					if prev > 0 && c.heldRes != nil {
						c.armBgGrace()
					}
					if c.bgGraceTimer == nil {
						c.armIdle()
						c.restartIfDue()
						c.kickCompactionNudge()
					}
				}
				c.refreshShellHold()
				c.watched()
			}
		}
	case ev.Alert != nil:
		c.alert(*ev.Alert, ev.AlertManualOnly, quiet)
	case ev.Retry != nil || ev.RetryEnd:
		c.retry = ev.Retry
		c.syncHost(quiet)
	case ev.MCPServers != nil:
		c.mcpReported(ev.MCPServers, quiet)
	case ev.MCPCheck:
		if !quiet {
			c.checkMCP(nil)
		}
	}
}

// changed tells the daemon a lead's state outside its live snapshot moved
// (pending approvals, the reported mode, the background task count).
func (c *conv) changed(quiet bool) {
	if !quiet {
		c.r.notify(c.rec.ReplyID, c.rec.Root)
	}
}

// bgResumeGrace is how long a worker holds its completed-turn report after its
// last background task finishes, waiting for the harness's self-resumed wakeup
// turn before settling and reporting the earlier turn.
var bgResumeGrace = 6 * time.Second

func (c *conv) armBgGrace() {
	c.stopBgGrace()
	c.bgGraceTimer = time.NewTimer(bgResumeGrace)
}

func (c *conv) stopBgGrace() {
	if c.bgGraceTimer != nil {
		c.bgGraceTimer.Stop()
		c.bgGraceTimer = nil
	}
}

// beginTurn projects a harness-initiated turn or a standalone prompt after
// its send succeeds. Ordinary input turns publish their start in kickInput.
func (c *conv) beginTurn(quiet bool) {
	if c.turn {
		return
	}
	c.stopIdle()
	c.stopBgGrace()
	c.setStatus(func() {
		c.turn = true
		if !quiet {
			c.lastErr = ""
		}
	})
	c.apply(wslive.Event{Kind: wslive.KindTurnStart}, quiet)
	c.reply, c.replayTurnError = "", ""
	c.snapshotBoardRevision()
	if !quiet {
		// There is no newly drained input, so the turn keeps the last mark.
		if !c.compactCommand && !c.compactInternalTurn {
			c.hold(nil)
			if c.rec.Parent != nil && (!c.rec.TurnOpen || c.rec.HeldTurn != nil || c.rec.TurnStartedAt == 0) {
				c.update(func(rec *Record) {
					if !rec.TurnOpen || rec.TurnStartedAt == 0 {
						rec.TurnStartSeq = c.seq
					}
					rec.TurnOpen, rec.HeldTurn = true, nil
				})
			}
		}
		c.watched()
	}
}

// endTurn closes the running turn, applies settings changed during it, and
// submits the messages held meanwhile.
func (c *conv) endTurn(turnErr string, quiet bool) {
	// The cutover's instruction goes once the turn is over: a steer into the
	// running turn would replace it.
	defer c.sendCutoverInstruction()
	defer c.publishCompaction(false)
	compactCommand, wasCompacting := c.compactCommand, c.compacting
	if !quiet {
		c.compactCommand = false
		c.stopCompactWatchdog()
	}
	if !quiet && c.compacting {
		p := c.r.opts.Backends[c.rec.Backend].CompactionProtocol()
		if turnErr == "" && c.summaryReason != wslive.TurnAborted && p.CommandEndConfirms && (compactCommand || c.turn) {
			c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd})
			c.recordInferredCompaction(wslive.TriggerManual, "command_end", 0, 0)
			c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactEnd, Trigger: wslive.TriggerManual})
		} else {
			c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
			c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed, Text: turnErr})
		}
	}
	if !quiet {
		// The runner accepted the logical turn's end (a watchdog continue
		// returned before reaching here), so agy native rows stop being
		// observed; settling them before the turn end's forced snapshot
		// puts them on disk in that write. A replayed (quiet) end never
		// settles them, and a live end during a replay only notes them:
		// replayDone settles them, once.
		c.endAgyNativeObservation(c.r.opts.Now().UnixMilli())
	}
	if c.wrapHold && !c.rec.Lead && c.rec.Parent != nil && !quiet {
		// A gap while streaming is reconciled at agent_settled, not left
		// dependent on acknowledgements that may have left the ring.
		c.probeWorkerRecovery()
		return
	}
	if !c.turn && !c.rec.Lead && !compactCommand && !wasCompacting {
		return
	}
	ran := c.turn
	if c.turn {
		c.setStatus(func() { c.turn = false })
		c.apply(wslive.Event{Kind: wslive.KindTurnEnd, Reason: c.summaryReason}, quiet)
	}
	c.turnSettled(turnErr, quiet, compactCommand)
	internalPrompt := c.clearAgyInternalPrompt()
	if c.replaying && c.rec.Parent != nil {
		c.replayTurnError = turnErr
	}
	if quiet {
		if internalPrompt {
			c.compactInternalTurn = false
		}
		return
	}
	if ran && !compactCommand && !c.replaying {
		c.clearCompactionSteerReplies()
		// A daemon hard-cap interrupt aborts the turn yet still resumes it,
		// unless the per-turn auto-rotation cap fired (hardAbort).
		hardInt := c.compactHardInterrupt
		hardAbort := c.compactHardAbort
		c.compactHardInterrupt = false
		c.compactHardAbort = false
		if hardAbort {
			if turnErr == "" {
				turnErr = fmt.Sprintf("compaction rotation cap reached (%d automatic compactions in one turn); stopped to prevent compaction loop", compactTurnRotationCap)
			}
			c.compactContinueAtIdle = false
			c.compactIdleInternal = false
			c.compactTurnRotations = 0
		} else {
			c.compactContinueAtIdle = turnErr == "" && (c.summaryReason != wslive.TurnAborted || hardInt)
			c.compactIdleInternal = c.compactInternalTurn
			if c.compactStage1Active {
				resume, internal, auto, usageLimited := c.compactStage1Resume, c.compactStage1Internal, c.compactStage1Auto, c.compactStage1UsageLimited
				c.compactStage1Active, c.compactStage1Resume, c.compactStage1Internal, c.compactStage1Auto, c.compactStage1UsageLimited = false, false, false, false, false
				if c.compactPending != nil {
					// Lead queued ws/compact during Stage 1; kick() below runs it.
				} else if usageLimited {
					// Quota exhausted during Stage 1: suppress immediate Stage 2
					// native /compact so quota continuation can recover cleanly.
				} else if c.proc != nil {
					c.compactPending = &compactRequest{resume: resume, internal: internal, auto: auto, crossing: auto, triggerPath: "stage1_fallback"}
					c.publishCompaction(false)
				}
			} else {
				c.compactHardForce = hardInt
				c.stageHardCompaction(c.compactContinueAtIdle, c.compactIdleInternal)
				c.compactHardForce = false
			}
		}
		if !c.compacting && !c.compactCommand && c.compactPending == nil && !c.compactResume && !c.compactStage1Active {
			c.compactTurnRotations = 0
		}
	}
	// Observers must see the terminal diagnostic in the same emission as
	// the settled lifecycle, including mapped Pi agent_settled failures.
	if turnErr != "" {
		c.setStatus(func() { c.lastErr = turnErr })
	}
	// settleTurn clears the reply; the whiteboard nudge judges it afterwards.
	turnReply := c.reply
	if c.compactInternalTurn {
		c.compactInternalTurn = false
		c.reply = ""
		c.watched()
	} else if ran && !c.replaying && !compactCommand && (c.rec.Parent == nil || c.rec.TurnOpen) {
		// A replayed internal turn can be running without a reportable
		// child turn open in the record.
		status := TurnDone
		if turnErr != "" {
			status = TurnFailed
		}
		if err := c.settleTurn(status, turnErr, c.turnMark); err != nil {
			c.setStatus(func() { c.lastErr = "save turn settlement: " + err.Error() })
			c.live.writeSnapshot()
			c.r.notify(c.rec.ReplyID, c.rec.Root)
			c.retryDurableTurn()
			return
		}
		if !wasCompacting {
			c.stageWhiteboardNudge(turnErr, turnReply)
		}
		if c.r.lifecycle(c.rec.Backend).TurnActivity && c.r.opts.AgyActivity != nil {
			c.r.opts.AgyActivity()
		}
	}
	if c.replaying {
		return
	}
	if c.rec.Lead {
		// Track hosted lead activity in memory only; persists when agent.json
		// is already written (never adds a per-turn disk write).
		c.setStatus(func() { c.rec.LastActiveAt = c.r.opts.Now().UTC().Format(time.RFC3339) })
	}
	c.wrapHold = false
	// A child's report steered into the turn that the turn did not take
	// goes as the next one (children.go).
	c.unsteer()
	c.applySettings()
	compactionFirst := c.compactPending != nil || c.compactResume
	c.restartIfDue()
	c.kick()
	if compactionFirst {
		c.restartIfDue()
	}
	c.releaseHeld()
	c.flushRecord()
	if !c.turn && !c.compacting && !c.compactCommand {
		c.armIdle()
	}
}

// addApproval records a waiting tool permission prompt; the session shows
// as awaiting approval while any waits.
func (c *conv) addApproval(a ApprovalRequest, quiet bool) {
	for _, have := range c.approvals {
		if have.ID == a.ID {
			return
		}
	}
	if slices.Contains(c.rec.Answered, a.ID) {
		// A replay of a prompt an earlier daemon already answered. A refused
		// cutover request still owes its turn the instruction, which
		// admission keeps from being sent twice.
		if c.cutoverRejects(a.Tool) {
			c.oweCutoverInstruction(a.ID)
			if c.replaying {
				// The answer is recorded before it is written, so a crash
				// between the two leaves the process waiting on it. A second
				// reject_once is harmless (the process drops an answer to a
				// request it no longer waits on): the rejoin's end sends it
				// again (rejectCutoverPending).
				c.setStatus(func() { c.approvals = append(c.approvals, a) })
			}
		}
		return
	}
	if !quiet && !c.replaying && c.record().CompanionRole != "" {
		// A hidden companion session has no page to answer a prompt: the
		// daemon allows or refuses it now, and it is never parked (see
		// companion_permission.go). A replay or a quiet event parks it for
		// the rejoin's end (settleCompanionPending).
		if !c.settleCompanionApproval(a) {
			// The process still waits on a request nobody can answer and
			// nothing is parked for the watchdogs: stop it so the companion
			// restarts instead of waiting forever.
			c.approvalFailed(errNoApprovalAnswer)
		}
		return
	}
	if !quiet && !c.replaying && c.rejectCutoverRequest(a) {
		// Refused before it is published, ahead of bypass and worker
		// auto-approval: nobody decides it.
		return
	}
	if rec := c.record(); !quiet && !c.replaying && !c.cutoverRejects(a.Tool) && (rec.agyBypass() || c.cutoverAutoApproves(rec, a)) {
		// Bypass answers before the prompt is published: the page must not
		// see a modal, a pending row or a wake for a request nobody decides.
		// Without an answerer, or with no answer the process could build
		// (nothing written), the prompt falls through to the user.
		if answerer := controlsFor(c.proc).approval; answerer != nil {
			err := c.sendApproval(answerer, a, true, "")
			if err == nil {
				return
			}
			fmt.Fprintf(c.r.opts.Log, "agent %s: auto-allow permission: %v\n", c.rec.ReplyID, err)
			if !errors.Is(err, errNoApprovalAnswer) {
				return
			}
		}
	}
	c.setStatus(func() { c.approvals = append(c.approvals, a) })
	if !quiet && c.modelWaitArmed {
		c.stopModelWaitTimer()
		c.stopModelFailureDrain()
		if c.modelWaitFailure != "" {
			c.modelWaitFailureExcluded = true
		}
		c.modelWaitFailure = ""
	}
	c.checkAction(quiet)
	c.showApprovals(quiet)
	if !quiet && !c.rec.Lead && c.r.opts.Backends[c.rec.Backend].WorkerAutoApprove && !c.cutoverRejects(a.Tool) {
		// Use the same answer path as a user, including at-most-once
		// tracking and a fatal write failure. The request remains traced.
		if err := c.answerApproval(a.ID, true, ""); err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: auto-allow permission: %v\n", c.rec.ReplyID, err)
		}
	}
}

// actionGrace is how long the harness may report that it waits on the user
// with no prompt for the runner to show before the session says so. The
// state line arrives just before its prompt's own line, and stays for a
// moment after a prompt's answer.
var actionGrace = 2 * time.Second

// actionNeededText is State.ActionNeeded for a wait nobody can answer here.
const actionNeededText = "The harness waits on an answer this page has no prompt for. Interrupt the turn or stop the session to go on."

// actionNeededTool names that wait in the live state's permission wait.
const actionNeededTool = "prompt"

// checkAction follows the harness's wait on the user: while it waits with
// no prompt of the runner's to answer (one this process inherited, or a
// request the runner has no form for), the session must not look like it
// works. The wait shows after actionGrace, and ends with the state, with a
// prompt's arrival, or with the process.
func (c *conv) checkAction(quiet bool) {
	if c.harnessState == HarnessWaiting && len(c.approvals) == 0 && c.proc != nil {
		if c.actionNeeded == "" && c.actionTimer == nil && !quiet {
			c.actionTimer = time.NewTimer(actionGrace)
		}
		return
	}
	if c.actionTimer != nil {
		c.actionTimer.Stop()
		c.actionTimer = nil
	}
	if c.actionNeeded != "" {
		c.setStatus(func() { c.actionNeeded = "" })
		c.showApprovals(quiet)
	}
}

// actionDue shows the wait once its grace has passed.
func (c *conv) actionDue() {
	if c.harnessState != HarnessWaiting || len(c.approvals) > 0 || c.proc == nil || c.actionNeeded != "" {
		return
	}
	fmt.Fprintf(c.r.opts.Log, "agent %s: the harness waits on the user (requires_action) with no permission prompt to answer\n", c.rec.ReplyID)
	c.setStatus(func() { c.actionNeeded = actionNeededText })
	c.showApprovals(false)
}

// dropApproval forgets a prompt that needs no answer any more, and reports
// whether it was waiting.
func (c *conv) dropApproval(id string, quiet bool) bool {
	for i, have := range c.approvals {
		if have.ID == id {
			c.setStatus(func() { c.approvals = append(c.approvals[:i:i], c.approvals[i+1:]...) })
			if !quiet && c.modelWaitArmed && len(c.approvals) == 0 && c.modelWaitPending == 0 {
				c.resetModelWaitTimer()
			}
			c.checkAction(quiet)
			c.showApprovals(quiet)
			return true
		}
	}
	return false
}

// forgetAnswered drops a settled prompt from the record's answered list
// ("" drops them all: their process is gone). One settled prompt changes
// memory only, and the next record write carries it: an id left on disk
// only keeps a replay of its own, already answered request from waiting.
func (c *conv) forgetAnswered(id string) {
	if len(c.rec.Answered) == 0 || (id != "" && !slices.Contains(c.rec.Answered, id)) {
		return
	}
	if id != "" {
		c.setStatus(func() {
			c.rec.Answered = slices.DeleteFunc(slices.Clone(c.rec.Answered), func(have string) bool { return have == id })
		})
		return
	}
	c.update(func(rec *Record) { rec.Answered = nil })
}

// clearApprovals settles every waiting prompt with no answer: the process
// they came from is gone. A daemon-owned exec approval stays: it came from
// an exec call, which its own end withdraws (ApproveExec).
func (c *conv) clearApprovals() {
	c.forgetAnswered("")
	kept := c.execApprovalsOnly()
	if len(c.approvals) == len(kept) {
		return
	}
	c.setStatus(func() { c.approvals = kept })
	c.showApprovals(false)
}

// showApprovals carries the oldest waiting prompt to the live state as its
// permission wait, or a wait with no prompt to answer (actionNeeded), or
// ends the wait when neither is left.
func (c *conv) showApprovals(quiet bool) {
	ev := wslive.Event{Kind: wslive.KindPermission, Clear: true}
	if c.actionNeeded != "" {
		ev = wslive.Event{Kind: wslive.KindPermission, Tool: actionNeededTool}
	}
	if len(c.approvals) > 0 {
		a := c.approvals[0]
		var args any
		if json.Unmarshal(a.Input, &args) != nil {
			args = nil
		}
		ev = wslive.Event{Kind: wslive.KindPermission, Tool: a.Tool, Args: args}
	}
	c.apply(ev, quiet)
	c.changed(quiet)
}

// answerApproval writes the answer to the waiting prompt id. The prompt is
// forgotten before the write, so it is answered at most once.
func (c *conv) answerApproval(id string, allow bool, message string) error {
	conflict := func(msg string) error { return &requestError{status: http.StatusConflict, msg: msg} }
	// A daemon-owned exec approval has no process answer: the waiting exec
	// call takes it (ApproveExec), whatever the process is doing.
	if c.answerExecApproval(id, allow, message) {
		return nil
	}
	if c.replaying {
		return conflict("the session is rejoining its process; answer again in a moment")
	}
	var req *ApprovalRequest
	for i := range c.approvals {
		if c.approvals[i].ID == id {
			a := c.approvals[i]
			req = &a
		}
	}
	answerer := controlsFor(c.proc).approval
	if req == nil || answerer == nil {
		return conflict("that permission prompt is not waiting: it was answered, withdrawn, or its process ended")
	}
	c.dropApproval(id, false)
	if c.cutoverRejects(req.Tool) {
		// A request the cutover refuses stays refused whoever answers it.
		allow = false
		c.oweCutoverInstruction(req.ID)
	}
	err := c.sendApproval(answerer, *req, allow, message)
	if errors.Is(err, errNoApprovalAnswer) {
		// The prompt is gone from the page and the process still waits on
		// it: the process stops, as on a failed write.
		c.approvalFailed(err)
	}
	return err
}

// sendApproval tracks req as answered before writing the answer, so it is
// answered at most once, and stops the process when the write fails. An
// answer the process could not build wrote nothing: the id is untracked and
// the error (errNoApprovalAnswer) left to the caller. Under agy bypass,
// which answers every tool call, the id is tracked in memory only: the next
// record write carries it, at the latest the one of the holder sequence a
// replay skips to (persistSeq, also on shutdown). Otherwise an answer is
// one write per user decision and is durable at once.
func (c *conv) sendApproval(answerer approvalAnswerer, req ApprovalRequest, allow bool, message string) error {
	track := func(rec *Record) {
		if !slices.Contains(rec.Answered, req.ID) {
			rec.Answered = append(rec.Answered, req.ID)
		}
	}
	if rec := c.record(); rec.agyBypass() || rec.CompanionRole != "" || c.cutoverAutoApproves(rec, req) {
		c.setStatus(func() { track(&c.rec) })
	} else {
		c.update(track)
	}
	if err := answerer.AnswerApproval(req, allow, message); err != nil {
		err = fmt.Errorf("answer the permission prompt: %w", err)
		if errors.Is(err, errNoApprovalAnswer) {
			c.forgetAnswered(req.ID)
			return err
		}
		c.approvalFailed(err)
		return err
	}
	return nil
}

// approvalFailed stops the process an answer could not reach.
func (c *conv) approvalFailed(err error) {
	c.setStatus(func() { c.lastErr = err.Error() })
	c.stopProcess("")
}

// processExited handles a process whose output ended without the runner
// stopping it.
func (c *conv) processExited() {
	if c.turn && c.compactCommand {
		c.setStatus(func() { c.turn = false })
		c.apply(wslive.Event{Kind: wslive.KindTurnEnd})
	}
	if c.compacting {
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
		c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
	}
	c.compactPending, c.compactCommand, c.compactResume = nil, false, false
	c.compactStage1Active, c.compactStage1Resume, c.compactStage1Internal, c.compactStage1Auto, c.compactStage1UsageLimited = false, false, false, false, false
	c.compactOrigin = ""
	c.compactTurnRotations = 0
	c.compactHardAbort, c.compactHardStepPending = false, false
	c.compactPostRotationCheck, c.compactPostRotationNoShrink = false, 0
	c.publishCompaction(false)
	c.compactResumeInternal = false
	if !c.quitting {
		c.clearPendingCompactionResume()
	}
	c.compactUsageFresh = false
	c.stopCompactWatchdog()
	rejoining, proc := c.replaying, c.proc
	c.events = nil
	// No compaction follows from a process that is gone.
	c.holdForLever(false)
	c.interruptAsked = false
	if c.replaying && c.rec.Parent != nil {
		// Apply any completed replayed turn before handling an exited child.
		if c.rec.TurnOpen && !c.turn && c.replayBoundary {
			status := TurnDone
			if c.replayTurnError != "" {
				status = TurnFailed
			}
			c.settleTurn(status, c.replayTurnError, c.turnMark)
		} else {
			c.interrupted()
		}
	}
	if proc != nil {
		_ = proc.Close()
		c.releaseHolder(proc)
	}
	c.setStatus(func() {
		c.proc = nil
		if c.lastErr == "" {
			if c.modelWaitFailErr != "" {
				c.lastErr = c.modelWaitFailErr
			} else {
				c.lastErr = "the backend process exited"
			}
		}
	})
	c.processEnded()
	if !c.closed && !c.quitting && c.rec.CompanionRole != "" && c.r.opts.OnCompanionClose != nil {
		c.r.opts.OnCompanionClose(c.rec.CompanionRole, c.rec.ReplyID, c.rec.WSKey, "crash")
	}
	if !c.closed && !c.quitting {
		c.releaseHeld()
	}
	if rejoining {
		// The process ended while no daemon ran: what waited goes to a new
		// one.
		c.replaying, c.replayAsked = false, false
		c.kick()
	}
	c.retryDurableTurn()
}

// processGone handles a rejoin whose holder no longer exists: the process
// ended with it while no daemon ran.
func (c *conv) processGone() {
	proc := c.proc
	c.events = nil
	if hp, ok := proc.(holderProcess); ok {
		hp.Detach()
	}
	c.setStatus(func() { c.proc = nil })
	c.clearWorkerRejoin()
	c.update(func(rec *Record) {
		rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
		if !rec.Lead {
			rec.HandedOffAt = ""
		}
	})
	c.writeRegistration()
	c.replaying, c.replayAsked = false, false
	c.interrupted()
	c.processEnded()
	if !c.crossHarnessRejoin {
		c.kick()
	}
}

// processEnded settles what ends with a process: its turn, its waiting
// prompts, and what it last reported.
func (c *conv) processEnded() {
	failure := c.modelWaitBackoffFailure
	if c.modelWaitCancelling || c.modelWaitFailErr != "" {
		failure = c.modelWaitFailure
	}
	// A process loss after retry exhaustion must publish the same visible
	// diagnostic as TurnEnd before disarming the watchdog. Earlier losses still
	// restore the failed attempt's saved reply when no final reply followed.
	if c.modelWaitFailErr != "" && c.modelWaitRetries >= c.modelWaitMaxRetries {
		c.publishModelWaitExhaustedDiagnostic()
	} else {
		c.restoreModelWaitDiagnostic()
	}
	c.disarmModelWait()
	c.stopIdle()
	hadBgGrace := c.bgGraceTimer != nil
	c.stopBgGrace()
	c.wrapHold = false
	hadTurn := c.turn
	internal := c.compactInternalTurn
	c.compactInternalTurn = false
	c.clearAgyInternalPrompt()
	if c.turn {
		var reason string
		c.setStatus(func() {
			c.turn = false
			if failure != "" {
				c.lastErr = agyTerminalFailureError(failure, c.lastErr)
				reason = "error"
			}
		})
		c.apply(wslive.Event{Kind: wslive.KindTurnEnd, Reason: reason})
		// A turn the process took with it. A close settles none (its
		// parent ended it), and the daemon's exit leaves it to the next
		// daemon (interrupted).
		if !c.closed && !c.quitting && !internal && c.rec.DurableTurn == nil && (c.rec.Parent == nil || c.rec.TurnOpen) {
			c.settleTurn(TurnFailed, c.lastError(), c.turnMark)
		}
		if internal {
			c.reply = ""
			c.watched()
		}
	}
	c.r.revokeToken(c.rec.ReplyID)
	if c.rec.Capability != "" || c.rec.MCPToken != "" {
		c.update(func(rec *Record) { rec.Capability = ""; rec.forgetMCP() })
	}
	if c.requeueSteered() && !c.closed && !c.quitting {
		// The steered reports the process took with it go as the next
		// turn; nothing else would send them.
		c.signalInbox()
	}
	c.clearApprovals()
	c.harnessState = ""
	c.checkAction(false)
	hadBackgroundTasks := c.bgTasks != 0
	c.setStatus(func() {
		c.activeMode, c.bgTasks, c.dashboardControlEffective = "", 0, false
		c.background, c.monitors = nil, nil
	})
	c.clearProcessMCP()
	c.clearWorkerRejoin()
	if hadBackgroundTasks || hadBgGrace || c.heldRes != nil {
		// A child's held background turn settles and its parent's idle
		// timer re-arms once the process exits and clears background work
		// (including when the process exits during bgResumeGrace).
		if !c.closed && !c.quitting {
			c.releaseHeld()
		}
		if hadBackgroundTasks || hadBgGrace {
			c.watched()
		}
	}
	c.retry, c.alerted, c.quotaUsageLimitAlerted, c.quotaUsageLimitHandled = nil, false, false, false
	c.syncHost(true)
	// A replay the process's end cut short still resolves what it staged of
	// agy native rows and what the restored recovery file held (dropping
	// the rows retention had evicted, settling the rows a live turn end
	// accepted during it), before endAgents fails the rest; a path that
	// cleared replaying first still holds the file (diskHold). The fold's
	// single write carries this end's other changes too.
	if len(c.agyNativeReplay.staged) > 0 || c.replaying || c.live.diskHold {
		if !c.finishAgyNativeReplay(true) {
			c.live.writeSnapshot()
		}
	} else {
		c.live.endAgents()
		c.live.writeSnapshot()
	}
	if !c.closed && !c.quitting && !hadTurn && !hadBackgroundTasks && !hadBgGrace {
		c.watched()
	}
	c.r.notify(c.rec.ReplyID, c.rec.Root)
}

func (c *conv) clearAgyInternalPrompt() bool {
	if !c.r.internalPrompt(c.record()) {
		return false
	}
	c.update(func(rec *Record) { rec.TurnStartedAt = 0 })
	return true
}

// releaseHolder forgets the holder of an ended lead process, unless it
// could not be ended: then the record keeps it, and the next start ends it
// before anything runs (retireHolder).
func (c *conv) releaseHolder(proc Process) {
	hp, ok := proc.(holderProcess)
	if !ok || c.rec.HolderID == "" {
		return
	}
	if c.replaying && !c.r.scanDone() {
		// ErrUnknownHolder is not proof of absence before the first scan.
		// Retain the resume/start fence even if another control stops this
		// restored transport while the archived holder is still unscanned.
		return
	}
	if err := hp.StopError(); err != nil {
		c.setStatus(func() { c.lastErr = "end the session's process: " + err.Error() })
		fmt.Fprintf(c.r.opts.Log, "agent %s: stop holder %s: %v\n", c.rec.ReplyID, c.rec.HolderID, err)
		return
	}
	c.seqDirty, c.live.ringDirty = false, false
	c.update(func(rec *Record) {
		rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
		if !rec.Lead {
			rec.HandedOffAt = ""
		}
	})
	c.writeRegistration()
}

// stopProcess ends the process, if any; a running turn ends with it.
func (c *conv) stopProcess(reason string) {
	if c.turn && c.compactCommand {
		c.setStatus(func() { c.turn = false })
		c.apply(wslive.Event{Kind: wslive.KindTurnEnd})
	}
	if c.compacting {
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
		c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactFailed})
	}
	c.compactPending, c.compactCommand, c.compactResume = nil, false, false
	c.compactStage1Active, c.compactStage1Resume, c.compactStage1Internal, c.compactStage1Auto, c.compactStage1UsageLimited = false, false, false, false, false
	c.compactOrigin = ""
	c.compactTurnRotations = 0
	c.compactHardAbort, c.compactHardStepPending = false, false
	c.compactPostRotationCheck, c.compactPostRotationNoShrink = false, 0
	c.publishCompaction(false)
	c.compactResumeInternal = false
	if !c.quitting {
		c.clearPendingCompactionResume()
	}
	c.compactUsageFresh = false
	c.stopCompactWatchdog()
	c.stopIdle()
	if c.retiringHolder() {
		// The retained process is a stream a retirement already detached:
		// closing it would end nothing, and releaseHolder would then forget
		// a holder that still runs. Only the holder's own stop ends it; a
		// failure keeps the holder, the error, and the heartbeat retry.
		// Retained input stays held for the next send.
		if hp, ok := c.proc.(holderProcess); ok {
			c.retryRetirement(hp, false)
		}
		return
	}
	if c.proc == nil {
		if !c.closed && !c.quitting {
			c.releaseHeld()
		}
		return
	}
	proc := c.proc
	c.events = nil
	_ = proc.Close()
	c.setStatus(func() { c.proc = nil })
	c.releaseHolder(proc)
	// Close waits for the holder manager's exit write. Record the runner's
	// intent afterwards: EOF may otherwise look like a plain child exit.
	if reason != "" && c.rec.Lead && c.rec.HostPID == 0 {
		if unlock, ok := c.lockRow(); ok {
			reg, err := wsdashboard.ReadRegistration(c.r.opts.Dir, c.rec.ReplyID)
			if err == nil {
				reg.HostExitReason = reason
				reg.HostStoppedAt = c.r.opts.Now().UTC().Format(time.RFC3339)
				err = wsdashboard.WriteRegistration(c.r.opts.Dir, reg)
			}
			unlock()
			if err != nil {
				fmt.Fprintf(c.r.opts.Log, "agent %s: record stop reason: %v\n", c.rec.ReplyID, err)
			}
		}
	}
	if reason != "" {
		fmt.Fprintf(c.r.opts.Log, "agent %s: process stopped: %s\n", c.rec.ReplyID, reason)
	}
	c.replaying, c.replayAsked = false, false
	c.processEnded()
	if !c.closed && !c.quitting {
		c.releaseHeld()
	}
}

// letGo is the daemon's shutdown: a held process (a lead's, or a spawned
// worker's still at work) runs on under its holder, with the sequence
// applied so far in the record for the next daemon's rejoin; any other
// process ends. A finished-idle spawned child (finishedIdleChild) is put to
// sleep instead: its holder and child end, so no idle worker outlives the
// daemon holding its working directory, and the next daemon neither
// restarts it nor sends it a standalone restore turn. A later wake gets the
// restore bundle with its input. The check runs here, after finishLine and
// persistSeq, where the turn state is the loop's own.
func (c *conv) letGo() {
	c.quitting = true
	c.drainCompaction()
	c.finishLine()
	c.persistSeq()
	if hp, ok := c.proc.(holderProcess); ok {
		if _, _, held := hp.HolderInfo(); held && !c.r.opts.detachFinishedAtShutdown && c.finishedIdleChild() && c.sleepAtShutdown(hp) {
			c.flushRecord()
			return
		}
		if hp.Detach() {
			c.flushRecord()
			return // loop's closeSlot flushes the snapshot before dropping RAM
		}
	}
	c.stopProcess("")
	c.flushRecord()
}

// finishedIdleChild reports a spawned non-lead conversation whose held
// process merely waits for its idle stop: no turn or wrap hold, replay,
// background task, compaction or its continuation, quota continuation,
// model-wait backoff, approval, live exec job, awake child, held retirement,
// and no input, mail, report, held turn or inbox message waiting for it. Such
// a process is put to sleep across a daemon restart rather than kept alive
// or restarted. It runs in the loop.
func (c *conv) finishedIdleChild() bool {
	if c.rec.Lead || c.rec.Parent == nil || c.closed || c.proc == nil {
		return false
	}
	if c.wrapHold || c.retiringHolder() || c.crossHarnessRejoin || len(c.approvals) != 0 || !c.idleNow() {
		return false
	}
	if c.compactPending != nil || c.compactResume || c.compactStage1Active || c.hasQuotaContinuation() || c.modelWaitBackoffC != nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rec := c.rec
	if rec.TurnOpen || rec.HeldTurn != nil || len(rec.Queued) != 0 || len(rec.Mail) != 0 || len(rec.Reports) != 0 {
		return false
	}
	// keepsAwakeLocked without its background count: idleNow above already
	// refused a known running task (bgTasks > 0). Codex reports its count as
	// unknown (-1) for its process's whole life; workingLocked counts that as
	// work, so a parent stays awake, but the idle stop (idleNow) ignores it,
	// and so does this sleep, or no Codex worker would ever sleep here.
	return c.held == 0 && !c.holding && !c.spawnPending &&
		(c.wakeRefused || c.sends <= max(c.settled.mark, c.voided))
}

// sleepFinishedChild stops a finishedIdleChild's process the way its idle
// timer would: stopProcess ends the holder and its child and forgets the
// holder in the record, leaving a wakeable sleeping conversation. A
// restore marker it holds stays in memory, so a wake prepends the bundle
// to its input (compactInput) instead of a standalone restore turn.
func (c *conv) sleepFinishedChild(why string) {
	fmt.Fprintf(c.r.opts.Log, "agent %s: finished child sleeps (%s)\n", c.rec.ReplyID, why)
	c.stopProcess("")
	c.live.writeSnapshot()
	c.r.notify(c.rec.ReplyID, c.rec.Root)
}

// sleepAtShutdown is sleepFinishedChild for the daemon's shutdown (letGo),
// where waiting out each child's exit would spend the successor's lock
// budget (and the holder manager serializes its waiting stops): the stream
// is detached, the holder is asked to stop (it ends its child itself, by
// TERM and then KILL), and the record forgets the holder, its credentials
// and its handed-off fence, leaving a sleeping conversation for the next
// daemon. A refused stop request keeps the plain detach: the record still
// names the holder, so the next daemon rejoins it and puts it to sleep
// after replay (replayDone). It reports whether the process was let go.
func (c *conv) sleepAtShutdown(hp holderProcess) bool {
	id := c.rec.HolderID
	if id == "" || !hp.Detach() {
		return false
	}
	c.events = nil
	if err := c.r.requestHolderStop(id); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: finished child left to its holder %s: %v\n", c.rec.ReplyID, id, err)
		return true
	}
	fmt.Fprintf(c.r.opts.Log, "agent %s: finished child sleeps (daemon shutdown)\n", c.rec.ReplyID)
	c.setStatus(func() { c.proc = nil })
	c.seqDirty, c.live.ringDirty = false, false
	c.update(func(rec *Record) {
		rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
		rec.HandedOffAt = ""
	})
	c.processEnded()
	c.writeRegistration()
	return true
}

// finishLine applies the rest of a holder line the loop stands in the
// middle of, so the sequence the record keeps covers every entry the line
// wrote and the next daemon's replay writes none of them again. The line's
// events are already on their way; a stream that stalls is not waited for.
func (c *conv) finishLine() {
	wait := time.After(time.Second)
	for c.lineOpen != 0 && c.events != nil {
		select {
		case ev, ok := <-c.events:
			if !ok {
				return
			}
			c.handle(ev)
		case <-wait:
			return
		}
	}
}

// idleNow reports whether the idle timer may stop the process. Read child
// state here too: a spawn notification may still be queued when the timer fires.
func (c *conv) idleNow() bool {
	if c.turn || c.replaying || c.bgTasks > 0 || c.bgGraceTimer != nil || c.compacting || c.compactCommand || c.ownsLiveExec() {
		return false
	}
	kind := ParentConversation
	if c.rec.Lead {
		kind = ParentLead
	}
	parent := ParentRef{Kind: kind, ReplyID: c.rec.ReplyID}
	c.r.mu.Lock()
	children := make([]*conv, 0, len(c.r.convs))
	for _, child := range c.r.convs {
		if child != c {
			children = append(children, child)
		}
	}
	c.r.mu.Unlock()
	for _, child := range children {
		child.mu.Lock()
		// Send coverage protects initial queuing, but not input retained
		// for retry after a settled launch failure. A held result still
		// waits for the child's own descendants.
		// The one sleep predicate (keepsAwakeLocked): an awake child keeps
		// its parent awake, a sleeping one (a refused wake included) does not.
		active := child.rec.Parent != nil && *child.rec.Parent == parent && child.keepsAwakeLocked()
		child.mu.Unlock()
		if active {
			return false
		}
	}
	return true
}

// checkInterrupt takes the inbox's interrupt requests and interrupts the
// running turn when there is one; with no turn they are dropped.
func (c *conv) checkInterrupt() {
	recs, err := wsinbox.DrainInterrupt(c.inbox)
	if err != nil || len(recs) == 0 {
		return
	}
	if _, err := c.cancelTurn(); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: interrupt: %v\n", c.rec.ReplyID, err)
		c.stopProcess("")
	}
}

// cancelTurn is the turn-cancellation step: it ends a model-wait backoff, or
// asks the backend to interrupt the running turn. It reports whether a turn
// or backoff was there to cancel, and the backend's interrupt error. What a
// failure leads to is the caller's: the page's interrupt stops the process,
// a forced worktree release only reports it. It runs in the loop.
func (c *conv) cancelTurn() (bool, error) {
	if c.hasQuotaContinuation() {
		c.cancelQuotaContinuation(false)
	}
	if c.modelWaitBackoffC != nil {
		c.cancelModelWaitBackoff()
		return true, nil
	}
	if !c.turn || c.proc == nil {
		return false, nil
	}
	c.interruptAsked = true
	c.disarmModelWait()
	return true, c.proc.Interrupt()
}

// leverHoldCap bounds how long queued input waits, after a turn that
// carried a successful compaction lever call, for that compaction to start.
const leverHoldCap = 5 * time.Second

// holdForLever lifts the lever hold, ending its cap timer, and with arm
// starts it anew. Lifting submits nothing itself: the caller's own kick
// (a turn end's) or the compaction turn's end does.
func (c *conv) holdForLever(arm bool) {
	if c.leverStop != nil {
		c.leverStop()
	}
	c.leverC, c.leverStop = nil, nil
	if arm {
		c.leverC, c.leverStop = c.r.opts.Timer(leverHoldCap)
	}
}

func (c *conv) checkModelSetting(model *string) error {
	if model != nil && c.rec.Lead {
		if provider, ok := c.r.opts.Backends[c.rec.Backend].Backend.(leadProviderAdmitter); ok {
			if err := provider.admitLeadProvider(c.rec.Provider, *model); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *conv) setDashboardControlAccess(enabled bool) error {
	if !c.rec.Lead {
		return badRequest("dashboard_control_access applies to a lead session only")
	}
	if c.r.isChildRecord(c.record()) {
		return &requestError{status: http.StatusForbidden, msg: "child session settings are fixed at spawn"}
	}
	if c.record().DashboardControlAccess == enabled {
		return nil
	}
	if err := c.update(func(rec *Record) {
		rec.DashboardControlAccess = enabled
	}); err != nil {
		return err
	}
	c.changed(false)
	return nil
}

func (c *conv) setSettings(model, effort *string) {
	c.update(func(rec *Record) {
		if model != nil && *model != rec.Model {
			rec.Model = *model
			c.dirtyModel = true
		}
		if effort != nil && *effort != rec.Effort {
			rec.Effort = *effort
			c.dirtyEffort = true
		}
	})
	if !c.turn {
		c.applySettings()
	}
}

// setConvHistoryAccess persists the lead's conv_history_access flag without
// stopping or restarting a running process (decision 1640).
func (c *conv) setConvHistoryAccess(enabled bool) error {
	if !c.record().Lead {
		return &requestError{status: http.StatusForbidden, msg: "conv_history_access applies to lead sessions only"}
	}
	changed := false
	if err := c.update(func(rec *Record) {
		if rec.ConvHistoryAccess != enabled {
			rec.ConvHistoryAccess = enabled
			changed = true
		}
	}); err != nil {
		return err
	}
	if changed {
		c.r.notify(c.rec.ReplyID, c.rec.Root)
	}
	return nil
}

// applySettings carries changed settings to the process at a turn
// boundary: in-stream when the backend can, and only otherwise by stopping
// the process so the next turn restarts it with the new values on the same
// backend session. A restart ends the session's background work with the
// process, so it is the last resort.
func (c *conv) applySettings() {
	model, effort, mode, permission, reconcile := c.dirtyModel, c.dirtyEffort, c.dirtyMode, c.dirtyPermission, c.reconcileMode
	if c.replaying {
		return
	}
	c.dirtyModel, c.dirtyEffort, c.dirtyMode, c.dirtyPermission, c.reconcileMode = false, false, false, false, false
	if c.proc == nil || (!model && !effort && !mode && !permission) {
		return
	}
	rec := c.record()
	restart := false
	controls := controlsFor(c.proc)
	if permission && (!rec.Lead || c.r.durableProcess(rec.Backend) || c.r.lifecycle(rec.Backend).RestartLeadOnPermissionChange) {
		restart = true
	}
	if model && !restart && controls.SetModel(rec.Model) != nil {
		restart = true
	}
	if effort && !restart && controls.SetEffort(rec.Effort) != nil {
		restart = true
	}
	if mode && !restart {
		level, _, err := c.r.leadPermission(rec, c.dir)
		if err != nil {
			c.setStatus(func() { c.lastErr = err.Error() })
			return
		}
		// The accepted native target is already in Record. Common selection
		// writes its mapping there; a permitted native control keeps its literal
		// mode. Only an actual new launch remaps explicit common intent.
		target := rec.launchPermissionMode()
		if mode := c.cutoverLaunchMode(); mode != "" {
			// A live settings change keeps the saved preference only.
			target = mode
		}
		if err := controls.setPermissionMode(target); err != nil {
			if reconcile {
				fmt.Fprintf(c.r.opts.Log, "agent %s: permission reconciliation: %v\n", rec.ReplyID, err)
				c.permissionNudge(level, false, c.activeMode)
			} else {
				restart = true
			}
		}
	}
	if restart {
		c.stopProcess("")
	}
}

func (c *conv) armIdle() {
	c.stopIdle()
	if c.proc != nil && c.idleAfter > 0 {
		c.idle = time.NewTimer(c.idleAfter)
	}
}

func (c *conv) stopIdle() {
	if c.idle != nil {
		c.idle.Stop()
		c.idle = nil
	}
}

// close ends the conversation's current lifetime. It marks it closed
// first, under mu, so a Send either wrote before (and its message is
// dropped below with the other held ones) or is refused. A retiring holder
// whose stop fails again refuses it first (closeRetired): nothing is marked
// closed, and the conversation keeps its heartbeat retry.
func (c *conv) close() error {
	if err := c.closeRetired(); err != nil {
		return err
	}
	if c.closed {
		return nil
	}
	if c.r.durableProcess(c.rec.Backend) {
		return c.closeDurable()
	}
	c.closeIf(nil)
	return nil
}

// closeIfPermitted is close guarded by done under wmu+mu; it reports whether
// the conversation closed.
func (c *conv) closeIfPermitted(done func() bool) (bool, error) {
	if err := c.closeRetired(); err != nil {
		return false, err
	}
	if c.closed {
		return false, nil
	}
	if c.r.durableProcess(c.rec.Backend) {
		return c.closeDurableIf(done)
	}
	return c.closeIf(done), nil
}

// closeIf is close when done (nil for always) holds in the same mu hold
// that marks the conversation closed, and reports whether it closed. It
// runs in the loop.
func (c *conv) closeIf(done func() bool) bool {
	c.wmu.Lock()
	c.mu.Lock()
	if done != nil && !done() {
		c.mu.Unlock()
		c.wmu.Unlock()
		return false
	}
	c.rec.ClosedAt = c.r.opts.Now().UTC().Format(time.RFC3339)
	// The completion fence (AdmitCompletion): a notice admitted under an
	// earlier generation never reaches the conversation again, revived or not.
	c.rec.OpenGen++
	c.rec.QuotaContinuation = nil
	c.rec.PendingCompaction = nil
	c.closed = true
	c.syncStateLocked()
	rec := c.rec
	c.mu.Unlock()
	if err := c.r.persistAgentRecord(c.dir, rec); err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: record write: %v\n", rec.ReplyID, err)
	}
	c.wmu.Unlock()
	c.finishClose()
	return true
}

// finishClose is the rest of a close, once the conversation is marked
// closed. It runs in the loop.
func (c *conv) finishClose() {
	c.stopQuotaTimer()
	c.syncHost(false)
	c.stopProcess("")
	if len(c.rec.Mail) > 0 || len(c.rec.Reports) > 0 {
		c.parkedReports = 0
		c.update(func(rec *Record) { rec.Mail, rec.Reports = nil, nil })
	}
	c.heldSenders = nil
	c.r.revokeToken(c.rec.ReplyID)
	c.hold(nil)
	c.dropHeld()
	c.live.setAdapter("")
	c.writeRegistration()
	if c.rec.TurnOpen {
		c.update(func(rec *Record) {
			finishActiveTurn(rec, c.r.opts.Now().UnixMilli())
			rec.TurnOpen = false
		})
	}
	c.signalSettled()
	c.watched()
	if c.rec.CompanionRole != "" && c.r.opts.OnCompanionClose != nil {
		c.r.opts.OnCompanionClose(c.rec.CompanionRole, c.rec.ReplyID, c.rec.WSKey, "close")
	}
}

func (c *conv) notifyCompanionActivity() {
	fn := c.r.opts.OnCompanionActivity
	if fn == nil {
		return
	}
	if c.rec.CompanionRole != "" {
		fn(c.rec.CompanionRole, c.rec.ReplyID, c.rec.WSKey)
		return
	}
	parent := c.rec.Parent
	for depth := 0; parent != nil && depth < 8; depth++ {
		prec, ok := c.r.recordOf(parent.ReplyID)
		if !ok {
			break
		}
		if prec.CompanionRole != "" {
			fn(prec.CompanionRole, prec.ReplyID, prec.WSKey)
			return
		}
		parent = prec.Parent
	}
}

// harnessReported resolves the backend's catalog profile at the version
// the process reported, demotes the items its init shape does not support,
// and offers the command list again under the result. A backend with no
// profile stays handle-only: no grades, no demotion, no warning.
func (c *conv) harnessReported(report HarnessReport, quiet bool) {
	c.r.noteHarnessVersion(c.rec.Backend, report.Version, c.launchBinary)
	profile := c.r.opts.Backends[c.rec.Backend].Catalog
	if profile == nil {
		return
	}
	role := RoleWorker
	if c.rec.Lead {
		role = RoleLead
	}
	caps := profile.Resolve(role, report.Version)
	caps.demote(report.Demoted)
	if c.caps != nil && reflect.DeepEqual(*c.caps, caps) {
		return
	}
	if !quiet && (caps.Unverified || len(caps.Demoted) > 0) {
		fmt.Fprintf(c.r.opts.Log, "agent %s: harness version %q (verified %s..%s) runs on %s's capabilities; demoted by its init: %v\n",
			c.rec.ReplyID, report.Version, caps.Oldest, caps.Newest, caps.ProfileVersion, caps.Demoted)
	}
	// A whole-record copy takes mu (record): Send stamps rec under it.
	rec := c.record()
	before := c.r.withPlaybookCommands(rec, allowedCommands(c.commands, c.caps))
	c.setStatus(func() { c.caps = &caps })
	if c.commands != nil {
		if after := c.r.withPlaybookCommands(rec, allowedCommands(c.commands, c.caps)); !slices.Equal(after, before) {
			c.apply(wslive.Event{Kind: wslive.KindCommands, Commands: after}, quiet)
		}
	}
	c.changed(quiet)
}
