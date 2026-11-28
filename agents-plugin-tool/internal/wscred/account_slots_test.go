package wscred

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestAccountSlotRegistryCRUDAndT0Preservation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AccountSlotsFileName)
	slotA := filepath.Join(dir, "codex-a")
	slotB := filepath.Join(dir, "codex-b")

	reg := NewAccountSlotRegistry(path)
	t.Cleanup(reg.Close)

	// Implicit default slot always exists and starts active.
	slots := reg.Slots("codex")
	if len(slots) != 1 || slots[0].ID != DefaultAccountSlotID || !slots[0].Implicit || !slots[0].Active {
		t.Fatalf("initial slots: %+v", slots)
	}
	if active := reg.ActiveSlot("codex"); active.ID != DefaultAccountSlotID || active.ConfigDir != "" {
		t.Fatalf("initial active: %+v", active)
	}

	// Add two codex slots.
	addedA, err := reg.AddSlot(AccountSlot{ID: "work", Harness: "codex", Label: "Work", ConfigDir: slotA, Managed: true})
	if err != nil || addedA.ID != "work" || addedA.ConfigDir != slotA {
		t.Fatalf("add work: %+v %v", addedA, err)
	}
	if _, err := reg.AddSlot(AccountSlot{ID: "work", Harness: "codex", ConfigDir: slotA}); !errors.Is(err, ErrAccountSlotExists) {
		t.Fatalf("duplicate add should fail with ErrAccountSlotExists: %v", err)
	}
	if _, err := reg.AddSlot(AccountSlot{ID: "personal", Harness: "codex", Label: "Personal", ConfigDir: slotB}); err != nil {
		t.Fatalf("add personal: %v", err)
	}
	if !slices.Contains(RegisteredSlotDirs(), slotA) || !slices.Contains(RegisteredSlotDirs(), slotB) {
		t.Fatalf("RegisteredSlotDirs missing added slots: %v", RegisteredSlotDirs())
	}

	// Switch active to work, then back to implicit default, then to personal.
	if err := reg.SetActive("codex", "work"); err != nil {
		t.Fatalf("set active work: %v", err)
	}
	if got := reg.ActiveSlot("codex"); got.ID != "work" || got.ConfigDir != slotA {
		t.Fatalf("active after switch: %+v", got)
	}
	if err := reg.SetActive("codex", DefaultAccountSlotID); err != nil {
		t.Fatalf("set active default: %v", err)
	}
	if got := reg.ActiveSlot("codex"); got.ID != DefaultAccountSlotID || got.ConfigDir != "" {
		t.Fatalf("active after default switch: %+v", got)
	}
	if err := reg.SetActive("codex", "personal"); err != nil {
		t.Fatalf("set active personal: %v", err)
	}
	if err := reg.SetActive("codex", "missing"); !errors.Is(err, ErrAccountSlotNotFound) {
		t.Fatalf("set active missing: %v", err)
	}

	// Inject an unknown top-level field (simulating a newer daemon T0 write) and
	// verify reloading + mutating preserves it.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatal(err)
	}
	top["future_feature"] = json.RawMessage(`{"enabled":true}`)
	patched, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, patched, 0o600); err != nil {
		t.Fatal(err)
	}

	reg2 := NewAccountSlotRegistry(path)
	t.Cleanup(reg2.Close)
	if got := reg2.ActiveSlot("codex"); got.ID != "personal" {
		t.Fatalf("reloaded active: %+v", got)
	}
	if err := reg2.UpdateSlotLabel("codex", "work", "Work Team"); err != nil {
		t.Fatalf("update label: %v", err)
	}
	raw2, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var top2 map[string]json.RawMessage
	if err := json.Unmarshal(raw2, &top2); err != nil {
		t.Fatal(err)
	}
	if string(top2["future_feature"]) == "" {
		t.Fatalf("T0 unknown field dropped: %s", raw2)
	}

	// Removing active slot falls back to default; removing default is refused.
	if err := reg2.RemoveSlot("codex", DefaultAccountSlotID); !errors.Is(err, ErrDefaultSlotImmutable) {
		t.Fatalf("remove default: %v", err)
	}
	if err := reg2.RemoveSlot("codex", "personal"); err != nil {
		t.Fatalf("remove personal: %v", err)
	}
	if got := reg2.ActiveSlot("codex"); got.ID != DefaultAccountSlotID {
		t.Fatalf("active after removing active slot: %+v", got)
	}

	// Close blocks subsequent writes and unregisters deny dirs.
	reg.Close()
	reg2.Close()
	if err := reg2.SetActive("codex", "work"); !errors.Is(err, ErrStoreClosed) {
		t.Fatalf("write after close: %v", err)
	}
	if slices.Contains(RegisteredSlotDirs(), slotA) {
		t.Fatalf("RegisteredSlotDirs leaked after close: %v", RegisteredSlotDirs())
	}
}

func TestResolveHarnessHomePrecedenceD10(t *testing.T) {
	userHome := filepath.Join(t.TempDir(), "user-home")
	explicitHome := filepath.Join(t.TempDir(), "explicit-home")
	envDir := filepath.Join(t.TempDir(), "env-codex")
	slotDir := filepath.Join(t.TempDir(), "slot-codex")

	env := func(k string) string {
		if k == "CODEX_HOME" {
			return envDir
		}
		if k == "CLAUDE_CONFIG_DIR" {
			return filepath.Join(envDir, "claude")
		}
		return ""
	}
	userHomeFn := func() (string, error) { return userHome, nil }

	// 1. SlotConfigDir wins over everything (including ExplicitHome and env).
	if got := ResolveHarnessHome(HarnessHomeOptions{
		Harness: "codex", ExplicitHome: explicitHome, SlotConfigDir: slotDir, Getenv: env, UserHomeDir: userHomeFn,
	}); got != slotDir {
		t.Fatalf("slot config dir precedence: got %q want %q", got, slotDir)
	}

	// 2. ExplicitHome suppresses inherited CODEX_HOME / CLAUDE_CONFIG_DIR (075fa3ff).
	if got := ResolveHarnessHome(HarnessHomeOptions{
		Harness: "codex", ExplicitHome: explicitHome, Getenv: env, UserHomeDir: userHomeFn,
	}); got != filepath.Join(explicitHome, ".codex") {
		t.Fatalf("explicit home must suppress env: got %q", got)
	}
	if got := ResolveHarnessHome(HarnessHomeOptions{
		Harness: "claude", ExplicitHome: explicitHome, Getenv: env, UserHomeDir: userHomeFn,
	}); got != filepath.Join(explicitHome, ".claude") {
		t.Fatalf("explicit home claude must suppress env: got %q", got)
	}

	// 3. Without ExplicitHome or SlotConfigDir, inherited env wins over UserHomeDir.
	if got := ResolveHarnessHome(HarnessHomeOptions{
		Harness: "codex", Getenv: env, UserHomeDir: userHomeFn,
	}); got != envDir {
		t.Fatalf("env precedence: got %q want %q", got, envDir)
	}

	// 4. Without env, default home is UserHomeDir/.codex.
	if got := ResolveHarnessHome(HarnessHomeOptions{
		Harness: "codex", Getenv: func(string) string { return "" }, UserHomeDir: userHomeFn,
	}); got != filepath.Join(userHome, ".codex") {
		t.Fatalf("default home fallback: got %q", got)
	}
}

func TestCreateManagedSlotDirPermissions(t *testing.T) {
	cacheHome := t.TempDir()
	canonCache, err := filepath.EvalSymlinks(cacheHome)
	if err != nil {
		canonCache = cacheHome
	}
	dir, err := CreateManagedSlotDir(cacheHome, "codex", "slot-1")
	if err != nil {
		t.Fatalf("CreateManagedSlotDir: %v", err)
	}
	want := filepath.Join(canonCache, "dashboard", "slots", "codex", "slot-1")
	if dir != want {
		t.Fatalf("got %q want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("stat slot dir: %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o700 {
		t.Fatalf("slot dir perms: got %o want 0700", info.Mode().Perm())
	}
	if _, err := CreateManagedSlotDir(cacheHome, "codex", DefaultAccountSlotID); !errors.Is(err, ErrInvalidAccountSlot) {
		t.Fatalf("default id must be refused: %v", err)
	}
	if _, err := CreateManagedSlotDir(cacheHome, "codex", "../escape"); !errors.Is(err, ErrInvalidAccountSlot) {
		t.Fatalf("traversal id must be refused: %v", err)
	}
}

func TestAccountSlotLabelRuneBounds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, AccountSlotsFileName)
	slotA := filepath.Join(dir, "codex-a")
	slotB := filepath.Join(dir, "codex-b")

	reg := NewAccountSlotRegistry(path)
	t.Cleanup(reg.Close)

	label80 := strings.Repeat("가", 80) // 240 UTF-8 bytes, 80 runes
	label81 := strings.Repeat("가", 81)

	added, err := reg.AddSlot(AccountSlot{
		ID:        "work",
		Harness:   "codex",
		Label:     "  " + label80 + "  ",
		ConfigDir: slotA,
	})
	if err != nil {
		t.Fatalf("AddSlot with 80 runes failed: %v", err)
	}
	if added.Label != label80 {
		t.Fatalf("AddSlot did not trim label: got %q", added.Label)
	}

	if _, err := reg.AddSlot(AccountSlot{
		ID:        "too-long",
		Harness:   "codex",
		Label:     label81,
		ConfigDir: slotB,
	}); !errors.Is(err, ErrInvalidAccountSlot) {
		t.Fatalf("AddSlot 81 runes = %v, want ErrInvalidAccountSlot", err)
	}

	if err := reg.UpdateSlotLabel("codex", "work", "  "+label80+"\t"); err != nil {
		t.Fatalf("UpdateSlotLabel 80 runes failed: %v", err)
	}
	if got, ok := reg.ResolveSlot("codex", "work"); !ok || got.Label != label80 {
		t.Fatalf("ResolveSlot after 80-rune rename: ok=%v got=%+v", ok, got)
	}
	if err := reg.UpdateSlotLabel("codex", "work", label81); !errors.Is(err, ErrInvalidAccountSlot) {
		t.Fatalf("UpdateSlotLabel 81 runes = %v, want ErrInvalidAccountSlot", err)
	}
	if err := reg.UpdateSlotLabel("codex", "work", "   "); !errors.Is(err, ErrInvalidAccountSlot) {
		t.Fatalf("UpdateSlotLabel blank = %v, want ErrInvalidAccountSlot", err)
	}
	if err := reg.UpdateSlotLabel("codex", DefaultAccountSlotID, "Renamed Default"); !errors.Is(err, ErrDefaultSlotImmutable) {
		t.Fatalf("UpdateSlotLabel default = %v, want ErrDefaultSlotImmutable", err)
	}
	if err := reg.UpdateSlotLabel("codex", "missing", "Valid"); !errors.Is(err, ErrAccountSlotNotFound) {
		t.Fatalf("UpdateSlotLabel missing = %v, want ErrAccountSlotNotFound", err)
	}
}
