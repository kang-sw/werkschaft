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

// shippedTierRoleInventory is the owner's assignment (1515, 1534) by primary
// work: role -> parent and the playbook's unchanged size. A playbook that
// declares tier: must be listed here, and both trees must carry it.
var shippedTierRoleInventory = map[string][2]string{
	"playbook-explore":             {"explore:*", "small"},
	"reference-discovery":          {"explore:*", "small"},
	"ticket-selector":              {"explore:*", "small"},
	"ticket-batch-selector":        {"explore:*", "medium"},
	"ticket-classifier":            {"explore:*", "medium"},
	"implementer":                  {"implement:*", "medium"},
	"mockup-writer":                {"implement:*", "medium"},
	"ticket-worker":                {"implement:*", "medium"},
	"ticket-worker-elevated":       {"implement:*", "large"},
	"ticket-handler":               {"implement:*", "large"},
	"ship-handler":                 {"implement:*", "small"},
	"reviewer":                     {"review:*", "large"},
	"code-review-correctness":      {"review:*", "large"},
	"code-review-fit":              {"review:*", "medium"},
	"code-review-lite":             {"review:*", "medium"},
	"code-review-test":             {"review:*", "medium"},
	"ticket-reviewer-completeness": {"review:*", "medium"},
	"ticket-reviewer-design":       {"review:*", "large"},
	"review-handler":               {"review:*", "large"},
	"ticket-fact-populator":        {"review:*", "medium"},
	"fresh-read-doc-auditor":       {"review:*", "large"},
	"fresh-reader-audit":           {"review:*", "large"},
}

// Every tier-declaring playbook carries its role and default parent, with
// the size it declared before the migration, in both shipped trees.
func TestShippedPlaybooksDeclareTierRoles(t *testing.T) {
	for _, dir := range []string{"agents-plugin", "agents-plugin-wsflow"} {
		root := os.DirFS(filepath.Join("..", "..", "..", dir, "rsrc"))
		roles, err := TierRoles(root)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		got := map[string][2]string{}
		for _, r := range roles {
			got[r.Role] = [2]string{r.Parent, r.Size}
			if r.Role != r.Playbook && r.Role != "playbook-"+r.Playbook {
				t.Errorf("%s: playbook %s declares role %q", dir, r.Playbook, r.Role)
			}
		}
		if len(got) != len(shippedTierRoleInventory) {
			t.Errorf("%s: roles = %v, want %v", dir, got, shippedTierRoleInventory)
			continue
		}
		for role, want := range shippedTierRoleInventory {
			if got[role] != want {
				t.Errorf("%s: role %s = %v, want %v", dir, role, got[role], want)
			}
		}
		// No playbook may declare a size without a role.
		manifest, err := ReadManifestFS(root)
		if err != nil {
			t.Fatal(err)
		}
		for rel := range manifest.Files {
			base := strings.TrimSuffix(filepath.Base(rel), ".md")
			if filepath.Dir(rel) != base || !strings.HasSuffix(rel, ".md") {
				continue
			}
			data, err := os.ReadFile(filepath.Join("..", "..", "..", dir, "rsrc", rel))
			if err != nil {
				t.Fatal(err)
			}
			fm, _ := parseFrontmatter(string(data))
			meta := metaFromFrontmatter(fm)
			if meta.Tier != "" && meta.TierRole == "" {
				t.Errorf("%s: %s declares tier %q without tier-role", dir, rel, meta.Tier)
			}
		}
	}
}

// A child never exceeds its parent's tier, so a handler spawned below the
// reviewers it spawns can never start them. The handlers must declare at least
// the tier of every reviewer they render.
func TestShippedHandlersDeclareAtLeastTheirReviewersTier(t *testing.T) {
	rank := map[string]int{"small": 1, "medium": 2, "large": 3, "xlarge": 4}
	handlers := map[string][]string{
		"ticket-handler": {"ticket-reviewer-design", "ticket-reviewer-completeness", "ticket-fact-populator"},
		"review-handler": {"reviewer", "code-review-correctness", "code-review-fit", "code-review-test", "fresh-reader-audit"},
	}
	for _, dir := range []string{"agents-plugin", "agents-plugin-wsflow"} {
		root := filepath.Join("..", "..", "..", dir, "rsrc")
		tier := func(name string) int {
			pb, err := Load(root, name, "", "", nil)
			if err != nil {
				t.Fatalf("%s: load %s: %v", dir, name, err)
			}
			r, ok := rank[pb.Meta.Tier]
			if !ok {
				t.Fatalf("%s: %s declares tier %q", dir, name, pb.Meta.Tier)
			}
			return r
		}
		for handler, spawned := range handlers {
			floor := tier(handler)
			for _, name := range spawned {
				if got := tier(name); got > floor {
					t.Errorf("%s: %s declares a lower tier than %s, which it spawns", dir, handler, name)
				}
			}
		}
	}
}
