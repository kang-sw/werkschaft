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
		PermissionModes:     []string{werkModeReadOnly, werkModeWorkspaceWrite, werkModeFullAccess},
		Permission:          werkPermissionPolicy(),
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
			RestoreUsage:             true,
			NoWorkerCompactionSteer:  true,
			ExecReadTools:            true,
			FileWriteTools:           true,
			ReadOnlyNoExec:           true,
			LeadCompactionWSOnly:     true,
		},
		// The harness has no native process tools: shell goes through the
		// ws exec tools, so every worker runs under the exec cutover (which
		// lists them, holds the report for a live job, and protects the
		// worktree from release). There is no binary version to verify the
		// removal against: the removal is the absence of the tool.
		ExecCutover: &ExecCutoverSupport{NoNativeTools: true},
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
	leadProviders() (available []string, notice string)
	admitLeadProvider(provider, model string) error
	leadCredentialGap() string
}

func (b WerkBackend) werkProvider(id string) (wscred.Provider, error) {
	if id == "" {
		id = string(wscred.ProviderOpenRouter)
	}
	if provider, ok := wscred.Lookup(wscred.ProviderID(id)); ok {
		return provider, nil
	}
	for _, provider := range b.localProviders() {
		if provider.ID == wscred.ProviderID(id) {
			return provider, nil
		}
	}
	return wscred.Provider{}, fmt.Errorf("unsupported werk provider %q", id)
}

func (b WerkBackend) localProviders() []wscred.Provider {
	if catalog, ok := b.credentials.(interface{ LocalProviders() []wscred.Provider }); ok {
		return catalog.LocalProviders()
	}
	return nil
}

const werkSIWCReason = "sign in with ChatGPT using the CLI command \"credential sign-in openai\""

func (b WerkBackend) providerGap(id wscred.ProviderID) string {
	if wsconfig.IsLocalWerkProvider(string(id)) || b.credentialStatus == nil {
		return ""
	}
	if configured, err := b.credentialStatus(string(id)); err != nil {
		return "the provider credential store could not be read: " + err.Error()
	} else if !configured {
		if id == wscred.ProviderOpenAI {
			if store, ok := b.credentials.(interface {
				Status() ([]wscred.ProviderStatus, error)
			}); ok {
				statuses, err := store.Status()
				if err != nil {
					return "the provider credential store could not be read: " + err.Error()
				}
				for _, status := range statuses {
					for _, account := range status.OpenAIAccounts {
						if account.Active && account.SignedIn && !account.PlanEnabled {
							return wscred.ErrOpenAIPlanDisabled.Error()
						}
					}
				}
			}
			return werkSIWCReason
		}
		return werkNoKeyReason
	}
	return ""
}

func (b WerkBackend) admitLeadProvider(id, model string) error {
	provider, err := b.werkProvider(id)
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

func (b WerkBackend) leadProviders() (available []string, notice string) {
	for _, id := range []wscred.ProviderID{wscred.ProviderOpenRouter, wscred.ProviderOpenAI} {
		gap := b.providerGap(id)
		if gap == "" {
			available = append(available, string(id))
		} else if id == wscred.ProviderOpenAI && gap == wscred.ErrOpenAIPlanDisabled.Error() {
			notice = gap
		}
	}
	for _, provider := range b.localProviders() {
		available = append(available, string(provider.ID))
	}
	return available, notice
}

func (b WerkBackend) leadCredentialGap() string {
	openRouter, openAI := b.providerGap(wscred.ProviderOpenRouter), b.providerGap(wscred.ProviderOpenAI)
	if openRouter == "" || openAI == "" || len(b.localProviders()) > 0 {
		return ""
	}
	if openRouter == openAI {
		return openRouter
	}
	return openRouter + ", or " + openAI
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
	provider, err := b.werkProvider(id)
	if err != nil {
		return err.Error()
	}
	if why := b.providerGap(provider.ID); why != "" {
		return why
	}
	if provider.ID != wscred.ProviderOpenAI {
		return ""
	}
	consent, known, err := b.planBackgroundConsent()
	if err != nil {
		return "the provider credential store could not be read: " + err.Error()
	}
	if known && !consent {
		return (&wscred.ProviderError{Provider: provider.ID, Err: wscred.ErrConsentRequired}).Error()
	}
	return ""
}

// planBackgroundConsent reads the active registration without acquiring a token.
func (b WerkBackend) planBackgroundConsent() (allowed, known bool, err error) {
	if store, ok := b.credentials.(interface {
		Status() ([]wscred.ProviderStatus, error)
	}); ok {
		statuses, err := store.Status()
		if err != nil {
			return false, false, err
		}
		for _, status := range statuses {
			for _, account := range status.OpenAIAccounts {
				if account.Active && account.BackgroundConsent != nil && *account.BackgroundConsent {
					return true, true, nil
				}
			}
		}
		return false, true, nil
	}
	return false, false, nil
}

func (b WerkBackend) withPlanBackgroundConsent(use func(bool)) {
	if store, ok := b.credentials.(interface {
		WithOpenAIBackgroundConsent(func(bool))
	}); ok {
		store.WithOpenAIBackgroundConsent(use)
		return
	}
	allowed, _, err := b.planBackgroundConsent()
	use(err == nil && allowed)
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
