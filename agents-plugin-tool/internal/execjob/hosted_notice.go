package execjob

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// deliver admits j's pending completion notice, once.
func (h *Hosted) deliver(j *hostedJob) {
	h.mu.Lock()
	if !h.started || h.closed || j.row.Notice != NoticePending || j.delivering || j.publishing {
		h.mu.Unlock()
		return
	}
	j.delivering = true
	row := h.rowLocked(j)
	h.mu.Unlock()
	text := noticeText(row, j.out)
	out, err := h.opts.Sessions.Notify(row.Owner, row.Gen, "exec:"+row.ID+":exit", text)
	h.mu.Lock()
	j.delivering = false
	switch {
	case err != nil || out == NoticeDeferred:
		if err != nil {
			h.logf("completion notice of %s: %v", row.ID, err)
		}
		h.scheduleRetryLocked()
		h.mu.Unlock()
		return
	case out == NoticeDropped:
		j.row.Notice = NoticeSuppressed
	default:
		j.row.Notice = NoticeDelivered
	}
	row, rev := h.snapLocked(j)
	h.mu.Unlock()
	h.save(j, row, rev)
}

// deliverPending tries every pending notice.
func (h *Hosted) deliverPending() {
	h.mu.Lock()
	var pending []*hostedJob
	for _, j := range h.jobs {
		if j.row.Notice == NoticePending {
			pending = append(pending, j)
		}
	}
	h.mu.Unlock()
	for _, j := range pending {
		h.deliver(j)
	}
}

func (h *Hosted) scheduleRetryLocked() {
	if h.closed || h.retry != nil {
		return
	}
	h.retry = time.AfterFunc(noticeRetryEvery, func() {
		h.mu.Lock()
		h.retry = nil
		h.mu.Unlock()
		h.deliverPending()
	})
}

// noticeText is a completion notice: what ended how, and its output tail.
func noticeText(row HostedJobRow, out *JobOutput) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[ws exec] %s %s", row.ID, endText(row))
	if row.StartedAt > 0 && row.EndedAt >= row.StartedAt {
		fmt.Fprintf(&b, " after %s", (time.Duration(row.EndedAt-row.StartedAt) * time.Millisecond).Round(100*time.Millisecond))
	}
	b.WriteString(".\n")
	if cmd := displayCommand(row); cmd != "" {
		fmt.Fprintf(&b, "command: %s\n", cmd)
	}
	writeOutputTail(&b, out)
	fmt.Fprintf(&b, "Read more with exec.tail (exec_id %s).", row.ID)
	return b.String()
}

// writeOutputTail writes out's last HostedPreviewBytes of combined output,
// or says there is none.
func writeOutputTail(b *strings.Builder, out *JobOutput) {
	tail := out.Tail(StreamCombined, HostedPreviewBytes)
	if len(tail.Data) > 0 {
		text, _ := DecodeText(tail.Data)
		if tail.Start > 0 {
			fmt.Fprintf(b, "output (last %d of %d bytes):\n", len(tail.Data), tail.Written)
		} else {
			b.WriteString("output:\n")
		}
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteString("\n")
		}
	} else {
		b.WriteString("no output.\n")
	}
}

// endText says how a finished job ended.
func endText(row HostedJobRow) string {
	switch row.State {
	case HostedRunning:
		return "is running"
	case HostedStopped:
		return "was stopped"
	case HostedFailed:
		return "failed to start: " + row.Error
	case HostedLost:
		return "was lost: " + row.Error
	}
	switch {
	case row.ExitCode != nil:
		return "exited with code " + strconv.Itoa(*row.ExitCode)
	case row.Signal != nil:
		return "was ended by signal " + strconv.Itoa(*row.Signal)
	}
	return "ended"
}

// displayCommand is the command as a person reads it.
func displayCommand(row HostedJobRow) string {
	if row.Command != "" {
		return row.Command
	}
	return strings.Join(row.Argv, " ")
}
