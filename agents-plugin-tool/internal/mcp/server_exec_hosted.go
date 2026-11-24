package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/mcpframe"
	"github.com/kang-sw/devenv/internal/wsfs"
)

// execToolNames are every exec tool callExec dispatches.
var execToolNames = []string{
	"exec.spawn", "exec.shell", "exec.status", "exec.result", "exec.abort", "exec.raw.tail", "exec.raw.read", "exec.raw.grep",
	"exec.run", "exec.stop", "exec.tail", "exec.list", "exec.write",
}

// errNoHostedExec refuses a hosted exec tool on a connection without the
// trusted binding of a hosted session, or on a daemon without hosted exec.
var errNoHostedExec = errors.New("this exec tool runs only in a session the dashboard hosts (its connection's own session token names the session that owns the job); here use exec.spawn or exec.shell with command")

// hostedExec reports whether this connection launches through the
// daemon's hosted exec jobs: the daemon runs them and the connection's
// trusted binding names the owning conversation. A supplied session_key
// never stands in for that binding; it only grants the root.
func (s *Server) hostedExec() bool {
	return s.execJobs() != nil && s.conversation != ""
}

// execJobs is the daemon's hosted exec jobs, nil when there are none.
func (s *Server) execJobs() *execjob.Hosted {
	if s.exec == nil {
		return nil
	}
	return s.exec()
}

// callExec dispatches every exec tool. The new tools and the new exec.shell
// form need the hosted exec jobs (hostedExec); the legacy launches go
// through them on a hosted connection and through the file-backed store
// elsewhere; the legacy readers find a hosted job by its key first.
func (s *Server) callExec(ctx context.Context, id json.RawMessage, name string, args, meta map[string]any) response {
	switch name {
	case "exec.run", "exec.stop", "exec.tail", "exec.list", "exec.write":
		return s.callHostedExec(ctx, id, name, args, meta)
	case "exec.shell":
		if family, _ := execShellFamily(args); !family.legacy {
			return s.callHostedExec(ctx, id, name, args, meta)
		}
	}
	return s.callLegacyExec(ctx, id, name, args, meta)
}

// callHostedExec runs the new exec tools.
func (s *Server) callHostedExec(ctx context.Context, id json.RawMessage, name string, args, meta map[string]any) response {
	if !s.hostedExec() {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, errNoHostedExec))
	}
	owner, jobs := s.conversation, s.execJobs()
	switch name {
	case "exec.list":
		views, more := jobs.List(owner)
		return toolTextResponse(id, formatHostedList(views, more), nil)
	case "exec.stop":
		execID, _ := args["exec_id"].(string)
		v, err := jobs.Stop(owner, execID)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		_, tail, _ := jobs.Tail(owner, execID, 0)
		return toolTextResponse(id, formatHostedView(v, tail, ""), nil)
	case "exec.tail":
		execID, _ := args["exec_id"].(string)
		v, tail, err := jobs.Tail(owner, execID, intFromArgument(args["bytes"], 0))
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		return toolTextResponse(id, formatHostedView(v, tail, ""), nil)
	case "exec.write":
		execID, _ := args["exec_id"].(string)
		data, _ := args["data"].(string)
		v, err := jobs.Write(owner, execID, []byte(data), boolArgument(args["eof"]))
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		written := fmt.Sprintf("wrote %d bytes", len(data))
		if boolArgument(args["eof"]) {
			written += " and closed stdin"
		}
		return toolTextResponse(id, fmt.Sprintf("exec_id: %s\nstatus: %s\n%s\n", v.ID, hostedStatus(v), written), nil)
	}

	// exec.run and the new exec.shell form launch.
	root, err := s.resolveToolRoot(args, meta)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	cwdArg, _ := args["cwd"].(string)
	overlay := stringMapArgument(args["env"])
	readOnly := s.readOnlyAuthority()
	var req execjob.HostedLaunch
	if name == "exec.run" {
		cwd, err := execjob.ResolveWorkingDir(root, cwdArg)
		if err != nil {
			return toolTextResponse(id, "", err)
		}
		exe, _ := args["exe"].(string)
		if strings.TrimSpace(exe) == "" {
			return toolTextResponse(id, "", errors.New("exec.run: exe is required"))
		}
		argv := append([]string{exe}, stringList(args["args"])...)
		if readOnly {
			// A read-only worker's launch is checked and rebuilt, never
			// passed through (execjob.ReadOnlyArgvLaunch).
			req, err = execjob.ReadOnlyArgvLaunch(owner, name, root, cwd, argv, s.environ(), overlay)
			var refusal *execjob.ErrReadOnlyLaunch
			if errors.As(err, &refusal) {
				err = fmt.Errorf("%s: %w", name, err)
			}
			if err != nil {
				return toolTextResponse(id, "", err)
			}
		} else {
			req = execjob.HostedLaunch{Owner: owner, Tool: name, Root: root, Cwd: cwd, Environ: execjob.OverlayEnv(s.environ(), overlay), Env: overlay, Argv: argv}
		}
		req.Display = displayArgv(argv)
	} else {
		// The dashboard composer prepares its launch the same way.
		line, _ := args["cmd"].(string)
		if readOnly {
			req, err = execjob.ReadOnlyShellLaunch(owner, name, root, cwdArg, line, s.environ(), overlay)
			var refusal *execjob.ErrReadOnlyLaunch
			if errors.As(err, &refusal) {
				err = fmt.Errorf("%s: %w", name, err)
			}
		} else {
			req, err = execjob.ShellLaunch(owner, name, root, cwdArg, line, s.environ(), overlay)
		}
		if err != nil {
			return toolTextResponse(id, "", err)
		}
	}
	req.Async = boolArgument(args["async"])
	if stdin, ok := args["stdin"].(string); ok {
		req.Stdin, req.StdinSet = []byte(stdin), true
	}
	if err := s.bindExecLaunch(ctx, &req, args, meta); err != nil {
		return toolTextResponse(id, "", err)
	}
	v, err := jobs.Launch(ctx, s.drainContext().Done(), req)
	if err != nil {
		return toolTextResponse(id, "", fmt.Errorf("%s: %w", name, err))
	}
	_, tail, _ := jobs.Tail(owner, v.ID, 0)
	return toolTextResponse(id, formatHostedView(v, tail, launchHeader(v)), nil)
}

// launchHeader is a launch reply's first line.
func launchHeader(v execjob.HostedView) string {
	switch v.Outcome {
	case execjob.OutcomeAsync:
		return fmt.Sprintf("started async, exec id %s; its completion arrives as a message.", v.ID)
	case execjob.OutcomeConverted:
		return fmt.Sprintf("converted to async, exec id %s; it keeps running and its completion arrives as a message.", v.ID)
	}
	return ""
}

// callLegacyExec runs the eight legacy exec tools with their original
// arguments and replies.
func (s *Server) callLegacyExec(ctx context.Context, id json.RawMessage, name string, args, meta map[string]any) response {
	root, err := s.resolveToolRoot(args, meta)
	if err != nil {
		return toolTextResponse(id, "", err)
	}
	jobs := s.execJobs()
	key, _ := args["exec_key"].(string)
	stream, _ := args["stream"].(string)
	switch name {
	case "exec.spawn", "exec.shell":
		if s.hostedExec() {
			return s.hostedLegacyLaunch(ctx, id, name, root, args, meta)
		}
		if s.recordExecLaunch != nil {
			return toolTextResponse(id, "", errNoHostedExec)
		}
		workingDir, _ := args["working_dir"].(string)
		stdin, _ := args["stdin"].(string)
		opts := execjob.LaunchOptions{Root: root, WorkingDir: workingDir, Env: stringMapArgument(args["env"]), Stdin: stdin, Environ: s.environ()}
		if name == "exec.spawn" {
			opts.Cmd, _ = args["cmd"].(string)
			opts.Args = stringList(args["args"])
		} else {
			opts.Command, _ = args["command"].(string)
			opts.Shell, _ = args["shell"].(string)
			opts.ShellMode = true
		}
		result, err := s.execStore().Launch(opts)
		return execResponse(id, result, err, true)
	case "exec.status":
		if jobs != nil {
			if r, ok := jobs.LegacyStatus(root, key); ok {
				return execResponse(id, r, nil, false)
			}
		}
		result, err := s.execStore().Status(root, key)
		return execResponse(id, result, err, false)
	case "exec.result":
		timeout := durationFromSeconds(args["timeout_seconds"])
		if jobs != nil {
			wait, cancel := mergeDone(ctx, s.drainContext())
			r, ok := jobs.LegacyResult(wait, root, key, timeout)
			cancel()
			if ok {
				return execResponse(id, r, nil, true)
			}
		}
		result, err := s.execStore().ResultWithContext(s.drainContext(), root, key, timeout)
		return execResponse(id, result, err, true)
	case "exec.abort":
		if jobs != nil {
			if r, ok := jobs.LegacyAbort(root, key); ok {
				return execResponse(id, r, nil, false)
			}
		}
		result, err := s.execStore().Abort(root, key)
		return execResponse(id, result, err, false)
	case "exec.raw.tail":
		if jobs != nil {
			if r, ok, err := jobs.LegacyTail(root, key, stream, intFromArgument(args["lines"], 0)); ok {
				return execRawTailResponse(id, r, err)
			}
		}
		result, err := s.execStore().Tail(root, key, stream, intFromArgument(args["lines"], 0))
		return execRawTailResponse(id, result, err)
	case "exec.raw.read":
		offset, limit := int64FromArgument(args["offset"], 0), int64FromArgument(args["limit"], 0)
		if jobs != nil {
			if r, ok, err := jobs.LegacyRead(root, key, stream, offset, limit); ok {
				return execRawReadResponse(id, r, err)
			}
		}
		result, err := s.execStore().Read(root, key, stream, offset, limit)
		return execRawReadResponse(id, result, err)
	case "exec.raw.grep":
		pattern, _ := args["pattern"].(string)
		before, after, maxMatches := intFromArgument(args["before"], 0), intFromArgument(args["after"], 0), intFromArgument(args["max_matches"], 0)
		regex := boolArgument(args["regex"])
		if jobs != nil {
			if r, ok, err := jobs.LegacyGrep(root, key, stream, pattern, before, after, maxMatches, regex); ok {
				return execRawGrepResponse(id, r, err)
			}
		}
		result, err := s.execStore().Grep(root, key, stream, pattern, before, after, maxMatches, regex)
		return execRawGrepResponse(id, result, err)
	}
	return toolTextResponse(id, "", fmt.Errorf("unknown exec tool %s", name))
}

// hostedLegacyLaunch runs a legacy exec.spawn or exec.shell launch as a
// hosted job: the daemon's approval and execution window, the legacy key
// and reply, and a finite stdin (closed after the given text, or at once).
func (s *Server) hostedLegacyLaunch(ctx context.Context, id json.RawMessage, name, root string, args, meta map[string]any) response {
	jobs := s.execJobs()
	workingDir, _ := args["working_dir"].(string)
	cwd, err := execjob.ResolveWorkingDir(root, workingDir)
	if err != nil {
		return execResponse(id, execjob.Response{}, err, true)
	}
	overlay := stringMapArgument(args["env"])
	req := execjob.HostedLaunch{Owner: s.conversation, Tool: name, Root: root, Cwd: cwd, Environ: execjob.OverlayEnv(s.environ(), overlay), Env: overlay, Legacy: true}
	if stdin, _ := args["stdin"].(string); stdin != "" {
		req.Stdin, req.StdinSet = []byte(stdin), true
	}
	if name == "exec.spawn" {
		cmd, _ := args["cmd"].(string)
		if strings.TrimSpace(cmd) == "" {
			return execResponse(id, execjob.Response{}, errors.New("cmd is required"), true)
		}
		req.Argv = append([]string{cmd}, stringList(args["args"])...)
		req.Display = displayArgv(req.Argv)
	} else {
		command, _ := args["command"].(string)
		if command == "" {
			return execResponse(id, execjob.Response{}, errors.New("command is required"), true)
		}
		shell, _ := args["shell"].(string)
		req.Argv = execjob.LegacyShellArgv(shell, command)
		req.Display, req.Command, req.Shell = command, command, req.Argv[0]
	}
	if err := s.bindExecLaunch(ctx, &req, args, meta); err != nil {
		return execResponse(id, execjob.Response{}, err, true)
	}
	v, err := jobs.Launch(ctx, s.drainContext().Done(), req)
	if err != nil && v.ID == "" {
		return execResponse(id, execjob.Response{}, err, true)
	}
	if err != nil && v.State != execjob.HostedFailed {
		return execResponse(id, execjob.Response{}, err, true)
	}
	// A start failure answers as the legacy launch did: a failed record.
	return execResponse(id, jobs.LegacyResponse(v, v.Outcome == execjob.OutcomeDone), nil, true)
}

// bindExecLaunch takes call identity only from the authenticated model
// binding. The launch cannot proceed if it lacks a matching pending call.
func (s *Server) bindExecLaunch(ctx context.Context, req *execjob.HostedLaunch, args, meta map[string]any) error {
	if s.recordExecLaunch == nil {
		return nil
	}
	callID, _ := meta[mcpframe.ToolCallIDMeta].(string)
	if callID == "" {
		return errors.New("the exec launch requires a model call identity")
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	tool := req.Tool
	if s.fileWrites != nil && s.fileWrites.LiveAuthority {
		req.ApproveModel = func(ctx context.Context, preview execjob.HostedCall, requireExplicit bool) (bool, error) {
			return s.recordExecLaunch(ctx, execjob.ModelLaunch{ID: callID, Tool: tool, Arguments: raw, Approval: &preview, RequireExplicit: requireExplicit})
		}
	}
	if s.fileWrites != nil {
		req.ModelAuthority = func() (bool, error) {
			level := s.fileWrites.AuthorityForCall()
			if level != wsfs.AuthorityFull && level != wsfs.AuthorityWorkdirWrite {
				return false, errors.New("the session cannot launch exec jobs with its current authority")
			}
			return !s.fileWrites.LiveAuthority || level == wsfs.AuthorityFull, nil
		}
	}
	req.BindJob = func(jobID string) error {
		_, err := s.recordExecLaunch(ctx, execjob.ModelLaunch{ID: callID, Tool: tool, Arguments: raw, JobID: jobID})
		return err
	}
	return nil
}

// mergeDone returns a context that ends when either a or b does.
func mergeDone(a, b context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(a)
	stop := context.AfterFunc(b, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

// displayArgv is argv as one line a person reads, quoting arguments that
// carry spaces or quotes.
func displayArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		if a == "" || strings.ContainsAny(a, " \t\n\"'\\") {
			parts[i] = strconv.Quote(a)
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

// hostedStatus is a job's state as one phrase.
func hostedStatus(v execjob.HostedView) string {
	switch v.State {
	case execjob.HostedRunning:
		return "running"
	case execjob.HostedExited:
		switch {
		case v.ExitCode != nil:
			return "exited " + strconv.Itoa(*v.ExitCode)
		case v.Signal != nil:
			return "killed by signal " + strconv.Itoa(*v.Signal)
		}
		return "exited"
	case execjob.HostedStopped:
		return "stopped"
	case execjob.HostedFailed:
		return "failed to start"
	case execjob.HostedLost:
		return "lost"
	}
	return v.State
}

// formatHostedView is one job's reply: an optional header line, its
// fields, and its output tail with the offsets it covers.
func formatHostedView(v execjob.HostedView, tail execjob.Slice, header string) string {
	var b strings.Builder
	if header != "" {
		b.WriteString(header + "\n")
	}
	fmt.Fprintf(&b, "exec_id: %s\n", v.ID)
	fmt.Fprintf(&b, "status: %s\n", hostedStatus(v))
	if v.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", v.Error)
	}
	if cmd := v.Command; cmd != "" {
		fmt.Fprintf(&b, "command: %s\n", cmd)
	} else if len(v.Argv) > 0 {
		fmt.Fprintf(&b, "command: %s\n", displayArgv(v.Argv))
	}
	fmt.Fprintf(&b, "cwd: %s\n", v.Cwd)
	if v.PID != 0 {
		fmt.Fprintf(&b, "pid: %d\n", v.PID)
	}
	if v.StartedAt > 0 {
		end := time.Now()
		if v.EndedAt > 0 {
			end = time.UnixMilli(v.EndedAt)
		}
		fmt.Fprintf(&b, "elapsed: %s\n", end.Sub(time.UnixMilli(v.StartedAt)).Round(100*time.Millisecond))
	}
	written := v.Written[execjob.StreamCombined]
	fmt.Fprintf(&b, "output_bytes: %d (stdout %d, stderr %d)\n", written, v.Written[execjob.StreamStdout], v.Written[execjob.StreamStderr])
	if tail.Evicted > 0 {
		fmt.Fprintf(&b, "evicted_before: %d\n", tail.Evicted)
	}
	if len(tail.Data) == 0 {
		if written == 0 {
			b.WriteString("no output yet.\n")
		}
		return b.String()
	}
	text, replaced := execjob.DecodeText(tail.Data)
	fmt.Fprintf(&b, "========== output [%d, %d) ==========\n", tail.Start, tail.End)
	b.WriteString(text)
	if !strings.HasSuffix(text, "\n") {
		b.WriteString("\n")
	}
	if replaced {
		b.WriteString("(invalid UTF-8 bytes shown as U+FFFD; the raw bytes are kept)\n")
	}
	return b.String()
}

// formatHostedList is exec.list's reply.
func formatHostedList(views []execjob.HostedView, more int) string {
	if len(views) == 0 {
		return "no exec jobs.\n"
	}
	var b strings.Builder
	for _, v := range views {
		cmd := v.Command
		if cmd == "" {
			cmd = displayArgv(v.Argv)
		}
		if len([]rune(cmd)) > 80 {
			cmd = string([]rune(cmd)[:79]) + "…"
		}
		fmt.Fprintf(&b, "%s  %s  %d bytes  %s\n", v.ID, hostedStatus(v), v.Written[execjob.StreamCombined], cmd)
	}
	if more > 0 {
		fmt.Fprintf(&b, "(%d older jobs not shown)\n", more)
	}
	return b.String()
}
