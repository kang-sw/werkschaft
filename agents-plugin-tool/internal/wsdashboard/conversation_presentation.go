package wsdashboard

import (
	"encoding/hex"
	"html/template"
	"regexp"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/wsi18n"
	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// SystemMail reports a received mail the ws runtime posted itself (the hourly
// whiteboard nudge, hire and config notices): peer "system", no reply address,
// and no peer-lead body. It feeds data-mail-system so the whiteboard-only
// cluster can absorb such a row without matching localized text; a peer or
// user mail never qualifies.
func (e ConvEntry) SystemMail() bool {
	return e.Kind == wslive.RingMail && e.Direction == wslive.MailRecv && e.Peer == wsmailbox.SystemLabel && !e.PeerLinked && !e.PeerLead()
}

// Child reports require a typed wake cause. Whiteboard-origin user records
// also carry dashboard inbox input, so their origin cannot demote user turns.
func (e ConvEntry) ChildReport() bool { return e.Kind == "wake" && e.Cause == "subagent_report" }

// Wakes without an unambiguous display association keep compact diagnostics.
// Repeated/reclaimed records lack shared delivery identity and stay separate.
func (e ConvEntry) MailWake() bool { return e.Kind == "wake" && e.Cause == "mail" }

// AgentBadge, AgentPreview and AgentExpands feed the shared agent row frame
// (child reports and subagent/background notifications).
func (e ConvEntry) AgentBadge() string {
	if e.ChildReport() {
		return wsi18n.T(e.Lang, "entry.childreport")
	}
	return ""
}

func (e ConvEntry) AgentPreview() string {
	if e.Caption != "" {
		return e.Caption
	}
	line, _, _ := strings.Cut(strings.TrimSpace(e.Text), "\n")
	if line == "" && !e.ChildReport() {
		return e.Cause
	}
	return line
}

func (e ConvEntry) AgentExpands() bool {
	return e.ChildReport() || e.Text != "" || (e.Cause != "subagent_done" && e.Cause != "subagent_failed" && e.Cause != "background_exit")
}

func (e ConvEntry) BoardNudgeSummary() string {
	reasons, topics := e.boardNudge()
	if len(topics) == 0 {
		return ""
	}
	for i, reason := range reasons {
		switch reason {
		case "overdue":
			reasons[i] = wsi18n.T(e.Lang, "entry.board.nudge.overdue")
		case "unblocked":
			reasons[i] = wsi18n.T(e.Lang, "entry.board.nudge.unblocked")
		case "hourly":
			reasons[i] = wsi18n.T(e.Lang, "entry.board.nudge.hourly")
		}
	}
	preview := strings.Join(topics[:min(2, len(topics))], ", ")
	if len(topics) > 2 {
		preview += " +" + strconv.Itoa(len(topics)-2)
	}
	if len(e.BoardNudgeMails) > 1 {
		return wsi18n.T(e.Lang, "entry.board.nudge.batch", strings.Join(reasons, ", "), len(e.BoardNudgeMails), preview)
	}
	return wsi18n.T(e.Lang, "entry.board.nudge", strings.Join(reasons, ", "), preview)
}

// The note's optional harness label is stored mail text written by earlier
// versions, which labeled only these three harnesses; the pattern reads those
// persisted bytes, so it stays a fixed list rather than the registered names
// (1881 survivor; TestConversationPresentationPeerNoteIsDisplayOnly).
var peerLeadFirstLine = regexp.MustCompile(`^\[(?:ws|wsflow) note: from a peer lead session \((?:(?:claude|codex|pi), )?not a worker\), with the same authority as yours\]$`)

// PeerLead recognizes a stored note for mail preview and body formatting.
func (e ConvEntry) PeerLead() bool {
	if !e.HasBody {
		return false
	}
	line, _, _ := strings.Cut(e.Body, "\n")
	return peerLeadFirstLine.MatchString(line)
}

func (e ConvEntry) ShowPeerLeadBadge() bool {
	return !e.suppressPeerLeadBadge && e.PeerLead()
}

func (e ConvEntry) MailBody() string {
	if e.PeerLead() {
		_, body, _ := strings.Cut(e.Body, "\n")
		return body
	}
	return e.Body
}

func (e ConvEntry) MailPreview() string {
	if e.PeerLead() {
		_, rest, _ := strings.Cut(e.Body, "\n")
		line, _, _ := strings.Cut(rest, "\n")
		return line
	}
	return e.Title
}

type conversationEffectRow struct {
	Kind, Target, ID, Text string
	Parts                  []conversationBoardText
	TextHTML               template.HTML
	PreviewHTML            template.HTML
}

type conversationEffectRowGroup struct {
	IsLog bool
	Rows  []conversationEffectRow
}

type conversationEffectTopic struct {
	Name, Label, Target string
	Rows                []conversationEffectRow
	Items               []conversationEffectRow
}

// RowGroups groups contiguous log rows so templates can wrap consecutive logs
// in a single shared well while leaving Rows flat for existing callers/tests.
func (t conversationEffectTopic) RowGroups() []conversationEffectRowGroup {
	if len(t.Rows) == 0 {
		return nil
	}
	var groups []conversationEffectRowGroup
	for i := 0; i < len(t.Rows); {
		if t.Rows[i].Kind == "log" {
			j := i + 1
			for j < len(t.Rows) && t.Rows[j].Kind == "log" {
				j++
			}
			groups = append(groups, conversationEffectRowGroup{IsLog: true, Rows: t.Rows[i:j]})
			i = j
			continue
		}
		groups = append(groups, conversationEffectRowGroup{Rows: t.Rows[i : i+1]})
		i++
	}
	return groups
}

type conversationEffectCard struct {
	Topics []conversationEffectTopic
}

var conversationEntryID = regexp.MustCompile(`^e[1-9][0-9]*$`)
var conversationPanelID = regexp.MustCompile(`^p[1-9][0-9]*$`)

// WhiteboardCard uses only the committed projection, never current board text.
// Missing item fields are not reconstructed. Panels render recaps alongside a
// bounded, sanitized content preview derived from committed payload effects.
func (e ConvEntry) WhiteboardCard() conversationEffectCard {
	changes := e.WhiteboardChanges()
	card := conversationEffectCard{}
	for _, effect := range changes {
		index := -1
		for i := range card.Topics {
			if card.Topics[i].Name == effect.Topic {
				index = i
				break
			}
		}
		if index < 0 {
			target := ""
			if effect.Topic != "" {
				target = "topic-" + hex.EncodeToString([]byte(effect.Topic))
			}
			card.Topics = append(card.Topics, conversationEffectTopic{Name: effect.Topic, Label: effect.Topic, Target: target})
			index = len(card.Topics) - 1
		}
		topic := &card.Topics[index]
		// The stamped label is the committed display label; a label-change
		// effect below still overrides it with the newer value.
		if effect.Label != "" {
			topic.Label = effect.Label
		}
		switch effect.Kind {
		case "log":
			target := ""
			if conversationEntryID.MatchString(effect.EntryID) {
				target = effect.EntryID
			}
			topic.Rows = append(topic.Rows, conversationEffectRow{Kind: "log", Target: target, Text: effect.After})
		case "panel":
			// A payload and its recap describe one immutable panel revision.
			key := effect.EntryID + "/" + effect.PanelID
			row := effectRow(topic, "panel", key)
			if conversationPanelID.MatchString(effect.PanelID) {
				row.Target = effect.PanelID
			}
			switch effect.Field {
			case "recap":
				row.Text = firstEffectLine(effect.After)
			case "payload":
				row.PreviewHTML = whiteboardPanelPreview(effect.After, e.Lang)
			}
		case "item":
			row := effectRow(topic, "item", strconv.Itoa(effect.ID))
			if effect.ID > 0 && effect.ID <= 999999 {
				row.Target = "item-" + strconv.Itoa(effect.ID)
			}

		case "topic":
			if effect.Field == "label" && effect.After != "" {
				topic.Label = effect.After
			}
		}
	}
	// A root-level effect (e.g. a wb:floor "setting") outside any topic group
	// opens a nameless topic that gains no row; rendered, its bar would fall
	// back to the bare confirmed title as an empty card. Drop such topics.
	kept := card.Topics[:0]
	for _, topic := range card.Topics {
		if topic.Name == "" && topic.Label == "" && len(topic.Rows) == 0 && len(topic.Items) == 0 {
			continue
		}
		kept = append(kept, topic)
	}
	card.Topics = kept
	for i := range card.Topics {
		for j := range card.Topics[i].Rows {
			row := &card.Topics[i].Rows[j]
			row.Parts = conversationBoardTextParts(row.Text, e.BoardTargets)
			if row.Kind == "log" {
				row.TextHTML = renderConversationLogMarkdown(row.Text, e.BoardTargets)
			}
		}
	}
	return card
}

func effectRow(topic *conversationEffectTopic, kind, id string) *conversationEffectRow {
	rows := &topic.Rows
	if kind == "item" {
		rows = &topic.Items
	}
	for i := range *rows {
		if (*rows)[i].Kind == kind && (*rows)[i].ID == id {
			return &(*rows)[i]
		}
	}
	*rows = append(*rows, conversationEffectRow{Kind: kind, ID: id})
	return &(*rows)[len(*rows)-1]
}

func firstEffectLine(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}
