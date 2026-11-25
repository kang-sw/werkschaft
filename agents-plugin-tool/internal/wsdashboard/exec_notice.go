package wsdashboard

import (
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsi18n"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// ExecNoticeView is a parsed ws/exec completion or user-stop notice for
// compact conversation rendering and optional live monitor jump.
type ExecNoticeView struct {
	ID            string
	State         string // exited, signal, stopped, running, failed, lost, ended
	ExitCode      *int
	Signal        *int
	ErrorText     string
	Duration      string
	Command       string
	Summary       string
	StoppedByUser bool
	StopInfo      string
	OutputHeader  string
	OutputShown   int
	OutputTotal   int
	Output        string
	NoOutput      bool
	Failed        bool
}

const (
	execNoticePrefix  = "[ws exec] "
	execTailPrefix    = "Read more with exec.tail (exec_id "
	execTailSuffix    = ")."
	execCmdPrefix     = "command: "
	execStopPrefix    = "stopped by user: "
	execOutLastPrefix = "output (last "
	execOutLastMid    = " of "
	execOutLastSuffix = " bytes):"
)

// parseExecNoticeEntry parses either a raw notice payload or a single
// <ws:exec>...</ws:exec> block wrapping a notice payload.
func parseExecNoticeEntry(text string) (*ExecNoticeView, bool) {
	if blk, ok := wsinbox.ParseExecBlock(text); ok {
		return parseExecNotice(blk.Body)
	}
	return parseExecNotice(text)
}

// parseExecNotice strictly parses a hosted ws/exec completion notice
// (execjob/hosted_notice.go:noticeText) or user-stop notice
// (execjob/hosted_hud.go:stopNoticeText). Payloads with extra leading or
// trailing prose, mismatched or missing exec.tail trailers, unknown end states,
// or concatenated notices are rejected.
func parseExecNotice(text string) (*ExecNoticeView, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, execNoticePrefix) {
		return nil, false
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return nil, false
	}
	v, ok := parseExecHeader(lines[0])
	if !ok {
		return nil, false
	}
	wantTail := execTailPrefix + v.ID + execTailSuffix
	if lines[len(lines)-1] != wantTail {
		return nil, false
	}
	middle := lines[1 : len(lines)-1]
	if len(middle) == 0 || hasEmbeddedNotice(middle) {
		return nil, false
	}

	i := 0
	if strings.HasPrefix(middle[i], execCmdPrefix) {
		cmdLines := []string{strings.TrimPrefix(middle[i], execCmdPrefix)}
		i++
		for i < len(middle) &&
			!strings.HasPrefix(middle[i], execStopPrefix) &&
			!isOutputSectionLine(middle[i], i == len(middle)-1) {
			cmdLines = append(cmdLines, middle[i])
			i++
		}
		v.Command = strings.Join(cmdLines, "\n")
	}
	if i < len(middle) && strings.HasPrefix(middle[i], execStopPrefix) {
		v.StoppedByUser = true
		stopLines := []string{strings.TrimPrefix(middle[i], execStopPrefix)}
		i++
		for i < len(middle) && !isOutputSectionLine(middle[i], i == len(middle)-1) {
			stopLines = append(stopLines, middle[i])
			i++
		}
		v.StopInfo = strings.Join(stopLines, "\n")
	}
	if i >= len(middle) {
		return nil, false
	}
	switch {
	case middle[i] == "no output.":
		if i != len(middle)-1 {
			return nil, false
		}
		v.NoOutput = true
	case middle[i] == "output:":
		v.OutputHeader = middle[i]
		v.Output = strings.Join(middle[i+1:], "\n")
	default:
		shown, total, ok := parseOutputLastHeader(middle[i])
		if !ok {
			return nil, false
		}
		v.OutputHeader = middle[i]
		v.OutputShown = shown
		v.OutputTotal = total
		v.Output = strings.Join(middle[i+1:], "\n")
	}
	v.Summary = execCommandSummary(v.Command, v.ID)
	return v, true
}

func parseExecHeader(line string) (*ExecNoticeView, bool) {
	rest, ok := strings.CutPrefix(line, execNoticePrefix)
	if !ok || !strings.HasSuffix(rest, ".") {
		return nil, false
	}
	rest = strings.TrimSuffix(rest, ".")
	sp := strings.IndexByte(rest, ' ')
	if sp <= 0 || sp == len(rest)-1 {
		return nil, false
	}
	id := rest[:sp]
	if strings.ContainsAny(id, " \t\r") {
		return nil, false
	}
	endPart := rest[sp+1:]
	dur := ""
	if idx := strings.LastIndex(endPart, " after "); idx > 0 {
		cand := endPart[idx+len(" after "):]
		if cand != "" && !strings.ContainsAny(cand, " \t\r") {
			if _, err := time.ParseDuration(cand); err == nil {
				dur = cand
				endPart = endPart[:idx]
			}
		}
	}
	v := &ExecNoticeView{ID: id, Duration: dur}
	switch {
	case strings.HasPrefix(endPart, "exited with code "):
		raw := strings.TrimPrefix(endPart, "exited with code ")
		code, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(code) != raw {
			return nil, false
		}
		v.State = "exited"
		v.ExitCode = &code
		v.Failed = code != 0
	case strings.HasPrefix(endPart, "was ended by signal "):
		raw := strings.TrimPrefix(endPart, "was ended by signal ")
		sig, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(sig) != raw {
			return nil, false
		}
		v.State = "signal"
		v.Signal = &sig
		v.Failed = true
	case endPart == "was stopped":
		v.State = "stopped"
	case endPart == "is running":
		v.State = "running"
	case strings.HasPrefix(endPart, "failed to start: "):
		v.State = "failed"
		v.ErrorText = strings.TrimPrefix(endPart, "failed to start: ")
		v.Failed = true
	case strings.HasPrefix(endPart, "was lost: "):
		v.State = "lost"
		v.ErrorText = strings.TrimPrefix(endPart, "was lost: ")
		v.Failed = true
	case endPart == "ended":
		v.State = "ended"
	default:
		return nil, false
	}
	return v, true
}

func isOutputSectionLine(line string, isLast bool) bool {
	if line == "output:" {
		return true
	}
	if line == "no output." && isLast {
		return true
	}
	_, _, ok := parseOutputLastHeader(line)
	return ok
}

func parseOutputLastHeader(line string) (int, int, bool) {
	rest, ok := strings.CutPrefix(line, execOutLastPrefix)
	if !ok {
		return 0, 0, false
	}
	rest, ok = strings.CutSuffix(rest, execOutLastSuffix)
	if !ok {
		return 0, 0, false
	}
	a, b, ok := strings.Cut(rest, execOutLastMid)
	if !ok {
		return 0, 0, false
	}
	shown, err1 := strconv.Atoi(a)
	total, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || shown <= 0 || total <= 0 || strconv.Itoa(shown) != a || strconv.Itoa(total) != b {
		return 0, 0, false
	}
	return shown, total, true
}

// hasEmbeddedNotice rejects two concatenated exec notices stuffed into one
// block (even when they share an exec_id).
func hasEmbeddedNotice(middle []string) bool {
	for i, line := range middle {
		if !strings.HasPrefix(line, execTailPrefix) || !strings.HasSuffix(line, execTailSuffix) {
			continue
		}
		innerID := line[len(execTailPrefix) : len(line)-len(execTailSuffix)]
		if innerID == "" || strings.ContainsAny(innerID, " \t\r") {
			continue
		}
		for j := i + 1; j < len(middle); j++ {
			if strings.TrimSpace(middle[j]) == "" {
				continue
			}
			if strings.HasPrefix(middle[j], execNoticePrefix) {
				return true
			}
			break
		}
	}
	return false
}

func execCommandSummary(command, id string) string {
	s := strings.Join(strings.Fields(command), " ")
	if s != "" {
		return s
	}
	return id
}

// StatusLabel returns the compact localized status shown on the collapsed row.
func (v *ExecNoticeView) StatusLabel(lang string) string {
	if v == nil {
		return ""
	}
	switch v.State {
	case "exited":
		if v.ExitCode != nil {
			return wsi18n.T(lang, "entry.exec.exit", *v.ExitCode)
		}
		return wsi18n.T(lang, "entry.exec.ended")
	case "signal":
		if v.Signal != nil {
			return wsi18n.T(lang, "entry.exec.signal", *v.Signal)
		}
		return wsi18n.T(lang, "entry.exec.ended")
	case "stopped":
		if v.StoppedByUser {
			return wsi18n.T(lang, "entry.exec.stopped.user")
		}
		return wsi18n.T(lang, "entry.exec.stopped")
	case "failed":
		return wsi18n.T(lang, "entry.exec.failed")
	case "lost":
		return wsi18n.T(lang, "entry.exec.lost")
	case "running":
		return wsi18n.T(lang, "entry.exec.running")
	default:
		return wsi18n.T(lang, "entry.exec.ended")
	}
}

// OutputLabel returns the localized output heading for the expanded details.
func (v *ExecNoticeView) OutputLabel(lang string) string {
	if v == nil {
		return ""
	}
	if v.NoOutput {
		return wsi18n.T(lang, "entry.exec.no_output")
	}
	if v.OutputShown > 0 && v.OutputTotal > 0 {
		return wsi18n.T(lang, "entry.exec.output.tail", v.OutputShown, v.OutputTotal)
	}
	return wsi18n.T(lang, "entry.exec.output")
}
