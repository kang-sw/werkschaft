package wsfs

import (
	"os"
	"runtime"
	"syscall"
	"unsafe"
)

func readMutationACL(file *os.File) ([]byte, error) {
	acl, _, err := mutationACLSyscallPtr(aclGetFD, file.Fd(), 0, 0)
	runtime.KeepAlive(file)
	if err == syscall.ENOENT {
		return nil, nil
	}
	if err != 0 {
		return nil, err
	}
	defer mutationACLSyscall(aclFree, acl, 0, 0)
	size, _, err := mutationACLSyscall(aclSize, acl, 0, 0)
	if err != 0 {
		return nil, err
	}
	data := make([]byte, size)
	_, _, err = mutationACLSyscall(aclCopyExt, uintptr(unsafe.Pointer(unsafe.SliceData(data))), acl, size)
	if err != 0 {
		return nil, err
	}
	return data, nil
}

func setMutationACL(file *os.File, data []byte) error {
	const removeACL = uintptr(1) // _FILESEC_REMOVE_ACL in sys/fcntl.h.
	acl := removeACL
	if len(data) != 0 {
		var err syscall.Errno
		acl, _, err = mutationACLSyscallPtr(aclCopyInt, uintptr(unsafe.Pointer(&data[0])), 0, 0)
		if err != 0 {
			return err
		}
		defer mutationACLSyscall(aclFree, acl, 0, 0)
	}
	_, _, err := mutationACLSyscall(aclSetFD, file.Fd(), acl, 0)
	runtime.KeepAlive(file)
	if err != 0 {
		return err
	}
	return nil
}

// Release builds are cgo-free. Use the same libSystem syscall bridge as
// golang.org/x/sys/unix to call the native descriptor-based ACL functions.
//
//go:linkname mutationACLSyscall syscall.syscall
//go:uintptrescapes
func mutationACLSyscall(fn, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno)

//go:linkname mutationACLSyscallPtr syscall.syscallPtr
//go:uintptrescapes
func mutationACLSyscallPtr(fn, a1, a2, a3 uintptr) (uintptr, uintptr, syscall.Errno)

//go:cgo_import_dynamic libc_acl_get_fd acl_get_fd "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_acl_set_fd acl_set_fd "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_acl_free acl_free "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_acl_size acl_size "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_acl_copy_ext acl_copy_ext "/usr/lib/libSystem.B.dylib"
//go:cgo_import_dynamic libc_acl_copy_int acl_copy_int "/usr/lib/libSystem.B.dylib"

var aclGetFD, aclSetFD, aclFree, aclSize, aclCopyExt, aclCopyInt uintptr
