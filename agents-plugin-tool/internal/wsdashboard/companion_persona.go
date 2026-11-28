package wsdashboard

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The official persona presets ship inside the binary, independent of any
// visual pack, so a pack without a persona (a bare .vrm import included)
// still gets a well-formed Characteristic Sheet. Each file is one preset;
// its file stem is its ID, and "official-default" is the fallback persona.
//
//go:embed assets/companion/personas/*.json
var companionPersonaPresetFS embed.FS

// Persona sources, in precedence order: the user's persona.md, an official
// preset the user picked for the Soul, the active pack's persona, and the
// embedded default preset.
const (
	CompanionPersonaSourceUser     = "user"
	CompanionPersonaSourcePreset   = "preset"
	CompanionPersonaSourcePack     = "pack"
	CompanionPersonaSourceOfficial = "official"

	// CompanionPersonaPresetDefault is the embedded fallback preset's ID.
	CompanionPersonaPresetDefault = "official-default"

	companionOfficialPersonaSchema = "ws-companion-persona/v1"
	companionPresetLabelMax        = 120
)

// companionPersonaPresetID is the shape of a preset ID (and its file stem).
var companionPersonaPresetID = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

// CompanionPersonaLocales are the dashboard locales a persona voice overlay
// is keyed by; they match the wsi18n table codes.
var CompanionPersonaLocales = []string{"de", "en", "es", "fr", "it", "ja", "ko", "pt-BR", "ru", "zh-Hans", "zh-Hant"}

// companionOfficialPersona is one embedded official preset: a picker label
// (title and summary, each with per-locale variants) and an English core
// Characteristic Sheet with per-locale voice overlays.
type companionOfficialPersona struct {
	Schema           string            `json:"schema"`
	ID               string            `json:"id"`
	Title            string            `json:"title"`
	TitleByLang      map[string]string `json:"title_by_lang"`
	Summary          string            `json:"summary"`
	SummaryByLang    map[string]string `json:"summary_by_lang"`
	Name             string            `json:"name"`
	Archetype        string            `json:"archetype"`
	Voice            string            `json:"voice"`
	SpeechQuirks     []string          `json:"speech_quirks"`
	GapMoeTriggers   []string          `json:"gap_moe_triggers"`
	ComfortBehaviors []string          `json:"comfort_behaviors"`
	FocusBehaviors   []string          `json:"focus_behaviors"`
	Notes            string            `json:"notes"`
	VoiceByLang      map[string]string `json:"voice_by_lang"`
}

// CompanionPersonaPresetView is a preset as the Settings picker shows it.
type CompanionPersonaPresetView struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	Title         string            `json:"title"`
	TitleByLang   map[string]string `json:"titleByLang,omitempty"`
	Summary       string            `json:"summary"`
	SummaryByLang map[string]string `json:"summaryByLang,omitempty"`
}

var (
	companionPersonaPresetsOnce  sync.Once
	companionPersonaPresetsValue []companionOfficialPersona
)

// decodeCompanionPersonaPresets reads every embedded preset, skipping a file
// that does not decode, lacks a voice, or whose id differs from its file stem
// (tests reject such a file). The result is the default first, then by ID; a
// missing default degrades to a minimal sheet rather than failing a wake.
func decodeCompanionPersonaPresets(fsys fs.FS) []companionOfficialPersona {
	names, _ := fs.Glob(fsys, "assets/companion/personas/*.json")
	var out []companionOfficialPersona
	hasDefault := false
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			continue
		}
		var p companionOfficialPersona
		if json.Unmarshal(raw, &p) != nil || strings.TrimSpace(p.Voice) == "" ||
			p.ID != strings.TrimSuffix(path.Base(name), ".json") || !companionPersonaPresetID.MatchString(p.ID) {
			continue
		}
		hasDefault = hasDefault || p.ID == CompanionPersonaPresetDefault
		out = append(out, p)
	}
	if !hasDefault {
		out = append(out, companionOfficialPersona{
			ID:    CompanionPersonaPresetDefault,
			Title: "Default",
			Name:  "Companion",
			Voice: "Warm, sharp, and concise desk companion.",
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if (out[i].ID == CompanionPersonaPresetDefault) != (out[j].ID == CompanionPersonaPresetDefault) {
			return out[i].ID == CompanionPersonaPresetDefault
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// companionPersonaPresets returns the embedded preset catalog, decoded once.
func companionPersonaPresets() []companionOfficialPersona {
	companionPersonaPresetsOnce.Do(func() {
		companionPersonaPresetsValue = decodeCompanionPersonaPresets(companionPersonaPresetFS)
	})
	return companionPersonaPresetsValue
}

// companionPersonaPreset looks up an embedded preset by ID.
func companionPersonaPreset(id string) (companionOfficialPersona, bool) {
	for _, p := range companionPersonaPresets() {
		if p.ID == id {
			return p, true
		}
	}
	return companionOfficialPersona{}, false
}

// officialCompanionPersona is the embedded default preset.
func officialCompanionPersona() companionOfficialPersona {
	return companionPersonaPresets()[0]
}

// CompanionPersonaPresetViews lists the embedded presets for the Settings
// picker, default first.
func CompanionPersonaPresetViews() []CompanionPersonaPresetView {
	presets := companionPersonaPresets()
	out := make([]CompanionPersonaPresetView, 0, len(presets))
	for _, p := range presets {
		title := strings.TrimSpace(p.Title)
		if title == "" {
			title = strings.TrimSpace(p.Name)
		}
		out = append(out, CompanionPersonaPresetView{
			ID:            p.ID,
			Name:          strings.TrimSpace(p.Name),
			Title:         title,
			TitleByLang:   companionPresetLabels(p.TitleByLang),
			Summary:       strings.TrimSpace(p.Summary),
			SummaryByLang: companionPresetLabels(p.SummaryByLang),
		})
	}
	return out
}

// companionPresetLabels keeps the dashboard-locale entries of a label map.
func companionPresetLabels(byLang map[string]string) map[string]string {
	out := map[string]string{}
	for _, loc := range CompanionPersonaLocales {
		if v := strings.TrimSpace(byLang[loc]); v != "" {
			out[loc] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (p companionOfficialPersona) sheet(langKey string) CharacteristicSheet {
	return CharacteristicSheet{
		Name:             strings.TrimSpace(p.Name),
		Archetype:        strings.TrimSpace(p.Archetype),
		Voice:            joinCompanionVoice(p.Voice, companionVoiceOverlay(p.VoiceByLang, langKey)),
		SpeechQuirks:     append([]string(nil), p.SpeechQuirks...),
		GapMoeTriggers:   append([]string(nil), p.GapMoeTriggers...),
		ComfortBehaviors: append([]string(nil), p.ComfortBehaviors...),
		FocusBehaviors:   append([]string(nil), p.FocusBehaviors...),
		ExtraNotes:       strings.TrimSpace(p.Notes),
		Structured:       true,
	}
}

func joinCompanionVoice(core, overlay string) string {
	core, overlay = strings.TrimSpace(core), strings.TrimSpace(overlay)
	switch {
	case core == "":
		return overlay
	case overlay == "":
		return core
	}
	return core + "\n" + overlay
}

// companionVoiceOverlay returns the overlay for langKey (a normalized locale
// from NormalizeCompanionLang): an exact key first, then any key that
// normalizes to the same locale, in sorted key order for determinism.
func companionVoiceOverlay(byLang map[string]string, langKey string) string {
	if langKey == "" || len(byLang) == 0 {
		return ""
	}
	if v := strings.TrimSpace(byLang[langKey]); v != "" {
		return v
	}
	keys := make([]string, 0, len(byLang))
	for k := range byLang {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if NormalizeCompanionLang(k) == langKey {
			if v := strings.TrimSpace(byLang[k]); v != "" {
				return v
			}
		}
	}
	return ""
}

// companionLangNames maps lowercased language names (English and native) to
// a dashboard locale. Tags are handled separately in NormalizeCompanionLang.
var companionLangNames = []struct {
	name   string
	locale string
}{
	// Traditional Chinese names come first: they contain the generic ones.
	{"traditional chinese", "zh-Hant"}, {"chinese traditional", "zh-Hant"}, {"繁體中文", "zh-Hant"}, {"繁体中文", "zh-Hant"},
	{"正體中文", "zh-Hant"}, {"繁體", "zh-Hant"}, {"繁体", "zh-Hant"}, {"國語", "zh-Hant"},
	{"simplified chinese", "zh-Hans"}, {"chinese simplified", "zh-Hans"}, {"简体中文", "zh-Hans"}, {"简体", "zh-Hans"},
	{"中文", "zh-Hans"}, {"汉语", "zh-Hans"}, {"漢語", "zh-Hant"}, {"普通话", "zh-Hans"}, {"mandarin", "zh-Hans"}, {"chinese", "zh-Hans"},
	{"korean", "ko"}, {"한국어", "ko"}, {"한국말", "ko"}, {"한글", "ko"},
	{"japanese", "ja"}, {"日本語", "ja"}, {"にほんご", "ja"},
	{"brazilian portuguese", "pt-BR"}, {"portuguese", "pt-BR"}, {"português", "pt-BR"}, {"portugues", "pt-BR"},
	{"english", "en"}, {"영어", "en"}, {"英語", "en"}, {"英语", "en"},
	{"german", "de"}, {"deutsch", "de"},
	{"spanish", "es"}, {"español", "es"}, {"espanol", "es"}, {"castellano", "es"},
	{"french", "fr"}, {"français", "fr"}, {"francais", "fr"},
	{"italian", "it"}, {"italiano", "it"},
	{"russian", "ru"}, {"русский", "ru"},
}

// NormalizeCompanionLang maps a free-form language value (a BCP-47 tag in any
// case, with "_" or "-", or a language name in English or the language
// itself) to one of CompanionPersonaLocales, or "" when nothing matches.
// Chinese splits by script, then region (TW/HK/MO are Traditional); every
// Portuguese tag maps to pt-BR.
func NormalizeCompanionLang(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	if strings.Contains(s, "chinese") && strings.Contains(s, "traditional") {
		// "Chinese (Traditional)", "Chinese, Traditional", ...
		return "zh-Hant"
	}
	tag := strings.ReplaceAll(s, "_", "-")
	if companionLanguageTag.MatchString(tag) {
		parts := strings.Split(tag, "-")
		switch parts[0] {
		case "zh", "cmn", "yue":
			for _, p := range parts[1:] {
				switch p {
				case "hant", "tw", "hk", "mo":
					return "zh-Hant"
				case "hans", "cn", "sg", "my":
					return "zh-Hans"
				}
			}
			if parts[0] == "yue" {
				return "zh-Hant"
			}
			return "zh-Hans"
		case "pt":
			return "pt-BR"
		case "de", "en", "es", "fr", "it", "ja", "ko", "ru":
			return parts[0]
		}
	}
	for _, n := range companionLangNames {
		if strings.Contains(s, n.name) {
			return n.locale
		}
	}
	return ""
}

// CompanionResolvedPersona is the effective persona for the active Soul.
type CompanionResolvedPersona struct {
	// Source is CompanionPersonaSourceUser, ...Preset, ...Pack, or ...Official.
	Source string
	// PresetID is the official preset in effect for a ...Preset or ...Official
	// source, and "" otherwise.
	PresetID string
	Sheet    CharacteristicSheet
	// Markdown is the editable persona text: the user's persona.md verbatim,
	// or the inherited sheet rendered as Markdown.
	Markdown string
	// Lang is the raw reply language; LangKey its normalized locale ("" when
	// unmatched, which keeps the English core without an overlay).
	Lang    string
	LangKey string
}

// Inherited reports whether the persona comes from a preset, a pack, or the
// official default rather than the user's own persona.md.
func (r CompanionResolvedPersona) Inherited() bool {
	return r.Source != CompanionPersonaSourceUser
}

// Fingerprint identifies what a companion-body would be told: the rendered
// sheet and the reply language.
func (r CompanionResolvedPersona) Fingerprint() string {
	sum := sha256.Sum256([]byte(FormatCharacteristicSheet(r.Sheet, 0) + "\x00" + strings.TrimSpace(r.Lang)))
	return hex.EncodeToString(sum[:12])
}

// ResolveCompanionPersona applies the four-tier precedence: a non-empty user
// persona.md; then the official preset the user picked for the Soul (presetID
// naming a known non-default preset); then the active pack's persona; then
// the embedded default preset. Every inherited tier adds its voice_by_lang
// overlay for the language: the pack's quipLanguage when set, else
// workflowLang. An unknown presetID is ignored.
func ResolveCompanionPersona(userPersona, presetID string, pack *CompanionPersona, workflowLang string) CompanionResolvedPersona {
	langRaw := ""
	if pack != nil {
		langRaw = strings.TrimSpace(pack.QuipLanguage)
	}
	if langRaw == "" {
		langRaw = strings.TrimSpace(workflowLang)
	}

	if strings.TrimSpace(userPersona) != "" {
		sheet := ParseCharacteristicSheet(userPersona)
		lang := strings.TrimSpace(sheet.Language)
		if lang == "" {
			lang = langRaw
		}
		return CompanionResolvedPersona{
			Source:   CompanionPersonaSourceUser,
			Sheet:    sheet,
			Markdown: userPersona,
			Lang:     lang,
			LangKey:  NormalizeCompanionLang(lang),
		}
	}

	key := NormalizeCompanionLang(langRaw)
	official := officialCompanionPersona()
	var (
		sheet    CharacteristicSheet
		source   string
		presetOf string
	)
	if preset, ok := companionPersonaPreset(strings.TrimSpace(presetID)); ok && preset.ID != CompanionPersonaPresetDefault {
		source, presetOf = CompanionPersonaSourcePreset, preset.ID
		sheet = preset.sheet(key)
		if pack != nil && strings.TrimSpace(pack.Name) != "" {
			// The preset supplies the personality; the pack still names the
			// character the user sees on screen.
			sheet.Name = strings.TrimSpace(pack.Name)
		}
	} else if pack != nil && (strings.TrimSpace(pack.Name) != "" || strings.TrimSpace(pack.Voice) != "") {
		source = CompanionPersonaSourcePack
		sheet = CharacteristicSheet{
			Name:     strings.TrimSpace(pack.Name),
			Voice:    joinCompanionVoice(pack.Voice, companionVoiceOverlay(pack.VoiceByLang, key)),
			Language: strings.TrimSpace(pack.QuipLanguage),
		}
		if strings.TrimSpace(pack.Voice) == "" {
			// A name-only pack persona keeps the official voice and habits.
			named := official.sheet(key)
			named.Name = sheet.Name
			named.Language = sheet.Language
			if overlay := companionVoiceOverlay(pack.VoiceByLang, key); overlay != "" {
				named.Voice = joinCompanionVoice(official.Voice, overlay)
			}
			sheet = named
		}
	} else {
		source, presetOf = CompanionPersonaSourceOfficial, official.ID
		sheet = official.sheet(key)
	}
	return CompanionResolvedPersona{
		Source:   source,
		PresetID: presetOf,
		Sheet:    sheet,
		Markdown: FormatCharacteristicSheetMarkdown(sheet),
		Lang:     langRaw,
		LangKey:  key,
	}
}

// FormatCharacteristicSheetMarkdown renders a sheet as persona.md Markdown
// that ParseCharacteristicSheet reads back into the same fields.
func FormatCharacteristicSheetMarkdown(sheet CharacteristicSheet) string {
	var b strings.Builder
	name := strings.TrimSpace(sheet.Name)
	if name == "" {
		name = "Companion"
	}
	b.WriteString("# " + name + "\n")
	section := func(title, body string) {
		if body = strings.TrimSpace(body); body != "" {
			b.WriteString("\n## " + title + "\n\n" + body + "\n")
		}
	}
	list := func(title string, items []string) {
		var lines []string
		for _, item := range items {
			if item = strings.TrimSpace(item); item != "" {
				lines = append(lines, "- "+item)
			}
		}
		section(title, strings.Join(lines, "\n"))
	}
	section("Archetype", sheet.Archetype)
	section("Voice", sheet.Voice)
	section("Language", sheet.Language)
	list("Speech Quirks", sheet.SpeechQuirks)
	list("Gap Moe Triggers", sheet.GapMoeTriggers)
	list("Comfort Behaviors", sheet.ComfortBehaviors)
	list("Focus Behaviors", sheet.FocusBehaviors)
	section("Notes", sheet.ExtraNotes)
	return b.String()
}

// companionWorkflowLang is the global workflow.lang value, held in memory
// with the rest of the companion configuration.
func (d *Daemon) companionWorkflowLang() string {
	return strings.TrimSpace(d.companion.current().config.Lang)
}

// resolveCompanionPersonaFor resolves the effective persona given the Soul's
// persona.md text and picked preset ("" when absent), the active pack, and
// workflow.lang.
func (d *Daemon) resolveCompanionPersonaFor(userPersona, presetID string) CompanionResolvedPersona {
	pack, _ := d.CompanionActivePersona()
	return ResolveCompanionPersona(userPersona, presetID, pack, d.companionWorkflowLang())
}

// CompanionResolvedPersona resolves the active Soul's effective persona from
// memory (one read-only cold load of the Soul, then zero file touches).
func (d *Daemon) CompanionResolvedPersona() CompanionResolvedPersona {
	cfg := d.companion.current().config
	user, preset := "", ""
	if snap, ok, err := d.companionMemoryStore().Soul().PeekExisting(cfg.SoulDir, cfg.Soul, 1); err == nil && ok {
		user, preset = snap.Persona, snap.PersonaPreset
	}
	return d.resolveCompanionPersonaFor(user, preset)
}

// markCompanionBodyPersonaLaunched records the persona a new companion-body
// launch prompt carries, so later wakes can tell when it changed.
func (d *Daemon) markCompanionBodyPersonaLaunched(fp string) {
	d.companionBodyPersonaMu.Lock()
	d.companionBodyPersonaFP = fp
	d.companionBodyPersonaDiverged = false
	d.companionBodyPersonaMu.Unlock()
}

// companionBodyPersonaOverride reports whether the next wake must carry the
// current persona as an override: once the persona differs from the launch
// prompt's, every later wake carries it (so reverting is delivered too and
// a compacted body still sees it) until the next launch.
func (d *Daemon) companionBodyPersonaOverride(fp string) bool {
	d.companionBodyPersonaMu.Lock()
	defer d.companionBodyPersonaMu.Unlock()
	if d.companionBodyPersonaFP == "" {
		return false
	}
	if fp != d.companionBodyPersonaFP {
		d.companionBodyPersonaDiverged = true
	}
	return d.companionBodyPersonaDiverged
}
