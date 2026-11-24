package wsfs

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"syscall"

	"golang.org/x/sys/unix"
)

const darwinMutationStageFile = "replacement"

func darwinMutationStageName(name string) string {
	sum := sha256.Sum256([]byte(name))
	return ".ws-stage-" + hex.EncodeToString(sum[:])
}

func mutationStagingLocation(parent *os.File, name string, replacing bool) (*os.File, string, func(), error) {
	if !replacing {
		return siblingMutationStagingLocation(parent)
	}
	// A macOS ACL can grant access despite 0600. Strip inheritance on a
	// private directory before creating the file, even if the file is empty.
	stageName := darwinMutationStageName(name)
	err := unix.Mkdirat(int(parent.Fd()), stageName, 0o700)
	runtime.KeepAlive(parent)
	created := err == nil
	if !created && !errors.Is(err, unix.EEXIST) {
		return nil, "", nil, fmt.Errorf("create replacement staging directory: %w", err)
	}
	stage, err := openMutationDir(parent, stageName, false)
	if err != nil {
		if created {
			removeMutationStagingDir(parent, stageName)
		}
		return nil, "", nil, fmt.Errorf("open replacement staging directory: %w", err)
	}
	if err := lockMutationStagingDir(stage); err != nil {
		_ = stage.Close()
		return nil, "", nil, err
	}
	cleanup := func() {
		removeMutationStagingDir(parent, stageName)
		_ = stage.Close()
	}
	if err := prepareMutationStagingDir(stage, created); err != nil {
		if created {
			cleanup()
		} else {
			_ = stage.Close()
		}
		return nil, "", nil, fmt.Errorf("prepare replacement staging directory: %w", err)
	}
	return stage, darwinMutationStageFile, cleanup, nil
}

func lockMutationStagingDir(stage *os.File) error {
	// Holding a directory lock prevents reclaiming another process's stage.
	if err := unix.Flock(int(stage.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("lock replacement staging directory: %w", err)
	}
	return nil
}

func recoverMutationStage(parent *os.File, name string) error {
	// An identical-content retry can follow a crash just after publication.
	stageName := darwinMutationStageName(name)
	stage, err := openMutationDir(parent, stageName, false)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open replacement staging directory: %w", err)
	}
	defer stage.Close()
	if err := lockMutationStagingDir(stage); err != nil {
		return err
	}
	if err := prepareMutationStagingDir(stage, false); err != nil {
		return fmt.Errorf("recover replacement staging directory: %w", err)
	}
	removeMutationStagingDir(parent, stageName)
	return nil
}

func prepareMutationStagingDir(stage *os.File, created bool) error {
	info, err := stage.Stat()
	if err != nil {
		return err
	}
	if int(info.Sys().(*syscall.Stat_t).Uid) != os.Geteuid() || info.Mode()&mutationModeBits != 0o700 {
		return fmt.Errorf("unexpected owner or mode")
	}
	acl, err := readMutationACL(stage)
	if err != nil {
		return err
	}
	if !created {
		entries, err := stage.ReadDir(2)
		if err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		// An empty stage can have inherited an ACL if the process stopped
		// between mkdirat and ACL removal. A stage with data must be private.
		if len(entries) != 0 {
			if len(entries) != 1 || len(acl) != 0 || entries[0].Name() != darwinMutationStageFile || !entries[0].Type().IsRegular() {
				return fmt.Errorf("unexpected entries or access ACL in an existing stage")
			}
			if err := unix.Unlinkat(int(stage.Fd()), darwinMutationStageFile, 0); err != nil {
				return fmt.Errorf("remove interrupted replacement: %w", err)
			}
		}
	}
	if len(acl) == 0 {
		return nil
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
