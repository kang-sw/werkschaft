package execjob

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/textreader"
)

// The legacy surface over hosted jobs. A hosted exec.spawn or legacy-form
// exec.shell launch answers in the legacy Response shape with a legacy key,
// so an older client's exec.status, exec.result, exec.abort and exec.raw.*
// calls keep working on it; those readers find the job here first, scoped
// by the session root as the file-backed jobs are, and fall back to the
// file-backed store for every other key.

// LegacyResponse is v in the legacy reply shape. include inlines the
// output of a finished job within InlineBudget, as the file-backed
// Response does; a running or larger one carries the reader guidance.
func (h *Hosted) LegacyResponse(v HostedView, include bool) Response {
	r := Response{
		ExecKey:       v.ID,
		Status:        legacyStatus(v.HostedJobRow),
		PID:           v.PID,
		StartedAt:     msTimestamp(v.StartedAt),
		UpdatedAt:     ts(),
		CompletedAt:   msTimestamp(v.EndedAt),
		Error:         legacyError(v.HostedJobRow),
		ResultReady:   v.Terminal(),
		StdoutBytes:   v.Written[StreamStdout],
		StderrBytes:   v.Written[StreamStderr],
		CombinedBytes: v.Written[StreamCombined],
	}
	if v.ExitCode != nil {
		r.ExitCode = *v.ExitCode
	} else if v.Signal != nil {
		// What Go's ProcessState.ExitCode reports for a signal end.
		r.ExitCode = -1
	}
	if !include || !v.Terminal() || r.CombinedBytes > InlineBudget {
		if !v.Terminal() || include {
			r.Guidance = guidance()
		}
		return r
	}
	out := h.output(v.ID)
	if out == nil {
		return r
	}
	for _, s := range []Stream{StreamStdout, StreamStderr} {
		slice := out.Read(s, 0, InlineBudget)
		text, _ := DecodeText(slice.Data)
		if slice.Start > 0 {
			r.Error = appendError(r.Error, fmt.Sprintf("%s before byte %d left the output ring", s, slice.Start))
		}
		if s == StreamStdout {
			r.Stdout = text
		} else {
			r.Stderr = text
		}
	}
	return r
}

// legacyStatus maps a hosted state onto the legacy status words.
func legacyStatus(row HostedJobRow) string {
	switch row.State {
	case HostedRunning:
		return stateRunning
	case HostedExited:
		if row.ExitCode != nil && *row.ExitCode == 0 {
			return stateSucceeded
		}
		return stateFailed
	case HostedStopped:
		return stateCancelled
	}
	return stateFailed
}

// legacyError is the legacy record's error text for a hosted end.
func legacyError(row HostedJobRow) string {
	switch {
	case row.Error != "":
		return row.Error
	case row.State == HostedStopped:
		return "exec job cancelled"
	case row.State != HostedExited:
		return ""
	case row.ExitCode != nil && *row.ExitCode != 0:
		return "exit status " + strconv.Itoa(*row.ExitCode)
	case row.ExitCode == nil && row.Signal != nil:
		return "signal: " + strconv.Itoa(*row.Signal)
	}
	return ""
}

func msTimestamp(ms int64) string {
	if ms <= 0 {
		return ""
	}
	return time.UnixMilli(ms).UTC().Format(time.RFC3339Nano)
}

// legacyJob returns the hosted legacy-launch job key of root, nil for none.
// Only a legacy launch is found by root alone: a job of the new tools
// belongs to its owning conversation (owned), and another session of the
// same root must not read or abort it through a legacy reader.
func (h *Hosted) legacyJob(root, key string) *hostedJob {
	h.mu.Lock()
	defer h.mu.Unlock()
	j := h.jobs[strings.TrimSpace(key)]
	if j == nil || !j.row.Legacy || j.row.Root != filepath.Clean(root) {
		return nil
	}
	return j
}

// LegacyStatus is exec.status for a hosted job; ok is false for a key
// that is not one of root's hosted jobs.
func (h *Hosted) LegacyStatus(root, key string) (Response, bool) {
	j := h.legacyJob(root, key)
	if j == nil {
		return Response{}, false
	}
	return h.LegacyResponse(h.view(j, 0), false), true
}

// LegacyResult is exec.result for a hosted job: it waits up to timeout
// (0 for none) or ctx's end for the job to finish, then answers its state.
func (h *Hosted) LegacyResult(ctx context.Context, root, key string, timeout time.Duration) (Response, bool) {
	j := h.legacyJob(root, key)
	if j == nil {
		return Response{}, false
	}
	if timeout > 0 {
		timer := time.NewTimer(timeout)
		select {
		case <-j.done:
		case <-timer.C:
		case <-ctx.Done():
		}
		timer.Stop()
	}
	v := h.view(j, 0)
	return h.LegacyResponse(v, v.Terminal()), true
}

// LegacyAbort is exec.abort for a hosted job: it stops the job's process
// group and answers its state after.
func (h *Hosted) LegacyAbort(root, key string) (Response, bool) {
	j := h.legacyJob(root, key)
	if j == nil {
		return Response{}, false
	}
	h.stop(j)
	return h.LegacyResponse(h.view(j, 0), false), true
}

// LegacyTail is exec.raw.tail for a hosted job.
func (h *Hosted) LegacyTail(root, key, stream string, lines int) (RawTailResponse, bool, error) {
	j := h.legacyJob(root, key)
	if j == nil {
		return RawTailResponse{}, false, nil
	}
	s, name, err := ParseStream(stream)
	if err != nil {
		return RawTailResponse{}, true, err
	}
	text, _ := DecodeText([]byte(j.out.TailLines(s, lines)))
	return RawTailResponse{ExecKey: key, Stream: name, Text: text}, true, nil
}

// LegacyRead is exec.raw.read for a hosted job. Offsets are the stream's
// absolute byte offsets; a read below what the output ring still holds
// starts at the oldest retained byte and says where (EvictedBefore).
func (h *Hosted) LegacyRead(root, key, stream string, offset, limit int64) (RawReadResponse, bool, error) {
	j := h.legacyJob(root, key)
	if j == nil {
		return RawReadResponse{}, false, nil
	}
	s, name, err := ParseStream(stream)
	if err != nil {
		return RawReadResponse{}, true, err
	}
	if offset < 0 {
		offset = 0
	}
	if limit <= 0 {
		limit = textreader.DefaultReadLimit
	}
	if limit > textreader.MaxReadLimit {
		limit = textreader.MaxReadLimit
	}
	slice := j.out.Read(s, offset, limit)
	text, _ := DecodeText(slice.Data)
	rr := textreader.ReadResult{Text: text, Offset: slice.Start, NextOffset: slice.End, Limit: limit, Size: slice.Written, EOF: slice.End >= slice.Written}
	resp := RawReadResponse{ExecKey: key, Stream: name, ReadResult: rr}
	if offset < slice.Evicted {
		resp.EvictedBefore = slice.Evicted
	}
	return resp, true, nil
}

// LegacyGrep is exec.raw.grep for a hosted job, over what its output ring
// retains, with absolute line numbers.
func (h *Hosted) LegacyGrep(root, key, stream, pattern string, before, after, max int, regex bool) (RawGrepResponse, bool, error) {
	j := h.legacyJob(root, key)
	if j == nil {
		return RawGrepResponse{}, false, nil
	}
	s, name, err := ParseStream(stream)
	if err != nil {
		return RawGrepResponse{}, true, err
	}
	gr, err := j.out.Grep(s, pattern, before, after, max, regex)
	return RawGrepResponse{ExecKey: key, Stream: name, GrepResult: gr}, true, err
}
