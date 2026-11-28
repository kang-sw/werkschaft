package wsdashboard

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

func companionAuthoringDoc(t testing.TB, file string) string {
	t.Helper()
	data, err := companionAuthoringFS.ReadFile("assets/companion/authoring/" + file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func companionAuthoringSchema(t testing.TB, file string) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(companionAuthoringDoc(t, file)), &schema); err != nil {
		t.Fatalf("%s: %v", file, err)
	}
	return schema
}

// ---- routes ----

func TestCompanionAuthoringRoutes(t *testing.T) {
	d := authFixture(t)
	d.config = &ConfigBackend{Options: wsconfig.Options{ConfigHome: t.TempDir()}}
	d.lan = true
	cookie := authCookie(t, d)
	token, err := d.lanToken()
	if err != nil {
		t.Fatal(err)
	}
	lanRequest := func(method, path string) *httptest.ResponseRecorder {
		r := authRequest(method, path, "")
		r.Host, r.RemoteAddr = "192.0.2.1:43210", "192.0.2.2:1234"
		r.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
		r.Header.Set("Origin", "http://"+r.Host)
		return authServe(d, r)
	}
	for _, tc := range []struct{ route, file, contentType string }{
		{companionAuthoringGuideRoute, "guide.md", "text/markdown; charset=utf-8"},
		{companionAuthoringPackSchemaRoute, "pack.schema.json", "application/schema+json"},
		{companionAuthoringMarketplaceSchemaRoute, "marketplace.schema.json", "application/schema+json"},
	} {
		t.Run(tc.file, func(t *testing.T) {
			want := companionAuthoringDoc(t, tc.file)
			w := companionTestRequest(d, cookie, "GET", tc.route, "")
			if w.Code != 200 || w.Body.String() != want {
				t.Fatalf("GET: %d, body matches embedded file: %v", w.Code, w.Body.String() == want)
			}
			if w.Header().Get("Content-Type") != tc.contentType || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("GET headers: %+v", w.Header())
			}
			head := companionTestRequest(d, cookie, "HEAD", tc.route, "")
			if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Type") != tc.contentType ||
				head.Header().Get("X-Content-Type-Options") != "nosniff" || head.Header().Get("Content-Length") != fmt.Sprint(len(want)) {
				t.Fatalf("HEAD: %d %q %+v", head.Code, head.Body.String(), head.Header())
			}
			for _, method := range []string{"POST", "PUT", "PATCH", "DELETE"} {
				w := companionTestRequest(d, cookie, method, tc.route, companionTestOrigin)
				if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "GET, HEAD" {
					t.Errorf("%s: %d %+v", method, w.Code, w.Header())
				}
			}
			// The same access outcomes as the catalog route: no cookie is
			// refused, a local session reads, and a LAN client with the
			// token reads (the routes are read routes, not TrustedWrite).
			for name, outcome := range map[string]func(string) int{
				"anonymous": func(path string) int { return companionTestRequest(d, nil, "GET", path, "").Code },
				"session":   func(path string) int { return companionTestRequest(d, cookie, "GET", path, "").Code },
				"lan":       func(path string) int { return lanRequest("GET", path).Code },
			} {
				if got, want := outcome(tc.route), outcome(companionPacksRoute); got != want {
					t.Errorf("%s: %d, catalog route answers %d", name, got, want)
				}
			}
			if code := companionTestRequest(d, nil, "GET", tc.route, "").Code; code != http.StatusUnauthorized {
				t.Errorf("anonymous read: %d", code)
			}
			if code := lanRequest("GET", tc.route).Code; code != http.StatusOK {
				t.Errorf("LAN read: %d", code)
			}
		})
	}
}

// ---- a small JSON Schema evaluator for the keywords the kit's schemas use ----

type companionSchemaEval struct {
	t    testing.TB
	root map[string]any
}

func (e companionSchemaEval) valid(node, value any) bool {
	e.t.Helper()
	switch n := node.(type) {
	case bool:
		return n
	case map[string]any:
		return e.validObject(n, value)
	}
	e.t.Fatalf("unsupported schema node %T", node)
	return false
}

func (e companionSchemaEval) validObject(node map[string]any, value any) bool {
	e.t.Helper()
	keys := make([]string, 0, len(node))
	for key := range node {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		arg := node[key]
		ok := true
		switch key {
		case "$schema", "$id", "$comment", "$defs", "title", "description", "format", "default", "examples":
		case "$ref":
			ref, _ := arg.(string)
			name, found := strings.CutPrefix(ref, "#/$defs/")
			defs, _ := e.root["$defs"].(map[string]any)
			target, exists := defs[name]
			if !found || !exists {
				e.t.Fatalf("unresolved $ref %q", ref)
			}
			ok = e.valid(target, value)
		case "type":
			types, isList := arg.([]any)
			if !isList {
				types = []any{arg}
			}
			ok = false
			for _, name := range types {
				ok = ok || companionSchemaType(e.t, name.(string), value)
			}
		case "const":
			ok = reflect.DeepEqual(arg, value)
		case "enum":
			ok = false
			for _, option := range arg.([]any) {
				ok = ok || reflect.DeepEqual(option, value)
			}
		case "pattern":
			if s, isString := value.(string); isString {
				ok = regexp.MustCompile(arg.(string)).MatchString(s)
			}
		case "minLength":
			if s, isString := value.(string); isString {
				ok = float64(utf8.RuneCountInString(s)) >= arg.(float64)
			}
		case "maxLength":
			if s, isString := value.(string); isString {
				ok = float64(utf8.RuneCountInString(s)) <= arg.(float64)
			}
		case "minimum":
			if n, isNumber := value.(float64); isNumber {
				ok = n >= arg.(float64)
			}
		case "maximum":
			if n, isNumber := value.(float64); isNumber {
				ok = n <= arg.(float64)
			}
		case "minItems":
			if list, isList := value.([]any); isList {
				ok = float64(len(list)) >= arg.(float64)
			}
		case "maxItems":
			if list, isList := value.([]any); isList {
				ok = float64(len(list)) <= arg.(float64)
			}
		case "minProperties":
			if object, isObject := value.(map[string]any); isObject {
				ok = float64(len(object)) >= arg.(float64)
			}
		case "maxProperties":
			if object, isObject := value.(map[string]any); isObject {
				ok = float64(len(object)) <= arg.(float64)
			}
		case "propertyNames":
			if object, isObject := value.(map[string]any); isObject {
				for name := range object {
					ok = ok && e.valid(arg, name)
				}
			}
		case "prefixItems":
			if list, isList := value.([]any); isList {
				for i, sub := range arg.([]any) {
					if i < len(list) && !e.valid(sub, list[i]) {
						ok = false
					}
				}
			}
		case "items":
			if list, isList := value.([]any); isList {
				skip := 0
				if prefix, has := node["prefixItems"].([]any); has {
					skip = len(prefix)
				}
				for i := skip; i < len(list); i++ {
					ok = ok && e.valid(arg, list[i])
				}
			}
		case "required":
			if object, isObject := value.(map[string]any); isObject {
				for _, name := range arg.([]any) {
					if _, present := object[name.(string)]; !present {
						ok = false
					}
				}
			}
		case "properties":
			if object, isObject := value.(map[string]any); isObject {
				for name, sub := range arg.(map[string]any) {
					if v, present := object[name]; present && !e.valid(sub, v) {
						ok = false
					}
				}
			}
		case "additionalProperties":
			if object, isObject := value.(map[string]any); isObject {
				known, _ := node["properties"].(map[string]any)
				for name, v := range object {
					if _, listed := known[name]; !listed && !e.valid(arg, v) {
						ok = false
					}
				}
			}
		case "allOf":
			for _, sub := range arg.([]any) {
				ok = ok && e.valid(sub, value)
			}
		case "anyOf":
			ok = false
			for _, sub := range arg.([]any) {
				ok = ok || e.valid(sub, value)
			}
		case "oneOf":
			matches := 0
			for _, sub := range arg.([]any) {
				if e.valid(sub, value) {
					matches++
				}
			}
			ok = matches == 1
		case "not":
			ok = !e.valid(arg, value)
		default:
			e.t.Fatalf("the test evaluator does not support the schema keyword %q; extend it", key)
		}
		if !ok {
			return false
		}
	}
	return true
}

func companionSchemaType(t testing.TB, name string, value any) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := value.(float64)
		return ok
	case "integer":
		n, ok := value.(float64)
		return ok && n == math.Trunc(n)
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	}
	t.Fatalf("unsupported schema type %q", name)
	return false
}

func (e companionSchemaEval) def(name string) any {
	return e.root["$defs"].(map[string]any)[name]
}

func companionSchemaRequired(node any) map[string]bool {
	out := map[string]bool{}
	if m, ok := node.(map[string]any); ok {
		for _, name := range m["required"].([]any) {
			out[name.(string)] = true
		}
	}
	return out
}

// ---- the guide's examples ----

var companionExampleBlock = regexp.MustCompile("(?s)```json example:([a-z-]+)\n(.*?)\n```")

func companionGuideExamples(t testing.TB) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, m := range companionExampleBlock.FindAllStringSubmatch(companionAuthoringDoc(t, "guide.md"), -1) {
		if _, dup := out[m[1]]; dup {
			t.Fatalf("example %s appears twice", m[1])
		}
		out[m[1]] = []byte(m[2])
	}
	for _, name := range []string{"pack-minimal", "pack-full", "pack-kit", "marketplace", "marketplace-external-entry"} {
		if out[name] == nil {
			t.Fatalf("the guide has no example:%s block", name)
		}
	}
	return out
}

func companionDecode(t testing.TB, raw []byte) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%v in %s", err, raw)
	}
	return doc
}

func companionEncode(t testing.TB, doc any) []byte {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Drift (a): the guide's examples pass the real install pipeline.
func TestCompanionAuthoringExamplesPassPipeline(t *testing.T) {
	examples := companionGuideExamples(t)
	market := companionDecode(t, examples["marketplace"])
	external := companionDecode(t, examples["marketplace-external-entry"])
	if entry := parseCompanionMarketplaceEntry(examples["marketplace-external-entry"]); entry.Err != nil || entry.URL == "" || entry.SHA256 == "" || entry.Path != "" {
		t.Fatalf("external entry example: %+v", entry)
	}
	listed := market["packs"].([]any)
	if len(listed) != 1 {
		t.Fatalf("the marketplace example lists %d packs", len(listed))
	}
	path := listed[0].(map[string]any)["path"].(string)
	id := listed[0].(map[string]any)["id"].(string)

	for _, packExample := range []string{"pack-minimal", "pack-full", "pack-kit"} {
		t.Run(packExample, func(t *testing.T) {
			raw := examples[packExample]
			if packExample == "pack-kit" {
				// The kit example takes its own pack ID, so it is installed
				// under that ID rather than the marketplace example's.
				if _, _, err := validateCompanionManifest(raw, "fixture-fox"); err != nil {
					t.Fatalf("the kit example under its own ID: %v", err)
				}
				doc := companionDecode(t, raw)
				doc["id"] = id
				raw = companionEncode(t, doc)
				if issues := lintCompanionKit(raw); len(issues) != 0 {
					t.Fatalf("the kit example has advisory findings: %q", issues)
				}
			}
			if got := companionDecode(t, raw)["id"]; got != id {
				t.Fatalf("pack id %v differs from the marketplace example id %s", got, id)
			}
			_, files, err := validateCompanionManifest(raw, id)
			if err != nil || len(files) == 0 {
				t.Fatalf("validate: %v, files %v", err, files)
			}
			entries := []companionZipEntry{
				{name: "marketplace.json", data: examples["marketplace"]},
				{name: path + "/pack.json", data: raw},
			}
			for _, name := range files {
				data := companionTestPNG(t, 8, 8)
				if strings.HasSuffix(name, ".webp") {
					data = companionTestWebP(8, 8)
				}
				entries = append(entries, companionZipEntry{name: path + "/" + name, data: data})
			}
			// The same marketplace also lists the external example; a dry
			// run lists it without downloading it.
			withExternal := companionDecode(t, examples["marketplace"])
			withExternal["packs"] = append(withExternal["packs"].([]any), external)
			for name, marketplace := range map[string][]byte{
				"self-contained": examples["marketplace"],
				"with external":  companionEncode(t, withExternal),
			} {
				entries[0].data = marketplace
				result, err := companionTestRun(&CompanionPackStore{}, t.TempDir(), companionTestZip(t, entries...), nil, true)
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				if len(result.Listing.Errors) != 0 {
					t.Fatalf("%s: listing errors %+v", name, result.Listing.Errors)
				}
				for _, pack := range result.Listing.Packs {
					if pack.Error != nil || pack.Status != companionStatusAvailable {
						t.Errorf("%s: pack %s: %+v", name, pack.ID, pack)
					}
				}
				if got := companionListed(result, id); got == nil || got.Rev == "" {
					t.Errorf("%s: pack %s was not read: %+v", name, id, got)
				}
				if name == "with external" && companionListed(result, "sample-b") == nil {
					t.Errorf("%s: the external entry is not listed", name)
				}
			}
		})
	}
}

// The examples also satisfy the schemas the guide ships.
func TestCompanionAuthoringExamplesMatchSchemas(t *testing.T) {
	examples := companionGuideExamples(t)
	pack := companionSchemaEval{t, companionAuthoringSchema(t, "pack.schema.json")}
	market := companionSchemaEval{t, companionAuthoringSchema(t, "marketplace.schema.json")}
	for _, name := range []string{"pack-minimal", "pack-full", "pack-kit"} {
		if !pack.valid(pack.root, companionDecode(t, examples[name])) {
			t.Errorf("%s does not match pack.schema.json", name)
		}
	}
	if !market.valid(market.root, companionDecode(t, examples["marketplace"])) {
		t.Error("the marketplace example does not match marketplace.schema.json")
	}
	if !market.valid(market.def("entry"), companionDecode(t, examples["marketplace-external-entry"])) {
		t.Error("the external entry example does not match marketplace.schema.json")
	}
}

// ---- Drift (b): the schemas against the validators, in both directions ----

// companionOverlay returns base with each key of patch applied; a null patch
// value deletes the key.
func companionOverlay(base map[string]any, patch map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for key, value := range base {
		out[key] = value
	}
	for key, value := range patch {
		if value == nil {
			delete(out, key)
		} else {
			out[key] = value
		}
	}
	return out
}

func companionParse(t testing.TB, text string) map[string]any {
	t.Helper()
	return companionDecode(t, []byte(text))
}

func TestCompanionAuthoringPackSchemaMatchesValidator(t *testing.T) {
	examples := companionGuideExamples(t)
	schema := companionSchemaEval{t, companionAuthoringSchema(t, "pack.schema.json")}
	required := companionSchemaRequired(schema.root)
	minimal := companionDecode(t, examples["pack-minimal"])

	agree := func(name string, doc map[string]any) (schemaOK, validatorOK bool) {
		t.Helper()
		id, _ := doc["id"].(string)
		_, _, err := validateCompanionManifest(companionEncode(t, doc), id)
		schemaOK, validatorOK = schema.valid(schema.root, doc), err == nil
		if schemaOK != validatorOK {
			t.Errorf("%s: schema accepts %v, validator accepts %v (%v)", name, schemaOK, validatorOK, err)
		}
		return
	}

	// Removing a top-level key of an example fails exactly when the schema
	// lists it as required, and the schema agrees with the validator.
	for _, example := range []string{"pack-minimal", "pack-full"} {
		doc := companionDecode(t, examples[example])
		for key := range doc {
			_, validatorOK := agree(example+" without "+key, companionOverlay(doc, map[string]any{key: nil}))
			if validatorOK == required[key] {
				t.Errorf("%s without %s: validator accepts %v but the schema lists it required: %v", example, key, validatorOK, required[key])
			}
		}
	}
	// The minimal example holds every required key, and no listed
	// required key is one the validator ignores.
	for key := range required {
		if _, present := minimal[key]; !present {
			t.Errorf("the minimal example lacks required key %s", key)
		}
	}
	if len(required) != len(minimal) {
		t.Errorf("the minimal example holds %d keys, the schema requires %d", len(minimal), len(required))
	}

	stand := `{"layers":[{"file":"a.png"}]}`
	for _, tc := range []struct {
		name  string
		patch string
	}{
		{"unknown top-level field", `{"future":{"x":1}}`},
		{"wrong schema value", `{"schema":"ws-companion/v2"}`},
		{"blank name", `{"name":"  "}`},
		{"canvas of one", `{"canvas":[512]}`},
		{"canvas of three", `{"canvas":[512,512,512]}`},
		{"canvas below 64", `{"canvas":[63,512]}`},
		{"canvas at 64", `{"canvas":[64,64]}`},
		{"canvas above 2048", `{"canvas":[512,2049]}`},
		{"canvas at 2048", `{"canvas":[2048,2048]}`},
		{"fractional canvas", `{"canvas":[512,512.5]}`},
		{"uppercase id", `{"id":"Sample"}`},
		{"long id", `{"id":"` + strings.Repeat("a", 65) + `"}`},
		{"no stand pose", `{"poses":{"typing":` + stand + `}}`},
		{"empty poses", `{"poses":{}}`},
		{"stand without layers", `{"poses":{"stand":{}}}`},
		{"stand with empty layers", `{"poses":{"stand":{"layers":[]}}}`},
		{"extra pose", `{"poses":{"stand":` + stand + `,"typing":{"fallback":"stand","layers":[{"file":"b.webp"}]}}}`},
		{"non-array replaces", `{"gestures":{"g":{"replaces":"x"}}}`},
		{"non-string fallback", `{"poses":{"stand":{"fallback":5,"layers":[{"file":"a.png"}]}}}`},
		{"layer without a raster", `{"poses":{"stand":{"layers":[{}]}}}`},
		{"layer with only z", `{"poses":{"stand":{"layers":[{"z":3}]}}}`},
		{"unknown layer mode", `{"poses":{"stand":{"layers":[{"file":"a.png","mode":"bogus"}]}}}`},
		{"known layer mode", `{"poses":{"stand":{"layers":[{"file":"a.png","mode":"static"}]}}}`},
		{"svg raster", `{"poses":{"stand":{"layers":[{"file":"a.svg"}]}}}`},
		{"raster in a folder", `{"poses":{"stand":{"layers":[{"file":"dir/a.png"}]}}}`},
		{"raster with ..", `{"poses":{"stand":{"layers":[{"file":"a..b.png"}]}}}`},
		{"hidden raster", `{"poses":{"stand":{"layers":[{"file":".a.png"}]}}}`},
		{"tinted channels", `{"poses":{"stand":{"layers":[{"line":"l.png","jacketFill":"j.png"}]}}}`},
		{"frames with fps", `{"poses":{"stand":{"layers":[{"frames":["a.png","b.png"],"fps":4}]}}}`},
		{"frames without fps", `{"poses":{"stand":{"layers":[{"frames":["a.png"]}]}}}`},
		{"empty frames", `{"poses":{"stand":{"layers":[{"frames":[],"fps":4}]}}}`},
		{"fps too high", `{"poses":{"stand":{"layers":[{"frames":["a.png"],"fps":31}]}}}`},
		{"fps too low", `{"poses":{"stand":{"layers":[{"frames":["a.png"],"fps":0.05}]}}}`},
		{"frame objects", `{"poses":{"stand":{"layers":[{"frames":[{"line":"a.png"},{"file":"b.png"}],"fps":2}]}}}`},
		{"empty frame object", `{"poses":{"stand":{"layers":[{"frames":[{}],"fps":2}]}}}`},
		{"expression decal", `{"expressions":{"happy":{"decal":"f.png"}}}`},
		{"svg decal", `{"expressions":{"happy":{"decal":"f.svg"}}}`},
		{"head mouths", `{"head":{"mouths":{"o":"m.png"},"eyes":{"sclera":"s.webp"}}}`},
		{"bad eye raster", `{"head":{"eyes":{"sclera":"s.exe"}}}`},
		{"hairs", `{"hairs":{"h":{"back":{"line":"a.png"},"front":{"hairFill":"b.png"}}}}`},
		{"prop without layers", `{"props":{"p":{}}}`},
		{"prop with an empty layer", `{"props":{"p":{"layers":[{}]}}}`},
		{"prop file", `{"props":{"p":{"file":"p.png","z":60}}}`},
		{"motion map of numbers", `{"motions":{"m":5}}`},
		{"autoMatch tags", `{"autoMatch":{"harnesses":["a"],"models":["b"]}}`},
		{"33 autoMatch tags", `{"autoMatch":{"models":[` + strings.TrimSuffix(strings.Repeat(`"a",`, 33), ",") + `]}}`},
		{"32 autoMatch tags", `{"autoMatch":{"models":[` + strings.TrimSuffix(strings.Repeat(`"a",`, 32), ",") + `]}}`},
		{"non-ASCII autoMatch tag", `{"autoMatch":{"models":["é"]}}`},
		{"blank autoMatch tag", `{"autoMatch":{"models":[" "]}}`},
		{"long autoMatch tag", `{"autoMatch":{"models":["` + strings.Repeat("a", 129) + `"]}}`},
		{"scenario", `{"scenarios":{"s":{"on":"a.b","steps":[{"ms":10,"pose":"stand"}],"cooldownMs":5}}}`},
		{"scenario with events", `{"scenarios":{"s":{"on":["a.b","c.d"],"when":{"streak":3,"hourRange":[22,24]}}}}`},
		{"scenario empty on", `{"scenarios":{"s":{"on":[]}}}`},
		{"scenario unknown guard", `{"scenarios":{"s":{"when":{"bogus":1}}}}`},
		{"scenario empty steps", `{"scenarios":{"s":{"steps":[]}}}`},
		{"scenario step without ms", `{"scenarios":{"s":{"steps":[{}]}}}`},
		{"scenario negative cooldown", `{"scenarios":{"s":{"cooldownMs":-1}}}`},
		{"hour range past 24", `{"scenarios":{"s":{"when":{"hourRange":[0,25]}}}}`},
		{"hour range start 24", `{"scenarios":{"s":{"when":{"hourRange":[24,24]}}}}`},
		{"non-object preset", `{"presets":{"p":3}}`},
		// The paper-doll sections and fills.
		{"fills on a pose layer", `{"poses":{"stand":{"layers":[{"line":"a.png","fills":{"trim":"t.png"}}]}}}`},
		{"fills-only pose layer", `{"poses":{"stand":{"layers":[{"fills":{"skin":"s.png"}}]}}}`},
		{"fills-only pose frame", `{"poses":{"stand":{"layers":[{"frames":[{"fills":{"skin":"s.png"}}],"fps":2}]}}}`},
		{"svg fill", `{"poses":{"stand":{"layers":[{"line":"a.png","fills":{"trim":"t.svg"}}]}}}`},
		{"fills on a hair part", `{"hairs":{"h":{"front":{"hairFill":"b.png","fills":{"tip":"c.png"}}}}}`},
		{"fills-only outfit layer", `{"outfits":{"o":{"poses":{"stand":{"layers":[{"fills":{"skin":"s.png"}}]}}}}}`},
		{"fills-only outfit frame", `{"outfits":{"o":{"gestures":{"wave":{"layers":[{"frames":[{"fills":{"skin":"s.png"}}],"fps":2}]}}}}}`},
		{"empty outfit layer", `{"outfits":{"o":{"poses":{"stand":{"layers":[{}]}}}}}`},
		{"outfit column layers not a list", `{"outfits":{"o":{"poses":{"stand":{"layers":3}}}}}`},
		{"non-object outfit", `{"outfits":{"o":5}}`},
		{"non-object outfits", `{"outfits":5}`},
		{"attachment", `{"attachments":{"cap":{"slot":"headdress","bind":"head","layers":[{"fills":{"c":"c.png"}}],"columns":{"typing":{"layers":[{"file":"t.png"}]}}}}}`},
		{"attachment with an empty layer", `{"attachments":{"cap":{"layers":[{}]}}}`},
		{"attachment column with an empty layer", `{"attachments":{"tail":{"columns":{"typing":{"layers":[{}]}}}}}`},
		{"null attachment", `{"attachments":{"cap":null}}`},
		{"eye set", `{"eyesets":{"round":{"sclera":"r.png","irisFill":"i.webp"}}}`},
		{"bad eye-set raster", `{"eyesets":{"round":{"sclera":"r.exe"}}}`},
		{"null eye set", `{"eyesets":{"round":null}}`},
		{"persona voice_by_lang", `{"persona":{"name":"Kit","voice":"Dry.","voice_by_lang":{"ko":"해요체로 짧게.","pt-BR":"Leve e direto."}}}`},
	} {
		doc := companionOverlay(minimal, companionParse(t, tc.patch))
		agree(tc.name, doc)
	}
	// The schema flags voice_by_lang entries the validator drops alone (the
	// pack still loads, so these are lint, not rejections).
	var many strings.Builder
	for i := 0; i <= companionVoiceByLangMaxEntries; i++ {
		fmt.Fprintf(&many, `%s"x%c%c":"v"`, map[bool]string{true: "", false: ","}[i == 0], 'a'+i/26, 'a'+i%26)
	}
	for name, patch := range map[string]string{
		"bad voice_by_lang tag":        `{"persona":{"voice_by_lang":{"not a tag":"v"}}}`,
		"blank voice_by_lang overlay":  `{"persona":{"voice_by_lang":{"ko":""}}}`,
		"long voice_by_lang overlay":   `{"persona":{"voice_by_lang":{"ko":"` + strings.Repeat("a", companionPersonaVoiceMax+1) + `"}}}`,
		"too many voice_by_lang tags":  `{"persona":{"voice_by_lang":{` + many.String() + `}}}`,
		"non-string voice_by_lang tag": `{"persona":{"voice_by_lang":{"ko":5}}}`,
	} {
		doc := companionOverlay(minimal, companionParse(t, patch))
		if schema.valid(schema.root, doc) {
			t.Errorf("%s: the schema accepts it", name)
		}
		id, _ := doc["id"].(string)
		if _, _, err := validateCompanionManifest(companionEncode(t, doc), id); err != nil {
			t.Errorf("%s: the validator rejects the pack instead of dropping the entry: %v", name, err)
		}
	}
	// The paper-doll fixture, every section at once, matches the schema.
	if !schema.valid(schema.root, companionDecode(t, []byte(companionKitFixture))) {
		t.Error("the paper-doll fixture does not match pack.schema.json")
	}
}

func TestCompanionAuthoringMarketplaceSchemaMatchesParser(t *testing.T) {
	examples := companionGuideExamples(t)
	schema := companionSchemaEval{t, companionAuthoringSchema(t, "marketplace.schema.json")}
	required := companionSchemaRequired(schema.root)
	entryDef := schema.def("entry")
	entryRequired := companionSchemaRequired(entryDef)
	market := companionDecode(t, examples["marketplace"])
	validEntry := market["packs"].([]any)[0].(map[string]any)
	external := companionDecode(t, examples["marketplace-external-entry"])
	sha := external["sha256"].(string)

	topAgree := func(name string, doc map[string]any) bool {
		t.Helper()
		_, err := parseCompanionMarketplace(companionEncode(t, doc))
		schemaOK, parserOK := schema.valid(schema.root, doc), err == nil
		if schemaOK != parserOK {
			t.Errorf("%s: schema accepts %v, parser accepts %v (%v)", name, schemaOK, parserOK, err)
		}
		return parserOK
	}
	entryAgree := func(name string, doc map[string]any) bool {
		t.Helper()
		entry := parseCompanionMarketplaceEntry(companionEncode(t, doc))
		schemaOK, parserOK := schema.valid(entryDef, doc), entry.Err == nil
		if schemaOK != parserOK {
			t.Errorf("%s: schema accepts %v, parser accepts %v (%v)", name, schemaOK, parserOK, entry.Err)
		}
		return parserOK
	}

	// Top level: removing a key fails exactly when the schema requires it.
	for key := range market {
		if parserOK := topAgree("marketplace without "+key, companionOverlay(market, map[string]any{key: nil})); parserOK == required[key] {
			t.Errorf("marketplace without %s: parser accepts %v but the schema lists it required: %v", key, parserOK, required[key])
		}
	}
	for key := range required {
		if _, present := market[key]; !present {
			t.Errorf("the marketplace example lacks required key %s", key)
		}
	}
	// Entries: the same for every key of both examples. path, url and sha256
	// are the one-of group; their removal is judged by the parser alone.
	oneOf := map[string]bool{"path": true, "url": true, "sha256": true}
	for _, entry := range []map[string]any{validEntry, external} {
		for key := range entry {
			parserOK := entryAgree(fmt.Sprintf("%v without %s", entry["id"], key), companionOverlay(entry, map[string]any{key: nil}))
			if !oneOf[key] && parserOK == entryRequired[key] {
				t.Errorf("%v without %s: parser accepts %v but the schema lists it required: %v", entry["id"], key, parserOK, entryRequired[key])
			}
		}
	}
	for key := range entryRequired {
		if _, present := validEntry[key]; !present {
			t.Errorf("the entry example lacks required key %s", key)
		}
	}
	if oneOfBranches := entryDef.(map[string]any)["oneOf"].([]any); len(oneOfBranches) != 2 {
		t.Fatalf("the entry one-of has %d branches", len(oneOfBranches))
	}

	base := map[string]any{"id": "sample-x", "name": "Sample X", "version": "1"}
	for _, tc := range []struct {
		name  string
		patch string
	}{
		{"path", `{"path":"packs/x"}`},
		{"path with a dot-leading name", `{"path":".hidden/x"}`},
		{"url and sha256", `{"url":"https://example.invalid/x.zip","sha256":"` + sha + `"}`},
		{"both path and url", `{"path":"packs/x","url":"https://example.invalid/x.zip","sha256":"` + sha + `"}`},
		{"neither path nor url", `{}`},
		{"url without sha256", `{"url":"https://example.invalid/x.zip"}`},
		{"sha256 without url", `{"sha256":"` + sha + `"}`},
		{"path with sha256", `{"path":"packs/x","sha256":"` + sha + `"}`},
		{"path and url without sha256", `{"path":"packs/x","url":"https://example.invalid/x.zip"}`},
		{"uppercase sha256", `{"url":"https://example.invalid/x.zip","sha256":"` + strings.ToUpper(sha) + `"}`},
		{"short sha256", `{"url":"https://example.invalid/x.zip","sha256":"` + sha[:63] + `"}`},
		{"http url", `{"url":"http://example.invalid/x.zip","sha256":"` + sha + `"}`},
		{"url with credentials", `{"url":"https://user:pw@example.invalid/x.zip","sha256":"` + sha + `"}`},
		{"url with a space", `{"url":"https://example.invalid/a b.zip","sha256":"` + sha + `"}`},
		{"url without a host", `{"url":"https:///x.zip","sha256":"` + sha + `"}`},
		{"long url", `{"url":"https://example.invalid/` + strings.Repeat("a", companionURLMax) + `","sha256":"` + sha + `"}`},
		{"url of 2048", `{"url":"https://example.invalid/` + strings.Repeat("a", companionURLMax-len("https://example.invalid/")) + `","sha256":"` + sha + `"}`},
		{"absolute path", `{"path":"/packs/x"}`},
		{"dot path", `{"path":"."}`},
		{"parent path", `{"path":"../x"}`},
		{"middle parent path", `{"path":"packs/../x"}`},
		{"backslash path", `{"path":"packs\\x"}`},
		{"drive path", `{"path":"C:/x"}`},
		{"unclean path", `{"path":"packs//x"}`},
		{"trailing slash path", `{"path":"packs/x/"}`},
		{"empty path", `{"path":""}`},
		{"long path", `{"path":"` + strings.Repeat("a", companionArchivePathMax+1) + `"}`},
		{"default id", `{"id":"default","path":"p"}`},
		{"auto id", `{"id":"auto","path":"p"}`},
		{"device id", `{"id":"con","path":"p"}`},
		{"numbered device id", `{"id":"com1","path":"p"}`},
		{"id that only starts like a device", `{"id":"com10","path":"p"}`},
		{"uppercase id", `{"id":"Sample","path":"p"}`},
		{"underscore id", `{"id":"a_b-c","path":"p"}`},
		{"leading dash id", `{"id":"-a","path":"p"}`},
		{"empty id", `{"id":"","path":"p"}`},
		{"blank name", `{"name":" ","path":"p"}`},
		{"64 character name", `{"name":"` + strings.Repeat("n", 64) + `","path":"p"}`},
		{"65 character name", `{"name":"` + strings.Repeat("n", 65) + `","path":"p"}`},
		{"multibyte name of 64", `{"name":"` + strings.Repeat("é", 64) + `","path":"p"}`},
		{"32 character version", `{"version":"` + strings.Repeat("1", 32) + `","path":"p"}`},
		{"33 character version", `{"version":"` + strings.Repeat("1", 33) + `","path":"p"}`},
		{"blank version", `{"version":"","path":"p"}`},
		{"65 character author", `{"author":"` + strings.Repeat("a", 65) + `","path":"p"}`},
		{"65 character license", `{"license":"` + strings.Repeat("a", 65) + `","path":"p"}`},
		{"280 character description", `{"description":"` + strings.Repeat("d", 280) + `","path":"p"}`},
		{"281 character description", `{"description":"` + strings.Repeat("d", 281) + `","path":"p"}`},
		{"control character in name", `{"name":"a\u0001b","path":"p"}`},
		{"newline in description", `{"description":"a\nb","path":"p"}`},
		{"unknown field", `{"path":"p","future":1}`},
		{"non-string version", `{"version":1,"path":"p"}`},
		{"format 2d", `{"path":"p","format":"2d"}`},
		{"format vrm", `{"path":"p","format":"vrm"}`},
		{"format invalid", `{"path":"p","format":"3d"}`},
		{"format empty", `{"path":"p","format":""}`},
		{"vroidSourceUrl https", `{"path":"p","vroidSourceUrl":"https://example.invalid/model"}`},
		{"vroidSourceUrl http", `{"path":"p","vroidSourceUrl":"http://example.invalid/model"}`},
		{"vroidSourceUrl empty", `{"path":"p","vroidSourceUrl":""}`},
	} {
		entryAgree("entry: "+tc.name, companionOverlay(base, companionParse(t, tc.patch)))
	}

	// Top level values.
	for _, tc := range []struct {
		name  string
		patch string
	}{
		{"wrong schema value", `{"schema":"ws-companion-marketplace/v2"}`},
		{"blank name", `{"name":" "}`},
		{"65 character name", `{"name":"` + strings.Repeat("n", 65) + `"}`},
		{"control character in name", `{"name":"a\u0007"}`},
		{"empty packs", `{"packs":[]}`},
		{"non-array packs", `{"packs":{}}`},
		{"owner with name and url", `{"owner":{"name":"A","url":"https://example.invalid/"}}`},
		{"owner with http url", `{"owner":{"url":"http://example.invalid/"}}`},
		{"owner with a long name", `{"owner":{"name":"` + strings.Repeat("o", 65) + `"}}`},
		{"unknown field", `{"future":{"ignored":true}}`},
	} {
		topAgree("marketplace: "+tc.name, companionOverlay(market, companionParse(t, tc.patch)))
	}
	for _, count := range []int{companionMaxPacks, companionMaxPacks + 1} {
		var packs []any
		for i := 0; i < count; i++ {
			packs = append(packs, companionOverlay(validEntry, map[string]any{"id": fmt.Sprintf("p%d", i)}))
		}
		topAgree(fmt.Sprintf("marketplace: %d packs", count), companionOverlay(market, map[string]any{"packs": packs}))
	}
}

// The schemas' patterns and bounds are the validators' own.
func TestCompanionAuthoringSchemaConstantsMatchValidators(t *testing.T) {
	pack := companionAuthoringSchema(t, "pack.schema.json")
	market := companionAuthoringSchema(t, "marketplace.schema.json")
	packDefs, marketDefs := pack["$defs"].(map[string]any), market["$defs"].(map[string]any)
	for _, tc := range []struct {
		name, got, want string
	}{
		{"pack id", packDefs["packId"].(map[string]any)["pattern"].(string), companionPackID.String()},
		{"raster", packDefs["raster"].(map[string]any)["pattern"].(string), companionRaster.String()},
		{"match tag", packDefs["matchTags"].(map[string]any)["items"].(map[string]any)["allOf"].([]any)[0].(map[string]any)["pattern"].(string), companionMatchTag.String()},
		{"language tag", pack["properties"].(map[string]any)["persona"].(map[string]any)["properties"].(map[string]any)["quipLanguage"].(map[string]any)["pattern"].(string), companionLanguageTag.String()},
		{"voice_by_lang key", pack["properties"].(map[string]any)["persona"].(map[string]any)["properties"].(map[string]any)["voice_by_lang"].(map[string]any)["propertyNames"].(map[string]any)["pattern"].(string), companionLanguageTag.String()},
		{"entry id", marketDefs["entry"].(map[string]any)["properties"].(map[string]any)["id"].(map[string]any)["pattern"].(string), companionPackID.String()},
		{"sha256", marketDefs["entry"].(map[string]any)["properties"].(map[string]any)["sha256"].(map[string]any)["pattern"].(string), companionSHA256.String()},
	} {
		if tc.got != tc.want {
			t.Errorf("%s pattern %q differs from the validator's %q", tc.name, tc.got, tc.want)
		}
	}
	packProps := pack["properties"].(map[string]any)
	persona := packProps["persona"].(map[string]any)["properties"].(map[string]any)
	canvasItems := packProps["canvas"].(map[string]any)["items"].(map[string]any)
	entryProps := marketDefs["entry"].(map[string]any)["properties"].(map[string]any)
	voiceByLang := persona["voice_by_lang"].(map[string]any)
	for name, pair := range map[string][2]any{
		"persona name":          {persona["name"].(map[string]any)["maxLength"], float64(companionPersonaNameMax)},
		"persona voice":         {persona["voice"].(map[string]any)["maxLength"], float64(companionPersonaVoiceMax)},
		"voice_by_lang entries": {voiceByLang["maxProperties"], float64(companionVoiceByLangMaxEntries)},
		"voice_by_lang overlay": {voiceByLang["additionalProperties"].(map[string]any)["maxLength"], float64(companionPersonaVoiceMax)},
		"url length":            {marketDefs["httpsUrl"].(map[string]any)["maxLength"], float64(companionURLMax)},
		"path length":           {entryProps["path"].(map[string]any)["maxLength"], float64(companionArchivePathMax)},
		"packs per market":      {market["properties"].(map[string]any)["packs"].(map[string]any)["maxItems"], float64(companionMaxPacks)},
		"canvas minimum":        {canvasItems["minimum"], float64(64)},
		"canvas maximum":        {canvasItems["maximum"], float64(2048)},
	} {
		if pair[0] != pair[1] {
			t.Errorf("%s: schema %v, validator %v", name, pair[0], pair[1])
		}
	}
}

// ---- Drift (c): the kit stands alone ----

func TestCompanionAuthoringKitStandsAlone(t *testing.T) {
	forbidden := map[string]*regexp.Regexp{
		"ticket stem":       regexp.MustCompile(`\d{6}-(feat|bug|refactor|chore|research|epic)-`),
		"ticket path":       regexp.MustCompile(`ai-docs/|tickets/`),
		"repository layout": regexp.MustCompile(`agents-plugin|claude-plugin|internal/|wsdashboard|wsconfig|devenv|install\.sh|\.go\b`),
		"Go identifier":     regexp.MustCompile(`\b(companion|Companion)[A-Z]\w*|\bvalidate[A-Z]\w*|\bparse[A-Z]\w*|\bload[A-Z]\w*`),
		"commit hash":       regexp.MustCompile(`\b[0-9a-f]{40}\b`),
	}
	for _, file := range []string{"guide.md", "pack.schema.json", "marketplace.schema.json"} {
		text := companionAuthoringDoc(t, file)
		for name, pattern := range forbidden {
			if loc := pattern.FindStringIndex(text); loc != nil {
				t.Errorf("%s holds a %s: %q", file, name, text[loc[0]:loc[1]])
			}
		}
	}
	// Every URL in the kit is an example.invalid address or a schema
	// vocabulary link.
	for _, file := range []string{"guide.md", "pack.schema.json", "marketplace.schema.json"} {
		var kept []string
		for _, line := range strings.Split(companionAuthoringDoc(t, file), "\n") {
			if !strings.Contains(line, `"pattern":`) { // a regular expression is not a link
				kept = append(kept, line)
			}
		}
		for _, link := range regexp.MustCompile(`https?://[^\s"<>)]+`).FindAllString(strings.Join(kept, "\n"), -1) {
			if !strings.HasPrefix(link, "https://example.invalid") && !strings.HasPrefix(link, "https://json-schema.org/draft/2020-12/") {
				t.Errorf("%s: URL %s is not an example.invalid address", file, link)
			}
		}
	}
}

// ---- Drift (d): every cap the guide states is the code's ----

func companionGuideSize(n int) string {
	switch {
	case n%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", n>>20)
	case n%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", n>>10)
	}
	return fmt.Sprintf("%d bytes", n)
}

func TestCompanionAuthoringGuideCaps(t *testing.T) {
	// The first group is computed from the pipeline's constants. The second
	// group has no constant of its own: the entry-text, pack ID and raster
	// name caps are pinned below through the parsers, the canvas and
	// autoMatch bounds through the schema test.
	want := map[string]string{
		"`marketplace.json` size":          companionGuideSize(companionMarketplaceCap),
		"Packs listed in one marketplace":  fmt.Sprint(companionMaxPacks),
		"Installed local packs":            fmt.Sprint(companionMaxPacks),
		"Entries in one zip":               fmt.Sprint(companionZipEntryMax),
		"Files in one pack directory":      fmt.Sprint(companionPackEntryMax),
		"Compression ratio of one entry":   fmt.Sprintf("%d:1", companionZipRatioMax),
		"Unpacked bytes in one install":    companionGuideSize(companionOperationCap),
		"`pack.json` size":                 companionGuideSize(companionManifestCap),
		"One raster file":                  companionGuideSize(companionAssetCap),
		"One pack directory, total":        companionGuideSize(companionPackCap),
		"All installed local packs, total": companionGuideSize(companionCacheCap),
		"Raster width or height":           fmt.Sprintf("%d px", companionImageSideMax),
		"URL length":                       fmt.Sprintf("%d characters", companionURLMax),
		"Archive path length":              fmt.Sprintf("%d characters", companionArchivePathMax),
		"Persona name":                     fmt.Sprintf("%d characters", companionPersonaNameMax),
		"Persona voice":                    fmt.Sprintf("%d characters", companionPersonaVoiceMax),
		"Persona `voice_by_lang` entries":  fmt.Sprint(companionVoiceByLangMaxEntries),
		"Persona `voice_by_lang` overlay":  fmt.Sprintf("%d characters", companionPersonaVoiceMax),
		"Source zip download":              companionGuideSize(int(companionSourceCap)),
		"External pack zip download":       companionGuideSize(int(companionPackZipCap)),
		"Download time":                    fmt.Sprintf("%d s", int(companionFetchTimeout.Seconds())),
		"Redirects followed (https only)":  fmt.Sprint(companionRedirectMax),

		"Canvas width or height":     "64 to 2048 px",
		"Tags per `autoMatch` field": "32",
		"`autoMatch` tag length":     "128 characters",
		"Frame loop speed":           "0.1 to 30 fps",
		"Pack ID length":             "64 characters",
		"Raster file name length":    "128 characters before the extension",
		"Marketplace and pack name":  "64 characters",
		"Pack version":               "32 characters",
		"Author and license":         "64 characters",
		"Pack description":           "280 characters",
	}
	rows := map[string]string{}
	inTable := false
	for _, line := range strings.Split(companionAuthoringDoc(t, "guide.md"), "\n") {
		switch {
		case strings.HasPrefix(line, "| Limit |"):
			inTable = true
		case inTable && strings.HasPrefix(line, "|---"):
		case inTable && strings.HasPrefix(line, "|"):
			cells := strings.Split(strings.Trim(line, "|"), "|")
			if len(cells) != 2 {
				t.Fatalf("limits row %q does not have two cells", line)
			}
			label := strings.TrimSpace(cells[0])
			if _, dup := rows[label]; dup {
				t.Errorf("limit %q is listed twice", label)
			}
			rows[label] = strings.TrimSpace(cells[1])
		default:
			inTable = false
		}
	}
	for label, value := range want {
		if got, ok := rows[label]; !ok {
			t.Errorf("the guide lists no limit %q (expected %s)", label, value)
		} else if got != value {
			t.Errorf("limit %q: the guide says %s, the code says %s", label, got, value)
		}
	}
	for label := range rows {
		if _, ok := want[label]; !ok {
			t.Errorf("the guide lists a limit the test does not know: %q", label)
		}
	}
	// The literal caps are the validators' own.
	entry := func(field string, n int) error {
		doc := map[string]any{"id": "a", "name": "n", "version": "1", "path": "p"}
		doc[field] = strings.Repeat("x", n)
		return parseCompanionMarketplaceEntry(companionEncode(t, doc)).Err
	}
	for field, limit := range map[string]int{"name": 64, "version": 32, "author": 64, "license": 64, "description": 280} {
		if entry(field, limit) != nil || entry(field, limit+1) == nil {
			t.Errorf("entry %s is not capped at %d", field, limit)
		}
	}
	if companionPackID.MatchString(strings.Repeat("a", 65)) || !companionPackID.MatchString(strings.Repeat("a", 64)) {
		t.Error("pack ID length is not 64")
	}
	if validCompanionRaster(strings.Repeat("a", 129)+".png") || !validCompanionRaster(strings.Repeat("a", 128)+".png") {
		t.Error("raster file name length is not 128 characters before the extension")
	}
}

// ---- Drift (e): the schema names are in the canonical rigging manual ----

func companionSchemaProperties(node any, into map[string]bool) {
	switch n := node.(type) {
	case map[string]any:
		for key, value := range n {
			if key == "properties" {
				for name, sub := range value.(map[string]any) {
					into[name] = true
					companionSchemaProperties(sub, into)
				}
				continue
			}
			companionSchemaProperties(value, into)
		}
	case []any:
		for _, item := range n {
			companionSchemaProperties(item, into)
		}
	}
}

func TestCompanionAuthoringSchemaMatchesManual(t *testing.T) {
	raw, err := os.ReadFile("../../../ai-docs/manuals/companion-pet-rigging.md")
	if err != nil {
		t.Fatal(err)
	}
	manual := string(raw)
	names := map[string]bool{}
	companionSchemaProperties(companionAuthoringSchema(t, "pack.schema.json"), names)
	if len(names) < 40 {
		t.Fatalf("only %d property names found in the pack schema", len(names))
	}
	for name := range names {
		// A name counts when the manual writes it as a code span or a quoted
		// JSON key, so a prose word does not satisfy it.
		quoted := regexp.MustCompile("[`\"]" + regexp.QuoteMeta(name) + "[`\"]")
		if !quoted.MatchString(manual) {
			t.Errorf("pack.schema.json property %q is missing from companion-pet-rigging.md; add it to the manual's pack field reference", name)
		}
	}
}
