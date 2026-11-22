package wsdashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const companionPackSchema = "ws-companion/v1"

var (
	companionPackID   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	companionRaster   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,127}\.(webp|png)$`)
	companionMatchTag = regexp.MustCompile(`^[\x20-\x7e]{1,128}$`)
)

func validCompanionRaster(name string) bool {
	return companionRaster.MatchString(name) && !strings.Contains(name, "..")
}

// Read known fields by exact key, as the browser does. Keep extension
// fields in the original bytes instead of projecting a rewritten manifest.
type companionObject map[string]json.RawMessage

type companionManifest struct {
	ID          string
	Name        string
	Schema      string
	Canvas      []int
	AutoMatch   companionAutoMatch
	Head        companionObject
	Hairs       map[string]companionObject
	Poses       map[string]companionLayerGroup
	Gestures    map[string]companionLayerGroup
	Expressions map[string]companionObject
	Props       map[string]companionObject
	FX          map[string]companionObject
	Motions     map[string]companionObject
	Presets     map[string]companionObject
	Scenarios   map[string]companionObject
	Persona     *CompanionPersona
}

// CompanionPersona is a pack's optional character voice. The agent director
// quotes it as data in its prompt; it never reaches a lead or worker prompt.
type CompanionPersona struct {
	Name         string `json:"name,omitempty"`
	Voice        string `json:"voice,omitempty"`
	QuipLanguage string `json:"quipLanguage,omitempty"`
}

const (
	companionPersonaNameMax  = 40
	companionPersonaVoiceMax = 800
)

var companionLanguageTag = regexp.MustCompile(`^[A-Za-z]{2,8}(-[A-Za-z0-9]{1,8}){0,4}$`)

// companionPersonaField returns the validated persona, or nil when the field
// is absent, malformed, oversize, or carries control characters (the voice
// may hold line breaks and tabs). Lengths count Unicode code points.
func companionPersonaField(obj companionObject) *CompanionPersona {
	raw, ok := obj["persona"]
	if !ok {
		return nil
	}
	fields, err := parseCompanionObject(raw)
	if err != nil {
		return nil
	}
	var p CompanionPersona
	if fields.fields(map[string]any{"name": &p.Name, "voice": &p.Voice, "quipLanguage": &p.QuipLanguage}) != nil {
		return nil
	}
	p.Name, p.Voice, p.QuipLanguage = strings.TrimSpace(p.Name), strings.TrimSpace(p.Voice), strings.TrimSpace(p.QuipLanguage)
	if p.Name == "" && p.Voice == "" {
		return nil
	}
	if utf8.RuneCountInString(p.Name) > companionPersonaNameMax || utf8.RuneCountInString(p.Voice) > companionPersonaVoiceMax {
		return nil
	}
	if p.QuipLanguage != "" && !companionLanguageTag.MatchString(p.QuipLanguage) {
		return nil
	}
	for _, r := range p.Name {
		if unicode.IsControl(r) {
			return nil
		}
	}
	for _, r := range p.Voice {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return nil
		}
	}
	return &p
}

type companionAutoMatch struct {
	Harnesses []string `json:"harnesses"`
	Models    []string `json:"models"`
}

type companionLayer companionObject

type companionLayerGroup struct {
	Pose     string
	Fallback string
	Replaces []string
	Layers   []companionLayer
}

func (g *companionLayerGroup) UnmarshalJSON(data []byte) error {
	obj, err := parseCompanionObject(data)
	if err != nil {
		return err
	}
	return obj.fields(map[string]any{
		"pose": &g.Pose, "fallback": &g.Fallback, "replaces": &g.Replaces, "layers": &g.Layers,
	})
}

func parseCompanionObject(raw []byte) (companionObject, error) {
	var obj companionObject
	if err := json.Unmarshal(raw, &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return obj, nil
}

func (obj companionObject) field(key string, target any) error {
	raw, ok := obj[key]
	if !ok {
		return nil
	}
	if bytes.Equal(raw, []byte("null")) || json.Unmarshal(raw, target) != nil {
		return fmt.Errorf("invalid %s", key)
	}
	return nil
}

func (obj companionObject) fields(targets map[string]any) error {
	for _, key := range sortedCompanionKeys(targets) {
		if err := obj.field(key, targets[key]); err != nil {
			return err
		}
	}
	return nil
}

func sortedCompanionKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func validateCompanionManifest(raw []byte, id string) (companionManifest, []string, error) {
	var m companionManifest
	if !utf8.Valid(raw) {
		return m, nil, fmt.Errorf("manifest must be UTF-8")
	}
	obj, err := parseCompanionObject(raw)
	if err != nil {
		return m, nil, err
	}
	// persona is optional voice data: a malformed or oversize entry drops the
	// field, never the pack, so it is read apart from the strict fields.
	m.Persona = companionPersonaField(obj)
	var auto companionObject
	if err := obj.fields(map[string]any{
		"id": &m.ID, "name": &m.Name, "schema": &m.Schema, "canvas": &m.Canvas,
		"autoMatch": &auto, "head": &m.Head, "hairs": &m.Hairs, "poses": &m.Poses,
		"gestures": &m.Gestures, "expressions": &m.Expressions, "props": &m.Props,
		"fx": &m.FX, "motions": &m.Motions, "presets": &m.Presets, "scenarios": &m.Scenarios,
	}); err != nil {
		return m, nil, err
	}
	if m.Schema != companionPackSchema {
		return m, nil, fmt.Errorf("schema must be %s", companionPackSchema)
	}
	if m.ID != id || !companionPackID.MatchString(id) {
		return m, nil, fmt.Errorf("id must match the pack directory")
	}
	if strings.TrimSpace(m.Name) == "" {
		return m, nil, fmt.Errorf("name is required")
	}
	if len(m.Canvas) != 2 || m.Canvas[0] < 64 || m.Canvas[0] > 2048 || m.Canvas[1] < 64 || m.Canvas[1] > 2048 {
		return m, nil, fmt.Errorf("canvas must contain two dimensions between 64 and 2048")
	}
	if len(m.Poses["stand"].Layers) == 0 {
		return m, nil, fmt.Errorf("poses.stand requires at least one layer")
	}
	if err := auto.fields(map[string]any{"harnesses": &m.AutoMatch.Harnesses, "models": &m.AutoMatch.Models}); err != nil {
		return m, nil, err
	}
	for _, tags := range [][]string{m.AutoMatch.Harnesses, m.AutoMatch.Models} {
		if len(tags) > 32 {
			return m, nil, fmt.Errorf("autoMatch supports at most 32 tags per field")
		}
		for _, tag := range tags {
			if !companionMatchTag.MatchString(tag) || strings.TrimSpace(tag) == "" {
				return m, nil, fmt.Errorf("autoMatch tags must contain 1 to 128 printable ASCII characters")
			}
		}
	}

	check := companionManifestCheck{files: make(map[string]struct{}), motions: m.Motions, hasHead: m.Head != nil}
	if err := check.manifest(m); err != nil {
		return m, nil, err
	}
	return m, sortedCompanionKeys(check.files), nil
}

type companionManifestCheck struct {
	files   map[string]struct{}
	motions map[string]companionObject
	hasHead bool
}

func (c companionManifestCheck) manifest(m companionManifest) error {
	if err := c.head(m.Head); err != nil {
		return fmt.Errorf("head: %w", err)
	}
	for _, entry := range []struct {
		name   string
		values map[string]companionObject
		check  func(companionObject) error
	}{
		{"motions", m.Motions, func(o companionObject) error { return c.motion(o, false) }},
		{"hairs", m.Hairs, c.hair},
		{"expressions", m.Expressions, func(o companionObject) error { return c.assets(o, "decal") }},
		{"props", m.Props, c.effect},
		{"fx", m.FX, c.effect},
		{"presets", m.Presets, nil},
		{"scenarios", m.Scenarios, validateCompanionScenario},
	} {
		for _, id := range sortedCompanionKeys(entry.values) {
			obj := entry.values[id]
			if obj == nil {
				return fmt.Errorf("%s.%s must be an object", entry.name, id)
			}
			if entry.check != nil {
				if err := entry.check(obj); err != nil {
					return fmt.Errorf("%s.%s: %w", entry.name, id, err)
				}
			}
		}
	}
	for _, entry := range []struct {
		name   string
		groups map[string]companionLayerGroup
	}{
		{"poses", m.Poses},
		{"gestures", m.Gestures},
	} {
		for _, id := range sortedCompanionKeys(entry.groups) {
			if err := c.layers(entry.groups[id].Layers); err != nil {
				return fmt.Errorf("%s.%s: %w", entry.name, id, err)
			}
		}
	}
	return nil
}

var companionLayerAssets = []string{"file", "line", "jacketFill", "accentFill", "skinFill", "detail", "hairFill"}

func (c companionManifestCheck) addAsset(name string) error {
	if !validCompanionRaster(name) {
		return fmt.Errorf("asset names must be plain .webp or .png filenames")
	}
	c.files[name] = struct{}{}
	return nil
}

func (c companionManifestCheck) assets(obj companionObject, keys ...string) error {
	for _, key := range keys {
		if _, present := obj[key]; !present {
			continue
		}
		var name string
		if err := obj.field(key, &name); err != nil {
			return err
		}
		if err := c.addAsset(name); err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
	}
	return nil
}

func (c companionManifestCheck) head(obj companionObject) error {
	var egg, eyes companionObject
	var mouths map[string]string
	if err := obj.fields(map[string]any{"egg": &egg, "eyes": &eyes, "mouths": &mouths}); err != nil {
		return err
	}
	if err := c.assets(egg, companionLayerAssets...); err != nil {
		return err
	}
	if err := c.assets(eyes, "sclera", "irisFill", "irisLine", "frameLower", "lidCoverL", "lidCoverR",
		"lidLashL", "lidLashR", "browL", "browR"); err != nil {
		return err
	}
	for _, name := range mouths {
		if err := c.addAsset(name); err != nil {
			return err
		}
	}
	return nil
}

func (c companionManifestCheck) hair(obj companionObject) error {
	for _, key := range []string{"back", "front", "acc"} {
		var layer companionObject
		if err := obj.field(key, &layer); err != nil {
			return err
		}
		if err := c.assets(layer, companionLayerAssets...); err != nil {
			return err
		}
	}
	return nil
}

func (c companionManifestCheck) effect(obj companionObject) error {
	var layers []companionLayer
	if err := obj.field("layers", &layers); err != nil {
		return err
	}
	if err := c.renderable(obj, false); err != nil {
		return err
	}
	return c.layers(layers)
}

func (c companionManifestCheck) layers(layers []companionLayer) error {
	for i, layer := range layers {
		if err := c.renderable(companionObject(layer), true); err != nil {
			return fmt.Errorf("layer %d: %w", i, err)
		}
	}
	return nil
}

func (c companionManifestCheck) renderable(layer companionObject, required bool) error {
	var mode string
	if err := layer.field("mode", &mode); err != nil {
		return err
	}
	if err := c.assets(layer, companionLayerAssets...); err != nil {
		return err
	}
	if raw, ok := layer["motion"]; ok {
		if err := c.layerMotion(raw); err != nil {
			return err
		}
	}
	switch {
	case mode == "" && layer["frames"] != nil:
		mode = "semantic-split-frames"
	case mode == "" && companionHasAny(layer, companionLayerAssets[1:]...):
		mode = "semantic-split"
	case mode == "" && layer["file"] != nil:
		mode = "static"
	}
	switch mode {
	case "static":
		if layer["file"] == nil || layer["frames"] != nil {
			return fmt.Errorf("static layer requires file and no frames")
		}
	case "semantic-split":
		if !companionHasAny(layer, companionLayerAssets[1:]...) || layer["frames"] != nil {
			return fmt.Errorf("semantic-split layer requires raster channels and no frames")
		}
	case "head-egg":
		if !c.hasHead || layer["frames"] != nil {
			return fmt.Errorf("head-egg layer requires head and no frames")
		}
	case "semantic-split-frames", "static-frames":
		return c.frames(layer)
	case "":
		if required {
			return fmt.Errorf("layer requires a raster or frame loop")
		}
	default:
		return fmt.Errorf("invalid layer mode")
	}
	return nil
}

func (c companionManifestCheck) frames(layer companionObject) error {
	var fps float64
	var frames []json.RawMessage
	if err := layer.fields(map[string]any{"fps": &fps, "frames": &frames}); err != nil {
		return err
	}
	if len(frames) == 0 || fps < 0.1 || fps > 30 {
		return fmt.Errorf("frame loop requires frames and fps between 0.1 and 30")
	}
	for _, raw := range frames {
		if len(raw) != 0 && raw[0] == '"' {
			var file string
			if err := json.Unmarshal(raw, &file); err != nil {
				return err
			}
			if err := c.addAsset(file); err != nil {
				return err
			}
			continue
		}
		frame, err := parseCompanionObject(raw)
		if err != nil || !companionHasAny(frame, companionLayerAssets...) {
			return fmt.Errorf("frame requires a raster filename or raster channels")
		}
		if err := c.assets(frame, companionLayerAssets...); err != nil {
			return err
		}
	}
	return nil
}

func companionHasAny(obj companionObject, keys ...string) bool {
	for _, key := range keys {
		if obj[key] != nil {
			return true
		}
	}
	return false
}
