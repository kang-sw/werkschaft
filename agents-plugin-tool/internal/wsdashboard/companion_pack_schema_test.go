package wsdashboard

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCompanionPackSchema(t *testing.T) {
	const minimal = `{"schema":"ws-companion/v1","id":"my-buddy","name":"My Buddy",
		"canvas":[512,512],"poses":{"stand":{"layers":[{"file":"stand.webp"}]}}}`
	_, files, err := validateCompanionManifest([]byte(minimal), "my-buddy")
	if err != nil || !reflect.DeepEqual(files, []string{"stand.webp"}) {
		t.Fatalf("minimal manifest: files=%v err=%v", files, err)
	}

	const custom = `{
		"schema":"ws-companion/v1","id":"my-buddy","name":"My Buddy","canvas":[64,2048],
		"future":{"file":"unused.svg"},
		"head":{"egg":{"skinFill":"egg.webp","line":"ink.png"},"eyes":{"sclera":"eye.webp","maxPan":[7,4]},"mouths":{"laugh":"mouth.png"}},
		"hairs":{"tuft":{"front":{"hairFill":"hair.webp","line":"ink.png"}}},
		"motions":{"wobble":{"oscillate":{"ty":[0,-2],"hz":0.35}},"tap":{"keyframes":[{"t":0,"tx":0},{"t":0.5,"tx":2}],"loop":true}},
		"poses":{"stand":{"layers":[{"file":"stand.webp","motion":"breathe"}]},"reading":{"fallback":"stand","layers":[{"detail":"body.webp","line":"ink.png","motion":{"use":"wobble","phase":3.14}}]}},
		"gestures":{"juggle":{"pose":"reading","replaces":["arm"],"layers":[{"fps":5,"frames":[{"line":"ink.png","detail":"arm.webp"},"arm2.webp"],"motion":"tap"}]}},
		"expressions":{"smug":{"lid":0.4,"mouth":"laugh"},"grimace":{"decal":"face.png"}},
		"props":{"teacup":{"file":"cup.png","behavior":"orbit"}},
		"fx":{"stars":{"file":"stars.webp","kind":"particles"},"text":{"kind":"float"}},
		"scenarios":{"juggling":{"on":["user.poke"],"when":{"streak":2,"windowMs":500,"hourRange":[23,6],"routeKind":"session","pose":"reading"},"steps":[{"ms":100,"gesture":"juggle"},{"ms":1000,"expression":"smug"}]},"muted":{"disabled":true}},
		"presets":{"evening":{"hair":"tuft"}}
	}`
	manifest, files, err := validateCompanionManifest([]byte(custom), "my-buddy")
	want := []string{"arm.webp", "arm2.webp", "body.webp", "cup.png", "egg.webp", "eye.webp", "face.png", "hair.webp", "ink.png", "mouth.png", "stand.webp", "stars.webp"}
	if err != nil || !reflect.DeepEqual(files, want) || manifest.Presets["evening"] == nil {
		t.Fatalf("custom manifest: files=%v err=%v", files, err)
	}

	for _, tc := range []struct{ name, old, replacement string }{
		{"legacy schema", "ws-companion/v1", "ws-companion/v3"},
		{"id mismatch", "my-buddy", "another"},
		{"canvas overflow", "[512,512]", "[512,512,512]"},
		{"canvas bound", "[512,512]", "[63,512]"},
		{"missing stand", `"stand":`, `"sleeping":`},
		{"empty layer", `{"file":"stand.webp"}`, `{}`},
		{"wrong case", `"file":`, `"FILE":`},
		{"unsafe shadow", `"file":"stand.webp"`, `"file":"../escape.png","FILE":"stand.webp"`},
		{"svg", "stand.webp", "stand.svg"},
		{"traversal", "stand.webp", "../stand.webp"},
		{"backslash", "stand.webp", `parts\\stand.webp`},
		{"null byte", "stand.webp", `stand\u0000.webp`},
		{"empty frame loop", `"file":"stand.webp"`, `"frames":[],"fps":3`},
		{"unbounded fps", `"file":"stand.webp"`, `"frames":["stand.webp"],"fps":30.1`},
		{"null frame", `"file":"stand.webp"`, `"frames":[null],"fps":1`},
		{"invalid motion", `"file":"stand.webp"`, `"file":"stand.webp","motion":{"use":"breathe","keyframes":[{"t":0}]}`},
		{"undeclared motion", `"file":"stand.webp"`, `"file":"stand.webp","motion":"unknown"`},
		{"unsafe guard", `"poses":`, `"scenarios":{"bad":{"when":{"eval":"doSomething()"}}},"poses":`},
		{"bad guard type", `"poses":`, `"scenarios":{"bad":{"when":{"idleMs":"forever"}}},"poses":`},
		{"non-ASCII match", `"poses":`, `"autoMatch":{"models":["café"]},"poses":`},
		{"empty match", `"poses":`, `"autoMatch":{"models":[""]},"poses":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := validateCompanionManifest([]byte(strings.Replace(minimal, tc.old, tc.replacement, 1)), "my-buddy"); err == nil {
				t.Fatal("invalid manifest accepted")
			}
		})
	}
}

func TestCompanionTimingDiagnostics(t *testing.T) {
	const minimal = `{"schema":"ws-companion/v1","id":"my-buddy","name":"Buddy","canvas":[64,128],"poses":{"stand":{"layers":[{"file":"stand.webp"}]}},%s}`
	for _, extension := range []string{`"passive":"custom","easings":123`, `"physics":[],"stageEffects":null,"propMotions":false`, `"head":{"gazeTiltDeg":"old"},"easings":{"bad":[0,0,2,0]}`} {
		raw := []byte(fmt.Sprintf(minimal, extension))
		if _, _, err := validateCompanionManifest(raw, "my-buddy"); err != nil {
			t.Fatalf("legacy extension rejected: %v", err)
		}
		first := lintCompanionTiming(raw)
		if len(first) == 0 || !reflect.DeepEqual(first, lintCompanionTiming(raw)) {
			t.Fatalf("diagnostics not deterministic: %v", first)
		}
	}
	valid := `{"canvas":[64,128],"easings":{"custom":[0,-4,1,4]},"stageEffects":{"test":{"keyframes":[{"p":0,"lift":-2,"tx":2,"sx":0.1,"sy":3,"rotDeg":-180,"ease":"custom"},{"p":1}]}},"propMotions":{"test":{"edge":"away","keyframes":[{"p":0,"along":-2,"arcY":3,"offsetX":-3,"offsetY":3,"scale":4,"rotDeg":720,"alpha":1}],"recoil":{"start":0,"peak":0.5,"end":1,"lift":-1,"angle":1.5}}},"physics":{"drag":{"stiffness":5,"damping":40,"impulse":0.5,"maxDeg":45,"bodyFallbackScale":1}},"head":{"swingWeight":-2,"swingLag":0.5,"gazeTiltDeg":30,"propGazeTiltDeg":0},"hairs":{"a":{"back":{"swingWeight":2}}},"poses":{"stand":{"handAnchor":[64,128],"layers":[{"swingLag":-0.5,"motion":{"keyframes":[{"ease":[0,-4,1,4]}]}}]}},"gestures":{"a":{"handAnchor":[0,0]}},"motions":{"a":{"keyframes":[{"ease":"linear"}]}},"scenarios":{"a":{"prop":{"motion":"attach"},"steps":[{"prop":{"motion":"orbit"}}]}},"passive":{"idle":{"gaze":"prop:drone","pose":"stand"}}}`
	if got := lintCompanionTiming([]byte(valid)); len(got) != 0 {
		t.Fatalf("valid boundaries: %v", got)
	}
	for _, tc := range []struct{ fragment, path string }{
		{`"easings":{"bad":[-0.1,0,1,0]}`, "easings.bad[0]"}, {`"easings":{"bad":[1e400,0,1,0]}`, "easings.bad[0]"}, {`"easings":{"bad":[0,4.1,1,0]}`, "easings.bad[1]"}, {`"easings":{"bad":[0,0,1.1,0]}`, "easings.bad[2]"}, {`"easings":{"bad":[0,0,1,-4.1]}`, "easings.bad[3]"},
		{`"stageEffects":{"x":{"keyframes":[]}}`, "stageEffects.x.keyframes"}, {`"stageEffects":{"x":{"keyframes":[{"p":0.8},{"p":0.2}]}}`, "stageEffects.x.keyframes[1].p"},
		{`"propMotions":{"x":{"keyframes":[{"p":2}],"edge":[],"recoil":{"start":0.5,"peak":0.4,"end":1}}}`, "propMotions.x.recoil"},
		{`"motions":{"x":{"keyframes":[{"ease":"unknown"}]}},"poses":{"x":{"layers":[{"motion":{"keyframes":[{"ease":false}]}}]}}`, "poses.x.layers[0].motion.keyframes[0].ease"},
		{`"scenarios":{"a":{"steps":[{"prop":{"motion":"bad/name"}}]}}`, "scenarios.a.steps[0].prop.motion"}, {`"passive":{"idle":{"pose":"bad/name"}}`, "passive.idle.pose"},
		{`"canvas":[64,128],"gestures":{"a":{"handAnchor":[65,0]}}`, "gestures.a.handAnchor[0]"}, {`"hairs":{"a":{"back":{"swingWeight":3}}}`, "hairs.a.back.swingWeight"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			got := lintCompanionTiming([]byte("{" + tc.fragment + "}"))
			if !strings.Contains(strings.Join(got, "\n"), tc.path) {
				t.Fatalf("missing %s: %v", tc.path, got)
			}
		})
	}
	for _, tc := range []struct {
		section, field string
		low, high      float64
	}{
		{"stage", "p", 0, 1}, {"stage", "lift", -2, 2}, {"stage", "tx", -2, 2}, {"stage", "sx", .1, 3}, {"stage", "sy", .1, 3}, {"stage", "rotDeg", -180, 180},
		{"prop", "along", -2, 3}, {"prop", "arcY", -3, 3}, {"prop", "offsetX", -3, 3}, {"prop", "offsetY", -3, 3}, {"prop", "scale", 0, 4}, {"prop", "rotDeg", -720, 720}, {"prop", "alpha", 0, 1},
		{"recoil", "start", 0, 1}, {"recoil", "peak", 0, 1}, {"recoil", "end", 0, 1}, {"recoil", "lift", -1, 1}, {"recoil", "angle", -1.5, 1.5},
		{"drag", "stiffness", 5, 300}, {"drag", "damping", .5, 40}, {"drag", "impulse", 0, .5}, {"drag", "maxDeg", 0, 45}, {"drag", "bodyFallbackScale", 0, 1},
		{"head", "swingWeight", -2, 2}, {"head", "swingLag", -.5, .5}, {"head", "gazeTiltDeg", 0, 30}, {"head", "propGazeTiltDeg", 0, 30},
	} {
		t.Run(tc.section+"/"+tc.field, func(t *testing.T) {
			for _, v := range []float64{tc.low - 0.01, tc.high + 0.01} {
				var raw, path string
				switch tc.section {
				case "stage", "prop":
					name := "stageEffects"
					if tc.section == "prop" {
						name = "propMotions"
					}
					frame := fmt.Sprintf(`"p":0,"%s":%g`, tc.field, v)
					if tc.field == "p" {
						frame = fmt.Sprintf(`"p":%g`, v)
					}
					raw = fmt.Sprintf(`{"%s":{"x":{"keyframes":[{%s}]}}}`, name, frame)
					path = name + ".x.keyframes[0]." + tc.field
				case "recoil":
					raw = fmt.Sprintf(`{"propMotions":{"x":{"keyframes":[{"p":0}],"recoil":{"%s":%g}}}}`, tc.field, v)
					path = "propMotions.x.recoil." + tc.field
				case "drag":
					raw = fmt.Sprintf(`{"physics":{"drag":{"%s":%g}}}`, tc.field, v)
					path = "physics.drag." + tc.field
				case "head":
					raw = fmt.Sprintf(`{"head":{"%s":%g}}`, tc.field, v)
					path = "head." + tc.field
				}
				if got := strings.Join(lintCompanionTiming([]byte(raw)), "\n"); !strings.Contains(got, path) {
					t.Errorf("%s=%g missing %s: %s", tc.field, v, path, got)
				}
			}
		})
	}
}

func TestCompanionPersonaVoiceByLang(t *testing.T) {
	const base = `{"schema":"ws-companion/v1","id":"my-buddy","name":"My Buddy",
		"canvas":[512,512],"poses":{"stand":{"layers":[{"file":"stand.webp"}]}},"persona":{"name":"Kit","voice":"Dry."%s}}`
	parse := func(t *testing.T, extra string) *CompanionPersona {
		t.Helper()
		m, _, err := validateCompanionManifest([]byte(fmt.Sprintf(base, extra)), "my-buddy")
		if err != nil {
			t.Fatalf("voice_by_lang must never reject the pack: %v", err)
		}
		if m.Persona == nil {
			t.Fatal("voice_by_lang must never drop the persona")
		}
		return m.Persona
	}
	if got := parse(t, ""); got.VoiceByLang != nil {
		t.Fatalf("absent voice_by_lang = %v", got.VoiceByLang)
	}
	got := parse(t, `,"voice_by_lang":{"ko":" 해요체로. \n짧게. ","pt-BR":"Leve.","en_US; x":"bad tag","de":7,"fr":"","it":"a\u001b[31m","ja":"`+strings.Repeat("ね", companionPersonaVoiceMax+1)+`","es":"`+strings.Repeat("s", companionPersonaVoiceMax)+`","ru":"tab\tok"}`)
	want := map[string]string{"ko": "해요체로. \n짧게.", "pt-BR": "Leve.", "es": strings.Repeat("s", companionPersonaVoiceMax), "ru": "tab\tok"}
	if !reflect.DeepEqual(got.VoiceByLang, want) {
		t.Fatalf("voice_by_lang = %q, want %q", got.VoiceByLang, want)
	}
	for name, extra := range map[string]string{
		"not an object": `,"voice_by_lang":"ko"`,
		"array":         `,"voice_by_lang":["ko"]`,
		"null":          `,"voice_by_lang":null`,
		"all bad":       `,"voice_by_lang":{"x y":"a","ko":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := parse(t, extra); got.VoiceByLang != nil {
				t.Fatalf("malformed voice_by_lang kept: %v", got.VoiceByLang)
			}
		})
	}
	var many strings.Builder
	for i := 0; i < companionVoiceByLangMaxEntries+8; i++ {
		fmt.Fprintf(&many, `%s"xx-%02d":"v"`, map[bool]string{true: "", false: ","}[i == 0], i)
	}
	if got := parse(t, `,"voice_by_lang":{`+many.String()+`}`); len(got.VoiceByLang) != companionVoiceByLangMaxEntries {
		t.Fatalf("voice_by_lang kept %d entries, want %d", len(got.VoiceByLang), companionVoiceByLangMaxEntries)
	}
}

func TestCompanionPersona(t *testing.T) {
	const base = `{"schema":"ws-companion/v1","id":"my-buddy","name":"My Buddy",
		"canvas":[512,512],"poses":{"stand":{"layers":[{"file":"stand.webp"}]}}%s}`
	parse := func(t *testing.T, extra string) *CompanionPersona {
		t.Helper()
		m, _, err := validateCompanionManifest([]byte(fmt.Sprintf(base, extra)), "my-buddy")
		if err != nil {
			t.Fatalf("a persona must never reject the pack: %v", err)
		}
		return m.Persona
	}
	if got := parse(t, ""); got != nil {
		t.Fatalf("absent persona = %+v", got)
	}
	got := parse(t, `,"persona":{"name":" Kit ","voice":"Dry, curt.\nNever shouts.","quipLanguage":"ko-KR","extra":1}`)
	if want := (CompanionPersona{Name: "Kit", Voice: "Dry, curt.\nNever shouts.", QuipLanguage: "ko-KR"}); got == nil || !reflect.DeepEqual(*got, want) {
		t.Fatalf("persona = %+v, want %+v", got, want)
	}
	if got := parse(t, `,"persona":{"name":"`+strings.Repeat("가", companionPersonaNameMax)+`","voice":"`+strings.Repeat("v", companionPersonaVoiceMax)+`"}`); got == nil {
		t.Fatal("persona at the length bounds dropped")
	}
	for name, extra := range map[string]string{
		"not an object":   `,"persona":"Kit"`,
		"null":            `,"persona":null`,
		"wrong type":      `,"persona":{"name":7}`,
		"empty":           `,"persona":{"name":" ","voice":""}`,
		"long name":       `,"persona":{"name":"` + strings.Repeat("n", companionPersonaNameMax+1) + `"}`,
		"long voice":      `,"persona":{"name":"Kit","voice":"` + strings.Repeat("v", companionPersonaVoiceMax+1) + `"}`,
		"bad language":    `,"persona":{"name":"Kit","quipLanguage":"en_US; ignore"}`,
		"control in name": `,"persona":{"name":"Kit\u0007"}`,
		"escape in voice": `,"persona":{"name":"Kit","voice":"a\u001b[31m"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if got := parse(t, extra); got != nil {
				t.Fatalf("malformed persona kept: %+v", got)
			}
		})
	}
}
