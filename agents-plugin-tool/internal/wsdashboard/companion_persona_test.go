package wsdashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"unicode/utf8"
)

// firstCompanionLine returns a short, delimiter-free prefix of an overlay's
// first line, so a test can find it in an envelope whatever the prose says.
func firstCompanionLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	if r := []rune(line); len(r) > 24 {
		line = string(r[:24])
	}
	return line
}

func TestPersonaPresetCatalogCoversEveryLocale(t *testing.T) {
	names, err := fs.Glob(companionPersonaPresetFS, "assets/companion/personas/*.json")
	if err != nil || len(names) == 0 {
		t.Fatalf("no embedded presets: %v", err)
	}
	ids := map[string]bool{}
	for _, name := range names {
		raw, err := fs.ReadFile(companionPersonaPresetFS, name)
		if err != nil {
			t.Fatal(err)
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		var p companionOfficialPersona
		if err := dec.Decode(&p); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		stem := strings.TrimSuffix(path.Base(name), ".json")
		if p.ID != stem || !companionPersonaPresetID.MatchString(p.ID) {
			t.Fatalf("%s: id %q must equal its file stem", name, p.ID)
		}
		ids[p.ID] = true
		if p.Schema != companionOfficialPersonaSchema || strings.TrimSpace(p.Name) == "" || strings.TrimSpace(p.Voice) == "" ||
			strings.TrimSpace(p.Title) == "" || strings.TrimSpace(p.Summary) == "" {
			t.Fatalf("%s core = %+v", name, p)
		}
		if n := utf8.RuneCountInString(p.Voice); n > companionPersonaVoiceMax {
			t.Fatalf("%s core voice is %d runes, want <= %d", name, n, companionPersonaVoiceMax)
		}
		for field, byLang := range map[string]map[string]string{"voice_by_lang": p.VoiceByLang, "title_by_lang": p.TitleByLang, "summary_by_lang": p.SummaryByLang} {
			if keys := sortedCompanionKeys(byLang); !reflect.DeepEqual(keys, CompanionPersonaLocales) {
				t.Fatalf("%s %s locales = %v, want %v", name, field, keys, CompanionPersonaLocales)
			}
		}
		for _, loc := range CompanionPersonaLocales {
			if v := p.VoiceByLang[loc]; strings.TrimSpace(v) == "" || utf8.RuneCountInString(v) > companionPersonaVoiceMax || !companionVoiceText(v) {
				t.Errorf("%s %s overlay invalid (%d runes): %q", name, loc, utf8.RuneCountInString(v), v)
			}
			for _, label := range []string{p.TitleByLang[loc], p.SummaryByLang[loc]} {
				if strings.TrimSpace(label) == "" || utf8.RuneCountInString(label) > companionPresetLabelMax || strings.ContainsAny(label, "\n\t") || !companionVoiceText(label) {
					t.Errorf("%s %s label invalid: %q", name, loc, label)
				}
			}
			if NormalizeCompanionLang(loc) != loc {
				t.Errorf("locale %s does not normalize to itself", loc)
			}
		}
		// Overlays pass the same validation a pack's voice_by_lang does.
		raw, _ = json.Marshal(map[string]any{"voice_by_lang": p.VoiceByLang})
		var obj companionObject
		if err := json.Unmarshal(raw, &obj); err != nil {
			t.Fatal(err)
		}
		if got := companionVoiceByLangField(obj); !reflect.DeepEqual(got, p.VoiceByLang) {
			t.Fatalf("%s overlays fail pack validation: %v", name, got)
		}
		// Overlays are per-locale prose, not copies of the English one.
		for _, loc := range CompanionPersonaLocales {
			if loc != "en" && p.VoiceByLang[loc] == p.VoiceByLang["en"] {
				t.Errorf("%s %s overlay duplicates the en overlay", name, loc)
			}
		}
	}
	if !ids[CompanionPersonaPresetDefault] {
		t.Fatal("the catalog has no default preset")
	}
	// Every file decodes into the catalog, default first, then by ID.
	views := CompanionPersonaPresetViews()
	if len(views) != len(names) || views[0].ID != CompanionPersonaPresetDefault {
		t.Fatalf("catalog = %+v, want %d presets with default first", views, len(names))
	}
	for i := 2; i < len(views); i++ {
		if views[i-1].ID >= views[i].ID {
			t.Fatalf("catalog order: %q before %q", views[i-1].ID, views[i].ID)
		}
	}
	for _, v := range views {
		if len(v.TitleByLang) != len(CompanionPersonaLocales) || len(v.SummaryByLang) != len(CompanionPersonaLocales) {
			t.Fatalf("view %s lacks localized labels: %+v", v.ID, v)
		}
	}
}

func TestPersonaPresetCatalogDecodeIsTolerant(t *testing.T) {
	good := `{"id":"zed","name":"Z","voice":"Zippy."}`
	fsys := fstest.MapFS{
		"assets/companion/personas/zed.json":    {Data: []byte(good)},
		"assets/companion/personas/wrong.json":  {Data: []byte(`{"id":"other","voice":"V."}`)},
		"assets/companion/personas/broken.json": {Data: []byte(`{`)},
		"assets/companion/personas/mute.json":   {Data: []byte(`{"id":"mute","name":"M"}`)},
	}
	got := decodeCompanionPersonaPresets(fsys)
	if len(got) != 2 || got[0].ID != CompanionPersonaPresetDefault || got[1].ID != "zed" || strings.TrimSpace(got[0].Voice) == "" {
		t.Fatalf("decoded = %+v", got)
	}
}

func TestPersonaNormalizeCompanionLang(t *testing.T) {
	for raw, want := range map[string]string{
		"":                      "",
		"ko":                    "ko",
		"ko-KR":                 "ko",
		"ko_kr":                 "ko",
		"KO":                    "ko",
		"Korean":                "ko",
		"한국어":                   "ko",
		"Respond in Korean":     "ko",
		"ja-JP":                 "ja",
		"日本語":                   "ja",
		"en":                    "en",
		"en-US":                 "en",
		"English":               "en",
		"de-AT":                 "de",
		"Deutsch":               "de",
		"es-419":                "es",
		"Español":               "es",
		"fr-CA":                 "fr",
		"français":              "fr",
		"it":                    "it",
		"Italiano":              "it",
		"pt":                    "pt-BR",
		"pt-PT":                 "pt-BR",
		"pt_br":                 "pt-BR",
		"Portuguese":            "pt-BR",
		"ru-RU":                 "ru",
		"русский":               "ru",
		"zh":                    "zh-Hans",
		"zh-CN":                 "zh-Hans",
		"zh-Hans-CN":            "zh-Hans",
		"zh-TW":                 "zh-Hant",
		"zh_HK":                 "zh-Hant",
		"zh-Hant":               "zh-Hant",
		"yue":                   "zh-Hant",
		"Chinese":               "zh-Hans",
		"Traditional Chinese":   "zh-Hant",
		"Chinese (Traditional)": "zh-Hant",
		"Chinese, Traditional":  "zh-Hant",
		"Chinese (Simplified)":  "zh-Hans",
		"简体中文":                  "zh-Hans",
		"繁體中文":                  "zh-Hant",
		"nl":                    "",
		"Klingon":               "",
		"x":                     "",
	} {
		if got := NormalizeCompanionLang(raw); got != want {
			t.Errorf("NormalizeCompanionLang(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestPersonaFourTierPrecedence(t *testing.T) {
	official := officialCompanionPersona()

	// Official tier: the English core plus the matching locale overlay.
	got := ResolveCompanionPersona("", "", nil, "ko-KR")
	if got.Source != CompanionPersonaSourceOfficial || !got.Inherited() || got.LangKey != "ko" || got.Lang != "ko-KR" {
		t.Fatalf("official tier = %+v", got)
	}
	if want := strings.TrimSpace(official.Voice) + "\n" + official.VoiceByLang["ko"]; got.Sheet.Voice != want {
		t.Fatalf("official ko voice = %q, want %q", got.Sheet.Voice, want)
	}
	if !strings.HasPrefix(got.Markdown, "# "+official.Name+"\n") {
		t.Fatalf("official markdown = %q", got.Markdown)
	}
	// Unmatched language: the English core only.
	if got := ResolveCompanionPersona("", "", nil, "Klingon"); got.Sheet.Voice != strings.TrimSpace(official.Voice) || got.LangKey != "" {
		t.Fatalf("unmatched language voice = %q key=%q", got.Sheet.Voice, got.LangKey)
	}
	if got := ResolveCompanionPersona("", "", nil, ""); got.Sheet.Voice != strings.TrimSpace(official.Voice) {
		t.Fatalf("no language voice = %q", got.Sheet.Voice)
	}

	// Pack tier: the pack's voice plus its own overlay; quipLanguage wins
	// over workflow.lang.
	pack := &CompanionPersona{Name: "Kit", Voice: "Dry.", QuipLanguage: "ja", VoiceByLang: map[string]string{"ja-JP": "です・ます調で。", "ko": "해요체."}}
	got = ResolveCompanionPersona("", "", pack, "ko")
	if got.Source != CompanionPersonaSourcePack || !got.Inherited() || got.Sheet.Name != "Kit" || got.Sheet.Voice != "Dry.\nです・ます調で。" || got.Lang != "ja" {
		t.Fatalf("pack tier = %+v", got)
	}
	// A pack without an overlay for the language keeps its plain voice; it
	// never borrows the official overlay.
	got = ResolveCompanionPersona("", "", &CompanionPersona{Name: "Kit", Voice: "Dry."}, "fr")
	if got.Sheet.Voice != "Dry." {
		t.Fatalf("pack voice without overlay = %q", got.Sheet.Voice)
	}
	// A name-only pack keeps the official voice and habits under its name.
	got = ResolveCompanionPersona("", "", &CompanionPersona{Name: "Mo"}, "de")
	if got.Source != CompanionPersonaSourcePack || got.Sheet.Name != "Mo" ||
		got.Sheet.Voice != strings.TrimSpace(official.Voice)+"\n"+official.VoiceByLang["de"] || len(got.Sheet.SpeechQuirks) == 0 {
		t.Fatalf("name-only pack = %+v", got)
	}

	// User tier: a non-empty persona.md wins verbatim; blank does not count.
	user := "# Luna\n\n## Voice\nCalm telemetry.\n\n## Language\nja\n"
	got = ResolveCompanionPersona(user, "", pack, "ko")
	if got.Source != CompanionPersonaSourceUser || got.Inherited() || got.Sheet.Name != "Luna" || got.Markdown != user || got.Lang != "ja" {
		t.Fatalf("user tier = %+v", got)
	}
	if got := ResolveCompanionPersona(" \n\t", "", pack, "ko"); got.Source != CompanionPersonaSourcePack {
		t.Fatalf("blank persona.md source = %q", got.Source)
	}

	// Preset tier: a picked non-default preset wins over the pack (with the
	// preset's own overlay; the pack still names the character) and loses
	// to persona.md.
	var picked companionOfficialPersona
	for _, p := range companionPersonaPresets() {
		if p.ID != CompanionPersonaPresetDefault {
			picked = p
			break
		}
	}
	if picked.ID == "" {
		t.Fatal("the catalog has no non-default preset")
	}
	got = ResolveCompanionPersona("", picked.ID, pack, "ko")
	if got.Source != CompanionPersonaSourcePreset || !got.Inherited() || got.PresetID != picked.ID || got.Sheet.Name != "Kit" ||
		got.Sheet.Voice != strings.TrimSpace(picked.Voice)+"\n"+picked.VoiceByLang["ja"] || got.Lang != "ja" {
		t.Fatalf("preset tier over pack = %+v", got)
	}
	if got := ResolveCompanionPersona("", picked.ID, nil, "ko"); got.Sheet.Voice != strings.TrimSpace(picked.Voice)+"\n"+picked.VoiceByLang["ko"] ||
		got.Sheet.Name != picked.Name {
		t.Fatalf("preset without a pack = %+v", got)
	}
	if got := ResolveCompanionPersona(user, picked.ID, pack, "ko"); got.Source != CompanionPersonaSourceUser {
		t.Fatalf("persona.md under a preset: source = %q", got.Source)
	}
	// Default and unknown picks fall through to the pack, then the default.
	for _, id := range []string{CompanionPersonaPresetDefault, "no-such-preset", "../x"} {
		if got := ResolveCompanionPersona("", id, pack, "ko"); got.Source != CompanionPersonaSourcePack || got.PresetID != "" {
			t.Fatalf("pick %q over a pack = %+v", id, got)
		}
		if got := ResolveCompanionPersona("", id, nil, "ko"); got.Source != CompanionPersonaSourceOfficial || got.PresetID != CompanionPersonaPresetDefault {
			t.Fatalf("pick %q without a pack = %+v", id, got)
		}
	}
	if ResolveCompanionPersona("", picked.ID, nil, "ko").Fingerprint() == ResolveCompanionPersona("", "", nil, "ko").Fingerprint() {
		t.Fatal("a preset switch does not change the fingerprint")
	}

	// Fingerprints follow what the body is told: sheet and reply language.
	a := ResolveCompanionPersona("", "", nil, "ko").Fingerprint()
	if a != ResolveCompanionPersona("", "", nil, "ko").Fingerprint() || a == ResolveCompanionPersona("", "", nil, "ja").Fingerprint() {
		t.Fatal("fingerprint is not a stable function of sheet and language")
	}
}

func TestPersonaMarkdownRoundTrip(t *testing.T) {
	for _, preset := range companionPersonaPresets() {
		for _, loc := range append([]string{""}, CompanionPersonaLocales...) {
			sheet := preset.sheet(loc)
			sheet.Language = loc
			md := FormatCharacteristicSheetMarkdown(sheet)
			back := ParseCharacteristicSheet(md)
			// The Notes heading maps back to the extra notes without a label,
			// so saving inherited text unchanged keeps the same sheet.
			if FormatCharacteristicSheet(back, 0) != FormatCharacteristicSheet(sheet, 0) {
				t.Fatalf("%s/%q round trip:\n%s\n--- vs ---\n%s", preset.ID, loc, FormatCharacteristicSheet(back, 0), FormatCharacteristicSheet(sheet, 0))
			}
			if again := FormatCharacteristicSheetMarkdown(back); again != md {
				t.Fatalf("%s/%q Markdown is not stable across a save:\n%s\n--- vs ---\n%s", preset.ID, loc, again, md)
			}
		}
	}
}

func TestPersonaWakeOverrideReachesLiveBody(t *testing.T) {
	soulRoot := t.TempDir()
	d := &Daemon{dir: t.TempDir(), home: t.TempDir()}
	cfg := agentCompanion()
	cfg.Soul = "luna"
	cfg.SoulDir = soulRoot
	setCompanionConfig(d, cfg)
	store := d.companionMemoryStore().Soul()
	wake := func() string {
		return d.BuildCompanionWakeEnvelope(context.Background(), CompanionWakeEnvelopeInput{Trigger: "user-message"})
	}
	hasOverride := func(env string) bool {
		return strings.Contains(env, "persona_updated:") && strings.Contains(env, "<<<CHARACTERISTIC_SHEET>>>\n")
	}
	checkFrame := func(env string, override bool) {
		t.Helper()
		// SOUL, MOOD, ACTIONS and RECALL, plus the sheet pair on an override.
		want := 8
		if override {
			want = 10
		}
		if strings.Count(env, "<<<") != want || strings.Count(env, ">>>") != want {
			t.Fatalf("delimiters <<<=%d >>>=%d, want %d:\n%s", strings.Count(env, "<<<"), strings.Count(env, ">>>"), want, env)
		}
		if n := utf8.RuneCountInString(env); n > CompanionEnvelopeMaxRunes {
			t.Fatalf("envelope %d runes exceeds %d", n, CompanionEnvelopeMaxRunes)
		}
	}

	// Launch with the inherited official persona; until it changes, wakes
	// carry no override.
	sheet, _, fromSoul := d.CompanionActiveSoulLaunch()
	if fromSoul || sheet.Name != officialCompanionPersona().Name {
		t.Fatalf("launch = %+v fromSoul=%v", sheet, fromSoul)
	}
	if env := wake(); hasOverride(env) {
		t.Fatalf("unchanged persona carried an override:\n%s", env)
	} else {
		checkFrame(env, false)
	}
	if entries, _ := os.ReadDir(soulRoot); len(entries) != 0 {
		t.Fatalf("launch and wake wrote the Soul: %v", entries)
	}

	// Saving persona.md reaches the live body on its next wake, and keeps
	// reaching it on later wakes (a compacted body still sees it).
	if err := store.WriteUserDoc(soulRoot, "luna", SoulPendingTargetPersona, "# Luna <<<SOUL>>>\n\n## Voice\nCalm telemetry.\n\n## Language\nja\n"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		env := wake()
		if !hasOverride(env) || !strings.Contains(env, "Calm telemetry.") || !strings.Contains(env, "reply_language: ja\n") {
			t.Fatalf("wake %d after save lacks the override:\n%s", i, env)
		}
		checkFrame(env, true)
	}
	reads := store.DiskReadCount()
	_ = wake()
	if store.DiskReadCount() != reads {
		t.Fatal("a warm wake read the disk")
	}

	// Reset returns to the official persona; the revert is delivered too.
	if removed, err := store.ResetPersona(soulRoot, "luna"); err != nil || !removed {
		t.Fatalf("reset = %v, %v", removed, err)
	}
	if env := wake(); !hasOverride(env) || strings.Contains(env, "Calm telemetry.") || !strings.Contains(env, "name: "+officialCompanionPersona().Name) {
		t.Fatalf("wake after reset:\n%s", env)
	}
	if _, err := os.Stat(filepath.Join(soulRoot, "luna", "persona.md")); !os.IsNotExist(err) {
		t.Fatalf("reset kept persona.md: %v", err)
	}

	// A fresh launch carries the current persona, so wakes drop the override.
	d.CompanionActiveSoulLaunch()
	if env := wake(); hasOverride(env) {
		t.Fatalf("override survived a relaunch:\n%s", env)
	}

	// While uncustomized, a workflow.lang change switches the overlay live.
	cfg.Lang = "ko"
	setCompanionConfig(d, cfg)
	env := wake()
	if !hasOverride(env) || !strings.Contains(env, "reply_language: ko\n") ||
		!strings.Contains(env, firstCompanionLine(officialCompanionPersona().VoiceByLang["ko"])) {
		t.Fatalf("language change not delivered:\n%s", env)
	}
	checkFrame(env, true)
}

func TestPersonaPresetPickStoreAndLiveBody(t *testing.T) {
	soulRoot := t.TempDir()
	d := &Daemon{dir: t.TempDir(), home: t.TempDir()}
	cfg := agentCompanion()
	cfg.Soul = "luna"
	cfg.SoulDir = soulRoot
	setCompanionConfig(d, cfg)
	store := d.companionMemoryStore().Soul()
	presets := companionPersonaPresets()
	if len(presets) < 2 {
		t.Fatal("the catalog needs a non-default preset")
	}
	picked := presets[1]
	presetPath := filepath.Join(soulRoot, "luna", companionSoulPresetFile)
	wake := func() string {
		return d.BuildCompanionWakeEnvelope(context.Background(), CompanionWakeEnvelopeInput{Trigger: "user-message"})
	}

	d.CompanionActiveSoulLaunch()
	// Picking "default" on a fresh Soul writes nothing.
	if err := store.SetPersonaPreset(soulRoot, "luna", CompanionPersonaPresetDefault); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(soulRoot); len(entries) != 0 {
		t.Fatalf("a default pick wrote the Soul: %v", entries)
	}
	// A non-default pick is one small file and reaches the live body.
	if err := store.SetPersonaPreset(soulRoot, "luna", picked.ID); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(presetPath); err != nil || strings.TrimSpace(string(raw)) != picked.ID {
		t.Fatalf("preset file = %q, %v", raw, err)
	}
	if env := wake(); !strings.Contains(env, "persona_updated:") || !strings.Contains(env, "name: "+picked.Name) {
		t.Fatalf("preset pick not delivered:\n%s", env)
	}
	if got := d.CompanionResolvedPersona(); got.Source != CompanionPersonaSourcePreset || got.PresetID != picked.ID {
		t.Fatalf("resolved after pick = %+v", got)
	}
	// The pick survives a cold reload; malformed or unknown IDs are refused.
	store.Invalidate()
	if snap, ok, err := store.PeekExisting(soulRoot, "luna", 1); err != nil || !ok || snap.PersonaPreset != picked.ID {
		t.Fatalf("reloaded pick = %q ok=%v err=%v", snap.PersonaPreset, ok, err)
	}
	for _, bad := range []string{"../x", "UPPER", "a b"} {
		if err := store.SetPersonaPreset(soulRoot, "luna", bad); err == nil {
			t.Fatalf("SetPersonaPreset(%q) accepted", bad)
		}
	}
	// A file holding garbage reads as no pick.
	if err := os.WriteFile(presetPath, []byte("../../etc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store.Invalidate()
	if got := d.CompanionResolvedPersona(); got.Source != CompanionPersonaSourceOfficial {
		t.Fatalf("garbage pick resolved to %+v", got)
	}
	// Picking default removes the file again.
	if err := store.SetPersonaPreset(soulRoot, "luna", CompanionPersonaPresetDefault); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(presetPath); !os.IsNotExist(err) {
		t.Fatalf("default pick kept the preset file: %v", err)
	}
}

// TestPersonaPresetSheetsFitBudget renders every embedded preset in every
// dashboard locale (and with no locale) and requires the whole sheet, with
// its locale overlay, to fit the launch budget and the live-override frame
// untrimmed.
func TestPersonaPresetSheetsFitBudget(t *testing.T) {
	presets := companionPersonaPresets()
	views := CompanionPersonaPresetViews()
	for _, want := range []string{CompanionPersonaPresetDefault, "cheeky-imp", "crisp-partner"} {
		found := false
		for _, v := range views {
			if v.ID == want {
				found = len(v.TitleByLang) == len(CompanionPersonaLocales) && len(v.SummaryByLang) == len(CompanionPersonaLocales)
			}
		}
		if !found {
			t.Errorf("preset %q is missing from the picker or lacks localized labels", want)
		}
	}
	if presets[0].ID != CompanionPersonaPresetDefault {
		t.Fatalf("first preset = %q, want %q", presets[0].ID, CompanionPersonaPresetDefault)
	}
	for _, preset := range presets {
		for _, loc := range CompanionPersonaLocales {
			if strings.TrimSpace(preset.VoiceByLang[loc]) == "" {
				t.Errorf("%s: missing voice_by_lang[%q]", preset.ID, loc)
			}
		}
		for _, loc := range append([]string{""}, CompanionPersonaLocales...) {
			sheet := preset.sheet(loc)
			if loc != "" && !strings.Contains(sheet.Voice, strings.TrimSpace(preset.VoiceByLang[loc])) {
				t.Errorf("%s/%s: sheet voice lacks the locale overlay", preset.ID, loc)
			}
			full := FormatCharacteristicSheet(sheet, 0)
			n := utf8.RuneCountInString(full)
			if n > CompanionLaunchSheetBudgetRunes {
				t.Errorf("%s/%q: sheet is %d runes, over the %d-rune budget", preset.ID, loc, n, CompanionLaunchSheetBudgetRunes)
			}
			if got := FormatCharacteristicSheet(sheet, CompanionLaunchSheetBudgetRunes); got != full {
				t.Errorf("%s/%q: the launch budget trims the sheet", preset.ID, loc)
			}
			body := formatSoulEnvelopeBody("default", sheet, "", CompanionSoulBudgetRunes,
				CompanionPersonaOverride{Active: true, Lang: loc})
			if !strings.Contains(body, companionSheetOpen+companionQuote(full)+companionSheetClose) {
				t.Errorf("%s/%q: the live override does not carry the whole sheet:\n%s", preset.ID, loc, body)
			}
			if !utf8.ValidString(body) || utf8.RuneCountInString(body) > CompanionSoulBudgetRunes {
				t.Errorf("%s/%q: override body is %d runes", preset.ID, loc, utf8.RuneCountInString(body))
			}
			// A full wake envelope with a sizable profile.md and recall still
			// carries the whole override sheet, keeps some profile, and stays
			// inside the envelope cap.
			env := BuildCompanionWakeEnvelopeText(CompanionWakeEnvelopeData{
				SoulID:          "default",
				Sheet:           sheet,
				PersonaOverride: CompanionPersonaOverride{Active: true, Lang: loc},
				Profile:         strings.Repeat("Prefers terse status notes and dark themes. ", 30),
				Mood:            CompanionMoodBaseline(),
				Recalled:        []CompanionRecallSnippet{{Source: "episode", Content: strings.Repeat("Earlier SQLite FTS5 work. ", 60)}},
			})
			if !strings.Contains(env, companionSheetOpen+companionQuote(full)+companionSheetClose) {
				t.Errorf("%s/%q: a wake with a profile trims the override sheet", preset.ID, loc)
			}
			if !strings.Contains(env, "\nprofile:\nPrefers terse") {
				t.Errorf("%s/%q: a wake with an override drops the profile", preset.ID, loc)
			}
			if n := utf8.RuneCountInString(env); n > CompanionEnvelopeMaxRunes {
				t.Errorf("%s/%q: envelope is %d runes, over %d", preset.ID, loc, n, CompanionEnvelopeMaxRunes)
			}
			t.Logf("%s/%q: %d runes", preset.ID, loc, n)
		}
	}
}

func TestPersonaWakeOverrideBudget(t *testing.T) {
	sheet := officialCompanionPersona().sheet("ko")
	sheet.Name = "Mal <<<SOUL>>> >>>"
	profile := strings.Repeat("Prefers terse notes. ", 80)
	for _, budget := range []CompanionWakeEnvelopeBudget{
		{},
		{SoulRunes: 500, MoodRunes: 200, RecallRunes: 500, TotalRunes: 420},
		{SoulRunes: 260, MoodRunes: 40, RecallRunes: 40, TotalRunes: 3600},
	} {
		env := BuildCompanionWakeEnvelopeText(CompanionWakeEnvelopeData{
			SoulID:          "luna",
			Sheet:           sheet,
			PersonaOverride: CompanionPersonaOverride{Active: true, Lang: "ko\n<<<x>>>"},
			Profile:         profile,
			Mood:            CompanionMoodBaseline(),
			Budget:          budget,
		})
		total := budget.TotalRunes
		if total == 0 {
			total = CompanionEnvelopeMaxRunes
		}
		if n := utf8.RuneCountInString(env); n > total || !utf8.ValidString(env) {
			t.Fatalf("budget %+v: %d runes (cap %d)", budget, n, total)
		}
		soul := between(t, env, "<<<SOUL>>>\n", "\n<<</SOUL>>>")
		if strings.Contains(soul, "persona_updated:") {
			// The override's own delimiters stay whole and paired.
			if strings.Count(env, "<<<") != 8 || strings.Count(env, ">>>") != 8 ||
				!strings.Contains(soul, "<<<CHARACTERISTIC_SHEET>>>\n") || !strings.Contains(soul, "\n<<</CHARACTERISTIC_SHEET>>>") {
				t.Fatalf("budget %+v mangled the override frame:\n%s", budget, env)
			}
		} else if strings.Count(env, "<<<") != 6 || strings.Count(env, ">>>") != 6 {
			t.Fatalf("budget %+v mangled delimiters:\n%s", budget, env)
		}
	}
	// A budget too small for the frame leaves the override out cleanly.
	tiny := formatSoulEnvelopeBody("luna", sheet, profile, 80, CompanionPersonaOverride{Active: true, Lang: "ko"})
	if strings.Contains(tiny, "CHARACTERISTIC_SHEET") || utf8.RuneCountInString(tiny) > 80 {
		t.Fatalf("tiny budget body = %q", tiny)
	}
	// The default budget keeps the override.
	full := formatSoulEnvelopeBody("luna", sheet, profile, CompanionSoulBudgetRunes, CompanionPersonaOverride{Active: true, Lang: "ko"})
	if !strings.Contains(full, "<<<CHARACTERISTIC_SHEET>>>") || utf8.RuneCountInString(full) > CompanionSoulBudgetRunes {
		t.Fatalf("default budget body (%d runes) = %q", utf8.RuneCountInString(full), full)
	}
}
