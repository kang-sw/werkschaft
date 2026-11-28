package wsdashboard

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompanionSoulRAMCacheAndSingleLineShards(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()
	nowTime := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	store := NewCompanionSoulStore(stateDir, homeDir, func() time.Time { return nowTime })

	// 1. Empty soul ID resolves to "default"; a cold load is read-only and
	// never seeds persona.md (the persona is inherited live instead).
	snap1, err := store.Load("", "", 30)
	if err != nil {
		t.Fatalf("cold load: %v", err)
	}
	if snap1.SoulID != "default" {
		t.Fatalf("soulID = %q, want default", snap1.SoulID)
	}
	if snap1.Persona != "" || !snap1.PersonaInherited || snap1.PersonaSource != "" {
		t.Fatalf("cold load persona = %q inherited=%v source=%q, want empty inherited", snap1.Persona, snap1.PersonaInherited, snap1.PersonaSource)
	}
	if _, err := os.Stat(filepath.Join(homeDir, ".ws", "companion-soul")); !os.IsNotExist(err) {
		t.Fatalf("cold load wrote the soul root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, companionMachineIDFile)); !os.IsNotExist(err) {
		t.Fatalf("cold load wrote the machine id: %v", err)
	}
	if got := store.DiskReadCount(); got != 1 {
		t.Fatalf("disk reads after cold load = %d, want 1", got)
	}

	// 2. Warm reads hit RAM hot cache with ZERO disk reads.
	for i := 0; i < 5; i++ {
		snap, err := store.Load("", "", 30)
		if err != nil {
			t.Fatalf("warm load %d: %v", i, err)
		}
		if snap.Persona != snap1.Persona {
			t.Fatalf("warm load persona mismatch")
		}
	}
	if got := store.DiskReadCount(); got != 1 {
		t.Fatalf("warm reads triggered disk I/O: diskReads = %d, want 1", got)
	}

	// 3. MachineID is persisted once in stateDir and sanitized to [a-z0-9-].
	machineID, err := store.MachineID()
	if err != nil {
		t.Fatalf("MachineID: %v", err)
	}
	if !companionMachineIDPattern.MatchString(machineID) {
		t.Fatalf("machineID %q does not match [a-z0-9-]", machineID)
	}
	rawID, err := os.ReadFile(filepath.Join(stateDir, companionMachineIDFile))
	if err != nil {
		t.Fatalf("machine-id file not persisted: %v", err)
	}
	if strings.TrimSpace(string(rawID)) != machineID {
		t.Fatalf("persisted machineID = %q, want %q", strings.TrimSpace(string(rawID)), machineID)
	}

	// 4. AppendShardEntry appends ONE JSONL line per delta without rescanning.
	e1, err := store.AppendShardEntry("", "", "rapport", "Prefers dark mode late at night.", []string{"ui", "night"})
	if err != nil {
		t.Fatalf("append 1: %v", err)
	}
	nowTime = nowTime.Add(5 * time.Minute)
	e2, err := store.AppendShardEntry("", "", "preference", "Likes concise commit summaries.", []string{"style"})
	if err != nil {
		t.Fatalf("append 2: %v", err)
	}
	if got := store.DiskReadCount(); got != 1 {
		t.Fatalf("shard appends triggered rescan: diskReads = %d, want 1", got)
	}

	shardPath := filepath.Join(homeDir, ".ws", "companion-soul", "default", "log", "2026-11-"+machineID+".jsonl")
	rawShard, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatalf("read shard file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(rawShard)), "\n")
	if len(lines) != 2 {
		t.Fatalf("shard line count = %d, want 2", len(lines))
	}
	for i, ln := range lines {
		var parsed SoulShardEntry
		if err := json.Unmarshal([]byte(ln), &parsed); err != nil {
			t.Fatalf("line %d invalid JSON: %v", i, err)
		}
	}

	warmAfterAppend, err := store.Load("", "", 30)
	if err != nil {
		t.Fatalf("warm load after append: %v", err)
	}
	if store.DiskReadCount() != 1 {
		t.Fatalf("diskReads = %d, want 1", store.DiskReadCount())
	}
	if warmAfterAppend.TotalEntries != 2 || len(warmAfterAppend.RecentEntries) != 2 {
		t.Fatalf("entries = %d / %d, want 2", warmAfterAppend.TotalEntries, len(warmAfterAppend.RecentEntries))
	}
	// Newest-first ordering in RecentEntries.
	if warmAfterAppend.RecentEntries[0].ID != e2.ID || warmAfterAppend.RecentEntries[1].ID != e1.ID {
		t.Fatalf("unexpected recent ordering: %+v", warmAfterAppend.RecentEntries)
	}

	// 5. Multi-machine shard merge without conflicts.
	otherShardPath := filepath.Join(homeDir, ".ws", "companion-soul", "default", "log", "2026-11-m-workbox99.jsonl")
	otherLine1, _ := json.Marshal(SoulShardEntry{
		ID:        "se-other-1",
		Timestamp: "2026-11-28T12:02:00Z",
		MachineID: "m-workbox99",
		Kind:      "rapport",
		Content:   "Drinks espresso before standup.",
		Tags:      []string{"coffee"},
	})
	// Duplicate ID from machine 1 to prove deduplication.
	dupLine, _ := json.Marshal(e1)
	if err := os.WriteFile(otherShardPath, append(append(otherLine1, '\n'), append(dupLine, '\n')...), 0o600); err != nil {
		t.Fatalf("write other machine shard: %v", err)
	}

	// Reload in a fresh store (simulating restart or git pull on another
	// machine): shard appends never created persona.md.
	if _, err := os.Stat(filepath.Join(homeDir, ".ws", "companion-soul", "default", "persona.md")); !os.IsNotExist(err) {
		t.Fatalf("persona.md was auto-seeded: %v", err)
	}
	store2 := NewCompanionSoulStore(stateDir, homeDir, func() time.Time { return nowTime })
	reloaded, err := store2.Load("", "", 30)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Persona != "" || !reloaded.PersonaInherited {
		t.Fatalf("reloaded persona = %q inherited=%v, want inherited", reloaded.Persona, reloaded.PersonaInherited)
	}
	if reloaded.ShardCount != 2 {
		t.Fatalf("ShardCount = %d, want 2", reloaded.ShardCount)
	}
	if reloaded.TotalEntries != 3 {
		t.Fatalf("TotalEntries = %d, want 3 (deduplicated merge across 2 machines)", reloaded.TotalEntries)
	}
	// Verify chronological merge order (newest first in RecentEntries):
	// e2 (12:05), se-other-1 (12:02), e1 (12:00)
	gotIDs := []string{
		reloaded.RecentEntries[0].ID,
		reloaded.RecentEntries[1].ID,
		reloaded.RecentEntries[2].ID,
	}
	wantIDs := []string{e2.ID, "se-other-1", e1.ID}
	for i := range wantIDs {
		if gotIDs[i] != wantIDs[i] {
			t.Fatalf("merged order = %v, want %v", gotIDs, wantIDs)
		}
	}
}

func TestCompanionScrubberGuardrailRules(t *testing.T) {
	activeProjects := []string{"/Users/dev/src/devenv", "my-webapp"}

	secretCases := []struct {
		name string
		text string
	}{
		{"openai_key", "User key is sk-proj-abcdefghijklmnopqrstuvwxyz123456"},
		{"github_pat", "Token ghp_1234567890abcdefghij1234567890abcd"},
		{"aws_key", "AWS AKIAIOSFODNN7EXAMPLE"},
		{"pem_key", "-----BEGIN RSA PRIVATE KEY-----\nMIIE..."},
		{"jwt", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"},
		{"secret_assign", "api_key=supersecretvalue123456"},
		{"bearer", "Authorization: Bearer abcdefghijklmnopqrstuvwxyz1234"},
	}
	for _, tc := range secretCases {
		t.Run("secret_"+tc.name, func(t *testing.T) {
			res := ScrubSoulContent(tc.text, activeProjects)
			if res.Action != ScrubRejectSecret {
				t.Fatalf("action = %q, want %q (findings=%+v)", res.Action, ScrubRejectSecret, res.Findings)
			}
			for _, f := range res.Findings {
				if f.Category == ScrubCategorySecret && !strings.Contains(f.Sample, "[redacted]") {
					t.Fatalf("secret finding leaked raw value: %q", f.Sample)
				}
			}
		})
	}

	holdCases := []struct {
		name string
		text string
		cat  ScrubCategory
	}{
		{"unix_path", "Keeps scratch notes in /Users/alice/notes/todo.md", ScrubCategoryPath},
		{"home_path", "Uses ~/secrets/work.txt for checklists", ScrubCategoryPath},
		{"win_path", `Stores logs under C:\Users\alice\logs\app.log`, ScrubCategoryPath},
		{"email", "Reach me at alice@corp.example for reviews", ScrubCategoryEmail},
		{"https_url", "Checks https://internal.corp.example/dashboard every morning", ScrubCategoryURL},
		{"git_scp", "Clones from git@github.com:org/private-repo.git", ScrubCategoryURL},
	}
	for _, tc := range holdCases {
		t.Run("hold_"+tc.name, func(t *testing.T) {
			res := ScrubSoulContent(tc.text, activeProjects)
			if res.Action != ScrubHoldPending {
				t.Fatalf("action = %q, want %q (findings=%+v)", res.Action, ScrubHoldPending, res.Findings)
			}
			found := false
			for _, f := range res.Findings {
				if f.Category == tc.cat {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected finding category %q, got %+v", tc.cat, res.Findings)
			}
		})
	}

	downgradeCases := []struct {
		name    string
		text    string
		wantCat ScrubCategory
		wantPrj string
	}{
		{"ticket_stem", "Finished phase 1 of 261128-feat-ws-companion-soul-memory-and-pacing", ScrubCategoryTicketStem, ""},
		{"project_name", "Working late on devenv release checks", ScrubCategoryProjectName, "devenv"},
		{"project_case_insensitive", "Debugging MY-WEBAPP layout regressions", ScrubCategoryProjectName, "my-webapp"},
	}
	for _, tc := range downgradeCases {
		t.Run("downgrade_"+tc.name, func(t *testing.T) {
			res := ScrubSoulContent(tc.text, activeProjects)
			if res.Action != ScrubDowngradeEpisode {
				t.Fatalf("action = %q, want %q (findings=%+v)", res.Action, ScrubDowngradeEpisode, res.Findings)
			}
			if tc.wantPrj != "" && !strings.EqualFold(res.MatchedProject, tc.wantPrj) {
				t.Fatalf("matchedProject = %q, want %q", res.MatchedProject, tc.wantPrj)
			}
		})
	}

	// Clean portable personal/rapport memories (including substring non-match).
	cleanCases := []string{
		"Prefers brief encouragement after long debugging stretches.",
		"Enjoys lo-fi jazz and tea while planning architecture.",
		"Works on mydevenvtool experiments occasionally.", // must not word-boundary match "devenv"
	}
	for _, text := range cleanCases {
		res := ScrubSoulContent(text, activeProjects)
		if res.Action != ScrubAllow {
			t.Fatalf("clean text %q got action %q (%+v)", text, res.Action, res.Findings)
		}
	}
}

func TestCompanionMemoryGuardrailFlowsAndRestartPersistence(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	homeDir := t.TempDir()
	nowTime := time.Date(2026, 11, 28, 12, 30, 0, 0, time.UTC)

	mem := NewCompanionMemory(stateDir, homeDir, func() time.Time { return nowTime }, nil)
	mem.epIdleWait = -1 // disable timer during unit test
	defer mem.Close()

	activeProjects := []string{"devenv", "billing-api"}

	// 1. Secret rejected outright — NEVER written to Soul shards, soul_pending, or episodes.
	secRes, err := mem.RememberSoul(ctx, RememberSoulInput{
		Content:        "Remember my token ghp_1234567890abcdefghij1234567890abcd",
		ActiveProjects: activeProjects,
	})
	if err != nil {
		t.Fatalf("RememberSoul secret: %v", err)
	}
	if secRes.Status != "rejected_secret" {
		t.Fatalf("secret status = %q, want rejected_secret", secRes.Status)
	}

	// 2. Path/email/URL held in soul_pending.
	holdRes, err := mem.RememberSoul(ctx, RememberSoulInput{
		Kind:           "rapport",
		Content:        "Send summaries to alice@corp.example after work",
		ActiveProjects: activeProjects,
	})
	if err != nil {
		t.Fatalf("RememberSoul hold: %v", err)
	}
	if holdRes.Status != "held_pending" || holdRes.Pending == nil || holdRes.Pending.ID <= 0 {
		t.Fatalf("hold result = %+v", holdRes)
	}

	// 3. Project/ticket downgraded to Local Episodes.
	downRes, err := mem.RememberSoul(ctx, RememberSoulInput{
		Content:        "Completed 261128-feat-ws-companion-soul-memory-and-pacing on devenv",
		ActiveProjects: activeProjects,
	})
	if err != nil {
		t.Fatalf("RememberSoul downgrade: %v", err)
	}
	if downRes.Status != "downgraded_episode" || downRes.Episode == nil || downRes.Episode.ProjectKey != "devenv" {
		t.Fatalf("downgrade result = %+v", downRes)
	}

	// 4. Clean memory saved directly to Soul shard.
	cleanRes, err := mem.RememberSoul(ctx, RememberSoulInput{
		Kind:           "preference",
		Content:        "Prefers concise checklists before shipping.",
		ActiveProjects: activeProjects,
	})
	if err != nil {
		t.Fatalf("RememberSoul clean: %v", err)
	}
	if cleanRes.Status != "saved" || cleanRes.Entry == nil {
		t.Fatalf("clean result = %+v", cleanRes)
	}

	// 5. Settings drawer edit of profile.md containing suspicious content (even a token)
	// is HELD in approval queue rather than rejected outright, and does not touch disk.
	docHold, err := mem.SaveUserDoc(ctx, "", "default", SoulPendingTargetProfile,
		"Contact me at bob@example.org or use /Users/bob/work", activeProjects)
	if err != nil {
		t.Fatalf("SaveUserDoc hold: %v", err)
	}
	if docHold.Status != "held_pending" || docHold.Pending == nil {
		t.Fatalf("SaveUserDoc hold = %+v", docHold)
	}
	snapBeforeApprove, err := mem.Soul().Load("", "default", 30)
	if err != nil {
		t.Fatalf("load before approve: %v", err)
	}
	if snapBeforeApprove.Profile != "" {
		t.Fatalf("held profile.md wrote prematurely: %q", snapBeforeApprove.Profile)
	}
	if snapBeforeApprove.TotalEntries != 1 {
		t.Fatalf("Soul shard count = %d, want 1 (only clean entry)", snapBeforeApprove.TotalEntries)
	}

	// 6. Close and reopen SQLite store: soul_pending MUST survive restart.
	if err := mem.Close(); err != nil {
		t.Fatalf("close mem: %v", err)
	}
	mem2 := NewCompanionMemory(stateDir, homeDir, func() time.Time { return nowTime }, nil)
	mem2.epIdleWait = -1
	defer mem2.Close()

	ep2, err := mem2.Episodes()
	if err != nil {
		t.Fatalf("reopen episodes: %v", err)
	}
	pendingItems, err := ep2.ListPending(ctx, "default")
	if err != nil {
		t.Fatalf("list pending after restart: %v", err)
	}
	if len(pendingItems) != 2 {
		t.Fatalf("pending count after restart = %d, want 2 (secret must never be queued)", len(pendingItems))
	}

	// Approve the held profile.md with redacted/edited content.
	editedProfile := "Prefers async written updates."
	if _, err := mem2.ApprovePending(ctx, "", docHold.Pending.ID, &editedProfile); err != nil {
		t.Fatalf("ApprovePending profile: %v", err)
	}
	snapAfterApprove, err := mem2.Soul().Load("", "default", 30)
	if err != nil {
		t.Fatalf("load after approve: %v", err)
	}
	if snapAfterApprove.Profile != editedProfile {
		t.Fatalf("profile.md = %q, want %q", snapAfterApprove.Profile, editedProfile)
	}
	// Approve the held shard item with edited text.
	editedShard := "Likes end-of-day wrap-up summaries."
	if _, err := mem2.ApprovePending(ctx, "", holdRes.Pending.ID, &editedShard); err != nil {
		t.Fatalf("ApprovePending shard: %v", err)
	}
	snapAfterShard, err := mem2.Soul().Load("", "default", 30)
	if err != nil {
		t.Fatalf("load after shard approve: %v", err)
	}
	if snapAfterShard.TotalEntries != 2 {
		t.Fatalf("TotalEntries = %d, want 2", snapAfterShard.TotalEntries)
	}
	remaining, err := ep2.ListPending(ctx, "default")
	if err != nil {
		t.Fatalf("list remaining pending: %v", err)
	}
	if len(remaining) != 0 {
		t.Fatalf("remaining pending = %d, want 0", len(remaining))
	}
}

func TestCompanionEpisodesWALAndFTS5SearchAndPrune(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	nowTime := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	store, err := OpenCompanionEpisodes(stateDir, CompanionEpisodesOptions{
		Now:            func() time.Time { return nowTime },
		CheckpointIdle: -1,
	})
	if err != nil {
		t.Fatalf("OpenCompanionEpisodes: %v", err)
	}
	defer store.Close()

	// Verify WAL journal mode.
	var mode string
	if err := store.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}

	ep1, err := store.AddEpisode(ctx, CompanionEpisodeInput{
		ProjectKey: "alpha",
		Summary:    "Refactored SQLite FTS5 indexing for companion episodes",
		Tags:       []string{"sqlite", "fts5"},
		CreatedAt:  nowTime.AddDate(0, 0, -40).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("add ep1: %v", err)
	}
	_, err = store.AddEpisode(ctx, CompanionEpisodeInput{
		ProjectKey: "beta",
		Summary:    "Investigated SQLite WAL checkpoint contention during burst traffic",
		Tags:       []string{"sqlite", "wal"},
		CreatedAt:  nowTime.AddDate(0, 0, -2).Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("add ep2: %v", err)
	}
	ep3, err := store.AddEpisode(ctx, CompanionEpisodeInput{
		ProjectKey: "alpha",
		Summary:    "Tuned VRM idle blink cadence",
		Tags:       []string{"vrm", "animation"},
		CreatedAt:  nowTime.Format(time.RFC3339),
	})
	if err != nil {
		t.Fatalf("add ep3: %v", err)
	}

	// Project-scoped FTS5 search.
	alphaSQLite, err := store.SearchEpisodes(ctx, CompanionEpisodeQuery{
		ProjectKey: "alpha",
		Query:      "SQLite FTS5",
	})
	if err != nil {
		t.Fatalf("search alpha sqlite: %v", err)
	}
	if len(alphaSQLite) != 1 || alphaSQLite[0].ID != ep1.ID {
		t.Fatalf("alpha sqlite matches = %+v, want only ep1", alphaSQLite)
	}

	// Cross-project search with punctuation that would break raw FTS5 syntax.
	cross, err := store.SearchEpisodes(ctx, CompanionEpisodeQuery{
		Query: `"SQLite" (wal:checkpoint-contention)`,
	})
	if err != nil {
		t.Fatalf("search with punctuation: %v", err)
	}
	if len(cross) != 1 || cross[0].ProjectKey != "beta" {
		t.Fatalf("punctuated search = %+v, want beta match", cross)
	}

	// Prune older than 30 days removes ep1 (and its FTS5 row).
	deleted, err := store.PruneEpisodes(ctx, CompanionEpisodePruneOptions{OlderThanDays: 30})
	if err != nil {
		t.Fatalf("prune older: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	afterPrune, err := store.SearchEpisodes(ctx, CompanionEpisodeQuery{ProjectKey: "alpha", Query: "SQLite"})
	if err != nil {
		t.Fatalf("search after prune: %v", err)
	}
	if len(afterPrune) != 0 {
		t.Fatalf("pruned row still matched FTS5: %+v", afterPrune)
	}

	// Delete single episode by ID.
	deleted, err = store.PruneEpisodes(ctx, CompanionEpisodePruneOptions{ID: ep3.ID})
	if err != nil || deleted != 1 {
		t.Fatalf("prune by id: deleted=%d err=%v", deleted, err)
	}

	// Delete remaining all.
	deleted, err = store.PruneEpisodes(ctx, CompanionEpisodePruneOptions{All: true})
	if err != nil || deleted != 1 {
		t.Fatalf("prune all: deleted=%d err=%v", deleted, err)
	}
	count, err := store.CountEpisodes(ctx)
	if err != nil || count != 0 {
		t.Fatalf("remaining count = %d (err=%v), want 0", count, err)
	}
}

func TestCompanionSoulExportImportHardening(t *testing.T) {
	stateDir := t.TempDir()
	homeDir := t.TempDir()
	nowTime := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	store := NewCompanionSoulStore(stateDir, homeDir, func() time.Time { return nowTime })

	if err := store.WriteUserDoc("", "work", SoulPendingTargetPersona, "# Work Companion\n\nFocused.\n"); err != nil {
		t.Fatalf("write persona: %v", err)
	}
	if err := store.WriteUserDoc("", "work", SoulPendingTargetProfile, "Prefers structured diffs.\n"); err != nil {
		t.Fatalf("write profile: %v", err)
	}
	if _, err := store.AppendShardEntry("", "work", "rapport", "Celebrates clean green builds.", []string{"builds"}); err != nil {
		t.Fatalf("append shard: %v", err)
	}
	// Plant an unrelated sqlite file inside the soul directory to verify export strictly excludes it.
	soulDir := filepath.Join(homeDir, ".ws", "companion-soul", "work")
	if err := os.WriteFile(filepath.Join(soulDir, companionEpisodesDBName), []byte("sqlite-bytes"), 0o600); err != nil {
		t.Fatalf("plant sqlite: %v", err)
	}

	zipData, filename, err := store.ExportZIP("", "work")
	if err != nil {
		t.Fatalf("ExportZIP: %v", err)
	}
	if filename != "companion-soul-work.zip" {
		t.Fatalf("filename = %q", filename)
	}

	zr, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatalf("open exported zip: %v", err)
	}
	files := map[string]bool{}
	for _, f := range zr.File {
		files[f.Name] = true
		if strings.Contains(f.Name, ".sqlite") {
			t.Fatalf("exported ZIP leaked SQLite file: %q", f.Name)
		}
	}
	if !files["manifest.json"] || !files["persona.md"] || !files["profile.md"] {
		t.Fatalf("exported ZIP missing core files: %v", files)
	}

	// Import into a brand-new Soul ("imported") on another home directory.
	homeDir2 := t.TempDir()
	store2 := NewCompanionSoulStore(t.TempDir(), homeDir2, func() time.Time { return nowTime })
	res, err := store2.ImportZIP("", "imported", zipData, false)
	if err != nil {
		t.Fatalf("ImportZIP: %v", err)
	}
	if res.SoulID != "imported" || !res.UpdatedPersona || !res.UpdatedProfile || res.MergedEntries != 1 {
		t.Fatalf("unexpected import result: %+v", res)
	}

	// Re-importing the exact same archive is idempotent (0 duplicate entries, no overwrite).
	res2, err := store2.ImportZIP("", "imported", zipData, false)
	if err != nil {
		t.Fatalf("re-import: %v", err)
	}
	if res2.UpdatedPersona || res2.UpdatedProfile || res2.MergedEntries != 0 {
		t.Fatalf("re-import was not idempotent: %+v", res2)
	}

	// Reject malicious archives: Zip Slip, unexpected .sqlite file, symlink.
	badArchives := []struct {
		name  string
		build func(*zip.Writer)
	}{
		{
			name: "zip_slip",
			build: func(zw *zip.Writer) {
				w, _ := zw.Create("../escape.md")
				_, _ = w.Write([]byte("evil"))
			},
		},
		{
			name: "episodes_sqlite",
			build: func(zw *zip.Writer) {
				w, _ := zw.Create("companion-episodes.sqlite")
				_, _ = w.Write([]byte("SQLite format 3"))
			},
		},
		{
			name: "symlink",
			build: func(zw *zip.Writer) {
				h := &zip.FileHeader{Name: "persona.md", Method: zip.Deflate}
				h.SetMode(fs.ModeSymlink | 0o777)
				w, _ := zw.CreateHeader(h)
				_, _ = w.Write([]byte("/etc/passwd"))
			},
		},
	}
	for _, tc := range badArchives {
		t.Run("reject_"+tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			zw := zip.NewWriter(&buf)
			tc.build(zw)
			_ = zw.Close()
			if _, err := store2.ImportZIP("", "imported", buf.Bytes(), false); err == nil {
				t.Fatalf("expected error importing %s archive", tc.name)
			}
		})
	}
}

func TestCompanionMemoryHTTPRoutesAndGuards(t *testing.T) {
	d := authFixture(t)
	d.home = t.TempDir()
	d.lan = true
	t.Cleanup(d.closeCompanionMemory)
	cookie := authCookie(t, d)

	postJSON := func(path, body, origin string) *httptest.ResponseRecorder {
		r := authRequest(http.MethodPost, path, body)
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return authServe(d, r)
	}

	// 1. Guards: unauthenticated, cross-origin, and LAN viewer are refused.
	if w := companionTestRequest(d, nil, http.MethodGet, companionSoulRoute, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauth GET soul = %d, want 401", w.Code)
	}
	if w := postJSON(companionSoulRoute, `{"target":"profile.md","content":"hello"}`, "http://evil.test"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST soul = %d, want 403", w.Code)
	}
	if w := companionTestRequest(d, cookie, http.MethodPost, companionSoulExportRoute, "http://evil.test"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin POST export = %d, want 403", w.Code)
	}
	token, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	lanReq := func(method, path string) *httptest.ResponseRecorder {
		r := authRequest(method, path, "")
		r.Host, r.RemoteAddr = "192.0.2.1:43210", "192.0.2.2:1234"
		r.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
		r.Header.Set("Origin", "http://"+r.Host)
		return authServe(d, r)
	}
	if w := lanReq(http.MethodGet, companionSoulRoute); w.Code != http.StatusNotFound {
		t.Fatalf("LAN GET soul = %d, want 404", w.Code)
	}
	if w := lanReq(http.MethodPost, companionSoulExportRoute); w.Code != http.StatusNotFound {
		t.Fatalf("LAN POST export = %d, want 404", w.Code)
	}
	if w := lanReq(http.MethodGet, companionMemoryPendingRoute); w.Code != http.StatusNotFound {
		t.Fatalf("LAN GET pending = %d, want 404", w.Code)
	}
	if w := lanReq(http.MethodGet, companionMemoryEpisodesRoute); w.Code != http.StatusNotFound {
		t.Fatalf("LAN GET episodes = %d, want 404", w.Code)
	}

	// 2. Local viewer GET /api/companion/soul shows the inherited official
	// persona read-only: it writes nothing to disk.
	getSoul := func() companionSoulResponse {
		t.Helper()
		w := companionTestRequest(d, cookie, http.MethodGet, companionSoulRoute, "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET soul = %d: %s", w.Code, w.Body.String())
		}
		var resp companionSoulResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode soul response: %v", err)
		}
		return resp
	}
	soulResp := getSoul()
	if soulResp.SoulID != "default" || !strings.HasPrefix(soulResp.Persona, "# Companion\n") ||
		soulResp.PersonaSource != CompanionPersonaSourceOfficial || !soulResp.PersonaInherited ||
		soulResp.ActivePresetID != CompanionPersonaPresetDefault || len(soulResp.Presets) < 2 ||
		soulResp.Presets[0].ID != CompanionPersonaPresetDefault || soulResp.Presets[0].TitleByLang["ko"] == "" {
		t.Fatalf("unexpected initial soul: %+v", soulResp)
	}
	pickedPreset := soulResp.Presets[1]
	soulRoot := filepath.Join(d.home, ".ws", "companion-soul")
	personaPath := filepath.Join(soulRoot, "default", "persona.md")
	if _, err := os.Stat(soulRoot); !os.IsNotExist(err) {
		t.Fatalf("GET soul wrote the soul root: %v", err)
	}

	// 2b. Saving persona.md makes it the user's; reset removes it and the
	// Soul inherits the official persona again. Only persona.md resets.
	w := postJSON(companionSoulRoute, `{"target":"persona.md","content":"# Mine\n\n## Voice\nDry and brief.\n"}`, companionTestOrigin)
	if w.Code != http.StatusOK {
		t.Fatalf("persona save = %d: %s", w.Code, w.Body.String())
	}
	if got := getSoul(); got.PersonaSource != CompanionPersonaSourceUser || got.PersonaInherited || !strings.HasPrefix(got.Persona, "# Mine") {
		t.Fatalf("customized soul = %+v", got)
	}
	if w = postJSON(companionSoulRoute, `{"action":"reset","target":"profile.md"}`, companionTestOrigin); w.Code != http.StatusBadRequest {
		t.Fatalf("profile reset = %d, want 400", w.Code)
	}
	if w = postJSON(companionSoulRoute, `{"action":"reset","target":"persona.md"}`, "http://evil.test"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin reset = %d, want 403", w.Code)
	}
	if w = postJSON(companionSoulRoute, `{"action":"bogus","target":"persona.md","content":"x"}`, companionTestOrigin); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown action = %d, want 400", w.Code)
	}
	w = postJSON(companionSoulRoute, `{"action":"reset","target":"persona.md"}`, companionTestOrigin)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"removed":true`) {
		t.Fatalf("persona reset = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(personaPath); !os.IsNotExist(err) {
		t.Fatalf("reset kept persona.md: %v", err)
	}
	if got := getSoul(); got.PersonaSource != CompanionPersonaSourceOfficial || !got.PersonaInherited {
		t.Fatalf("reset soul = %+v", got)
	}
	if w = postJSON(companionSoulRoute, `{"action":"reset","target":"persona.md"}`, companionTestOrigin); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"removed":false`) {
		t.Fatalf("repeat reset = %d: %s", w.Code, w.Body.String())
	}

	// 2c. Picking an official preset replaces a custom persona.md and wins
	// over the default; "reset" clears both; unknown presets are refused.
	presetPath := filepath.Join(soulRoot, "default", companionSoulPresetFile)
	if w = postJSON(companionSoulRoute, `{"target":"persona.md","content":"# Mine\n"}`, companionTestOrigin); w.Code != http.StatusOK {
		t.Fatalf("persona save = %d: %s", w.Code, w.Body.String())
	}
	if w = postJSON(companionSoulRoute, `{"action":"preset","presetId":"no-such-preset"}`, companionTestOrigin); w.Code != http.StatusBadRequest {
		t.Fatalf("unknown preset = %d, want 400", w.Code)
	}
	if w = postJSON(companionSoulRoute, `{"action":"preset","presetId":"`+pickedPreset.ID+`"}`, "http://evil.test"); w.Code != http.StatusForbidden {
		t.Fatalf("cross-origin preset = %d, want 403", w.Code)
	}
	w = postJSON(companionSoulRoute, `{"action":"preset","presetId":"`+pickedPreset.ID+`"}`, companionTestOrigin)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"removed":true`) {
		t.Fatalf("preset pick = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(personaPath); !os.IsNotExist(err) {
		t.Fatalf("preset pick kept persona.md: %v", err)
	}
	if got := getSoul(); got.PersonaSource != CompanionPersonaSourcePreset || !got.PersonaInherited || got.ActivePresetID != pickedPreset.ID ||
		!strings.HasPrefix(got.Persona, "# "+pickedPreset.Name+"\n") {
		t.Fatalf("preset soul = %+v", got)
	}
	if w = postJSON(companionSoulRoute, `{"action":"preset","presetId":"`+CompanionPersonaPresetDefault+`"}`, companionTestOrigin); w.Code != http.StatusOK {
		t.Fatalf("default pick = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(presetPath); !os.IsNotExist(err) {
		t.Fatalf("default pick kept the preset file: %v", err)
	}
	if got := getSoul(); got.PersonaSource != CompanionPersonaSourceOfficial || got.ActivePresetID != CompanionPersonaPresetDefault {
		t.Fatalf("default-pick soul = %+v", got)
	}
	if w = postJSON(companionSoulRoute, `{"action":"preset","presetId":"`+pickedPreset.ID+`"}`, companionTestOrigin); w.Code != http.StatusOK {
		t.Fatalf("re-pick = %d: %s", w.Code, w.Body.String())
	}
	if w = postJSON(companionSoulRoute, `{"action":"reset","target":"persona.md"}`, companionTestOrigin); w.Code != http.StatusOK {
		t.Fatalf("reset after pick = %d: %s", w.Code, w.Body.String())
	}
	if _, err := os.Stat(presetPath); !os.IsNotExist(err) {
		t.Fatalf("reset kept the preset file: %v", err)
	}
	if got := getSoul(); got.PersonaSource != CompanionPersonaSourceOfficial || got.ActivePresetID != CompanionPersonaPresetDefault {
		t.Fatalf("reset-after-pick soul = %+v", got)
	}

	// 3. Clean POST saves profile.md (200); suspicious POST holds in approval queue (202).
	w = postJSON(companionSoulRoute, `{"target":"profile.md","content":"Enjoys concise changelogs."}`, companionTestOrigin)
	if w.Code != http.StatusOK {
		t.Fatalf("clean profile save = %d: %s", w.Code, w.Body.String())
	}
	w = postJSON(companionSoulRoute, `{"target":"profile.md","content":"Email me at dev@corp.example"}`, companionTestOrigin)
	if w.Code != http.StatusAccepted {
		t.Fatalf("suspicious profile save = %d, want 202: %s", w.Code, w.Body.String())
	}
	var holdResp SaveUserDocResult
	if err := json.Unmarshal(w.Body.Bytes(), &holdResp); err != nil || holdResp.Pending == nil {
		t.Fatalf("decode hold resp: %v (%s)", err, w.Body.String())
	}

	// 4. List pending -> approve edited -> verify profile updated and queue drained.
	w = companionTestRequest(d, cookie, http.MethodGet, companionMemoryPendingRoute, "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "dev@corp.example") {
		t.Fatalf("GET pending = %d: %s", w.Code, w.Body.String())
	}
	approvePath := companionMemoryPendingRoute + "/" + itoa64(holdResp.Pending.ID) + "/approve"
	w = postJSON(approvePath, `{"content":"Prefers written updates."}`, companionTestOrigin)
	if w.Code != http.StatusOK {
		t.Fatalf("approve pending = %d: %s", w.Code, w.Body.String())
	}

	// 5. Episodes API search & prune.
	epStore, err := d.companionMemoryStore().Episodes()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := epStore.AddEpisode(context.Background(), CompanionEpisodeInput{
		ProjectKey: "devenv",
		Summary:    "Verified companion memory drawer FTS5 search",
		Tags:       []string{"memory"},
	}); err != nil {
		t.Fatal(err)
	}
	w = companionTestRequest(d, cookie, http.MethodGet, companionMemoryEpisodesRoute+"?project=devenv&q=FTS5", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Verified companion memory drawer") {
		t.Fatalf("GET episodes = %d: %s", w.Code, w.Body.String())
	}
	w = postJSON(companionMemoryEpisodesRoute+"/prune", `{"project":"devenv"}`, companionTestOrigin)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"deleted":1`) {
		t.Fatalf("POST prune = %d: %s", w.Code, w.Body.String())
	}

	// 6. Export ZIP -> Import ZIP into another Soul ID.
	w = companionTestRequest(d, cookie, http.MethodPost, companionSoulExportRoute, companionTestOrigin)
	if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("POST export = %d (%s)", w.Code, w.Header().Get("Content-Type"))
	}
	impReq := authRequest(http.MethodPost, companionSoulImportRoute+"?soul=backup-soul", string(w.Body.Bytes()))
	impReq.AddCookie(cookie)
	impReq.Header.Set("Origin", companionTestOrigin)
	impReq.Header.Set("Content-Type", "application/zip")
	wImp := authServe(d, impReq)
	if wImp.Code != http.StatusOK || !strings.Contains(wImp.Body.String(), `"soulId":"backup-soul"`) {
		t.Fatalf("POST import = %d: %s", wImp.Code, wImp.Body.String())
	}
}

func itoa64(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
