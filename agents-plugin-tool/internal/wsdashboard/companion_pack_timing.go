package wsdashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
)

var companionTimingID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

type companionTimingLint struct {
	issues  []string
	easings map[string]bool
}
type companionTimingBound struct {
	name     string
	min, max float64
}

func companionTimingNumber(v any) (float64, bool) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, false
	}
	f, err := n.Float64()
	return f, err == nil
}

func (l *companionTimingLint) issue(path, message string) {
	l.issues = append(l.issues, path+": "+message)
}
func (l *companionTimingLint) object(v any, path string) map[string]any {
	o, ok := v.(map[string]any)
	if !ok {
		l.issue(path, "must be an object")
		return nil
	}
	return o
}
func (l *companionTimingLint) number(o map[string]any, path string, b companionTimingBound, required bool) (float64, bool) {
	v, exists := o[b.name]
	if !exists && !required {
		return 0, false
	}
	n, ok := companionTimingNumber(v)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < b.min || n > b.max {
		l.issue(path+"."+b.name, fmt.Sprintf("must be a number in [%g, %g]", b.min, b.max))
		return 0, false
	}
	return n, true
}
func (l *companionTimingLint) bounds(o map[string]any, path string, bounds []companionTimingBound) {
	for _, b := range bounds {
		l.number(o, path, b, false)
	}
}
func (l *companionTimingLint) tuple(v any, path string, limits [][2]float64) bool {
	a, ok := v.([]any)
	if !ok || len(a) != len(limits) {
		l.issue(path, fmt.Sprintf("must contain %d numbers", len(limits)))
		return false
	}
	valid := true
	for i, b := range limits {
		n, ok := companionTimingNumber(a[i])
		if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < b[0] || n > b[1] {
			l.issue(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("must be a number in [%g, %g]", b[0], b[1]))
			valid = false
		}
	}
	return valid
}
func (l *companionTimingLint) ease(v any, path string) {
	if s, ok := v.(string); ok {
		if !l.easings[s] {
			l.issue(path, "must name a valid easing")
		}
		return
	}
	l.tuple(v, path, [][2]float64{{0, 1}, {-4, 4}, {0, 1}, {-4, 4}})
}
func (l *companionTimingLint) frames(v any, path string, normalized bool, bounds []companionTimingBound) {
	frames, ok := v.([]any)
	if !ok || (normalized && (len(frames) < 1 || len(frames) > 64)) {
		l.issue(path, "must be an array of 1 to 64 keyframes")
		return
	}
	previous := 0.0
	for i, v := range frames {
		p := fmt.Sprintf("%s[%d]", path, i)
		o := l.object(v, p)
		if o == nil {
			continue
		}
		if normalized {
			n, ok := l.number(o, p, companionTimingBound{"p", 0, 1}, true)
			if ok {
				if n < previous {
					l.issue(p+".p", "must be nondecreasing")
				}
				previous = n
			}
		}
		l.bounds(o, p, bounds)
		if ease, ok := o["ease"]; ok {
			l.ease(ease, p+".ease")
		}
	}
}
func (l *companionTimingLint) dictionary(root map[string]any, key string, f func(string, any)) {
	v, ok := root[key]
	if !ok {
		return
	}
	o := l.object(v, key)
	for _, id := range sortedCompanionKeys(o) {
		path := key + "." + id
		if !companionTimingID.MatchString(id) {
			l.issue(path, "invalid identifier")
		}
		f(path, o[id])
	}
}
func (l *companionTimingLint) motion(v any, path string) {
	o, ok := v.(map[string]any)
	if !ok {
		return
	}
	if frames, ok := o["keyframes"]; ok {
		l.frames(frames, path+".keyframes", false, nil)
	}
}
func (l *companionTimingLint) swing(o map[string]any, path string) {
	l.bounds(o, path, []companionTimingBound{{"swingWeight", -2, 2}, {"swingLag", -.5, .5}})
}
func (l *companionTimingLint) beat(o map[string]any, path string) {
	if p, ok := o["prop"].(map[string]any); ok {
		if v, ok := p["motion"]; ok {
			if s, ok := v.(string); !ok || !companionTimingID.MatchString(s) {
				l.issue(path+".prop.motion", "must be a safe identifier")
			}
		}
	}
}

// lintCompanionTiming is advisory only; the safety validator preserves opaque extensions.
func lintCompanionTiming(raw []byte) []string {
	var root map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !json.Valid(raw) {
		return []string{"manifest: must be a JSON object"}
	}
	if err := decoder.Decode(&root); err != nil || root == nil {
		return []string{"manifest: must be a JSON object"}
	}
	l := companionTimingLint{easings: map[string]bool{"linear": true, "easeIn": true, "easeOut": true, "easeInOut": true, "anticipate": true, "overshoot": true, "snapBounce": true}}
	l.dictionary(root, "easings", func(path string, v any) {
		if l.tuple(v, path, [][2]float64{{0, 1}, {-4, 4}, {0, 1}, {-4, 4}}) {
			l.easings[path[len("easings."):]] = true
		}
	})
	stage := []companionTimingBound{{"lift", -2, 2}, {"tx", -2, 2}, {"sx", .1, 3}, {"sy", .1, 3}, {"rotDeg", -180, 180}}
	prop := []companionTimingBound{{"along", -2, 3}, {"arcY", -3, 3}, {"offsetX", -3, 3}, {"offsetY", -3, 3}, {"scale", 0, 4}, {"rotDeg", -720, 720}, {"alpha", 0, 1}}
	for _, entry := range []struct {
		name   string
		bounds []companionTimingBound
	}{{"stageEffects", stage}, {"propMotions", prop}} {
		l.dictionary(root, entry.name, func(path string, v any) {
			o := l.object(v, path)
			if o == nil {
				return
			}
			l.frames(o["keyframes"], path+".keyframes", true, entry.bounds)
			if entry.name == "propMotions" {
				if edge, ok := o["edge"]; ok && edge != "away" && edge != "left" && edge != "right" {
					l.issue(path+".edge", "must be away, left, or right")
				}
				if v, ok := o["recoil"]; ok {
					r := l.object(v, path+".recoil")
					if r != nil {
						rp := path + ".recoil"
						a, ao := l.number(r, rp, companionTimingBound{"start", 0, 1}, true)
						b, bo := l.number(r, rp, companionTimingBound{"peak", 0, 1}, true)
						c, co := l.number(r, rp, companionTimingBound{"end", 0, 1}, true)
						if ao && bo && co && !(a < b && b < c) {
							l.issue(rp, "requires start < peak < end")
						}
						l.bounds(r, rp, []companionTimingBound{{"lift", -1, 1}, {"angle", -1.5, 1.5}})
					}
				}
			}
		})
	}
	if v, ok := root["physics"]; ok {
		o := l.object(v, "physics")
		if v, ok := o["drag"]; ok {
			d := l.object(v, "physics.drag")
			l.bounds(d, "physics.drag", []companionTimingBound{{"stiffness", 5, 300}, {"damping", .5, 40}, {"impulse", 0, .5}, {"maxDeg", 0, 45}, {"bodyFallbackScale", 0, 1}})
		}
	}
	if v, ok := root["head"]; ok {
		if o, ok := v.(map[string]any); ok {
			l.swing(o, "head")
			l.bounds(o, "head", []companionTimingBound{{"gazeTiltDeg", 0, 30}, {"propGazeTiltDeg", 0, 30}})
		}
	}
	if hairs, ok := root["hairs"].(map[string]any); ok {
		for _, id := range sortedCompanionKeys(hairs) {
			if h, ok := hairs[id].(map[string]any); ok {
				if back, ok := h["back"].(map[string]any); ok {
					l.swing(back, "hairs."+id+".back")
				}
			}
		}
	}
	canvas, _ := root["canvas"].([]any)
	for _, name := range []string{"poses", "gestures"} {
		groups, _ := root[name].(map[string]any)
		for _, id := range sortedCompanionKeys(groups) {
			o, ok := groups[id].(map[string]any)
			if !ok {
				continue
			}
			path := name + "." + id
			if a, ok := o["handAnchor"]; ok {
				if len(canvas) == 2 {
					w, wok := companionTimingNumber(canvas[0])
					h, hok := companionTimingNumber(canvas[1])
					if wok && hok {
						l.tuple(a, path+".handAnchor", [][2]float64{{0, w}, {0, h}})
					}
				}
			}
			layers, _ := o["layers"].([]any)
			for i, v := range layers {
				if layer, ok := v.(map[string]any); ok {
					lp := fmt.Sprintf("%s.layers[%d]", path, i)
					l.swing(layer, lp)
					l.motion(layer["motion"], lp+".motion")
				}
			}
		}
	}
	if motions, ok := root["motions"].(map[string]any); ok {
		for _, id := range sortedCompanionKeys(motions) {
			l.motion(motions[id], "motions."+id)
		}
	}
	if scenarios, ok := root["scenarios"].(map[string]any); ok {
		for _, id := range sortedCompanionKeys(scenarios) {
			if o, ok := scenarios[id].(map[string]any); ok {
				path := "scenarios." + id
				l.beat(o, path)
				steps, _ := o["steps"].([]any)
				for i, v := range steps {
					if step, ok := v.(map[string]any); ok {
						l.beat(step, fmt.Sprintf("%s.steps[%d]", path, i))
					}
				}
			}
		}
	}
	if v, ok := root["passive"]; ok {
		states := l.object(v, "passive")
		allowed := map[string]bool{"idle": true, "doze": true, "user_typing": true, "typing_user": true, "thinking": true, "tool": true, "replying": true, "streaming": true, "permission": true, "error": true}
		for _, id := range sortedCompanionKeys(states) {
			path := "passive." + id
			if !allowed[id] {
				l.issue(path, "unknown passive state")
			}
			o := l.object(states[id], path)
			for _, key := range []string{"pose", "gesture", "expression", "gaze", "stage", "fx", "prop"} {
				if v, ok := o[key]; ok {
					s, ok := v.(string)
					if !ok || !(companionTimingID.MatchString(s) || (key == "gaze" && len(s) > 5 && s[:5] == "prop:" && companionTimingID.MatchString(s[5:]))) {
						l.issue(path+"."+key, "must be a safe string")
					}
				}
			}
		}
	}
	sort.Strings(l.issues)
	return l.issues
}
