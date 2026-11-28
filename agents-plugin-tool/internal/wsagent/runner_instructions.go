package wsagent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Instruction files a process appends to its system prompt, and the lead workflow manual.

// composeInstructions writes the instruction file a process appends to the
// harness's own system prompt: agent.spawn's system prompt, the profile's
// instruction file or builtin text and, when the profile allows ws tools,
// the conversation's ws session key and direct parent's reply address,
// followed by steer, the native-subagent line (Runner.subagentSteer; "" for
// none). It returns "" when there is nothing to append.
func composeInstructions(dir string, rec Record, p Profile, system, steer string) (string, error) {
	var parts []string
	if text := strings.TrimSpace(system); text != "" {
		parts = append(parts, text)
	}
	if text, ok := p.BuiltinInstructions(); ok {
		parts = append(parts, strings.TrimSpace(text))
	} else if path := p.InstructionsPath(rec.Root); path != "" {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("agent profile %q: read instruction file: %w", rec.Profile, err)
		}
		parts = append(parts, strings.TrimSpace(string(raw)))
	}
	if len(p.WSTools) > 0 {
		key := fmt.Sprintf("Your ws session key is `%s`. Pass it as the `session_key` argument of every ws tool call. This key is bound to worktree `%s`; if work moves to another worktree, use a key minted for that destination (from a direct spawn there or a legitimate acquisition that returns one), because this key is never rebound.", rec.WSKey, rec.Root)
		if rec.Parent != nil {
			key += fmt.Sprintf(" Your direct parent's mailbox reply address is `id:%s`; send it reports or questions with `mailbox.send`.", rec.Parent.ReplyID)
		}
		if steer != "" {
			key += " " + steer
		}
		parts = append(parts, key)
	}
	if len(parts) == 0 {
		return "", nil
	}
	path := filepath.Join(dir, "instructions.md")
	if err := writeFileAtomic(path, []byte(strings.Join(parts, "\n\n")+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// composeLeadInstructions writes the instruction file a hosted lead (Claude,
// agy, Codex) appends to its system prompt: the session key the daemon minted for
// it and, when one rendered, its workflow manual. This prompt teaches both
// from the first turn without depending on a plugin or a tool call. The
// manual is a snapshot of this launch. steer is the native-subagent line
// (Runner.subagentSteer; "" for none). commands marks a lead whose backend
// has BackendRegistration.PlaybookCommands: it has no ws skills, so the
// prompt says what a `/lead-*` reference is, and the manual it carries is
// the only copy it is told to read.
func composeLeadInstructions(dir string, rec Record, manual, steer string, commands bool) (string, error) {
	text := fmt.Sprintf("Your ws session key is `%s`. Pass it as the `session_key` argument of every ws tool call. "+
		"This hosted session already has a key; do not mint a replacement (no ferrule call, no fresh-bootstrap key). ", rec.WSKey)
	if steer != "" {
		text += steer + " "
	}
	if commands {
		text += "This session has no ws skills: a `/<name>` reference such as `/lead-run` names a workflow playbook. " +
			"The user runs one as that dashboard command, which sends you its playbook; you run one by reading it with the ws `playbook.read` tool and following it. "
	}
	if manual = strings.TrimSpace(manual); manual != "" && commands {
		text += "The workflow manual below is binding and stays in this prompt across compaction. " +
			"Use the session state delivered with your first turn or in a `## Hosted Session Restore` bundle: it already carries fresh `## Session State` after compaction or daemon restart, so do not call `workflow_state` right after receiving one. " +
			"Call the ws `workflow_state` tool with this key only for mid-session refreshes, if a restore bundle reports a render error, or where the manual says to invoke `/lead-revive`.\n\n" + manual
	} else if manual != "" {
		text += "The workflow manual below is binding and stays in this prompt across compaction. " +
			"Use the session state delivered with your first turn or in a `## Hosted Session Restore` bundle: it already carries fresh `## Session State` after compaction or daemon restart, so do not call `workflow_state` right after receiving one. " +
			"Call `workflow_state` with this key only for mid-session refreshes or if a restore bundle reports a render error.\n\n" + manual
	} else {
		text += "At session start, call the workflow_manual tool with this key and follow it; after any compaction, call workflow_state for fresh session state."
	}
	if rec.CompanionRole == companionRoleWork {
		if addendum := strings.TrimSpace(rec.CompanionSystemPrompt); addendum != "" {
			text += "\n\n" + addendum
		}
	}
	path := filepath.Join(dir, "instructions.md")
	if err := writeFileAtomic(path, []byte(text+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// combineLeadManualAndAddendum joins the full Lead workflow manual with a
// companion-work Brain persona/memory addendum when either or both are present.
func combineLeadManualAndAddendum(manual, addendum string) string {
	manual = strings.TrimSpace(manual)
	addendum = strings.TrimSpace(addendum)
	if manual == "" {
		return addendum
	}
	if addendum == "" {
		return manual
	}
	return manual + "\n\n" + addendum
}

// composeCompanionInstructions writes the instruction file for a daemon-managed
// companion hosted session (Tier-1 companion-body or Tier-2 companion-work).
// It injects the minted ws session key and the companion prompt directly,
// bypassing the standard lead workflow manual.
func composeCompanionInstructions(dir string, rec Record) (string, error) {
	var b strings.Builder
	fmt.Fprintf(&b, "Your ws session key is `%s`. Pass it as the `session_key` argument of every ws tool call. "+
		"This hosted session already has a key; do not mint a replacement.", rec.WSKey)
	if prompt := strings.TrimSpace(rec.CompanionSystemPrompt); prompt != "" {
		b.WriteString("\n\n")
		b.WriteString(prompt)
	}
	b.WriteByte('\n')
	path := filepath.Join(dir, "instructions.md")
	if err := writeFileAtomic(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// leadManual is the workflow manual rec's lead reads from its first turn
// (Options.LeadManual), or "" when the daemon supplies no renderer or the
// render failed, which is logged: the lead then starts with its key alone.
func (c *conv) leadManual(rec Record) string {
	if c.r.opts.LeadManual == nil {
		return ""
	}
	text, err := c.r.opts.LeadManual(rec.Backend, rec.workDir(), rec.WSKey)
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: workflow manual not rendered, the lead's prompt carries its key alone: %v\n", rec.ReplyID, err)
		return ""
	}
	return text
}
