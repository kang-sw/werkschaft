package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsdashboard"
	"github.com/kang-sw/devenv/internal/wsmailbox"
	"github.com/kang-sw/devenv/internal/wsstate"
)

// AgentRecordReader exposes in-memory daemon agent records without touching
// agent.json on disk. Implemented by *wsagent.Runner.
type AgentRecordReader interface {
	RecordByReplyID(replyID string) (wsagent.Record, bool)
}

func (s *Server) agentRecordReader() AgentRecordReader {
	if s.recordReader != nil {
		return s.recordReader
	}
	if s.agents != nil {
		if reader, ok := s.agents.(AgentRecordReader); ok {
			return reader
		}
	}
	return nil
}

func (s *Server) daemonUnavailableError(dir string, cause error) error {
	cfgOpts := s.configOptions()
	cacheRoot, err := wsstate.CacheRoot(wsstate.Options{CacheHome: cfgOpts.CacheHome, Env: s.environ()})
	if err != nil || strings.TrimSpace(cacheRoot) == "" {
		cacheRoot = dir
	}
	configRoot := cfgOpts.ConfigHome
	if globalPath, err := wsconfig.GlobalPath(cfgOpts); err == nil && strings.TrimSpace(globalPath) != "" {
		configRoot = filepath.Dir(globalPath)
	}
	rootDesc := fmt.Sprintf("cache root %s", cacheRoot)
	if configRoot != "" {
		rootDesc = fmt.Sprintf("cache root %s, config root %s", cacheRoot, configRoot)
	}
	if s.directStdio {
		return fmt.Errorf("dashboard daemon is unavailable for %s (recoverable; start or enable the daemon with `ws-mcp daemon-start` for the same root and retry): %w", rootDesc, cause)
	}
	return fmt.Errorf("dashboard daemon is unavailable for %s: run `ws-mcp daemon-start` for the same root: %w", rootDesc, cause)
}

func (s *Server) metaHTTPClient() *http.Client {
	return &http.Client{
		Timeout:   2 * time.Second,
		Transport: &http.Transport{Proxy: nil},
	}
}

// withDaemonMeta runs fn against the verified daemon for dir. For direct
// serve --stdio sessions, a daemon-unavailable error triggers EnsureDaemon once
// under the existing dashboard policy and retries once before returning a
// recoverable unavailable error. Raw out-of-daemon calls never auto-start a
// daemon.
func (s *Server) withDaemonMeta(dir string, fn func() error) error {
	err := fn()
	if err == nil {
		return nil
	}
	if !errors.Is(err, wsdashboard.ErrDaemonUnavailable) {
		return err
	}
	if s.directStdio && s.ensureDaemon != nil {
		ensured := false
		s.ensureMetaOnce.Do(func() {
			ensured = true
			s.ensureDaemon()
		})
		if ensured {
			if retryErr := fn(); retryErr == nil {
				return nil
			} else if !errors.Is(retryErr, wsdashboard.ErrDaemonUnavailable) {
				return retryErr
			} else {
				err = retryErr
			}
		}
	}
	return s.daemonUnavailableError(dir, err)
}

func recordFromSnapshot(snap wsdashboard.AgentRecordSnapshot, wsKey string) wsagent.Record {
	rec := wsagent.Record{
		ReplyID:     snap.ReplyID,
		WSKey:       wsKey,
		Profile:     snap.Profile,
		Backend:     snap.Backend,
		Root:        snap.Root,
		Model:       snap.Model,
		Effort:      snap.Effort,
		Lead:        snap.Lead,
		ClosedAt:    snap.ClosedAt,
		HandedOffAt: snap.HandedOffAt,
		HolderID:    snap.HolderID,
		HostPID:     snap.HostPID,
		Tier:        snap.Tier,
	}
	if len(snap.ResolvedDefinition) > 0 {
		var profile wsagent.Profile
		if json.Unmarshal(snap.ResolvedDefinition, &profile) == nil {
			rec.ResolvedDefinition = &profile
		}
	}
	if snap.Parent != nil {
		rec.Parent = &wsagent.ParentRef{
			Kind:    snap.Parent.Kind,
			ReplyID: snap.Parent.ReplyID,
		}
	}
	return rec
}

// lookupAgentRecord returns key's daemon-owned agent record when its stored
// WSKey matches key. Daemon-hosted servers read memory directly; out-of-daemon
// processes query the verified daemon over loopback /meta/agent-record.
func (s *Server) lookupAgentRecord(key string) (wsagent.Record, bool, error) {
	if key == "" {
		return wsagent.Record{}, false, nil
	}
	replyID := wsmailbox.ReplyID(key)
	if !wsdashboard.ValidReplyID(replyID) {
		return wsagent.Record{}, false, nil
	}
	if reader := s.agentRecordReader(); reader != nil {
		rec, ok := reader.RecordByReplyID(replyID)
		if !ok || rec.WSKey != key {
			return wsagent.Record{}, false, nil
		}
		return rec, true, nil
	}
	dir, err := s.dashboard.stateDir()
	if err != nil {
		return wsagent.Record{}, false, err
	}
	var snap wsdashboard.AgentRecordSnapshot
	var found bool
	client := s.metaHTTPClient()
	err = s.withDaemonMeta(dir, func() error {
		var fetchErr error
		snap, found, fetchErr = wsdashboard.FetchAgentRecord(context.Background(), client, dir, replyID, key)
		return fetchErr
	})
	if err != nil {
		return wsagent.Record{}, false, err
	}
	if !found {
		return wsagent.Record{}, false, nil
	}
	return recordFromSnapshot(snap, key), true, nil
}

// lookupHostedDescendant reports whether key's daemon-owned agent record
// descends from hosted within hostedLineageDepth hops.
func (s *Server) lookupHostedDescendant(hosted, key string) (bool, error) {
	if hosted == "" || key == "" {
		return false, nil
	}
	hostedReplyID := wsmailbox.ReplyID(hosted)
	replyID := wsmailbox.ReplyID(key)
	if !wsdashboard.ValidReplyID(hostedReplyID) || !wsdashboard.ValidReplyID(replyID) {
		return false, nil
	}
	if reader := s.agentRecordReader(); reader != nil {
		rec, ok := reader.RecordByReplyID(replyID)
		if !ok || rec.WSKey != key {
			return false, nil
		}
		for depth := 0; rec.Parent != nil && depth < hostedLineageDepth; depth++ {
			parent := rec.Parent.ReplyID
			if parent == hostedReplyID {
				return true, nil
			}
			if !wsdashboard.ValidReplyID(parent) {
				return false, nil
			}
			rec, ok = reader.RecordByReplyID(parent)
			if !ok {
				return false, nil
			}
		}
		return false, nil
	}
	dir, err := s.dashboard.stateDir()
	if err != nil {
		return false, err
	}
	var descendant bool
	client := s.metaHTTPClient()
	err = s.withDaemonMeta(dir, func() error {
		var checkErr error
		descendant, checkErr = wsdashboard.CheckHostedDescendant(context.Background(), client, dir, hostedReplyID, replyID, key, hostedLineageDepth)
		return checkErr
	})
	return descendant, err
}

// lookupHasAgentRecord reports whether replyID has a daemon-owned agent record.
func (s *Server) lookupHasAgentRecord(dir, replyID string) (bool, error) {
	if !wsdashboard.ValidReplyID(replyID) {
		return false, nil
	}
	if reader := s.agentRecordReader(); reader != nil {
		_, ok := reader.RecordByReplyID(replyID)
		return ok, nil
	}
	var has bool
	client := s.metaHTTPClient()
	err := s.withDaemonMeta(dir, func() error {
		var checkErr error
		has, checkErr = wsdashboard.HasAgentRecord(context.Background(), client, dir, replyID)
		return checkErr
	})
	return has, err
}
