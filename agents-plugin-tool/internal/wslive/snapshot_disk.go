package wslive

import "reflect"

// ForDisk returns the mutable recovery-cache projection. Live reasoning summaries
// and live ws/exec background counts belong only to RAM/live delivery.
// Keep ordinary JSON encoding unchanged for in-memory HostedSlot publication.
func (s Snapshot) ForDisk() Snapshot {
	s.Lead.ReasoningSummary = nil
	if s.Host != nil && (s.Host.BgExecCount != 0 || s.Host.BgExecSelf != 0 || s.Host.BgExecDesc != 0) {
		h := s.Host.clone()
		h.BgExecCount, h.BgExecSelf, h.BgExecDesc = 0, 0, 0
		if reflect.DeepEqual(*h, Host{}) {
			s.Host = nil
		} else {
			s.Host = h
		}
	}
	return s
}
