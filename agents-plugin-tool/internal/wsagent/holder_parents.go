package wsagent

// AgentParents answers, for each of ids this runner knows as a spawned
// child, the reply id of the session that spawned it, from the live child
// index (wsdashboard.AgentParentHost). The dashboard's holder listing uses
// it to nest a holder whose descriptor predates parent_reply_id under its
// lead; an id without a known parent is absent.
func (r *Runner) AgentParents(ids []string) map[string]string {
	out := map[string]string{}
	if len(ids) == 0 {
		return out
	}
	want := make(map[string]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for parent, kids := range r.childrenIndex {
		for id := range kids {
			if want[id] {
				out[id] = parent.ReplyID
			}
		}
	}
	return out
}
