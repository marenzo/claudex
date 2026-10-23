package proxy

import (
	"bytes"
	"encoding/json"
)

// Canonicalize JSON without changing numeric values. Anthropic cache annotations
// are transport hints; serializing moving breakpoints into reviewer text changes
// old evidence unnecessarily. Remove them only at API block/definition locations,
// never from tool inputs, schemas, permission context or quoted text.
func canonicalReviewEvidence(field string, raw json.RawMessage) json.RawMessage {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return raw
	}
	stripBlocks := func(blocks any) {
		if list, ok := blocks.([]any); ok {
			for _, item := range list {
				if block, ok := item.(map[string]any); ok {
					delete(block, "cache_control")
				}
			}
		}
	}
	switch field {
	case "system", "tools":
		stripBlocks(value)
	case "messages":
		if messages, ok := value.([]any); ok {
			for _, item := range messages {
				message, ok := item.(map[string]any)
				if !ok {
					continue
				}
				delete(message, "cache_control")
				stripBlocks(message["content"])
				if parts, ok := message["content"].([]any); ok {
					for _, item := range parts {
						if part, ok := item.(map[string]any); ok && part["type"] == "tool_result" {
							stripBlocks(part["content"])
						}
					}
				}
			}
		}
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return raw
	}
	return encoded
}
