package wsagent

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

func (CodexBackend) sessionChoices() ([]ModelChoice, []string) {
	codexChoices.Lock()
	defer codexChoices.Unlock()
	if time.Since(codexChoices.at) >= choicesTTL {
		return nil, nil
	}
	return slices.Clone(codexChoices.models), slices.Clone(codexChoices.efforts)
}

func (b CodexBackend) hostedChoices() ([]ModelChoice, []string, error) {
	if models, efforts := b.sessionChoices(); len(models) > 0 {
		return models, efforts, nil
	}
	home := os.Getenv("CODEX_HOME")
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, nil
		}
		home = filepath.Join(userHome, ".codex")
	}
	// Support owns the TTL: one read per listing window, no stat/probe on
	// render. Never log raw bytes, paths, parse errors or account metadata.
	raw, err := os.ReadFile(filepath.Join(home, "models_cache.json"))
	if errors.Is(err, fs.ErrNotExist) {
		// A Codex never run on this machine has no cache: an empty list,
		// not a failure.
		return nil, nil, nil
	}
	var models []ModelChoice
	var efforts []string
	if err == nil {
		models, efforts = parseCodexCache(raw)
	}
	if reported, levels := b.sessionChoices(); len(reported) > 0 {
		return reported, levels, nil
	}
	// An existing file that is unreadable, empty, or names no model is a
	// failure, so it takes the short failure TTL and shows, not a cached
	// empty success.
	if len(models) == 0 {
		return nil, nil, errors.New("codex models_cache.json is unreadable, empty, or names no model")
	}
	return models, efforts, nil
}

func parseCodexCache(raw []byte) ([]ModelChoice, []string) {
	// Decode only the model vocabulary, not identity, etag or account data.
	var cache struct {
		Models []struct {
			Slug string `json:"slug"`
			// Visibility is Codex's picker visibility: "hide" and "none"
			// keep a model out of its own list.
			Visibility string `json:"visibility"`
			Levels     []struct {
				Effort string `json:"effort"`
			} `json:"supported_reasoning_levels"`
		} `json:"models"`
	}
	if json.Unmarshal(raw, &cache) != nil {
		return nil, nil
	}
	var models []ModelChoice
	var efforts []string
	for _, model := range cache.Models {
		if !modelPattern.MatchString(model.Slug) || model.Visibility == "hide" || model.Visibility == "none" {
			continue
		}
		if !slices.ContainsFunc(models, func(m ModelChoice) bool { return m.ID == model.Slug }) {
			models = append(models, ModelChoice{ID: model.Slug})
		}
		for _, level := range model.Levels {
			if level.Effort != "" && !slices.Contains(efforts, level.Effort) {
				efforts = append(efforts, level.Effort)
			}
		}
	}
	order := []string{"none", "off", "minimal", "low", "medium", "high", "xhigh", "max"}
	slices.SortStableFunc(efforts, func(a, b string) int {
		rank := func(value string) int {
			if i := slices.Index(order, value); i >= 0 {
				return i
			}
			return len(order)
		}
		return rank(a) - rank(b)
	})
	return models, efforts
}
