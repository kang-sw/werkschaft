package wsdashboard

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wslive"
)

var errPlanUsage = errors.New("usage unavailable")

// Legacy CLI-owned credential provenance, not transport: acquisition is REST
// with no subprocess. Keep this wire/history identifier readable by old writers.
// Retained name check: the persisted history source label
// (TestPlanUsageHistoryValidatorsKeepPersistedNames).
const codexUsageSource = "via Codex CLI"

// Coalesce simultaneous popover opens as well as ordinary page polls.
func usageQueryInterval(force bool) time.Duration {
	if force {
		return 5 * time.Second
	}
	return 5 * time.Minute
}

// refresh answers viewers from the cached value: a due probe runs in the
// background, one at a time, so no viewer waits on the upstream round trip or
// queues behind queryMu. Only a store with nothing to show (no cached and no
// persisted GPT entry) probes inline, so a first page still gets a value.
func (s *planUsageStore) refresh(ctx context.Context, force bool) {
	// Popover refreshes (force) and agy activity both reach refreshAgy. Neither
	// runs the agy probe inline or from ordinary page polls or visibility changes.
	if force {
		s.refreshAgy()
	}
	s.refreshOpenRouter()
	s.refreshWerkOpenAI()
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	if s.gpt.AsOf == 0 && s.gpt.Status == "" && len(s.gpt.Windows) == 0 {
		s.mu.Unlock()
		s.query(ctx, force)
		return
	}
	if s.probing || time.Since(s.lastQuery) < usageQueryInterval(force) {
		s.mu.Unlock()
		return
	}
	parentCtx := s.lifetimeCtx
	if parentCtx == nil {
		parentCtx = context.Background()
	}
	s.probing = true
	s.probes.Add(1)
	s.mu.Unlock()
	go func() {
		defer s.probes.Done()
		s.query(parentCtx, force)
		s.mu.Lock()
		s.probing = false
		s.mu.Unlock()
	}()
}

func (s *planUsageStore) query(ctx context.Context, force bool) {
	s.queryMu.Lock()
	defer s.queryMu.Unlock()
	if time.Since(s.lastQuery) < usageQueryInterval(force) {
		return
	}
	_, codex := s.detect()
	if !codex {
		return
	}
	s.mu.Lock()
	slotID := s.gptSlotID
	s.mu.Unlock()
	// A viewer disconnect must not cache a failed query for every viewer.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	entry, err := s.queryCodex(ctx)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.gptSlotID != slotID {
		return
	}
	if err != nil {
		s.gpt.Status = "usage unavailable"
		if s.gpt.Source == "" {
			s.gpt.Source = codexUsageSource
		}
	} else {
		s.gpt = entry
		now := time.Now().UnixMilli()
		var shared, codex5h []planUsageSample
		for _, sample := range usageSamplesWithSlot(entry, slotID) {
			if s.harnesses.separateFiveHour(sample.Harness) && sample.Kind == wslive.LimitFiveHour {
				codex5h = append(codex5h, sample)
			} else {
				shared = append(shared, sample)
			}
		}
		s.appendHistoryLocked(shared, now)
		s.appendHistoryFileLocked(&s.codexHist, codexFiveHourHistorySpec, codex5h, now)
	}
	s.saveLocked()
	s.lastQuery = time.Now()
}

type codexUsageWindow struct {
	Percent *float64        `json:"usedPercent"`
	Minutes int             `json:"windowDurationMins"`
	Reset   json.RawMessage `json:"resetsAt"`
}
type codexUsageLimits struct {
	Plan      string            `json:"planType"`
	Primary   *codexUsageWindow `json:"primary"`
	Secondary *codexUsageWindow `json:"secondary"`
}

// codexUsageEntry is the ChatGPT-account reader's entry, which keeps its
// persisted "codex" harness label (1881 survivor; see planUsageStore.entries).
func codexUsageEntry(limits codexUsageLimits, source string) (planUsageEntry, error) {
	at := time.Now().UnixMilli()
	e := planUsageEntry{Harness: "codex", Plan: limits.Plan, Source: source, AsOf: at, Windows: []planUsageWindow{}}
	var fiveHour, sevenDay *planUsageWindow
	for _, w := range []*codexUsageWindow{limits.Primary, limits.Secondary} {
		// Primary and secondary can exchange positions across plans.
		if w == nil || w.Percent == nil || !validUsage(*w.Percent) {
			continue
		}
		switch w.Minutes {
		case 300:
			if fiveHour == nil {
				fiveHour = &planUsageWindow{Kind: wslive.LimitFiveHour, Percent: *w.Percent, ResetsAt: usageReset(w.Reset), AsOf: at}
			}
		case 10080:
			if sevenDay == nil {
				sevenDay = &planUsageWindow{Kind: wslive.LimitSevenDay, Percent: *w.Percent, ResetsAt: usageReset(w.Reset), AsOf: at}
			}
		}
	}
	if fiveHour != nil {
		e.Windows = append(e.Windows, *fiveHour)
	}
	if sevenDay != nil {
		e.Windows = append(e.Windows, *sevenDay)
	}
	if len(e.Windows) == 0 {
		return planUsageEntry{}, errPlanUsage
	}
	return e, nil
}

func (s *planUsageStore) queryCodex(ctx context.Context) (planUsageEntry, error) {
	// File selection stays narrower than the CLI's full auth resolver. Never
	// borrow an API key, refresh/login, or switch providers after selection.
	var auth struct {
		Mode   string          `json:"auth_mode"`
		APIKey json.RawMessage `json:"OPENAI_API_KEY"`
		Tokens struct {
			Access  string `json:"access_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if !readUsageJSON(filepath.Join(s.configHome("CODEX_HOME", ".codex"), "auth.json"), &auth) ||
		(auth.Mode != "chatgpt" && auth.Mode != "") ||
		(auth.Mode == "" && len(auth.APIKey) > 0 && string(auth.APIKey) != "null") ||
		strings.TrimSpace(auth.Tokens.Access) == "" ||
		!validUsageHeaderValue("Bearer "+auth.Tokens.Access) ||
		!validUsageHeaderValue(auth.Tokens.Account) || expiredUsageAccess(auth.Tokens.Access) {
		return planUsageEntry{}, errPlanUsage
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.usageURL, nil)
	if err != nil {
		return planUsageEntry{}, errPlanUsage
	}
	req.Header.Set("Authorization", "Bearer "+auth.Tokens.Access)
	if auth.Tokens.Account != "" {
		req.Header.Set("ChatGPT-Account-Id", auth.Tokens.Account)
	}
	req.Header.Set("User-Agent", "ws-dashboard")
	// Retained name check: the provider's usage endpoint headers
	// (TestPlanUsageCodexRESTWindows).
	req.Header.Set("originator", "codex_cli_rs")
	body, err := s.readUsageResponse(req)
	if err != nil {
		return planUsageEntry{}, err
	}
	convert := func(w *usageHTTPWindow) *codexUsageWindow {
		// Do not truncate arbitrary durations into 5-hour or weekly Codex quotas.
		if w == nil {
			return nil
		}
		switch w.Seconds {
		case 18000:
			return &codexUsageWindow{Percent: w.Percent, Minutes: 300, Reset: w.Reset}
		case 604800:
			return &codexUsageWindow{Percent: w.Percent, Minutes: 10080, Reset: w.Reset}
		default:
			return nil
		}
	}
	return codexUsageEntry(codexUsageLimits{Plan: body.Plan, Primary: convert(body.Limits.Primary), Secondary: convert(body.Limits.Secondary)}, codexUsageSource)
}

// HTTP field values allow horizontal tabs and visible/obs-text bytes, but not
// other controls or DEL. Validate before transport, including fake transports.
func validUsageHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] == 127 || value[i] < 32 && value[i] != '\t' {
			return false
		}
	}
	return true
}

func expiredUsageAccess(access string) bool {
	parts := strings.Split(access, ".")
	if len(parts) != 3 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	var claims struct {
		Exp *float64 `json:"exp"`
	}
	// Decoding is only an early expiry refusal, never signature validation.
	// Unknown/missing expiry remains server-authoritative for eligible tokens.
	return json.Unmarshal(payload, &claims) == nil && claims.Exp != nil && *claims.Exp <= float64(time.Now().Unix())
}

type usageHTTPWindow struct {
	Percent *float64        `json:"used_percent"`
	Seconds int             `json:"limit_window_seconds"`
	Reset   json.RawMessage `json:"reset_at"`
}
type usageHTTPResponse struct {
	Plan   string `json:"plan_type"`
	Limits struct {
		Primary   *usageHTTPWindow `json:"primary_window"`
		Secondary *usageHTTPWindow `json:"secondary_window"`
	} `json:"rate_limit"`
}

func (s *planUsageStore) readUsageResponse(req *http.Request) (usageHTTPResponse, error) {
	var body usageHTTPResponse
	response, err := s.client.Do(req)
	if err != nil {
		return body, errPlanUsage
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return body, errPlanUsage
	}
	// Read at most one byte beyond the cap, and reject trailing JSON as well
	// as oversized bodies. Never return body bytes or URL-bearing HTTP errors.
	const maxUsageResponse = 1 << 20
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxUsageResponse+1))
	if err != nil || len(raw) > maxUsageResponse || json.Unmarshal(raw, &body) != nil {
		return usageHTTPResponse{}, errPlanUsage
	}
	return body, nil
}
