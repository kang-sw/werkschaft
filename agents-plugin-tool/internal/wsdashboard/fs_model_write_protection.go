package wsdashboard

import (
	"errors"
	"path/filepath"
	"strings"
)

var errModelSessionState = errors.New("daemon session state is read-only to model file tools")

// NewModelFileWriteProtection adds the daemon-owned inbox, queue, and replay
// state to the credential guard for write calls. Read calls keep their own
// credential-only guard.
func NewModelFileWriteProtection() *modelFileWriteProtection {
	p := &modelFileWriteProtection{ProviderCredentialProtection: NewProviderCredentialProtection()}
	if dir, err := Dir(""); err == nil {
		sessions := filepath.Clean(SessionDir(dir, ""))
		p.sessionPath = &denySet{loaded: true, lower: []string{strings.ToLower(sessions)}}
		p.deny.bases = append(p.deny.bases, sessions)
		p.deny.lower = append(p.deny.lower, p.sessionPath.lower...)
	}
	return p
}

type modelFileWriteProtection struct {
	*ProviderCredentialProtection
	sessionPath *denySet
}

func (p *modelFileWriteProtection) CheckPath(path string) error {
	if p.sessionPath != nil && p.sessionPath.containsBasePath(strings.ToLower(filepath.Clean(path))) {
		return errModelSessionState
	}
	return p.ProviderCredentialProtection.CheckPath(path)
}
