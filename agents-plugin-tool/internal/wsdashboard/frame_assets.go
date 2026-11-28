package wsdashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"regexp"
	"sort"
	"strings"

	"github.com/kang-sw/devenv/internal/wsi18n"
)

// settingsKeyLiteral matches a client string key written as a literal in a
// script.
var settingsKeyLiteral = regexp.MustCompile(`["'](js\.[a-z0-9_]+(?:\.[a-z0-9_-]+)+)["']`)

// settingsSharedI18nKeys are the client strings outside the js.settings.*
// namespace that the Settings page's own scripts name. The page loads only its
// js.settings.* subset (the frame table is not loaded there), so a key it shows
// from another namespace (the companion agents card reuses a lead modal
// notice) would render as the raw key unless the page's table carries it. The
// set is read from the scripts themselves, so a card that shows another
// namespace's string needs no second list; a literal that is no table key
// (an id, a dotted data value) adds nothing.
func settingsSharedI18nKeys(scripts ...string) []string {
	seen := map[string]bool{}
	var keys []string
	for _, script := range scripts {
		for _, m := range settingsKeyLiteral.FindAllStringSubmatch(script, -1) {
			key := m[1]
			if !strings.HasPrefix(key, "js.settings.") && !seen[key] {
				seen[key] = true
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}

// buildFrameFiles keeps the frame's immutable resources beside the existing
// main script. These URLs deliberately retain the session page's access gate;
// unlike sandboxed topic documents, the session frame has the host's cookie.
// Shell, index and non-frame renderers continue to use the original fields.
func (a *dashboardAssets) buildFrameFiles() {
	a.files = map[string]dashboardFile{}
	add := func(body, ext string) string {
		hash := sha256.Sum256([]byte(body))
		name := "dashboard-" + hex.EncodeToString(hash[:12]) + "." + ext
		ctype := "text/javascript; charset=utf-8"
		if ext == "css" {
			ctype = "text/css; charset=utf-8"
		}
		a.files[name] = dashboardFile{body: body, ctype: ctype}
		return name
	}
	d := &a.data
	d.ScriptFile = add(string(d.Script), "js")
	d.FrameWhiteboardFile = add(string(d.WhiteboardCSS), "css")
	d.FrameCSSFile = add(string(d.CSS)+"\n"+string(d.TrackerCSS), "css")
	d.FrameXtermFile = add(string(d.XtermCSS), "css")
	mermaid, _ := json.Marshal(d.MermaidFile)
	// Preserve the former classic scripts' source order and existing wrappers.
	// The explicit separator also protects a trailing line comment/ASI site.
	wbBlock := make([]scriptPart, 0, 4+len(a.wbFormParts))
	wbBlock = append(wbBlock, scriptPart{content: "window.wbForm = function () {\n"})
	wbBlock = append(wbBlock, a.wbFormParts...)
	wbBlock = append(wbBlock,
		scriptPart{content: "};\nwindow.wbMermaid = function () {\n"},
		scriptPart{file: "mermaid-init.js", content: string(d.MermaidInit)},
		scriptPart{content: "};\nwindow.wbMermaidFile = " + string(mermaid) + ";"},
	)
	groups := [][]scriptPart{
		{{file: "confirm.js", content: string(d.ConfirmScript)}},
		{{file: "answers.js", content: string(d.AnswersScript)}},
		wbBlock,
		a.wbViewerParts,
		{{file: "toast.js", content: string(d.ToastScript)}},
		{{file: "hold-confirm.js", content: string(d.HoldConfirmScript)}},
		a.trackerParts,
		a.mentionParts,
		{{file: "rail.js", content: string(d.RailScript)}},
		{{file: "memo.js", content: string(d.MemoScript)}},
		a.usageParts,
		{{file: "shell.js", content: string(d.ShellScript)}},
		a.cardsParts,
		{{file: "overview.js", content: string(d.OverviewScript)}},
		{{file: "preview.js", content: string(d.PreviewScript)}},
		{{file: "panelcard.js", content: string(d.PanelScript)}},
		{{file: "files.js", content: string(d.FilesScript)}},
	}
	frameParts := make([]scriptPart, 0, 64)
	for i, grp := range groups {
		if i > 0 {
			frameParts = append(frameParts, scriptPart{content: "\n;\n"})
		}
		frameParts = append(frameParts, grp...)
	}
	frameBytes, frameRanges, _, _ := assembleScriptParts(frameParts)
	d.FrameScriptFile = add(string(frameBytes), "js")
	d.FrameTermFile = add(string(d.TerminalScript), "js")
	d.FrameWebFile = add(string(d.WebScript), "js")
	d.FrameSelectFile = add(string(d.SelectAllScript), "js")
	var sm scriptSourceMapData
	if len(d.ScriptSourceMap) > 0 {
		_ = json.Unmarshal([]byte(d.ScriptSourceMap), &sm)
	}
	sm.ScriptFile = d.ScriptFile
	sm.FrameScriptFile = d.FrameScriptFile
	sm.TermFile = d.FrameTermFile
	sm.WebFile = d.FrameWebFile
	sm.SelectFile = d.FrameSelectFile
	sm.FrameFiles = frameRanges
	if smJSON, err := json.Marshal(sm); err == nil {
		d.ScriptSourceMap = template.JS(smJSON)
		d.FrameSourceMapFile = add("window.wsSourceMap="+string(smJSON)+";", "js")
	}
	d.SettingsCSSFile = add(string(d.SettingsCSS), "css")
	d.SettingsScriptFile = add(string(d.ShellScript)+"\n;\nwindow.WsSettingsCatalog = "+string(d.SettingsCatalog)+";\n"+string(d.SettingsScript), "js")
	d.FrameI18nFiles = map[string]string{}
	d.SettingsI18nFiles = map[string]string{}
	shared := settingsSharedI18nKeys(string(d.ShellScript), string(d.SettingsScript))
	for _, lang := range wsi18n.Languages() {
		d.FrameI18nFiles[lang.Code] = add(string(wsi18n.ScriptExcept(lang.Code, "js.settings.")), "js")
		d.SettingsI18nFiles[lang.Code] = add(string(wsi18n.SubsetScriptKeys(lang.Code, "js.settings.", shared...)), "js")
	}
}
