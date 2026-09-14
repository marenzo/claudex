package claude

import (
	"bytes"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestStreamTextSnapshots(t *testing.T) {
	const created = `{"type":"response.created","response":{"id":"r1","model":"gpt-6-astra"}}`
	const item = `{"type":"message","id":"m1","content":[{"type":"output_text","text":"FIRST"}]}`
	const completed = `{"type":"response.completed","response":{"id":"r1","model":"gpt-6-astra","output":[` + item + `]}}`
	const done = `{"type":"response.output_item.done","output_index":2,"item":` + item + `}`
	tests := []struct {
		name   string
		events []string
		want   string
	}{
		{"terminal only", []string{completed}, "FIRST"},
		{"multiple terminal items", []string{`{"type":"response.completed","response":{"output":[` + item + `,{"type":"message","id":"m2","content":[{"type":"output_text","text":"SECOND"}]}]}}`}, "FIRSTSECOND"},
		{"done deduplicated against reindexed terminal", []string{done, completed}, "FIRST"},
		{"partial delta completed by snapshot", []string{`{"type":"response.output_text.delta","output_index":2,"content_index":0,"delta":"FI"}`, done, completed}, "FIRST"},
		{"part done without item", []string{`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"FIRST"}}`, `{"type":"response.completed","response":{"output":[]}}`}, "FIRST"},
		{"text done without item", []string{`{"type":"response.output_text.done","output_index":0,"content_index":0,"text":"FIRST"}`, `{"type":"response.completed","response":{"output":[]}}`}, "FIRST"},
		{"second item has no delta", []string{`{"type":"response.output_text.delta","output_index":2,"content_index":0,"delta":"FIRST"}`, done, `{"type":"response.output_item.done","output_index":3,"item":{"type":"message","id":"m2","content":[{"type":"output_text","text":"SECOND"}]}}`, completed}, "FIRSTSECOND"},
		{"done without index retains identity", []string{`{"type":"response.output_text.delta","item_id":"m1","output_index":2,"content_index":0,"delta":"FI"}`, strings.ReplaceAll(done, `"output_index":2,`, ""), completed}, "FIRST"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunks := [][]byte{[]byte("data: " + created)}
			for _, event := range tt.events {
				chunks = append(chunks, []byte("data: "+event))
			}
			outputs := translateCodexClaudeChunks(t, chunks)
			blocks := assertCodexClaudeContentBlockLifecycle(t, outputs)
			var text strings.Builder
			for _, block := range blocks {
				text.WriteString(block.Text)
			}
			if text.String() != tt.want {
				t.Fatalf("text = %q, want %q", text.String(), tt.want)
			}
		})
	}
}

func TestOmittedThinkingRetainsReplaySignature(t *testing.T) {
	signature := validCodexReasoningSignature()
	for _, thinking := range []string{`{"type":"adaptive","display":"omitted"}`, `{"type":"disabled"}`} {
		original := []byte(`{"messages":[],"thinking":` + thinking + `}`)
		state := NewStream("gpt-6-astra", original)
		var output [][]byte
		terminal := []byte(`{"type":"response.completed","response":{"model":"gpt-6-astra","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"Hidden summary"}],"encrypted_content":"` + signature + `"}]}}`)
		for _, event := range []string{`{"type":"response.created","response":{"id":"r1","model":"gpt-6-astra"}}`, `{"type":"response.reasoning_summary_part.added"}`, `{"type":"response.reasoning_summary_text.delta","delta":"Hidden summary"}`, `{"type":"response.output_item.done","item":{"type":"reasoning","encrypted_content":"` + signature + `"}}`, string(terminal)} {
			output = append(output, convertStreamChunk(t, state, []byte("data: "+event))...)
		}
		stream := bytes.Join(output, nil)
		if bytes.Contains(stream, []byte("Hidden summary")) || !bytes.Contains(stream, []byte(signature)) {
			t.Fatalf("hidden summary or signature mismatch: %s", stream)
		}
		assertCodexClaudeContentBlockLifecycle(t, output)
		response := convertResponse(t, "gpt-6-astra", original, terminal)
		if gjson.GetBytes(response, "content.0.thinking").String() != "" || gjson.GetBytes(response, "content.0.signature").String() != signature {
			t.Fatalf("hidden nonstream summary: %s", response)
		}
		replay := []byte(`{"messages":[{"role":"assistant","content":` + gjson.GetBytes(response, "content").Raw + `},{"role":"user","content":"Continue"}]}`)
		translated := ConvertRequest("gpt-6-astra", replay)
		if gjson.GetBytes(translated, "input.0.encrypted_content").String() != signature {
			t.Fatal("encrypted replay state lost")
		}
	}
}

func TestTerminalOnlyReasoningRetainsSignature(t *testing.T) {
	signature := validCodexReasoningSignature()
	for _, display := range []string{"summarized", "omitted"} {
		original := []byte(`{"messages":[],"thinking":{"type":"adaptive","display":"` + display + `"}}`)
		state := NewStream("", original)
		var outputs [][]byte
		for _, event := range []string{
			`{"type":"response.created","response":{"id":"r1","model":"gpt-6-astra"}}`,
			`{"type":"response.completed","response":{"output":[{"type":"reasoning","encrypted_content":"` + signature + `","summary":[{"type":"summary_text","text":"Summary"}]}]}}`,
		} {
			outputs = append(outputs, convertStreamChunk(t, state, []byte("data: "+event))...)
		}
		assertCodexClaudeContentBlockLifecycle(t, outputs)
		joined := bytes.Join(outputs, nil)
		if bytes.Count(joined, []byte(signature)) != 1 {
			t.Fatalf("terminal reasoning signature missing or duplicated: %s", joined)
		}
		if got := bytes.Contains(joined, []byte("Summary")); got != (display != "omitted") {
			t.Fatalf("summary visibility incorrect: %s", joined)
		}
	}
}

func TestRepeatedAndTerminalOnlyWebSearchIsNotExposed(t *testing.T) {
	const item = `{"type":"web_search_call","id":"search1","action":{"query":"Go release","sources":[{"url":"https://go.dev/","title":"Go"}]}}`
	const terminal = `{"type":"response.completed","response":{"output":[` + item + `]}}`
	const done = `{"type":"response.output_item.done","output_index":0,"item":` + item + `}`
	for _, events := range [][]string{{terminal}, {done, done, terminal}} {
		chunks := [][]byte{[]byte(`data: {"type":"response.created","response":{"id":"r1","model":"gpt-6-astra"}}`)}
		for _, event := range events {
			chunks = append(chunks, []byte("data: "+event))
		}
		outputs := translateCodexClaudeChunks(t, chunks)
		blocks := assertCodexClaudeContentBlockLifecycle(t, outputs)
		for _, block := range blocks {
			if block.Type == "server_tool_use" || block.Type == "web_search_tool_result" {
				t.Fatalf("synthetic Anthropic search block %q cannot be resumed without encrypted_content", block.Type)
			}
		}
	}
}
