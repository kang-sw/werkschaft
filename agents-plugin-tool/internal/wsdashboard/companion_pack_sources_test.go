package wsdashboard

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestCompanionPackSourcesParse(t *testing.T) {
	long := "https://example.invalid/" + strings.Repeat("a", companionURLMax)
	raw := "  https://example.invalid/a.git\n\thttp://example.invalid/b.zip https://example.invalid/a.git " +
		"https://user:pw@example.invalid/c.git " + long + " official https://example.invalid/d.zip#x "
	got := parseCompanionSources(raw)
	want := []struct{ url, err string }{
		{"https://example.invalid/a.git", ""},
		{"http://example.invalid/b.zip", "absolute https URL"},
		{"https://example.invalid/a.git", "listed twice"},
		{"https://user:pw@example.invalid/c.git", "credentials"},
		{long, "exceeds"},
		{"official", "absolute https URL"},
		{"https://example.invalid/d.zip#x", "fragment"},
	}
	if len(got) != len(want) {
		t.Fatalf("parsed %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].URL != w.url || (w.err == "") != (got[i].Error == "") || !strings.Contains(got[i].Error, w.err) {
			t.Errorf("entry %d = %+v, want url %q error containing %q", i, got[i], w.url, w.err)
		}
	}
	if len(parseCompanionSources("")) != 0 || parseCompanionSources(" \n ") == nil {
		t.Fatal("an empty value must parse to an empty, non-nil list")
	}

	var many []string
	for i := 0; i < companionSourcesMax+2; i++ {
		many = append(many, fmt.Sprintf("https://example.invalid/%d.git", i))
	}
	// An invalid entry does not use up a slot.
	entries := parseCompanionSources("http://bad " + strings.Join(many, " "))
	for i, entry := range entries {
		if wantErr := i == 0 || i > companionSourcesMax; (entry.Error != "") != wantErr {
			t.Errorf("entry %d = %+v, want error %v", i, entry, wantErr)
		}
	}
	for _, tc := range []struct {
		source, problem string
		listed          bool
	}{
		{"https://example.invalid/a.git", "", true},
		{"http://example.invalid/b.zip", "absolute https URL", false},
		{"official", "absolute https URL", false},
		{"https://example.invalid/d.zip#x", "fragment", false},
		{"https://example.invalid/other.git", "", false},
	} {
		problem, listed := companionSourceProblem(raw, tc.source)
		if listed != tc.listed || (tc.problem == "") != (problem == "") || !strings.Contains(problem, tc.problem) {
			t.Errorf("companionSourceProblem(%q) = %q, %v; want problem containing %q, listed %v",
				tc.source, problem, listed, tc.problem, tc.listed)
		}
	}
}

// TestCompanionConfigSourceEntriesJSON pins the daemon's verdicts the modal
// renders: entries a prefix check would pass but the source routes refuse
// carry the routes' error, and only valid entries use up one of the slots.
func TestCompanionConfigSourceEntriesJSON(t *testing.T) {
	var many []string
	for i := 0; i < companionSourcesMax; i++ {
		many = append(many, fmt.Sprintf("https://example.invalid/%d.git", i))
	}
	refused := []string{
		"https://example.invalid/team/repo?tab=readme",
		"https://example.invalid/packs.zip#top",
		"https://example.invalid/repo.git#" + strings.Repeat("r", companionRefMax+1),
		"https://example.invalid/%zz.git",
		"https://example.invalid:port/repo.git",
	}
	config := CompanionConfig{Enabled: true, Sources: strings.Join(append(refused, many...), " ")}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Enabled       bool                   `json:"enabled"`
		Sources       string                 `json:"sources"`
		SourceEntries []companionSourceEntry `json:"sourceEntries"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Sources != config.Sources || len(got.SourceEntries) != len(refused)+len(many) {
		t.Fatalf("config JSON = %s", raw)
	}
	for i, entry := range got.SourceEntries {
		if wantErr := i < len(refused); (entry.Error != "") != wantErr {
			t.Errorf("entry %d %q error %q, want error %v", i, entry.URL, entry.Error, wantErr)
		}
	}
	// The empty list is an empty array, never null, so the modal can tell
	// "no entries" from "no verdicts".
	raw, _ = json.Marshal(CompanionConfig{})
	if !strings.Contains(string(raw), `"sourceEntries":[]`) {
		t.Fatalf("empty config JSON = %s", raw)
	}
}
