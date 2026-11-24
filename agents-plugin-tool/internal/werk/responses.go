package werk

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/kang-sw/devenv/internal/wscred"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CachedTokens     int64  `json:"cached_tokens"`
	CacheWriteTokens *int64 `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int64  `json:"reasoning_tokens,omitempty"`
	// CostUSD is the provider's own cost figure, nil when it sent none.
	CostUSD *float64 `json:"cost_usd,omitempty"`
}

// Response is a completed model response.
type Response struct {
	ID string `json:"id"`
	// Output are the response's items in order, as received.
	Output []json.RawMessage `json:"output"`
	Usage  Usage             `json:"usage"`
	window int64
	model  string
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
	BaseURL  string
	Provider wscred.ProviderID
	// Dialect is "ollama" or "vllm" for configured local endpoints; empty
	// retains hosted credential requirements.
	Dialect string
	// Token returns the credential to send, asked for on every request.
	Token func(ctx context.Context) (string, error)
	// PlanToken admits a request against the signed-in account's pause.
	PlanToken func(context.Context) (wscred.OpenAIPlanRequest, error)
	HTTP      *http.Client
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

// The event cap also covers multiline data, and the response cap covers all
// fragments, including text already delivered to a streaming callback.
const maxStreamEvent = maxStreamLine
const maxStreamResponse = 128 << 20

var errResponseTooLarge = errors.New("the model response exceeds the size limit")

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
	// ResetsAt is the latest future rate-limit reset time parsed from
	// response headers (epoch milliseconds), or 0 when unavailable.
	ResetsAt int64
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

type planProbeError struct{ error }

func (e planProbeError) Unwrap() error { return e.error }

const openAIPlanLimitCode = "subscription_sharing_usage_limit_exceeded"

func planLimitExceeded(err error) bool {
	var h *HTTPError
	var f *FailedError
	return (errors.As(err, &h) && h.Code == openAIPlanLimitCode) ||
		(errors.As(err, &f) && f.Code == openAIPlanLimitCode)
}

// Retryable reports whether err is worth another request and how long to wait
// before it: delay is the endpoint's own Retry-After when it sent one, else
// zero (the caller's backoff). A refused credential, a bad request and a
// context end are not retryable.
func Retryable(err error) (delay time.Duration, ok bool) {
	var probe planProbeError
	if errors.As(err, &probe) || planLimitExceeded(err) {
		return 0, false
	}
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
	base := c.HTTP
	if base == nil {
		base = http.DefaultClient
	}
	if c.Dialect == "" {
		return base
	}
	client := *base
	// Never forward an endpoint credential to a redirected URL. Copy the
	// client so the caller's redirect policy remains untouched.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
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
func (r Request) body() ([]byte, error) { return r.bodyFor("") }

func (r Request) bodyFor(provider wscred.ProviderID) ([]byte, error) {
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
	}
	if r.Effort != "" || provider == wscred.ProviderOpenAI {
		w.Include = []string{"reasoning.encrypted_content"}
	}
	return json.Marshal(w)
}

// Respond sends req and reads the streamed response to its terminal event.
func (c *Client) Respond(ctx context.Context, req Request, h StreamHandler) (out *Response, err error) {
	var token string
	if c.PlanToken != nil {
		plan, admitErr := c.PlanToken(ctx)
		if admitErr != nil {
			return nil, admitErr
		}
		token = plan.Credential.Secret.Reveal()
		defer func() {
			outcome := wscred.OpenAIPlanUnchanged
			switch {
			case planLimitExceeded(err):
				outcome = wscred.OpenAIPlanLimitExceeded
				err = wscred.ErrOpenAIPlanPaused
			case err == nil && out != nil:
				outcome = wscred.OpenAIPlanSucceeded
			case plan.Probe && err != nil && ctx.Err() == nil:
				// A probe is one provider request. Give the operator its
				// failure and keep the account paused until a later success.
				err = planProbeError{err}
			}
			plan.Finish(outcome)
		}()
	} else if c.Token != nil {
		token, err = c.Token(ctx)
		if err != nil {
			return nil, err
		}
	}
	if token == "" && c.Dialect != "ollama" && c.Dialect != "vllm" {
		return nil, ErrNoCredential
	}
	body, err := req.bodyFor(c.Provider)
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
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
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
	out, err = readStream(resp.Body, h, func() { timer.Reset(stall) }, maxStreamEvent, maxStreamResponse)
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
	now := time.Now()
	e := &HTTPError{
		Status:     resp.StatusCode,
		RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), now),
		ResetsAt:   parseRateLimitReset(resp.Header, now),
	}
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

// parseRateLimitReset extracts the maximum future reset timestamp (epoch ms)
// across standard provider rate-limit headers:
//   - Retry-After (seconds or HTTP-date)
//   - x-ratelimit-reset-requests / x-ratelimit-reset-tokens (Go-style relative durations)
//   - anthropic-ratelimit-*-reset (RFC3339 timestamps)
//
// Past or unparseable values are ignored; returns 0 when no future reset time is present.
func parseRateLimitReset(h http.Header, now time.Time) int64 {
	if len(h) == 0 {
		return 0
	}
	nowMs := now.UnixMilli()
	var best int64
	consider := func(ms int64) {
		if ms > nowMs && ms > best {
			best = ms
		}
	}
	if d := parseRetryAfter(h.Get("Retry-After"), now); d > 0 {
		consider(now.Add(d).UnixMilli())
	}
	for _, key := range []string{"X-Ratelimit-Reset-Requests", "X-Ratelimit-Reset-Tokens"} {
		if v := strings.TrimSpace(h.Get(key)); v != "" {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				consider(now.Add(d).UnixMilli())
			}
		}
	}
	for k, vals := range h {
		lk := strings.ToLower(k)
		if !strings.HasPrefix(lk, "anthropic-ratelimit-") || !strings.HasSuffix(lk, "-reset") {
			continue
		}
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if v == "" {
				continue
			}
			if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
				consider(t.UnixMilli())
			}
		}
	}
	return best
}

// streamState assembles one response from its events.
type streamState struct {
	maxOutput int
	items     map[int]json.RawMessage
	order     []int
	// args accumulates a function call's argument deltas by item id, for a
	// provider whose done item leaves them out.
	args map[string]*strings.Builder
}

func readStream(r io.Reader, h StreamHandler, touch func(), eventLimit, responseLimit int) (*Response, error) {
	st := &streamState{maxOutput: responseLimit, items: map[int]json.RawMessage{}, args: map[string]*strings.Builder{}}
	remaining := responseLimit
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
		line, err := readLine(br, min(eventLimit, remaining))
		if errors.Is(err, errResponseTooLarge) {
			return nil, err
		}
		remaining -= len(line)
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
			part := strings.TrimPrefix(strings.TrimPrefix(text, "data:"), " ")
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			if len(part) > eventLimit-data.Len() {
				return nil, errResponseTooLarge
			}
			data.WriteString(part)
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

// readLine reads one line without growing its buffer past the limit.
func readLine(br *bufio.Reader, limit int) ([]byte, error) {
	var out []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if len(chunk) > limit-len(out) {
			return nil, errResponseTooLarge
		}
		out = append(out, chunk...)
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
	CacheWriteTokens   *int64 `json:"cache_write_tokens"`
	InputTokens        int64  `json:"input_tokens"`
	OutputTokens       int64  `json:"output_tokens"`
	InputTokensDetails struct {
		CachedTokens     int64  `json:"cached_tokens"`
		CacheWriteTokens *int64 `json:"cache_write_tokens"`
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
	if err := s.fillArguments(resp.Output); err != nil {
		return nil, err
	}
	if u := d.Response.Usage; u != nil {
		cacheWrite := u.InputTokensDetails.CacheWriteTokens
		if cacheWrite == nil {
			cacheWrite = u.CacheWriteTokens
		}
		resp.Usage = Usage{
			InputTokens: u.InputTokens, OutputTokens: u.OutputTokens,
			CachedTokens: u.InputTokensDetails.CachedTokens, ReasoningTokens: u.OutputTokensDetails.ReasoningTokens,
			CostUSD: u.Cost, CacheWriteTokens: cacheWrite,
		}
	}
	return resp, nil
}

func (s *streamState) sortedIndexes() []int {
	slices.Sort(s.order)
	return s.order
}

// fillArguments gives a function call item that arrived with no arguments the
// ones its deltas carried; every other item is returned as it came.
func (s *streamState) fillArguments(items []json.RawMessage) error {
	if len(s.args) == 0 {
		return nil
	}
	remaining := s.maxOutput
	for _, raw := range items {
		remaining -= len(raw)
	}
	for i, raw := range items {
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
		if b.Len() > remaining {
			return errResponseTooLarge
		}
		start, end, prefix := argumentSlot(raw)
		growth := len(prefix) + jsonStringSize(b.String()) - (end - start)
		if growth > remaining {
			return errResponseTooLarge
		}
		args, _ := json.Marshal(b.String())
		patched := make(json.RawMessage, 0, len(raw)+growth)
		patched = append(patched, raw[:start]...)
		patched = append(patched, prefix...)
		patched = append(patched, args...)
		items[i] = append(patched, raw[end:]...)
		remaining -= growth
	}
	return nil
}

// argumentSlot locates the last exact "arguments" member in a function
// call object already validated by json.Unmarshal. Preserve the other fields
// as received so their size does not change during reconstruction.
func argumentSlot(raw json.RawMessage) (start, end int, prefix string) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	_, _ = dec.Token() // opening brace
	for dec.More() {
		key, _ := dec.Token()
		var value json.RawMessage
		_ = dec.Decode(&value)
		if key == "arguments" {
			end = int(dec.InputOffset())
			start = end - len(value)
		}
	}
	if end == 0 {
		end = bytes.LastIndexByte(raw, '}')
		return end, end, `,"arguments":`
	}
	return start, end, ""
}

// jsonStringSize matches encoding/json's string quoting, including its
// default HTML escaping, without allocating the quoted string.
func jsonStringSize(s string) int {
	size := 2
	for len(s) != 0 {
		r, width := utf8.DecodeRuneInString(s)
		s = s[width:]
		switch {
		case r == '"' || r == '\\' || r == '\b' || r == '\f' || r == '\n' || r == '\r' || r == '\t':
			size += 2
		case r < 0x20 || r == '<' || r == '>' || r == '&' || r == '\u2028' || r == '\u2029' || (r == utf8.RuneError && width == 1):
			size += 6
		default:
			size += width
		}
	}
	return size
}
