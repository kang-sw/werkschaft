package wsdashboard

import "testing"

// ValidateLegacyUsageHistory applies the reader's whole-document protection,
// including each history's own sample validity.
func TestValidateLegacyUsageHistoryMatchesReader(t *testing.T) {
	const codexOK = `{"schema_version":1,"samples":[{"harness":"codex","kind":"five_hour","source":"via Codex CLI","resetsAt":0,"asOf":10,"percent":5}]}`
	const codexNegative = `{"schema_version":1,"samples":[{"harness":"codex","kind":"five_hour","source":"via Codex CLI","resetsAt":0,"asOf":-1,"percent":5}]}`
	if err := ValidateLegacyUsageHistory(planUsageCodexHistoryCache, "", []byte(codexOK)); err != nil {
		t.Fatalf("valid codex history refused: %v", err)
	}
	if err := ValidateLegacyUsageHistory(planUsageCodexHistoryCache, "", []byte(codexNegative)); err == nil {
		t.Fatal("codex history with a negative asOf accepted")
	}
	if _, err := parsePlanUsageHistoryWith([]byte(codexNegative), codexFiveHourHistorySpec); err == nil {
		t.Fatal("the reader accepts what the validator refuses")
	}
	// The shared history refuses a codex five-hour sample (claude only).
	if err := ValidateLegacyUsageHistory(planUsageHistoryCache, "", []byte(codexOK)); err == nil {
		t.Fatal("shared history accepted a codex five_hour sample")
	}
	if err := ValidateLegacyUsageHistory(planUsageAgyHistoryCache, "", []byte(`{"schema_version":2,"samples":[]}`)); err == nil {
		t.Fatal("newer agy history accepted")
	}
	if err := ValidateLegacyUsageHistory("sessions/x/"+planUsageCodexHistoryCache, "", []byte(codexOK)); err == nil {
		t.Fatal("a history outside the dashboard directory accepted")
	}
	if err := ValidateLegacyUsageHistory("other.json", "", []byte(codexOK)); err == nil {
		t.Fatal("unknown history name accepted")
	}
}
