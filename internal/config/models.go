package config

import "strings"

// Models is a deliberately small, local catalog. Adding another model requires
// verifying its Codex subscription route; no remote catalog updates are run.
type Model struct {
	ID          string
	Name        string
	Alias       string
	Efforts     []string
	UltraEffort string
}

func Models() []Model {
	// Verified against openai/codex 430a167 (2026-10-21), models-manager/models.json.
	// UI-only ultra is normalized separately. Astra and 6.1 Sol reject none.
	levels := []string{"low", "medium", "high", "xhigh", "max"}
	withNone := []string{"none", "low", "medium", "high", "xhigh", "max"}
	return []Model{
		{"gpt-6.1-sol", "GPT-6.1 Sol", "sol", levels, "xhigh"},
		{"gpt-5.6-terra", "GPT-5.6 Terra", "terra", withNone, "max"},
		{"gpt-6-luna", "GPT-6 Luna", "luna", withNone, "max"},
		{"gpt-6-astra", "GPT-6 Astra", "astra", levels, "xhigh"},
		{"gpt-6-sol", "GPT-6 Sol", "sol6", withNone, "max"},
	}
}

func FindModel(id string) (Model, bool) {
	id = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(id)), "[1m]")
	// Claude Code can send a family alias or a versioned Anthropic model ID.
	for family, target := range map[string]string{"haiku": "luna", "sonnet": "sol6", "opus": "sol", "fable": "astra"} {
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
