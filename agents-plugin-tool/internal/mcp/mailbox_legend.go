package mcp

import (
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsmailbox"
)

// mailbox_legend.go renders the sender legend: one generated line per
// delivered batch, "[senders: <handle> = <label>; ...]", naming the display
// label of each distinct sender that has one. Labels are resolved when the
// batch is delivered, from the daemon's in-memory views (MailLabelFunc), and
// never stored: no envelope, held text, or message head or body carries one,
// so a renamed session shows its current label the next time its mail is
// delivered.

// mailLegendPrefix opens the legend line. The dashboard mirrors it
// (wsdashboard's trimMailLegend).
const mailLegendPrefix = "[senders: "

// mailLegendSender is one message's sender as the legend names it: the
// handle its head shows, and the reply-id its label is resolved from.
type mailLegendSender struct {
	handle  string
	replyID string
}

// mailLegendLine is the legend for senders, in first-appearance order with
// one entry per distinct handle that has a label; "" when none has one.
func (s *Server) mailLegendLine(senders []mailLegendSender) string {
	seen := map[string]bool{}
	var parts []string
	for _, sender := range senders {
		if sender.handle == "" || seen[sender.handle] {
			continue
		}
		seen[sender.handle] = true
		if label := s.mailLabel(sender.replyID); label != "" {
			parts = append(parts, sender.handle+" = "+label)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return mailLegendPrefix + strings.Join(parts, "; ") + "]"
}

// envelopeLegendSenders are the legend senders of envelopes as mailbox.recv
// shows them (mailboxSender), each with its sender's reply-id
// (labelSenderReplyID).
func (s *Server) envelopeLegendSenders(envelopes []wsmailbox.Envelope, root string) []mailLegendSender {
	out := make([]mailLegendSender, 0, len(envelopes))
	for _, m := range envelopes {
		out = append(out, mailLegendSender{handle: mailboxSender(m), replyID: s.labelSenderReplyID(m, root)})
	}
	return out
}

// labelEnvelopes is envelopes as the structured mailbox.recv response shows
// them: an addressed sender's label, when it has one, fills the envelope's
// label field. The stored envelopes are not changed, and a senderless one
// keeps its stored label as its handle.
func (s *Server) labelEnvelopes(envelopes []wsmailbox.Envelope, root string) []wsmailbox.Envelope {
	if envelopes == nil {
		return nil
	}
	out := slices.Clone(envelopes)
	for i, m := range out {
		if m.From == "" && m.ReplyTo == "" {
			continue
		}
		if label := s.mailLabel(s.labelSenderReplyID(m, root)); label != "" {
			out[i].Label = label
		}
	}
	return out
}

// Legend places the sender legend on one submission of claimed mail, a
// steer or a turn (wsagent.Options.MailLegend). texts are the texts
// submitted together, in order: mail texts as Drain returned them, and in a
// turn the reports it carries, among them a steered mail no run took;
// senders are the reply-ids Drain gave them, where known: a held text
// (Record.Mail) comes without one, and its sender is read from its head as
// mailSenderReplyID maps an envelope. The legend names only the senders of
// the mail texts, and goes on the first of them; a text of another shape
// (a child's report) is left as it is. When that text opens with the batch
// head formatMailBatch writes, the legend is that head's second line;
// otherwise it is a first line followed by a blank line. texts comes back
// unchanged when no sender has a label.
func (m *LeadMailbox) Legend(root string, texts, senders []string) []string {
	if len(texts) == 0 {
		return texts
	}
	list := make([]mailLegendSender, 0, len(texts))
	first := -1
	for i, text := range texts {
		handle, _, _, ok := splitMailText(text)
		if !ok {
			continue
		}
		if first < 0 {
			first = i
		}
		replyID := ""
		if i < len(senders) {
			replyID = senders[i]
		}
		if replyID == "" {
			replyID = m.server.labelHandleReplyID(handle, root)
		}
		list = append(list, mailLegendSender{handle: handle, replyID: replyID})
	}
	legend := m.server.mailLegendLine(list)
	if legend == "" {
		return texts
	}
	out := slices.Clone(texts)
	out[first] = placeMailLegend(out[first], legend)
	return out
}

// placeMailLegend puts legend on text: as the second line of its batch
// head when text opens with one, else as a first line and a blank line.
func placeMailLegend(text, legend string) string {
	if head, rest, ok := strings.Cut(text, "\n\n"); ok && isMailBatchHead(head) {
		return head + "\n" + legend + "\n\n" + rest
	}
	return legend + "\n\n" + text
}

// trimMailLegend is text without a legend placed as its own first line,
// when one is: a one-line "[senders: ...]" paragraph followed by a "mail
// from" head. A message body never starts a text, so a body that opens with
// such a line is never taken for one.
func trimMailLegend(text string) string {
	if !strings.HasPrefix(text, mailLegendPrefix) {
		return text
	}
	line, rest, ok := strings.Cut(text, "\n\n")
	if !ok || strings.Contains(line, "\n") || !strings.HasSuffix(line, "]") || !strings.HasPrefix(rest, "mail from ") {
		return text
	}
	return rest
}
