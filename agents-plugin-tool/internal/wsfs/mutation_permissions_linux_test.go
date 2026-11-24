package wsfs

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestReplacementKeepsLinuxACLUnderDefaultACL(t *testing.T) {
	root := t.TempDir()
	privatePath := filepath.Join(root, "private")
	must(t, os.Chmod(root, 0o777))
	must(t, os.WriteFile(privatePath, []byte("before"), 0o600))
	acl := binary.LittleEndian.AppendUint32(nil, 2) // POSIX ACL xattr format version.
	for _, entry := range []struct {
		tag, perms uint16
		id         uint32
	}{
		{1, 7, ^uint32(0)},  // Owner.
		{2, 4, 65534},       // Named user.
		{4, 0, ^uint32(0)},  // Owning group.
		{16, 4, ^uint32(0)}, // Mask for group and named entries.
		{32, 0, ^uint32(0)}, // Other.
	} {
		acl = binary.LittleEndian.AppendUint16(acl, entry.tag)
		acl = binary.LittleEndian.AppendUint16(acl, entry.perms)
		acl = binary.LittleEndian.AppendUint32(acl, entry.id)
	}
	if err := unix.Setxattr(root, "system.posix_acl_default", acl, 0); errors.Is(err, unix.EOPNOTSUPP) {
		t.Skip("test filesystem does not support POSIX default ACLs")
	} else {
		must(t, err)
	}
	aclPath := filepath.Join(root, "with-acl")
	must(t, os.WriteFile(aclPath, []byte("before"), 0o640))
	if len(unixPermissionsAt(t, aclPath).acl) == 0 {
		t.Fatal("inheriting POSIX default ACL fixture produced no access ACL")
	}
	for _, path := range []string{privatePath, aclPath} {
		t.Run(filepath.Base(path), func(t *testing.T) {
			want := unixPermissionsAt(t, path)
			files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(stage *os.File) {
				must(t, want.verify(stage, nil))
			}}
			_, err := callMutation(t, files, "write", map[string]any{"path": filepath.Base(path), "content": "after"})
			must(t, err)
			file, err := os.Open(path)
			must(t, err)
			defer file.Close()
			must(t, want.verify(file, nil))
			assertReplacementContent(t, path, "after")
		})
	}
}
