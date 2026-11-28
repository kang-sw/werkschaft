package mcp

import (
	"strings"
	"testing"
)

// TestCompanionActGestureActionKey pins the act schema: gesture stays a free
// string whose description names the ACTIONS block, and a custom action key
// passes through to the host unchanged.
func TestCompanionActGestureActionKey(t *testing.T) {
	var gesture map[string]string
	for _, tool := range companionTools() {
		if tool["name"] != companionActTool {
			continue
		}
		props := tool["inputSchema"].(map[string]any)["properties"].(map[string]any)
		gesture = props["gesture"].(map[string]string)
	}
	if gesture == nil {
		t.Fatal("act tool has no gesture property")
	}
	if gesture["type"] != "string" || !strings.HasSuffix(gesture["description"], "or an action key from the ACTIONS block.") {
		t.Fatalf("gesture property = %+v", gesture)
	}
	if _, ok := gesture["enum"]; ok {
		t.Fatal("gesture must stay a free-form string")
	}

	t.Setenv("WS_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	host := &testCompanionHost{}
	s := NewServer(root, "test")
	s.descriptor = connectionDescriptor{callFallback: true}
	s.companion = host
	bodyKey := mintCompanionTestKey(t, s, root, CompanionRoleBody)
	if txt, isErr := invokeCompanionTestTool(t, s, companionActTool, map[string]any{"session_key": bodyKey, "gesture": "bow_deep"}); isErr {
		t.Fatalf("act with a custom key = %q", txt)
	}
	if len(host.acts) != 1 || host.acts[0].Gesture != "bow_deep" {
		t.Fatalf("custom gesture did not pass through: %+v", host.acts)
	}
}
