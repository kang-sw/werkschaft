package wsagent

import "github.com/kang-sw/devenv/internal/wsdashboard"

// werkDashboardTraits: the daemon's replayed initialize presents the name the
// harness's own MCP client announces (werk/mcpclient.go), so the server
// resolves the replayed session as it did the live one.
func werkDashboardTraits() wsdashboard.HarnessTraits {
	return wsdashboard.HarnessTraits{ReplayClientName: "ws-werk"}
}
