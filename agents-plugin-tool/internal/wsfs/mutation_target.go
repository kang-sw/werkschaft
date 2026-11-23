package wsfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// mutationTarget keeps the deepest existing parent open from inspection to
// write. All opens and mkdirs use directory handles, so replacing a path
// component with a symlink cannot redirect the mutation.
type mutationTarget struct {
	path    string
	parent  *os.File
	missing []string
}

func pinMutationTarget(path string) (*mutationTarget, error) {
	root := filepath.VolumeName(path) + string(filepath.Separator)
	relative, err := filepath.Rel(root, filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	if path == root {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	dir, err := openMutationDir(nil, root, false)
	if err != nil {
		return nil, err
	}
	t := &mutationTarget{path: path, parent: dir}
	if relative == "." {
		return t, nil
	}
	parts := strings.Split(relative, string(filepath.Separator))
	for i, name := range parts {
		next, err := openMutationDir(t.parent, name, false)
		if errors.Is(err, fs.ErrNotExist) {
			t.missing = parts[i:]
			return t, nil
		}
		if err != nil {
			t.close()
			return nil, err
		}
		t.close()
		t.parent = next
	}
	return t, nil
}

func (t *mutationTarget) close() {
	_ = t.parent.Close()
}

// checkParent refuses a replacement that happened while computing the edit
// or persisting its intent, even when the new entry is an ordinary directory.
func (t *mutationTarget) checkParent() error {
	current, err := pinMutationTarget(t.path)
	if err == nil {
		defer current.close()
		before, oldErr := t.parent.Stat()
		after, newErr := current.parent.Stat()
		if oldErr == nil && newErr == nil && len(t.missing) == len(current.missing) && os.SameFile(before, after) {
			return nil
		}
	}
	return fmt.Errorf("%s changed while preparing the mutation", t.path)
}

func (t *mutationTarget) createParents() error {
	for _, name := range t.missing {
		dir, err := openMutationDir(t.parent, name, true)
		if err != nil {
			return err
		}
		t.close()
		t.parent = dir
	}
	t.missing = nil
	return nil
}
