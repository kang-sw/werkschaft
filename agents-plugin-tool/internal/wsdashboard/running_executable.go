package wsdashboard

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// RunningExecutableFile is private restart evidence, not a runtime selection
// registry. Only the daemon holding the singleton writes it. Older daemons
// ignore it; settled executables and the last record survive their runs.
const RunningExecutableFile = "last-running-executable.json"

type runningExecutable struct {
	SchemaVersion int    `json:"schema_version"`
	Executable    string `json:"executable"`
	Version       string `json:"version"`
	SHA256        string `json:"binary_sha256"`
	BuildTime     int64  `json:"build_time"`
}

// recordRunningExecutable retains the current build before publishing restart
// evidence. It is called once at startup under the daemon lock, before hosted
// processes can reconnect. No session, heartbeat or tool call writes this file.
//
// When contentHash is known (from process startup before lock contention), it
// prefers the matching content-addressed copy under <dir>/bin directly and
// refuses to record a mismatched executable if the launch path on disk (such as
// macOS's mutable bin/ws-mcp-dashboard) was concurrently overwritten.
func recordRunningExecutable(dir, exe, contentHash, version string, buildTime int64) error {
	if contentHash != "" && len(contentHash) >= 16 {
		expectedStaged := filepath.Join(dir, stagedBinDir, stagedExecutableName(exe, contentHash))
		if abs, err := filepath.Abs(expectedStaged); err == nil {
			expectedStaged = abs
		}
		if info, err := os.Lstat(expectedStaged); err == nil && info.Mode().IsRegular() {
			if sum, err := fileSHA256(expectedStaged); err == nil && sum == contentHash {
				return writeRunningExecutableRecord(dir, expectedStaged, version, contentHash, buildTime)
			}
		}
	}
	if contentHash != "" {
		exeHash, err := fileSHA256(exe)
		if err != nil {
			return fmt.Errorf("retain running executable: %w", err)
		}
		if exeHash != contentHash {
			return fmt.Errorf("running executable %s changed on disk (got sha256 %s, want %s)", exe, exeHash, contentHash)
		}
	}
	staged, err := DaemonExecutable(exe, dir)
	if err != nil {
		return fmt.Errorf("retain running executable: %w", err)
	}
	staged, err = filepath.Abs(staged)
	if err != nil {
		return err
	}
	digest, err := fileSHA256(staged)
	if err != nil {
		return err
	}
	if contentHash != "" && digest != contentHash {
		return fmt.Errorf("staged running executable %s hash mismatch (got sha256 %s, want %s)", staged, digest, contentHash)
	}
	return writeRunningExecutableRecord(dir, staged, version, digest, buildTime)
}

func writeRunningExecutableRecord(dir, staged, version, digest string, buildTime int64) error {
	raw, err := json.Marshal(runningExecutable{1, staged, version, digest, buildTime})
	if err != nil {
		return err
	}
	return writePrivateFile(dir, RunningExecutableFile, append(raw, '\n'))
}
