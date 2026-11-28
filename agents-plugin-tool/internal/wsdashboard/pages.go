package wsdashboard

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"

	"github.com/kang-sw/devenv/internal/wsdevassets"
	"github.com/kang-sw/devenv/internal/wsi18n"
	"github.com/kang-sw/devenv/internal/wswhiteboard"
)

// The daemon's pages: / lists the sessions, /s/<reply-id> shows one. Both are
// built of regions, each rendered by its own template so the page can swap
// one region at a time when an SSE update names it (sse.go). The board and
// panels regions are the static whiteboard page's own fragments
// (wswhiteboard.RenderRegions); the rest render from the view model
// (pageview.go).

//go:embed assets/dashboard.tmpl assets/dashboard-settings-catalog.json assets/dashboard.css assets/dashboard-*.css assets/dashboard.js assets/dashboard-*.js assets/sprite.svg assets/tracker.css assets/tracker.js assets/tracker-*.js assets/mention.js assets/capsules.js assets/md-parser.js assets/md-editor.js assets/ws-input.js assets/toast.js assets/confirm.js assets/hold-confirm.js assets/rail.js assets/memo.js assets/memo.css assets/usage.js assets/usage-*.js assets/usage.css assets/gallery.js assets/gal-rail-preview.js assets/cards.js assets/preview.js assets/panelcard.js assets/files.js assets/terminal.js assets/shell.js assets/webcard.js assets/webframe.js assets/webinject.js assets/overview.js assets/brand/werkschaft-dark.svg assets/brand/werkschaft-light.svg
var dashboardFS embed.FS

// These are ordered source fragments, not independently loaded scripts or
// stylesheets. Joining without separators preserves the original single IIFE,
// declaration hoisting, initialization order, CSS cascade and asset identity.
var dashboardScriptFiles = []string{
	"dashboard.js",
	"dashboard-jank.js",
	"dashboard-animation.js",
	"dashboard-board.js",
	"dashboard-board-anchor.js",
	"dashboard-board-reveal.js",
	"dashboard-board-dial.js",
	"dashboard-conversation.js",
	"dashboard-conv-motion.js",
	"dashboard-bundles.js",
	"dashboard-hud.js",
	"dashboard-composer.js",
	"dashboard-composer-references.js",
	"dashboard-composer-input.js",
	"dashboard-composer-images.js",
	"dashboard-composer-textrefs.js",
	"dashboard-composer-pickers.js",
	"dashboard-composer-highlight.js",
	"dashboard-composer-selections.js",
	"dashboard-composer-delivery.js",
	"dashboard-whiteboard-only.js",
	"dashboard-composer-exec.js",
	"dashboard-composer-ucmd-editor.js",
	"dashboard-composer-ucmd-card.js",
	"dashboard-composer-ucmd.js",
	"dashboard-composer-restore.js",
	"dashboard-composer-status.js",
	"dashboard-exec-hud.js",
	"dashboard-exec-hud-peek.js",
	"dashboard-bg-exec-monitor.js",
	"dashboard-composer-keyboard.js",
	"dashboard-actions.js",
	"dashboard-drawer.js",
	"dashboard-ticketmap.js",
	"dashboard-ticketmap-viewb.js",
	"dashboard-ticketmap-geom.js",
	"dashboard-ticketmap-graph.js",
	"dashboard-workpanel.js",
	"dashboard-frames.js",
	"dashboard-git.js",
	"dashboard-gitpage.js",
	"dashboard-gitpage-wt.js",
	"dashboard-tracker.js",
	"dashboard-selection.js",
	"dashboard-stream.js",
	"dashboard-daemon-confirm.js",
	"dashboard-daemon-bridge.js",
	"dashboard-daemon-guide.js",
	"dashboard-rail.js",
	"dashboard-workspace-admission.js",
	"dashboard-rail-popovers.js",
	"dashboard-holders.js",
	"dashboard-leads.js",
	"dashboard-harness-switch.js",
	"dashboard-new-session.js",
	"dashboard-tier-picker.js",
	"dashboard-session-view.js",
	"dashboard-companion-relay.js",
	"dashboard-audit-window.js",
	"dashboard-audit.js",
	"dashboard-audit-rows.js",
	"dashboard-ide-shell.js",
	"dashboard-lifecycle.js",
	"dashboard-voice-ptt.js",
	"dashboard-voice.js",
	"dashboard-voice-field.js",
	"dashboard-voice-native.js",
}

var companionScriptFiles = []string{
	"dashboard-companion-builtins.js",
	"dashboard-companion-packs.js",
	"dashboard-companion-focus.js",
	"dashboard-companion-deform.js",
	"dashboard-companion-compositor.js",
	"dashboard-companion-scenarios.js",
	"dashboard-companion-menu.js",
	"dashboard-companion-reflex.js",
	"dashboard-companion-quip.js",
	"dashboard-companion-vrm-fx.js",
	"dashboard-companion-vrm-studio.js",
	"dashboard-companion-vrm-props.js",
	"dashboard-companion-vrm-pose.js",
	"dashboard-companion-vrm-ik.js",
	"dashboard-companion-vrm-clip.js",
	"dashboard-companion-vrm-colead.js",
	"dashboard-companion-vrm.js",
	"dashboard-companion-messenger-md.js",
	"dashboard-companion-messenger.js",
	"dashboard-companion-messenger-presence.js",
	"dashboard-companion-stage.js",
}

var companionStyleFiles = []string{
	"dashboard-companion.css",
	"dashboard-companion-quip.css",
	"dashboard-companion-messenger.css",
}

var dashboardStyleFiles = []string{
	"dashboard.css",
	"dashboard-conversation.css",
	"dashboard-conv-motion.css",
	"dashboard-composer-md.css",
	"dashboard-board.css",
	"dashboard-layout.css",
	"dashboard-drawer.css",
	"dashboard-ticketmap.css",
	"dashboard-workpanel.css",
	"dashboard-git.css",
	"dashboard-gitpage.css",
	"dashboard-menus.css",
	"dashboard-rail.css",
	"dashboard-new-session.css",
	"dashboard-shell.css",
	"dashboard-account-consent.css",
	"dashboard-daemon-confirm.css",
	"dashboard-daemon-guide.css",
	"dashboard-ide-shell.css",
	"dashboard-holders.css",
	"dashboard-audit.css",
	"dashboard-audit-rows.css",
	"dashboard-exec-hud.css",
	"dashboard-bg-exec-monitor.css",
	"dashboard-composer-ucmd.css",
	"dashboard-composer-ucmd-editor.css",
	"dashboard-voice.css",
	"dashboard-jank.css",
	"dashboard-compact.css",
}

var trackerScriptFiles = []string{
	"tracker.js",
	"tracker-org-editor.js",
	"tracker-org-actions.js",
	"tracker-view-helpers.js",
	"tracker-item-rows.js",
	"tracker-panel.js",
	"tracker-wiki.js",
	"tracker-window.js",
	"tracker-forms.js",
	"tracker-menu.js",
	"tracker-picker.js",
	"tracker-writes.js",
	"tracker-render.js",
	"tracker-mount.js",
}

var usageScriptFiles = []string{
	"usage.js",
	"usage-charts.js",
	"usage-popover.js",
}

func readDashboardSource(read func(string) ([]byte, error), name string) ([]byte, error) {
	var files []string
	switch name {
	case "dashboard.js":
		files = dashboardScriptFiles
	case "dashboard.css":
		files = dashboardStyleFiles
	case "tracker.js":
		files = trackerScriptFiles
	case "usage.js":
		files = usageScriptFiles
	case "dashboard-companion.js":
		files = companionScriptFiles
	case "dashboard-companion.css":
		files = companionStyleFiles
	default:
		return read(name)
	}
	var source bytes.Buffer
	for _, file := range files {
		raw, err := read(file)
		if err != nil {
			return nil, err
		}
		source.Write(raw)
	}
	return source.Bytes(), nil
}

type scriptFileRange struct {
	File      string `json:"file"`
	StartChar int    `json:"startChar"`
	EndChar   int    `json:"endChar"`
	StartLine int    `json:"startLine"`
	EndLine   int    `json:"endLine"`
}

type scriptSourceMapData struct {
	ScriptFile      string            `json:"scriptFile,omitempty"`
	FrameScriptFile string            `json:"frameScriptFile,omitempty"`
	TermFile        string            `json:"termFile,omitempty"`
	WebFile         string            `json:"webFile,omitempty"`
	SelectFile      string            `json:"selectFile,omitempty"`
	TotalChars      int               `json:"totalChars"`
	TotalLines      int               `json:"totalLines"`
	Files           []scriptFileRange `json:"files"`
	FrameFiles      []scriptFileRange `json:"frameFiles,omitempty"`
}

type scriptPart struct {
	file    string
	content string
}

func assembleScriptParts(parts []scriptPart) ([]byte, []scriptFileRange, int, int) {
	var source bytes.Buffer
	ranges := make([]scriptFileRange, 0, len(parts))
	charPos := 0
	lineNum := 1
	for _, part := range parts {
		startChar := charPos
		startLine := lineNum
		for _, r := range part.content {
			n := utf16.RuneLen(r)
			if n < 0 {
				n = 1
			}
			charPos += n
			if r == '\n' {
				lineNum++
			}
		}
		if part.file != "" {
			endLine := lineNum
			if len(part.content) > 0 && part.content[len(part.content)-1] == '\n' && endLine > startLine {
				endLine--
			}
			ranges = append(ranges, scriptFileRange{
				File:      part.file,
				StartChar: startChar,
				EndChar:   charPos,
				StartLine: startLine,
				EndLine:   endLine,
			})
		}
		source.WriteString(part.content)
	}
	totalLines := lineNum
	b := source.Bytes()
	if len(b) == 0 {
		totalLines = 0
	} else if b[len(b)-1] == '\n' && totalLines > 1 {
		totalLines--
	}
	return b, ranges, charPos, totalLines
}

func readNamedScriptParts(read func(string) ([]byte, error), names []string, sep string) ([]scriptPart, string, error) {
	parts := make([]scriptPart, 0, len(names)*2)
	var buf strings.Builder
	for i, name := range names {
		if i > 0 && sep != "" {
			parts = append(parts, scriptPart{content: sep})
			buf.WriteString(sep)
		}
		raw, err := read(name)
		if err != nil {
			return nil, "", err
		}
		s := string(raw)
		parts = append(parts, scriptPart{file: name, content: s})
		buf.WriteString(s)
	}
	return parts, buf.String(), nil
}

// assembleDashboardScripts joins dashboardScriptFiles without separators while
// computing exact UTF-16 character and 1-based line spans for each fragment.
func assembleDashboardScripts(read func(string) ([]byte, error)) ([]byte, scriptSourceMapData, error) {
	parts, _, err := readNamedScriptParts(read, dashboardScriptFiles, "")
	if err != nil {
		return nil, scriptSourceMapData{}, err
	}
	b, ranges, totalChars, totalLines := assembleScriptParts(parts)
	return b, scriptSourceMapData{
		TotalChars: totalChars,
		TotalLines: totalLines,
		Files:      ranges,
	}, nil
}

// Region names. The session page's regions in order; the index page has one.
//
// The conversation is two regions inside one scroll wrapper: entries (the
// finished messages of the live view, which the page merges by entry name
// and never re-renders) and convlive (the streaming reply, the approval or
// activity line, the held messages), which a streaming tick changes alone.
// The status band is two regions inside the composer: ctx (the context bar
// above the message box) and sline (the line below it). The composer also
// holds cmds, the slash commands the session's adapter runs, as hidden data
// the page's command picker reads; empty when the adapter reports none.
const (
	regionHead     = "head"
	regionTree     = "tree"
	regionEntries  = "entries"
	regionConvLive = "convlive"
	regionCtx      = "ctx"
	regionCmds     = "cmds"
	regionGit      = "git"
	regionSLine    = "sline"
	regionBoard    = "board"
	regionPanels   = "panels"
	regionSessions = "sessions"
	// regionMode is a pseudo-region with no element: its value is the page
	// mode, and a change tells the page that its layout no longer fits.
	regionMode = "mode"
)

// sessionRegions lists the regions a session page in mode holds.
func sessionRegions(mode string) []string {
	if mode == modeLive {
		return []string{regionHead, regionGit, regionTree, regionEntries, regionConvLive, regionCtx, regionCmds, regionSLine, regionBoard, regionPanels}
	}
	if mode == modeHistory {
		return []string{regionHead, regionGit, regionEntries, regionBoard, regionPanels}
	}
	return []string{regionHead, regionGit, regionBoard, regionPanels}
}

type dashboardAssets struct {
	// tmpl is the default language's template set; tmpls holds one per
	// language, each parsed with the translation funcs bound to it, since a
	// parsed set's FuncMap is fixed (templateFor picks).
	tmpl          *template.Template
	tmpls         map[string]*template.Template
	data          pageAssetsData
	files         map[string]dashboardFile
	wbFormParts   []scriptPart
	wbViewerParts []scriptPart
	trackerParts  []scriptPart
	mentionParts  []scriptPart
	usageParts    []scriptPart
	cardsParts    []scriptPart
}

type dashboardFile struct {
	body  string
	ctype string
}

// templateFor is the template set that renders in lang; an unsupported or
// empty lang renders the default language.
func (a *dashboardAssets) templateFor(lang string) *template.Template {
	if t, ok := a.tmpls[lang]; ok {
		return t
	}
	return a.tmpl
}

// pageAssetsData is what a page template inlines.
type pageAssetsData struct {
	CSS                 template.CSS
	Script              template.JS
	ScriptSourceMap     template.JS
	ScriptFile          string // Content-addressed page script for fresh session frames.
	FrameSourceMapFile  string
	FrameWhiteboardFile string
	FrameCSSFile        string
	FrameScriptFile     string
	FrameXtermFile      string
	FrameTermFile       string
	FrameWebFile        string
	FrameSelectFile     string
	FrameI18nFiles      map[string]string
	SettingsCSS         template.CSS
	SettingsScript      template.JS
	SettingsCatalog     template.JS
	SettingsCSSFile     string
	SettingsScriptFile  string
	SettingsI18nFiles   map[string]string
	Sprite              template.HTML
	WhiteboardCSS       template.CSS
	WhiteboardSprite    template.HTML
	FormScript          template.JS
	AnswersScript       template.JS
	MermaidInit         template.JS
	MermaidFile         string
	ViewerScript        template.JS
	// SelectAllScript is shared with standalone whiteboards, separate from
	// the assembled dashboard script and loaded once in every page kind.
	SelectAllScript template.JS
	// TrackerCSS and TrackerScript are the tracker renderer (tracker.js),
	// which dashboard.js mounts when the daemon serves the tracker routes.
	TrackerCSS    template.CSS
	TrackerScript template.JS
	// MentionScript is the @ file-mention popup module (mention.js), which
	// the session page's composer attaches.
	MentionScript template.JS
	// ToastScript is the shared toast stack (toast.js, window.WsToast),
	// inlined ahead of the tracker and page scripts that both use it.
	ToastScript template.JS
	// ConfirmScript handles modal confirmation and installs its Escape handler
	// before the page shortcuts in every dashboard document.
	ConfirmScript template.JS
	// HoldConfirmScript provides the reusable 1.5s press-and-hold confirmation
	// controller (hold-confirm.js, window.WsHoldConfirm).
	HoldConfirmScript template.JS
	// RailScript is the session rail's browser-side state (rail.js,
	// window.WsRail): per-session drafts, stars, and the rail's own
	// toggles, which the session page's script uses.
	RailScript  template.JS
	MemoScript  template.JS
	UsageScript template.JS
	// ShellScript is the iframe shell's pure helpers (shell.js,
	// window.WsShell): the frame LRU, the paths a shell frames, and the
	// shell-frame message shapes, which the shell and its frames both load.
	ShellScript     template.JS
	CompanionScript template.JS
	CompanionCSS    template.CSS
	// CompanionVrmFile is the content-hashed filename of the lazy-loaded pure-VRM
	// runtime ES module (/s/<CompanionVrmFile>); never inlined into dashboard.tmpl.
	CompanionVrmFile string
	// CardsScript is the board card framework (cards.js, window.WsCards),
	// led by its paged docking gallery's pure model (gallery.js,
	// window.WsGallery),
	// PreviewScript its file preview kind (preview.js, window.WsPreview),
	// and FilesScript the conversation column's file explorer (files.js,
	// window.WsFiles); LucideSprite holds the vendored icons they draw
	// (lucide.go). A session page loads them before its own script, which
	// mounts them.
	OverviewScript template.JS
	CardsScript    template.JS
	PreviewScript  template.JS
	PanelScript    template.JS
	FilesScript    template.JS
	// TerminalScript is the terminal card kind (terminal.js,
	// window.WsTerm), which only a page that offers terminals carries
	// (sessionPageData.Terminal).
	TerminalScript template.JS
	// WebScript is the web card kind (webcard.js, window.WsWeb), which
	// only a page asked for by a browser on this machine carries
	// (sessionPageData.SysOpen).
	WebScript template.JS
	// XtermCSS is the vendored terminal emulator's stylesheet, which a
	// page that offers terminals inlines (sessionPageData.Terminal).
	XtermCSS     template.CSS
	LucideSprite template.HTML
	// Logo is the Werkschaft lockup the index page centres (the landing):
	// both theme variants inline, the stylesheet showing the one that reads
	// on the page's theme (dlogo-d on dark, dlogo-l on light).
	Logo template.HTML
	// ID is the assets' identity (assetID), which the page roots carry
	// (data-assets) and every stream sends first (sse.go), so an open page
	// can tell that the daemon now serves different assets than it runs.
	ID string `json:"-"`
}

var (
	dashboardAssetsOnce sync.Once
	loadedDashboard     *dashboardAssets
	dashboardAssetsErr  error

	symbolPattern = regexp.MustCompile(`<symbol id="i-([a-z0-9-]+)"`)
)

// loadDashboardAssets returns the dashboard assets. The embedded copy is
// built once; with dev assets on (SetDevAssets) every call rebuilds from the
// working tree, so an edit shows on the next request and a failed build is
// never kept.
func loadDashboardAssets() (*dashboardAssets, error) {
	if root := devAssets.Load(); root != nil {
		return buildDashboardAssets(func(name string) ([]byte, error) { return root.ReadFile(devAssetDir + "/" + name) })
	}
	dashboardAssetsOnce.Do(func() { loadedDashboard, dashboardAssetsErr = buildDashboardAssets(embeddedDashboardAsset) })
	return loadedDashboard, dashboardAssetsErr
}

func embeddedDashboardAsset(name string) ([]byte, error) {
	return dashboardFS.ReadFile("assets/" + name)
}

// devAssets, when set, is the source root loadDashboardAssets reads from
// instead of the embedded copy.
var devAssets atomic.Pointer[wsdevassets.Root]

// devAssetDir is where the dashboard assets live under a dev source root.
const devAssetDir = "internal/wsdashboard/assets"

// SetDevAssets turns dev assets on for this process, for the dashboard pages
// and the whiteboard page assets they inline, or off with nil. The daemon
// calls it at startup; the mermaid bundle stays embedded.
func SetDevAssets(root *wsdevassets.Root) {
	devAssets.Store(root)
	wswhiteboard.SetDevAssets(root)
}

// buildDashboardAssets reads every dashboard asset by file name through read
// and parses the template for each language.
func buildDashboardAssets(read func(name string) ([]byte, error)) (*dashboardAssets, error) {
	var err error
	get := func(name string) string {
		if err != nil {
			return ""
		}
		var raw []byte
		raw, err = readDashboardSource(read, name)
		return string(raw)
	}
	optional := func(name string) string {
		if err != nil {
			return ""
		}
		raw, readErr := read(name)
		if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
			err = readErr
		}
		return string(raw)
	}
	wb, werr := wswhiteboard.PageAssets()
	if werr != nil {
		return nil, werr
	}
	wbFormFrags, wbViewerFrags, wferr := wswhiteboard.PageScriptFragments()
	if wferr != nil {
		return nil, wferr
	}
	mermaidName, _ := wswhiteboard.MermaidAsset()
	companionVrmName, _ := CompanionVrmAsset()
	lucide, lerr := lucideSprite()
	if lerr != nil {
		return nil, lerr
	}
	scriptBytes, sourceMap, smErr := assembleDashboardScripts(read)
	if smErr != nil {
		return nil, smErr
	}
	sourceMapJSON, smMarshalErr := json.Marshal(sourceMap)
	if smMarshalErr != nil {
		return nil, smMarshalErr
	}
	trackerParts, trackerJoined, tErr := readNamedScriptParts(read, trackerScriptFiles, "")
	if tErr != nil {
		return nil, tErr
	}
	mentionParts, mentionJoined, mErr := readNamedScriptParts(read, []string{"mention.js", "capsules.js", "md-parser.js", "md-editor.js", "ws-input.js"}, "\n")
	if mErr != nil {
		return nil, mErr
	}
	usageParts, usageJoined, uErr := readNamedScriptParts(read, usageScriptFiles, "")
	if uErr != nil {
		return nil, uErr
	}
	cardsParts, cardsJoined, cErr := readNamedScriptParts(read, []string{"gallery.js", "gal-rail-preview.js", "cards.js"}, "\n;\n")
	if cErr != nil {
		return nil, cErr
	}
	wbFormParts := make([]scriptPart, len(wbFormFrags))
	for i, frag := range wbFormFrags {
		wbFormParts[i] = scriptPart{file: frag.File, content: frag.Body}
	}
	wbViewerParts := make([]scriptPart, len(wbViewerFrags))
	for i, frag := range wbViewerFrags {
		wbViewerParts[i] = scriptPart{file: frag.File, content: frag.Body}
	}
	a := &dashboardAssets{
		wbFormParts:   wbFormParts,
		wbViewerParts: wbViewerParts,
		trackerParts:  trackerParts,
		mentionParts:  mentionParts,
		usageParts:    usageParts,
		cardsParts:    cardsParts,
		data: pageAssetsData{
			CSS:               template.CSS(get("dashboard.css") + "\n" + get("memo.css") + "\n" + get("usage.css")),
			Script:            template.JS(scriptBytes),
			ScriptSourceMap:   template.JS(sourceMapJSON),
			Sprite:            template.HTML(strings.TrimSpace(get("sprite.svg"))),
			TrackerCSS:        template.CSS(get("tracker.css")),
			TrackerScript:     template.JS(trackerJoined),
			MentionScript:     template.JS(mentionJoined),
			ToastScript:       template.JS(get("toast.js")),
			ConfirmScript:     template.JS(get("confirm.js")),
			HoldConfirmScript: template.JS(get("hold-confirm.js")),
			RailScript:        template.JS(get("rail.js")),
			MemoScript:        template.JS(get("memo.js")),
			UsageScript:       template.JS(usageJoined),
			ShellScript:       template.JS(get("shell.js")),
			CompanionScript:   template.JS(get("dashboard-companion.js")),
			CompanionCSS:      template.CSS(get("dashboard-companion.css")),
			CompanionVrmFile:  companionVrmName,
			SettingsCSS:       template.CSS(get("dashboard-settings.css") + "\n" + optional("dashboard-settings-extras.css") + "\n" + get("dashboard-settings-tiers.css") + "\n" + get("dashboard-settings-preferences.css") + "\n" + get("dashboard-settings-account.css") + "\n" + get("dashboard-settings-voice.css") + "\n" + get("dashboard-settings-companion.css")),
			SettingsScript:    template.JS(get("hold-confirm.js") + "\n;\n" + optional("dashboard-settings-extras.js") + "\n;\n" + get("dashboard-settings-combobox.js") + "\n;\n" + get("dashboard-settings-tiers.js") + "\n;\n" + get("dashboard-settings-preferences.js") + "\n;\n" + get("dashboard-voice-native.js") + "\n;\n" + get("dashboard-settings-account-shared.js") + "\n;\n" + get("dashboard-settings-account-harness.js") + "\n;\n" + get("dashboard-settings-account-runtime.js") + "\n;\n" + get("dashboard-settings-account.js") + "\n;\n" + get("dashboard-settings-voice.js") + "\n;\n" + get("dashboard-settings-companion-agents.js") + "\n;\n" + get("dashboard-settings-companion.js") + "\n;\n" + get("dashboard-settings.js")),
			SettingsCatalog:   template.JS(get("dashboard-settings-catalog.json")),
			OverviewScript:    template.JS(get("overview.js")),
			CardsScript:       template.JS(cardsJoined),
			PreviewScript:     template.JS(get("preview.js")),
			PanelScript:       template.JS(get("panelcard.js")),
			FilesScript:       template.JS(get("files.js")),
			TerminalScript:    template.JS(get("terminal.js")),
			WebScript:         template.JS(get("webcard.js")),
			XtermCSS:          xtermStyle(),
			LucideSprite:      lucide,
			Logo:              template.HTML(`<span class="dlogo-d">` + strings.TrimSpace(get("brand/werkschaft-dark.svg")) + `</span><span class="dlogo-l">` + strings.TrimSpace(get("brand/werkschaft-light.svg")) + `</span>`),
			WhiteboardCSS:     wb.CSS,
			WhiteboardSprite:  wb.Sprite,
			FormScript:        wb.FormScript,
			AnswersScript:     wb.AnswersScript,
			MermaidInit:       wb.MermaidInit,
			MermaidFile:       mermaidName,
			ViewerScript:      wb.ViewerScript,
			SelectAllScript:   wb.SelectAllScript,
		}}
	source := get("dashboard.tmpl")
	if err != nil {
		return nil, err
	}
	icons := map[string]bool{}
	for _, sprite := range []string{string(a.data.Sprite), string(wb.Sprite)} {
		for _, m := range symbolPattern.FindAllStringSubmatch(sprite, -1) {
			icons[m[1]] = true
		}
	}
	a.buildFrameFiles()
	a.data.ID = assetID(a.data, source)
	a.tmpls = map[string]*template.Template{}
	for _, l := range wsi18n.Languages() {
		// The file's template is "dashboard.tmpl" under the root
		// "dashboard", as ParseFS names it.
		t := template.New("dashboard").Funcs(pageFuncs(l.Code, icons))
		if _, err := t.New("dashboard.tmpl").Parse(source); err != nil {
			return nil, err
		}
		a.tmpls[l.Code] = t
	}
	a.tmpl = a.tmpls[wsi18n.Default]
	return a, nil
}

// assetID is the content hash of everything a dashboard page inlines: the
// asset data (every inlined script, stylesheet, and sprite), the template
// source, and every language's client string table. It is a content hash, not
// the build time, so a daemon replacement that leaves the assets unchanged
// keeps it; and it covers every language alike, so a page and its stream
// agree whatever language each was rendered in. With dev assets on it follows
// the files read for the build, so a page and the stream it opens disagree
// only while a file changed in between, and the reload converges.
func assetID(data pageAssetsData, source string) string {
	h := sha256.New()
	raw, _ := json.Marshal(data)
	h.Write(raw)
	h.Write([]byte(source))
	h.Write([]byte(guideAssets().id))
	for _, l := range wsi18n.Languages() {
		h.Write([]byte(wsi18n.Script(l.Code)))
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

type settingsGear struct{ Scope, Target, Worktree string }

type settingsPageData struct {
	Assets     pageAssetsData
	Theme      string
	HasAccount bool
}

func renderSettingsPage(lang, theme string, hasAccount bool) (string, error) {
	assets, err := loadDashboardAssets()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	err = assets.templateFor(lang).ExecuteTemplate(&buf, "config-settings", settingsPageData{Assets: assets.data, Theme: theme, HasAccount: hasAccount})
	return buf.String(), err
}
