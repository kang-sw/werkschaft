package wsdashboard

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kang-sw/devenv/internal/wscred"
)

var errHarnessAuthCLIUnavailable = errors.New("cli_unavailable")

// harnessAuthProvider describes a single-account harness whose interactive CLI
// login and auth-hold latch are managed through /settings?section=account
// without extracting third-party OAuth or Keychain tokens (D15, D17, D18).
type harnessAuthProvider struct {
	harness              string
	label                string
	state                func() harnessAuthSnapshot
	resolveCmd           func() (exe string, args []string, env []string, err error)
	clearHoldAndReverify func() bool
}

// harnessAuthSnapshot is the viewer-facing status of one single-account
// harness auth provider. Served purely from memory (zero disk/subprocess I/O).
type harnessAuthSnapshot struct {
	Harness      string                  `json:"harness"`
	Label        string                  `json:"label"`
	SingleSlot   bool                    `json:"single_slot"`
	Available    bool                    `json:"available"`
	Probed       bool                    `json:"probed"`
	Probing      bool                    `json:"probing"`
	AuthRequired bool                    `json:"auth_required"`
	BilledLatch  bool                    `json:"billed_latch,omitempty"`
	Status       string                  `json:"status,omitempty"`
	SignIn       harnessAuthSignInStatus `json:"sign_in"`
}

// harnessAuthSignInStatus tracks an in-progress single-account CLI login
// attempt. Auth URLs are cleared immediately on completion, failure, or cancel.
type harnessAuthSignInStatus struct {
	State         string `json:"state"`
	Attempt       string `json:"attempt,omitempty"`
	Harness       string `json:"harness,omitempty"`
	Reason        string `json:"reason,omitempty"`
	AuthorizeURL  string `json:"authorize_url,omitempty"`
	CallbackReady bool   `json:"callback_ready,omitempty"`
	StdinReady    bool   `json:"stdin_ready,omitempty"`
	BrowserError  bool   `json:"browser_error,omitempty"`
}

// harnessAuthManager holds at most one in-progress single-account harness CLI
// sign-in across HTTP requests.
type harnessAuthManager struct {
	mu           sync.Mutex
	stdinMu      sync.Mutex
	state        harnessAuthSignInStatus
	cancel       context.CancelFunc
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	callbackPort int
	closed       bool
}

func (m *harnessAuthManager) status() harnessAuthSignInStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.State == "" {
		return harnessAuthSignInStatus{State: "idle"}
	}
	return m.state
}

func (m *harnessAuthManager) statusFor(harness string) harnessAuthSignInStatus {
	st := m.status()
	if st.Harness != "" && st.Harness != harness {
		return harnessAuthSignInStatus{State: "idle"}
	}
	return st
}

func (m *harnessAuthManager) stop() {
	m.mu.Lock()
	m.closed = true
	cancel := m.cancel
	stdin := m.stdin
	m.stdin = nil
	m.mu.Unlock()
	if stdin != nil {
		m.stdinMu.Lock()
		_ = stdin.Close()
		m.stdinMu.Unlock()
	}
	// Cancelling the context kills the process tree through cmd.Cancel.
	if cancel != nil {
		cancel()
	}
}

func (d *Daemon) harnessAuthProviders() []harnessAuthProvider {
	if d.planUsage == nil {
		return nil
	}
	return d.planUsage.harnessAuthProviders()
}

func (d *Daemon) findHarnessAuthProvider(harness string) (harnessAuthProvider, bool) {
	for _, p := range d.harnessAuthProviders() {
		if p.harness == harness {
			return p, true
		}
	}
	return harnessAuthProvider{}, false
}

func (d *Daemon) harnessAuthSnapshots() []harnessAuthSnapshot {
	providers := d.harnessAuthProviders()
	if len(providers) == 0 {
		return nil
	}
	out := make([]harnessAuthSnapshot, 0, len(providers))
	for _, p := range providers {
		snap := p.state()
		if d.harnessAuthCmd != nil {
			snap.Available = true
		}
		snap.SignIn = d.harnessAuth.statusFor(p.harness)
		out = append(out, snap)
	}
	return out
}

func (d *Daemon) handleAccountHarnessAuth(w http.ResponseWriter, r *http.Request, action string) {
	if action == "harness-auth/status" {
		writeJSON(w, http.StatusOK, struct {
			Providers []harnessAuthSnapshot   `json:"providers"`
			SignIn    harnessAuthSignInStatus `json:"sign_in"`
		}{
			Providers: d.harnessAuthSnapshots(),
			SignIn:    d.harnessAuth.status(),
		})
		return
	}
	if !loopbackPeer(r.RemoteAddr) {
		accountError(w, http.StatusForbidden, "loopback_required")
		return
	}
	switch action {
	case "harness-auth/start":
		d.handleAccountHarnessAuthStart(w, r)
	case "harness-auth/complete":
		d.handleAccountHarnessAuthComplete(w, r)
	case "harness-auth/cancel":
		d.handleAccountHarnessAuthCancel(w, r)
	case "harness-auth/reverify":
		d.handleAccountHarnessAuthReverify(w, r)
	default:
		accountError(w, http.StatusNotFound, "not_found")
	}
}

func (d *Daemon) handleAccountHarnessAuthStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Harness  string `json:"harness"`
		CopyOnly bool   `json:"copy_only"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	harness := strings.TrimSpace(body.Harness)
	provider, ok := d.findHarnessAuthProvider(harness)
	if !ok {
		accountError(w, http.StatusBadRequest, "harness_auth_unsupported")
		return
	}

	var (
		exe  string
		args []string
		env  []string
		err  error
	)
	if d.harnessAuthCmd != nil {
		exe, args, err = d.harnessAuthCmd(harness)
		if err != nil {
			accountError(w, http.StatusServiceUnavailable, "cli_unavailable")
			return
		}
	} else {
		exe, args, env, err = provider.resolveCmd()
		if err != nil {
			accountError(w, http.StatusServiceUnavailable, "cli_unavailable")
			return
		}
	}

	m := &d.harnessAuth
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "harness_auth_unavailable")
		return
	}
	if m.cancel != nil {
		m.mu.Unlock()
		accountError(w, http.StatusConflict, "sign_in_in_progress")
		return
	}

	timeout := d.harnessAuthTimeout
	if timeout <= 0 {
		timeout = defaultSlotSignInTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	attempt := uuid.NewString()
	m.cancel = cancel
	m.cmd = nil
	m.stdin = nil
	m.callbackPort = 0
	m.state = harnessAuthSignInStatus{
		State:   "starting",
		Attempt: attempt,
		Harness: harness,
	}
	resp := m.state
	m.mu.Unlock()

	openFn := d.openSystem
	if body.CopyOnly {
		openFn = nil
	}
	go d.runHarnessAuth(ctx, attempt, provider, exe, args, env, openFn)
	writeJSON(w, http.StatusAccepted, resp)
}

func (d *Daemon) runHarnessAuth(
	ctx context.Context,
	attempt string,
	provider harnessAuthProvider,
	exe string,
	args, env []string,
	openBrowser func(string) error,
) {
	m := &d.harnessAuth
	// CommandContext guarantees Cancel never fires after Wait has reaped the
	// child, so the process-group kill can never hit a recycled PID/PGID.
	cmd := exec.CommandContext(ctx, exe, args...)
	configureSlotLoginCmd(cmd)
	cmd.Cancel = func() error {
		killSlotLoginProcessTree(cmd)
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	if len(env) > 0 {
		cmd.Env = env
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		d.finishHarnessAuth(attempt, "failed", "login_failed")
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		d.finishHarnessAuth(attempt, "failed", "login_failed")
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		d.finishHarnessAuth(attempt, "failed", "login_failed")
		return
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		// CommandContext refuses to start once ctx is done; keep the cancel and
		// timeout outcomes distinct from a missing CLI.
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				d.finishHarnessAuth(attempt, "failed", "timed_out")
			} else {
				d.finishHarnessAuth(attempt, "cancelled", "")
			}
			return
		}
		d.finishHarnessAuth(attempt, "failed", "cli_unavailable")
		return
	}

	m.mu.Lock()
	if m.state.Attempt == attempt {
		m.cmd = cmd
		m.stdin = stdin
		m.state.StdinReady = true
		if m.state.State == "starting" {
			m.state.State = "waiting"
		}
	} else {
		_ = stdin.Close()
	}
	m.mu.Unlock()

	var openOnce sync.Once
	scanStream := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 8192), 64*1024)
		for scanner.Scan() {
			authURL, _, _, cbPort := wscred.ParseSlotLoginLine(scanner.Text())
			if authURL == "" && cbPort == 0 {
				continue
			}
			var browserTarget string
			m.mu.Lock()
			if m.state.Attempt == attempt {
				if authURL != "" {
					m.state.AuthorizeURL = authURL
					if openBrowser != nil {
						browserTarget = authURL
					}
				}
				if cbPort > 0 {
					m.callbackPort = cbPort
					m.state.CallbackReady = true
				}
				if m.state.State == "starting" {
					m.state.State = "waiting"
				}
			}
			m.mu.Unlock()
			if browserTarget != "" {
				openOnce.Do(func() {
					if openBrowser(browserTarget) != nil {
						m.mu.Lock()
						if m.state.Attempt == attempt {
							m.state.BrowserError = true
						}
						m.mu.Unlock()
					}
				})
			}
		}
	}

	var scanWG sync.WaitGroup
	scanWG.Add(2)
	go func() { defer scanWG.Done(); scanStream(stdout) }()
	go func() { defer scanWG.Done(); scanStream(stderr) }()
	scanWG.Wait()

	waitErr := cmd.Wait()

	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			d.finishHarnessAuth(attempt, "failed", "timed_out")
		} else {
			d.finishHarnessAuth(attempt, "cancelled", "")
		}
		return
	}
	if waitErr != nil {
		d.finishHarnessAuth(attempt, "failed", "login_failed")
		return
	}
	d.finishHarnessAuth(attempt, "succeeded", "")
	if provider.clearHoldAndReverify != nil {
		provider.clearHoldAndReverify()
	}
	d.accountChanged()
}

func (d *Daemon) finishHarnessAuth(attempt, state, reason string) {
	m := &d.harnessAuth
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Attempt != attempt {
		return
	}
	// Clear auth URLs and stdin handles on completion/failure/cancel (D18).
	m.state.State = state
	m.state.Reason = reason
	m.state.AuthorizeURL = ""
	m.state.CallbackReady = false
	m.state.StdinReady = false
	m.state.BrowserError = false
	m.callbackPort = 0
	m.cmd = nil
	stdin := m.stdin
	m.stdin = nil
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	if stdin != nil {
		m.stdinMu.Lock()
		_ = stdin.Close()
		m.stdinMu.Unlock()
	}
}

func (d *Daemon) handleAccountHarnessAuthComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Harness string `json:"harness"`
		Attempt string `json:"attempt"`
		Input   string `json:"input"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	trimmed := strings.TrimSpace(body.Input)
	if body.Attempt == "" || trimmed == "" {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	m := &d.harnessAuth
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "harness_auth_unavailable")
		return
	}
	if m.cancel == nil || body.Attempt != m.state.Attempt || (body.Harness != "" && body.Harness != m.state.Harness) {
		m.mu.Unlock()
		accountError(w, http.StatusConflict, "sign_in_changed")
		return
	}
	port := m.callbackPort
	stdin := m.stdin
	m.mu.Unlock()

	lower := strings.ToLower(trimmed)
	looksURL := strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") || strings.Contains(lower, "://")
	if looksURL {
		targetURL, err := wscred.ValidateLoopbackCallbackReplay(trimmed, port)
		if err != nil {
			switch {
			case errors.Is(err, wscred.ErrSlotCallbackInvalidHost):
				accountError(w, http.StatusUnprocessableEntity, "callback_host_not_loopback")
			case errors.Is(err, wscred.ErrSlotCallbackPortMismatch):
				if port <= 0 {
					accountError(w, http.StatusConflict, "callback_not_ready")
				} else {
					accountError(w, http.StatusUnprocessableEntity, "callback_port_mismatch")
				}
			default:
				accountError(w, http.StatusUnprocessableEntity, "invalid_callback_url")
			}
			return
		}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, targetURL, nil)
		if err != nil {
			accountError(w, http.StatusUnprocessableEntity, "invalid_callback_url")
			return
		}
		resp, err := slotCallbackReplayClient.Do(req)
		if err != nil {
			accountError(w, http.StatusBadGateway, "callback_replay_failed")
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode >= 400 {
			accountError(w, http.StatusUnprocessableEntity, "callback_rejected")
			return
		}
		writeJSON(w, http.StatusAccepted, m.status())
		return
	}

	// Non-URL input is written directly to the login subprocess's open stdin
	// pipe without logging or echoing the code (D17 + D18).
	code, err := wscred.ValidateAuthCodeInput(trimmed)
	if err != nil {
		accountError(w, http.StatusUnprocessableEntity, "invalid_auth_code")
		return
	}
	if stdin == nil {
		accountError(w, http.StatusConflict, "stdin_not_ready")
		return
	}
	m.stdinMu.Lock()
	_, writeErr := io.WriteString(stdin, code+"\n")
	m.stdinMu.Unlock()
	if writeErr != nil {
		accountError(w, http.StatusConflict, "stdin_write_failed")
		return
	}
	writeJSON(w, http.StatusAccepted, m.status())
}

func (d *Daemon) handleAccountHarnessAuthCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Harness string `json:"harness"`
		Attempt string `json:"attempt"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	m := &d.harnessAuth
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "harness_auth_unavailable")
		return
	}
	if m.cancel == nil || body.Attempt == "" || body.Attempt != m.state.Attempt || (body.Harness != "" && body.Harness != m.state.Harness) {
		m.mu.Unlock()
		accountError(w, http.StatusConflict, "sign_in_changed")
		return
	}
	m.state.State = "cancelling"
	cancel := m.cancel
	resp := m.state
	m.mu.Unlock()

	cancel()
	writeJSON(w, http.StatusAccepted, resp)
}

func (d *Daemon) handleAccountHarnessAuthReverify(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Harness string `json:"harness"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	harness := strings.TrimSpace(body.Harness)
	provider, ok := d.findHarnessAuthProvider(harness)
	if !ok {
		accountError(w, http.StatusBadRequest, "harness_auth_unsupported")
		return
	}
	if provider.clearHoldAndReverify == nil || !provider.clearHoldAndReverify() {
		accountError(w, http.StatusServiceUnavailable, "cli_unavailable")
		return
	}
	d.accountChanged()
	snap := provider.state()
	if d.harnessAuthCmd != nil {
		snap.Available = true
	}
	snap.SignIn = d.harnessAuth.statusFor(provider.harness)
	writeJSON(w, http.StatusAccepted, snap)
}
