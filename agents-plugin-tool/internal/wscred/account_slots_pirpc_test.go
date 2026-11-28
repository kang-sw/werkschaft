package wscred

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestAccountSlotPiRPCD10ResolutionAndT0Preservation(t *testing.T) {
	if !SupportedSlotHarness("pi-rpc") {
		t.Fatal("SupportedSlotHarness(pi-rpc) = false, want true")
	}
	if got := HarnessForConfigEnv("PI_CODING_AGENT_DIR"); got != "pi-rpc" {
		t.Fatalf("HarnessForConfigEnv(PI_CODING_AGENT_DIR) = %q, want pi-rpc", got)
	}
	if got := SlotConfigEnvKey("pi-rpc"); got != "PI_CODING_AGENT_DIR" {
		t.Fatalf("SlotConfigEnvKey(pi-rpc) = %q, want PI_CODING_AGENT_DIR", got)
	}
	if slices.Contains(SlotLoginHarnesses(), "pi-rpc") {
		t.Fatalf("SlotLoginHarnesses() must remain codex/claude only, got %v", SlotLoginHarnesses())
	}

	tmp := t.TempDir()
	fakeHome := filepath.Join(tmp, "home")
	slotDir := filepath.Join(tmp, "pi-work")
	envDir := filepath.Join(tmp, "pi-env")

	// D10 precedence: slot config_dir > explicit home > inherited env > user home.
	if got := ResolveHarnessHome(HarnessHomeOptions{
		Harness:       "pi-rpc",
		SlotConfigDir: slotDir,
		ExplicitHome:  fakeHome,
		Getenv:        func(string) string { return envDir },
		UserHomeDir:   func() (string, error) { return fakeHome, nil },
	}); got != filepath.Clean(slotDir) {
		t.Fatalf("slot dir precedence = %q, want %q", got, filepath.Clean(slotDir))
	}
	if got := ResolveHarnessHome(HarnessHomeOptions{
		Harness:     "pi-rpc",
		Getenv:      func(k string) string { if k == "PI_CODING_AGENT_DIR" { return envDir }; return "" },
		UserHomeDir: func() (string, error) { return fakeHome, nil },
	}); got != filepath.Clean(envDir) {
		t.Fatalf("env fallback = %q, want %q", got, filepath.Clean(envDir))
	}
	wantDefault := filepath.Join(fakeHome, ".pi", "agent")
	if got := ResolveHarnessHome(HarnessHomeOptions{
		Harness:      "pi-rpc",
		ExplicitHome: fakeHome,
		Getenv:       func(string) string { return envDir },
		UserHomeDir:  func() (string, error) { return fakeHome, nil },
	}); got != wantDefault {
		t.Fatalf("explicit home suppresses env = %q, want %q", got, wantDefault)
	}

	// Registry CRUD, ActiveSlots(), RegisteredSlotDirs(), and T0 preservation.
	path := filepath.Join(tmp, AccountSlotsFileName)
	reg := NewAccountSlotRegistry(path)
	t.Cleanup(reg.Close)

	if _, err := reg.AddSlot(AccountSlot{ID: "work", Harness: "pi-rpc", Label: "Pi Work", ConfigDir: slotDir, Managed: true}); err != nil {
		t.Fatalf("AddSlot(pi-rpc): %v", err)
	}
	if err := reg.SetActive("pi-rpc", "work"); err != nil {
		t.Fatalf("SetActive(pi-rpc, work): %v", err)
	}
	active := reg.ActiveSlots()
	if active["pi-rpc"] != "work" {
		t.Fatalf("ActiveSlots()[pi-rpc] = %q, want work (all=%v)", active["pi-rpc"], active)
	}
	if !slices.Contains(RegisteredSlotDirs(), filepath.Clean(slotDir)) {
		t.Fatalf("RegisteredSlotDirs() missing pi-rpc slot dir %q", slotDir)
	}

	// Verify T0 round-trip: reload from disk, mutate a codex slot, confirm pi-rpc
	// active entry and slot row are preserved verbatim.
	reg2 := NewAccountSlotRegistry(path)
	t.Cleanup(reg2.Close)
	if _, err := reg2.AddSlot(AccountSlot{ID: "cx", Harness: "codex", ConfigDir: filepath.Join(tmp, "cx")}); err != nil {
		t.Fatalf("AddSlot(codex): %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Active map[string]string `json:"active"`
		Slots  []AccountSlot     `json:"slots"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Active["pi-rpc"] != "work" {
		t.Fatalf("persisted active[pi-rpc] = %q, want work", doc.Active["pi-rpc"])
	}
	foundPiSlot := false
	for _, s := range doc.Slots {
		if s.Harness == "pi-rpc" && s.ID == "work" && s.ConfigDir == filepath.Clean(slotDir) {
			foundPiSlot = true
		}
	}
	if !foundPiSlot {
		t.Fatalf("persisted slots missing pi-rpc work slot: %+v", doc.Slots)
	}
}
