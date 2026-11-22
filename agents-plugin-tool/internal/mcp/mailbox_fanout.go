package mcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wswake"
)

// mailbox_fanout.go is mailbox.send's explicit multi-recipient path: an
// array "to", or any "cc" or "bcc". Each recipient gets one ordinary
// envelope through the same per-copy delivery a scalar send uses; there is
// no group, broadcast, or new store field. The public audience (To and CC,
// never BCC) travels as a generated header line inside the body, so every
// existing reader, history entry, and host adapter shows it unchanged.

// Recipient roles, in delivery and receipt order.
const (
	mailboxRoleTo  = "to"
	mailboxRoleCc  = "cc"
	mailboxRoleBcc = "bcc"
)

// mailboxRecipient is one distinct destination of a fan-out send.
type mailboxRecipient struct {
	addr    wsmailbox.Address
	address string // canonical form, as shown in the header and receipt
	role    string
}

// mailboxFanOut is a validated fan-out send.
type mailboxFanOut struct {
	to, cc     []string // public audience, canonical and deduplicated
	bcc        []string // hidden recipients not already public
	recipients []mailboxRecipient
	content    string // the supplied body, untrimmed
}

// parseMailboxFanOut returns nil for a legacy scalar send (a string "to"
// with no cc or bcc), which keeps its existing path byte for byte, and the
// validated fan-out otherwise. Any malformed address refuses the whole call
// before a copy is sent: an unparsed value must never reach the public
// header.
func parseMailboxFanOut(tool string, args map[string]any) (*mailboxFanOut, error) {
	toList, toIsArray, err := mailboxToArgument(tool, args["to"])
	if err != nil {
		return nil, err
	}
	cc, err := mailboxAddressListArgument(tool, mailboxRoleCc, args[mailboxRoleCc])
	if err != nil {
		return nil, err
	}
	bcc, err := mailboxAddressListArgument(tool, mailboxRoleBcc, args[mailboxRoleBcc])
	if err != nil {
		return nil, err
	}
	if !toIsArray && len(cc) == 0 && len(bcc) == 0 {
		return nil, nil
	}
	if len(toList) == 0 {
		return nil, fmt.Errorf("%s: to is required: give at least one recipient address", tool)
	}
	content, _ := args["content"].(string)
	if strings.TrimSpace(content) == "" {
		return nil, fmt.Errorf("%s: content is required", tool)
	}

	out := &mailboxFanOut{content: content}
	seen := map[string]bool{}
	for _, group := range []struct {
		role string
		raw  []string
	}{{mailboxRoleTo, toList}, {mailboxRoleCc, cc}, {mailboxRoleBcc, bcc}} {
		for _, raw := range group.raw {
			addr, perr := wsmailbox.ParseAddress(raw)
			if perr != nil {
				return nil, fmt.Errorf("%s: %s: %w", tool, group.role, perr)
			}
			address := mailboxAddressString(addr)
			// One copy per destination: a repeated address keeps its
			// first, most public role.
			if seen[address] {
				continue
			}
			seen[address] = true
			out.recipients = append(out.recipients, mailboxRecipient{addr: addr, address: address, role: group.role})
			switch group.role {
			case mailboxRoleTo:
				out.to = append(out.to, address)
			case mailboxRoleCc:
				out.cc = append(out.cc, address)
			default:
				out.bcc = append(out.bcc, address)
			}
		}
	}
	return out, nil
}

// mailboxToArgument reads "to" as one address string or an array of them,
// reporting whether the array shape was given. A missing or non-string
// scalar yields no address, so a legacy send keeps its own "to is required"
// error.
func mailboxToArgument(tool string, raw any) ([]string, bool, error) {
	switch v := raw.(type) {
	case string:
		return []string{v}, false, nil
	case []any, []string:
		list, err := mailboxAddressListArgument(tool, mailboxRoleTo, v)
		return list, true, err
	}
	return nil, false, nil
}

// mailboxAddressListArgument reads an optional array of address strings.
func mailboxAddressListArgument(tool, name string, raw any) ([]string, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case []string:
		return v, nil
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("%s: %s must be an array of address strings", tool, name)
			}
			out = append(out, text)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s: %s must be an array of address strings", tool, name)
}

// mailboxAudienceHeader is the generated public-audience line every copy of a
// fan-out send carries. It names To and CC only; BCC never appears.
func mailboxAudienceHeader(namespace string, to, cc []string) string {
	header := "[" + namespace + " mail to: " + strings.Join(to, ", ")
	if len(cc) > 0 {
		header += "; cc: " + strings.Join(cc, ", ")
	}
	return header + "]"
}

// composeMailboxFanOutBody places the public header in the supplied body: a
// leading peer-lead note (one the caller supplied, from either product, or
// the one a top-level lead's send stamps) stays the first line, so the
// dashboard still recognizes it; the header follows; then the supplied bytes,
// untrimmed.
func composeMailboxFanOutBody(content, header string, topLevelLead bool, harness, namespace string) string {
	if hasPeerLeadNote(content, namespace) {
		note, rest, _ := strings.Cut(content, "\n")
		return note + "\n" + header + "\n" + rest
	}
	body := header + "\n" + content
	if topLevelLead {
		body = stampPeerLeadNote(body, harness, namespace)
	}
	return body
}

// mailboxFanOutResult is one recipient's outcome in the sender's receipt.
type mailboxFanOutResult struct {
	Address    string `json:"address"`
	Role       string `json:"role"`
	Stamp      string `json:"stamp,omitempty"`
	StampValue string `json:"stamp_value,omitempty"`
	Error      string `json:"error,omitempty"`
}

// handleMailboxFanOut delivers one ordinary copy per recipient, each in its
// own store transaction, so one recipient's routing or enqueue failure
// neither rolls back nor stops the others. The receipt lists every outcome;
// when no copy went, the call is a tool error carrying the same list.
func (s *Server) handleMailboxFanOut(id json.RawMessage, args map[string]any, sessionKey string, entry sessionEntry, f *mailboxFanOut) response {
	const tool = "mailbox.send"
	namespace := s.runtimeNamespace()
	header := mailboxAudienceHeader(namespace, f.to, f.cc)
	content := composeMailboxFanOutBody(f.content, header, entry.isTopLevelLead(), s.currentHarness(), namespace)

	sender := s.mailboxSendIdentity(sessionKey, entry)
	now := mailboxNowString()
	var delivered, failed []mailboxFanOutResult
	var history []mailboxHistoryEntry
	var historyNames []string
	for _, r := range f.recipients {
		d, err := s.deliverMailboxCopy(sessionKey, entry, sender, r.addr, content, now)
		if err != nil {
			s.debugEvent("mailbox.fanout_copy_error", map[string]any{"role": r.role, "error": err.Error()})
			failed = append(failed, mailboxFanOutResult{Address: r.address, Role: r.role, Error: err.Error()})
			continue
		}
		label, value := d.stamp()
		delivered = append(delivered, mailboxFanOutResult{Address: r.address, Role: r.role, Stamp: label, StampValue: value})
		history = append(history, d.history...)
		historyNames = append(historyNames, d.historyNames...)
	}

	if len(delivered) == 0 {
		var b strings.Builder
		fmt.Fprintf(&b, "%s: delivered to none of %d recipients\n", tool, len(f.recipients))
		writeMailboxFanOutResults(&b, nil, failed)
		return toolErrorTextResponse(id, b.String())
	}
	s.markMailboxReplyOpened(sessionKey)
	// One wake and one nudge cover every enqueued copy.
	wswake.Mailbox.Wake()
	if s.live.host == nil {
		s.dashboardMailNudge(sessionKey, entry.root)
	}
	s.recordLiveMail(sessionKey, history, historyNames)

	replyID := wsmailbox.ReplyIDPrefix + s.mailboxReplyID(sessionKey)
	if wantsJSON(args) {
		return toolJSONResponse(id, map[string]any{
			"to": f.to, "cc": nonNilStrings(f.cc), "bcc": nonNilStrings(f.bcc),
			"sent_at": now, "your_reply_id": replyID,
			"delivered": delivered, "failed": nonNilFanOutResults(failed),
		}, nil)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "sent to %d of %d recipients\n", len(delivered), len(f.recipients))
	writeMailboxFanOutResults(&b, delivered, failed)
	fmt.Fprintf(&b, "your_reply_id: %s\n", replyID)
	return toolTextResponse(id, b.String(), nil)
}

func writeMailboxFanOutResults(b *strings.Builder, delivered, failed []mailboxFanOutResult) {
	for _, r := range delivered {
		fmt.Fprintf(b, "delivered %s [%s] %s: %s\n", r.Address, r.Role, r.Stamp, r.StampValue)
	}
	for _, r := range failed {
		fmt.Fprintf(b, "failed %s [%s]: %s\n", r.Address, r.Role, r.Error)
	}
}

func nonNilFanOutResults(values []mailboxFanOutResult) []mailboxFanOutResult {
	if values == nil {
		return []mailboxFanOutResult{}
	}
	return values
}
