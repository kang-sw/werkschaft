package wsdashboard

import (
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
	if !companionSourceListed(raw, "https://example.invalid/a.git") || companionSourceListed(raw, "http://example.invalid/b.zip") ||
		companionSourceListed(raw, "official") || companionSourceListed(raw, "https://example.invalid/other.git") {
		t.Fatal("companionSourceListed must accept exactly the valid configured entries")
	}
}
