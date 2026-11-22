package wsdashboard

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// companionKitFixture is an IP-clean outfit pack: two outfits over one head
// set, a body and a head attachment, an eye set, and fills on outfit layers,
// an outfit frame, a hair part and an attachment column.
const companionKitFixture = `{
	"schema":"ws-companion/v1","id":"kit","name":"Kit Fixture","canvas":[512,512],
	"registration":{"headAnchor":[255,184],"feetPivot":[256,503]},
	"defaultOutfit":"street",
	"head":{"egg":{"skinFill":"egg-fill.png","line":"egg-line.png"},"eyes":{"sclera":"eye-sclera.png"}},
	"hairs":{"bob":{"front":{"hairFill":"hair-front.png","fills":{"hairTip":"hair-tip.png"}}}},
	"poses":{
		"stand":{"headAnchor":[255,184],"feetPivot":[256,503],"layers":[{"id":"arm","line":"stand-line.png","jacketFill":"stand-jacket.png","skinFill":"stand-skin.png"}]},
		"typing":{"fallback":"stand","headAnchor":[255,240]}
	},
	"gestures":{"wave":{"pose":"stand"}},
	"outfits":{
		"street":{"palette":{"outfitPrimary":"#203040"},
			"poses":{
				"stand":{"layers":[{"line":"street-stand-line.png","skinFill":"street-stand-skin.png","fills":{"trim":"street-stand-trim.png"}}]},
				"typing":{"headAnchor":[255,240],"layers":[{"line":"street-typing-line.png","fills":{"skin":"street-typing-skin.png"}}]}
			},
			"gestures":{"wave":{"replaces":["arm"],"layers":[{"fps":4,"frames":[{"fills":{"skin":"street-wave-1.png"}},{"line":"street-wave-2.png"}]}]}}
		},
		"gown":{"poses":{"stand":{"layers":[{"line":"gown-stand-line.png","fills":{"skin":"gown-stand-skin.png","outfitPrimary":"gown-stand-fill.png"}}]}}}
	},
	"attachments":{
		"cap":{"slot":"headdress","bind":"head","layers":[{"line":"cap-line.png","fills":{"capFill":"cap-fill.png"}}]},
		"tail":{"slot":"tail","bind":"body","layers":[{"file":"tail.png"}],"columns":{"typing":{"layers":[{"fills":{"hair":"tail-seated.png"}}]}}}
	},
	"eyesets":{"round":{"sclera":"round-sclera.png","irisFill":"round-iris.png","maxPan":[6,3]}}
}`

var companionKitFiles = []string{
	"cap-fill.png", "cap-line.png", "egg-fill.png", "egg-line.png", "eye-sclera.png",
	"gown-stand-fill.png", "gown-stand-line.png", "gown-stand-skin.png", "hair-front.png", "hair-tip.png",
	"round-iris.png", "round-sclera.png", "stand-jacket.png", "stand-line.png", "stand-skin.png",
	"street-stand-line.png", "street-stand-skin.png", "street-stand-trim.png", "street-typing-line.png",
	"street-typing-skin.png", "street-wave-1.png", "street-wave-2.png", "tail-seated.png", "tail.png",
}

func TestCompanionKitEnumeration(t *testing.T) {
	_, files, err := validateCompanionManifest([]byte(companionKitFixture), "kit")
	if err != nil || !reflect.DeepEqual(files, companionKitFiles) {
		t.Fatalf("outfit fixture: files=%q err=%v", files, err)
	}
	if issues := lintCompanionKit([]byte(companionKitFixture)); len(issues) != 0 {
		t.Fatalf("clean kit reported: %q", issues)
	}

	// The embedded default pack predates the paper doll; its file list stays.
	raw, err := companionDefaultAssets.ReadFile("assets/companion/default/pack.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, files, err := validateCompanionManifest(raw, "default"); err != nil ||
		!reflect.DeepEqual(files, []string{"drone.png", "mail.png", "stand.png", "typing-desk.png"}) {
		t.Fatalf("default pack: files=%q err=%v", files, err)
	}

	// The old-reader view: an older daemon ignores the new top-level sections
	// and must still load the legacy projection alone.
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(companionKitFixture), &root); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"outfits", "attachments", "eyesets", "registration", "defaultOutfit"} {
		delete(root, key)
	}
	stripped, _ := json.Marshal(root)
	_, files, err = validateCompanionManifest(stripped, "kit")
	want := []string{"egg-fill.png", "egg-line.png", "eye-sclera.png", "hair-front.png", "hair-tip.png", "stand-jacket.png", "stand-line.png", "stand-skin.png"}
	if err != nil || !reflect.DeepEqual(files, want) {
		t.Fatalf("old-reader view: files=%q err=%v", files, err)
	}

	// Unknown keys in every new section pass through and add no files.
	extended := companionKitFixture
	for _, tc := range []struct{ old, replacement string }{
		{`"street":{"palette"`, `"street":{"future":{"file":"x.png"},"palette"`},
		{`"typing":{"headAnchor":[255,240],"layers"`, `"typing":{"future":1,"headAnchor":[255,240],"layers"`},
		{`{"fills":{"skin":"street-wave-1.png"}}`, `{"fills":{"skin":"street-wave-1.png"},"future":"y.png"}`},
		{`"slot":"headdress"`, `"slot":"headdress","future":[1]`},
		{`"columns":{"typing":{"layers"`, `"columns":{"typing":{"future":{},"layers"`},
		{`"maxPan":[6,3]`, `"maxPan":[6,3],"future":"z.png"`},
		{`"defaultOutfit":"street"`, `"defaultOutfit":"street","wardrobeFuture":{"file":"w.png"}`},
	} {
		if !strings.Contains(extended, tc.old) {
			t.Fatalf("fixture lacks %s", tc.old)
		}
		extended = strings.Replace(extended, tc.old, tc.replacement, 1)
	}
	if _, files, err := validateCompanionManifest([]byte(extended), "kit"); err != nil || !reflect.DeepEqual(files, companionKitFiles) {
		t.Fatalf("unknown keys: files=%q err=%v", files, err)
	}
}

func TestCompanionKitRejectsUnsafeAndMalformedSections(t *testing.T) {
	for _, name := range []string{
		"street-stand-trim.png", "street-typing-line.png", "street-wave-1.png", "street-wave-2.png",
		"gown-stand-skin.png", "cap-fill.png", "cap-line.png", "tail.png", "tail-seated.png",
		"round-sclera.png", "hair-tip.png",
	} {
		for _, unsafe := range []string{"../" + name, strings.TrimSuffix(name, ".png") + ".svg", `parts\\` + name} {
			t.Run(name+"/"+unsafe, func(t *testing.T) {
				raw := strings.Replace(companionKitFixture, `"`+name+`"`, `"`+unsafe+`"`, 1)
				if _, _, err := validateCompanionManifest([]byte(raw), "kit"); err == nil {
					t.Fatal("unsafe filename accepted")
				}
			})
		}
	}
	for _, tc := range []struct{ name, old, replacement string }{
		{"outfits not an object", `"outfits":{`, `"outfits":5,"x":{`},
		{"outfit null", `"gown":{"poses"`, `"gown":null,"g":{"poses"`},
		{"outfit poses not a map", `"gown":{"poses":{`, `"gown":{"poses":[],"x":{`},
		{"outfit column null", `"typing":{"headAnchor":[255,240],"layers"`, `"typing":null,"t":{"layers"`},
		{"outfit layers not a list", `"stand":{"layers":[{"line":"gown-stand-line.png"`, `"stand":{"layers":{"x":1},"y":[{"line":"gown-stand-line.png"`},
		{"empty outfit layer", `{"line":"street-typing-line.png","fills":{"skin":"street-typing-skin.png"}}`, `{}`},
		{"empty fills", `{"line":"street-typing-line.png","fills":{"skin":"street-typing-skin.png"}}`, `{"fills":{}}`},
		{"fills not a map", `"fills":{"trim":"street-stand-trim.png"}`, `"fills":["street-stand-trim.png"]`},
		{"fills value not a name", `"fills":{"trim":"street-stand-trim.png"}`, `"fills":{"trim":5}`},
		{"attachment null", `"cap":{`, `"cap":null,"c":{`},
		{"attachment columns not a map", `"columns":{"typing":`, `"columns":5,"x":{"typing":`},
		{"eyeset null", `"round":{`, `"round":null,"r":{`},
		{"eyeset raster not a string", `"round-iris.png"`, `5`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(companionKitFixture, tc.old) {
				t.Fatalf("fixture lacks %s", tc.old)
			}
			raw := strings.Replace(companionKitFixture, tc.old, tc.replacement, 1)
			if _, _, err := validateCompanionManifest([]byte(raw), "kit"); err == nil {
				t.Fatal("malformed section accepted")
			}
		})
	}
}

func TestCompanionKitFillsOnlyLayers(t *testing.T) {
	const base = `{"schema":"ws-companion/v1","id":"kit","name":"Kit","canvas":[512,512],"poses":{"stand":{"layers":[%s]}}%s}`
	accept := func(t *testing.T, raw string, want ...string) {
		t.Helper()
		_, files, err := validateCompanionManifest([]byte(raw), "kit")
		if err != nil || !reflect.DeepEqual(files, want) {
			t.Fatalf("files=%q err=%v for %s", files, err, raw)
		}
	}
	reject := func(t *testing.T, raw string) {
		t.Helper()
		if _, _, err := validateCompanionManifest([]byte(raw), "kit"); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	const legacy = `{"file":"s.png"}`
	for name, layer := range map[string]string{
		"layer": `{"fills":{"skin":"a.png"}}`,
		"frame": `{"fps":2,"frames":[{"fills":{"skin":"a.png"}}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			accept(t, fmt.Sprintf(base, legacy, `,"outfits":{"o":{"poses":{"stand":{"layers":[`+layer+`]}}}}`), "a.png", "s.png")
			accept(t, fmt.Sprintf(base, legacy, `,"outfits":{"o":{"gestures":{"wave":{"layers":[`+layer+`]}}}}`), "a.png", "s.png")
			accept(t, fmt.Sprintf(base, legacy, `,"attachments":{"t":{"slot":"tail","layers":[`+layer+`],"columns":{"stand":{"layers":[`+layer+`]}}}}`), "a.png", "s.png")
			// The legacy projection keeps the v1 rule an older daemon applies.
			reject(t, fmt.Sprintf(base, layer, ""))
			reject(t, fmt.Sprintf(base, legacy, `,"gestures":{"wave":{"layers":[`+layer+`]}}`))
			reject(t, fmt.Sprintf(base, legacy, `,"props":{"p":{"layers":[`+layer+`]}}`))
		})
	}
	// A legacy layer with a v1 raster may also carry fills: an older daemon
	// ignores the key, a new one enumerates and draws it.
	accept(t, fmt.Sprintf(base, `{"line":"l.png","fills":{"trim":"t.png"}}`, ""), "l.png", "t.png")
	accept(t, fmt.Sprintf(base, `{"fps":2,"frames":[{"line":"l.png","fills":{"trim":"t.png"}}]}`, ""), "l.png", "t.png")
	reject(t, fmt.Sprintf(base, `{"line":"l.png","fills":{"trim":"../t.png"}}`, ""))
	// An explicit semantic-split mode follows the same section rule.
	accept(t, fmt.Sprintf(base, legacy, `,"outfits":{"o":{"poses":{"stand":{"layers":[{"mode":"semantic-split","fills":{"skin":"a.png"}}]}}}}`), "a.png", "s.png")
	reject(t, fmt.Sprintf(base, `{"mode":"semantic-split","fills":{"skin":"a.png"}}`, ""))
}

func TestCompanionKitLint(t *testing.T) {
	const defective = `{
		"schema":"ws-companion/v1","id":"kit","name":"Kit","canvas":[512,512],
		"registration":{"headAnchor":[255,184],"feetPivot":[256,503]},
		"hairs":{"Bob!":{},"bob":{}},
		"eyesets":{"Big Eyes":{},"round":{}},
		"attachments":{"none":{"slot":"tail"},"Cap":{"slot":"headdress"},"cap":{"slot":"Head"},"tail":{"slot":"tail"}},
		"poses":{
			"stand":{"headAnchor":[250,184],"feetPivot":[256,503],"layers":[{"file":"s.png"},{"fills":{"skin":"p.png"}},{"fps":2,"frames":[{"line":"l.png"},{"fills":{"skin":"f.png"}}]}]},
			"typing":{"fallback":"stand","headAnchor":[255,240]}
		},
		"gestures":{"wave":{"layers":[{"fills":{"skin":"g.png"}}]}},
		"outfits":{
			"street":{"poses":{
				"stand":{"headAnchor":[255,190],"feetPivot":[262,503],"layers":[{"fills":{"skin":"a.png"}}]},
				"typing":{"headAnchor":[255,260],"layers":[{"fills":{"skin":"b.png"}}]},
				"perched":{"headAnchor":[262,300],"layers":[{"fills":{"skin":"c.png"}}]}
			},"gestures":{"cheer":{"headAnchor":[255,100],"layers":[{"fills":{"skin":"d.png"}}]}}},
			"gown":{"poses":{"stand":{"layers":[{"fills":{"skin":"e.png"}}]}}},
			"bare":{"poses":{"typing":{"layers":[{"fills":{"skin":"h.png"}}]},"stand":{"layers":[]}}},
			"Fancy":{"poses":{"stand":{"layers":[{"fills":{"skin":"i.png"}}]}}}
		}
	}`
	files, verr := companionValidation(defective)
	issues := lintCompanionKit([]byte(defective))
	if again, err := companionValidation(defective); !reflect.DeepEqual(again, files) || fmt.Sprint(err) != fmt.Sprint(verr) {
		t.Fatalf("lint changed the validation result: %q %v -> %q %v", files, verr, again, err)
	}
	if verr == nil {
		t.Fatal("the projection defect must also fail validation")
	}
	got := strings.Join(issues, "\n")
	for _, path := range []string{
		"poses.stand.layers[1]:",              // projection layer with only fills
		"poses.stand.layers[2].frames[1]:",    // projection frame with only fills
		"gestures.wave.layers[0]:",            // legacy gesture layer with only fills
		"outfits.bare: binds no stand column", // empty layers do not bind
		"outfits.street.poses.perched: has no top-level poses entry",
		"outfits.street.gestures.cheer: has no top-level gestures entry",
		"outfits.street.poses.stand.headAnchor[1]:", // stand compares y
		"outfits.street.poses.stand.feetPivot[0]:",
		"outfits.street.poses.perched.headAnchor[0]:", // other columns compare x
		"outfits.gown.poses.stand.headAnchor[0]:",     // inherited from poses.stand
		"outfits.Fancy: ID cannot be selected",
		"hairs.Bob!: ID cannot be selected",
		"eyesets.Big Eyes: ID cannot be selected",
		"attachments.none: ID none is reserved",
		"attachments.Cap: ID cannot be selected",
		"attachments.cap.slot:",
	} {
		if !strings.Contains(got, path) {
			t.Errorf("missing %s in:\n%s", path, got)
		}
	}
	for _, path := range []string{
		"outfits.street.poses.typing.headAnchor[1]", // a lower anchor is by design
		"outfits.street.poses.perched.headAnchor[1]",
		"outfits.street.gestures.cheer.headAnchor[1]",
		"outfits.street.poses.stand.headAnchor[0]",
		"outfits.street.poses.typing",
		"outfits.gown: binds no stand",
		"poses.stand.layers[0]", "poses.stand.layers[2]:",
		"hairs.bob", "eyesets.round", "attachments.tail",
	} {
		if strings.Contains(got, path) {
			t.Errorf("unexpected %s in:\n%s", path, got)
		}
	}
	if !reflect.DeepEqual(issues, lintCompanionKit([]byte(defective))) {
		t.Fatal("lint not deterministic")
	}

	// A wider tolerance absorbs the drift; no registration compares nothing.
	wide := strings.Replace(defective, `"feetPivot":[256,503]},`, `"feetPivot":[256,503],"tolerance":10},`, 1)
	if got := strings.Join(lintCompanionKit([]byte(wide)), "\n"); strings.Contains(got, "drifts") {
		t.Fatalf("tolerance ignored:\n%s", got)
	}
	none := strings.Replace(defective, `"registration":{"headAnchor":[255,184],"feetPivot":[256,503]},`, ``, 1)
	if got := strings.Join(lintCompanionKit([]byte(none)), "\n"); strings.Contains(got, "drifts") {
		t.Fatalf("drift without registration:\n%s", got)
	}
	if got := lintCompanionKit([]byte(`[]`)); len(got) != 1 {
		t.Fatalf("non-object manifest: %q", got)
	}
}

func companionValidation(raw string) ([]string, error) {
	_, files, err := validateCompanionManifest([]byte(raw), "kit")
	return files, err
}
