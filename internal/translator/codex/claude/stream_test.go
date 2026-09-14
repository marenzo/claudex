package claude

import (
	"strings"
	"testing"
)

// convertStreamChunk lets the event-sequence fixtures collect converted chunks
// while checking every conversion error at the point where it occurs.
func convertStreamChunk(t *testing.T, stream *Stream, event []byte) [][]byte {
	t.Helper()
	output, err := stream.Convert(event)
	if err != nil {
		t.Fatalf("convert stream event: %v", err)
	}
	if len(output) == 0 {
		return nil
	}
	return [][]byte{output}
}

func convertResponse(t *testing.T, model string, request, event []byte) []byte {
	t.Helper()
	output, err := ConvertResponse(model, request, event)
	if err != nil {
		t.Fatalf("convert response: %v", err)
	}
	return output
}

func TestStreamRejectsInvalidCompletedToolArguments(t *testing.T) {
	const added = `data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"call1","name":"Read"}}`
	const partial = `data: {"type":"response.function_call_arguments.delta","item_id":"fc1","delta":"{\"path\":"}`
	tests := []struct {
		name     string
		prior    string
		terminal string
	}{
		{"truncated terminal", partial, `data: {"type":"response.incomplete","response":{"output":[]}}`},
		{"truncated item done", partial, `data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","name":"Read","arguments":"{\"path\":"}}`},
		{"array arguments", "", `data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","name":"Read","arguments":"[]"}}`},
		{"null arguments", "", `data: {"type":"response.function_call_arguments.done","item_id":"fc1","arguments":"null"}`},
		{"non-string arguments", "", `data: {"type":"response.function_call_arguments.done","item_id":"fc1","arguments":{}}`},
		{"divergent snapshot", partial, `data: {"type":"response.completed","response":{"output":[{"type":"function_call","id":"fc1","name":"Read","arguments":"{\"other\":1}"}]}}`},
		{"divergent after done", `data: {"type":"response.function_call_arguments.done","item_id":"fc1","arguments":"{\"path\":1}"}`, `data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","name":"Read","arguments":"{\"path\":2}"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := NewStream("model", nil)
			convertStreamChunk(t, stream, []byte(added))
			if tt.prior != "" {
				convertStreamChunk(t, stream, []byte(tt.prior))
			}
			out, err := stream.Convert([]byte(tt.terminal))
			if err == nil || len(out) != 0 {
				t.Fatalf("invalid call closed successfully: output=%q, error=%v", out, err)
			}
			if strings.Contains(err.Error(), "path") || strings.Contains(err.Error(), "Read") {
				t.Fatalf("error exposes tool data: %v", err)
			}
		})
	}
}

func TestStreamTerminalSnapshotCompletesPartialArguments(t *testing.T) {
	stream := NewStream("model", nil)
	for _, event := range []string{
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"call1","name":"Read"}}`,
		`data: {"type":"response.function_call_arguments.delta","item_id":"fc1","delta":"{\"path\":"}`,
	} {
		convertStreamChunk(t, stream, []byte(event))
	}
	out, err := stream.Convert([]byte(`data: {"type":"response.incomplete","response":{"output":[{"type":"function_call","id":"fc1","call_id":"call1","name":"Read","arguments":"{\"path\":\"test\"}"}]}}`))
	if err != nil || !strings.Contains(string(out), `"type":"message_stop"`) || !strings.Contains(string(out), `"stop_reason":"tool_use"`) {
		t.Fatalf("valid snapshot did not complete tool call: %q, %v", out, err)
	}
}

func TestStreamRejectsIncompleteWithoutUsableContent(t *testing.T) {
	for _, output := range []string{`[]`, `[{"type":"message","content":[]}]`, `[{"type":"reasoning"}]`, `[{"type":"message","content":[{"type":"output_text","text":""}]}]`} {
		stream := NewStream("model", nil)
		out, err := stream.Convert([]byte(`data: {"type":"response.incomplete","response":{"output":` + output + `}}`))
		if err == nil || len(out) != 0 {
			t.Fatalf("empty incomplete response succeeded: %q, %v", out, err)
		}
	}
}

func TestConvertResponseRejectsInvalidArguments(t *testing.T) {
	for _, arguments := range []string{`""`, `"{\"path\":"`, `"[]"`, `"null"`, `{}`} {
		event := []byte(`{"type":"response.completed","response":{"output":[{"type":"function_call","name":"Read","call_id":"call1","arguments":` + arguments + `}]}}`)
		output, err := ConvertResponse("model", nil, event)
		if err == nil || len(output) != 0 {
			t.Fatalf("invalid arguments became an executable tool: %q, %v", output, err)
		}
	}
}

func TestStreamRetainsDeferredEventWhenInputBufferIsReused(t *testing.T) {
	stream := NewStream("model", nil)
	convertStreamChunk(t, stream, []byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","id":"fc1","call_id":"call1","name":"Read"}}`))
	event := []byte(`data: {"type":"response.output_text.delta","delta":"original text"}`)
	if output := convertStreamChunk(t, stream, event); len(output) != 0 {
		t.Fatalf("text was not deferred during the tool call: %q", output)
	}
	copy(event, strings.Repeat("x", len(event)))
	output, err := stream.Convert([]byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","call_id":"call1","name":"Read","arguments":"{}"}}`))
	if err != nil || !strings.Contains(string(output), "original text") {
		t.Fatalf("deferred event changed with reused input buffer: %q, %v", output, err)
	}
}
