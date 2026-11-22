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
	"sort"
	"strings"
	"time"
)

// agy writes one glog-format log per session process. The stall cutoff
// reads it only when the silence timer fires; the format is agy's, not a
// contract, so anything this reader does not recognize yields
// agyStallUnknown, which never cuts.
const (
	// agyStallTailBytes bounds one firing's read: one open, one seek to
	// the end, and one positioned read.
	agyStallTailBytes = 512 << 10
	// agyStallSkew is the margin between the daemon's clock reading at a
	// request boundary and agy's own log timestamps.
	agyStallSkew = 2 * time.Second
	// agyLocateScanBytes bounds the content scan of one candidate log. A
	// process serves one session, and its first prompt names it.
	agyLocateScanBytes = 8 << 20
	// agyLocateSlack widens "modified since the process started" for file
	// timestamp granularity.
	agyLocateSlack = 2 * time.Second
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
	// agyStallHeaderless: a verified tail shows no main-request header at
	// or after the boundary minus the skew margin.
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
	dir   string
	since time.Time
	// tried is set after the one locator scan; path is its result, err
	// why it found none.
	tried bool
	path  string
	err   error
}

func newAgyStallLog(dir string, started time.Time) *agyStallLog {
	return &agyStallLog{dir: dir, since: started.Add(-agyLocateSlack)}
}

// verdict reads the cached log's tail once and judges it against boundary.
// The locator scan runs at most once per process.
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
	return parseAgyStallTail(tail, clipped, boundary, agyStallSkew)
}

// locateAgyWorkerLog finds the newest acp-worker-*.log modified since since
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

// readAgyLogTail reads at most limit bytes from the end of path with one
// open, one seek, and one positioned read. clipped reports that the tail
// starts inside the file, so its first line may be partial.
func readAgyLogTail(path string, limit int64) ([]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("open agy log: %w", err)
	}
	defer f.Close()
	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, false, fmt.Errorf("seek agy log: %w", err)
	}
	start := max(0, end-limit)
	buf := make([]byte, end-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && !(errors.Is(err, io.EOF) && n == len(buf)) {
		return nil, false, fmt.Errorf("read agy log: %w", err)
	}
	return buf[:n], start > 0, nil
}

// agyLogLine is one parsed glog line of a tail.
type agyLogLine struct {
	at        time.Time
	goroutine string
	file      string
	msg       string
}

// parseAgyStallTail judges a tail against the request boundary:
//   - coverage: the first complete glog line is before boundary-skew;
//   - format: at least one streamGenerateContent header line (main or
//     monitor) is in this tail;
//   - a header line counts as main unless its goroutine logged an
//     llm_monitor.go "Calling LLM" line within this tail.
//
// A verified tail with a main header at or after boundary-skew is
// agyStallHeader, one without is agyStallHeaderless; anything else is
// agyStallUnknown with the reason.
func parseAgyStallTail(tail []byte, clipped bool, boundary time.Time, skew time.Duration) (agyStallVerdict, string) {
	if boundary.IsZero() {
		return agyStallUnknown, "no request boundary"
	}
	if clipped {
		nl := bytes.IndexByte(tail, '\n')
		if nl < 0 {
			return agyStallUnknown, "the agy log tail holds no complete line"
		}
		tail = tail[nl+1:]
	}
	local := boundary.Local()
	window := boundary.Add(-skew)
	var lines []agyLogLine
	monitors := map[string]bool{}
	for _, raw := range strings.Split(string(tail), "\n") {
		line, ok := parseAgyGlogLine(strings.TrimSuffix(raw, "\r"), local)
		if !ok {
			continue
		}
		if strings.HasPrefix(line.file, "llm_monitor.go:") && strings.Contains(line.msg, "Calling LLM") {
			monitors[line.goroutine] = true
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return agyStallUnknown, "the agy log tail holds no glog line"
	}
	if !lines[0].at.Before(window) {
		return agyStallUnknown, "the agy log tail starts after the request boundary"
	}
	recognized, main := false, false
	for _, line := range lines {
		if !agyHeaderLine(line) {
			continue
		}
		recognized = true
		if !monitors[line.goroutine] && !line.at.Before(window) {
			main = true
		}
	}
	switch {
	case !recognized:
		return agyStallUnknown, "the agy log tail holds no recognized streamGenerateContent header line"
	case main:
		return agyStallHeader, ""
	}
	return agyStallHeaderless, ""
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

// parseAgyGlogLine parses one glog line. glog stamps local month, day, and
// time without a year: the year is the boundary's, moved by one when that
// puts the line more than half a year from the boundary (a year wrap).
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
	at := time.Date(boundary.Year(), time.Month(month), day, hour, minute, sec, micros*1000, boundary.Location())
	if at.Month() != time.Month(month) {
		// Feb 29 in a non-leap boundary year; the log is a year away.
		at = time.Date(boundary.Year()-1, time.Month(month), day, hour, minute, sec, micros*1000, boundary.Location())
	}
	switch {
	case at.After(boundary.AddDate(0, 6, 0)):
		at = at.AddDate(-1, 0, 0)
	case at.Before(boundary.AddDate(0, -6, 0)):
		at = at.AddDate(1, 0, 0)
	}
	return agyLogLine{at: at, goroutine: m[7], file: m[8], msg: m[9]}, true
}
