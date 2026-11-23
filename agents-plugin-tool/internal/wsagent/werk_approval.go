package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsredact"
)

const (
	werkModeReadOnly       = "read-only"
	werkModeWorkspaceWrite = "workspace-write"
	werkModeFullAccess     = "full-access"
)

func werkPermissionPolicy() PermissionPolicy {
	return PermissionPolicy{
		NativeModes: map[PermissionLevel]string{
			PermissionReadOnly:     werkModeReadOnly,
			PermissionWorkdirWrite: werkModeWorkspaceWrite,
			PermissionFull:         werkModeFullAccess,
		},
		LegacyModes: map[string]PermissionLevel{
			werkModeReadOnly:       PermissionReadOnly,
			werkModeWorkspaceWrite: PermissionWorkdirWrite,
			werkModeFullAccess:     PermissionFull,
		},
		LegacyDefault: PermissionWorkdirWrite,
	}
}

func (p *werkLoop) SetPermissionMode(mode string) error {
	level := werkPermissionPolicy().LegacyModes[mode]
	if !p.lead || level == "" {
		return ErrUnsupported
	}
	p.agent.SetAuthority(werk.Authority(level))
	return nil
}

func (p *werkLoop) activePermissionMode() string {
	return werkPermissionPolicy().nativeMode(PermissionLevel(p.agent.Authority()))
}

type werkApproval struct {
	id     string
	answer chan error
}

func (p *werkLoop) requestApproval(ctx context.Context, tool string, input json.RawMessage) error {
	waiting := &werkApproval{id: werk.NewSessionID(), answer: make(chan error, 1)}
	p.approvalMu.Lock()
	if p.pendingApproval != nil {
		p.approvalMu.Unlock()
		return errors.New("another Werk approval is waiting")
	}
	p.pendingApproval = waiting
	p.approvalMu.Unlock()
	defer func() {
		p.approvalMu.Lock()
		p.pendingApproval = nil
		p.approvalMu.Unlock()
		p.deliver(Event{ApprovalDone: waiting.id})
	}()

	p.deliver(Event{Approval: &ApprovalRequest{ID: waiting.id, Tool: tool, Input: p.approvalInput(input), StartedAt: time.Now().UnixMilli()}})
	select {
	case err := <-waiting.answer:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *werkLoop) approvalInput(raw json.RawMessage) json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	delete(fields, "session_key")
	encoded, _ := json.Marshal(fields)
	text := string(encoded)
	for _, secret := range p.secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, wsredact.Mask)
		}
	}
	text = wsredact.Secrets(text)
	if !json.Valid([]byte(text)) {
		return nil
	}
	return json.RawMessage(text)
}

func (p *werkLoop) AnswerApproval(req ApprovalRequest, allow bool, message string) error {
	p.approvalMu.Lock()
	defer p.approvalMu.Unlock()
	waiting := p.pendingApproval
	if waiting == nil || waiting.id != req.ID {
		// The turn can be cancelled before the runner sees ApprovalDone.
		return nil
	}
	var err error
	if !allow {
		err = errors.New("the user denied " + req.Tool)
		if message != "" {
			err = fmt.Errorf("%w: %s", err, message)
		}
	}
	select {
	case waiting.answer <- err:
	default:
	}
	return nil
}
