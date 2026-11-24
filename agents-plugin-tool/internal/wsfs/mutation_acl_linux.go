package wsfs

import (
	"errors"
	"os"
	"runtime"

	"golang.org/x/sys/unix"
)

const mutationACLAttribute = "system.posix_acl_access"

func readMutationACL(file *os.File) ([]byte, error) {
	fd := int(file.Fd())
	defer runtime.KeepAlive(file)
	buf := make([]byte, 256)
	n, err := unix.Fgetxattr(fd, mutationACLAttribute, buf)
	if errors.Is(err, unix.ERANGE) {
		buf = make([]byte, 64<<10) // Linux XATTR_SIZE_MAX.
		n, err = unix.Fgetxattr(fd, mutationACLAttribute, buf)
	}
	if errors.Is(err, unix.ENODATA) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

func setMutationACL(file *os.File, acl []byte) error {
	fd := int(file.Fd())
	defer runtime.KeepAlive(file)
	if len(acl) == 0 {
		return unix.Fremovexattr(fd, mutationACLAttribute)
	}
	return unix.Fsetxattr(fd, mutationACLAttribute, acl, 0)
}
