package config

import "strings"

// Models is a deliberately small, local catalog. Adding another model requires
// verifying its Codex subscription route; no remote catalog updates are run.
type Model struct {
	ID      string
	Name    string
	Alias   string
	Efforts []string
}

func Models() []Model {
	return []Model{
		{"gpt-6-astra", "GPT-6 Astra", "astra", []string{"low", "medium", "high", "xhigh", "max"}},
		{"gpt-5.6-terra", "GPT-5.6 Terra", "terra", []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{"gpt-5.6-sol", "GPT-5.6 Sol", "sol", []string{"none", "low", "medium", "high", "xhigh", "max"}},
		{"gpt-5.6-luna", "GPT-5.6 Luna", "luna", []string{"none", "low", "medium", "high", "xhigh", "max"}},
	}
}

func FindModel(id string) (Model, bool) {
	id = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(id)), "[1m]")
	// Claude Code can send a family alias or a versioned Anthropic model ID.
	for family, target := range map[string]string{"haiku": "luna", "sonnet": "terra", "opus": "sol", "fable": "astra"} {
		if id == family || id == "claude-"+family || strings.HasPrefix(id, "claude-"+family+"-") {
			id = target
			break
		}
	}
	for _, model := range Models() {
		if model.ID == id || model.Alias == id {
			return model, true
		}
	}
	return Model{}, false
}
