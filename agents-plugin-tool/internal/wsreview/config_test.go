package wsreview

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func reviewLocalConfigPath(root string) string {
	return filepath.Join(root, "ai-docs", "_review.local.md")
}

func mustWriteReviewLocalConfig(t *testing.T, root, content string) {
	t.Helper()
	path := reviewLocalConfigPath(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create ai-docs dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestStalenessKnobDefaultsWhenFileAbsent(t *testing.T) {
	root := t.TempDir()
	if got := StalenessKnob(root); got != DefaultStalenessCommits {
		t.Fatalf("StalenessKnob = %d, want default %d", got, DefaultStalenessCommits)
	}
}

func TestStalenessKnobDefaultsWhenSectionAbsent(t *testing.T) {
	root := t.TempDir()
	mustWriteReviewLocalConfig(t, root, "# Review: example\n\n## Deep Review\nthreshold: 20 files / 500 lines\n")
	if got := StalenessKnob(root); got != DefaultStalenessCommits {
		t.Fatalf("StalenessKnob = %d, want default %d", got, DefaultStalenessCommits)
	}
}

func TestStalenessKnobDefaultsWhenValueMalformed(t *testing.T) {
	root := t.TempDir()
	mustWriteReviewLocalConfig(t, root, "## Checkpoint Nudge\nstaleness: not-a-number commits\n")
	if got := StalenessKnob(root); got != DefaultStalenessCommits {
		t.Fatalf("StalenessKnob = %d, want default %d", got, DefaultStalenessCommits)
	}
}

func TestStalenessKnobParsesValidValue(t *testing.T) {
	root := t.TempDir()
	mustWriteReviewLocalConfig(t, root, "# Review: example\n\n## Checkpoint Nudge\nstaleness: 15 commits\n\n## Deep Review\nthreshold: 20 files / 500 lines\n")
	if got := StalenessKnob(root); got != 15 {
		t.Fatalf("StalenessKnob = %d, want 15", got)
	}
}

func TestReadCheckpointConfigDefaultsWhenAbsentOrEmpty(t *testing.T) {
	root := t.TempDir()
	got := readCheckpointConfig(root)
	if got.legacyOnly || got.merges != defaultMergesThreshold || got.lines != defaultLinesThreshold || len(got.excludes) != 0 {
		t.Fatalf("absent file config = %+v, want defaults (%d merges, %d lines)", got, defaultMergesThreshold, defaultLinesThreshold)
	}

	mustWriteReviewLocalConfig(t, root, "# Review: example\n\n## Deep Review\nmerges: 99\nlines: 99999\nexclude: dist/**\n")
	got = readCheckpointConfig(root)
	if got.legacyOnly || got.merges != defaultMergesThreshold || got.lines != defaultLinesThreshold || len(got.excludes) != 0 {
		t.Fatalf("missing section config = %+v, want defaults without leaking other sections", got)
	}
}

func TestParseCheckpointConfigOverridesAndPartialDefaults(t *testing.T) {
	cases := []struct {
		name       string
		content    string
		wantLegacy bool
		wantStale  int
		wantMerges int
		wantLines  int
		wantExcl   []string
	}{
		{
			name: "both metric thresholds and repeated excludes",
			content: "# Review: example\r\n\r\n## Checkpoint Nudge\r\n" +
				"merges: 25\r\n" +
				"lines: 20000\r\n" +
				"exclude: dist/**\r\n" +
				"exclude: pkg*/generated/**\r\n" +
				"exclude: dist/**\r\n" +
				"\r\n## Deep Review\r\nmerges: 1\r\n",
			wantMerges: 25,
			wantLines:  20000,
			wantExcl:   []string{"dist/**", "pkg*/generated/**"},
		},
		{
			name:       "merges override only keeps shipped lines default",
			content:    "## Checkpoint Nudge\nmerges: 4\n",
			wantMerges: 4,
			wantLines:  defaultLinesThreshold,
		},
		{
			name:       "lines override only keeps shipped merges default",
			content:    "## Checkpoint Nudge\nlines: 1200\n",
			wantMerges: defaultMergesThreshold,
			wantLines:  1200,
		},
		{
			name:       "legacy only selects legacy mode",
			content:    "## Checkpoint Nudge\nstaleness: 15 commits\nexclude: dist/**\n",
			wantLegacy: true,
			wantStale:  15,
			wantMerges: defaultMergesThreshold,
			wantLines:  defaultLinesThreshold,
			wantExcl:   []string{"dist/**"},
		},
		{
			name:       "invalid new key does not disable legacy mode",
			content:    "## Checkpoint Nudge\nstaleness: 12 commits\nmerges: 0\nlines: -5\n",
			wantLegacy: true,
			wantStale:  12,
			wantMerges: defaultMergesThreshold,
			wantLines:  defaultLinesThreshold,
		},
		{
			name:       "valid merges takes precedence over legacy staleness",
			content:    "## Checkpoint Nudge\nstaleness: 12 commits\nmerges: 25\n",
			wantLegacy: false,
			wantMerges: 25,
			wantLines:  defaultLinesThreshold,
		},
		{
			name:       "valid lines takes precedence over legacy staleness",
			content:    "## Checkpoint Nudge\nstaleness: 12 commits\nlines: 8000\n",
			wantLegacy: false,
			wantMerges: defaultMergesThreshold,
			wantLines:  8000,
		},
		{
			name:       "malformed staleness with no new keys falls back to shipped metric defaults",
			content:    "## Checkpoint Nudge\nstaleness: 0 commits\n",
			wantLegacy: false,
			wantMerges: defaultMergesThreshold,
			wantLines:  defaultLinesThreshold,
		},
		{
			name: "invalid pathspecs are ignored while valid ones stay",
			content: "## Checkpoint Nudge\n" +
				"exclude: :!secret\n" +
				"exclude: :(exclude)gen/**\n" +
				"exclude: :/root\n" +
				"exclude: /abs/path\n" +
				"exclude: ../outside\n" +
				"exclude: sub/../escape\n" +
				"exclude: C:/windows/path\n" +
				"exclude: win\\slash\n" +
				"exclude:    \n" +
				"exclude: generated/**\n" +
				"exclude: *.pb.go\n",
			wantMerges: defaultMergesThreshold,
			wantLines:  defaultLinesThreshold,
			wantExcl:   []string{"generated/**", "*.pb.go"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCheckpointConfig(tc.content)
			if got.legacyOnly != tc.wantLegacy {
				t.Fatalf("legacyOnly = %v, want %v (cfg=%+v)", got.legacyOnly, tc.wantLegacy, got)
			}
			if got.legacyCommits != tc.wantStale {
				t.Fatalf("legacyCommits = %d, want %d", got.legacyCommits, tc.wantStale)
			}
			if got.merges != tc.wantMerges {
				t.Fatalf("merges = %d, want %d", got.merges, tc.wantMerges)
			}
			if got.lines != tc.wantLines {
				t.Fatalf("lines = %d, want %d", got.lines, tc.wantLines)
			}
			if !slices.Equal(got.excludes, tc.wantExcl) {
				t.Fatalf("excludes = %v, want %v", got.excludes, tc.wantExcl)
			}
		})
	}
}
