package wsagent

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"
)

// agy writes one glog-format log per session process. The stall cutoff
// reads it only when the silence timer fires; the format is agy's, not a
// contract, so anything this reader does not recognize yields
// agyStallUnknown, which never cuts.
const (
	// agyStallTailBytes bounds one firing's read: one open, one stat, at
	// most one seek, and a bounded read.
	agyStallTailBytes = 512 << 10
	// agyStallSkew is the margin between the daemon's clock reading at a
	// request boundary and agy's own log timestamps.
	agyStallSkew = 2 * time.Second
	// agyLocateScanBytes bounds the content scan of one candidate log. A
	// process serves one session, and its first prompt names it.
	agyLocateScanBytes = 8 << 20
	// AgyFrameLogName is the frame trace capture file in a session's
	// directory.
	AgyFrameLogName = "agy-http2-frames.log"
	// agyFrameCaptureMaxBytes caps the holder's frame capture file.
	agyFrameCaptureMaxBytes = 64 << 20
)

// agyStallVerdict is what one tail says about the current model request.
type agyStallVerdict int

const (
	// agyStallUnknown: the log was not located, read, verified, or parsed.
	agyStallUnknown agyStallVerdict = iota
	// agyStallHeader: a main-request response header is in the window, so
	// the model is generating.
	agyStallHeader
	// agyStallHeaderless: a verified tail shows agy received the current
	// session/prompt request but no main-request header at or after the
	// boundary minus the skew margin, and this process's log has shown a
	// main-request header before.
	agyStallHeaderless
)

// agyGlogLine is one glog line: severity, MMDD, HH:MM:SS.micros, the
// goroutine id, file:line, and the message.
var agyGlogLine = regexp.MustCompile(`^[IWEF](\d{2})(\d{2}) (\d{2}):(\d{2}):(\d{2})\.(\d{6})\s+(\d+) ([^\s\]]+)\] (.*)$`)

// agyDefaultLogDir is agy's own log directory under the user's home.
func agyDefaultLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".gemini", "antigravity-cli", "log")
}

// agyStallLog is one agy process's worker log: located once, on the first
// silence firing, and cached for the process lifetime.
type agyStallLog struct {
	dir string
	// since is the process start (or the daemon's rejoin of it): a log
	// last modified before it belongs to an earlier process.
	since time.Time
	// tried is set after the one locator scan; path is its result, err
	// why it found none.
	tried bool
	path  string
	err   error
	// sawMain is set once a tail of this process's log has shown a
	// main-request header. Until then a firing never cuts: a monitor
	// header verifies the format but proves nothing about main requests.
	sawMain bool
}

// newAgyStallLog takes started with no backward slack: a predecessor
// process of the same session stopped before it, so its log does not
// qualify, and a coarse file timestamp only makes the log unknown.
//
// agy names a worker log after the worker's pid, which the daemon never
// learns (it starts the front process, which starts the worker), so the
// locator matches by session and modification time, not by pid.
func newAgyStallLog(dir string, started time.Time) *agyStallLog {
	return &agyStallLog{dir: dir, since: started}
}

// verdict reads the cached log's tail once and judges it against boundary
// in the local zone. The locator scan runs at most once per process.
func (l *agyStallLog) verdict(sessionID string, boundary time.Time) (agyStallVerdict, string) {
	if l == nil {
		return agyStallUnknown, "no agy process log state"
	}
	if !l.tried {
		l.tried = true
		l.path, l.err = locateAgyWorkerLog(l.dir, sessionID, l.since)
	}
	if l.err != nil {
		return agyStallUnknown, l.err.Error()
	}
	tail, clipped, err := readAgyLogTail(l.path, agyStallTailBytes)
	if err != nil {
		return agyStallUnknown, err.Error()
	}
	verdict, anyMain, reason := parseAgyStallTail(tail, clipped, boundary.Local(), agyStallSkew)
	l.sawMain = l.sawMain || anyMain
	if verdict == agyStallHeaderless && !l.sawMain {
		return agyStallUnknown, "this agy process's log has shown no main request header yet"
	}
	return verdict, reason
}

// locateAgyWorkerLog finds the newest acp-worker-*.log modified at or after
// since
// whose content names sessionID in agy's "Sending user message to
// conversation <id>" line.
func locateAgyWorkerLog(dir, sessionID string, since time.Time) (string, error) {
	if dir == "" {
		return "", errors.New("agy log directory unknown")
	}
	if sessionID == "" {
		return "", errors.New("no backend session id to locate the agy log by")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("list agy logs: %w", err)
	}
	type candidate struct {
		path string
		mod  time.Time
	}
	var candidates []candidate
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "acp-worker-") || !strings.HasSuffix(name, ".log") {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().Before(since) {
			continue
		}
		candidates = append(candidates, candidate{filepath.Join(dir, name), info.ModTime()})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].mod.After(candidates[j].mod) })
	needle := []byte("Sending user message to conversation " + sessionID)
	for _, c := range candidates {
		if agyLogNames(c.path, needle) {
			return c.path, nil
		}
	}
	return "", fmt.Errorf("no agy log modified since the process started names session %s", sessionID)
}

// agyLogNames reports whether the first agyLocateScanBytes of path hold a
// server.go line carrying needle followed by a word boundary.
func agyLogNames(path string, needle []byte) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(io.LimitReader(f, agyLocateScanBytes))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Bytes()
		i := bytes.Index(line, needle)
		if i < 0 || !bytes.Contains(line[:i], []byte("server.go:")) {
			continue
		}
		if rest := line[i+len(needle):]; len(rest) == 0 || rest[0] == ' ' {
			return true
		}
	}
	return false
}

// readAgyLogTail reads at most limit bytes from the end of path: one open,
// one stat of the open file, one seek when the file is longer than limit,
// and sequential reads of the remaining bytes (one read in the usual case).
// ReadAt is avoided: on Windows a positioned read on a blocking handle
// saves and restores the file pointer with two more seeks. clipped reports
// that the tail starts inside the file, so its first line may be partial.
func readAgyLogTail(path string, limit int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("open agy log: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("stat agy log: %w", err)
	}
	size := info.Size()
	start := max(0, size-limit)
	if start > 0 {
		if _, err := f.Seek(start, io.SeekStart); err != nil {
			return nil, false, fmt.Errorf("seek agy log: %w", err)
		}
	}
	// The file may change after the stat: read what is there, at most the
	// planned bytes.
	buf := make([]byte, size-start)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, false, fmt.Errorf("read agy log: %w", err)
	}
	return buf[:n], start > 0, nil
}

// agyLogLine is one parsed glog line of a tail. A local time that a clock
// rollback repeats names two instants: at is the earlier and late the later
// (equal when the time is unambiguous).
type agyLogLine struct {
	at        time.Time
	late      time.Time
	goroutine string
	file      string
	msg       string
}

// parseAgyStallTail judges a tail against the request boundary, in the
// boundary's location:
//   - coverage: the first complete glog line is before boundary-skew;
//   - attribution (headerless only): agy's per-request line for a
//     session/prompt is at or after the boundary itself, with no skew, so
//     this log's worker received the current request. The runner takes the
//     boundary before the send and agy stamps the receipt on the same host
//     clock, so the current prompt's line is never before it; a line before
//     it may be an earlier prompt's, which a cached log whose worker agy
//     replaced under the same front process can hold (the current request
//     then logs elsewhere). Without the line the tail proves nothing about
//     the current request. The skew margin only widens the header lookback,
//     which suppresses cuts and never enables one;
//   - format: at least one streamGenerateContent header line (main or
//     monitor) is in this tail;
//   - a header line counts as main unless its goroutine logged an
//     llm_monitor.go "Calling LLM" line within this tail.
//
// A verified tail with a main header at or after boundary-skew is
// agyStallHeader, one without is agyStallHeaderless; anything else is
// agyStallUnknown with the reason. anyMain reports a main header anywhere
// in the tail, inside the window or not.
//
// An ambiguous local time counts on the side that never cuts: a header in
// the window under either reading is in the window, the first line covers
// the boundary only if both readings precede the window, and the request
// line counts only if both readings are at or after the boundary.
func parseAgyStallTail(tail []byte, clipped bool, boundary time.Time, skew time.Duration) (verdict agyStallVerdict, anyMain bool, reason string) {
	if boundary.IsZero() {
		return agyStallUnknown, false, "no request boundary"
	}
	if clipped {
		nl := bytes.IndexByte(tail, '\n')
		if nl < 0 {
			return agyStallUnknown, false, "the agy log tail holds no complete line"
		}
		tail = tail[nl+1:]
	}
	window := boundary.Add(-skew)
	var lines []agyLogLine
	monitors := map[string]bool{}
	for _, raw := range strings.Split(string(tail), "\n") {
		line, ok := parseAgyGlogLine(strings.TrimSuffix(raw, "\r"), boundary)
		if !ok {
			continue
		}
		if strings.HasPrefix(line.file, "llm_monitor.go:") && strings.Contains(line.msg, "Calling LLM") {
			monitors[line.goroutine] = true
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return agyStallUnknown, false, "the agy log tail holds no glog line"
	}
	recognized, main, requested := false, false, false
	for _, line := range lines {
		if agyPromptRequestLine(line) && !line.at.Before(boundary) {
			requested = true
		}
		if !agyHeaderLine(line) {
			continue
		}
		recognized = true
		if monitors[line.goroutine] {
			continue
		}
		anyMain = true
		if !line.late.Before(window) {
			main = true
		}
	}
	switch {
	case !lines[0].late.Before(window):
		return agyStallUnknown, anyMain, "the agy log tail starts after the request boundary"
	case !recognized:
		return agyStallUnknown, anyMain, "the agy log tail holds no recognized streamGenerateContent header line"
	case main:
		return agyStallHeader, anyMain, ""
	case !requested:
		return agyStallUnknown, anyMain, "the agy log tail holds no session/prompt request line since the request boundary"
	}
	return agyStallHeaderless, anyMain, ""
}

// agyPromptRequestSource is the source tag of the line agy's ACP server
// logs once per request it receives, naming the request's method.
const agyPromptRequestSource = "server.go:213"

// agyPromptRequestLine reports agy's per-request line for a session/prompt:
// the source tag and the method token, no other message content. A version
// that moves the line matches nothing, which never cuts.
func agyPromptRequestLine(line agyLogLine) bool {
	return line.file == agyPromptRequestSource && slices.Contains(strings.Fields(line.msg), "session/prompt")
}

// agyHeaderLine reports an http_helpers.go line agy logs once a
// streamGenerateContent response has headers.
func agyHeaderLine(line agyLogLine) bool {
	if !strings.HasPrefix(line.file, "http_helpers.go:") {
		return false
	}
	url, ok := strings.CutPrefix(line.msg, "URL: ")
	return ok && strings.Contains(url, "streamGenerateContent")
}

// parseAgyGlogLine parses one glog line in the boundary's location. glog
// stamps local month, day, and time without a year: the year is the
// boundary's, moved by one when that puts the line more than half a year
// from the boundary (a year wrap).
func parseAgyGlogLine(raw string, boundary time.Time) (agyLogLine, bool) {
	m := agyGlogLine.FindStringSubmatch(raw)
	if m == nil {
		return agyLogLine{}, false
	}
	var n [6]int
	for i := range n {
		for _, r := range m[i+1] {
			n[i] = n[i]*10 + int(r-'0')
		}
	}
	month, day, hour, minute, sec, micros := n[0], n[1], n[2], n[3], n[4], n[5]
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || minute > 59 || sec > 60 {
		return agyLogLine{}, false
	}
	loc := boundary.Location()
	year := boundary.Year()
	at := time.Date(year, time.Month(month), day, hour, minute, sec, micros*1000, loc)
	if at.Month() != time.Month(month) {
		// Feb 29 in a non-leap boundary year; the log is a year away.
		year--
		at = time.Date(year, time.Month(month), day, hour, minute, sec, micros*1000, loc)
	}
	switch {
	case at.After(boundary.AddDate(0, 6, 0)):
		year--
	case at.Before(boundary.AddDate(0, -6, 0)):
		year++
	}
	early, late := localInstants(year, time.Month(month), day, hour, minute, sec, micros*1000, loc)
	return agyLogLine{at: early, late: late, goroutine: m[7], file: m[8], msg: m[9]}, true
}

// localInstants returns the earliest and latest instants whose wall clock in
// loc reads the given time. They differ only inside an hour that a clock
// rollback repeats; a time that a forward jump skips normalizes as
// time.Date does.
func localInstants(year int, month time.Month, day, hour, minute, sec, nsec int, loc *time.Location) (time.Time, time.Time) {
	t := time.Date(year, month, day, hour, minute, sec, nsec, loc)
	early, late := t, t
	_, before := t.Add(-24 * time.Hour).Zone()
	_, after := t.Add(24 * time.Hour).Zone()
	if before == after {
		return early, late
	}
	shift := time.Duration(before-after) * time.Second
	if shift < 0 {
		shift = -shift
	}
	for _, alt := range []time.Time{t.Add(-shift), t.Add(shift)} {
		y, mo, d := alt.Date()
		h, mi, s := alt.Clock()
		if y != year || mo != month || d != day || h != hour || mi != minute || s != sec {
			continue
		}
		if alt.Before(early) {
			early = alt
		}
		if alt.After(late) {
			late = alt
		}
	}
	return early, late
}
