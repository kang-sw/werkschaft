package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/kang-sw/devenv/internal/execjob"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wscron"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsnote"
)

// hostedCallLinger bounds how long the daemon process stays, after it has
// released its lock and endpoint, for a hosted call that outlived the
// drain budget (wsdashboard.Daemon.WaitProxy): long enough for a git
// merge or a ticket close, short enough that a wedged call never leaves a
// daemon process behind for good.
const hostedCallLinger = 2 * time.Minute

// proxy.go hosts the MCP sessions the dashboard daemon serves for the
// launchers that connect to its MCP route (internal/wsdashboard/proxy.go).
// Each accepted hello becomes one mcp.Server built from the launcher's
// process context — its environment, project root, and PID — never from
// the daemon's own, so two sessions on one daemon are as separate as two
// ws-mcp processes were. A CLI-mode hello runs the shared CLI core in the
// daemon and answers with its output and exit code.

// proxyHost is the daemon's ProxyHandler.
type proxyHost struct {
	dir         string
	port        int
	lan         bool
	buildTime   int64
	contentHash string
	logw        io.Writer
	// configPath is the global config file this daemon's own environment
	// resolves to; a session anchored at another one (its WS_CONFIG_HOME)
	// reads a dashboard.lan_enabled that is not this daemon's to follow.
	configPath string
	// replacing is set once a connection's global dashboard.lan_enabled
	// differs from this daemon's mode and the replacement began; background
	// counts the replacement goroutine, which the daemon process waits for
	// before it exits (it spawns the successor).
	replacing  atomic.Bool
	background sync.WaitGroup
	// live is the daemon, bound once StartDaemon returned (the host is built
	// before it): each hosted server hands its live snapshots to it in
	// memory (mcp.ServerOptions.Live). A connection accepted before the
	// binding writes its snapshot files as a ws-mcp of its own would.
	live atomic.Pointer[wsdashboard.Daemon]
	// agents is the daemon's agent runner, where a hosted session's pi.*
	// tools spawn its Pi subagents (mcp.ServerOptions.Agents); nil leaves
	// them refusing.
	agents mcp.AgentHost
	// ephemeralPool is the daemon's shared lightweight ephemeral harness pool.
	ephemeralPool *wsagent.EphemeralPool
	// exec is the daemon's hosted exec jobs, bound after StartDaemon; nil
	// keeps every connection on the legacy exec path.
	exec atomic.Pointer[execjob.Hosted]
	// cron is the daemon's scheduler, bound after StartDaemon; nil leaves
	// the cron tool refusing as unavailable.
	cron atomic.Pointer[wscron.Scheduler]
	// userCmds is the daemon's user commands, bound after the runner starts;
	// nil leaves the tools unlisted and the dashboard's routes unavailable.
	userCmds atomic.Pointer[userCommands]
	// userCmdsReady, when set, is closed once the daemon has settled its user
	// commands (bound, or unavailable): a hosted lead that connects while the
	// daemon is still starting waits for it (bounded), so its first tool list
	// already names the user command tools. nil never waits.
	userCmdsReady <-chan struct{}
	boards        *mcp.WhiteboardStores
	notes         *wsnote.Cache
	// mailLabels resolves mail sender labels for every hosted server
	// (mcp.ServerOptions.MailLabels). The servers share the process's group
	// store, so a recipient on one connection expands a group mail another
	// connection sent.
	mailLabels mcp.MailLabelFunc
	// credentials is the daemon's provider credential store, which a hosted
	// `credential` command applies through; nil refuses the command.
	credentials *wscred.Store
}

// userCommandsReadyWait bounds how long a hosted lead's connection waits for
// the daemon's user commands to settle at its start.
var userCommandsReadyWait = 5 * time.Second

// userCommandsFor returns the user command lookup of one connection. A hosted
// lead that connects while the daemon is still starting waits (bounded, and only
// inside its own lookup: the first tool list of that one session) until the
// daemon settled its user commands, so that list already names the tools. The
// wait is never in Accept: the HTTP route builds a session under a daemon-wide
// lock, and a session of one token must not delay another's. A worker, or a
// conversation the runner does not report as an open lead, never waits.
func (h *proxyHost) userCommandsFor(conversation string) func() mcp.UserCommandHost {
	deadline := time.Now().Add(userCommandsReadyWait)
	return func() mcp.UserCommandHost {
		if u := h.userCommandsNow(); u != nil || conversation == "" || h.userCmdsReady == nil {
			return u
		}
		select {
		case <-h.userCmdsReady:
		default:
			open, ok := h.agents.(interface {
				ComposerOpen(string) (uint64, error)
			})
			if !ok {
				return nil
			}
			if _, err := open.ComposerOpen(conversation); err != nil {
				return nil
			}
			// The budget left is read after the runner lookup, which may itself
			// have taken the runner's lock.
			left := time.Until(deadline)
			if left <= 0 {
				// Spent: report whatever was bound meanwhile.
				return h.userCommandsNow()
			}
			wait := time.NewTimer(left)
			defer wait.Stop()
			select {
			case <-h.userCmdsReady:
			case <-wait.C:
			}
		}
		return h.userCommandsNow()
	}
}

// userCommandsNow is the daemon's user commands as they are bound this instant,
// nil before they are (or when they are unavailable). A registration or
// withdrawal reads this and never waits.
func (h *proxyHost) userCommandsNow() mcp.UserCommandHost {
	if u := h.userCmds.Load(); u != nil {
		return u
	}
	return nil
}

// wait blocks until the replacement goroutine, if any, has ended.
func (h *proxyHost) wait() { h.background.Wait() }

// Accept builds the session for hello: an MCP server or a one-shot CLI run.
func (h *proxyHost) Accept(hello wsdashboard.ProxyHello) (wsdashboard.ProxySession, error) {
	hello = callerHello(hello)
	env := mcp.EnvFromMap(hello.Env)
	switch hello.Mode {
	case wsdashboard.ProxyModeMCP:
		root := hello.ProjectRoot
		if root == "" {
			root = defaultRootFrom(".", func(key string) string { return hello.Env[key] })
		}
		if root == "" || root == "." {
			root = hello.Cwd
		}
		opts := mcp.ServerOptions{
			BoardOwner:   true,
			Boards:       h.boards,
			Root:         root,
			Notes:        h.notes,
			Version:      version,
			SourceCommit: sourceCommit,
			Env:          env,
			OwnerPID:     hello.OwnerPID,
			Harness:      hello.Harness,
			Diag:         prefixedWriter{w: h.logw, prefix: fmt.Sprintf("dashboard: session pid %d: ", hello.OwnerPID)},
			MailLabels:   h.mailLabels,
		}
		if d := h.live.Load(); d != nil {
			opts.Live = d
			opts.Companion = d
			opts.DaemonControl = d
			opts.Conv = d
		}
		if h.agents != nil {
			if source, ok := h.agents.(mcp.AgentBackendRegistry); ok {
				opts.AgentBackends = source
			}
			if reader, ok := h.agents.(mcp.WorktreeChildrenReader); ok {
				opts.WorktreeChildren = reader
			}
			// Every connection, a forwarding worker's too, checks its
			// authenticated caller against the runner's memory.
			if binding, ok := h.agents.(mcp.CallerBindingReader); ok {
				opts.CallerBinding = binding
			}
			if reader, ok := h.agents.(mcp.AgentRecordReader); ok {
				opts.RecordReader = reader
			}
			// A worker now connects through the launcher too. Its capability
			// token, not a lead runner handle, scopes its nested pi.* calls.
			if strings.TrimSpace(hello.Env[wsagent.EnvPiToken]) == "" {
				opts.Agents = h.agents
			}
		}
		// Only a token-resolved hello names its conversation: the binding
		// that owns hosted exec jobs.
		opts.Exec = h.exec.Load
		opts.EphemeralPool = h.ephemeralPool
		opts.Cron = h.cron.Load
		opts.UserCommands = h.userCommandsNow
		opts.UserCommandsDiscovery = h.userCommandsFor(hello.Conversation)
		opts.Conversation = hello.Conversation
		opts.ExecCutover = hello.ExecCutover
		opts.DashboardControlAccess = hello.DashboardControlAccess
		opts.ConvHistoryAccess = hello.ConvHistoryAccess
		opts.FileWrites = hello.FileWrites
		opts.RecordExecLaunch = hello.RecordExecLaunch
		server := mcp.NewServerWithOptions(opts)
		h.applyLANChange(server)
		return &proxyMCPSession{server: server}, nil
	case wsdashboard.ProxyModeCLI:
		return &proxyCLISession{hello: hello, host: h}, nil
	}
	return nil, fmt.Errorf("unsupported mode %q", hello.Mode)
}

// callerHello resolves filesystem inputs against the caller, including defaults
// when an older launcher omitted cache/config overrides. Passing empty options
// to a store would otherwise fall back to the daemon's WS_* environment.
func callerHello(hello wsdashboard.ProxyHello) wsdashboard.ProxyHello {
	env := make(map[string]string, len(hello.Env)+2)
	for key, value := range hello.Env {
		env[key] = value
	}
	hello.Env = env
	home := env["HOME"]
	if runtime.GOOS == "windows" {
		home = env["USERPROFILE"]
	}
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	if env["WS_CACHE_HOME"] == "" {
		env["WS_CACHE_HOME"] = filepath.Join(home, ".cache", "ws@kang-sw-devenv")
	}
	if env["WS_CONFIG_HOME"] == "" {
		env["WS_CONFIG_HOME"] = filepath.Join(home, ".ws")
	}
	absolute := func(path string) string {
		if path != "" && !filepath.IsAbs(path) {
			return filepath.Join(hello.Cwd, path)
		}
		return path
	}
	for _, key := range []string{"WS_CACHE_HOME", "WS_CONFIG_HOME", "WS_RSRC_ROOT", "WS_SKILLS_ROOT", "WS_MCP_PROJECT_ROOT", "WS_WORKTREE_DIR", "WS_MCP_DEBUG_LOG", "WS_MCP_RUNTIME_BINARY", "WS_MCP_RUNTIME_DIR"} {
		if path, ok := env[key]; ok {
			env[key] = absolute(path)
		}
	}
	if manifests := env["WS_MCP_CONFIG_MANIFESTS"]; manifests != "" {
		paths := strings.FieldsFunc(manifests, func(r rune) bool { return r == ';' || r == rune(os.PathListSeparator) })
		for i, path := range paths {
			paths[i] = absolute(path)
		}
		env["WS_MCP_CONFIG_MANIFESTS"] = strings.Join(paths, ";")
	}
	hello.ProjectRoot = absolute(hello.ProjectRoot)
	return hello
}

// applyLANChange re-reads the global dashboard.lan_enabled through the
// connection's server: a value that differs from this daemon's mode
// replaces the daemon, once, through the drain path (Ensure asks this
// daemon to stop and starts a successor; the launchers reconnect to it).
//
// Only a session that takes part in the dashboard has a say: one whose
// manifests declare no dashboard keys, or that sets WS_MCP_NO_DASHBOARD,
// reads the key's default rather than the user's value, and one anchored at
// another WS_CONFIG_HOME reads another user's config file. Each of those
// used to flip the daemon back and forth with every connection.
func (h *proxyHost) applyLANChange(server *mcp.Server) {
	if !server.DashboardApplies() {
		return
	}
	if path, err := server.GlobalConfigPath(); err != nil || filepath.Clean(path) != h.configPath {
		return
	}
	settings := server.DashboardSettings()
	if settings.LAN == h.lan || h.replacing.Swap(true) {
		return
	}
	fmt.Fprintf(h.logw, "dashboard: global dashboard.lan_enabled is now %v; replacing this daemon\n", settings.LAN)
	port := settings.Port
	if port <= 0 {
		port = h.port
	}
	h.background.Add(1)
	go func() {
		defer h.background.Done()
		defer func() { _ = recover() }()
		ctx := context.Background()
		candidate := func() (string, error) {
			exe, err := os.Executable()
			if err != nil {
				return "", err
			}
			return wsdashboard.LaunchExecutable(exe, h.dir)
		}
		spawn, prepare := dashboardStarters(ctx, h.dir, candidate, false)
		outcome, err := wsdashboard.Ensure(ctx, wsdashboard.EnsureOptions{
			Dir:                 h.dir,
			Version:             version,
			BuildTime:           h.buildTime,
			ContentHash:         h.contentHash,
			Port:                port,
			LAN:                 settings.LAN,
			Spawn:               spawn,
			SpawnBeforeShutdown: true,
			JoinStarterLock:     true,
			PrepareSuccessor:    prepare,
			Log:                 h.logw,
		})
		if err != nil || outcome == wsdashboard.OutcomeUnavailable {
			fmt.Fprintf(h.logw, "dashboard: LAN mode change did not take (%s, %v)\n", outcome, err)
			h.replacing.Store(false)
		}
	}()
}

// prefixedWriter prefixes every write, so a session's diagnostics in the
// daemon log say which session they belong to.
type prefixedWriter struct {
	w      io.Writer
	prefix string
}

func (p prefixedWriter) Write(b []byte) (int, error) {
	if _, err := io.WriteString(p.w, p.prefix+string(b)); err != nil {
		return 0, err
	}
	return len(b), nil
}

// proxyMCPSession serves one mcp.Server over the stream.
type proxyMCPSession struct {
	server *mcp.Server
}

func (s *proxyMCPSession) Serve(ctx context.Context, stream io.ReadWriteCloser) {
	defer func() {
		if r := recover(); r != nil {
			mcp.RecordLifecycleEvent("session.panic", map[string]any{"panic": fmt.Sprint(r)})
		}
	}()
	_ = s.server.ServeStdio(ctx, stream, stream)
	s.server.Close()
}

func (s *proxyMCPSession) Drain(ctx context.Context) bool { return s.server.Drain(ctx) }

// proxyCLISession frames a CLI invocation's output. Disconnect cancels the
// invocation. Drain cancels long waits, but lets mutations finish, just like
// hosted MCP calls, rather than interrupting a commit or ticket move halfway.
type proxyCLISession struct {
	hello    wsdashboard.ProxyHello
	host     *proxyHost
	server   atomic.Pointer[mcp.Server]
	draining atomic.Bool

	cancel func()
	done   chan struct{}
	mu     sync.Mutex
}

func (s *proxyCLISession) Serve(ctx context.Context, stream io.ReadWriteCloser) {
	ctx, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.cancel = cancel
	s.done = make(chan struct{})
	s.mu.Unlock()
	defer close(s.done)
	defer cancel()
	// The launcher never writes after the hello; a read that ends means it
	// is gone.
	go func() {
		_, _ = io.Copy(io.Discard, stream)
		cancel()
	}()
	var writeMu sync.Mutex
	writeFrame := func(frame wsdashboard.ProxyCLIFrame) {
		raw, err := json.Marshal(frame)
		if err != nil {
			return
		}
		writeMu.Lock()
		defer writeMu.Unlock()
		_, _ = stream.Write(append(raw, '\n'))
	}
	var stdout bytes.Buffer
	stderr := &frameWriter{emit: func(data string) { writeFrame(wsdashboard.ProxyCLIFrame{Stream: "stderr", Data: data}) }}
	streamout := &frameWriter{emit: func(data string) { writeFrame(wsdashboard.ProxyCLIFrame{Stream: "stdout", Data: data}) }}
	command := hostedCLI{hello: s.hello, host: s.host, ctx: ctx, stdout: &stdout, stderr: stderr, streamout: streamout, env: mcp.EnvFromMap(s.hello.Env), serverReady: func(server *mcp.Server) {
		s.server.Store(server)
		if s.draining.Load() {
			server.Drain(context.Background())
		}
	}}
	code := command.run()
	writeFrame(wsdashboard.ProxyCLIFrame{Final: true, ExitCode: code, Data: stdout.String()})
}

func (s *proxyCLISession) Drain(ctx context.Context) bool {
	s.draining.Store(true)
	s.mu.Lock()
	cancel, done := s.cancel, s.done
	s.mu.Unlock()
	if cancel == nil {
		return true
	}
	if len(s.hello.Argv) >= 2 && s.hello.Argv[0] == "mailbox" && s.hello.Argv[1] == "wait" {
		cancel()
	}
	if server := s.server.Load(); server != nil {
		server.Drain(ctx)
	}
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// frameWriter turns each write into one frame.
type frameWriter struct {
	emit func(string)
}

func (f *frameWriter) Write(b []byte) (int, error) {
	f.emit(string(b))
	return len(b), nil
}

// daemonStartLAN is the LAN mode daemon-start asks Ensure for. A starter
// the dashboard applies to (a dashboard manifest declared, WS_MCP_NO_DASHBOARD
// unset) resolved settingsLAN from its configuration as serve does, and
// decides. Any other starter does not decide LAN, the rule applyLANChange
// applies to a hosted session: a daemon already running keeps its mode
// (running reports it when one answers), and with none the global
// dashboard.lan_enabled value under the starter's WS_CONFIG_HOME decides
// (global), since the daemon it starts inherits that config home. The
// alternative, settings.LAN read as false, would replace a LAN daemon on
// every start by a session the dashboard does not apply to.
func daemonStartLAN(applies, settingsLAN bool, running func() (lan, ok bool), global func() bool) bool {
	if applies {
		return settingsLAN
	}
	if lan, ok := running(); ok {
		return lan
	}
	return global()
}

// daemonStart is the entry the launcher runs from the staged daemon binary
// when no daemon answers, or the running one is older: it resolves the
// dashboard settings from the launcher's environment and configuration as
// serve does, and runs the Ensure handoff (equal cores stay unless --replace
// or a LAN change requests a swap; newer cores stay protected). The dashboard
// key's own value does not gate it: the daemon always hosts the MCP
// sessions, and only each session's listing follows the key. A starter the
// dashboard does not apply to does not decide the LAN mode
// (daemonStartLAN). It prints the
// outcome as one JSON line and exits non-zero only when no daemon came up.
func daemonStart(args []string) {
	daemonStartWithEnsure(args, wsdashboard.Ensure)
}

func desktopPredecessor(input io.Reader) (*wsdashboard.DaemonIdentity, error) {
	const limit = 4096
	raw, err := io.ReadAll(io.LimitReader(input, limit+1))
	var expected *wsdashboard.DaemonIdentity
	if err != nil || len(raw) > limit || json.Unmarshal(raw, &expected) != nil {
		return nil, fmt.Errorf("invalid desktop predecessor input")
	}
	if expected != nil {
		hash, err := hex.DecodeString(expected.TokenHash)
		if err != nil || len(hash) != 32 || expected.PID <= 0 || expected.Port <= 0 ||
			expected.Port > 65535 || expected.Version == "" {
			return nil, fmt.Errorf("incomplete desktop predecessor identity")
		}
	}
	return expected, nil
}

func daemonStartWithEnsure(args []string, ensure func(context.Context, wsdashboard.EnsureOptions) (wsdashboard.Outcome, error)) {
	// Consume the launcher's ownership marker before spawning a successor.
	const inheritedLock = "WS_MCP_LAUNCHER_START_LOCK_HELD"
	inherited := os.Getenv(inheritedLock) == "1"
	_ = os.Unsetenv(inheritedLock)
	fs := flag.NewFlagSet("daemon-start", flag.ExitOnError)
	buildTime := fs.Int64("build-time", 0, "the build time of the ws-mcp this daemon stands for, in Unix milliseconds; 0 reads this executable's modification time")
	startWait := fs.Duration("start-wait", 0, "how long to wait for a started daemon; 0 means the default")
	replace := fs.Bool("replace", false, "explicitly replace an equal-core daemon once, even with identical content; never downgrade a newer core")
	belowFloor := fs.Bool("below-floor", false, "admit starting a daemon below the state directory's daemon-minor-floor and lower the floor to this daemon's minor")
	independent := fs.Bool("require-independent-lifetime", false, "Windows Electron: prestart before drain, preferring independent lifetime (explicit job-lifetime retry supported)")
	desktop := fs.Bool("desktop-start", false, "prestart before drain, with an observed predecessor identity or null read from stdin")
	_ = fs.Parse(args)
	if *independent && runtime.GOOS != "windows" {
		fatal("daemon-start", fmt.Errorf("--require-independent-lifetime is Windows-only"))
	}
	var predecessor *wsdashboard.DaemonIdentity
	if *desktop {
		var err error
		predecessor, err = desktopPredecessor(os.Stdin)
		if err != nil {
			fatal("daemon-start", err)
		}
	}

	server := mcp.NewServer(defaultRoot("."), version, sourceCommit)
	settings := server.DashboardSettings()
	dir, err := wsdashboard.Dir("")
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fatal("daemon-start", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*wsdashboard.DefaultStartWait+*startWait)
	defer cancel()
	lan := daemonStartLAN(server.DashboardApplies(), settings.LAN,
		func() (bool, bool) {
			ep, err := wsdashboard.ReadEndpoint(dir)
			if err != nil {
				return false, false
			}
			info, err := wsdashboard.Health(ctx, &http.Client{Timeout: wsdashboard.DefaultHealthTimeout, Transport: &http.Transport{Proxy: nil}}, ep)
			return info.LAN, err == nil
		},
		func() bool {
			value, _ := wsconfig.GlobalOverride(wsconfig.Options{}, mcp.DashboardLANKey)
			return value == "true"
		})
	exe, err := os.Executable()
	if err != nil {
		fatal("daemon-start", err)
	}
	if *buildTime <= 0 {
		*buildTime = wsdashboard.ExecutableBuildTime(exe)
	}
	// The desktop flags identify direct starters. Plain daemon-start can
	// come from an older launcher that holds the lock without the marker.
	strict := *independent || *desktop
	candidate := func() (string, error) {
		if strict {
			return exe, nil
		}
		return wsdashboard.LaunchExecutable(exe, dir)
	}
	spawn, prepare := dashboardStarters(ctx, dir, candidate, strict)
	opts := wsdashboard.EnsureOptions{
		Dir:                 dir,
		Version:             version,
		BuildTime:           *buildTime,
		ContentHash:         wsdashboard.ExecutableContentHash(exe),
		Replace:             *replace,
		BelowFloor:          *belowFloor,
		SpawnBeforeShutdown: true,
		JoinStarterLock:     strict && !inherited,
		PrepareSuccessor:    prepare,
		PreserveRunningLAN:  *independent || *desktop,
		DesktopStart:        *desktop,
		Predecessor:         predecessor,
		Port:                settings.Port,
		LAN:                 lan,
		Spawn:               spawn,
		StartWait:           *startWait,
		Log:                 os.Stderr,
	}
	outcome, err := ensure(ctx, opts)
	if err != nil {
		fatal("daemon-start", err)
	}
	ep, _ := wsdashboard.ReadEndpoint(dir)
	printJSONOrFatal("daemon-start", map[string]any{"outcome": outcome, "port": ep.Port, "pid": ep.PID, "version": ep.Version}, nil)
	if outcome == wsdashboard.OutcomeUnavailable {
		os.Exit(1)
	}
}
