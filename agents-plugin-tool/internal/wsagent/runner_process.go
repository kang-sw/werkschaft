package wsagent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsinbox"
	"github.com/kang-sw/devenv/internal/wslive"
)

// runCommand sends a slash command as its own turn: through the process's
// own mapping when it has one (commandRunner), else as the bare typed line,
// which a harness that reads a message starting with a slash as a command
// runs as one.
func (c *conv) runCommand(cmd wsinbox.Command) error {
	p := c.r.opts.Backends[c.rec.Backend].CompactionProtocol()
	isCompact := cmd.Name == "compact" && p.DetectsLifecycle()
	if isCompact && !c.compacting {
		if c.compactOrigin == "" {
			c.compactOrigin = wslive.CompactOriginManual
		}
		c.compactEvent(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart})
		if p.CommandEndConfirms {
			c.apply(wslive.Event{Kind: wslive.KindCompact, Phase: wslive.CompactStart, Trigger: wslive.TriggerManual})
		}
	}
	err := controlsFor(c.proc).runCommand(cmd)
	if err == nil && isCompact {
		c.armCompactWatchdog()
	}
	return err
}

func (c *conv) setPromptBoundary(rec *Record, startedAt int64, reportable bool) {
	rec.TurnOpen = reportable
	rec.TurnStartedAt = startedAt
	rec.TurnStartSeq = max(c.seq, c.lineOpen)
}

// sendStandalonePrompt persists the boundary for prompts sent outside the
// inbox drain. Internal prompts wait for ACP without opening a parent result.
// The boundary is recorded only once the holder accepted the prompt: one
// recorded before it would leave a successor waiting for a response to a
// prompt that never reached the harness. send runs only when the binding
// survives the last release check (admitInput); otherwise nothing runs and
// the error is an inputReleasedError, which is no send failure.
func (c *conv) sendStandalonePrompt(reportable bool, send func() error) error {
	// The prompt is a keep-awake member while its send is in flight. A
	// successful send's caller opens the turn (beginTurn, or compactCommand
	// for a native compaction) before the loop step ends, so membership
	// carries over; a failed one is dropped by that step's sync.
	c.awakeSending = true
	c.syncAwake()
	defer func() { c.awakeSending = false }()
	heldAgy := c.r.heldInternalWorker(c.record())
	startedAt := c.r.opts.Now().UnixMilli()
	var err error
	if checkout := c.admitInput(func() { err = send() }); checkout != "" {
		return &inputReleasedError{checkout: checkout}
	}
	if err != nil || !heldAgy {
		return err
	}
	c.update(func(rec *Record) { c.setPromptBoundary(rec, startedAt, reportable) })
	return nil
}

// startProcess resolves the profile again (it may have changed since the
// conversation opened) and starts the backend process, resuming the
// recorded backend session.
func (c *conv) startProcess() error {
	if c.hidden {
		return conflict("the session is archived; unhide it before starting")
	}
	rec := c.record()
	if c.r.durableProcess(rec.Backend) {
		release, err := c.r.admitDurableTransition()
		if err != nil {
			return err
		}
		defer release()
	}
	// A launch, wake or resume of a spawned conversation in a checkout
	// released after it was bound is refused: the release ended that binding.
	// A start is a cold path, so the check reads the checkouts' markers, not
	// memory alone: a release whose notice never reached this daemon still
	// refuses it. The check and the starting mark share one mu hold with the
	// release notice and the occupancy read: a notice either lands first
	// (this start is refused) or finds the start live.
	// A checkout removed since the conversation was loaded (its release
	// marker can go with its admin directory) refuses the start the same
	// way: nothing launches into a deleted or unregistered directory. Its
	// file checks run before mu is taken.
	gone := goneBinding(rec, c.canonRoot, c.canonCwd, c.checkouts)
	c.mu.Lock()
	checkout := c.releasedOnDiskLocked()
	if checkout == "" && gone != "" {
		c.releasedCheckout, checkout = gone, gone
	}
	if checkout == "" {
		c.starting = true
	}
	c.mu.Unlock()
	if checkout != "" {
		return deadCheckoutRefusal(checkout)
	}
	defer c.setStatus(func() { c.starting = false })
	// Before the profile is read: a removed or edited profile must not mask
	// a cross-harness refusal (admitWorkerRecord).
	if err := c.r.admitWorkerRecord(rec); err != nil {
		return err
	}
	rp, err := c.r.resolve(rec.Profile, rec.Root)
	if err != nil {
		return err
	}
	if rp.Definition.Backend != rec.Backend {
		return fmt.Errorf("agent profile %q now selects backend %q; this conversation runs on %q", rec.Profile, rp.Definition.Backend, rec.Backend)
	}
	if rp.Definition.Lead != rec.Lead {
		return fmt.Errorf("agent profile %q changed its lead setting; refusing to start", rec.Profile)
	}
	if c.r.durableProcess(rec.Backend) && rec.SessionID == "" {
		session := newReportID()
		if err := c.commitDurableRecord(func(rec *Record) { rec.SessionID = session }, nil); err != nil {
			return fmt.Errorf("save the backend session identity: %w", err)
		}
		rec.SessionID = session
	}
	if rec.Lead {
		return c.startLead(rec, rp.Definition, false)
	}
	return c.startWorker(rec, rp.Definition, false)
}

func (c *conv) startWorker(rec Record, def Profile, attach bool) error {
	durable := c.r.durableProcess(rec.Backend)
	if durable && attach {
		return errors.New("a durable process cannot attach to a holder")
	}
	releaseAdmission := func() {}
	if !attach {
		releaseAdmission = c.r.holdParentAdmission(rec.Parent)
	}
	defer releaseAdmission()
	c.mcpStale = false
	c.clearWorkerRejoin()
	// A cross-harness rejoin attaches only to match ownership and end the
	// holder: no compaction, agy watchdog, or permission config is read.
	refused := attach && c.crossHarnessRejoin
	if !refused {
		c.loadCompaction()
		c.loadModelWait()
	}
	level, source := PermissionLevel(""), ""
	if refused {
		// Keeps the level startConv left (none for a refused record).
		level, source = c.workerPermissionLevel, c.workerPermissionSource
	} else if attach {
		var err error
		level, source, err = c.r.workerPermission(rec, def, c.dir)
		if err != nil {
			// A daemon replacement observes an existing held process instead of
			// orphaning it solely because its selection cannot be read.
			fmt.Fprintf(c.r.opts.Log, "agent %s: rejoin worker permission selection (advisory): %v\n", rec.ReplyID, err)
			level, source = "", ""
		}
	} else {
		// Every launch re-admits the worker's current explicit or legacy-derived
		// authority against its parent's current level (agent_tools.go).
		var err error
		if level, source, err = c.r.childAdmission(rec, def); err != nil {
			return err
		}
	}
	c.setStatus(func() {
		c.workerPermissionLevel = level
		c.workerPermissionSource = source
	})
	if !attach {
		// Emit only after reading the stored authority used for admission.
		c.permissionNudge(level, true, "")
	}
	if c.r.opts.Holders != nil && !attach {
		if !c.r.scanDone() {
			return errors.New("holder scan is not complete")
		}
		if err := c.retireHolder(); err != nil {
			return err
		}
	}
	instructions := ""
	if !attach {
		if err := c.r.opts.Keys.Ensure(rec.Root, rec.WSKey); err != nil {
			return fmt.Errorf("ws session key: %w", err)
		}
		if rec.SystemPrompt && c.systemPrompt == "" {
			// Read once per daemon: the text stays in memory for every
			// later start (a new segment re-injects it from there).
			raw, err := os.ReadFile(filepath.Join(c.dir, systemPromptFile))
			if err != nil {
				return fmt.Errorf("agent.spawn system prompt: %w", err)
			}
			c.systemPrompt = string(raw)
		}
		var err error
		instructions, err = composeInstructions(c.dir, rec, def, c.systemPrompt, c.r.subagentSteer(rec))
		if err != nil {
			return err
		}
	}
	spec := StartSpec{
		Root:             rec.Root,
		Dir:              c.dir,
		Profile:          def,
		Model:            rec.Model,
		Provider:         rec.Provider,
		Effort:           rec.Effort,
		AccountConfigDir: c.r.resolveAccountSlotConfigDir(rec.Backend, rec.AccountSlotID),
		SessionID:        rec.SessionID,
		Cwd:          rec.Cwd,
		Instructions: instructions,
		// The child's authority, which a backend that sandboxes its worker
		// maps itself (codexWorkerMode).
		PermissionLevel: level,
		// A Claude worker reports its session state and background tasks,
		// so a task it still runs keeps it from idling (idleNow).
		SessionState: c.r.lifecycle(rec.Backend).WorkerSessionState && isAgentWorkerProfile(rec.Profile),
		// An agent.spawn child delegates through agent.spawn only.
		RemoveSubagents: RemovesSubagents(rec),
	}
	if !durable {
		spec.Env = environ()
	}
	if !refused {
		// Claude compacts by itself mid-turn at or before the hard point
		// (claudeAutoCompactEnv); a rejoin's process keeps its launch env.
		spec.compactAdvisory, spec.compactHard = c.compactAdvisory, c.compactHard
	}
	if attach {
		// Harness-initiated wakeups also set TurnOpen. Only a runner send
		// saves the start time alongside it.
		if rec.TurnStartedAt != 0 && (rec.TurnOpen || c.r.internalPrompt(rec)) {
			startSeq := rec.TurnStartSeq
			spec.reattachedPromptAfter = &startSeq
		}
		for _, steer := range rec.Steered {
			spec.RecoveredSteers = append(spec.RecoveredSteers, steer.Text)
		}
	}
	if attach && len(def.WSTools) > 0 {
		// The mapper needs the original ws tool surface, not a new bridge.
		spec.WSMCP = &MCPCommand{}
	} else if len(def.WSTools) > 0 {
		cmd, err := c.r.mcpCommand(durable)(rec.Root)
		if err != nil {
			return fmt.Errorf("ws-mcp command: %w", err)
		}
		c.update(func(rec *Record) { rec.PluginWarning = cmd.PluginWarning })
		if cmd.PluginWarning != "" {
			fmt.Fprintf(c.r.opts.Log, "agent %s: %s\n", rec.ReplyID, cmd.PluginWarning)
		}
		env := map[string]string{}
		for k, v := range cmd.Env {
			env[k] = v
		}
		env[EnvAllowedTools] = strings.Join(def.WSTools, ",")
		env[EnvNoDashboard] = "1"
		env[EnvPermissionLevel] = string(level)
		env[EnvAgentSessionKey] = rec.WSKey
		// The daemon may inherit a lead's mailbox identity; the agent's
		// ws-mcp must never claim that inbox's presence. Blank is inert.
		for _, name := range mailboxIdentityEnv {
			env[name] = ""
		}
		// A spawned conversation that may spawn gets this start's
		// capability token (children.go); any other gets both blank, so
		// none is inherited.
		env[EnvPiToken], env[EnvPiEndpoint] = "", ""
		if mayHoldToken(rec, def) {
			if !attach {
				token, endpoint := c.r.issueToken(rec.ReplyID)
				env[EnvPiToken], env[EnvPiEndpoint] = token, endpoint
				if c.r.opts.Holders != nil {
					c.update(func(rec *Record) { rec.Capability = token })
				}
				rec.Capability = token
			}
		}
		cmd.Env = env
		spec.WSMCP = &cmd
		if durable || c.r.opts.Backends[rec.Backend].WorkerHTTPMCP {
			// Only an external process persists the HTTP tuple with its holder.
			spec.MCPSession = c.r.attachMCP(rec.ReplyID, "", env, rec.workDir(), 0, rec.Backend)
		}
	}
	if durable && spec.MCPSession == nil {
		c.r.revokeToken(rec.ReplyID)
		return errors.New("a durable process requires an authenticated MCP session")
	}
	if !durable && c.r.opts.Holders != nil {
		session := holderSession{Kind: holderKindAgentWorker, Backend: rec.Backend, ReplyID: rec.ReplyID, Cwd: rec.workDir(), Label: rec.Label, Capability: capabilityHash(rec.Capability)}
		if rec.Parent != nil {
			session.ParentReplyID = rec.Parent.ReplyID
		}
		spec.Holder = &holderChild{host: c.r.opts.Holders, table: &c.r.holders, session: session}
		if attach {
			spec.Holder.id, spec.Holder.skipTo = rec.HolderID, rec.HolderSeq
			spec.Holder.attachReady = make(chan struct{})
		}
	}
	if attach && c.r.opts.Backends[rec.Backend].WorkerHTTPMCP {
		// Let the backend reconstruct its filtered harness environment, but
		// do not register a bearer before checkReattach validates ownership.
		spec.MCPSession = &MCPSession{Started: func(env []string, _ int) { c.workerMCPEnv = env }}
	}
	if attach {
		// The token is restored after ownership validation
		// (restoreWorkerMCP), which marks it again.
		spec.ExecCutover = c.rejoinExecCutover(rec)
	} else {
		var mcpEnv map[string]string
		if spec.WSMCP != nil {
			mcpEnv = spec.WSMCP.Env
		}
		c.decideExecCutover(rec, def, &spec, mcpEnv)
	}
	proc, err := c.startBackend(spec)
	if err != nil {
		c.clearWorkerRejoin()
		c.r.revokeToken(rec.ReplyID)
		return err
	}
	c.setStatus(func() { c.proc = proc })
	c.events = proc.Events()
	if durable {
		c.bindDurableMCP(spec.MCPSession, rec, proc, level)
		spec.MCPSession.Started(nil, 0)
	}
	if !attach {
		c.steerSeq, c.steerAnswers = 0, 0
	}
	c.idleAfter = def.IdleTimeout()
	if hp, ok := proc.(holderProcess); ok {
		id, pid, held := hp.HolderInfo()
		if held {
			if attach {
				c.replaying, c.seq = true, rec.HolderSeq
				// Hold runner events until ownership and downgrade reconciliation
				// finish; Scan may already be delivering buffered output.
				c.events = nil
				return nil
			}
			c.seq, c.seqDirty, c.live.ringDirty = 0, false, false
			c.update(func(rec *Record) {
				rec.HolderID, rec.HostPID, rec.HolderSeq = id, pid, 0
				rec.ExecCutoverHolder = cutoverHolder(spec.ExecCutover, id)
				rec.SteerSeq, rec.SteerAnswers, rec.SteerAnswerSeq = 0, 0, 0
				rec.HandedOffAt = c.r.opts.Now().UTC().Format(time.RFC3339)
				rec.forgetMCP()
				if s := spec.MCPSession; s != nil {
					rec.MCPToken, rec.MCPURL, rec.MCPEnv, rec.MCPHolder = s.Token, s.URL, spec.WSMCP.Env, id
				}
			})
		}
	}
	c.dirtyModel, c.dirtyEffort, c.dirtyPermission = false, false, false
	wantListed := def.ListInIndex && rec.CompanionRole == ""
	if wantListed != rec.Listed {
		c.update(func(rec *Record) { rec.Listed = wantListed })
		c.writeRegistration()
	}
	if rec.SessionID != "" || rec.SessionFile != "" {
		c.queryRecoveryUsage()
	}
	return nil
}
