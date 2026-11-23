package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync/atomic"
	"syscall"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/mcp"
	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsholder"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wstrackerhook"
	"github.com/kang-sw/devenv/internal/wstrackerhost"
	"github.com/kang-sw/devenv/internal/wswake"
)

// dashboardCommand runs the machine-wide dashboard daemon in the foreground.
// The launcher's `daemon-start` (and a direct `ws-mcp serve`) starts it
// detached through wsdashboard.Ensure; running it by hand is for diagnosis
// and, with WS_MCP_DASHBOARD_DEV_SOURCE set, for editing the page assets
// live.
func dashboardCommand(args []string) {
	fs := flag.NewFlagSet("dashboard", flag.ExitOnError)
	port := fs.Int("port", wsdashboard.DefaultPort, "preferred loopback port; 0 lets the OS choose, and a taken port falls back to an OS-chosen one")
	lan := fs.Bool("lan", false, "listen on every network interface and admit a request from another device only with the LAN access token; the starter (daemon-start, or ws-mcp serve) passes it when the global dashboard.lan_enabled value is on")
	buildTime := fs.Int64("build-time", 0, "the ws-mcp build time in Unix milliseconds to report in /health; the starter passes the time of the executable it measured, and 0 reads this executable's")
	lockWait := fs.Duration("lock-wait", wsdashboard.DefaultLockWait, "how long to wait for another daemon to release the single-instance lock")
	preflight := fs.Bool("desktop-preflight", false, "report pre-lock readiness and wait for the starter's admission")
	_ = fs.Parse(args)

	// Before anything reads the environment or spawns from it: the session
	// that started this daemon is not one it hosts.
	mcp.DropHarnessSessionEnv()
	dir, err := wsdashboard.Dir("")
	if err != nil {
		fatal("dashboard", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fatal("dashboard", err)
	}
	exe, _ := os.Executable()
	if *buildTime <= 0 && exe != "" {
		*buildTime = wsdashboard.ExecutableBuildTime(exe)
	}
	// The content hash is this executable's own: the staged copy a launcher
	// made has the plugin binary's content, so the launcher's and the
	// starter's hashes of their binaries compare against it.
	contentHash := ""
	if exe != "" {
		contentHash = wsdashboard.ExecutableContentHash(exe)
	}
	configPath, _ := wsconfig.GlobalPath(wsconfig.Options{})
	logw := openDashboardLog(dir)
	// Dev assets switch on before the daemon serves its first request. The
	// notice waits for StartDaemon to acquire the singleton lock.
	devRoot, err := wsdashboard.EnableDevAssets(os.Getenv(wsdashboard.DevSourceEnv))
	if err != nil {
		fatal("dashboard", err)
	}
	// One tracker host per daemon, shared by the poll and the tracker
	// routes; a host that cannot be created still goes in, so the page
	// shows why.
	tracker, trackerErr := wstrackerhost.New(wstrackerhost.Options{})
	closeTracker := func() {
		if tracker != nil {
			_ = tracker.Close()
		}
	}
	boards := mcp.NewWhiteboardStores(logw)
	sessions := mcp.NewDashboardSessionSource(boards)
	// The daemon serves the mailbox from memory: every store call opens (or
	// reuses) the cache root's persistent instance, holding its restore
	// ring for the daemon's life. A daemon that loses the dashboard lock
	// makes no store call, so it never takes the ring.
	wsmailbox.EnablePersistent()
	// StartDaemon starts the runner before it returns the daemon, so the
	// runner's daemon callbacks are bound once the daemon exists.
	var trackerChanged lateFunc
	var agyActivity lateFunc
	// Bound once the mailbox sweep runs; Close calls it before releasing
	// the dashboard lock.
	var exportMailbox lateFunc
	// The holders a predecessor left running are found in the state
	// directory; the interpreter for a new one is the launcher's own. The
	// runner takes the manager first: it rejoins its lead sessions'
	// processes before the manager's first scan replays their records.
	// The hosted exec jobs bind after StartDaemon (their store opens under
	// the lock); their holders' records and exits go through execLink.
	execLink := &hostedExecLink{}
	cron := &cronHost{}
	holders := newDashboardHolders(dir, logw, execLink)
	// Mail sender labels read the runner and the daemon once each exists.
	labels := &mailLabels{}
	agents := newAgentRunner(dir, logw, trackerChanged.call, agyActivity.call, holders, boards, labels.label)
	// The hosted exec jobs bind after the runner starts (openHostedExec):
	// until then no held report is released and no session sleeps.
	agents.ExpectExecJobs()
	labels.agents.Store(agents)
	nudges := mcp.NewWhiteboardNudges(dir, agents, logw, boards)
	// The one provider credential store: the daemon's token route and its
	// change route serve it, and hosted `credential` commands apply to it.
	var credentials *wscred.Store
	if credPath, err := wscred.DefaultPath(""); err != nil {
		fmt.Fprintf(logw, "dashboard: credential store unavailable: %v\n", err)
	} else {
		credentials = wscred.NewStore(credPath)
	}
	userCmdsReady := make(chan struct{})
	host := &proxyHost{userCmdsReady: userCmdsReady, credentials: credentials, notes: sessions.Notes, dir: dir, port: *port, lan: *lan, buildTime: *buildTime, contentHash: contentHash, logw: logw, configPath: filepath.Clean(configPath), agents: agents, boards: boards, mailLabels: labels.label}
	boards.SetChanged(func(root, key string) {
		if d := host.live.Load(); d != nil {
			d.Index().Reread(wsmailbox.ReplyID(key))
		}
		nudges.Poke(filepath.Dir(dir), root, key)
	})
	// A store's support conversation runs in the store's clone, so an
	// unregister ends it: right after the dashboard's own, and on the store
	// watch's change for one another process ran (tk:unregister).
	support := wsagent.NewSupport(wsagent.SupportOptions{
		Runner:    agents,
		Dir:       dir,
		StoreRoot: trackerStoreRoot(tracker, trackerErr),
	})
	packageName, _, packageErr := wsagent.ResolveHostedPackage()
	var settings *wsdashboard.ConfigBackend
	if packageErr == nil {
		settings, packageErr = mcp.DashboardConfigBackend(packageName, wsconfig.Options{}, sessions, agents.RefreshAmbient, agents.HarnessAvailability)
	}
	if packageErr != nil {
		fmt.Fprintln(logw, "dashboard: settings catalog unavailable:", packageErr)
	}
	if *preflight && !wsdashboard.AwaitDesktopPreflight(os.Stdin, os.Stdout) {
		closeTracker()
		return
	}
	daemon, err := wsdashboard.StartDaemon(wsdashboard.DaemonOptions{
		Dir:          dir,
		Port:         *port,
		LAN:          *lan,
		Version:      version,
		BuildTime:    *buildTime,
		ContentHash:  contentHash,
		Executable:   exe,
		LockWait:     *lockWait,
		Log:          logw,
		LogUnderLock: logw.begin,
		Proxy:        host,
		Pprof:        wsdashboard.PprofEnabled(os.Getenv(wsdashboard.PprofEnv)),
		// User-curated, so beside the config file rather than in the cache.
		WorkspacesFile: wsdashboard.WorkspacesFilePath(configPath),
		Sessions:       sessions,
		Config:         settings,
		Worktrees:      sessions,
		MailWake:       wswake.Mailbox,
		AgyCommand:     wsagent.AgyBackend{}.ResolveCommand,
		// How each registered harness differs on the dashboard's surfaces,
		// from its registration.
		Harnesses:      agents.DashboardHarnesses(),
		Agents:         agents,
		Credentials:    credentials,
		LeadNudges:     nudges,
		Tracker:        &dashboardTracker{h: tracker, err: trackerErr, unregistered: func() { support.CloseUnregistered() }},
		Holders:        holders,
		Support:        support,
		ScratchCapture: os.Getenv("WS_MCP_DASHBOARD_SCRATCH_CAPTURE") == "1",
		// The servers this daemon hosts for launcher sessions run their exec
		// jobs in this process, so its own job set is what the rail counts.
		ExecJobs: execjob.ActiveRoots,
		// A hosted session's jobs run under the hosted exec jobs, which
		// know their owning conversation; nil until they bind.
		HostedExecJobs: func() map[string]int { return host.exec.Load().RunningOwners() },
		// A session that left the index may have ended for good: its
		// scheduled prompts go once the runner no longer knows it.
		SessionForgotten: func(id string) { cron.sessionForgotten(agents, id) },
		// The companion director's isolated one-shot call, dispatched
		// through the harness's backend registration.
		CompanionOneShot:    wsagent.RunOneShot,
		CompanionHasOneShot: wsagent.HasOneShot,
		BeforeUnlock: func() {
			if err := boards.Close(); err != nil {
				fmt.Fprintf(logw, "dashboard: whiteboard close: %v\n", err)
			}
			exportMailbox.call()
			// Under the lock, so the successor never fires the same schedules.
			cron.close(logw)
			// And so the successor's user commands are the only ones that commit.
			if u := host.userCmds.Load(); u != nil {
				if err := u.Close(); err != nil {
					fmt.Fprintf(logw, "dashboard: user commands close: %v\n", err)
				}
			}
		},
	})
	if errors.Is(err, wsdashboard.ErrLocked) {
		closeTracker()
		fmt.Fprintln(os.Stderr, "ws-mcp dashboard: another dashboard daemon is running (it holds the lock); exiting")
		os.Exit(3)
	}
	if err != nil {
		closeTracker()
		fatal("dashboard", err)
	}
	// Open the store now, so the start's import of the legacy files (and a
	// wait on a predecessor still releasing the ring) is not on a session's
	// first mailbox call.
	if _, err := wsmailbox.OpenPersistent(wsmailbox.Paths{}); err != nil {
		fmt.Fprintf(logw, "dashboard: mailbox store: %v\n", err)
	}
	stopMailboxSweep := startMailboxSweep(logw)
	// The export for a previous version runs under the dashboard lock: an
	// older daemon that takes the lock after it never has its own mail
	// overwritten by it. The sweep stops first, so it makes no store call
	// once the persistent mode is off.
	exportMailbox.set(func() {
		stopMailboxSweep()
		if err := wsmailbox.ClosePersistent(); err != nil {
			fmt.Fprintf(logw, "dashboard: mailbox store close: %v\n", err)
		}
	})
	wsdashboard.LogDevAssets(logw, devRoot)
	// Settled copies remain available for surviving launchers and rollback.
	if exe != "" {
		wsdashboard.PruneStagedExecutables(dir, exe)
	}
	trackerChanged.set(daemon.TrackerChanged)
	agyActivity.set(daemon.AgyActivity)
	host.live.Store(daemon)
	labels.daemon.Store(daemon)
	execJobsChanged := func() {
		if d := host.live.Load(); d != nil {
			d.ExecJobsChanged()
		}
		agents.ExecJobsChanged()
	}
	execjob.SetActiveObserver(execJobsChanged)
	defer execjob.SetActiveObserver(nil)
	// Under the lock and with the runner started: a completion notice for
	// a conversation the runner does not know yet would be dropped.
	hostedExec := openHostedExec(dir, holders, agents, execLink, logw, execJobsChanged)
	host.exec.Store(hostedExec)
	if hostedExec != nil {
		// The dashboard's `!` commands run on the same jobs and runner.
		daemon.SetComposerExec(composerExec{hosted: hostedExec, runner: agents})
		// And the job HUD's reads and stop: the same binding.
		daemon.SetExecJobs(composerExec{hosted: hostedExec, runner: agents})
	}
	// The user commands (command slots) open under the lock too, once the
	// runner knows its conversations: the commands of a conversation it lost
	// are dropped, and each change wakes the open pages.
	if u, err := openUserCommands(dir, agents, daemon.UserCommandsChanged, logw); err != nil {
		fmt.Fprintf(logw, "dashboard: user commands unavailable: %v\n", err)
	} else {
		host.userCmds.Store(u)
		daemon.SetUserCommands(u)
	}
	// Settled either way: a lead that connected during the start lists its tools now.
	close(userCmdsReady)
	// The jobs a predecessor left are settled: the runner's report holds,
	// sleep and close cascade read them from now on.
	if hostedExec != nil {
		agents.BindExecJobs(hostedExec)
	} else {
		agents.BindExecJobs(nil)
	}
	// Start ran before this Store, so its wakes read no jobs: a job a
	// predecessor left running reaches the rail with this one.
	execJobsChanged()
	// Scheduled prompts open under the lock too, once the runner knows its
	// conversations: the start reaps schedules of sessions it lost and
	// rebuilds which fires still wait in an inbox.
	if sched := openCron(dir, agents, logw); sched != nil {
		cron.sched.Store(sched)
		host.cron.Store(sched)
		agents.BindCron(sched)
		// A conversation the runner forgot between the start's existence
		// check and the binding had its notice dropped: reap it now, after the
		// binding, so none is missed on either side.
		sched.Reconcile()
	}
	// The hooks read the stores on every change signal the page gets from
	// the watch and the poll; the watch also sees the daemon's own writes.
	nudgeHooks, stopHooks := startTrackerHooks(tracker, trackerErr, dir, daemon.Endpoint().Port, logw)
	stopPoll := startTrackerPoll(tracker, trackerErr, func() {
		daemon.TrackerChanged()
		nudgeHooks()
	}, logw)
	stopWatch := startTrackerWatch(tracker, trackerErr, func() {
		daemon.TrackerChanged()
		support.CloseUnregistered()
		nudgeHooks()
	})
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	select {
	case <-signals:
		daemon.Close()
	case <-daemon.Done():
	}
	// Close already exported the mailbox under the lock; this is the
	// fallback for a Close that ran before the export was bound, and a
	// no-op after it (stopMailboxSweep and ClosePersistent are idempotent).
	stopMailboxSweep()
	if err := wsmailbox.ClosePersistent(); err != nil {
		fmt.Fprintf(logw, "dashboard: mailbox store close: %v\n", err)
	}
	// The lock and endpoint are released (the successor is starting); a
	// hosted call that outlived the drain budget still runs and ends on its
	// own, and the replacement goroutine, when one runs, spawns the
	// successor. Both finish before this process exits.
	linger, cancel := context.WithTimeout(context.Background(), hostedCallLinger)
	if !daemon.WaitProxy(linger) {
		fmt.Fprintf(logw, "dashboard: a hosted call still runs after %s; exiting\n", hostedCallLinger)
	}
	cancel()
	host.wait()
	// The jobs keep running under their holders for the next daemon.
	if hosted := host.exec.Load(); hosted != nil {
		if err := hosted.Close(); err != nil {
			fmt.Fprintf(logw, "dashboard: hosted exec close: %v\n", err)
		}
	}
	cron.close(logw)
	stopWatch()
	stopPoll()
	stopHooks()
	closeTracker()
}

// lateFunc is a callback bound after the code that calls it is built; a
// call before set does nothing. Safe from any goroutine.
type lateFunc struct{ f atomic.Pointer[func()] }

func (l *lateFunc) set(f func()) { l.f.Store(&f) }

func (l *lateFunc) call() {
	if f := l.f.Load(); f != nil {
		(*f)()
	}
}

// trackerWatchInterval is the daemon's tracker store watch period (a test
// seam).
var trackerWatchInterval = wstrackerhost.DefaultWatchInterval

// startTrackerWatch runs the tracker's store watch in the daemon on h, so a
// write by another process (a lead session's or a support agent's ws-mcp)
// calls onChange within trackerWatchInterval; onChange is
// also what a page read that served the last state calls when the store's
// reload lands. newErr is h's creation error: the daemon runs without the
// watch. The caller closes h.
func startTrackerWatch(h *wstrackerhost.Host, newErr error, onChange func()) (stop func()) {
	if newErr != nil {
		return func() {}
	}
	return h.StartWatch(trackerWatchInterval, onChange)
}

// startTrackerHooks runs the tracker event hooks (wstrackerhook) on h, with
// the hooks file and their state in the daemon's state directory dir and
// each event's url on the daemon's loopback port. nudge asks for a read of
// every store; stop kills a running hook and drops the queued ones. newErr
// is h's creation error: no hook runs. The caller closes h after stop.
func startTrackerHooks(h *wstrackerhost.Host, newErr error, dir string, port int, logw io.Writer) (nudge, stop func()) {
	if newErr != nil {
		return func() {}, func() {}
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	r := wstrackerhook.Start(wstrackerhook.Options{Dir: dir, Source: h, Log: logw, BaseURL: func() string { return base }})
	return r.Nudge, r.Close
}

// startTrackerPoll runs the tracker's background poll in the daemon on h:
// every wstrackerhost.DefaultPollInterval, a fetch of every registered
// store, whatever workflow.tracker_enabled says (the knob gates only manual
// text), and onRound after the first round and each later one that changed
// something (wstrackerhost.Host.PollOnce). While it runs, a ws-mcp's pre-read
// check leaves the network to it. newErr is h's creation error: it is
// logged once, and the daemon runs without the poll. The caller closes h.
func startTrackerPoll(h *wstrackerhost.Host, newErr error, onRound func(), logw io.Writer) (stop func()) {
	if newErr != nil {
		fmt.Fprintf(logw, "ws-mcp dashboard: tracker poll not started: %v\n", wstrackerhost.RedactCredentials(newErr.Error()))
		return func() {}
	}
	return h.StartPoll(wstrackerhost.DefaultPollInterval, onRound)
}

// trackerStoreRoot returns a tracker store's working root for the support
// conversations: the registry entry's directory on the daemon's tracker
// host h, the same root the tracker gate pins a support conversation's
// writes to. newErr is h's creation error; every store is unknown then.
func trackerStoreRoot(h *wstrackerhost.Host, newErr error) func(id string) (string, error) {
	return func(id string) (string, error) {
		if newErr != nil {
			return "", redacted(newErr)
		}
		e, err := h.Entry(id)
		if errors.Is(err, wstrackerhost.ErrNotRegistered) {
			return "", fmt.Errorf("%w: %v", wsagent.ErrStoreUnregistered, redacted(err))
		}
		if err != nil {
			return "", redacted(err)
		}
		return e.Dir, nil
	}
}

// mailLabels is the daemon's mail sender-label view (mcp.MailLabelFunc): a
// spawned child's agent.spawn label, else a session's dashboard title, both
// read from memory. Each source binds once it exists; until then a sender
// has no label from it.
type mailLabels struct {
	agents atomic.Pointer[wsagent.Runner]
	daemon atomic.Pointer[wsdashboard.Daemon]
}

func (l *mailLabels) label(replyID string) string {
	if r := l.agents.Load(); r != nil {
		if label := r.SpawnLabel(replyID); label != "" {
			return label
		}
	}
	if d := l.daemon.Load(); d != nil {
		if s, ok := d.Index().Session(replyID); ok {
			return s.Label
		}
	}
	return ""
}

func newDashboardHolders(dir string, logw io.Writer, execLink *hostedExecLink) *wsholder.Manager {
	holders := wsholder.New(wsholder.Options{
		Dir:          filepath.Join(dir, wsholder.DirName),
		Python:       os.Getenv(wsholder.PythonEnv),
		Log:          logw,
		OnExit:       execLink.onExit(persistHolderExit(dir)),
		AuditChannel: true,
	})
	holders.AddSink(execLink)
	return holders
}

// newAgentRunner is the daemon's agent runner: the claude and pi backends,
// ws-mcp's key store for conversation keys, and the plugin's Python launcher
// for the ws-mcp it attaches to a conversation whose profile allows ws tools.
// trackerChanged runs after a conversation's successful tracker write;
// agyActivity runs after a hosted agy turn settles. labels resolves the
// sender legend of the mail the runner delivers; nil shows none.
func newAgentRunner(dir string, logw io.Writer, trackerChanged, agyActivity func(), holders *wsholder.Manager, boards *mcp.WhiteboardStores, labels mcp.MailLabelFunc) *wsagent.Runner {
	modelWaitSeconds := wsagent.DefaultAgyModelWaitTimeoutSeconds
	modelWaitRetries := wsagent.DefaultAgyModelWaitRetries
	if name, _, err := wsagent.ResolveHostedPackage(); err == nil && name == "wsflow" {
		modelWaitSeconds = 0
	}
	keys := mcp.NewAgentKeyStore(boards)
	backends := wsagent.BuiltinBackendRegistrations()
	mail := mcp.NewLeadMailbox(dir).WithLabels(labels)
	return wsagent.NewRunner(wsagent.Options{
		Dir:                        dir,
		DaemonVersion:              version,
		AgyModelWaitTimeoutSeconds: &modelWaitSeconds,
		AgyModelWaitRetries:        &modelWaitRetries,
		Backends:                   backends,
		Keys:                       keys,
		// A lead session's process runs under a session holder with a lead
		// key of its own.
		Holders:  wsagent.ManagerHost{Manager: holders},
		LeadKeys: keys,
		// The daemon, not the session's own mailbox wait, delivers a lead
		// session's mail, each submission with its sender legend.
		Mail:       mail,
		MailLegend: mail.Legend,
		WSMCP: func(root string) (wsagent.MCPCommand, error) {
			return agentMCPCommand(root, version)
		},
		// A hosted lead's launch appends its workflow manual to its prompt,
		// and a lead without ws skills runs the package's lead playbooks as
		// dashboard commands whose turn carries the playbook body.
		LeadManual: func(backend, dir, key string) (string, error) {
			return renderLeadManual(hostedRender(backends[backend], boards), dir, key)
		},
		LeadSessionState: keys.RenderHostedSessionState,
		RestoreBundle: func(backend, dir, key string, cause wsagent.RestoreCause, children []wsagent.AgentChild) (string, error) {
			return renderHostedRestoreBundle(hostedRender(backends[backend], boards), dir, key, cause, children)
		},
		PlaybookCommands: hostedPlaybookCommands,
		ExpandCommand: func(backend, dir, key, name string) (string, error) {
			return renderLeadPlaybook(hostedRender(backends[backend], boards), dir, key, name)
		},
		Log:            logw,
		TrackerChanged: trackerChanged,
		AgyActivity:    agyActivity,
		// A spawned Pi subagent's settled turns go to its lead's mailbox.
		Report: mcp.PiTurnReporter(logw),
		// A Pi subagent's own pi.* calls (the token route) need a usable
		// Pi, as a lead's do.
		ChildCheck: mcp.PiReady,
		// The dashboard's tier routing view asks availability without
		// running pi --version (Runner.HarnessAvailability).
		ChildCheckKnown: mcp.PiReadyKnown,
		// agent.spawn routes role tiers through the packaged playbook
		// roles' agent.tier_routing graph.
		TierRoles: mcp.PackagedTierRoles,
		// A message that cannot wake its sleeping agent stays queued; its
		// sender hears why once.
		WakeRefused: mcp.AgentWakeRefused(logw),
		// A message for an agent whose worktree was released after it was
		// bound there never reaches it; its sender hears why.
		CheckoutReleased: mcp.AgentCheckoutReleased(logw),
	})
}

// dashboardSpawner starts a detached daemon process with the given ws-mcp
// arguments.
type dashboardSpawner func(args []string) error

type dashboardPreparer func(args []string) (func(bool) error, error)

func dashboardStarters(ctx context.Context, dir string, candidate func() (string, error), strict bool) (dashboardSpawner, dashboardPreparer) {
	spawn := func(args []string) error {
		exe, err := candidate()
		if err != nil {
			return err
		}
		if strict {
			return wsdashboard.SpawnDetachedStrict(exe, append(args, "--lock-wait", "8s"), dir)
		}
		return wsdashboard.SpawnDetached(exe, args, dir)
	}
	prepare := func(args []string) (func(bool) error, error) {
		exe, err := candidate()
		if err != nil {
			return nil, err
		}
		preflight, cancel := context.WithTimeout(ctx, 2*wsdashboard.DefaultStartWait)
		defer cancel()
		return wsdashboard.PrepareDetachedSuccessor(preflight, exe, args, dir, strict)
	}
	return spawn, prepare
}

// startDashboard is the serve-startup hook: with the dashboard key on, it
// makes sure a daemon at least as new as this ws-mcp runs, in the LAN mode
// the global dashboard.lan_enabled value asks for (a same-version daemon in the
// other mode, or from an older build time, is replaced). It never fails or
// delays serve: the caller runs it on its own goroutine, and every error is
// a stderr line.
func startDashboard(ctx context.Context, settings mcp.DashboardSettings, dir string, buildTime int64, contentHash string, spawn dashboardSpawner, prepare dashboardPreparer, logw io.Writer) wsdashboard.Outcome {
	if !settings.Enabled {
		return ""
	}
	outcome, err := wsdashboard.Ensure(ctx, wsdashboard.EnsureOptions{
		Dir:                 dir,
		Version:             version,
		BuildTime:           buildTime,
		ContentHash:         contentHash,
		Port:                settings.Port,
		LAN:                 settings.LAN,
		Spawn:               spawn,
		SpawnBeforeShutdown: true,
		JoinStarterLock:     true,
		PrepareSuccessor:    prepare,
		Log:                 logw,
	})
	if err != nil {
		fmt.Fprintf(logw, "ws-mcp: dashboard unavailable: %v\n", err)
	} else if outcome == wsdashboard.OutcomeUnavailable {
		fmt.Fprintln(logw, "ws-mcp: dashboard unavailable: no daemon answered after starting one (another may hold the lock without answering)")
	}
	return outcome
}

// serveDashboard runs startDashboard for a stdio serve with the real daemon
// directory and a detached spawner of this binary.
func serveDashboard(server *mcp.Server) {
	defer func() { _ = recover() }()
	settings := server.DashboardSettings()
	if !settings.Enabled {
		return
	}
	dir, err := wsdashboard.Dir("")
	if err == nil {
		err = os.MkdirAll(dir, 0o755)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "ws-mcp: dashboard unavailable: %v\n", err)
		return
	}
	// The build time comes from the original executable, before
	// DaemonExecutable may stage a copy whose mtime is the copy time; the
	// content hash is the same for both.
	var buildTime int64
	var contentHash string
	if exe, err := os.Executable(); err == nil {
		buildTime = wsdashboard.ExecutableBuildTime(exe)
		contentHash = wsdashboard.ExecutableContentHash(exe)
	}
	ctx := context.Background()
	candidate := func() (string, error) {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		exe, err = wsdashboard.DaemonExecutable(exe, dir)
		if err != nil {
			return "", err
		}
		return wsdashboard.LaunchExecutable(exe, dir)
	}
	spawn, prepare := dashboardStarters(ctx, dir, candidate, false)
	startDashboard(ctx, settings, dir, buildTime, contentHash, spawn, prepare, os.Stderr)
}

// hostedRender is what reg says about text the daemon renders for its
// hosted lead.
func hostedRender(reg wsagent.BackendRegistration, boards *mcp.WhiteboardStores) mcp.HostedRender {
	return mcp.HostedRender{Harness: reg.ManualHarness, PlaybookCommands: reg.PlaybookCommands, PreferNativeSubagent: reg.PreferNativeSubagent, ResolveLeadSubagent: reg.ResolveLeadSubagent, ToolNameScheme: reg.ToolNameScheme, CallFallback: reg.CallFallback, DirectTools: reg.DirectTools, Boards: boards}
}
