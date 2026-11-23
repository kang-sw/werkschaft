package werk

import (
	"encoding/json"
	"fmt"
	"strings"
)

const loadSkillTool = "load_skill"

// Skill is one indexed skill. Load opens the body only when the model asks.
type Skill struct {
	Name        string
	Description string
	Load        func() (string, error)
}

var skillToolDefinition = ToolDef{
	Name:        loadSkillTool,
	Description: "Load the full instructions for an available skill by its exact name.",
	Parameters:  json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}`),
}

func skillInstructions(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nAvailable skills. Use load_skill with the exact name to read a skill before following it.\n")
	for _, skill := range skills {
		fmt.Fprintf(&b, "- %s: %s\n", skill.Name, strings.Join(strings.Fields(skill.Description), " "))
	}
	return b.String()
}

func (a *Agent) loadSkill(args json.RawMessage) (string, bool) {
	var request struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(args, &request); err != nil || request.Name == "" {
		return "Error: load_skill needs an exact skill name.", true
	}
	skill, ok := a.skills[request.Name]
	if !ok {
		return "Error: no available skill named " + request.Name, true
	}
	body, err := skill.Load()
	if err != nil {
		return "Error: the indexed skill could not be loaded.", true
	}
	return body, false
}
