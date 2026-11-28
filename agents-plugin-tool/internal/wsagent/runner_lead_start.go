package wsagent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
)

// Starting a hosted lead conversation: startLead and retiring the holder it replaces.

// startLead starts a lead conversation's process under a new session
// holder, or with attach rejoins the one the record names (a daemon
// restart). The harness loads the user's own setup; what makes it this
// session is the hosted environment: the flag, the lead key, and the path
// of the registration row whose host_pid names the one process the flag
// holds for. The row says 0 until the process exists, so a harness that
// reads it early waits rather than misreading an older PID.
func (c *conv) startLead(rec Record, def Profile, attach bool) error {
	life := c.r.lifecycle(rec.Backend)
	durable := c.r.durableProcess(rec.Backend)
	if durable && attach {
		return errors.New("a durable lead cannot attach to a holder")
	}
	c.loadCompaction()
	c.loadModelWait()
	// Each agy process writes its own worker log: locate it afresh.
	c.agyLog = nil
	if life.WorkerLog {
		c.agyLog = c.newAgyLog()
	}
	level, source, err := c.r.leadPermission(rec, c.dir)
	if err != nil {
		if !attach {
			return err
		}
		// Admission checks still block new launches, but must not orphan an
		// existing held process. Rejoin observes it without applying a selection.
		fmt.Fprintf(c.r.opts.Log, "agent %s: rejoin permission selection (advisory): %v\n", rec.ReplyID, err)
	}
	if attach {
		// Root lead keys remain parentless under the existing key-store contract.
		err := c.ensureLeadKey(rec)
		c.setStatus(func() { c.rejoinKeyPending = err != nil })
		if err != nil {
			diagnostic := "rejoin ws session key (advisory): " + err.Error()
			fmt.Fprintf(c.r.opts.Log, "agent %s: %s\n", rec.ReplyID, diagnostic)
		}
	}
	var holder *holderChild
	if !durable {
		holder = &holderChild{host: c.r.opts.Holders, table: &c.r.holders, session: holderSession{Kind: holderKindLead, Backend: rec.Backend, ReplyID: rec.ReplyID, Cwd: rec.workDir(), Label: rec.Profile}}
	}
	instructions := ""
	if attach {
		holder.id, holder.skipTo = rec.HolderID, rec.HolderSeq
	} else {
		// One process per session: whatever holder the record still names
		// (a stop that failed, a crash) ends before --resume runs again.
		if err := c.retireHolder(); err != nil {
			return err
		}
		if err := c.ensureLeadKey(rec); err != nil {
			return fmt.Errorf("ws session key: %w", err)
		}
		c.setStatus(func() { c.rejoinKeyPending = false })
		if life.ComposedLeadInstructions {
			// Written on every launch, --resume included, so each process
			// carries the key and the manual in its prompt. Pi leads get
			// them from their extension, or from its bridge fallback
			// (writePiLeadBridge, LeadSpec.Manual).
			if rec.CompanionRole != "" && rec.CompanionRole != companionRoleWork {
				if instructions, err = composeCompanionInstructions(c.dir, rec); err != nil {
					return err
				}
			} else if instructions, err = composeLeadInstructions(c.dir, rec, c.leadManual(rec), c.r.subagentSteer(rec), c.r.opts.Backends[rec.Backend].PlaybookCommands); err != nil {
				return err
			}
		}
		// One step with the check that the session is still the daemon's:
		// the row written here names no process and is not stopped, so a
		// terminal login is refused from now until the process it gets has
		// ended. No row written, no process.
		c.spawning = true
		if !c.writeRegistration() {
			c.spawning = false
			if c.isForeign() {
				return errHandedOff
			}
			return errors.New("the session's registration could not be written, so no process was started")
		}
	}
	// spawned: the backend recorded its process before Start returned (LeadSpec.Spawned).
	var spawned bool
	// cutover is this start's exec cutover, settled below before the start.
	var cutover bool
	leadSubagent := c.r.leadSubagentPolicy(rec)
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
		SessionFile:  rec.SessionFile,
		Instructions: instructions,
		Holder:       holder,
		// A hosted lead delegates through agent.spawn only. A rejoin builds
		// no command line; the replayed init's shape shows whether the
		// running process was started without the tool.
		RemoveSubagents: RemovesSubagents(rec),
		Lead: &LeadSpec{
			DaemonVersion:        c.r.opts.DaemonVersion,
			PermissionMode:       rec.launchPermissionMode(),
			PluginIsolation:      leadPluginIsolation(rec.PluginIsolation),
			PreferNativeSubagent: leadSubagent.Active && leadSubagent.PreferNativeSubagent,
			PluginWarning: func(warning string) {
				c.update(func(rec *Record) { rec.PluginWarning = warning })
				if warning != "" {
					fmt.Fprintf(c.r.opts.Log, "agent %s: %s\n", rec.ReplyID, warning)
					// A usable package fallback is a persisted warning, not a
					// process/recovery error. Real launch errors use lastErr.
					c.changed(false)
				}
			},
			WSMCP: func() (MCPCommand, error) {
				mcp := c.r.mcpCommand(durable)
				if mcp == nil {
					return MCPCommand{}, errors.New("bundled ws-mcp launcher unavailable")
				}
				cmd, err := mcp(rec.workDir())
				if err != nil {
					return MCPCommand{}, err
				}
				env := map[string]string{}
				for name, value := range cmd.Env {
					env[name] = value
				}
				env[EnvAllowedTools], env[EnvPiToken], env[EnvPiEndpoint] = "", "", ""
				for _, name := range mailboxIdentityEnv {
					env[name] = ""
				}
				env[EnvHostedSession], env[EnvHostedSessionKey] = "1", rec.WSKey
				env[EnvHostedSessionRow] = wsdashboard.RegistrationPath(c.r.opts.Dir, rec.ReplyID)
				cmd.Env = env
				return cmd, nil
			},
			Env: map[string]string{
				EnvHostedSession:    "1",
				EnvHostedSessionKey: rec.WSKey,
				EnvHostedSessionRow: wsdashboard.RegistrationPath(c.r.opts.Dir, rec.ReplyID),
			},
			Manual: func() string {
				if rec.CompanionRole == companionRoleWork {
					return combineLeadManualAndAddendum(c.leadManual(rec), rec.CompanionSystemPrompt)
				}
				if rec.CompanionRole != "" {
					return rec.CompanionSystemPrompt
				}
				return c.leadManual(rec)
			},
			FrameTrace: c.agyFrameTrace,
			Spawned: func(holderID string, pid int) {
				spawned = true
				c.update(func(rec *Record) {
					rec.HolderID, rec.HostPID, rec.HolderSeq = holderID, pid, 0
					rec.ExecCutoverHolder = cutoverHolder(cutover, holderID)
				})
				c.writeRegistration()
			},
		},
	}
	if !durable {
		spec.Env = environ()
	}
	c.mcpStale = false
	dashboardControlEffective := rec.DashboardControlAccess
	if attach {
		dashboardControlEffective = rec.MCPDashboardControl
	}
	var mcpEnv map[string]string
	switch {
	case durable:
		cmd, err := spec.Lead.WSMCP()
		if err != nil {
			c.spawning = false
			c.writeRegistration()
			return fmt.Errorf("load the durable lead's MCP environment: %w", err)
		}
		mcpEnv = cmd.Env
		spec.Lead.Env, spec.Lead.Spawned, spec.Lead.WSMCP = nil, nil, nil
		spec.MCPSession = c.r.attachMCP(rec.ReplyID, "", mcpEnv, rec.workDir(), 0, rec.Backend)
		if spec.MCPSession == nil {
			c.spawning = false
			c.writeRegistration()
			return errors.New("a durable lead requires an authenticated MCP session")
		}
	case attach && rec.MCPToken != "" && rec.MCPHolder != "" && rec.MCPHolder != rec.HolderID:
		// The process was started again since its token was drawn, by a
		// daemon that keeps the record's members without knowing them: it
		// loaded the bundle's stdio server, and the token is no one's.
		c.update(func(rec *Record) { rec.forgetMCP() })
	case attach && rec.MCPToken != "" && (!c.r.servesMCPURL(rec.MCPURL) || rec.MCPEnv == nil):
		// The process sends its token to a route this daemon does not
		// serve (it listens on another port), or its hello is unknown:
		// its ws-mcp is gone until it restarts, at its first idle point
		// (restartIfDue).
		fmt.Fprintf(c.r.opts.Log, "agent %s: the process's ws-mcp route %s is not this daemon's; it restarts at its first idle point\n", rec.ReplyID, rec.MCPURL)
		c.mcpStale = true
	case attach && rec.MCPToken == "" && life.LeadHTTPMCP && !life.LeadStdioFallback:
		// A start the last daemon did not finish: Spawned named the
		// process before its token was recorded. These HTTP-only backends
		// have no stdio fallback; restart at a known-safe idle point,
		// as for a stale route.
		fmt.Fprintf(c.r.opts.Log, "agent %s: the %s process's start did not finish; it restarts at its first known idle point\n", rec.ReplyID, rec.Backend)
		c.mcpStale = true
	case attach && rec.MCPToken != "":
		// The recorded token and hello, as the process was started with.
		spec.MCPSession = c.r.attachMCP(rec.ReplyID, rec.MCPToken, rec.MCPEnv, rec.workDir(), rec.HostPID, rec.Backend)
	case life.LeadHTTPMCP && !attach:
		// The lead's ws-mcp goes over the daemon's HTTP route with a new
		// token. Without a route a Claude lead runs the same server over
		// stdio (its --mcp-config entry); an agy lead has no stdio
		// fallback. Neither loads a plugin, so a start without any ws-mcp
		// is refused by its backend.
		cmd, err := spec.Lead.WSMCP()
		if err != nil {
			fmt.Fprintf(c.r.opts.Log, "agent %s: ws-mcp unavailable, the start is refused: %v\n", rec.ReplyID, err)
			spec.Lead.WSMCPErr = err
			break
		}
		if life.LeadStdioFallback {
			spec.WSMCP, spec.Lead.PluginNote = &cmd, cmd.PluginWarning
		}
		if spec.MCPSession = c.r.attachMCP(rec.ReplyID, "", cmd.Env, rec.workDir(), 0, rec.Backend); spec.MCPSession != nil {
			mcpEnv = cmd.Env
		}
	}
	if spec.MCPSession != nil {
		c.r.markMCPDashboardControl(rec.ReplyID, dashboardControlEffective)
	}
	var convHistoryEffective bool
	if attach {
		spec.ExecCutover = c.rejoinExecCutover(rec)
		if durable {
			// Durable leads always mint a fresh in-daemon MCP session (never persist MCPToken).
			convHistoryEffective = rec.Lead && rec.ConvHistoryAccess
		} else {
			convHistoryEffective = rec.Lead && rec.MCPToken != "" && rec.MCPHolder == rec.HolderID && rec.MCPConvHistoryAccess
		}
	} else {
		c.decideExecCutover(rec, def, &spec, mcpEnv)
		convHistoryEffective = rec.Lead && rec.ConvHistoryAccess
	}
	c.setStatus(func() { c.convHistoryAccessEffective = convHistoryEffective })
	c.r.markMCPConvHistoryAccess(rec.ReplyID, convHistoryEffective)
	cutover = spec.ExecCutover
	if durable {
		spec.PermissionLevel = level
	}
	if source == "explicit" {
		spec.PermissionLevel = level
		if mode := c.r.permissionPolicy(rec.Backend).nativeMode(level); mode != "" {
			spec.Lead.PermissionMode = mode
		}
	}
	if mode := c.cutoverLaunchMode(); mode != "" {
		// The cutover's native mode wins over the saved one, which stays on
		// record for a start outside the cutover.
		spec.Lead.PermissionMode = mode
	}
	if !attach {
		c.permissionNudge(level, false, "")
	}
	proc, err := c.startBackend(spec)
	if c.spawning {
		c.spawning = false
		// The spawn settled: its row, written below with the process on
		// success, says stopped again when there is none.
		defer func() {
			if c.proc == nil {
				c.writeRegistration()
			}
		}()
	}
	if err != nil {
		c.r.revokeToken(rec.ReplyID)
		if spawned {
			// The process the row named early is gone (the backend closed
			// it); the deferred write says stopped.
			c.update(func(rec *Record) { rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0 })
		}
		return err
	}
	hp, ok := proc.(holderProcess)
	id, pid, held := "", 0, false
	if ok {
		id, pid, held = hp.HolderInfo()
	}
	if !held && !durable {
		_ = proc.Close()
		c.r.revokeToken(rec.ReplyID)
		return fmt.Errorf("the %s backend did not start the process under a session holder", rec.Backend)
	}
	// A process-scoped observation cannot carry into a new process or holder
	// attach. Replay/open reports repopulate it; silence stays unknown.
	activeMode := ""
	if live, ok := proc.(liveLeadMode); ok {
		activeMode = live.activePermissionMode()
	}
	c.setStatus(func() { c.proc, c.activeMode, c.dashboardControlEffective = proc, activeMode, dashboardControlEffective })
	c.events = proc.Events()
	if durable {
		c.bindDurableMCP(spec.MCPSession, rec, proc, level)
		// See startWorker: the daemon's filtered environment is the hosted loop's.
		spec.MCPSession.Started(durableEnv(environ()), 0)
	}
	c.idleAfter = def.IdleTimeout()
	if attach {
		c.replaying, c.seq = true, rec.HolderSeq
		return nil
	}
	// A pending restore already carries state. Queue startup state for the
	// first model turn after a launch, including when the harness resumes.
	if (rec.CompanionRole == "" || rec.CompanionRole == companionRoleWork) && c.r.opts.LeadSessionState != nil &&
		!strings.HasPrefix(c.compactNote, restartRestoreMarker) &&
		!strings.HasPrefix(c.compactNote, compactionRestoreMarker) &&
		!strings.HasPrefix(c.compactNote, leadStartStateMarker) {
		c.compactNote = leadStartStateMarker + c.compactNote
	}
	c.dirtyModel, c.dirtyEffort, c.dirtyMode, c.dirtyPermission, c.reconcileMode = false, false, false, false, false
	c.seq, c.seqDirty, c.live.ringDirty = 0, false, false
	c.update(func(rec *Record) {
		rec.HolderID, rec.HostPID, rec.HolderSeq = id, pid, 0
		rec.ExecCutoverHolder = cutoverHolder(cutover, id)
		rec.forgetMCP()
		if s := spec.MCPSession; s != nil && !durable {
			rec.MCPToken, rec.MCPURL, rec.MCPEnv, rec.MCPHolder, rec.MCPConvHistoryAccess, rec.MCPDashboardControl = s.Token, s.URL, mcpEnv, id, convHistoryEffective, dashboardControlEffective
		}
	})
	c.writeRegistration()
	if rec.SessionID != "" || rec.SessionFile != "" {
		c.queryRecoveryUsage()
	}
	return nil
}

// retireHolder ends the holder the record names, if it still exists, and
// forgets it; an error means its process may still run, and no new one
// starts.
func (c *conv) retireHolder() error {
	id := c.rec.HolderID
	if id == "" {
		return nil
	}
	for _, info := range c.r.opts.Holders.List().Holders {
		if info.ID != id {
			continue
		}
		if err := c.r.opts.Holders.Stop(id); err != nil && !errors.Is(err, wsholder.ErrUnknownHolder) {
			return fmt.Errorf("the session's previous process could not be ended (holder %s): %w", id, err)
		}
	}
	c.update(func(rec *Record) {
		rec.HolderID, rec.HostPID, rec.HolderSeq = "", 0, 0
		if !rec.Lead {
			rec.HandedOffAt = ""
		}
	})
	c.writeRegistration()
	return nil
}

func (c *conv) ensureLeadKey(rec Record) error {
	if rec.CompanionRole != "" {
		if cks, ok := c.r.opts.LeadKeys.(CompanionLeadKeyStore); ok {
			return cks.EnsureCompanionLead(rec.Root, rec.WSKey, rec.CompanionRole)
		}
	}
	return c.r.opts.LeadKeys.EnsureLead(rec.Root, rec.WSKey)
}
