package execjob

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsholder"
)

func TestHostedModelLaunchPersistsJobBeforeSpawn(t *testing.T) {
	fx := newHostedFixture(t, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	drain := make(chan struct{})
	var id string
	fx.holders.beforeLaunch = func() {
		if id == "" {
			t.Fatal("the holder launched before the model call recorded its job")
		}
	}
	fx.holders.beforeSpawn = func(holder string) {
		row, ok := fx.store.row(id)
		if !ok || row.HolderID != holder || row.Owner != "lead" || row.Gen != 3 || row.StartedAt == 0 {
			t.Fatalf("spawn began before its job was saved: %+v", row)
		}
	}
	fx.holders.onSpawn = func(string) {
		close(drain)
		cancel()
	}
	view, err := fx.launch(ctx, drain, HostedLaunch{BindJob: func(job string) error {
		id = job
		return nil
	}})
	if err != nil || view.ID != id || view.Outcome != OutcomeConverted || view.State != HostedRunning {
		t.Fatalf("launch during drain = %+v, %v", view, err)
	}
	if err := fx.h.Close(); err != nil {
		t.Fatal(err)
	}
	fx.open(time.Hour)
	if got := fx.h.RunningOwners()["lead"]; got != 1 {
		t.Fatalf("replacement retained %d jobs, want one report hold", got)
	}
	row, _ := fx.store.row(id)
	for _, wrong := range []struct {
		owner string
		gen   uint64
	}{
		{"other", 3}, {"lead", 2},
	} {
		if _, err := fx.h.RecoverModelJob(wrong.owner, wrong.gen, id); !errors.Is(err, ErrExecNotFound) {
			t.Fatalf("job accessible to the wrong owner or generation: %v", err)
		}
	}
	running, err := fx.h.RecoverModelJob("lead", 3, id)
	if err != nil || !strings.Contains(running, "exec_id: "+id+"\nstatus: running") {
		t.Fatalf("recovered running job = %q, %v", running, err)
	}
	fx.holders.out(row.HolderID, "stdout", "once")
	fx.holders.exit(row.HolderID, 0)
	terminal, err := fx.h.RecoverModelJob("lead", 3, id)
	if err != nil || !strings.Contains(terminal, "status: exited\nexit_code: 0") || !strings.Contains(terminal, "once") {
		t.Fatalf("recovered terminal job = %q, %v", terminal, err)
	}
	if n := fx.holders.n; n != 1 {
		t.Fatalf("replacement allocated %d holders, want one", n)
	}
}

func TestHostedModelLaunchFailsClosed(t *testing.T) {
	for _, when := range []string{"intent", "job row"} {
		t.Run(when, func(t *testing.T) {
			fx := newHostedFixture(t, time.Hour)
			fx.store.failPut = func(row HostedJobRow) bool {
				return when == "job row" && row.HolderID != "" && !row.Terminal()
			}
			view, err := fx.launch(context.Background(), nil, HostedLaunch{BindJob: func(string) error {
				if when == "intent" {
					return errors.New("intent write failed")
				}
				return nil
			}})
			if err == nil || view.State != HostedFailed {
				t.Fatalf("launch = %+v, %v", view, err)
			}
			if len(fx.holders.spawned) != 0 || len(fx.holders.live) != 0 || (when == "intent" && fx.holders.n != 0) {
				t.Fatal("a failed durability barrier launched a command or left a holder running")
			}
		})
	}
}

func TestHostedModelLaunchReplacementBeforeSpawn(t *testing.T) {
	fx := newHostedFixture(t, time.Hour)
	const id = "exec_test-job-id"
	session, err := json.Marshal(hostedHolderSession{Kind: HolderKindExec, ExecID: id, Owner: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	holder, err := fx.holders.LaunchWith(wsholder.LaunchSpec{Session: session})
	if err != nil {
		t.Fatal(err)
	}
	if err := fx.store.Put(HostedJobRow{
		ID: id, Owner: "lead", Gen: 3, HolderID: holder, State: HostedRunning,
		Root: fx.exitDir, Cwd: fx.exitDir, StartedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := fx.h.Close(); err != nil {
		t.Fatal(err)
	}
	fx.open(time.Hour)
	result, err := fx.h.RecoverModelJob("lead", 3, id)
	if err != nil || !strings.Contains(result, "status: failed") || !strings.Contains(result, "before the command started") {
		t.Fatalf("replacement returned %q, %v", result, err)
	}
	if len(fx.h.RunningOwners()) != 0 || len(fx.holders.spawned) != 0 || len(fx.holders.live) != 0 {
		t.Fatal("replacement kept a running job hold or started a command")
	}
}
