package mcp

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/execjob"
	"github.com/kang-sw/devenv/internal/wsagent"
)

// toolSchemaByteCap is the most a model-visible tool may cost as tools/list
// serves it: name, description, and input schema, marshaled with Go's
// encoding/json defaults (so <, >, and & cost six bytes each). agy adds a
// 39 B description prefix and drops definitions over 1,500 B; 1,450 leaves
// about 10 B of margin. This test limit is unrelated to CallFallback admission.
const toolSchemaByteCap = 1450

type listedToolSize struct {
	name string
	size int
}

// listedToolSizes measures every tool a server's tools/list answer shows.
func listedToolSizes(t *testing.T, s *Server) []listedToolSize {
	t.Helper()
	var resp struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				InputSchema any    `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	raw := callToolsList(t, s)
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("tools/list: %v: %.200s", err, raw)
	}
	if len(resp.Result.Tools) == 0 {
		t.Fatalf("tools/list listed no tools: %.200s", raw)
	}
	sizes := make([]listedToolSize, 0, len(resp.Result.Tools))
	for _, tool := range resp.Result.Tools {
		encoded, err := json.Marshal(struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			InputSchema any    `json:"inputSchema"`
		}{tool.Name, tool.Description, tool.InputSchema})
		if err != nil {
			t.Fatalf("%s: %v", tool.Name, err)
		}
		sizes = append(sizes, listedToolSize{tool.Name, len(encoded)})
	}
	sort.Slice(sizes, func(i, j int) bool {
		if sizes[i].size != sizes[j].size {
			return sizes[i].size > sizes[j].size
		}
		return sizes[i].name < sizes[j].name
	})
	return sizes
}

// TestListedToolSchemasFitByteCap: every tool tools/list shows fits
// toolSchemaByteCap, on each surface that lists a different set or text:
// ws and wsflow (whose namespace rewrite lengthens text), with the daemon's
// runner (agent.* listed), and as a forwarding worker (agent.* rewritten).
// The agy alias spelling only swaps "." for "_" in the name, so it costs
// what the canonical list costs. A failure lists every tool over the cap,
// largest first.
func TestListedToolSchemasFitByteCap(t *testing.T) {

	wsflow := dashboardPackages[1]
	for _, tc := range []struct {
		name      string
		manifests string
		namespace string
		forward   bool
		cutover   string
	}{
		{name: "ws lead", manifests: wsManifests()},
		{name: "wsflow lead", manifests: wsflow.own + ";" + wsflow.dashboard, namespace: "wsflow"},
		{name: "ws forwarding worker", manifests: wsManifests(), forward: true},
		{name: "ws cutover lead", manifests: wsManifests(), cutover: wsagent.BackendAgy},
		{name: "wsflow cutover lead", manifests: wsflow.own + ";" + wsflow.dashboard, namespace: "wsflow", cutover: wsagent.BackendAgy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envNamespace, tc.namespace)
			var agents AgentHost
			if tc.forward {
				t.Setenv(wsagent.EnvPiToken, "token")
				t.Setenv(wsagent.EnvPiEndpoint, "http://127.0.0.1:1")
			} else {
				agents = newFakePiAgents()
			}
			f := agentLeadFixture(t, tc.manifests, agents)
			if tc.cutover != "" {
				hosted := &execjob.Hosted{}
				f.server.conversation = "lead"
				f.server.execCutover = tc.cutover
				f.server.exec = func() *execjob.Hosted { return hosted }
			}
			sizes := listedToolSizes(t, f.server)
			var over []string
			seenExec := map[string]int{}
			for _, tool := range sizes {
				if slices.Contains(cutoverExecTools, tool.name) {
					seenExec[tool.name] = tool.size
				}
				if tool.size > toolSchemaByteCap {
					over = append(over, fmt.Sprintf("%s: %d B", tool.name, tool.size))
				}
			}
			if tc.cutover != "" {
				for _, name := range cutoverExecTools {
					sz, ok := seenExec[name]
					if !ok {
						t.Errorf("cutover surface missing %s", name)
					} else {
						t.Logf("%s: %d B", name, sz)
					}
				}
			}
			if len(over) > 0 {
				t.Errorf("%d listed tool(s) over the %d B cap, largest first:\n%s", len(over), toolSchemaByteCap, strings.Join(over, "\n"))
			}
		})
	}
}
