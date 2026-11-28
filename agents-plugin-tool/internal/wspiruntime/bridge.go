package wspiruntime

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"

	"github.com/kang-sw/devenv/internal/wsstate"
)

//go:embed pirpc_bridge.mjs
var embeddedBridgeSource []byte

var embeddedBridgeHash = func() string {
	sum := sha256.Sum256(embeddedBridgeSource)
	return hex.EncodeToString(sum[:])[:16]
}()

// BridgeSource returns a copy of the embedded pirpc_bridge.mjs bytes.
func BridgeSource() []byte {
	out := make([]byte, len(embeddedBridgeSource))
	copy(out, embeddedBridgeSource)
	return out
}

// BridgePath extracts the embedded pirpc_bridge.mjs extension once to a
// content-hashed file under <CacheRoot>/runtimes/pi/bridge-<sha256[:16]>.mjs
// using wsstate.AtomicReplaceFile and memoizes the path in memory.
func (m *Manager) BridgePath() (string, error) {
	m.mu.Lock()
	if m.bridgePath != "" {
		path := m.bridgePath
		m.mu.Unlock()
		return path, nil
	}
	m.mu.Unlock()

	root, err := m.runtimesRoot()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("create pi runtime dir: %w", err)
	}
	target := filepath.Join(root, "bridge-"+embeddedBridgeHash+".mjs")

	m.bridgeWriteMu.Lock()
	defer m.bridgeWriteMu.Unlock()

	m.mu.Lock()
	if m.bridgePath != "" {
		path := m.bridgePath
		m.mu.Unlock()
		return path, nil
	}
	m.mu.Unlock()

	if info, statErr := os.Stat(target); statErr != nil || info.IsDir() || info.Size() != int64(len(embeddedBridgeSource)) {
		if err := atomicWriteFile(target, embeddedBridgeSource, 0o644); err != nil {
			return "", fmt.Errorf("write pi rpc bridge: %w", err)
		}
	}

	m.mu.Lock()
	m.bridgePath = target
	m.mu.Unlock()
	return target, nil
}

func atomicWriteFile(target string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(target)
	tmpFile, err := os.CreateTemp(dir, filepath.Base(target)+".*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Chmod(mode); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := wsstate.AtomicReplaceFile(tmpPath, target); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
