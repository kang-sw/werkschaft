package wsagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// agyBypass is deliberately restricted to hosted agy leads: neither stray
// metadata on another backend nor a worker changes its approval policy.
// Bypass is stored with native default (selectPermissionMode) and this build
// keeps that mode while it holds (a native report does not move it). Any other
// mode means a writer that does not know the flag (an older daemon keeps it as
// an unknown member) changed the mode since: the stale flag prompts instead.
func (rec Record) agyBypass() bool {
	return rec.Lead && rec.Backend == BackendAgy && rec.AgyBypass && rec.PermissionMode == "default"
}

// agyCutoverAutoApprove restores non-exec approval convenience for cutover
// agy leads forced to stay in native "default" mode so run_command can be
// refused:
//   - saved mode "auto" auto-approves non-exec ACP requests (ws/exec still
//     prompts through ApproveExec because execImmediate is false for "auto");
//   - saved mode "accept-edits" auto-approves ACP kind=="edit" requests only
//     when every target path resolves inside rec.Root.
func agyCutoverAutoApprove(rec Record, req ApprovalRequest) bool {
	if !rec.Lead || req.Tool == agyCommandTool {
		return false
	}
	switch rec.selectedPermissionMode() {
	case "auto", "bypass":
		return true
	case "accept-edits":
		return req.Kind == "edit" && agyEditInputWithinRoot(rec.Root, rec.workDir(), req.Input)
	default:
		return false
	}
}

// agyEditInputWithinRoot reports whether req.Input names at least one target
// path and every target path resolves within root. It fails closed on empty
// roots, malformed payloads, missing path fields, or symlink escapes.
func agyEditInputWithinRoot(root, cwd string, raw json.RawMessage) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	paths, ok := agyExtractEditPaths(raw)
	if !ok || len(paths) == 0 {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	resolvedRoot, err := resolvePathWithMissingLeaf(absRoot)
	if err != nil {
		return false
	}
	base := cwd
	if strings.TrimSpace(base) == "" {
		base = root
	}
	for _, p := range paths {
		target := p
		if !filepath.IsAbs(target) {
			target = filepath.Join(base, target)
		}
		absTarget, err := filepath.Abs(target)
		if err != nil {
			return false
		}
		resolvedTarget, err := resolvePathWithMissingLeaf(absTarget)
		if err != nil {
			return false
		}
		if !pathWithinRootOS(resolvedRoot, resolvedTarget, runtime.GOOS == "windows") {
			return false
		}
	}
	return true
}

func agyExtractEditPaths(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil || len(obj) == 0 {
		return nil, false
	}
	var out []string
	singleKeys := map[string]bool{
		"path":          true,
		"target_file":   true,
		"targetfile":    true,
		"file_path":     true,
		"filepath":      true,
		"absolute_path": true,
		"absolutepath":  true,
		"file":          true,
		"target_path":   true,
		"targetpath":    true,
		"destination":   true,
		"old_path":      true,
		"oldpath":       true,
		"new_path":      true,
		"newpath":       true,
	}
	listKeys := map[string]bool{
		"paths":     true,
		"files":     true,
		"locations": true,
	}
	for k, v := range obj {
		lk := strings.ToLower(strings.TrimSpace(k))
		if singleKeys[lk] {
			s, ok := v.(string)
			s = strings.TrimSpace(s)
			if !ok || s == "" {
				return nil, false
			}
			out = append(out, s)
			continue
		}
		if listKeys[lk] {
			items, ok := v.([]any)
			if !ok || len(items) == 0 {
				return nil, false
			}
			for _, item := range items {
				switch x := item.(type) {
				case string:
					s := strings.TrimSpace(x)
					if s == "" {
						return nil, false
					}
					out = append(out, s)
				case map[string]any:
					sub, ok := agyExtractPathFromObject(x, singleKeys)
					if !ok {
						return nil, false
					}
					out = append(out, sub...)
				default:
					return nil, false
				}
			}
		}
	}
	return out, len(out) > 0
}

func agyExtractPathFromObject(obj map[string]any, singleKeys map[string]bool) ([]string, bool) {
	var out []string
	for k, v := range obj {
		if !singleKeys[strings.ToLower(strings.TrimSpace(k))] {
			continue
		}
		s, ok := v.(string)
		s = strings.TrimSpace(s)
		if !ok || s == "" {
			return nil, false
		}
		out = append(out, s)
	}
	return out, len(out) > 0
}

// resolvePathWithMissingLeaf resolves symlinks on the longest existing ancestor
// of p and rejoins any trailing non-existent segments cleanly.
func resolvePathWithMissingLeaf(p string) (string, error) {
	cur := filepath.Clean(p)
	var tail []string
	for {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			for i := len(tail) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, tail[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		// EvalSymlinks also reports ENOENT for a link with a missing target.
		// Only peel a name that is itself absent.
		if _, statErr := os.Lstat(cur); statErr == nil {
			return "", err
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", err
		}
		tail = append(tail, filepath.Base(cur))
		cur = parent
	}
}

// pathWithinRootOS reports whether clean absolute target lies inside clean
// absolute root. When windowsCase is true, drive volumes and path components
// are compared case-insensitively and backslashes are normalized.
func pathWithinRootOS(root, target string, windowsCase bool) bool {
	if windowsCase {
		rNorm := strings.ReplaceAll(root, "/", `\`)
		tNorm := strings.ReplaceAll(target, "/", `\`)
		rVol, rRest := splitWindowsVolume(rNorm)
		tVol, tRest := splitWindowsVolume(tNorm)
		if !strings.EqualFold(rVol, tVol) {
			return false
		}
		rParts := cleanPathSegments(rRest, `\`)
		tParts := cleanPathSegments(tRest, `\`)
		if len(tParts) < len(rParts) {
			return false
		}
		for i := range rParts {
			if !strings.EqualFold(rParts[i], tParts[i]) {
				return false
			}
		}
		return true
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func splitWindowsVolume(p string) (vol, rest string) {
	if len(p) >= 2 && ((p[0] >= 'a' && p[0] <= 'z') || (p[0] >= 'A' && p[0] <= 'Z')) && p[1] == ':' {
		return p[:2], p[2:]
	}
	if strings.HasPrefix(p, `\\`) {
		trimmed := strings.TrimPrefix(p, `\\`)
		parts := strings.SplitN(trimmed, `\`, 3)
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			vol = `\\` + parts[0] + `\` + parts[1]
			if len(parts) == 3 {
				rest = `\` + parts[2]
			}
			return vol, rest
		}
	}
	return "", p
}

func cleanPathSegments(p, sep string) []string {
	raw := strings.Split(p, sep)
	var out []string
	for _, seg := range raw {
		switch seg {
		case "", ".":
		case "..":
			if len(out) > 0 {
				out = out[:len(out)-1]
			}
		default:
			out = append(out, seg)
		}
	}
	return out
}

// allowAgyBypassPending serves bypass leads, cutover auto/accept-edits leads,
// and auto-approving workers, including an old daemon's unanswered prompts
// replayed by a new daemon. addApproval already discarded ids durably answered
// before. A rejoin answers them at its end (replayDone), not while it replays.
// A prompt the process could not answer stays for the user; a failed write
// stopped the process, so nothing is left to answer.
func (c *conv) allowAgyBypassPending() {
	rec := c.record()
	if c.replaying {
		return
	}
	// A hidden companion session's prompts are the daemon's to settle, by the
	// companion policy and never by the user's mode.
	if rec.CompanionRole != "" {
		c.settleCompanionPending()
		return
	}
	bypass := rec.agyBypass()
	workerAuto := !rec.Lead && c.r.opts.Backends[rec.Backend].WorkerAutoApprove
	cutoverAuto := c.cutoverSupport() != nil && c.cutoverSupport().AutoApprove != nil
	if !(bypass || workerAuto || cutoverAuto) {
		return
	}
	answerer := controlsFor(c.proc).approval
	if answerer == nil {
		return
	}
	for _, approval := range slices.Clone(c.approvals) {
		if _, daemon := c.execAnswers[approval.ID]; daemon {
			// The process never asked: recheckExecApprovals settles it.
			continue
		}
		if c.cutoverRejects(approval.Tool) {
			// rejectCutoverPending refuses it; bypass never allows it.
			continue
		}
		if !(bypass || workerAuto || c.cutoverAutoApproves(rec, approval)) {
			continue
		}
		err := c.sendApproval(answerer, approval, true, "")
		if err == nil {
			c.dropApproval(approval.ID, false)
			continue
		}
		fmt.Fprintf(c.r.opts.Log, "agent %s: auto-allow permission: %v\n", rec.ReplyID, err)
		if !errors.Is(err, errNoApprovalAnswer) {
			return
		}
	}
}

// selectedPermissionMode projects ws intent to the existing settings UI while
// keeping the persisted/native mode readable by older daemons.
func (rec Record) selectedPermissionMode() string {
	if rec.agyBypass() {
		return "bypass"
	}
	return rec.PermissionMode
}

func (rec Record) launchPermissionMode() string {
	if rec.agyBypass() {
		return "default"
	}
	return rec.PermissionMode
}

func (rec *Record) selectPermissionMode(mode string) {
	rec.PermissionMode = mode
	if rec.Lead && rec.Backend == BackendAgy {
		rec.AgyBypass = mode == "bypass"
		if rec.AgyBypass {
			rec.PermissionMode = "default"
		}
	}
}
