package wsfs

import (
	"strings"
	"testing"
)

func TestUnifiedDiffHunks(t *testing.T) {
	var a, b []string
	for i := 1; i <= 30; i++ {
		a = append(a, "l"+string(rune('A'+i%26))+string(rune('a'+i%7)))
	}
	b = append(b, a...)
	b[2] = "CHANGED-3"
	b[25] = "CHANGED-26"
	got := unifiedDiff("f", strings.Join(a, "\n")+"\n", strings.Join(b, "\n")+"\n")
	if strings.Count(got, "@@ -") != 2 || !strings.Contains(got, "+CHANGED-3") || !strings.Contains(got, "+CHANGED-26") {
		t.Errorf("diff = %s", got)
	}
	if unifiedDiff("f", "same\n", "same\n") != "" {
		t.Error("equal inputs gave a diff")
	}
	if got := unifiedDiff("f", "", "new\n"); !strings.Contains(got, "@@ -0,0 +1,1 @@") || !strings.Contains(got, "+new") {
		t.Errorf("creation diff = %q", got)
	}
}
