//go:build live

package proxy

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Simulate a memory file result without reading or writing any real memory.
// The marker exists only in the tool result, not in the initial request.
func TestLiveMemoryRecallThroughGateway(t *testing.T) {
	s, _ := liveProtocolServer(t)
	for _, model := range []string{"sol", "sol6"} {
		t.Run(model, func(t *testing.T) {
			raw := []byte(`{"model":"sol","stream":false,"thinking":{"type":"disabled"},"system":[{"type":"text","text":"# Memory\nYou have persistent file-based memory at /fixture/memory/. MEMORY.md is the index; read relevant topic files to recall their details."}],"messages":[{"role":"user","content":[{"type":"text","text":"<system-reminder>Contents of /fixture/memory/MEMORY.md:\n- [Fixture naming preference](fixture-naming.md) - the user's naming convention for new fixtures.\n</system-reminder>"},{"type":"text","text":"Name a new smoke-test fixture using my previously recorded naming convention. Consult the relevant memory, then give only the identifier."}]}],"tools":[{"name":"Read","description":"Read a local file by absolute path.","input_schema":{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}}]}`)
			raw, _ = sjson.SetBytes(raw, "model", model)
			first := request(s, "/v1/messages", string(raw))
			if first.Code != 200 {
				t.Fatalf("recall HTTP %d", first.Code)
			}
			var call gjson.Result
			for _, block := range gjson.Get(first.Body.String(), "content").Array() {
				if block.Get("type").String() == "tool_use" {
					call = block
				}
			}
			if call.Get("name").String() != "Read" || call.Get("input.file_path").String() != "/fixture/memory/fixture-naming.md" {
				t.Fatal("did not request the relevant memory topic")
			}
			raw, _ = sjson.SetBytes(raw, "messages.-1", map[string]any{"role": "assistant", "content": json.RawMessage(gjson.Get(first.Body.String(), "content").Raw)})
			raw, _ = sjson.SetBytes(raw, "messages.-1", map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": call.Get("id").String(), "content": "The user's fixture convention: every new fixture identifier must start with alder37_. Use lowercase snake_case."}}})
			second := request(s, "/v1/messages", string(raw))
			if second.Code != 200 {
				t.Fatalf("memory result HTTP %d", second.Code)
			}
			var answer strings.Builder
			for _, block := range gjson.Get(second.Body.String(), "content").Array() {
				answer.WriteString(block.Get("text").String())
			}
			if !strings.Contains(answer.String(), "alder37_") {
				t.Fatal("memory topic result not used in final answer")
			}
			t.Log("relevant topic requested through Read; translated result used in answer")
		})
	}
}
