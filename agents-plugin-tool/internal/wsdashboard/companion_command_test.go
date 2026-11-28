package wsdashboard

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeCompanionCommand(t *testing.T) {
	if _, ok := normalizeCompanionCommand(CompanionCommand{Quip: "hi"}); ok {
		t.Fatal("a command without a session must be dropped")
	}
	long := strings.Repeat("가", CompanionQuipMaxRunes+9)
	got, ok := normalizeCompanionCommand(CompanionCommand{
		SessionID: "s", Expression: strings.Repeat("x", companionCommandIDLimit+1), Gesture: "wave", Quip: long, TTLms: 90000,
	})
	if !ok || got.TTLms != CompanionCommandMaxTTL || got.Expression != "" || got.Gesture != "wave" ||
		len([]rune(got.Quip)) != CompanionQuipMaxRunes {
		t.Fatalf("normalize = %+v", got)
	}
	if got, _ := normalizeCompanionCommand(CompanionCommand{SessionID: "s"}); got.TTLms != CompanionCommandMinTTL {
		t.Fatalf("zero TTL clamps to the minimum, got %d", got.TTLms)
	}
}

func TestCompanionCommandReachesOnlyRailStreams(t *testing.T) {
	f := startPageFixture(t)
	// A command published before a stream connects is never replayed.
	f.d.PublishCompanionCommand(CompanionCommand{SessionID: "livesession", Quip: "stale"})
	rail := openSSE(t, f.base+"/rail/events")
	page := openSSE(t, f.base+"/s/livesession/events")
	commands := func(events <-chan sseEvent) []CompanionCommand {
		t.Helper()
		all, _ := collect(events)
		var out []CompanionCommand
		for _, e := range all {
			if e.name != companionCommandEvent {
				continue
			}
			var cmd CompanionCommand
			if err := json.Unmarshal([]byte(e.data), &cmd); err != nil {
				t.Fatalf("bad command payload %q: %v", e.data, err)
			}
			out = append(out, cmd)
		}
		return out
	}
	if got := commands(rail); len(got) != 0 {
		t.Fatalf("a stale command was replayed on connect: %+v", got)
	}
	commands(page)

	if !f.d.PublishCompanionCommand(CompanionCommand{SessionID: "livesession", Expression: "happy", Quip: "<b>ok</b>", TTLms: 4000}) {
		t.Fatal("publish refused")
	}
	got := commands(rail)
	if len(got) != 1 {
		t.Fatalf("rail commands = %+v, want 1 command", got)
	}
	if got[0].Seq <= 0 {
		t.Fatalf("rail command missing monotonic seq: %+v", got[0])
	}
	want := CompanionCommand{Seq: got[0].Seq, SessionID: "livesession", Expression: "happy", Quip: "<b>ok</b>", TTLms: 4000}
	if got[0] != want {
		t.Fatalf("rail commands = %+v, want [%+v]", got, want)
	}
	if got := commands(page); len(got) != 0 {
		t.Fatalf("a session page stream received director commands: %+v", got)
	}
}
