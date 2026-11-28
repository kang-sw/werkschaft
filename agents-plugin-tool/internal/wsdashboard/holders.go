package wsdashboard

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/kang-sw/devenv/internal/wsholder"
)

// The session holders (internal/wsholder): processes that each own one
// child and outlive this daemon. The daemon attaches to every holder it
// finds at start and on each rescan, and never ends one on its own. These
// routes serve the shell's Holders popup:
//
//   - GET /holders answers every known holder (attached or not, with the
//     cause when not, and whether it is restart-safe), the recent holder
//     start failures, can_stop: whether this viewer may stop one, and
//     parents: the live parent of a holder whose descriptor names none.
//   - GET /holders/metrics is the open popup's resource trace
//     (holders_metrics.go).
//   - POST /holders/<id>/stop ends a holder and its child: through the
//     protocol when attached, otherwise with the kill guarded by the
//     holder's recorded start time. A holder may own a terminal, so the
//     route is for a browser on this machine only (localViewer; a LAN
//     viewer, token or not, gets 404, as if the route did not exist) that
//     sends the daemon's own Origin (sameOrigin, as the write routes do).
//     It answers 204, 404 for an unknown holder, and 409 with the reason
//     when the kill was refused or the holder did not end.

// HolderHost is the session holder manager the daemon runs
// (*wsholder.Manager). Run scans at once and rescans until Close; Close
// detaches from every holder and ends none. StopAllFenced ends every holder for
// the full stop route (handleShutdownAll), holding its launch fence until
// the daemon shuts down or the caller releases it after a partial stop.
type HolderHost interface {
	Run()
	Close()
	List() wsholder.Listing
	Stop(id string) error
	StopAllFenced() (wsholder.StopAllResult, func())
}

// DurableSessionHost is the agent runner's Full Quit surface. A durable
// session has a lifetime in the runner even if its loop is asleep.
type DurableSessionHost interface {
	DurableSessions() []DurableSession
	StopDurableSession(id string) error
	FenceDurableStarts() (resume func())
}

// DurableSession carries only the identity and availability of a live session.
type DurableSession struct {
	ID          string
	Unavailable bool
}

var ErrUnknownDurableSession = errors.New("unknown durable session")

// The dot cannot occur in a process holder ID.
const durableHolderPrefix = "durable."

func (d *Daemon) durableSessionHost() DurableSessionHost {
	host, _ := d.agents.(DurableSessionHost)
	return host
}

// holdersOffered reports whether /holders answers with anything: a holder
// manager, or a host of durable in-process sessions, which the listing
// merges in. Pages show the Holders surface on the same condition.
func (d *Daemon) holdersOffered() bool {
	return d.holders != nil || d.durableSessionHost() != nil
}

func durableHolderInfo(session DurableSession) wsholder.Info {
	return wsholder.Info{ID: durableHolderPrefix + session.ID, Attached: !session.Unavailable, RestartSafe: true}
}

// AgentParentHost is an AgentHost that knows which session spawned each of
// its conversations: the holders route nests a worker's holder whose
// descriptor predates parent_reply_id under its lead with it.
type AgentParentHost interface {
	// AgentParents answers the spawning session's reply id of each of
	// replyIDs that has one.
	AgentParents(replyIDs []string) map[string]string
}

// holderList is the holders route's answer.
type holderList struct {
	wsholder.Listing
	// CanStop reports whether this viewer may use the stop route.
	CanStop bool `json:"can_stop"`
	// Parents is the spawning session's reply id, by holder id, of each
	// holder whose session descriptor names none (an older descriptor)
	// while the agent host knows one.
	Parents map[string]string `json:"parents,omitempty"`
}

// holderParents looks up, in the agent host, the parent of each holder
// whose descriptor has a reply id but no parent_reply_id.
func (d *Daemon) holderParents(holders []wsholder.Info) map[string]string {
	host, ok := d.agents.(AgentParentHost)
	if !ok {
		return nil
	}
	byReply := map[string]string{}
	var ids []string
	for _, h := range holders {
		var s struct {
			ReplyID       string `json:"reply_id"`
			ParentReplyID string `json:"parent_reply_id"`
		}
		if len(h.Session) == 0 || json.Unmarshal(h.Session, &s) != nil || s.ReplyID == "" || s.ParentReplyID != "" {
			continue
		}
		byReply[s.ReplyID] = h.ID
		ids = append(ids, s.ReplyID)
	}
	if len(ids) == 0 {
		return nil
	}
	out := map[string]string{}
	for reply, parent := range host.AgentParents(ids) {
		if id, ok := byReply[reply]; ok && parent != "" {
			out[id] = parent
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// handleHolders serves GET /holders, GET /holders/metrics
// (holders_metrics.go), and POST /holders/<id>/stop.
func (d *Daemon) handleHolders(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/holders"), "/")
	if rest == "metrics" {
		d.serveHolderMetrics(w, r)
		return
	}
	if rest == "" {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		listing := wsholder.Listing{Holders: []wsholder.Info{}, Failures: []wsholder.StartFailure{}}
		if d.holders != nil {
			listing = d.holders.List()
		}
		if host := d.durableSessionHost(); host != nil {
			for _, session := range host.DurableSessions() {
				listing.Holders = append(listing.Holders, durableHolderInfo(session))
			}
		}
		_ = json.NewEncoder(w).Encode(holderList{Listing: listing, CanStop: localViewer(r), Parents: d.holderParents(listing.Holders)})
		return
	}
	id, ok := strings.CutSuffix(rest, "/stop")
	sessionID, durable := strings.CutPrefix(id, durableHolderPrefix)
	if !ok || !localViewer(r) || (durable && !ValidReplyID(sessionID)) || (!durable && !wsholder.ValidID(id)) {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var err error
	if durable {
		if host := d.durableSessionHost(); host != nil {
			err = host.StopDurableSession(sessionID)
		} else {
			err = ErrUnknownDurableSession
		}
	} else if d.holders != nil {
		err = d.holders.Stop(id)
	} else {
		err = wsholder.ErrUnknownHolder
	}
	switch {
	case err == nil:
		if !durable && d.terminals != nil {
			// A stopped terminal's viewers learn of it now.
			d.terminals.sync()
		}
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, wsholder.ErrUnknownHolder), errors.Is(err, ErrUnknownDurableSession):
		http.NotFound(w, r)
	default:
		http.Error(w, err.Error(), http.StatusConflict)
	}
}
