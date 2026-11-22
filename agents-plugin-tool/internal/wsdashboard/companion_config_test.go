package wsdashboard

import (
	"net/http"
	"path/filepath"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

func TestCompanionConfigNotificationRequiresMachineCredential(t *testing.T) {
	d := authFixture(t)
	opts := wsconfig.Options{ConfigHome: t.TempDir()}
	loads := 0
	d.config = &ConfigBackend{Options: opts, LoadCompanion: func() (CompanionConfig, error) {
		loads++
		return CompanionConfig{Mode: "visual"}, nil
	}}
	path, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, credential, origin, configID string
		cookie                             bool
		want                               int
	}{
		{name: "missing bearer", configID: companionConfigID(path), want: http.StatusForbidden},
		{name: "browser credential", cookie: true, configID: companionConfigID(path), want: http.StatusForbidden},
		{name: "browser origin", credential: d.endpoint.Token, origin: "http://127.0.0.1:43210", configID: companionConfigID(path), want: http.StatusForbidden},
		{name: "different config home", credential: d.endpoint.Token, configID: companionConfigID(filepath.Join(t.TempDir(), "config.json")), want: http.StatusNoContent},
		{name: "matching config home", credential: d.endpoint.Token, configID: companionConfigID(path), want: http.StatusNoContent},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := authRequest(http.MethodPost, companionChangedPath, "")
			req.Header.Set(companionConfigHeader, tc.configID)
			if tc.credential != "" {
				req.Header.Set("Authorization", "Bearer "+tc.credential)
			}
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			if tc.cookie {
				req.AddCookie(authCookie(t, d))
			}
			if got := authServe(d, req); got.Code != tc.want {
				t.Fatalf("status = %d, want %d", got.Code, tc.want)
			}
		})
	}
	if loads != 1 || d.companion.current().config.Mode != "visual" {
		t.Fatalf("companion settings reloaded %d times: %+v", loads, d.companion.current().config)
	}
	current := d.companion.current()
	if handled := d.NotifyCompanionConfigChange(t.TempDir(), path); handled {
		t.Fatal("handled a notification for a different daemon state directory")
	}
	if handled := d.NotifyCompanionConfigChange(d.dir, path); !handled || loads != 2 {
		t.Fatalf("hosted notification handled = %v, loads = %d", handled, loads)
	}
	if got := d.companion.current(); got != current {
		t.Fatalf("unchanged companion values published a fresh snapshot: %+v", got.config)
	}
}
