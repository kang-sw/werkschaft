// Package wscred is the provider authentication layer: the one owner of
// provider credentials. Consumers (an agent harness, web tools, later image
// or video features) ask a Source for a provider's current credential by
// provider ID; none reads the credential file, a key, or a token on its own.
//
//   - Registry. Providers lists the providers the layer knows, each with a
//     stable ID. A new API-key provider is one more entry; nothing else in
//     the contract changes.
//   - Store. The daemon holds the one live Store (store.go): the credential
//     file is read once, at the first credential use, and rewritten only when
//     a credential changes. While a daemon runs it is the file's only writer;
//     a CLI change reaches it over a daemon channel instead.
//   - Route. A harness process the daemon started obtains a credential over
//     the daemon's internal token route (route.go), authenticated by its
//     per-session bearer; the route is not an MCP tool, and no MCP tool,
//     config surface, or model-visible text ever carries a credential.
//   - Redaction. A secret travels as Secret, whose every printed or
//     marshalled form is redacted; only Reveal returns the value, at the one
//     place that must send it (an Authorization header, the token route's
//     answer, the credential file).
package wscred

import "slices"

// ProviderID is a provider's stable identifier: the credential file's key,
// the token route's argument, and the CLI's argument.
type ProviderID string

// ProviderOpenRouter is OpenRouter, authenticated by an API key.
const ProviderOpenRouter ProviderID = "openrouter"

// Kind is how a provider authenticates.
type Kind string

// KindAPIKey is a static bearer API key the user supplies.
const KindAPIKey Kind = "api_key"

// Provider is one registry entry.
type Provider struct {
	ID ProviderID
	// Name is the provider's display name.
	Name string
	Kind Kind
	// BaseURL is the provider's API root its credential authenticates
	// against.
	BaseURL string
	// KeyPrefix is the non-secret prefix the provider's keys start with,
	// which a masked display keeps; "" when its keys have none.
	KeyPrefix string
}

// providers is the registry, in display order.
var providers = []Provider{
	{ID: ProviderOpenRouter, Name: "OpenRouter", Kind: KindAPIKey, BaseURL: "https://openrouter.ai/api/v1", KeyPrefix: "sk-or-"},
}

// Providers returns the registry in display order.
func Providers() []Provider { return slices.Clone(providers) }

// Lookup returns the provider with id.
func Lookup(id ProviderID) (Provider, bool) {
	for _, p := range providers {
		if p.ID == id {
			return p, true
		}
	}
	return Provider{}, false
}

// Mask is the display form of a provider's key: the provider's non-secret
// prefix and the last four characters, never more. A key too short to keep
// any of it shows as a bare mask.
func Mask(p Provider, key string) string {
	const tail = 4
	if len(key) < 16 {
		return "****"
	}
	prefix := ""
	if p.KeyPrefix != "" && len(key) > len(p.KeyPrefix)+tail && key[:len(p.KeyPrefix)] == p.KeyPrefix {
		prefix = p.KeyPrefix
	}
	return prefix + "..." + key[len(key)-tail:]
}
