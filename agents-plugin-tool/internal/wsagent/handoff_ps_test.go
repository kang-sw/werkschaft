package wsagent

import (
	"slices"
	"strings"
	"testing"
)

// TestResumeLineArgvThroughPowerShell checks what a Windows resume line
// actually hands the harness. It models the pipeline: PowerShell's
// tokenizer (verbatim strings, doubled quote marks, the typographic single quotes),
// Windows PowerShell 5.1's legacy native command line (whitespace quoted,
// nothing escaped), cmd.exe's re-read when the harness is an npm .cmd/.bat
// shim, and the harness's argv split (CommandLineToArgvW). A rendered line
// must reach the harness as exactly [flag, value] for both an .exe and a
// .cmd harness; a value it cannot carry gets comment-only steps.
func TestResumeLineArgvThroughPowerShell(t *testing.T) {
	setHostWindows(t, true)
	dir := `C:\Users\O'Neil\my repo & co`
	values := []string{
		"abc-1", "agy 1", `C:\h\.pi\s 1.jsonl`, `C:\Users\O'Neil\s.jsonl`, "it\u2019s", "a$b`c(d);e,f=g",
		"a&b", "100%x%", `say "hi"`, "a\nb", `C:\dir with space\`, "a^b", "a|b", "a<b>",
	}
	for _, value := range values {
		line := resumeLine(dir, "pi", "--session", value)
		if strings.HasPrefix(line, "#") {
			for _, l := range strings.Split(line, "\n") {
				if !strings.HasPrefix(l, "# ") {
					t.Errorf("steps for %q hold a runnable line %q", value, l)
				}
			}
			if !strings.Contains(line, resumeShown(value)) || !strings.Contains(line, dir) {
				t.Errorf("steps for %q do not show the directory and value: %q", value, line)
			}
			exeOK := slices.Equal(nativeArgv([]string{"--session", value}, false), []string{"--session", value})
			cmdOK := slices.Equal(nativeArgv([]string{"--session", value}, true), []string{"--session", value})
			if exeOK && cmdOK {
				t.Errorf("value %q would pass intact yet got steps", value)
			}
			continue
		}
		statements := psStatements(t, line)
		if len(statements) != 2 {
			t.Fatalf("%q: statements %q", line, statements)
		}
		if loc := statements[0]; !slices.Equal(loc, []string{"Set-Location", "-LiteralPath", dir, "-ErrorAction", "Stop"}) {
			t.Errorf("%q: Set-Location tokens %q", line, loc)
		}
		run := statements[1]
		if len(run) != 3 || run[0] != "pi" {
			t.Fatalf("%q: harness tokens %q", line, run)
		}
		for _, batch := range []bool{false, true} {
			if got := nativeArgv(run[1:], batch); !slices.Equal(got, []string{"--session", value}) {
				t.Errorf("%q (batch=%v): harness argv %q, want [--session %q]", line, batch, got, value)
			}
		}
	}
}

func isPSSingleQuote(r rune) bool { return r == '\'' || r >= '\u2018' && r <= '\u201b' }

// psStatements tokenizes a line of bare words and verbatim strings
// separated by spaces and ";", as PowerShell's tokenizer reads them.
func psStatements(t *testing.T, line string) [][]string {
	t.Helper()
	var out [][]string
	var cur []string
	var tok strings.Builder
	inTok := false
	rs := []rune(line)
	flush := func() {
		if inTok {
			cur = append(cur, tok.String())
			tok.Reset()
			inTok = false
		}
	}
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case isPSSingleQuote(r):
			inTok = true
			for i++; ; i++ {
				if i >= len(rs) {
					t.Fatalf("unterminated string in %q", line)
				}
				if isPSSingleQuote(rs[i]) {
					if i+1 < len(rs) && isPSSingleQuote(rs[i+1]) {
						i++
						tok.WriteRune(rs[i])
						continue
					}
					break
				}
				tok.WriteRune(rs[i])
			}
		case r == ' ':
			flush()
		case r == ';':
			flush()
			out = append(out, cur)
			cur = nil
		case r == '\n' || r == '"' || r == '$' || r == '`' || r == '&' || r == '|':
			t.Fatalf("PowerShell would interpret %q unquoted in %q", r, line)
		default:
			inTok = true
			tok.WriteRune(r)
		}
	}
	flush()
	return append(out, cur)
}

// nativeArgv is the argv a harness receives for args from Windows
// PowerShell 5.1 (legacy passing), through cmd.exe when batch, or nil when
// cmd.exe would re-read part of the line.
func nativeArgv(args []string, batch bool) []string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		parts[i] = a
	}
	line := strings.Join(parts, " ")
	if batch {
		if strings.ContainsAny(line, "%\n\r") {
			return nil
		}
		quoted := false
		for _, r := range line {
			if r == '"' {
				quoted = !quoted
			} else if !quoted && strings.ContainsRune("&|<>^", r) {
				return nil
			}
		}
	}
	return splitWindowsArgs(line)
}

// splitWindowsArgs splits a command line as CommandLineToArgvW does (the
// program name already removed).
func splitWindowsArgs(line string) []string {
	var out []string
	var arg strings.Builder
	inArg, quoted := false, false
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\':
			n := 0
			for i < len(rs) && rs[i] == '\\' {
				n++
				i++
			}
			if i < len(rs) && rs[i] == '"' {
				arg.WriteString(strings.Repeat(`\`, n/2))
				if n%2 == 1 {
					arg.WriteRune('"')
				} else {
					quoted = !quoted
				}
			} else {
				arg.WriteString(strings.Repeat(`\`, n))
				i--
			}
			inArg = true
		case r == '"':
			if quoted && i+1 < len(rs) && rs[i+1] == '"' {
				arg.WriteRune('"')
				i++
			} else {
				quoted = !quoted
			}
			inArg = true
		case (r == ' ' || r == '\t') && !quoted:
			if inArg {
				out = append(out, arg.String())
				arg.Reset()
				inArg = false
			}
		default:
			arg.WriteRune(r)
			inArg = true
		}
	}
	if inArg {
		out = append(out, arg.String())
	}
	return out
}
