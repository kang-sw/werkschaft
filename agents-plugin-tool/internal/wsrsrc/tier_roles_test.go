package wsrsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTierRoleFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, content := range files {
		writeFile(t, root, rel, content)
	}
	m, err := GenerateManifest(root)
	if err != nil {
		t.Fatalf("GenerateManifest: %v", err)
	}
	if err := WriteManifest(root, m); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}
	return root
}

func TestTierRolesReadsBasePlaybookFrontmatter(t *testing.T) {
	root := writeTierRoleFixture(t, map[string]string{
		"worker/worker.md":        "---\nkind: render\ntier: medium\ntier-role: ticket-worker\ntier-parent: implement:*\n---\nbody\n",
		"worker/worker.claude.md": "---\nkind: render\ntier: medium\ntier-role: other\ntier-parent: review:*\n---\nvariant\n",
		"plain/plain.md":          "---\nkind: render\ntier: small\n---\nbody\n",
		"explore/explore.md":      "---\nkind: render\ntier: small\ntier-role: playbook-explore\ntier-parent: explore:*\n---\nbody\n",
		"flat.md":                 "---\ntier-role: flat\ntier-parent: explore:*\n---\n",
	})
	roles, err := TierRoles(os.DirFS(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 {
		t.Fatalf("roles = %+v, want the two base declarations", roles)
	}
	if roles[0] != (PlaybookTierRole{Playbook: "explore", Role: "playbook-explore", Parent: "explore:*", Size: "small"}) ||
		roles[1] != (PlaybookTierRole{Playbook: "worker", Role: "ticket-worker", Parent: "implement:*", Size: "medium"}) {
		t.Fatalf("roles = %+v", roles)
	}
	pb, err := Load(root, "worker", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if pb.Meta.TierRole != "ticket-worker" || pb.Meta.TierParent != "implement:*" {
		t.Fatalf("meta = %+v", pb.Meta)
	}
	if _, leaked := pb.Meta.Extra["tier-role"]; leaked {
		t.Fatalf("tier-role leaked into Extra: %v", pb.Meta.Extra)
	}
}

func TestTierRolesRejectsDuplicatesAndMissingParents(t *testing.T) {
	for name, files := range map[string]map[string]string{
		"needs a tier-parent": {"a/a.md": "---\ntier: small\ntier-role: a\n---\n"},
		"declared by both": {
			"a/a.md": "---\ntier: small\ntier-role: same\ntier-parent: explore:*\n---\n",
			"b/b.md": "---\ntier: small\ntier-role: same\ntier-parent: explore:*\n---\n",
		},
	} {
		root := writeTierRoleFixture(t, files)
		if _, err := TierRoles(os.DirFS(root)); err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("TierRoles = %v, want %q", err, name)
		}
	}
}

// The shipped resource trees must always scan cleanly: the daemon builds
// its routing graph from their packaged copies.
func TestShippedPackagesTierRolesScan(t *testing.T) {
	for _, dir := range []string{"agents-plugin", "agents-plugin-wsflow"} {
		root := filepath.Join("..", "..", "..", dir, "rsrc")
		if _, err := TierRoles(os.DirFS(root)); err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
	}
}
