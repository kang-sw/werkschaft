package wsdashboard

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Pack clips: a ws-companion-vrm/v1 pack may carry an actions catalog, sentinel
// overrides, prop calibration and reserved prop declarations in pack.json, and
// clip files under clips/. The flat-file validator never sees these keys, so an
// older daemon loads the same pack unchanged; this file parses them leniently
// (a bad entry is dropped with a warning, never the pack).
const (
	companionClipSchema       = "ws-companion-clip/v1"
	companionClipSchemaPrefix = "ws-companion-clip/v"
	companionClipsDirName     = "clips"
	companionClipExt          = ".clip.json"
	companionClipVRMAExt      = ".vrma"
	companionClipKindClip     = "clip"
	companionClipKindVRMA     = "vrma"

	companionClipFileCap      = 256 << 10
	companionActionsMax       = 64
	companionPropsMax         = 16
	companionClipKeyframesMax = 32
	companionClipExprMax      = 8
	companionClipLabelMax     = 32
	companionClipDurationMin  = 0.1
	companionClipDurationMax  = 30.0
	companionCalibrationMax   = 16
	companionClipWarningsMax  = 32
	// companionClipWarningMaxRunes bounds one warning line.
	companionClipWarningMaxRunes = 160
)

// companionSentinelKeys are the state-machine anchors a pack may override.
var companionSentinelKeys = []string{"idle", "laptop_work", "phone_hold", "phone_typing", "phone_putdown", "knock_startle", "wakeup_jolt"}

var (
	companionClipPresetID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	companionHumanoidBone = regexp.MustCompile(`^[a-z][A-Za-z0-9]{1,31}$`)
	companionClipFileName = regexp.MustCompile(`^([a-z][a-z0-9_]{0,31})\.(clip\.json|vrma)$`)
)

func companionSentinelKey(key string) bool {
	for _, k := range companionSentinelKeys {
		if k == key {
			return true
		}
	}
	return false
}

// validCompanionClipKey accepts an action or sentinel key: the expression ID
// grammar, minus Windows device names, so a key-derived file name is always
// a plain, portable name.
func validCompanionClipKey(key string) bool {
	return companionVrmKeyID.MatchString(key) && !companionReservedName(key)
}

// companionClipFile returns the pack-relative clip file name derived from a
// validated key and kind; request input never names a file.
func companionClipFile(key, kind string) string {
	if kind == companionClipKindVRMA {
		return companionClipsDirName + "/" + key + companionClipVRMAExt
	}
	return companionClipsDirName + "/" + key + companionClipExt
}

// companionClipEntry is one parsed actions or sentinels entry.
type companionClipEntry struct {
	Key         string
	Kind        string
	File        string
	Description string
	Expression  string
	Prop        string
	Expose      bool
	Loop        *bool
	Duration    *float64
	// exposable is false when a hand-edited description carries characters
	// the write API refuses; the entry still plays but never reaches the agent.
	exposable bool
	// loaded is set when the clip file loaded and validated; clipLoop is the
	// clip file's own loop flag.
	loaded   bool
	clipLoop bool
}

// effectiveLoop applies the entry's loop over the clip file's.
func (e companionClipEntry) effectiveLoop() bool {
	if e.Loop != nil {
		return *e.Loop
	}
	return e.clipLoop
}

// companionClipCatalog is the parsed authoring state of one VRM pack.
type companionClipCatalog struct {
	actions        []companionClipEntry
	sentinels      map[string]companionClipEntry
	hasCalibration bool
	// hasReserved is set when calibration or props is present at all, valid
	// or not: the raw bytes are preserved and an install must not drop them.
	hasReserved bool
}

// authored reports Studio-authored content an install must not replace.
func (c *companionClipCatalog) authored(clips map[string][]byte) bool {
	return c != nil && (len(c.actions) > 0 || len(c.sentinels) > 0 || c.hasCalibration || c.hasReserved || len(clips) > 0)
}

// entries lists sentinels (fixed order) then actions (catalog order).
func (c *companionClipCatalog) entries() []*companionClipEntry {
	if c == nil {
		return nil
	}
	out := make([]*companionClipEntry, 0, len(c.sentinels)+len(c.actions))
	for _, key := range companionSentinelKeys {
		if e, ok := c.sentinels[key]; ok {
			entry := e
			out = append(out, &entry)
		}
	}
	for i := range c.actions {
		out = append(out, &c.actions[i])
	}
	return out
}

type companionClipWarnings struct{ list []string }

func (w *companionClipWarnings) add(format string, args ...any) {
	if len(w.list) < companionClipWarningsMax {
		// Warnings embed manifest-controlled keys and errors; bound each one.
		w.list = append(w.list, companionTrimRunes(fmt.Sprintf(format, args...), companionClipWarningMaxRunes))
	}
}

// companionDescriptionText checks an action description as the write API
// does: at most CompanionActionDescMaxRunes code points, no control,
// format (Cf: bidi overrides, zero-width), line- or paragraph-separator
// characters.
func companionDescriptionText(value string) error {
	if !utf8.ValidString(value) {
		return errors.New("description must be UTF-8")
	}
	if utf8.RuneCountInString(value) > CompanionActionDescMaxRunes {
		return fmt.Errorf("description exceeds %d characters", CompanionActionDescMaxRunes)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return errors.New("description must not contain control, format or separator characters")
		}
	}
	return nil
}

// parseCompanionClipEntry parses one entry. A sentinel entry reads only
// kind, file, loop and duration_s; every other field is preserved unread.
func parseCompanionClipEntry(raw json.RawMessage, sentinelKey string) (companionClipEntry, error) {
	var e companionClipEntry
	obj, err := parseCompanionObject(raw)
	if err != nil {
		return e, err
	}
	var (
		loop     bool
		duration float64
	)
	targets := map[string]any{"kind": &e.Kind, "file": &e.File, "loop": &loop, "duration_s": &duration}
	if sentinelKey == "" {
		targets["key"] = &e.Key
		targets["description"] = &e.Description
		targets["expression"] = &e.Expression
		targets["prop"] = &e.Prop
		targets["expose_to_agent"] = &e.Expose
	} else {
		e.Key = sentinelKey
	}
	if err := obj.fields(targets); err != nil {
		return e, err
	}
	if !validCompanionClipKey(e.Key) {
		return e, errors.New("invalid key")
	}
	if e.Kind != companionClipKindClip && e.Kind != companionClipKindVRMA {
		return e, errors.New(`kind must be "clip" or "vrma"`)
	}
	if e.File != companionClipFile(e.Key, e.Kind) {
		return e, fmt.Errorf("file must be %s", companionClipFile(e.Key, e.Kind))
	}
	if _, ok := obj["loop"]; ok {
		e.Loop = &loop
	}
	if _, ok := obj["duration_s"]; ok {
		if duration < companionClipDurationMin || duration > companionClipDurationMax {
			return e, fmt.Errorf("duration_s must be between %g and %g", companionClipDurationMin, companionClipDurationMax)
		}
		e.Duration = &duration
	}
	if sentinelKey != "" {
		return e, nil
	}
	if _, ok := obj["expression"]; ok && !companionVrmKeyID.MatchString(e.Expression) {
		return e, errors.New("invalid expression ID")
	}
	if _, ok := obj["prop"]; ok && !companionVrmKeyID.MatchString(e.Prop) {
		return e, errors.New("invalid prop ID")
	}
	e.exposable = companionDescriptionText(e.Description) == nil
	return e, nil
}

// parseCompanionClipCatalog reads actions, sentinels, calibration and the
// reserved props declaration from a VRM manifest object. Nothing here can
// refuse the pack.
func parseCompanionClipCatalog(obj companionObject) (*companionClipCatalog, []string) {
	cat := &companionClipCatalog{sentinels: map[string]companionClipEntry{}}
	var warn companionClipWarnings

	if raw, ok := obj["sentinels"]; ok {
		entries, err := parseCompanionObject(raw)
		if err != nil {
			warn.add("sentinels must be an object; ignored")
		}
		for _, key := range sortedCompanionKeys(entries) {
			if !companionSentinelKey(key) {
				warn.add("sentinels.%s: not a sentinel key; dropped", key)
				continue
			}
			entry, err := parseCompanionClipEntry(entries[key], key)
			if err != nil {
				warn.add("sentinels.%s: %s; dropped", key, err)
				continue
			}
			cat.sentinels[key] = entry
		}
	}

	if raw, ok := obj["actions"]; ok {
		var items []json.RawMessage
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &items) != nil {
			warn.add("actions must be an array; ignored")
		}
		var legacy map[string]json.RawMessage
		_ = json.Unmarshal(obj["animations"], &legacy)
		seen := map[string]bool{}
		for i, item := range items {
			if i >= companionActionsMax {
				warn.add("actions: entries past %d dropped", companionActionsMax)
				break
			}
			entry, err := parseCompanionClipEntry(item, "")
			if err != nil {
				warn.add("actions[%d]: %s; dropped", i, err)
				continue
			}
			switch {
			case companionSentinelKey(entry.Key):
				warn.add("actions.%s: sentinel keys belong in sentinels; dropped", entry.Key)
				continue
			case seen[entry.Key]:
				warn.add("actions.%s: duplicate key; dropped", entry.Key)
				continue
			}
			seen[entry.Key] = true
			if _, clash := legacy[entry.Key]; clash {
				warn.add("actions.%s: overrides animations.%s", entry.Key, entry.Key)
			}
			if entry.Expose && !entry.exposable {
				warn.add("actions.%s: description has unsupported characters; not shown to the agent", entry.Key)
			}
			cat.actions = append(cat.actions, entry)
		}
	}

	if raw, ok := obj["calibration"]; ok {
		cat.hasReserved = true
		if err := validateCompanionCalibration(raw); err != nil {
			warn.add("calibration: %s; ignored", err)
		} else {
			cat.hasCalibration = true
		}
	}
	if raw, ok := obj["props"]; ok {
		cat.hasReserved = true
		if err := validateCompanionReservedProps(raw); err != nil {
			warn.add("props: %s; ignored", err)
		}
	}
	return cat, warn.list
}

func companionFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// companionVec3Field reads an optional [x,y,z] field; limit 0 means any
// finite value. present reports whether the field exists.
func companionVec3Field(obj companionObject, key string, limit float64) error {
	if _, ok := obj[key]; !ok {
		return nil
	}
	var v []float64
	if err := obj.field(key, &v); err != nil || len(v) != 3 {
		return fmt.Errorf("%s must be [x, y, z]", key)
	}
	for _, n := range v {
		if !companionFinite(n) || (limit > 0 && math.Abs(n) > limit) {
			return fmt.Errorf("%s values must be within ±%g", key, limit)
		}
	}
	return nil
}

func companionNumberField(obj companionObject, key string, lo, hi float64) error {
	if _, ok := obj[key]; !ok {
		return nil
	}
	var v float64
	if err := obj.field(key, &v); err != nil || !companionFinite(v) || v < lo || v > hi {
		return fmt.Errorf("%s must be between %g and %g", key, lo, hi)
	}
	return nil
}

func companionFiniteField(obj companionObject, key string) error {
	return companionNumberField(obj, key, -math.MaxFloat64, math.MaxFloat64)
}

func companionOptionalObject(obj companionObject, key string) (companionObject, error) {
	raw, ok := obj[key]
	if !ok {
		return nil, nil
	}
	out, err := parseCompanionObject(raw)
	if err != nil {
		return nil, fmt.Errorf("%s must be an object", key)
	}
	return out, nil
}

// validateCompanionCalibration checks
// {props: {<propId>: {offset?, rotation_deg?, scale?}}}; unknown subkeys are
// allowed and preserved.
func validateCompanionCalibration(raw []byte) error {
	obj, err := parseCompanionObject(raw)
	if err != nil {
		return errors.New("must be an object")
	}
	props, err := companionOptionalObject(obj, "props")
	if err != nil {
		return err
	}
	if len(props) > companionCalibrationMax {
		return fmt.Errorf("props supports at most %d entries", companionCalibrationMax)
	}
	for _, id := range sortedCompanionKeys(props) {
		if !companionVrmKeyID.MatchString(id) {
			return fmt.Errorf("props.%s: invalid prop ID", id)
		}
		entry, err := parseCompanionObject(props[id])
		if err != nil {
			return fmt.Errorf("props.%s: must be an object", id)
		}
		if err := companionVec3Field(entry, "offset", 0.5); err != nil {
			return fmt.Errorf("props.%s: %w", id, err)
		}
		if err := companionVec3Field(entry, "rotation_deg", 0); err != nil {
			return fmt.Errorf("props.%s: %w", id, err)
		}
		if err := companionNumberField(entry, "scale", 0.25, 4); err != nil {
			return fmt.Errorf("props.%s: %w", id, err)
		}
	}
	return nil
}

// validateCompanionReservedProps checks the reserved props[] declaration.
// It has no runtime yet; the result only drives a warning.
func validateCompanionReservedProps(raw []byte) error {
	var items []json.RawMessage
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &items) != nil {
		return errors.New("must be an array")
	}
	if len(items) > companionPropsMax {
		return fmt.Errorf("supports at most %d entries", companionPropsMax)
	}
	transform := func(obj companionObject, where string) error {
		if err := companionVec3Field(obj, "offset", 0); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		if err := companionVec3Field(obj, "rotation_deg", 0); err != nil {
			return fmt.Errorf("%s: %w", where, err)
		}
		return nil
	}
	for i, item := range items {
		prop, err := parseCompanionObject(item)
		if err != nil {
			return fmt.Errorf("[%d] must be an object", i)
		}
		var id, kind string
		if prop.fields(map[string]any{"id": &id, "kind": &kind}) != nil || !companionVrmKeyID.MatchString(id) {
			return fmt.Errorf("[%d]: invalid id", i)
		}
		if kind != "rigid" && kind != "chain" {
			return fmt.Errorf("%s: kind must be rigid or chain", id)
		}
		sockets, err := companionOptionalObject(prop, "sockets")
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		for _, name := range sortedCompanionKeys(sockets) {
			socket, err := parseCompanionObject(sockets[name])
			var bone string
			if err != nil || !companionVrmKeyID.MatchString(name) || socket.field("bone", &bone) != nil || !companionHumanoidBone.MatchString(bone) {
				return fmt.Errorf("%s.sockets.%s: invalid socket", id, name)
			}
			if err := transform(socket, id+".sockets."+name); err != nil {
				return err
			}
		}
		kit, err := companionOptionalObject(prop, "kit_offsets")
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		for _, name := range sortedCompanionKeys(kit) {
			entry, err := parseCompanionObject(kit[name])
			if err != nil {
				return fmt.Errorf("%s.kit_offsets.%s: must be an object", id, name)
			}
			if err := transform(entry, id+".kit_offsets."+name); err != nil {
				return err
			}
		}
		life, err := companionOptionalObject(prop, "lifecycle")
		if err != nil {
			return fmt.Errorf("%s: %w", id, err)
		}
		for _, state := range []string{"equip", "unequip", "enter", "exit"} {
			var key string
			if _, ok := life[state]; ok && (life.field(state, &key) != nil || !companionVrmKeyID.MatchString(key)) {
				return fmt.Errorf("%s.lifecycle.%s: invalid action key", id, state)
			}
		}
		var using []string
		if life.field("using", &using) != nil {
			return fmt.Errorf("%s.lifecycle.using: must be a list of action keys", id)
		}
		for _, key := range using {
			if !companionVrmKeyID.MatchString(key) {
				return fmt.Errorf("%s.lifecycle.using: invalid action key", id)
			}
		}
	}
	return nil
}

// companionClipMeta is what the backend needs from a validated clip file.
type companionClipMeta struct {
	Loop     bool
	Duration float64
}

// validateCompanionClip validates one ws-companion-clip/v1 file whose name
// stem is key. Unknown fields anywhere are allowed (the Studio round-trips
// them); unknown easing values read as linear at runtime.
func validateCompanionClip(data []byte, key string) (companionClipMeta, error) {
	var meta companionClipMeta
	if len(data) > companionClipFileCap {
		return meta, fmt.Errorf("clip exceeds the %s limit", companionByteSize(companionClipFileCap))
	}
	if !utf8.Valid(data) {
		return meta, errors.New("clip must be UTF-8 JSON")
	}
	obj, err := parseCompanionObject(data)
	if err != nil {
		return meta, errors.New("clip must be a JSON object")
	}
	var (
		schema, clipKey, easing string
		frames                  []json.RawMessage
	)
	if err := obj.fields(map[string]any{
		"schema": &schema, "key": &clipKey, "duration_s": &meta.Duration, "loop": &meta.Loop,
		"easing": &easing, "keyframes": &frames,
	}); err != nil {
		return meta, err
	}
	if schema != companionClipSchema {
		if strings.HasPrefix(schema, companionClipSchemaPrefix) {
			return meta, fmt.Errorf("clip schema %q is newer than this daemon supports", schema)
		}
		return meta, fmt.Errorf("schema must be %s", companionClipSchema)
	}
	if clipKey != key {
		return meta, errors.New("key must match the clip file name")
	}
	if _, ok := obj["duration_s"]; !ok || meta.Duration < companionClipDurationMin || meta.Duration > companionClipDurationMax {
		return meta, fmt.Errorf("duration_s must be between %g and %g", companionClipDurationMin, companionClipDurationMax)
	}
	prop, err := companionOptionalObject(obj, "prop")
	if err != nil {
		return meta, err
	}
	if prop != nil {
		var id string
		if prop.field("id", &id) != nil || !companionVrmKeyID.MatchString(id) {
			return meta, errors.New("prop.id is invalid")
		}
		if err := companionVec3Field(prop, "offset", 1); err != nil {
			return meta, fmt.Errorf("prop: %w", err)
		}
		if err := companionFiniteField(prop, "tilt_deg"); err != nil {
			return meta, fmt.Errorf("prop: %w", err)
		}
	}
	if len(frames) < 1 || len(frames) > companionClipKeyframesMax {
		return meta, fmt.Errorf("keyframes must hold 1 to %d entries", companionClipKeyframesMax)
	}
	last := -1.0
	for i, raw := range frames {
		t, err := validateCompanionKeyframe(raw, meta.Duration)
		if err != nil {
			return meta, fmt.Errorf("keyframes[%d]: %w", i, err)
		}
		if t <= last {
			return meta, fmt.Errorf("keyframes[%d]: t must be strictly ascending", i)
		}
		last = t
	}
	return meta, nil
}

func validateCompanionKeyframe(raw []byte, duration float64) (float64, error) {
	frame, err := parseCompanionObject(raw)
	if err != nil {
		return 0, errors.New("must be an object")
	}
	var (
		t             float64
		label, easing string
		expression    map[string]float64
	)
	if err := frame.fields(map[string]any{"t": &t, "label": &label, "easing": &easing, "expression": &expression}); err != nil {
		return 0, err
	}
	if _, ok := frame["t"]; !ok || !companionFinite(t) || t < 0 || t > duration {
		return 0, errors.New("t must be within [0, duration_s]")
	}
	if err := companionText("label", label, companionClipLabelMax, false); err != nil {
		return 0, err
	}
	if len(expression) > companionClipExprMax {
		return 0, fmt.Errorf("expression supports at most %d entries", companionClipExprMax)
	}
	for _, id := range sortedCompanionKeys(expression) {
		if w := expression[id]; !companionVrmKeyID.MatchString(id) || !companionFinite(w) || w < 0 || w > 1 {
			return 0, fmt.Errorf("expression.%s: invalid ID or weight", id)
		}
	}
	head, err := companionOptionalObject(frame, "head")
	if err != nil {
		return 0, err
	}
	for _, key := range []string{"yaw_deg", "pitch_deg", "roll_deg", "lean_deg", "turn_deg"} {
		if err := companionFiniteField(head, key); err != nil {
			return 0, fmt.Errorf("head: %w", err)
		}
	}
	hands, err := companionOptionalObject(frame, "hands")
	if err != nil {
		return 0, err
	}
	for _, side := range []string{"left", "right"} {
		hand, err := companionOptionalObject(hands, side)
		if err != nil {
			return 0, fmt.Errorf("hands: %w", err)
		}
		if err := validateCompanionHand(hand); err != nil {
			return 0, fmt.Errorf("hands.%s: %w", side, err)
		}
	}
	attach, err := companionOptionalObject(frame, "target_attach")
	if err != nil {
		return 0, err
	}
	for _, slot := range []string{"gaze", "head", "hand_l", "hand_r"} {
		target, err := companionOptionalObject(attach, slot)
		if err != nil {
			return 0, fmt.Errorf("target_attach: %w", err)
		}
		if target == nil {
			continue
		}
		var space string
		if target.field("space", &space) != nil || !validCompanionAttachSpace(space) {
			return 0, fmt.Errorf("target_attach.%s: invalid space", slot)
		}
		if err := companionVec3Field(target, "offset", 1); err != nil {
			return 0, fmt.Errorf("target_attach.%s: %w", slot, err)
		}
		if err := companionNumberField(target, "weight", 0, 1); err != nil {
			return 0, fmt.Errorf("target_attach.%s: %w", slot, err)
		}
	}
	handoff, err := companionOptionalObject(frame, "socket_handoff")
	if err != nil {
		return 0, err
	}
	if handoff != nil {
		var prop, socket string
		if handoff.fields(map[string]any{"prop": &prop, "to_socket": &socket}) != nil ||
			!companionVrmKeyID.MatchString(prop) || !companionVrmKeyID.MatchString(socket) {
			return 0, errors.New("socket_handoff needs a prop and a to_socket ID")
		}
	}
	return t, nil
}

func validateCompanionHand(hand companionObject) error {
	if hand == nil {
		return nil
	}
	if err := companionVec3Field(hand, "ik", 0); err != nil {
		return err
	}
	if err := companionVec3Field(hand, "pole", 0); err != nil {
		return err
	}
	fingers, err := companionOptionalObject(hand, "fingers")
	if err != nil || fingers == nil {
		return err
	}
	var preset string
	if _, ok := fingers["preset"]; ok && (fingers.field("preset", &preset) != nil || !companionClipPresetID.MatchString(preset)) {
		return errors.New("fingers.preset is invalid")
	}
	if _, ok := fingers["curl"]; ok {
		// Values are clamped to [-0.5, 1.8] at playback; they must be finite.
		var curl []float64
		if fingers.field("curl", &curl) != nil || len(curl) != 15 {
			return errors.New("fingers.curl must hold 15 numbers")
		}
	}
	if err := companionNumberField(fingers, "thumb_opp", -0.5, 1.5); err != nil {
		return fmt.Errorf("fingers: %w", err)
	}
	if err := companionNumberField(fingers, "spread", -0.3, 0.5); err != nil {
		return fmt.Errorf("fingers: %w", err)
	}
	return nil
}

// validCompanionAttachSpace accepts free | camera | prop:<id>#<anchor> |
// bone:<humanoidBone>.
func validCompanionAttachSpace(space string) bool {
	switch {
	case space == "free" || space == "camera":
		return true
	case strings.HasPrefix(space, "prop:"):
		id, anchor, ok := strings.Cut(strings.TrimPrefix(space, "prop:"), "#")
		return ok && companionVrmKeyID.MatchString(id) && companionVrmKeyID.MatchString(anchor)
	case strings.HasPrefix(space, "bone:"):
		return companionHumanoidBone.MatchString(strings.TrimPrefix(space, "bone:"))
	}
	return false
}

// validateCompanionClipBytes validates a clip file of either kind.
func validateCompanionClipBytes(entry companionClipEntry, data []byte) (companionClipMeta, error) {
	if entry.Kind == companionClipKindVRMA {
		if len(data) > companionVrmaAssetCap {
			return companionClipMeta{}, errCompanionFileLimit
		}
		return companionClipMeta{}, validateCompanionVRMA(data)
	}
	return validateCompanionClip(data, entry.Key)
}

func companionClipCap(kind string) int {
	if kind == companionClipKindVRMA {
		return companionVrmaAssetCap
	}
	return companionClipFileCap
}

// attachCompanionClips loads every clip the catalog names through read
// (relative to clips/, by key-derived base name only) and records the valid
// ones on the pack. A missing or invalid clip disables that entry only.
// budget bounds the pack's total cached bytes.
func attachCompanionClips(pack *companionPack, budget int, read func(base string, limit int) ([]byte, error)) {
	cat := pack.catalog
	if cat == nil {
		return
	}
	warn := companionClipWarnings{list: pack.info.ClipWarnings}
	clips := map[string][]byte{}
	load := func(e *companionClipEntry) {
		if _, done := clips[e.File]; done {
			e.loaded = true
			return
		}
		limit := min(companionClipCap(e.Kind), budget-pack.size, companionVrmPackCap-pack.size)
		if limit <= 0 {
			warn.add("%s: pack byte limit exceeded; clip disabled", e.File)
			return
		}
		data, err := read(strings.TrimPrefix(e.File, companionClipsDirName+"/"), limit)
		if err != nil {
			warn.add("%s: %s; clip disabled", e.File, err)
			return
		}
		meta, err := validateCompanionClipBytes(*e, data)
		if err != nil {
			warn.add("%s: %s; clip disabled", e.File, err)
			return
		}
		clips[e.File] = data
		pack.size += len(data)
		e.loaded, e.clipLoop = true, meta.Loop
	}
	for _, key := range companionSentinelKeys {
		if e, ok := cat.sentinels[key]; ok {
			load(&e)
			cat.sentinels[key] = e
		}
	}
	for i := range cat.actions {
		load(&cat.actions[i])
	}
	pack.info.ClipWarnings = warn.list
	if len(clips) > 0 {
		pack.clips = clips
		pack.info.Rev = companionPackRevision(pack.manifest, pack.files, pack.assets, clips)
	}
}

// companionPackRevision hashes the raw manifest, then each flat asset in the
// validator's order, then, only when clips loaded, each clip under its
// clips/-prefixed name. A pack without clips keeps the pre-clip revision.
func companionPackRevision(raw []byte, files []string, assets, clips map[string][]byte) string {
	revision := sha256.New()
	hashCompanionPart(revision, raw)
	for _, name := range files {
		hashCompanionPart(revision, []byte(name))
		hashCompanionPart(revision, assets[name])
	}
	for _, name := range sortedCompanionKeys(clips) {
		hashCompanionPart(revision, []byte(name))
		hashCompanionPart(revision, clips[name])
	}
	return hex.EncodeToString(revision.Sum(nil)[:16])
}

// loadCompanionLocalClips opens clips/ pinned under the pack directory and
// reads only the files the catalog names; clips/ is never listed.
func loadCompanionLocalClips(open companionPackOpen, dir companionPackFile, pack *companionPack, budget int) {
	if pack.catalog == nil || len(pack.catalog.entries()) == 0 {
		return
	}
	var clipsDir companionPackFile
	var openErr error
	clipsDir, openErr = open(dir, companionClipsDirName, true)
	if openErr == nil {
		defer clipsDir.Close()
		if !companionDirectory(clipsDir) {
			openErr = errors.New("clips must be a plain directory")
		}
	}
	attachCompanionClips(pack, budget, func(base string, limit int) ([]byte, error) {
		if openErr != nil {
			return nil, errors.New("clips directory is missing or not a plain directory")
		}
		return readCompanionLocalFile(open, clipsDir, base, limit)
	})
}

// companionActionIndices lists every raw actions element with key, in order.
// A hand-edited manifest can repeat a key; the loader keeps only the first,
// so writes replace the first and drop the shadowed rest.
func companionActionIndices(items []json.RawMessage, key string) []int {
	var out []int
	for i, item := range items {
		obj, err := parseCompanionObject(item)
		if err != nil {
			continue
		}
		var k string
		if obj.field("key", &k) == nil && k == key {
			out = append(out, i)
		}
	}
	return out
}

// companionActionsWithout returns items minus the elements at drop
// (ascending indices).
func companionActionsWithout(items []json.RawMessage, drop []int) []json.RawMessage {
	out := make([]json.RawMessage, 0, len(items))
	for i, item := range items {
		if len(drop) > 0 && drop[0] == i {
			drop = drop[1:]
			continue
		}
		out = append(out, item)
	}
	return out
}

// companionOrderedObject is a JSON object read with its key order and the
// exact bytes of every value, so a rewrite changes only the edited values.
type companionOrderedObject struct {
	keys   []string
	values map[string]json.RawMessage
}

func parseCompanionOrderedObject(raw []byte) (*companionOrderedObject, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, errors.New("expected a JSON object")
	}
	out := &companionOrderedObject{values: map[string]json.RawMessage{}}
	for dec.More() {
		tok, err := dec.Token()
		key, ok := tok.(string)
		if err != nil || !ok {
			return nil, errors.New("expected a JSON object key")
		}
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return nil, errors.New("invalid JSON value")
		}
		if _, dup := out.values[key]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		out.keys = append(out.keys, key)
		out.values[key] = value
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, errors.New("expected the end of the JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("unexpected data after the JSON object")
	}
	return out, nil
}

// set replaces or appends key; a nil value removes it.
func (o *companionOrderedObject) set(key string, value json.RawMessage) {
	if value == nil {
		if _, ok := o.values[key]; ok {
			delete(o.values, key)
			for i, k := range o.keys {
				if k == key {
					o.keys = append(o.keys[:i], o.keys[i+1:]...)
					break
				}
			}
		}
		return
	}
	if _, ok := o.values[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.values[key] = value
}

// encode writes the object with one key per line at indent; values keep
// their bytes.
func (o *companionOrderedObject) encode(indent string) []byte {
	var b bytes.Buffer
	if len(o.keys) == 0 {
		return []byte("{}")
	}
	b.WriteString("{\n")
	for i, key := range o.keys {
		name, _ := json.Marshal(key)
		b.WriteString(indent + "  ")
		b.Write(name)
		b.WriteString(": ")
		b.Write(o.values[key])
		if i < len(o.keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent + "}")
	return b.Bytes()
}

// companionEncodeArray writes elements one per line; each keeps its bytes.
func companionEncodeArray(items []json.RawMessage, indent string) []byte {
	if len(items) == 0 {
		return []byte("[]")
	}
	var b bytes.Buffer
	b.WriteString("[\n")
	for i, item := range items {
		b.WriteString(indent + "  ")
		b.Write(item)
		if i < len(items)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent + "]")
	return b.Bytes()
}

// companionIndentValue re-indents a request-supplied JSON value for a nested
// position in pack.json.
func companionIndentValue(raw []byte, prefix string) (json.RawMessage, error) {
	var b bytes.Buffer
	if err := json.Indent(&b, raw, prefix, "  "); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// companionSameJSON compares two JSON values semantically.
func companionSameJSON(a, b []byte) bool {
	var va, vb any
	if json.Unmarshal(a, &va) != nil || json.Unmarshal(b, &vb) != nil {
		return false
	}
	ca, errA := json.Marshal(va)
	cb, errB := json.Marshal(vb)
	return errA == nil && errB == nil && bytes.Equal(ca, cb)
}
