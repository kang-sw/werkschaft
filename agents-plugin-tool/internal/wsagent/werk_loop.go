package wsagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsinbox"
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
	agent  *werk.Agent
}

func (b WerkBackend) startLoop(ctx context.Context, spec StartSpec) (Process, error) {
	if err := validateWerkStart(spec); err != nil {
		return nil, err
	}
	endpoint, err := b.loopResponderFor(spec.Provider)
	if err != nil {
		return nil, err
	}
	spec.modelContextWindow = func(model string) int64 { return b.providerContextWindow(spec.Provider, model) }
	return startWerkLoop(ctx, spec, endpoint)
}

func (b WerkBackend) loopResponder() (*werk.Client, error) { return b.loopResponderFor("") }

func (b WerkBackend) loopResponderFor(id string) (*werk.Client, error) {
	if b.credentials == nil {
		return nil, errors.New("werk backend: the provider credential source is unavailable")
	}
	provider, err := werkProvider(id)
	if err != nil {
		return nil, err
	}
	return &werk.Client{
		BaseURL:  provider.BaseURL,
		Provider: provider.ID,
		HTTP:     b.httpClient,
		Token: func(ctx context.Context) (string, error) {
			cred, err := b.credentials.Credential(ctx, wscred.Request{
				Provider: provider.ID, AgentSession: true, Background: !werk.InteractiveTurn(ctx),
			})
			if err != nil {
				return "", err
			}
			return cred.Secret.Reveal(), nil
		},
	}, nil
}

func validateWerkStart(spec StartSpec) error {
	if strings.TrimSpace(spec.Model) == "" {
		return errors.New("werk backend: a model is required")
	}
	switch spec.PermissionLevel {
	case PermissionReadOnly, PermissionWorkdirWrite, PermissionFull:
	default:
		return errors.New("werk backend: a permission level is required")
	}
	if s := spec.MCPSession; s == nil || s.URL == "" || s.Token == "" {
		return errors.New("werk backend: an authenticated MCP session is required")
	}
	return nil
}

func startWerkLoop(ctx context.Context, spec StartSpec, endpoint werk.Responder) (*werkLoop, error) {
	if err := validateWerkStart(spec); err != nil {
		return nil, err
	}
	var instructions []byte
	if spec.Instructions != "" {
		var err error
		instructions, err = os.ReadFile(spec.Instructions)
		if err != nil {
			return nil, fmt.Errorf("read the werk instruction file: %w", err)
		}
	}
	session := spec.MCPSession
	ctx, cancel := context.WithCancel(ctx)
	p := &werkLoop{
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		cmds: make(chan werk.Command, 32), events: make(chan Event, 256),
		mapper: newWerkMapper(spec.SessionID),
	}
	if spec.Lead != nil {
		provider, err := werkProvider(spec.Provider)
		if err != nil {
			cancel()
			return nil, err
		}
		p.mapper.provider = string(provider.ID)
	}
	cfg := werk.Config{
		Dir: spec.Dir, Root: spec.Root, Cwd: spec.workDir(),
		Authority: werk.Authority(spec.PermissionLevel), Session: spec.SessionID,
		Lead:  spec.Lead != nil,
		Model: spec.Model, Effort: spec.Effort, Instructions: string(instructions),
		Endpoint: endpoint, RecoverExec: spec.recoverExec,
		WS:             &werk.MCP{URL: session.URL, Token: session.Token, Version: werk.ProtocolVersion},
		SummarySecrets: []string{spec.sessionSecret, session.Token},
		Emit:           p.emit,
	}
	if spec.compactState != nil {
		cfg.CompactionState = func(since time.Time) werk.CompactionState { return werk.CompactionState(spec.compactState(since)) }
	}
	if cfg.Lead {
		cfg.RereadFiles = func(paths []string, maxBytes int64) map[string]string {
			return wsdashboard.ReadWorktreeTextFiles(spec.Root, paths, maxBytes)
		}
	}
	cfg.ContextWindow = spec.modelContextWindow
	a, err := werk.New(cfg)
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
	p.agent = a
	go p.run(a)
	return p, nil
}

func (p *werkLoop) recordFileCall(ctx context.Context, call wsfs.FileCall) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	return p.agent.RecordFileCall(ctx, call)
}

type execLaunchRecorder interface {
	recordExecLaunch(context.Context, execjob.ModelLaunch) error
}

func (p *werkLoop) recordExecLaunch(ctx context.Context, call execjob.ModelLaunch) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	return p.agent.RecordExecLaunch(ctx, call)
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
	if err := a.ServeCommands(p.ctx, p.cmds); err != nil && p.ctx.Err() == nil {
		p.deliver(Event{Notice: err.Error()})
	}
}

func (p *werkLoop) emit(line werk.Line) {
	if p.audit != nil {
		if line.T == werk.LineCheckpoint {
			id := wsaudit.SourceID{Source: "checkpoint-" + p.agent.Session(), Seq: line.Checkpoint}
			ev := p.audit.adapter.(*werkAudit).checkpoint(line)
			if err := p.audit.commitCheckpoint(p.ctx, id, ev); err != nil {
				p.deliver(Event{Notice: "save the committed compaction audit cut: " + err.Error()})
				p.cancel()
				return
			}
		} else if raw, err := json.Marshal(line); err == nil {
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

func (p *werkLoop) submitTurn(id, text string, steers []durableSteer, interactive bool) error {
	cmd := werk.Command{Type: werk.CmdUser, TurnID: id, Text: text, Interactive: interactive}
	for _, steer := range steers {
		cmd.Steers = append(cmd.Steers, werk.Steer{ID: steer.ID, Text: steer.Text})
	}
	return p.command(cmd)
}

func (p *werkLoop) submitSteer(turnID string, steer durableSteer) error {
	return p.command(werk.Command{Type: werk.CmdSteer, TurnID: turnID, ID: steer.ID, Text: steer.Text})
}

func (p *werkLoop) Send(text string) error {
	return p.command(werk.Command{Type: werk.CmdUser, Text: text})
}

type leadSummaryCompactor interface {
	compactWithSummary(*wscompact.LeadSummary) error
}

func (p *werkLoop) compactWithSummary(summary *wscompact.LeadSummary) error {
	return p.command(werk.Command{Type: werk.CmdCompact, LeadSummary: summary})
}

func (p *werkLoop) RunCommand(cmd wsinbox.Command) error {
	if cmd.Name != "compact" {
		return ErrUnsupported
	}
	return p.compactWithSummary(nil)
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
	return p.command(werk.Command{Type: werk.CmdSet, Effort: &effort})
}

func (p *werkLoop) Close() error {
	p.cancel()
	<-p.done
	return nil
}

func (p *werkLoop) processControls() processControls {
	return processControls{Process: p, steer: p, reports: p, commands: p}
}
