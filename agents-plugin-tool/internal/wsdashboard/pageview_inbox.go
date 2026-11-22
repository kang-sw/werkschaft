package wsdashboard

import (
	"cmp"
	"html/template"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsi18n"
	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// HeldView is a message the user sent from the page that the session has
// not received yet: it sits in the session's board-answer inbox until the
// host adapter drains it at the end of a turn, or at the next tool boundary
// once promoted to steering, and can be cancelled until then.
type HeldView struct {
	BoardTargets map[string]string
	// ID is the inbox entry's ID, which the cancel and steer controls name.
	ID string
	// Steer is set once the user promoted the message to steering; the
	// bubble then shows a steer marker instead of the steer control.
	Steer bool
	// At is the send's time (epoch ms), kept by a steer.
	At int64
	// Parts are the send's text; an entry merged before sends stopped
	// merging holds one per send, and the bubble divides them.
	Parts []string
	// Content projects grouped Parts without changing the stored send.
	Content []SelectionContentPart
	Chips   []wsinbox.Chip
	Topics  []wsinbox.TopicNote
	// Mentions are the board text the message points at, shown as capsules
	// after the answer capsules.
	Mentions      []wsinbox.Mention
	CloseRequests []wsinbox.CloseRequest
	// Picks and Consoles are the picked page elements and attached console
	// errors, shown as capsules after the mentions.
	Picks    []wsinbox.Pick
	Consoles []wsinbox.Console
	// Images are the entry's attached images.
	Images []AttachmentView
	// Command is a command entry's slash command; the bubble shows its
	// typed line. Its steer control sends the line as steered text the agent
	// acts on itself (wsinbox.Steer).
	Command *wsinbox.Command
	Lang    string
}

// AllImages returns every attached image for the held message, combining
// selection-owned images and loose images without duplicates.
func (h HeldView) AllImages() []AttachmentView {
	return collectBubbleImages(h.Content, h.Images)
}

// Hydrated projects inline anchor-matched capsules inside Parts/Content and
// deduplicates matched items from the header chip strip.
func (h HeldView) Hydrated() hydratedUserMessage {
	return hydrateUserMessage(h.Lang, h.Answers(), h.Mentions, h.CloseRequests, h.Picks, h.Consoles, h.BoardTargets, "", h.Parts, h.Content)
}

// AllImages returns every attached image for the entry, combining
// selection-owned images and loose images without duplicates.
func (e ConvEntry) AllImages() []AttachmentView {
	return collectBubbleImages(e.Content, e.Images)
}

// Hydrated projects inline anchor-matched capsules inside Text/Content and
// deduplicates matched items from the header chip strip.
func (e ConvEntry) Hydrated() hydratedUserMessage {
	return hydrateUserMessage(e.Lang, e.Answers, e.Mentions, e.CloseRequests, e.Picks, e.Consoles, e.BoardTargets, e.Text, nil, e.Content)
}

func collectBubbleImages(content []SelectionContentPart, loose []AttachmentView) []AttachmentView {
	if len(content) == 0 {
		return loose
	}
	var out []AttachmentView
	seen := map[int]bool{}
	add := func(img AttachmentView) {
		if img.Num > 0 {
			if seen[img.Num] {
				return
			}
			seen[img.Num] = true
		}
		out = append(out, img)
	}
	for _, p := range content {
		if p.Selection != nil {
			for _, img := range p.Selection.Images {
				add(img)
			}
		}
	}
	for _, img := range loose {
		add(img)
	}
	return out
}

type hydratedUserMessage struct {
	AnswerChips   []conversationChip
	MentionChips  []conversationChip
	CloseRequests []wsinbox.CloseRequest
	Picks         []wsinbox.Pick
	Consoles      []wsinbox.Console
	Grouped       bool
	Body          template.HTML
	Parts         []template.HTML
}

var (
	inlineAnswerAnchorRe    = regexp.MustCompile(`^\(([0-9]{1,6})\)=([A-Za-z0-9]{1,16})`)
	inlinePanelAnchorRe     = regexp.MustCompile(`^\{\{(p[1-9][0-9]*)\}\}`)
	inlineLeadingNewlineRe  = regexp.MustCompile(`^(?:\r?\n){1,2}`)
	inlineTrailingNewlineRe = regexp.MustCompile(`(?:\r?\n){1,2}$`)
)

func isAnchorWordByte(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

func singleLinePreview(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

type inlineHydrator struct {
	lang     string
	targets  map[string]string
	answers  []conversationChip
	mentions []conversationChip
	picks    []wsinbox.Pick
	consoles []wsinbox.Console
	usedAns  []bool
	usedMen  []bool
	usedPick []bool
	usedCon  []bool
}

func newInlineHydrator(lang string, answers []wsinbox.AnswerLine, mentions []wsinbox.Mention, picks []wsinbox.Pick, consoles []wsinbox.Console, targets map[string]string) *inlineHydrator {
	ac := answerChips(answers, targets)
	mc := mentionChips(mentions, targets)
	return &inlineHydrator{
		lang:     lang,
		targets:  targets,
		answers:  ac,
		mentions: mc,
		picks:    picks,
		consoles: consoles,
		usedAns:  make([]bool, len(ac)),
		usedMen:  make([]bool, len(mc)),
		usedPick: make([]bool, len(picks)),
		usedCon:  make([]bool, len(consoles)),
	}
}

func (h *inlineHydrator) hasCandidates() bool {
	return len(h.answers) > 0 || len(h.mentions) > 0 || len(h.picks) > 0 || len(h.consoles) > 0
}

func (h *inlineHydrator) jumpButton(target, topicTarget string) string {
	if target == "" {
		return ""
	}
	label := wsi18n.T(h.lang, "entry.whiteboard.topic")
	return `<button type="button" class="icapsule-jump" data-wb-nav="` + template.HTMLEscapeString(target) + `" data-wb-topic="` + template.HTMLEscapeString(topicTarget) + `" title="` + template.HTMLEscapeString(label) + `" aria-label="` + template.HTMLEscapeString(label) + `">&#8599;</button>`
}

func (h *inlineHydrator) renderAnswer(c conversationChip) string {
	val := c.Value
	if val == "" {
		val = "note"
	}
	esc := template.HTMLEscapeString
	if strings.TrimSpace(c.Note) == "" {
		nav := ""
		if c.Target != "" {
			nav = ` role="link" tabindex="0" data-wb-nav="` + esc(c.Target) + `" data-wb-topic="` + esc(c.TopicTarget) + `"`
		}
		return `<span class="icapsule icapsule-answer"` + nav + `><b class="icapsule-head">(` + esc(c.IDs) + `)</b> <span class="icapsule-val">` + esc(val) + `</span></span>`
	}
	sub := singleLinePreview(c.Note)
	var b strings.Builder
	b.WriteString(`<span class="icapsule icapsule-answer" data-icapsule="1" role="button" tabindex="0" aria-haspopup="dialog" aria-expanded="false" title="` + esc(c.Note) + `">`)
	b.WriteString(`<b class="icapsule-head">(` + esc(c.IDs) + `)</b> <span class="icapsule-val">` + esc(val) + `</span>`)
	if sub != "" {
		b.WriteString(`<span class="icapsule-sub">` + esc(sub) + `</span>`)
	}
	b.WriteString(`<template class="icapsule-source"><div class="icapsule-pop-hd"><b class="icapsule-pop-title">(` + esc(c.IDs) + `) ` + esc(val) + `</b>` + h.jumpButton(c.Target, c.TopicTarget) + `</div><div class="icapsule-pop-text">` + esc(c.Note) + `</div></template></span>`)
	return b.String()
}

func (h *inlineHydrator) renderTopic(c conversationChip) string {
	esc := template.HTMLEscapeString
	sub := singleLinePreview(c.Note)
	var b strings.Builder
	b.WriteString(`<span class="icapsule icapsule-topic" data-icapsule="1" role="button" tabindex="0" aria-haspopup="dialog" aria-expanded="false" title="` + esc(c.Note) + `">`)
	b.WriteString(`<b class="icapsule-head">[` + esc(c.Topic) + `]</b>`)
	if sub != "" {
		b.WriteString(`<span class="icapsule-sub">` + esc(sub) + `</span>`)
	}
	b.WriteString(`<template class="icapsule-source"><div class="icapsule-pop-hd"><b class="icapsule-pop-title">[` + esc(c.Topic) + `]</b>` + h.jumpButton(c.Target, c.TopicTarget) + `</div><div class="icapsule-pop-text">` + esc(c.Note) + `</div></template></span>`)
	return b.String()
}

func (h *inlineHydrator) renderMention(c conversationChip, panelID string) string {
	esc := template.HTMLEscapeString
	head := "[" + c.Topic + "]"
	if c.IDs != "" {
		head += " (" + c.IDs + ")"
	}
	popTitle := head
	subSrc := c.Text
	if panelID != "" {
		head = panelID
		popTitle = "[" + c.Topic + "] " + panelID
		if rest, ok := strings.CutPrefix(c.Text, panelID+" · "); ok {
			subSrc = rest
		}
	}
	sub := singleLinePreview(subSrc)
	var b strings.Builder
	b.WriteString(`<span class="icapsule icapsule-mention" data-icapsule="1" role="button" tabindex="0" aria-haspopup="dialog" aria-expanded="false" title="` + esc(c.Text) + `">`)
	b.WriteString(`<b class="icapsule-head">` + esc(head) + `</b>`)
	if sub != "" {
		b.WriteString(`<span class="icapsule-sub">` + esc(sub) + `</span>`)
	}
	b.WriteString(`<template class="icapsule-source"><div class="icapsule-pop-hd"><b class="icapsule-pop-title">` + esc(popTitle) + `</b>` + h.jumpButton(c.Target, c.TopicTarget) + `</div><pre class="icapsule-pop-quote">` + esc(c.Text) + `</pre></template></span>`)
	return b.String()
}

func (h *inlineHydrator) renderPick(p wsinbox.Pick) string {
	esc := template.HTMLEscapeString
	title := p.URL + "\n" + p.Selector
	if p.Text != "" {
		title += "\n" + p.Text
	}
	sub := singleLinePreview(p.Text)
	var b strings.Builder
	b.WriteString(`<span class="icapsule icapsule-pick" data-icapsule="1" role="button" tabindex="0" aria-haspopup="dialog" aria-expanded="false" title="` + esc(title) + `">`)
	b.WriteString(`<b class="icapsule-head icapsule-mono">` + esc(p.Selector) + `</b>`)
	if sub != "" {
		b.WriteString(`<span class="icapsule-sub">` + esc(sub) + `</span>`)
	}
	b.WriteString(`<template class="icapsule-source"><div class="icapsule-pop-hd"><b class="icapsule-pop-title icapsule-mono">` + esc(p.Selector) + `</b></div><div class="icapsule-pop-url">` + esc(p.URL) + `</div>`)
	if p.Text != "" {
		b.WriteString(`<div class="icapsule-pop-text">` + esc(p.Text) + `</div>`)
	}
	b.WriteString(`</template></span>`)
	return b.String()
}

func (h *inlineHydrator) renderConsole(c wsinbox.Console) string {
	esc := template.HTMLEscapeString
	head := "console ×" + strconv.Itoa(len(c.Lines))
	joined := strings.Join(c.Lines, "\n")
	title := c.URL
	if joined != "" {
		title += "\n" + joined
	}
	var b strings.Builder
	b.WriteString(`<span class="icapsule icapsule-console" data-icapsule="1" role="button" tabindex="0" aria-haspopup="dialog" aria-expanded="false" title="` + esc(title) + `">`)
	b.WriteString(`<b class="icapsule-head">` + esc(head) + `</b><span class="icapsule-sub">` + esc(c.URL) + `</span>`)
	b.WriteString(`<template class="icapsule-source"><div class="icapsule-pop-hd"><b class="icapsule-pop-title">` + esc(head) + `</b></div><div class="icapsule-pop-url">` + esc(c.URL) + `</div><pre class="icapsule-pop-lines">` + esc(joined) + `</pre></template></span>`)
	return b.String()
}

func (h *inlineHydrator) hydrateProse(prose string) template.HTML {
	if prose == "" || !h.hasCandidates() {
		return markedText(h.lang, prose)
	}
	var b strings.Builder
	last := 0
	for i := 0; i < len(prose); {
		switch prose[i] {
		case '(':
			if i == 0 || !isAnchorWordByte(prose[i-1]) {
				if m := inlineAnswerAnchorRe.FindStringSubmatchIndex(prose[i:]); m != nil {
					id, val := prose[i+m[2]:i+m[3]], prose[i+m[4]:i+m[5]]
					matched := -1
					for k, c := range h.answers {
						if h.usedAns[k] || c.Topic != "" {
							continue
						}
						wantVal := c.Value
						if wantVal == "" {
							wantVal = "note"
						}
						if c.IDs == id && wantVal == val {
							matched = k
							break
						}
					}
					if matched >= 0 {
						b.WriteString(string(markedText(h.lang, prose[last:i])))
						b.WriteString(h.renderAnswer(h.answers[matched]))
						h.usedAns[matched] = true
						i += m[1]
						last = i
						continue
					}
				}
			}
		case '{':
			if strings.HasPrefix(prose[i:], "{{") {
				if m := inlinePanelAnchorRe.FindStringSubmatchIndex(prose[i:]); m != nil {
					panelID := prose[i+m[2] : i+m[3]]
					matched := -1
					for k, c := range h.mentions {
						if h.usedMen[k] {
							continue
						}
						if c.Text == panelID || strings.HasPrefix(c.Text, panelID+" · ") {
							matched = k
							break
						}
					}
					if matched >= 0 {
						b.WriteString(string(markedText(h.lang, prose[last:i])))
						b.WriteString(h.renderMention(h.mentions[matched], panelID))
						h.usedMen[matched] = true
						i += m[1]
						last = i
						continue
					}
				}
			}
		case '[':
			switch {
			case strings.HasPrefix(prose[i:], "[note:"):
				bestIdx, bestLen := -1, -1
				for k, c := range h.answers {
					if h.usedAns[k] || c.Topic == "" {
						continue
					}
					if len(c.Topic) > bestLen && strings.HasPrefix(prose[i:], "[note:"+c.Topic+"]") {
						bestIdx, bestLen = k, len(c.Topic)
					}
				}
				if bestIdx >= 0 {
					b.WriteString(string(markedText(h.lang, prose[last:i])))
					b.WriteString(h.renderTopic(h.answers[bestIdx]))
					h.usedAns[bestIdx] = true
					i += len("[note:") + bestLen + 1
					last = i
					continue
				}
			case strings.HasPrefix(prose[i:], "[mention:"):
				bestIdx, bestLen := -1, -1
				for k, c := range h.mentions {
					if h.usedMen[k] {
						continue
					}
					if len(c.Topic) > bestLen && strings.HasPrefix(prose[i:], "[mention:"+c.Topic+"]") {
						bestIdx, bestLen = k, len(c.Topic)
					}
				}
				if bestIdx >= 0 {
					b.WriteString(string(markedText(h.lang, prose[last:i])))
					b.WriteString(h.renderMention(h.mentions[bestIdx], ""))
					h.usedMen[bestIdx] = true
					i += len("[mention:") + bestLen + 1
					last = i
					continue
				}
			case strings.HasPrefix(prose[i:], "[pick:"):
				bestIdx, bestLen := -1, -1
				for k, p := range h.picks {
					if h.usedPick[k] {
						continue
					}
					if len(p.Selector) > bestLen && strings.HasPrefix(prose[i:], "[pick:"+p.Selector+"]") {
						bestIdx, bestLen = k, len(p.Selector)
					}
				}
				if bestIdx >= 0 {
					b.WriteString(string(markedText(h.lang, prose[last:i])))
					b.WriteString(h.renderPick(h.picks[bestIdx]))
					h.usedPick[bestIdx] = true
					i += len("[pick:") + bestLen + 1
					last = i
					continue
				}
			case strings.HasPrefix(prose[i:], "[console:"):
				bestIdx, bestLen := -1, -1
				for k, c := range h.consoles {
					if h.usedCon[k] {
						continue
					}
					if len(c.URL) > bestLen && strings.HasPrefix(prose[i:], "[console:"+c.URL+"]") {
						bestIdx, bestLen = k, len(c.URL)
					}
				}
				if bestIdx >= 0 {
					b.WriteString(string(markedText(h.lang, prose[last:i])))
					b.WriteString(h.renderConsole(h.consoles[bestIdx]))
					h.usedCon[bestIdx] = true
					i += len("[console:") + bestLen + 1
					last = i
					continue
				}
			}
		}
		i++
	}
	b.WriteString(string(markedText(h.lang, prose[last:])))
	return template.HTML(b.String())
}

func trimSelectionSeparator(s string, prevSel, nextSel bool) string {
	if s == "" {
		return ""
	}
	hadPrevNL := false
	if prevSel {
		trimmed := inlineLeadingNewlineRe.ReplaceAllString(s, "")
		if len(trimmed) != len(s) {
			hadPrevNL = true
			s = trimmed
		}
	}
	hadNextNL := false
	if nextSel {
		trimmed := inlineTrailingNewlineRe.ReplaceAllString(s, "")
		if len(trimmed) != len(s) {
			hadNextNL = true
			s = trimmed
		}
	}
	if s == "" {
		if prevSel && nextSel {
			return " "
		}
		return ""
	}
	if hadPrevNL && s[0] != ' ' && s[0] != '\t' && s[0] != '\n' && s[0] != '\r' {
		s = " " + s
	}
	if hadNextNL && s[len(s)-1] != ' ' && s[len(s)-1] != '\t' && s[len(s)-1] != '\n' && s[len(s)-1] != '\r' {
		s += " "
	}
	return s
}

func renderSelectionInlineHTML(lang string, s *SelectionView, targets map[string]string) template.HTML {
	if s == nil || len(s.Mentions) == 0 {
		return ""
	}
	esc := template.HTMLEscapeString
	first := s.Mentions[0]
	head := "[" + first.Topic + "]"
	if first.ID != "" {
		head += " (" + first.ID + ")"
	} else if first.Entry != "" {
		head += " " + first.Entry
	}
	sub := singleLinePreview(first.Text)
	hasNote := strings.TrimSpace(s.Comment) != ""
	var titleParts []string
	for _, m := range s.Mentions {
		titleParts = append(titleParts, m.Text)
	}
	if hasNote {
		titleParts = append(titleParts, s.Comment)
	}
	var b strings.Builder
	b.WriteString(`<span class="icapsule icapsule-selection" data-icapsule="1" role="button" tabindex="0" aria-haspopup="dialog" aria-expanded="false" title="` + esc(strings.Join(titleParts, "\n")) + `">`)
	b.WriteString(`<b class="icapsule-head">` + esc(head) + `</b>`)
	if len(s.Mentions) > 1 {
		b.WriteString(`<span class="icapsule-count">+` + strconv.Itoa(len(s.Mentions)-1) + `</span>`)
	}
	if sub != "" {
		b.WriteString(`<span class="icapsule-sub">` + esc(sub) + `</span>`)
	}
	if hasNote {
		b.WriteString(`<span class="icapsule-note-mk" aria-hidden="true">&#9998;</span>`)
	}
	for _, img := range s.Images {
		if img.Num > 0 {
			b.WriteString(`<span class="icapsule-img-mk">#` + strconv.Itoa(img.Num) + `</span>`)
		}
	}
	b.WriteString(`<template class="icapsule-source"><div class="icapsule-pop-quotes">`)
	jumpLabel := wsi18n.T(lang, "entry.whiteboard.topic")
	chips := mentionChips(s.Mentions, targets)
	for i, m := range s.Mentions {
		label := "[" + m.Topic + "]"
		if m.ID != "" {
			label += " (" + m.ID + ")"
		}
		if m.Entry != "" {
			label += " " + m.Entry
		}
		b.WriteString(`<div class="icapsule-pop-item"><div class="icapsule-pop-meta"><b>` + esc(label) + `</b>`)
		if i < len(chips) && chips[i].Target != "" {
			b.WriteString(`<button type="button" class="icapsule-jump" data-wb-nav="` + esc(chips[i].Target) + `" data-wb-topic="` + esc(chips[i].TopicTarget) + `" title="` + esc(jumpLabel) + `" aria-label="` + esc(jumpLabel) + `">&#8599;</button>`)
		}
		b.WriteString(`</div><pre class="icapsule-pop-quote">` + esc(m.Text) + `</pre></div>`)
	}
	b.WriteString(`</div>`)
	if s.Comment != "" {
		b.WriteString(`<div class="icapsule-pop-comment">` + esc(s.Comment) + `</div>`)
	}
	if len(s.Images) > 0 {
		b.WriteString(`<div class="icapsule-pop-imgs">`)
		for _, img := range s.Images {
			if img.Num > 0 {
				b.WriteString(`<span class="imgtag" title="` + esc(wsi18n.T(lang, "imgtag.title", img.Num)) + `">#` + strconv.Itoa(img.Num) + `</span>`)
			} else if img.Path != "" {
				b.WriteString(`<span class="ipath">` + esc(img.Path) + `</span>`)
			}
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</template></span>`)
	return template.HTML(b.String())
}

func hydrateUserMessage(lang string, answers []wsinbox.AnswerLine, mentions []wsinbox.Mention, closeReqs []wsinbox.CloseRequest, picks []wsinbox.Pick, consoles []wsinbox.Console, targets map[string]string, text string, parts []string, content []SelectionContentPart) hydratedUserMessage {
	h := newInlineHydrator(lang, answers, mentions, picks, consoles, targets)
	out := hydratedUserMessage{CloseRequests: closeReqs}
	if len(content) > 0 {
		out.Grouped = true
		var b strings.Builder
		for i, part := range content {
			if part.Selection != nil {
				b.WriteString(string(renderSelectionInlineHTML(lang, part.Selection, targets)))
				continue
			}
			if part.Plain {
				b.WriteString(template.HTMLEscapeString(part.Text))
				continue
			}
			prevSel := i > 0 && content[i-1].Selection != nil
			nextSel := i+1 < len(content) && content[i+1].Selection != nil
			t := trimSelectionSeparator(part.Text, prevSel, nextSel)
			b.WriteString(string(h.hydrateProse(t)))
		}
		out.Body = template.HTML(b.String())
	} else if len(parts) > 0 {
		out.Parts = make([]template.HTML, len(parts))
		for i, p := range parts {
			out.Parts[i] = h.hydrateProse(p)
		}
	} else {
		out.Body = h.hydrateProse(text)
	}
	for i, c := range h.answers {
		if !h.usedAns[i] {
			out.AnswerChips = append(out.AnswerChips, c)
		}
	}
	for i, c := range h.mentions {
		if !h.usedMen[i] {
			out.MentionChips = append(out.MentionChips, c)
		}
	}
	for i, p := range h.picks {
		if !h.usedPick[i] {
			out.Picks = append(out.Picks, p)
		}
	}
	for i, c := range h.consoles {
		if !h.usedCon[i] {
			out.Consoles = append(out.Consoles, c)
		}
	}
	return out
}

// Answers are the held message's chips and topic notes as the capsules a
// delivered message's answer block shows (ConvEntry.Answers), one per chip.
func (h HeldView) Answers() []wsinbox.AnswerLine {
	var out []wsinbox.AnswerLine
	for _, c := range h.Chips {
		out = append(out, wsinbox.AnswerLine{IDs: c.ID, Value: c.Answer, Note: c.Note})
	}
	for _, t := range h.Topics {
		out = append(out, wsinbox.AnswerLine{Topic: t.Topic, Note: t.Note})
	}
	return out
}

// AttachmentView is one image a message carries. Src is the attachment
// route's URL when Path names a file in this session's attachments
// directory; the page shows any other path as text. Num is the image's
// number, which the message's "[Image #N]" markers name it by; 0 for an
// image a message carried before images were numbered.
type AttachmentView struct {
	Path, Name, Src string
	Num             int
}

// attachmentView maps image n's path to its view: a thumbnail for a file
// that exists in attachDir, the session replyID's attachments directory,
// else the path, so a removed attachment shows as text, not a broken image.
func attachmentView(attachDir, replyID string, n int, path string) AttachmentView {
	if name, ok := wsinbox.AttachmentName(attachDir, path); ok && ValidReplyID(replyID) && regularFile(path) {
		return AttachmentView{Path: path, Name: name, Src: AttachmentSrc(replyID, name), Num: n}
	}
	return AttachmentView{Path: path, Num: n}
}

// markedText renders a message text with each image marker
// (wsinbox.ImageMarker) as a tag, escaping the rest; the page shows the text
// as it is otherwise, whitespace kept.
func markedText(lang, text string) template.HTML {
	var b strings.Builder
	last := 0
	wsinbox.ImageMarkers(text, func(start, end, n int) {
		b.WriteString(template.HTMLEscapeString(text[last:start]))
		b.WriteString(`<span class="imgtag" title="` + template.HTMLEscapeString(wsi18n.T(lang, "imgtag.title", n)) + `">#` + strconv.Itoa(n) + `</span>`)
		last = end
	})
	b.WriteString(template.HTMLEscapeString(text[last:]))
	return template.HTML(b.String())
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// SelectionContentPart is an ordered display segment of existing Parts text.
// Plain keeps a failed selection envelope opaque, including image-like text.
// Selection source and memo are decoded once by ParseSelections and escaped
// by html/template, never passed through markedText or a second parser.
type SelectionContentPart struct {
	Text      string
	Plain     bool
	Selection *SelectionView
}

type SelectionView struct {
	Mentions []wsinbox.Mention
	Comment  string
	Images   []AttachmentView
}

// selectionViews associates thumbnails by the current message's numbers, not
// across messages. Missing references retain their number; the original path
// line remains ordinary text through userImages' existing fallback.
func selectionViews(parts []wsinbox.SelectionPart, images []AttachmentView) ([]SelectionContentPart, []AttachmentView) {
	grouped := false
	for _, p := range parts {
		grouped = grouped || p.Selection != nil || strings.Contains(p.Text, "<wb:selection")
	}
	if !grouped {
		return nil, images
	}
	used := map[int]bool{}
	var out []SelectionContentPart
	for _, p := range parts {
		v := SelectionContentPart{Text: p.Text, Plain: strings.Contains(p.Text, "<wb:selection")}
		if s := p.Selection; s != nil {
			v.Selection = &SelectionView{Mentions: s.Mentions, Comment: s.Comment}
			for _, n := range s.Images {
				img := AttachmentView{Num: n}
				for _, candidate := range images {
					if candidate.Num == n {
						img = candidate
						break
					}
				}
				v.Selection.Images = append(v.Selection.Images, img)
				used[n] = true
			}
		}
		out = append(out, v)
	}
	var loose []AttachmentView
	for _, img := range images {
		if !used[img.Num] {
			loose = append(loose, img)
		}
	}
	return out, loose
}

// splitSelectionContent consumes bare message content, after legacy leading
// blocks (and, when present, the caller-owned message envelope). Extract image
// definitions only outside selections so source/memo path lines stay literal.
func splitSelectionContent(text, attachDir, replyID string) (string, []SelectionContentPart, []AttachmentView) {
	parts := wsinbox.ParseSelections(text)
	var images []AttachmentView
	var rest strings.Builder
	for i, p := range parts {
		// ParseSelections deliberately merges failed envelopes with adjacent
		// plain text. Keep that entire segment opaque rather than guessing a
		// second boundary grammar or extracting an image from a failed group.
		if p.Selection == nil && !strings.Contains(p.Text, "<wb:selection") {
			var found []AttachmentView
			parts[i].Text, found = userImages(p.Text, attachDir, replyID)
			images = append(images, found...)
		}
		rest.WriteString(parts[i].Text)
	}
	content, loose := selectionViews(parts, images)
	return rest.String(), content, loose
}

// splitUserEntries unwraps each inbox item before the leading-anchored
// capsule parsers run. A batch retains one stream key; its bubbles keep
// independent timestamps and attachment numbers. Malformed envelopes stay
// wholly plain, as does report/mail text outside a valid batch's envelopes.
func splitUserEntries(entries []ConvEntry, attachDir, replyID string) {
	for i, e := range entries {
		if e.Kind != wslive.RingUser {
			continue
		}
		blocks, ok := wsinbox.ParseUserMessages(e.Text)
		if !ok {
			continue
		}
		if !hasUserEnvelope(blocks) {
			parseUserContent(&entries[i], attachDir, replyID)
			continue
		}
		var views []ConvEntry
		for _, b := range blocks {
			v := e
			v.Key, v.Text, v.EnvelopeAt = "", b.Text, b.At
			if b.At != "" {
				at, _ := time.Parse("2006-01-02T15:04-07:00", b.At)
				v.At = at.UnixMilli()
				parseUserContent(&v, attachDir, replyID)
			}
			views = append(views, v)
		}
		if len(views) == 1 {
			views[0].Key = e.Key
			entries[i] = views[0]
		} else {
			entries[i].UserBlocks = views
		}
	}
}

func hasUserEnvelope(blocks []wsinbox.UserMessage) bool {
	for _, b := range blocks {
		if b.At != "" {
			return true
		}
	}
	return false
}

// userBlockTexts is the text of each bubble splitUserEntries draws of a user
// entry's text, in order, as it stands before parseUserContent: each block of
// an enveloped batch, else the whole text as its one bubble. The entry text
// route answers it, so a bubble's index in its batch picks its text.
func userBlockTexts(text string) []string {
	blocks, ok := wsinbox.ParseUserMessages(text)
	if !ok || !hasUserEnvelope(blocks) {
		return []string{text}
	}
	out := make([]string, len(blocks))
	for i, b := range blocks {
		out[i] = b.Text
	}
	return out
}

func parseUserContent(e *ConvEntry, attachDir, replyID string) {
	text := e.Text
	if lines, rest, ok := wsinbox.ParseAnswerBlock(text); ok {
		e.Answers, text = lines, rest
	}
	e.Mentions, text = wsinbox.ParseMentions(text)
	e.CloseRequests, text = wsinbox.ParseCloseRequests(text)
	e.Picks, e.Consoles, text = wsinbox.ParseWebElements(text)
	e.Text, e.Content, e.Images = splitSelectionContent(text, attachDir, replyID)
}

// userImages takes the image lines (wsinbox.ParseImageLine: the numbered
// form and the older unnumbered one) that name this session's existing
// attachments out of a delivered user message, returning the text left and
// their views. A line naming any other path, or a removed attachment, stays
// in the text.
func userImages(text, attachDir, replyID string) (string, []AttachmentView) {
	if !strings.Contains(text, "[image: ") && !strings.Contains(text, "Image #") {
		return text, nil
	}
	var kept []string
	var images []AttachmentView
	for _, line := range strings.Split(text, "\n") {
		if n, path, ok := wsinbox.ParseImageLine(strings.TrimSpace(line)); ok {
			if v := attachmentView(attachDir, replyID, n, path); v.Src != "" {
				images = append(images, v)
				continue
			}
		}
		// Keep one blank line where a removed block left two.
		if strings.TrimSpace(line) == "" && len(kept) > 0 && strings.TrimSpace(kept[len(kept)-1]) == "" {
			continue
		}
		kept = append(kept, line)
	}
	if len(images) == 0 {
		return text, nil
	}
	return strings.Trim(strings.Join(kept, "\n"), "\n"), images
}

// SentMark is the latest answer sent for one board item, from the inbox:
// Held while the message holding it is undrained.
type SentMark struct {
	ID, Answer, Note string
	Held             bool
}

// heldViews lists the held entries in the order given, the inbox's queue
// order (wsinbox.Held: steered, then the rest); attachDir and replyID map
// their images as attachmentView does.
func heldViews(held []wsinbox.Record, attachDir, replyID string) []HeldView {
	out := make([]HeldView, 0, len(held))
	for _, r := range held {
		v := HeldView{ID: r.ID(), Steer: r.Entry.Steer, At: r.Entry.SentAt, Parts: r.Entry.Parts, Chips: r.Entry.Chips, Topics: r.Entry.Topics, Mentions: r.Entry.Mentions, CloseRequests: r.Entry.CloseRequests, Picks: r.Entry.Picks, Consoles: r.Entry.Consoles, Command: r.Entry.Command}
		nums := r.Entry.Nums()
		for i, path := range r.Entry.Images {
			v.Images = append(v.Images, attachmentView(attachDir, replyID, nums[i], path))
		}
		v.Content, v.Images = selectionViews(wsinbox.ParseSelections(strings.Join(v.Parts, "\n\n")), v.Images)
		out = append(out, v)
	}
	return out
}

// newestTurn is the conversation's newest finished turn: the newest RingTurn
// record of ring (oldest first); false when ring holds none.
func newestTurn(ring []wslive.RingRecord) (wslive.RingRecord, bool) {
	for i := len(ring) - 1; i >= 0; i-- {
		if ring[i].Kind == wslive.RingTurn {
			return ring[i], true
		}
	}
	return wslive.RingRecord{}, false
}

// turnStart is a finished turn's start, epoch milliseconds: its record's
// time less its duration. False when the turn carries no duration (its
// turn_end came after the state lost the turn's start, as across a daemon
// restart): its start would read as its end, and a chip sent during the
// turn would count as sent before it.
//
// The start is approximate. At is the ring file's write stamp, taken after
// the adapter's turn_end reached ws-mcp and the ring flushed, while
// DurationMs comes from the event clocks, so the start runs late by that
// latency. The answer check's held-message gate and its coalescing delay
// absorb it.
func turnStart(turn wslive.RingRecord) (int64, bool) {
	if turn.DurationMs <= 0 {
		return 0, false
	}
	return turn.At.UnixMilli() - turn.DurationMs, true
}

// settledTurnStart is the start of the conversation's newest finished turn
// (newestTurn, turnStart); 0 when there is none or it has no usable start.
// A chip sent before it was in the inbox when that turn began, and both
// adapters drain the inbox as a turn starts, so the turn has handled it.
func settledTurnStart(ring []wslive.RingRecord) int64 {
	turn, ok := newestTurn(ring)
	if !ok {
		return 0
	}
	start, _ := turnStart(turn)
	return start
}

// sentMarks derives each board item's sent mark from the inbox: the newest
// chip for the item across the drained records and then the held entries, so
// a held answer shows over an earlier delivered one. The drained records are
// on disk, so a mark survives a page reload while the newest DrainedCap
// records hold it. reasked maps a board item's ID to its latest re-ask time
// (epoch ms, PageContent.Reasked): a chip for it sent before that time
// answered an earlier ask and marks nothing, so the item waits on the user
// again. A chip without a send time (written before chips carried one)
// keeps its mark.
//
// settled is the newest settled turn's start (settledTurnStart, 0 for none):
// a drained note-only chip (no answer value) sent before it was delivered
// to a turn that has finished, so it clears its item's mark, and the item
// waits on the user again; an older value chip's mark does not come back.
// The note stays in the delivered message and the drained record. A held
// chip, and a value chip, keep their marks.
func sentMarks(drained, held []wsinbox.Record, reasked map[int]int64, settled int64) []SentMark {
	var order []string
	seen := map[string]bool{}
	marks := map[string]SentMark{}
	stale := func(c wsinbox.Chip) bool {
		if c.SentAt == 0 || len(reasked) == 0 {
			return false
		}
		id, err := strconv.Atoi(c.ID)
		if err != nil {
			return false
		}
		at, ok := reasked[id]
		return ok && c.SentAt < at
	}
	add := func(records []wsinbox.Record, isHeld bool) {
		// The newest send's chip wins, so read the chips in send order: a
		// steer moves an entry ahead of later sends in queue order (held)
		// and gives it a newer name (drained). A chip's own send time
		// counts (an entry merged before sends stopped merging holds chips
		// of several sends), its entry's when it has none; the sort is
		// stable, so a tie keeps the given order.
		type timed struct {
			c  wsinbox.Chip
			at int64
		}
		var list []timed
		for _, r := range records {
			for _, c := range r.Entry.Chips {
				at := c.SentAt
				if at == 0 {
					at = r.Entry.SentAt
				}
				list = append(list, timed{c, at})
			}
		}
		slices.SortStableFunc(list, func(a, b timed) int { return cmp.Compare(a.at, b.at) })
		for _, tc := range list {
			c := tc.c
			if stale(c) {
				continue
			}
			if !isHeld && c.Answer == "" && c.SentAt > 0 && c.SentAt < settled {
				delete(marks, c.ID)
				continue
			}
			if !seen[c.ID] {
				seen[c.ID] = true
				order = append(order, c.ID)
			}
			marks[c.ID] = SentMark{ID: c.ID, Answer: c.Answer, Note: c.Note, Held: isHeld}
		}
	}
	add(drained, false)
	add(held, true)
	out := make([]SentMark, 0, len(order))
	for _, id := range order {
		if m, ok := marks[id]; ok {
			out = append(out, m)
		}
	}
	return out
}
