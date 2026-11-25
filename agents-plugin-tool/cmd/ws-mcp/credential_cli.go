package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// maxCredentialInput bounds what `credential set` reads from stdin.
const maxCredentialInput = 64 << 10

// credentialCommand runs the provider credential commands. Inside the daemon
// (c.host != nil) it applies through the daemon's own store; outside it,
// ApplyCredentialChange writes the file under the daemon lock or forwards
// the change to the running daemon (single-writer rule). The key is read
// from stdin only and is never printed.
func (c *hostedCLI) credentialCommand(args []string) int {
	const usage = "usage: ws-mcp credential <set|remove|show|sign-in|endpoint> [<provider>] [--format json]"
	if len(args) == 0 {
		return c.failed("credential", errors.New(usage))
	}
	sub := args[0]
	if sub == "endpoint" {
		return c.credentialEndpoint(args[1:])
	}
	if sub == "sign-in" {
		return c.credentialSignIn(args[1:])
	}
	// An unrecognized argument is never echoed: it may be a key typed in
	// the wrong place, and an error lands in terminal scrollback and logs.
	if sub != "set" && sub != "remove" && sub != "show" {
		return c.failed("credential", fmt.Errorf("unknown subcommand; use set, remove, show, or sign-in\n%s", usage))
	}
	name := "credential " + sub
	fs := c.flags(name)
	format := fs.String("format", "text", "output format: text or json")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	var positional []string
	if fs.NArg() > 0 {
		positional = append(positional, fs.Arg(0))
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return 2
		}
		positional = append(positional, fs.Args()...)
	}
	switch {
	case sub == "show" && len(positional) > 1, sub != "show" && len(positional) < 1:
		return c.failed(name, errors.New(usage))
	case sub == "set" && len(positional) > 1:
		return c.failed(name, errors.New("the API key is read from stdin; pass it on stdin, never as an argument"))
	case sub == "remove" && len(positional) > 1:
		return c.failed(name, errors.New(usage))
	}
	var id wscred.ProviderID
	if len(positional) == 1 {
		id = wscred.ProviderID(positional[0])
		if _, ok := wscred.Lookup(id); !ok && !wsconfig.IsLocalWerkProvider(string(id)) {
			return c.failed(name, fmt.Errorf("unknown provider; known providers: %s", knownProviderIDs()))
		}
	}
	switch sub {
	case "show":
		return c.credentialShow(name, *format, id)
	case "remove":
		return c.credentialApply(name, *format, wscred.Change{Op: wscred.OpRemove, Provider: id})
	}
	if p, ok := wscred.Lookup(id); ok && p.Kind != wscred.KindAPIKey {
		return c.failed(name, errors.New("OpenAI uses ChatGPT sign-in; run \"credential sign-in openai\""))
	}
	key, err := c.readKey()
	if err != nil {
		return c.failed(name, err)
	}
	return c.credentialApply(name, *format, wscred.Change{Op: wscred.OpSet, Provider: id, APIKey: wscred.NewSecret(key)})
}

func (c *hostedCLI) credentialSignIn(args []string) int {
	const name = "credential sign-in"
	const usage = "usage: ws-mcp credential sign-in openai [--new-account | --account <client-id>] [--enable-plan]"
	if len(args) == 0 || args[0] != string(wscred.ProviderOpenAI) {
		return c.failed(name, errors.New(usage))
	}
	if c.host != nil {
		return c.failed(name, errors.New("run the ws-mcp binary directly to sign in with ChatGPT"))
	}
	var selection wscred.OpenAISignIn
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--enable-plan":
			selection.EnablePlan = true
		case "--new-account":
			selection.NewAccount = true
		case "--account":
			i++
			if i >= len(args) || args[i] == "" || strings.HasPrefix(args[i], "-") {
				return c.failed(name, errors.New(usage))
			}
			selection.ClientID = args[i]
		default:
			return c.failed(name, errors.New(usage))
		}
	}
	if selection.NewAccount && selection.ClientID != "" {
		return c.failed(name, errors.New(usage))
	}
	dir, err := wsdashboard.Dir(c.getenv(wsstate.EnvCacheHome))
	if err != nil {
		return c.failed(name, err)
	}
	path, err := c.credentialPath()
	if err != nil {
		return c.failed(name, err)
	}
	statuses, err := wsdashboard.SignInOpenAIOffline(c.ctx, dir, path, selection)
	if err != nil {
		return c.failed(name, err)
	}
	for _, status := range statuses {
		if status.Provider == wscred.ProviderOpenAI && status.Configured {
			fmt.Fprintln(c.stdout, "Signed in to OpenAI with ChatGPT plan usage enabled.")
			return 0
		}
	}
	fmt.Fprintln(c.stdout, "Signed in to OpenAI. To enable ChatGPT plan usage, run credential sign-in openai --enable-plan.")
	return 0
}

func knownProviderIDs() string {
	var ids []string
	for _, p := range wscred.Providers() {
		ids = append(ids, string(p.ID))
	}
	return strings.Join(ids, ", ")
}

// readKey reads the key from the first stdin line. When this process's own
// stdin is a terminal (a directly run binary), it prompts on stderr and keeps
// echo off while the key is typed, restoring it even on an interrupt; the
// launcher prompts the same way with getpass before it forwards the line.
func (c *hostedCLI) readKey() (string, error) {
	if f, ok := c.stdin.(*os.File); ok {
		restore, terminal, err := disableEcho(f)
		if err != nil {
			return "", err
		}
		if terminal {
			fmt.Fprint(c.stderr, "API key: ")
			stop := restoreOnInterrupt(func() {
				restore()
				fmt.Fprintln(c.stderr)
			})
			defer func() {
				stop()
				restore()
				fmt.Fprintln(c.stderr)
			}()
		}
	}
	return readCredentialLine(c.input())
}

// restoreOnInterrupt runs restore and exits when the process is interrupted
// before stop is called: an interrupt at the prompt would otherwise leave the
// terminal without echo.
func restoreOnInterrupt(restore func()) (stop func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case <-signals:
			restore()
			os.Exit(130)
		case <-done:
		}
	}()
	return func() {
		signal.Stop(signals)
		close(done)
	}
}

// readCredentialLine returns the first line of r, trimmed. It stops at the
// first newline (stdin may be a terminal that never reaches EOF) and reads at
// most maxCredentialInput bytes.
func readCredentialLine(r io.Reader) (string, error) {
	br := bufio.NewReader(r)
	var line []byte
	for len(line) <= maxCredentialInput {
		b, err := br.ReadByte()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				return "", fmt.Errorf("read stdin: %w", err)
			}
			break
		}
		if b == '\n' {
			break
		}
		line = append(line, b)
	}
	if len(line) > maxCredentialInput {
		return "", errors.New("the API key on stdin is too long")
	}
	key := strings.TrimSpace(string(line))
	if key == "" {
		return "", errors.New("no API key on stdin")
	}
	return key, nil
}

func (c *hostedCLI) credentialPath() (string, error) {
	return wscred.DefaultPath(c.getenv(wsstate.EnvCacheHome))
}

func (c *hostedCLI) credentialApply(name, format string, change wscred.Change) int {
	var result wscred.ChangeResult
	var err error
	if c.host != nil {
		if c.host.credentials == nil {
			return c.failed(name, errors.New("the credential store is unavailable in this daemon"))
		}
		result, err = c.host.credentials.Apply(change)
	} else {
		var dir, credPath string
		if dir, err = wsdashboard.Dir(c.getenv(wsstate.EnvCacheHome)); err == nil {
			if credPath, err = c.credentialPath(); err == nil {
				result, _, err = wsdashboard.ApplyCredentialChange(c.ctx, dir, credPath, change)
			}
		}
	}
	text := ""
	if err == nil {
		p := result.Status.Provider
		switch {
		case change.Op == wscred.OpRemove && result.Changed:
			text = fmt.Sprintf("%s: removed\n", p)
		case change.Op == wscred.OpRemove:
			text = fmt.Sprintf("%s: was not set\n", p)
		case result.Changed:
			text = fmt.Sprintf("%s: set %s\n", p, result.Status.Masked)
		default:
			text = fmt.Sprintf("%s: unchanged %s\n", p, result.Status.Masked)
		}
	}
	return c.output(name, format, result, text, err)
}

func (c *hostedCLI) credentialShow(name, format string, id wscred.ProviderID) int {
	var statuses []wscred.ProviderStatus
	var err error
	if c.host != nil {
		if c.host.credentials == nil {
			return c.failed(name, errors.New("the credential store is unavailable in this daemon"))
		}
		statuses, err = c.host.credentials.Status()
	} else {
		var dir, credPath string
		if dir, err = wsdashboard.Dir(c.getenv(wsstate.EnvCacheHome)); err == nil {
			if credPath, err = c.credentialPath(); err == nil {
				if err = wsdashboard.MigrateCredentialFile(dir, credPath); err == nil {
					store := wscred.NewStore(credPath)
					if err = store.LoadLocalEndpoints(wsconfig.Options{ConfigHome: c.getenv(wsconfig.EnvConfigHome)}); err == nil {
						statuses, err = store.Status()
					}
				}
			}
		}
	}
	if err == nil && id != "" {
		var one []wscred.ProviderStatus
		for _, s := range statuses {
			if s.Provider == id {
				one = append(one, s)
			}
		}
		statuses = one
		if len(one) == 0 {
			err = errors.New("unknown provider; check agent.werk_endpoints")
		}
	}
	var text strings.Builder
	for _, s := range statuses {
		if s.Provider == wscred.ProviderOpenAI && len(s.OpenAIAccounts) > 0 {
			displayOpenAIAccountStatus(&text, s.OpenAIAccounts)
			continue
		}
		if !s.Configured {
			fmt.Fprintf(&text, "%s (%s): not set\n", s.Provider, s.Name)
			continue
		}
		fmt.Fprintf(&text, "%s (%s): %s, updated %s\n", s.Provider, s.Name, s.Masked, s.UpdatedAt)
	}
	return c.output(name, format, statuses, text.String(), err)
}

func displayOpenAIAccountStatus(out io.Writer, accounts []wscred.OpenAIAccountStatus) {
	for _, account := range accounts {
		state := "sign-in required"
		if account.SignedIn {
			state = "ChatGPT plan usage disabled"
			if account.PlanEnabled {
				state = "ChatGPT plan usage enabled"
			}
		}
		if account.Active {
			state += " (active)"
		}
		fmt.Fprintf(out, "openai (OpenAI): %q, %s\n", account.ClientID, state)
	}
}

func (c *hostedCLI) credentialEndpoint(args []string) int {
	const name = "credential endpoint"
	const usage = "usage: ws-mcp credential endpoint <set|remove> <name> [--base-url <url> --kind vllm|ollama]"
	if len(args) < 2 || (args[0] != "set" && args[0] != "remove") {
		return c.failed(name, errors.New(usage))
	}
	change := wscred.LocalEndpointChange{Op: wscred.ChangeOp(args[0]), Name: args[1]}
	fs := c.flags(name)
	baseURL := fs.String("base-url", "", "API root")
	kind := fs.String("kind", "", "vllm or ollama")
	format := fs.String("format", "text", "output format")
	if err := fs.Parse(args[2:]); err != nil {
		return 2
	}
	if fs.NArg() != 0 || (change.Op == wscred.OpRemove && (*baseURL != "" || *kind != "")) {
		return c.failed(name, errors.New(usage))
	}
	change.BaseURL, change.Kind = *baseURL, *kind
	opts := wsconfig.Options{ConfigHome: c.getenv(wsconfig.EnvConfigHome)}
	var endpoints []wscred.LocalEndpointStatus
	var err error
	if c.host != nil {
		if c.host.credentials == nil {
			return c.failed(name, errors.New("credential store unavailable"))
		}
		if c.host.configPath != "" {
			path, err := wsconfig.GlobalPath(opts)
			if err != nil || path != c.host.configPath {
				return c.failed(name, errors.New("the daemon uses a different global configuration"))
			}
		}
		endpoints, err = c.host.credentials.ApplyLocalEndpoint(opts, change)
	} else {
		dir, e := wsdashboard.Dir(c.getenv(wsstate.EnvCacheHome))
		if e != nil {
			return c.failed(name, e)
		}
		path, e := c.credentialPath()
		if e != nil {
			return c.failed(name, e)
		}
		endpoints, err = wsdashboard.ApplyLocalEndpointChange(c.ctx, dir, path, opts, change)
	}
	return c.output(name, *format, endpoints, "Endpoint updated.\n", err)
}
