package common

import (
	"github.com/tidwall/gjson"
)

// AlignClaudeToolResults orders tool_result blocks by the preceding tool_use IDs,
// preserving non-result content blocks at their existing indexes. If a complete
// one-to-one match is unavailable, the original content is returned.
func AlignClaudeToolResults(content gjson.Result, toolUseIDs []string) gjson.Result {
	if !content.IsArray() || len(toolUseIDs) == 0 {
		return content
	}

	parts := content.Array()
	toolResults := make([]gjson.Result, 0, len(toolUseIDs))
	toolResultIndices := make([]int, 0, len(toolUseIDs))
	for i, part := range parts {
		if part.Get("type").String() == "tool_result" {
			toolResults = append(toolResults, part)
			toolResultIndices = append(toolResultIndices, i)
		}
	}
	if len(toolResults) != len(toolUseIDs) {
		return content
	}

	reorderedResults := make([]gjson.Result, 0, len(toolUseIDs))
	used := make([]bool, len(toolResults))
	for _, toolUseID := range toolUseIDs {
		matched := -1
		for resultIndex, toolResult := range toolResults {
			if !used[resultIndex] && toolUseID != "" && toolResult.Get("tool_use_id").String() == toolUseID {
				matched = resultIndex
				break
			}
		}
		if matched < 0 {
			return content
		}
		used[matched] = true
		reorderedResults = append(reorderedResults, toolResults[matched])
	}

	ordered := make([][]byte, len(parts))
	for i, part := range parts {
		ordered[i] = []byte(part.Raw)
	}
	for i, slotIndex := range toolResultIndices {
		ordered[slotIndex] = []byte(reorderedResults[i].Raw)
	}
	return gjson.ParseBytes(JoinRawArray(ordered))
}
