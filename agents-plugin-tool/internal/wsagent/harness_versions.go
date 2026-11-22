// harness_versions.go keeps the harness version each backend's processes
// last reported, with the binary they ran from, for the exec cutover's
// version gate (execCutoverFor). A version stands for a binary only while
// that binary is unchanged: a downgrade, upgrade or reinstall since the
// report leaves the version unknown until a process of the new binary
// reports.
//
// Storage (ai-docs/manuals/performance.md): memory is the source of truth;
// the file is read once when the runner is made, and each change is one
// appended JSONL line, written under the store's lock so the file's order
// is the order of the changes. An unchanged report writes nothing.
package wsagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// harnessVersionsFile, under the runner's directory, is the append-only
// log of reported harness versions.
const harnessVersionsFile = "harness-versions.jsonl"

// harnessVersionsCompactAt is the line count past which loading rewrites
// the log as one line per backend.
const harnessVersionsCompactAt = 256

// harnessSeen is one reported harness version: the backend, the version,
// and the fingerprint of the binary the reporting process launched from
// (harnessBinaryOf).
type harnessSeen struct {
	Backend string `json:"backend"`
	Version string `json:"version"`
	Binary  string `json:"binary"`
}

// harnessVersions is the runner's reported-version store.
type harnessVersions struct {
	mu   sync.Mutex
	seen map[string]harnessSeen
	path string
	log  io.Writer
}

// loadHarnessVersions reads dir's log once; the last line per backend wins.
// An empty dir keeps the store in memory only.
func loadHarnessVersions(dir string, log io.Writer) *harnessVersions {
	s := &harnessVersions{seen: map[string]harnessSeen{}, log: log}
	if dir == "" {
		return s
	}
	s.path = filepath.Join(dir, harnessVersionsFile)
	f, err := os.Open(s.path)
	if err != nil {
		return s
	}
	lines := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var v harnessSeen
		// A torn last line from a crash is skipped.
		if json.Unmarshal(sc.Bytes(), &v) == nil && v.Backend != "" && v.Version != "" {
			s.seen[v.Backend] = v
		}
		lines++
	}
	f.Close()
	if lines > harnessVersionsCompactAt {
		s.compact()
	}
	return s
}

// compact rewrites the log as its current state, once, at load.
func (s *harnessVersions) compact() {
	var b strings.Builder
	for _, v := range s.seen {
		raw, _ := json.Marshal(v)
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := writeFileAtomic(s.path, []byte(b.String()), 0o600); err != nil {
		fmt.Fprintf(s.log, "agent runner: compact harness versions: %v\n", err)
	}
}

// get is backend's last reported version, zero when none is known.
func (s *harnessVersions) get(backend string) harnessSeen {
	if s == nil {
		return harnessSeen{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[backend]
}

// note keeps a process's report of backend's version, launched from
// binary, and appends it to the log when it changed anything. A report
// with no version or no known binary (a rejoined process this daemon did
// not launch) is not kept.
func (s *harnessVersions) note(backend, version, binary string) {
	if s == nil || version == "" || binary == "" {
		return
	}
	v := harnessSeen{Backend: backend, Version: version, Binary: binary}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen[backend] == v {
		return
	}
	s.seen[backend] = v
	if s.path == "" {
		return
	}
	raw, _ := json.Marshal(v)
	if err := appendLine(s.path, append(raw, '\n')); err != nil {
		fmt.Fprintf(s.log, "agent runner: save harness version: %v\n", err)
	}
}

// appendLine appends line to path with one write.
func appendLine(path string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, werr := f.Write(line)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}

// harnessBinaryOf fingerprints the binary reg's backend launches: each
// file of its resolved command (the program, and on Windows the script a
// cmd shim runs) by real path, size and modification time. A backend that
// names no executable has the empty fingerprint's stand-in "-"; a command
// that does not resolve fingerprints as unresolved, which never matches a
// launched one.
func harnessBinaryOf(reg BackendRegistration) string {
	lister, ok := reg.Backend.(ChoiceLister)
	if !ok {
		return "-"
	}
	exe := lister.Executable()
	prog, pre, err := harnessCommand(exe, exe)
	if err != nil {
		return "unresolved:" + exe
	}
	parts := make([]string, 0, 1+len(pre))
	for _, p := range append([]string{prog}, pre...) {
		if !filepath.IsAbs(p) {
			continue
		}
		real, err := filepath.EvalSymlinks(p)
		if err != nil {
			parts = append(parts, p+"|missing")
			continue
		}
		info, err := os.Stat(real)
		if err != nil || !info.Mode().IsRegular() {
			parts = append(parts, real+"|missing")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s|%d|%d", real, info.Size(), info.ModTime().UnixNano()))
	}
	if len(parts) == 0 {
		return "unresolved:" + exe
	}
	return strings.Join(parts, ";")
}
