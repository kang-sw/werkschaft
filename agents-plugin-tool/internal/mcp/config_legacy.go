package mcp

import (
	"fmt"
	"strings"

	"github.com/kang-sw/devenv/internal/wsdashboard"
)

// A released key or enum value that a later release removed stays accepted by
// config.tune (a removal without an alias refuses old callers: ai-docs/manuals/
// release-compatibility.md, Tools and Protocol Mapping). The declaration keeps
// the current domain; these helpers translate the removed spellings.

// deprecatedTuneValue maps a removed released value of a declared key to the
// value that replaced it. Only the director's 0.51.0 values were removed from
// a declared enum; the deprecated companion_mode key is still declared whole.
func deprecatedTuneValue(key, value string) (string, bool) {
	if key == wsdashboard.CompanionDirectorKey {
		return wsdashboard.CompanionLegacyDirector(value)
	}
	return "", false
}

// deprecatedTuneNote tells a config.tune caller that the key or value it used
// is deprecated and what was stored, or returns "" for a current spelling.
// input is the value as sent, stored the value written.
func deprecatedTuneNote(key, input, stored string) string {
	switch {
	case key == wsdashboard.CompanionModeKey:
		return fmt.Sprintf("deprecated: %s is an alias of %s (stored together); use %s\n",
			key, wsdashboard.CompanionEnabledKey, wsdashboard.CompanionEnabledKey)
	case key == wsdashboard.CompanionDirectorKey && strings.TrimSpace(input) != stored:
		if _, legacy := wsdashboard.CompanionLegacyDirector(input); legacy {
			return fmt.Sprintf("deprecated: %s value %q is stored as %q\n", key, strings.TrimSpace(input), stored)
		}
	}
	return ""
}
