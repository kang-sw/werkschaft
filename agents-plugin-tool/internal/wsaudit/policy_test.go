package wsaudit

import (
	"testing"
	"time"
)

func TestShouldCaptureMatrix(t *testing.T) {
	hostedSessions := []Session{
		{ID: "lead-1", Class: SessionHostedLead},
		{ID: "child-1", Class: SessionHostedChild},
	}
	nonHostedSessions := []Session{
		{ID: "cli-1", Class: SessionCLI},
		{ID: "unknown-1", Class: SessionClass("")},
		{ID: "bogus-1", Class: SessionClass("external")},
	}

	admittedKinds := map[Kind]bool{
		KindToolCall:       true,
		KindToolResult:     true,
		KindThinking:       true,
		KindHook:           true,
		KindSystemReminder: true,
		KindSystem:         true,
		KindCompaction:     true,
		KindSubagent:       true,
		KindOther:          true,
		KindUser:           false,
		KindAssistantText:  false,
	}

	// Ensure our test table covers every declared Kind.
	if len(admittedKinds) != len(AllKinds()) {
		t.Fatalf("test table covers %d kinds, AllKinds() has %d", len(admittedKinds), len(AllKinds()))
	}

	for _, sess := range hostedSessions {
		for _, kind := range AllKinds() {
			want := admittedKinds[kind]
			if got := ShouldCapture(sess, kind); got != want {
				t.Errorf("ShouldCapture(%s/%s, %s) = %v, want %v", sess.ID, sess.Class, kind, got, want)
			}
		}
		if ShouldCapture(sess, Kind("unrecognized_kind")) {
			t.Errorf("ShouldCapture(%s, invalid) = true, want false", sess.Class)
		}
	}

	for _, sess := range nonHostedSessions {
		for _, kind := range AllKinds() {
			if ShouldCapture(sess, kind) {
				t.Errorf("ShouldCapture(%s/%s, %s) = true, want false for non-hosted session", sess.ID, sess.Class, kind)
			}
		}
	}
}

func TestCaptureLineMultiEventAndRecognizedOutOfScope(t *testing.T) {
	at := time.Date(2026, 11, 22, 8, 30, 0, 0, time.UTC)
	lead := Session{ID: "lead-1", Class: SessionHostedLead}
	child := Session{ID: "child-1", Class: SessionHostedChild}
	cli := Session{ID: "cli-1", Class: SessionCLI}

	t.Run("assistant_line_with_text_and_tool_call_keeps_only_tool_call", func(t *testing.T) {
		mapped := []Envelope{
			{V: SchemaVersion, Session: lead.ID, At: at, Harness: "claude", Kind: KindAssistantText, Out: "Let me check."},
			{V: SchemaVersion, Session: lead.ID, At: at, Harness: "claude", Kind: KindToolCall, CallID: "c1", In: `{"cmd":"ls"}`},
		}
		got := CaptureLine(lead, "claude", at, `{"type":"assistant",...}`, true, mapped)
		if len(got) != 1 || got[0].Kind != KindToolCall || got[0].CallID != "c1" {
			t.Fatalf("got = %+v, want single tool_call c1", got)
		}
	})

	t.Run("user_labelled_line_with_tool_result_keeps_only_tool_result", func(t *testing.T) {
		mapped := []Envelope{
			{V: SchemaVersion, Session: child.ID, At: at, Harness: "claude", Kind: KindUser, In: "ignored"},
			{V: SchemaVersion, Session: child.ID, At: at, Harness: "claude", Kind: KindToolResult, CallID: "c1", Out: "ok"},
		}
		got := CaptureLine(child, "claude", at, `{"type":"user",...}`, true, mapped)
		if len(got) != 1 || got[0].Kind != KindToolResult || got[0].Session != child.ID {
			t.Fatalf("got = %+v, want single tool_result under child session", got)
		}
	})

	t.Run("recognized_line_all_out_of_scope_yields_nothing_not_other", func(t *testing.T) {
		mapped := []Envelope{
			{V: SchemaVersion, Session: lead.ID, At: at, Harness: "claude", Kind: KindUser, In: "hello"},
			{V: SchemaVersion, Session: lead.ID, At: at, Harness: "claude", Kind: KindAssistantText, Out: "hi"},
		}
		got := CaptureLine(lead, "claude", at, `{"type":"user","text":"hello"}`, true, mapped)
		if len(got) != 0 {
			t.Fatalf("got = %+v, want empty (not other)", got)
		}
	})

	t.Run("recognized_line_with_zero_events_yields_nothing_not_other", func(t *testing.T) {
		got := CaptureLine(lead, "claude", at, `{"type":"ping"}`, true, nil)
		if len(got) != 0 {
			t.Fatalf("got = %+v, want empty (not other)", got)
		}
	})

	t.Run("unrecognized_line_on_hosted_session_becomes_other_with_raw", func(t *testing.T) {
		raw := `{"type":"brand_new_event","x":1}`
		got := CaptureLine(lead, "claude", at, raw, false, nil)
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1", len(got))
		}
		if got[0].Kind != KindOther || got[0].Raw != raw || got[0].Session != lead.ID || got[0].V != SchemaVersion {
			t.Fatalf("got[0] = %+v, want other with verbatim raw", got[0])
		}
	})

	t.Run("unrecognized_line_on_hosted_child_becomes_other_under_child_session", func(t *testing.T) {
		raw := `{"type":"child_unknown_event"}`
		got := CaptureLine(child, "codex", at, raw, false, nil)
		if len(got) != 1 || got[0].Kind != KindOther || got[0].Raw != raw || got[0].Session != child.ID {
			t.Fatalf("got = %+v, want single other under child session", got)
		}
	})

	t.Run("unrecognized_line_on_cli_session_yields_nothing", func(t *testing.T) {
		got := CaptureLine(cli, "claude", at, `{"type":"brand_new_event"}`, false, nil)
		if len(got) != 0 {
			t.Fatalf("got = %+v, want empty for non-hosted CLI session", got)
		}
	})

	t.Run("recognized_in_scope_events_on_cli_session_yield_nothing", func(t *testing.T) {
		mapped := []Envelope{
			{V: SchemaVersion, Session: cli.ID, At: at, Harness: "claude", Kind: KindToolCall, CallID: "c1"},
		}
		got := CaptureLine(cli, "claude", at, `{"type":"assistant"}`, true, mapped)
		if len(got) != 0 {
			t.Fatalf("got = %+v, want empty for non-hosted CLI session", got)
		}
	})
}
