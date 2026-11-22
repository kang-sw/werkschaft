package wsdashboard

import (
	"fmt"
	"math"
	"regexp"
)

var companionBoneID = regexp.MustCompile(`^[a-z][a-zA-Z0-9_-]{0,31}$`)

// companionBodyLayerZ is the z an outfit column's body layer takes when it
// names none, matching the compositor.
const companionBodyLayerZ = 20

// lintDeform reports advisory mesh-deform findings for one outfit column's
// optional `deform` field: bone IDs, pivots and roll pivot inside the canvas,
// regions of at least 3 points, resolvable parents without cycles, grid
// values within 2..32, resolvable named motions, and a rigid layer whose z
// lies between the column's static layers (the compositor draws the meshed
// canvas at their lowest z, so such a layer would move above them). It never
// changes a validation result.
func (l *companionKitLint) deform(path string, column, root map[string]any) {
	spec, ok := column["deform"].(map[string]any)
	if !ok {
		return
	}
	columnPath := path
	path += ".deform"
	canvas, hasCanvas := companionPoint(root["canvas"])
	inside := func(p [2]float64) bool {
		return !hasCanvas || p[0] >= 0 && p[0] <= canvas[0] && p[1] >= 0 && p[1] <= canvas[1]
	}
	motions, _ := root["motions"].(map[string]any)
	resolves := func(v any) bool {
		name, _ := v.(string)
		if obj, isObj := v.(map[string]any); isObj {
			name, _ = obj["use"].(string)
		}
		switch name {
		case "", "wave-pivot", "bob-pivot", "sway-pivot", "breathe":
			return true
		}
		return motions[name] != nil
	}
	if grid, present := spec["grid"]; present {
		values, _ := grid.([]any)
		if len(values) != 2 {
			l.issue(path+".grid", "must be [columns, rows]")
		}
		for i, v := range values {
			if n, isNum := companionTimingNumber(v); !isNum || n < 2 || n > 32 || n != math.Trunc(n) {
				l.issue(fmt.Sprintf("%s.grid[%d]", path, i), "must be an integer within 2..32")
			}
		}
	}
	bones, _ := spec["bones"].(map[string]any)
	for _, id := range sortedCompanionKeys(bones) {
		bonePath := path + ".bones." + id
		bone, _ := bones[id].(map[string]any)
		if !companionBoneID.MatchString(id) {
			l.issue(bonePath, "ID must match ^[a-z][a-zA-Z0-9_-]{0,31}$")
		}
		if pivot, ok := companionPoint(bone["pivot"]); !ok || !inside(pivot) {
			l.issue(bonePath+".pivot", "must be a point inside the canvas")
		}
		if region, _ := bone["region"].([]any); len(region) < 3 {
			l.issue(bonePath+".region", "needs at least 3 points")
		}
		if parent, has := bone["parent"]; has {
			name, _ := parent.(string)
			if bones[name] == nil {
				l.issue(bonePath+".parent", fmt.Sprintf("refers to undefined bone %q", name))
			} else if companionBoneCycle(bones, id) {
				l.issue(bonePath+".parent", "parent chain loops back to this bone")
			}
		}
		if v, has := bone["motion"]; has && !resolves(v) {
			l.issue(bonePath+".motion", "refers to an undefined motion")
		}
	}
	if roll, has := spec["roll"]; has {
		obj, _ := roll.(map[string]any)
		if v, hasMotion := obj["motion"]; hasMotion && !resolves(v) {
			l.issue(path+".roll.motion", "refers to an undefined motion")
		}
		if v, hasPivot := obj["pivot"]; hasPivot {
			if pivot, ok := companionPoint(v); !ok || !inside(pivot) {
				l.issue(path+".roll.pivot", "must be a point inside the canvas")
			}
		}
	}
	l.rigidBetween(columnPath, column)
}

// companionBoneCycle reports whether following parent links from id returns to it.
func companionBoneCycle(bones map[string]any, id string) bool {
	at := id
	for steps := 0; steps <= len(bones); steps++ {
		bone, _ := bones[at].(map[string]any)
		parent, _ := bone["parent"].(string)
		if parent == "" || bones[parent] == nil {
			return false
		}
		if parent == id {
			return true
		}
		at = parent
	}
	return false
}

// rigidBetween warns on a layer with frames, a motion track or drag inertia
// whose z lies strictly between the lowest and highest z of the column's
// static layers.
func (l *companionKitLint) rigidBetween(columnPath string, column map[string]any) {
	layers, _ := column["layers"].([]any)
	type item struct {
		index int
		z     float64
	}
	var static, rigid []item
	for i, v := range layers {
		layer, _ := v.(map[string]any)
		z := float64(companionBodyLayerZ)
		if n, ok := companionTimingNumber(layer["z"]); ok {
			z = n
		}
		frames, _ := layer["frames"].([]any)
		switch {
		case layer["mode"] == "head-egg":
		case len(frames) > 0 || layer["motion"] != nil || layer["swingWeight"] != nil:
			rigid = append(rigid, item{i, z})
		default:
			static = append(static, item{i, z})
		}
	}
	if len(static) < 2 {
		return
	}
	low, high := static[0].z, static[0].z
	for _, s := range static {
		low, high = math.Min(low, s.z), math.Max(high, s.z)
	}
	for _, r := range rigid {
		if r.z > low && r.z < high {
			l.issue(fmt.Sprintf("%s.layers[%d]", columnPath, r.index),
				"moves between the static layers of a deform column; the meshed canvas draws at their lowest z, so this layer would draw above them")
		}
	}
}
