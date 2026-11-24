package wsagent

import (
	"context"
	"slices"
	"sync"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wscred"
)

// TierRuntimeHarness is the backend that runs a tier after resolving its
// stored spelling. It does not change saved tiers or routing definitions.
// Retained name check: stored Pi tier alias to werk (TestRetiredPiTierRedirectsToWerk).
func TierRuntimeHarness(harness string) string {
	if harness == BackendPi {
		return BackendWerk
	}
	return harness
}

// TierBindingResolver shares one lazily fetched OpenRouter catalog within a
// resolution or listing. Keep it local to the operation so later reads can
// observe a changed catalog.
type TierBindingResolver struct {
	config wsconfig.Options
	models func() ([]ModelChoice, error)
}

func NewTierBindingResolver(cfg wsconfig.Options, registries ...map[string]BackendRegistration) *TierBindingResolver {
	return &TierBindingResolver{
		config: cfg,
		models: sync.OnceValues(func() ([]ModelChoice, error) {
			return werkOpenRouterChoices(werkChoiceLister(registries))
		}),
	}
}

// Retained name check: stored Pi tier alias to werk (TestRetiredPiTierRedirectsToWerk).
func (r *TierBindingResolver) tierBindings(harness, size string) (map[string]wsconfig.AgentTier, error) {
	tiers, err := wsconfig.HarnessTiers(r.config, harness)
	if err != nil || harness != BackendWerk {
		return tiers, err
	}
	if _, explicit := tiers[size]; explicit {
		return tiers, nil
	}
	legacy, err := wsconfig.HarnessTiers(r.config, BackendPi)
	if err != nil {
		return nil, err
	}
	for candidate := range legacy {
		if _, explicit := tiers[candidate]; explicit || (size != "" && size != candidate) {
			delete(legacy, candidate)
		}
	}
	if len(legacy) == 0 {
		return tiers, nil
	}
	models, err := r.models()
	if err != nil {
		// Discovery errors leave the unset slots unset. The caller reports
		// werk's setup instructions and can still use explicit werk slots.
		return tiers, nil
	}
	for size, binding := range legacy {
		if modelListed(models, binding.Model) {
			binding.Backend = BackendWerk
			binding.Provider = "" // Legacy bindings use the default OpenRouter provider.
			tiers[size] = binding
		}
	}
	return tiers, nil
}

// Retained name check: stored Pi tier alias to werk (TestRetiredPiTierRedirectsToWerk).
func werkChoiceLister(registries []map[string]BackendRegistration) ChoiceLister {
	if len(registries) == 0 {
		return WerkBackend{}
	}
	lister, _ := registries[0][BackendWerk].Backend.(ChoiceLister)
	return lister
}

func werkOpenRouterChoices(lister ChoiceLister) ([]ModelChoice, error) {
	if lister == nil {
		return nil, nil
	}
	// The combined picker also lists OpenAI account models. Legacy Pi
	// bindings can reuse only IDs verified in OpenRouter's public catalog.
	if backend, ok := lister.(WerkBackend); ok {
		models, _, err := backend.openRouterChoices(context.Background())
		return models, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), werkModelsTimeout)
	defer cancel()
	models, _, err := lister.Choices(ctx)
	return models, err
}

func (r *TierBindingResolver) hasExactModel(model string) bool {
	models, err := r.models()
	return err == nil && modelListed(models, model)
}

func modelListed(models []ModelChoice, id string) bool {
	return id != "" && slices.ContainsFunc(models, func(model ModelChoice) bool {
		return model.ID == id && (model.Provider == "" || model.Provider == string(wscred.ProviderOpenRouter))
	})
}
