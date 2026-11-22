package main

import (
	"os"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsagent"
)

// The builtin agent harness's loop runs as a subcommand of this binary: the
// daemon starts it under a session holder (wsagent.WerkBackend), so it
// survives a daemon replacement. It reads commands from standard input and
// writes its event lines to standard output (internal/werk/protocol.go).
func init() {
	hiddenCommands[wsagent.WerkSubcommand] = func(args []string) int {
		return werk.Main(args, os.Getenv, os.Stdin, os.Stdout, os.Stderr)
	}
}
