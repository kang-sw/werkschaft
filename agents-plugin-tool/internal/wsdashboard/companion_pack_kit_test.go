package wsdashboard

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"reflect"
	"sort"
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

// TestCompanionKitDeformLint covers the advisory mesh-deform findings: a clean
// column reports nothing, a defective one reports every rule by path, and the
// validator still accepts the manifest (deform is T0).
func TestCompanionKitDeformLint(t *testing.T) {
	const clean = `{
		"schema":"ws-companion/v1","id":"mesh","name":"Mesh","canvas":[512,512],
		"motions":{"hip-l":{"oscillate":{"rotDeg":[-6,6],"hz":0.8}}},
		"poses":{"stand":{"layers":[{"file":"s.png"}]},"dangle":{"fallback":"stand"}},
		"outfits":{"street":{"poses":{
			"stand":{"layers":[{"file":"a.png"}]},
			"dangle":{"layers":[{"id":"body","file":"b.png"},{"id":"tail","file":"t.png","z":25,"motion":"breathe"}],"deform":{
				"grid":[12,16],
				"bones":{
					"hipL":{"pivot":[235,330],"region":[[200,300],[260,300],[260,420],[200,420]],"feather":24,"motion":"hip-l"},
					"kneeL":{"pivot":[238,410],"parent":"hipL","region":[[200,400],[260,400],[260,500],[200,500]],"motion":{"oscillate":{"rotDeg":[-6,6],"hz":0.8,"phase":0.17}}}
				},
				"roll":{"pivot":[256,184],"motion":{"use":"sway-pivot"}}}}
		}}}
	}`
	if _, _, err := validateCompanionManifest([]byte(clean), "mesh"); err != nil {
		t.Fatalf("deform manifest rejected: %v", err)
	}
	if issues := lintCompanionKit([]byte(clean)); len(issues) != 0 {
		t.Fatalf("clean deform reported: %q", issues)
	}

	defective := `{
		"schema":"ws-companion/v1","id":"mesh","name":"Mesh","canvas":[512,512],
		"poses":{"stand":{"layers":[{"file":"s.png"}],"deform":{"bones":{}}}},
		"outfits":{"street":{"poses":{"stand":{"layers":[
			{"id":"low","file":"a.png","z":10},
			{"id":"mid","file":"m.png","z":15,"fps":2,"frames":[{"file":"m1.png"},{"file":"m2.png"}]},
			{"id":"swing","file":"w.png","z":16,"swingWeight":0.5},
			{"id":"top","file":"b.png","z":20},
			{"id":"above","file":"c.png","z":30,"motion":"breathe"}
		],"deform":{
			"grid":[1,40],
			"bones":{
				"Bad Id":{"pivot":[10,10],"region":[[0,0],[9,0],[9,9]]},
				"outside":{"pivot":[900,10],"region":[[0,0],[9,0],[9,9]]},
				"thin":{"pivot":[10,10],"region":[[0,0],[9,0]]},
				"orphan":{"pivot":[10,10],"parent":"ghost","region":[[0,0],[9,0],[9,9]]},
				"loopA":{"pivot":[10,10],"parent":"loopB","region":[[0,0],[9,0],[9,9]]},
				"loopB":{"pivot":[10,10],"parent":"loopA","region":[[0,0],[9,0],[9,9]]},
				"lost":{"pivot":[10,10],"region":[[0,0],[9,0],[9,9]],"motion":"nowhere"}
			},
			"roll":{"pivot":[-5,10],"motion":{"use":"nowhere"}}}}}}}
	}`
	got := strings.Join(lintCompanionKit([]byte(defective)), "\n")
	for _, want := range []string{
		"poses.stand.deform: is ignored on a registry column",
		"outfits.street.poses.stand.deform.grid[0]: must be an integer within 2..32",
		"outfits.street.poses.stand.deform.grid[1]: must be an integer within 2..32",
		`outfits.street.poses.stand.deform.bones.Bad Id: ID must match`,
		"outfits.street.poses.stand.deform.bones.outside.pivot:",
		"outfits.street.poses.stand.deform.bones.thin.region: needs at least 3 points",
		`outfits.street.poses.stand.deform.bones.orphan.parent: refers to undefined bone "ghost"`,
		"outfits.street.poses.stand.deform.bones.loopA.parent: parent chain loops",
		"outfits.street.poses.stand.deform.bones.loopB.parent: parent chain loops",
		"outfits.street.poses.stand.deform.bones.lost.motion: refers to an undefined motion",
		"outfits.street.poses.stand.deform.roll.pivot:",
		"outfits.street.poses.stand.deform.roll.motion: refers to an undefined motion",
		"outfits.street.poses.stand.layers[1]: moves between the static layers",
		"outfits.street.poses.stand.layers[2]: moves between the static layers",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"layers[4]", "layers[0]", "layers[3]", "bones.orphan.pivot", "bones.Bad Id.pivot"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("unexpected %q in:\n%s", unwanted, got)
		}
	}
	if _, _, err := validateCompanionManifest([]byte(strings.Replace(clean, `"grid":[12,16]`, `"grid":[1,40]`, 1)), "mesh"); err != nil {
		t.Fatalf("a bad grid must lint, never block a load: %v", err)
	}
}

// TestCompanionDeformFixtureIsValidAndLintClean builds the shared synthetic
// deform fixture (the one the node tests and the Chromium captures render) and
// checks that the daemon validator accepts it and the kit lint reports nothing.
func TestCompanionDeformFixtureIsValidAndLintClean(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	script := `const { deformFixture } = require("./testdata/companion-deform-fixture.cjs");
const canvas = () => ({ getContext: () => new Proxy({}, { get: () => () => {}, set: () => true }) });
process.stdout.write(JSON.stringify(deformFixture(canvas).manifest));`
	raw, err := exec.Command(node, "-e", script).Output()
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	if _, _, err := validateCompanionManifest(raw, "deform-fixture"); err != nil {
		t.Fatalf("deform fixture rejected: %v", err)
	}
	if issues := lintCompanionKit(raw); len(issues) != 0 {
		t.Fatalf("deform fixture lint: %q", issues)
	}
	if !strings.Contains(string(raw), `"kneeL"`) || !strings.Contains(string(raw), `"roll"`) {
		t.Fatalf("fixture lost its leg chain or roll: %s", raw)
	}
}

// companionClosedFixture adds closed-eye plates to the head eyes and an eye
// set, each registered under head.mouths as an eyeclosed-* key.
func companionClosedFixture(t *testing.T, edit func(string) string) string {
	t.Helper()
	raw := strings.Replace(companionKitFixture, `"eyes":{"sclera":"eye-sclera.png"}}`,
		`"eyes":{"sclera":"eye-sclera.png","closedL":"closed-l.png","closedR":"closed-r.png"},`+
			`"mouths":{"eyeclosed-kit-L":"closed-l.png","eyeclosed-kit-R":"closed-r.png","eyeclosed-round-L":"round-closed-l.png","eyeclosed-round-R":"round-closed-r.png"}}`, 1)
	raw = strings.Replace(raw, `"round-sclera.png","irisFill"`, `"round-sclera.png","closedL":"round-closed-l.png","closedR":"round-closed-r.png","irisFill"`, 1)
	if raw == companionKitFixture {
		t.Fatal("fixture edit missed")
	}
	if edit != nil {
		raw = edit(raw)
	}
	return raw
}

func TestCompanionClosedEyePlatesAreReferencedAndLinted(t *testing.T) {
	raw := companionClosedFixture(t, nil)
	_, files, err := validateCompanionManifest([]byte(raw), "kit")
	want := append(append([]string{}, companionKitFiles...), "closed-l.png", "closed-r.png", "round-closed-l.png", "round-closed-r.png")
	sort.Strings(want)
	if err != nil || !reflect.DeepEqual(files, want) {
		t.Fatalf("closed plates: files=%q err=%v", files, err)
	}
	if issues := lintCompanionKit([]byte(raw)); len(issues) != 0 {
		t.Fatalf("registered plates reported: %q", issues)
	}
	// A pack that omits the keys still validates (the base fixture).
	if _, _, err := validateCompanionManifest([]byte(companionKitFixture), "kit"); err != nil {
		t.Fatal(err)
	}
	// An unsafe plate name is refused like every other raster.
	bad := strings.Replace(raw, `"closed-l.png","closedR"`, `"../closed-l.png","closedR"`, 1)
	if _, _, err := validateCompanionManifest([]byte(bad), "kit"); err == nil {
		t.Fatal("unsafe closed plate accepted")
	}

	for _, tc := range []struct{ name, old, replacement, want string }{
		{"one side", `"closedR":"closed-r.png"`, `"x":1`, "closes one eye side only"},
		{"no sclera", `"sclera":"eye-sclera.png","closedL"`, `"closedL"`, "no sclera"},
		{"unregistered", `"eyeclosed-kit-L":"closed-l.png",`, ``, "head.mouths as an eyeclosed-* key"},
	} {
		edited := companionClosedFixture(t, func(s string) string {
			if !strings.Contains(s, tc.old) {
				t.Fatalf("%s: fixture lacks %s", tc.name, tc.old)
			}
			return strings.Replace(s, tc.old, tc.replacement, 1)
		})
		issues := lintCompanionKit([]byte(edited))
		if got := strings.Join(issues, "\n"); !strings.Contains(got, tc.want) {
			t.Fatalf("%s: lint = %q, want %q", tc.name, got, tc.want)
		}
		if _, _, err := validateCompanionManifest([]byte(edited), "kit"); err != nil {
			t.Fatalf("%s: lint-only defect rejected: %v", tc.name, err)
		}
	}
}
