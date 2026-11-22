package wsdashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
)

// companionSlotID is the attachment slot pattern the wardrobe config key
// companion_attachments accepts; attachment, outfit, hair and eye-set IDs use
// companionPackID.
var companionSlotID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// companionRegistrationTolerance is the default registration drift, in
// canvas pixels, when the pack's registration names none.
const companionRegistrationTolerance = 4

type companionKitLint struct{ issues []string }

func (l *companionKitLint) issue(path, message string) {
	l.issues = append(l.issues, path+": "+message)
}

// lintCompanionKit is the advisory paper-doll lint. It parses the manifest on
// its own, so it also reports a manifest the validator refuses, and it never
// changes a validation result. It reports:
//   - a legacy poses/gestures layer or frame whose only rasters are fills,
//     which an older daemon rejects (the legacy projection must use v1 keys);
//   - an outfit that binds no stand column;
//   - an outfit column with no top-level poses/gestures entry;
//   - column headAnchor/feetPivot drift from the pack registration;
//   - outfit, hair, eye-set and attachment IDs (and attachment slots) the
//     wardrobe config keys cannot select, and the reserved attachment ID none;
//   - an outfit column's mesh `deform` field (see lintDeform) and a deform
//     on a registry column, where the engine ignores it.
func lintCompanionKit(raw []byte) []string {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid(raw) || decoder.Decode(&root) != nil || root == nil {
		return []string{"manifest: must be a JSON object"}
	}
	l := &companionKitLint{}
	top := map[string]map[string]any{}
	for _, section := range []string{"poses", "gestures"} {
		groups, _ := root[section].(map[string]any)
		top[section] = groups
		for _, id := range sortedCompanionKeys(groups) {
			group, _ := groups[id].(map[string]any)
			if group["deform"] != nil {
				l.issue(fmt.Sprintf("%s.%s.deform", section, id), "is ignored on a registry column; declare it on the outfit column")
			}
			layers, _ := group["layers"].([]any)
			for i, v := range layers {
				layer, _ := v.(map[string]any)
				path := fmt.Sprintf("%s.%s.layers[%d]", section, id, i)
				if layer["frames"] == nil && companionFillsOnly(layer) {
					l.issue(path, "carries only fills; an older daemon rejects a legacy layer without a v1 raster key")
				}
				frames, _ := layer["frames"].([]any)
				for j, f := range frames {
					if frame, ok := f.(map[string]any); ok && companionFillsOnly(frame) {
						l.issue(fmt.Sprintf("%s.frames[%d]", path, j), "carries only fills; an older daemon rejects a legacy frame without a v1 raster key")
					}
				}
			}
		}
	}

	registration, hasRegistration := companionRegistration(root["registration"])
	outfits, _ := root["outfits"].(map[string]any)
	for _, id := range sortedCompanionKeys(outfits) {
		path := "outfits." + id
		if !companionPackID.MatchString(id) {
			l.issue(path, "ID cannot be selected by dashboard.companion_outfit")
		}
		outfit, _ := outfits[id].(map[string]any)
		poses, _ := outfit["poses"].(map[string]any)
		if !companionColumnBound(poses["stand"]) {
			l.issue(path, "binds no stand column")
		}
		for _, section := range []string{"poses", "gestures"} {
			columns, _ := outfit[section].(map[string]any)
			for _, column := range sortedCompanionKeys(columns) {
				columnPath := path + "." + section + "." + column
				registry, known := top[section][column]
				if !known {
					l.issue(columnPath, "has no top-level "+section+" entry, the column registry")
				}
				if hasRegistration {
					entry, _ := columns[column].(map[string]any)
					inherited, _ := registry.(map[string]any)
					// Only the stand column is compared on both axes: other
					// columns anchor lower or hang by design.
					l.drift(columnPath, entry, inherited, registration, section == "poses" && column == "stand")
				}
				entry, _ := columns[column].(map[string]any)
				l.deform(columnPath, entry, root)
			}
		}
	}

	for _, entry := range []struct{ section, key string }{
		{"hairs", "dashboard.companion_hair"}, {"eyesets", "dashboard.companion_eyes"},
	} {
		values, _ := root[entry.section].(map[string]any)
		for _, id := range sortedCompanionKeys(values) {
			if !companionPackID.MatchString(id) {
				l.issue(entry.section+"."+id, "ID cannot be selected by "+entry.key)
			}
		}
	}
	attachments, _ := root["attachments"].(map[string]any)
	for _, id := range sortedCompanionKeys(attachments) {
		path := "attachments." + id
		switch {
		case id == "none":
			l.issue(path, "ID none is reserved to clear a slot")
		case !companionPackID.MatchString(id):
			l.issue(path, "ID cannot be selected by dashboard.companion_attachments")
		}
		attachment, _ := attachments[id].(map[string]any)
		if slot, ok := attachment["slot"].(string); ok && !companionSlotID.MatchString(slot) {
			l.issue(path+".slot", "slot cannot be selected by dashboard.companion_attachments")
		}
	}
	sort.Strings(l.issues)
	return l.issues
}

type companionRegistrationRef struct {
	head, feet       [2]float64
	hasHead, hasFeet bool
	tolerance        float64
}

func companionRegistration(v any) (companionRegistrationRef, bool) {
	o, ok := v.(map[string]any)
	if !ok {
		return companionRegistrationRef{}, false
	}
	ref := companionRegistrationRef{tolerance: companionRegistrationTolerance}
	ref.head, ref.hasHead = companionPoint(o["headAnchor"])
	ref.feet, ref.hasFeet = companionPoint(o["feetPivot"])
	if n, ok := companionTimingNumber(o["tolerance"]); ok && n >= 0 && !math.IsInf(n, 0) {
		ref.tolerance = n
	}
	return ref, ref.hasHead || ref.hasFeet
}

// drift compares a column's resolved anchors with the registration. A field
// the outfit entry omits is inherited from the top-level entry, as the
// compositor resolves it.
func (l *companionKitLint) drift(path string, entry, inherited map[string]any, ref companionRegistrationRef, bothAxes bool) {
	for _, field := range []struct {
		name string
		want [2]float64
		has  bool
	}{{"headAnchor", ref.head, ref.hasHead}, {"feetPivot", ref.feet, ref.hasFeet}} {
		if !field.has {
			continue
		}
		value, own := entry[field.name]
		if !own {
			value = inherited[field.name]
		}
		got, ok := companionPoint(value)
		if !ok {
			continue
		}
		axes := 1
		if bothAxes {
			axes = 2
		}
		for axis := 0; axis < axes; axis++ {
			if d := math.Abs(got[axis] - field.want[axis]); d > ref.tolerance {
				l.issue(fmt.Sprintf("%s.%s[%d]", path, field.name, axis),
					fmt.Sprintf("drifts %g px from registration (tolerance %g)", d, ref.tolerance))
			}
		}
	}
}

func companionPoint(v any) ([2]float64, bool) {
	a, ok := v.([]any)
	if !ok || len(a) != 2 {
		return [2]float64{}, false
	}
	x, xok := companionTimingNumber(a[0])
	y, yok := companionTimingNumber(a[1])
	if !xok || !yok || math.IsNaN(x) || math.IsInf(x, 0) || math.IsNaN(y) || math.IsInf(y, 0) {
		return [2]float64{}, false
	}
	return [2]float64{x, y}, true
}

// companionColumnBound mirrors the compositor: an outfit column is bound only
// when its entry carries a non-empty layers list.
func companionColumnBound(v any) bool {
	entry, _ := v.(map[string]any)
	layers, _ := entry["layers"].([]any)
	return len(layers) > 0
}

// companionFillsOnly reports a layer or frame that names fills but none of the
// v1 raster keys an older daemon requires.
func companionFillsOnly(obj map[string]any) bool {
	fills, _ := obj["fills"].(map[string]any)
	if len(fills) == 0 {
		return false
	}
	for _, key := range companionLayerAssets {
		if obj[key] != nil {
			return false
		}
	}
	return true
}
