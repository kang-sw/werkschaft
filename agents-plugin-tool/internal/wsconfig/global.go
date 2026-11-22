package wsconfig

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	// EnvConfigHome names the global config directory override a caller
	// process carries.
	EnvConfigHome        = "WS_CONFIG_HOME"
	defaultConfigDirName = ".ws"
)

// GlobalPath resolves the cross-project global config file path.
// Resolution order: opts.ConfigHome → $WS_CONFIG_HOME → ~/.ws/config.json.
func GlobalPath(opts Options) (string, error) {
	if opts.ConfigHome != "" {
		return filepath.Join(opts.ConfigHome, "config.json"), nil
	}
	if env := os.Getenv(EnvConfigHome); env != "" {
		return filepath.Join(env, "config.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir for global config: %w", err)
	}
	return filepath.Join(home, defaultConfigDirName, "config.json"), nil
}

// GlobalOverride is the raw value key holds in the global config file's
// overrides, and whether it is set: the file's own text, through no
// manifest and no resolution. A missing file is an unset key.
func GlobalOverride(opts Options, key string) (string, bool) {
	overrides, err := GlobalOverrides(opts)
	if err != nil {
		return "", false
	}
	value, ok := overrides[key]
	return value, ok
}

// GlobalOverrides reads the persisted scalar overrides from one global snapshot.
func GlobalOverrides(opts Options) (map[string]string, error) {
	cfg, err := loadGlobalConfig(opts)
	return cfg.Overrides, err
}

// loadGlobalConfig reads the global config file. A missing file returns an
// empty Config (equivalent to "no global overrides"), mirroring the project
// Load pattern.
func loadGlobalConfig(opts Options) (Config, error) {
	path, err := GlobalPath(opts)
	if err != nil {
		return Config{}, err
	}
	cfg, err := loadConfigFile(path, "global")
	return reconcileGlobal(cfg), err
}
