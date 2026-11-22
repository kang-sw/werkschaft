package wsagent

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsawake"
	"github.com/kang-sw/devenv/internal/wsconfig"
)

// Keep-awake (agent.keep_awake_during_turns): while the setting is on, the
// runner holds one daemon-wide idle-system-sleep assertion from its first
// live turn to its last. Membership and the setting live in memory only
// (wsawake.Core); the turn path adds no file operation, and every OS call
// runs on the core's own goroutine, never on a conversation loop.
//
// A conversation instance is a member while it is not closed and its turn
// runs: Runner-admitted input from the moment its send is attempted
// (kickInput sets turn before the send), a standalone prompt while its send
// is in flight (sendStandalonePrompt), a native compaction command until its
// command lifecycle settles (compactCommand), and a turn the harness started
// itself or a rejoin took as running. A tool call, approval or retry inside
// the turn keeps it. Queued input, an idle process, terminal sessions and
// work that outlives a turn (background or exec jobs) do not count.

// newKeepAwake builds the runner's core over the platform backend, or the
// test backend in opts.
func newKeepAwake(opts Options) *wsawake.Core {
	backend := opts.keepAwake
	if backend == nil {
		backend = wsawake.Platform(opts.Log)
	}
	return wsawake.New(backend, opts.Log)
}

// ReloadGlobalConfig rereads the runner's global settings that change at
// runtime: today agent.keep_awake_during_turns. The daemon calls it once its
// endpoint is published and after every invalidation (an MCP config.tune
// set or reset, a dashboard settings write). The read and the publish are
// one serialized step, so of overlapping reloads the last read wins, and a
// change applies at once to turns already running.
func (r *Runner) ReloadGlobalConfig() {
	r.awakeReloadMu.Lock()
	defer r.awakeReloadMu.Unlock()
	r.awake.SetEnabled(r.readKeepAwake())
}

// readKeepAwake resolves the setting from the global layer (the key is
// global-only), falling back to the builtin false when it is absent or does
// not parse.
func (r *Runner) readKeepAwake() bool {
	key := wsconfig.ItemAgentKeepAwakeDuringTurns
	resolver := wsconfig.NewResolver(r.opts.Config, map[string]string{key: "false"}, nil, nil)
	rv, err := resolver.Get("", key)
	if err != nil {
		fmt.Fprintf(r.opts.Log, "keep-awake: read %s: %v\n", key, err)
		return false
	}
	on, err := strconv.ParseBool(strings.TrimSpace(rv.Value))
	return err == nil && on
}

// wantAwake is the conversation's membership as its loop state shows it.
func (c *conv) wantAwake() bool {
	return !c.closed && (c.turn || c.compactCommand || c.awakeSending)
}

// syncAwake publishes the conversation's membership when it changed. It runs
// in the loop: after every loop step, and right before a send so the send
// already has membership. While a rejoin replays the holder's records the
// membership stays as it was, since the replay is what tells whether a turn
// runs; replayDone publishes the reconstructed state (wrapHold's
// conservative running turn included) at the next step.
func (c *conv) syncAwake() {
	if c.replaying && !c.closed {
		return
	}
	c.publishAwake(c.wantAwake())
}

// publishAwake sets the membership of this conversation instance. Keying by
// instance fences a later generation of the same session, and a repeated
// value changes nothing.
func (c *conv) publishAwake(live bool) {
	if live == c.awakeMember {
		return
	}
	c.awakeMember = live
	c.r.awake.SetMember(c, live)
}
