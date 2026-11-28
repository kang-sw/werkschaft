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

func TestSlotAuthReadyPiRPC(t *testing.T) {
	if slices.Contains(SlotLoginHarnesses(), "pi-rpc") {
		t.Fatalf("SlotLoginHarnesses() must remain codex/claude only, got %v", SlotLoginHarnesses())
	}

	cases := []struct {
		name    string
		content *string
		want    bool
	}{
		{name: "missing file", content: nil, want: false},
		{name: "invalid json", content: ptrString("{not-json"), want: false},
		{name: "empty object", content: ptrString("{}"), want: false},
		{name: "empty provider object", content: ptrString(`{"openai":{}}`), want: false},
		{name: "whitespace and non-string values only", content: ptrString(`{"openai":{"type":"   ","expires":1700000000,"active":true}}`), want: false},
		{name: "provider entry not object", content: ptrString(`{"openai":"sk-raw"}`), want: false},
		{name: "valid api key", content: ptrString(`{"openai":{"type":"api_key","key":"sk-test-secret"}}`), want: true},
		{name: "valid oauth credentials", content: ptrString(`{"anthropic":{"type":"oauth","access":"tok-access","refresh":"tok-refresh"}}`), want: true},
		{name: "mixed empty and valid provider", content: ptrString(`{"openai":{},"openrouter":{"key":"or-key"}}`), want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.content != nil {
				if err := os.WriteFile(filepath.Join(dir, "auth.json"), []byte(*tc.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if got := SlotAuthReady("pi-rpc", dir); got != tc.want {
				t.Fatalf("SlotAuthReady(pi-rpc) = %v, want %v", got, tc.want)
			}
		})
	}
	if SlotAuthReady("pi-rpc", "") {
		t.Fatal("SlotAuthReady(pi-rpc, '') = true, want false")
	}
}

func ptrString(s string) *string { return &s }
