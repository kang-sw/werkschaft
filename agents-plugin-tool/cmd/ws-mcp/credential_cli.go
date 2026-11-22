package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// maxCredentialInput bounds what `credential set` reads from stdin.
const maxCredentialInput = 64 << 10

// credentialCommand runs `credential set|remove|show`. Inside the daemon
// (c.host != nil) it applies through the daemon's own store; outside it,
// ApplyCredentialChange writes the file under the daemon lock or forwards
// the change to the running daemon (single-writer rule). The key is read
// from stdin only and is never printed.
func (c *hostedCLI) credentialCommand(args []string) int {
	const usage = "usage: ws-mcp credential <set|remove|show> [<provider>] [--format json]"
	if len(args) == 0 {
		return c.failed("credential", errors.New(usage))
	}
	sub := args[0]
	// An unrecognized argument is never echoed: it may be a key typed in
	// the wrong place, and an error lands in terminal scrollback and logs.
	if sub != "set" && sub != "remove" && sub != "show" {
		return c.failed("credential", fmt.Errorf("unknown subcommand; use set, remove, or show\n%s", usage))
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
		if _, ok := wscred.Lookup(id); !ok {
			return c.failed(name, fmt.Errorf("unknown provider; known providers: %s", knownProviderIDs()))
		}
	}
	switch sub {
	case "show":
		return c.credentialShow(name, *format, id)
	case "remove":
		return c.credentialApply(name, *format, wscred.Change{Op: wscred.OpRemove, Provider: id})
	}
	key, err := readCredentialLine(c.input())
	if err != nil {
		return c.failed(name, err)
	}
	return c.credentialApply(name, *format, wscred.Change{Op: wscred.OpSet, Provider: id, APIKey: wscred.NewSecret(key)})
}

func knownProviderIDs() string {
	var ids []string
	for _, p := range wscred.Providers() {
		ids = append(ids, string(p.ID))
	}
	return strings.Join(ids, ", ")
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
		var credPath string
		if credPath, err = c.credentialPath(); err == nil {
			statuses, err = wscred.NewStore(credPath).Status()
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
	}
	var text strings.Builder
	for _, s := range statuses {
		if !s.Configured {
			fmt.Fprintf(&text, "%s (%s): not set\n", s.Provider, s.Name)
			continue
		}
		fmt.Fprintf(&text, "%s (%s): %s, updated %s\n", s.Provider, s.Name, s.Masked, s.UpdatedAt)
	}
	return c.output(name, format, statuses, text.String(), err)
}
