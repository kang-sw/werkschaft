package wsfs

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

const darwinMutationStageFile = "replacement"

func lockMutationDestination(file *os.File) error {
	// Competing replacements see the same inode until one publishes.
	// Keep the lock on this handle through the final check and rename.
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	runtime.KeepAlive(file)
	if err != nil {
		return fmt.Errorf("lock replacement destination: %w", err)
	}
	return nil
}

func mutationStagingLocation(parent *os.File, replacing bool) (*os.File, string, func(), error) {
	if !replacing {
		return siblingMutationStagingLocation(parent)
	}
	_, stageName, _, err := siblingMutationStagingLocation(parent)
	if err != nil {
		return nil, "", nil, err
	}
	// On macOS a child can inherit an ACL despite 0600. Create a private
	// directory exclusively and clear its ACL before creating the file.
	// A retry uses a fresh directory. A name left by a previous process
	// supplies no proof of ownership, so it is never reclaimed.
	err = unix.Mkdirat(int(parent.Fd()), stageName, 0o700)
	runtime.KeepAlive(parent)
	if err != nil {
		return nil, "", nil, fmt.Errorf("create replacement staging directory: %w", err)
	}
	stage, err := openMutationDir(parent, stageName, false)
	if err != nil {
		removeMutationStagingDir(parent, stageName)
		return nil, "", nil, fmt.Errorf("open replacement staging directory: %w", err)
	}
	cleanup := func() {
		removeMutationStagingDir(parent, stageName)
		_ = stage.Close()
	}
	if err := prepareMutationStagingDir(stage); err != nil {
		cleanup()
		return nil, "", nil, fmt.Errorf("prepare replacement staging directory: %w", err)
	}
	return stage, darwinMutationStageFile, cleanup, nil
}

func prepareMutationStagingDir(stage *os.File) error {
	info, err := stage.Stat()
	if err != nil {
		return err
	}
	if int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() || info.Mode()&mutationModeBits != 0o700 {
		return fmt.Errorf("unexpected owner or mode")
	}
	acl, err := readMutationACL(stage)
	if err != nil || len(acl) == 0 {
		return err
	}
	if err := setMutationACL(stage, nil); err != nil {
		return fmt.Errorf("remove inherited access ACL: %w", err)
	}
	acl, err = readMutationACL(stage)
	if err != nil {
		return err
	}
	if len(acl) != 0 {
		return fmt.Errorf("inherited access ACL remains")
	}
	return nil
}

func removeMutationStagingDir(parent *os.File, name string) {
	_ = unix.Unlinkat(int(parent.Fd()), name, unix.AT_REMOVEDIR)
	runtime.KeepAlive(parent)
}
