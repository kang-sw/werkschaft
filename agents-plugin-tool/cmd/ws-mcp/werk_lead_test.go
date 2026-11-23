package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
	"github.com/kang-sw/devenv/internal/wsinbox"
)

type werkLeadCredential struct {
	requests  chan wscred.Request
	consented *atomic.Bool
}

func (s werkLeadCredential) hasConsent() bool { return s.consented != nil && s.consented.Load() }

func (s werkLeadCredential) Status() ([]wscred.ProviderStatus, error) {
	consent := s.hasConsent()
	return []wscred.ProviderStatus{{Provider: wscred.ProviderOpenAI, Configured: true, OpenAIAccounts: []wscred.OpenAIAccountStatus{
		{Active: true, PlanEnabled: true, SignedIn: true, BackgroundConsent: &consent},
	}}}, nil
}

func (s werkLeadCredential) Credential(_ context.Context, req wscred.Request) (wscred.Credential, error) {
	s.requests <- req
	if req.Provider == wscred.ProviderOpenAI && req.Background && !s.hasConsent() {
		return wscred.Credential{}, wscred.ErrConsentRequired
	}
	provider, ok := wscred.Lookup(req.Provider)
	if !ok || !req.AgentSession {
		return wscred.Credential{}, wscred.ErrNotPermitted
	}
	return wscred.Credential{Provider: provider.ID, Kind: provider.Kind, Secret: wscred.NewSecret("fixture-provider-key")}, nil
}

func (s werkLeadCredential) OpenAIModels(_ context.Context, req wscred.Request) ([]wscred.OpenAIModel, error) {
	if !req.AgentSession || req.Background {
		return nil, wscred.ErrNotPermitted
	}
	return []wscred.OpenAIModel{{Slug: "vendor/fixture", DisplayName: "Account fixture", ContextWindow: 64000}}, nil
}

func waitWerkLead(t *testing.T, r *wsagent.Runner, id string, timeout time.Duration, ready func(wsagent.State) bool) wsagent.State {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		st, err := r.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if ready(st) {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("lead did not settle as expected (status %s, failure %v)", st.Status, st.Failure != nil)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWerkHostedLeadProviders(t *testing.T) {
	for _, tc := range []struct {
		id         wscred.ProviderID
		host, path string
		window     int64
	}{
		{wscred.ProviderOpenRouter, "openrouter.ai", "/api/v1/responses", 32000},
		{wscred.ProviderOpenAI, "api.openai.com", "/v1/responses", 64000},
	} {
		t.Run(string(tc.id), func(t *testing.T) {
			f := newWerkDaemonFixture(t)
			source := werkLeadCredential{requests: make(chan wscred.Request, 8)}
			f.credential = source
			var calls atomic.Int32
			installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper {
				return werkFixtureRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					if req.URL.Hostname() == "localhost" || req.URL.Hostname() == "127.0.0.1" {
						return wrapped.RoundTrip(req)
					}
					if req.Method == http.MethodGet && req.URL.Host == "openrouter.ai" && req.URL.Path == "/api/v1/models" {
						return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(
							`{"data":[{"id":"vendor/fixture","context_length":32000}]}`))}, nil
					}
					if req.URL.Host != tc.host || req.URL.Path != tc.path || req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer fixture-provider-key" {
						return nil, fmt.Errorf("unexpected fixture endpoint or authorization")
					}
					defer req.Body.Close()
					var wire map[string]json.RawMessage
					if err := json.NewDecoder(req.Body).Decode(&wire); err != nil {
						return nil, err
					}
					if string(wire["model"]) != `"vendor/fixture"` || string(wire["stream"]) != "true" || string(wire["store"]) != "false" ||
						!bytes.Contains(wire["reasoning"], []byte(`"effort":"high"`)) || !bytes.Contains(wire["include"], []byte(`"reasoning.encrypted_content"`)) {
						return nil, fmt.Errorf("wrong Responses request for hosted lead")
					}
					if tc.id == wscred.ProviderOpenAI {
						for _, field := range []string{"temperature", "max_output_tokens", "background"} {
							if _, ok := wire[field]; ok {
								return nil, fmt.Errorf("SIWC rejected field %s", field)
							}
						}
					}
					var instructions string
					_ = json.Unmarshal(wire["instructions"], &instructions)
					_, rest, found := strings.Cut(instructions, "Your ws session key is "+"\x60")
					key, _, _ := strings.Cut(rest, "\x60")
					if !found || key == "" {
						return nil, fmt.Errorf("lead is missing hosted instructions")
					}
					switch call := calls.Add(1); call {
					case 1:
						if !bytes.Contains(wire["tools"], []byte(`"name":"exec_shell"`)) || !bytes.Contains(wire["tools"], []byte(`"lead_summary"`)) {
							return nil, fmt.Errorf("lead did not discover tool and compaction controls")
						}
						return werkLeadFixtureCall("exec_shell", "exec-fixture", map[string]any{"session_key": key, "cmd": "printf fixture"})
					case 2:
						if !bytes.Contains(wire["input"], []byte("denied")) {
							return nil, fmt.Errorf("model did not receive the exec denial")
						}
						return werkLeadFixtureCall("compact", "compact-fixture", map[string]any{
							"session_key": key, "resume": false, "lead_summary": map[string]string{
								"user_preferences": "Fixture preference", "working_practices": "", "unrecorded_decisions": "",
								"current_work": "Fixture compaction", "next_step": "", "mood_rapport": "", "residual_details": "",
							},
						})
					case 3, 4:
						if call == 3 && !bytes.Contains(wire["input"], []byte("queued")) {
							return nil, fmt.Errorf("model did not receive compact acknowledgement")
						}
						if call == 4 && (!bytes.Contains(wire["input"], []byte("Fixture compaction")) ||
							!bytes.Contains(wire["input"], []byte("Continue after replacement"))) {
							return nil, fmt.Errorf("rejoined lead did not send its checkpoint and next prompt")
						}
						raw := fmt.Sprintf(`{"type":"response.completed","response":{"id":"lead-answer-%d","status":"completed","output":[{"type":"message","id":"msg_%d","role":"assistant","content":[{"type":"output_text","text":"Hosted lead answered."}]}],"usage":{"input_tokens":300,"output_tokens":8}}}`, call, call)
						return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
							Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: " + raw + "\n\n"))}, nil
					default:
						return nil, fmt.Errorf("unexpected additional model request")
					}
				})
			})
			open := f.open(nil)
			lister := open.backends[wsagent.BackendWerk].Backend.(wsagent.ChoiceLister)
			models, _, err := lister.Choices(context.Background())
			if err != nil || len(models) != 2 {
				t.Fatalf("expected both provider catalogs, got %d models: %v", len(models), err)
			}
			st, err := open.runner.CreateLead(wsagent.BackendWerk, f.root, wsagent.LeadOptions{Provider: string(tc.id), Model: "vendor/fixture", Effort: "high"})
			if err != nil {
				t.Fatal(err)
			}
			if !st.Lead || st.Provider != string(tc.id) || st.PermissionLevel != wsagent.PermissionWorkdirWrite {
				t.Fatalf("wrong lead registration, provider or permissions")
			}
			if _, err = open.runner.Send(st.ID, "Run the fixture"); err != nil {
				t.Fatal(err)
			}
			waiting := waitWerkLead(t, open.runner, st.ID, 10*time.Second, func(st wsagent.State) bool { return len(st.Approvals) == 1 })
			prompt := waiting.Approvals[0]
			if prompt.Tool != "exec.shell" || !bytes.Contains(prompt.Input, []byte(`"program"`)) ||
				bytes.Contains(prompt.Input, []byte(`"session_key"`)) {
				t.Fatal("Werk approval must show the resolved exec without connection credentials")
			}
			if _, err = open.runner.AnswerApproval(st.ID, prompt.ID, false, "fixture denied"); err != nil {
				t.Fatal(err)
			}
			done := waitWerkLead(t, open.runner, st.ID, 10*time.Second, func(st wsagent.State) bool {
				return st.Compactions == 1 && st.Status == wsagent.StatusIdle
			})
			if done.ContextWindow != tc.window || done.Error != "" || len(done.Approvals) != 0 {
				t.Fatalf("unexpected final usage, approval or error state (window %d)", done.ContextWindow)
			}
			open.daemon.Close()
			open = f.open(nil)
			rejoined, err := open.runner.Get(st.ID)
			if err != nil || !rejoined.Lead || rejoined.Provider != string(tc.id) || rejoined.PermissionLevel != st.PermissionLevel {
				t.Fatalf("rejoined lead metadata lost its provider or permissions: %v", err)
			}
			if _, err := open.runner.Send(st.ID, "Continue after replacement"); err != nil {
				t.Fatal(err)
			}
			resumed, err := open.runner.Wait(context.Background(), st.ID, 10*time.Second)
			if err != nil || resumed.Status != wsagent.TurnDone || resumed.Message != "Hosted lead answered." {
				t.Fatalf("compacted lead did not continue after replacement: %s, %v", resumed.Status, err)
			}
			for i := 0; i < 4; i++ {
				req := awaitWerkFixture(t, source.requests, "interactive credential request")
				if req.Background || !req.AgentSession || req.Provider != tc.id {
					t.Fatalf("wrong credential scope: %+v", req)
				}
			}
			if tc.id == wscred.ProviderOpenAI {
				mark := wsinbox.Cron{ID: "fixture", Name: "Fixture", At: time.Now().Format(time.RFC3339)}
				if accepted, err := open.runner.DeliverCron(st.ID, mark, "Scheduled fixture"); err != nil || !accepted {
					t.Fatalf("queue scheduled prompt: %v", err)
				}
				req := awaitWerkFixture(t, source.requests, "background credential request")
				if !req.Background || !req.AgentSession || req.Provider != tc.id {
					t.Fatalf("wrong credential scope: %+v", req)
				}
				waitWerkLead(t, open.runner, st.ID, 10*time.Second, func(st wsagent.State) bool {
					return st.Status == wsagent.StatusIdle && st.Error != ""
				})
				if calls.Load() != 4 {
					t.Fatal("scheduled prompt reached SIWC without consent")
				}
			}
		})
	}
}

func TestWerkLeadApprovalHonorsLivePermissionAndCommitsOnce(t *testing.T) {
	f := newWerkDaemonFixture(t)
	var calls atomic.Int32
	installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper {
		return werkFixtureRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.URL.Host != "openrouter.ai" {
				return wrapped.RoundTrip(req)
			}
			var wire map[string]json.RawMessage
			defer req.Body.Close()
			if err := json.NewDecoder(req.Body).Decode(&wire); err != nil {
				return nil, err
			}
			switch n := calls.Add(1); n {
			case 1:
				return werkLeadFixtureCall("fs_write", "allow-file", map[string]any{"path": "allowed.txt", "content": "approved"})
			case 2:
				if !bytes.Contains(wire["input"], []byte("allowed.txt")) {
					return nil, fmt.Errorf("the first write did not return a tool result")
				}
				return werkLeadFixtureCall("fs_write", "deny-file", map[string]any{"path": "blocked.txt", "content": "blocked"})
			case 3:
				if bytes.Contains(wire["tools"], []byte(`"name":"fs_write"`)) ||
					!bytes.Contains(wire["input"], []byte("became read-only")) {
					return nil, fmt.Errorf("the active turn did not apply the new read-only mode")
				}
				raw := `{"type":"response.completed","response":{"id":"permission-done","status":"completed","output":[{"type":"message","id":"permission-msg","role":"assistant","content":[{"type":"output_text","text":"done"}]}]}}`
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("event: response.completed\ndata: " + raw + "\n\n"))}, nil
			default:
				return nil, fmt.Errorf("unexpected model request %d", n)
			}
		})
	})
	open := f.open(nil)
	st, err := open.runner.CreateLead(wsagent.BackendWerk, f.root, wsagent.LeadOptions{Model: "vendor/fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open.runner.Send(st.ID, "Try both fixture writes."); err != nil {
		t.Fatal(err)
	}
	first := waitWerkLead(t, open.runner, st.ID, 10*time.Second, func(st wsagent.State) bool { return len(st.Approvals) == 1 })
	if first.ActiveMode != "workspace-write" || first.Approvals[0].Tool != "fs.write" {
		t.Fatal("the workspace-write lead did not ask before the file mutation")
	}
	if _, err := os.Stat(filepath.Join(f.root, "allowed.txt")); !os.IsNotExist(err) {
		t.Fatal("the file write ran before approval")
	}
	if _, err := open.runner.AnswerApproval(st.ID, first.Approvals[0].ID, true, ""); err != nil {
		t.Fatal(err)
	}
	second := waitWerkLead(t, open.runner, st.ID, 10*time.Second, func(st wsagent.State) bool {
		return len(st.Approvals) == 1 && st.Approvals[0].ID != first.Approvals[0].ID
	})
	if got, err := os.ReadFile(filepath.Join(f.root, "allowed.txt")); err != nil || string(got) != "approved" {
		t.Fatal("the explicitly approved write did not reach the worktree")
	}
	mode, err := open.runner.SetPermissionLevel(st.ID, wsagent.PermissionReadOnly)
	if err != nil || mode.ActiveMode != "read-only" {
		t.Fatalf("read-only mode was not applied in the running turn: %v", err)
	}
	if _, err := open.runner.AnswerApproval(st.ID, second.Approvals[0].ID, true, ""); err != nil {
		t.Fatal(err)
	}
	done, err := open.runner.Wait(context.Background(), st.ID, 10*time.Second)
	if err != nil || done.Status != wsagent.TurnDone || done.Message != "done" {
		t.Fatalf("the fixture lead did not finish: %s, %v", done.Status, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatal("the second write ignored the live read-only mode")
	}
}

func TestWerkTierUsesSIWCOnlyWithBackgroundConsent(t *testing.T) {
	f := newWerkDaemonFixture(t)
	var consent atomic.Bool
	source := werkLeadCredential{requests: make(chan wscred.Request, 2), consented: &consent}
	f.credential = source
	f.configured = func(provider string) (bool, error) { return provider == "openai", nil }
	if _, err := wsconfig.SetAgentTierValue(f.config, true, "small", wsagent.BackendWerk, wsconfig.AgentTier{
		Model: "fixture", Provider: "openai",
	}); err != nil {
		t.Fatal(err)
	}
	installWerkFixtureTransport(t, func(wrapped http.RoundTripper) http.RoundTripper {
		return werkFixtureRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Hostname() {
			case "localhost", "127.0.0.1":
				return wrapped.RoundTrip(req)
			case "api.openai.com":
				return werkFixtureReply("Worker ran with consent.")
			default:
				return nil, fmt.Errorf("unexpected fixture endpoint")
			}
		})
	})
	open := f.open(nil)
	caller := wsagent.LeadCaller("boundworkertestparent", f.root, wsagent.BackendClaude, f.config)
	request := wsagent.AgentSpawnRequest{Tier: "werk.small", Prompt: "Run the fixture", Authority: "read-only"}
	if _, err := open.runner.AgentSpawn(caller, request); err == nil || !strings.Contains(err.Error(), "background session") {
		t.Fatalf("spawn without consent: %v", err)
	}
	consent.Store(true)
	spawned, err := open.runner.AgentSpawn(caller, request)
	if err != nil {
		t.Fatal(err)
	}
	req := awaitWerkFixture(t, source.requests, "worker credential request")
	if req.Provider != wscred.ProviderOpenAI || !req.Background || !req.AgentSession {
		t.Fatalf("wrong worker credential scope: %+v", req)
	}
	report := awaitWerkFixture(t, f.reports, "worker report")
	if "id:"+report.Result.ID != spawned.Address || report.Result.Status != wsagent.TurnDone || report.Child.Provider != "openai" {
		t.Fatal("worker did not complete on the selected provider")
	}
}

func werkLeadFixtureCall(name, id string, args map[string]any) (*http.Response, error) {
	raw, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	return werkReplacementResponse(map[string]any{
		"type": "function_call", "id": "fc_" + id, "call_id": id, "name": name, "arguments": string(raw),
	})
}

// TestWerkLiveHostedLead uses an explicitly chosen model and an owner-prepared
// credential store. SIWC refresh can write to that store, so use a fixture copy.
func TestWerkLiveHostedLead(t *testing.T) {
	provider := wscred.ProviderID(os.Getenv("WERK_LIVE_LEAD_PROVIDER"))
	if provider == "" {
		t.Skip("set WERK_LIVE_LEAD_PROVIDER to opt in")
	}
	if provider != wscred.ProviderOpenRouter && provider != wscred.ProviderOpenAI {
		t.Fatal("WERK_LIVE_LEAD_PROVIDER must be openrouter or openai")
	}
	model, path := os.Getenv("WERK_LIVE_LEAD_MODEL"), os.Getenv("WERK_LIVE_CREDENTIAL_PATH")
	if model == "" || path == "" {
		t.Fatal("set WERK_LIVE_LEAD_MODEL and WERK_LIVE_CREDENTIAL_PATH to the fixture credentials file")
	}
	f := newWerkDaemonFixture(t)
	store := wscred.NewStore(path)
	f.credential, f.configured, f.log = store, credentialStatus(store), io.Discard
	open := f.open(nil)
	st, err := open.runner.CreateLead(wsagent.BackendWerk, f.root, wsagent.LeadOptions{
		Provider: string(provider), Model: model, PermissionLevel: wsagent.PermissionReadOnly,
	})
	if err != nil {
		t.Fatal("could not create the hosted lead with the chosen provider and model")
	}
	if _, err := open.runner.Send(st.ID, "Reply briefly with the word READY."); err != nil {
		t.Fatal("could not send the live fixture prompt")
	}
	got := waitWerkLead(t, open.runner, st.ID, 2*time.Minute, func(st wsagent.State) bool {
		return st.Status == wsagent.StatusIdle && (st.Observed != nil || st.Error != "")
	})
	if got.Observed == nil || got.Error != "" {
		t.Fatal("the live provider did not complete the hosted lead turn")
	}
}
