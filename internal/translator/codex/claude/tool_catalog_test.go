package claude

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const handbackFixture = `{"tools":[{"name":"Read","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"Review the fixture."},{"role":"system","content":[{"type":"text","text":"The following tools just became available and are ready to use:\nSubagentHandback"},{"type":"tool_addition","tool":{"type":"tool_definition","definition":{"name":"SubagentHandback","description":"Deliver the final report to your caller.","input_schema":{"type":"object","properties":{"message":{"type":"string"}},"required":["message"],"additionalProperties":false}}}}]}],"tool_choice":{"type":"tool","name":"SubagentHandback"}}`

func TestInlineHandbackSchemaBecomesCallable(t *testing.T) {
	body := ConvertRequest("gpt-6.1-sol", []byte(handbackFixture))
	if gjson.GetBytes(body, "tools.1.name").String() != "SubagentHandback" || gjson.GetBytes(body, "tools.1.parameters.required.0").String() != "message" {
		t.Fatalf("inline handback schema lost: %s", body)
	}
	if gjson.GetBytes(body, "tool_choice.name").String() != "SubagentHandback" {
		t.Fatalf("forced inline tool lost: %s", body)
	}
	if !strings.Contains(gjson.GetBytes(body, "input.1.content.0.text").String(), "became available") {
		t.Fatal("availability reminder lost")
	}
}

func TestInlineCatalogChanges(t *testing.T) {
	for _, tc := range []struct {
		name, changes, want string
		fail                bool
	}{
		{"remove", `[{"type":"tool_removal","tool":{"type":"tool_reference","name":"Read"}}]`, "SubagentHandback", false},
		{"readd", `[{"type":"tool_removal","tool":{"type":"tool_reference","name":"Read"}},{"type":"tool_addition","tool":{"type":"tool_reference","name":"Read"}}]`, "Read,SubagentHandback", false},
		{"replace", `[{"type":"tool_addition","tool":{"type":"tool_definition","definition":{"name":"Read","description":"updated","input_schema":{"type":"object"}}}}]`, "Read,SubagentHandback", false},
		{"unknown_reference", `[{"type":"tool_addition","tool":{"type":"tool_reference","name":"Missing"}}]`, "", true},
		{"invalid_change", `[{"type":"tool_addition","tool":{"type":"future_tool_kind"}}]`, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, _ := sjson.SetRawBytes([]byte(handbackFixture), "messages.-1", []byte(`{"role":"system","content":`+tc.changes+`}`))
			tools, err := ResolveToolCatalog(raw)
			if tc.fail {
				if err == nil {
					t.Fatal("accepted unresolved tool change")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, tool := range tools {
				names = append(names, tool.Get("name").String())
			}
			if strings.Join(names, ",") != tc.want {
				t.Fatalf("catalog = %v", names)
			}
		})
	}
}

func TestInlineLongToolNamesRoundTrip(t *testing.T) {
	name := "mcp__fixture__" + strings.Repeat("long_tool_name_", 8)
	raw := []byte(strings.ReplaceAll(handbackFixture, "SubagentHandback", name))
	body := ConvertRequest("gpt-6.1-sol", raw)
	short := gjson.GetBytes(body, "tools.1.name").String()
	if len(short) > 64 || buildReverseMapFromClaudeOriginalShortToOriginal(raw)[short] != name {
		t.Fatal("inline tool name does not round trip")
	}
}

func TestDocumentInMixedToolResultIsPreserved(t *testing.T) {
	for _, source := range []string{`{"type":"base64","media_type":"application/pdf","data":"fixture-pdf"}`, `{"type":"text","data":"fixture document text"}`} {
		raw := []byte(`{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"call_fixture","name":"Read","input":{}}]},{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_fixture","content":[{"type":"text","text":"before"},{"type":"document","title":"Fixture","context":"Preserve this context","source":` + source + `},{"type":"text","text":"after"}]}]}]}`)
		body := ConvertRequest("gpt-6.1-sol", raw)
		output := gjson.GetBytes(body, "input.1.output").Array()
		if len(output) != 4 || output[0].Get("text").String() != "before" || !strings.Contains(output[1].Get("text").String(), "Preserve this context") || output[3].Get("text").String() != "after" {
			t.Fatalf("mixed document order lost: %s", body)
		}
		if !strings.Contains(output[2].Raw, "fixture") {
			t.Fatal("document payload dropped")
		}
	}
}
