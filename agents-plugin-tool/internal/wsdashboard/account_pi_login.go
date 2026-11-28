package wsdashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"golang.org/x/net/websocket"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsholder"
)

const (
	piLoginHolderKind             = "pi-login"
	defaultPiLoginTimeout         = 15 * time.Minute
	defaultPiLoginIdleTimeout     = 5 * time.Minute
	defaultPiLoginDisconnectGrace = 5 * time.Second
	piLoginDefaultRows            = 24
	piLoginDefaultCols            = 80
)

var piLoginProviderRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

type piLoginHolderSession struct {
	Kind    string `json:"kind"`
	Attempt string `json:"attempt"`
	SlotID  string `json:"slot_id"`
	Label   string `json:"label,omitempty"`
}

type piLoginStatus struct {
	State     string `json:"state"`
	Attempt   string `json:"attempt,omitempty"`
	SlotID    string `json:"slot_id,omitempty"`
	Label     string `json:"label,omitempty"`
	Existing  bool   `json:"existing"`
	Provider  string `json:"provider"`
	WSPath    string `json:"ws_path,omitempty"`
	Reason    string `json:"reason,omitempty"`
	AuthReady *bool  `json:"auth_ready,omitempty"`
}

type piLoginController struct {
	daemon *Daemon

	// finalMu serializes attempt finalization (explicit complete, cancel,
	// child exit, timeouts, and daemon close). Lock order is strictly:
	// finalMu -> mu -> attempt.mu.
	finalMu sync.Mutex

	mu       sync.Mutex
	closed   bool
	starting bool
	active   *piLoginAttempt
	state    piLoginStatus
}

type piLoginAttempt struct {
	attempt         string
	holderID        string
	slotID          string
	label           string
	existing        bool
	provider        string
	dir             string
	releaseTempDeny func()
	host            TerminalHost

	opMu sync.Mutex

	mu        sync.Mutex
	viewers   map[*termViewer]struct{}
	cursor    uint64
	head      uint64
	replaying bool
	again     bool
	exited    bool
	exitCode  *int
	signal    *int
	rows      int
	cols      int
	active    *termViewer
	ended     bool
	finalized bool

	maxTimer   *time.Timer
	idleTimer  *time.Timer
	graceTimer *time.Timer
	idleDur    time.Duration
	graceDur   time.Duration
}

func (c *piLoginController) status() piLoginStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.State == "" {
		return piLoginStatus{State: "idle"}
	}
	return clonePiLoginStatus(c.state)
}

func (c *piLoginController) snapshot() *piLoginStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.state.State == "" {
		return nil
	}
	cp := clonePiLoginStatus(c.state)
	return &cp
}

func clonePiLoginStatus(st piLoginStatus) piLoginStatus {
	if st.AuthReady != nil {
		v := *st.AuthReady
		st.AuthReady = &v
	}
	return st
}

func (c *piLoginController) isRunning() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.starting || c.active != nil
}

func (c *piLoginController) activeByAttempt(attempt string) *piLoginAttempt {
	if attempt == "" {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active != nil && c.active.attempt == attempt {
		return c.active
	}
	return nil
}

func (c *piLoginController) stop() {
	c.finalMu.Lock()
	defer c.finalMu.Unlock()

	c.mu.Lock()
	c.closed = true
	att := c.active
	c.mu.Unlock()

	if att != nil && c.daemon != nil {
		c.daemon.finalizePiLoginLocked(att, "cancelled", "daemon_closed", nil, false, false)
	}
}

// HolderEvent implements wsholder.Sink for active ephemeral Pi PTY login holders.
// It runs on the holder's reader goroutine and never blocks on the holder.
func (c *piLoginController) HolderEvent(id string, ev wsholder.Event) {
	c.mu.Lock()
	att := c.active
	d := c.daemon
	c.mu.Unlock()
	if att == nil || att.holderID != id {
		return
	}
	if exitedNow := att.handleEvent(ev); exitedNow && d != nil {
		go d.handlePiLoginChildExit(att)
	}
}

func (d *Daemon) sweepOrphanPiLoginHolders() {
	if d.holders == nil {
		return
	}
	d.piLogin.mu.Lock()
	activeHolder := ""
	if d.piLogin.active != nil {
		activeHolder = d.piLogin.active.holderID
	}
	d.piLogin.mu.Unlock()

	listing := d.holders.List()
	for _, h := range listing.Holders {
		if h.ID == activeHolder || len(h.Session) == 0 {
			continue
		}
		var meta piLoginHolderSession
		if json.Unmarshal(h.Session, &meta) != nil || meta.Kind != piLoginHolderKind {
			continue
		}
		if err := d.holders.Stop(h.ID); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
			fmt.Fprintf(d.log, "dashboard: sweep orphan pi-login holder %s: %v\n", h.ID, err)
		} else {
			fmt.Fprintf(d.log, "dashboard: swept orphan pi-login holder %s\n", h.ID)
		}
	}
}

func (d *Daemon) piLoginBusyForSlot(harness, slotID string) bool {
	if harness != piRPCHarnessName {
		return false
	}
	norm := wscred.NormalizeAccountSlotID(slotID)
	d.piLogin.mu.Lock()
	defer d.piLogin.mu.Unlock()
	if d.piLogin.active == nil {
		return false
	}
	return wscred.NormalizeAccountSlotID(d.piLogin.active.slotID) == norm
}

func (d *Daemon) checkPiSlotAuthReady(dir string) bool {
	if d.slotAuthPreflight != nil {
		return d.slotAuthPreflight(piRPCHarnessName, dir) == nil
	}
	return wscred.SlotAuthReady(piRPCHarnessName, dir)
}

func (d *Daemon) piLoginTimeouts() (maxDur, idleDur, graceDur time.Duration) {
	maxDur = d.piLoginTimeout
	if maxDur <= 0 {
		maxDur = defaultPiLoginTimeout
	}
	idleDur = d.piLoginIdleTimeout
	if idleDur <= 0 {
		idleDur = defaultPiLoginIdleTimeout
	}
	graceDur = d.piLoginDisconnectGrace
	if graceDur <= 0 {
		graceDur = defaultPiLoginDisconnectGrace
	}
	return maxDur, idleDur, graceDur
}

func piLoginEnv(dir string) map[string]string {
	cleanDir := filepath.Clean(dir)
	base := os.Environ()
	out := make(map[string]string, len(base)+5)
	for _, kv := range base {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			continue
		}
		upper := strings.ToUpper(k)
		if strings.HasPrefix(upper, "PI_") ||
			strings.HasPrefix(upper, "CLAUDE") ||
			strings.HasPrefix(upper, "CODEX_") ||
			strings.HasPrefix(upper, "ORCA_") ||
			strings.HasPrefix(upper, "WS_PI_") ||
			upper == "TERM" ||
			upper == "COLORTERM" {
			continue
		}
		out[k] = v
	}
	out["PI_CODING_AGENT_DIR"] = cleanDir
	out["PI_SKIP_VERSION_CHECK"] = "1"
	out["PI_TELEMETRY"] = "0"
	out["TERM"] = "xterm-256color"
	out["COLORTERM"] = "truecolor"
	return out
}

func (d *Daemon) handleAccountPiLoginStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.piLogin.status())
}

func (d *Daemon) handleAccountPiLoginStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label    string `json:"label"`
		SlotID   string `json:"slot_id"`
		Existing bool   `json:"existing"`
		Provider string `json:"provider"`
		Rows     int    `json:"rows"`
		Cols     int    `json:"cols"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	if d.piRuntime == nil || !d.piRuntime.Status().Installed {
		accountError(w, http.StatusConflict, "runtime_not_installed")
		return
	}
	if d.accountSlots == nil {
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	host, ok := d.holders.(TerminalHost)
	if !ok || host == nil {
		accountError(w, http.StatusServiceUnavailable, "pty_unavailable")
		return
	}
	if d.piRuntimeState.isBusy() {
		accountError(w, http.StatusConflict, "runtime_busy")
		return
	}

	provider := strings.TrimSpace(body.Provider)
	if provider != "" && !piLoginProviderRe.MatchString(provider) {
		accountError(w, http.StatusBadRequest, "invalid_provider")
		return
	}
	label := strings.TrimSpace(body.Label)
	if utf8.RuneCountInString(label) > 80 {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	var slotID, dir string
	if !body.Existing {
		slotID = deriveSlotID(piRPCHarnessName, body.SlotID, label)
		if !wscred.ValidAccountSlotID(slotID) || slotID == wscred.DefaultAccountSlotID {
			accountError(w, http.StatusBadRequest, "invalid_slot_id")
			return
		}
		if label == "" {
			label = slotID
		}
		if _, exists := d.accountSlots.ResolveSlot(piRPCHarnessName, slotID); exists {
			accountError(w, http.StatusConflict, "slot_exists")
			return
		}
	} else {
		slotID = wscred.NormalizeAccountSlotID(body.SlotID)
		if slotID == wscred.DefaultAccountSlotID {
			explicitHome := d.home
			if d.planUsage != nil && d.planUsage.explicitHome != "" {
				explicitHome = d.planUsage.explicitHome
			}
			dir = wscred.ResolveHarnessHome(wscred.HarnessHomeOptions{
				Harness:      piRPCHarnessName,
				ExplicitHome: explicitHome,
			})
			if dir == "" {
				accountError(w, http.StatusInternalServerError, "slot_dir_failed")
				return
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				accountError(w, http.StatusInternalServerError, "slot_dir_failed")
				return
			}
			_ = wscred.RestrictPrivateDir(dir)
			if label == "" {
				label = wscred.DefaultAccountSlotID
			}
		} else {
			slot, exists := d.accountSlots.ResolveSlot(piRPCHarnessName, slotID)
			if !exists {
				accountError(w, http.StatusNotFound, "account_slot_not_found")
				return
			}
			dir = slot.ConfigDir
			if dir == "" {
				accountError(w, http.StatusInternalServerError, "slot_dir_failed")
				return
			}
			if err := os.MkdirAll(dir, 0o700); err != nil {
				accountError(w, http.StatusInternalServerError, "slot_dir_failed")
				return
			}
			if label == "" {
				label = slot.Label
				if label == "" {
					label = slot.ID
				}
			}
		}
	}

	var argv []string
	if d.piLoginCommand != nil {
		exe, args, err := d.piLoginCommand(provider)
		if err != nil || strings.TrimSpace(exe) == "" {
			accountError(w, http.StatusInternalServerError, "login_start_failed")
			return
		}
		argv = append([]string{exe}, args...)
	} else {
		binPath := d.piRuntime.Status().BinaryPath
		if binPath == "" {
			accountError(w, http.StatusConflict, "runtime_not_installed")
			return
		}
		if provider == "" {
			argv = []string{binPath, "--no-session", "-ne", "-ns", "-np", "--no-themes", "-na", "-nc"}
		} else {
			argv = []string{binPath, "auth", "login", provider}
		}
	}

	c := &d.piLogin
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "pty_unavailable")
		return
	}
	if c.starting || c.active != nil {
		c.mu.Unlock()
		accountError(w, http.StatusConflict, "login_in_progress")
		return
	}
	if d.piRuntimeState.isBusy() {
		c.mu.Unlock()
		accountError(w, http.StatusConflict, "runtime_busy")
		return
	}
	c.starting = true
	c.mu.Unlock()

	releaseStarting := true
	var releaseTempDeny func()
	var createdStagingDir string
	var launchedHolderID string
	defer func() {
		if !releaseStarting {
			return
		}
		c.mu.Lock()
		c.starting = false
		c.active = nil
		c.mu.Unlock()
		if launchedHolderID != "" {
			_ = host.Stop(launchedHolderID)
		}
		if releaseTempDeny != nil {
			releaseTempDeny()
		}
		if createdStagingDir != "" {
			_ = os.RemoveAll(createdStagingDir)
		}
	}()

	if !body.Existing {
		var err error
		dir, err = wscred.CreateManagedSlotDirUnder(d.dir, piRPCHarnessName, slotID)
		if err != nil {
			accountError(w, http.StatusInternalServerError, "slot_dir_failed")
			return
		}
		createdStagingDir = dir
		releaseTempDeny = wscred.RegisterTempSlotDir(dir)
	}

	rows, cols := clampTerminalSize(body.Rows, body.Cols, piLoginDefaultRows, piLoginDefaultCols)
	attemptID := uuid.NewString()
	sessionMeta, err := json.Marshal(piLoginHolderSession{
		Kind:    piLoginHolderKind,
		Attempt: attemptID,
		SlotID:  slotID,
		Label:   "pi-login: " + label,
	})
	if err != nil {
		accountError(w, http.StatusInternalServerError, "login_start_failed")
		return
	}
	holderID, err := host.Launch(sessionMeta)
	if err != nil {
		accountError(w, http.StatusInternalServerError, "login_start_failed")
		return
	}
	launchedHolderID = holderID
	conn := host.Client(holderID)
	if conn == nil {
		accountError(w, http.StatusInternalServerError, "login_start_failed")
		return
	}

	maxDur, idleDur, graceDur := d.piLoginTimeouts()
	att := &piLoginAttempt{
		attempt:         attemptID,
		holderID:        holderID,
		slotID:          slotID,
		label:           label,
		existing:        body.Existing,
		provider:        provider,
		dir:             filepath.Clean(dir),
		releaseTempDeny: releaseTempDeny,
		host:            host,
		viewers:         map[*termViewer]struct{}{},
		rows:            rows,
		cols:            cols,
		idleDur:         idleDur,
		graceDur:        graceDur,
	}

	// Install active attempt BEFORE Spawn so HolderEvent captures even immediate output/exit.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "pty_unavailable")
		return
	}
	c.active = att
	c.mu.Unlock()

	if _, err := conn.Spawn(wsholder.SpawnSpec{
		Argv: argv,
		Cwd:  att.dir,
		Env:  piLoginEnv(att.dir),
		Mode: "pty",
		Rows: rows,
		Cols: cols,
	}); err != nil {
		accountError(w, http.StatusInternalServerError, "login_start_failed")
		return
	}

	att.mu.Lock()
	if !att.finalized {
		att.maxTimer = time.AfterFunc(maxDur, func() {
			d.onPiLoginTimeout(att, "timed_out")
		})
		att.idleTimer = time.AfterFunc(idleDur, func() {
			d.onPiLoginTimeout(att, "idle_timeout")
		})
	}
	att.mu.Unlock()

	st := piLoginStatus{
		State:    "running",
		Attempt:  attemptID,
		SlotID:   slotID,
		Label:    label,
		Existing: body.Existing,
		Provider: provider,
		WSPath:   "/api/account/pi/login/ws?attempt=" + attemptID,
	}
	c.mu.Lock()
	c.starting = false
	if c.active == att {
		c.state = st
	}
	resp := clonePiLoginStatus(c.state)
	c.mu.Unlock()

	releaseStarting = false
	d.accountChanged()
	writeJSON(w, http.StatusOK, resp)
}

func decodeOptionalPiAttempt(w http.ResponseWriter, r *http.Request) (string, bool) {
	if r.Body == nil {
		return "", true
	}
	var body struct {
		Attempt string `json:"attempt"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	err := dec.Decode(&body)
	if errors.Is(err, io.EOF) {
		return "", true
	}
	if err != nil || dec.Decode(new(any)) != io.EOF {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return "", false
	}
	return strings.TrimSpace(body.Attempt), true
}

func (d *Daemon) handleAccountPiLoginComplete(w http.ResponseWriter, r *http.Request) {
	attempt, ok := decodeOptionalPiAttempt(w, r)
	if !ok {
		return
	}

	c := &d.piLogin
	c.finalMu.Lock()
	defer c.finalMu.Unlock()

	c.mu.Lock()
	att := c.active
	st := clonePiLoginStatus(c.state)
	c.mu.Unlock()

	if att == nil {
		if attempt != "" && st.Attempt == attempt && st.State == "succeeded" {
			writeJSON(w, http.StatusOK, st)
			return
		}
		accountError(w, http.StatusConflict, "login_not_active")
		return
	}
	if attempt != "" && att.attempt != attempt {
		accountError(w, http.StatusConflict, "login_changed")
		return
	}

	ready := d.checkPiSlotAuthReady(att.dir)
	att.mu.Lock()
	exited := att.exited
	att.mu.Unlock()

	if !att.existing && !ready {
		if exited {
			d.finalizePiLoginLocked(att, "failed", "auth_not_ready", &ready, false, true)
		}
		accountError(w, http.StatusUnprocessableEntity, "auth_not_ready")
		return
	}

	if !att.existing {
		if _, err := d.accountSlots.AddSlot(wscred.AccountSlot{
			ID:        att.slotID,
			Harness:   piRPCHarnessName,
			Label:     att.label,
			ConfigDir: filepath.Clean(att.dir),
			Managed:   true,
		}); err != nil {
			d.finalizePiLoginLocked(att, "failed", "slot_register_failed", &ready, false, true)
			accountSlotError(w, err)
			return
		}
	}

	d.notifyPiRuntimeChanged()
	finalStatus := d.finalizePiLoginLocked(att, "succeeded", "", &ready, true, true)
	writeJSON(w, http.StatusOK, finalStatus)
}

func (d *Daemon) handleAccountPiLoginCancel(w http.ResponseWriter, r *http.Request) {
	attempt, ok := decodeOptionalPiAttempt(w, r)
	if !ok {
		return
	}

	c := &d.piLogin
	c.finalMu.Lock()
	defer c.finalMu.Unlock()

	c.mu.Lock()
	att := c.active
	st := clonePiLoginStatus(c.state)
	c.mu.Unlock()

	if att == nil {
		if attempt != "" && st.Attempt != "" && st.Attempt != attempt {
			accountError(w, http.StatusConflict, "login_changed")
			return
		}
		if st.State == "" {
			st.State = "idle"
		}
		writeJSON(w, http.StatusOK, st)
		return
	}
	if attempt != "" && att.attempt != attempt {
		accountError(w, http.StatusConflict, "login_changed")
		return
	}

	finalStatus := d.finalizePiLoginLocked(att, "cancelled", "", nil, false, true)
	writeJSON(w, http.StatusOK, finalStatus)
}

func (d *Daemon) handlePiLoginChildExit(att *piLoginAttempt) {
	c := &d.piLogin
	c.finalMu.Lock()
	defer c.finalMu.Unlock()

	c.mu.Lock()
	if c.active != att {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	ready := d.checkPiSlotAuthReady(att.dir)
	if att.existing {
		d.notifyPiRuntimeChanged()
		d.finalizePiLoginLocked(att, "succeeded", "", &ready, true, true)
		return
	}
	if !ready {
		d.finalizePiLoginLocked(att, "failed", "auth_not_ready", &ready, false, true)
		return
	}
	if _, err := d.accountSlots.AddSlot(wscred.AccountSlot{
		ID:        att.slotID,
		Harness:   piRPCHarnessName,
		Label:     att.label,
		ConfigDir: filepath.Clean(att.dir),
		Managed:   true,
	}); err != nil {
		d.finalizePiLoginLocked(att, "failed", "slot_register_failed", &ready, false, true)
		return
	}
	d.notifyPiRuntimeChanged()
	d.finalizePiLoginLocked(att, "succeeded", "", &ready, true, true)
}

func (d *Daemon) onPiLoginTimeout(att *piLoginAttempt, reason string) {
	c := &d.piLogin
	c.finalMu.Lock()
	defer c.finalMu.Unlock()

	c.mu.Lock()
	if c.active != att {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	d.finalizePiLoginLocked(att, "failed", reason, nil, false, true)
}

func (d *Daemon) onPiLoginDisconnectTimeout(att *piLoginAttempt) {
	c := &d.piLogin
	c.finalMu.Lock()
	defer c.finalMu.Unlock()

	c.mu.Lock()
	if c.active != att {
		c.mu.Unlock()
		return
	}
	c.mu.Unlock()

	att.mu.Lock()
	stillEmpty := len(att.viewers) == 0 && !att.finalized && !att.exited
	att.mu.Unlock()
	if !stillEmpty {
		return
	}

	d.finalizePiLoginLocked(att, "cancelled", "disconnected", nil, false, true)
}

// finalizePiLoginLocked transitions att out of active state, stops its timers
// and holder, closes WebSocket viewers, releases any temp deny registration,
// removes uncommitted staging directories, and updates status. Caller holds
// d.piLogin.finalMu.
func (d *Daemon) finalizePiLoginLocked(att *piLoginAttempt, state, reason string, authReady *bool, keepDir, emitChange bool) piLoginStatus {
	c := &d.piLogin
	c.mu.Lock()
	if c.active != att {
		st := clonePiLoginStatus(c.state)
		c.mu.Unlock()
		return st
	}
	c.mu.Unlock()

	att.stopTimersAndEnd()

	if att.host != nil && att.holderID != "" {
		if err := att.host.Stop(att.holderID); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
			fmt.Fprintf(d.log, "dashboard: stop pi-login holder %s: %v\n", att.holderID, err)
		}
	}
	if att.releaseTempDeny != nil {
		att.releaseTempDeny()
	}
	if !att.existing && !keepDir && att.dir != "" {
		managedRoot := filepath.Join(filepath.Clean(d.dir), "slots", piRPCHarnessName)
		if withinDir(att.dir, managedRoot) {
			_ = os.RemoveAll(att.dir)
		}
	}

	var readyCopy *bool
	if authReady != nil {
		v := *authReady
		readyCopy = &v
	}
	finalStatus := piLoginStatus{
		State:     state,
		Attempt:   att.attempt,
		SlotID:    att.slotID,
		Label:     att.label,
		Existing:  att.existing,
		Provider:  att.provider,
		Reason:    reason,
		AuthReady: readyCopy,
	}
	c.mu.Lock()
	if c.active == att {
		c.active = nil
		c.state = finalStatus
	}
	c.mu.Unlock()
	if emitChange {
		d.accountChanged()
	}
	return clonePiLoginStatus(finalStatus)
}

func (d *Daemon) handleAccountPiLoginWS(w http.ResponseWriter, r *http.Request) {
	attempt := strings.TrimSpace(r.URL.Query().Get("attempt"))
	if attempt == "" {
		accountError(w, http.StatusNotFound, "login_not_found")
		return
	}
	from, err := strconv.ParseUint(r.URL.Query().Get("from"), 10, 64)
	if err != nil && r.URL.Query().Get("from") != "" {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	att := d.piLogin.activeByAttempt(attempt)
	if att == nil {
		accountError(w, http.StatusNotFound, "login_not_found")
		return
	}
	if _, ok := w.(http.Hijacker); !ok {
		accountError(w, http.StatusInternalServerError, "streaming_unsupported")
		return
	}

	window := wsReadWindow * d.keepAlive
	websocket.Server{Handler: func(ws *websocket.Conn) {
		ws.MaxPayloadBytes = terminalFrameMax
		v := att.join(from)
		if v == nil {
			_ = websocket.Message.Send(ws, `{"t":"closed"}`)
			_ = ws.Close()
			return
		}
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			for {
				_ = ws.SetReadDeadline(time.Now().Add(window))
				var raw string
				if err := websocket.Message.Receive(ws, &raw); err != nil {
					return
				}
				var in termInput
				if json.Unmarshal([]byte(raw), &in) != nil {
					continue
				}
				switch in.T {
				case "in":
					data := in.Bin
					if in.Data != "" {
						data = []byte(in.Data)
					}
					att.resetIdle()
					if errors.Is(att.input(v, data), errTerminalLost) {
						return
					}
				case "size":
					if in.Rows > 0 && in.Cols > 0 {
						att.resize(v, in.Rows, in.Cols)
					}
				case "focus":
					att.focus(v)
				}
			}
		}()
		keepAlive := time.NewTicker(d.keepAlive)
		defer keepAlive.Stop()
		send := func(frame string) error {
			_ = ws.SetWriteDeadline(time.Now().Add(wsWriteTimeout))
			return websocket.Message.Send(ws, frame)
		}
	write:
		for {
			select {
			case <-v.wake:
				frames, last := v.take()
				for _, frame := range frames {
					if send(string(frame)) != nil {
						break write
					}
				}
				if last {
					break write
				}
			case <-keepAlive.C:
				if send(`{"t":"ping"}`) != nil {
					break write
				}
			case <-v.gone:
				break write
			case <-readDone:
				break write
			case <-d.stop:
				break write
			}
		}
		att.leave(v, func() {
			d.onPiLoginDisconnectTimeout(att)
		})
		_ = ws.Close()
		<-readDone
	}}.ServeHTTP(w, r)
}

func (att *piLoginAttempt) handleEvent(ev wsholder.Event) (exitedNow bool) {
	att.mu.Lock()
	defer att.mu.Unlock()
	if att.ended {
		return false
	}
	switch ev.Event {
	case "output", "exit":
		if ev.Event == "output" && att.idleTimer != nil && att.idleDur > 0 {
			att.idleTimer.Reset(att.idleDur)
		}
		frame := termFrame{T: "out", Seq: ev.Seq, Data: ev.Data}
		if ev.Event == "exit" {
			frame = termFrame{T: "exit", Seq: ev.Seq, ExitCode: ev.ExitCode, Signal: ev.Signal}
			if !att.exited {
				att.exited, att.exitCode, att.signal = true, ev.ExitCode, ev.Signal
				exitedNow = true
			}
		}
		var raw []byte
		lag := false
		for v := range att.viewers {
			switch {
			case ev.Seq == v.next:
				if raw == nil {
					raw = frame.encode()
				}
				v.next++
				v.fresh = false
				v.push(raw)
			case ev.Seq > v.next:
				lag = true
			}
		}
		att.cursor = ev.Seq + 1
		if att.cursor > att.head {
			att.head = att.cursor
		}
		if lag && !att.replaying {
			att.replayLocked()
		}
	case "gap":
		lag := false
		for v := range att.viewers {
			switch {
			case v.next < ev.From:
				lag = true
			case v.next < ev.To:
				if !v.fresh {
					v.push(termFrame{T: "gap", From: v.next, To: ev.To}.encode())
				}
				v.next = ev.To
				v.fresh = false
			}
		}
		att.cursor = ev.To
		if att.cursor > att.head {
			att.head = att.cursor
		}
		if lag && !att.replaying {
			att.replayLocked()
		}
	}
	return exitedNow
}

func (att *piLoginAttempt) replayLocked() {
	if att.replaying {
		att.again = true
		return
	}
	att.replaying = true
	go att.replay()
}

func (att *piLoginAttempt) replay() {
	for {
		att.mu.Lock()
		att.again = false
		low, ok := att.lowLocked()
		if !ok || low >= att.cursor || att.ended {
			att.replaying = false
			att.mu.Unlock()
			return
		}
		att.mu.Unlock()
		err := errTerminalLost
		if conn := att.host.Client(att.holderID); conn != nil {
			_, _, err = conn.Replay(low)
		}
		att.mu.Lock()
		if err != nil {
			for v := range att.viewers {
				if v.next < att.cursor {
					v.drop()
				}
			}
			att.replaying = false
			att.mu.Unlock()
			return
		}
		if !att.again {
			att.replaying = false
			att.mu.Unlock()
			return
		}
		att.mu.Unlock()
	}
}

func (att *piLoginAttempt) lowLocked() (uint64, bool) {
	low, ok := uint64(0), false
	for v := range att.viewers {
		if !ok || v.next < low {
			low, ok = v.next, true
		}
	}
	return low, ok
}

func (att *piLoginAttempt) join(from uint64) *termViewer {
	v := &termViewer{next: from, wake: make(chan struct{}, 1), gone: make(chan struct{})}
	if from == 0 {
		v.next, v.fresh = 1, true
	}
	att.mu.Lock()
	defer att.mu.Unlock()
	if att.ended || att.finalized {
		return nil
	}
	if att.graceTimer != nil {
		att.graceTimer.Stop()
		att.graceTimer = nil
	}
	hello := termFrame{T: "hello", State: "running", Next: att.head, Rows: att.rows, Cols: att.cols}
	if att.exited {
		hello.State, hello.ExitCode, hello.Signal = "exited", att.exitCode, att.signal
	}
	v.push(hello.encode())
	att.viewers[v] = struct{}{}
	if v.next < att.cursor || (att.replaying && v.next < att.head) {
		att.replayLocked()
	}
	return v
}

func (att *piLoginAttempt) leave(v *termViewer, onGrace func()) {
	att.mu.Lock()
	delete(att.viewers, v)
	if att.active == v {
		att.active = nil
	}
	if len(att.viewers) == 0 && !att.ended && !att.finalized && !att.exited && onGrace != nil {
		if att.graceTimer != nil {
			att.graceTimer.Stop()
		}
		att.graceTimer = time.AfterFunc(att.graceDur, onGrace)
	}
	att.mu.Unlock()
	v.drop()
}

func (att *piLoginAttempt) resetIdle() {
	att.mu.Lock()
	if att.idleTimer != nil && att.idleDur > 0 && !att.finalized {
		att.idleTimer.Reset(att.idleDur)
	}
	att.mu.Unlock()
}

func (att *piLoginAttempt) stopTimersAndEnd() {
	att.mu.Lock()
	att.finalized = true
	att.ended = true
	if att.maxTimer != nil {
		att.maxTimer.Stop()
		att.maxTimer = nil
	}
	if att.idleTimer != nil {
		att.idleTimer.Stop()
		att.idleTimer = nil
	}
	if att.graceTimer != nil {
		att.graceTimer.Stop()
		att.graceTimer = nil
	}
	closed := termFrame{T: "closed"}.encode()
	for v := range att.viewers {
		v.push(closed)
		v.finish()
	}
	att.mu.Unlock()
}

func (att *piLoginAttempt) input(v *termViewer, data []byte) error {
	att.opMu.Lock()
	defer att.opMu.Unlock()
	att.activateLocked(v)
	if len(data) == 0 {
		return nil
	}
	conn := att.host.Client(att.holderID)
	if conn == nil {
		return errTerminalLost
	}
	err := conn.Input(data, false)
	if err != nil {
		select {
		case <-conn.Done():
			return errTerminalLost
		default:
		}
	}
	return err
}

func (att *piLoginAttempt) focus(v *termViewer) {
	att.opMu.Lock()
	defer att.opMu.Unlock()
	att.activateLocked(v)
}

func (att *piLoginAttempt) resize(v *termViewer, rows, cols int) {
	rows, cols = clampTerminalSize(rows, cols, 0, 0)
	att.opMu.Lock()
	defer att.opMu.Unlock()
	att.mu.Lock()
	v.rows, v.cols = rows, cols
	apply := att.active == nil || att.active == v
	att.mu.Unlock()
	if apply {
		att.applySizeLocked(rows, cols)
	}
}

func (att *piLoginAttempt) activateLocked(v *termViewer) {
	att.mu.Lock()
	att.active = v
	rows, cols := v.rows, v.cols
	att.mu.Unlock()
	if rows > 0 && cols > 0 {
		att.applySizeLocked(rows, cols)
	}
}

func (att *piLoginAttempt) applySizeLocked(rows, cols int) {
	att.mu.Lock()
	same := (rows == att.rows && cols == att.cols) || att.exited || att.ended
	att.mu.Unlock()
	if same {
		return
	}
	conn := att.host.Client(att.holderID)
	if conn == nil || conn.Resize(rows, cols) != nil {
		return
	}
	att.mu.Lock()
	att.rows, att.cols = rows, cols
	att.mu.Unlock()
}
