package mcp

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// envRegenSettingsDescriptionDigests rewrites the digest fixture from the
// current manifests.
const envRegenSettingsDescriptionDigests = "WS_REGEN_SETTINGS_DESCRIPTION_DIGESTS"

const settingsDescriptionDigestFixture = "testdata/settings-description-digests.json"

// settingsDescriptionManifests are the source manifests whose key descriptions
// the dashboard's locale tables summarize. The wsflow copies are mirrors the
// mirroring rules keep in step, so they are not listed.
var settingsDescriptionManifests = []string{
	"agents-plugin/config-manifest.json",
	"agents-plugin/agy-config-manifest.json",
	"agents-plugin/dashboard-config-manifest.json",
}

// settingsDescriptionDigests maps "<manifest>#<key>" to a digest of the key's
// description in the manifest.
func settingsDescriptionDigests(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, rel := range settingsDescriptionManifests {
		raw, err := os.ReadFile(filepath.Join("..", "..", "..", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Keys []struct{ Key, Description string }
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		for _, entry := range manifest.Keys {
			sum := sha256.Sum256([]byte(entry.Description))
			out[rel+"#"+entry.Key] = hex.EncodeToString(sum[:])[:16]
		}
	}
	return out
}

// The dashboard's locale tables carry a translated summary of each manifest
// description, which the catalog test cannot compare against the manifest. A
// manifest description that changes without a locale review leaves every
// translation describing the old behavior (agy.model_wait_timeout_seconds did,
// after agy.stall_log_check). This fixture makes the change visible: it fails
// until the locale text is reviewed and the fixture regenerated.
func TestSettingsDescriptionsMatchTheLocaleReviewFixture(t *testing.T) {
	got := settingsDescriptionDigests(t)
	if os.Getenv(envRegenSettingsDescriptionDigests) == "1" {
		raw, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(settingsDescriptionDigestFixture, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(settingsDescriptionDigestFixture)
	if err != nil {
		t.Fatalf("%v (regenerate with %s=1)", err, envRegenSettingsDescriptionDigests)
	}
	var want map[string]string
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	var drift []string
	for key, digest := range got {
		switch old, ok := want[key]; {
		case !ok:
			drift = append(drift, key+": new key")
		case old != digest:
			drift = append(drift, key+": description changed")
		}
	}
	for key := range want {
		if _, ok := got[key]; !ok {
			drift = append(drift, key+": key removed")
		}
	}
	if len(drift) > 0 {
		sort.Strings(drift)
		t.Fatalf("manifest descriptions changed without a locale review:\n  %s\n"+
			"Review the matching js.settings.key.<key>.description entries in every internal/wsi18n/tables/*.json, then run:\n  %s=1 go test ./internal/mcp -run TestSettingsDescriptionsMatchTheLocaleReviewFixture -count=1",
			strings.Join(drift, "\n  "), envRegenSettingsDescriptionDigests)
	}
}

// Every locale's agy.model_wait_timeout_seconds text names the log check the
// manifest description gained, so a translation cannot silently fall back to
// "time to wait before a retry".
func TestAgyModelWaitTimeoutLocaleTextNamesTheLogCheck(t *testing.T) {
	tables, err := filepath.Glob("../wsi18n/tables/*.json")
	if err != nil || len(tables) != 11 {
		t.Fatalf("locale tables = %v, %v, want 11", tables, err)
	}
	const id = "js.settings.key.agy.model_wait_timeout_seconds.description"
	for _, path := range tables {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var table map[string]string
		if err := json.Unmarshal(raw, &table); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		if !strings.Contains(table[id], "agy.stall_log_check") {
			t.Errorf("%s: %s = %q does not mention agy.stall_log_check", filepath.Base(path), id, table[id])
		}
	}
}
