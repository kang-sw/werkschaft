package wsaudit

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSinkFuncAndDiscardSink(t *testing.T) {
	ctx := context.Background()
	ev := Envelope{
		V:         SchemaVersion,
		ID:        "evt-sink-1",
		Seq:       1,
		Session:   "lead-1",
		At:        time.Date(2026, 11, 22, 8, 40, 0, 0, time.UTC),
		Harness:   "claude",
		Kind:      KindToolCall,
		CallID:    "toolu_1",
		ModelSeen: true,
	}

	if err := DiscardSink.Append(ctx, ev); err != nil {
		t.Fatalf("DiscardSink.Append() error: %v", err)
	}

	var received []Envelope
	var sink Sink = SinkFunc(func(_ context.Context, got Envelope) error {
		received = append(received, got)
		return nil
	})
	if err := sink.Append(ctx, ev); err != nil {
		t.Fatalf("SinkFunc.Append() error: %v", err)
	}
	if len(received) != 1 || received[0].ID != ev.ID {
		t.Fatalf("received = %+v, want [%+v]", received, ev)
	}

	wantErr := errors.New("sink full")
	failing := SinkFunc(func(context.Context, Envelope) error { return wantErr })
	if err := failing.Append(ctx, ev); !errors.Is(err, wantErr) {
		t.Fatalf("failing.Append() = %v, want %v", err, wantErr)
	}
}
