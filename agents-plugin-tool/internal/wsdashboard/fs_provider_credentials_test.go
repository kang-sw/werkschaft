package wsdashboard

import (
	"errors"
	"os"
	"testing"

	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsstate"
)

func TestProviderCredentialProtectionMissingName(t *testing.T) {
	t.Setenv(wsstate.EnvCacheHome, t.TempDir())
	path, err := wscred.DefaultPath("")
	if err != nil {
		t.Fatal(err)
	}
	guard := NewProviderCredentialProtection()
	if info, err := guard.deny.stat(path); info != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat of missing credential = %v, %v", info, err)
	}
	if err := guard.checkIdentityError(); err != nil {
		t.Fatalf("missing name invalidated the guard: %v", err)
	}
	if !guard.DeniesName(path) {
		t.Fatal("missing credential name was not protected")
	}
}
