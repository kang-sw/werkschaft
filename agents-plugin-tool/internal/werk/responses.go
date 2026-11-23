package werk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// responses.go is the Responses streaming client: POST <base>/responses with
// stream true and store false, the stateless shape OpenRouter and the OpenAI
// SIWC endpoint both accept (the whole history is re-sent each request).
//
// Output items are kept as the raw JSON the provider sent. A reasoning item
// must come back unchanged and in order for the provider to continue a tool
// loop, so the client never decodes an item into a typed form it would have
// to re-encode.
//
// The bearer is asked for at the one place it is sent. It is never logged,
// never part of an error text, and never kept past the request.

// ToolDef is one function tool offered to the model.
type ToolDef struct {
	Name        string
	Description string
	// Parameters is the tool's JSON schema; nil sends an empty object schema.
	Parameters json.RawMessage
}

// Request is one model request.
type Request struct {
	Model        string
	Instructions string
	// Input is the whole conversation so far, as items.
	Input []json.RawMessage
	Tools []ToolDef
	// Effort is the reasoning effort; empty sends no reasoning setting.
	Effort string
}

// Usage is one response's figures.
type Usage struct {
	InputTokens     int64 `json:"input_tokens"`
	OutputTokens    int64 `json:"output_tokens"`
	CachedTokens    int64 `json:"cached_tokens"`
	ReasoningTokens int64 `json:"reasoning_tokens"`
	// CostUSD is the provider's own cost figure, nil when it sent none.
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

// Response is a completed model response.
type Response struct {
	ID string `json:"id"`
	// Output are the response's items in order, as received.
	Output []json.RawMessage `json:"output"`
	Usage  Usage             `json:"usage"`
}

// StreamHandler receives the response's streamed text. Both callbacks are
// optional and run on the reading goroutine.
type StreamHandler struct {
	// TextDelta is an assistant message's text delta.
	TextDelta func(item, delta string)
	// SummaryDelta is a provider-exposed reasoning summary delta. Hidden
	// reasoning is never passed on.
	SummaryDelta func(item, delta string)
}

// Client talks to one Responses endpoint.
type Client struct {
	// BaseURL is the API root ("https://openrouter.ai/api/v1").
	BaseURL string
	// Token returns the credential to send, asked for on every request.
	Token func(ctx context.Context) (string, error)
	HTTP  *http.Client
	// StallTimeout cuts a stream that delivers nothing for this long; zero
	// is DefaultStallTimeout.
	StallTimeout time.Duration
	// HeaderTimeout cuts a request whose response headers do not arrive in
	// this long (a server that accepted the connection and went quiet); zero
	// is DefaultHeaderTimeout. The cut is retryable, like a stalled stream.
	HeaderTimeout time.Duration
}

// DefaultStallTimeout is how long a response stream may stay silent.
const DefaultStallTimeout = 2 * time.Minute

// DefaultHeaderTimeout is how long the response headers may take.
const DefaultHeaderTimeout = 2 * time.Minute

// maxStreamLine bounds one server-sent line: an encrypted reasoning item can
// be large.
const maxStreamLine = 64 << 20

// maxErrorBody bounds the error body read from a refused request.
const maxErrorBody = 64 << 10

// HTTPError is a request the endpoint refused before streaming.
type HTTPError struct {
	Status  int
	Code    string
	Message string
	// RetryAfter is the delay the endpoint asked for (Retry-After); zero
	// when it named none.
	RetryAfter time.Duration
}

func (e *HTTPError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("the model endpoint answered %d: %s", e.Status, msg)
}

// FailedError is a response the endpoint ended with response.failed, an
// error event, or an incomplete status.
type FailedError struct {
	Code    string
	Message string
}

func (e *FailedError) Error() string {
	switch {
	case e.Code != "" && e.Message != "":
		return fmt.Sprintf("the model response failed (%s): %s", e.Code, e.Message)
	case e.Message != "":
		return "the model response failed: " + e.Message
	case e.Code != "":
		return "the model response failed: " + e.Code
	}
	return "the model response failed"
}

// ErrStreamCut marks a response stream that ended, or went silent, before a
// terminal event.
var ErrStreamCut = errors.New("the model response stream was cut before it completed")

// ErrNoCredential marks a request that had no credential to send.
var ErrNoCredential = errors.New("no provider credential")

// TransientError marks an error the caller knows is worth another request
// (the daemon's token route was unreachable, say).
type TransientError struct{ Err error }

func (e *TransientError) Error() string { return e.Err.Error() }
func (e *TransientError) Unwrap() error { return e.Err }

// Retryable reports whether err is worth another request and how long to wait
// before it: delay is the endpoint's own Retry-After when it sent one, else
// zero (the caller's backoff). A refused credential, a bad request and a
// context end are not retryable.
func Retryable(err error) (delay time.Duration, ok bool) {
	var tr *TransientError
	if errors.As(err, &tr) {
		return 0, true
	}
	var h *HTTPError
	if errors.As(err, &h) {
		switch {
		case h.Status == http.StatusTooManyRequests,
			h.Status == http.StatusRequestTimeout,
			h.Status >= 500:
			return h.RetryAfter, true
		}
		return 0, false
	}
	var f *FailedError
	if errors.As(err, &f) {
		switch f.Code {
		case "server_error", "rate_limit_exceeded", "overloaded", "temporarily_unavailable", "timeout":
			return 0, true
		}
		return 0, false
	}
	if errors.Is(err, ErrStreamCut) {
		return 0, true
	}
	return 0, false
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

type toolWire struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
	Strict      bool            `json:"strict"`
}

type requestWire struct {
	Model             string            `json:"model"`
	Instructions      string            `json:"instructions,omitempty"`
	Input             []json.RawMessage `json:"input"`
	Tools             []toolWire        `json:"tools,omitempty"`
	ParallelToolCalls bool              `json:"parallel_tool_calls,omitempty"`
	Stream            bool              `json:"stream"`
	Store             bool              `json:"store"`
	Reasoning         *reasoningWire    `json:"reasoning,omitempty"`
	Include           []string          `json:"include,omitempty"`
}

type reasoningWire struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

var emptyObjectSchema = json.RawMessage(`{"type":"object","properties":{}}`)

// body is the request's JSON.
func (r Request) body() ([]byte, error) {
	w := requestWire{
		Model: r.Model, Instructions: r.Instructions, Input: r.Input,
		Stream: true, Store: false,
	}
	if w.Input == nil {
		w.Input = []json.RawMessage{}
	}
	for _, t := range r.Tools {
		params := t.Parameters
		if len(params) == 0 {
			params = emptyObjectSchema
		}
		w.Tools = append(w.Tools, toolWire{Type: "function", Name: t.Name, Description: t.Description, Parameters: params})
	}
	if len(w.Tools) > 0 {
		w.ParallelToolCalls = true
	}
	if r.Effort != "" {
		// A provider-exposed summary is asked for with the effort; opaque
		// reasoning content comes back for the stateless replay.
		w.Reasoning = &reasoningWire{Effort: r.Effort, Summary: "auto"}
		w.Include = []string{"reasoning.encrypted_content"}
	}
	return json.Marshal(w)
}

// Respond sends req and reads the streamed response to its terminal event.
func (c *Client) Respond(ctx context.Context, req Request, h StreamHandler) (*Response, error) {
	token, err := c.Token(ctx)
	if err != nil {
		return nil, err
	}
	if token == "" {
		return nil, ErrNoCredential
	}
	body, err := req.body()
	if err != nil {
		return nil, err
	}
	stall := c.StallTimeout
	if stall <= 0 {
		stall = DefaultStallTimeout
	}
	rctx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(rctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/responses", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("Authorization", "Bearer "+token)
	secret := token
	token = ""
	// The header watchdog: Do returns only once the headers arrive, so a
	// server that never sends them would hold the request until the context
	// ends. The timer cancels the request; the stream's own watchdog takes
	// over once the headers are in.
	header := c.HeaderTimeout
	if header <= 0 {
		header = DefaultHeaderTimeout
	}
	headerTimer := time.AfterFunc(header, cancel)
	resp, err := c.httpClient().Do(httpReq)
	waited := !headerTimer.Stop()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if waited {
			return nil, fmt.Errorf("%w: no response headers within %s", ErrStreamCut, header)
		}
		// Redacted by construction: a transport error names the URL, never
		// the request headers.
		return nil, fmt.Errorf("%w: %v", ErrStreamCut, Scrub(err.Error(), secret))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, httpFailure(resp, secret, stall, cancel)
	}
	timer := time.AfterFunc(stall, cancel)
	defer timer.Stop()
	out, err := readStream(resp.Body, h, func() { timer.Reset(stall) })
	var failed *FailedError
	if errors.As(err, &failed) {
		failed.Message = Scrub(failed.Message, secret)
		failed.Code = Scrub(failed.Code, secret)
	}
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return out, err
}

// httpFailure reads a refused request into an HTTPError. The body read is
// bounded in size (maxErrorBody) and in time: a server that sends headers and
// then stalls cannot hold the request past the stall deadline. Whatever was
// read by then is used, and the status decides whether the request is
// retried, as for a body that arrived whole (a 429 or 5xx is retried).
func httpFailure(resp *http.Response, secret string, stall time.Duration, cancel func()) error {
	timer := time.AfterFunc(stall, cancel)
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	timer.Stop()
	e := &HTTPError{Status: resp.StatusCode, RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	var wrap struct {
		Error struct {
			Message string          `json:"message"`
			Code    json.RawMessage `json:"code"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &wrap) == nil {
		e.Message = wrap.Error.Message
		if e.Message == "" {
			e.Message = wrap.Message
		}
		e.Code = Scrub(strings.Trim(string(wrap.Error.Code), `"`), secret)
	}
	if e.Message == "" {
		// Masked before it is cut, so a cut cannot leave a key's head.
		e.Message = Scrub(strings.TrimSpace(string(raw)), secret)
		if len(e.Message) > 300 {
			e.Message = e.Message[:300]
		}
	}
	e.Message = Scrub(e.Message, secret)
	return e
}

// parseRetryAfter reads a Retry-After value: whole seconds, or an HTTP date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseFloat(v, 64); err == nil {
		if n < 0 {
			return 0
		}
		return time.Duration(n * float64(time.Second))
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

// streamState assembles one response from its events.
type streamState struct {
	items map[int]json.RawMessage
	order []int
	// args accumulates a function call's argument deltas by item id, for a
	// provider whose done item leaves them out.
	args map[string]*strings.Builder
}

func readStream(r io.Reader, h StreamHandler, touch func()) (*Response, error) {
	st := &streamState{items: map[int]json.RawMessage{}, args: map[string]*strings.Builder{}}
	br := bufio.NewReaderSize(r, 64<<10)
	var event string
	var data bytes.Buffer
	flush := func() (*Response, bool, error) {
		defer data.Reset()
		if data.Len() == 0 {
			event = ""
			return nil, false, nil
		}
		ev := event
		event = ""
		return st.handle(ev, bytes.TrimSpace(data.Bytes()), h)
	}
	for {
		line, err := readLine(br)
		if len(line) > 0 || err == nil {
			touch()
		}
		if err != nil && len(line) == 0 {
			if resp, done, ferr := flush(); done {
				return resp, ferr
			}
			if errors.Is(err, io.EOF) {
				return nil, ErrStreamCut
			}
			return nil, fmt.Errorf("%w: %v", ErrStreamCut, err)
		}
		text := strings.TrimRight(string(line), "\r\n")
		switch {
		case text == "":
			if resp, done, ferr := flush(); done {
				return resp, ferr
			}
		case strings.HasPrefix(text, ":"):
		case strings.HasPrefix(text, "event:"):
			event = strings.TrimSpace(strings.TrimPrefix(text, "event:"))
		case strings.HasPrefix(text, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimPrefix(strings.TrimPrefix(text, "data:"), " "))
		}
		if err != nil {
			if resp, done, ferr := flush(); done {
				return resp, ferr
			}
			if errors.Is(err, io.EOF) {
				return nil, ErrStreamCut
			}
			return nil, fmt.Errorf("%w: %v", ErrStreamCut, err)
		}
	}
}

// readLine reads one line of any length up to maxStreamLine.
func readLine(br *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > maxStreamLine {
			return nil, fmt.Errorf("a stream line exceeds %d bytes", maxStreamLine)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return out, err
	}
}

// handle takes one event. done is true when the event ended the stream.
func (s *streamState) handle(event string, data []byte, h StreamHandler) (*Response, bool, error) {
	if string(data) == "[DONE]" {
		return nil, true, ErrStreamCut
	}
	var head struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(data, &head) != nil {
		// A line that is not JSON is not an event of this protocol.
		return nil, false, nil
	}
	typ := head.Type
	if typ == "" {
		typ = event
	}
	switch typ {
	case "response.output_text.delta":
		var d struct {
			ItemID string `json:"item_id"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(data, &d) == nil && h.TextDelta != nil && d.Delta != "" {
			h.TextDelta(d.ItemID, d.Delta)
		}
	case "response.reasoning_summary_text.delta":
		var d struct {
			ItemID string `json:"item_id"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(data, &d) == nil && h.SummaryDelta != nil && d.Delta != "" {
			h.SummaryDelta(d.ItemID, d.Delta)
		}
	case "response.function_call_arguments.delta":
		var d struct {
			ItemID string `json:"item_id"`
			Delta  string `json:"delta"`
		}
		if json.Unmarshal(data, &d) == nil {
			b := s.args[d.ItemID]
			if b == nil {
				b = &strings.Builder{}
				s.args[d.ItemID] = b
			}
			b.WriteString(d.Delta)
		}
	case "response.function_call_arguments.done":
		var d struct {
			ItemID    string `json:"item_id"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(data, &d) == nil && d.Arguments != "" {
			b := &strings.Builder{}
			b.WriteString(d.Arguments)
			s.args[d.ItemID] = b
		}
	case "response.output_item.done":
		var d struct {
			OutputIndex int             `json:"output_index"`
			Item        json.RawMessage `json:"item"`
		}
		if json.Unmarshal(data, &d) == nil && len(d.Item) > 0 {
			if _, seen := s.items[d.OutputIndex]; !seen {
				s.order = append(s.order, d.OutputIndex)
			}
			s.items[d.OutputIndex] = d.Item
		}
	case "response.completed":
		resp, err := s.completed(data)
		return resp, true, err
	case "response.incomplete":
		var d struct {
			Response struct {
				IncompleteDetails struct {
					Reason string `json:"reason"`
				} `json:"incomplete_details"`
			} `json:"response"`
		}
		_ = json.Unmarshal(data, &d)
		reason := d.Response.IncompleteDetails.Reason
		if reason == "" {
			reason = "incomplete"
		}
		return nil, true, &FailedError{Code: "incomplete", Message: "the response ended incomplete: " + reason}
	case "response.failed":
		var d struct {
			Response struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			} `json:"response"`
		}
		_ = json.Unmarshal(data, &d)
		return nil, true, &FailedError{Code: d.Response.Error.Code, Message: d.Response.Error.Message}
	case "error":
		var d struct {
			Code    json.RawMessage `json:"code"`
			Message string          `json:"message"`
			Error   struct {
				Code    json.RawMessage `json:"code"`
				Message string          `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &d)
		code, msg := strings.Trim(string(d.Code), `"`), d.Message
		if msg == "" {
			code, msg = strings.Trim(string(d.Error.Code), `"`), d.Error.Message
		}
		if code == "null" {
			code = ""
		}
		return nil, true, &FailedError{Code: code, Message: msg}
	}
	return nil, false, nil
}

type usageWire struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int64 `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	Cost *float64 `json:"cost"`
}

func (s *streamState) completed(data []byte) (*Response, error) {
	var d struct {
		Response struct {
			ID     string            `json:"id"`
			Output []json.RawMessage `json:"output"`
			Usage  *usageWire        `json:"usage"`
		} `json:"response"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, &FailedError{Code: "bad_response", Message: "the completed response is not readable"}
	}
	resp := &Response{ID: d.Response.ID, Output: d.Response.Output}
	if len(resp.Output) == 0 {
		for _, idx := range s.sortedIndexes() {
			resp.Output = append(resp.Output, s.items[idx])
		}
	}
	resp.Output = s.fillArguments(resp.Output)
	if u := d.Response.Usage; u != nil {
		resp.Usage = Usage{
			InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
			CachedTokens: u.InputTokensDetails.CachedTokens, ReasoningTokens: u.OutputTokensDetails.ReasoningTokens,
			CostUSD: u.Cost,
		}
	}
	return resp, nil
}

func (s *streamState) sortedIndexes() []int {
	idx := append([]int(nil), s.order...)
	for i := 1; i < len(idx); i++ {
		for j := i; j > 0 && idx[j-1] > idx[j]; j-- {
			idx[j-1], idx[j] = idx[j], idx[j-1]
		}
	}
	return idx
}

// fillArguments gives a function call item that arrived with no arguments the
// ones its deltas carried; every other item is returned as it came.
func (s *streamState) fillArguments(items []json.RawMessage) []json.RawMessage {
	if len(s.args) == 0 {
		return items
	}
	out := make([]json.RawMessage, len(items))
	for i, raw := range items {
		out[i] = raw
		var probe struct {
			Type      string `json:"type"`
			ID        string `json:"id"`
			Arguments string `json:"arguments"`
		}
		if json.Unmarshal(raw, &probe) != nil || probe.Type != "function_call" || probe.Arguments != "" {
			continue
		}
		b := s.args[probe.ID]
		if b == nil || b.Len() == 0 {
			continue
		}
		var m map[string]json.RawMessage
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		args, _ := json.Marshal(b.String())
		m["arguments"] = args
		if patched, err := json.Marshal(m); err == nil {
			out[i] = patched
		}
	}
	return out
}
