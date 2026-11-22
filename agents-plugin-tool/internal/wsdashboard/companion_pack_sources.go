package wsdashboard

import (
	"fmt"
	"strings"
)

// companionSourcesMax bounds the entries of dashboard.companion_sources.
const companionSourcesMax = 16

// companionSourceEntry is one entry of dashboard.companion_sources. An entry
// with Error is shown in the modal and skipped; it never fails the key.
type companionSourceEntry struct {
	URL   string `json:"url"`
	Error string `json:"error,omitempty"`
}

// parseCompanionSources splits the whitespace-separated source list. Each
// entry is judged alone by the fetcher's source rules (parseCompanionSource),
// listed once, and at most companionSourcesMax valid entries in total.
func parseCompanionSources(raw string) []companionSourceEntry {
	entries := []companionSourceEntry{}
	seen := map[string]bool{}
	valid := 0
	for _, url := range strings.Fields(raw) {
		entry := companionSourceEntry{URL: url}
		switch {
		case seen[url]:
			entry.Error = "this source is listed twice"
		case valid >= companionSourcesMax:
			entry.Error = fmt.Sprintf("at most %d sources are used", companionSourcesMax)
		default:
			if _, err := parseCompanionSource(url); err != nil {
				entry.Error = err.Error()
			}
		}
		seen[url] = true
		if entry.Error == "" {
			valid++
		}
		entries = append(entries, entry)
	}
	return entries
}

// companionSourceProblem reports whether source is a valid entry of the
// configured list. Fetch and install act only on listed sources, so a source
// is always one the user added. When source is listed only as an invalid
// entry, problem carries that entry's error; a source not listed at all has
// neither.
func companionSourceProblem(raw, source string) (problem string, listed bool) {
	for _, entry := range parseCompanionSources(raw) {
		if entry.URL != source {
			continue
		}
		if entry.Error == "" {
			return "", true
		}
		if problem == "" {
			problem = entry.Error
		}
	}
	return problem, false
}
