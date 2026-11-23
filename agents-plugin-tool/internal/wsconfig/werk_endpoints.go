package wsconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
)

// ItemWerkEndpoints holds the machine's user-named Responses endpoints.
const ItemWerkEndpoints = "agent.werk_endpoints"

const localWerkProviderPrefix = "local/"

var werkEndpointName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// IsLocalWerkProvider reports whether id names a user-configured endpoint.
func IsLocalWerkProvider(id string) bool {
	name, ok := strings.CutPrefix(id, localWerkProviderPrefix)
	return ok && werkEndpointName.MatchString(name)
}

// WerkEndpoint is the config for one self-hosted Responses API root.
type WerkEndpoint struct {
	BaseURL string `json:"base_url"`
	Kind    string `json:"kind"`
}

func ParseWerkEndpoints(raw string) (map[string]WerkEndpoint, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var endpoints map[string]WerkEndpoint
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&endpoints); err != nil || endpoints == nil {
		return nil, errors.New("Werk endpoints must be a JSON object mapping names to base_url and kind")
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, errors.New("Werk endpoints must contain only one JSON object")
	}
	for name, endpoint := range endpoints {
		if !werkEndpointName.MatchString(name) {
			return nil, errors.New("Werk endpoint names must use lowercase letters, digits and hyphens and start with a letter or digit")
		}
		if endpoint.Kind != "vllm" && endpoint.Kind != "ollama" {
			return nil, fmt.Errorf("Werk endpoint %q needs kind vllm or ollama", name)
		}
		u, err := url.Parse(endpoint.BaseURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" ||
			u.User != nil || u.ForceQuery || u.RawQuery != "" || strings.Contains(endpoint.BaseURL, "#") {
			return nil, fmt.Errorf("Werk endpoint %q needs an absolute HTTP or HTTPS API root without credentials, query or fragment", name)
		}
		endpoint.BaseURL = strings.TrimRight(u.String(), "/")
		endpoints[name] = endpoint
	}
	return endpoints, nil
}

func LocalWerkProviderID(name string) string { return localWerkProviderPrefix + name }
