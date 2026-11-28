package wspiruntime

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/flock"
	"github.com/kang-sw/devenv/internal/wsstate"
)

const (
	readyMarkerName   = ".ws-ready"
	installLockName   = ".install.lock"
	maxSHA256SUMSSize = 1 << 20 // 1 MiB
)

// Options configures a Manager instance. Zero values resolve to the production
// defaults (wsstate.CacheRoot, DefaultBaseURL, bounded http.Client, current
// runtime.GOOS/GOARCH).
type Options struct {
	CacheRoot  string
	BaseURL    string
	HTTPClient *http.Client
	GOOS       string
	GOARCH     string
}

// RuntimeStatus reports the cached readiness state of the managed Pi runtime.
type RuntimeStatus struct {
	Installed  bool   `json:"installed"`
	Version    string `json:"version"`
	BinaryPath string `json:"binary_path"`
	InstallDir string `json:"install_dir"`
	Installing bool   `json:"installing"`
	Error      string `json:"error,omitempty"`
}

// Progress describes download progress during Install / EnsureInstalled.
type Progress struct {
	Phase      string
	Downloaded int64
	Total      int64
}

// ProgressFunc receives bounded download progress updates.
type ProgressFunc func(Progress)

// readyMarker is persisted atomically as <installDir>/.ws-ready before
// swapping staging into <installDir>.
type readyMarker struct {
	Version string `json:"version"`
	GOOS    string `json:"goos"`
	GOARCH  string `json:"goarch"`
	SHA256  string `json:"sha256"`
}

type inflightInstall struct {
	done chan struct{}
	err  error

	mu        sync.Mutex
	waiters   int
	cancel    context.CancelFunc
	listeners []ProgressFunc
}

func (f *inflightInstall) addListener(fn ProgressFunc) {
	if fn == nil {
		return
	}
	f.mu.Lock()
	f.listeners = append(f.listeners, fn)
	f.mu.Unlock()
}

func (f *inflightInstall) notify(p Progress) {
	f.mu.Lock()
	listeners := append([]ProgressFunc(nil), f.listeners...)
	f.mu.Unlock()
	for _, fn := range listeners {
		fn(p)
	}
}

func (f *inflightInstall) releaseWaiter() {
	f.mu.Lock()
	f.waiters--
	cancel := f.cancel
	noneLeft := f.waiters <= 0
	f.mu.Unlock()
	if noneLeft && cancel != nil {
		cancel()
	}
}

// Manager manages the pinned standalone Pi runtime and embedded bridge under
// <CacheRoot>/runtimes/pi/. Readiness is cached in memory after the first disk
// check so interactive paths perform zero stat() polling.
type Manager struct {
	opts       Options
	goos       string
	goarch     string
	baseURL    string
	httpClient *http.Client

	mu         sync.Mutex
	cached     bool
	status     RuntimeStatus
	bridgePath string
	inflight   *inflightInstall

	bridgeWriteMu sync.Mutex

	// Test hooks (nil in production).
	resolveAsset func(goos, goarch string) (AssetSpec, error)
	renameDir    func(oldpath, newpath string) error
	removeAll    func(path string) error
	statHook     func()
}

func (m *Manager) assetSpec() (AssetSpec, error) {
	if m.resolveAsset != nil {
		return m.resolveAsset(m.goos, m.goarch)
	}
	return ResolveAsset(m.goos, m.goarch)
}

// NewManager creates a managed Pi runtime Manager.
func NewManager(opts Options) *Manager {
	goos := strings.TrimSpace(opts.GOOS)
	if goos == "" {
		goos = runtime.GOOS
	}
	goarch := strings.TrimSpace(opts.GOARCH)
	if goarch == "" {
		goarch = runtime.GOARCH
	}
	baseURL := strings.TrimSpace(opts.BaseURL)
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if !strings.HasSuffix(baseURL, "/") {
		baseURL += "/"
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: 10 * time.Minute,
		}
	}
	return &Manager{
		opts:       opts,
		goos:       goos,
		goarch:     goarch,
		baseURL:    baseURL,
		httpClient: client,
	}
}

func (m *Manager) runtimesRoot() (string, error) {
	cacheRoot, err := wsstate.CacheRoot(wsstate.Options{CacheHome: m.opts.CacheRoot})
	if err != nil {
		return "", fmt.Errorf("resolve cache root: %w", err)
	}
	return filepath.Join(cacheRoot, "runtimes", "pi"), nil
}

// InstallDir returns the deterministic versioned install directory path for
// this platform, even before the runtime is installed.
func (m *Manager) InstallDir() string {
	root, err := m.runtimesRoot()
	if err != nil {
		return ""
	}
	return filepath.Join(root, fmt.Sprintf("%s-%s-%s", ReleaseTag, m.goos, m.goarch))
}

// BinaryPath returns the deterministic path of the managed Pi binary
// (<installDir>/pi or <installDir>/pi.exe), even before install.
func (m *Manager) BinaryPath() string {
	dir := m.InstallDir()
	if dir == "" {
		return ""
	}
	binName := "pi"
	if spec, err := m.assetSpec(); err == nil {
		binName = spec.BinaryName
	} else if strings.EqualFold(m.goos, "windows") {
		binName = "pi.exe"
	}
	return filepath.Join(dir, binName)
}

// Status returns the managed runtime's readiness status. Disk is inspected once
// on first call and cached in memory until Install, Uninstall, or Invalidate.
func (m *Manager) Status() RuntimeStatus {
	m.mu.Lock()
	if m.cached {
		st := m.status
		st.Installing = m.inflight != nil
		m.mu.Unlock()
		return st
	}
	installing := m.inflight != nil
	m.mu.Unlock()

	st := m.probeDisk()

	m.mu.Lock()
	if !m.cached {
		m.status = st
		m.cached = true
	} else {
		st = m.status
	}
	st.Installing = m.inflight != nil || installing
	m.mu.Unlock()
	return st
}

// Invalidate clears the in-memory readiness and bridge-path caches so the next
// Status / BridgePath call re-probes disk.
func (m *Manager) Invalidate() {
	m.mu.Lock()
	m.cached = false
	m.status = RuntimeStatus{}
	m.bridgePath = ""
	m.mu.Unlock()
}

func (m *Manager) probeDisk() RuntimeStatus {
	if m.statHook != nil {
		m.statHook()
	}
	st := RuntimeStatus{
		Version:    PinnedVersion,
		InstallDir: m.InstallDir(),
		BinaryPath: m.BinaryPath(),
	}
	spec, err := m.assetSpec()
	if err != nil {
		st.Error = err.Error()
		return st
	}
	if st.InstallDir == "" || st.BinaryPath == "" {
		st.Error = "could not resolve cache root"
		return st
	}
	if !isInstallReadyOnDisk(st.InstallDir, spec) {
		return st
	}
	st.Installed = true
	return st
}

func isInstallReadyOnDisk(installDir string, spec AssetSpec) bool {
	markerRaw, err := wsstate.ReadFile(filepath.Join(installDir, readyMarkerName))
	if err != nil {
		return false
	}
	var marker readyMarker
	if json.Unmarshal(markerRaw, &marker) != nil {
		return false
	}
	if marker.Version != PinnedVersion || marker.GOOS != spec.GOOS || marker.GOARCH != spec.GOARCH || !strings.EqualFold(marker.SHA256, spec.SHA256) {
		return false
	}
	info, err := os.Stat(filepath.Join(installDir, spec.BinaryName))
	if err != nil || info.IsDir() || !info.Mode().IsRegular() {
		return false
	}
	return true
}

// EnsureInstalled guarantees the managed Pi runtime is installed, returning
// immediately from memory when already ready. Otherwise it runs a single-flight
// Install.
func (m *Manager) EnsureInstalled(ctx context.Context, onProgress ProgressFunc) error {
	if m.Status().Installed {
		return nil
	}
	return m.runInstall(ctx, false, onProgress)
}

// Install downloads, verifies, and atomically installs (or repairs/reinstalls)
// the pinned standalone Pi runtime. Concurrent callers share one in-flight
// download inside the process and coordinate via flock across processes.
func (m *Manager) Install(ctx context.Context, onProgress ProgressFunc) error {
	return m.runInstall(ctx, true, onProgress)
}

func (m *Manager) runInstall(ctx context.Context, force bool, onProgress ProgressFunc) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	m.mu.Lock()
	if m.inflight != nil {
		call := m.inflight
		call.mu.Lock()
		call.waiters++
		call.mu.Unlock()
		call.addListener(onProgress)
		m.mu.Unlock()
		return waitInflight(ctx, call)
	}
	installCtx, cancel := context.WithCancel(context.Background())
	call := &inflightInstall{
		done:    make(chan struct{}),
		waiters: 1,
		cancel:  cancel,
	}
	call.addListener(onProgress)
	m.inflight = call
	m.mu.Unlock()

	go func() {
		defer cancel()
		err := m.executeInstall(installCtx, force, call.notify)
		call.err = err
		m.mu.Lock()
		m.inflight = nil
		if err == nil {
			m.status = RuntimeStatus{
				Installed:  true,
				Version:    PinnedVersion,
				BinaryPath: m.BinaryPath(),
				InstallDir: m.InstallDir(),
			}
			m.cached = true
		} else {
			m.cached = false
		}
		m.mu.Unlock()
		close(call.done)
	}()

	return waitInflight(ctx, call)
}

func waitInflight(ctx context.Context, call *inflightInstall) error {
	defer call.releaseWaiter()
	select {
	case <-call.done:
		return call.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *Manager) executeInstall(ctx context.Context, force bool, notify ProgressFunc) error {
	spec, err := m.assetSpec()
	if err != nil {
		return err
	}
	root, err := m.runtimesRoot()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create pi runtimes root: %w", err)
	}

	fl := flock.New(filepath.Join(root, installLockName))
	locked, err := fl.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquire pi runtime install lock: %w", err)
	}
	if !locked {
		return errors.New("could not acquire pi runtime install lock")
	}
	defer func() { _ = fl.Unlock() }()

	installDir := filepath.Join(root, fmt.Sprintf("%s-%s-%s", ReleaseTag, m.goos, m.goarch))
	if !force && isInstallReadyOnDisk(installDir, spec) {
		return nil
	}

	if notify != nil {
		notify(Progress{Phase: "verify_manifest"})
	}
	manifestDigest, err := m.fetchExpectedDigest(ctx, spec)
	if err != nil {
		return err
	}
	if !strings.EqualFold(manifestDigest, spec.SHA256) {
		return fmt.Errorf("SHA256SUMS digest mismatch for %s: remote %s != pinned %s", spec.Filename, manifestDigest, spec.SHA256)
	}

	suffix, err := randomHex(6)
	if err != nil {
		return err
	}
	stagingDir := installDir + ".tmp-" + suffix
	archiveTmp := filepath.Join(root, spec.Filename+".tmp-"+suffix)
	cleanup := func() {
		_ = m.doRemoveAll(stagingDir)
		_ = os.Remove(archiveTmp)
	}
	defer cleanup()

	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	if err := m.downloadAndVerifyArchive(ctx, spec, archiveTmp, notify); err != nil {
		return err
	}

	if notify != nil {
		notify(Progress{Phase: "extract"})
	}
	if err := extractArchive(archiveTmp, spec.ArchiveKind, stagingDir, spec.BinaryName); err != nil {
		return err
	}

	// Write .ws-ready inside staging before swapping into installDir so
	// <installDir>/pi never appears without a matching readiness marker.
	markerBytes, err := json.MarshalIndent(readyMarker{
		Version: PinnedVersion,
		GOOS:    spec.GOOS,
		GOARCH:  spec.GOARCH,
		SHA256:  spec.SHA256,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal ready marker: %w", err)
	}
	markerBytes = append(markerBytes, '\n')
	if err := atomicWriteFile(filepath.Join(stagingDir, readyMarkerName), markerBytes, 0o644); err != nil {
		return fmt.Errorf("write ready marker: %w", err)
	}

	if err := m.swapInstallDir(stagingDir, installDir, suffix); err != nil {
		return err
	}
	// Staging has been renamed to installDir; prevent deferred cleanup from touching it.
	stagingDir = ""
	pruneStaleDirs(root, installDir, m.doRemoveAll)
	return nil
}

func (m *Manager) fetchExpectedDigest(ctx context.Context, spec AssetSpec) (string, error) {
	url := m.baseURL + "SHA256SUMS"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("build SHA256SUMS request: %w", err)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch SHA256SUMS: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch SHA256SUMS: unexpected HTTP status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSHA256SUMSSize+1))
	if err != nil {
		return "", fmt.Errorf("read SHA256SUMS: %w", err)
	}
	if len(data) > maxSHA256SUMSSize {
		return "", errors.New("SHA256SUMS response exceeds size limit")
	}
	digest, ok := parseSHA256SUMS(string(data), spec.Filename)
	if !ok {
		return "", fmt.Errorf("SHA256SUMS does not contain an entry for %s", spec.Filename)
	}
	return digest, nil
}

func parseSHA256SUMS(content, filename string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		digest := strings.ToLower(strings.TrimSpace(fields[0]))
		name := strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./")
		if name == filename && len(digest) == 64 && isLowerHex(digest) {
			return digest, true
		}
	}
	return "", false
}

func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func (m *Manager) downloadAndVerifyArchive(ctx context.Context, spec AssetSpec, destPath string, notify ProgressFunc) error {
	url := m.baseURL + spec.Filename
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build archive request: %w", err)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("download %s: %w", spec.Filename, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download %s: unexpected HTTP status %d", spec.Filename, resp.StatusCode)
	}

	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temp archive: %w", err)
	}

	hasher := sha256.New()
	total := resp.ContentLength
	var downloaded int64
	buf := make([]byte, 32<<10)
	lastReport := time.Time{}

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				_ = out.Close()
				return fmt.Errorf("write archive: %w", werr)
			}
			_, _ = hasher.Write(buf[:n])
			downloaded += int64(n)
			if notify != nil && (lastReport.IsZero() || time.Since(lastReport) >= 100*time.Millisecond) {
				notify(Progress{Phase: "download", Downloaded: downloaded, Total: total})
				lastReport = time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			_ = out.Close()
			return fmt.Errorf("read %s: %w", spec.Filename, readErr)
		}
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	if notify != nil {
		notify(Progress{Phase: "download", Downloaded: downloaded, Total: total})
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, spec.SHA256) {
		return fmt.Errorf("archive SHA-256 mismatch for %s: got %s, want %s", spec.Filename, actual, spec.SHA256)
	}
	return nil
}

func (m *Manager) swapInstallDir(stagingDir, installDir, suffix string) error {
	var backupDir string
	if _, err := os.Lstat(installDir); err == nil {
		backupDir = installDir + ".old-" + suffix
		if err := m.doRenameDir(installDir, backupDir); err != nil {
			return fmt.Errorf("move existing runtime aside: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat existing runtime dir: %w", err)
	}

	if err := m.doRenameDir(stagingDir, installDir); err != nil {
		if backupDir != "" {
			_ = m.doRenameDir(backupDir, installDir)
		}
		return fmt.Errorf("activate staged runtime: %w", err)
	}

	if backupDir != "" {
		_ = m.doRemoveAll(backupDir)
	}
	return nil
}

// Uninstall removes the managed Pi runtime directory, stale staging/backup
// leftovers, and extracted bridge scripts under the install lock, and resets
// the in-memory readiness cache.
func (m *Manager) Uninstall(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := m.runtimesRoot()
	if err != nil {
		return err
	}
	installDir := m.InstallDir()

	if _, statErr := os.Stat(root); errors.Is(statErr, os.ErrNotExist) {
		m.mu.Lock()
		m.cached = true
		m.status = RuntimeStatus{
			Installed:  false,
			Version:    PinnedVersion,
			BinaryPath: m.BinaryPath(),
			InstallDir: installDir,
		}
		m.bridgePath = ""
		m.mu.Unlock()
		return nil
	}

	fl := flock.New(filepath.Join(root, installLockName))
	locked, err := fl.TryLockContext(ctx, 50*time.Millisecond)
	if err != nil {
		return fmt.Errorf("acquire pi runtime lock for uninstall: %w", err)
	}
	if !locked {
		return errors.New("could not acquire pi runtime lock for uninstall")
	}
	defer func() { _ = fl.Unlock() }()

	if installDir != "" {
		if err := m.doRemoveAll(installDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove pi runtime dir: %w", err)
		}
	}
	pruneStaleDirs(root, installDir, m.doRemoveAll)
	if entries, err := os.ReadDir(root); err == nil {
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasPrefix(name, "bridge-") && strings.HasSuffix(name, ".mjs") {
				_ = os.Remove(filepath.Join(root, name))
			}
		}
	}

	m.mu.Lock()
	m.cached = true
	m.status = RuntimeStatus{
		Installed:  false,
		Version:    PinnedVersion,
		BinaryPath: m.BinaryPath(),
		InstallDir: installDir,
	}
	m.bridgePath = ""
	m.mu.Unlock()
	return nil
}

func (m *Manager) doRenameDir(oldpath, newpath string) error {
	if m.renameDir != nil {
		return m.renameDir(oldpath, newpath)
	}
	return renameDirWithRetry(oldpath, newpath)
}

func (m *Manager) doRemoveAll(path string) error {
	if m.removeAll != nil {
		return m.removeAll(path)
	}
	return removeAllWithRetry(path)
}

func pruneStaleDirs(root, installDir string, removeAll func(string) error) {
	base := filepath.Base(installDir)
	entries, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, base+".tmp-") || strings.HasPrefix(name, base+".old-") {
			_ = removeAll(filepath.Join(root, name))
		}
	}
}

func randomHex(nBytes int) (string, error) {
	buf := make([]byte, nBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate random suffix: %w", err)
	}
	return hex.EncodeToString(buf), nil
}
