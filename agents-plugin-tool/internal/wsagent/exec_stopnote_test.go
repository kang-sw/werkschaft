package wsagent

import (
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// TestStopNoteFollowsWorkflowLang: the user-stop sentence is keyed by the
// normalized workflow.lang, English for an empty or unrecognized value.
func TestStopNoteFollowsWorkflowLang(t *testing.T) {
	en, ko := userStopNotes["en"], userStopNotes["ko"]
	if en == "" || ko == "" || en == ko {
		t.Fatalf("tables: en %q ko %q", en, ko)
	}
	for lang, want := range map[string]string{
		"": en, "English": en, "ja": en, "Japanese": en, "kor": en,
		"ko": ko, "KO": ko, " ko-KR ": ko, "ko_kr": ko, "Korean": ko, "한국어": ko, "한글 (Korean)": ko,
	} {
		if got := UserStopNote(lang); got != want {
			t.Fatalf("UserStopNote(%q) = %q, want %q", lang, got, want)
		}
	}
	for _, note := range []string{en, ko} {
		if !strings.Contains(note, "relaunch") && !strings.Contains(note, "다시 실행") {
			t.Fatalf("a note must say the stop is deliberate and not to relaunch: %q", note)
		}
	}
}

// TestExecStopNoteUsesTheConversationsResolvedLanguage: the runner answers
// from the conversation's cached ambient resolution, with no config read of
// its own; an unknown conversation gets English.
func TestExecStopNoteUsesTheConversationsResolvedLanguage(t *testing.T) {
	store := &ambientKeyStore{value: "Korean"}
	r := &Runner{opts: Options{Keys: store, Config: wsconfig.Options{}}, convs: map[string]*conv{}}
	c := &conv{r: r, rec: Record{Lead: true, WSKey: "lead-key", Root: t.TempDir(), ReplyID: "lead"}}
	if c.ambientLang.Load() != nil {
		t.Fatal("no language before the first resolution")
	}
	c.refreshAmbient()
	r.convs["lead"] = c
	reads := store.reads.Load()
	if got := r.ExecStopNote("lead"); got != userStopNotes["ko"] {
		t.Fatalf("note = %q", got)
	}
	store.value = "Japanese"
	c.refreshAmbient()
	if got := r.ExecStopNote("lead"); got != userStopNotes["en"] {
		t.Fatalf("note after the language changed = %q", got)
	}
	if got := store.reads.Load(); got != reads+1 {
		t.Fatalf("config reads = %d, want %d (only the refresh reads)", got, reads+1)
	}
	if got := r.ExecStopNote("nobody"); got != userStopNotes["en"] {
		t.Fatalf("unknown conversation note = %q", got)
	}
}
