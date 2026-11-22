package wsholder

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// Cause is why the daemon is not attached to a holder.
type Cause string

const (
	// CauseNoAnswer: the holder's endpoint does not answer.
	CauseNoAnswer Cause = "no_answer"
	// CauseProtocol: the holder speaks a protocol this daemon does not
	// (older than the version before the daemon's, or newer than it).
	CauseProtocol Cause = "protocol"
	// CauseToken: the holder refused the descriptor's token.
	CauseToken Cause = "token"
)

// AttachError is a failed attach with its cause.
type AttachError struct {
	Cause Cause
	// Protocol is the version the holder reported, 0 when it reported none.
	Protocol int
	Err      error
}

func (e *AttachError) Error() string {
	return fmt.Sprintf("holder attach (%s): %v", e.Cause, e.Err)
}

func (e *AttachError) Unwrap() error { return e.Err }

// OpError is a holder's refusal of an operation.
type OpError struct {
	Code    string
	Message string
}

func (e *OpError) Error() string { return "holder: " + e.Code + ": " + e.Message }

// ErrDetached is returned by an operation on a connection that has closed.
var ErrDetached = errors.New("holder: not attached")

const (
	attachDialTimeout = 2 * time.Second
	attachStepTimeout = 5 * time.Second
	callTimeout       = 10 * time.Second
)

// ChildStatus is a holder's child: State is "none", "running", or "exited".
type ChildStatus struct {
	State    string `json:"state"`
	PID      int    `json:"pid,omitempty"`
	Mode     string `json:"mode,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
	Signal   *int   `json:"signal,omitempty"`
	// InputClosed reports, for a running pipe child, whether an eof closed
	// its stdin. A holder that predates it sends none (nil): unknown.
	InputClosed *bool `json:"input_closed,omitempty"`
}

// Event is one line the holder sends that answers no request: a record of
// the ring ("output", "exit"), a "gap" (the records From..To-1 have left
// the ring), or "detached" (a newer attach took over).
type Event struct {
	Event string `json:"event"`
	Seq   uint64 `json:"seq,omitempty"`
	// Stream is "stdout", "stderr", or "pty"; Data is the record's bytes.
	Stream  string `json:"stream,omitempty"`
	Data    []byte `json:"data,omitempty"`
	Partial bool   `json:"partial,omitempty"`
	// Offset and CombinedOffset are an output record's absolute byte
	// offsets: of Data in its own stream, and in all of the child's output
	// in arrival order. A holder that predates them sends neither.
	Offset         *int64 `json:"offset,omitempty"`
	CombinedOffset *int64 `json:"combined_offset,omitempty"`
	// ExitCode and Signal are an exit record's status.
	ExitCode *int `json:"exit_code,omitempty"`
	Signal   *int `json:"signal,omitempty"`
	// StreamBytes and OutputBytes are an exit record's output totals, by
	// stream name and in all, so a replay with no output record left still
	// knows them. A holder that predates them sends neither.
	StreamBytes map[string]int64 `json:"stream_bytes,omitempty"`
	OutputBytes *int64           `json:"output_bytes,omitempty"`
	From        uint64           `json:"from,omitempty"`
	To          uint64           `json:"to,omitempty"`
	Reason      string           `json:"reason,omitempty"`
}

// SpawnSpec is the child a holder starts. Env, when not nil, is the child's
// whole environment. Mode is "pipe" or "pty"; Rows and Cols are a PTY's
// initial size.
type SpawnSpec struct {
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd,omitempty"`
	Env  map[string]string `json:"env,omitempty"`
	Mode string            `json:"mode"`
	Rows int               `json:"rows,omitempty"`
	Cols int               `json:"cols,omitempty"`
	// Input is written to the child's input first. Up to inputChunk bytes
	// travel in the spawn request itself, which the holder queues before
	// its input writer runs, so they reach even a child that exits at
	// once; the rest follows in input operations (see Client.Spawn).
	Input []byte `json:"input,omitempty"`
	// InputEOF closes the child's input after Input (a pipe child's stdin;
	// an end-of-file key on a PTY), so a program that reads to EOF
	// finishes without a later Input call. With no Input it is an
	// immediate EOF.
	InputEOF bool `json:"input_eof,omitempty"`
}

// AttachInfo is what a holder reports when an attach succeeds.
type AttachInfo struct {
	// Protocol is the version spoken on this connection.
	Protocol int         `json:"protocol"`
	Child    ChildStatus `json:"child"`
	FirstSeq uint64      `json:"first_seq"`
	NextSeq  uint64      `json:"next_seq"`
}

// reply is a holder's answer to one request.
type reply struct {
	Reply    *int64      `json:"reply"`
	OK       bool        `json:"ok"`
	Error    string      `json:"error"`
	Message  string      `json:"message"`
	Protocol int         `json:"protocol"`
	Child    ChildStatus `json:"child"`
	FirstSeq uint64      `json:"first_seq"`
	NextSeq  uint64      `json:"next_seq"`
}

// Client is one attach connection to a holder. A holder serves one at a
// time: a newer attach closes this one.
type Client struct {
	conn    net.Conn
	onEvent func(Event)

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int64
	waiters map[int64]chan reply
	done    chan struct{}
}

// Attach connects to the holder desc names, authenticates, and takes over
// its attach slot. onEvent receives every event on the connection's reader
// goroutine, in order; it may be nil. A failure is an *AttachError.
func Attach(ctx context.Context, desc Descriptor, onEvent func(Event)) (*Client, AttachInfo, error) {
	fail := func(cause Cause, protocol int, err error) (*Client, AttachInfo, error) {
		return nil, AttachInfo{}, &AttachError{Cause: cause, Protocol: protocol, Err: err}
	}
	dialer := net.Dialer{Timeout: attachDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(desc.Port)))
	if err != nil {
		return fail(CauseNoAnswer, 0, err)
	}
	reader := bufio.NewReaderSize(conn, 64<<10)
	_ = conn.SetDeadline(time.Now().Add(attachStepTimeout))
	var hello struct {
		Hello    string `json:"hello"`
		Protocol int    `json:"protocol"`
	}
	line, err := reader.ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &hello)
	}
	if err == nil && hello.Hello != "ws-holder" {
		err = errors.New("the endpoint is not a holder")
	}
	if err != nil {
		_ = conn.Close()
		return fail(CauseNoAnswer, 0, err)
	}
	if !supportedProtocol(hello.Protocol) {
		_ = conn.Close()
		return fail(CauseProtocol, hello.Protocol, fmt.Errorf("the holder speaks protocol %d, this daemon %d and the one before it", hello.Protocol, protocolVersion))
	}
	request, _ := json.Marshal(map[string]any{"op": "attach", "id": 0, "token": desc.Token, "protocol": hello.Protocol})
	if _, err := conn.Write(append(request, '\n')); err != nil {
		_ = conn.Close()
		return fail(CauseNoAnswer, hello.Protocol, err)
	}
	var answer reply
	line, err = reader.ReadBytes('\n')
	if err == nil {
		err = json.Unmarshal(line, &answer)
	}
	if err != nil {
		_ = conn.Close()
		return fail(CauseNoAnswer, hello.Protocol, err)
	}
	if !answer.OK {
		_ = conn.Close()
		cause := CauseNoAnswer
		switch answer.Error {
		case "token":
			cause = CauseToken
		case "protocol":
			cause = CauseProtocol
		}
		return fail(cause, hello.Protocol, errors.New(answer.Message))
	}
	_ = conn.SetDeadline(time.Time{})
	c := &Client{conn: conn, onEvent: onEvent, waiters: map[int64]chan reply{}, done: make(chan struct{})}
	go c.read(reader)
	return c, AttachInfo{Protocol: answer.Protocol, Child: answer.Child, FirstSeq: answer.FirstSeq, NextSeq: answer.NextSeq}, nil
}

// read delivers replies to their callers and events to onEvent until the
// connection ends.
func (c *Client) read(reader *bufio.Reader) {
	defer func() {
		_ = c.conn.Close()
		c.mu.Lock()
		close(c.done)
		c.mu.Unlock()
	}()
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return
		}
		var probe struct {
			Reply *int64 `json:"reply"`
			Event string `json:"event"`
		}
		if json.Unmarshal(line, &probe) != nil {
			continue
		}
		if probe.Event != "" {
			var ev Event
			if json.Unmarshal(line, &ev) == nil && c.onEvent != nil {
				c.onEvent(ev)
			}
			continue
		}
		if probe.Reply == nil {
			continue
		}
		var answer reply
		if json.Unmarshal(line, &answer) != nil {
			continue
		}
		c.mu.Lock()
		ch := c.waiters[*probe.Reply]
		delete(c.waiters, *probe.Reply)
		c.mu.Unlock()
		if ch != nil {
			ch <- answer
		}
	}
}

// Done is closed once the connection has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close ends the connection; the holder and its child keep running.
func (c *Client) Close() { _ = c.conn.Close() }

func (c *Client) call(op string, fields map[string]any) (reply, error) {
	ch := make(chan reply, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return reply{}, ErrDetached
	default:
	}
	c.nextID++
	id := c.nextID
	c.waiters[id] = ch
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
	}
	request := map[string]any{"op": op, "id": id}
	for k, v := range fields {
		request[k] = v
	}
	raw, err := json.Marshal(request)
	if err != nil {
		forget()
		return reply{}, err
	}
	c.writeMu.Lock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(callTimeout))
	_, err = c.conn.Write(append(raw, '\n'))
	c.writeMu.Unlock()
	if err != nil {
		forget()
		_ = c.conn.Close()
		return reply{}, ErrDetached
	}
	timer := time.NewTimer(callTimeout)
	defer timer.Stop()
	select {
	case answer := <-ch:
		if !answer.OK {
			return answer, &OpError{Code: answer.Error, Message: answer.Message}
		}
		return answer, nil
	case <-c.done:
		forget()
		return reply{}, ErrDetached
	case <-timer.C:
		forget()
		return reply{}, fmt.Errorf("holder: %s was not answered within %s", op, callTimeout)
	}
}

// Spawn starts the holder's child. A holder has one child in its lifetime.
//
// spec.Input and spec.InputEOF ride in the spawn request, atomic with the
// start, when Input fits in one inputChunk. Longer input sends its first
// chunk with the spawn and the rest through input operations, the EOF on
// the last; only that remainder can race the child's exit or a daemon
// crash. A child that has already exited when the remainder arrives
// (no_child) left its input unread, as a direct child's broken stdin pipe
// would, so Spawn still reports the start; the exit record follows.
func (c *Client) Spawn(spec SpawnSpec) (ChildStatus, error) {
	fields := map[string]any{"argv": spec.Argv, "mode": spec.Mode}
	if spec.Cwd != "" {
		fields["cwd"] = spec.Cwd
	}
	if spec.Env != nil {
		fields["env"] = spec.Env
	}
	if spec.Rows > 0 || spec.Cols > 0 {
		fields["rows"], fields["cols"] = spec.Rows, spec.Cols
	}
	first, rest := spec.Input, []byte(nil)
	if n := chunkEnd(first, utf8.Valid(first)); n < len(first) {
		first, rest = first[:n], first[n:]
	}
	if len(first) > 0 {
		fields["input"] = first
	}
	if spec.InputEOF && len(rest) == 0 {
		fields["input_eof"] = true
	}
	answer, err := c.call("spawn", fields)
	if err != nil || len(rest) == 0 {
		return answer.Child, err
	}
	if err := c.Input(rest, spec.InputEOF); err != nil {
		var opErr *OpError
		if errors.As(err, &opErr) && opErr.Code == "no_child" {
			return answer.Child, nil
		}
		return answer.Child, fmt.Errorf("holder: the child started, but its input was not delivered: %w", err)
	}
	return answer.Child, nil
}

// inputChunk bounds one input operation's bytes, well under the holder's
// request line limit once encoded.
const inputChunk = 256 << 10

// chunkEnd is the length of data's first input chunk: all of it within
// inputChunk, else inputChunk bytes, moved back to the start of a rune
// when text is set, so every chunk boundary (and an InputError's Accepted)
// splits valid UTF-8 between runes and the rest stays expressible as text.
func chunkEnd(data []byte, text bool) int {
	if len(data) <= inputChunk {
		return len(data)
	}
	n := inputChunk
	if text {
		for n > inputChunk-utf8.UTFMax && !utf8.RuneStart(data[n]) {
			n--
		}
	}
	return n
}

// InputError is an Input call that did not complete. The holder queued
// the first Accepted bytes of its data, in chunks it acknowledged. Refused
// reports that the holder refused the next chunk (Err is its *OpError), so
// nothing after Accepted was queued and the eof was not; otherwise that
// chunk's delivery, and the eof's, is unknown: the call failed after the
// chunk may have been sent.
type InputError struct {
	Accepted int
	Refused  bool
	Err      error
}

func (e *InputError) Error() string {
	if e.Refused {
		return fmt.Sprintf("the holder took the first %d bytes and refused the rest: %v", e.Accepted, e.Err)
	}
	return fmt.Sprintf("the holder took the first %d bytes; whether the rest arrived is unknown: %v", e.Accepted, e.Err)
}

func (e *InputError) Unwrap() error { return e.Err }

// Input writes data to the child's standard input or PTY. With eof the
// child's input closes after it (a pipe child's stdin; an end-of-file key
// on a PTY). Data longer than one chunk travels in several operations; a
// failure answers an *InputError that says how much the holder took.
func (c *Client) Input(data []byte, eof bool) error {
	accepted := 0
	fail := func(err error) error {
		var opErr *OpError
		return &InputError{Accepted: accepted, Refused: errors.As(err, &opErr), Err: err}
	}
	text := utf8.Valid(data)
	for len(data) > inputChunk {
		n := chunkEnd(data, text)
		if _, err := c.call("input", map[string]any{"data": data[:n]}); err != nil {
			return fail(err)
		}
		data = data[n:]
		accepted += n
	}
	fields := map[string]any{"data": data}
	if eof {
		fields["eof"] = true
	}
	if _, err := c.call("input", fields); err != nil {
		return fail(err)
	}
	return nil
}

// Resize sets a PTY child's size.
func (c *Client) Resize(rows, cols int) error {
	_, err := c.call("resize", map[string]any{"rows": rows, "cols": cols})
	return err
}

// Signal sends the child a signal by name: INT, TERM, KILL, HUP, or QUIT.
func (c *Client) Signal(name string) error {
	_, err := c.call("signal", map[string]any{"name": name})
	return err
}

// Stop asks the holder to end its child and exit. The connection closes
// once the holder has gone.
func (c *Client) Stop() error {
	_, err := c.call("stop", nil)
	return err
}

// Status reads the child's status and the ring's bounds.
func (c *Client) Status() (AttachInfo, error) {
	answer, err := c.call("status", nil)
	return AttachInfo{Child: answer.Child, FirstSeq: answer.FirstSeq, NextSeq: answer.NextSeq}, err
}

// Replay starts the record stream at sequence from; 0 asks for the whole
// ring. When from has already left the ring the stream begins with a gap
// event. It returns the ring's bounds at that moment.
func (c *Client) Replay(from uint64) (first, next uint64, err error) {
	answer, err := c.call("replay", map[string]any{"from": from})
	return answer.FirstSeq, answer.NextSeq, err
}
