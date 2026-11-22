package wsdashboard

import (
	"encoding/json"
	"fmt"
)

func (c companionManifestCheck) motionRef(name string) error {
	switch name {
	case "wave-pivot", "bob-pivot", "sway-pivot", "breathe":
		return nil
	}
	if name == "" || c.motions[name] == nil {
		return fmt.Errorf("motion refers to an undefined track")
	}
	return nil
}

func (c companionManifestCheck) layerMotion(raw json.RawMessage) error {
	if len(raw) != 0 && raw[0] == '"' {
		var ref string
		if err := json.Unmarshal(raw, &ref); err != nil {
			return err
		}
		return c.motionRef(ref)
	}
	obj, err := parseCompanionObject(raw)
	if err != nil {
		return err
	}
	return c.motion(obj, true)
}

func (c companionManifestCheck) motion(obj companionObject, allowRef bool) error {
	var shape string
	for _, key := range []string{"use", "oscillate", "keyframes"} {
		if obj[key] != nil {
			if shape != "" {
				return fmt.Errorf("motion must choose one track shape")
			}
			shape = key
		}
	}
	if (shape != "use" && obj["phase"] != nil) || (shape != "keyframes" && obj["loop"] != nil) {
		return fmt.Errorf("motion option does not match the track shape")
	}
	switch shape {
	case "use":
		if !allowRef {
			return fmt.Errorf("named motion must define a track")
		}
		var ref string
		var phase float64
		if err := obj.fields(map[string]any{"use": &ref, "phase": &phase}); err != nil {
			return err
		}
		return c.motionRef(ref)
	case "oscillate":
		var track companionObject
		if err := obj.field("oscillate", &track); err != nil {
			return err
		}
		return validateCompanionOscillation(track)
	case "keyframes":
		var keys []companionObject
		var loop bool
		if err := obj.fields(map[string]any{"keyframes": &keys, "loop": &loop}); err != nil {
			return err
		}
		return validateCompanionKeyframes(keys)
	default:
		return fmt.Errorf("motion requires a reference, oscillation or keyframes")
	}
}

var companionTransformFields = []string{"rotDeg", "tx", "ty", "sx", "sy"}

func validateCompanionOscillation(track companionObject) error {
	var hz, phase float64
	var ease string
	if err := track.fields(map[string]any{"hz": &hz, "phase": &phase, "ease": &ease}); err != nil {
		return err
	}
	if hz <= 0 {
		return fmt.Errorf("oscillate.hz must be positive")
	}
	if ease != "" && ease != "sine" && ease != "easeInOut" && ease != "linear" {
		return fmt.Errorf("invalid oscillate.ease")
	}
	for _, key := range companionTransformFields {
		if track[key] == nil {
			continue
		}
		var pair []*float64
		if err := track.field(key, &pair); err != nil || len(pair) != 2 || pair[0] == nil || pair[1] == nil {
			return fmt.Errorf("oscillate.%s requires a pair of numbers", key)
		}
	}
	return nil
}

func validateCompanionKeyframes(keys []companionObject) error {
	if len(keys) == 0 {
		return fmt.Errorf("keyframes must contain at least one key")
	}
	var previous float64
	for _, key := range keys {
		var t float64
		if err := key.field("t", &t); err != nil || key["t"] == nil || t < previous {
			return fmt.Errorf("keyframe times must be nonnegative and in order")
		}
		previous = t
		for _, name := range companionTransformFields {
			var value float64
			if err := key.field(name, &value); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCompanionScenario(obj companionObject) error {
	var disabled bool
	var when companionObject
	var steps []companionObject
	var priority, cooldown, duration float64
	if err := obj.fields(map[string]any{
		"disabled": &disabled, "when": &when, "steps": &steps,
		"priority": &priority, "cooldownMs": &cooldown, "durationMs": &duration,
	}); err != nil {
		return err
	}
	if cooldown < 0 || duration < 0 {
		return fmt.Errorf("scenario durations must be nonnegative")
	}
	if on, ok := obj["on"]; ok {
		var event string
		var events []*string
		if on[0] == '"' {
			if err := obj.field("on", &event); err != nil || event == "" {
				return fmt.Errorf("scenario on must name an event")
			}
		} else {
			if err := obj.field("on", &events); err != nil || len(events) == 0 {
				return fmt.Errorf("scenario on must name events")
			}
			for _, event := range events {
				if event == nil || *event == "" {
					return fmt.Errorf("scenario on must name events")
				}
			}
		}
	}
	if err := validateCompanionGuards(when); err != nil {
		return err
	}
	if obj["steps"] != nil && len(steps) == 0 {
		return fmt.Errorf("scenario steps must contain at least one step")
	}
	for _, step := range steps {
		var ms float64
		if err := step.field("ms", &ms); err != nil || step["ms"] == nil || ms < 0 {
			return fmt.Errorf("scenario step ms must be nonnegative")
		}
		if err := validateCompanionBeat(step); err != nil {
			return err
		}
	}
	return validateCompanionBeat(obj)
}

func validateCompanionBeat(obj companionObject) error {
	for _, key := range []string{"pose", "gesture", "expression", "gaze", "stage", "quip"} {
		var value string
		if err := obj.field(key, &value); err != nil {
			return err
		}
	}
	for _, key := range []string{"prop", "fx"} {
		if raw := obj[key]; len(raw) != 0 && raw[0] != '"' {
			if _, err := parseCompanionObject(raw); err != nil {
				return fmt.Errorf("scenario %s must be an ID or an object", key)
			}
		}
	}
	return nil
}

func validateCompanionGuards(when companionObject) error {
	for _, key := range sortedCompanionKeys(when) {
		switch key {
		case "streak", "windowMs", "idleMs":
			var value int
			if err := when.field(key, &value); err != nil || value < 0 {
				return fmt.Errorf("when.%s must be a nonnegative integer", key)
			}
		case "hourRange":
			var hours []*int
			if err := when.field(key, &hours); err != nil || len(hours) != 2 ||
				hours[0] == nil || hours[1] == nil || *hours[0] < 0 || *hours[0] > 23 || *hours[1] < 0 || *hours[1] > 24 {
				return fmt.Errorf("when.hourRange must contain two hours")
			}
		case "routeKind", "pose":
			var value string
			if err := when.field(key, &value); err != nil || value == "" {
				return fmt.Errorf("when.%s must be a nonempty string", key)
			}
		default:
			return fmt.Errorf("unknown scenario guard %q", key)
		}
	}
	return nil
}
