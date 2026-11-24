package wsdashboard

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeTestMinorFloor(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, MinorFloorFile)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTestMinorFloor(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, MinorFloorFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestMinorFloorAbsentAdmitsAndMalformedErrors(t *testing.T) {
	t.Run("absent floor admits", func(t *testing.T) {
		dir := t.TempDir()
		floor, exists, err := ReadMinorFloor(dir)
		if err != nil || exists || floor != (MinorVersion{}) {
			t.Fatalf("ReadMinorFloor(absent) = (%v, %v, %v)", floor, exists, err)
		}
		if err := CheckMinorFloor(dir, "0.51.2", StartIntentAbsence, false); err != nil {
			t.Fatalf("absent floor refused candidate: %v", err)
		}
	})

	t.Run("malformed floor errors and never admits", func(t *testing.T) {
		bad := []string{"", "   \n", "0.52.0\n", "v0.52\n", "0.52-dev\n", "-1.2\n", "0.-2\n", "0.\n", ".52\n", "abc\n", "1.2.3\n"}
		for _, content := range bad {
			dir := t.TempDir()
			writeTestMinorFloor(t, dir, content)
			for _, below := range []bool{false, true} {
				if err := CheckMinorFloor(dir, "0.52.0", StartIntentAbsence, below); err == nil {
					t.Fatalf("content %q (belowFloor=%v): accepted malformed floor", content, below)
				}
				if _, err := StartDaemon(DaemonOptions{Dir: dir, Version: "0.52.0", BelowFloor: below, LockWait: 200 * time.Millisecond}); err == nil {
					t.Fatalf("content %q (belowFloor=%v): StartDaemon accepted malformed floor", content, below)
				}
			}
		}
	})

	t.Run("unreadable floor path errors and never admits", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, MinorFloorFile), 0o700); err != nil {
			t.Fatal(err)
		}
		for _, below := range []bool{false, true} {
			if err := CheckMinorFloor(dir, "0.52.0", StartIntentAbsence, below); err == nil {
				t.Fatalf("belowFloor=%v: accepted unreadable floor directory", below)
			}
			if _, err := StartDaemon(DaemonOptions{Dir: dir, Version: "0.52.0", BelowFloor: below, LockWait: 200 * time.Millisecond}); err == nil {
				t.Fatalf("belowFloor=%v: StartDaemon accepted unreadable floor directory", below)
			}
		}
	})

	t.Run("invalid start intent rejected", func(t *testing.T) {
		if _, err := ParseStartIntent("bogus"); err == nil {
			t.Fatal("accepted bogus start intent")
		}
		if intent, err := ParseStartIntent(""); err != nil || intent != StartIntentAbsence {
			t.Fatalf("ParseStartIntent(\"\") = (%q, %v), want absence", intent, err)
		}
	})
}

func TestMinorFloorRefusesLowerCandidateInEnsureAndStartDaemon(t *testing.T) {
	t.Run("Ensure cold start refused", func(t *testing.T) {
		dir := t.TempDir()
		writeTestMinorFloor(t, dir, "0.52\n")
		opts := ensureOpts(dir, "0.51.2", func([]string) error {
			t.Fatal("spawn called despite minor floor refusal")
			return nil
		})
		outcome, err := Ensure(context.Background(), opts)
		if outcome != OutcomeUnavailable || !errors.Is(err, ErrBelowMinorFloor) {
			t.Fatalf("Ensure = (%s, %v), want unavailable + ErrBelowMinorFloor", outcome, err)
		}
		if !strings.Contains(err.Error(), "update this plugin") || !strings.Contains(err.Error(), "daemon-start --below-floor") {
			t.Fatalf("refusal missing update/rollback guidance: %v", err)
		}
	})

	t.Run("StartDaemon under lock refuses every intent with no side effects", func(t *testing.T) {
		for _, intent := range []StartIntent{StartIntentAbsence, StartIntentUpgrade, StartIntentReplace} {
			t.Run(string(intent), func(t *testing.T) {
				dir := t.TempDir()
				writeTestMinorFloor(t, dir, "0.52\n")
				d, err := StartDaemon(DaemonOptions{
					Dir:         dir,
					Port:        0,
					Version:     "0.51.9",
					StartIntent: intent,
					Executable:  filepath.Join(dir, "fake-ws-mcp"),
					LockWait:    200 * time.Millisecond,
				})
				if d != nil || !errors.Is(err, ErrBelowMinorFloor) {
					if d != nil {
						d.Close()
					}
					t.Fatalf("StartDaemon(%s) = (%v, %v), want ErrBelowMinorFloor", intent, d, err)
				}
				if !strings.Contains(err.Error(), "update this plugin") {
					t.Fatalf("StartDaemon(%s) refusal missing update path: %v", intent, err)
				}
				for _, artifact := range []string{endpointFileName, lastPortFileName, RunningExecutableFile} {
					if _, statErr := os.Stat(filepath.Join(dir, artifact)); !errors.Is(statErr, os.ErrNotExist) {
						t.Fatalf("refused StartDaemon(%s) wrote %s", intent, artifact)
					}
				}
				if got := readTestMinorFloor(t, dir); got != "0.52\n" {
					t.Fatalf("floor changed on refusal: %q", got)
				}
			})
		}
	})
}

// floorOrderAgents verifies the floor is updated after startup initialization
// and before endpoint publication.
type floorOrderAgents struct {
	dir                string
	floorAtStart       string
	floorReadErr       error
	endpointAtStartErr error
}

func (a *floorOrderAgents) Handler() http.Handler { return http.NotFoundHandler() }
func (a *floorOrderAgents) Start(func(replyID, project string)) {
	raw, err := os.ReadFile(filepath.Join(a.dir, MinorFloorFile))
	a.floorAtStart, a.floorReadErr = string(raw), err
	_, a.endpointAtStartErr = ReadEndpoint(a.dir)
}
func (a *floorOrderAgents) InboxChanged(string) {}
func (a *floorOrderAgents) Close()              {}

func TestMinorFloorAdvancesOnlyUpwardAfterInitBeforePublish(t *testing.T) {
	dir := t.TempDir()

	// 1. Absent -> 0.51.0 writes 0.51 after agents.Start and before endpoint.
	agents := &floorOrderAgents{dir: dir}
	d1, err := StartDaemon(DaemonOptions{Dir: dir, Port: 0, Version: "0.51.0", LockWait: 200 * time.Millisecond, Agents: agents})
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(agents.floorReadErr, os.ErrNotExist) {
		t.Fatalf("floor already written before startup init finished: %q (%v)", agents.floorAtStart, agents.floorReadErr)
	}
	if agents.endpointAtStartErr == nil {
		t.Fatal("endpoint already written before startup init finished")
	}
	if got := readTestMinorFloor(t, dir); got != "0.51\n" {
		t.Fatalf("floor after 0.51.0 = %q, want 0.51\\n", got)
	}
	d1.Close()

	// 2. Same minor (0.51.3) does NOT rewrite the floor file.
	floorPath := filepath.Join(dir, MinorFloorFile)
	oldTime := time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := os.Chtimes(floorPath, oldTime, oldTime); err != nil {
		t.Fatal(err)
	}
	d2, err := StartDaemon(DaemonOptions{Dir: dir, Port: 0, Version: "0.51.3", LockWait: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(floorPath)
	if err != nil {
		t.Fatal(err)
	}
	if !st.ModTime().Equal(oldTime) {
		t.Fatalf("same-minor start rewrote %s (mtime %v != %v)", MinorFloorFile, st.ModTime(), oldTime)
	}
	d2.Close()

	// 3. Higher minor (0.52.0) advances the floor to 0.52.
	agents2 := &floorOrderAgents{dir: dir}
	d3, err := StartDaemon(DaemonOptions{Dir: dir, Port: 0, Version: "0.52.0", LockWait: 200 * time.Millisecond, Agents: agents2})
	if err != nil {
		t.Fatal(err)
	}
	if agents2.floorAtStart != "0.51\n" {
		t.Fatalf("floor advanced before startup init finished: %q", agents2.floorAtStart)
	}
	if got := readTestMinorFloor(t, dir); got != "0.52\n" {
		t.Fatalf("floor after 0.52.0 = %q, want 0.52\\n", got)
	}
	d3.Close()

	// 4. Lower minor (0.51.9) without BelowFloor is refused and leaves 0.52.
	if d4, err := StartDaemon(DaemonOptions{Dir: dir, Port: 0, Version: "0.51.9", LockWait: 200 * time.Millisecond}); err == nil {
		d4.Close()
		t.Fatal("lower minor admitted without BelowFloor")
	}
	if got := readTestMinorFloor(t, dir); got != "0.52\n" {
		t.Fatalf("floor lowered on refused start: %q", got)
	}
}

func TestMinorFloorRefusedCandidateNeverDrainsLiveDaemon(t *testing.T) {
	for _, spawnBefore := range []bool{false, true} {
		for _, tc := range []struct {
			name      string
			liveVer   string
			candVer   string
			replace   bool
		}{
			{"raced older daemon under higher floor (upgrade intent)", "0.50.0", "0.51.0", false},
			{"equal-core explicit replace under higher floor (replace intent)", "0.51.0", "0.51.0", true},
		} {
			t.Run(tc.name+"/spawnBefore="+strings.ToLower(string(rune('0'+map[bool]int{false: 0, true: 1}[spawnBefore]))), func(t *testing.T) {
				dir := t.TempDir()
				live := startTestDaemon(t, dir, tc.liveVer)
				// Simulate a state directory whose floor is 0.52 while a raced older daemon answers.
				writeTestMinorFloor(t, dir, "0.52\n")

				opts := ensureOpts(dir, tc.candVer, func([]string) error {
					t.Fatal("unexpected spawn on floor refusal")
					return nil
				})
				opts.Replace = tc.replace
				opts.SpawnBeforeShutdown = spawnBefore
				opts.PrepareSuccessor = func([]string) (func(bool) error, error) {
					t.Fatal("unexpected PrepareSuccessor on floor refusal")
					return nil, nil
				}
				outcome, err := Ensure(context.Background(), opts)
				if outcome != OutcomeUnavailable || !errors.Is(err, ErrBelowMinorFloor) {
					t.Fatalf("Ensure = (%s, %v), want unavailable + ErrBelowMinorFloor", outcome, err)
				}
				if info, herr := Health(context.Background(), testClient(), live.Endpoint()); herr != nil || info.Version != tc.liveVer {
					t.Fatalf("live daemon %s was drained or altered: info=%+v err=%v", tc.liveVer, info, herr)
				}
				if got := readTestMinorFloor(t, dir); got != "0.52\n" {
					t.Fatalf("floor changed: %q", got)
				}
			})
		}
	}
}

func TestEnsurePassesStartIntentAndAgreesWithStartDaemon(t *testing.T) {
	t.Run("intent reaches StartDaemon for absence, upgrade, and replace", func(t *testing.T) {
		cases := []struct {
			name        string
			initialVer  string
			candVer     string
			replace     bool
			spawnBefore bool
			wantIntent  StartIntent
			wantOutcome Outcome
		}{
			{"absence cold start", "", "0.52.0", false, false, StartIntentAbsence, OutcomeStarted},
			{"upgrade direct", "0.51.0", "0.52.0", false, false, StartIntentUpgrade, OutcomeReplaced},
			{"upgrade prepared", "0.51.0", "0.52.0", false, true, StartIntentUpgrade, OutcomeReplaced},
			{"explicit replace prepared", "0.52.0", "0.52.0", true, true, StartIntentReplace, OutcomeReplaced},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				var old *Daemon
				if tc.initialVer != "" {
					old = startTestDaemon(t, dir, tc.initialVer)
				}
				sp := &inProcessSpawner{t: t, dir: dir, versions: []string{tc.candVer}}
				defer sp.closeAll()
				var captured []string
				opts := ensureOpts(dir, tc.candVer, func(args []string) error {
					captured = append([]string(nil), args...)
					return sp.spawn(args)
				})
				opts.Replace = tc.replace
				opts.SpawnBeforeShutdown = tc.spawnBefore
				if tc.spawnBefore {
					opts.PrepareSuccessor = func(args []string) (func(bool) error, error) {
						captured = append([]string(nil), args...)
						return func(allow bool) error {
							if allow {
								return sp.spawn(args)
							}
							return nil
						}, nil
					}
				}
				outcome, err := Ensure(context.Background(), opts)
				if err != nil || outcome != tc.wantOutcome {
					t.Fatalf("Ensure = (%s, %v), want %s", outcome, err, tc.wantOutcome)
				}
				if old != nil {
					waitDone(t, old)
				}
				_, _, _, gotIntent, gotBelow, ok := parseSpawnArgs(captured)
				if !ok || gotIntent != tc.wantIntent || gotBelow {
					t.Fatalf("spawn args = %v -> intent=%q below=%v ok=%v, want %q", captured, gotIntent, gotBelow, ok, tc.wantIntent)
				}
			})
		}
	})

	t.Run("Ensure and StartDaemon agree across every intent", func(t *testing.T) {
		for _, intent := range []StartIntent{StartIntentAbsence, StartIntentUpgrade, StartIntentReplace} {
			for _, tc := range []struct {
				cand       string
				belowFloor bool
				wantAllow  bool
			}{
				{"0.51.2", false, false},
				{"0.51.2", true, true},
				{"0.52.0", false, true},
				{"0.53.0", false, true},
			} {
				dir := t.TempDir()
				writeTestMinorFloor(t, dir, "0.52\n")
				checkErr := CheckMinorFloor(dir, tc.cand, intent, tc.belowFloor)
				d, startErr := StartDaemon(DaemonOptions{
					Dir:         dir,
					Port:        0,
					Version:     tc.cand,
					StartIntent: intent,
					BelowFloor:  tc.belowFloor,
					LockWait:    200 * time.Millisecond,
				})
				if d != nil {
					d.Close()
				}
				if (checkErr == nil) != tc.wantAllow || (startErr == nil) != tc.wantAllow {
					t.Fatalf("intent=%s cand=%s below=%v: CheckMinorFloor=%v StartDaemon=%v wantAllow=%v",
						intent, tc.cand, tc.belowFloor, checkErr, startErr, tc.wantAllow)
				}
			}
		}
	})
}

func TestMinorFloorBelowFloorRollbackLowersFloorAndLeavesLiveNewerAlone(t *testing.T) {
	t.Run("cold start with BelowFloor admits and lowers floor", func(t *testing.T) {
		dir := t.TempDir()
		writeTestMinorFloor(t, dir, "0.52\n")
		sp := &inProcessSpawner{t: t, dir: dir, versions: []string{"0.51.2"}}
		defer sp.closeAll()
		opts := ensureOpts(dir, "0.51.2", sp.spawn)
		opts.BelowFloor = true
		outcome, err := Ensure(context.Background(), opts)
		if err != nil || outcome != OutcomeStarted {
			t.Fatalf("Ensure(BelowFloor) = (%s, %v), want started", outcome, err)
		}
		if got := readTestMinorFloor(t, dir); got != "0.51\n" {
			t.Fatalf("floor after rollback = %q, want 0.51\\n", got)
		}
		if len(sp.calls) != 1 {
			t.Fatalf("spawns = %d, want 1", len(sp.calls))
		}
		_, _, _, intent, below, ok := parseSpawnArgs(sp.calls[0])
		if !ok || intent != StartIntentAbsence || !below {
			t.Fatalf("spawn args = %v, want --start-intent absence --below-floor", sp.calls[0])
		}
	})

	t.Run("BelowFloor never drains a live newer daemon", func(t *testing.T) {
		dir := t.TempDir()
		live := startTestDaemon(t, dir, "0.52.0")
		opts := ensureOpts(dir, "0.51.2", func([]string) error {
			t.Fatal("unexpected spawn against live newer daemon")
			return nil
		})
		opts.Replace = true
		opts.BelowFloor = true
		outcome, err := Ensure(context.Background(), opts)
		if err != nil || outcome != OutcomeRunning {
			t.Fatalf("Ensure = (%s, %v), want running", outcome, err)
		}
		if _, herr := Health(context.Background(), testClient(), live.Endpoint()); herr != nil {
			t.Fatalf("live newer daemon drained: %v", herr)
		}
		if got := readTestMinorFloor(t, dir); got != "0.52\n" {
			t.Fatalf("floor lowered while live newer daemon stayed: %q", got)
		}
	})

	t.Run("explicit Replace without BelowFloor never lowers floor", func(t *testing.T) {
		dir := t.TempDir()
		writeTestMinorFloor(t, dir, "0.52\n")
		opts := ensureOpts(dir, "0.51.2", func([]string) error {
			t.Fatal("unexpected spawn without BelowFloor")
			return nil
		})
		opts.Replace = true
		outcome, err := Ensure(context.Background(), opts)
		if outcome != OutcomeUnavailable || !errors.Is(err, ErrBelowMinorFloor) {
			t.Fatalf("Ensure(Replace) = (%s, %v), want ErrBelowMinorFloor", outcome, err)
		}
		if got := readTestMinorFloor(t, dir); got != "0.52\n" {
			t.Fatalf("floor changed: %q", got)
		}
	})
}
