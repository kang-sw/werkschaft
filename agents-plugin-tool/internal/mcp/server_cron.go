package mcp

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wscron"
)

// Distinct refusals of the cron tool; each is raised before anything is
// written.
var (
	errCronNotHosted   = errors.New("cron schedules prompts for a session the dashboard hosts, and this connection is not one (its own session token names the session a schedule delivers to; a supplied session_key never does)")
	errCronUnavailable = errors.New("cron is unavailable: the daemon could not open its schedule store")
	errCronLeadOnly    = errors.New("cron is available to a hosted lead session only; a worker cannot receive scheduled prompts")
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
	if lead, err := sched.IsLead(owner); err != nil || !lead {
		return toolTextResponse(id, "", fmt.Errorf("cron: %w", errCronLeadOnly))
	}
	asJSON := wantsJSON(args)
	switch {
	case hasAdd:
		spec, ok := args["add"].(map[string]any)
		if !ok {
			return toolTextResponse(id, "", errors.New("cron: add must be an object {name, expr, prompt, tz?}"))
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
		var fields [4]string
		for i, key := range []string{"name", "expr", "prompt", "tz"} {
			text, err := field(key, key != "tz")
			if err != nil {
				return toolTextResponse(id, "", err)
			}
			fields[i] = text
		}
		for key := range spec {
			switch key {
			case "name", "expr", "prompt", "tz":
			default:
				return toolTextResponse(id, "", fmt.Errorf("cron: unknown add field %q (use name, expr, prompt, tz)", key))
			}
		}
		schedID, nextAt, err := sched.Add(owner, fields[0], fields[1], fields[2], fields[3])
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
			out = append(out, map[string]any{
				"id": i.ID, "name": i.Name, "expr": i.Expr, "tz": i.TZ, "next_at": i.NextAt,
				"last_at": last, "pending": i.Pending, "dropped": i.Dropped,
			})
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
		fmt.Fprintf(&b, "\n%s  %s  %q  tz=%s  next=%s  last=%s  pending=%s  dropped=%d", i.ID, i.Name, i.Expr, i.TZ, next, last, pending, i.Dropped)
	}
	return b.String()
}
