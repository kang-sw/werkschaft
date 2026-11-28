package wsdashboard

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/kang-sw/devenv/internal/wscred"
)

func (d *Daemon) handleAccountOpenAISwitch(w http.ResponseWriter, r *http.Request, store browserCredentialStore) {
	var body struct {
		ClientID string `json:"client_id"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	clientID := strings.TrimSpace(body.ClientID)
	if clientID == "" {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := store.SetActiveOpenAIAccount(clientID); err != nil {
		switch {
		case errors.Is(err, wscred.ErrOpenAIAccountNotFound):
			accountError(w, http.StatusNotFound, "account_not_found")
		case errors.Is(err, wscred.ErrOpenAITokensMissing):
			accountError(w, http.StatusUnprocessableEntity, "tokens_missing")
		case errors.Is(err, wscred.ErrOpenAIPlanDisabled):
			accountError(w, http.StatusUnprocessableEntity, "plan_disabled")
		case errors.Is(err, wscred.ErrConsentRequired):
			accountError(w, http.StatusUnprocessableEntity, "consent_required")
		default:
			accountStoreError(w, err)
		}
		return
	}
	d.accountChanged()
	writeJSON(w, http.StatusOK, struct {
		ClientID string `json:"client_id"`
		Mode     string `json:"mode"`
	}{ClientID: clientID, Mode: "immediate"})
}

func (d *Daemon) handleAccountSlotsAdd(w http.ResponseWriter, r *http.Request) {
	if d.accountSlots == nil {
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	var body struct {
		Harness   string `json:"harness"`
		SlotID    string `json:"slot_id"`
		Label     string `json:"label"`
		ConfigDir string `json:"config_dir"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	harness := strings.TrimSpace(body.Harness)
	if !wscred.SupportedSlotHarness(harness) {
		accountError(w, http.StatusBadRequest, "account_slot_unsupported")
		return
	}
	slotID := deriveSlotID(harness, body.SlotID, body.Label)
	if !wscred.ValidAccountSlotID(slotID) || slotID == wscred.DefaultAccountSlotID {
		accountError(w, http.StatusBadRequest, "invalid_slot_id")
		return
	}
	configDir := strings.TrimSpace(body.ConfigDir)
	if configDir == "" || !filepath.IsAbs(configDir) {
		accountError(w, http.StatusBadRequest, "invalid_config_dir")
		return
	}
	cleanDir := filepath.Clean(configDir)
	info, err := os.Stat(cleanDir)
	if err != nil || !info.IsDir() {
		accountError(w, http.StatusUnprocessableEntity, "config_dir_not_found")
		return
	}
	// Reject aliasing the harness's default home as an isolated custom slot
	// (D14: default home stays the implicit default slot).
	var explicitHome string
	if d.planUsage != nil {
		explicitHome = d.planUsage.explicitHome
	}
	defaultHome := wscred.ResolveHarnessHome(wscred.HarnessHomeOptions{
		Harness:      harness,
		ExplicitHome: explicitHome,
	})
	if defaultHome != "" && sameSlotPath(cleanDir, defaultHome) {
		accountError(w, http.StatusUnprocessableEntity, "config_dir_is_default")
		return
	}
	slot, err := d.accountSlots.AddSlot(wscred.AccountSlot{
		ID:        slotID,
		Harness:   harness,
		Label:     strings.TrimSpace(body.Label),
		ConfigDir: cleanDir,
		Managed:   false,
	})
	if err != nil {
		accountSlotError(w, err)
		return
	}
	d.notifyPiRuntimeChanged()
	d.accountChanged()
	writeJSON(w, http.StatusOK, slot)
}

func (d *Daemon) handleAccountSlotsRemove(w http.ResponseWriter, r *http.Request) {
	if d.accountSlots == nil {
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	var body struct {
		Harness   string `json:"harness"`
		SlotID    string `json:"slot_id"`
		DeleteDir bool   `json:"delete_dir"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	harness := strings.TrimSpace(body.Harness)
	slotID := strings.TrimSpace(body.SlotID)
	if !wscred.SupportedSlotHarness(harness) || slotID == "" {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if d.piLoginBusyForSlot(harness, slotID) {
		accountError(w, http.StatusConflict, "login_in_progress")
		return
	}
	slot, exists := d.accountSlots.ResolveSlot(harness, slotID)
	if err := d.accountSlots.RemoveSlot(harness, slotID); err != nil {
		accountSlotError(w, err)
		return
	}
	if exists && slot.Managed && body.DeleteDir && slot.ConfigDir != "" {
		managedRoot := filepath.Join(filepath.Clean(d.dir), "slots", harness)
		if withinDir(slot.ConfigDir, managedRoot) {
			_ = os.RemoveAll(slot.ConfigDir)
		}
	}
	d.notifyPiRuntimeChanged()
	d.accountChanged()
	w.WriteHeader(http.StatusNoContent)
}

func (d *Daemon) handleAccountSlotsRename(w http.ResponseWriter, r *http.Request) {
	if d.accountSlots == nil {
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	var body struct {
		Harness string `json:"harness"`
		SlotID  string `json:"slot_id"`
		Label   string `json:"label"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	harness := strings.TrimSpace(body.Harness)
	slotID := strings.TrimSpace(body.SlotID)
	if !wscred.SupportedSlotHarness(harness) || slotID == "" {
		accountError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := d.accountSlots.UpdateSlotLabel(harness, slotID, body.Label); err != nil {
		accountSlotError(w, err)
		return
	}
	slot, ok := d.accountSlots.ResolveSlot(harness, slotID)
	if !ok {
		accountError(w, http.StatusNotFound, "account_slot_not_found")
		return
	}
	d.accountChanged()
	writeJSON(w, http.StatusOK, slot)
}

func (d *Daemon) handleAccountSlotsSwitch(w http.ResponseWriter, r *http.Request) {
	if d.accountSlots == nil {
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
		return
	}
	var body struct {
		Harness        string `json:"harness"`
		SlotID         string `json:"slot_id"`
		ConversationID string `json:"conversation_id"`
		Mode           string `json:"mode"`
	}
	if !accountBody(w, r, &body) {
		return
	}
	slotID := wscred.NormalizeAccountSlotID(body.SlotID)
	convID := strings.TrimSpace(body.ConversationID)
	mode := strings.TrimSpace(body.Mode)

	if convID != "" {
		host, ok := d.agents.(AgentAccountSlotHost)
		if !ok {
			accountError(w, http.StatusServiceUnavailable, "agent_host_unavailable")
			return
		}
		var (
			res AccountSlotSwitchResult
			err error
		)
		switch mode {
		case "", "queue":
			res, err = host.QueueConversationAccountSlot(convID, slotID)
		case "now":
			res, err = host.SwitchConversationAccountSlotNow(convID, slotID)
		default:
			accountError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if err != nil {
			accountSlotHostError(w, err)
			return
		}
		d.notifyPiRuntimeChanged()
		d.accountChanged()
		writeJSON(w, http.StatusOK, res)
		return
	}

	// Harness-level switch (D13): update harness default slot AND fan out
	// PendingAccountSlotID to all live conversations of that harness.
	harness := strings.TrimSpace(body.Harness)
	if !wscred.SupportedSlotHarness(harness) {
		accountError(w, http.StatusBadRequest, "account_slot_unsupported")
		return
	}
	if err := d.accountSlots.SetActive(harness, slotID); err != nil {
		accountSlotError(w, err)
		return
	}
	var queued, skipped int
	if host, ok := d.agents.(AgentAccountSlotHost); ok {
		var err error
		queued, skipped, err = host.QueueHarnessAccountSlot(harness, slotID)
		if err != nil {
			accountSlotHostError(w, err)
			return
		}
	}
	d.notifyPiRuntimeChanged()
	d.accountChanged()
	writeJSON(w, http.StatusOK, AccountSlotSwitchResult{
		AccountSlotID: slotID,
		Mode:          "harness_default",
		QueuedCount:   queued,
		SkippedCount:  skipped,
	})
}

func accountSlotError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, wscred.ErrAccountSlotNotFound):
		accountError(w, http.StatusNotFound, "account_slot_not_found")
	case errors.Is(err, wscred.ErrAccountSlotExists):
		accountError(w, http.StatusConflict, "slot_exists")
	case errors.Is(err, wscred.ErrDefaultSlotImmutable):
		accountError(w, http.StatusBadRequest, "default_slot_immutable")
	case errors.Is(err, wscred.ErrInvalidAccountSlot):
		accountError(w, http.StatusBadRequest, "invalid_slot")
	default:
		accountError(w, http.StatusServiceUnavailable, "slot_registry_unavailable")
	}
}

func accountSlotHostError(w http.ResponseWriter, err error) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "conversation_busy"):
		accountError(w, http.StatusConflict, "conversation_busy")
	case strings.Contains(msg, "account_slot_not_found"):
		accountError(w, http.StatusNotFound, "account_slot_not_found")
	case strings.Contains(msg, "account_slot_unsupported"):
		accountError(w, http.StatusBadRequest, "account_slot_unsupported")
	case strings.Contains(msg, "ended"), strings.Contains(msg, "closed"), strings.Contains(msg, "handed off"):
		accountError(w, http.StatusConflict, "conversation_ended")
	case strings.Contains(msg, "unknown"):
		accountError(w, http.StatusNotFound, "conversation_not_found")
	default:
		accountError(w, http.StatusInternalServerError, "account_slot_switch_failed")
	}
}

func sameSlotPath(a, b string) bool {
	if samePath(a, b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(filepath.Clean(a))
	rb, errB := filepath.EvalSymlinks(filepath.Clean(b))
	if errA == nil && errB == nil {
		return samePath(ra, rb)
	}
	return false
}

func withinDir(path, root string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
