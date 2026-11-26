package wsdashboard

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kang-sw/devenv/internal/wsconfig"
)

// buildSyntheticGLB constructs a minimal valid GLB v2 payload whose first JSON
// chunk contains jsonPayload (4-byte space-padded per the GLB 2.0 spec) and an
// optional binary chunk payload.
func buildSyntheticGLB(jsonPayload string, binChunk []byte) []byte {
	padded := []byte(jsonPayload)
	for len(padded)%4 != 0 {
		padded = append(padded, ' ')
	}
	total := companionGLBHeaderSize + companionGLBChunkHeaderSize + len(padded)
	var paddedBin []byte
	if len(binChunk) > 0 {
		paddedBin = append([]byte(nil), binChunk...)
		for len(paddedBin)%4 != 0 {
			paddedBin = append(paddedBin, 0)
		}
		total += companionGLBChunkHeaderSize + len(paddedBin)
	}
	out := make([]byte, 0, total)
	out = binary.LittleEndian.AppendUint32(out, companionGLBMagic)
	out = binary.LittleEndian.AppendUint32(out, companionGLBVersion)
	out = binary.LittleEndian.AppendUint32(out, uint32(total))
	out = binary.LittleEndian.AppendUint32(out, uint32(len(padded)))
	out = binary.LittleEndian.AppendUint32(out, companionGLBChunkJSON)
	out = append(out, padded...)
	if len(paddedBin) > 0 {
		out = binary.LittleEndian.AppendUint32(out, uint32(len(paddedBin)))
		out = binary.LittleEndian.AppendUint32(out, 0x004E4942) // "BIN\0"
		out = append(out, paddedBin...)
	}
	return out
}

func syntheticVRM0GLB() []byte {
	return buildSyntheticGLB(`{"asset":{"version":"2.0"},"extensions":{"VRM":{"exporterVersion":"fixture","specVersion":"0.0"}}}`, []byte{1, 2, 3, 4})
}

func syntheticVRM1GLB() []byte {
	return buildSyntheticGLB(`{"asset":{"version":"2.0"},"extensions":{"VRMC_vrm":{"specVersion":"1.0"}}}`, []byte{5, 6, 7, 8})
}

func syntheticVRMAGLB() []byte {
	return buildSyntheticGLB(`{"asset":{"version":"2.0"},"extensions":{"VRMC_vrm_animation":{"specVersion":"1.0"}}}`, nil)
}

func companionTestVRMManifest(id, model, extra string) []byte {
	return []byte(fmt.Sprintf(`{"schema":"ws-companion-vrm/v1","id":%q,"name":%q,"model":%q%s}`, id, "Sample "+id, model, extra))
}

func companionTestVRMPack(t *testing.T, home, id string, manifest, modelBytes []byte, extraFiles map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(home, "companions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, companionManifestName), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if modelBytes != nil {
		if err := os.WriteFile(filepath.Join(dir, "model.vrm"), modelBytes, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range extraFiles {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCompanionGLBInspector(t *testing.T) {
	if ver, err := inspectCompanionVRM(syntheticVRM0GLB()); err != nil || ver != "0.x" {
		t.Fatalf("VRM 0.x: ver=%q err=%v", ver, err)
	}
	if ver, err := inspectCompanionVRM(syntheticVRM1GLB()); err != nil || ver != "1.0" {
		t.Fatalf("VRM 1.0: ver=%q err=%v", ver, err)
	}
	if err := validateCompanionVRMA(syntheticVRMAGLB()); err != nil {
		t.Fatalf("VRMA: %v", err)
	}

	// Non-VRM GLB rejected as .vrm and .vrma.
	plainGLB := buildSyntheticGLB(`{"asset":{"version":"2.0"},"extensions":{"KHR_materials_unlit":{}}}`, nil)
	if _, err := inspectCompanionVRM(plainGLB); err == nil {
		t.Fatal("plain GLB accepted as VRM")
	}
	if err := validateCompanionVRMA(plainGLB); err == nil {
		t.Fatal("plain GLB accepted as VRMA")
	}
	if err := validateCompanionVRMA(syntheticVRM1GLB()); err == nil {
		t.Fatal("VRM accepted as VRMA")
	}

	// Truncated, bad magic, bad version, mismatched length, bad chunk type, oversized chunk length, non-object extension.
	badLength := append([]byte(nil), syntheticVRM1GLB()...)
	binary.LittleEndian.PutUint32(badLength[8:12], uint32(len(badLength)+4))

	badMagic := append([]byte(nil), syntheticVRM1GLB()...)
	binary.LittleEndian.PutUint32(badMagic[0:4], 0x12345678)

	badVersion := append([]byte(nil), syntheticVRM1GLB()...)
	binary.LittleEndian.PutUint32(badVersion[4:8], 1)

	badChunkType := append([]byte(nil), syntheticVRM1GLB()...)
	binary.LittleEndian.PutUint32(badChunkType[16:20], 0x004E4942)

	overflowChunkLen := append([]byte(nil), syntheticVRM1GLB()...)
	binary.LittleEndian.PutUint32(overflowChunkLen[12:16], 0xffffffff)

	nullExt := buildSyntheticGLB(`{"asset":{"version":"2.0"},"extensions":{"VRMC_vrm":null}}`, nil)
	noExts := buildSyntheticGLB(`{"asset":{"version":"2.0"}}`, nil)
	invalidJSON := buildSyntheticGLB(`{"extensions":`, nil)

	for name, payload := range map[string][]byte{
		"empty":              nil,
		"truncated header":   syntheticVRM1GLB()[:12],
		"truncated body":     syntheticVRM1GLB()[:len(syntheticVRM1GLB())-2],
		"bad magic":          badMagic,
		"bad version":        badVersion,
		"length mismatch":    badLength,
		"non-JSON first":     badChunkType,
		"overflow chunk len": overflowChunkLen,
		"null extension":     nullExt,
		"no extensions":      noExts,
		"invalid JSON":       invalidJSON,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := inspectCompanionVRM(payload); err == nil {
				t.Fatalf("malformed VRM %q accepted", name)
			}
			if err := validateCompanionVRMA(payload); err == nil {
				t.Fatalf("malformed VRMA %q accepted", name)
			}
		})
	}
}

func TestCompanionVRMManifestValidation(t *testing.T) {
	full := `{
		"schema": "ws-companion-vrm/v1",
		"id": "fixture-vrm",
		"name": "Fixture Avatar",
		"model": "model.vrm",
		"thumbnail": "thumb.webp",
		"vroidSourceUrl": "https://example.test/models/123",
		"persona": {"name": "Echo", "voice": "Calm and concise.", "quipLanguage": "en"},
		"expressionMap": {
			"happy": {"preset": "happy", "weight": 0.92},
			"oo": {"custom": "surprised_alt"}
		},
		"animations": {
			"wave": "wave.vrma",
			"nod": "nod.vrma"
		},
		"framing": {"cameraY": 1.45},
		"futureExtension": {"flag": true}
	}`
	m, files, err := validateCompanionManifest([]byte(full), "fixture-vrm")
	if err != nil {
		t.Fatalf("valid VRM manifest rejected: %v", err)
	}
	wantFiles := []string{"model.vrm", "nod.vrma", "thumb.webp", "wave.vrma"}
	if !reflect.DeepEqual(files, wantFiles) {
		t.Fatalf("files = %v, want %v", files, wantFiles)
	}
	if m.Schema != companionPackVRMSchema || m.Name != "Fixture Avatar" || m.Persona == nil || m.Persona.Name != "Echo" {
		t.Fatalf("manifest fields: %+v", m)
	}

	// Malformed persona drops persona without failing the pack.
	badPersona := `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","persona":{"name":7}}`
	m, _, err = validateCompanionManifest([]byte(badPersona), "fixture-vrm")
	if err != nil || m.Persona != nil {
		t.Fatalf("bad persona: err=%v persona=%+v", err, m.Persona)
	}

	// Build 17-entry expressionMap and animations to check cap enforcement.
	var expr17, anim17 []string
	for i := range 17 {
		expr17 = append(expr17, fmt.Sprintf(`"e%d":{"preset":"happy"}`, i))
		anim17 = append(anim17, fmt.Sprintf(`"a%d":"a%d.vrma"`, i, i))
	}

	badCases := map[string]string{
		"missing model":         `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture"}`,
		"traversal model":       `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"../model.vrm"}`,
		"dotdot in model":       `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"a..b.vrm"}`,
		"wrong model ext":       `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.glb"}`,
		"bad thumbnail":         `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","thumbnail":"thumb.svg"}`,
		"http vroidSourceUrl":   `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","vroidSourceUrl":"http://example.test/m"}`,
		"empty vroidSourceUrl":  `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","vroidSourceUrl":""}`,
		"expr both preset+cust": `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","expressionMap":{"happy":{"preset":"happy","custom":"c"}}}`,
		"expr neither":          `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","expressionMap":{"happy":{"weight":0.5}}}`,
		"expr bad key":          `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","expressionMap":{"BadKey":{"preset":"happy"}}}`,
		"expr weight > 1":       `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","expressionMap":{"happy":{"preset":"happy","weight":1.1}}}`,
		"expr weight < 0":       `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","expressionMap":{"happy":{"preset":"happy","weight":-0.1}}}`,
		"expr empty target":     `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","expressionMap":{"happy":{"preset":"   "}}}`,
		"expr over 64 chars":    `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","expressionMap":{"happy":{"custom":"` + strings.Repeat("a", 65) + `"}}}`,
		"expr over 16 entries":  `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","expressionMap":{` + strings.Join(expr17, ",") + `}}`,
		"anim bad key":          `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","animations":{"1wave":"wave.vrma"}}`,
		"anim wrong ext":        `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","animations":{"wave":"wave.vrm"}}`,
		"anim traversal":        `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","animations":{"wave":"../wave.vrma"}}`,
		"anim over 16 entries":  `{"schema":"ws-companion-vrm/v1","id":"fixture-vrm","name":"Fixture","model":"model.vrm","animations":{` + strings.Join(anim17, ",") + `}}`,
	}
	for name, raw := range badCases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := validateCompanionManifest([]byte(raw), "fixture-vrm"); err == nil {
				t.Fatalf("invalid VRM manifest %q accepted", name)
			}
		})
	}
}

func TestCompanionVRMPackStoreIsolationCapsAndSymlinks(t *testing.T) {
	t.Run("bad expressionMap or GLB rejects only that pack", func(t *testing.T) {
		home := t.TempDir()
		companionTestPack(t, home, "good-2d", companionTestManifest("good-2d", "stand.webp", ""))
		companionTestVRMPack(t, home, "good-vrm",
			companionTestVRMManifest("good-vrm", "model.vrm", `,"animations":{"wave":"wave.vrma"},"framing":{"cameraY":1.4}`),
			syntheticVRM1GLB(),
			map[string][]byte{"wave.vrma": syntheticVRMAGLB()},
		)
		companionTestVRMPack(t, home, "bad-expr",
			companionTestVRMManifest("bad-expr", "model.vrm", `,"expressionMap":{"happy":{"preset":"happy","custom":"both"}}`),
			syntheticVRM1GLB(), nil,
		)
		companionTestVRMPack(t, home, "bad-glb",
			companionTestVRMManifest("bad-glb", "model.vrm", ""),
			[]byte("not a glb"), nil,
		)

		s := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: home})
		if s.byID["default"] == nil || s.byID["good-2d"] == nil || s.byID["good-vrm"] == nil {
			t.Fatalf("valid sibling packs missing: packs=%+v errors=%+v", s.packs, s.errors)
		}
		if s.byID["good-2d"].info.Renderer != companionPackRenderer2D || s.byID["good-vrm"].info.Renderer != companionPackRendererVRM {
			t.Fatalf("renderers: 2d=%q vrm=%q", s.byID["good-2d"].info.Renderer, s.byID["good-vrm"].info.Renderer)
		}
		if !strings.Contains(string(s.byID["good-vrm"].manifest), `"framing":{"cameraY":1.4}`) {
			t.Fatalf("unknown framing extension not preserved: %s", s.byID["good-vrm"].manifest)
		}
		for _, id := range []string{"bad-expr", "bad-glb"} {
			if s.byID[id] != nil || !companionTestError(s, id) {
				t.Fatalf("invalid pack %s not isolated: %+v", id, s.errors)
			}
		}
	})

	t.Run("vrm symlink rejected", func(t *testing.T) {
		home := t.TempDir()
		external := t.TempDir()
		extModel := filepath.Join(external, "model.vrm")
		if err := os.WriteFile(extModel, syntheticVRM1GLB(), 0o600); err != nil {
			t.Fatal(err)
		}
		dir := companionTestVRMPack(t, home, "link-vrm", companionTestVRMManifest("link-vrm", "model.vrm", ""), nil, nil)
		if err := os.Symlink(extModel, filepath.Join(dir, "model.vrm")); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		s := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: home})
		if s.byID["link-vrm"] != nil || !companionTestError(s, "link-vrm") {
			t.Fatalf("symlinked .vrm accepted: %+v", s.errors)
		}
	})

	t.Run("per-schema size caps enforced", func(t *testing.T) {
		// VRM .vrm over 32 MiB rejected; .vrma over 4 MiB rejected; VRM pack over 40 MiB rejected.
		vrmManifest := companionTestVRMManifest("big-vrm", "model.vrm", "")
		if _, _, err := loadCompanionPack("big-vrm", "local", companionCacheCap, func(name string, limit int) ([]byte, error) {
			if name == companionManifestName {
				return vrmManifest, nil
			}
			if limit < companionVrmAssetCap+1 {
				return nil, errCompanionFileLimit
			}
			return nil, nil
		}); err == nil || !strings.Contains(err.Error(), "file byte limit") {
			t.Fatalf(".vrm > 32 MiB: %v", err)
		}

		vrmaManifest := companionTestVRMManifest("big-vrma", "model.vrm", `,"animations":{"wave":"wave.vrma"}`)
		if _, _, err := loadCompanionPack("big-vrma", "local", companionCacheCap, func(name string, limit int) ([]byte, error) {
			switch name {
			case companionManifestName:
				return vrmaManifest, nil
			case "model.vrm":
				return syntheticVRM1GLB(), nil
			default:
				if limit < companionVrmaAssetCap+1 {
					return nil, errCompanionFileLimit
				}
				return nil, nil
			}
		}); err == nil || !strings.Contains(err.Error(), "file byte limit") {
			t.Fatalf(".vrma > 4 MiB: %v", err)
		}

		// VRM pack > 40 MiB: 32 MiB model + three 3 MiB .vrma clips = 41 MiB.
		bigVrmBin := make([]byte, companionVrmAssetCap-1024)
		bigVrm := buildSyntheticGLB(`{"asset":{"version":"2.0"},"extensions":{"VRMC_vrm":{"specVersion":"1.0"}}}`, bigVrmBin)
		clipBin := make([]byte, 3<<20)
		bigClip := buildSyntheticGLB(`{"asset":{"version":"2.0"},"extensions":{"VRMC_vrm_animation":{"specVersion":"1.0"}}}`, clipBin)
		packOver40 := companionTestVRMManifest("over-vrm", "model.vrm",
			`,"animations":{"a":"a.vrma","b":"b.vrma","c":"c.vrma"}`)
		if _, _, err := loadCompanionPack("over-vrm", "local", companionCacheCap, func(name string, limit int) ([]byte, error) {
			var b []byte
			switch name {
			case companionManifestName:
				b = packOver40
			case "model.vrm":
				b = bigVrm
			default:
				b = bigClip
			}
			if len(b) > limit {
				return nil, errCompanionFileLimit
			}
			return b, nil
		}); err == nil || !strings.Contains(err.Error(), "pack byte limit") {
			t.Fatalf("VRM pack > 40 MiB: %v", err)
		}

		// Regression: 2D pack > 16 MiB and 2D asset > 2 MiB are still rejected after raising cache cap.
		raw2DAsset := companionTestManifest("over-2d-asset", "stand.webp", "")
		if _, _, err := loadCompanionPack("over-2d-asset", "local", companionCacheCap, func(name string, limit int) ([]byte, error) {
			if name == companionManifestName {
				return raw2DAsset, nil
			}
			if companionAssetCap+1 > limit {
				return nil, errCompanionFileLimit
			}
			return nil, nil
		}); err == nil || !strings.Contains(err.Error(), "file byte limit") {
			t.Fatalf("2D asset > 2 MiB: %v", err)
		}

		layers := make([]string, 9)
		for i := range layers {
			layers[i] = fmt.Sprintf(`{"file":"%d.webp"}`, i)
		}
		raw2DPack := []byte(fmt.Sprintf(`{"schema":"ws-companion/v1","id":"over-2d-pack","name":"over-2d-pack","canvas":[512,512],"poses":{"stand":{"layers":[%s]}}}`, strings.Join(layers, ",")))
		twoMiB := make([]byte, companionAssetCap)
		if _, _, err := loadCompanionPack("over-2d-pack", "local", companionCacheCap, func(name string, limit int) ([]byte, error) {
			b := twoMiB
			if name == companionManifestName {
				b = raw2DPack
			}
			if len(b) > limit {
				return nil, errCompanionFileLimit
			}
			return b, nil
		}); err == nil || !strings.Contains(err.Error(), "pack byte limit") {
			t.Fatalf("2D pack > 16 MiB: %v", err)
		}
	})
}

func TestCompanionVRMHTTPRoutes(t *testing.T) {
	home := t.TempDir()
	model := syntheticVRM0GLB()
	vrma := syntheticVRMAGLB()
	manifest := companionTestVRMManifest("sample-vrm", "model.vrm", `,"thumbnail":"thumb.png","animations":{"wave":"wave.vrma"}`)
	companionTestVRMPack(t, home, "sample-vrm", manifest, model, map[string][]byte{
		"wave.vrma": vrma,
		"thumb.png": []byte("png-stub"),
	})

	d := authFixture(t)
	d.config = &ConfigBackend{Options: wsconfig.Options{ConfigHome: home}}
	cookie := authCookie(t, d)

	w := companionTestRequest(d, cookie, http.MethodGet, companionPacksRoute, "")
	if w.Code != http.StatusOK {
		t.Fatalf("catalog: %d %s", w.Code, w.Body)
	}
	var cat companionCatalog
	if err := json.Unmarshal(w.Body.Bytes(), &cat); err != nil {
		t.Fatal(err)
	}
	var defInfo, vrmInfo *companionPackInfo
	for i := range cat.Packs {
		switch cat.Packs[i].ID {
		case companionDefaultID:
			defInfo = &cat.Packs[i]
		case "sample-vrm":
			vrmInfo = &cat.Packs[i]
		}
	}
	if defInfo == nil || defInfo.Renderer != companionPackRenderer2D || defInfo.Schema != companionPackSchema {
		t.Fatalf("default pack info: %+v", defInfo)
	}
	if vrmInfo == nil || vrmInfo.Renderer != companionPackRendererVRM || vrmInfo.Schema != companionPackVRMSchema {
		t.Fatalf("vrm pack info: %+v", vrmInfo)
	}

	for _, tc := range []struct {
		file        string
		contentType string
		wantBody    []byte
	}{
		{"model.vrm", "model/gltf-binary", model},
		{"wave.vrma", "model/gltf-binary", vrma},
		{"thumb.png", "image/png", []byte("png-stub")},
	} {
		path := companionPacksRoute + "/sample-vrm/assets/" + tc.file + "?rev=" + vrmInfo.Rev
		w = companionTestRequest(d, cookie, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", tc.file, w.Code)
		}
		if w.Header().Get("Content-Type") != tc.contentType ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" ||
			w.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" ||
			w.Header().Get("Cache-Control") != "private, max-age=31536000, immutable" ||
			w.Header().Get("ETag") != `"`+vrmInfo.Rev+`"` {
			t.Fatalf("%s headers: %+v", tc.file, w.Header())
		}
		if string(w.Body.Bytes()) != string(tc.wantBody) {
			t.Fatalf("%s body mismatch", tc.file)
		}
		req := authRequest(http.MethodGet, path, "")
		req.AddCookie(cookie)
		req.Header.Set("If-None-Match", `"`+vrmInfo.Rev+`"`)
		if notMod := authServe(d, req); notMod.Code != http.StatusNotModified {
			t.Fatalf("%s If-None-Match: %d", tc.file, notMod.Code)
		}
	}
}

func TestCompanionT1OldValidatorSkipsVRMPack(t *testing.T) {
	home := t.TempDir()
	manifest2D := companionTestManifest("sibling-2d", "stand.webp", "")
	companionTestPack(t, home, "sibling-2d", manifest2D)
	manifestVRM := companionTestVRMManifest("new-vrm", "model.vrm", "")
	vrmModel := syntheticVRM1GLB()
	vrmDir := companionTestVRMPack(t, home, "new-vrm", manifestVRM, vrmModel, nil)

	// Simulate an older daemon whose validator accepts only ws-companion/v1.
	root, err := openCompanionPackEntry(nil, filepath.Join(home, "companions"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	entries, err := root.ReadDir(-1)
	if err != nil {
		t.Fatal(err)
	}
	oldSnap := &companionSnapshot{byID: make(map[string]*companionPack)}
	for _, entry := range entries {
		id := entry.Name()
		dir, err := openCompanionPackEntry(root, id, true)
		if err != nil {
			t.Fatal(err)
		}
		pack, name, err := loadCompanionPackWithValidator(id, "local", companionCacheCap, func(name string, limit int) ([]byte, error) {
			return readCompanionLocalFile(openCompanionPackEntry, dir, name, limit)
		}, validateCompanionManifestV1)
		_ = dir.Close()
		if err != nil {
			oldSnap.addError(id, id+"/"+name, err.Error())
			continue
		}
		oldSnap.add(pack)
	}
	if oldSnap.byID["sibling-2d"] == nil {
		t.Fatalf("old validator dropped sibling 2D pack: %+v", oldSnap.errors)
	}
	if oldSnap.byID["new-vrm"] != nil || !companionTestError(oldSnap, "new-vrm") {
		t.Fatalf("old validator did not isolate VRM pack: %+v", oldSnap.errors)
	}
	// Disk contents of the VRM pack remain untouched; reopening with the new store loads both.
	gotManifest, err := os.ReadFile(filepath.Join(vrmDir, companionManifestName))
	if err != nil || string(gotManifest) != string(manifestVRM) {
		t.Fatalf("VRM manifest modified on disk: err=%v", err)
	}
	gotModel, err := os.ReadFile(filepath.Join(vrmDir, "model.vrm"))
	if err != nil || string(gotModel) != string(vrmModel) {
		t.Fatalf("VRM model modified on disk: err=%v", err)
	}
	newSnap := new(CompanionPackStore).snapshot(wsconfig.Options{ConfigHome: home})
	if newSnap.byID["sibling-2d"] == nil || newSnap.byID["new-vrm"] == nil {
		t.Fatalf("new store did not restore both packs: %+v", newSnap.errors)
	}
}

func TestCompanionImportVRMRoute(t *testing.T) {
	f := newCompanionRouteFixture(t)
	model0 := syntheticVRM0GLB()
	model1 := syntheticVRM1GLB()

	postVRM := func(path, contentType string, body []byte) *httptest.ResponseRecorder {
		r := authRequest(http.MethodPost, path, string(body))
		r.AddCookie(f.cookie)
		r.Header.Set("Origin", companionTestOrigin)
		if contentType != "" {
			r.Header.Set("Content-Type", contentType)
		}
		return authServe(f.d, r)
	}

	// TrustedWrite gate: unauthenticated, cross-origin, LAN, wrong method.
	t.Run("trusted write gate", func(t *testing.T) {
		token, err := f.d.lanToken()
		if err != nil {
			t.Fatal(err)
		}
		path := companionPacksRoute + "/import-vrm?id=gate-test"
		send := func(method, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
			r := authRequest(method, path, string(model1))
			r.Header.Set("Content-Type", "model/gltf-binary")
			if cookie != nil {
				r.AddCookie(cookie)
			}
			if origin != "" {
				r.Header.Set("Origin", origin)
			}
			return authServe(f.d, r)
		}
		if w := send(http.MethodPost, companionTestOrigin, nil); w.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated: %d", w.Code)
		}
		if w := send(http.MethodPost, "http://evil.test", f.cookie); w.Code != http.StatusForbidden {
			t.Errorf("cross-origin: %d", w.Code)
		}
		r := authRequest(http.MethodPost, path, string(model1))
		r.Header.Set("Content-Type", "model/gltf-binary")
		r.Host, r.RemoteAddr = "192.0.2.1:43210", "192.0.2.2:1234"
		r.AddCookie(&http.Cookie{Name: lanTokenCookie, Value: token})
		r.Header.Set("Origin", "http://"+r.Host)
		if w := authServe(f.d, r); w.Code != http.StatusNotFound {
			t.Errorf("LAN: %d", w.Code)
		}
		if w := send(http.MethodGet, "", f.cookie); w.Code != http.StatusMethodNotAllowed ||
			w.Header().Get("Allow") != "POST" || w.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("GET: %d %+v", w.Code, w.Header())
		}
	})

	// 415 on unsupported content type; 400 on invalid/non-VRM GLB; 413 over cap.
	t.Run("validation and caps", func(t *testing.T) {
		if w := postVRM(companionPacksRoute+"/import-vrm", "application/json", []byte(`{}`)); w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("bad content-type: %d %s", w.Code, w.Body)
		}
		if w := postVRM(companionPacksRoute+"/import-vrm", "", model1); w.Code != http.StatusUnsupportedMediaType {
			t.Fatalf("missing content-type: %d %s", w.Code, w.Body)
		}
		nonVRM := buildSyntheticGLB(`{"asset":{"version":"2.0"}}`, nil)
		if w := postVRM(companionPacksRoute+"/import-vrm", "model/gltf-binary", nonVRM); w.Code != http.StatusBadRequest {
			t.Fatalf("non-VRM GLB: %d %s", w.Code, w.Body)
		}
		if w := postVRM(companionPacksRoute+"/import-vrm?id=INVALID", "model/gltf-binary", model1); w.Code != http.StatusBadRequest {
			t.Fatalf("bad explicit id: %d %s", w.Code, w.Body)
		}
		oldCap := companionVrmImportCap
		companionVrmImportCap = 64
		defer func() { companionVrmImportCap = oldCap }()
		if w := postVRM(companionPacksRoute+"/import-vrm", "model/gltf-binary", model1); w.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("over cap raw: %d %s", w.Code, w.Body)
		}
	})

	// Raw model/gltf-binary with explicit id + 409 collision on repeat + uninstall.
	t.Run("raw upload explicit id and 409 collision", func(t *testing.T) {
		w := postVRM(companionPacksRoute+"/import-vrm?id=my-vrm&name=My%20VRM", "model/gltf-binary", model1)
		if w.Code != http.StatusOK {
			t.Fatalf("raw import: %d %s", w.Code, w.Body)
		}
		info := companionRouteDecode[companionPackInfo](t, w)
		if info.ID != "my-vrm" || info.Name != "My VRM" || info.Schema != companionPackVRMSchema || info.Renderer != companionPackRendererVRM || info.Rev == "" {
			t.Fatalf("unexpected info: %+v", info)
		}
		// Explicit id collision answers 409 without overwriting.
		if w = postVRM(companionPacksRoute+"/import-vrm?id=my-vrm&name=Other", "application/octet-stream", model0); w.Code != http.StatusConflict {
			t.Fatalf("explicit collision: %d %s", w.Code, w.Body)
		}
		// Model asset is immediately served from the reloaded in-memory snapshot.
		assetW := companionTestRequest(f.d, f.cookie, http.MethodGet, companionPacksRoute+"/my-vrm/assets/model.vrm?rev="+info.Rev, "")
		if assetW.Code != http.StatusOK || assetW.Header().Get("Content-Type") != "model/gltf-binary" || !bytes.Equal(assetW.Body.Bytes(), model1) {
			t.Fatalf("served asset: %d %q len=%d", assetW.Code, assetW.Header().Get("Content-Type"), assetW.Body.Len())
		}
		// Uninstall removes the sidecar-installed VRM pack.
		unW := f.post(companionPacksRoute+"/my-vrm/uninstall", nil)
		if unW.Code != http.StatusOK {
			t.Fatalf("uninstall: %d %s", unW.Code, unW.Body)
		}
		cat := companionRouteDecode[companionCatalog](t, unW)
		if companionRouteCatalogPack(&cat, "my-vrm") != nil {
			t.Fatalf("uninstalled pack still in catalog: %+v", cat.Packs)
		}
	})

	// Multipart upload + derived slug collision suffixing (-2, -3).
	t.Run("multipart and derived slug collision suffix", func(t *testing.T) {
		sendMultipart := func(filename, id, name string, payload []byte) *httptest.ResponseRecorder {
			var buf bytes.Buffer
			mw := multipart.NewWriter(&buf)
			if id != "" {
				_ = mw.WriteField("id", id)
			}
			if name != "" {
				_ = mw.WriteField("name", name)
			}
			fw, err := mw.CreateFormFile("file", filename)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = fw.Write(payload)
			_ = mw.Close()
			return postVRM(companionPacksRoute+"/import-vrm", mw.FormDataContentType(), buf.Bytes())
		}
		w1 := sendMultipart("Sample Avatar.vrm", "", "", model0)
		if w1.Code != http.StatusOK {
			t.Fatalf("multipart 1: %d %s", w1.Code, w1.Body)
		}
		i1 := companionRouteDecode[companionPackInfo](t, w1)
		if i1.ID != "sample-avatar" || i1.Name != "Sample Avatar" || i1.Renderer != companionPackRendererVRM {
			t.Fatalf("first derived info: %+v", i1)
		}
		w2 := sendMultipart("Sample Avatar.vrm", "", "", model1)
		if w2.Code != http.StatusOK {
			t.Fatalf("multipart 2: %d %s", w2.Code, w2.Body)
		}
		i2 := companionRouteDecode[companionPackInfo](t, w2)
		if i2.ID != "sample-avatar-2" {
			t.Fatalf("second derived info should get -2 suffix: %+v", i2)
		}
		w3 := sendMultipart("Sample Avatar.vrm", "", "", model1)
		if w3.Code != http.StatusOK {
			t.Fatalf("multipart 3: %d %s", w3.Code, w3.Body)
		}
		i3 := companionRouteDecode[companionPackInfo](t, w3)
		if i3.ID != "sample-avatar-3" {
			t.Fatalf("third derived info should get -3 suffix: %+v", i3)
		}
		// Multipart over cap answers 413.
		oldCap := companionVrmImportCap
		companionVrmImportCap = 64
		defer func() { companionVrmImportCap = oldCap }()
		if wOver := sendMultipart("Huge.vrm", "", "", model1); wOver.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("multipart over cap: %d %s", wOver.Code, wOver.Body)
		}
	})
}

func TestCompanionZipVRMPackAndMarketplaceMetadata(t *testing.T) {
	f := newCompanionRouteFixture(t)
	model := syntheticVRM1GLB()
	vrma := syntheticVRMAGLB()
	thumb := companionTestPNG(t, 16, 16)

	vrmManifest := companionTestVRMManifest("vrm-pack", "model.vrm",
		`,"thumbnail":"thumb.png","animations":{"wave":"wave.vrma"}`)
	market := []byte(`{
		"schema":"ws-companion-marketplace/v1",
		"name":"Mixed Source",
		"packs":[
			{
				"id":"vrm-pack",
				"name":"VRM Pack",
				"version":"1.0.0",
				"format":"vrm",
				"vroidSourceUrl":"https://example.invalid/avatar",
				"path":"packs/vrm-pack"
			},
			{
				"id":"bad-2d",
				"name":"Bad 2D",
				"version":"1.0.0",
				"format":"2d",
				"path":"packs/bad-2d"
			}
		]
	}`)
	zipBytes := companionTestZip(t,
		companionZipEntry{name: "marketplace.json", data: market},
		companionZipEntry{name: "packs/vrm-pack/pack.json", data: vrmManifest},
		companionZipEntry{name: "packs/vrm-pack/model.vrm", data: model},
		companionZipEntry{name: "packs/vrm-pack/wave.vrma", data: vrma},
		companionZipEntry{name: "packs/vrm-pack/thumb.png", data: thumb},
		// 2D pack smuggling a .vrm file must still be rejected.
		companionZipEntry{name: "packs/bad-2d/pack.json", data: companionTestManifest("bad-2d", "stand.png", "")},
		companionZipEntry{name: "packs/bad-2d/stand.png", data: thumb},
		companionZipEntry{name: "packs/bad-2d/extra.vrm", data: model},
	)

	// Validate lists both packs: vrm-pack succeeds with format & vroidSourceUrl preserved; bad-2d is rejected.
	valW := f.post(companionPacksRoute+"/validate?name=mixed.zip", zipBytes)
	if valW.Code != http.StatusOK {
		t.Fatalf("validate: %d %s", valW.Code, valW.Body)
	}
	valRes := companionRouteDecode[companionInstallResult](t, valW)
	vp := companionListed(valRes, "vrm-pack")
	if vp == nil || vp.Error != nil || vp.Status != companionStatusAvailable ||
		vp.Format != "vrm" || vp.VroidSourceURL != "https://example.invalid/avatar" {
		t.Fatalf("vrm-pack listing: %+v", vp)
	}
	b2 := companionListed(valRes, "bad-2d")
	if b2 == nil || b2.Error == nil || b2.Format != "2d" {
		t.Fatalf("2D pack with .vrm should fail: %+v", b2)
	}

	// Import vrm-pack, serve assets, and uninstall it cleanly.
	impW := f.post(companionPacksRoute+"/import?name=mixed.zip&id=vrm-pack", zipBytes)
	if impW.Code != http.StatusOK {
		t.Fatalf("import: %d %s", impW.Code, impW.Body)
	}
	impRes := companionRouteDecode[companionInstallResult](t, impW)
	info := companionRouteCatalogPack(impRes.Catalog, "vrm-pack")
	if info == nil || info.Renderer != companionPackRendererVRM || info.Schema != companionPackVRMSchema {
		t.Fatalf("installed vrm-pack info: %+v", info)
	}
	aw := companionTestRequest(f.d, f.cookie, http.MethodGet, companionPacksRoute+"/vrm-pack/assets/wave.vrma?rev="+info.Rev, "")
	if aw.Code != http.StatusOK || aw.Header().Get("Content-Type") != "model/gltf-binary" || !bytes.Equal(aw.Body.Bytes(), vrma) {
		t.Fatalf("wave.vrma: %d %q", aw.Code, aw.Header().Get("Content-Type"))
	}
	unW := f.post(companionPacksRoute+"/vrm-pack/uninstall", nil)
	if unW.Code != http.StatusOK {
		t.Fatalf("uninstall vrm-pack: %d %s", unW.Code, unW.Body)
	}
	unCat := companionRouteDecode[companionCatalog](t, unW)
	if companionRouteCatalogPack(&unCat, "vrm-pack") != nil {
		t.Fatalf("vrm-pack remained after uninstall: %+v", unCat.Packs)
	}

	// Official source fetch preserves format and vroidSourceUrl on listed entries.
	oldMarket := companionOfficialMarketplace
	companionOfficialMarketplace = []byte(`{
		"schema":"ws-companion-marketplace/v1",
		"name":"Official Packs",
		"packs":[
			{
				"id":"official-vrm",
				"name":"Official VRM",
				"version":"1.0.0",
				"format":"vrm",
				"vroidSourceUrl":"https://example.invalid/official-vrm",
				"url":"https://example.invalid/official-vrm.zip",
				"sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
			}
		]
	}`)
	defer func() { companionOfficialMarketplace = oldMarket }()
	f.d.companion.snapshot.Store(&companionConfigSnapshot{wake: make(chan struct{}), config: CompanionConfig{Enabled: true}})
	srcW := f.post(companionSourcesRoute+"/fetch", []byte(`{"source":"official"}`))
	if srcW.Code != http.StatusOK {
		t.Fatalf("official source fetch: %d %s", srcW.Code, srcW.Body)
	}
	srcRes := companionRouteDecode[companionInstallResult](t, srcW)
	ov := companionListed(srcRes, "official-vrm")
	if ov == nil || ov.Format != "vrm" || ov.VroidSourceURL != "https://example.invalid/official-vrm" {
		t.Fatalf("official source listing entry: %+v", ov)
	}
}
