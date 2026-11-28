package wsdashboard

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/kang-sw/devenv/internal/wscred"
)

const defaultSlotSignInTimeout = 5 * time.Minute

// accountSlotSignIn tracks one in-progress harness CLI slot sign-in across
// HTTP requests (D14 + D18).
type accountSlotSignIn struct {
	mu           sync.Mutex
	stdinMu      sync.Mutex
	state        accountSlotSignInStatus
	cancel       context.CancelFunc
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	callbackPort int
	closed       bool
}

type accountSlotSignInStatus struct {
	State         string `json:"state"`
	Attempt       string `json:"attempt,omitempty"`
	Harness       string `json:"harness,omitempty"`
	SlotID        string `json:"slot_id,omitempty"`
	Label         string `json:"label,omitempty"`
	Mode          string `json:"mode,omitempty"`
	Reason        string `json:"reason,omitempty"`
	AuthorizeURL  string `json:"authorize_url,omitempty"`
	VerifyURL     string `json:"verify_url,omitempty"`
	UserCode      string `json:"user_code,omitempty"`
	CallbackReady bool   `json:"callback_ready,omitempty"`
	StdinReady    bool   `json:"stdin_ready,omitempty"`
	BrowserError  bool   `json:"browser_error,omitempty"`
}

func (s *accountSlotSignIn) status() accountSlotSignInStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.State == "" {
		return accountSlotSignInStatus{State: "idle"}
	}
	return s.state
}

func (s *accountSlotSignIn) stop() {
	s.mu.Lock()
	s.closed = true
	cancel := s.cancel
	stdin := s.stdin
	s.stdin = nil
	s.mu.Unlock()
	if stdin != nil {
		s.stdinMu.Lock()
		_ = stdin.Close()
		s.stdinMu.Unlock()
	}
	// Cancelling the context kills the process tree through cmd.Cancel.
	if cancel != nil {
		cancel()
	}
}

var slotSlugCleanRe = regexp.MustCompile(`[^a-z0-9_-]+`)

func deriveSlotID(harness, rawID, label string) string {
	id := strings.TrimSpace(strings.ToLower(rawID))
	if id != "" {
		return id
	}
	base := strings.TrimSpace(strings.ToLower(label))
	base = slotSlugCleanRe.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-_")
	if len(base) > 32 {
		base = base[:32]
	}
	suffix := uuid.NewString()[:8]
	if base != "" && base != wscred.DefaultAccountSlotID {
		cand := base + "-" + suffix
		if wscred.ValidAccountSlotID(cand) {
			return cand
		}
	}
	return harness + "-" + suffix
}

func replaceEnvVar(env []string, key, value string) []string {
	prefix := key + "="
	out := make([]string, 0, len(env)+1)
	replaced := false
	for _, entry := range env {
		if strings.HasPrefix(entry, prefix) {
			if !replaced {
				out = append(out, prefix+value)
				replaced = true
			}
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, prefix+value)
	}
	return out
}

func (d *Daemon) handleAccountSlotSignIn(w http.ResponseWriter, r *http.Request, action string) {
	s := &d.slotSignIn
	if action == "slots/sign-in/status" {
		writeJSON(w, http.StatusOK, s.status())
		return
	}
	if d.accountSlots == nil {
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	switch action {
	case "slots/sign-in/cancel":
		d.handleAccountSlotSignInCancel(w, r)
	case "slots/sign-in/complete":
		d.handleAccountSlotSignInComplete(w, r)
	case "slots/sign-in/start":
		d.handleAccountSlotSignInStart(w, r)
	default:
		accountError(w, http.StatusNotFound, "not_found")
	}
}

func (d *Daemon) handleAccountSlotSignInStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Harness  string `json:"harness"`
		SlotID   string `json:"slot_id"`
		Label    string `json:"label"`
		Mode     string `json:"mode"`
		CopyOnly bool   `json:"copy_only"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	harness := strings.TrimSpace(body.Harness)
	mode := wscred.SlotLoginMode(strings.TrimSpace(body.Mode))
	if mode == "" {
		mode = wscred.SlotLoginModeBrowser
	}
	defaultCmd, defaultArgs, envKey, err := wscred.SlotLoginCommandSpec(harness, mode)
	if err != nil {
		accountError(w, http.StatusBadRequest, "account_slot_unsupported")
		return
	}
	slotID := deriveSlotID(harness, body.SlotID, body.Label)
	if !wscred.ValidAccountSlotID(slotID) || slotID == wscred.DefaultAccountSlotID {
		accountError(w, http.StatusBadRequest, "invalid_slot_id")
		return
	}
	label := strings.TrimSpace(body.Label)
	if label == "" {
		label = slotID
	}
	if utf8.RuneCountInString(label) > 80 {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, exists := d.accountSlots.ResolveSlot(harness, slotID); exists {
		accountError(w, http.StatusConflict, "slot_exists")
		return
	}

	var exe string
	var args []string
	if d.slotLoginCmd != nil {
		exe, args, err = d.slotLoginCmd(harness, mode)
		if err != nil {
			accountError(w, http.StatusServiceUnavailable, "cli_unavailable")
			return
		}
	} else {
		exe, err = exec.LookPath(defaultCmd)
		if err != nil {
			accountError(w, http.StatusServiceUnavailable, "cli_unavailable")
			return
		}
		args = defaultArgs
	}

	s := &d.slotSignIn
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	if s.cancel != nil {
		s.mu.Unlock()
		accountError(w, http.StatusConflict, "sign_in_in_progress")
		return
	}

	slotDir, err := wscred.CreateManagedSlotDirUnder(d.dir, harness, slotID)
	if err != nil {
		s.mu.Unlock()
		accountError(w, http.StatusInternalServerError, "slot_dir_failed")
		return
	}
	releaseTempDeny := wscred.RegisterTempSlotDir(slotDir)

	timeout := d.slotSignInTimeout
	if timeout <= 0 {
		timeout = defaultSlotSignInTimeout
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	attempt := uuid.NewString()
	s.cancel = cancel
	s.cmd = nil
	s.callbackPort = 0
	s.state = accountSlotSignInStatus{
		State:   "starting",
		Attempt: attempt,
		Harness: harness,
		SlotID:  slotID,
		Label:   label,
		Mode:    string(mode),
	}
	resp := s.state
	s.mu.Unlock()

	openFn := d.openSystem
	if body.CopyOnly {
		openFn = nil
	}
	go d.runSlotSignIn(ctx, attempt, harness, slotID, label, mode, envKey, slotDir, exe, args, openFn, releaseTempDeny)
	writeJSON(w, http.StatusAccepted, resp)
}

func (d *Daemon) runSlotSignIn(
	ctx context.Context,
	attempt, harness, slotID, label string,
	mode wscred.SlotLoginMode,
	envKey, slotDir, exe string,
	args []string,
	openBrowser func(string) error,
	releaseTempDeny func(),
) {
	s := &d.slotSignIn
	defer releaseTempDeny()

	registered := false
	defer func() {
		if !registered {
			_ = os.RemoveAll(slotDir)
		}
	}()

	// CommandContext guarantees Cancel never fires after Wait has reaped the
	// child, so the process-group kill can never hit a recycled PID/PGID.
	cmd := exec.CommandContext(ctx, exe, args...)
	configureSlotLoginCmd(cmd)
	cmd.Cancel = func() error {
		killSlotLoginProcessTree(cmd)
		return nil
	}
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = slotDir
	cmd.Env = replaceEnvVar(os.Environ(), envKey, slotDir)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		d.finishSlotSignIn(attempt, "failed", "login_failed")
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		d.finishSlotSignIn(attempt, "failed", "login_failed")
		return
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		d.finishSlotSignIn(attempt, "failed", "login_failed")
		return
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		// CommandContext refuses to start once ctx is done; keep the cancel and
		// timeout outcomes distinct from a missing CLI.
		if ctxErr := ctx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) {
				d.finishSlotSignIn(attempt, "failed", "timed_out")
			} else {
				d.finishSlotSignIn(attempt, "cancelled", "")
			}
			return
		}
		d.finishSlotSignIn(attempt, "failed", "cli_unavailable")
		return
	}

	s.mu.Lock()
	if s.state.Attempt == attempt {
		s.cmd = cmd
		s.stdin = stdin
		s.state.StdinReady = true
		if s.state.State == "starting" {
			s.state.State = "waiting"
		}
	} else {
		_ = stdin.Close()
	}
	s.mu.Unlock()

	var openOnce sync.Once
	scanStream := func(r io.Reader) {
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 8192), 64*1024)
		for scanner.Scan() {
			authURL, verifyURL, userCode, cbPort := wscred.ParseSlotLoginLine(scanner.Text())
			if authURL == "" && verifyURL == "" && userCode == "" && cbPort == 0 {
				continue
			}
			var browserTarget string
			s.mu.Lock()
			if s.state.Attempt == attempt {
				if authURL != "" {
					s.state.AuthorizeURL = authURL
				}
				if verifyURL != "" {
					s.state.VerifyURL = verifyURL
					if mode == wscred.SlotLoginModeDeviceAuth && openBrowser != nil {
						browserTarget = verifyURL
					}
				}
				if userCode != "" {
					s.state.UserCode = userCode
				}
				if cbPort > 0 {
					s.callbackPort = cbPort
					s.state.CallbackReady = true
				}
				if s.state.State == "starting" {
					s.state.State = "waiting"
				}
			}
			s.mu.Unlock()
			if browserTarget != "" {
				openOnce.Do(func() {
					if openBrowser(browserTarget) != nil {
						s.mu.Lock()
						if s.state.Attempt == attempt {
							s.state.BrowserError = true
						}
						s.mu.Unlock()
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
			d.finishSlotSignIn(attempt, "failed", "timed_out")
		} else {
			d.finishSlotSignIn(attempt, "cancelled", "")
		}
		return
	}
	if waitErr != nil {
		d.finishSlotSignIn(attempt, "failed", "login_failed")
		return
	}
	if !wscred.SlotAuthReady(harness, slotDir) {
		d.finishSlotSignIn(attempt, "failed", "auth_not_ready")
		return
	}
	if d.slotAuthPreflight != nil {
		if err := d.slotAuthPreflight(harness, slotDir); err != nil {
			d.finishSlotSignIn(attempt, "failed", "auth_not_ready")
			return
		}
	}
	if _, err := d.accountSlots.AddSlot(wscred.AccountSlot{
		ID:        slotID,
		Harness:   harness,
		Label:     label,
		ConfigDir: filepath.Clean(slotDir),
		Managed:   true,
	}); err != nil {
		d.finishSlotSignIn(attempt, "failed", "slot_register_failed")
		return
	}
	registered = true
	d.finishSlotSignIn(attempt, "succeeded", "")
	d.accountChanged()
}

func (d *Daemon) finishSlotSignIn(attempt, state, reason string) {
	s := &d.slotSignIn
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.Attempt != attempt {
		return
	}
	// Clear auth URLs and one-time codes on completion/failure/cancel (D18).
	s.state.State = state
	s.state.Reason = reason
	s.state.AuthorizeURL = ""
	s.state.VerifyURL = ""
	s.state.UserCode = ""
	s.state.CallbackReady = false
	s.state.StdinReady = false
	s.state.BrowserError = false
	s.callbackPort = 0
	s.cmd = nil
	stdin := s.stdin
	s.stdin = nil
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if stdin != nil {
		s.stdinMu.Lock()
		_ = stdin.Close()
		s.stdinMu.Unlock()
	}
}

func (d *Daemon) handleAccountSlotSignInCancel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Attempt string `json:"attempt"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	s := &d.slotSignIn
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	if s.cancel == nil || body.Attempt == "" || body.Attempt != s.state.Attempt {
		s.mu.Unlock()
		accountError(w, http.StatusConflict, "sign_in_changed")
		return
	}
	s.state.State = "cancelling"
	cancel := s.cancel
	resp := s.state
	s.mu.Unlock()

	cancel()
	writeJSON(w, http.StatusAccepted, resp)
}

var slotCallbackReplayClient = &http.Client{
	Timeout: 10 * time.Second,
	Transport: &http.Transport{
		// Never route loopback callback replays through HTTP_PROXY.
		Proxy: nil,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func (d *Daemon) handleAccountSlotSignInComplete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Attempt string `json:"attempt"`
		Input   string `json:"input"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	if body.Attempt == "" || strings.TrimSpace(body.Input) == "" {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	s := &d.slotSignIn
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	if s.cancel == nil || body.Attempt != s.state.Attempt {
		s.mu.Unlock()
		accountError(w, http.StatusConflict, "sign_in_changed")
		return
	}
	port := s.callbackPort
	mode := s.state.Mode
	stdin := s.stdin
	s.mu.Unlock()

	if mode != string(wscred.SlotLoginModeBrowser) {
		accountError(w, http.StatusConflict, "callback_not_ready")
		return
	}
	trimmed := strings.TrimSpace(body.Input)
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
		writeJSON(w, http.StatusAccepted, s.status())
		return
	}

	// Non-URL input is delivered as a raw pasted authorization code over the
	// login subprocess's open stdin pipe without logging or echoing it (D18).
	code, err := wscred.ValidateAuthCodeInput(trimmed)
	if err != nil {
		accountError(w, http.StatusUnprocessableEntity, "invalid_auth_code")
		return
	}
	if stdin == nil {
		accountError(w, http.StatusConflict, "stdin_not_ready")
		return
	}
	s.stdinMu.Lock()
	_, writeErr := io.WriteString(stdin, code+"\n")
	s.stdinMu.Unlock()
	if writeErr != nil {
		accountError(w, http.StatusConflict, "stdin_write_failed")
		return
	}
	writeJSON(w, http.StatusAccepted, s.status())
}
