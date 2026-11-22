package wsaudit

// MetaRequest is the meta key of an event's model request (Decision 7):
// {id, prev_id, ctx}. id is the harness's request or message ID; prev_id is
// the request seen immediately before it in the same model conversation,
// absent for a conversation's first request; ctx is input + cache read +
// cache creation tokens from the request's usage, absent when the stream
// carried no usage. Both IDs are in NormalizeID form.
const MetaRequest = "request"

// RequestChain tracks one model conversation's requests: the lead's own
// chain, or one native subagent's. The chain advances once per distinct
// request ID, so several stream lines of one request share it.
type RequestChain struct {
	cur, prev string
	ctx       int64
	hasCtx    bool
}

// See records request id with optional usage context and reports whether id
// opened a new request. An empty id leaves the chain as it is.
func (c *RequestChain) See(id string, ctx int64, hasCtx bool) bool {
	if id == "" {
		return false
	}
	opened := id != c.cur
	if opened {
		c.prev, c.cur, c.ctx, c.hasCtx = c.cur, id, 0, false
	}
	if hasCtx {
		c.ctx, c.hasCtx = ctx, true
	}
	return opened
}

// Meta is the chain's current request as a fresh MetaRequest value, or nil
// before any request was seen.
func (c *RequestChain) Meta() map[string]any {
	if c == nil || c.cur == "" {
		return nil
	}
	m := map[string]any{"id": NormalizeID(c.cur)}
	if c.prev != "" {
		m["prev_id"] = NormalizeID(c.prev)
	}
	if c.hasCtx {
		m["ctx"] = c.ctx
	}
	return m
}
