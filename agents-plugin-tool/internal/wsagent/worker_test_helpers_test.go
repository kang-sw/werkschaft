package wsagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsholder"
)

type recoveryPythonHost struct {
	ManagerHost
	python, script string
}

func (h recoveryPythonHost) Spawn(id string, spec wsholder.SpawnSpec) (wsholder.ChildStatus, error) {
	spec.Argv = append([]string{h.python, "-I", "-u", h.script}, spec.Argv[1:]...)
	return h.ManagerHost.Spawn(id, spec)
}

// Some Linux test hosts do not reap orphaned holders. This predates worker
// recovery: tolerate only Stop's liveness timeout for a verified zombie whose
// holder already removed its descriptor, never a live or unidentified process.
func recoveryStoppedLinuxZombie(dir string, pid int, stopErr error) bool {
	if runtime.GOOS != "linux" || pid <= 0 || stopErr.Error() != fmt.Sprintf("holder process %d did not end", pid) {
		return false
	}
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	// comm is parenthesized and can itself contain spaces and parentheses.
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return false
	}
	fields := strings.Fields(string(raw)[end+1:])
	if len(fields) == 0 || fields[0] != "Z" {
		return false
	}
	_, err = wsholder.ReadDescriptor(dir)
	return os.IsNotExist(err)
}

func newHeldWorkerHarness(t *testing.T) (*leadHarness, *reports) {
	t.Helper()
	h := newLeadHarness(t)
	got := &reports{}
	h.opts.Report = got.add
	h.opts.WSMCP = func(string) (MCPCommand, error) { return MCPCommand{Command: "/unused/ws-mcp"}, nil }
	for tier, model := range map[string]string{"small": "haiku", "medium": "sonnet"} {
		if _, err := wsconfig.SetGlobalAgentsTierForHarness(h.opts.Config, tier, "", model, BackendClaude, "low"); err != nil {
			t.Fatal(err)
		}
	}
	h.endpoint = "http://127.0.0.1:1"
	h.start()
	return h, got
}

func spawnRecoveryWorker(t *testing.T, h *leadHarness) (State, string) {
	t.Helper()
	res, err := h.runner.AgentSpawn(LeadCaller(leadParent.ReplyID, h.root, BackendClaude, h.opts.Config), AgentSpawnRequest{Prompt: "work", Tier: "claude.medium", Authority: "workdir-write"})
	if err != nil {
		t.Fatal(err)
	}
	worker := h.state(agentID(res.Address))
	holder := h.holder(worker.ID)
	h.holders.out(holder, stateLine(HarnessRunning), initLine("worker-s1", "dontAsk"))
	eventually(t, "persisted worker stream", func() bool { return h.record(worker.ID).HolderSeq > 0 })
	return worker, holder
}

func recoveryWorkerReply(text string) []string {
	return []string{replyLine("worker-turn", text), resultLine(), stateLine(HarnessIdle)}
}

func recoveryTokenCall(t *testing.T, endpoint, token, op string, body any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+RoutePrefix+"/by-token/"+op, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(answer)
}
