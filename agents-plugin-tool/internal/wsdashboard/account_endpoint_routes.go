package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

const EndpointChangePath = "/auth/provider/endpoint"

type endpointWriter interface {
	ApplyLocalEndpoint(wsconfig.Options, wscred.LocalEndpointChange) ([]wscred.LocalEndpointStatus, error)
}
type endpointBody struct {
	Op      wscred.ChangeOp `json:"op"`
	Name    string          `json:"name"`
	BaseURL string          `json:"base_url,omitempty"`
	Kind    string          `json:"kind,omitempty"`
	Key     string          `json:"key,omitempty"`
}
type endpointResult struct {
	Endpoints []wscred.LocalEndpointStatus `json:"endpoints"`
	Changed   bool                         `json:"changed"`
}

func (d *Daemon) handleEndpointChange(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodPost {
		writeJSON(w, 405, credentialChangeRefusal{Error: "method_not_allowed"})
		return
	}
	if !d.machineAuth(r) {
		writeJSON(w, 401, credentialChangeRefusal{Error: "unauthorized"})
		return
	}
	d.handleAccountEndpoint(w, r, d.credentials)
}

// handleAccountEndpoint is called after the Account route's local browser guard.
func (d *Daemon) handleAccountEndpoint(w http.ResponseWriter, r *http.Request, store credentialStore) {
	w.Header().Set("Cache-Control", "no-store")
	writer, ok := store.(endpointWriter)
	if !ok {
		writeJSON(w, 503, credentialChangeRefusal{Error: "unavailable"})
		return
	}
	var body endpointBody
	dec := json.NewDecoder(io.LimitReader(r.Body, 128<<10))
	dec.DisallowUnknownFields()
	if dec.Decode(&body) != nil || dec.Decode(new(any)) != io.EOF {
		writeJSON(w, 400, credentialChangeRefusal{Error: "invalid_endpoint"})
		return
	}
	endpoints, err := writer.ApplyLocalEndpoint(wsconfig.Options{}, wscred.LocalEndpointChange{Op: body.Op, Name: body.Name, BaseURL: body.BaseURL, Kind: body.Kind, Key: wscred.NewSecret(body.Key)})
	switch {
	case errors.Is(err, wscred.ErrInvalidEndpoint):
		writeJSON(w, 400, credentialChangeRefusal{Error: "invalid_endpoint"})
	case errors.Is(err, wscred.ErrStoreClosed):
		writeJSON(w, 503, credentialChangeRefusal{Error: "shutting_down"})
	case err != nil:
		writeJSON(w, 500, credentialChangeRefusal{Error: "endpoint_update_failed"})
	default:
		writeJSON(w, 200, endpointResult{Endpoints: endpoints, Changed: true})
	}
}

// ApplyLocalEndpointChange uses the daemon when running, otherwise its writer lock.
func ApplyLocalEndpointChange(ctx context.Context, dir, credPath string, opts wsconfig.Options, change wscred.LocalEndpointChange) ([]wscred.LocalEndpointStatus, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(dir, lockFileName))
	defer lock.Close()
	deadline := time.Now().Add(credentialChangeWait)
	for {
		locked, err := lock.TryLock()
		if err != nil {
			return nil, err
		}
		if locked {
			defer lock.Unlock()
			store := wscred.NewStore(credPath)
			if err := store.MigrateLegacy(); err != nil {
				return nil, err
			}
			return store.ApplyLocalEndpoint(opts, change)
		}
		if ep, err := ReadEndpoint(dir); err == nil {
			client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
			if _, err := Health(ctx, client, ep); err == nil {
				raw, _ := json.Marshal(endpointBody{Op: change.Op, Name: change.Name, BaseURL: change.BaseURL, Kind: change.Kind, Key: change.Key.Reveal()})
				req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpointURL(ep, EndpointChangePath), bytes.NewReader(raw))
				if err != nil {
					return nil, errors.New("endpoint_update_failed")
				}
				req.Header.Set("Authorization", "Bearer "+ep.Token)
				req.Header.Set("Content-Type", "application/json")
				resp, err := client.Do(req)
				if err == nil {
					var result endpointResult
					decodeErr := json.NewDecoder(io.LimitReader(resp.Body, 128<<10)).Decode(&result)
					resp.Body.Close()
					if resp.StatusCode == 200 && decodeErr == nil {
						return result.Endpoints, nil
					}
					if resp.StatusCode != 503 && resp.StatusCode != 401 {
						return nil, errors.New("endpoint_update_failed")
					}
				}
			}
		}
		if time.Now().After(deadline) {
			return nil, errors.New("the dashboard daemon did not take the endpoint change")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(lockRetryDelay):
		}
	}
}
