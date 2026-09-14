package claude

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertCodexResponseToClaude_StreamRefusal(t *testing.T) {
	const refusal = "I cannot help with that request."
	const created = `{"type":"response.created","response":{"id":"resp_refusal","model":"gpt-6-astra"}}`
	const added = `{"type":"response.content_part.added","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":""}}`
	const delta = `{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"I cannot help with that request."}`
	const done = `{"type":"response.refusal.done","output_index":0,"content_index":0,"refusal":"I cannot help with that request."}`
	const partDone = `{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"refusal","refusal":"I cannot help with that request."}}`
	const itemDone = `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_refusal","type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot help with that request."}]}}`
	const completed = `{"type":"response.completed","response":{"id":"resp_refusal","model":"gpt-6-astra","status":"completed","output":[{"id":"msg_refusal","type":"message","role":"assistant","content":[{"type":"refusal","refusal":"I cannot help with that request."}]}]}}`
	const emptyCompleted = `{"type":"response.completed","response":{"id":"resp_refusal","model":"gpt-6-astra","status":"completed","output":[]}}`

	tests := []struct {
		name   string
		events []string
		want   string
	}{
		{"streamed once despite repeated snapshots", []string{created, added, delta, done, partDone, itemDone, completed}, refusal},
		{"done without deltas", []string{created, done, partDone, itemDone, completed}, refusal},
		{"part done without deltas", []string{created, partDone, itemDone, completed}, refusal},
		{"item done without deltas", []string{created, itemDone, completed}, refusal},
		{"terminal output only", []string{created, completed}, refusal},
		{"empty terminal output", []string{created, added, delta, partDone, emptyCompleted}, refusal},
		{"partial deltas completed by snapshot", []string{created, `{"type":"response.refusal.delta","output_index":0,"content_index":0,"delta":"I cannot "}`, done, itemDone, completed}, refusal},
		{"refusal after ordinary text", []string{created,
			`{"type":"response.output_text.delta","output_index":0,"content_index":0,"delta":"Answer: "}`,
			`{"type":"response.content_part.done","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Answer: "}}`,
			`{"type":"response.output_item.done","output_index":0,"item":{"type":"message","content":[{"type":"output_text","text":"Answer: "},{"type":"refusal","refusal":"I cannot help with that request."}]}}`, emptyCompleted}, "Answer: " + refusal},
		{"multiple refusal items", []string{created, itemDone,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"message","content":[{"type":"refusal","refusal":"A second explanation."}]}}`, emptyCompleted}, refusal + "A second explanation."},
		{"terminal output reindexed by executor", []string{created,
			strings.ReplaceAll(delta, `"output_index":0`, `"output_index":2`),
			strings.ReplaceAll(itemDone, `"output_index":0`, `"output_index":2`), completed}, refusal},
		{"done item missing index retains streamed identity", []string{created,
			`{"type":"response.refusal.delta","item_id":"msg_refusal","output_index":2,"content_index":0,"delta":"I cannot help with that request."}`,
			strings.ReplaceAll(itemDone, `"output_index":0,`, ""), completed}, refusal},
		{"deferred behind tool call", []string{created,
			`{"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","call_id":"call_read","name":"Read"}}`,
			added, delta, partDone, itemDone,
			`{"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","call_id":"call_read","name":"Read","arguments":"{}"}}`, completed}, refusal},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var chunks [][]byte
			for _, event := range test.events {
				chunks = append(chunks, []byte("data: "+event))
			}
			outputs := translateCodexClaudeChunks(t, chunks)
			blocks := assertCodexClaudeContentBlockLifecycle(t, outputs)
			var text strings.Builder
			toolUse := false
			for _, block := range blocks {
				text.WriteString(block.Text)
				toolUse = toolUse || block.Type == "tool_use"
			}
			if got := text.String(); got != test.want {
				t.Fatalf("text = %q, want %q", got, test.want)
			}
			terminal, ok := firstClaudeStreamPayloadForEvent(string(bytes.Join(outputs, nil)), "message_delta")
			wantStop := "refusal"
			if toolUse {
				wantStop = "tool_use"
			}
			if !ok || terminal.Get("delta.stop_reason").String() != wantStop {
				t.Fatalf("stop reason = %s, want %s", terminal.Raw, wantStop)
			}
		})
	}
}

func TestConvertCodexResponseToClaudeNonStream_Refusal(t *testing.T) {
	for _, test := range []struct {
		name     string
		content  string
		terminal string
		wantText string
		wantStop string
	}{
		{"refusal", `[{"type":"refusal","refusal":"Cannot do that."}]`, "", "Cannot do that.", "refusal"},
		{"mixed text", `[{"type":"output_text","text":"Answer: "},{"type":"refusal","refusal":"Cannot do that."}]`, "", "Answer: Cannot do that.", "refusal"},
		{"empty refusal", `[{"type":"refusal","refusal":""}]`, "", "", "refusal"},
		{"truncated refusal", `[{"type":"refusal","refusal":"Cannot"}]`, `,"incomplete_details":{"reason":"max_output_tokens"}`, "Cannot", "max_tokens"},
		{"ordinary text", `[{"type":"output_text","text":"OK"}]`, "", "OK", "end_turn"},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := []byte(fmt.Sprintf(`{"type":"response.completed","response":{"model":"gpt-6-astra","output":[{"type":"message","content":%s}]%s}}`, test.content, test.terminal))
			output := convertResponse(t, "gpt-6-astra", []byte(`{}`), input)
			var text strings.Builder
			for _, block := range gjson.GetBytes(output, "content").Array() {
				text.WriteString(block.Get("text").String())
			}
			if text.String() != test.wantText || gjson.GetBytes(output, "stop_reason").String() != test.wantStop {
				t.Fatalf("output = %s, want text %q and stop %q", output, test.wantText, test.wantStop)
			}
		})
	}
}

func TestConvertClaudeRequestToCodex_ToolResultError(t *testing.T) {
	for _, test := range []struct {
		name    string
		content string
	}{
		{"text", `,"content":"Resource temporarily unavailable"`},
		{"empty", `,"content":""`},
		{"absent", ""},
		{"text and image", `,"content":[{"type":"text","text":"Resource temporarily unavailable"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cGl4ZWw="}}]`},
		{"image only", `,"content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"cGl4ZWw="}}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			convert := func(flag string) []byte {
				request := []byte(fmt.Sprintf(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_read"%s%s}]}]}`, flag, test.content))
				return ConvertRequest("gpt-6-astra", request)
			}
			success := convert(`,"is_error":false`)
			failed := convert(`,"is_error":true`)
			if bytes.Equal(success, failed) {
				t.Fatalf("tool failure flag was lost: %s", failed)
			}
			if !bytes.Equal(success, convert("")) {
				t.Fatal("an explicit success flag changed normal tool output")
			}
			if gjson.GetBytes(failed, "input.0.call_id").String() != "call_read" {
				t.Fatalf("tool result ID changed: %s", failed)
			}
			before := gjson.GetBytes(success, "input.0.output")
			after := gjson.GetBytes(failed, "input.0.output")
			if before.IsArray() {
				blocks := after.Array()
				if len(blocks) != len(before.Array())+1 || blocks[0].Get("type").String() != "input_text" || !strings.Contains(blocks[0].Get("text").String(), "is_error=true") {
					t.Fatalf("missing error marker before multimodal result: %s", failed)
				}
				for i, block := range before.Array() {
					if block.Raw != blocks[i+1].Raw {
						t.Fatalf("original tool content changed: %s", failed)
					}
				}
			} else if !strings.Contains(after.String(), "is_error=true") || !strings.HasSuffix(after.String(), before.String()) {
				t.Fatalf("missing error status or original content: %s", failed)
			}
		})
	}
}
