package wsfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestReplacementKeepsWindowsDACLAtFirstWrite(t *testing.T) {
	for _, protected := range []bool{true, false} {
		name := "inheriting"
		if protected {
			name = "protected"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			user := mutationTestWindowsUser(t)
			// The parent grants the Anonymous SID read access, while the
			// destination either omits that grant or explicitly denies it.
			setMutationTestWindowsDACL(t, root, "D:P(A;OICI;FA;;;"+user+")(A;OICI;FR;;;AN)")
			path := filepath.Join(root, "private")
			if protected {
				createMutationTestWindowsFile(t, path, "D:P(A;;FA;;;"+user+")")
			} else {
				must(t, os.WriteFile(path, []byte("before"), 0o600))
				setMutationTestWindowsDACL(t, path, "D:(D;;FR;;;AN)(A;;FA;;;"+user+")")
			}
			want := windowsPermissionsAt(t, path)
			if want.protected != protected || (!protected && want.dacl.AceCount < 3) {
				t.Fatalf("fixture protection = %v, ACEs = %d", want.protected, want.dacl.AceCount)
			}
			var observed bool
			files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(stage *os.File) {
				observed = true
				must(t, want.verify(stage, nil))
				info, err := stage.Stat()
				must(t, err)
				if info.Size() != 0 {
					t.Fatalf("stage already holds %d bytes at first write", info.Size())
				}
			}}
			_, err := callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
			must(t, err)
			if !observed {
				t.Fatal("missed first write")
			}
			file, err := os.Open(path)
			must(t, err)
			defer file.Close()
			must(t, want.verify(file, nil))
			assertReplacementContent(t, path, "after")
		})
	}
}

func TestReplacementRefusesWindowsDACLMismatch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	user := mutationTestWindowsUser(t)
	createMutationTestWindowsFile(t, path, "D:P(A;;FA;;;"+user+")")
	files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(stage *os.File) {
		setMutationTestWindowsDACL(t, stage.Name(), "D:P(D;;FR;;;AN)(A;;FA;;;"+user+")")
	}}
	_, err := callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
	if err == nil || !strings.Contains(err.Error(), "DACL or DACL protection differs") {
		t.Fatalf("error = %v, want DACL refusal", err)
	}
	assertReplacementContent(t, path, "before")
}

func TestReplacementRefusesWindowsIntegrityLabelMismatch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "private")
	user := mutationTestWindowsUser(t)
	createMutationTestWindowsFile(t, path, "D:P(A;;FA;;;"+user+")")
	labelSD, err := windows.SecurityDescriptorFromString("S:(ML;;NW;;;LW)")
	must(t, err)
	label, _, err := labelSD.SACL()
	must(t, err)
	files := &Files{Root: root, Authority: AuthorityWorkdirWrite, beforeReplacementWrite: func(stage *os.File) {
		must(t, windows.SetNamedSecurityInfo(stage.Name(), windows.SE_FILE_OBJECT,
			windows.LABEL_SECURITY_INFORMATION, nil, nil, nil, label))
	}}
	_, err = callMutation(t, files, "write", map[string]any{"path": "private", "content": "after"})
	if err == nil || !strings.Contains(err.Error(), "mandatory integrity label differs") {
		t.Fatalf("error = %v, want integrity-label refusal", err)
	}
	assertReplacementContent(t, path, "before")
}

func TestReplacementDistinguishesWindowsNullAndEmptyDACL(t *testing.T) {
	empty, err := windows.SecurityDescriptorFromString("D:P")
	must(t, err)
	acl, _, err := empty.DACL()
	must(t, err)
	if acl == nil || acl.AceCount != 0 || equalMutationACL(acl, nil) || !equalMutationACL(nil, nil) {
		t.Fatal("null and empty DACLs compared as equivalent")
	}
}

func mutationTestWindowsUser(t *testing.T) string {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	must(t, err)
	return user.User.Sid.String()
}

func windowsPermissionsAt(t *testing.T, path string) *mutationPermissions {
	t.Helper()
	file, err := os.Open(path)
	must(t, err)
	defer file.Close()
	permissions, err := readMutationPermissions(file, nil)
	must(t, err)
	return permissions
}

func createMutationTestWindowsFile(t *testing.T, path, descriptor string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(descriptor)
	must(t, err)
	name, err := windows.UTF16PtrFromString(path)
	must(t, err)
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	handle, err := windows.CreateFile(name, windows.GENERIC_WRITE, mutationShare, &sa, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	must(t, err)
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	_, err = file.WriteString("before")
	must(t, err)
}

func setMutationTestWindowsDACL(t *testing.T, path, descriptor string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(descriptor)
	must(t, err)
	acl, _, err := sd.DACL()
	must(t, err)
	control, _, err := sd.Control()
	must(t, err)
	protection := windows.SECURITY_INFORMATION(windows.UNPROTECTED_DACL_SECURITY_INFORMATION)
	if control&windows.SE_DACL_PROTECTED != 0 {
		protection = windows.PROTECTED_DACL_SECURITY_INFORMATION
	}
	must(t, windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|protection, nil, nil, acl, nil))
}
