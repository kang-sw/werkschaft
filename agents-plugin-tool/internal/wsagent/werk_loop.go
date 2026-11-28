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
	"sync"
	"time"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/werk"
	"github.com/kang-sw/devenv/internal/wsaudit"
	"github.com/kang-sw/devenv/internal/wscompact"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsfs"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// werkLoop runs a session in the daemon and delivers typed events to the
// conversation owner. Only the runner sends commands and consumes events.
type werkLoop struct {
	ctx             context.Context
	cancel          context.CancelFunc
	done            chan struct{}
	cmds            chan werk.Command
	events          chan Event
	mapper          *werkMapper
	lead            bool
	ephemeral       bool
	approvalMu      sync.Mutex
	pendingApproval *werkApproval
	secrets         []string
	audit           *auditCapture
	steer           uint64
	agent           *werk.Agent
	provider        string
	onUsage         func(wsdashboard.WerkUsageDelta)
	accountMu       sync.Mutex
	turnAccount     string
}

func (p *werkLoop) setTurnAccount(id string) {
	p.accountMu.Lock()
	p.turnAccount = id
	p.accountMu.Unlock()
}

func (p *werkLoop) currentTurnAccount() string {
	p.accountMu.Lock()
	defer p.accountMu.Unlock()
	return p.turnAccount
}

type openAIAccountPinKey struct{}

func withOpenAIAccountPin(ctx context.Context, clientID string) context.Context {
	if clientID == "" {
		return ctx
	}
	return context.WithValue(ctx, openAIAccountPinKey{}, clientID)
}

func openAIAccountPin(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(openAIAccountPinKey{}).(string)
	return id
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
	var activeAccount func() string
	if spec.Provider == string(wscred.ProviderOpenAI) {
		if source, ok := b.credentials.(interface{ ActiveOpenAIAccount() string }); ok {
			activeAccount = source.ActiveOpenAIAccount
		}
	}
	return startWerkLoopWithHooks(ctx, spec, endpoint, activeAccount, b.onUsage)
}

func (b WerkBackend) loopResponder() (*werk.Client, error) { return b.loopResponderFor("") }

func (b WerkBackend) loopResponderFor(id string) (*werk.Client, error) {
	if b.credentials == nil {
		return nil, errors.New("werk backend: the provider credential source is unavailable")
	}
	provider, err := b.werkProvider(id)
	if err != nil {
		return nil, err
	}
	client := &werk.Client{
		BaseURL:  provider.BaseURL,
		Provider: provider.ID,
		Dialect:  provider.Dialect,
		HTTP:     b.httpClient,
		Token: func(ctx context.Context) (string, error) {
			return b.providerToken(ctx, provider, !werk.InteractiveTurn(ctx))
		},
	}
	if provider.ID == wscred.ProviderOpenAI {
		if source, ok := b.credentials.(interface {
			BeginOpenAIPlanRequestFor(context.Context, string, bool) (wscred.OpenAIPlanRequest, error)
		}); ok {
			client.PlanToken = func(ctx context.Context) (wscred.OpenAIPlanRequest, error) {
				return source.BeginOpenAIPlanRequestFor(ctx, openAIAccountPin(ctx), !werk.InteractiveTurn(ctx))
			}
		} else if source, ok := b.credentials.(interface {
			BeginOpenAIPlanRequest(context.Context, bool) (wscred.OpenAIPlanRequest, error)
		}); ok {
			client.PlanToken = func(ctx context.Context) (wscred.OpenAIPlanRequest, error) {
				return source.BeginOpenAIPlanRequest(ctx, !werk.InteractiveTurn(ctx))
			}
		}
	}
	return client, nil
}

func (b WerkBackend) providerToken(ctx context.Context, provider wscred.Provider, background bool) (string, error) {
	request := wscred.Request{Provider: provider.ID, AgentSession: true, Background: background}
	if provider.ID == wscred.ProviderOpenAI {
		request.OpenAIAccount = openAIAccountPin(ctx)
	}
	if provider.Dialect != "" {
		request.BaseURL = provider.BaseURL
	}
	cred, err := b.credentials.Credential(ctx, request)
	if provider.Dialect != "" && errors.Is(err, wscred.ErrNotConfigured) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return cred.Secret.Reveal(), nil
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
	if !spec.Ephemeral {
		if s := spec.MCPSession; s == nil || s.URL == "" || s.Token == "" {
			return errors.New("werk backend: an authenticated MCP session is required")
		}
	}
	return nil
}

func startWerkLoop(ctx context.Context, spec StartSpec, endpoint werk.Responder) (*werkLoop, error) {
	return startWerkLoopWithHooks(ctx, spec, endpoint, nil, nil)
}

func startWerkLoopWithTurnHook(ctx context.Context, spec StartSpec, endpoint werk.Responder, wrapTurn func(context.Context) context.Context) (*werkLoop, error) {
	return startWerkLoopConfigured(ctx, spec, endpoint, wrapTurn, nil, nil)
}

func startWerkLoopWithHooks(ctx context.Context, spec StartSpec, endpoint werk.Responder, activeAccount func() string, onUsage func(wsdashboard.WerkUsageDelta)) (*werkLoop, error) {
	return startWerkLoopConfigured(ctx, spec, endpoint, nil, activeAccount, onUsage)
}

func startWerkLoopConfigured(ctx context.Context, spec StartSpec, endpoint werk.Responder, wrapTurn func(context.Context) context.Context, activeAccount func() string, onUsage func(wsdashboard.WerkUsageDelta)) (*werkLoop, error) {
	if err := validateWerkStart(spec); err != nil {
		return nil, err
	}
	var instructions string
	if spec.Instructions != "" {
		raw, err := os.ReadFile(spec.Instructions)
		if err != nil {
			return nil, fmt.Errorf("read the werk instruction file: %w", err)
		}
		instructions = string(raw)
	}
	if sp := strings.TrimSpace(spec.SystemPrompt); sp != "" {
		if strings.TrimSpace(instructions) != "" {
			instructions = strings.TrimRight(instructions, "\n") + "\n\n" + sp
		} else {
			instructions = sp
		}
	}
	var secrets []string
	if spec.sessionSecret != "" {
		secrets = append(secrets, spec.sessionSecret)
	}
	if !spec.Ephemeral && spec.MCPSession != nil && spec.MCPSession.Token != "" {
		secrets = append(secrets, spec.MCPSession.Token)
	}
	ctx, cancel := context.WithCancel(ctx)
	providerName := spec.Provider
	if providerName == "" {
		providerName = string(wscred.ProviderOpenRouter)
	}
	p := &werkLoop{
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
		cmds: make(chan werk.Command, 32), events: make(chan Event, 256),
		mapper: newWerkMapper(spec.SessionID), lead: !spec.Ephemeral && spec.Lead != nil,
		ephemeral: spec.Ephemeral,
		secrets:   secrets,
		provider:  providerName,
		onUsage:   onUsage,
	}
	if activeAccount != nil {
		prevWrap := wrapTurn
		wrapTurn = func(tctx context.Context) context.Context {
			if prevWrap != nil {
				tctx = prevWrap(tctx)
			}
			id := activeAccount()
			p.setTurnAccount(id)
			return withOpenAIAccountPin(tctx, id)
		}
	}
	p.mapper.restoreUsage(spec.restoredUsage)
	if !spec.Ephemeral && spec.Lead != nil {
		p.mapper.provider = spec.Provider
		if p.mapper.provider == "" {
			p.mapper.provider = string(wscred.ProviderOpenRouter)
		}
	}
	cfg := werk.Config{
		Ephemeral:    spec.Ephemeral,
		Root:         spec.Root,
		Cwd:          spec.workDir(),
		Authority:    werk.Authority(spec.PermissionLevel),
		Session:      spec.SessionID,
		Model:        spec.Model,
		Effort:       spec.Effort,
		Instructions: instructions,
		Endpoint:     endpoint,
		SummarySecrets:  p.secrets,
		Emit:            p.emit,
		WrapTurnContext: wrapTurn,
	}
	if !spec.Ephemeral {
		cfg.Dir = spec.Dir
		cfg.Lead = spec.Lead != nil
		cfg.ClosedTurnID = spec.closedTurnID
		cfg.Skills = discoverWerkSkills(spec)
		cfg.RecoverExec = spec.recoverExec
		cfg.WS = &werk.MCP{URL: spec.MCPSession.URL, Token: spec.MCPSession.Token, Version: werk.ProtocolVersion}
		if spec.compactState != nil {
			cfg.CompactionState = func(since time.Time) werk.CompactionState { return werk.CompactionState(spec.compactState(since)) }
		}
		if cfg.Lead {
			cfg.Approve = p.requestApproval
			cfg.RereadFiles = func(paths []string, maxBytes int64) map[string]string {
				return wsdashboard.ReadWorktreeTextFiles(spec.Root, paths, maxBytes)
			}
		}
	}
	cfg.ContextWindow = spec.modelContextWindow
	if !spec.Ephemeral && spec.sessionConfig != nil {
		cfg.WebSearch = resolveWerkWebSearchPolicy(spec.sessionConfig)
	} else {
		// Ephemeral sessions and private fixed-profile runners never attach
		// provider server-side web search.
		cfg.WebSearch = func() werk.WebSearchPolicy { return werk.WebSearchPolicy{Enabled: false} }
	}
	if spec.Provider != "" {
		cfg.Provider = wscred.ProviderID(spec.Provider)
		if wsconfig.IsLocalWerkProvider(spec.Provider) {
			cfg.Dialect = "local"
		}
	} else if _, isClient := endpoint.(*werk.Client); !isClient && spec.sessionConfig != nil {
		cfg.Provider = wscred.ProviderOpenRouter
	}
	a, err := werk.New(cfg)
	if err != nil {
		cancel()
		return nil, err
	}
	if !spec.Ephemeral && spec.audit != nil {
		harness, adapter := p.mapper.newAudit(spec.audit.Session.ID)
		// Capture sequences are local to this loop. Give each opening a
		// distinct source even when it resumes an existing conversation.
		p.audit = newAuditCapture(spec.audit, harness, werk.NewSessionID(), adapter, false)
	}
	p.agent = a
	go p.run(a)
	return p, nil
}

func resolveWerkWebSearchPolicy(sessionConfig func() (wsconfig.Options, wsconfig.SessionReader, string)) func() werk.WebSearchPolicy {
	return func() werk.WebSearchPolicy {
		cfg, reader, key := sessionConfig()
		resolver := wsconfig.NewResolver(cfg, wsconfig.WerkWebSearchDefaults(), reader, nil)
		resolved := wsconfig.ResolveWerkWebSearch(&resolver, key)
		return werk.WebSearchPolicy{
			Enabled:    resolved.Enabled,
			MaxUses:    resolved.MaxUses,
			MaxResults: resolved.MaxResults,
			Engine:     resolved.Engine,
			SIWC:       resolved.SIWC,
		}
	}
}

func (p *werkLoop) approveFileCall(ctx context.Context, call wsfs.FileCall) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	return p.agent.ApproveFileCall(ctx, call)
}

func (p *werkLoop) recordFileCall(ctx context.Context, call wsfs.FileCall) error {
	if err := p.ctx.Err(); err != nil {
		return err
	}
	return p.agent.RecordFileCall(ctx, call)
}

type execLaunchRecorder interface {
	recordExecLaunch(context.Context, execjob.ModelLaunch) (bool, error)
}

func (p *werkLoop) recordExecLaunch(ctx context.Context, call execjob.ModelLaunch) (bool, error) {
	if err := p.ctx.Err(); err != nil {
		return false, err
	}
	if call.Approval != nil {
		preview := call.Approval
		input, _, err := execApprovalInput(ExecApproval{
			Tool: preview.Tool, Command: preview.Command, Cwd: preview.Cwd, Program: preview.Program,
			Env: preview.Env, Stdin: preview.Stdin, StdinBytes: preview.StdinBytes,
		})
		if err != nil {
			return false, err
		}
		return p.agent.ApproveExecLaunch(ctx, call, input)
	}
	return false, p.agent.RecordExecLaunch(ctx, call)
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
	if line.T == werk.LineUsage && p.onUsage != nil && line.Model != "" {
		var cacheWrite int64
		if line.CacheWriteTokens != nil {
			cacheWrite = *line.CacheWriteTokens
		}
		var cost float64
		if line.CostUSD != nil {
			cost = *line.CostUSD
		}
		p.onUsage(wsdashboard.WerkUsageDelta{
			Provider:         p.provider,
			AccountKey:       p.currentTurnAccount(),
			Model:            line.Model,
			InputTokens:      line.InputTokens,
			OutputTokens:     line.OutputTokens,
			CachedTokens:     line.CachedTokens,
			CacheWriteTokens: cacheWrite,
			ReasoningTokens:  line.ReasoningTokens,
			CostUSD:          cost,
			At:               lineTime(line),
		})
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

func (p *werkLoop) Reset(ctx context.Context) error {
	if !p.ephemeral {
		return ErrUnsupported
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	ack := make(chan error, 1)
	if err := p.command(werk.Command{Type: werk.CmdReset, Ack: ack}); err != nil {
		return err
	}
	select {
	case err := <-ack:
		return err
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return io.ErrClosedPipe
	}
}

func (p *werkLoop) Close() error {
	p.cancel()
	<-p.done
	return nil
}

func (p *werkLoop) processControls() processControls {
	ctl := processControls{Process: p, steer: p, reports: p, commands: p}
	if p.lead {
		ctl.approval, ctl.mode = p, p
	}
	if p.ephemeral {
		ctl.reset = p
	}
	return ctl
}
