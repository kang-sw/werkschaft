package wsagent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsharnessenv"
)

// mcp_session.go is the hosted path's ws-mcp attachment over HTTP: a
// harness the daemon starts reaches the daemon's ws MCP route
// (wsdashboard.ProxyHTTPPath) directly, with no launcher between them.
//
//   - Token. Each process start draws a per-session token; a new start
//     voids the one the conversation's last process had, and the token is
//     void once the process ends or the conversation closes (revokeToken).
//     A held process's token is persisted in its record (Record.MCPToken),
//     with the hello's ws-mcp environment and the holder it was drawn for,
//     and restored when a later daemon rejoins the process. Worker restoration follows holder ownership validation.
//   - Context. The token resolves to the hello the stdio launcher would
//     have sent from the same harness (mcpHello): the harness's own
//     environment, the ws-mcp entry's environment (the tool allowlist, the
//     hosted package policy), the harness PID and the working directory.
//   - Credentials. The harness reads the URL and the token from its
//     environment (EnvMCPSessionURL, EnvMCPSessionToken), or takes them in
//     a protocol request (agy's session request), never its argv, so
//     neither shows in a process listing or a config file.

// Env names the backend sets on a harness attached over HTTP.
const (
	// EnvMCPSessionURL is the daemon's ws MCP route for the session.
	EnvMCPSessionURL = "WS_MCP_SESSION_URL"
	// EnvMCPSessionToken is the session's bearer token on that route.
	EnvMCPSessionToken = "WS_MCP_SESSION_TOKEN"
	// EnvAgentSessionKey identifies a worker's connection for descriptor
	// lookup only. It grants no hosted-lead authority or key substitution.
	EnvAgentSessionKey = "WS_MCP_AGENT_SESSION_KEY"
)

// mcpStartWait bounds how long a token's first resolution waits for its
// process to start: the harness connects as soon as it runs, so only a
// start that never reports (a failed launch) reaches it.
var mcpStartWait = 10 * time.Second

// mcpRejoinWait bounds how long an unknown token waits for the daemon's
// startup rejoin. Dashboard publication waits for the initial holder scan,
// but a runner can serve in-process requests while rejoining. A request
// that exhausts this wait is refused if the token is still unknown.
var mcpRejoinWait = 8 * time.Second

// mcpAfterMiss is a test hook: it runs between an unknown token's first
// lookup and its wait for the rejoin.
var mcpAfterMiss func()

// MCPSession attaches a harness's ws-mcp over the daemon's HTTP route
// instead of the stdio launcher.
type MCPSession struct {
	URL, Token string
	// Started is called by the backend once the harness runs, with the
	// whole environment it was started with and its PID (0 when unknown):
	// the context the stdio launcher would have sent in its hello. A rejoin
	// calls it too.
	Started func(env []string, pid int)
	// Connected is closed once the token first resolves on the route: the
	// harness reached ws-mcp with it. A harness that drops a refused
	// server silently is checked against it.
	Connected <-chan struct{}
}

// mcpToken is one live token's state, under Runner.mu.
type mcpToken struct {
	id string
	// env is the ws-mcp entry's environment, laid over the harness's.
	env map[string]string
	cwd string
	// hello is set once ready is closed; void is closed when the token is;
	// connected is closed at the token's first resolution.
	hello     wsdashboard.ProxyHello
	ready     chan struct{}
	void      chan struct{}
	connected chan struct{}
	// cutover is the backend of a process started under the exec cutover
	// (markMCPCutover), "" otherwise.
	cutover                string
	convHistoryAccess      bool
	dashboardControlAccess bool
	fileWrites             *wsfs.FileWriteSession
	recordExecLaunch       func(context.Context, execjob.ModelLaunch) (bool, error)
}

// newMCPToken draws a token, or "" when no random source answers.
func newMCPToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return ""
	}
	return hex.EncodeToString(raw)
}

// attachMCP binds token (a new one when empty) to conversation id for its
// next process, voiding the one it had, and returns the session to start
// it with; nil when a new token has no endpoint to go with or none could
// be drawn. A given token is a rejoined process's, which already holds
// its URL. env is the ws-mcp entry's environment, cwd the harness's working
// directory, and harness the record's backend identity. knownPID stands in for
// a PID the backend cannot report (a rejoined process, whose PID the record
// holds).
func (r *Runner) attachMCP(id, token string, env map[string]string, cwd string, knownPID int, harness string) *MCPSession {
	restore := token != ""
	if !restore {
		token = newMCPToken()
	}
	if token == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.endpoint == "" && !restore {
		return nil
	}
	r.voidMCPLocked(id)
	t := &mcpToken{id: id, env: env, cwd: cwd, ready: make(chan struct{}), void: make(chan struct{}), connected: make(chan struct{})}
	if r.mcpTokens == nil {
		r.mcpTokens, r.mcpTokenOf = map[string]*mcpToken{}, map[string]string{}
	}
	r.mcpTokens[token], r.mcpTokenOf[id] = t, token
	version := r.opts.DaemonVersion
	return &MCPSession{
		URL:       r.endpoint + wsdashboard.ProxyHTTPPath,
		Token:     token,
		Connected: t.connected,
		Started: func(harnessEnv []string, pid int) {
			r.mu.Lock()
			defer r.mu.Unlock()
			if r.mcpTokens[token] != t {
				return
			}
			select {
			case <-t.ready:
				return
			default:
			}
			if pid <= 0 {
				pid = knownPID
			}
			t.hello = mcpHello(harnessEnv, t.env, t.cwd, pid, version)
			t.hello.Harness = harness
			close(t.ready)
		},
	}
}

type durableStarter interface {
	startLoop(context.Context, StartSpec) (Process, error)
}

// WrapDurableBackend adapts a Backend whose Start method implements a durable
// process loop so callers in tests outside wsagent can register ProcessDurable
// fixtures without relaxing the runner's durable-process guard.
func WrapDurableBackend(b Backend) Backend {
	if b == nil {
		return nil
	}
	if _, ok := b.(durableStarter); ok {
		return b
	}
	return durableBackendAdapter{Backend: b}
}

type durableBackendAdapter struct{ Backend }

func (a durableBackendAdapter) startLoop(ctx context.Context, spec StartSpec) (Process, error) {
	proc, err := a.Backend.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	if _, ok := proc.(durableTurnProcess); ok {
		return proc, nil
	}
	return newDurableProcessAdapter(proc), nil
}

type durableProcessAdapter struct {
	Process
	events chan Event
	turnID atomic.Value // string
}

func newDurableProcessAdapter(proc Process) *durableProcessAdapter {
	a := &durableProcessAdapter{
		Process: proc,
		events:  make(chan Event, 64),
	}
	a.turnID.Store("")
	go func() {
		defer close(a.events)
		for ev := range proc.Events() {
			if ev.TurnID == "" && (ev.TurnStart || ev.TurnEnd) {
				if id, _ := a.turnID.Load().(string); id != "" {
					ev.TurnID = id
				}
			}
			a.events <- ev
		}
	}()
	return a
}

func (a *durableProcessAdapter) Events() <-chan Event { return a.events }

func (a *durableProcessAdapter) submitTurn(id, text string, _ []durableSteer, _ bool) error {
	a.turnID.Store(id)
	return a.Process.Send(text)
}

type fileCallRecorder interface {
	recordFileCall(context.Context, wsfs.FileCall) error
}

type fileCallApprover interface {
	approveFileCall(context.Context, wsfs.FileCall) error
}

// bindDurableMCP binds a just-installed daemon process before the token
// becomes ready. The connection may outlive its admission, so each call
// checks the token and the stored permission again.
func (c *conv) bindDurableMCP(session *MCPSession, rec Record, proc Process, level PermissionLevel) {
	c.r.mu.Lock()
	defer c.r.mu.Unlock()
	t := c.r.mcpTokens[session.Token]
	if t == nil || t.id != rec.ReplyID {
		return
	}
	authority := func() wsfs.Authority {
		select {
		case <-t.void:
			return ""
		default:
		}
		c.mu.Lock()
		if c.closed || c.proc == nil {
			c.mu.Unlock()
			return ""
		}
		current, workerLevel := c.rec, c.workerPermissionLevel
		c.mu.Unlock()
		if current.Lead {
			selected, explicit, readErr := c.r.cachedPermissionSelection(current.ReplyID, c.dir)
			level, _, err := c.r.resolveLeadPermission(current, selected, explicit, readErr)
			if err != nil {
				return ""
			}
			return wsfs.Authority(level)
		}
		return wsfs.Authority(workerLevel)
	}
	_, liveModes := proc.(liveLeadMode)
	admission := &c.r.permissionSlot(rec.ReplyID, c.dir).admission
	files := &wsfs.FileWriteSession{
		Root: rec.Root, Cwd: rec.workDir(), Authority: wsfs.Authority(level),
		CurrentAuthority: authority, LiveAuthority: rec.Lead && liveModes,
		HoldAuthority: func() func() {
			admission.RLock()
			return admission.RUnlock
		},
	}
	if approver, ok := proc.(fileCallApprover); ok {
		files.Approve = approver.approveFileCall
	}
	if recorder, ok := proc.(fileCallRecorder); ok && c.r.lifecycle(rec.Backend).FileWriteTools {
		files.Record = func(ctx context.Context, call wsfs.FileCall) error {
			expected := files.Authority
			if files.LiveAuthority {
				expected = call.Authority
			}
			if expected == "" || authority() != expected {
				return errors.New("the file call's session authority changed")
			}
			if err := recorder.recordFileCall(ctx, call); err != nil {
				return err
			}
			if authority() != expected {
				return errors.New("the file call's session authority changed")
			}
			return nil
		}
	}
	t.fileWrites = files
	t.recordExecLaunch = func(ctx context.Context, call execjob.ModelLaunch) (bool, error) {
		level := files.AuthorityForCall()
		readOnlyWorker := !files.LiveAuthority && files.Authority == wsfs.AuthorityReadOnly && level == wsfs.AuthorityReadOnly
		if !readOnlyWorker && level != wsfs.AuthorityWorkdirWrite && level != wsfs.AuthorityFull {
			return false, errors.New("the session cannot launch exec jobs with its current authority")
		}
		recorder, ok := proc.(execLaunchRecorder)
		if !ok {
			return false, errors.New("the session has no durable exec job recorder")
		}
		if call.Approval != nil && level == wsfs.AuthorityWorkdirWrite {
			call.RequireExplicit = true
		}
		explicit, err := recorder.recordExecLaunch(ctx, call)
		if err != nil {
			return false, err
		}
		current := files.AuthorityForCall()
		if readOnlyWorker {
			if current != wsfs.AuthorityReadOnly {
				return false, errors.New("the exec launch's authority changed")
			}
		} else {
			if current != wsfs.AuthorityWorkdirWrite && current != wsfs.AuthorityFull {
				return false, errors.New("the exec launch's authority changed")
			}
			if call.Approval == nil && current != level {
				return false, errors.New("the exec launch's authority changed")
			}
		}
		return explicit, nil
	}
}

// markMCPDashboardControl snapshots whether conversation id's attached MCP
// session opted into ws/daemon.* dashboard control at process start/attach.
func (r *Runner) markMCPDashboardControl(id string, enabled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if token, ok := r.mcpTokenOf[id]; ok {
		if t := r.mcpTokens[token]; t != nil {
			t.dashboardControlAccess = enabled
		}
	}
}

// voidMCPLocked voids conversation id's token, if any; r.mu is held.
func (r *Runner) voidMCPLocked(id string) {
	token, ok := r.mcpTokenOf[id]
	if !ok {
		return
	}
	if t := r.mcpTokens[token]; t != nil {
		close(t.void)
	}
	delete(r.mcpTokens, token)
	delete(r.mcpTokenOf, id)
}

// ResolveMCPToken resolves a live session token to its process's hello and
// a channel closed once the token is void (wsdashboard.AgentMCPTokenHost).
// A token whose process has not reported its start yet waits for it, up
// to mcpStartWait; an unknown token waits for the startup rejoin, up to
// mcpRejoinWait, since that scan restores a replaced daemon's workers.
func (r *Runner) ResolveMCPToken(token string) (wsdashboard.ProxyHello, <-chan struct{}, bool) {
	hello, void, reason := r.ResolveMCPTokenWithReason(token)
	return hello, void, reason == ""
}

// ResolveMCPTokenWithReason returns the same resolution with a local
// diagnostic reason on failure (wsdashboard.AgentMCPTokenDiagnostics).
func (r *Runner) ResolveMCPTokenWithReason(token string) (wsdashboard.ProxyHello, <-chan struct{}, string) {
	return r.resolveMCPToken(token, true)
}

// ResolveCredentialToken resolves a live session token for the daemon's
// provider token route (wsdashboard.AgentCredentialHost). The session
// passes only when its backend's registration admits provider credentials
// (BackendRegistration.ProviderCredentials); the backend is the record's,
// bound when the token was drawn, never what the caller claims. Unlike
// ResolveMCPToken it is not the harness reaching ws-mcp.
func (r *Runner) ResolveCredentialToken(token string) (wsdashboard.CredentialSession, bool) {
	hello, void, reason := r.resolveMCPToken(token, false)
	if reason != "" {
		return wsdashboard.CredentialSession{}, false
	}
	select {
	case <-void:
		return wsdashboard.CredentialSession{}, false
	default:
	}
	reg, known := r.opts.Backends[hello.Harness]
	session := wsdashboard.CredentialSession{Conversation: hello.Conversation, Harness: hello.Harness, Permitted: known && reg.ProcessKind != ProcessDurable && reg.ProviderCredentials}
	if c := r.resident(hello.Conversation); c != nil {
		c.mu.Lock()
		session.Lead = c.rec.Lead
		c.mu.Unlock()
	}
	return session, true
}

// resolveMCPToken resolves token as ResolveMCPToken does; connect marks the
// token's first resolution as the harness reaching ws-mcp
// (MCPSession.Connected).
func (r *Runner) resolveMCPToken(token string, connect bool) (wsdashboard.ProxyHello, <-chan struct{}, string) {
	if token == "" {
		return wsdashboard.ProxyHello{}, nil, "missing bearer"
	}
	reason := "token unknown"
	expired := false
	r.mu.Lock()
	t := r.mcpTokens[token]
	r.mu.Unlock()
	if t == nil && r.started.Load() {
		// Unknown until the startup rejoin finished: the token may be a
		// surviving worker's, restored by that scan. Afterwards it is
		// unknown for good.
		if mcpAfterMiss != nil {
			mcpAfterMiss()
		}
		select {
		case <-r.rejoined:
		default:
			wait := time.NewTimer(mcpRejoinWait)
			select {
			case <-r.rejoined:
			case <-r.quit:
				reason = "daemon stopping"
			case <-wait.C:
				reason = "rejoin wait expired"
				expired = true
			}
			wait.Stop()
		}
		// Read again whichever way the wait ended: the rejoin may have
		// restored the token between the first lookup and the wait, so a
		// rejoin that was already done is no reason to refuse it.
		r.mu.Lock()
		t = r.mcpTokens[token]
		if t == nil && expired && connect {
			r.recordMCPWaitMissLocked(token)
		}
		r.mu.Unlock()
	}
	if t == nil {
		return wsdashboard.ProxyHello{}, nil, reason
	}
	timer := time.NewTimer(mcpStartWait)
	defer timer.Stop()
	select {
	case <-t.ready:
	case <-t.void:
		return wsdashboard.ProxyHello{}, nil, "token void"
	case <-timer.C:
		return wsdashboard.ProxyHello{}, nil, "process start wait expired"
	}
	select {
	case <-t.void:
		return wsdashboard.ProxyHello{}, nil, "token void"
	default:
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if connect {
		select {
		case <-t.connected:
		default:
			close(t.connected)
		}
	}
	hello := t.hello
	// The token is the binding: only this resolution names the conversation.
	hello.Conversation = t.id
	hello.ExecCutover = t.cutover
	hello.ConvHistoryAccess = t.convHistoryAccess
	hello.DashboardControlAccess = t.dashboardControlAccess
	hello.FileWrites = t.fileWrites
	hello.RecordExecLaunch = t.recordExecLaunch
	return hello, t.void, ""
}

// markMCPConvHistoryAccess binds the start-time conv_history_access snapshot
// onto conversation id's current ws-mcp token so tools/list reflects it.
func (r *Runner) markMCPConvHistoryAccess(id string, on bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if t := r.mcpTokens[r.mcpTokenOf[id]]; t != nil {
		t.convHistoryAccess = on
	}
}

// recordMCPWaitMissLocked only tracks misses while rejoin can still restore
// a worker. Cap the table because the bearer is untrusted until it matches.
func (r *Runner) recordMCPWaitMissLocked(token string) {
	select {
	case <-r.rejoined:
		return
	default:
	}
	const maxMisses = 256
	if len(r.mcpWaitMisses) >= maxMisses {
		return
	}
	if r.mcpWaitMisses == nil {
		r.mcpWaitMisses = make(map[[32]byte]struct{})
	}
	r.mcpWaitMisses[sha256.Sum256([]byte(token))] = struct{}{}
}

func (r *Runner) takeMCPWaitMiss(token string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.mcpWaitMisses) == 0 {
		return false
	}
	digest := sha256.Sum256([]byte(token))
	_, missed := r.mcpWaitMisses[digest]
	delete(r.mcpWaitMisses, digest)
	return missed
}

func (r *Runner) finishMCPRejoin() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.mcpWaitMisses = nil
	r.rejoinOnce.Do(func() { close(r.rejoined) })
}

// durableEnv returns base without inherited ws session identity or parent
// harness session variables, keeping user state such as PATH, HOME,
// USERPROFILE, WS_CACHE_HOME, and WS_CONFIG_HOME.
func durableEnv(base []string) []string {
	return wsharnessenv.DropDurable(base)
}

// mcpHello is the hello the stdio launcher would have sent for a harness
// started with harnessEnv whose ws-mcp entry carries env: the launcher
// inherits the harness's environment, the entry's environment overrides
// it, and the launcher adds the harness PID and its runtime binary.
func mcpHello(harnessEnv []string, env map[string]string, cwd string, pid int, version string) wsdashboard.ProxyHello {
	return mcpHelloForOS(runtime.GOOS, harnessEnv, env, cwd, pid, version)
}

func mcpHelloForOS(goos string, harnessEnv []string, env map[string]string, cwd string, pid int, version string) wsdashboard.ProxyHello {
	out := make(map[string]string, len(harnessEnv)+len(env)+2)
	for _, kv := range harnessEnv {
		if name, value, ok := strings.Cut(kv, "="); ok {
			if goos == "windows" {
				deleteEnvFold(out, name)
			}
			out[name] = value
		}
	}
	// The session's credentials are the harness's, not its server's.
	if goos == "windows" {
		deleteEnvFold(out, EnvMCPSessionURL)
		deleteEnvFold(out, EnvMCPSessionToken)
		for name := range env {
			deleteEnvFold(out, name)
		}
		deleteEnvFold(out, "WS_MCP_HARNESS_PID")
		deleteEnvFold(out, "WS_MCP_RUNTIME_BINARY")
	} else {
		delete(out, EnvMCPSessionURL)
		delete(out, EnvMCPSessionToken)
	}
	for name, value := range env {
		out[name] = value
	}
	out["WS_MCP_HARNESS_PID"] = strconv.Itoa(pid)
	if exe, err := os.Executable(); err == nil {
		out["WS_MCP_RUNTIME_BINARY"] = exe
	}
	return wsdashboard.ProxyHello{
		Protocol:    wsdashboard.ProxyProtocol,
		Mode:        wsdashboard.ProxyModeMCP,
		Cwd:         cwd,
		ProjectRoot: lookupEnvKey(goos, out, "WS_MCP_PROJECT_ROOT"),
		Env:         out,
		Harness:     lookupEnvKey(goos, out, "WS_MCP_HARNESS"),
		OwnerPID:    pid,
		Version:     version,
	}
}

func deleteEnvFold(m map[string]string, target string) {
	for k := range m {
		if strings.EqualFold(k, target) {
			delete(m, k)
		}
	}
}

func lookupEnvKey(goos string, m map[string]string, target string) string {
	if v, ok := m[target]; ok || goos != "windows" {
		return v
	}
	for k, v := range m {
		if strings.EqualFold(k, target) {
			return v
		}
	}
	return ""
}

// mcpSessionEnv is env with session's credentials, and without any a
// parent session left behind when session is nil.
func mcpSessionEnv(env []string, session *MCPSession) []string {
	set := map[string]string{EnvMCPSessionURL: "", EnvMCPSessionToken: ""}
	if session != nil {
		set[EnvMCPSessionURL], set[EnvMCPSessionToken] = session.URL, session.Token
	}
	return harnessEnv(env, nil, nil, set)
}

// forgetMCP clears the record's ws-mcp session (Record.MCPToken).
func (rec *Record) forgetMCP() {
	rec.MCPToken, rec.MCPURL, rec.MCPEnv, rec.MCPHolder, rec.MCPConvHistoryAccess, rec.MCPDashboardControl = "", "", nil, "", false, false
}

// mcpURL is the HTTP route a process started now is given, or "" when the
// runner has no endpoint.
func (r *Runner) mcpURL() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.endpoint == "" {
		return ""
	}
	return r.endpoint + wsdashboard.ProxyHTTPPath
}
