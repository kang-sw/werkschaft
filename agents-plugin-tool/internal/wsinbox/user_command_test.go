package wsinbox

import (
	"strings"
	"testing"
)

func TestParseUserCommandsRoundTripAndEscaping(t *testing.T) {
	cases := []struct {
		name string
		cmd  UserCommand
	}{
		{
			name: "full with shell operators quotes and multiline",
			cmd: UserCommand{
				Label:  `Build & "Test" <pkg> 'v2'`,
				Edited: true,
				Cwd:    `pkg/sub & "dir"`,
				Cmd:    "echo 'a < b' && cat <<EOF > out.txt\n[Image #1] &#91;Image #2]\nEOF | grep \"ok\"",
				Prompt: `Please run & verify <output> with "quotes" and [Image #3].`,
			},
		},
		{
			name: "minimal no label cwd edited or prompt",
			cmd: UserCommand{
				Cmd: "go test ./...",
			},
		},
		{
			name: "label only without edited or cwd",
			cmd: UserCommand{
				Label:  "Lint",
				Cmd:    "golangci-lint run",
				Prompt: "Check lint issues.",
			},
		},
		{
			name: "edited without prompt",
			cmd: UserCommand{
				Label:  "Quick check",
				Edited: true,
				Cwd:    "agents-plugin-tool",
				Cmd:    "git status -sb",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := FormatUserCommand(tc.cmd)
			if tc.cmd.Edited && !strings.Contains(raw, ` edited="true"`) {
				t.Fatalf("missing edited attr: %s", raw)
			}
			if !tc.cmd.Edited && strings.Contains(raw, `edited=`) {
				t.Fatalf("unexpected edited attr: %s", raw)
			}
			if tc.cmd.Cwd != "" && !strings.Contains(raw, `<cmd cwd="`) {
				t.Fatalf("missing cwd attr: %s", raw)
			}
			if tc.cmd.Cwd == "" && strings.Contains(raw, `<cmd cwd=`) {
				t.Fatalf("unexpected cwd attr: %s", raw)
			}
			if tc.cmd.Prompt == "" && strings.Contains(raw, `<prompt>`) {
				t.Fatalf("unexpected prompt tag: %s", raw)
			}
			// Literal [Image #N] inside command/prompt must be protected from flat-text remappers.
			if strings.Contains(raw, "[Image #") {
				t.Fatalf("unprotected image marker in serialized XML: %s", raw)
			}

			parts := ParseUserCommands(raw)
			if len(parts) != 1 || parts[0].Command == nil {
				t.Fatalf("ParseUserCommands = %+v", parts)
			}
			if parts[0].Text != raw {
				t.Fatalf("Text not lossless:\ngot:  %q\nwant: %q", parts[0].Text, raw)
			}
			got := *parts[0].Command
			if got != tc.cmd {
				t.Fatalf("round-trip mismatch:\ngot:  %+v\nwant: %+v", got, tc.cmd)
			}
		})
	}
}

func TestParseUserCommandsSurroundingProseAndMalformed(t *testing.T) {
	block := FormatUserCommand(UserCommand{
		Label:  "Unit Tests",
		Cwd:    "pkg",
		Cmd:    "npm test",
		Prompt: "Run tests.",
	})
	input := "Before note.\n\n" + block + "\n\nAfter note."
	parts := ParseUserCommands(input)
	if len(parts) != 3 {
		t.Fatalf("len(parts) = %d, want 3: %+v", len(parts), parts)
	}
	if parts[0].Command != nil || parts[0].Text != "Before note.\n\n" {
		t.Fatalf("parts[0] = %+v", parts[0])
	}
	if parts[1].Command == nil || parts[1].Command.Label != "Unit Tests" || parts[1].Command.Cmd != "npm test" {
		t.Fatalf("parts[1] = %+v", parts[1])
	}
	if parts[2].Command != nil || parts[2].Text != "\n\nAfter note." {
		t.Fatalf("parts[2] = %+v", parts[2])
	}

	malformed := []string{
		"<ws:ucmd label=\"Unclosed\">\n<cmd>echo hi</cmd>",
		"<ws:ucmd unknown=\"1\">\n<cmd>echo hi</cmd>\n</ws:ucmd>",
		"<ws:ucmd edited=\"false\">\n<cmd>echo hi</cmd>\n</ws:ucmd>",
		"<ws:ucmd>\n<cmd></cmd>\n</ws:ucmd>",
		"<ws:ucmd>\n<cmd>echo <raw></cmd>\n</ws:ucmd>",
		"inline " + block,
		block + " trailing",
		"<ws:ucmd>\n<other/>\n<cmd>echo hi</cmd>\n</ws:ucmd>",
		"<ws:ucmd>\n<cmd>echo hi</cmd>\n<prompt>hi</prompt>\nextra\n</ws:ucmd>",
	}
	for _, raw := range malformed {
		parts := ParseUserCommands(raw)
		if len(parts) != 1 || parts[0].Command != nil || parts[0].Text != raw {
			t.Fatalf("malformed %q parsed as %+v", raw, parts)
		}
	}

	// <textref> bodies stay opaque even when they contain a valid <ws:ucmd> block.
	refText, err := TextRefBlock(1, block)
	if err != nil {
		t.Fatal(err)
	}
	refParts := ParseUserCommands(refText)
	if len(refParts) != 1 || refParts[0].Command != nil || refParts[0].Text != refText {
		t.Fatalf("textref was not opaque: %+v", refParts)
	}
}

func TestUserCommandEntryTextAndUserMessageEnvelopeSafety(t *testing.T) {
	// Verify Entry.Text() + ParseUserMessages never mangles </ws:ucmd>
	// (userBodyDelimiter matches </ws:user, not </ws:ucmd>).
	payload := FormatUserCommand(UserCommand{
		Label:  "Check",
		Edited: true,
		Cwd:    "agents-plugin-tool",
		Cmd:    "go test ./... && echo done",
		Prompt: "Please run and verify this command.",
	})
	offset := 540
	entry := Entry{
		SentAt:           1700000000000,
		UTCOffsetMinutes: &offset,
		Parts:            []string{payload},
	}
	wire := entry.Text()
	if strings.Contains(wire, "&lt;/ws:ucmd>") {
		t.Fatalf("Entry.Text() mangled </ws:ucmd>: %s", wire)
	}
	blocks, ok := ParseUserMessages(wire)
	if !ok || len(blocks) != 1 {
		t.Fatalf("ParseUserMessages ok=%v blocks=%+v", ok, blocks)
	}
	if blocks[0].Text != payload {
		t.Fatalf("unwrapped body mismatch:\ngot:  %q\nwant: %q", blocks[0].Text, payload)
	}
	parsed := ParseUserCommands(blocks[0].Text)
	if len(parsed) != 1 || parsed[0].Command == nil {
		t.Fatalf("ParseUserCommands after envelope unwrap = %+v", parsed)
	}
	if parsed[0].Command.Cmd != "go test ./... && echo done" || !parsed[0].Command.Edited {
		t.Fatalf("parsed command = %+v", parsed[0].Command)
	}
}

func TestUserCommandSteerWithAdmissionPreservesPayloadT0(t *testing.T) {
	dir := t.TempDir()
	payload := FormatUserCommand(UserCommand{
		Label:  "Deploy Check",
		Cwd:    "scripts",
		Cmd:    "sh check.sh && echo 'ok > 0'",
		Prompt: "Verify deployment readiness.",
	})
	offset := 540
	name, err := writeEntry(dir, Entry{
		SentAt:           1700000000000,
		UTCOffsetMinutes: &offset,
		Parts:            []string{payload},
	})
	if err != nil {
		t.Fatalf("writeEntry: %v", err)
	}
	steeredName, err := SteerWithAdmission(dir, name, func(e Entry) error {
		if len(e.Parts) != 1 || e.Parts[0] != payload {
			t.Fatalf("admit saw altered parts: %+v", e.Parts)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("SteerWithAdmission: %v", err)
	}
	held, err := Held(dir)
	if err != nil {
		t.Fatalf("Held: %v", err)
	}
	if len(held) != 1 || held[0].Name != steeredName {
		t.Fatalf("Held = %+v", held)
	}
	if !held[0].Entry.Steer {
		t.Fatalf("expected Steer=true")
	}
	if len(held[0].Entry.Parts) != 1 || held[0].Entry.Parts[0] != payload {
		t.Fatalf("steered entry lost payload:\ngot:  %q\nwant: %q", held[0].Entry.Parts, payload)
	}
	parsed := ParseUserCommands(held[0].Entry.Parts[0])
	if len(parsed) != 1 || parsed[0].Command == nil || parsed[0].Command.Cmd != "sh check.sh && echo 'ok > 0'" {
		t.Fatalf("parsed steered command = %+v", parsed)
	}
}
