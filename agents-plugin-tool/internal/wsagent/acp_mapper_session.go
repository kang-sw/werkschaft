package wsagent

import (
	"encoding/json"
	"slices"

	"github.com/kang-sw/devenv/internal/wslive"
)

// initialized reads the harness version and the init shape.
//
// Init-field to item mapping that gates demotion (the catalog's
// HarnessReport):
//
//	agentInfo.version              -> the version the profile resolves at
//	sessionCapabilities.resume     -> resume: absent
//	(session answer) configOptions -> model/effort: no model or thought_level option
//	(session answer) modes         -> approval: no available modes
//	available_commands_update      -> compaction: a list without "compact"
//
// Steer, interrupt, follow-up, usage and the other items have no shape to
// check: their grade follows the version alone.
func (m *acpMapper) initialized(result json.RawMessage) []Event {
	var r struct {
		AgentInfo struct {
			Version string `json:"version"`
		} `json:"agentInfo"`
		AgentCapabilities struct {
			SessionCapabilities map[string]json.RawMessage `json:"sessionCapabilities"`
		} `json:"agentCapabilities"`
	}
	_ = json.Unmarshal(result, &r)
	m.version = r.AgentInfo.Version
	m.initDemoted = nil
	if _, ok := r.AgentCapabilities.SessionCapabilities["resume"]; !ok {
		m.initDemoted = append(m.initDemoted, ItemResume)
	}
	return []Event{m.harnessReport()}
}

func (m *acpMapper) harnessReport() Event {
	demoted := slices.Clone(m.initDemoted)
	for _, item := range m.sessionDemoted {
		if !slices.Contains(demoted, item) {
			demoted = append(demoted, item)
		}
	}
	return Event{Harness: &HarnessReport{Version: m.version, Demoted: demoted}}
}

// opened reads a session/new or session/resume answer: the session id,
// the mode, and the session shape.
func (m *acpMapper) opened(result json.RawMessage) []Event {
	var r struct {
		SessionID string `json:"sessionId"`
		Models    struct {
			CurrentModelID  string `json:"currentModelId"`
			AvailableModels []struct {
				ModelID string `json:"modelId"`
			} `json:"availableModels"`
		} `json:"models"`
		Modes *struct {
			CurrentModeID  string            `json:"currentModeId"`
			AvailableModes []json.RawMessage `json:"availableModes"`
		} `json:"modes"`
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(result, &r) != nil {
		return nil
	}
	var out []Event
	if r.SessionID != "" {
		if m.sessionID != "" && m.sessionID != r.SessionID {
			// Rebinding the mapper to a fresh session after Reset: drop prior
			// session turn, reply, and approval state.
			m.inTurn = false
			m.unsolicitedTurn = false
			m.wakeStepHadText = false
			m.wakeSawUsage = false
			m.stopWakeTimerLocked()
			m.reply.Reset()
			m.replyAt = 0
			m.modelFailure = ""
			m.lastContext = nil
			clear(m.outstanding)
			clear(m.steerText)
			m.steerOrder = nil
			clear(m.openCalls)
			clear(m.pendingTools)
			clear(m.openBg)
			clear(m.bgCalls)
			clear(m.approvals)
			clear(m.approvalOf)
		}
		m.sessionID = r.SessionID
		out = append(out, Event{SessionID: r.SessionID})
	}
	m.models = nil
	for _, model := range r.Models.AvailableModels {
		m.models = append(m.models, model.ModelID)
	}
	m.model = r.Models.CurrentModelID
	m.configOptions(result)
	// The thought level is per model (the agent may drop the option for a
	// model without one), so only a missing model option is a shape mismatch.
	m.sessionDemoted = slices.DeleteFunc(m.sessionDemoted, func(item CapabilityItem) bool {
		return item == ItemModelEffort || item == ItemApproval
	})
	if _, ok := m.options["model"]; !ok {
		m.sessionDemoted = append(m.sessionDemoted, ItemModelEffort)
	}
	if r.Modes == nil || len(r.Modes.AvailableModes) == 0 {
		m.sessionDemoted = append(m.sessionDemoted, ItemApproval)
	} else if r.Modes.CurrentModeID != "" {
		m.curMode = r.Modes.CurrentModeID
		out = append(out, Event{PermissionMode: r.Modes.CurrentModeID})
	}
	return append(out, m.harnessReport())
}

// configOptions keeps the model and thought-level values a session
// reports for Choices.
func (m *acpMapper) configOptions(body json.RawMessage) {
	var r struct {
		ConfigOptions []acpConfigOption `json:"configOptions"`
	}
	if json.Unmarshal(body, &r) != nil || r.ConfigOptions == nil {
		return
	}
	m.options = map[string][]string{}
	for _, o := range r.ConfigOptions {
		values := []string{}
		for _, v := range o.Options {
			values = append(values, v.Value)
		}
		m.options[o.ID] = values
	}
	var models []ModelChoice
	var efforts []string
	seen := map[string]bool{}
	effortOption := m.d.effortOption()
	for _, o := range r.ConfigOptions {
		if o.ID == "model" {
			if o.CurrentValue != "" && o.CurrentValue != m.model {
				m.rate.clearWindow()
			}
			m.model = o.CurrentValue
		}
		for _, v := range o.Options {
			switch o.ID {
			case "model":
				base, _ := m.d.baseModel(v.Value)
				if !seen[base] {
					models = append(models, ModelChoice{ID: base})
					seen[base] = true
				}
			case effortOption:
				efforts = append(efforts, v.Value)
			}
		}
	}
	if len(models) == 0 && len(efforts) == 0 || m.ephemeral {
		return
	}
	m.d.publishChoices(models, efforts)
}

// commands maps the agent's command list (its slash commands and skills); a list
// without "compact" demotes the compaction item.
func (m *acpMapper) commands(raw []json.RawMessage) []Event {
	list := make([]wslive.SlashCommand, 0, len(raw))
	compact := false
	for _, r := range raw {
		var c struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		}
		if json.Unmarshal(r, &c) != nil || c.Name == "" {
			continue
		}
		compact = compact || c.Name == "compact"
		list = append(list, wslive.SlashCommand{Name: c.Name, Description: c.Description})
	}
	out := []Event{{Live: &wslive.Event{Kind: wslive.KindCommands, Commands: list}}}
	had := slices.Contains(m.sessionDemoted, ItemCompaction)
	if compact == had {
		if compact {
			m.sessionDemoted = slices.DeleteFunc(m.sessionDemoted, func(item CapabilityItem) bool { return item == ItemCompaction })
		} else {
			m.sessionDemoted = append(m.sessionDemoted, ItemCompaction)
		}
		out = append([]Event{m.harnessReport()}, out...)
	}
	return out
}
