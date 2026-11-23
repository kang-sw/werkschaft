package wsagent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

func (AgyBackend) sessionChoices() ([]ModelChoice, []string) {
	agyChoices.mu.Lock()
	defer agyChoices.mu.Unlock()
	efforts := agyChoices.efforts
	if len(efforts) == 0 {
		efforts = agyEfforts
	}
	return slices.Clone(agyChoices.models), slices.Clone(efforts)
}

func (b AgyBackend) hostedChoices(ctx context.Context) ([]ModelChoice, []string, error) {
	models, efforts := b.sessionChoices()
	if len(models) > 0 {
		return models, efforts, nil
	}
	command, pre, err := harnessCommand(b.Command, "agy")
	if err != nil {
		return nil, efforts, fmt.Errorf("agy models: %w", err)
	}
	out, err := runListing(ctx, command, append(pre, "models"), agyEnv(StartSpec{Env: environ()}))
	// A session may have reported while the listing was running, even if
	// that listing failed. Its report wins without waiting out a cache TTL.
	if reported, levels := b.sessionChoices(); len(reported) > 0 {
		return reported, levels, nil
	}
	if err != nil {
		return nil, efforts, fmt.Errorf("agy models: %w", err)
	}
	// A clean exit with no model (agy signed out, say) is a failure, so it
	// takes the short failure TTL and shows, not a cached empty success.
	models = parseAgyModels(out)
	if len(models) == 0 {
		return nil, efforts, errors.New("agy models: the listing named no model; is agy signed in?")
	}
	return models, efforts, nil
}

func parseAgyModels(out []byte) []ModelChoice {
	var models []ModelChoice
	seen := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		id, _, ok := strings.Cut(strings.TrimSpace(line), "\t")
		if !ok { // progress and other non-table lines
			continue
		}
		base, _ := agyBaseModel(strings.TrimSpace(id))
		if seen[base] || !modelPattern.MatchString(base) {
			continue
		}
		seen[base] = true
		models = append(models, ModelChoice{ID: base})
	}
	return models
}

// runListing runs a harness's model listing and returns its stdout; a test
// replaces it with a fixture.
var runListing = func(ctx context.Context, command string, args []string, env []string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = env
	// A grandchild that inherited stdout (a Windows launcher shim's) must
	// not hold the listing, and Support's choicesMu, past ctx's timeout.
	cmd.WaitDelay = time.Second
	return cmd.Output()
}
