package wsconfig

// FileLayers captures the raw config from each file scope once. A caller
// supplies the worktree root only when its inspection context is known.
func FileLayers(opts Options) (map[Scope]Config, error) {
	layers := make(map[Scope]Config, 3)
	var err error
	layers[ScopeGlobal], err = loadGlobalConfig(opts)
	if err != nil {
		return nil, err
	}
	layers[ScopeProject], err = loadProjectConfig(opts)
	if err != nil {
		return nil, err
	}
	if _, ok := RepoPath(opts); ok {
		layers[ScopeRepo], err = loadRepoConfig(opts)
		if err != nil {
			return nil, err
		}
	}
	return layers, nil
}

// TierInspection preserves the resolver's bucket fallback and labels the
// legacy tier seed separately from a writable harness-specific leaf.
type TierInspection struct {
	Default, Value AgentTier
	Scope          Scope
	Bucket         string
	Layers         map[Scope]TierLayer
}

type TierLayer struct {
	Value    AgentTier
	Bucket   string
	Selected *AgentTier
	Legacy   bool
}

func InspectTier(files map[Scope]Config, tier, harness string) TierInspection {
	global, project := files[ScopeGlobal], files[ScopeProject]
	result := TierInspection{Scope: ScopeBuiltin, Layers: make(map[Scope]TierLayer, 2)}
	if harness == "werk" || harness == "pi-rpc" {
		// werk and pi-rpc have no builtin tier defaults and reject unconfigured
		// tiers rather than falling back to default/codex.
		for _, scope := range []Scope{ScopeProject, ScopeGlobal} {
			if own, ok := files[scope].Agents.ModelAliases[tier][harness]; ok {
				effective := own
				if harness == "werk" && (effective.Backend == "" || effective.Backend == "werk") && effective.Provider == "" {
					effective.Provider = WerkDefaultProvider
				}
				if harness == "pi-rpc" && effective.Backend == "" && effective.Model != "" {
					effective.Backend = "pi-rpc"
				}
				selected := own
				result.Layers[scope] = TierLayer{Value: effective, Bucket: harness, Selected: &selected}
				if result.Scope == ScopeBuiltin {
					result.Value, result.Bucket, result.Scope = effective, harness, scope
				}
			}
		}
		return result
	}
	result.Default, _, _ = resolveAliasMapping(effectiveAgentConfig(), tier, "", harness)
	result.Value, result.Bucket, _ = resolveAliasMapping(effectiveAgentConfig(global, project), tier, "", harness)
	scopes := []Scope{ScopeProject, ScopeGlobal}
	seeded := result.Bucket == "default" || result.Bucket == "codex" || result.Bucket == "tiers"
	for _, scope := range scopes {
		stored := files[scope]
		aliases := stored.Agents.ModelAliases[tier]
		own, selected := aliases[harness]
		winner, explicit := aliases[result.Bucket]
		switch {
		case selected:
			result.Layers[scope] = TierLayer{Value: own, Bucket: harness, Selected: &own}
		case explicit:
			result.Layers[scope] = TierLayer{Value: winner, Bucket: result.Bucket}
		case seeded:
			if legacy, ok := stored.Agents.Tiers[tier]; ok {
				result.Layers[scope] = TierLayer{Value: legacy, Bucket: "tiers", Legacy: true}
			}
		}
		if explicit && result.Scope == ScopeBuiltin {
			result.Scope = scope
		}
	}
	if result.Scope == ScopeBuiltin && seeded {
		for _, scope := range scopes {
			if _, ok := files[scope].Agents.Tiers[tier]; ok {
				result.Scope = scope
				break
			}
		}
	}
	return result
}
