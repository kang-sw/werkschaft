package wsredact

import (
	"math/rand"
	"regexp"
	"strings"
	"testing"
)

// TestURLCredentials pins the URL rule the tracker's remote display shares.
func TestURLCredentials(t *testing.T) {
	for in, want := range map[string]string{
		"https://x-access-token:ghp_secret@github.com/o/r.git": "https://***@github.com/o/r.git",
		"git@github.com:o/r.git":                               "git@github.com:o/r.git",
		"https://user:p@ss@host/x.git":                         "https://***@host/x.git",
		"https://host/r.git?private_token=glpat-x&ref=main":    "https://host/r.git?private_token=***&ref=***",
		"see https://host/docs?q=1 and ssh://h/r?x=1":          "see https://host/docs?q=*** and ssh://h/r?x=1",
	} {
		if got := URLCredentials(in); got != want {
			t.Errorf("URLCredentials(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSecretsMasksEachRule pins every rule with a positive case.
func TestSecretsMasksEachRule(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"url userinfo", "git clone https://u:hunter2@host/r.git", "git clone https://***@host/r.git"},
		{"url query", "curl https://api/x?access_token=abc&v=1", "curl https://api/x?access_token=***&v=***"},
		{"aws key id", "AWS_ACCESS_KEY_ID AKIAIOSFODNN7EXAMPLE set", "AWS_ACCESS_KEY_ID *** set"},
		{"aws session key id", "id ASIAY34FZKBOKMUTVV7A", "id ***"},
		{"github classic", "echo ghp_abcdefghijklmnopqrstuvwxyz0123456789", "echo ***"},
		{"github oauth", "gho_16C7e42F292c6912E7710c838347Ae178B4a", "***"},
		{"github user-to-server", "ghu_16C7e42F292c6912E7710c838347Ae178B4a", "***"},
		{"github server", "x ghs_16C7e42F292c6912E7710c838347Ae178B4a y", "x *** y"},
		{"github refresh", "ghr_16C7e42F292c6912E7710c838347Ae178B4a", "***"},
		{"github fine-grained", "github_pat_11ABCDEFG0123456789_abcdefghijklmnop", "***"},
		{"github cut by the preview cap", "command: echo ghp_abcdefghij…", "command: echo ***…"},
		{"gitlab", "glpat-xxxxxxxxxxxxxxxxxxxx", "***"},
		{"slack", "xoxb-1234567890-abcdefghij", "***"},
		{"slack user", "xoxp-1234567890-abcdefghij", "***"},
		{"openai", "sk-abcdefghijklmnopqrstuvwxyz012345", "***"},
		{"openai project", "OPENAI sk-proj-abcdefghijklmnopqrstuvwxyz_01", "OPENAI ***"},
		{"anthropic", "sk-ant-api03-abcdefghijklmnop", "***"},
		{"google", "key AIzaSyA-1234567890abcdefghijklmnopqrs", "key ***"},
		{"jwt", "jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U done", "jwt *** done"},
		{"jwt cut", "t=eyJhbGciOiJIUzI1NiJ9.eyJzdW", "t=***"},
		{"pem block", "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY----- tail", "-----BEGIN RSA PRIVATE KEY-----***-----END RSA PRIVATE KEY----- tail"},
		{"pem cut", "content: -----BEGIN PRIVATE KEY----- MIIEvQIBADANBgkqhkiG9w0BAQEFAASC…", "content: -----BEGIN PRIVATE KEY-----***"},
		{"pem openssh", "-----BEGIN OPENSSH PRIVATE KEY-----b3BlbnNzaC1rZXk=-----END OPENSSH PRIVATE KEY-----", "-----BEGIN OPENSSH PRIVATE KEY-----***-----END OPENSSH PRIVATE KEY-----"},
		{"authorization bearer", `curl -H "Authorization: Bearer abc.def" x`, `curl -H "Authorization: Bearer ***" x`},
		{"authorization basic", "authorization=basic dXNlcjpwYXNz", "authorization=basic ***"},
		{"authorization token", "Authorization: token abc123", "Authorization: token ***"},
		{"key=value", "PASSWORD=hunter2 ./run", "PASSWORD=*** ./run"},
		{"prefixed key=value", "export DB_PASSWORD=hunter2", "export DB_PASSWORD=***"},
		{"env token", "GITHUB_TOKEN=abc make", "GITHUB_TOKEN=*** make"},
		{"key: value", "secret: s3cr3t, path: /a", "secret: ***, path: /a"},
		{"json pair", `{"api_key": "abc def", "n": 1}`, `{"api_key": "***", "n": 1}`},
		{"single quoted", "passwd='p w'", "passwd='***'"},
		{"apikey", "apikey=abc", "apikey=***"},
		{"api-key", "api-key: abc", "api-key: ***"},
		{"access_key", "aws_secret_access_key=abc", "aws_secret_access_key=***"},
		{"private_key", "private_key: abc", "private_key: ***"},
		{"client_secret", "client_secret=abc&x=1", "client_secret=***&x=1"},
		{"auth", "auth: abc", "auth: ***"},
		{"pwd", "pwd=abc", "pwd=***"},
		{"case-insensitive", "Token: abc", "Token: ***"},
		{"flag value", "mysql --password hunter2 -u root", "mysql --password *** -u root"},
		{"flag=value", "gh auth login --with-token=abc", "gh auth login --with-token=***"},
		{"flag quoted", `tool --api-key "a b"`, `tool --api-key "***"`},
	} {
		if got := Secrets(c.in); got != c.want {
			t.Errorf("%s: Secrets(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// TestSecretsLeavesOrdinaryTextAlone pins the negatives: text that only
// resembles a rule survives unchanged.
func TestSecretsLeavesOrdinaryTextAlone(t *testing.T) {
	for _, in := range []string{
		"file_path: /home/user/project/internal/wslive/state.go",
		"command: git show a7b53d6b2c1e4f5a6b7c8d9e0f1a2b3c4d5e6f7a",
		"id 123e4567-e89b-12d3-a456-426614174000",
		"the tokenizer splits tokens: 1000, max_tokens: 4096",
		"pattern: authorization flow, description: rotate the api key",
		"Bearer of bad news",
		"secret garden",
		"command: cd $(pwd) && ls",
		"git@github.com:o/r.git",
		"risk-assessment-for-the-quarterly-plan",
		"AKIA alone",
		"ghp_ short",
		"eyJ",
		"oauthlib: installed",
		"--token --verbose",
		"-----BEGIN PUBLIC KEY----- MIIBIjANBgkq",
		"https://github.com/o/r/pull/12",
	} {
		if got := Secrets(in); got != in {
			t.Errorf("Secrets(%q) = %q, want it unchanged", in, got)
		}
	}
}

// legacySecrets is the pre-Redact implementation of Secrets, kept to pin that
// the span engine writes byte-identical text.
func legacySecrets(s string) string {
	s = URLCredentials(s)
	for _, ru := range rules {
		s = legacyMask(ru.re, s)
	}
	return s
}

func legacyMask(re *regexp.Regexp, s string) string {
	matches := re.FindAllStringSubmatchIndex(s, -1)
	if matches == nil {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		spans := [][2]int{{m[0], m[1]}}
		if len(m) > 2 {
			spans = spans[:0]
			for g := 2; g < len(m); g += 2 {
				if m[g] >= 0 && m[g+1] > m[g] {
					spans = append(spans, [2]int{m[g], m[g+1]})
				}
			}
		}
		for _, sp := range spans {
			b.WriteString(s[last:sp[0]])
			b.WriteString(Mask)
			last = sp[1]
		}
	}
	b.WriteString(s[last:])
	return b.String()
}

var redactCorpus = []string{
	"", "plain", "https://x-access-token:ghp_secret@github.com/o/r.git",
	"https://@host/x", "https://h/p?a=&b&&c=1#f", "https://h/p?token=ghp_abcdefghijklmnop&x=1",
	"password=x.ghp_12345678 and more", "PASSWORD=hunter2 ./run GITHUB_TOKEN=abc",
	`{"api_key": "sk-ant-api03-abcdefghijklmnop", "n": 1}`,
	"-----BEGIN RSA PRIVATE KEY-----\nMIIEpAIBAAKCAQEA\n-----END RSA PRIVATE KEY----- tail",
	"curl -H \"Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIi.sig\" https://u:p@h/x?k=v",
	"mysql --password hunter2 -u root --token=AKIAIOSFODNN7EXAMPLE",
	"token: https://a:b@h/?x=ghp_abcdefghijk", "secret='p w' auth: x",
}

// TestRedactTextEqualsLegacySecrets pins byte-identical output over a corpus
// of overlapping and nested rule matches.
func TestRedactTextEqualsLegacySecrets(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	inputs := append([]string(nil), redactCorpus...)
	for i := 0; i < 400; i++ {
		var b strings.Builder
		for j := 0; j < 1+rng.Intn(5); j++ {
			b.WriteString(redactCorpus[rng.Intn(len(redactCorpus))])
			b.WriteString([]string{" ", "\n", "", "=", "&", "@"}[rng.Intn(6)])
		}
		inputs = append(inputs, b.String())
	}
	for _, in := range inputs {
		got, matches := Redact(in)
		if want := legacySecrets(in); got != want {
			t.Fatalf("Redact(%q) = %q, want %q", in, got, want)
		}
		for i, m := range matches {
			if got[m.Offset:m.Offset+len(Mask)] != Mask {
				t.Fatalf("Redact(%q) match %d offset %d is not a mask in %q", in, i, m.Offset, got)
			}
			if m.OrigOffset < 0 || m.OrigOffset+m.Length > len(in) || m.Rule == "" {
				t.Fatalf("Redact(%q) match %d out of range: %+v", in, i, m)
			}
			if i > 0 && (m.Offset < matches[i-1].Offset+len(Mask) || m.OrigOffset < matches[i-1].OrigOffset+matches[i-1].Length) {
				t.Fatalf("Redact(%q) matches overlap or are unordered: %+v", in, matches)
			}
		}
	}
}

// TestRedactReportsRuleAndOriginalLength pins the match report: the rule
// name, the original length, and never the original text.
func TestRedactReportsRuleAndOriginalLength(t *testing.T) {
	in := "echo ghp_abcdefghijklmnopqrstuvwxyz0123456789 then PASSWORD=hunter2"
	got, matches := Redact(in)
	if got != "echo *** then PASSWORD=***" {
		t.Fatalf("text = %q", got)
	}
	want := []Match{
		{Offset: 5, OrigOffset: 5, Length: 40, Rule: "github_token"},
		{Offset: 23, OrigOffset: 60, Length: 7, Rule: "secret_assignment"},
	}
	if len(matches) != len(want) {
		t.Fatalf("matches = %+v", matches)
	}
	for i := range want {
		if matches[i] != want[i] {
			t.Errorf("match %d = %+v, want %+v", i, matches[i], want[i])
		}
	}
}

// TestRedactNestedMatchCoversBoth pins that a later rule swallowing an
// earlier mask reports one match covering both originals.
func TestRedactNestedMatchCoversBoth(t *testing.T) {
	in := "password=x.ghp_12345678 end"
	got, matches := Redact(in)
	if got != "password=*** end" || len(matches) != 1 {
		t.Fatalf("Redact = %q %+v", got, matches)
	}
	if m := matches[0]; m.Length != len("x.ghp_12345678") || m.OrigOffset != 9 || m.Rule != "secret_assignment" {
		t.Fatalf("match = %+v", m)
	}
}

// TestRedactEmptyQueryValueReportsZeroLength pins the zero-length mask an
// empty query value gets.
func TestRedactEmptyQueryValueReportsZeroLength(t *testing.T) {
	got, matches := Redact("https://h/p?a=&b=1")
	if got != "https://h/p?a=***&b=***" || len(matches) != 2 || matches[0].Length != 0 || matches[1].Length != 1 {
		t.Fatalf("Redact = %q %+v", got, matches)
	}
}

// TestMapOffset pins the input-to-output offset map, including an offset
// inside a masked span, which maps to the mask's end.
func TestMapOffset(t *testing.T) {
	in := "ab PASSWORD=hunter2 cd"
	out, matches := Redact(in)
	if out != "ab PASSWORD=*** cd" {
		t.Fatalf("out = %q", out)
	}
	for orig, want := range map[int]int{0: 0, 12: 12, 13: 15, 18: 15, 19: 15, 20: 16, len(in): len(out)} {
		if got := MapOffset(matches, orig); got != want {
			t.Errorf("MapOffset(%d) = %d, want %d", orig, got, want)
		}
	}
}
