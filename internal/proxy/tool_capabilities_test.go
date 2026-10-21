package proxy

import (
	"github.com/marenzo/claudex/internal/config"
	"github.com/tidwall/gjson"
	"testing"
)

func TestClaudeToolTransportControlsDoNotReachCodex(t *testing.T) {
	raw := []byte(`{"model":"sol","messages":[],"tools":[{"name":"Bash","description":"Run a command","input_schema":{"type":"object","properties":{"command":{"type":"string"}}},"eager_input_streaming":true,"allowed_callers":["direct"],"input_examples":[{"command":"pwd"}],"defer_loading":true}]}`)
	body, _, err := prepareRequest(raw, config.Config{ReasoningEffort: "high"})
	if err != nil {
		t.Fatal(err)
	}
	tool := gjson.GetBytes(body, "tools.0")
	for _, field := range []string{"input_schema", "eager_input_streaming", "allowed_callers", "input_examples", "defer_loading"} {
		if tool.Get(field).Exists() {
			t.Fatalf("Claude-only field %s reached Codex", field)
		}
	}
	if tool.Get("name").String() != "Bash" || tool.Get("parameters.properties.command.type").String() != "string" {
		t.Fatalf("tool contract lost: %s", body)
	}
}

func TestUnsupportedServerToolsFailExplicitly(t *testing.T) {
	for _, kind := range []string{"advisor_20260301", "web_fetch_20250910", "tool_search_tool_regex_20251119"} {
		raw := []byte(`{"messages":[],"tools":[{"type":"` + kind + `","name":"server_tool"}]}`)
		if _, _, err := prepareRequest(raw, config.Config{Model: "astra", ReasoningEffort: "high"}); err == nil {
			t.Fatalf("silently converted %s to a client function", kind)
		}
	}
}

func TestProgrammaticOnlyToolsCannotBecomeDirectCalls(t *testing.T) {
	for _, callers := range []string{`[]`, `["code_execution_20250825"]`, `["direct","code_execution_20250825"]`, `"direct"`} {
		raw := []byte(`{"messages":[],"tools":[{"name":"PrivateTool","input_schema":{"type":"object"},"allowed_callers":` + callers + `}]}`)
		if _, _, err := prepareRequest(raw, config.Config{Model: "astra", ReasoningEffort: "high"}); err == nil {
			t.Fatalf("broadened programmatic tool into direct call: %s", callers)
		}
	}
}
