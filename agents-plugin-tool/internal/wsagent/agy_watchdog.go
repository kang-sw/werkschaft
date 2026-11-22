package wsagent

import (
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
)

// DefaultAgyModelWaitTimeoutSeconds and DefaultAgyModelWaitRetries are the ws
// package defaults: seconds of raw ACP session/update silence during an active
// agy model wait, and the automatic `continue` attempts shared by silence and
// in-band failures. The wsflow package supplies zero for the timeout instead.
const (
	DefaultAgyModelWaitTimeoutSeconds = 120
	DefaultAgyModelWaitRetries        = 3
)

// DefaultAgyStallLogCheck and DefaultAgyHTTP2FrameTrace are both packages'
// defaults for agy.stall_log_check and agy.http2_frame_trace.
const (
	DefaultAgyStallLogCheck   = true
	DefaultAgyHTTP2FrameTrace = false
)

// agyAPIErrorPrefix starts agy's in-band API error line. The full matcher is
// deliberately anchored to the accumulated reply tail: agy can split the
// marker across chunks, and later reply text means the stream recovered.
const agyAPIErrorPrefix = "⚠ API error"

const agyStreamInterrupted = "⚠ The stream was interrupted. Please continue the task you were working on."

const (
	// agy logs an in-band failure line and then retries natively. The retry
	// can hold response headers within seconds yet emit no ACP session/update
	// until model output starts, so a short drain cancels a retry that is
	// already succeeding. Failures agy is known to retry natively (stream idle
	// timeout, unexpected EOF, stream interrupted) get the long window: agy's
	// full 10-minute SSE idle window plus a margin. Raw ACP activity renews it
	// while the failure is pending; a turn end still takes the retry path.
	//
	// Every other class keeps the 15s drain. The "(attempt N)" line is the
	// same for a retried and a final failure, so the class list is the only
	// signal; making all classes long would leave a terminal failure hanging
	// for 11 minutes before ws continues it.
	modelNativeIdleRetryFallback = 11 * time.Minute
	modelFailureDrain            = 15 * time.Second
	modelWaitCancelGrace         = 5 * time.Second
	modelWaitBackoffBase         = time.Second
	modelWaitBackoffCap          = 30 * time.Second
)

const (
	modelWaitBackoffIdle uint32 = iota
	modelWaitBackoffPending
	modelWaitBackoffCancelled
	modelWaitBackoffFiring
)

// Owner disabled silence detection: long tool-call generation looks identical
// to a stalled stream, while agy's in-band failures still trigger retries.
// A hosted lead's silence timer runs again only behind agy's own log
// (agy.stall_log_check): a firing cuts only a request with no response
// headers since its boundary. This switch, for tests, restores the raw
// silence cutoff that needs no log.
var agySilenceWatchdogEnabled = false

// defaultAgyModelWaitBackoff uses equal jitter: each attempt waits between
// half and all of an exponentially growing ceiling, bounded at 30 seconds.
func defaultAgyModelWaitBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	ceiling := modelWaitBackoffBase
	for i := 1; i < attempt && ceiling < modelWaitBackoffCap; i++ {
		ceiling = min(ceiling*2, modelWaitBackoffCap)
	}
	half := ceiling / 2
	return half + time.Duration(rand.Int64N(int64(ceiling-half)+1))
}

func agyReplyTailFailure(reply string) string {
	line := strings.TrimRight(reply, " \t\r\n")
	if i := strings.LastIndexByte(line, '\n'); i >= 0 {
		line = line[i+1:]
	}
	line = strings.TrimSuffix(line, "\r")
	if line == agyStreamInterrupted {
		return line
	}
	prefix := agyAPIErrorPrefix + " (attempt "
	if !strings.HasPrefix(line, prefix) {
		return ""
	}
	rest := strings.TrimPrefix(line, prefix)
	end := strings.Index(rest, "): ")
	if end <= 0 || strings.TrimSpace(rest[end+3:]) == "" {
		return ""
	}
	for _, r := range rest[:end] {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return line
}

// loadModelWait reads agy's model-wait settings once at process start for
// hosted agy leads and workers. Never touches disk during turns or timer ticks.
func (c *conv) loadModelWait() {
	if c.rec.Backend != BackendAgy {
		c.modelWaitTimeout, c.modelWaitMaxRetries = 0, 0
		return
	}
	cfg := c.r.opts.Config
	cfg.RepoRoot = c.rec.Root
	if roots, ok := c.r.opts.Keys.(interface{ Root(string) (string, bool) }); ok {
		if root, found := roots.Root(c.rec.WSKey); found {
			cfg.RepoRoot = root
		}
	}
	reader, _ := c.r.opts.Keys.(wsconfig.SessionReader)
	sec := DefaultAgyModelWaitTimeoutSeconds
	if c.r.opts.AgyModelWaitTimeoutSeconds != nil {
		sec = *c.r.opts.AgyModelWaitTimeoutSeconds
	}
	retries := DefaultAgyModelWaitRetries
	if c.r.opts.AgyModelWaitRetries != nil {
		retries = *c.r.opts.AgyModelWaitRetries
	}
	r := wsconfig.NewResolver(cfg, map[string]string{
		wsconfig.ItemAgyModelWaitTimeoutSeconds: strconv.Itoa(sec),
		wsconfig.ItemAgyModelWaitRetries:        strconv.Itoa(retries),
	}, reader, nil)
	rv, err := r.Get(c.rec.WSKey, wsconfig.ItemAgyModelWaitTimeoutSeconds)
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %s: %v\n", c.rec.ReplyID, wsconfig.ItemAgyModelWaitTimeoutSeconds, err)
	} else if n, perr := strconv.Atoi(strings.TrimSpace(rv.Value)); perr != nil || n < 0 {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %s: invalid value %q; using %d\n", c.rec.ReplyID, wsconfig.ItemAgyModelWaitTimeoutSeconds, rv.Value, sec)
	} else {
		sec = n
	}
	rv, err = r.Get(c.rec.WSKey, wsconfig.ItemAgyModelWaitRetries)
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %s: %v\n", c.rec.ReplyID, wsconfig.ItemAgyModelWaitRetries, err)
	} else if n, perr := strconv.Atoi(strings.TrimSpace(rv.Value)); perr != nil || n < 0 {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %s: invalid value %q; using %d\n", c.rec.ReplyID, wsconfig.ItemAgyModelWaitRetries, rv.Value, retries)
	} else {
		retries = n
	}
	c.modelWaitTimeout = time.Duration(sec) * time.Second
	c.modelWaitMaxRetries = retries
	// The log-confirmed cutoff and the frame trace are hosted-lead only.
	c.modelWaitLogCheck, c.agyFrameTrace = false, ""
	if !c.rec.Lead {
		return
	}
	bools := wsconfig.NewResolver(cfg, map[string]string{
		wsconfig.ItemAgyStallLogCheck:   strconv.FormatBool(DefaultAgyStallLogCheck),
		wsconfig.ItemAgyHTTP2FrameTrace: strconv.FormatBool(DefaultAgyHTTP2FrameTrace),
	}, reader, nil)
	readBool := func(item string, def bool) bool {
		rv, err := bools.Get(c.rec.WSKey, item)
		if err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: %s: %v\n", c.rec.ReplyID, item, err)
			return def
		}
		v, perr := strconv.ParseBool(strings.TrimSpace(rv.Value))
		if perr != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: %s: invalid value %q; using %v\n", c.rec.ReplyID, item, rv.Value, def)
			return def
		}
		return v
	}
	c.modelWaitLogCheck = readBool(wsconfig.ItemAgyStallLogCheck, DefaultAgyStallLogCheck)
	if readBool(wsconfig.ItemAgyHTTP2FrameTrace, DefaultAgyHTTP2FrameTrace) && c.dir != "" {
		c.agyFrameTrace = filepath.Join(c.dir, AgyFrameLogName)
	}
}

// markModelWaitBoundary records that a fresh model request started at or
// after at: a user-turn prompt with no turn running or the watchdog's own
// automatic continue, with at taken before the send reaches the holder. A
// steer and a background tool completion are not boundaries. A zero at
// means no trustworthy time: the attempt has no boundary and never cuts.
func (c *conv) markModelWaitBoundary(at time.Time) {
	c.modelWaitBoundary = at
	c.modelWaitContent = false
}

// silenceTimerEnabled reports whether the silence timer may arm: always in
// the raw test mode, otherwise only for a hosted lead's log-confirmed
// cutoff while the current attempt has a boundary and no model content.
func (c *conv) silenceTimerEnabled() bool {
	if agySilenceWatchdogEnabled {
		return true
	}
	return c.modelWaitLogCheck && !c.modelWaitBoundary.IsZero() && !c.modelWaitContent
}

// newAgyLog is the worker log state of an agy process starting (or
// rejoined) now: the locator considers only logs modified since then.
func (c *conv) newAgyLog() *agyStallLog {
	dir := c.r.opts.AgyLogDir
	if dir == "" {
		dir = agyDefaultLogDir()
	}
	return newAgyStallLog(dir, time.Now())
}

// headerlessStall reads this session's agy worker log once and reports
// whether it confirms that the request begun at the boundary has no
// response headers. A log that cannot be located, read, verified, or parsed
// is not confirmation; the session logs one notice for it.
func (c *conv) headerlessStall() bool {
	if c.agyLog == nil {
		c.agyLog = c.newAgyLog()
	}
	verdict, reason := c.agyLog.verdict(c.rec.SessionID, c.modelWaitBoundary)
	switch verdict {
	case agyStallHeaderless:
		return true
	case agyStallUnknown:
		if !c.agyLogNoted {
			c.agyLogNoted = true
			fmt.Fprintf(c.r.opts.Log, "agent %s: agy stall log check cannot confirm a stalled request (%s); silence does not interrupt this session's turns while the log stays unconfirmed\n", c.rec.ReplyID, reason)
		}
	}
	return false
}

// noteAgyFrameCutoff asks proc's holder to add one marker line to its frame
// capture, so the stalled stream can be found by its time. The holder owns
// the capture file and its cap: it writes the line only when its child was
// spawned with a capture, whatever the daemon's settings say now, and a
// holder that predates the operation writes nothing. The marker is a
// diagnostic: it is asked for after the interrupt went out and off the
// loop, so a slow holder reply never delays the cut and its failure only
// logs.
func (c *conv) noteAgyFrameCutoff(proc Process, boundary, cutoff time.Time) {
	noter, ok := proc.(frameCaptureNoter)
	if !ok {
		return
	}
	text := fmt.Sprintf("ws: headerless cutoff boundary=%s cutoff=%s", boundary.Local().Format(time.RFC3339Nano), cutoff.Local().Format(time.RFC3339Nano))
	go func() {
		if err := noter.NoteFrameCapture(text); err != nil {
			c.do(func() { fmt.Fprintf(c.r.opts.Log, "agent %s: agy frame trace marker: %v\n", c.rec.ReplyID, err) })
		}
	}()
}

// startModelWait resets per-turn state for a freshly sent ordinary prompt;
// sentAt was read before the send.
func (c *conv) startModelWait(sentAt time.Time) {
	c.disarmModelWait()
	c.markModelWaitBoundary(sentAt)
	c.armModelWait()
}

// armModelWait also accepts a turn recovered from holder replay, with its
// pending tool count already restored.
func (c *conv) armModelWait() {
	if c.rec.Backend != BackendAgy || c.modelWaitTimeout <= 0 || !c.turn || c.replaying || c.wrapHold || c.compacting || c.compactCommand || c.compactInternalTurn || c.interruptAsked {
		return
	}
	c.modelWaitArmed = true
	c.resetModelWaitTimer()
}

func (c *conv) resetModelWaitTimer() {
	c.stopModelWaitTimer()
	if !c.silenceTimerEnabled() || !c.modelWaitArmed || c.modelWaitCancelling || c.modelWaitFailure != "" || c.modelWaitFailErr != "" || c.modelWaitTimeout <= 0 {
		return
	}
	if c.modelWaitPending > 0 || len(c.approvals) > 0 {
		return
	}
	if c.r != nil && c.r.opts.Timer != nil {
		c.modelWaitC, c.modelWaitStop = c.r.opts.Timer(c.modelWaitTimeout)
	}
}

func (c *conv) stopModelWaitTimer() {
	if c.modelWaitStop != nil {
		c.modelWaitStop()
	}
	c.modelWaitC, c.modelWaitStop = nil, nil
}

func (c *conv) stopModelFailureDrain() {
	if c.modelWaitDrainStop != nil {
		c.modelWaitDrainStop()
	}
	c.modelWaitDrainC, c.modelWaitDrainStop = nil, nil
}

func (c *conv) stopModelWaitCancelGrace() {
	if c.modelWaitCancelStop != nil {
		c.modelWaitCancelStop()
	}
	c.modelWaitCancelC, c.modelWaitCancelStop = nil, nil
}

func (c *conv) stopModelWaitBackoff() {
	if c.modelWaitBackoffStop != nil {
		c.modelWaitBackoffStop()
	}
	c.modelWaitBackoffC, c.modelWaitBackoffStop = nil, nil
	c.modelWaitBackoffFailure = ""
	c.modelWaitBackoffFence.Store(modelWaitBackoffIdle)
}

func (c *conv) armModelWaitCancelGrace() {
	c.stopModelWaitCancelGrace()
	if c.r != nil && c.r.opts.Timer != nil {
		c.modelWaitCancelC, c.modelWaitCancelStop = c.r.opts.Timer(modelWaitCancelGrace)
	}
}

func (c *conv) disarmModelWait() {
	c.stopModelWaitTimer()
	c.stopModelFailureDrain()
	c.stopModelWaitCancelGrace()
	c.stopModelWaitBackoff()
	c.modelWaitArmed = false
	c.modelWaitRetries = 0
	c.modelWaitPending = 0
	c.modelWaitCancelling = false
	c.modelWaitFailErr = ""
	c.modelWaitDiagnostic = ""
	c.modelWaitExhaustedPublished = false
	c.modelWaitFailureClasses = nil
	c.modelWaitFailure = ""
	c.modelWaitFailureExcluded = false
	c.modelWaitBoundary = time.Time{}
	c.modelWaitContent = false
}

// onModelActivity handles a raw ACP session/update activity signal carrying
// the current count of pending tool calls (including daemon run_command
// calls) and whether the update carried model content.
func (c *conv) onModelActivity(pending int, content bool) {
	if !c.modelWaitArmed {
		return
	}
	// The turn's last pending tool call (one that paused the timer)
	// reached a terminal status: agy's next model request follows its
	// result. No time at or before that completion is known (the update
	// can wait behind the holder's ring and the stream's backpressure while
	// agy already logs the next request's header), so that request has no
	// boundary and the headerless cutoff stays off until the next prompt or
	// automatic continue. A background command that outlived an earlier
	// turn is not among the pending calls, so its completion moves nothing.
	if pending < c.modelWaitPending && pending == 0 {
		c.markModelWaitBoundary(time.Time{})
	} else if content {
		c.modelWaitContent = true
	}
	c.modelWaitPending = pending
	if c.modelWaitCancelling || c.modelWaitFailErr != "" {
		return
	}
	if pending > 0 || len(c.approvals) > 0 {
		c.stopModelWaitTimer()
		c.stopModelFailureDrain()
		if c.modelWaitFailure != "" {
			c.modelWaitFailureExcluded = true
		}
		c.modelWaitFailure = ""
		return
	}
	if c.modelWaitFailure != "" {
		if d := modelFailureDrainDuration(c.modelWaitFailure); d == modelNativeIdleRetryFallback {
			c.resetModelFailureDrain(d)
		}
		return
	}
	c.resetModelWaitTimer()
}

// onModelFailure starts a recovery wait when an anchored failure line becomes
// the accumulated reply tail. Later model output clears the failure; a
// prompt response goes through the existing settlement and retry path.
func (c *conv) onModelFailure(failure string) {
	if !c.modelWaitArmed || c.modelWaitCancelling || c.modelWaitFailErr != "" {
		return
	}
	if failure == "" {
		c.modelWaitFailureExcluded = false
		if c.modelWaitFailure == "" {
			return
		}
		c.modelWaitFailure = ""
		c.stopModelFailureDrain()
		c.resetModelWaitTimer()
		return
	}
	if !c.turn || c.proc == nil || c.modelWaitPending > 0 || len(c.approvals) > 0 {
		c.modelWaitFailureExcluded = true
		return
	}
	c.modelWaitFailureExcluded = false
	c.modelWaitFailure = failure
	c.stopModelWaitTimer()
	c.resetModelFailureDrain(modelFailureDrainDuration(failure))
}

func modelFailureDrainDuration(failure string) time.Duration {
	switch agyModelFailureClass(failure) {
	case "stream idle timeout", "unexpected EOF", "stream interrupted":
		return modelNativeIdleRetryFallback
	}
	return modelFailureDrain
}

func (c *conv) resetModelFailureDrain(d time.Duration) {
	c.stopModelFailureDrain()
	if c.r != nil && c.r.opts.Timer != nil {
		c.modelWaitDrainC, c.modelWaitDrainStop = c.r.opts.Timer(d)
	}
}

func (c *conv) modelFailureDrainTimedOut() {
	c.modelWaitDrainC, c.modelWaitDrainStop = nil, nil
	if !c.modelWaitArmed || !c.turn || c.proc == nil || c.modelWaitFailure == "" || c.modelWaitCancelling || c.modelWaitFailErr != "" {
		return
	}
	if c.modelWaitPending > 0 || len(c.approvals) > 0 {
		return
	}
	if c.modelWaitRetries < c.modelWaitMaxRetries {
		c.modelWaitCancelling = true
		fmt.Fprintf(c.r.opts.Log, "agent %s: agy model failure remained unresolved for %s; interrupting before automatic continue attempt %d/%d (%s)\n", c.rec.ReplyID, modelFailureDrainDuration(c.modelWaitFailure), c.modelWaitRetries+1, c.modelWaitMaxRetries, c.modelWaitFailure)
		if err := c.proc.Interrupt(); err != nil {
			c.failModelWaitImmediately(fmt.Sprintf("agy model failure interrupt failed: %v (%s)", err, c.modelWaitFailure))
		} else {
			c.armModelWaitCancelGrace()
		}
		return
	}
	c.noteModelWaitFailure(c.modelWaitFailure)
	c.modelWaitFailErr = c.modelWaitExhaustedError(c.modelWaitFailure)
	fmt.Fprintf(c.r.opts.Log, "agent %s: %s\n", c.rec.ReplyID, c.modelWaitFailErr)
	if err := c.proc.Interrupt(); err != nil {
		c.failModelWaitImmediately(fmt.Sprintf("%s (interrupt failed: %v)", c.modelWaitFailErr, err))
	} else {
		c.armModelWaitCancelGrace()
	}
}

func (c *conv) modelWaitTimedOut() {
	c.modelWaitC, c.modelWaitStop = nil, nil
	if !c.modelWaitArmed || !c.turn || c.proc == nil || c.modelWaitCancelling || c.modelWaitFailErr != "" {
		return
	}
	if c.modelWaitPending > 0 || len(c.approvals) > 0 {
		return
	}
	if !agySilenceWatchdogEnabled {
		// A main-request header in the window means the model is
		// generating; an unconfirmed log never cuts. Either way the
		// timer runs again without spending a retry.
		if c.modelWaitContent || !c.headerlessStall() {
			c.resetModelWaitTimer()
			return
		}
		// The marker follows the interrupt below, whichever branch sends it.
		defer c.noteAgyFrameCutoff(c.proc, c.modelWaitBoundary, c.r.opts.Now())
	}
	c.noteModelWaitFailure("")
	dur := formatWatchdogDuration(c.modelWaitTimeout)
	if c.modelWaitRetries < c.modelWaitMaxRetries {
		c.modelWaitCancelling = true
		fmt.Fprintf(c.r.opts.Log, "agent %s: agy model stream stalled for %s; interrupting before automatic continue attempt %d/%d\n", c.rec.ReplyID, dur, c.modelWaitRetries+1, c.modelWaitMaxRetries)
		if err := c.proc.Interrupt(); err != nil {
			c.failModelWaitImmediately(fmt.Sprintf("agy model stream stalled for %s and interrupt failed: %v", dur, err))
		} else {
			c.armModelWaitCancelGrace()
		}
		return
	}
	reason := c.modelWaitExhaustedError("")
	c.modelWaitFailErr = reason
	fmt.Fprintf(c.r.opts.Log, "agent %s: %s\n", c.rec.ReplyID, reason)
	if err := c.proc.Interrupt(); err != nil {
		c.failModelWaitImmediately(fmt.Sprintf("%s (interrupt failed: %v)", reason, err))
	} else {
		c.armModelWaitCancelGrace()
	}
}

func (c *conv) modelWaitCancelTimedOut() {
	c.modelWaitCancelC, c.modelWaitCancelStop = nil, nil
	if !c.modelWaitArmed || !c.turn || c.proc == nil || (!c.modelWaitCancelling && c.modelWaitFailErr == "") {
		return
	}
	reason := c.modelWaitFailErr
	if reason == "" {
		reason = fmt.Sprintf("agy model stream did not stop within %s after interrupt", formatWatchdogDuration(modelWaitCancelGrace))
	} else {
		reason += fmt.Sprintf(" and did not stop within %s after interrupt", formatWatchdogDuration(modelWaitCancelGrace))
	}
	c.failModelWaitImmediately(reason)
}

// interceptModelWaitTurnEnd intercepts a cancelled TurnEnd or terminal in-band
// model failure while the per-turn retry budget remains, keeps the logical ws
// turn open, sends `continue`, and rearms the watchdog.
func (c *conv) interceptModelWaitTurnEnd(ev Event) bool {
	if !c.modelWaitArmed || !c.turn || c.proc == nil {
		return false
	}
	if ev.ModelFailure != "" && (c.modelWaitFailureExcluded || c.modelWaitPending > 0 || len(c.approvals) > 0) {
		return false
	}
	if c.modelWaitCancelling {
		c.stopModelWaitCancelGrace()
		c.modelWaitCancelling = false
		// If the prompt actually finished normally before session/cancel took
		// effect (and not on an in-band failure), let it settle as done.
		if ev.TurnReason != wslive.TurnAborted && ev.TurnError == "" && ev.ModelFailure == "" && c.modelWaitFailure == "" {
			return false
		}
		failure := ev.ModelFailure
		if failure == "" {
			failure = c.modelWaitFailure
		}
		return c.retryModelWait(failure)
	}
	if ev.ModelFailure != "" && ev.TurnError == "" && ev.TurnReason != wslive.TurnAborted && c.modelWaitFailErr == "" && c.modelWaitRetries < c.modelWaitMaxRetries {
		return c.retryModelWait(ev.ModelFailure)
	}
	return false
}

func (c *conv) retryModelWait(failure string) bool {
	c.stopModelFailureDrain()
	c.modelWaitPending = 0
	c.modelWaitFailure = ""
	c.modelWaitFailureExcluded = false
	if c.reply != "" {
		c.modelWaitDiagnostic = c.reply
	}
	c.reply = ""
	c.modelWaitBackoffFailure = failure
	c.modelWaitBackoffFence.Store(modelWaitBackoffPending)
	c.mu.Lock()
	held := c.held > 0
	c.mu.Unlock()
	if held {
		c.cancelModelWaitBackoffForInput()
	}
	if c.modelWaitBackoffFence.Load() == modelWaitBackoffCancelled {
		c.cancelModelWaitBackoff()
		return true
	}
	attempt := c.modelWaitRetries + 1
	delay := c.r.opts.AgyModelWaitBackoff(attempt)
	if delay <= 0 {
		c.modelWaitBackoffTimedOut()
		return true
	}
	fmt.Fprintf(c.r.opts.Log, "agent %s: waiting %s before automatic continue attempt %d/%d\n", c.rec.ReplyID, formatWatchdogDuration(delay), attempt, c.modelWaitMaxRetries)
	c.modelWaitBackoffC, c.modelWaitBackoffStop = c.r.opts.Timer(delay)
	return true
}

func (c *conv) modelWaitBackoffTimedOut() {
	failure := c.modelWaitBackoffFailure
	c.modelWaitBackoffC, c.modelWaitBackoffStop = nil, nil
	if !c.modelWaitBackoffFence.CompareAndSwap(modelWaitBackoffPending, modelWaitBackoffFiring) {
		if c.modelWaitBackoffFence.Load() == modelWaitBackoffCancelled {
			c.cancelModelWaitBackoff()
		}
		return
	}
	defer func() {
		c.modelWaitBackoffFailure = ""
		c.modelWaitBackoffFence.Store(modelWaitBackoffIdle)
	}()
	if !c.modelWaitArmed || !c.turn || c.proc == nil || c.modelWaitFailErr != "" {
		return
	}
	c.noteModelWaitFailure(failure)
	c.modelWaitRetries++
	if failure == "" {
		fmt.Fprintf(c.r.opts.Log, "agent %s: agy model stream stalled for %s; sending automatic continue attempt %d/%d\n", c.rec.ReplyID, formatWatchdogDuration(c.modelWaitTimeout), c.modelWaitRetries, c.modelWaitMaxRetries)
	} else {
		fmt.Fprintf(c.r.opts.Log, "agent %s: agy reported terminal in-band model failure; sending automatic continue attempt %d/%d (%s)\n", c.rec.ReplyID, c.modelWaitRetries, c.modelWaitMaxRetries, failure)
	}
	sentAt := c.r.opts.Now()
	if err := c.proc.Send("continue"); err != nil {
		reason := fmt.Sprintf("agy model stream stalled for %s and retry send failed: %v", formatWatchdogDuration(c.modelWaitTimeout), err)
		if failure != "" {
			reason = fmt.Sprintf("agy model failure retry send failed: %v (%s)", err, failure)
		}
		c.failModelWaitImmediately(reason)
		return
	}
	c.markModelWaitBoundary(sentAt)
	c.resetModelWaitTimer()
}

// modelWaitSteered restarts the silence timer with a full timeout after a
// successful steer, keeping the boundary: the steer's acknowledgement is a
// prompt response, not model activity.
func (c *conv) modelWaitSteered() {
	if c.modelWaitArmed {
		c.resetModelWaitTimer()
	}
}

func (c *conv) cancelModelWaitBackoffForInput() {
	c.modelWaitBackoffFence.CompareAndSwap(modelWaitBackoffPending, modelWaitBackoffCancelled)
}

func (c *conv) cancelModelWaitBackoff() {
	state := c.modelWaitBackoffFence.Load()
	if c.modelWaitBackoffC == nil && state != modelWaitBackoffPending && state != modelWaitBackoffCancelled {
		return
	}
	failure := c.modelWaitBackoffFailure
	c.stopModelWaitBackoff()
	c.restoreModelWaitDiagnostic()
	c.summaryReason = wslive.TurnAborted
	turnErr := ""
	if failure != "" {
		turnErr = agyTerminalFailureError(failure, "")
		c.summaryReason = "error"
	}
	c.disarmModelWait()
	c.endTurn(turnErr, false)
}

// modelWaitTurnError returns the TurnFailed error message to apply when a
// watched turn settles after exhausting its retry budget.
func (c *conv) modelWaitTurnError(ev Event) string {
	if !c.modelWaitArmed {
		return ""
	}
	if ev.ModelFailure != "" && (c.modelWaitFailureExcluded || c.modelWaitPending > 0 || len(c.approvals) > 0) {
		return ""
	}
	if c.modelWaitFailErr != "" {
		c.publishModelWaitExhaustedDiagnostic()
		return c.modelWaitFailErr
	}
	if ev.ModelFailure != "" && ev.TurnError == "" && ev.TurnReason != wslive.TurnAborted && c.modelWaitRetries >= c.modelWaitMaxRetries {
		c.noteModelWaitFailure(ev.ModelFailure)
		c.modelWaitFailErr = c.modelWaitExhaustedError(ev.ModelFailure)
		c.publishModelWaitExhaustedDiagnostic()
		return c.modelWaitFailErr
	}
	return ""
}

// agyTerminalFailureError carries the failure class through the same error
// field as other failed turns, including persisted results and parent reports.
func agyTerminalFailureError(failure, detail string) string {
	if detail == "" {
		detail = failure
	}
	return fmt.Sprintf("[%s] %s", agyModelFailureClass(failure), detail)
}

func (c *conv) modelWaitExhaustedError(failure string) string {
	if failure == "" {
		return fmt.Sprintf("agy model stream stalled for %s after %d automatic continue attempts", formatWatchdogDuration(c.modelWaitTimeout), c.modelWaitRetries)
	}
	if strings.HasPrefix(failure, agyAPIErrorPrefix) {
		return fmt.Sprintf("agy API error after %d automatic continue attempts: %s", c.modelWaitRetries, failure)
	}
	return fmt.Sprintf("agy model failure after %d automatic continue attempts: %s", c.modelWaitRetries, failure)
}

func (c *conv) modelWaitExhaustedDiagnostic() string {
	classes := ""
	if len(c.modelWaitFailureClasses) > 0 {
		classes = " Failure classes: " + strings.Join(c.modelWaitFailureClasses, ", ") + "."
	}
	if c.modelWaitRetries == 0 {
		return "ws: agy stalled; automatic continue retries are disabled (0 attempts configured)." + classes
	}
	return fmt.Sprintf("ws: agy stalled; sent %d automatic continue attempts, all failed.%s", c.modelWaitRetries, classes)
}

func (c *conv) noteModelWaitFailure(failure string) {
	class := agyModelFailureClass(failure)
	for _, seen := range c.modelWaitFailureClasses {
		if seen == class {
			return
		}
	}
	c.modelWaitFailureClasses = append(c.modelWaitFailureClasses, class)
}

func agyModelFailureClass(failure string) string {
	if failure == "" {
		return "no-event silence"
	}
	lower := strings.ToLower(failure)
	switch {
	case strings.Contains(lower, "no capacity"):
		if strings.Contains(lower, "503") || strings.Contains(lower, "unavailable") {
			return "no capacity (503/UNAVAILABLE)"
		}
		return "no capacity"
	case strings.Contains(lower, "unexpected eof"):
		return "unexpected EOF"
	case strings.Contains(lower, "stream idle timeout"):
		return "stream idle timeout"
	case strings.Contains(lower, "tls handshake timeout"):
		return "TLS handshake timeout"
	case strings.Contains(lower, "tls") && strings.Contains(lower, "timeout"):
		return "TLS timeout"
	case strings.Contains(lower, "503"):
		return "HTTP 503"
	case strings.Contains(lower, "unavailable"):
		return "UNAVAILABLE"
	case failure == agyStreamInterrupted:
		return "stream interrupted"
	case strings.HasPrefix(failure, agyAPIErrorPrefix):
		return "API error"
	default:
		return "model failure"
	}
}

func (c *conv) publishModelWaitExhaustedDiagnostic() {
	if c.modelWaitExhaustedPublished {
		return
	}
	c.restoreModelWaitDiagnostic()
	diagnostic := c.modelWaitExhaustedDiagnostic()
	if c.reply == "" {
		c.reply = diagnostic
	} else {
		c.reply += "\n\n" + diagnostic
	}
	c.apply(wslive.Event{Kind: wslive.KindReply, Text: diagnostic, Final: true})
	c.modelWaitExhaustedPublished = true
}

func (c *conv) restoreModelWaitDiagnostic() {
	if c.reply == "" {
		c.reply = c.modelWaitDiagnostic
	}
}

func (c *conv) failModelWaitImmediately(reason string) {
	c.setStatus(func() { c.lastErr = reason })
	fmt.Fprintf(c.r.opts.Log, "agent %s: %s\n", c.rec.ReplyID, reason)
	c.stopProcess("")
}

func formatWatchdogDuration(d time.Duration) string {
	if d >= time.Second && d%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
	return d.String()
}
