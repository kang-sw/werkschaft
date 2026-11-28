package wsagent

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wslive"
)

// agyDialect is Antigravity's acpDialect: what `agy acp` adds to the shared
// ACP core. Its hooks read agy's constants and timing variables at each call
// and never lock.
type agyDialect struct{}

var _ acpDialect = agyDialect{}

func (agyDialect) label() string { return "agy" }

func (agyDialect) timing() acpTiming {
	return acpTiming{
		init:           agyInitTimeout,
		session:        agySessionTimeout,
		control:        agyControlTimeout,
		mcpConnect:     agyMCPConnectWait,
		wakeUsageQuiet: agyWakeUsageQuiet,
		wakeIdleQuiet:  agyWakeIdleQuiet,
	}
}

// instructions is the rule appended to a non-ephemeral session's system
// prompt. A cutover session runs commands through the ws exec tools, whose
// completions come as messages; one outside it keeps run_command and the
// rule that keeps its turn open while a background one runs.
func (agyDialect) instructions(spec StartSpec) string {
	if spec.ExecCutover {
		return agyExecCutoverInstruction
	}
	return agyBgShellRule
}

// openMeta carries the appended system prompt under _meta.antigravity.
func (agyDialect) openMeta(appendText string) map[string]any {
	return map[string]any{"antigravity": map[string]any{"systemPrompt": map[string]any{"append": appendText}}}
}

func (agyDialect) effortOption() string { return "thought_level" }

// modelVariant is agy's suffixed model id for effort.
func (agyDialect) modelVariant(base, effort string) string { return base + "-" + effort }

func (agyDialect) baseModel(model string) (string, string) { return agyBaseModel(model) }

func (agyDialect) permissionModes() []string { return AgyPermissionModes }

func (agyDialect) cutoverMode() string { return agyCutoverMode }

// classifyTool knows agy's run_command (IsDaemon detaches it) and
// invoke_subagent (labelled by Role, else TypeName, else "subagent"; its
// Prompt argument is never read).
func (agyDialect) classifyTool(name string, args, output json.RawMessage) acpToolClass {
	switch name {
	case agyCommandTool:
		var a struct {
			IsDaemon bool `json:"IsDaemon"`
		}
		_ = json.Unmarshal(args, &a)
		return acpToolClass{command: true, daemon: a.IsDaemon, ran: agyCommandRan(output)}
	case agyInvokeSubagentTool:
		var a struct {
			Role     string `json:"Role"`
			TypeName string `json:"TypeName"`
		}
		_ = json.Unmarshal(args, &a)
		label := strings.TrimSpace(a.Role)
		if label == "" {
			label = strings.TrimSpace(a.TypeName)
		}
		if label == "" {
			label = "subagent"
		}
		return acpToolClass{subagent: true, label: label}
	}
	return acpToolClass{}
}

func (agyDialect) mcpToolMatches(name, server, tool string) bool {
	return agyMCPToolNameMatches(name, server, tool)
}

func (agyDialect) replyFailure(reply string) string { return agyReplyTailFailure(reply) }

func (agyDialect) usageLimit(msg acpMessage, modelFailure string, now int64) *wslive.Notice {
	return agyUsageLimitAlert(msg, modelFailure, now)
}

// publishChoices keeps a session's report for AgyBackend.Choices, which
// wins over the session-less `agy models` listing.
func (agyDialect) publishChoices(models []ModelChoice, efforts []string) {
	agyChoices.mu.Lock()
	defer agyChoices.mu.Unlock()
	if len(models) > 0 {
		agyChoices.models = models
	}
	if len(efforts) > 0 {
		agyChoices.efforts = efforts
	}
}

// agyBaseModel separates the thinking axis from agy's model vocabulary.
func agyBaseModel(model string) (string, string) {
	for _, effort := range []string{"xhigh", "high", "medium", "low"} {
		if base, ok := strings.CutSuffix(model, "-"+effort); ok {
			return base, effort
		}
	}
	return model, ""
}

// agyNativeSubagentTool is agy's native subagent tool name, surfaced to
// hosted-lead prompt steering when agy.prefer_native_subagent is false while
// keeping AgyCatalog().ItemSubagentRemoval unset.
const agyNativeSubagentTool = "invoke_subagent"

func agyLeadSubagentPolicy(cfg wsconfig.Options, reader wsconfig.SessionReader, wsKey string) LeadSubagentPolicy {
	// Session scope is unsupported for agy.* adapter overrides (noSession=true),
	// so resolve strictly from repo/project/global/builtin layers.
	_, _ = reader, wsKey
	r := wsconfig.NewResolver(cfg, map[string]string{
		wsconfig.ItemAgyPreferNativeSubagent: "true",
		wsconfig.ItemAgyAvoidAgySubagent:     "true",
	}, nil, nil)
	prefer := true
	if rv, err := r.Get("", wsconfig.ItemAgyPreferNativeSubagent); err == nil {
		if b, err := strconv.ParseBool(rv.Value); err == nil {
			prefer = b
		}
	}
	avoid := true
	if rv, err := r.Get("", wsconfig.ItemAgyAvoidAgySubagent); err == nil {
		if b, err := strconv.ParseBool(rv.Value); err == nil {
			avoid = b
		}
	}
	return LeadSubagentPolicy{
		Active:                true,
		PreferNativeSubagent:  prefer,
		AvoidSameHarnessSpawn: avoid,
		NativeSubagentTool:    agyNativeSubagentTool,
	}
}

// agyCommandTool is agy's native process tool.
const agyCommandTool = "run_command"

// agyCommandRan reports execution evidence in a run_command update's raw
// output: an exit code, or any output at all. A refused call ends failed
// with an empty output and no exit code.
func agyCommandRan(raw json.RawMessage) bool {
	var out struct {
		Output   string          `json:"output"`
		ExitCode json.RawMessage `json:"exit_code"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &out) != nil {
		return false
	}
	return len(out.ExitCode) > 0 && string(out.ExitCode) != "null" || strings.TrimSpace(out.Output) != ""
}

// isAgyQuotaText reports whether an ACP error payload or anchored in-band
// model-failure line indicates a quota or rate-limit exhaustion rather than a
// transient transport/capacity stall.
func isAgyQuotaText(text string) bool {
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "resource_exhausted"),
		strings.Contains(lower, "usage limit"),
		strings.Contains(lower, "usage_limit"),
		strings.Contains(lower, "rate limit"),
		strings.Contains(lower, "rate_limit"),
		strings.Contains(lower, "too many requests"),
		strings.Contains(lower, "quota"),
		strings.Contains(lower, "exhausted your capacity"),
		hasHTTPStatus429(lower):
		return true
	}
	return false
}

// hasHTTPStatus429 matches HTTP/API status-code 429 shapes while avoiding false
// positives on unrelated numbers such as line numbers or ports ("line 1429", "port 4290").
func hasHTTPStatus429(lower string) bool {
	if strings.TrimSpace(lower) == "429" {
		return true
	}
	for _, pat := range []string{
		"http 429", "http/1.1 429", "http/2 429",
		"status 429", "status: 429", "status=429",
		"code 429", "code: 429", "code=429",
		"error 429", "error: 429",
		"429 too many", "(429)", "[429]",
	} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

// agyUsageLimitAlert classifies an ACP prompt error or anchored in-band model
// failure into a common NoticeUsageLimit alert.
func agyUsageLimitAlert(msg acpMessage, modelFailure string, now int64) *wslive.Notice {
	if msg.Error != nil {
		dataText := string(msg.Error.Data)
		if msg.Error.Code == 429 || msg.Error.Code == 8 || isAgyQuotaText(msg.Error.Message) || isAgyQuotaText(dataText) {
			text := strings.TrimSpace(msg.Error.Message)
			if text == "" {
				text = "usage limit reached"
			}
			return &wslive.Notice{
				Kind:     wslive.NoticeUsageLimit,
				Text:     "agy: " + text,
				ResetsAt: parseAgyErrorResetTime(msg.Error.Data, now),
			}
		}
	}
	if modelFailure != "" && isAgyQuotaText(modelFailure) {
		return &wslive.Notice{
			Kind: wslive.NoticeUsageLimit,
			Text: modelFailure,
		}
	}
	return nil
}

// parseAgyErrorResetTime extracts a future reset timestamp (epoch ms) from an
// optional ACP error.data JSON object when present.
func parseAgyErrorResetTime(raw json.RawMessage, now int64) int64 {
	if len(raw) == 0 {
		return 0
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return 0
	}
	for _, key := range []string{"resetsAt", "resets_at", "reset_time", "resetTime"} {
		val, ok := obj[key]
		if !ok || len(val) == 0 {
			continue
		}
		var s string
		if json.Unmarshal(val, &s) == nil && s != "" {
			if t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(s)); err == nil {
				if ms := t.UnixMilli(); ms > now {
					return ms
				}
			}
		}
		var n float64
		if json.Unmarshal(val, &n) == nil && n > 0 {
			if n < 1e12 {
				n *= 1000
			}
			if ms := int64(n); ms > now {
				return ms
			}
		}
	}
	return 0
}

// agyMCPToolName is a tool's current name under agy's MCP naming: the
// sanitized tool name prefixed by its server, so ws tracker.update is
// mcp_ws_tracker_update.
func agyMCPToolName(server, tool string) string {
	sanitized, _ := ApplyToolNameScheme(ToolNameSchemeSanitize, tool)
	return "mcp_" + server + "_" + sanitized
}

// agyMCPToolNameMatches also accepts the dotted spelling reported by agy
// connections created before ws began advertising sanitized aliases.
func agyMCPToolNameMatches(name, server, tool string) bool {
	return name == agyMCPToolName(server, tool) || name == "mcp_"+server+"_"+tool
}

// agyInvokeSubagentTool is agy's native subagent launch tool. It stays an
// adapter-local name: the catalog's native-subagent removal entry is left
// unset on purpose, since filling it adds a "never use" line to the hosted
// lead prompt.
const agyInvokeSubagentTool = "invoke_subagent"
