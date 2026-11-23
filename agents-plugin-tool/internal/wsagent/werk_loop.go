package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"

	"github.com/kang-sw/devenv/internal/werk"
)

// werkLoop runs a session in the daemon and delivers typed events to the
// conversation owner. Only the runner sends commands and consumes events.
type werkLoop struct {
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	cmds   chan werk.Command
	events chan Event
	mapper *werkMapper
	audit  *auditCapture
	steer  uint64
}

func startWerkLoop(ctx context.Context, spec StartSpec, endpoint werk.Responder) (*werkLoop, error) {
	if spec.Lead != nil {
		return nil, errWerkLead
	}
	if strings.TrimSpace(spec.Model) == "" {
		return nil, errors.New("werk backend: a tier model is required")
	}
	session := spec.MCPSession
	if session == nil || session.URL == "" || session.Token == "" {
		return nil, errors.New("werk backend: an authenticated MCP session is required")
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &werkLoop{
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		cmds: make(chan werk.Command, 32), events: make(chan Event, 256),
		mapper: newWerkMapper(spec.SessionID),
	}
	a, err := werk.New(werk.Config{
		Dir: spec.Dir, Root: spec.Root, Cwd: spec.workDir(),
		Authority: werk.Authority(spec.PermissionLevel), Session: spec.SessionID,
		Model: spec.Model, Effort: spec.Effort, Instructions: spec.Instructions,
		MCPFilesOnly: true,
		Endpoint:     endpoint,
		WS:           &werk.MCP{URL: session.URL, Token: session.Token, Version: werk.ProtocolVersion},
		Emit:         p.emit,
	})
	if err != nil {
		cancel()
		return nil, err
	}
	if spec.audit != nil {
		harness, adapter := p.mapper.newAudit(spec.audit.Session.ID)
		// Capture sequences are local to this loop. Give each opening a
		// distinct source even when it resumes an existing conversation.
		p.audit = newAuditCapture(spec.audit, harness, werk.NewSessionID(), adapter)
	}
	go p.run(a)
	return p, nil
}

func (p *werkLoop) run(a *werk.Agent) {
	defer close(p.done)
	defer close(p.events)
	defer p.cancel()
	defer func() {
		if err := a.Close(); err != nil {
			p.deliver(Event{Notice: "the conversation could not be closed: " + err.Error()})
		}
	}()
	if p.audit != nil {
		defer func() {
			// Finish capture before releasing the log's writer lease,
			// so a new opening cannot race the previous audit source.
			p.audit.close()
			<-p.audit.done
		}()
	}
	defer func() {
		if recover() != nil {
			p.deliver(Event{TurnEnd: true, TurnError: "the werk session failed unexpectedly"})
		}
	}()
	_ = a.ServeCommands(p.ctx, p.cmds)
}

func (p *werkLoop) emit(line werk.Line) {
	if p.audit != nil {
		if raw, err := json.Marshal(line); err == nil {
			p.audit.line(raw, 0, false)
		}
	}
	for _, event := range p.mapper.mapEvent(line) {
		p.deliver(event)
	}
}

func (p *werkLoop) deliver(event Event) {
	select {
	case p.events <- event:
	case <-p.ctx.Done():
	}
}

func (p *werkLoop) Events() <-chan Event { return p.events }

func (p *werkLoop) command(cmd werk.Command) error {
	if p.ctx.Err() != nil {
		return io.ErrClosedPipe
	}
	// The caller also drains Events. It cannot wait for the loop to consume
	// a command while the loop is waiting to publish an event.
	select {
	case p.cmds <- cmd:
		return nil
	case <-p.ctx.Done():
		return io.ErrClosedPipe
	default:
		return errors.New("werk backend: the session command queue is full")
	}
}

func (p *werkLoop) Send(text string) error {
	return p.command(werk.Command{Type: werk.CmdUser, Text: text})
}

func (p *werkLoop) Interrupt() error {
	return p.command(werk.Command{Type: werk.CmdInterrupt})
}

func (p *werkLoop) Steer(text string) error {
	p.steer++
	id := "wsc/steer/" + strconv.FormatUint(p.steer, 10)
	return p.command(werk.Command{Type: werk.CmdSteer, ID: id, Text: text})
}

func (p *werkLoop) ClearSteered() error {
	return p.command(werk.Command{Type: werk.CmdClearSteered})
}

func (p *werkLoop) SetModel(model string) error {
	if model == "" {
		return ErrUnsupported
	}
	return p.command(werk.Command{Type: werk.CmdSet, Model: model})
}

func (p *werkLoop) SetEffort(effort string) error {
	if effort == "" {
		return ErrUnsupported
	}
	return p.command(werk.Command{Type: werk.CmdSet, Effort: &effort})
}

func (p *werkLoop) Close() error {
	p.cancel()
	<-p.done
	return nil
}

func (p *werkLoop) processControls() processControls {
	return processControls{Process: p, steer: p, reports: p}
}
