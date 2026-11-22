package wsdashboard

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
	"github.com/kang-sw/devenv/internal/wsi18n"
)

func TestCompanionConfigNotificationRequiresMachineCredential(t *testing.T) {
	d := authFixture(t)
	opts := wsconfig.Options{ConfigHome: t.TempDir()}
	loads := 0
	d.config = &ConfigBackend{Options: opts, LoadCompanion: func() (CompanionConfig, error) {
		loads++
		return CompanionConfig{Enabled: true}, nil
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
	if loads != 1 || !d.companion.current().config.Enabled {
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

// The shell embeds the catalog's companion-modal rows and keeps exactly the
// settings strings those rows name, in every language.
func TestCompanionModalRowsAndShellStrings(t *testing.T) {
	catalog, err := os.ReadFile("assets/dashboard-settings-catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := companionModalRows(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var rows []companionModalRow
	if err := json.Unmarshal([]byte(raw), &rows); err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, row := range rows {
		keys = append(keys, row.Key)
	}
	want := []string{CompanionPackKey, CompanionPresetKey, CompanionAgentCooldownKey, CompanionAgentHourlyCapKey}
	if !slices.Equal(keys, want) {
		t.Fatalf("modal rows = %v, want %v", keys, want)
	}
	for _, lang := range wsi18n.Languages() {
		script := string(wsi18n.ScriptExceptKeeping(lang.Code, []string{"js.settings."}, shellSettingsStrings))
		for _, row := range rows {
			for _, id := range []string{row.Label, row.Description, row.Unit} {
				if id != "" && !strings.Contains(script, strconv.Quote(id)) {
					t.Errorf("%s: the shell drops %s", lang.Code, id)
				}
			}
		}
		for _, dropped := range []string{"js.settings.save", "js.settings.key.dashboard.lan_enabled.label"} {
			if strings.Contains(script, strconv.Quote(dropped)) {
				t.Errorf("%s: the shell keeps settings-page string %s", lang.Code, dropped)
			}
		}
		if !strings.Contains(script, strconv.Quote("js.companion.settings")) {
			t.Errorf("%s: the shell lacks the companion modal title", lang.Code)
		}
	}
	if _, err := companionModalRows([]byte("{")); err == nil {
		t.Fatal("a malformed catalog was accepted")
	}
}

// reloadAgents is an agent host that keeps global configuration in memory
// and counts the daemon's requests to reread it.
type reloadAgents struct{ reloads int }

func (a *reloadAgents) Handler() http.Handler               { return http.NotFoundHandler() }
func (a *reloadAgents) Start(func(replyID, project string)) {}
func (a *reloadAgents) InboxChanged(string)                 {}
func (a *reloadAgents) Close()                              {}
func (a *reloadAgents) ReloadGlobalConfig()                 { a.reloads++ }

// TestGlobalConfigNotificationReloadsTheAgentHost: an authenticated
// invalidation of this daemon's own global file, by the route or in process,
// rereads the agent host's global settings too; a refused request, another
// config home, and another daemon's state directory do not.
func TestGlobalConfigNotificationReloadsTheAgentHost(t *testing.T) {
	d := authFixture(t)
	opts := wsconfig.Options{ConfigHome: t.TempDir()}
	agents := &reloadAgents{}
	d.agents = agents
	d.config = &ConfigBackend{Options: opts, LoadCompanion: func() (CompanionConfig, error) { return CompanionConfig{}, nil }}
	path, err := wsconfig.GlobalPath(opts)
	if err != nil {
		t.Fatal(err)
	}
	send := func(credential, configID string) int {
		req := authRequest(http.MethodPost, companionChangedPath, "")
		req.Header.Set(companionConfigHeader, configID)
		if credential != "" {
			req.Header.Set("Authorization", "Bearer "+credential)
		}
		return authServe(d, req).Code
	}
	if code := send("", companionConfigID(path)); code != http.StatusForbidden || agents.reloads != 0 {
		t.Fatalf("unauthenticated: status %d, reloads %d", code, agents.reloads)
	}
	if code := send(d.endpoint.Token, companionConfigID(filepath.Join(t.TempDir(), "config.json"))); code != http.StatusNoContent || agents.reloads != 0 {
		t.Fatalf("another config home: status %d, reloads %d", code, agents.reloads)
	}
	if code := send(d.endpoint.Token, companionConfigID(path)); code != http.StatusNoContent || agents.reloads != 1 {
		t.Fatalf("matching: status %d, reloads %d", code, agents.reloads)
	}
	if d.NotifyCompanionConfigChange(t.TempDir(), path) || agents.reloads != 1 {
		t.Fatalf("another state directory reloaded: %d", agents.reloads)
	}
	if !d.NotifyCompanionConfigChange(d.dir, path) || agents.reloads != 2 {
		t.Fatalf("in-process notification: reloads %d", agents.reloads)
	}
	for _, key := range []string{CompanionEnabledKey, CompanionDirectorKey, wsconfig.ItemAgentKeepAwakeDuringTurns} {
		if !IsDaemonGlobalConfigKey(key) {
			t.Errorf("%s is not a daemon global key", key)
		}
	}
	if IsDaemonGlobalConfigKey(wsconfig.ItemAgentChildWaitingDays) {
		t.Error("agent.child_waiting_days is read once per start, not invalidated")
	}
}
