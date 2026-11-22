package wsrsrc

import (
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// PlaybookTierRole is one playbook's tier-routing role declaration: the
// role node (tier-role), its packaged default pointer (tier-parent), and
// the playbook's declared size (tier).
type PlaybookTierRole struct {
	Playbook string
	Role     string
	Parent   string
	Size     string
}

// TierRoles lists the tier-role declarations of every base playbook file
// (<name>/<name>.md) in root's manifest, sorted by role. Harness and
// profile variants share their base file's role. Two playbooks declaring
// one role, or a role without a parent, is an error.
func TierRoles(root fs.FS) ([]PlaybookTierRole, error) {
	manifest, err := ReadManifestFS(root)
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	var out []PlaybookTierRole
	for rel := range manifest.Files {
		dir, file := path.Split(rel)
		dir = strings.TrimSuffix(dir, "/")
		if dir == "" || strings.Contains(dir, "/") || file != dir+".md" {
			continue
		}
		data, err := fs.ReadFile(root, rel)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", rel, err)
		}
		fm, _ := parseFrontmatter(string(data))
		meta := metaFromFrontmatter(fm)
		role := strings.TrimSpace(meta.TierRole)
		if role == "" {
			continue
		}
		parent := strings.TrimSpace(meta.TierParent)
		if parent == "" {
			return nil, fmt.Errorf("%s: tier-role %q needs a tier-parent", rel, role)
		}
		if other, dup := seen[role]; dup {
			return nil, fmt.Errorf("tier-role %q is declared by both %s and %s", role, other, dir)
		}
		seen[role] = dir
		out = append(out, PlaybookTierRole{Playbook: dir, Role: role, Parent: parent, Size: strings.TrimSpace(meta.Tier)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Role < out[j].Role })
	return out, nil
}
