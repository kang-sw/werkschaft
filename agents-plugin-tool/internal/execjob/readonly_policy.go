package execjob

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	goruntime "runtime"

	"github.com/kang-sw/devenv/internal/wsgit"
)

// readonly_policy.go is the command policy of a read-only worker's exec
// launches (261123-bug-ws-exec-cutover-worker-authority). The worker may
// launch only a command made of allowlisted read programs. The gate parses
// the line into words without running it, resolves each program against the
// launch's PATH, and launches a line re-serialized from the checked words
// (every word single-quoted) under an allowlisted environment, so what is
// checked is exactly what runs. Anything not provably read-only is refused.

// ErrReadOnlyLaunch is the refusal of a read-only worker's launch.
type ErrReadOnlyLaunch struct{ Reason string }

func (e *ErrReadOnlyLaunch) Error() string {
	return "permission denied: a read-only worker's exec launches are limited to read commands (" + e.Reason + "). Do the read-only part of the task and put the change you would make in your report."
}

func refuse(format string, a ...any) error {
	return &ErrReadOnlyLaunch{Reason: fmt.Sprintf(format, a...)}
}

// readOnlyPrograms are the bare program names a read-only launch may run.
var readOnlyPrograms = map[string]bool{
	"git": true, "ls": true, "cat": true, "head": true, "tail": true, "wc": true, "grep": true, "rg": true,
	"cut": true, "tr": true, "nl": true, "diff": true, "cmp": true, "comm": true, "stat": true, "file": true,
	"pwd": true, "echo": true, "basename": true, "dirname": true, "realpath": true, "du": true, "tree": true,
	"which": true, "jq": true, "find": true, "sort": true, "uniq": true, "sed": true,
}

var readOnlyGitSubcommands = map[string]bool{
	"status": true, "log": true, "show": true, "diff": true, "blame": true, "grep": true, "ls-files": true,
	"ls-tree": true, "rev-parse": true, "rev-list": true, "merge-base": true, "cat-file": true, "describe": true,
	"shortlog": true, "for-each-ref": true, "name-rev": true,
}

// gitRefusedOptions are long options that write a file or run a helper;
// git accepts any unambiguous prefix of a long option, so a prefix of one
// of these names (or an extension of one) is refused as well.
var gitRefusedOptions = []string{"output", "ext-diff", "open-files-in-pager", "textconv", "filters", "show-signature"}

var gitPrettySignature = regexp.MustCompile(`%[+ -]?G`)
var gitRefSignature = regexp.MustCompile(`%\(\*?signature[:)]`)
var gitSortSignature = regexp.MustCompile(`^-?(v:|version:)?\*?signature(:|$)`)

var readOnlySedScript = regexp.MustCompile(`^(\d+|\$)(,(\d+|\$))?p$`)

// checkReadOnlyArgv refuses argv (argv[0] a bare program name) unless it is
// an allowlisted read command with no option that writes or runs a program.
func checkReadOnlyArgv(argv []string) error {
	if len(argv) == 0 || argv[0] == "" {
		return refuse("no program")
	}
	name, args := argv[0], argv[1:]
	if strings.ContainsAny(name, `/\`) {
		return refuse("program %q: the program must be a bare name, not a path", name)
	}
	if !readOnlyPrograms[name] {
		return refuse("program %q is not an allowed read command", name)
	}
	if goruntime.GOOS == "windows" {
		if err := windowsWordRefusal(argv); err != nil {
			return err
		}
		if name == "sort" {
			if err := windowsSortRefusal(args); err != nil {
				return err
			}
		}
	}
	switch name {
	case "git":
		return checkGit(args)
	case "find":
		for _, a := range args {
			switch {
			case a == "-exec" || a == "-execdir" || a == "-ok" || a == "-okdir" || a == "-delete", strings.HasPrefix(a, "-fprint"), a == "-fls":
				return refuse("find %s writes files or runs programs", a)
			}
		}
	case "sort":
		for _, a := range args {
			if shortHas(a, "oT") || longMatches(a, "output", "compress-program", "temporary-directory") {
				return refuse("sort %s writes files or runs programs", a)
			}
		}
	case "uniq":
		// Stdin only: a platform's uniq opens its second operand for
		// writing, and parses options differently around the first operand,
		// so no operand (a bare - or --, which open one) is admitted.
		for _, a := range args {
			if a == "-" || a == "--" || !strings.HasPrefix(a, "-") {
				return refuse("uniq reads stdin only here: %q is an operand (the second operand names an output file)", a)
			}
		}
	case "sed":
		if len(args) < 2 || args[0] != "-n" || !readOnlySedScript.MatchString(args[1]) {
			return refuse("sed is limited to -n with a line-address print script such as '10,40p'")
		}
		for _, a := range args[2:] {
			if strings.HasPrefix(a, "-") && a != "-" {
				return refuse("sed option %s", a)
			}
		}
	case "tree":
		for _, a := range args {
			if shortHas(a, "oR") || longMatches(a, "output") {
				return refuse("tree %s writes files or runs a program", a)
			}
		}
	case "file":
		for _, a := range args {
			if shortHas(a, "Cp") || longMatches(a, "compile", "preserve-date") {
				return refuse("file %s writes a file or file metadata", a)
			}
			if shortHas(a, "zZ") || longMatches(a, "uncompress", "uncompress-noreport") {
				return refuse("file %s can run a decompression program", a)
			}
		}
	case "diff":
		for _, a := range args {
			if shortHas(a, "l") || longMatches(a, "paginate") {
				return refuse("diff %s runs a pagination program", a)
			}
		}
	case "rg":
		for _, a := range args {
			if shortHas(a, "z") || longExact(a, "pre", "pre-glob", "search-zip", "hostname-bin") {
				return refuse("rg %s runs a program", a)
			}
		}
	}
	return nil
}

// windowsWordRefusal refuses a word the MSYS runtime would read differently
// from the checked vector when its parent is a native process: it takes a
// double quote as an argument boundary (it ignores Python's backslash
// escape, also inside the Bash -c script) and expands a word starting with @
// as a response file.
func windowsWordRefusal(words []string) error {
	for _, w := range words {
		// A single quote is quoting syntax and a control character (CR, LF
		// included) a separator to the MSYS runtime; a tab is handled below.
		if i := strings.IndexFunc(w, func(r rune) bool { return r == '\'' || r == 0x7f || (r < 0x20 && r != '\t') }); i >= 0 {
			return refuse("word %q: a single quote or control character (such as a line break) is refused on Windows, where the command line is parsed again by the MSYS runtime", w)
		}
		if strings.Contains(w, `"`) {
			return refuse("word %q: a double quote is refused on Windows, where the command line is parsed again by the MSYS runtime", w)
		}
		// Python quotes a word with whitespace (or an empty one) and doubles
		// its trailing backslashes; MSYS keeps them doubled.
		if (w == "" || strings.ContainsAny(w, " \t")) && strings.HasSuffix(w, `\`) {
			return refuse("word %q: a word with whitespace that ends in a backslash is refused on Windows, where the command line is parsed again by the MSYS runtime", w)
		}
		if strings.HasPrefix(w, "@") {
			return refuse("word %q: a word starting with @ is refused on Windows, where the MSYS runtime reads it as a response file", w)
		}
	}
	return nil
}

// windowsSortRefusal refuses every slash-leading word of a sort launch. The
// PATH may resolve sort to Windows' native sort.exe (System32 ahead of Git's
// usr\bin), which reads /O file (and /OUTPUT, in any letter case) as an output
// file and /T dir as a temporary directory. Its slash options are not
// enumerated: a prefix or case variant it accepts would slip past a list, and
// no read-only use needs a slash-leading word (an absolute path argument
// reads the same through a relative one or stdin). It is a pure argument
// check, so it runs on any platform.
func windowsSortRefusal(args []string) error {
	for _, a := range args {
		if strings.HasPrefix(a, "/") {
			return refuse("sort %s: a slash-leading word is refused on Windows, where the native sort.exe reads it as an option (/O writes a file)", a)
		}
	}
	return nil
}

// shortHas reports whether a is a short-option cluster (-abc) naming any of
// the letters.
func shortHas(a, letters string) bool {
	return len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.ContainsAny(a[1:], letters)
}

// longName is the option name of a long option argument ("--out=x" ->
// "out"), "" when a is not one.
func longName(a string) string {
	if len(a) < 3 || !strings.HasPrefix(a, "--") {
		return ""
	}
	name, _, _ := strings.Cut(a[2:], "=")
	return name
}

// longExact reports whether a is exactly one of the long options.
func longExact(a string, names ...string) bool {
	n := longName(a)
	for _, want := range names {
		if n == want {
			return true
		}
	}
	return false
}

// longMatches reports whether a is, or abbreviates, or extends one of the
// long options.
func longMatches(a string, names ...string) bool {
	n := longName(a)
	if n == "" {
		return false
	}
	for _, want := range names {
		if strings.HasPrefix(want, n) || strings.HasPrefix(n, want) {
			return true
		}
	}
	return false
}

func checkGit(args []string) error {
	i := 0
	for i < len(args) && (args[i] == "--no-pager" || args[i] == "--no-optional-locks") {
		i++
	}
	if i >= len(args) {
		return refuse("git needs a read subcommand")
	}
	sub := args[i]
	if !readOnlyGitSubcommands[sub] {
		return refuse("git %q is not an allowed read subcommand (global options such as -c are refused)", sub)
	}
	rest := args[i+1:]
	for j, a := range rest {
		// O (git grep's pager, git diff's order file) anywhere in a short
		// cluster is refused: a cluster's later letters may be its value.
		if shortHas(a, "O") || longMatches(a, gitRefusedOptions...) || longMatches(a, "remerge-diff") {
			return refuse("git %s %s writes a file or runs a program", sub, a)
		}
		if sub == "describe" && longMatches(a, "dirty", "broken") {
			return refuse("git describe %s refreshes the index", a)
		}
		n := longName(a)
		if n == "" {
			continue
		}
		_, value, hasValue := strings.Cut(a, "=")
		if !hasValue && j+1 < len(rest) {
			value = rest[j+1]
		}
		if strings.HasPrefix("diff-merges", n) && (value == "r" || (value != "" && strings.HasPrefix("remerge", value))) {
			return refuse("git %s %s writes objects into the repository", sub, a)
		}
		if strings.HasPrefix("submodule", n) && hasValue && value == "diff" {
			return refuse("git %s %s can run helpers during an inline submodule diff", sub, a)
		}
		// The pretty and ref format languages can start a signature verifier
		// even without --show-signature. Check both inline and separate values.
		switch sub {
		case "log", "show", "rev-list", "shortlog":
			if (strings.HasPrefix("format", n) || strings.HasPrefix("pretty", n) || (sub == "shortlog" && strings.HasPrefix("group", n))) && gitPrettySignature.MatchString(value) {
				return refuse("git %s signature formats run a verifier", sub)
			}
		case "for-each-ref":
			if (strings.HasPrefix("format", n) && gitRefSignature.MatchString(value)) || (strings.HasPrefix("sort", n) && gitSortSignature.MatchString(value)) {
				return refuse("git %s signature fields run a verifier", sub)
			}
		}
	}
	return nil
}

// word is one parsed shell word: its literal text.
type word struct {
	text string
}

// parseReadOnlyLine parses line into pipeline segments of words and
// redirects, refusing everything outside the allowed shell subset. Nothing
// runs and nothing is read.
func parseReadOnlyLine(line string) ([]segment, error) {
	var segs []segment
	var cur segment
	var buf strings.Builder
	var inWord, hasUnquotedMeta bool
	start := true // at the start of a word position, for ~ and #
	flush := func() error {
		if !inWord {
			return nil
		}
		text := buf.String()
		inWord = false
		meta := hasUnquotedMeta
		buf.Reset()
		hasUnquotedMeta = false
		if meta {
			switch text {
			case "2>&1", ">/dev/null", "2>/dev/null":
				if len(cur.words) == 0 {
					return refuse("a redirect cannot start a command")
				}
				cur.redirects = append(cur.redirects, text)
				return nil
			}
			return refuse("redirection or control operator in %q (only 2>&1, >/dev/null and 2>/dev/null are allowed)", text)
		}
		cur.words = append(cur.words, word{text: text})
		return nil
	}
	begin := func() { inWord = true }
	rs := []rune(line)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch r {
		case ' ', '\t':
			if err := flush(); err != nil {
				return nil, err
			}
			start = true
			continue
		case '\n', '\r':
			return nil, refuse("newline or command list")
		case ';':
			return nil, refuse("command list operator ;")
		case '(', ')':
			return nil, refuse("subshell or substitution %c", r)
		case '`':
			return nil, refuse("command substitution `")
		case '<':
			return nil, refuse("input redirection, here-doc or process substitution <")
		case '|':
			if err := flush(); err != nil {
				return nil, err
			}
			if i+1 < len(rs) && (rs[i+1] == '|' || rs[i+1] == '&') {
				return nil, refuse("control operator %c%c", r, rs[i+1])
			}
			if len(cur.words) == 0 {
				return nil, refuse("empty pipeline segment")
			}
			segs = append(segs, cur)
			cur = segment{}
			start = true
			continue
		case '{', '}':
			return nil, refuse("brace expansion or group %c (quote it to pass it literally)", r)
		case '*', '?', '[':
			return nil, refuse("unquoted glob character %c (quote the pattern, e.g. find . -name '*.go')", r)
		case '$':
			return nil, refuse("unquoted $ (variable, arithmetic or command substitution)")
		case '~':
			if start {
				return nil, refuse("unquoted word starting with ~")
			}
		case '#':
			if start {
				return nil, refuse("unquoted # (a comment)")
			}
		case '&', '>':
			begin()
			hasUnquotedMeta = true
			buf.WriteRune(r)
			start = false
			continue
		case '\'':
			begin()
			i++
			for i < len(rs) && rs[i] != '\'' {
				buf.WriteRune(rs[i])
				i++
			}
			if i >= len(rs) {
				return nil, refuse("unterminated single quote")
			}
			start = false
			continue
		case '"':
			begin()
			i++
			closed := false
			for ; i < len(rs); i++ {
				c := rs[i]
				if c == '"' {
					closed = true
					break
				}
				if c == '$' || c == '`' {
					return nil, refuse("$ or backtick inside double quotes (use single quotes)")
				}
				if c == '\\' && i+1 < len(rs) {
					if n := rs[i+1]; n == '"' || n == '\\' {
						buf.WriteRune(n)
						i++
						continue
					} else if n == '\n' {
						return nil, refuse("newline")
					}
				}
				buf.WriteRune(c)
			}
			if !closed {
				return nil, refuse("unterminated double quote")
			}
			start = false
			continue
		case '\\':
			if i+1 >= len(rs) {
				return nil, refuse("trailing backslash")
			}
			i++
			if rs[i] == '\n' || rs[i] == '\r' {
				return nil, refuse("newline")
			}
			begin()
			buf.WriteRune(rs[i])
			start = false
			continue
		}
		begin()
		buf.WriteRune(r)
		start = false
	}
	if err := flush(); err != nil {
		return nil, err
	}
	if len(cur.words) == 0 {
		return nil, refuse("empty pipeline segment")
	}
	segs = append(segs, cur)
	return segs, nil
}

type segment struct {
	words     []word
	redirects []string
}

var assignmentWord = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*=`)

// readOnlyEnvironment is environ cut to the allowlisted names plus the
// settings that keep a read from writing or paging.
func readOnlyEnvironment(environ []string) []string {
	win := goruntime.GOOS == "windows"
	allowed := map[string]bool{"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "LANG": true, "TERM": true, "TMPDIR": true, "TZ": true}
	if win {
		for _, n := range []string{"SystemRoot", "SystemDrive", "windir", "TEMP", "TMP", "USERPROFILE", "LOCALAPPDATA", "APPDATA", "ComSpec", "PATHEXT", "HOMEDRIVE", "HOMEPATH"} {
			allowed[n] = true
		}
	}
	var out []string
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		key := name
		if win {
			for n := range allowed {
				if strings.EqualFold(n, name) {
					key = n
				}
			}
		}
		if allowed[key] || strings.HasPrefix(name, "LC_") {
			out = append(out, kv)
		}
	}
	out = append(out,
		"GIT_OPTIONAL_LOCKS=0", "GIT_PAGER=cat", "PAGER=cat",
		// No lazy fetch of a partial clone's missing objects (Git 2.44 and
		// later; older Git ignores it), which would write a packfile.
		"GIT_NO_LAZY_FETCH=1",
		// git diff refreshes a stale index by default, which rewrites it.
		// Env-supplied config outranks the repository's. log.diffMerges
		// pins what -m and --diff-merges=on select: a repository setting of
		// remerge would write objects into the repository.
		"GIT_CONFIG_COUNT=2", "GIT_CONFIG_KEY_0=diff.autoRefreshIndex", "GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=log.diffMerges", "GIT_CONFIG_VALUE_1=separate")
	if win {
		// Git Bash must pass the checked words to a native process as they are.
		// MSYS/Cygwin programs also expand wildcards in their own command
		// line at startup; noglob keeps the argv as checked.
		out = append(out, "MSYS_NO_PATHCONV=1", "MSYS2_ARG_CONV_EXCL=*", "MSYS=noglob", "CYGWIN=noglob")
	}
	return out
}

// resolveReadOnly resolves bare name on environ's PATH to the absolute path
// the launch runs. A relative result would resolve against the job's cwd,
// and on Windows only native executables start without a system shell
// parsing the arguments.
func resolveReadOnly(name string, environ []string) (string, error) {
	bin, err := wsgit.LookPathEnv(name, environ)
	if err != nil {
		return "", refuse("%s is not on the job's PATH", name)
	}
	return checkReadOnlyExecutable(name, bin)
}

func checkReadOnlyExecutable(name, bin string) (string, error) {
	if !filepath.IsAbs(bin) {
		return "", refuse("%s resolves to %q, which is not an absolute path", name, bin)
	}
	if goruntime.GOOS == "windows" {
		if ext := strings.ToLower(filepath.Ext(bin)); ext != ".exe" && ext != ".com" {
			return "", refuse("%s resolves to %q, which is not a native executable (.exe or .com)", name, bin)
		}
	}
	return bin, nil
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// ReadOnlyShellLaunch is ShellLaunch for a read-only worker: the line is
// parsed and checked, and the launched line is re-serialized from the
// checked words. An environment override is refused.
func ReadOnlyShellLaunch(owner, tool, root, cwdArg, line string, environ []string, overlay map[string]string) (HostedLaunch, error) {
	if len(overlay) > 0 {
		return HostedLaunch{}, refuse("an environment override (env) can run programs")
	}
	cwd, err := ResolveWorkingDir(root, cwdArg)
	if err != nil {
		return HostedLaunch{}, err
	}
	if strings.TrimSpace(line) == "" {
		return HostedLaunch{}, fmt.Errorf("%s: cmd is required", tool)
	}
	segs, err := parseReadOnlyLine(line)
	if err != nil {
		return HostedLaunch{}, err
	}
	env := readOnlyEnvironment(environ)
	parts := make([]string, 0, len(segs))
	for _, seg := range segs {
		argv := make([]string, len(seg.words))
		for i, w := range seg.words {
			argv[i] = w.text
		}
		if assignmentWord.MatchString(argv[0]) {
			return HostedLaunch{}, refuse("variable assignment before the command: %q", argv[0])
		}
		if err := checkReadOnlyArgv(argv); err != nil {
			return HostedLaunch{}, err
		}
		bin, err := resolveReadOnly(argv[0], env)
		if err != nil {
			return HostedLaunch{}, err
		}
		if goruntime.GOOS == "windows" {
			bin = filepath.ToSlash(bin)
		}
		quoted := make([]string, len(argv))
		quoted[0] = shQuote(bin)
		for i := 1; i < len(argv); i++ {
			quoted[i] = shQuote(argv[i])
		}
		parts = append(parts, strings.Join(append(quoted, seg.redirects...), " "))
	}
	shellArgv, err := ShellArgv(strings.Join(parts, " | "), env)
	if err != nil {
		return HostedLaunch{}, err
	}
	if _, err := checkReadOnlyExecutable("the shell", shellArgv[0]); err != nil {
		return HostedLaunch{}, err
	}
	if base := strings.ToLower(filepath.Base(shellArgv[0])); strings.HasPrefix(base, "bash") {
		shellArgv = append([]string{shellArgv[0], "--noprofile", "--norc"}, shellArgv[1:]...)
	}
	return HostedLaunch{Owner: owner, Tool: tool, Root: root, Cwd: cwd, Environ: env, Argv: shellArgv, Display: line, Command: line, Shell: shellArgv[0]}, nil
}

// ReadOnlyArgvLaunch is the exec.run launch of a read-only worker: argv's
// program is checked and resolved, and the process starts under the
// allowlisted environment.
func ReadOnlyArgvLaunch(owner, tool, root, cwd string, argv []string, environ []string, overlay map[string]string) (HostedLaunch, error) {
	if len(overlay) > 0 {
		return HostedLaunch{}, refuse("an environment override (env) can run programs")
	}
	if err := checkReadOnlyArgv(argv); err != nil {
		return HostedLaunch{}, err
	}
	env := readOnlyEnvironment(environ)
	bin, err := resolveReadOnly(argv[0], env)
	if err != nil {
		return HostedLaunch{}, err
	}
	run := append([]string{bin}, argv[1:]...)
	return HostedLaunch{Owner: owner, Tool: tool, Root: root, Cwd: cwd, Environ: env, Argv: run}, nil
}
