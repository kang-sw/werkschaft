package wsdashboard

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strings"

	"github.com/kang-sw/devenv/internal/wsi18n"
)

//go:embed assets/companion-rig-studio.tmpl
var companionRigStudioTmplFS embed.FS

const companionRigStudioRoute = "/companion/studio"

// companionRigStudioScriptFiles lists the scripts inlined (with per-response
// nonce) into the standalone /companion/studio page. Studio-only files live
// exclusively here and are never added to companionScriptFiles or dashboard.tmpl.
var companionRigStudioScriptFiles = []string{
	"hold-confirm.js",
	"dashboard-companion-packs.js",
	"dashboard-companion-vrm-fx.js",
	"dashboard-companion-vrm-studio.js",
	"dashboard-companion-vrm-props.js",
	"dashboard-companion-vrm-pose.js",
	"dashboard-companion-vrm-ik.js",
	"dashboard-companion-vrm-clip.js",
	"dashboard-companion-rig-studio-stage.js",
	"dashboard-companion-rig-studio.js",
}

type companionRigStudioBoot struct {
	PackID     string `json:"packId"`
	RuntimeURL string `json:"runtimeUrl"`
	Lang       string `json:"lang"`
	Theme      string `json:"theme"`
	Version    string `json:"version"`
}

type companionRigStudioPageData struct {
	Lang      string
	Theme     string
	Nonce     string
	Title     string
	CSS       template.CSS
	BootJSON  template.JS
	I18nJS    template.JS
	StudioJS  template.JS
}

func readCompanionRigStudioAsset(name string) ([]byte, error) {
	if root := devAssets.Load(); root != nil {
		return root.ReadFile(devAssetDir + "/" + name)
	}
	if name == "companion-rig-studio.tmpl" {
		return companionRigStudioTmplFS.ReadFile("assets/" + name)
	}
	return embeddedDashboardAsset(name)
}

func companionRigStudioCSP(nonce string) string {
	return "default-src 'none'; " +
		"script-src 'nonce-" + nonce + "' 'self'; " +
		"style-src 'nonce-" + nonce + "'; " +
		"connect-src 'self' blob:; " +
		"img-src 'self' blob: data:; " +
		"base-uri 'none'; " +
		"form-action 'none'; " +
		frameAncestorsNone
}

func renderCompanionRigStudioPage(lang, theme, packID, version string) (string, string, error) {
	if !wsi18n.Supported(lang) {
		lang = wsi18n.Default
	}
	var nonceRaw [16]byte
	if _, err := rand.Read(nonceRaw[:]); err != nil {
		return "", "", err
	}
	nonce := hex.EncodeToString(nonceRaw[:])

	tmplBytes, err := readCompanionRigStudioAsset("companion-rig-studio.tmpl")
	if err != nil {
		return "", "", fmt.Errorf("read studio template: %w", err)
	}
	cssBytes, err := readCompanionRigStudioAsset("dashboard-companion-rig-studio.css")
	if err != nil {
		return "", "", fmt.Errorf("read studio css: %w", err)
	}

	var jsBuf strings.Builder
	for i, name := range companionRigStudioScriptFiles {
		raw, rerr := readCompanionRigStudioAsset(name)
		if rerr != nil {
			return "", "", fmt.Errorf("read studio script %s: %w", name, rerr)
		}
		if i > 0 {
			jsBuf.WriteString("\n;\n")
		}
		jsBuf.Write(raw)
	}

	runtimeName, _ := CompanionRigStudioAsset()
	bootBytes, err := json.Marshal(companionRigStudioBoot{
		PackID:     packID,
		RuntimeURL: "/s/" + runtimeName,
		Lang:       lang,
		Theme:      theme,
		Version:    version,
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal studio boot: %w", err)
	}

	funcs := template.FuncMap{
		"t": func(key string, args ...any) string {
			return wsi18n.T(lang, key, args...)
		},
	}
	tmpl, err := template.New("companion-rig-studio").Funcs(funcs).Parse(string(tmplBytes))
	if err != nil {
		return "", "", fmt.Errorf("parse studio template: %w", err)
	}

	data := companionRigStudioPageData{
		Lang:     lang,
		Theme:    theme,
		Nonce:    nonce,
		Title:    wsi18n.T(lang, "js.companion.rig_studio.page_title"),
		CSS:      template.CSS(cssBytes),
		BootJSON: template.JS(bootBytes),
		I18nJS:   wsi18n.SubsetScript(lang, "js.companion.rig_studio."),
		StudioJS: template.JS(jsBuf.String()),
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", "", fmt.Errorf("render studio template: %w", err)
	}
	return buf.String(), nonce, nil
}

// handleCompanionRigStudio serves the standalone top-level Control-Rig & Pack
// Clip Studio page (/companion/studio?pack=<id>). Gated on localBrowseViewer
// (non-local viewer -> 404) behind accessGuard, with a per-response nonce CSP
// and frame-ancestors 'none'. An unknown or non-authorable pack renders an
// in-page error state (HTTP 200), not a 404.
func (d *Daemon) handleCompanionRigStudio(w http.ResponseWriter, r *http.Request) {
	if !readMethod(w, r) {
		return
	}
	if !localBrowseViewer(r) {
		http.NotFound(w, r)
		return
	}
	lang, _ := requestLang(r)
	theme := requestTheme(r)
	packID := strings.TrimSpace(r.URL.Query().Get("pack"))
	html, nonce, err := renderCompanionRigStudioPage(lang, theme, packID, d.endpoint.Version)
	if err != nil {
		fmt.Fprintf(d.log, "dashboard: render companion rig studio: %v\n", err)
		http.Error(w, "render failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", companionRigStudioCSP(nonce))
	w.Header().Set("X-Frame-Options", "DENY")
	writeHTML(w, html)
}
