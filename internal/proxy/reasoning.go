package proxy

import (
	"fmt"
	"slices"
	"strings"

	"github.com/marenzo/claudex/internal/config"
	"github.com/tidwall/gjson"
)

// Keep policy in the gateway: the translator only converts conversation data.
// Disabled thinking wins over a harness effort. Models without none use low.
func resolveReasoningEffort(root gjson.Result, defaultEffort string, model config.Model) (string, error) {
	if root.Get("thinking.type").String() == "disabled" {
		if slices.Contains(model.Efforts, "none") {
			return "none", nil
		}
		return "low", nil
	}

	effort := defaultEffort
	if explicit := root.Get("output_config.effort"); explicit.Exists() {
		effort = strings.ToLower(strings.TrimSpace(explicit.String()))
	} else if root.Get("thinking.type").String() == "enabled" {
		if budget := root.Get("thinking.budget_tokens"); budget.Exists() {
			if budget.Type != gjson.Number || budget.Num != float64(budget.Int()) || budget.Int() < -1 {
				return "", fmt.Errorf("thinking.budget_tokens must be an integer of at least -1")
			}
			effort = reasoningEffortForBudget(budget.Int())
			if budget.Int() == 0 && !slices.Contains(model.Efforts, "none") {
				effort = "low"
			}
		}
	}
	if effort == "auto" || effort == "" {
		effort = defaultEffort
	}
	switch effort {
	case "ultra", "ultracode":
		// Match the upstream model-owned Ultra normalization. Astra and 6.1 Sol
		// use xhigh; models without an override fall back to max.
		effort = model.UltraEffort
	case "minimal":
		// None of the supported models exposes minimal; preserve the request's
		// low-effort intent using the lowest reasoning-enabled level.
		effort = "low"
	}
	if !slices.Contains(model.Efforts, effort) {
		return "", fmt.Errorf("reasoning effort %q is unsupported for %s", effort, model.ID)
	}
	return effort, nil
}

func reasoningEffortForBudget(budget int64) string {
	switch {
	case budget == -1:
		return "auto"
	case budget == 0:
		return "none"
	case budget <= 1024:
		return "low"
	case budget <= 8192:
		return "medium"
	case budget <= 24576:
		return "high"
	default:
		return "xhigh"
	}
}
