package proxy

import (
	"strings"

	"github.com/tidwall/gjson"
)

const (
	// Token accounting is not evidence that any usable response was delivered.
	CodexEmptyIncompleteStreamMessage = "Codex terminated with an incomplete empty response"
)

// HasMeaningfulCodexOutputDelta reports whether an event carries non-empty generated content
// (such as non-empty text, reasoning, or function call arguments delta).
func HasMeaningfulCodexOutputDelta(eventData []byte) bool {
	eventType := gjson.GetBytes(eventData, "type").String()
	switch eventType {
	case "response.output_text.delta", "response.reasoning_text.delta", "response.reasoning_summary_text.delta", "response.refusal.delta":
		delta := gjson.GetBytes(eventData, "delta")
		return delta.Exists() && len(strings.TrimSpace(delta.String())) > 0
	case "response.function_call_arguments.delta":
		delta := gjson.GetBytes(eventData, "delta")
		return delta.Exists() && len(strings.TrimSpace(delta.String())) > 0
	}
	return false
}

// IsCodexTerminalEmptyIncomplete reports whether a response.incomplete event represents
// an upstream silent abort with no output items or non-empty output deltas.
// Missing usage, nonzero reasoning usage, or alternate zero representations must
// not turn an empty incomplete response into a successful turn.
func IsCodexTerminalEmptyIncomplete(eventData []byte, outputItemsCount int, sawOutputDelta bool) bool {
	eventType := gjson.GetBytes(eventData, "type").String()
	if eventType != "response.incomplete" {
		return false
	}
	// If any non-empty text delta, reasoning delta, or tool argument delta was emitted, content was produced.
	if sawOutputDelta {
		return false
	}
	// If any completed output items exist, output was produced.
	if outputItemsCount > 0 {
		return false
	}
	// If response.output contains any items, output was produced.
	output := gjson.GetBytes(eventData, "response.output")
	if output.Exists() && output.IsArray() && len(output.Array()) > 0 {
		return false
	}
	return true
}
