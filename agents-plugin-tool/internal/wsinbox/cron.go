// The scheduled-prompt envelope: an inbox entry a daemon-held schedule sent,
// marked by Entry.Cron and stored under its own file name, rendered as
// <ws:cron> beside the <ws:user> family.
package wsinbox

import (
	"errors"
	"fmt"
	"path/filepath"
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

// A scheduled prompt is stored under a name an earlier daemon's inbox reader
// never lists: its stamp and hex, then ".cron.json" in place of ".json". That
// reader (and its drain, cancel and dashboard steer, all of which go through
// the same name pattern) skips the file without an error, so a downgraded
// daemon neither shows nor delivers a scheduled prompt; the current daemon
// reads it again once it reopens the inbox. The name is the only provenance:
// the Cron field is honored in a file with that name and dropped from any
// other, so no message text and no JSON a plain send wrote can pass for a
// scheduler's prompt.

// cronNameSuffix replaces ".json" in a scheduled prompt's entry name.
const cronNameSuffix = ".cron.json"

// cronStored matches the base name of a scheduled prompt's entry file: held,
// drained, or claimed by a drain, steer, cancel or interrupt (a hidden file
// that keeps the name and appends the operation).
var cronStored = regexp.MustCompile(`^\.?[0-9]{8}T[0-9]{6}\.[0-9]{9}Z-[0-9a-f]{8}\.cron\.json(?:\.[a-z]+-[0-9a-f]+)?$`)

// isCronFile reports whether path names a scheduler-written entry file.
func isCronFile(path string) bool { return cronStored.MatchString(filepath.Base(path)) }

// entryFileName is the name a new entry gets under stamp and suffix.
func entryFileName(stamp, suffix string, e Entry) string {
	if e.Cron != nil {
		return stamp + "-" + suffix + cronNameSuffix
	}
	return stamp + "-" + suffix + ".json"
}

// provenCron settles an entry read from path: only a scheduler-named file
// keeps its marker (and is never steered); a cron-named file without one does
// not read; any other file loses a marker it carries.
func provenCron(path string, e Entry) (Entry, error) {
	if !isCronFile(path) {
		e.Cron = nil
		return e, nil
	}
	if e.Cron == nil {
		return Entry{}, fmt.Errorf("parse inbox entry: %s carries no schedule marker", filepath.Base(path))
	}
	e.Steer = false
	return e, nil
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
