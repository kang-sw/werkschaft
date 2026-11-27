package wsinbox

import (
	"strings"
	"testing"
)

func TestUserCommandAddBlockRoundTripsAndIgnoresGuidance(t *testing.T) {
	block := FormatUserCommandAdd("run <go test> & \"report\"", "call `session.register_user_command`\nwith {id}")
	parts := ParseUserCommands("hi\n" + block + "\nbye")
	var got *UserCommand
	for _, p := range parts {
		if p.Command != nil {
			got = p.Command
		}
	}
	if got == nil || !got.Add || got.Request != "run <go test> & \"report\"" || got.Cmd != "" {
		t.Fatalf("parsed %+v from %+v", got, parts)
	}
	var joined string
	for _, p := range parts {
		joined += p.Text
	}
	if joined != "hi\n"+block+"\nbye" {
		t.Fatalf("lossless split broken: %q", joined)
	}
}

func TestUserCommandAddIsNotAnAskBlockAndMalformedStaysText(t *testing.T) {
	for _, bad := range []string{
		"<ws:ucmd-add scope=\"worktree\" tool=\"session.register_user_command\">\n<request></request>\n<mcp-guidance>\nx\n</mcp-guidance>\n</ws:ucmd-add>",
		"<ws:ucmd-add scope=\"worktree\" tool=\"session.register_user_command\">\n<request>x</request>\n</ws:ucmd-add>",
		"<ws:ucmd-add scope=\"other\">\n<request>x</request>\n<mcp-guidance>\ng\n</mcp-guidance>\n</ws:ucmd-add>",
	} {
		for _, p := range ParseUserCommands(bad) {
			if p.Command != nil {
				t.Fatalf("malformed block parsed: %q", bad)
			}
		}
	}
	ask := FormatUserCommand(UserCommand{Label: "L", Cmd: "ls"})
	ps := ParseUserCommands(ask)
	if len(ps) != 1 || ps[0].Command == nil || ps[0].Command.Add || !strings.Contains(ps[0].Text, "ws:ucmd") {
		t.Fatalf("ask block regressed: %+v", ps)
	}
}
