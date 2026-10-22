package launcher

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/marenzo/claudex/internal/config"
)

// ClaudeSettings is derived from config and passed inline with --settings.
type ClaudeSettings struct {
	Env             map[string]string `json:"env"`
	Model           string            `json:"model"`
	EffortLevel     string            `json:"effortLevel"`
	AvailableModels []string          `json:"availableModels"`
	ModelPicker     ClaudeModelPicker `json:"modelPicker"`
	Permissions     struct {
		Deny []string `json:"deny"`
	} `json:"permissions"`
}

type ClaudeModelPicker struct {
	ReplaceBuiltInOptions bool                `json:"replaceBuiltInOptions"`
	Options               []ClaudeModelOption `json:"options"`
}

type ClaudeModelOption struct {
	Model       string `json:"model"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Families maps Claude model families to the GPT model that serves them.
var Families = map[string]string{"FABLE": "astra", "OPUS": "sol", "SONNET": "sol6", "HAIKU": "luna"}

const suffix = " (Codex subscription)"

// Settings derives Claude Code settings from the gateway configuration.
func Settings(cfg config.Config) (ClaudeSettings, error) {
	model, ok := config.FindModel(cfg.Model)
	if !ok {
		return ClaudeSettings{}, fmt.Errorf("unsupported model %q: choose a supported GPT model alias or Claude family", cfg.Model)
	}
	small, _ := config.FindModel(Families["HAIKU"])
	env := map[string]string{
		"ANTHROPIC_BASE_URL":                         "http://" + cfg.Listen,
		"ANTHROPIC_API_KEY":                          "",
		"ANTHROPIC_MODEL":                            model.ID,
		"ANTHROPIC_DEFAULT_MODEL":                    model.ID,
		"ANTHROPIC_CUSTOM_MODEL_OPTION":              model.ID,
		"ANTHROPIC_CUSTOM_MODEL_OPTION_NAME":         model.Name + suffix,
		"ANTHROPIC_CUSTOM_MODEL_OPTION_DESCRIPTION":  "Local Claudex proxy",
		"ANTHROPIC_SMALL_FAST_MODEL":                 small.ID,
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS":             strconv.Itoa(cfg.ContextWindow),
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW":            strconv.Itoa(cfg.CompactWindow),
		"API_TIMEOUT_MS":                             "600000",
	}
	// Client-side auto mode remains available when the experimental server
	// classifier is disabled. This setting does not disable permission checks.
	env["CLAUDE_CODE_AUTO_MODE_SERVER"] = "0"
	if cfg.AutoModeClassifierModel != "" {
		env["CLAUDE_CODE_AUTO_MODE_SERVER"] = "1"
	}
	for family, alias := range Families {
		target, _ := config.FindModel(alias)
		env["ANTHROPIC_DEFAULT_"+family+"_MODEL"] = target.ID
		env["ANTHROPIC_DEFAULT_"+family+"_MODEL_NAME"] = target.Name + suffix
	}
	settings := ClaudeSettings{Env: env, Model: model.ID, EffortLevel: cfg.ReasoningEffort}
	// Gateway discovery adds rows to Claude's built-in lineup. Replace it to
	// avoid image models, unsupported versions and duplicate family/custom rows.
	// These settings apply only to Claude instances launched through Claudex.
	settings.ModelPicker.ReplaceBuiltInOptions = true
	for _, option := range config.Models() {
		settings.AvailableModels = append(settings.AvailableModels, option.ID)
		description := "Codex subscription; --model " + option.Alias
		if option.Alias == "sol6" {
			description = "Previous Sol version; " + description
		}
		settings.ModelPicker.Options = append(settings.ModelPicker.Options, ClaudeModelOption{Model: option.ID, Label: option.Name, Description: description})
	}
	if cfg.ReasoningEffort == "ultra" {
		settings.EffortLevel = "ultracode"
	}
	settings.Permissions.Deny = []string{"Artifact"}
	return settings, nil
}

func encodeJSON(value any) ([]byte, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// RewriteArgs maps friendly model aliases and the ultra effort to the values
// Claude Code understands. Arguments after "--" are left untouched.
func RewriteArgs(args []string) []string {
	out := append([]string(nil), args...)
	for i := 0; i < len(out); i++ {
		arg := out[i]
		switch {
		case arg == "--":
			return out
		case arg == "--model" && i+1 < len(out):
			out[i+1] = canonicalAlias(out[i+1])
			i++
		case strings.HasPrefix(arg, "--model="):
			out[i] = "--model=" + canonicalAlias(strings.TrimPrefix(arg, "--model="))
		case arg == "--effort" && i+1 < len(out) && out[i+1] == "ultra":
			out[i+1] = "ultracode"
			i++
		case arg == "--effort=ultra":
			out[i] = "--effort=ultracode"
		}
	}
	return out
}

// canonicalAlias expands catalog aliases only. Claude
// family names such as "opus" are passed through so Claude Code resolves them.
func canonicalAlias(value string) string {
	for _, model := range config.Models() {
		if model.Alias == strings.ToLower(value) {
			return model.ID
		}
	}
	return value
}
