package mcp

import (
	"fmt"
	"io"
	"strings"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsrsrc"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// HostedRender is what a hosted lead's backend registration
// (wsagent.BackendRegistration) says about text the daemon renders for it:
// Harness, the harness its workflow manual renders under ("" for none), and
// PlaybookCommands, whether its sessions run without ws's plugin skills and
// reach ws only as the daemon's own server. ToolNameScheme and CallFallback
// keep its generated tool index identical to the connection's discovery.
// PreferNativeSubagent selects the native-first wording of the text the lead
// executes itself (nativeFirstTerminology).
type HostedRender struct {
	Harness              string
	PlaybookCommands     bool
	PreferNativeSubagent bool
	ResolveLeadSubagent  func(cfg wsconfig.Options, reader wsconfig.SessionReader, wsKey string) wsagent.LeadSubagentPolicy
	ToolNameScheme       string
	CallFallback         bool
	DirectTools          []string
	ExecCutover          string
	DashboardControl     bool
	Boards               *WhiteboardStores
	Records              AgentRecordReader
}

type emptyRecordReader struct{}

func (emptyRecordReader) RecordByReplyID(string) (wsagent.Record, bool) {
	return wsagent.Record{}, false
}

// hostedRenderServer is a throwaway server that renders for the hosted lead
// whose key is key as that lead's own connection would: env is the
// environment its ws-mcp serves under (the selected package's resource tree
// and policy) and root its project root. It serves no harness process, so
// it never adopts or validates a hosted session itself; release returns it.
func hostedRenderServer(env []string, root string, render HostedRender, key string) (*Server, func(), error) {
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case envHostedSession, envHostedSessionKey, envHostedSessionRow, envHarnessPID:
			continue
		}
		kept = append(kept, kv)
	}
	records := render.Records
	if records == nil {
		records = emptyRecordReader{}
	}
	s := NewServerWithOptions(ServerOptions{Root: root, Env: kept, Harness: render.Harness, Diag: io.Discard, BoardOwner: render.Boards != nil, Boards: render.Boards, RecordReader: records})
	// The harness is declared here, not detected: a name the detection list
	// does not know (agy) still selects the manual's overlay. leadRender
	// renders as the daemon-hosted connection, which lists the agent tools.
	var names []string
	for _, tool := range tools() {
		names = append(names, tool["name"].(string))
	}
	aliases, err := buildToolAliasTable(render.ToolNameScheme, names)
	if err != nil {
		s.Close()
		return nil, nil, err
	}
	s.descriptor = connectionDescriptor{manualHarness: render.Harness, playbookCommands: render.PlaybookCommands, preferNativeSubagent: render.PreferNativeSubagent, resolveLeadSubagent: render.ResolveLeadSubagent, aliases: aliases, callFallback: render.CallFallback, directTools: render.DirectTools, leadRender: true}
	s.descriptorResolved = true
	if render.ExecCutover != "" {
		s.execCutover = render.ExecCutover
	}
	if render.DashboardControl {
		s.dashboardControlEffective = true
	} else if render.Records != nil {
		if rec, ok := render.Records.RecordByReplyID(wsmailbox.ReplyID(key)); ok && rec.Lead && rec.Parent == nil && rec.ClosedAt == "" {
			if isCompanionBodyRecord(rec) {
				// Tier-1 body: the guide subset only (tabs/dom/highlight/monitor).
				s.dashboardGuideEffective = true
			} else if rec.DashboardControlAccess || isCompanionWorkRecord(rec) {
				s.dashboardControlEffective = true
			}
		}
	}
	// A render may start the session-start tracker check (a background
	// fetch); the server is released once that settles, without holding up
	// the caller.
	release := func() { go func() { s.tracker.checks.Wait(); s.Close() }() }
	if _, ok := s.sessions.lookup(key); !ok {
		release()
		return nil, nil, fmt.Errorf("session key %q has no record", key)
	}
	return s, release, nil
}

// RenderLeadManual renders a hosted lead's workflow manual for its system
// prompt. The daemon delivers the mutable session state in turn input.
func RenderLeadManual(env []string, root string, render HostedRender, key string) (string, error) {
	s, release, err := hostedRenderServer(env, root, render, key)
	if err != nil {
		return "", fmt.Errorf("workflow manual: %w", err)
	}
	defer release()
	resp := s.renderWorkflowManual(nil, map[string]any{"session_key": key}, false)
	text := ""
	if result, ok := resp.Result.(map[string]any); ok {
		if content, ok := result["content"].([]map[string]string); ok && len(content) > 0 {
			text = content[0]["text"]
		}
	}
	if responseIsError(resp) {
		return "", fmt.Errorf("workflow manual: %s", text)
	}
	return strings.TrimSpace(strings.TrimPrefix(text, workflowManualReadFullNotice)), nil
}

// RenderHostedSessionState reads the current state for a hosted lead's first
// turn, using the daemon's agent key and whiteboard stores.
func (a *AgentKeyStore) RenderHostedSessionState(key string) (string, error) {
	rec, err := a.store.readStateWithError(key, partAgenda|partTodos|partWhiteboard)
	if err != nil {
		return "", err
	}
	return renderSessionState(rec), nil
}

// RenderHostedRestoreBundle reads the current split session state and renders
// the transient bundle used after native compaction and daemon restart.
func RenderHostedRestoreBundle(env []string, root string, render HostedRender, key, cause string, children []wsagent.AgentChild) (string, error) {
	s, release, err := hostedRenderServer(env, root, render, key)
	if err != nil {
		return "", fmt.Errorf("restore bundle: %w", err)
	}
	defer release()
	rec, ok := s.sessions.readState(key, partAll)
	if !ok {
		return "", fmt.Errorf("restore bundle: session key %q has no state", key)
	}
	whiteboardOn := false
	if (sessionEntry{scope: toolRole(rec.Scope), parent: rec.Parent}).isTopLevelLead() {
		knobResolver := s.sessionResolver(key, builtinConfigDefaults())
		if on, err := s.whiteboardEnabled(&knobResolver, key); err == nil {
			whiteboardOn = on
		}
	}
	recentRecv, totalRecv := recentRecvMailForRestore(s.getenv(wsstate.EnvCacheHome), key)
	return renderHostedRestoreBundleWithMail(key, cause, rec, children, whiteboardOn, recentRecv, totalRecv), nil
}

func recentRecvMailForRestore(cacheHome, key string) ([]mailboxHistoryRecord, int) {
	records, err := readMailboxHistory(cacheHome, key)
	if err != nil {
		return nil, 0
	}
	var recent []mailboxHistoryRecord
	total := 0
	for _, r := range records {
		if r.Direction != mailboxHistoryRecv {
			continue
		}
		total++
		if len(recent) < restoreRecentMailMax {
			recent = append(recent, r)
		}
	}
	return recent, total
}

// RenderLeadPlaybook renders playbook name for the hosted lead whose key is
// key, as playbook.read returns it on that session's own connection
// (hostedRenderServer). The daemon sends it as the turn of the dashboard
// command that runs the playbook, so the session needs no read of its own.
func RenderLeadPlaybook(env []string, root string, render HostedRender, key, name string) (string, error) {
	s, release, err := hostedRenderServer(env, root, render, key)
	if err != nil {
		return "", fmt.Errorf("playbook %s: %w", name, err)
	}
	defer release()
	text, err := s.readPlaybook(name, key, nil)
	if err != nil {
		return "", fmt.Errorf("playbook %s: %w", name, err)
	}
	return strings.TrimSpace(text), nil
}

// HostedPlaybookCommands is the dashboard command list of a hosted lead
// without ws's plugin skills: one command per user-invocable lead skill of
// the package env selects (a skill shim whose preload block reads its own
// playbook, wsrsrc.PreloadCommands), described as the skill is. The
// workflow manual's own skill is left out: the lead's prompt already
// carries the manual.
func HostedPlaybookCommands(env []string) ([]wslive.SlashCommand, error) {
	namespace := ""
	for _, kv := range env {
		if name, value, ok := strings.Cut(kv, "="); ok && strings.EqualFold(name, envNamespace) {
			namespace = strings.TrimSpace(value)
		}
	}
	skills, err := wsrsrc.PackageSkills(namespace)
	if err != nil {
		return nil, err
	}
	found, err := wsrsrc.PreloadCommands(skills)
	if err != nil {
		return nil, err
	}
	out := make([]wslive.SlashCommand, 0, len(found))
	for _, cmd := range found {
		if cmd.Name == workflowManualPlaybookName {
			continue
		}
		out = append(out, wslive.SlashCommand{Name: cmd.Name, Description: cmd.Description})
	}
	return out, nil
}
