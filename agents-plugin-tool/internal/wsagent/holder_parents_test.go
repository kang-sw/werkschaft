package wsagent

import (
	"encoding/json"
	"reflect"
	"testing"
)

// TestAgentParentsReadsTheLiveChildIndex pins the dashboard's parent
// lookup: each asked id the child index holds answers its parent's reply
// id, whichever parent kind; an unknown id is absent.
func TestAgentParentsReadsTheLiveChildIndex(t *testing.T) {
	r := &Runner{childrenIndex: map[ParentRef]map[string]bool{
		{Kind: ParentLead, ReplyID: "lead1"}:         {"w1": true, "w2": true},
		{Kind: ParentConversation, ReplyID: "conv9"}: {"w3": true},
	}}
	got := r.AgentParents([]string{"w1", "w3", "nobody"})
	if want := map[string]string{"w1": "lead1", "w3": "conv9"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AgentParents = %v, want %v", got, want)
	}
	if got := r.AgentParents(nil); len(got) != 0 {
		t.Fatalf("AgentParents(nil) = %v", got)
	}
}

// TestHolderSessionParentIsAdditive pins the descriptor's wire shape: the
// parent's reply id is omitted when empty, and an older descriptor without
// it reads as before.
func TestHolderSessionParentIsAdditive(t *testing.T) {
	b, _ := json.Marshal(holderSession{Kind: holderKindAgentWorker, ReplyID: "w1"})
	if string(b) != `{"kind":"agent-worker","reply_id":"w1"}` {
		t.Fatalf("descriptor without a parent = %s", b)
	}
	b, _ = json.Marshal(holderSession{Kind: holderKindAgentWorker, ReplyID: "w1", Label: "review", ParentReplyID: "lead1"})
	if string(b) != `{"kind":"agent-worker","reply_id":"w1","label":"review","parent_reply_id":"lead1"}` {
		t.Fatalf("descriptor with a parent = %s", b)
	}
	var old holderSession
	if err := json.Unmarshal([]byte(`{"kind":"agent-worker","reply_id":"w1","capability":"c"}`), &old); err != nil || old.ParentReplyID != "" || old.Capability != "c" {
		t.Fatalf("older descriptor = %+v, %v", old, err)
	}
}
