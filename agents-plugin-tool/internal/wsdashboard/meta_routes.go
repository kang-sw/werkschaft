package wsdashboard

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
	"github.com/kang-sw/devenv/internal/wsstate"
)

const (
	MetaAgentRecordPath      = "/meta/agent-record"
	MetaHostedDescendantPath = "/meta/hosted-descendant"
	MetaUnregisterCheckPath  = "/meta/unregister-check"
	MetaPrunePath            = "/meta/prune"

	metaBodyLimit          = 64 << 10
	defaultMetaLineageDepth = 16
	maxMetaLineageDepth     = 64
)

var _ MetaPruner = (*Daemon)(nil)

type metaAgentRecordRequest struct {
	ReplyID string `json:"reply_id"`
	WSKey   string `json:"ws_key"`
}

type metaAgentRecordResponse struct {
	Found  bool                 `json:"found"`
	Record *AgentRecordSnapshot `json:"record,omitempty"`
}

type metaHostedDescendantRequest struct {
	HostedReplyID string `json:"hosted_reply_id"`
	ReplyID       string `json:"reply_id"`
	WSKey         string `json:"ws_key"`
	MaxDepth      int    `json:"max_depth,omitempty"`
}

type metaHostedDescendantResponse struct {
	Descendant bool `json:"descendant"`
}

type metaUnregisterCheckRequest struct {
	ReplyID string `json:"reply_id"`
}

type metaUnregisterCheckResponse struct {
	HasAgentRecord bool `json:"has_agent_record"`
}

type metaPruneRequest struct {
	RetentionNS int64 `json:"retention_ns,omitempty"`
}

type metaPruneResponse struct {
	Pruned int `json:"pruned"`
}

func (d *Daemon) enterMeta() bool {
	d.metaMu.RLock()
	if d.metaClosed {
		d.metaMu.RUnlock()
		return false
	}
	d.metaWG.Add(1)
	d.metaMu.RUnlock()
	return true
}

func (d *Daemon) leaveMeta() {
	d.metaWG.Done()
}

func (d *Daemon) closeMeta() {
	d.metaMu.Lock()
	d.metaClosed = true
	d.metaMu.Unlock()
	d.metaWG.Wait()
}

// authorizeMeta guards loopback-only machine metadata routes: POST only, zero
// Origin/cross-site headers, loopback peer, and constant-time Bearer match
// against the daemon's endpoint.Token via d.machineAuth.
func (d *Daemon) authorizeMeta(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return false
	}
	if !localViewer(r) || len(r.Header.Values("Origin")) > 0 || crossSiteFetch(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	if !d.machineAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (d *Daemon) agentRecord(replyID string) (AgentRecordSnapshot, bool) {
	if d == nil || d.agents == nil || !ValidReplyID(replyID) {
		return AgentRecordSnapshot{}, false
	}
	host, ok := d.agents.(AgentMetadataHost)
	if !ok {
		return AgentRecordSnapshot{}, false
	}
	return host.AgentRecord(replyID)
}

func wsKeyMatches(stored, supplied string) bool {
	return stored != "" && supplied != "" && subtle.ConstantTimeCompare([]byte(stored), []byte(supplied)) == 1
}

func (d *Daemon) handleMetaAgentRecord(w http.ResponseWriter, r *http.Request) {
	if !d.authorizeMeta(w, r) {
		return
	}
	if !d.enterMeta() {
		http.Error(w, "daemon shutting down", http.StatusServiceUnavailable)
		return
	}
	defer d.leaveMeta()

	var req metaAgentRecordRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, metaBodyLimit)).Decode(&req); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	if !ValidReplyID(req.ReplyID) || req.WSKey == "" {
		writeJSON(w, http.StatusOK, metaAgentRecordResponse{Found: false})
		return
	}
	snap, ok := d.agentRecord(req.ReplyID)
	if !ok || !wsKeyMatches(snap.WSKey, req.WSKey) {
		writeJSON(w, http.StatusOK, metaAgentRecordResponse{Found: false})
		return
	}
	snap.WSKey = ""
	writeJSON(w, http.StatusOK, metaAgentRecordResponse{Found: true, Record: &snap})
}

func (d *Daemon) handleMetaHostedDescendant(w http.ResponseWriter, r *http.Request) {
	if !d.authorizeMeta(w, r) {
		return
	}
	if !d.enterMeta() {
		http.Error(w, "daemon shutting down", http.StatusServiceUnavailable)
		return
	}
	defer d.leaveMeta()

	var req metaHostedDescendantRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, metaBodyLimit)).Decode(&req); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	if !ValidReplyID(req.HostedReplyID) || !ValidReplyID(req.ReplyID) || req.WSKey == "" {
		writeJSON(w, http.StatusOK, metaHostedDescendantResponse{Descendant: false})
		return
	}
	maxDepth := req.MaxDepth
	if maxDepth <= 0 || maxDepth > maxMetaLineageDepth {
		maxDepth = defaultMetaLineageDepth
	}
	snap, ok := d.agentRecord(req.ReplyID)
	if !ok || !wsKeyMatches(snap.WSKey, req.WSKey) {
		writeJSON(w, http.StatusOK, metaHostedDescendantResponse{Descendant: false})
		return
	}
	for depth := 0; snap.Parent != nil && depth < maxDepth; depth++ {
		parent := snap.Parent.ReplyID
		if parent == req.HostedReplyID {
			writeJSON(w, http.StatusOK, metaHostedDescendantResponse{Descendant: true})
			return
		}
		if !ValidReplyID(parent) {
			break
		}
		next, found := d.agentRecord(parent)
		if !found {
			break
		}
		snap = next
	}
	writeJSON(w, http.StatusOK, metaHostedDescendantResponse{Descendant: false})
}

func (d *Daemon) handleMetaUnregisterCheck(w http.ResponseWriter, r *http.Request) {
	if !d.authorizeMeta(w, r) {
		return
	}
	if !d.enterMeta() {
		http.Error(w, "daemon shutting down", http.StatusServiceUnavailable)
		return
	}
	defer d.leaveMeta()

	var req metaUnregisterCheckRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, metaBodyLimit)).Decode(&req); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	if !ValidReplyID(req.ReplyID) {
		writeJSON(w, http.StatusOK, metaUnregisterCheckResponse{HasAgentRecord: false})
		return
	}
	_, found := d.agentRecord(req.ReplyID)
	writeJSON(w, http.StatusOK, metaUnregisterCheckResponse{HasAgentRecord: found})
}

func (d *Daemon) handleMetaPrune(w http.ResponseWriter, r *http.Request) {
	if !d.authorizeMeta(w, r) {
		return
	}
	if !d.enterMeta() {
		http.Error(w, "daemon shutting down", http.StatusServiceUnavailable)
		return
	}
	defer d.leaveMeta()

	var req metaPruneRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, metaBodyLimit)).Decode(&req); err != nil && err != io.EOF {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	retention := RegistryRetention
	if req.RetentionNS > 0 {
		retention = time.Duration(req.RetentionNS)
	}
	pruned := d.pruneRegistrationsInMeta(time.Now(), retention)
	writeJSON(w, http.StatusOK, metaPruneResponse{Pruned: pruned})
}

// PruneRegistrationsFor prunes expired session directories for stateDir when
// this daemon owns it, consulting daemon-owned in-memory agent metadata instead
// of reading agent.json from disk.
func (d *Daemon) PruneRegistrationsFor(stateDir string, now time.Time, retention time.Duration) (int, bool) {
	if d == nil || filepath.Clean(stateDir) != filepath.Clean(d.dir) {
		return 0, false
	}
	if !d.enterMeta() {
		// Daemon is draining; treat as handled so callers do not fall back to
		// reading legacy agent.json files directly.
		return 0, true
	}
	defer d.leaveMeta()
	return d.pruneRegistrationsInMeta(now, retention), true
}

func (d *Daemon) pruneRegistrationsInMeta(now time.Time, retention time.Duration) int {
	checker, _ := d.agents.(AgentForgetChecker)
	forgetter, _ := d.agents.(AgentForgetter)
	return pruneRegistrationsWith(d.dir, now, retention, func(replyID string) (agentRecordEnd, bool) {
		snap, ok := d.agentRecord(replyID)
		if !ok {
			return agentRecordEnd{}, false
		}
		end := agentRecordEnd{
			Lead:        snap.Lead,
			ClosedAt:    snap.ClosedAt,
			HandedOffAt: snap.HandedOffAt,
			HolderID:    snap.HolderID,
			Backend:     snap.Backend,
			Model:       snap.Model,
			Effort:      snap.Effort,
		}
		if snap.Parent != nil {
			end.Parent = &struct {
				Kind    string `json:"kind"`
				ReplyID string `json:"reply_id"`
			}{Kind: snap.Parent.Kind, ReplyID: snap.Parent.ReplyID}
		}
		return end, true
	}, func(replyID string, hosted bool) bool {
		if !hosted {
			return true
		}
		if checker != nil {
			return checker.CanForgetClosed(replyID)
		}
		if forgetter != nil {
			return forgetter.ForgetClosed(replyID)
		}
		return true
	}, func(replyID string, hosted bool) {
		if hosted && checker != nil {
			checker.ForgetClosed(replyID)
		}
		if err := d.deleteSessionMeta(replyID); err != nil && d.log != nil {
			fmt.Fprintf(d.log, "dashboard: delete pruned session metadata %s: %v\n", replyID, err)
		}
		d.deleteConvIndexSession(replyID)
		if d.index != nil {
			d.index.Reread(replyID)
		}
	})
}

// endedAt is the later of the record's close and handoff stamps; zero when
// neither parses. Used only after the retirement and Pi-history exemptions.
func (r agentRecordEnd) endedAt() time.Time {
	var last time.Time
	for _, stamp := range []string{r.ClosedAt, r.HandedOffAt} {
		if t, err := time.Parse(time.RFC3339, stamp); err == nil && t.After(last) {
			last = t
		}
	}
	return last
}

// removeSessionDir is a test seam for os.RemoveAll in pruneRegistrationsWith.
var removeSessionDir = os.RemoveAll

// pruneRegistrationsWith mirrors PruneRegistrations using lookup for agent
// record metadata instead of reading agent.json from disk. PruneRegistrations
// in registry.go stays untouched because legacy tests extract it by AST.
func pruneRegistrationsWith(dir string, now time.Time, retention time.Duration, lookup func(replyID string) (agentRecordEnd, bool), beforeRemove func(replyID string, hosted bool) bool, afterRemove func(replyID string, hosted bool)) int {
	entries, err := wsstate.ReadDir(filepath.Join(dir, sessionsDirName))
	if err != nil {
		return 0
	}
	pruned := 0
	for _, entry := range entries {
		if !entry.IsDir() || !ValidReplyID(entry.Name()) {
			continue
		}
		sessionDir := SessionDir(dir, entry.Name())
		record, hosted := lookup(entry.Name())
		if hosted && (retainsPiHistory(record.Backend) || !record.retired()) {
			continue
		}
		last := time.Time{}
		if reg, err := ReadRegistration(dir, entry.Name()); err == nil {
			if reg.Agent && !hosted {
				continue
			}
			last = reg.LastSeen()
		} else if hosted {
			// A retired lead with no readable row to age it by is left to
			// the runner.
			continue
		}
		if last.IsZero() {
			// The record's own end stamp ages the session before the
			// directory does: a post-cutover permission.json write bumps the
			// directory's ModTime and would otherwise restart the expiry.
			last = record.endedAt()
		}
		if last.IsZero() {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			last = info.ModTime()
		}
		if now.Sub(last) <= retention {
			continue
		}
		if beforeRemove != nil && !beforeRemove(entry.Name(), hosted) {
			continue
		}
		wslive.DropRing(wslive.RingDir(sessionDir))
		if removeSessionDir(sessionDir) == nil {
			if afterRemove != nil {
				afterRemove(entry.Name(), hosted)
			}
			pruned++
		}
	}
	return pruned
}
