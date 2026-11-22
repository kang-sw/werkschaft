package wsaudit

import "context"

// Sink receives admitted audit envelopes from capture producers for persistence
// by an audit store.
//
// Implementations must return promptly and never block the producer's stream
// mapper or holder reader on disk I/O. When a sink cannot accept an envelope it
// returns a non-nil error so the producer can log and drop the event rather
// than stall the session.
type Sink interface {
	Append(ctx context.Context, ev Envelope) error
}

// SinkFunc adapts a function to the Sink interface.
type SinkFunc func(ctx context.Context, ev Envelope) error

// Append calls f(ctx, ev).
func (f SinkFunc) Append(ctx context.Context, ev Envelope) error {
	return f(ctx, ev)
}

type discardSink struct{}

func (discardSink) Append(context.Context, Envelope) error { return nil }

// DiscardSink is a Sink that accepts and drops every envelope without error.
var DiscardSink Sink = discardSink{}
