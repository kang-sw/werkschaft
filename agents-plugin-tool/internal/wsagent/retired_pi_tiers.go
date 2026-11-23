package wsagent

import (
	"context"
	"slices"

	"github.com/kang-sw/devenv/internal/wsconfig"
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

// Retained name check: stored Pi tier alias to werk (TestRetiredPiTierRedirectsToWerk).
func tierBindings(cfg wsconfig.Options, harness, size string, registries ...map[string]BackendRegistration) (map[string]wsconfig.AgentTier, error) {
	tiers, err := wsconfig.HarnessTiers(cfg, harness)
	if err != nil || harness != BackendWerk {
		return tiers, err
	}
	if _, explicit := tiers[size]; explicit {
		return tiers, nil
	}
	legacy, err := wsconfig.HarnessTiers(cfg, BackendPi)
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
	lister := werkChoiceLister(registries)
	models, err := werkModelChoices(lister)
	if err != nil {
		// Discovery errors leave the unset slots unset. The caller reports
		// werk's setup instructions and can still use explicit werk slots.
		return tiers, nil
	}
	for size, binding := range legacy {
		if modelListed(models, binding.Model) {
			binding.Backend = BackendWerk
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

func werkModelChoices(lister ChoiceLister) ([]ModelChoice, error) {
	if lister == nil {
		return nil, nil
	}
	models, _, err := lister.Choices(context.Background())
	return models, err
}

func hasExactModel(lister ChoiceLister, model string) bool {
	models, err := werkModelChoices(lister)
	return err == nil && modelListed(models, model)
}

func modelListed(models []ModelChoice, id string) bool {
	return id != "" && slices.ContainsFunc(models, func(model ModelChoice) bool { return model.ID == id })
}
