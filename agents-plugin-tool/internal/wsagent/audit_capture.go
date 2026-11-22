package wsagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wsholder"
)

// Audit capture (ticket 261119-feat-ws-audit-capture-harness-adapters):
// every output line of a daemon-hosted harness process, and every line its
// session holder kept out of the ring (the holder's audit channel), is
// mapped to audit envelopes by the backend's auditAdapter and appended to
// the audit sink.
//
// Capture never blocks the stream mapper or the holder reader: both only
// hand a copy of the line to a bounded queue (dropped and counted when it is
// full), and one goroutine per process maps, orders and appends. A slow or
// failing sink costs audit events, never session latency.
//
// Ordering. The holder sends the skipped lines (tee lines) on an
// unsequenced channel, each anchored on the ring sequence of the last stdout
// line it kept before it, and fences every kept run: {anchor, next, through}
// closes the interval after anchor and releases the kept lines next..through.
// While the channel is on, a kept line waits until a fence released it, so
// the tee lines of the interval before it are mapped first; envelope seq is
// assigned in that source order. No fence within auditFenceWait closes the
// source's tee stream as incomplete (a loss marker), never reordering.

// auditAdapter maps one harness's output lines to audit events. It is
// stateful (it assembles streamed partial lines into events) and is only
// called from its capture's goroutine.
type auditAdapter interface {
	// mapLine maps one complete output line read at at. recognized is false
	// for a line outside the harness's protocol, which capture keeps raw as
	// KindOther. evs are the line's own events; carried are the assemblies
	// of earlier (tee'd) lines the line ended, which a replay does not
	// rebuild, kept apart under their own key.
	mapLine(line []byte, at time.Time) (recognized bool, carried, evs []auditEvent)
	// markLoss records n lost source lines (n 0: an unknown count) on the
	// assembled event still open, reporting false when none spans the loss.
	markLoss(n uint64, reason string) bool
	// flush ends every open assembly and returns the events built so far,
	// marked incomplete.
	flush(at time.Time) []auditEvent
}

// auditEvent is one mapped event and its slot, the ordinal it takes under
// its source record's identity. A slot is fixed by the event's role in the
// record (a content block's index), never by which other events the record
// yields: a replay that rebuilds fewer of them (a tee'd body gone, a
// started item wrapped out of the ring) keeps every retained event's
// identity, so none collides or shifts.
//
// key, when set, is the event's own identity in place of its record's (a
// keyed SourceID): for an event whichever record the ring still holds may
// yield, such as a Codex tool call from its started item or, that one gone,
// from its completed one, so either finds the other retained.
type auditEvent struct {
	slot int
	key  string
	ev   wsaudit.Envelope
}

// slotted gives evs their positions as slots, for a record whose events are
// all rebuilt whenever it is mapped.
func slotted(evs []wsaudit.Envelope) []auditEvent {
	out := make([]auditEvent, len(evs))
	for i, ev := range evs {
		out[i] = auditEvent{slot: i, ev: ev}
	}
	return out
}

// auditTap is what the runner gives a process to capture: the session's
// policy identity and the sink. Nil captures nothing.
type auditTap struct {
	Session wsaudit.Session
	Sink    wsaudit.Sink
	Log     io.Writer
}

const (
	// auditQueueItems bounds the lines and holder events waiting for the
	// capture goroutine; past it they are dropped and recorded as one loss.
	auditQueueItems = 4096
	// auditControlItems is queue room only attach and detach markers use:
	// a lost attach would fail every later tee and fence of its connection
	// (its Conn would be unknown), so they are never refused for the lines
	// filling the queue. Past it they coalesce (auditCapture.over).
	auditControlItems = 64
	// auditSinkTimeout bounds one sink call's context; the Sink contract
	// already requires a prompt return.
	auditSinkTimeout = time.Second
)

// auditFenceWait bounds how long a kept line waits for the fence that
// releases it; a test shortens it.
var auditFenceWait = 2 * time.Second

// auditPendingBytes bounds the tee lines held for intervals no fence closed
// yet. Without a kept line nothing waits on them, so no timer runs; one
// thinking or tool-input block can legitimately stream skipped deltas for
// minutes. Past the bound the tee stream closes as incomplete (queue_full)
// until the next fence reopens it. A test shortens it.
var auditPendingBytes = 32 << 20

type auditItemKind uint8

const (
	auditItemLine auditItemKind = iota
	auditItemTee
	auditItemFence
	auditItemAttached
	auditItemDetached
)

type auditItem struct {
	kind   auditItemKind
	data   []byte
	at     time.Time
	seq    uint64
	replay bool
	ev     wsholder.Event
}

// auditInterval is the tee lines skipped after the kept line anchor, with
// the holder's account of them (fence) or none (unfenced: the tee stream
// closed before its fence came).
type auditInterval struct {
	anchor        uint64
	tees          []wsholder.Event
	sent, dropped uint64
	// unfenced intervals closed without their fence; reason says why.
	unfenced bool
	reason   string
}

type auditCapture struct {
	session wsaudit.Session
	harness string
	source  string
	sink    wsaudit.Sink
	sourced wsaudit.SourcedSink
	adapter auditAdapter
	log     io.Writer
	wait    time.Duration

	in      chan auditItem
	refused atomic.Uint64
	// over holds the control markers the queue had no room for, coalesced
	// to the newest connection's (Conn grows with every attach, and a newer
	// connection's attach supersedes every older one): its attach and its
	// detach. overWake tells the capture goroutine; a marker goes here, not
	// to the queue, while over holds any, so markers never pass each other.
	overMu   sync.Mutex
	over     overflowControl
	overWake chan struct{}
	stop     chan struct{}
	stopOnce sync.Once
	done     chan struct{}

	// The fields below are the capture goroutine's.
	nextSeq  uint64
	recovery wsaudit.Recovery
	// localSeq numbers the lines of a process without a holder.
	localSeq uint64
	// teeOn is set while a holder connection that negotiated the audit
	// channel is attached and its tee stream has not been closed; waitFrom
	// is the first record sequence that connection delivered live.
	teeOn    bool
	waitFrom uint64
	// conn is the attached connection (wsholder.Event.Conn) and live
	// whether it still is: a fence on a live connection whose tee stream a
	// timeout closed opens it again.
	conn uint64
	live bool
	// detachAfter is an overflowed detach whose connection's attach had not
	// been handled yet: the attach was still queued (it took the reserve's
	// last slot), so the detach waits until the queue has drained past it
	// (applyDetachAfter), never applying before its attach.
	detachAfter *auditItem
	// late counts tee lines lost for arriving after their interval closed,
	// recorded as one loss before the next other item (noteLate).
	late uint64
	// partialAnchor and partialIndex are the interval a closed tee stream
	// left open and the last index it mapped, so the interval continuing on
	// the next connection maps only the rest.
	partialAnchor uint64
	partialIndex  uint64
	// through is the last kept sequence a fence released; fenced the anchor
	// of the last interval a fence closed (valid with anyFence).
	through  uint64
	fenced   uint64
	anyFence bool
	// pending are the tee lines of intervals no fence closed yet, by anchor;
	// ready the fenced intervals not mapped yet (their anchor line has not
	// been), in anchor order; held the kept lines waiting for a fence or for
	// an earlier interval. stuckAt is when they last stopped draining.
	pending map[uint64][]wsholder.Event
	// pendingBytes is the tee data held in pending.
	pendingBytes int
	ready        []auditInterval
	held         []auditItem
	stuckAt      time.Time
	// boundary is the highest kept sequence mapped, finalized the last one
	// reported to the sink.
	boundary  uint64
	finalized uint64
	losses    uint64
	logged    bool
}

// newAuditCapture starts capture for one process; nil when tap or adapter
// is nil.
func newAuditCapture(tap *auditTap, harness, source string, adapter auditAdapter) *auditCapture {
	if tap == nil || tap.Sink == nil || adapter == nil || !tap.Session.DaemonHosted() {
		return nil
	}
	c := &auditCapture{
		session: tap.Session, harness: harness, source: source, sink: tap.Sink, adapter: adapter,
		log: tap.Log, wait: auditFenceWait,
		in: make(chan auditItem, auditQueueItems+auditControlItems), overWake: make(chan struct{}, 1),
		stop: make(chan struct{}), done: make(chan struct{}),
		pending: map[uint64][]wsholder.Event{}, nextSeq: 1,
	}
	if c.log == nil {
		c.log = io.Discard
	}
	if s, ok := tap.Sink.(wsaudit.SourcedSink); ok {
		c.sourced = s
		c.recovery = s.Recover(c.session.ID, source)
		c.nextSeq = c.recovery.MaxSeq + 1
	}
	go c.run()
	return c
}

// line hands one output line to capture: seq is its holder record sequence
// (0 for a process without a holder), replay set for a line an earlier
// daemon applied. It never blocks.
func (c *auditCapture) line(data []byte, seq uint64, replay bool) {
	c.enqueue(auditItem{kind: auditItemLine, data: bytes.Clone(data), seq: seq, replay: replay, at: time.Now()})
}

// holderEvent hands one audit channel event to capture. It never blocks.
func (c *auditCapture) holderEvent(ev wsholder.Event) {
	it := auditItem{ev: ev, at: time.Now()}
	switch ev.Event {
	case wsholder.EventAudit:
		it.kind = auditItemTee
		if ev.At > 0 {
			it.at = time.Unix(0, ev.At)
		}
	case wsholder.EventAuditFence:
		it.kind = auditItemFence
	case wsholder.EventAuditAttached:
		it.kind = auditItemAttached
	case wsholder.EventAuditDetached:
		it.kind = auditItemDetached
	default:
		return
	}
	if it.kind == auditItemAttached || it.kind == auditItemDetached {
		c.enqueueControl(it)
		return
	}
	c.enqueue(it)
}

// enqueue queues a line, tee, or fence, or counts it refused when the queue
// holds auditQueueItems (the room past it is the control markers'). The
// length check races only with the process's other producer, so the markers
// keep nearly all of their room.
func (c *auditCapture) enqueue(it auditItem) {
	select {
	case <-c.stop:
		return
	default:
	}
	if len(c.in) >= auditQueueItems {
		c.refused.Add(1)
		return
	}
	select {
	case c.in <- it:
	default:
		c.refused.Add(1)
	}
}

// overflowControl is the coalesced control markers past the reserve: the
// newest connection's attach and detach (nil when none came).
type overflowControl struct {
	conn             uint64
	attach, detached *auditItem
}

func (o *overflowControl) empty() bool { return o.attach == nil && o.detached == nil }

// enqueueControl queues an attach or detach marker in order with the items
// before it. It never blocks: once the control room is exhausted too (more
// connections opening than it holds while capture is stalled), the marker
// coalesces into over, which capture applies ahead of the queue as a resync.
func (c *auditCapture) enqueueControl(it auditItem) {
	select {
	case <-c.stop:
		return
	default:
	}
	c.overMu.Lock()
	defer c.overMu.Unlock()
	if c.over.empty() {
		select {
		case c.in <- it:
			return
		default:
		}
	}
	o := &c.over
	switch conn := it.ev.Conn; {
	case o.empty() || conn > o.conn:
		*o = overflowControl{conn: conn}
	case conn < o.conn:
		return // an older connection's marker: the newer attach supersedes it
	}
	if it.kind == auditItemAttached {
		o.attach = &it
	} else {
		o.detached = &it
	}
	select {
	case c.overWake <- struct{}{}:
	default:
	}
}

// takeOverflow applies the coalesced control markers, if any, recording
// the resync as one loss: the items of connections whose markers coalesced
// away, and those queued ahead of the markers, map as late or are ignored.
func (c *auditCapture) takeOverflow() {
	c.overMu.Lock()
	o := c.over
	c.over = overflowControl{}
	c.overMu.Unlock()
	if o.empty() {
		return
	}
	c.noteLate()
	c.loss(0, wsaudit.LossQueueFull, c.boundary)
	if o.attach != nil {
		c.handle(*o.attach)
	}
	switch {
	case o.detached == nil:
	case o.attach == nil && o.detached.ev.Conn > c.conn:
		// Its attach is still queued: an attach is never dropped, and one
		// that overflowed would be here beside it.
		c.detachAfter = o.detached
	default:
		c.handle(*o.detached)
	}
}

// applyDetachAfter applies a deferred detach once the queue is empty: every
// item queued before the detach overflowed, its attach and that
// connection's tee lines and fences among them, has been handled. A newer
// connection's attach handled meanwhile has already ended it.
func (c *auditCapture) applyDetachAfter() {
	if d := c.detachAfter; d != nil && len(c.in) == 0 && d.ev.Conn <= c.conn {
		c.detachAfter = nil
		c.handle(*d)
	}
}

// close ends capture once the queued items are mapped; it does not wait.
func (c *auditCapture) close() {
	c.stopOnce.Do(func() { close(c.stop) })
}

func (c *auditCapture) run() {
	defer close(c.done)
	for {
		c.takeOverflow()
		var timeout <-chan time.Time
		if !c.stuckAt.IsZero() {
			timeout = time.After(max(c.wait-time.Since(c.stuckAt), 0))
		}
		select {
		case it := <-c.in:
			c.takeOverflow()
			c.handle(it)
			c.idle()
		case <-c.overWake:
			c.takeOverflow()
			c.idle()
		case <-timeout:
			c.closeTee(wsaudit.LossFenceTimeout)
			c.idle()
		case <-c.stop:
		drain:
			for {
				c.takeOverflow()
				select {
				case it := <-c.in:
					c.handle(it)
				default:
					break drain
				}
			}
			c.applyDetachAfter()
			c.noteRefused()
			c.noteLate()
			c.closeTee(wsaudit.LossStreamEnd)
			at := time.Now()
			// The assemblies still open sit at the last kept line mapped.
			c.emit(c.adapter.flush(at), true, nil, at, wsaudit.SourceID{Source: c.source, Seq: c.boundary}, "end."+strconv.FormatUint(c.boundary, 10))
			c.finalize()
			return
		}
	}
}

// idle runs when the queue may be empty: it records refused items and
// reports the finalized boundary once per burst.
func (c *auditCapture) idle() {
	c.noteRefused()
	c.applyDetachAfter()
	if len(c.in) == 0 {
		c.finalize()
	}
}

func (c *auditCapture) noteRefused() {
	if n := c.refused.Swap(0); n > 0 {
		c.loss(n, wsaudit.LossQueueFull, c.boundary)
	}
}

// noteLate records the tee lines counted late since the last item as one
// loss, before the next item maps.
func (c *auditCapture) noteLate() {
	if c.late > 0 {
		c.loss(c.late, wsaudit.LossLate, c.boundary)
		c.late = 0
	}
}

func (c *auditCapture) finalize() {
	if c.sourced == nil || c.boundary <= c.finalized {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditSinkTimeout)
	defer cancel()
	if err := c.sourced.Finalize(ctx, c.session.ID, c.source, c.boundary); err != nil {
		c.logf("finalize %s at %d: %v", c.source, c.boundary, err)
		return
	}
	c.finalized = c.boundary
}

func (c *auditCapture) handle(it auditItem) {
	if it.kind != auditItemTee {
		c.noteLate()
	}
	switch it.kind {
	case auditItemLine:
		if it.seq == 0 {
			c.localSeq++
			it.seq = c.localSeq
			c.mapKept(it)
			return
		}
		if !c.teeOn && len(c.held) == 0 && len(c.ready) == 0 {
			c.mapKept(it)
			return
		}
		c.held = append(c.held, it)
		c.pump()
	case auditItemTee:
		a := it.ev.Anchor
		if !c.teeOn || it.ev.Conn != c.conn || c.anyFence && a <= c.fenced {
			// Its interval is finalized (or no tee stream is open): mapping
			// it now would reorder, so it is lost.
			c.late++
			return
		}
		ev := it.ev
		if ev.At <= 0 {
			ev.At = it.at.UnixNano()
		}
		c.pending[a] = append(c.pending[a], ev)
		c.pendingBytes += len(ev.Data)
		if c.pendingBytes > auditPendingBytes {
			c.closeTee(wsaudit.LossQueueFull)
			return
		}
		c.pump()
	case auditItemFence:
		f := it.ev
		if !c.live || f.Conn != c.conn {
			return
		}
		if f.LostBefore > 0 {
			c.loss(f.LostBefore, wsaudit.LossDropped, f.Anchor)
		}
		if !c.teeOn {
			// A timeout closed the tee stream; this fence opens it again
			// from the interval after it. Its own lines were counted late.
			c.teeOn = true
			c.fenced, c.anyFence = f.Anchor, true
			if !f.Final && f.Through > c.through {
				c.through = f.Through
			}
			if f.Dropped > 0 {
				c.loss(f.Dropped, wsaudit.LossDropped, f.Anchor)
			}
			c.pump()
			return
		}
		// An earlier interval whose fence was lost (a full queue on either
		// side) closes unfenced before this one, never after it.
		var lost []uint64
		for a := range c.pending {
			if a < f.Anchor {
				lost = append(lost, a)
			}
		}
		sort.Slice(lost, func(i, j int) bool { return lost[i] < lost[j] })
		for _, a := range lost {
			c.ready = append(c.ready, auditInterval{anchor: a, tees: c.takePending(a), unfenced: true, reason: wsaudit.LossDropped})
		}
		c.ready = append(c.ready, auditInterval{anchor: f.Anchor, tees: c.takePending(f.Anchor), sent: f.Sent, dropped: f.Dropped})
		c.fenced, c.anyFence = f.Anchor, true
		if !f.Final && f.Through > c.through {
			c.through = f.Through
		}
		c.pump()
	case auditItemAttached:
		if c.live && it.ev.Conn == c.conn || it.ev.Conn < c.conn {
			// The same attach, read from the manager and delivered too, or an
			// older connection's, delivered after its successor's.
			return
		}
		if c.teeOn {
			c.closeTee(wsaudit.LossDisconnect)
		}
		c.teeOn, c.live, c.conn, c.waitFrom = true, true, it.ev.Conn, it.ev.Next
		c.through = 0
		if it.ev.Next > 0 {
			c.through = it.ev.Next - 1
		}
		c.anyFence = false
	case auditItemDetached:
		if !c.live || it.ev.Conn != c.conn {
			return // an earlier connection's, delivered after its successor's
		}
		c.live = false
		c.closeTee(wsaudit.LossDisconnect)
	}
}

// takePending removes and returns the pending tee lines of the interval
// after anchor.
func (c *auditCapture) takePending(anchor uint64) []wsholder.Event {
	tees := c.pending[anchor]
	delete(c.pending, anchor)
	for _, t := range tees {
		c.pendingBytes -= len(t.Data)
	}
	return tees
}

// pump maps, in source order, what the fences have released: a ready
// interval once its anchor line is mapped (a fence may reach the daemon
// before the kept lines it follows, the holder sending audit messages ahead
// of ring records), and a held kept line once a fence released it and no
// earlier interval waits. What stays blocked starts the bounded wait.
func (c *auditCapture) pump() {
	progressed := false
	for {
		if len(c.ready) > 0 && (c.ready[0].anchor == 0 || c.ready[0].anchor <= c.boundary) {
			c.mapInterval(c.ready[0])
			c.ready = c.ready[1:]
			progressed = true
			continue
		}
		if len(c.held) > 0 {
			s := c.held[0].seq
			fenced := !c.teeOn || s < c.waitFrom || s <= c.through
			earlier := len(c.ready) > 0 && c.ready[0].anchor < s
			if fenced && !earlier {
				it := c.held[0]
				c.held = c.held[1:]
				c.mapKept(it)
				progressed = true
				continue
			}
		}
		break
	}
	switch {
	case len(c.held) == 0 && len(c.ready) == 0:
		c.held, c.ready, c.stuckAt = nil, nil, time.Time{}
	case progressed || c.stuckAt.IsZero():
		c.stuckAt = time.Now()
	}
}

// mapInterval maps an interval's tee lines in index order and records the
// indices that never arrived (dropped by the holder, or by this queue) as
// loss where they fell; an unfenced interval is marked incomplete for
// reason after its lines.
func (c *auditCapture) mapInterval(iv auditInterval) {
	tees := iv.tees
	sort.SliceStable(tees, func(i, j int) bool { return tees[i].Index < tees[j].Index })
	next := uint64(1)
	if iv.anchor == c.partialAnchor && c.partialIndex > 0 {
		// The interval continues one a closed tee stream mapped in part.
		next = c.partialIndex + 1
	}
	c.partialAnchor, c.partialIndex = 0, 0
	for _, t := range tees {
		if t.Index < next {
			continue // a duplicate index
		}
		if t.Index > next && !iv.unfenced {
			c.loss(t.Index-next, wsaudit.LossDropped, iv.anchor)
		}
		c.mapTee(t)
		next = t.Index + 1
	}
	if iv.unfenced {
		if next > 1 {
			c.partialAnchor, c.partialIndex = iv.anchor, next-1
		}
		c.loss(0, iv.reason, iv.anchor)
		return
	}
	if total := iv.sent + iv.dropped; total >= next {
		c.loss(total-next+1, wsaudit.LossDropped, iv.anchor)
	}
}

// closeTee ends the source's tee stream (a disconnect, a fence that did not
// come in time, the end of the stream): everything still waiting is mapped
// in source order, an unfenced interval's assembly is marked incomplete,
// and tee lines arriving later are lost, never mapped late.
func (c *auditCapture) closeTee(reason string) {
	wasOn := c.teeOn
	stuck := len(c.held) > 0 || len(c.ready) > 0
	unfenced := len(c.pending) > 0
	for a, tees := range c.pending {
		c.ready = append(c.ready, auditInterval{anchor: a, tees: tees, unfenced: true, reason: reason})
	}
	clear(c.pending)
	c.pendingBytes = 0
	sort.SliceStable(c.ready, func(i, j int) bool { return c.ready[i].anchor < c.ready[j].anchor })
	c.teeOn = false
	// A tee line sits after the kept line it is anchored on.
	for len(c.held) > 0 || len(c.ready) > 0 {
		if len(c.ready) > 0 && (len(c.held) == 0 || c.ready[0].anchor < c.held[0].seq) {
			c.mapInterval(c.ready[0])
			c.ready = c.ready[1:]
			continue
		}
		it := c.held[0]
		c.held = c.held[1:]
		c.mapKept(it)
	}
	c.held, c.ready, c.stuckAt = nil, nil, time.Time{}
	if wasOn && stuck && !unfenced && reason == wsaudit.LossFenceTimeout {
		c.loss(0, reason, c.boundary)
	}
}

func (c *auditCapture) mapKept(it auditItem) {
	recognized, carried, evs := c.adapter.mapLine(it.data, it.at)
	rec := wsaudit.SourceID{Source: c.source, Seq: it.seq}
	c.emitCarried(carried, it.at, rec, "asm."+strconv.FormatUint(it.seq, 10))
	c.emit(evs, recognized, it.data, it.at, rec, "")
	if it.seq > c.boundary {
		c.boundary = it.seq
	}
}

func (c *auditCapture) mapTee(t wsholder.Event) {
	at := time.Unix(0, t.At)
	recognized, carried, evs := c.adapter.mapLine(bytes.TrimRight(t.Data, "\r\n"), at)
	rec := wsaudit.SourceID{Source: c.source, Tee: true, Anchor: t.Anchor, Index: t.Index}
	c.emitCarried(carried, at, rec, fmt.Sprintf("asm.t%d.%d", t.Anchor, t.Index))
	c.emit(evs, recognized, t.Data, at, rec, "")
}

// emitCarried appends the assemblies source record rec ended under their
// own key, apart from rec's identity: they come from tee'd lines, and rec's
// own events keep ordinals that do not depend on them.
func (c *auditCapture) emitCarried(evs []auditEvent, at time.Time, rec wsaudit.SourceID, key string) {
	if len(evs) > 0 {
		c.emit(evs, true, nil, at, rec, key)
	}
}

// emit filters the events source record rec yields through the capture
// policy and appends each, its slot the ordinal, under rec's identity or,
// with key set, under that key; an event's own key replaces either. Every
// one keeps rec as its position for the recovery rule.
func (c *auditCapture) emit(mapped []auditEvent, recognized bool, raw []byte, at time.Time, rec wsaudit.SourceID, key string) {
	base := rec
	if key != "" {
		base = wsaudit.SourceID{Source: c.source, Key: key}
	}
	if !recognized {
		for i, ev := range wsaudit.CaptureLine(c.session, c.harness, at, string(bytes.TrimRight(raw, "\r\n")), false, nil) {
			eid := base
			eid.Ordinal = i
			c.append(eid, rec, ev)
		}
		return
	}
	for _, m := range mapped {
		for _, ev := range wsaudit.FilterEvents(c.session, []wsaudit.Envelope{m.ev}) {
			eid := base
			eid.Ordinal = m.slot
			if m.key != "" {
				eid = wsaudit.SourceID{Source: c.source, Key: m.key}
			}
			c.append(eid, rec, ev)
		}
	}
}

// loss records n lost source lines at pos: on the open assembled event when
// one spans it, else as a standalone loss marker.
func (c *auditCapture) loss(n uint64, reason string, pos uint64) {
	if c.adapter.markLoss(n, reason) {
		return
	}
	ev, err := wsaudit.NewEnvelope(c.session.ID, c.harness, time.Now(), wsaudit.KindSystem)
	if err != nil {
		return
	}
	ev.Subkind = wsaudit.SubkindLoss
	ev.Meta = map[string]any{wsaudit.MetaLossReason: reason}
	if n > 0 {
		ev.Meta[wsaudit.MetaLost] = n
	}
	c.losses++
	if !wsaudit.ShouldCapture(c.session, ev.Kind) {
		return
	}
	// A loss marker is a record of its own, never refused as late.
	id := wsaudit.SourceID{Source: c.source, Key: fmt.Sprintf("loss.%d.%d", pos, c.losses)}
	c.append(id, id, ev)
}

// append writes one envelope under id, applying the recovery rule: an
// identity already retained is skipped, and one whose source record rec
// sits at or below the finalized boundary of an earlier capture and is not
// retained is recorded as loss, never appended late. The rule goes by rec,
// not id, so a keyed event (a carried assembly, a Codex call) is held to
// its record's place too: appended late it would land after events it
// precedes (its result, a compaction cut) with a fresh seq.
func (c *auditCapture) append(id, rec wsaudit.SourceID, ev wsaudit.Envelope) {
	if ev.ID == "" {
		ev.ID = id.String()
	}
	ctx, cancel := context.WithTimeout(context.Background(), auditSinkTimeout)
	defer cancel()
	if c.sourced != nil {
		if c.sourced.Retained(c.session.ID, id) {
			return
		}
		if b := c.recovery.Boundary; b > 0 && (rec.Tee && rec.Anchor < b || !rec.Tee && rec.Key == "" && rec.Seq <= b) {
			if err := c.sourced.RecordLoss(ctx, c.session.ID, id, wsaudit.LossAppendFailed); err != nil {
				c.logf("record loss of %s: %v", id, err)
			}
			return
		}
	}
	ev.Seq = c.nextSeq
	var err error
	if c.sourced != nil {
		err = c.sourced.AppendSourced(ctx, id, ev)
	} else {
		err = c.sink.Append(ctx, ev)
	}
	if err != nil && !errors.Is(err, wsaudit.ErrDuplicateSource) {
		c.logf("append %s: %v (dropped)", id, err)
		return
	}
	if err == nil {
		c.nextSeq++
	}
}

// logf logs a capture's first sink failure only, so a failing sink does not
// flood the daemon log.
func (c *auditCapture) logf(format string, args ...any) {
	if c.logged {
		return
	}
	c.logged = true
	fmt.Fprintf(c.log, "audit %s %s: "+format+" (further audit errors for this process are not logged)\n", append([]any{c.session.ID, c.source}, args...)...)
}
