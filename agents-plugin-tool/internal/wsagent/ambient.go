package wsagent

import (
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

const ambientBlockName = "ws-ambient"

// WorkflowLanguageInstruction is the canonical workflow.lang instruction used
// by both rendered workflow text and hosted-lead turn ambient.
func WorkflowLanguageInstruction(lang string) string {
	lang = strings.TrimSpace(lang)
	if lang == "" {
		return ""
	}
	return "Respond to the user in " + lang +
		". Write ticket frontmatter titles in that language. Keep all internal reasoning, subagent prompts, code comments, and other AI-authored artifacts in English."
}

func ambientBlock(instruction string) string {
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return ""
	}
	return "<" + ambientBlockName + ">\n" + instruction + "\n</" + ambientBlockName + ">"
}

// resolveAmbient resolves one conversation's ambient block outside its send
// path. The key store is an optional session-layer reader; older/test stores
// without that capability still resolve global, repo, and builtin values.
func (c *conv) resolveAmbient() (block, lang string, err error) {
	if !c.rec.Lead {
		return "", "", nil
	}
	cfg := c.r.opts.Config
	cfg.RepoRoot = c.rec.Root
	if roots, ok := c.r.opts.Keys.(interface{ Root(string) (string, bool) }); ok {
		if root, found := roots.Root(c.rec.WSKey); found {
			cfg.RepoRoot = root
		}
	}
	reader, _ := c.r.opts.Keys.(wsconfig.SessionReader)
	resolver := wsconfig.NewResolver(cfg, nil, reader, nil)
	resolved, err := resolver.Get(c.rec.WSKey, wsconfig.ItemWorkflowLang)
	if err != nil {
		return "", "", fmt.Errorf("resolve workflow.lang ambient: %w", err)
	}
	return ambientBlock(WorkflowLanguageInstruction(resolved.Value)), strings.TrimSpace(resolved.Value), nil
}

// refreshAmbient replaces the loop-owned cache after creation/restoration or
// a successful config.tune. A failed refresh keeps the last known-good block.
func (c *conv) refreshAmbient() {
	block, lang, err := c.resolveAmbient()
	if err != nil {
		fmt.Fprintf(c.r.opts.Log, "agent %s: %v\n", c.rec.ReplyID, err)
		return
	}
	c.ambient = block
	// The same resolution, kept for readers outside the loop (the language
	// of a machine-facing notice; ExecStopNote).
	c.ambientLang.Store(&lang)
}

// turnInput composes model-only turn input. Compaction notes go first, then
// the passive receipts of dashboard-run commands, and ambient stays last. Both are kept out of dashboard and history user text.
//
// whiteboard is the whiteboard-only state the turn's entries leave; a lead in
// the sticky whiteboard-only state (Record.WhiteboardOnly) gets the directive
// on every turn, not just the one a flagged message opens. The directive joins
// the ambient block once, model-only.
func (c *conv) turnInput(text string, whiteboard bool) string {
	whiteboardOnly := c.rec.Lead && (whiteboard || c.rec.WhiteboardOnly)
	if !whiteboardOnly {
		// A staged whiteboard advisory means nothing outside the mode.
		c.passive.clearAdvisory()
	} else if c.passive.hasAdvisoryRevision() {
		rev, ok := c.boardRevision()
		c.passive.dropStaleAdvisory(rev, ok)
	}
	if block, took := c.passive.render(c.openGen()); block != "" {
		// Passive receipts of dashboard-run commands ride this send; they are
		// acknowledged only once the send is accepted (passiveTook).
		c.passiveTook = took
		if strings.TrimSpace(text) == "" {
			text = block
		} else {
			text = block + "\n\n" + text
		}
	}
	text = c.compactInput(text)
	return c.appendAmbient(text, whiteboardOnly)
}

// appendAmbient appends the conversation's cached <ws-ambient> block to text,
// folding in whiteboardFirstDirective when whiteboardOnly is true.
func (c *conv) appendAmbient(text string, whiteboardOnly bool) string {
	ambient := c.ambient
	if whiteboardOnly {
		ambient = ambientWithDirective(ambient, whiteboardFirstDirective)
	}
	if ambient == "" {
		return text
	}
	if strings.TrimSpace(text) == "" {
		return ambient
	}
	return text + "\n\n" + ambient
}

// whiteboardFirstDirective is what a message sent with the composer's
// whiteboard-only toggle on adds to the model's input.
const whiteboardFirstDirective = "The user reads your output through the whiteboard only. Record every response, progress note, and explanation on the relevant topic with wb:log or wb:panel, and keep plain-text output to a minimum."

// ambientWithDirective joins directive into block, an already rendered
// ambient block (empty when none), keeping a single <ws-ambient> element.
func ambientWithDirective(block, directive string) string {
	closing := "\n</" + ambientBlockName + ">"
	if block == "" {
		return ambientBlock(directive)
	}
	if body, ok := strings.CutSuffix(block, closing); ok {
		return body + "\n" + directive + closing
	}
	return block + "\n\n" + ambientBlock(directive)
}

// steerWithDirective appends the whiteboard-only directive, as its own
// ambient block, to a steered text when the lead is in the whiteboard-only
// state recs leave it in (a steered user message may set or clear it, as a
// turn's input does); otherwise the text is returned unchanged. It does not
// persist that state: the steering paths call noteSteerWhiteboard first, and
// folding recs again over the result changes nothing.
func (c *conv) steerWithDirective(text string, recs []wsinbox.Record) string {
	if !c.rec.Lead || !whiteboardOnlyAfterRecs(c.rec.WhiteboardOnly, recs) {
		return text
	}
	return text + "\n\n" + ambientBlock(whiteboardFirstDirective)
}

// directedSteer builds the durable steer for recs: Text carries the directive
// when flagged and Clean the plain text a requeue falls back to.
func (c *conv) directedSteer(text string, recs []wsinbox.Record, mark uint64) durableSteer {
	steer := durableSteer{Text: c.steerWithDirective(text, recs), Mark: mark}
	if steer.Text != text {
		steer.Clean = text
	}
	return steer
}

// RefreshAmbient synchronously republishes every open hosted lead's rendered
// ambient block. config.tune calls it only after a successful workflow.lang
// set/reset, so the next turn observes the new value without polling files.
func (r *Runner) RefreshAmbient() {
	r.mu.Lock()
	convs := make([]*conv, 0, len(r.convs))
	for _, c := range r.convs {
		convs = append(convs, c)
	}
	r.mu.Unlock()
	for _, c := range convs {
		c.do(func() { c.refreshAmbient() })
	}
}
