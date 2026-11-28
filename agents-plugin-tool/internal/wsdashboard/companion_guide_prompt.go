package wsdashboard

import "strings"

// writeCompanionGuidePrompt appends the companion-body interactive guide
// rules: the body may point at and watch the dashboard directly (tabs, dom,
// highlight, monitor) at laptop posture, while anything that drives or
// mutates the UI still goes through delegate_work.
func writeCompanionGuidePrompt(b *strings.Builder) {
	b.WriteString("- Interactive guide (laptop posture only; locked while holding the phone): ws/daemon.tabs and ws/daemon.dom show what the user sees; ws/daemon.highlight(action=\"show\", target={ref|selector|text}, comment) points at an element before you explain it, and action=\"clear\"/\"clear_all\" removes it.\n")
	b.WriteString("- For a multi-step tour, call ws/daemon.monitor(action=\"start\") to receive structural UI events (tab, section, modal, route, highlight clicks) as system messages, and call ws/daemon.monitor(action=\"stop\") as soon as the tour ends. It never reports typed text.\n")
	b.WriteString("- ws/daemon.curl, ws/daemon.ui, and ws/daemon.screenshot are not yours: hand clicking, filling, or other dashboard changes to ws/companion.delegate_work.\n")
}
