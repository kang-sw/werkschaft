// exec_notice.go keeps an exec job's completion notice apart from the user's
// own text on its way to the model and to the live ring: the notice is a
// background_exit wake row, never a user bubble.
package wsagent

import (
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// livePart is one delivered inbox text, classified at the entry that
// produced it: Exec is the trusted EventID's verdict (Entry.ExecNotice),
// never the text's own claim.
type livePart struct {
	Text string
	Exec bool
}

// recordParts is the nonempty delivered text of each record, in order.
func recordParts(recs []wsinbox.Record) []livePart {
	var parts []livePart
	for _, rec := range recs {
		if text := strings.TrimSpace(rec.Entry.Text()); text != "" {
			_, exec := rec.Entry.ExecNotice()
			parts = append(parts, livePart{Text: text, Exec: exec})
		}
	}
	return parts
}

func partsText(parts []livePart) string {
	texts := make([]string, len(parts))
	for i, p := range parts {
		texts[i] = p.Text
	}
	return strings.Join(texts, "\n\n")
}

// execWakeEvent is the ring's wake for an exec notice's envelope text: the
// job id as its source and the unwrapped payload as its text. False when text
// is not exactly one <ws:exec> envelope.
func execWakeEvent(text string) (wslive.Event, bool) {
	block, ok := wsinbox.ParseExecBlock(text)
	if !ok {
		return wslive.Event{}, false
	}
	return *wakeEvent("background_exit", block.ID, "", block.Body).Live, true
}

// liveEvents is the ring's account of parts, in delivery order: each run of
// the user's own text one user entry, each exec notice one background_exit
// wake. A part marked Exec whose envelope does not parse stays user text.
func liveEvents(parts []livePart) []wslive.Event {
	var events []wslive.Event
	var run []string
	flush := func() {
		if len(run) > 0 {
			events = append(events, wslive.Event{Kind: wslive.KindUser, Origin: wslive.OriginWhiteboard, Text: strings.Join(run, "\n\n")})
			run = nil
		}
	}
	for _, p := range parts {
		if p.Exec {
			if ev, ok := execWakeEvent(p.Text); ok {
				flush()
				events = append(events, ev)
				continue
			}
		}
		run = append(run, p.Text)
	}
	flush()
	return events
}

// hasUserText reports whether events carry text of the user's own.
func hasUserText(events []wslive.Event) bool {
	return slices.ContainsFunc(events, func(ev wslive.Event) bool { return ev.Kind == wslive.KindUser })
}

func (c *conv) applyLive(events []wslive.Event) {
	for _, ev := range events {
		c.apply(ev)
	}
}

// userEvents is the one user entry of text, the default account of a steer
// that carries no exec notice.
func userEvents(text string) []wslive.Event {
	return []wslive.Event{{Kind: wslive.KindUser, Origin: wslive.OriginWhiteboard, Text: text}}
}

// splitReportTexts splits a joined steer text into the parts it was joined
// from (exec envelopes apart from the text around them), for a record that
// keeps its texts one per element (Record.Reports): the classification of a
// part must survive the join. The persisted element type stays a string, so
// an older binary reads the same record.
func splitReportTexts(text string) []string {
	segments := wsinbox.SplitExecNotices(text)
	if len(segments) == 1 {
		return []string{text}
	}
	out := make([]string, len(segments))
	for i, s := range segments {
		out[i] = s.Text
	}
	return out
}

// reportParts classifies each Record.Reports element: one that is exactly an
// exec envelope is a notice (a requeued steer keeps its envelope), the rest
// are text. An element whose text is such an envelope is read as one whoever
// wrote it: a report's own text is not authenticated, only its display is
// affected.
func reportParts(reports []string) []livePart {
	parts := make([]livePart, len(reports))
	for i, text := range reports {
		_, exec := wsinbox.ParseExecBlock(text)
		parts[i] = livePart{Text: text, Exec: exec}
	}
	return parts
}
