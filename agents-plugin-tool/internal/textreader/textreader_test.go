package textreader

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTailReadAndGrep(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\ntwo regex-42\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tail, err := Tail(path, 2)
	if err != nil || tail != "three\ntwo regex-42\n" {
		t.Fatalf("Tail = %q, %v", tail, err)
	}
	read, err := Read(path, 4, 3)
	if err != nil || read.Text != "two" || read.NextOffset != 7 || read.EOF {
		t.Fatalf("Read = %#v, %v", read, err)
	}
	lit, err := Grep([]string{path}, "regex-42", 1, 0, 10, false)
	if err != nil || len(lit.Matches) != 1 || lit.Matches[0].Before[0] != "three" {
		t.Fatalf("literal Grep = %#v, %v", lit, err)
	}
	re, err := Grep([]string{path}, `regex-\d+`, 0, 0, 10, true)
	if err != nil || len(re.Matches) != 1 || !strings.Contains(re.Matches[0].Text, "regex-42") {
		t.Fatalf("regex Grep = %#v, %v", re, err)
	}
	if _, err := Grep([]string{path}, `(`, 0, 0, 10, true); err == nil {
		t.Fatal("invalid regex returned nil error")
	}
	missing := filepath.Join(t.TempDir(), "missing.txt")
	missingRead, err := Read(missing, 0, 0)
	if err != nil || !missingRead.EOF {
		t.Fatalf("missing Read = %#v, %v", missingRead, err)
	}
	truncated, err := Grep([]string{path}, "two", 0, 0, 1, false)
	if err != nil || len(truncated.Matches) != 1 || !truncated.Truncated {
		t.Fatalf("truncated Grep = %#v, %v", truncated, err)
	}
}

func TestTailReadDefaultsCapsAndEmptyFiles(t *testing.T) {
	dir := t.TempDir()
	empty := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	emptyTail, err := Tail(empty, 0)
	if err != nil || emptyTail != "" {
		t.Fatalf("empty Tail = %q, %v", emptyTail, err)
	}
	emptyRead, err := Read(empty, 0, 0)
	if err != nil || emptyRead.Text != "" || !emptyRead.EOF || emptyRead.Limit != DefaultReadLimit {
		t.Fatalf("empty Read = %#v, %v", emptyRead, err)
	}

	many := filepath.Join(dir, "many.txt")
	var content strings.Builder
	for i := 0; i < MaxTailLines+25; i++ {
		content.WriteString("line\n")
	}
	if err := os.WriteFile(many, []byte(content.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	cappedTail, err := Tail(many, MaxTailLines+100)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(cappedTail, "line\n"); got != MaxTailLines {
		t.Fatalf("capped Tail line count = %d", got)
	}

	large := filepath.Join(dir, "large.txt")
	if err := os.WriteFile(large, []byte(strings.Repeat("x", int(MaxReadLimit)+100)), 0o644); err != nil {
		t.Fatal(err)
	}
	cappedRead, err := Read(large, 0, MaxReadLimit+100)
	if err != nil {
		t.Fatal(err)
	}
	if cappedRead.Limit != MaxReadLimit || len(cappedRead.Text) != int(MaxReadLimit) || cappedRead.EOF {
		t.Fatalf("capped Read = %#v", cappedRead)
	}
}

func TestInMemoryHelpersMatchFileReaders(t *testing.T) {
	inputs := []string{"", "a", "a\nb", "a\r\nb\r\n", "\n\n", "one\ntwo\nthree\ntwo regex-42\n"}
	for i, in := range inputs {
		path := filepath.Join(t.TempDir(), "in.txt")
		if err := os.WriteFile(path, []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		for _, n := range []int{0, 1, 2} {
			want, err := Tail(path, n)
			if err != nil {
				t.Fatal(err)
			}
			if got := TailText([]byte(in), n); got != want {
				t.Fatalf("input %d: TailText(%d) = %q, file Tail = %q", i, n, got, want)
			}
		}
		lines, err := ReadLines(strings.NewReader(in), 0)
		if err != nil {
			t.Fatal(err)
		}
		got, err := GrepLines(path, lines, 1, "two", 1, 1, 10, false)
		if err != nil {
			t.Fatal(err)
		}
		want, err := Grep([]string{path}, "two", 1, 1, 10, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Matches) != len(want.Matches) || got.Truncated != want.Truncated {
			t.Fatalf("input %d: GrepLines = %#v, file Grep = %#v", i, got, want)
		}
		for k := range got.Matches {
			g, w := got.Matches[k], want.Matches[k]
			if g.Line != w.Line || g.Text != w.Text || strings.Join(g.Before, "|") != strings.Join(w.Before, "|") || strings.Join(g.After, "|") != strings.Join(w.After, "|") {
				t.Fatalf("input %d: match %d = %#v, want %#v", i, k, g, w)
			}
		}
	}
}

func TestGrepLinesFirstLineOffset(t *testing.T) {
	res, err := GrepLines("", []string{"partial", "hit", "tail"}, 41, "hit", 1, 0, 0, false)
	if err != nil || len(res.Matches) != 1 || res.Matches[0].Line != 42 || res.Matches[0].Before[0] != "partial" {
		t.Fatalf("GrepLines = %#v, %v", res, err)
	}
	if _, err := GrepLines("", nil, 1, "", 0, 0, 0, false); err == nil {
		t.Fatal("empty pattern returned nil error")
	}
	if _, err := GrepLines("", nil, 1, "(", 0, 0, 0, true); err == nil {
		t.Fatal("invalid regex returned nil error")
	}
}
