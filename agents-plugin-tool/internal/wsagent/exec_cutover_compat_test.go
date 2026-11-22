package wsagent

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// The v0.51.0 readers a rolled-back daemon runs over a record a cutover
// process left, ported from f800ba30d with their conditions unchanged. They
// read only the members v0.51.0's Record knows; ExecCutoverHolder
// (exec_cutover_holder_id) is not one of them, so v0.51.0 neither reads nor
// drops it (Record.UnmarshalJSON keeps unknown top-level members and
// MarshalJSON writes them back: wsagent/record.go:196-258).

// v051Lead is what the lead rejoin does with a recorded ws-mcp session
// (wsagent/runner_process.go:435-451 at v0.51.0): "forget" voids the token,
// "stale" leaves the process without ws-mcp until an idle restart, "attach"
// restores the token the process still sends.
func v051Lead(rec Record, daemonURL string) string {
	switch {
	case rec.MCPToken != "" && rec.MCPHolder != "" && rec.MCPHolder != rec.HolderID:
		return "forget"
	case rec.MCPToken != "" && (rec.MCPURL != daemonURL || rec.MCPEnv == nil):
		return "stale"
	case rec.MCPToken != "":
		return "attach"
	}
	return "none"
}

// v051Worker is restoreWorkerMCP at v0.51.0 (wsagent/worker_rejoin.go:23-36):
// whether the worker's token is restored.
func v051Worker(rec Record, daemonURL string) bool {
	return !(rec.MCPToken == "" || rec.MCPHolder != rec.HolderID || rec.MCPURL != daemonURL || rec.MCPEnv == nil)
}

// v051Rewrite is a v0.51.0 daemon's read-modify-write of the record: a known
// member changes (the holder cursor), every other member, the cutover's
// among them, is written back as read.
func v051Rewrite(t *testing.T, dir string) {
	t.Helper()
	path := recordPath(dir)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	members["holder_seq"] = json.RawMessage("42")
	out, err := json.Marshal(members)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A cutover lead's record keeps the plain ws-mcp route a v0.51.0 daemon on
// the same port reattaches, and its cutover survives a v0.51.0 rewrite: new
// -> v0.51.0 -> new loses neither the token nor the cutover.
func TestExecCutoverLeadRecordIsReattachedByV051(t *testing.T) {
	h := cutoverHarness(t)
	st := h.lead(BackendClaude, LeadOptions{PermissionMode: "default"})
	_, rec := leadLaunch(t, h, st.ID)
	url := h.runner.mcpURL()
	if rec.ExecCutoverHolder == "" || rec.ExecCutoverHolder != rec.HolderID {
		t.Fatalf("the lead did not start under the cutover: holder %q, cutover holder %q", rec.HolderID, rec.ExecCutoverHolder)
	}
	if rec.MCPURL != url {
		t.Fatalf("cutover lead records route %q, want the plain %q", rec.MCPURL, url)
	}
	if got := v051Lead(rec, url); got != "attach" {
		t.Fatalf("a v0.51.0 daemon's lead rejoin: %s", got)
	}

	// The v0.51.0 daemon reattaches the token and rewrites the record.
	h.runner.Close()
	h.holders.restart()
	dir := wsdashboard.SessionDir(h.dir, st.ID)
	v051Rewrite(t, dir)
	rewritten, err := readRecord(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rewritten.ExecCutoverHolder != rec.ExecCutoverHolder || rewritten.HolderSeq != 42 {
		t.Fatalf("v0.51.0 rewrite: cutover holder %q, holder seq %d", rewritten.ExecCutoverHolder, rewritten.HolderSeq)
	}

	// Reopened, this daemon restores the cutover from the record.
	h.startAt("http://127.0.0.1:1")
	h.quiesce(st.ID)
	if hello, _, ok := h.runner.ResolveMCPToken(rec.MCPToken); !ok || hello.ExecCutover != BackendClaude {
		t.Fatalf("reopened token hello = %+v %v", hello, ok)
	}
}

// A cutover worker's record is restored by a v0.51.0 daemon as well.
func TestExecCutoverWorkerRecordIsReattachedByV051(t *testing.T) {
	h, _, id, _ := startAgyCutoverWorker(t, nil)
	rec := h.record(id)
	url := h.runner.mcpURL()
	if rec.MCPURL != url || rec.MCPToken == "" || rec.MCPEnv == nil || rec.MCPHolder != rec.HolderID {
		t.Fatalf("cutover worker's ws-mcp record: %+v (daemon route %q)", rec, url)
	}
	if !v051Worker(rec, url) {
		t.Fatal("a v0.51.0 daemon's worker rejoin skips the token")
	}
	if rec.ExecCutoverHolder != rec.HolderID {
		t.Fatalf("cutover holder %q, holder %q", rec.ExecCutoverHolder, rec.HolderID)
	}
}

// A record an unreleased development build marked with the old route
// fragment is still this daemon's route.
func TestExecCutoverLegacyRouteMarkIsStillServed(t *testing.T) {
	h := cutoverHarness(t)
	url := h.runner.mcpURL()
	if !h.runner.servesMCPURL(url) || !h.runner.servesMCPURL(url+legacyExecCutoverRouteMark) || h.runner.servesMCPURL(url+"#other") || h.runner.servesMCPURL("") {
		t.Fatal("servesMCPURL")
	}
}
