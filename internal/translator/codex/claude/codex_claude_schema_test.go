package claude

import (
	"fmt"
	"testing"

	"github.com/tidwall/gjson"
)

func TestStructuredOutputPreservesOpenObjectSchema(t *testing.T) {
	for _, tc := range []struct {
		name, schema string
		strict       bool
	}{
		{"missing additionalProperties", `{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`, false},
		{"allowed additionalProperties", `{"type":"object","properties":{},"additionalProperties":true}`, false},
		{"typed additionalProperties", `{"type":"object","properties":{},"additionalProperties":{"type":"string"}}`, false},
		{"nested open object", `{"type":"object","properties":{"nested":{"type":"object","properties":{}}},"required":["nested"],"additionalProperties":false}`, false},
		{"nullable open object", `{"type":"object","properties":{"nested":{"type":["object","null"]}},"required":["nested"],"additionalProperties":false}`, false},
		{"open array items", `{"type":"object","properties":{"items":{"type":"array","items":{"type":"object"}}},"required":["items"],"additionalProperties":false}`, false},
		{"open definition with empty properties", `{"type":"object","properties":{},"additionalProperties":false,"$defs":{"entry":{"type":"object"}}}`, false},
		{"closed objects", `{"type":"object","properties":{"nested":{"type":"object","properties":{},"additionalProperties":false}},"required":["nested"],"additionalProperties":false}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			request := []byte(fmt.Sprintf(`{"messages":[],"output_config":{"format":{"type":"json_schema","schema":%s}}}`, tc.schema))
			result := ConvertRequest("gpt-6-astra", request)
			if got := gjson.GetBytes(result, "text.format.strict").Bool(); got != tc.strict {
				t.Fatalf("strict = %t, want %t: %s", got, tc.strict, result)
			}
			if got := gjson.GetBytes(result, "text.format.schema").Raw; got != tc.schema {
				t.Fatalf("caller schema was changed: %s", got)
			}
		})
	}
}
