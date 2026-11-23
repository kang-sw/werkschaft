// daemon_runtime.go holds daemon change hooks, polling, and shutdown.
package wsdashboard

import (
	"context"
	"fmt"
	"github.com/kang-sw/devenv/internal/wslive"
	"path/filepath"
	"time"
)

// sessionChanged is the index's change hook: it wakes the streams whose
// page shows the session, and drops a session's cached conversation entries,
// entry files, and snapshot, and the source's caches of it
// (SessionForgetter), once it left the index.
func (d *Daemon) sessionChanged(replyID string) {
	if d.index != nil {
		if session, ok := d.index.Session(replyID); ok {
			d.auditSessionSeen(session.Registration)
			d.scheduleAnswerReplay(session.Registration)
			if d.planUsage != nil && d.harnesses.pushesClaudeUsage(session.Harness) {
				d.planUsage.pushClaude(d.snapshots.snapshotWithoutObserved(d.dir, replyID))
			}
		} else {
			d.entries.drop(replyID)
			d.entryFiles.drop(replyID)
			d.railMsgs.drop(replyID)
			d.live.drop(replyID)
			d.snapshots.Forget(d.dir, replyID)
			wslive.DropRing(wslive.RingDir(SessionDir(d.dir, replyID)))
			if f, ok := d.source.(SessionForgetter); ok {
				f.ForgetSession(replyID)
			}
			if d.forgotten != nil {
				d.forgotten(replyID)
			}
		}
	}
	d.stats.WakeSession.Add(1)
	d.broker.wake(replyID)
	d.acks.poke(replyID)
	d.pokeHires()
}

// agentChanged is the agent runner's write hook, called from the
// conversation's loop for every write: it schedules a re-read of the one
// conversation, as a ws-mcp's nudge does for a lead session, and returns
// without waiting for it (Index.Reread). The page wakes at once, since its
// stream reads the conversation's own live files; the re-read's change, if
// any, wakes it again when it lands.
func (d *Daemon) agentChanged(replyID, _ string) {
	if d.index != nil {
		// The runner changes live stores, not ws-mcp's record/board/mailbox.
		d.index.RereadLive(replyID)
	}
	d.stats.WakeAgent.Add(1)
	d.broker.wake(replyID)
	d.acks.poke(replyID)
}

// ExecJobsChanged re-renders the rail once after the live exec job counts
// (DaemonOptions.ExecJobs) changed. It reads no file: it wakes the rail
// streams as a layout change does, and their render reuses its cached
// inputs, asking ExecJobs again. Wakes coalesce in the broker's window and
// the rail's minimum interval, so a burst of jobs renders once.
//
// It also bumps the revision of the hosted exec job set and wakes every
// stream, which sends it as the opaque execjobs event (never any job
// content: every viewer shares the stream, and a page refetches the list
// from the localhost-only job route). It fires on every job start and end
// and when a user stop's notice outcome is recorded.
func (d *Daemon) ExecJobsChanged() {
	if d.broker == nil {
		return
	}
	d.execRev.Add(1)
	d.broker.wake(railListRepoKey)
	d.broker.signalAll()
}

// AgyActivity schedules a usage refresh after a hosted agy turn settles.
// The handoff must not wait for the plan-usage store's lock.
func (d *Daemon) AgyActivity() {
	d.agyActivity(nil)
}

// agyActivity exposes completion of the lock-taking handoff to package tests.
func (d *Daemon) agyActivity(done chan<- struct{}) {
	if d.planUsage == nil {
		return
	}
	go func() {
		d.planUsage.refreshAgy()
		if done != nil {
			close(done)
		}
	}()
}

// poll fills the session index at once and then re-reads every registered
// session at the external safety interval. Clock-only checks retain the old
// cadence, with targeted reads only when known liveness evidence expires.
func (d *Daemon) poll(interval time.Duration) {
	if interval <= 0 {
		interval = DefaultPollInterval
	}
	d.index.Refresh()
	d.auditFirstSweep()
	d.pokeHires()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	clock := time.NewTicker(min(indexClockInterval, interval))
	defer clock.Stop()
	for {
		select {
		case <-d.stop:
			return
		case <-ticker.C:
			d.index.Refresh()
			d.pokeHires()
		case <-clock.C:
			d.index.RefreshClock()
		}
	}
}

// Index returns the daemon's session index; nil when it runs without a
// session source.
func (d *Daemon) Index() *Index { return d.index }

// Endpoint returns what the daemon wrote to its endpoint file.
func (d *Daemon) Endpoint() Endpoint { return d.endpoint }

// Done is closed once the daemon has stopped serving, removed its endpoint
// file, and released the lock.
func (d *Daemon) Done() <-chan struct{} { return d.done }

// Close stops the daemon. The endpoint file is removed before the lock is
// released, so a successor that takes the lock never has its own file
// deleted. Close is idempotent. A hosted call that outlives the drain
// budget is still running when Close returns (drainProxy); WaitProxy waits
// for it.
func (d *Daemon) Close() {
	d.closeOnce.Do(func() {
		close(d.stop)
		// The composer's runs and background loop end with the stop (a
		// running launch converts on it); none writes after Close returns.
		d.composerWait()
		if d.hireDone != nil {
			<-d.hireDone
		}
		if d.leadNudges != nil {
			d.leadNudges.Close()
		}
		d.acks.close()
		// Shutdown closes the listener at once, so no new session or page
		// connects, and the hosted sessions drain meanwhile (proxy.go):
		// the whole stop stays under the successor's lock wait.
		ctx, cancel := context.WithTimeout(context.Background(), shutdownDeadline)
		if d.planUsage != nil {
			d.planUsage.beginClose()
		}
		shutdownDone := make(chan struct{})
		go func() {
			_ = d.server.Shutdown(ctx)
			close(shutdownDone)
		}()
		d.drainProxy()
		<-shutdownDone
		if d.planUsage != nil {
			d.planUsage.waitClose(ctx)
		}
		cancel()
		// Shutdown closes only the listeners Serve has registered, and Serve
		// runs on its own goroutine: a Close before that goroutine is
		// scheduled would leave the port bound past Close, so a restart could
		// not rebind it. Closing it here makes Close release the port.
		_ = d.listener.Close()
		// The web card listeners are servers of their own; closed here,
		// before the lock goes, a successor never races a leftover port.
		d.web.close()
		if d.agents != nil {
			d.agents.Close()
		}
		// Detaches only: the holders and their children outlive the daemon.
		if d.holders != nil {
			d.holders.Close()
		}
		d.foreign.close()
		// After the runner and holders let go, so no capture appends race
		// the final flush.
		d.closeAudit()
		_ = wslive.UnpinRingsUnder(filepath.Join(d.dir, sessionsDirName))
		// A credential change still in flight (a hosted call or a change
		// request the bounded drain left running) lands now or is refused:
		// once the lock goes, a successor or the offline CLI writes the file.
		if d.credentials != nil {
			d.credentials.Close()
		}
		if d.beforeUnlock != nil {
			d.beforeUnlock()
		}
		removeEndpointIfOwned(d.dir, d.endpoint.Token)
		fmt.Fprintf(d.log, "dashboard: stopped (pid %d)\n", d.endpoint.PID)
		if d.endLog != nil {
			d.endLog()
		}
		_ = d.lock.Unlock()
		close(d.done)
	})
}
