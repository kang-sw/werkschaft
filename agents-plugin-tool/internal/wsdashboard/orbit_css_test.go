package wsdashboard

import (
	"strings"
	"testing"
)

// TestRailHarnessIcon pins the shared harness badge: one .hb definition keyed
// by data-h, a spinner ring (.hbr) drawn over a working badge, approval
// wobbles and hops it (and wins over working), failure shows as a static
// ring, and reduced motion stops both animations.
func TestRailHarnessIcon(t *testing.T) {
	css := mustAsset(t, "dashboard.css")
	for _, want := range []string{
		`.hb[data-h="claude"]::before { content: "Cl"; }`,
		`.hb[data-h="pi"]::before { content: "Pi"; }`,
		`.hb[data-h="codex"]::before { content: "Co"; }`,
		`.hb[data-h="agy"]::before { content: "Ag"; }`,
		`.hb::before { content: "??"; }`,
		".hb.sm { width: 19px; height: 12px;",
		"@keyframes hbspin { to { transform: rotate(360deg); } }",
		".hb .hbr { position: absolute; inset: -2px;",
		"@keyframes rail-nudge {",
		".rail .hb.perm { outline: 2px solid var(--wb-perm-ink); animation: rail-nudge 1.5s steps(4) infinite; }",
		".rail .hb.err { outline: 2px solid var(--wb-fail); }",
		".hb .hbr, .rail .hb.perm { animation: none; }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard.css lacks %q", want)
		}
	}
	if strings.Contains(css, ".rail .hi ") || strings.Contains(css, ".rail .hi.") || strings.Contains(css, "stroke-dashoffset") {
		t.Error("dashboard.css still carries a retired per-site harness icon rule")
	}
	tmpl := mustAsset(t, "dashboard.tmpl")
	if !strings.Contains(tmpl, `{{if .Approval}} perm{{else if .Working}}{{else if .Failure}} err{{end}}`) ||
		!strings.Contains(tmpl, `{{if and .Working (not .Approval)}}<span class="hbr"></span>{{end}}`) {
		t.Error("the rail row lacks the spinner ring element or the approval-over-working order")
	}
}

// TestHarnessBadgeFont pins the bundled badge font: the stylesheet declares
// it under a hashed name the page serves, and its OFL text ships beside it.
func TestHarnessBadgeFont(t *testing.T) {
	if len(harnessFontWOFF2) < 100 || string(harnessFontWOFF2[:4]) != "wOF2" {
		t.Fatalf("the embedded badge font is not a woff2 file (%d bytes)", len(harnessFontWOFF2))
	}
	if raw, err := harnessFontLicense.ReadFile("assets/harness-font/OFL.txt"); err != nil || !strings.Contains(string(raw), "SIL OPEN FONT LICENSE Version 1.1") {
		t.Errorf("the badge font's OFL text is missing: %v", err)
	}
	if css := mustAsset(t, "dashboard.css"); !strings.Contains(css, `"`+harnessFontFamily+`"`) {
		t.Errorf("dashboard.css does not name the badge font family %q", harnessFontFamily)
	}
	if !strings.Contains(harnessFontFace(), "/s/"+harnessFontName()) {
		t.Error("the badge font face does not point at the served name")
	}
}

// TestRailUnreadAttentionCSS pins the unread session attention animation:
// a 3x fast flash on arrival chaining seamlessly at 1.14s into a continuous
// 1Hz gradient pulse, plus a static amber fallback under reduced motion.
func TestRailUnreadAttentionCSS(t *testing.T) {
	css := mustAsset(t, "dashboard.css")
	for _, want := range []string{
		"background: linear-gradient(90deg, var(--wb-accent-policy) 0%, color-mix(in srgb, var(--wb-accent-policy) 68%, transparent) 60%, color-mix(in srgb, var(--wb-accent-policy) 18%, transparent) 100%);",
		"animation: attn-breathe 1s ease-in-out infinite;",
		"animation: attn-breathe-ring 1s ease-in-out infinite;",
		".rail .rs.attn.attn-flash::before { animation: attn-flash .38s ease-in-out 3, attn-breathe 1s ease-in-out 1.14s infinite; }",
		".rail .rs.attn.attn-flash .hb::after { animation: attn-flash-ring .38s ease-in-out 3, attn-breathe-ring 1s ease-in-out 1.14s infinite; }",
		"@keyframes attn-flash { 0%, 100% { opacity: .04; } 50% { opacity: .44; } }",
		"@keyframes attn-breathe { 0%, 100% { opacity: .04; } 50% { opacity: .28; } }",
		".rail .rs.attn::before { animation: none !important; opacity: .16; }",
		".rail .rs.attn .hb::after { animation: none !important; box-shadow: 0 0 0 2px color-mix(in srgb, var(--wb-accent-policy) 55%, transparent); }",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard.css lacks %q", want)
		}
	}
}

// TestRailSelectedNeonOutlineCSS pins the crisp neon primary outline on the
// currently selected rail session row (.rail .rs.cur), covering both top-level
// and nested child .rs rows in light and dark themes without shifting layout or
// colliding with .attn pseudo-element animations.
func TestRailSelectedNeonOutlineCSS(t *testing.T) {
	css := mustAsset(t, "dashboard.css")
	for _, want := range []string{
		".rail .rs.cur {",
		"background: color-mix(in srgb, var(--wb-primary) 12%, var(--wb-card));",
		"inset 0 0 0 1.5px var(--wb-primary),",
		"inset 0 0 8px light-dark(color-mix(in srgb, var(--wb-primary) 12%, transparent), color-mix(in srgb, var(--wb-primary) 24%, transparent)),",
		"0 0 6px light-dark(color-mix(in srgb, var(--wb-primary) 26%, transparent), color-mix(in srgb, var(--wb-primary) 52%, transparent)),",
		"0 0 2px light-dark(transparent, color-mix(in srgb, var(--wb-primary) 80%, transparent));",
	} {
		if !strings.Contains(css, want) {
			t.Errorf("dashboard.css lacks %q", want)
		}
	}
}
