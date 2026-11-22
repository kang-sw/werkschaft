package wsagent

import (
	"testing"

	"github.com/kang-sw/devenv/internal/wslive"
)

// A nested child's report is handed to its conversation parent while the
// child's own loop keeps handling events that copy its record (a harness
// command list, offered through the record's playbook commands): the
// delivery's take of the child's pending reports must not race those
// copies (go test -race), and the report still reaches the parent once.
func TestNestedReportDeliveryDoesNotRaceTheChildLoop(t *testing.T) {
	h := agyRoutingHarness(t)
	agyLead := verifiedLeadCaller(leadParent.ReplyID, h.root, BackendAgy, h.config.Config)
	parent, pp := h.agentRunning(agyLead, AgentSpawnRequest{Prompt: "work", Tier: "agy.small", Authority: "read-only"})
	child, cp := h.agentRunning(h.holderOf(parent), AgentSpawnRequest{Prompt: "nested", Tier: "agy.small", Authority: "read-only"})
	pp.reply("parent done")
	commands := func(n int) {
		for range n {
			cp.events <- Event{Live: &wslive.Event{Kind: wslive.KindCommands, Commands: []wslive.SlashCommand{{Name: "compact"}}}}
		}
	}
	commands(20)
	cp.reply("child done")
	// The settle starts the delivery off the child's loop; the loop keeps
	// copying its record for each command list meanwhile.
	commands(200)
	eventually(t, "the nested report delivered", func() bool {
		rec := h.record(child)
		return rec.LastTurn != nil && len(rec.PendingReports) == 0
	})
	if sent := pp.nextSent(t); sent == "" {
		t.Fatal("the parent got an empty report turn")
	}
	pp.reply("report read")
	h.asleep(child)
	h.asleep(parent)
}
