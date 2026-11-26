package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wscron"
)

// Distinct refusals of the cron tool; each is raised before anything is
// written.
var (
	errCronNotHosted   = errors.New("cron schedules prompts for a session the dashboard hosts, and this connection is not one (its own session token names the session a schedule delivers to; a supplied session_key never does)")
	errCronUnavailable = errors.New("cron is unavailable: the daemon could not open its schedule store")
	errCronLeadOnly    = errors.New("cron is available to a hosted lead session only; a worker cannot receive scheduled prompts")
	errCronEnded       = errors.New("cron cannot reach this session: it is closed, handed off or no longer known")
)

// cronScheduler is the daemon's scheduler, nil when there is none.
func (s *Server) cronScheduler() *wscron.Scheduler {
	if s.cron == nil {
		return nil
	}
	return s.cron()
}

// handleCron serves the cron tool: no argument lists the caller's schedules,
// add registers one, delete removes one. The target is always the
// connection's own hosted conversation (Server.conversation, the trusted
// binding); the optional session_key is declared but never selects the owner.
func (s *Server) handleCron(id json.RawMessage, args map[string]any) response {
	_, hasAdd := args["add"]
	_, hasDelete := args["delete"]
	if hasAdd && hasDelete {
		return toolTextResponse(id, "", errors.New("cron: pass add or delete, not both (no argument lists your schedules)"))
	}
	if s.conversation == "" {
		return toolTextResponse(id, "", fmt.Errorf("cron: %w", errCronNotHosted))
	}
	sched := s.cronScheduler()
	if sched == nil {
		return toolTextResponse(id, "", fmt.Errorf("cron: %w", errCronUnavailable))
	}
	owner := s.conversation
	// A live non-lead is a worker; a session the runner can no longer deliver to
	// is refused with the runner's own reason, not misnamed a worker.
	lead, err := sched.IsLead(owner)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("cron: %w (%v)", errCronEnded, err))
	}
	if !lead {
		return toolTextResponse(id, "", fmt.Errorf("cron: %w", errCronLeadOnly))
	}
	asJSON := wantsJSON(args)
	switch {
	case hasAdd:
		spec, ok := args["add"].(map[string]any)
		if !ok {
			return toolTextResponse(id, "", errors.New("cron: add must be an object {name, expr | at, prompt, tz?}"))
		}
		for key := range spec {
			switch key {
			case "name", "expr", "at", "prompt", "tz":
			default:
				return toolTextResponse(id, "", fmt.Errorf("cron: unknown add field %q (use name, expr or at, prompt, tz)", key))
			}
		}
		field := func(key string, required bool) (string, error) {
			raw, present := spec[key]
			if !present || raw == nil {
				if required {
					return "", fmt.Errorf("cron: add.%s is required", key)
				}
				return "", nil
			}
			text, ok := raw.(string)
			if !ok {
				return "", fmt.Errorf("cron: add.%s must be a string", key)
			}
			return text, nil
		}
		var fields [5]string
		for i, key := range []string{"name", "expr", "at", "prompt", "tz"} {
			text, err := field(key, key == "name" || key == "prompt")
			if err != nil {
				return toolTextResponse(id, "", err)
			}
			fields[i] = text
		}
		name, expr, at, prompt, tz := fields[0], fields[1], fields[2], fields[3], fields[4]
		if (expr != "") == (at != "") {
			return toolTextResponse(id, "", errors.New("cron: add takes exactly one of expr (recurring) or at (one-shot RFC 3339 time)"))
		}
		var schedID, nextAt string
		var err error
		if at != "" {
			if tz != "" {
				return toolTextResponse(id, "", errors.New("cron: add.tz applies to expr only; give at's offset in the timestamp itself"))
			}
			when, perr := time.Parse(time.RFC3339, strings.TrimSpace(at))
			if perr != nil {
				return toolTextResponse(id, "", fmt.Errorf("cron: add.at must be an RFC 3339 time such as 2026-01-02T15:04:05+09:00: %v", perr))
			}
			schedID, nextAt, err = sched.AddAt(owner, name, prompt, when)
		} else {
			schedID, nextAt, err = sched.Add(owner, name, expr, prompt, tz)
		}
		if err != nil {
			return toolTextResponse(id, "", fmt.Errorf("cron add: %w", err))
		}
		if asJSON {
			return toolJSONResponse(id, map[string]any{"id": schedID, "next_at": nextAt}, nil)
		}
		return toolTextResponse(id, fmt.Sprintf("id: %s\nnext_at: %s", schedID, nextAt), nil)
	case hasDelete:
		key, ok := args["delete"].(string)
		if !ok || strings.TrimSpace(key) == "" {
			return toolTextResponse(id, "", errors.New("cron: delete must be a schedule id or name"))
		}
		if err := sched.Delete(owner, key); err != nil {
			return toolTextResponse(id, "", fmt.Errorf("cron delete: %w", err))
		}
		if asJSON {
			return toolJSONResponse(id, map[string]any{"ok": true}, nil)
		}
		return toolTextResponse(id, "ok", nil)
	}
	list := sched.List(owner)
	if asJSON {
		out := make([]map[string]any, 0, len(list))
		for _, i := range list {
			var last any
			if i.LastAt != "" {
				last = i.LastAt
			}
			entry := map[string]any{
				"id": i.ID, "name": i.Name, "expr": i.Expr, "tz": i.TZ, "next_at": i.NextAt,
				"last_at": last, "pending": i.Pending, "dropped": i.Dropped,
			}
			if i.At != "" {
				entry["at"] = i.At
			}
			out = append(out, entry)
		}
		return toolJSONResponse(id, out, nil)
	}
	return toolTextResponse(id, formatCronList(list), nil)
}

func formatCronList(list []wscron.Info) string {
	if len(list) == 0 {
		return "no schedules"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d schedule", len(list))
	if len(list) != 1 {
		b.WriteString("s")
	}
	for _, i := range list {
		last := i.LastAt
		if last == "" {
			last = "-"
		}
		next := i.NextAt
		if next == "" {
			next = "-"
		}
		pending := "no"
		if i.Pending {
			pending = "yes"
		}
		when := fmt.Sprintf("%q", i.Expr)
		if i.Expr == "" && i.At != "" {
			when = "at=" + i.At
		}
		fmt.Fprintf(&b, "\n%s  %s  %s  tz=%s  next=%s  last=%s  pending=%s  dropped=%d", i.ID, i.Name, when, i.TZ, next, last, pending, i.Dropped)
	}
	return b.String()
}
