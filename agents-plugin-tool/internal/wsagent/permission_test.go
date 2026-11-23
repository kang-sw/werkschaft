package wsagent

import "testing"

// builtinPermission is the production permission policy a built-in backend
// registers, so these tests pin what the registration declares.
func builtinPermission(backend string) PermissionPolicy {
	return builtinRegistration(backend).Permission
}

func TestPermissionLeadMapping(t *testing.T) {
	for _, tc := range []struct {
		mode  string
		level PermissionLevel
	}{
		{"plan", PermissionReadOnly}, {"bypassPermissions", PermissionFull},
		{"", PermissionWorkdirWrite}, {"auto", PermissionWorkdirWrite},
		{"default", PermissionWorkdirWrite}, {"acceptEdits", PermissionWorkdirWrite}, {"dontAsk", PermissionWorkdirWrite},
	} {
		if got := builtinPermission(BackendClaude).legacyLevel(tc.mode); got != tc.level {
			t.Fatalf("%s = %s, want %s", tc.mode, got, tc.level)
		}
	}
	for mode, level := range map[string]PermissionLevel{
		"read-only": PermissionReadOnly, "workspace-write": PermissionWorkdirWrite, "danger-full-access": PermissionFull,
	} {
		if got := builtinPermission(BackendCodex).legacyLevel(mode); got != level {
			t.Fatalf("codex %s = %s, want %s", mode, got, level)
		}
	}
	// Pin the contract independently: inverse projection accepts multiple native
	// modes for workdir-write and cannot establish the required launch flag.
	literalModes := map[PermissionLevel]string{PermissionReadOnly: "plan", PermissionWorkdirWrite: "acceptEdits", PermissionFull: "bypassPermissions"}
	codexModes := map[PermissionLevel]string{PermissionReadOnly: "read-only", PermissionWorkdirWrite: "workspace-write", PermissionFull: "danger-full-access"}
	for _, level := range LeadPermissionLevels() {
		if got := builtinPermission(BackendClaude).nativeMode(level); got != literalModes[level] {
			t.Fatalf("mapping %s = %s, want literal %s", level, got, literalModes[level])
		}
		if got := builtinPermission(BackendPi).nativeMode(level); got != "" {
			t.Fatalf("invented Pi mode %s", got)
		}
		if got := builtinPermission(BackendCodex).nativeMode(level); got != codexModes[level] {
			t.Fatalf("Codex mapping %s = %s, want literal %s", level, got, codexModes[level])
		}
		for _, active := range []string{"", "auto", "plan", "bypassPermissions"} {
			want := "advisory"
			if level == PermissionReadOnly && active == "plan" {
				want = "partial"
			}
			if got := builtinPermission(BackendClaude).quality(level, active); got != want {
				t.Fatalf("quality %s/%s = %s", level, active, got)
			}
			if got := builtinPermission(BackendPi).quality(level, active); got != "advisory" {
				t.Fatalf("Pi quality %s", got)
			}
		}
		for _, active := range []string{"", "read-only", "workspace-write", "danger-full-access", "read-only (approval never)"} {
			want := "advisory"
			if level == PermissionReadOnly && active == "read-only" {
				want = "partial"
			}
			if got := builtinPermission(BackendCodex).quality(level, active); got != want {
				t.Fatalf("Codex quality %s/%s = %s", level, active, got)
			}
		}
	}
	if got := builtinPermission(BackendClaude).legacyLevel("unknown"); got != "" {
		t.Fatalf("unknown mode fabricated %s", got)
	}
	if got := builtinPermission(BackendCodex).legacyLevel(""); got != "" {
		t.Fatalf("omitted Codex mode fabricated %s", got)
	}
}

func TestPermissionLeadAuthority(t *testing.T) {
	for _, current := range LeadPermissionLevels() {
		for _, next := range LeadPermissionLevels() {
			for _, user := range []bool{false, true} {
				for _, ack := range []bool{false, true} {
					err := authorizePermission(current, next, user, ack)
					allowed := (next.rank() <= current.rank() || user) && (next != PermissionFull || (user && ack))
					if (err == nil) != allowed {
						t.Fatalf("%s -> %s user=%v ack=%v: %v", current, next, user, ack, err)
					}
				}
			}
		}
	}
	if authorizePermission(PermissionFull, "unknown", true, true) == nil {
		t.Fatal("invalid selection accepted")
	}
	if authorizePermission("", PermissionReadOnly, false, false) == nil {
		t.Fatal("unknown evidence granted agent authority")
	}
}
