package wsdashboard

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	companionPackVRMSchema   = "ws-companion-vrm/v1"
	companionPackRenderer2D  = "2d"
	companionPackRendererVRM = "vrm"

	companionVrmAssetCap  = 32 << 20
	companionVrmaAssetCap = 4 << 20
	companionVrmPackCap   = 40 << 20

	companionVrmMapMax        = 16
	companionVrmExprTargetMax = 64

	companionGLBMagic           = 0x46546C67 // "glTF"
	companionGLBVersion         = 2
	companionGLBChunkJSON       = 0x4E4F534A // "JSON"
	companionGLBHeaderSize      = 12
	companionGLBChunkHeaderSize = 8
)

var (
	companionVrmFile  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.vrm$`)
	companionVrmaFile = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.vrma$`)
	companionVrmKeyID = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
)

func validCompanionVRMFile(name string) bool {
	return companionVrmFile.MatchString(name) && !strings.Contains(name, "..")
}

func validCompanionVRMAFile(name string) bool {
	return companionVrmaFile.MatchString(name) && !strings.Contains(name, "..")
}

func validCompanionVRMAsset(name string) bool {
	return validCompanionRaster(name) || validCompanionVRMFile(name) || validCompanionVRMAFile(name)
}

func companionAssetCapFor(schema, name string) int {
	if schema == companionPackVRMSchema {
		switch {
		case validCompanionVRMFile(name):
			return companionVrmAssetCap
		case validCompanionVRMAFile(name):
			return companionVrmaAssetCap
		}
	}
	return companionAssetCap
}

func validateCompanionVRMManifest(obj companionObject, id string) (companionManifest, []string, error) {
	var m companionManifest
	// persona is optional voice data: a malformed or oversize entry drops the
	// field, never the pack, matching ws-companion/v1.
	m.Persona = companionPersonaField(obj)
	var (
		auto      companionObject
		model     string
		thumbnail string
		vroidURL  string
		exprMap   map[string]json.RawMessage
		anims     map[string]string
	)
	if err := obj.fields(map[string]any{
		"id": &m.ID, "name": &m.Name, "schema": &m.Schema,
		"model": &model, "thumbnail": &thumbnail, "vroidSourceUrl": &vroidURL,
		"autoMatch": &auto, "expressionMap": &exprMap, "animations": &anims,
	}); err != nil {
		return m, nil, err
	}
	if m.Schema != companionPackVRMSchema {
		return m, nil, fmt.Errorf("schema must be %s", companionPackVRMSchema)
	}
	if m.ID != id || !companionPackID.MatchString(id) {
		return m, nil, fmt.Errorf("id must match the pack directory")
	}
	if strings.TrimSpace(m.Name) == "" {
		return m, nil, fmt.Errorf("name is required")
	}
	if !validCompanionVRMFile(model) {
		return m, nil, fmt.Errorf("model must be a plain .vrm filename")
	}
	files := map[string]struct{}{model: {}}
	if _, present := obj["thumbnail"]; present {
		if !validCompanionRaster(thumbnail) {
			return m, nil, fmt.Errorf("thumbnail must be a plain .webp or .png filename")
		}
		files[thumbnail] = struct{}{}
	}
	if _, present := obj["vroidSourceUrl"]; present {
		if err := companionHTTPSURL(vroidURL); err != nil {
			return m, nil, fmt.Errorf("vroidSourceUrl: %w", err)
		}
	}
	if err := parseCompanionAutoMatch(auto, &m.AutoMatch); err != nil {
		return m, nil, err
	}
	if _, present := obj["expressionMap"]; present {
		if len(exprMap) > companionVrmMapMax {
			return m, nil, fmt.Errorf("expressionMap supports at most %d entries", companionVrmMapMax)
		}
		for _, key := range sortedCompanionKeys(exprMap) {
			if !companionVrmKeyID.MatchString(key) {
				return m, nil, fmt.Errorf("expressionMap.%s: invalid expression ID", key)
			}
			entry, err := parseCompanionObject(exprMap[key])
			if err != nil {
				return m, nil, fmt.Errorf("expressionMap.%s: %w", key, err)
			}
			_, hasPreset := entry["preset"]
			_, hasCustom := entry["custom"]
			if hasPreset == hasCustom {
				return m, nil, fmt.Errorf("expressionMap.%s: must specify exactly one of preset or custom", key)
			}
			fieldName := "preset"
			if hasCustom {
				fieldName = "custom"
			}
			var target string
			if err := entry.field(fieldName, &target); err != nil {
				return m, nil, fmt.Errorf("expressionMap.%s: %w", key, err)
			}
			if err := companionText(fieldName, target, companionVrmExprTargetMax, true); err != nil {
				return m, nil, fmt.Errorf("expressionMap.%s: %w", key, err)
			}
			if _, hasWeight := entry["weight"]; hasWeight {
				var weight float64
				if err := entry.field("weight", &weight); err != nil {
					return m, nil, fmt.Errorf("expressionMap.%s: %w", key, err)
				}
				if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 || weight > 1 {
					return m, nil, fmt.Errorf("expressionMap.%s: weight must be between 0 and 1", key)
				}
			}
		}
	}
	if _, present := obj["animations"]; present {
		if len(anims) > companionVrmMapMax {
			return m, nil, fmt.Errorf("animations supports at most %d entries", companionVrmMapMax)
		}
		for _, key := range sortedCompanionKeys(anims) {
			if !companionVrmKeyID.MatchString(key) {
				return m, nil, fmt.Errorf("animations.%s: invalid animation ID", key)
			}
			file := anims[key]
			if !validCompanionVRMAFile(file) {
				return m, nil, fmt.Errorf("animations.%s: animation file must be a plain .vrma filename", key)
			}
			files[file] = struct{}{}
		}
	}
	return m, sortedCompanionKeys(files), nil
}

// parseCompanionGLBExtensions verifies the GLB v2 header and parses only the
// first JSON chunk, returning its top-level extensions object.
func parseCompanionGLBExtensions(data []byte) (companionObject, error) {
	const minLen = companionGLBHeaderSize + companionGLBChunkHeaderSize
	if len(data) < minLen {
		return nil, errors.New("GLB header is truncated")
	}
	if uint64(len(data)) > math.MaxUint32 {
		return nil, errors.New("GLB payload exceeds 32-bit length")
	}
	if binary.LittleEndian.Uint32(data[0:4]) != companionGLBMagic {
		return nil, errors.New("invalid GLB magic header")
	}
	if binary.LittleEndian.Uint32(data[4:8]) != companionGLBVersion {
		return nil, errors.New("unsupported GLB version (expected 2)")
	}
	if binary.LittleEndian.Uint32(data[8:12]) != uint32(len(data)) {
		return nil, errors.New("GLB declared byte length does not match payload")
	}
	chunkLen := binary.LittleEndian.Uint32(data[12:16])
	chunkType := binary.LittleEndian.Uint32(data[16:20])
	if chunkType != companionGLBChunkJSON {
		return nil, errors.New("first GLB chunk is not JSON")
	}
	if chunkLen == 0 || uint64(minLen)+uint64(chunkLen) > uint64(len(data)) {
		return nil, errors.New("GLB JSON chunk length is out of bounds")
	}
	chunk := data[minLen : minLen+int(chunkLen)]
	if !utf8.Valid(chunk) {
		return nil, errors.New("GLB JSON chunk must be UTF-8")
	}
	root, err := parseCompanionObject(chunk)
	if err != nil {
		return nil, errors.New("GLB JSON chunk is not a JSON object")
	}
	rawExts, ok := root["extensions"]
	if !ok {
		return nil, errors.New("GLB JSON chunk does not declare extensions")
	}
	exts, err := parseCompanionObject(rawExts)
	if err != nil {
		return nil, errors.New("GLB extensions must be a JSON object")
	}
	return exts, nil
}

// inspectCompanionVRM validates a GLB v2 .vrm binary and returns the detected
// VRM major version ("1.0" for VRMC_vrm or "0.x" for VRM).
func inspectCompanionVRM(data []byte) (string, error) {
	exts, err := parseCompanionGLBExtensions(data)
	if err != nil {
		return "", err
	}
	if raw, ok := exts["VRMC_vrm"]; ok {
		if _, err := parseCompanionObject(raw); err != nil {
			return "", errors.New("GLB extensions.VRMC_vrm must be a JSON object")
		}
		return "1.0", nil
	}
	if raw, ok := exts["VRM"]; ok {
		if _, err := parseCompanionObject(raw); err != nil {
			return "", errors.New("GLB extensions.VRM must be a JSON object")
		}
		return "0.x", nil
	}
	return "", errors.New("GLB does not declare VRM extensions (VRM or VRMC_vrm)")
}

// validateCompanionVRMA validates a GLB v2 .vrma animation binary by verifying
// the presence of extensions.VRMC_vrm_animation.
func validateCompanionVRMA(data []byte) error {
	exts, err := parseCompanionGLBExtensions(data)
	if err != nil {
		return err
	}
	raw, ok := exts["VRMC_vrm_animation"]
	if !ok {
		return errors.New("GLB does not declare VRMC_vrm_animation")
	}
	if _, err := parseCompanionObject(raw); err != nil {
		return errors.New("GLB extensions.VRMC_vrm_animation must be a JSON object")
	}
	return nil
}

// peekCompanionManifestSchema extracts only the top-level "schema" string from
// a raw pack.json payload without running full validation.
func peekCompanionManifestSchema(raw []byte) string {
	if !utf8.Valid(raw) {
		return ""
	}
	obj, err := parseCompanionObject(raw)
	if err != nil {
		return ""
	}
	var schema string
	if err := obj.field("schema", &schema); err != nil {
		return ""
	}
	return schema
}
