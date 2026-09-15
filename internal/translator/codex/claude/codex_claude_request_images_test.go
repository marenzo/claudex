package claude

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertClaudeRequestToCodex_ImageSources(t *testing.T) {
	const remoteURL = "https://images.example.test/a%20b.png?x=1&signature=a%2Bb"
	const imageContent = `{"type":"image","source":{"type":"url","url":"` + remoteURL + `"}}`
	const base64Content = `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cGl4ZWw="}}`
	type contentPart struct {
		Type     string `json:"type"`
		Text     string `json:"text,omitempty"`
		ImageURL string `json:"image_url,omitempty"`
	}
	for _, context := range []string{"attachment", "tool_result", "failed_tool_result"} {
		for _, mixed := range []bool{false, true} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/mixed=%t/stream=%t", context, mixed, stream), func(t *testing.T) {
					content := `[` + imageContent + `]`
					want := []contentPart{{Type: "input_image", ImageURL: remoteURL}}
					if mixed {
						content = `[{"type":"text","text":"before"},` + imageContent + `,{"type":"text","text":"between"},` + base64Content + `,{"type":"text","text":"after"}]`
						want = []contentPart{
							{Type: "input_text", Text: "before"},
							{Type: "input_image", ImageURL: remoteURL},
							{Type: "input_text", Text: "between"},
							{Type: "input_image", ImageURL: "data:image/png;base64,cGl4ZWw="},
							{Type: "input_text", Text: "after"},
						}
					}
					messages := `[{"role":"user","content":` + content + `}]`
					path := "input.0.content"
					if context != "attachment" {
						failed := context == "failed_tool_result"
						messages = fmt.Sprintf(`[{"role":"assistant","content":[{"type":"tool_use","id":"call_image","name":"Read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_image","is_error":%t,"content":%s}]}]`, failed, content)
						path = "input.1.output"
						if failed {
							want = append([]contentPart{{Type: "input_text", Text: "Tool execution failed (is_error=true)."}}, want...)
						}
					}
					request := []byte(`{"messages":` + messages + `}`)
					result := ConvertRequest("gpt-6-astra", request)
					var got []contentPart
					if err := json.Unmarshal([]byte(gjson.GetBytes(result, path).Raw), &got); err != nil {
						t.Fatalf("image content is not an array: %s: %v", result, err)
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("image content = %+v, want %+v", got, want)
					}
					if context != "attachment" && gjson.GetBytes(result, "input.1.call_id").String() != "call_image" {
						t.Fatalf("tool result call ID changed: %s", result)
					}
				})
			}
		}
	}
}
