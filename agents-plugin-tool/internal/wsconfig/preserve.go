package wsconfig

import (
	"encoding/json"
	"unicode"
	"unicode/utf8"
)

// preserveConfigMembers projects the edited known fields onto the original
// object. Agent tiers are typed, so preserve future fields inside the
// agents object and beside the fields of each surviving tier leaf.
func preserveConfigMembers(original []byte, cfg Config) ([]byte, error) {
	var members map[string]json.RawMessage
	if len(original) > 0 {
		if err := json.Unmarshal(original, &members); err != nil {
			return nil, err
		}
	}
	if members == nil {
		members = make(map[string]json.RawMessage)
	}
	known, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var edited map[string]json.RawMessage
	if err := json.Unmarshal(known, &edited); err != nil {
		return nil, err
	}
	agents, err := preserveAgentMembers(foldedMember(members, "agents"), edited["agents"])
	if err != nil {
		return nil, err
	}
	edited["agents"] = agents
	for key := range members {
		if jsonNameIs(key, "schema_version", "agents", "overrides", "structured_overrides") {
			delete(members, key)
		}
	}
	for key, value := range edited {
		members[key] = value
	}
	return json.MarshalIndent(members, "", "  ")
}

func preserveAgentMembers(original, edited json.RawMessage) (json.RawMessage, error) {
	return preserveObject(original, edited, 0)
}

func preserveObject(original, edited json.RawMessage, depth int) (json.RawMessage, error) {
	before, after := map[string]json.RawMessage{}, map[string]json.RawMessage{}
	if len(original) > 0 {
		if err := json.Unmarshal(original, &before); err != nil {
			return nil, err
		}
	}
	if err := json.Unmarshal(edited, &after); err != nil {
		return nil, err
	}
	switch depth {
	case 0:
		for key, value := range before {
			if !jsonNameIs(key, "tiers", "model_aliases") {
				after[key] = value
			}
		}
		for key, value := range after {
			if key != "tiers" && key != "model_aliases" {
				continue
			}
			next := 2
			if key == "model_aliases" {
				next = 1
			}
			merged, err := preserveObject(foldedMember(before, key), value, next)
			if err != nil {
				return nil, err
			}
			after[key] = merged
		}
	case 1, 2:
		for key, value := range after {
			merged, err := preserveObject(before[key], value, depth+1)
			if err != nil {
				return nil, err
			}
			after[key] = merged
		}
	case 3:
		for key, value := range before {
			if !jsonNameIs(key, "backend", "model", "effort") {
				after[key] = value
			}
		}
	}
	return json.Marshal(after)
}

// jsonNameIs reports whether the object member key decodes into a struct
// field named one of names. encoding/json matches member names to field
// names case-insensitively (jsonFoldName), so a differently cased spelling of
// a known field is that field, not an unknown member to keep beside the
// canonical one: kept, the next load would merge it back.
func jsonNameIs(key string, names ...string) bool {
	folded := jsonFoldName(key)
	for _, name := range names {
		if folded == jsonFoldName(name) {
			return true
		}
	}
	return false
}

// foldedMember is the member the decoder reads for the field name: the exact
// spelling when present, else a differently cased one.
func foldedMember(members map[string]json.RawMessage, name string) json.RawMessage {
	if value, ok := members[name]; ok {
		return value
	}
	for key, value := range members {
		if jsonNameIs(key, name) {
			return value
		}
	}
	return nil
}

// jsonFoldName folds name as encoding/json's appendFoldedName does: ASCII
// letters upper-cased, other runes to the smallest rune of their simple
// fold set (so U+017F long s matches s, U+212A Kelvin sign matches k).
func jsonFoldName(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); {
		if c := name[i]; c < utf8.RuneSelf {
			if 'a' <= c && c <= 'z' {
				c -= 'a' - 'A'
			}
			out = append(out, c)
			i++
			continue
		}
		r, n := utf8.DecodeRuneInString(name[i:])
		for {
			r2 := unicode.SimpleFold(r)
			if r2 <= r {
				r = r2
				break
			}
			r = r2
		}
		out = utf8.AppendRune(out, r)
		i += n
	}
	return string(out)
}
