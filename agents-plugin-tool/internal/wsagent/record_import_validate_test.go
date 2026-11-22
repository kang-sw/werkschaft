package wsagent

import (
	"os"
	"path/filepath"
	"testing"
)

// ValidateLegacyRecord is readRecord's decode: every known member keeps its
// type and the record names its own directory with a key and a root.
func TestValidateLegacyRecordMatchesReadRecord(t *testing.T) {
	cases := map[string]struct {
		raw string
		ok  bool
	}{
		"valid with unknown member": {`{"schema_version":1,"reply_id":"abc","root":"/r","ws_session_key":"k","future":{"x":1}}`, true},
		"mistyped known member":     {`{"reply_id":"abc","root":"/r","ws_session_key":"k","closed_at":123}`, false},
		"other reply-id":            {`{"reply_id":"xyz","root":"/r","ws_session_key":"k"}`, false},
		"no key":                    {`{"reply_id":"abc","root":"/r"}`, false},
		"not an object":             {`[]`, false},
	}
	for name, c := range cases {
		dir := filepath.Join(t.TempDir(), "abc")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(recordPath(dir), []byte(c.raw), 0o600); err != nil {
			t.Fatal(err)
		}
		_, readErr := readRecord(dir)
		err := ValidateLegacyRecord("abc", []byte(c.raw))
		if (err == nil) != c.ok || (readErr == nil) != c.ok {
			t.Errorf("%s: validator err %v, readRecord err %v, want ok=%v", name, err, readErr, c.ok)
		}
	}
}
