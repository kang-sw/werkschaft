package wsagent

import (
	"encoding/json"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

// acpDialect is what one ACP harness adds to the shared ACP core (acpProcess,
// acpMapper): its name in diagnostics, its timeouts, its session-request
// extensions, its model and effort vocabulary, and how it names and shapes
// its native tools.
//
// A hook computes its answer from its arguments and the dialect's own
// constants. It never takes a lock and never calls back into acpProcess or
// acpMapper: the mapper calls hooks while it holds its own lock, under
// streamProcess.feedMu. publishChoices is the one hook that writes outside
// state (the harness's Choices store), and newAudit returns a fresh audit
// adapter.
type acpDialect interface {
	// label names the harness in emitted errors and notices and tags its
	// tool batches.
	label() string
	// timing reports the request timeouts and wakeup quiet windows. It is
	// read at each point of use, so a test that changes a harness's timing
	// variables takes effect at once.
	timing() acpTiming
	// instructions is the dialect's own rule appended, after any instruction
	// file's text, to a non-ephemeral session's system prompt; empty adds
	// none.
	instructions(spec StartSpec) string
	// openMeta is the session request's _meta value carrying the appended
	// system prompt text.
	openMeta(appendText string) map[string]any
	// effortOption is the config option id of the effort (thinking) level.
	effortOption() string
	// modelVariant is the model id that carries effort for base.
	modelVariant(base, effort string) string
	// baseModel splits a model id into its base model and the effort its
	// variant carries ("" for none).
	baseModel(model string) (string, string)
	// permissionModes are the hosted lead's permission mode choices.
	permissionModes() []string
	// cutoverMode is the native mode an exec-cutover or ephemeral session
	// runs in: the one that asks before each command.
	cutoverMode() string
	// classifyTool classifies one native tool call from its name, arguments,
	// and (on a final update) raw output.
	classifyTool(name string, args, output json.RawMessage) acpToolClass
	// mcpToolMatches reports whether a tool call name is tool of MCP server.
	mcpToolMatches(name, server, tool string) bool
	// replyFailure is the in-band model-failure line the reply so far ends
	// with, or "".
	replyFailure(reply string) string
	// usageLimit classifies a prompt error or in-band model failure as a
	// usage-limit alert, or nil.
	usageLimit(msg acpMessage, modelFailure string, now int64) *wslive.Notice
	// publishChoices stores the model and effort choices a session reported
	// for the backend's Choices; either may be empty and keeps the stored
	// one.
	publishChoices(models []ModelChoice, efforts []string)
	// newAudit returns the audit harness name and adapter; a nil adapter
	// keeps no audit capture.
	newAudit(sessionID string) (string, auditAdapter)
	// nativeRowPrefix prefixes a native subagent launch's tool call id to
	// form its live agent row id.
	nativeRowPrefix() string
}

// acpTiming are a dialect's request timeouts and wakeup quiet windows.
type acpTiming struct {
	// init, session, and control bound initialize, the session request, and
	// each later control request.
	init, session, control time.Duration
	// mcpConnect bounds how long a start waits, after its session opened, for
	// the harness to reach the ws MCP route with the session's token.
	mcpConnect time.Duration
	// wakeUsageQuiet closes an unsolicited background-wakeup turn once
	// usage_update arrives and no tool call follows; wakeIdleQuiet is the
	// fallback when no usage_update arrives.
	wakeUsageQuiet, wakeIdleQuiet time.Duration
}

// acpToolClass is how a dialect classifies one native tool call.
type acpToolClass struct {
	// command marks the harness's native shell tool. daemon marks a command
	// the harness runs detached, which holds no turn open; ran reports
	// execution evidence in a final update's raw output.
	command, daemon, ran bool
	// subagent marks the harness's native subagent launch tool; label names
	// its row.
	subagent bool
	label    string
}
