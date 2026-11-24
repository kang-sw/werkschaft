package wsagent

import (
	"fmt"
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// PlaybookCommandSource is the Source of a dashboard command the runner
// offers for a lead playbook (Options.PlaybookCommands), not the harness.
const PlaybookCommandSource = "ws"

// NativeSubagentTool is the native subagent tool the profile's
// subagent_removal item names for role, whatever its grade: a tool the
// harness cannot remove is still the one a session must not use. "" when
// the item names none (or p is nil).
func (p *HarnessProfile) NativeSubagentTool(role CatalogRole) string {
	if p == nil {
		return ""
	}
	for _, entry := range p.Entries {
		if entry.Item != ItemSubagentRemoval || (entry.Role != "" && entry.Role != role) {
			continue
		}
		for _, value := range entry.Values {
			if value != "" && value != CatalogAnyValue {
				return value
			}
		}
	}
	return ""
}

// leadSubagentPolicy resolves the backend's LeadSubagent policy for rec using
// rec's repository root and session config overrides.
func (r *Runner) leadSubagentPolicy(rec Record) LeadSubagentPolicy {
	if !rec.Lead {
		return LeadSubagentPolicy{}
	}
	reg := r.opts.Backends[rec.Backend]
	cfg := r.opts.Config
	if rec.Root != "" {
		cfg.RepoRoot = rec.Root
	}
	if roots, ok := r.opts.Keys.(interface{ Root(string) (string, bool) }); ok {
		if root, found := roots.Root(rec.WSKey); found {
			cfg.RepoRoot = root
		}
	}
	reader, _ := r.opts.Keys.(wsconfig.SessionReader)
	return reg.LeadSubagent(cfg, reader, rec.WSKey)
}

// subagentSteer is the delegation line appended to a hosted conversation's
// prompt when RemovesSubagents holds; empty otherwise. A backend with
// PreferNativeSubagent keeps its lead on nativeFirstSteer instead.
func (r *Runner) subagentSteer(rec Record) string {
	if !RemovesSubagents(rec) {
		return ""
	}
	reg := r.opts.Backends[rec.Backend]
	if rec.Lead {
		policy := r.leadSubagentPolicy(rec)
		if policy.Active {
			if policy.PreferNativeSubagent {
				// Preserve Catalog.NativeSubagentTool(RoleLead) so
				// PreferNativeSubagent == true keeps nativeFirstSteer verbatim.
				return nativeFirstSteer(reg.Catalog.NativeSubagentTool(RoleLead))
			}
			return leadSpawnOnlySteer(policy.NativeSubagentTool, policy.AvoidSameHarnessSpawn)
		}
	}
	role := RoleWorker
	if rec.Lead {
		role = RoleLead
	}
	tool := reg.Catalog.NativeSubagentTool(role)
	if tool == "" {
		return ""
	}
	return fmt.Sprintf("Never use the native `%s` tool. Start a subagent only with the ws `agent.spawn` tool, and continue one with the ws `mailbox.send` tool.", tool)
}

// nativeFirstSteer is the delegation line of a PreferNativeSubagent lead
// (agy): its native subagent tool (named only when the catalog names it)
// explores without ws tools at the harness's default model; anything that
// needs ws tools runs through agent.spawn with a rendered tier passed as
// returned, which may end on agy, restrained by the Antigravity terms risk
// toward an external fixed tier when another harness fits (the same rule as
// its manual's tier line).
func nativeFirstSteer(tool string) string {
	native := "your native subagent tool"
	if tool != "" {
		native = "your native `" + tool + "` tool"
	}
	return "Use " + native + " for exploration and research that needs no ws tools; it runs at your harness's default model, so when a question needs a specific tier, use a read-only ws `agent.spawn` subagent at an external-harness tier instead. " +
		"Run rendered ws playbooks and any work that needs ws tools through the ws `agent.spawn` tool, and continue it with the ws `mailbox.send` tool. " +
		"Pass a rendered tier as returned: a role tier keeps its configured route, model and effort, and may end on agy, which is allowed. An agy route carries the Antigravity terms risk, so when another harness fits the work, choose an external fixed tier such as `claude.small` for `playbook-explore:small` instead; never put a harness prefix before a role tier. " +
		"A native subagent is one-shot: ask a follow-up of a new one; `mailbox.send` continues only agents started with `agent.spawn`."
}

// leadSpawnOnlySteer is the delegation line of a PreferNativeSubagent-capable
// hosted lead when agy.prefer_native_subagent is false: it explicitly prohibits
// the harness's native subagent tool in prompt text and routes all subagent
// work through ws agent.spawn / mailbox.send, optionally keeping the external
// fixed-tier / terms-risk guidance when avoidSameHarness is true.
func leadSpawnOnlySteer(tool string, avoidSameHarness bool) string {
	prohibition := "Never use your native subagent tool."
	if tool != "" {
		prohibition = fmt.Sprintf("Never use the native `%s` tool.", tool)
	}
	tierRule := "Pass a rendered tier as returned: a role tier keeps its configured route, model and effort; never put a harness prefix before a role tier."
	if avoidSameHarness {
		tierRule = "Pass a rendered tier as returned: a role tier keeps its configured route, model and effort, and may end on agy, which is allowed. An agy route carries the Antigravity terms risk, so when another harness fits the work, choose an external fixed tier such as `claude.small` for `playbook-explore:small` instead; never put a harness prefix before a role tier."
	}
	return prohibition + " Run all exploration, research, rendered ws playbooks, and subagent work through the ws `agent.spawn` tool, and continue one with the ws `mailbox.send` tool. " + tierRule
}

// playbookCommands is the lead playbook command list a hosted lead of
// backend offers (Options.PlaybookCommands), nil for a backend without
// BackendRegistration.PlaybookCommands. It is read once, at the first
// need, and kept: a failed read is logged once and offers none until the
// daemon restarts, so a broken package costs no read per turn.
func (r *Runner) playbookCommands(backend string) []wslive.SlashCommand {
	if !r.opts.Backends[backend].PlaybookCommands || r.opts.PlaybookCommands == nil {
		return nil
	}
	r.playbookMu.Lock()
	defer r.playbookMu.Unlock()
	if !r.playbookLoaded {
		r.playbookLoaded = true
		list, err := r.opts.PlaybookCommands()
		if err != nil {
			fmt.Fprintf(r.opts.Log, "agent runner: no playbook commands: %v\n", err)
		}
		for _, cmd := range list {
			cmd.Source, cmd.Layer = PlaybookCommandSource, ""
			r.playbookCmds = append(r.playbookCmds, cmd)
		}
	}
	return r.playbookCmds
}

// withPlaybookCommands is list, the commands a lead's harness offers, with
// rec's playbook commands added; a harness command of the same name gives
// way, since the runner expands that name itself. A worker, or a lead of a
// backend without BackendRegistration.PlaybookCommands, has its list
// returned as it is.
func (r *Runner) withPlaybookCommands(rec Record, list []wslive.SlashCommand) []wslive.SlashCommand {
	if !rec.Lead || !r.opts.Backends[rec.Backend].PlaybookCommands {
		return list
	}
	cmds := r.playbookCommands(rec.Backend)
	// A list a snapshot kept may hold an earlier runner's playbook
	// commands; they are replaced whole, by none when this runner has none
	// (it could not run them).
	out := slices.DeleteFunc(slices.Clone(list), func(c wslive.SlashCommand) bool {
		return c.Source == PlaybookCommandSource || slices.ContainsFunc(cmds, func(p wslive.SlashCommand) bool { return p.Name == c.Name })
	})
	return append(out, cmds...)
}

// playbookTurn is the turn text that runs cmd when it is one of the lead's
// playbook commands: the playbook's body (Options.ExpandCommand) under a
// line naming the command and its arguments. A body that cannot be
// rendered becomes a pointer to the read that gets it. ok is false for any
// other command, which runs as the harness's own.
func (c *conv) playbookTurn(cmd wsinbox.Command) (text string, ok bool) {
	rec := c.record()
	if !rec.Lead || !slices.ContainsFunc(c.r.playbookCommands(rec.Backend), func(p wslive.SlashCommand) bool { return p.Name == cmd.Name }) {
		return "", false
	}
	body := ""
	if expand := c.r.opts.ExpandCommand; expand != nil {
		rendered, err := expand(rec.Backend, rec.workDir(), rec.WSKey, cmd.Name)
		if err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: /%s: playbook not rendered, the turn points at it: %v\n", rec.ReplyID, cmd.Name, err)
		} else {
			body = strings.TrimSpace(rendered)
		}
	}
	return playbookCommandTurn(cmd, body), true
}

// playbookCommandTurn frames body, the rendered playbook of cmd, as the
// turn that runs it; an empty body points at the playbook.read that
// returns it instead.
func playbookCommandTurn(cmd wsinbox.Command, body string) string {
	head := fmt.Sprintf("The user ran the `/%s` command with no arguments.", cmd.Name)
	if args := strings.TrimSpace(cmd.Args); args != "" {
		head = fmt.Sprintf("The user ran the `/%s` command with these arguments:\n\n%s", cmd.Name, args)
	}
	if body == "" {
		return head + fmt.Sprintf("\n\nRead the `%s` playbook with the ws `playbook.read` tool and run it.", cmd.Name)
	}
	return head + fmt.Sprintf("\n\nRun the `%s` playbook below; it is the whole playbook, so it needs no `playbook.read`.\n\n<playbook name=%q>\n%s\n</playbook>", cmd.Name, cmd.Name, body)
}
