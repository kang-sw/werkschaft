package wsagent

// codexPermissionPolicy maps the common levels to Codex's sandbox names. A
// launch with no selection keeps the user's own Codex settings, whose
// reported mode derives the display level only while the process lives.
func codexPermissionPolicy() PermissionPolicy {
	return PermissionPolicy{
		NativeModes: map[PermissionLevel]string{
			PermissionReadOnly:     "read-only",
			PermissionWorkdirWrite: "workspace-write",
			PermissionFull:         "danger-full-access",
		},
		LegacyModes: map[string]PermissionLevel{
			"read-only":          PermissionReadOnly,
			"workspace-write":    PermissionWorkdirWrite,
			"danger-full-access": PermissionFull,
		},
		LiveModeProjection: true,
	}
}
