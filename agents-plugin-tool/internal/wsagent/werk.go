package wsagent

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

// BackendWerk is the daemon's builtin Go agent harness. Its model loop
// recovers from the session log and calls ws tools through authenticated
// loopback MCP.
const BackendWerk = "werk"

// LeadProfileWerk names the builtin hosted Werk lead profile.
const LeadProfileWerk = "lead-werk"

// WerkBackend starts a builtin hosted conversation.
type WerkBackend struct {
	// credentialStatus reports whether provider has a credential configured,
	// from the daemon's credential store. nil means the daemon cannot tell,
	// which blocks nothing: the first request then fails with the auth
	// layer's own refusal.
	credentialStatus func(provider string) (configured bool, err error)
	credentials      wscred.Source
	// modelsURL and httpClient stand in for OpenRouter's model list endpoint
	// and its client; a test sets them, a daemon never does.
	modelsURL  string
	httpClient *http.Client
	// The existing picker lookup populates this cache. Model requests
	// read its last successful publication from memory.
	modelWindows  *atomic.Pointer[map[string]int64]
	openAIWindows *atomic.Pointer[map[string]int64]
}

// werkWorkerText is the instruction text of the harness's worker profile.
//
//go:embed instructions/agent-worker-werk.md
var werkWorkerText string

func init() {
	builtinInstructionTexts[AgentWorkerProfile(BackendWerk)] = werkWorkerText
}

func (b WerkBackend) registration() (string, BackendRegistration) {
	b.modelWindows = new(atomic.Pointer[map[string]int64])
	b.openAIWindows = new(atomic.Pointer[map[string]int64])
	lead := leadBuiltinProfile(LeadProfileWerk, BackendWerk, "")
	worker := agentWorkerBuiltinProfile(BackendWerk, nil)
	worker.Definition.WSTools = append(slices.Clone(worker.Definition.WSTools), "fs.edit", "fs.write")
	return BackendWerk, BackendRegistration{
		Backend:             b,
		ProcessKind:         ProcessDurable,
		LeadProfile:         LeadProfileWerk,
		PermissionLevels:    LeadPermissionLevels(),
		Permission:          PermissionPolicy{LegacyDefault: PermissionWorkdirWrite},
		PlaybookCommands:    true,
		WorkerCapable:       true,
		WorkerProfile:       AgentWorkerProfile(BackendWerk),
		Catalog:             WerkCatalog(),
		BuiltinProfilesOnly: true,
		// A provider's function names admit no dot, so the model sees ws's
		// tools spelled exec_shell, agent_spawn (werk.ModelToolName).
		ToolNameScheme: ToolNameSchemeSanitize,
		Lifecycle: LifecyclePolicy{
			// Durable compaction waits for a quiescent boundary.
			ComposedLeadInstructions: true,
			LeadHTTPMCP:              true,
			NoWorkerCompactionSteer:  true,
			ExecReadTools:            true,
			FileWriteTools:           true,
			ReadOnlyNoExec:           true,
		},
		// The harness has no native process tools: shell goes through the
		// ws exec tools, so every worker runs under the exec cutover (which
		// lists them, holds the report for a live job, and protects the
		// worktree from release). There is no binary version to verify the
		// removal against: the removal is the absence of the tool.
		ExecCutover: &ExecCutoverSupport{},
		Dashboard:   werkDashboardTraits(),
		Profiles:    []BuiltinProfileRegistration{worker, lead},
	}
}

func (b WerkBackend) withProviderCredentials(source wscred.Source, status func(provider string) (bool, error)) Backend {
	b.credentials = source
	b.credentialStatus = status
	return b
}

// Executable is empty for the in-daemon harness.
func (b WerkBackend) Executable() string { return "" }

type credentialAware interface {
	withProviderCredentials(source wscred.Source, status func(provider string) (bool, error)) Backend
}

// WithProviderCredentials binds the daemon's source and status lookup to
// registered backends that use provider credentials.
func WithProviderCredentials(registrations map[string]BackendRegistration, source wscred.Source, status func(provider string) (bool, error)) map[string]BackendRegistration {
	for name, reg := range registrations {
		if aware, ok := reg.Backend.(credentialAware); ok {
			reg.Backend = aware.withProviderCredentials(source, status)
			registrations[name] = reg
		}
	}
	return registrations
}

type leadProviderAdmitter interface {
	ChoiceLister
	leadProviders() []string
	admitLeadProvider(provider, model string) error
	leadCredentialGap() string
}

func werkProvider(id string) (wscred.Provider, error) {
	if id == "" {
		id = string(wscred.ProviderOpenRouter)
	}
	switch wscred.ProviderID(id) {
	case wscred.ProviderOpenRouter, wscred.ProviderOpenAI:
		if provider, ok := wscred.Lookup(wscred.ProviderID(id)); ok {
			return provider, nil
		}
	}
	return wscred.Provider{}, fmt.Errorf("unsupported werk provider %q", id)
}

const werkSIWCReason = "sign in with ChatGPT using the CLI command \"credential sign-in openai\""

func (b WerkBackend) providerGap(id wscred.ProviderID) string {
	if b.credentialStatus == nil {
		return ""
	}
	if configured, err := b.credentialStatus(string(id)); err != nil {
		return "the provider credential store could not be read: " + err.Error()
	} else if !configured {
		if id == wscred.ProviderOpenAI {
			return werkSIWCReason
		}
		return werkNoKeyReason
	}
	return ""
}

func (b WerkBackend) admitLeadProvider(id, model string) error {
	provider, err := werkProvider(id)
	if err != nil {
		return badRequest("%s", err)
	}
	if model == "" {
		return badRequest("select a model for the werk lead")
	}
	if why := b.providerGap(provider.ID); why != "" {
		return badRequest("%s", why)
	}
	return nil
}

func (b WerkBackend) leadProviders() []string {
	var ready []string
	for _, id := range []wscred.ProviderID{wscred.ProviderOpenRouter, wscred.ProviderOpenAI} {
		if b.providerGap(id) == "" {
			ready = append(ready, string(id))
		}
	}
	return ready
}

func (b WerkBackend) leadCredentialGap() string {
	if openRouter := b.providerGap(wscred.ProviderOpenRouter); openRouter != "" && b.providerGap(wscred.ProviderOpenAI) != "" {
		return openRouter + ", or " + werkSIWCReason
	}
	return ""
}

// Reasons werk reports itself unavailable, in the words a user acts on.
const (
	werkNoKeyReason = "no OpenRouter API key is configured; set one with the CLI command \"credential set openrouter\" (or in the dashboard settings)"
	werkNoModelFmt  = "no tier model is configured for %s; set one with config.tune key \"agents.tier\" (harness %q, tier small, medium, large or xlarge, and a provider model ID); no default model is shipped"
)

// workerCredentialGap is the harness-wide check. Tier resolution checks
// the selected provider separately when only one is available.
func (b WerkBackend) workerCredentialGap() string {
	return b.leadCredentialGap()
}

func (b WerkBackend) workerProviderGap(id string) string {
	provider, err := werkProvider(id)
	if err != nil {
		return err.Error()
	}
	if why := b.providerGap(provider.ID); why != "" {
		return why
	}
	if provider.ID != wscred.ProviderOpenAI {
		return ""
	}
	// The Store reports consent without refreshing or borrowing the bearer.
	if store, ok := b.credentials.(interface {
		Status() ([]wscred.ProviderStatus, error)
	}); ok {
		statuses, err := store.Status()
		if err != nil {
			return "the provider credential store could not be read: " + err.Error()
		}
		for _, status := range statuses {
			for _, account := range status.OpenAIAccounts {
				if account.Active && account.BackgroundConsent != nil && *account.BackgroundConsent {
					return ""
				}
			}
		}
		return (&wscred.ProviderError{Provider: provider.ID, Err: wscred.ErrConsentRequired}).Error()
	}
	return ""
}

// workerTierGap is workerSetup: tiers start empty, so a harness with no tier
// model has nothing to spawn.
func (b WerkBackend) workerTierGap(tiers map[string]wsconfig.AgentTier) string {
	if len(tiers) > 0 {
		return ""
	}
	return fmt.Sprintf(werkNoModelFmt, BackendWerk, BackendWerk)
}

// Start opens the daemon loop for a backend supplied directly to a runner.
func (b WerkBackend) Start(ctx context.Context, spec StartSpec) (Process, error) {
	return b.startLoop(ctx, spec)
}
