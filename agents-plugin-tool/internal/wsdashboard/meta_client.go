package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// ErrDaemonUnavailable reports that no verified dashboard daemon answered a
// loopback metadata request for the target state directory.
var ErrDaemonUnavailable = errors.New("dashboard daemon unavailable")

const defaultMetaTimeout = 2 * time.Second

func newMetaHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = defaultMetaTimeout
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: &http.Transport{Proxy: nil},
	}
}

// verifiedMetaEndpoint reads dir's endpoint file and verifies /health (service
// name and PID) before any Bearer token is sent to it.
func verifiedMetaEndpoint(ctx context.Context, client *http.Client, dir string) (Endpoint, error) {
	ep, err := ReadEndpoint(dir)
	if err != nil {
		return Endpoint{}, fmt.Errorf("%w: read endpoint: %v", ErrDaemonUnavailable, err)
	}
	if ep.Port <= 0 || ep.Token == "" {
		return Endpoint{}, fmt.Errorf("%w: incomplete endpoint", ErrDaemonUnavailable)
	}
	if _, err := Health(ctx, client, ep); err != nil {
		return Endpoint{}, fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	return ep, nil
}

func postMetaJSON(ctx context.Context, client *http.Client, dir, path string, reqBody, respBody any) error {
	if client == nil {
		client = newMetaHTTPClient(defaultMetaTimeout)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultMetaTimeout)
		defer cancel()
	}
	ep, err := verifiedMetaEndpoint(ctx, client, dir)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(ep, path), bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ep.Token)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDaemonUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, metaBodyLimit))
		return fmt.Errorf("%w: %s returned status %d", ErrDaemonUnavailable, path, resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, metaBodyLimit))
		return fmt.Errorf("daemon metadata %s: status %d", path, resp.StatusCode)
	}
	if respBody == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, metaBodyLimit))
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, metaBodyLimit)).Decode(respBody); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

// FetchAgentRecord asks the verified daemon for dir for replyID's record when
// wsKey matches its stored WSKey.
func FetchAgentRecord(ctx context.Context, client *http.Client, dir, replyID, wsKey string) (AgentRecordSnapshot, bool, error) {
	var resp metaAgentRecordResponse
	if err := postMetaJSON(ctx, client, dir, MetaAgentRecordPath, metaAgentRecordRequest{
		ReplyID: replyID,
		WSKey:   wsKey,
	}, &resp); err != nil {
		return AgentRecordSnapshot{}, false, err
	}
	if !resp.Found || resp.Record == nil {
		return AgentRecordSnapshot{}, false, nil
	}
	return *resp.Record, true, nil
}

// CheckHostedDescendant asks the verified daemon for dir whether replyID (whose
// stored WSKey must match wsKey) is a descendant of hostedReplyID.
func CheckHostedDescendant(ctx context.Context, client *http.Client, dir, hostedReplyID, replyID, wsKey string, maxDepth int) (bool, error) {
	var resp metaHostedDescendantResponse
	if err := postMetaJSON(ctx, client, dir, MetaHostedDescendantPath, metaHostedDescendantRequest{
		HostedReplyID: hostedReplyID,
		ReplyID:       replyID,
		WSKey:         wsKey,
		MaxDepth:      maxDepth,
	}, &resp); err != nil {
		return false, err
	}
	return resp.Descendant, nil
}

// HasAgentRecord asks the verified daemon for dir whether replyID has a
// daemon-owned agent record.
func HasAgentRecord(ctx context.Context, client *http.Client, dir, replyID string) (bool, error) {
	var resp metaUnregisterCheckResponse
	if err := postMetaJSON(ctx, client, dir, MetaUnregisterCheckPath, metaUnregisterCheckRequest{
		ReplyID: replyID,
	}, &resp); err != nil {
		return false, err
	}
	return resp.HasAgentRecord, nil
}

// RequestPruneRegistrations asks the verified daemon for dir to prune expired
// session registrations using its in-memory agent metadata.
func RequestPruneRegistrations(ctx context.Context, client *http.Client, dir string, retention time.Duration) (int, error) {
	var resp metaPruneResponse
	if err := postMetaJSON(ctx, client, dir, MetaPrunePath, metaPruneRequest{
		RetentionNS: int64(retention),
	}, &resp); err != nil {
		return 0, err
	}
	return resp.Pruned, nil
}
