package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sync/atomic"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wscron"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

// cronHost holds the daemon's scheduler once it is open. The daemon options
// take its hooks before the scheduler exists (it opens under the daemon's
// lock, after StartDaemon), so they read it through here.
type cronHost struct {
	sched atomic.Pointer[wscron.Scheduler]
}

// sessionForgotten is the daemon's session-forget hook: a session left the
// index. That alone is not the session's end (an unmounted project or an
// identity mismatch also drops one), so the schedules go only once the runner
// knows the conversation no more. The work runs off the index's change path.
func (h *cronHost) sessionForgotten(runner *wsagent.Runner, id string) {
	sched := h.sched.Load()
	if sched == nil {
		return
	}
	go func() {
		if !runner.CronKnown(id) {
			sched.ConversationGone(id)
		}
	}()
}

// close stops the scheduler's timer, writes what it owes its store and
// closes the store. It is idempotent and a no-op before the scheduler opens.
func (h *cronHost) close(logw io.Writer) {
	if sched := h.sched.Load(); sched != nil {
		if err := sched.Close(); err != nil {
			fmt.Fprintf(logw, "dashboard: cron close: %v\n", err)
		}
	}
}

// openCron opens the scheduled-prompt store in the daemon's state directory
// and starts the scheduler over runner's conversations. Run it under the
// daemon's lock once the runner has started. A failure is logged and leaves
// the cron tool refusing as unavailable, never a crash.
func openCron(dir string, runner *wsagent.Runner, logw io.Writer) *wscron.Scheduler {
	store, err := wscron.OpenStore(filepath.Join(dir, wscron.StoreName))
	if err != nil {
		fmt.Fprintf(logw, "dashboard: cron unavailable: %v\n", err)
		return nil
	}
	sched, err := wscron.New(wscron.Options{Store: store, Sessions: cronSessions{r: runner}, Log: logw})
	if err != nil {
		_ = store.Close()
		fmt.Fprintf(logw, "dashboard: cron unavailable: %v\n", err)
		return nil
	}
	return sched
}

// cronSessions is the agent runner as the scheduler's target side.
type cronSessions struct{ r *wsagent.Runner }

func (s cronSessions) IsLead(id string) (bool, error) { return s.r.CronIsLead(id) }

func (s cronSessions) Exists(id string) bool { return s.r.CronKnown(id) }

func (s cronSessions) HeldSchedules(id string) []string { return s.r.CronHeld(id) }

func (s cronSessions) Deliver(id string, mark wsinbox.Cron, prompt string) (wscron.Delivery, error) {
	ok, err := s.r.DeliverCron(id, mark, prompt)
	if err != nil {
		return 0, err
	}
	if !ok {
		return wscron.Suppressed, nil
	}
	return wscron.Delivered, nil
}
