package mcp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

type testCompanionHost struct {
	store   wsdashboard.CompanionPhoneStore
	speaks  []string
	faces   []string
	motions []string
}

func (h *testCompanionHost) CompanionPosture(owner string) string {
	return h.store.PostureFor(owner)
}

func (h *testCompanionHost) CompanionUnreadPhoneCount() int {
	return h.store.UnreadAgentCount()
}

func (h *testCompanionHost) CompanionSpeak(_ string, text string, _ int) bool {
	h.speaks = append(h.speaks, text)
	return true
}

func (h *testCompanionHost) CompanionFace(_ string, expression string, _ int) bool {
	h.faces = append(h.faces, expression)
	return true
}

func (h *testCompanionHost) CompanionMotion(_ string, gesture, prop string, _ int) bool {
	h.motions = append(h.motions, gesture+":"+prop)
	return true
}

func (h *testCompanionHost) CompanionPhonePickup(owner string) (wsdashboard.CompanionPhoneReadResult, error) {
	res, _, err := h.store.PhonePickup(owner)
	return res, err
}

func (h *testCompanionHost) CompanionPhoneRead(owner string) (wsdashboard.CompanionPhoneReadResult, error) {
	res, _, err := h.store.PhoneRead(owner)
	return res, err
}

func (h *testCompanionHost) CompanionPhoneSend(owner, text, tone string) (wsdashboard.CompanionPhoneMessage, error) {
	msg, _, err := h.store.PhoneSend(owner, text, tone, nil)
	return msg, err
}

func (h *testCompanionHost) CompanionPhonePutdown(owner, style, next string) (wsdashboard.CompanionPhoneSnapshot, error) {
	snap, _, err := h.store.PhonePutdown(owner, style, next)
	return snap, err
}

func (h *testCompanionHost) CompanionReleaseOnTurnEnd(owner string) bool {
	_, changed := h.store.ReleaseOnTurnEnd(owner)
	return changed
}

func invokeCompanionTestTool(t *testing.T, s *Server, name string, args map[string]any) (string, bool) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	resp := s.callTool(context.Background(), request{
		ID:     json.RawMessage(`1`),
		Method: "tools/call",
		Params: raw,
	})
	if resp.Error != nil {
		t.Fatalf("unexpected RPC error calling %s: %+v", name, resp.Error)
	}
	res, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("unexpected result type %T for %s", resp.Result, name)
	}
	isErr, _ := res["isError"].(bool)
	content, ok := res["content"].([]map[string]string)
	if !ok || len(content) == 0 {
		t.Fatalf("unexpected content shape for %s: %#v", name, res["content"])
	}
	return content[0]["text"], isErr
}

func discoveryToolNames(tools []map[string]any) map[string]bool {
	out := make(map[string]bool, len(tools))
	for _, tool := range tools {
		if name, ok := tool["name"].(string); ok {
			out[name] = true
		}
	}
	return out
}

func TestCompanionPostureLockDirectAndDiscovery(t *testing.T) {
	host := &testCompanionHost{}
	if _, _, err := host.store.EnqueueUserMessage("hey check your phone"); err != nil {
		t.Fatalf("EnqueueUserMessage: %v", err)
	}

	s := NewServer(t.TempDir(), "test")
	s.descriptor = connectionDescriptor{callFallback: true}
	s.companion = host
	s.conversation = "sess-phone-1"

	leadEntry := sessionEntry{scope: roleLead}

	// Laptop posture: phone_read and phone_send must be refused via callTool,
	// via the call envelope, and hidden from callDiscoveryTools.
	laptopDisc := discoveryToolNames(s.callDiscoveryTools("", leadEntry, true))
	if laptopDisc[companionPhoneReadTool] || laptopDisc[companionPhoneSendTool] {
		t.Fatalf("callDiscoveryTools exposed phone_read/phone_send on laptop posture")
	}
	if !laptopDisc[companionPhonePickupTool] || !laptopDisc["runtime.read"] {
		t.Fatalf("callDiscoveryTools missing expected laptop tools: pickup=%v runtime.read=%v", laptopDisc[companionPhonePickupTool], laptopDisc["runtime.read"])
	}
	for _, tool := range []string{companionPhoneReadTool, companionPhoneSendTool} {
		args := map[string]any{}
		if tool == companionPhoneSendTool {
			args["text"] = "cannot send yet"
		}
		txt, isErr := invokeCompanionTestTool(t, s, tool, args)
		if !isErr || !strings.Contains(txt, companionRefusalPhoneOnDesk) {
			t.Fatalf("callTool(%s) on laptop = (%q, isErr=%v), want refusal %q", tool, txt, isErr, companionRefusalPhoneOnDesk)
		}
		txt, isErr = invokeCompanionTestTool(t, s, "call", map[string]any{"tool": tool, "arguments": args})
		if !isErr || !strings.Contains(txt, companionRefusalPhoneOnDesk) {
			t.Fatalf("call(%s) on laptop = (%q, isErr=%v), want refusal %q", tool, txt, isErr, companionRefusalPhoneOnDesk)
		}
	}

	// Cosmetic companion tools work in laptop posture.
	if txt, isErr := invokeCompanionTestTool(t, s, companionSpeakTool, map[string]any{"text": "working!"}); isErr || !strings.Contains(txt, "ok") {
		t.Fatalf("companion.speak on laptop = (%q, isErr=%v)", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionFaceTool, map[string]any{"expression": "happy"}); isErr || !strings.Contains(txt, "ok") {
		t.Fatalf("companion.face on laptop = (%q, isErr=%v)", txt, isErr)
	}
	if txt, isErr := invokeCompanionTestTool(t, s, companionMotionTool, map[string]any{"gesture": "wave"}); isErr || !strings.Contains(txt, "ok") {
		t.Fatalf("companion.motion on laptop = (%q, isErr=%v)", txt, isErr)
	}

	// Pick up phone -> posture locks to phone and drains unread message.
	txt, isErr := invokeCompanionTestTool(t, s, companionPhonePickupTool, map[string]any{})
	if isErr || !strings.Contains(txt, "hey check your phone") || !strings.Contains(txt, "posture: phone") {
		t.Fatalf("phone_pickup = (%q, isErr=%v)", txt, isErr)
	}

	// In phone posture: laptop tools are hidden from callDiscoveryTools and
	// hard-refused in both callTool and the call envelope!
	phoneDisc := discoveryToolNames(s.callDiscoveryTools("", leadEntry, true))
	if phoneDisc["runtime.read"] || phoneDisc["git.status"] || phoneDisc["tickets.query"] {
		t.Fatalf("callDiscoveryTools exposed laptop tools while holding phone")
	}
	if !phoneDisc[companionPhoneReadTool] || !phoneDisc[companionPhoneSendTool] || !phoneDisc[companionPhonePutdownTool] {
		t.Fatalf("callDiscoveryTools missing phone tools while holding phone")
	}
	for _, blocked := range []string{"runtime.read", "git.status", "tickets.query"} {
		txt, isErr = invokeCompanionTestTool(t, s, blocked, map[string]any{})
		if !isErr || !strings.Contains(txt, companionRefusalHandsBusy) {
			t.Fatalf("callTool(%s) on phone = (%q, isErr=%v), want refusal %q", blocked, txt, isErr, companionRefusalHandsBusy)
		}
		txt, isErr = invokeCompanionTestTool(t, s, "call", map[string]any{"tool": blocked, "arguments": map[string]any{}})
		if !isErr || !strings.Contains(txt, companionRefusalHandsBusy) {
			t.Fatalf("call(%s) on phone = (%q, isErr=%v), want refusal %q", blocked, txt, isErr, companionRefusalHandsBusy)
		}
	}

	// Companion phone tools succeed while holding the phone.
	txt, isErr = invokeCompanionTestTool(t, s, companionPhoneSendTool, map[string]any{"text": "merong!", "tone": "merong"})
	if isErr || !strings.Contains(txt, "sent ") {
		t.Fatalf("phone_send = (%q, isErr=%v)", txt, isErr)
	}
	txt, isErr = invokeCompanionTestTool(t, s, companionPhoneReadTool, map[string]any{})
	if isErr || !strings.Contains(txt, "posture: phone") {
		t.Fatalf("phone_read = (%q, isErr=%v)", txt, isErr)
	}

	// Putdown facedown_merong restores laptop posture.
	txt, isErr = invokeCompanionTestTool(t, s, companionPhonePutdownTool, map[string]any{
		"style": wsdashboard.CompanionPutdownFacedownMerong,
		"next":  wsdashboard.CompanionPutdownNextAmbient,
	})
	if isErr || !strings.Contains(txt, "posture: laptop") {
		t.Fatalf("phone_putdown = (%q, isErr=%v)", txt, isErr)
	}
	if host.store.Snapshot().PhoneFaceDown != true {
		t.Fatalf("expected PhoneFaceDown=true after facedown_merong putdown")
	}

	// Laptop tool works again after putdown.
	txt, isErr = invokeCompanionTestTool(t, s, "runtime.read", map[string]any{})
	if isErr {
		t.Fatalf("runtime.read after putdown failed: %q", txt)
	}
}

func TestCompanionPhoneBuzzPiggyback(t *testing.T) {
	host := &testCompanionHost{}
	s := NewServer(t.TempDir(), "test")
	s.companion = host
	s.conversation = "sess-buzz-1"

	// Zero unread -> no piggyback line.
	txt, isErr := invokeCompanionTestTool(t, s, "runtime.read", map[string]any{})
	if isErr || strings.Contains(txt, "[phone buzz:") {
		t.Fatalf("unexpected buzz with 0 unread: %q", txt)
	}

	// Two unread user messages -> non-JSON, non-error response carries [phone buzz: 2 unread].
	if _, _, err := host.store.EnqueueUserMessage("ping 1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := host.store.EnqueueUserMessage("ping 2"); err != nil {
		t.Fatal(err)
	}
	txt, isErr = invokeCompanionTestTool(t, s, "runtime.read", map[string]any{})
	if isErr || !strings.Contains(txt, "[phone buzz: 2 unread]") {
		t.Fatalf("expected [phone buzz: 2 unread] piggyback, got: %q", txt)
	}

	// JSON format must stay valid JSON (no piggyback appended).
	jsonTxt, isErr := invokeCompanionTestTool(t, s, "runtime.read", map[string]any{"format": "json"})
	if isErr || strings.Contains(jsonTxt, "[phone buzz:") {
		t.Fatalf("JSON output must not carry phone buzz piggyback: %q", jsonTxt)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(jsonTxt), &parsed); err != nil {
		t.Fatalf("JSON output corrupted: %v", err)
	}

	// Error response must not carry phone buzz piggyback.
	errTxt, isErr := invokeCompanionTestTool(t, s, companionPhoneSendTool, map[string]any{"text": "too early"})
	if !isErr || strings.Contains(errTxt, "[phone buzz:") {
		t.Fatalf("error output must not carry phone buzz piggyback: %q", errTxt)
	}

	// Picking up drains unread -> subsequent runtime.read after putdown has no buzz.
	if _, isErr := invokeCompanionTestTool(t, s, companionPhonePickupTool, map[string]any{}); isErr {
		t.Fatalf("pickup failed")
	}
	if _, isErr := invokeCompanionTestTool(t, s, companionPhonePutdownTool, map[string]any{}); isErr {
		t.Fatalf("putdown failed")
	}
	txt, isErr = invokeCompanionTestTool(t, s, "runtime.read", map[string]any{})
	if isErr || strings.Contains(txt, "[phone buzz:") {
		t.Fatalf("buzz persisted after drain: %q", txt)
	}
}
