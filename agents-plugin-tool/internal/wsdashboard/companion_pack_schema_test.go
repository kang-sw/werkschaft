package wsdashboard

import (
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
