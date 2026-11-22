// Package wscron holds scheduled prompts: a Linux (Vixie) cron expression
// parser with next-fire computation over a fixed UTC offset, and the
// daemon-held scheduler that delivers a prompt to a session each time its
// expression matches.
//
// This file is the pure part: Parse and Expr.Next never read a clock.
package wscron

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Horizon bounds the next-fire search. A leap-day expression is at most eight
// years from its previous match, so ten years finds every expression that can
// fire at all.
const Horizon = 10 * 365 * 24 * time.Hour

// ErrNeverMatches reports an expression with no matching minute within the
// search horizon, such as day 30 of February.
var ErrNeverMatches = errors.New("the expression never matches a real date")

// Expr is a parsed 5-field cron expression. The zero value is not usable;
// build one with Parse.
type Expr struct {
	text    string
	minute  uint64 // bits 0-59
	hour    uint64 // bits 0-23
	dom     uint64 // bits 1-31
	month   uint64 // bits 1-12
	dow     uint64 // bits 0-6 (7 folded to 0)
	domStar bool   // field began with "*" (Vixie: unrestricted)
	dowStar bool
}

// String is the caller's expression, trimmed.
func (e *Expr) String() string { return e.text }

type fieldSpec struct {
	name     string
	min, max int
	names    []string // lowercase three-letter names, index i is min+i
	nameBase int
}

var (
	minuteSpec = fieldSpec{name: "minute", min: 0, max: 59}
	hourSpec   = fieldSpec{name: "hour", min: 0, max: 23}
	domSpec    = fieldSpec{name: "day-of-month", min: 1, max: 31}
	monthSpec  = fieldSpec{name: "month", min: 1, max: 12, nameBase: 1,
		names: []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}}
	// Day-of-week parses over 0-7 and folds 7 onto Sunday.
	dowSpec = fieldSpec{name: "day-of-week", min: 0, max: 7, nameBase: 0,
		names: []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}}
)

var macros = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// Parse parses a standard 5-field cron expression (minute, hour,
// day-of-month, month, day-of-week) or one of the macros @yearly,
// @annually, @monthly, @weekly, @daily, @midnight, @hourly. It does not check
// that the expression can fire; Next reports that.
func Parse(expr string) (*Expr, error) {
	text := strings.TrimSpace(expr)
	if text == "" {
		return nil, errors.New("expr is empty")
	}
	body := text
	if strings.HasPrefix(text, "@") {
		macro, ok := macros[strings.ToLower(text)]
		if !ok {
			if strings.EqualFold(text, "@reboot") {
				return nil, errors.New(`expr "@reboot" is not supported: a schedule fires on the clock, not at start`)
			}
			return nil, fmt.Errorf("expr %q is not a known macro (use @yearly, @annually, @monthly, @weekly, @daily, @midnight or @hourly)", text)
		}
		body = macro
	}
	fields := strings.FieldsFunc(body, func(r rune) bool { return r == ' ' || r == '\t' })
	if len(fields) != 5 {
		return nil, fmt.Errorf("expr has %d fields; use 5 (minute hour day-of-month month day-of-week), without a seconds field", len(fields))
	}
	e := &Expr{text: text}
	var err error
	if e.minute, _, err = parseField(fields[0], minuteSpec); err != nil {
		return nil, err
	}
	if e.hour, _, err = parseField(fields[1], hourSpec); err != nil {
		return nil, err
	}
	if e.dom, e.domStar, err = parseField(fields[2], domSpec); err != nil {
		return nil, err
	}
	if e.month, _, err = parseField(fields[3], monthSpec); err != nil {
		return nil, err
	}
	if e.dow, e.dowStar, err = parseField(fields[4], dowSpec); err != nil {
		return nil, err
	}
	// 7 is Sunday too.
	if e.dow&(1<<7) != 0 {
		e.dow = e.dow&^(1<<7) | 1
	}
	return e, nil
}

// parseField parses one field into a bit set and reports whether the field
// began with "*", which Vixie cron treats as unrestricted for the
// day-of-month / day-of-week rule.
func parseField(field string, spec fieldSpec) (bits uint64, star bool, err error) {
	if field == "" {
		return 0, false, fmt.Errorf("%s field is empty", spec.name)
	}
	star = field[0] == '*'
	for _, item := range strings.Split(field, ",") {
		if item == "" {
			return 0, false, fmt.Errorf("%s field %q has an empty list item", spec.name, field)
		}
		b, err := parseItem(item, spec)
		if err != nil {
			return 0, false, err
		}
		bits |= b
	}
	return bits, star, nil
}

func parseItem(item string, spec fieldSpec) (uint64, error) {
	rangePart, stepPart, hasStep := strings.Cut(item, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepPart)
		if err != nil || stepPart == "" || stepPart[0] == '+' || stepPart[0] == '-' || n < 1 {
			return 0, fmt.Errorf("%s field: step %q in %q must be a positive number", spec.name, stepPart, item)
		}
		if n > spec.max-spec.min+1 {
			return 0, fmt.Errorf("%s field: step %d in %q is larger than the field's %d values", spec.name, n, item, spec.max-spec.min+1)
		}
		step = n
	}
	var lo, hi int
	switch {
	case rangePart == "*":
		lo, hi = spec.min, spec.max
	case strings.Contains(rangePart, "-"):
		a, b, _ := strings.Cut(rangePart, "-")
		var err error
		if lo, err = parseValue(a, spec); err != nil {
			return 0, err
		}
		if hi, err = parseValue(b, spec); err != nil {
			return 0, err
		}
		if lo > hi {
			return 0, fmt.Errorf("%s field: range %q is reversed", spec.name, rangePart)
		}
	default:
		v, err := parseValue(rangePart, spec)
		if err != nil {
			return 0, err
		}
		if hasStep {
			return 0, fmt.Errorf("%s field: step %q needs a range or *, not the single value %q", spec.name, item, rangePart)
		}
		lo, hi = v, v
	}
	var bits uint64
	for v := lo; ; v += step {
		bits |= 1 << uint(v)
		// Stop before the increment can pass hi, so no step wraps the counter.
		if step > hi-v {
			break
		}
	}
	return bits, nil
}

func parseValue(tok string, spec fieldSpec) (int, error) {
	if tok == "" {
		return 0, fmt.Errorf("%s field: missing value", spec.name)
	}
	if tok[0] >= '0' && tok[0] <= '9' {
		n, err := strconv.Atoi(tok)
		if err != nil {
			return 0, fmt.Errorf("%s field: %q is not a number", spec.name, tok)
		}
		if n < spec.min || n > spec.max {
			return 0, fmt.Errorf("%s field: %d is outside %d-%d", spec.name, n, spec.min, spec.max)
		}
		return n, nil
	}
	if spec.names != nil {
		lower := strings.ToLower(tok)
		for i, name := range spec.names {
			if lower == name {
				return spec.nameBase + i, nil
			}
		}
	}
	return 0, fmt.Errorf("%s field: unknown token %q", spec.name, tok)
}

// dayMatches applies Vixie's rule: when both day fields are restricted a day
// matches if either does; a field beginning with "*" counts as unrestricted,
// so the other alone decides (both bit sets must hold).
func (e *Expr) dayMatches(t time.Time) bool {
	domOK := e.dom&(1<<uint(t.Day())) != 0
	dowOK := e.dow&(1<<uint(t.Weekday())) != 0
	if e.domStar || e.dowStar {
		return domOK && dowOK
	}
	return domOK || dowOK
}

// Matches reports whether the minute t falls in, evaluated in t's own
// location; seconds are ignored.
func (e *Expr) Matches(t time.Time) bool {
	return e.minute&(1<<uint(t.Minute())) != 0 &&
		e.hour&(1<<uint(t.Hour())) != 0 &&
		e.month&(1<<uint(t.Month())) != 0 &&
		e.dayMatches(t)
}

// Next returns the first matching minute strictly after after, with its
// fields evaluated in loc (a fixed offset, so every wall-clock minute exists
// exactly once) and the result in loc. It walks days, not minutes, and
// reports false when nothing matches within Horizon.
func (e *Expr) Next(after time.Time, loc *time.Location) (time.Time, bool) {
	if loc == nil {
		loc = time.UTC
	}
	t := after.In(loc).Truncate(time.Minute).Add(time.Minute)
	// Truncate works on the absolute instant; a fixed offset that is not a
	// whole minute does not exist here (offsets are validated to minutes),
	// so the instant boundary is the wall-clock minute boundary.
	limit := after.Add(Horizon)
	y, mo, d := t.Date()
	first := true
	for {
		day := time.Date(y, mo, d, 0, 0, 0, 0, loc)
		if day.After(limit) {
			return time.Time{}, false
		}
		if e.month&(1<<uint(day.Month())) == 0 {
			// Skip to the first of the next month.
			y, mo, d = day.AddDate(0, 1, 1-day.Day()).Date()
			first = false
			continue
		}
		if e.dayMatches(day) {
			minH, minM := 0, 0
			if first {
				minH, minM = t.Hour(), t.Minute()
			}
			if h, m, ok := e.firstTime(minH, minM); ok {
				return time.Date(y, mo, d, h, m, 0, 0, loc), true
			}
		}
		first = false
		y, mo, d = day.AddDate(0, 0, 1).Date()
	}
}

// firstTime finds the earliest matching hour:minute at or after minH:minM
// within one day.
func (e *Expr) firstTime(minH, minM int) (int, int, bool) {
	for h := minH; h < 24; h++ {
		if e.hour&(1<<uint(h)) == 0 {
			continue
		}
		from := 0
		if h == minH {
			from = minM
		}
		for m := from; m < 60; m++ {
			if e.minute&(1<<uint(m)) != 0 {
				return h, m, true
			}
		}
	}
	return 0, 0, false
}

// ParseTZ parses a schedule's tz argument: UTC or Z (case-insensitive), or a
// fixed offset +HH:MM / -HH:MM from -12:00 to +14:00. It returns the
// normalized spelling (UTC for Z, z and utc; an offset keeps its sign and
// two-digit fields, with -00:00 written +00:00) and the location. An empty
// string is UTC.
func ParseTZ(s string) (string, *time.Location, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.EqualFold(s, "utc") || strings.EqualFold(s, "z") {
		return "UTC", time.UTC, nil
	}
	bad := fmt.Errorf("tz %q is not accepted: use UTC, Z, or a fixed offset +HH:MM or -HH:MM from -12:00 to +14:00 (zone names are not supported)", s)
	if len(s) != 6 || (s[0] != '+' && s[0] != '-') || s[3] != ':' {
		return "", nil, bad
	}
	for _, i := range []int{1, 2, 4, 5} {
		if s[i] < '0' || s[i] > '9' {
			return "", nil, bad
		}
	}
	h, _ := strconv.Atoi(s[1:3])
	m, _ := strconv.Atoi(s[4:6])
	if m > 59 {
		return "", nil, bad
	}
	minutes := h*60 + m
	if s[0] == '-' {
		minutes = -minutes
	}
	if minutes < -12*60 || minutes > 14*60 {
		return "", nil, bad
	}
	name := s
	if minutes == 0 {
		name = "+00:00"
	}
	return name, time.FixedZone(name, minutes*60), nil
}
