package wscron

import (
	"math/rand"
	"strings"
	"testing"
	"time"
)

func mustTZ(t *testing.T, s string) *time.Location {
	t.Helper()
	_, loc, err := ParseTZ(s)
	if err != nil {
		t.Fatalf("ParseTZ(%q): %v", s, err)
	}
	return loc
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestParseRefusals(t *testing.T) {
	cases := []struct {
		expr string
		want string // substring naming the field or token
	}{
		{"", "empty"},
		{"   ", "empty"},
		{"@reboot", "@reboot"},
		{"@sometimes", "macro"},
		{"* * * * * *", "6 fields"},
		{"0 * * * * * *", "7 fields"},
		{"* * * *", "4 fields"},
		{"60 * * * *", "minute field: 60"},
		{"* 24 * * *", "hour field: 24"},
		{"* * 0 * *", "day-of-month field: 0"},
		{"* * 32 * *", "day-of-month field: 32"},
		{"* * * 13 *", "month field: 13"},
		{"* * * 0 *", "month field: 0"},
		{"* * * * 8", "day-of-week field: 8"},
		{"*/0 * * * *", "minute field: step"},
		{"5-1 * * * *", "reversed"},
		{"1,,2 * * * *", "empty list item"},
		{"* * * * MON/2", "step"},
		{"* * * * 1-5/MON", "step"},
		{"5/2 * * * *", "single value"},
		{"* * * FOO *", "month field: unknown token"},
		{"x * * * *", "minute field"},
		{"* * * * 1-", "day-of-week field: missing"},
		{"-1 * * * *", "minute field"},
	}
	for _, c := range cases {
		_, err := Parse(c.expr)
		if err == nil {
			t.Errorf("Parse(%q) accepted", c.expr)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("Parse(%q) = %q, want it to contain %q", c.expr, err, c.want)
		}
	}
}

func TestParseAccepts(t *testing.T) {
	for _, expr := range []string{
		"* * * * *", "*/5 * * * *", "0 9-17 * * MON-FRI", "0 0 1,15 * *", "1-10/3 * * * *",
		"0 0 * jan,Jul *", "0\t0  *   * sun", "@DAILY", "  @hourly  ", "0 0 29 2 *", "0 0 * * 7", "0 0 * * 5-7",
	} {
		if _, err := Parse(expr); err != nil {
			t.Errorf("Parse(%q): %v", expr, err)
		}
	}
	e, _ := Parse("  0 0 * * *  ")
	if e.String() != "0 0 * * *" {
		t.Errorf("stored text %q, want trimmed", e.String())
	}
}

func TestMacros(t *testing.T) {
	want := map[string]string{
		"@yearly": "0 0 1 1 *", "@annually": "0 0 1 1 *", "@monthly": "0 0 1 * *",
		"@weekly": "0 0 * * 0", "@daily": "0 0 * * *", "@midnight": "0 0 * * *", "@hourly": "0 * * * *",
	}
	from := at(t, "2026-03-10T12:34:56Z")
	for macro, expanded := range want {
		m, err := Parse(macro)
		if err != nil {
			t.Fatalf("%s: %v", macro, err)
		}
		x, _ := Parse(expanded)
		a, aok := m.Next(from, time.UTC)
		b, bok := x.Next(from, time.UTC)
		if !aok || !bok || !a.Equal(b) {
			t.Errorf("%s next %v/%v, expanded %v/%v", macro, a, aok, b, bok)
		}
	}
}

func TestNextTable(t *testing.T) {
	utc, east, west := "UTC", "+09:00", "-05:30"
	cases := []struct {
		name, expr, tz, from, want string
	}{
		{"every minute", "* * * * *", utc, "2026-03-10T12:34:56Z", "2026-03-10T12:35:00Z"},
		{"strictly after an exact minute", "* * * * *", utc, "2026-03-10T12:34:00Z", "2026-03-10T12:35:00Z"},
		{"hourly", "0 * * * *", utc, "2026-03-10T12:00:00Z", "2026-03-10T13:00:00Z"},
		{"step", "*/15 * * * *", utc, "2026-03-10T12:16:00Z", "2026-03-10T12:30:00Z"},
		{"range step", "1-10/3 * * * *", utc, "2026-03-10T12:08:00Z", "2026-03-10T12:10:00Z"},
		{"list", "5,40 3 * * *", utc, "2026-03-10T03:05:00Z", "2026-03-10T03:40:00Z"},
		// 2026-03-10 is a Tuesday.
		{"dow name", "0 9 * * MON", utc, "2026-03-10T00:00:00Z", "2026-03-16T09:00:00Z"},
		{"sunday as 0", "0 9 * * 0", utc, "2026-03-10T00:00:00Z", "2026-03-15T09:00:00Z"},
		{"sunday as 7", "0 9 * * 7", utc, "2026-03-10T00:00:00Z", "2026-03-15T09:00:00Z"},
		{"range 5-7 includes sunday", "0 9 * * 5-7", utc, "2026-03-14T10:00:00Z", "2026-03-15T09:00:00Z"},
		{"range 5-7 includes friday", "0 9 * * 5-7", utc, "2026-03-10T00:00:00Z", "2026-03-13T09:00:00Z"},
		{"weekday range", "30 8 * * mon-fri", utc, "2026-03-13T09:00:00Z", "2026-03-16T08:30:00Z"},
		{"month name", "0 0 1 JUL *", utc, "2026-03-10T00:00:00Z", "2026-07-01T00:00:00Z"},
		// dom OR dow when both restricted: the 15th or any Monday.
		{"dom or dow first is monday", "0 0 15 * 1", utc, "2026-03-10T00:00:00Z", "2026-03-15T00:00:00Z"},
		{"dom or dow monday first", "0 0 20 * 1", utc, "2026-03-10T00:00:00Z", "2026-03-16T00:00:00Z"},
		// A field beginning with * is unrestricted: both must hold.
		{"dom star dow restricted", "0 0 * * 1", utc, "2026-03-10T00:00:00Z", "2026-03-16T00:00:00Z"},
		{"dom restricted dow star", "0 0 20 * *", utc, "2026-03-10T00:00:00Z", "2026-03-20T00:00:00Z"},
		{"dow star step is unrestricted AND", "0 0 13 * */2", utc, "2026-03-01T00:00:00Z", "2026-06-13T00:00:00Z"},
		{"dom star step is unrestricted AND", "0 0 */2 * 1", utc, "2026-03-10T00:00:00Z", "2026-03-23T00:00:00Z"},
		// Month end, year end, leap day.
		{"month end", "0 0 1 * *", utc, "2026-01-31T23:59:30Z", "2026-02-01T00:00:00Z"},
		{"31st skips short months", "0 0 31 * *", utc, "2026-01-31T00:00:00Z", "2026-03-31T00:00:00Z"},
		{"year end", "0 0 1 1 *", utc, "2026-12-31T23:59:59Z", "2027-01-01T00:00:00Z"},
		{"leap day", "0 0 29 2 *", utc, "2026-03-01T00:00:00Z", "2028-02-29T00:00:00Z"},
		{"leap day from a leap day", "0 0 29 2 *", utc, "2028-02-29T00:00:00Z", "2032-02-29T00:00:00Z"},
		{"leap day after a century non-leap year", "0 0 29 2 *", utc, "2096-03-01T00:00:00Z", "2104-02-29T00:00:00Z"},
		{"leap day across 2100", "0 0 29 2 *", utc, "2096-02-29T00:00:00Z", "2104-02-29T00:00:00Z"},
		// The same instant matches different local minutes per offset.
		{"daily 09:00 east", "0 9 * * *", east, "2026-03-09T23:30:00Z", "2026-03-10T09:00:00+09:00"},
		{"daily 09:00 east is 00:00Z", "0 9 * * *", east, "2026-03-09T00:30:00Z", "2026-03-10T09:00:00+09:00"},
		{"daily 09:00 utc", "0 9 * * *", utc, "2026-03-10T00:30:00Z", "2026-03-10T09:00:00Z"},
		{"daily 09:00 west", "0 9 * * *", west, "2026-03-10T00:30:00Z", "2026-03-10T09:00:00-05:30"},
		{"month end east", "0 0 1 * *", east, "2026-01-31T14:59:00Z", "2026-02-01T00:00:00+09:00"},
		{"month end east instant", "0 0 1 * *", east, "2026-01-31T15:00:00Z", "2026-03-01T00:00:00+09:00"},
		{"year end west", "0 0 1 1 *", west, "2026-12-31T23:59:00Z", "2027-01-01T00:00:00-05:30"},
		{"leap day east", "0 12 29 2 *", east, "2027-06-01T00:00:00Z", "2028-02-29T12:00:00+09:00"},
		{"leap day west", "0 12 29 2 *", west, "2027-06-01T00:00:00Z", "2028-02-29T12:00:00-05:30"},
		{"half hour offset minute", "45 * * * *", west, "2026-03-10T12:00:00Z", "2026-03-10T06:45:00-05:30"},
		{"weekday follows the offset", "0 0 * * MON", east, "2026-03-15T14:00:00Z", "2026-03-16T00:00:00+09:00"},
		{"weekday in utc differs", "0 0 * * MON", utc, "2026-03-15T14:00:00Z", "2026-03-16T00:00:00Z"},
	}
	for _, c := range cases {
		e, err := Parse(c.expr)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		got, ok := e.Next(at(t, c.from), mustTZ(t, c.tz))
		if !ok {
			t.Errorf("%s: no next fire", c.name)
			continue
		}
		if !got.Equal(at(t, c.want)) {
			t.Errorf("%s: Next(%s) = %s, want %s", c.name, c.from, got.Format(time.RFC3339), c.want)
		}
		if _, off := got.Zone(); off != func() int { _, o := at(t, c.want).Zone(); return o }() {
			t.Errorf("%s: result offset %d, want the schedule's offset", c.name, off)
		}
	}
}

func TestNeverMatches(t *testing.T) {
	for _, expr := range []string{"0 0 30 2 *", "0 0 31 4 *", "0 0 31 2,4,6,9,11 *"} {
		e, err := Parse(expr)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got, ok := e.Next(at(t, "2026-03-10T00:00:00Z"), time.UTC); ok {
			t.Errorf("%s matched %v", expr, got)
		}
	}
	// Feb 30 OR Monday is satisfiable through the weekday.
	e, _ := Parse("0 0 30 2 1")
	if _, ok := e.Next(at(t, "2026-03-10T00:00:00Z"), time.UTC); !ok {
		t.Error("dom/dow OR with an impossible dom should still fire on the dow")
	}
}

func TestParseTZ(t *testing.T) {
	ok := map[string]string{
		"UTC": "UTC", "utc": "UTC", "Z": "UTC", "z": "UTC", "": "UTC", " Z ": "UTC",
		"+09:00": "+09:00", "-05:30": "-05:30", "+00:00": "+00:00", "-00:00": "+00:00",
		"+14:00": "+14:00", "-12:00": "-12:00", "+05:45": "+05:45",
	}
	for in, want := range ok {
		name, loc, err := ParseTZ(in)
		if err != nil || name != want || loc == nil {
			t.Errorf("ParseTZ(%q) = %q, %v; want %q", in, name, err, want)
		}
	}
	_, loc, _ := ParseTZ("+09:30")
	if _, off := time.Unix(0, 0).In(loc).Zone(); off != 9*3600+1800 {
		t.Errorf("+09:30 offset %d", off)
	}
	for _, in := range []string{"Asia/Seoul", "KST", "+9", "+09", "+0900", "+09:00:00", "+09:60", "+14:01", "+15:00", "-12:01", "-13:00", "09:00", "+9:00", "+0a:00", "UTC+9", "America/New_York"} {
		_, _, err := ParseTZ(in)
		if err == nil {
			t.Errorf("ParseTZ(%q) accepted", in)
			continue
		}
		if !strings.Contains(err.Error(), "+HH:MM") {
			t.Errorf("ParseTZ(%q) error %q does not list the accepted forms", in, err)
		}
	}
}

// Property: Next is strictly after the input, in the requested offset, matches
// the parsed fields, and no earlier minute in between matches.
func TestNextProperty(t *testing.T) {
	exprs := []string{
		"* * * * *", "*/7 */3 * * *", "0 0 29 2 *", "15 4 1,15 * *", "0 12 * * 1-5",
		"30 6 31 * *", "0 0 15 * 1", "5 5 * 2,8 SUN", "59 23 31 12 *", "0 0 1 1 *",
	}
	zones := []string{"UTC", "+09:00", "-05:30", "+14:00", "-12:00", "+05:45"}
	rng := rand.New(rand.NewSource(7))
	base := at(t, "2020-01-01T00:00:00Z")
	for i := 0; i < 400; i++ {
		expr := exprs[rng.Intn(len(exprs))]
		e, err := Parse(expr)
		if err != nil {
			t.Fatal(err)
		}
		loc := mustTZ(t, zones[rng.Intn(len(zones))])
		from := base.Add(time.Duration(rng.Int63n(int64(12 * 365 * 24 * time.Hour))))
		got, ok := e.Next(from, loc)
		if !ok {
			t.Fatalf("%q from %v: no next", expr, from)
		}
		if !got.After(from) {
			t.Fatalf("%q: Next(%v) = %v is not strictly after", expr, from, got)
		}
		if got.Location() != loc {
			t.Fatalf("%q: result location %v, want %v", expr, got.Location(), loc)
		}
		if !e.Matches(got) || got.Second() != 0 {
			t.Fatalf("%q: %v does not match", expr, got)
		}
		// Nothing matches between: walk minutes for the dense expressions only.
		if gap := got.Sub(from); gap < 3*24*time.Hour {
			for m := from.In(loc).Truncate(time.Minute).Add(time.Minute); m.Before(got); m = m.Add(time.Minute) {
				if e.Matches(m) {
					t.Fatalf("%q: Next(%v) = %v skipped %v", expr, from, got, m)
				}
			}
		}
	}
}

func FuzzParseNext(f *testing.F) {
	for _, s := range []string{"* * * * *", "0 0 29 2 *", "@daily", "*/5 1-3 1,2 jan-mar mon-fri", "0 0 30 2 *"} {
		f.Add(s, int64(1_700_000_000))
	}
	f.Fuzz(func(t *testing.T, expr string, sec int64) {
		e, err := Parse(expr)
		if err != nil {
			return
		}
		from := time.Unix(sec%(4_000_000_000), 0)
		for _, loc := range []*time.Location{time.UTC, time.FixedZone("", 9*3600), time.FixedZone("", -(5*3600 + 1800))} {
			got, ok := e.Next(from, loc)
			if !ok {
				continue
			}
			if !got.After(from) || !e.Matches(got) {
				t.Fatalf("%q: Next(%v) = %v", expr, from, got)
			}
		}
	})
}
