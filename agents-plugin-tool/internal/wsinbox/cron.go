// The scheduled-prompt envelope: an inbox entry a daemon-held schedule sent,
// marked by Entry.Cron, rendered as <ws:cron> beside the <ws:user> family.
package wsinbox

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
)

// ErrNotSteerable reports a steer of an entry that never steers: a scheduled
// prompt waits for the turn end like any follow-up, so a dashboard steer
// cannot turn it into a mid-turn injection.
var ErrNotSteerable = errors.New("a scheduled prompt cannot be steered")

// Cron marks an entry a schedule sent: the schedule's id and name and the
// matched cron minute, RFC 3339 in the schedule's offset. The marker is the
// whole difference from a plain message; the prompt is the entry's one part.
type Cron struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	At   string `json:"at"`
}

var cronBodyDelimiter = regexp.MustCompile(`</ws:cron|&(?:amp;)*lt;/ws:cron`)

// escapeCronBody mirrors escapeUserBody for the cron closing delimiter, so a
// prompt cannot close or forge the envelope. A prompt that itself quotes a
// user envelope stays inert too: the parser consumes a cron envelope whole.
func escapeCronBody(text string) string {
	return cronBodyDelimiter.ReplaceAllStringFunc(text, func(s string) string {
		if s[0] == '<' {
			return "&lt;/ws:cron"
		}
		return "&amp;" + s[1:]
	})
}

func unescapeCronBody(text string) string {
	return cronBodyDelimiter.ReplaceAllStringFunc(text, func(s string) string {
		if strings.HasPrefix(s, "&lt;") {
			return "</ws:cron"
		}
		if strings.HasPrefix(s, "&amp;") {
			return "&" + s[len("&amp;"):]
		}
		return s
	})
}

var cronAttrEscaper = strings.NewReplacer("&", "&amp;", `"`, "&quot;", "<", "&lt;", ">", "&gt;")

// cronText is the entry's <ws:cron> envelope; the body is the entry's text.
func (e Entry) cronText() string {
	body := e.bodyText()
	if body == "" {
		return ""
	}
	c := e.Cron
	return `<ws:cron id="` + cronAttrEscaper.Replace(c.ID) + `" name="` + cronAttrEscaper.Replace(c.Name) +
		`" at="` + cronAttrEscaper.Replace(c.At) + `">` + "\n" + escapeCronBody(body) + "\n</ws:cron>"
}

func cleanCronMark(c Cron) (Cron, error) {
	c.ID, c.Name, c.At = strings.TrimSpace(c.ID), strings.TrimSpace(c.Name), strings.TrimSpace(c.At)
	for label, v := range map[string]string{"id": c.ID, "name": c.Name, "at": c.At} {
		if v == "" || strings.IndexFunc(v, unicode.IsControl) >= 0 {
			return Cron{}, fmt.Errorf("%w: scheduled prompt %s %q", ErrInvalid, label, v)
		}
	}
	if _, err := time.Parse(time.RFC3339, c.At); err != nil {
		return Cron{}, fmt.Errorf("%w: scheduled prompt time %q", ErrInvalid, c.At)
	}
	return c, nil
}

// normalizeCron checks a scheduled prompt: its marker, its text alone (no
// answers, mentions, images or the like), and never steered.
func normalizeCron(e Entry) (Entry, error) {
	mark, err := cleanCronMark(*e.Cron)
	if err != nil {
		return Entry{}, err
	}
	if e.Steer || e.Interrupt || e.Command != nil || len(e.Chips) > 0 || len(e.Topics) > 0 || len(e.Mentions) > 0 ||
		len(e.CloseRequests) > 0 || len(e.Picks) > 0 || len(e.Consoles) > 0 || len(e.Images) > 0 {
		return Entry{}, fmt.Errorf("%w: a scheduled prompt carries text alone and is never steered", ErrInvalid)
	}
	out := Entry{SchemaVersion: SchemaVersion, SentAt: e.SentAt, UTCOffsetMinutes: e.UTCOffsetMinutes, Cron: &mark}
	size := 0
	for _, p := range e.Parts {
		if p = strings.TrimSpace(p); p != "" {
			size += len(p)
			out.Parts = append(out.Parts, p)
		}
	}
	if len(out.Parts) == 0 {
		return Entry{}, ErrEmpty
	}
	if size > maxTextBytes {
		return Entry{}, fmt.Errorf("%w: text is longer than %d bytes", ErrInvalid, maxTextBytes)
	}
	return out, nil
}

// cronObservers are told when a held scheduled prompt leaves its inbox.
var cronObservers struct {
	sync.Mutex
	next int
	fns  map[int]func(Cron)
}

// ObserveCronRemoval registers fn to be called, with the entry's marker, each
// time a held scheduled prompt leaves an inbox by a drain, a cancel or an
// interrupt: every path that takes it out of the held set. fn runs on the
// removing caller's goroutine, after the removal, and must not block. The
// returned function unregisters it.
func ObserveCronRemoval(fn func(Cron)) (stop func()) {
	cronObservers.Lock()
	defer cronObservers.Unlock()
	if cronObservers.fns == nil {
		cronObservers.fns = map[int]func(Cron){}
	}
	id := cronObservers.next
	cronObservers.next++
	cronObservers.fns[id] = fn
	return func() {
		cronObservers.Lock()
		delete(cronObservers.fns, id)
		cronObservers.Unlock()
	}
}

func notifyCronRemoved(entries ...Entry) {
	var marks []Cron
	for _, e := range entries {
		if e.Cron != nil {
			marks = append(marks, *e.Cron)
		}
	}
	if len(marks) == 0 {
		return
	}
	cronObservers.Lock()
	fns := make([]func(Cron), 0, len(cronObservers.fns))
	for _, fn := range cronObservers.fns {
		fns = append(fns, fn)
	}
	cronObservers.Unlock()
	for _, m := range marks {
		for _, fn := range fns {
			fn(m)
		}
	}
}

func recordEntries(records []Record) []Entry {
	out := make([]Entry, len(records))
	for i, r := range records {
		out[i] = r.Entry
	}
	return out
}

var cronOpen = regexp.MustCompile(`^<ws:cron id="[^"\n]*" name="[^"\n]*" at="[^"\n]*">`)
