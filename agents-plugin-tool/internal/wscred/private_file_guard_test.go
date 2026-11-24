package wscred_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestProviderCredentialProtectionDuringReplacement(t *testing.T) {
	t.Setenv(wsstate.EnvCacheHome, t.TempDir())
	current, err := wscred.DefaultPath("")
	if err != nil {
		t.Fatal(err)
	}
	_, retiring, _ := wscred.GenerationPaths(current)
	store := wscred.NewStore(current)
	setKey := func(key string) error {
		_, err := store.Apply(wscred.Change{Op: wscred.OpSet, Provider: wscred.ProviderOpenRouter, APIKey: wscred.NewSecret(key)})
		return err
	}
	if err := setKey("synthetic-provider-fixture-1"); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	oldAlias, newAlias := filepath.Join(root, "old.txt"), filepath.Join(root, "new.txt")
	if err := os.Link(current, oldAlias); err != nil {
		t.Fatal(err)
	}
	paused, release := make(chan struct{}), make(chan struct{})
	wscred.AfterCredentialPublishForTest(store, func() {
		close(paused)
		<-release
	})
	done := make(chan error, 1)
	go func() { done <- setKey("synthetic-provider-fixture-2") }()
	select {
	case <-paused:
	case err := <-done:
		t.Fatalf("replacement exited before retirement: %v", err)
	}
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Errorf("replacement: %v", err)
		}
	}()
	if err := os.Link(current, newAlias); err != nil {
		t.Fatal(err)
	}
	files := &wsfs.Files{
		Root: root, Authority: wsfs.AuthorityFull,
		Protection: func() wsfs.FileProtection { return wsdashboard.NewProviderCredentialProtection() },
	}
	for _, path := range []string{current, retiring, oldAlias, newAlias} {
		p := wsdashboard.NewProviderCredentialProtection()
		if (path == current || path == retiring) && !p.DeniesName(path) {
			t.Errorf("store path %s allowed by name", filepath.Base(path))
		}
		file, err := wscred.OpenCredentialFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := file.Stat()
		_ = file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if err := p.CheckOpened(info); err == nil {
			t.Errorf("read from %s allowed by identity", filepath.Base(path))
		}
		raw, err := json.Marshal(map[string]string{"path": path, "content": "replacement"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := files.Write(context.Background(), raw); err == nil || !strings.Contains(err.Error(), "credential store is protected") {
			t.Errorf("write to %s = %v", filepath.Base(path), err)
		}
	}
}
