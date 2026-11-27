package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRecordAccountSlotFieldsT0RoundTrip(t *testing.T) {
	raw := []byte(`{
		"schema_version": 1,
		"reply_id": "reply-1",
		"profile": "codex-lead",
		"backend": "codex",
		"root": "/tmp/repo",
		"ws_session_key": "key-1",
		"created_at": "2026-11-28T00:00:00Z",
		"account_slot_id": "slot-a",
		"pending_account_slot": "slot-b",
		"future_unknown_field": {"v": 42}
	}`)
	var rec Record
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if rec.AccountSlotID != "slot-a" || rec.PendingAccountSlotID != "slot-b" {
		t.Fatalf("slot fields lost: %+v", rec)
	}
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var back map[string]json.RawMessage
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("unmarshal map: %v", err)
	}
	if string(back["account_slot_id"]) != `"slot-a"` || string(back["pending_account_slot"]) != `"slot-b"` {
		t.Fatalf("round-trip slot fields: %s", out)
	}
	if string(back["future_unknown_field"]) != `{"v": 42}` && string(back["future_unknown_field"]) != `{"v":42}` {
		t.Fatalf("unknown field lost on round-trip: %s", out)
	}

	// Legacy record without slot fields decodes cleanly and omits them on write.
	var legacy Record
	if err := json.Unmarshal([]byte(`{"schema_version":1,"reply_id":"r","profile":"p","backend":"codex","root":"/tmp","ws_session_key":"k","created_at":"2026-11-28T00:00:00Z"}`), &legacy); err != nil {
		t.Fatalf("unmarshal legacy: %v", err)
	}
	if legacy.AccountSlotID != "" || legacy.PendingAccountSlotID != "" {
		t.Fatalf("legacy slot fields should be empty: %+v", legacy)
	}
	legacyOut, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	var legacyMap map[string]json.RawMessage
	if err := json.Unmarshal(legacyOut, &legacyMap); err != nil {
		t.Fatal(err)
	}
	if _, ok := legacyMap["account_slot_id"]; ok {
		t.Fatalf("legacy marshal should omit empty account_slot_id: %s", legacyOut)
	}
	if _, ok := legacyMap["pending_account_slot"]; ok {
		t.Fatalf("legacy marshal should omit empty pending_account_slot: %s", legacyOut)
	}
}

func TestCodexChoicesScopedPerSlotCodexHome(t *testing.T) {
	emptyHostedChoices(t)
	slotA := t.TempDir()
	slotB := t.TempDir()
	if err := os.WriteFile(filepath.Join(slotA, "models_cache.json"), []byte(fixtureCodexCache("model-a")), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slotB, "models_cache.json"), []byte(fixtureCodexCache("model-b")), 0o600); err != nil {
		t.Fatal(err)
	}

	activeHome := slotA
	backend := CodexBackend{ResolveCodexHome: func() string { return activeHome }}

	// File-backed lookup switches cleanly with active slot home.
	gotA, _, err := backend.Choices(context.Background())
	if err != nil || !reflect.DeepEqual(gotA, []ModelChoice{{ID: "model-a"}}) {
		t.Fatalf("slot A file choices: %+v %v", gotA, err)
	}
	activeHome = slotB
	gotB, _, err := backend.Choices(context.Background())
	if err != nil || !reflect.DeepEqual(gotB, []ModelChoice{{ID: "model-b"}}) {
		t.Fatalf("slot B file choices: %+v %v", gotB, err)
	}

	// Live session report for slot A must not bleed into slot B.
	codexModelsForHome(slotA, json.RawMessage(`{"data":[{"model":"live-a"}]}`))
	activeHome = slotA
	liveA, _, err := backend.Choices(context.Background())
	if err != nil || len(liveA) != 1 || liveA[0].ID != "live-a" {
		t.Fatalf("slot A live choices: %+v %v", liveA, err)
	}
	activeHome = slotB
	stillB, _, err := backend.Choices(context.Background())
	if err != nil || !reflect.DeepEqual(stillB, []ModelChoice{{ID: "model-b"}}) {
		t.Fatalf("slot B must not see slot A live report: %+v %v", stillB, err)
	}
}

func TestCodexMapperPreservesRPCErrorCodeAndInfo(t *testing.T) {
	m := newCodexMapper("thread-1")
	var gotID string
	var gotAns controlAnswer
	replies := stubLineReplies{onAnswer: func(id string, a controlAnswer) {
		gotID, gotAns = id, a
	}}

	// Control response with JSON-RPC -32600 and codexErrorInfo="unauthorized".
	line := []byte(`{"id":"wsc/turn/ep/1","error":{"code":-32600,"message":"session belongs to another account","codexErrorInfo":"unauthorized"}}`)
	m.mapLine(line, replies)
	if gotID != "wsc/turn/ep/1" || gotAns.ok {
		t.Fatalf("unexpected answer: id=%q %+v", gotID, gotAns)
	}
	var rpcErr *CodexRPCError
	if !errors.As(gotAns.errVal, &rpcErr) {
		t.Fatalf("expected *CodexRPCError in answer.errVal, got %T (%v)", gotAns.errVal, gotAns.errVal)
	}
	if !rpcErr.HasCode || rpcErr.Code != -32600 || rpcErr.Info != "unauthorized" || rpcErr.Message != "session belongs to another account" {
		t.Fatalf("unexpected CodexRPCError: %+v", rpcErr)
	}

	// Failed turn/completed notification also preserves Code and Info on TurnErrorVal.
	m.mapLine([]byte(`{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"turn-1"}}}`), replies)
	events := m.mapLine([]byte(`{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":{"code":-32600,"message":"auth rejected","codexErrorInfo":"unauthorized"}}}}`), replies)
	var foundEnd *Event
	for i := range events {
		if events[i].TurnEnd {
			foundEnd = &events[i]
			break
		}
	}
	if foundEnd == nil {
		t.Fatalf("missing TurnEnd event: %+v", events)
	}
	var turnRPC *CodexRPCError
	if !errors.As(foundEnd.TurnErrorVal, &turnRPC) {
		t.Fatalf("expected *CodexRPCError on TurnErrorVal, got %T", foundEnd.TurnErrorVal)
	}
	if !turnRPC.HasCode || turnRPC.Code != -32600 || turnRPC.Info != "unauthorized" {
		t.Fatalf("unexpected TurnErrorVal: %+v", turnRPC)
	}
}

type stubLineReplies struct {
	onAnswer func(id string, a controlAnswer)
}

func (s stubLineReplies) answer(id string, a controlAnswer) {
	if s.onAnswer != nil {
		s.onAnswer(id, a)
	}
}

func (stubLineReplies) send(any) {}
