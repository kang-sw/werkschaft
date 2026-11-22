package wsagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsharnessenv"
)

// OneShotSupport is a backend's isolated single-prompt mode: the prompt on
// stdin, the reply text on stdout; no tools, no MCP servers, no settings
// sources, plugins, hooks, memory files, or session persistence. A backend
// registers it only when its CLI has a mode that verifiably disables all of
// them; the probe notes live at the registration.
type OneShotSupport struct {
	// Args is the argv without the executable. model and effort are the
	// caller's resolved values; "" leaves the CLI's default.
	Args func(model, effort string) []string
	// Env scrubs the child's environment from the daemon's; nil drops only
	// the inherited ws harness-session identity (wsharnessenv.Drop).
	Env func(env []string) []string
}

// ErrNoOneShot reports a harness with no registered one-shot mode.
var ErrNoOneShot = errors.New("harness has no one-shot mode")

const (
	// oneShotMaxOutputBytes caps stdout and stderr each; a reply is a
	// short quip, so more means a runaway or a wrong mode.
	oneShotMaxOutputBytes = 64 << 10
	oneShotWaitDelay      = 2 * time.Second
)

// oneShotExecutable resolves the program a backend launches, the same way
// its worker launch does (harnessCommand: a Command override, PATH lookup,
// Windows cmd-shim unwrap).
type oneShotExecutable interface{ Executable() string }

// RunOneShot sends prompt to the harness's isolated one-shot mode and returns
// the trimmed reply. The caller's ctx bounds it; its end kills the process
// tree. It dispatches only through the builtin registration: a harness with
// no OneShot returns ErrNoOneShot without spawning.
func RunOneShot(ctx context.Context, harness, model, effort, prompt string) (string, error) {
	return runOneShot(ctx, builtinRegistration(harness), harness, model, effort, prompt)
}

func runOneShot(ctx context.Context, reg BackendRegistration, harness, model, effort, prompt string) (string, error) {
	if reg.OneShot == nil || reg.OneShot.Args == nil {
		return "", fmt.Errorf("%w: %q", ErrNoOneShot, harness)
	}
	exe, ok := reg.Backend.(oneShotExecutable)
	if !ok {
		return "", fmt.Errorf("%w: %q has no executable", ErrNoOneShot, harness)
	}
	command, pre, err := harnessCommand(exe.Executable(), harness)
	if err != nil {
		return "", fmt.Errorf("%s one-shot: %w", harness, err)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, append(pre, reg.OneShot.Args(model, effort)...)...)
	// A neutral directory: no project files, instructions, or git state.
	cmd.Dir = os.TempDir()
	env := os.Environ()
	if reg.OneShot.Env != nil {
		cmd.Env = reg.OneShot.Env(env)
	} else {
		cmd.Env = wsharnessenv.Drop(env)
	}
	cmd.Stdin = strings.NewReader(prompt)
	configureOneShotCmd(cmd)
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return execjob.KillTree(cmd.Process.Pid)
		}
		return nil
	}
	cmd.WaitDelay = oneShotWaitDelay
	out := &oneShotBuffer{remaining: oneShotMaxOutputBytes, onOverflow: cancel}
	errBuf := &oneShotBuffer{remaining: oneShotMaxOutputBytes, onOverflow: cancel}
	cmd.Stdout, cmd.Stderr = out, errBuf

	runErr := cmd.Run()
	stdout, outOver := out.snapshot()
	stderr, errOver := errBuf.snapshot()
	if outOver || errOver {
		return "", fmt.Errorf("%s one-shot: output exceeds %d bytes", harness, oneShotMaxOutputBytes)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", fmt.Errorf("%s one-shot: %w", harness, ctxErr)
	}
	if runErr != nil {
		detail := strings.TrimSpace(string(stderr))
		if len(detail) > 200 {
			detail = detail[:200]
		}
		if detail != "" {
			return "", fmt.Errorf("%s one-shot: %w: %s", harness, runErr, detail)
		}
		return "", fmt.Errorf("%s one-shot: %w", harness, runErr)
	}
	return strings.TrimSpace(string(stdout)), nil
}

// oneShotBuffer keeps at most remaining bytes and calls onOverflow once on
// the first byte past the cap, dropping the rest.
type oneShotBuffer struct {
	mu         sync.Mutex
	buf        bytes.Buffer
	remaining  int
	overflowed bool
	onOverflow func()
}

func (w *oneShotBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(p) > w.remaining {
		w.buf.Write(p[:w.remaining])
		w.remaining = 0
		if !w.overflowed {
			w.overflowed = true
			if w.onOverflow != nil {
				w.onOverflow()
			}
		}
		return len(p), nil
	}
	w.buf.Write(p)
	w.remaining -= len(p)
	return len(p), nil
}

func (w *oneShotBuffer) snapshot() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.buf.Bytes()), w.overflowed
}
