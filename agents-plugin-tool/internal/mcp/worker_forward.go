package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsagent"
	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// workerForwardTimeout bounds a forwarded call.
const workerForwardTimeout = 60 * time.Second

// workerForward finds the current daemon in this server's cache, since the
// process's inherited endpoint can name a daemon replaced while it ran.
// The inherited endpoint remains a fallback for older setups and tests.
func (s *Server) workerForward() (token, endpoint string, ok bool) {
	// Existing worker processes pass these environment names across the daemon boundary.
	token = strings.TrimSpace(s.getenv(wsagent.EnvPiToken))
	endpoint = strings.TrimRight(strings.TrimSpace(s.getenv(wsagent.EnvPiEndpoint)), "/")
	if token != "" {
		if dir, err := s.dashboard.stateDir(); err == nil {
			if ep, err := wsdashboard.ReadEndpoint(dir); err == nil && ep.Port > 0 && ep.Port <= 65535 {
				endpoint = fmt.Sprintf("http://127.0.0.1:%d", ep.Port)
			}
		}
	}
	return token, endpoint, token != "" && endpoint != ""
}

// workerForwards reports whether this server forwards worker tools: it has
// no runner of its own and the runner gave it a token.
func (s *Server) workerForwards() bool {
	if s.agents != nil {
		return false
	}
	_, _, ok := s.workerForward()
	return ok
}

// daemonRefusal is a non-2xx answer the daemon wrote to a forwarded call: a
// definite refusal. Every other workerCall error (no answer, a timeout, a lost
// connection, an unreadable or undecodable answer) leaves unknown whether
// the daemon acted on the call before its answer was lost.
type daemonRefusal struct {
	status int
	msg    string
}

func (e *daemonRefusal) Error() string { return e.msg }

// definiteDaemonRefusal reports whether err is a daemon's written refusal,
// not a call whose outcome is unknown.
func definiteDaemonRefusal(err error) bool {
	var refusal *daemonRefusal
	return errors.As(err, &refusal)
}

// workerCall posts one forwarded call to the token route and decodes its
// answer into out; a non-2xx answer is a *daemonRefusal.
func (s *Server) workerCall(ctx context.Context, op string, body map[string]any, timeout time.Duration, out any) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	token, endpoint, _ := s.workerForward()
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+wsagent.RoutePrefix+"/by-token/"+op, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("the dashboard daemon did not answer at %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return fmt.Errorf("read the dashboard daemon's answer: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return &daemonRefusal{status: resp.StatusCode, msg: strings.TrimSpace(string(answer))}
	}
	if err := json.Unmarshal(answer, out); err != nil {
		return fmt.Errorf("the dashboard daemon's answer is not JSON: %w", err)
	}
	return nil
}
