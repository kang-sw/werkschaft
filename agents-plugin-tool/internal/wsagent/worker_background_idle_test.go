package wsagent

import (
	"slices"
	"testing"
	"time"
)

// claudeWorkerWithTask is a held Claude worker whose first turn ended while a
// native background task (a Bash run_in_background) still runs: the turn's
// reply and result came, the task list names one task, and the idle timer is
// short. bgResumeGrace is short too, for the wake-up the task's end may bring.
func claudeWorkerWithTask(t *testing.T) (h *leadHarness, got *reports, id, holder string) {
	t.Helper()
	restore := bgResumeGrace
	bgResumeGrace = 150 * time.Millisecond
	t.Cleanup(func() { bgResumeGrace = restore })
	h, got = workerRecoveryHarness(t, BackendClaude, true)
	id, holder = spawnHeldRecoveryWorker(t, h, BackendClaude, "workdir-write")
	h.runner.conv(id).do(func() { h.runner.conv(id).idleAfter = 60 * time.Millisecond })
	h.holders.out(holder, tasksLine(1), replyLine("u1", "started"), resultLine())
	eventually(t, "the worker's turn to end", func() bool {
		c := h.runner.conv(id)
		turn, bg := true, 0
		c.do(func() { turn, bg = c.turn, c.bgTasks })
		return !turn && bg == 1
	})
	return h, got, id, holder
}

func workerIdleState(h *leadHarness, id string) (proc bool, bg int, idle bool) {
	c := h.runner.conv(id)
	c.do(func() { proc, bg, idle = c.proc != nil, c.bgTasks, c.idle != nil })
	return
}

// TestClaudeWorkerBackgroundTaskKeepsItAlive: a worker whose harness still
// runs a background task is not idle: the idle timer does not stop it and its
// done turn is held, as for a lead and the other workers. Once the task ends
// the usual idle behaviour resumes: the held turn is reported and the idle
// timer stops the process.
func TestClaudeWorkerBackgroundTaskKeepsItAlive(t *testing.T) {
	h, got, id, holder := claudeWorkerWithTask(t)
	time.Sleep(400 * time.Millisecond) // several idle periods
	if proc, bg, _ := workerIdleState(h, id); !proc || bg != 1 || h.holders.opCount("stop") != 0 {
		t.Fatalf("a worker with a running background task was stopped: process %v, tasks %d, ops %v", proc, bg, h.holders.opList())
	}
	if n := len(got.list()); n != 0 {
		t.Fatalf("the worker's turn was reported while its task runs: %+v", got.list())
	}

	// The task ends; no wake-up turn follows. The held turn is reported once
	// and the idle timer then ends the process.
	h.holders.out(holder, tasksLine(0))
	eventually(t, "the held turn's report", func() bool { return len(got.list()) == 1 })
	if rep := got.list()[0]; rep.Result.Status != TurnDone || rep.Result.Message != "started" || rep.Result.ID != id {
		t.Fatalf("report: %+v", rep)
	}
	eventually(t, "the idle timer to stop the finished worker", func() bool { return h.holders.opCount("stop") == 1 })
	if n := len(got.list()); n != 1 {
		t.Fatalf("reports = %d, want 1", n)
	}
}

// TestClaudeWorkerBackgroundWakeTurnReportsOnce: the turn a finished task
// brings on its own (a running state with no send) is the worker's turn: its
// result is the one report to the parent, whole, neither lost nor repeated
// with the held earlier turn.
func TestClaudeWorkerBackgroundWakeTurnReportsOnce(t *testing.T) {
	h, got, id, holder := claudeWorkerWithTask(t)
	h.holders.out(holder, tasksLine(0), stateLine(HarnessRunning), replyLine("u2", "task finished"), resultLine(), stateLine(HarnessIdle))
	eventually(t, "the wake-up turn's report", func() bool { return len(got.list()) >= 1 })
	time.Sleep(400 * time.Millisecond)
	reps := got.list()
	if len(reps) != 1 || reps[0].Result.Status != TurnDone || reps[0].Result.Message != "task finished" || reps[0].Result.ID != id {
		t.Fatalf("reports = %+v, want one done report of the wake-up turn", reps)
	}
}

// TestClaudeWorkerBackgroundTaskBlocksStaleRouteRestart: a rejoined worker
// whose recorded ws-mcp route this daemon does not serve restarts at an idle
// point (restartIfDue); one with a background task running is not at one, so
// its process, and the task under it, is left alone. When the task ends the
// restart happens as it does for any idle worker.
func TestClaudeWorkerBackgroundTaskBlocksStaleRouteRestart(t *testing.T) {
	h, got, id, holder := claudeWorkerWithTask(t)
	h.runner.Close()
	h.holders.restart()
	h.startAt("http://127.0.0.1:2") // not the route the worker's token was drawn on
	wait(t, "the rejoin", h.runner.rejoined)
	h.quiesce(id)
	time.Sleep(400 * time.Millisecond)
	if proc, bg, _ := workerIdleState(h, id); !proc || bg != 1 || h.holders.opCount("stop") != 0 {
		t.Fatalf("a rejoined worker with a running task was restarted: process %v, tasks %d, ops %v", proc, bg, h.holders.opList())
	}
	h.holders.out(holder, tasksLine(0))
	eventually(t, "the stale route's restart once the task ended", func() bool { return h.holders.opCount("stop") == 1 })
	if n := len(got.list()); n > 1 {
		t.Fatalf("reports = %+v", got.list())
	}
}

// TestClaudeWorkerReadsSessionStateAndTasks: only an agent.spawn worker
// (StartSpec.SessionState) asks its harness for session states and reads the
// task list; any other non-lead conversation stays as it was.
func TestClaudeWorkerReadsSessionStateAndTasks(t *testing.T) {
	if env := claudeWorkerEnv([]string{"PATH=/bin", "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=0"}); !slices.Contains(env, "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=1") || slices.Contains(env, "CLAUDE_CODE_EMIT_SESSION_STATE_EVENTS=0") {
		t.Fatalf("claudeWorkerEnv = %v", env)
	}
	line := []byte(`{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"b","task_type":"local_bash"}]}`)
	spec := claudeSpec(t, `{"backend":"claude"}`)
	if evs := newClaudeMapper(spec).mapLine(line, nopReplies{}); len(evs) != 0 {
		t.Fatalf("a plain profile conversation read the task list: %+v", evs)
	}
	spec.SessionState = true
	evs := newClaudeMapper(spec).mapLine(line, nopReplies{})
	if len(evs) != 1 || evs[0].BackgroundTasks == nil || *evs[0].BackgroundTasks != 1 {
		t.Fatalf("a worker's task list = %+v, want one task", evs)
	}
}
