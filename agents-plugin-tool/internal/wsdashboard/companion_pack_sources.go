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
// entry is judged alone: https only, at most companionURLMax characters,
// listed once, and at most companionSourcesMax entries in total.
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
			if err := companionHTTPSURL(url); err != nil {
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

// companionSourceListed reports whether source is a valid entry of the
// configured list. Fetch and install act only on listed sources, so a source
// is always one the user added.
func companionSourceListed(raw, source string) bool {
	for _, entry := range parseCompanionSources(raw) {
		if entry.Error == "" && entry.URL == source {
			return true
		}
	}
	return false
}
