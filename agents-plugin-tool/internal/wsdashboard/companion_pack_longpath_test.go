package wsdashboard

import (
	"strings"
	"testing"
)

func TestCompanionExtendedPath(t *testing.T) {
	long := strings.Repeat(`\nested`, 40)
	for _, tc := range []struct{ in, want string }{
		{`C:\Users\me\.config\ws\companions`, `C:\Users\me\.config\ws\companions`},
		{`C:` + long, `\\?\C:` + long},
		{`\\server\share` + long, `\\?\UNC\server\share` + long},
		{`\\?\C:` + long, `\\?\C:` + long},
		{`\??\C:` + long, `\??\C:` + long},
		{`\\.\pipe` + long, `\\.\pipe` + long},
	} {
		if got := companionExtendedPath(tc.in); got != tc.want {
			t.Errorf("companionExtendedPath(%.40q...) = %.60q..., want %.60q...", tc.in, got, tc.want)
		}
	}
}
