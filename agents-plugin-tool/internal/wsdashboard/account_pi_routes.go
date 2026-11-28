package wsdashboard

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wspiruntime"
)

const piRPCHarnessName = "pi-rpc"

const piInstallProgressEmitInterval = 250 * time.Millisecond

type piRuntimeProgressView struct {
	Phase      string `json:"phase"`
	Downloaded int64  `json:"downloaded,omitempty"`
	Total      int64  `json:"total,omitempty"`
}

type piRuntimeStatusView struct {
	Installed  bool                   `json:"installed"`
	Version    string                 `json:"version"`
	BinaryPath string                 `json:"binary_path"`
	InstallDir string                 `json:"install_dir"`
	Installing bool                   `json:"installing"`
	Progress   *piRuntimeProgressView `json:"progress,omitempty"`
	Error      string                 `json:"error,omitempty"`
}

type piRuntimeController struct {
	mu           sync.Mutex
	installing   bool
	uninstalling bool
	cancel       context.CancelFunc
	progress     *piRuntimeProgressView
	lastErr      string
	closed       bool
}

func (c *piRuntimeController) stop() {
	c.mu.Lock()
	c.closed = true
	cancel := c.cancel
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *piRuntimeController) isBusy() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.installing || c.uninstalling
}

func (d *Daemon) notifyPiRuntimeChanged() {
	if d.onPiRuntimeChanged != nil {
		d.onPiRuntimeChanged()
	}
}

func (d *Daemon) piRuntimeStatus() *piRuntimeStatusView {
	if d.piRuntime == nil {
		return nil
	}
	st := d.piRuntime.Status()
	d.piRuntimeState.mu.Lock()
	installing := st.Installing || d.piRuntimeState.installing
	var prog *piRuntimeProgressView
	if installing && d.piRuntimeState.progress != nil {
		cp := *d.piRuntimeState.progress
		prog = &cp
	}
	errMsg := d.piRuntimeState.lastErr
	d.piRuntimeState.mu.Unlock()
	if errMsg == "" {
		errMsg = st.Error
	}
	return &piRuntimeStatusView{
		Installed:  st.Installed,
		Version:    st.Version,
		BinaryPath: st.BinaryPath,
		InstallDir: st.InstallDir,
		Installing: installing,
		Progress:   prog,
		Error:      errMsg,
	}
}

func (d *Daemon) populatePiAccountStatus(status *accountStatus) {
	if d.piRuntime != nil {
		status.PiRuntime = d.piRuntimeStatus()
		if d.accountSlots != nil {
			if status.Slots == nil {
				status.Slots = map[string][]wscred.AccountSlotView{}
			}
			status.Slots[piRPCHarnessName] = d.accountSlots.Slots(piRPCHarnessName)
		}
	}
	if login := d.piLogin.snapshot(); login != nil {
		status.PiLogin = login
	}
}

func (d *Daemon) handleAccountPi(w http.ResponseWriter, r *http.Request, action string) bool {
	if !strings.HasPrefix(action, "pi/") {
		return false
	}
	var wantMethod string
	switch action {
	case "pi/runtime/install", "pi/runtime/uninstall",
		"pi/login/start", "pi/login/complete", "pi/login/cancel":
		wantMethod = http.MethodPost
	case "pi/login/status", "pi/login/ws":
		wantMethod = http.MethodGet
	default:
		accountError(w, http.StatusNotFound, "not_found")
		return true
	}
	if r.Method != wantMethod {
		w.Header().Set("Allow", wantMethod)
		accountError(w, http.StatusMethodNotAllowed, "method_not_allowed")
		return true
	}
	origins := r.Header.Values("Origin")
	if crossSiteFetch(r) || len(origins) > 1 {
		accountError(w, http.StatusForbidden, "origin_required")
		return true
	}
	if action == "pi/login/ws" {
		if !sameOrigin(r) {
			accountError(w, http.StatusForbidden, "origin_required")
			return true
		}
		if !wsUpgrade(r) {
			accountError(w, http.StatusUpgradeRequired, "upgrade_required")
			return true
		}
		d.handleAccountPiLoginWS(w, r)
		return true
	}
	if (wantMethod == http.MethodPost || len(origins) == 1) && !sameOrigin(r) {
		accountError(w, http.StatusForbidden, "origin_required")
		return true
	}
	switch action {
	case "pi/runtime/install":
		d.handleAccountPiRuntimeInstall(w, r)
	case "pi/runtime/uninstall":
		d.handleAccountPiRuntimeUninstall(w, r)
	case "pi/login/start":
		d.handleAccountPiLoginStart(w, r)
	case "pi/login/status":
		d.handleAccountPiLoginStatus(w, r)
	case "pi/login/complete":
		d.handleAccountPiLoginComplete(w, r)
	case "pi/login/cancel":
		d.handleAccountPiLoginCancel(w, r)
	}
	return true
}

func piEmptyBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	var body struct{}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	err := dec.Decode(&body)
	if errors.Is(err, io.EOF) {
		return true
	}
	if err != nil || dec.Decode(new(any)) != io.EOF {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func (d *Daemon) handleAccountPiRuntimeInstall(w http.ResponseWriter, r *http.Request) {
	if d.piRuntime == nil {
		accountError(w, http.StatusServiceUnavailable, "runtime_unavailable")
		return
	}
	if !piEmptyBody(w, r) {
		return
	}
	c := &d.piRuntimeState
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "runtime_unavailable")
		return
	}
	if c.uninstalling {
		c.mu.Unlock()
		accountError(w, http.StatusConflict, "runtime_busy")
		return
	}
	if c.installing {
		c.mu.Unlock()
		writeJSON(w, http.StatusAccepted, d.piRuntimeStatus())
		return
	}
	installCtx, cancel := context.WithCancel(context.Background())
	c.installing = true
	c.cancel = cancel
	c.lastErr = ""
	c.progress = &piRuntimeProgressView{Phase: "verify_manifest"}
	c.mu.Unlock()

	d.accountChanged()

	go func() {
		var emitMu sync.Mutex
		var lastEmit time.Time
		var lastPhase string

		onProgress := func(p wspiruntime.Progress) {
			c.mu.Lock()
			if c.installing {
				c.progress = &piRuntimeProgressView{
					Phase:      p.Phase,
					Downloaded: p.Downloaded,
					Total:      p.Total,
				}
			}
			c.mu.Unlock()

			emitMu.Lock()
			now := time.Now()
			shouldEmit := p.Phase != lastPhase || lastEmit.IsZero() || now.Sub(lastEmit) >= piInstallProgressEmitInterval
			if shouldEmit {
				lastPhase = p.Phase
				lastEmit = now
			}
			emitMu.Unlock()
			if shouldEmit {
				d.accountChanged()
			}
		}

		installFn := d.piRuntime.Install
		if d.piRuntimeInstall != nil {
			installFn = d.piRuntimeInstall
		}
		err := installFn(installCtx, onProgress)

		c.mu.Lock()
		cancel()
		c.cancel = nil
		c.installing = false
		c.progress = nil
		if err != nil && !c.closed {
			c.lastErr = err.Error()
		} else {
			c.lastErr = ""
		}
		closed := c.closed
		c.mu.Unlock()

		if !closed {
			d.notifyPiRuntimeChanged()
			d.accountChanged()
		}
	}()

	writeJSON(w, http.StatusAccepted, d.piRuntimeStatus())
}

func (d *Daemon) handleAccountPiRuntimeUninstall(w http.ResponseWriter, r *http.Request) {
	if d.piRuntime == nil {
		accountError(w, http.StatusServiceUnavailable, "runtime_unavailable")
		return
	}
	if !piEmptyBody(w, r) {
		return
	}
	if d.piLogin.isRunning() {
		accountError(w, http.StatusConflict, "runtime_busy")
		return
	}
	c := &d.piRuntimeState
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		accountError(w, http.StatusServiceUnavailable, "runtime_unavailable")
		return
	}
	if c.installing || c.uninstalling {
		c.mu.Unlock()
		accountError(w, http.StatusConflict, "runtime_busy")
		return
	}
	c.uninstalling = true
	c.mu.Unlock()

	err := d.piRuntime.Uninstall(r.Context())

	c.mu.Lock()
	c.uninstalling = false
	if err == nil {
		c.lastErr = ""
		c.progress = nil
	} else if !c.closed {
		c.lastErr = err.Error()
	}
	c.mu.Unlock()

	if err != nil {
		accountError(w, http.StatusInternalServerError, "runtime_uninstall_failed")
		return
	}
	d.notifyPiRuntimeChanged()
	d.accountChanged()
	writeJSON(w, http.StatusOK, d.piRuntimeStatus())
}
