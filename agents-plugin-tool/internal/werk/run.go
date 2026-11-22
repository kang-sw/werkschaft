package werk

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kang-sw/devenv/internal/wscred"
)

// Provider is the provider the builtin harness asks the auth layer for.
const Provider = wscred.ProviderOpenRouter

// Main is the holder-run entry: the process a session holder keeps for one
// worker. It reads commands from stdin and writes lines to stdout; a message
// for the human goes to stderr (the daemon keeps its tail for an exit
// message). It returns the process exit code.
//
// The credential is never an argument or an environment variable of this
// process: each request asks the daemon's provider token route with the
// session's bearer (wscred.RouteClient), the one way the auth layer hands a
// consumer a credential.
func Main(args []string, getenv func(string) string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("werk-run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		dir          = fs.String("dir", "", "session directory the conversation is kept under")
		root         = fs.String("root", "", "worktree root")
		cwd          = fs.String("cwd", "", "working directory")
		authority    = fs.String("authority", "", "permission level: read-only, workdir-write or full")
		model        = fs.String("model", "", "model id")
		effort       = fs.String("effort", "", "reasoning effort")
		session      = fs.String("session", "", "session id to resume")
		instructions = fs.String("instructions", "", "instruction file")
		tools        = fs.String("tools", "", "comma-separated file tools to offer; empty offers all")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, "werk: "+format+"\n", a...)
		return 1
	}
	if *dir == "" || *model == "" {
		return fail("--dir and --model are required")
	}
	if *session != "" && !ValidSessionID(*session) {
		return fail("--session is not a werk session id")
	}
	url, token := getenv(EnvSessionURL), getenv(EnvSessionToken)
	if url == "" || token == "" {
		return fail("the daemon's session route is not in the environment; a werk process runs only under the daemon")
	}
	base, err := wscred.DaemonBaseURL(url)
	if err != nil {
		return fail("%v", err)
	}
	provider, ok := wscred.Lookup(Provider)
	if !ok {
		return fail("the provider registry has no %s entry", Provider)
	}
	text := ""
	if *instructions != "" {
		raw, err := os.ReadFile(*instructions)
		if err != nil {
			return fail("read the instruction file: %v", err)
		}
		text = string(raw)
	}
	var allow []string
	for _, name := range strings.Split(*tools, ",") {
		if name = strings.TrimSpace(name); name != "" {
			allow = append(allow, name)
		}
	}
	creds := wscred.NewRouteClient(base, wscred.NewSecret(token), nil)
	client := &Client{
		BaseURL: provider.BaseURL,
		Token: func(ctx context.Context) (string, error) {
			cred, err := creds.Credential(ctx, wscred.Request{Provider: Provider})
			if err != nil {
				if errors.Is(err, wscred.ErrDaemonUnavailable) {
					return "", &TransientError{Err: err}
				}
				return "", err
			}
			return cred.Secret.Reveal(), nil
		},
	}
	agent, err := New(Config{
		Dir: *dir, Root: *root, Cwd: *cwd, Authority: Authority(*authority),
		Model: *model, Effort: *effort, Session: *session,
		Instructions: text, LocalTools: allow,
		Endpoint: client,
		WS:       &MCP{URL: url, Token: token, Version: ProtocolVersion},
		Out:      stdout,
	})
	if err != nil {
		return fail("%v", err)
	}
	defer agent.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := agent.Serve(ctx, stdin); err != nil && !errors.Is(err, context.Canceled) {
		return fail("%v", err)
	}
	return 0
}
