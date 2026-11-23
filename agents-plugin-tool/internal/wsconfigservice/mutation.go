package wsconfigservice

import (
	"encoding/json"
	"maps"
	"slices"
	"strings"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

type Operation struct {
	Key       string            `json:"key"`
	Selectors map[string]string `json:"selectors"`
	Action    string            `json:"action"`
	Value     any               `json:"value,omitempty"`
}

type Mutation struct {
	Operation
	StoredKey  string
	Scalar     string
	Structured json.RawMessage
	// IsStructured marks a write to a structured (JSON object) key, a
	// profile family member or an exact key such as agent.tier_routing, so
	// Apply and AfterWrite address structured_overrides for a reset too.
	IsStructured bool
	// Precondition (nil: none) runs on the stored file inside a structured
	// write's locked read-modify-write; its error refuses the write and is
	// returned by Apply unchanged.
	Precondition wsconfig.StructuredPrecondition
}

func (c *Catalog) Validate(scope wsconfig.Scope, op Operation) (Mutation, error) {
	rule, ok := c.rule(op.Key)
	if !ok || strings.HasSuffix(op.Key, ".*") || (op.Action != "set" && op.Action != "reset") ||
		(op.Action == "set" && op.Value == nil) || (op.Action == "reset" && op.Value != nil) {
		return Mutation{}, ErrInvalid
	}
	if scope == wsconfig.ScopeBuiltin || rule.ReadOnly || !rule.scopeMayResolve(scope) ||
		(rule.Structured && scope != wsconfig.ScopeGlobal && scope != wsconfig.ScopeProject) ||
		(rule.Key == "agents.tier" && scope != wsconfig.ScopeGlobal && scope != wsconfig.ScopeProject) {
		return Mutation{}, ErrScope
	}
	harness := op.Selectors["harness"]
	// The preference migration can remove the common value stored before
	// prompt overrides had harness buckets. A bare reset names that old key.
	barePreferenceReset := op.Key == "prompt.UserPreferenceSection" && op.Action == "reset" && len(op.Selectors) == 0
	if len(rule.Harnesses) > 0 && !slices.Contains(rule.Harnesses, harness) && !barePreferenceReset {
		return Mutation{}, ErrInvalid
	}
	for field := range op.Selectors {
		if (field != "harness" || len(rule.Harnesses) == 0) && (field != "tier" || op.Key != "agents.tier") {
			return Mutation{}, ErrInvalid
		}
	}
	m := Mutation{Operation: op, StoredKey: rule.StoreKey(harness)}
	if rule.Structured {
		m.IsStructured = true
		// An exact structured key has no name part to validate.
		if rule.ValidateName != nil && rule.ValidateName(strings.TrimPrefix(op.Key, strings.TrimSuffix(FamilyKey(op.Key), "*"))) != nil {
			return Mutation{}, ErrInvalid
		}
		if op.Action == "set" {
			value, err := rule.NormalizeObject(op.Value, scope)
			if err != nil {
				return Mutation{}, InvalidValue{Reason: err}
			}
			m.Structured = value
		}
		return m, nil
	}
	if op.Key == "agents.tier" {
		tier := op.Selectors["tier"]
		if tier == "" || wsconfig.NormalizedTier(tier) != tier {
			return Mutation{}, ErrInvalid
		}
		if op.Action == "set" && !validTierObject(op.Value, tier, harness) {
			return Mutation{}, ErrInvalid
		}
		return m, nil
	}
	if op.Action == "reset" {
		return m, nil
	}
	value, ok := op.Value.(string)
	if !ok {
		switch v := op.Value.(type) {
		case bool:
			if rule.Type == "boolean" {
				value = "false"
				if v {
					value = "true"
				}
				ok = true
			}
		case json.Number:
			if rule.Type == "integer" {
				value, ok = v.String(), true
			}
		}
	}
	if !ok {
		return Mutation{}, ErrInvalid
	}
	if rule.Normalize != nil {
		var err error
		value, err = rule.Normalize(value)
		if err != nil {
			return Mutation{}, ErrInvalid
		}
	}
	m.Scalar = value
	return m, nil
}

func validTierObject(value any, tier, harness string) bool {
	fields, ok := value.(map[string]any)
	if !ok || len(fields) == 0 {
		return false
	}
	for k, v := range fields {
		s, ok := v.(string)
		if !ok {
			return false
		}
		switch k {
		case "tier":
			if wsconfig.NormalizedTier(s) != tier {
				return false
			}
		case "backend", "model", "effort":
		case "provider":
			if wsconfig.ValidateTierProvider(harness, strings.ToLower(strings.TrimSpace(s))) != nil {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func (m Mutation) Apply(scope wsconfig.Scope, opts wsconfig.Options, sessionKey string, writer wsconfig.SessionWriter) error {
	if scope == wsconfig.ScopeRepo || scope == wsconfig.ScopeBuiltin {
		return ErrScope
	}
	if m.Key == "agents.tier" {
		return m.writeTier(scope, opts)
	}
	r := wsconfig.NewResolver(opts, nil, nil, writer)
	if m.Action == "reset" && m.IsStructured {
		return r.UnsetStructuredIf(m.StoredKey, scope, m.Precondition)
	}
	if m.Structured != nil {
		return r.SetStructuredIf(m.StoredKey, m.Structured, scope, m.Precondition)
	}
	target := wsconfig.SetOptions{ExplicitScope: scope, SessionKey: sessionKey}
	if m.Action == "reset" {
		return r.Unset(m.StoredKey, target)
	}
	return r.Set(m.StoredKey, m.Scalar, target)
}

func (m Mutation) writeTier(scope wsconfig.Scope, opts wsconfig.Options) error {
	fields := map[string]any{"tier": m.Selectors["tier"]}
	reset := m.Action == "reset"
	if !reset {
		maps.Copy(fields, m.Value.(map[string]any))
	}
	_, _, err := WriteTier(opts, fields, m.Selectors["harness"], scope, reset)
	return err
}

// AfterWrite records the committed scalar when refreshing a layer fails
// after a successful save. The response marks the resolution stale.
func (m Mutation) AfterWrite(scope wsconfig.Scope, layers Layers) Layers {
	if m.Key == "agents.tier" {
		return layers
	}
	file := layers.Files[scope]
	values := file.Overrides
	if scope == wsconfig.ScopeSession {
		values = layers.Session.Own
	}
	if m.Structured != nil || m.IsStructured {
		if file.StructuredOverrides == nil {
			file.StructuredOverrides = make(map[string]json.RawMessage)
		}
		if m.Action == "reset" {
			delete(file.StructuredOverrides, m.StoredKey)
		} else {
			file.StructuredOverrides[m.StoredKey] = m.Structured
		}
	} else {
		if values == nil {
			values = make(map[string]string)
		}
		if m.Action == "reset" {
			delete(values, m.StoredKey)
		} else {
			values[m.StoredKey] = m.Scalar
		}
		if scope == wsconfig.ScopeSession {
			layers.Session.Own = values
			if m.Action == "set" {
				if layers.Session.Effective == nil {
					layers.Session.Effective = make(map[string]string)
				}
				layers.Session.Effective[m.StoredKey] = m.Scalar
			}
		} else {
			file.Overrides = values
		}
	}
	layers.Files[scope] = file
	return layers
}
