package wsdashboard

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsi18n"
)

// settingsClientTable is the string table a language's Settings page loads,
// read back from the served immutable file.
func settingsClientTable(t *testing.T, a *dashboardAssets, lang string) map[string]string {
	t.Helper()
	file, ok := a.files[a.data.SettingsI18nFiles[lang]]
	if !ok {
		t.Fatalf("%s: no Settings i18n file", lang)
	}
	_, rest, found := strings.Cut(file.body, ", t: ")
	table, _, ok2 := strings.Cut(rest, " };\n")
	if !found || !ok2 {
		t.Fatalf("%s: unexpected Settings i18n script shape: %.120s", lang, file.body)
	}
	out := map[string]string{}
	if err := json.Unmarshal([]byte(table), &out); err != nil {
		t.Fatalf("%s: Settings i18n table: %v", lang, err)
	}
	return out
}

// TestSettingsPageCarriesTheCompanionAgentsTermsNotice: the Settings page
// loads only its js.settings.* strings (the frame table is not loaded there),
// so the Companion Agents card's js.lead.agy.terms notice rendered as the raw
// key until the page's table kept it. Every language ships the localized text.
func TestSettingsPageCarriesTheCompanionAgentsTermsNotice(t *testing.T) {
	const key = "js.lead.agy.terms"
	a, err := loadDashboardAssets()
	if err != nil {
		t.Fatal(err)
	}
	for _, lang := range wsi18n.Languages() {
		table := settingsClientTable(t, a, lang.Code)
		got, want := table[key], wsi18n.T(lang.Code, key)
		if got == "" || got == key || got != want || !strings.Contains(got, "Antigravity") {
			t.Errorf("%s: Settings page table has %q = %q, want the localized notice %q", lang.Code, key, got, want)
		}
		if _, ok := table["js.settings.title"]; !ok {
			t.Errorf("%s: Settings page table lost its js.settings.* strings", lang.Code)
		}
		// Only the listed shared keys ride along: no other namespace leaks in.
		for k := range table {
			if !strings.HasPrefix(k, "js.settings.") && k != key {
				t.Errorf("%s: unexpected non-settings key %s in the Settings page table", lang.Code, k)
			}
		}
	}
}

// TestSettingsScriptKeysAreInThePageTable: every client string the Settings
// scripts name literally is in the Settings page's table, so none renders as
// its raw key.
func TestSettingsScriptKeysAreInThePageTable(t *testing.T) {
	a, err := loadDashboardAssets()
	if err != nil {
		t.Fatal(err)
	}
	keys := regexp.MustCompile(`["'](js\.[a-z0-9_]+(?:\.[a-z0-9_-]+)+)["']`).FindAllStringSubmatch(string(a.data.ShellScript)+"\n"+string(a.data.SettingsScript), -1)
	if len(keys) == 0 {
		t.Fatal("no client string keys found in the Settings script")
	}
	for _, lang := range wsi18n.Languages() {
		table := settingsClientTable(t, a, lang.Code)
		for _, m := range keys {
			if _, ok := table[m[1]]; !ok {
				if _, known := wsi18n.Lookup(lang.Code, m[1]); !known {
					continue // not a table key (a dotted data value, an id)
				}
				t.Errorf("%s: the Settings script shows %s but the Settings page table lacks it", lang.Code, m[1])
			}
		}
	}
}
