package wsdashboard

import "strings"

// HarnessTraits is how one registered harness differs on the dashboard's
// surfaces. The dashboard never branches on a harness name: the composition
// root hands it these declarations (DaemonOptions.Harnesses), taken from each
// backend's registration. They are runtime-only data and are never written to
// a record, profile, config, or history file. The zero value is a harness
// with nothing special.
type HarnessTraits struct {
	// PushesUsage marks a harness whose live snapshots report the account's
	// usage limits, which the plan-usage store keeps (pushClaude).
	PushesUsage bool
	// SeparateFiveHourHistory marks a harness whose five-hour usage samples go
	// to their own history file and projection instead of the shared one.
	SeparateFiveHourHistory bool
	// UntaggedRowsNative marks a harness whose snapshot rows that name no
	// harness, reply id, or agent kind are the harness's own subagents
	// (hudNative).
	UntaggedRowsNative bool
	// TerminalTakeover marks a harness whose ended terminal session the rail
	// offers to take over into a hosted session (railRevive).
	TerminalTakeover bool
	// ReplayClientName is the MCP clientInfo.name the daemon presents when it
	// replays a hosted session's initialize, a name the MCP server detects the
	// harness from; empty replays the generic name.
	ReplayClientName string
}

// HarnessTraitSet is the registered harnesses' traits by name. A name the set
// lacks is not registered; a nil set registers none.
type HarnessTraitSet map[string]HarnessTraits

// replayClientNameGeneric is the clientInfo name replayed for a harness with
// no ReplayClientName.
const replayClientNameGeneric = "ws-mcp-http-replay"

// normalize returns h lower-cased and trimmed when it names a registered
// harness, else "".
func (s HarnessTraitSet) normalize(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if _, ok := s[h]; ok {
		return h
	}
	return ""
}

func (s HarnessTraitSet) pushesUsage(h string) bool { return s[h].PushesUsage }

func (s HarnessTraitSet) separateFiveHour(h string) bool { return s[h].SeparateFiveHourHistory }

func (s HarnessTraitSet) untaggedRowsNative(h string) bool {
	return s[s.normalize(h)].UntaggedRowsNative
}

func (s HarnessTraitSet) terminalTakeover(h string) bool { return s[h].TerminalTakeover }

// replayClientName is the clientInfo name the replayed initialize presents for
// harness, one the MCP server detects the harness from (internal/mcp
// detectHarnessFromInitializeParams).
func (s HarnessTraitSet) replayClientName(h string) string {
	if name := s[h].ReplayClientName; name != "" {
		return name
	}
	return replayClientNameGeneric
}
