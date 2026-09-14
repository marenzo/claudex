package claude

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestConvertCodexResponseToClaude_StreamThinkingIncludesSignature(t *testing.T) {
	originalRequest := []byte(`{"messages":[]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5\"}}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Let me think\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_123\"}}"),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	startFound := false
	signatureDeltaFound := false
	stopFound := false

	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			switch data.Get("type").String() {
			case "content_block_start":
				if data.Get("content_block.type").String() == "thinking" {
					startFound = true
					if data.Get("content_block.signature").Exists() {
						t.Fatalf("thinking start block should NOT have signature field when signature is unknown: %s", line)
					}
				}
			case "content_block_delta":
				if data.Get("delta.type").String() == "signature_delta" {
					signatureDeltaFound = true
					if got := data.Get("delta.signature").String(); got != "enc_sig_123" {
						t.Fatalf("unexpected signature delta: %q", got)
					}
				}
			case "content_block_stop":
				stopFound = true
			}
		}
	}

	if !startFound {
		t.Fatal("expected thinking content_block_start event")
	}
	if !signatureDeltaFound {
		t.Fatal("expected signature_delta event for thinking block")
	}
	if !stopFound {
		t.Fatal("expected content_block_stop event for thinking block")
	}
}

func TestConvertCodexResponseToClaude_StreamThinkingWithoutReasoningItemStillIncludesSignatureField(t *testing.T) {
	originalRequest := []byte(`{"messages":[]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Let me think\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}"),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	thinkingStartFound := false
	thinkingStopFound := false
	signatureDeltaFound := false

	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			if data.Get("type").String() == "content_block_start" && data.Get("content_block.type").String() == "thinking" {
				thinkingStartFound = true
				if data.Get("content_block.signature").Exists() {
					t.Fatalf("thinking start block should NOT have signature field without encrypted_content: %s", line)
				}
			}
			if data.Get("type").String() == "content_block_stop" && data.Get("index").Int() == 0 {
				thinkingStopFound = true
			}
			if data.Get("type").String() == "content_block_delta" && data.Get("delta.type").String() == "signature_delta" {
				signatureDeltaFound = true
			}
		}
	}

	if !thinkingStartFound {
		t.Fatal("expected thinking content_block_start event")
	}
	if !thinkingStopFound {
		t.Fatal("expected thinking content_block_stop event")
	}
	if signatureDeltaFound {
		t.Fatal("did not expect signature_delta without encrypted_content")
	}
}

// codexThinkingStreamDigest collects the thinking-related events produced by a Codex
// stream so tests can assert block/signature counts and the reassembled thinking text.
type codexThinkingStreamDigest struct {
	Starts     int
	Stops      int
	Signatures []string
	Thinking   string
	Raw        string
}

func digestCodexThinkingStream(t *testing.T, chunks [][]byte) codexThinkingStreamDigest {
	t.Helper()

	originalRequest := []byte(`{"messages":[]}`)
	param := NewStream("", originalRequest)

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	var digest codexThinkingStreamDigest
	var thinking strings.Builder
	var raw strings.Builder
	for _, out := range outputs {
		raw.Write(out)
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			switch data.Get("type").String() {
			case "content_block_start":
				if data.Get("content_block.type").String() == "thinking" {
					digest.Starts++
				}
			case "content_block_delta":
				switch data.Get("delta.type").String() {
				case "thinking_delta":
					thinking.WriteString(data.Get("delta.thinking").String())
				case "signature_delta":
					digest.Signatures = append(digest.Signatures, data.Get("delta.signature").String())
				}
			case "content_block_stop":
				digest.Stops++
			}
		}
	}
	digest.Thinking = thinking.String()
	digest.Raw = raw.String()

	return digest
}

func TestConvertCodexResponseToClaude_StreamThinkingKeepsSingleBlockAcrossSummaryParts(t *testing.T) {
	digest := digestCodexThinkingStream(t, [][]byte{
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"First part\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Second part\"}"),
	})

	if digest.Starts != 1 {
		t.Fatalf("expected a single thinking block start for one reasoning item, got %d", digest.Starts)
	}
	if digest.Stops != 0 {
		t.Fatalf("expected the thinking block to stay open until output_item.done, got %d stops", digest.Stops)
	}
	if want := "First part\n\nSecond part"; digest.Thinking != want {
		t.Fatalf("thinking text = %q, want %q", digest.Thinking, want)
	}
}

func TestConvertCodexResponseToClaude_StreamThinkingEmitsSingleSignatureAcrossMultipartReasoning(t *testing.T) {
	digest := digestCodexThinkingStream(t, [][]byte{
		[]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_multipart\"}}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"First part\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Second part\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\"}}"),
	})

	if digest.Starts != 1 || digest.Stops != 1 {
		t.Fatalf("expected exactly one thinking block, got %d starts and %d stops", digest.Starts, digest.Stops)
	}
	if len(digest.Signatures) != 1 {
		t.Fatalf("expected one signature_delta for one reasoning item, got %d: %v", len(digest.Signatures), digest.Signatures)
	}
	// output_item.done omitted encrypted_content here, so the pre-content fallback is expected.
	if digest.Signatures[0] != "enc_sig_multipart" {
		t.Fatalf("unexpected signature delta: %q", digest.Signatures[0])
	}
	if want := "First part\n\nSecond part"; digest.Thinking != want {
		t.Fatalf("thinking text = %q, want %q", digest.Thinking, want)
	}
}

// TestConvertCodexResponseToClaude_StreamThinkingNeverEmitsPreContentEncryptedContent guards the
// real-world shape earlier tests missed: output_item.added carries a fixed-size pre-content
// snapshot of encrypted_content that always differs from the final value on output_item.done.
// Emitting that snapshot makes the client replay bogus reasoning items for the rest of the session.
func TestConvertCodexResponseToClaude_StreamThinkingNeverEmitsPreContentEncryptedContent(t *testing.T) {
	digest := digestCodexThinkingStream(t, [][]byte{
		[]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_pre_content_snapshot\"}}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Part A\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Part B\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Part C\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_final\"}}"),
	})

	if digest.Starts != 1 || digest.Stops != 1 {
		t.Fatalf("expected one thinking block for one reasoning item with three summary parts, got %d starts and %d stops", digest.Starts, digest.Stops)
	}
	if len(digest.Signatures) != 1 || digest.Signatures[0] != "enc_sig_final" {
		t.Fatalf("expected exactly one signature_delta carrying the final encrypted_content, got %v", digest.Signatures)
	}
	if strings.Contains(digest.Raw, "enc_sig_pre_content_snapshot") {
		t.Fatal("pre-content encrypted_content snapshot leaked into the Claude stream")
	}
	if want := "Part A\n\nPart B\n\nPart C"; digest.Thinking != want {
		t.Fatalf("thinking text = %q, want %q", digest.Thinking, want)
	}
}

// TestConvertCodexResponseToClaude_StreamThinkingEmitsOneBlockPerReasoningItem checks that two
// consecutive reasoning items stay separate blocks, each signed with its own final value.
func TestConvertCodexResponseToClaude_StreamThinkingEmitsOneBlockPerReasoningItem(t *testing.T) {
	digest := digestCodexThinkingStream(t, [][]byte{
		[]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_pre_1\"}}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"First item\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_final_1\"}}"),
		[]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_pre_2\"}}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Second item\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_final_2\"}}"),
	})

	if digest.Starts != 2 || digest.Stops != 2 {
		t.Fatalf("expected two thinking blocks for two reasoning items, got %d starts and %d stops", digest.Starts, digest.Stops)
	}
	if len(digest.Signatures) != 2 || digest.Signatures[0] != "enc_final_1" || digest.Signatures[1] != "enc_final_2" {
		t.Fatalf("expected each block signed with its own final encrypted_content, got %v", digest.Signatures)
	}
	if strings.Contains(digest.Raw, "enc_pre_1") || strings.Contains(digest.Raw, "enc_pre_2") {
		t.Fatal("pre-content encrypted_content snapshot leaked into the Claude stream")
	}
}

func TestConvertCodexResponseToClaude_StreamThinkingUsesEarlyCapturedSignatureWhenDoneOmitsIt(t *testing.T) {
	originalRequest := []byte(`{"messages":[]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_early\"}}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Let me think\"}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\"}}"),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	signatureDeltaCount := 0
	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			if data.Get("type").String() == "content_block_delta" && data.Get("delta.type").String() == "signature_delta" {
				signatureDeltaCount++
				if got := data.Get("delta.signature").String(); got != "enc_sig_early" {
					t.Fatalf("unexpected signature delta: %q", got)
				}
			}
		}
	}

	if signatureDeltaCount != 1 {
		t.Fatalf("expected signature_delta from early-captured signature, got %d", signatureDeltaCount)
	}
}

func TestConvertCodexResponseToClaude_StreamThinkingUsesFinalDoneSignature(t *testing.T) {
	originalRequest := []byte(`{"messages":[]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_initial\"}}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.added\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Let me think\"}"),
		[]byte("data: {\"type\":\"response.reasoning_summary_part.done\"}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_final\"}}"),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	signatureDeltaCount := 0
	events := []string{}
	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			if data.Get("type").String() == "content_block_start" && data.Get("content_block.type").String() == "thinking" {
				events = append(events, "thinking_start")
			}
			if data.Get("type").String() == "content_block_delta" && data.Get("delta.type").String() == "thinking_delta" {
				events = append(events, "thinking_delta")
			}
			if data.Get("type").String() == "content_block_stop" && data.Get("index").Int() == 0 {
				events = append(events, "thinking_stop")
			}
			if data.Get("type").String() != "content_block_delta" || data.Get("delta.type").String() != "signature_delta" {
				continue
			}
			events = append(events, "signature_delta")
			signatureDeltaCount++
			if got := data.Get("delta.signature").String(); got != "enc_sig_final" {
				t.Fatalf("signature delta = %q, want final done signature", got)
			}
		}
	}

	if signatureDeltaCount != 1 {
		t.Fatalf("expected one signature_delta, got %d", signatureDeltaCount)
	}
	if got, want := strings.Join(events, ","), "thinking_start,thinking_delta,signature_delta,thinking_stop"; got != want {
		t.Fatalf("thinking event order = %s, want %s", got, want)
	}
}

func TestConvertCodexResponseToClaude_StreamSignatureOnlyReasoningEmitsThinkingSignature(t *testing.T) {
	originalRequest := []byte(`{"messages":[]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_123\",\"model\":\"gpt-5\"}}"),
		[]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_initial\"}}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"reasoning\",\"encrypted_content\":\"enc_sig_only\"}}"),
		[]byte("data: {\"type\":\"response.content_part.added\"}"),
		[]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}"),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	thinkingStartFound := false
	thinkingDeltaFound := false
	signatureDeltaFound := false
	thinkingStopFound := false
	textStartIndex := int64(-1)
	events := []string{}

	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			switch data.Get("type").String() {
			case "content_block_start":
				if data.Get("content_block.type").String() == "thinking" {
					events = append(events, "thinking_start")
					thinkingStartFound = true
					if got := data.Get("index").Int(); got != 0 {
						t.Fatalf("thinking block index = %d, want 0", got)
					}
				}
				if data.Get("content_block.type").String() == "text" {
					events = append(events, "text_start")
					textStartIndex = data.Get("index").Int()
				}
			case "content_block_delta":
				switch data.Get("delta.type").String() {
				case "thinking_delta":
					thinkingDeltaFound = true
				case "signature_delta":
					events = append(events, "signature_delta")
					signatureDeltaFound = true
					if got := data.Get("index").Int(); got != 0 {
						t.Fatalf("signature delta index = %d, want 0", got)
					}
					if got := data.Get("delta.signature").String(); got != "enc_sig_only" {
						t.Fatalf("unexpected signature delta: %q", got)
					}
				}
			case "content_block_stop":
				if data.Get("index").Int() == 0 {
					events = append(events, "thinking_stop")
					thinkingStopFound = true
				}
			}
		}
	}

	if !thinkingStartFound {
		t.Fatal("expected signature-only reasoning to start a thinking block")
	}
	if thinkingDeltaFound {
		t.Fatal("did not expect thinking_delta when upstream omitted summary text")
	}
	if !signatureDeltaFound {
		t.Fatal("expected signature_delta from encrypted_content-only reasoning")
	}
	if !thinkingStopFound {
		t.Fatal("expected signature-only thinking block to stop")
	}
	if textStartIndex != 1 {
		t.Fatalf("text block index = %d, want 1 after signature-only thinking block", textStartIndex)
	}
	if got, want := strings.Join(events, ","), "thinking_start,signature_delta,thinking_stop,text_start"; got != want {
		t.Fatalf("signature-only event order = %s, want %s", got, want)
	}
}

func TestConvertCodexResponseToClaudeNonStream_ThinkingIncludesSignature(t *testing.T) {
	originalRequest := []byte(`{"messages":[]}`)
	response := []byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_123",
			"model":"gpt-5",
			"usage":{"input_tokens":10,"output_tokens":20},
			"output":[
				{
					"type":"reasoning",
					"encrypted_content":"enc_sig_nonstream",
					"summary":[{"type":"summary_text","text":"internal reasoning"}]
				},
				{
					"type":"message",
					"content":[{"type":"output_text","text":"final answer"}]
				}
			]
		}
	}`)

	out := convertResponse(t, "", originalRequest, response)
	parsed := gjson.ParseBytes(out)

	thinking := parsed.Get("content.0")
	if thinking.Get("type").String() != "thinking" {
		t.Fatalf("expected first content block to be thinking, got %s", thinking.Raw)
	}
	if got := thinking.Get("signature").String(); got != "enc_sig_nonstream" {
		t.Fatalf("expected signature to be preserved, got %q", got)
	}
	if got := thinking.Get("thinking").String(); got != "internal reasoning" {
		t.Fatalf("unexpected thinking text: %q", got)
	}
}

func TestConvertCodexResponseToClaude_StreamTextBeforeToolCallsDoesNotEmitGhostStop(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"Read","description":"read"}]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"grok-composer-2.5-fast"}}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"message","status":"in_progress"},"output_index":1}`),
		[]byte(`data: {"type":"response.content_part.added","part":{"type":"output_text"},"content_index":0,"output_index":1}`),
		[]byte(`data: {"type":"response.output_text.delta","delta":"查看项目的 README 和核心入口，以便准确说明项目用途。\n","output_index":1}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_a","name":"Read","status":"in_progress"},"output_index":2}`),
		[]byte(`data: {"type":"response.function_call_arguments.delta","delta":"{\"path\":\"/tmp/README.md\"}","output_index":2}`),
		[]byte(`data: {"type":"response.function_call_arguments.done","arguments":"{\"path\":\"/tmp/README.md\"}","output_index":2}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_a","name":"Read","arguments":"{\"path\":\"/tmp/README.md\"}"},"output_index":2}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_b","name":"Read","status":"in_progress"},"output_index":3}`),
		[]byte(`data: {"type":"response.function_call_arguments.delta","delta":"{\"path\":\"/tmp/main.go\"}","output_index":3}`),
		[]byte(`data: {"type":"response.content_part.done","part":{"type":"output_text"},"content_index":0,"output_index":1}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"message","status":"completed"},"output_index":1}`),
		[]byte(`data: {"type":"response.function_call_arguments.done","arguments":"{\"path\":\"/tmp/main.go\"}","output_index":3}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_b","name":"Read","arguments":"{\"path\":\"/tmp/main.go\"}"},"output_index":3}`),
		[]byte(`data: {"type":"response.completed","response":{"usage":{"input_tokens":1,"output_tokens":1}}}`),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	var startIndices []int64
	var stopIndices []int64
	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			switch data.Get("type").String() {
			case "content_block_start":
				startIndices = append(startIndices, data.Get("index").Int())
			case "content_block_stop":
				stopIndices = append(stopIndices, data.Get("index").Int())
			}
		}
	}

	if len(startIndices) != 3 {
		t.Fatalf("expected 3 content_block_start events (text + 2 tools), got %v", startIndices)
	}
	if len(stopIndices) != 3 {
		t.Fatalf("expected 3 content_block_stop events, got %v", stopIndices)
	}
	if startIndices[0] != 0 || startIndices[1] != 1 || startIndices[2] != 2 {
		t.Fatalf("unexpected start indices: %v", startIndices)
	}
	if stopIndices[0] != 0 || stopIndices[1] != 1 || stopIndices[2] != 2 {
		t.Fatalf("unexpected stop indices: %v", stopIndices)
	}
}

func TestConvertCodexResponseToClaude_StreamFunctionCallDefersStartUntilDoneName(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"web_search","description":"search"}]}`)
	param := NewStream("", originalRequest)

	_ = convertStreamChunk(t, param, []byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`))
	addedOutputs := convertStreamChunk(t, param, []byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1"},"output_index":1}`))
	argumentsOutputs := convertStreamChunk(t, param, []byte(`data: {"type":"response.function_call_arguments.done","arguments":"{\"query\":\"example\"}","output_index":1}`))
	doneOutputs := convertStreamChunk(t, param, []byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_1","name":"web_search","arguments":"{\"query\":\"example\"}"},"output_index":1}`))

	if bytes.Contains(bytes.Join(addedOutputs, nil), []byte(`"content_block_start"`)) {
		t.Fatalf("function_call without name must not emit content_block_start: %q", addedOutputs)
	}
	if bytes.Contains(bytes.Join(argumentsOutputs, nil), []byte(`"input_json_delta"`)) {
		t.Fatalf("arguments must be buffered until the tool name is available: %q", argumentsOutputs)
	}

	var toolStartCount int
	var toolStopCount int
	var argumentDeltas []string
	for _, out := range doneOutputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			switch data.Get("type").String() {
			case "content_block_start":
				if data.Get("content_block.type").String() != "tool_use" {
					continue
				}
				toolStartCount++
				if got := data.Get("content_block.name").String(); got != "web_search" {
					t.Fatalf("unexpected tool_use name %q in %s", got, data.Raw)
				}
			case "content_block_delta":
				if data.Get("delta.type").String() == "input_json_delta" {
					argumentDeltas = append(argumentDeltas, data.Get("delta.partial_json").String())
				}
			case "content_block_stop":
				toolStopCount++
			}
		}
	}

	if toolStartCount != 1 {
		t.Fatalf("expected one deferred tool_use start, got %d in %q", toolStartCount, doneOutputs)
	}
	if len(argumentDeltas) != 1 || argumentDeltas[0] != `{"query":"example"}` {
		t.Fatalf("unexpected buffered argument deltas: %v", argumentDeltas)
	}
	if toolStopCount != 1 {
		t.Fatalf("expected one deferred tool_use stop, got %d in %q", toolStopCount, doneOutputs)
	}
}

func TestConvertCodexResponseToClaude_StreamUnnamedFunctionCallDoneByCallIDKeepsPendingSlots(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"lookup","description":"lookup"}]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_first"},"output_index":1}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_second"},"output_index":2}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_first","name":"lookup","arguments":"{\"id\":1}"}}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"function_call","call_id":"call_second","name":"lookup","arguments":"{\"id\":2}"}}`),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	var toolIDs []string
	var startIndices []int64
	var stopIndices []int64
	var argumentDeltas []string
	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			switch data.Get("type").String() {
			case "content_block_start":
				if data.Get("content_block.type").String() == "tool_use" {
					toolIDs = append(toolIDs, data.Get("content_block.id").String())
					startIndices = append(startIndices, data.Get("index").Int())
				}
			case "content_block_delta":
				if data.Get("delta.type").String() == "input_json_delta" {
					argumentDeltas = append(argumentDeltas, data.Get("delta.partial_json").String())
				}
			case "content_block_stop":
				stopIndices = append(stopIndices, data.Get("index").Int())
			}
		}
	}

	if len(toolIDs) != 2 || toolIDs[0] != "call_first" || toolIDs[1] != "call_second" {
		t.Fatalf("unexpected tool IDs: %v; outputs=%q", toolIDs, outputs)
	}
	if len(startIndices) != 2 || startIndices[0] != 0 || startIndices[1] != 1 {
		t.Fatalf("unexpected start indices: %v; outputs=%q", startIndices, outputs)
	}
	if len(stopIndices) != 2 || stopIndices[0] != 0 || stopIndices[1] != 1 {
		t.Fatalf("unexpected stop indices: %v; outputs=%q", stopIndices, outputs)
	}
	if len(argumentDeltas) != 2 || argumentDeltas[0] != `{"id":1}` || argumentDeltas[1] != `{"id":2}` {
		t.Fatalf("unexpected argument deltas: %v; outputs=%q", argumentDeltas, outputs)
	}
}

func TestConvertCodexResponseToClaude_StreamDeferredUnnamedFunctionCallDoesNotReserveBlockIndex(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"lookup","description":"lookup"}]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_hidden"},"output_index":1}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]},"output_index":2}`),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			if data.Get("type").String() == "content_block_start" && data.Get("content_block.type").String() == "text" {
				if got := data.Get("index").Int(); got != 0 {
					t.Fatalf("text block index = %d, want 0; outputs=%q", got, outputs)
				}
				return
			}
		}
	}

	t.Fatalf("missing text content_block_start; outputs=%q", outputs)
}

func TestConvertCodexResponseToClaude_StreamTerminalOutputHydratesOpenFunctionCallArguments(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"lookup","description":"lookup"}]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1","name":"lookup"},"output_index":1}`),
		[]byte(`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"example\"}"}]}}`),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	var finalArgumentPosition = -1
	var stopPosition = -1
	var messageDeltaPosition = -1
	position := 0
	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			position++
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			switch data.Get("type").String() {
			case "content_block_delta":
				if data.Get("delta.type").String() == "input_json_delta" && data.Get("delta.partial_json").String() == `{"query":"example"}` {
					finalArgumentPosition = position
				}
			case "content_block_stop":
				if data.Get("index").Int() == 0 {
					stopPosition = position
				}
			case "message_delta":
				messageDeltaPosition = position
			}
		}
	}

	if finalArgumentPosition == -1 {
		t.Fatalf("missing terminal argument delta; outputs=%q", outputs)
	}
	if stopPosition == -1 {
		t.Fatalf("missing content_block_stop for open function call; outputs=%q", outputs)
	}
	if messageDeltaPosition == -1 {
		t.Fatalf("missing message_delta; outputs=%q", outputs)
	}
	if !(finalArgumentPosition < stopPosition && stopPosition < messageDeltaPosition) {
		t.Fatalf("unexpected event order: args=%d stop=%d message_delta=%d; outputs=%q", finalArgumentPosition, stopPosition, messageDeltaPosition, outputs)
	}
}

func TestConvertCodexResponseToClaude_StreamTerminalOutputEmitsPendingUnnamedFunctionCall(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"lookup","description":"lookup"}]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_1"},"output_index":1}`),
		[]byte(`data: {"type":"response.function_call_arguments.done","arguments":"{\"query\":\"example\"}","output_index":1}`),
		[]byte(`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"example\"}"}]}}`),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}
	outputText := string(bytes.Join(outputs, nil))

	if strings.Count(outputText, `"type":"tool_use"`) != 1 {
		t.Fatalf("expected one terminal tool_use block, got output:\n%s", outputText)
	}
	if !strings.Contains(outputText, `"name":"lookup"`) || !strings.Contains(outputText, `"partial_json":"{\"query\":\"example\"}"`) {
		t.Fatalf("expected terminal tool name and arguments, got output:\n%s", outputText)
	}
	gotReason, ok := findClaudeStreamStopReason(outputs)
	if !ok {
		t.Fatalf("missing message_delta; outputs=%q", outputs)
	}
	if gotReason != "tool_use" {
		t.Fatalf("stop_reason = %q, want tool_use. Outputs=%q", gotReason, outputs)
	}
	toolUsePosition := strings.Index(outputText, `"type":"tool_use"`)
	messageDeltaPosition := strings.Index(outputText, `"type":"message_delta"`)
	if toolUsePosition < 0 || messageDeltaPosition < 0 || toolUsePosition > messageDeltaPosition {
		t.Fatalf("terminal tool_use must be emitted before message_delta:\n%s", outputText)
	}
}

func TestStreamRejectsUnresolvedPendingFunctionCall(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"name":"lookup","description":"lookup"}]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_hidden"},"output_index":1}`),
		[]byte(`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":{"input_tokens":1,"output_tokens":1},"output":[]}}`),
	}

	var outputs [][]byte
	for _, chunk := range chunks[:len(chunks)-1] {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}
	terminal, err := param.Convert(chunks[len(chunks)-1])
	if err == nil || len(terminal) != 0 {
		t.Fatalf("unresolved tool call must fail without a successful terminal event: %q, %v", terminal, err)
	}
	outputText := string(bytes.Join(outputs, nil))

	if strings.Contains(outputText, `"type":"tool_use"`) {
		t.Fatalf("unresolved pending function_call must not emit tool_use:\n%s", outputText)
	}
	if _, ok := findClaudeStreamStopReason(outputs); ok {
		t.Fatalf("unresolved tool call emitted successful stop: %q", outputs)
	}
}

func TestConvertCodexResponseToClaude_StreamEmptyOutputUsesOutputItemDoneMessageFallback(t *testing.T) {
	originalRequest := []byte(`{"tools":[]}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\",\"model\":\"gpt-5\"}}"),
		[]byte("data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"ok\"}]},\"output_index\":0}"),
		[]byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}"),
	}

	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}

	foundText := false
	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			if data.Get("type").String() == "content_block_delta" && data.Get("delta.type").String() == "text_delta" && data.Get("delta.text").String() == "ok" {
				foundText = true
				break
			}
		}
		if foundText {
			break
		}
	}
	if !foundText {
		t.Fatalf("expected fallback content from response.output_item.done message; outputs=%q", outputs)
	}
}

func TestConvertCodexResponseToClaude_StreamWebSearchCallKeepsHistoryPortable(t *testing.T) {
	originalRequest := []byte(`{
		"tools":[{"type":"web_search_20250305","name":"web_search"}],
		"messages":[{"role":"user","content":"search weather"}]
	}`)
	param := NewStream("", originalRequest)

	chunks := [][]byte{
		[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5.4"}}`),
		[]byte(`data: {"type":"response.output_item.added","item":{"id":"ws_123","type":"web_search_call","status":"in_progress"}}`),
		[]byte(`data: {"type":"response.web_search_call.searching","item_id":"ws_123"}`),
		[]byte(`data: {"type":"response.web_search_call.completed","item_id":"ws_123"}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"id":"ws_123","type":"web_search_call","status":"completed","action":{"type":"search","query":"search weather","sources":[{"url":"https://example.com","title":"Example"}]}}}`),
		[]byte(`data: {"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"done"}]}}`),
		[]byte(`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":{"input_tokens":3,"output_tokens":2}}}`),
	}
	var outputs [][]byte
	for _, chunk := range chunks {
		outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
	}
	outputText := string(bytes.Join(outputs, nil))

	if strings.Contains(outputText, `"type":"server_tool_use"`) || strings.Contains(outputText, `"type":"web_search_tool_result"`) {
		t.Fatalf("stream exposed synthetic Anthropic search blocks without encrypted_content:\n%s", outputText)
	}
	if !strings.Contains(outputText, `"delta":{"type":"text_delta","text":"done"}`) {
		t.Fatalf("stream lost the search-grounded answer:\n%s", outputText)
	}
	if !strings.Contains(outputText, `event: message_stop`) {
		t.Fatalf("stream did not complete:\n%s", outputText)
	}
}

func TestConvertCodexResponseToClaude_ShortensLongToolUseIDs(t *testing.T) {
	longCallID := "call_" + strings.Repeat("a", 62)
	if len(longCallID) <= 64 {
		t.Fatalf("test setup error: longCallID length = %d, want > 64", len(longCallID))
	}

	t.Run("stream", func(t *testing.T) {
		originalRequest := []byte(`{"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]}`)
		param := NewStream("", originalRequest)

		outputs := convertStreamChunk(t, param, []byte(`data: {"type":"response.output_item.added","item":{"type":"function_call","call_id":"`+longCallID+`","name":"lookup"}}`))

		toolID := ""
		for _, out := range outputs {
			for _, line := range strings.Split(string(out), "\n") {
				if !strings.HasPrefix(line, "data: ") {
					continue
				}
				data := gjson.Parse(strings.TrimPrefix(line, "data: "))
				if data.Get("type").String() == "content_block_start" && data.Get("content_block.type").String() == "tool_use" {
					toolID = data.Get("content_block.id").String()
				}
			}
		}

		if toolID == "" {
			t.Fatalf("missing stream tool_use block. Outputs=%q", outputs)
		}
		if len(toolID) > 64 {
			t.Fatalf("stream tool_use id length = %d, want <= 64: %q", len(toolID), toolID)
		}
		if toolID == longCallID {
			t.Fatalf("stream tool_use id was not shortened: %q", toolID)
		}
	})

	t.Run("nonstream", func(t *testing.T) {
		originalRequest := []byte(`{"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]}`)
		response := []byte(`{
			"type":"response.completed",
			"response":{
				"id":"resp_1",
				"model":"gpt-5",
				"usage":{"input_tokens":1,"output_tokens":1},
				"output":[{"type":"function_call","call_id":"` + longCallID + `","name":"lookup","arguments":"{}"}]
			}
		}`)

		out := convertResponse(t, "", originalRequest, response)
		toolID := gjson.GetBytes(out, "content.0.id").String()
		if toolID == "" {
			t.Fatalf("missing nonstream tool_use id. Output: %s", string(out))
		}
		if len(toolID) > 64 {
			t.Fatalf("nonstream tool_use id length = %d, want <= 64: %q", len(toolID), toolID)
		}
		if toolID == longCallID {
			t.Fatalf("nonstream tool_use id was not shortened: %q", toolID)
		}
	})
}

func TestConvertCodexResponseToClaude_StreamStopReasonMapping(t *testing.T) {
	tests := []struct {
		name       string
		chunks     [][]byte
		wantReason string
	}{
		{
			name: "Stop maps to end_turn",
			chunks: [][]byte{
				[]byte("data: {\"type\":\"response.completed\",\"response\":{\"stop_reason\":\"stop\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}"),
			},
			wantReason: "end_turn",
		},
		{
			name: "Incomplete max output maps to max_tokens",
			chunks: [][]byte{
				[]byte(`data: {"type":"response.output_text.delta","delta":"Partial answer"}`),
				[]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}"),
			},
			wantReason: "max_tokens",
		},
		{
			name: "Tool call wins over stop",
			chunks: [][]byte{
				[]byte("data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"call_id\":\"call_1\",\"name\":\"lookup\"}}"),
				[]byte(`data: {"type":"response.function_call_arguments.done","call_id":"call_1","arguments":"{}"}`),
				[]byte("data: {\"type\":\"response.completed\",\"response\":{\"stop_reason\":\"stop\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}"),
			},
			wantReason: "tool_use",
		},
		{
			name: "Content filter maps to Claude refusal",
			chunks: [][]byte{
				[]byte(`data: {"type":"response.refusal.delta","delta":"Cannot comply"}`),
				[]byte("data: {\"type\":\"response.incomplete\",\"response\":{\"incomplete_details\":{\"reason\":\"content_filter\"},\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}"),
			},
			wantReason: "refusal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRequest := []byte(`{"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]}`)
			param := NewStream("", originalRequest)
			var outputs [][]byte

			for _, chunk := range tt.chunks {
				outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
			}

			got, ok := findClaudeStreamStopReason(outputs)
			if !ok {
				t.Fatalf("did not find message_delta stop_reason; outputs=%q", outputs)
			}
			if got != tt.wantReason {
				t.Fatalf("stop_reason = %q, want %q. Outputs=%q", got, tt.wantReason, outputs)
			}
		})
	}
}

func TestConvertCodexResponseToClaude_StreamStopSequenceMapping(t *testing.T) {
	originalRequest := []byte(`{"messages":[]}`)
	param := NewStream("", originalRequest)

	outputs := convertStreamChunk(t, param, []byte("data: {\"type\":\"response.completed\",\"response\":{\"stop_reason\":\"stop\",\"stop_sequence\":\"\\nEND\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}"))
	messageDelta, ok := findClaudeStreamMessageDelta(outputs)
	if !ok {
		t.Fatalf("did not find message_delta; outputs=%q", outputs)
	}
	if got := messageDelta.Get("delta.stop_reason").String(); got != "stop_sequence" {
		t.Fatalf("stop_reason = %q, want stop_sequence. Outputs=%q", got, outputs)
	}
	if got := messageDelta.Get("delta.stop_sequence").String(); got != "\nEND" {
		t.Fatalf("stop_sequence = %q, want newline END. Outputs=%q", got, outputs)
	}
}

func TestConvertCodexResponseToClaudeNonStream_WebSearchCallKeepsHistoryPortable(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"search weather"}]}`)
	response := []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.3-codex-spark","stop_reason":"stop","usage":{"input_tokens":3,"output_tokens":2},"output":[{"type":"web_search_call","id":"ws_123","status":"completed","action":{"type":"search","query":"search weather","sources":[{"url":"https://example.com","title":"Example"}]}},{"type":"message","content":[{"type":"output_text","text":"done"}]}]}}`)
	out := convertResponse(t, "", originalRequest, response)
	parsed := gjson.ParseBytes(out)
	if strings.Contains(string(out), `"type":"server_tool_use"`) || strings.Contains(string(out), `"type":"web_search_tool_result"`) {
		t.Fatalf("non-stream response exposed synthetic Anthropic search blocks without encrypted_content: %s", string(out))
	}
	if got := parsed.Get("content.0.text").String(); got != "done" {
		t.Fatalf("search-grounded answer = %q, want done; output=%s", got, string(out))
	}
}

func TestConvertCodexResponseToClaudeNonStream_WebSearchStopReasonEndTurn(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"search weather"}]}`)
	response := []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.3-codex-spark","stop_reason":"stop","usage":{"input_tokens":3,"output_tokens":2},"output":[{"type":"web_search_call","id":"ws_123","status":"completed","action":{"type":"search","query":"search weather"}},{"type":"message","content":[{"type":"output_text","text":"done"}]}]}}`)
	out := convertResponse(t, "", originalRequest, response)
	parsed := gjson.ParseBytes(out)
	if got := parsed.Get("stop_reason").String(); got != "end_turn" {
		t.Fatalf("stop_reason = %q, want end_turn when search activity precedes text", got)
	}
}

func TestConvertCodexResponseToClaudeNonStream_OmitsAllWebSearchActivity(t *testing.T) {
	originalRequest := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[{"role":"user","content":"q"}]}`)
	response := []byte(`{"type":"response.completed","response":{"id":"resp_1","model":"gpt-5.3-codex-spark","stop_reason":"stop","usage":{"input_tokens":1,"output_tokens":1},"output":[{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"open_page"}},{"type":"web_search_call","id":"ws_1","status":"completed","action":{"type":"search","query":"weather"}},{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}}`)
	out := convertResponse(t, "", originalRequest, response)
	if strings.Contains(string(out), `"type":"server_tool_use"`) || strings.Contains(string(out), `"type":"web_search_tool_result"`) {
		t.Fatalf("search activity leaked into portable history: %s", string(out))
	}
	if got := gjson.GetBytes(out, "content.0.text").String(); got != "ok" {
		t.Fatalf("answer = %q, want ok; output=%s", got, string(out))
	}
}

func TestConvertCodexResponseToClaudeNonStream_StopReasonMapping(t *testing.T) {
	tests := []struct {
		name       string
		response   []byte
		wantReason string
	}{
		{
			name: "Stop maps to end_turn",
			response: []byte(`{
				"type":"response.completed",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"stop_reason":"stop",
					"usage":{"input_tokens":1,"output_tokens":1},
					"output":[]
				}
			}`),
			wantReason: "end_turn",
		},
		{
			name: "Incomplete max output maps to max_tokens",
			response: []byte(`{
				"type":"response.incomplete",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"incomplete_details":{"reason":"max_output_tokens"},
					"usage":{"input_tokens":1,"output_tokens":1},
					"output":[{"type":"message","content":[{"type":"output_text","text":"Partial answer"}]}]
				}
			}`),
			wantReason: "max_tokens",
		},
		{
			name: "Tool call wins over stop",
			response: []byte(`{
				"type":"response.completed",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"stop_reason":"stop",
					"usage":{"input_tokens":1,"output_tokens":1},
					"output":[{"type":"function_call","call_id":"call_1","name":"lookup","arguments":"{}"}]
				}
			}`),
			wantReason: "tool_use",
		},
		{
			name: "Content filter maps to Claude refusal",
			response: []byte(`{
				"type":"response.incomplete",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"incomplete_details":{"reason":"content_filter"},
					"usage":{"input_tokens":1,"output_tokens":1},
					"output":[{"type":"message","content":[{"type":"refusal","refusal":"Cannot comply"}]}]
				}
			}`),
			wantReason: "refusal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRequest := []byte(`{"tools":[{"name":"lookup","input_schema":{"type":"object","properties":{}}}]}`)
			out := convertResponse(t, "", originalRequest, tt.response)
			parsed := gjson.ParseBytes(out)

			if got := parsed.Get("stop_reason").String(); got != tt.wantReason {
				t.Fatalf("stop_reason = %q, want %q. Output: %s", got, tt.wantReason, string(out))
			}
		})
	}
}

func TestConvertCodexResponseToClaudeNonStream_StopSequenceMapping(t *testing.T) {
	originalRequest := []byte(`{"messages":[]}`)
	response := []byte(`{
		"type":"response.completed",
		"response":{
			"id":"resp_1",
			"model":"gpt-5",
			"stop_reason":"stop",
			"stop_sequence":"\nEND",
			"usage":{"input_tokens":1,"output_tokens":1},
			"output":[]
		}
	}`)

	out := convertResponse(t, "", originalRequest, response)
	parsed := gjson.ParseBytes(out)

	if got := parsed.Get("stop_reason").String(); got != "stop_sequence" {
		t.Fatalf("stop_reason = %q, want stop_sequence. Output: %s", got, string(out))
	}
	if got := parsed.Get("stop_sequence").String(); got != "\nEND" {
		t.Fatalf("stop_sequence = %q, want newline END. Output: %s", got, string(out))
	}
}

func findClaudeStreamStopReason(outputs [][]byte) (string, bool) {
	messageDelta, ok := findClaudeStreamMessageDelta(outputs)
	if !ok {
		return "", false
	}
	return messageDelta.Get("delta.stop_reason").String(), true
}

func findClaudeStreamMessageDelta(outputs [][]byte) (gjson.Result, bool) {
	for _, out := range outputs {
		for _, line := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := gjson.Parse(strings.TrimPrefix(line, "data: "))
			if data.Get("type").String() == "message_delta" {
				return data, true
			}
		}
	}
	return gjson.Result{}, false
}

func firstClaudeStreamPayloadForEvent(output, event string) (gjson.Result, bool) {
	var currentEvent string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "event: ") {
			currentEvent = strings.TrimPrefix(line, "event: ")
			continue
		}
		if currentEvent != event || !strings.HasPrefix(line, "data: ") {
			continue
		}
		return gjson.Parse(strings.TrimPrefix(line, "data: ")), true
	}
	return gjson.Result{}, false
}

func TestConvertCodexResponseToClaude_StreamPreservesCacheWriteUsage(t *testing.T) {
	tests := []struct {
		name                 string
		terminalUsageJSON    string
		wantInputTokens      int64
		wantOutputTokens     int64
		wantCacheReadTokens  int64
		wantCacheWriteTokens int64
	}{
		{
			name:                 "cache_write_tokens field",
			terminalUsageJSON:    `{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":800,"cache_write_tokens":150}}`,
			wantInputTokens:      200,
			wantOutputTokens:     200,
			wantCacheReadTokens:  800,
			wantCacheWriteTokens: 150,
		},
		{
			name:                 "cache_creation_tokens field alias",
			terminalUsageJSON:    `{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":800,"cache_creation_tokens":150}}`,
			wantInputTokens:      200,
			wantOutputTokens:     200,
			wantCacheReadTokens:  800,
			wantCacheWriteTokens: 150,
		},
		{
			name:                 "cached_tokens greater than input_tokens clamps input_tokens to zero",
			terminalUsageJSON:    `{"input_tokens":500,"output_tokens":100,"input_tokens_details":{"cached_tokens":800,"cache_write_tokens":50}}`,
			wantInputTokens:      0,
			wantOutputTokens:     100,
			wantCacheReadTokens:  800,
			wantCacheWriteTokens: 50,
		},
		{
			name:                 "zero cache_write_tokens does not emit cache_creation_input_tokens",
			terminalUsageJSON:    `{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":800,"cache_write_tokens":0}}`,
			wantInputTokens:      200,
			wantOutputTokens:     200,
			wantCacheReadTokens:  800,
			wantCacheWriteTokens: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRequest := []byte(`{"messages":[]}`)
			param := NewStream("", originalRequest)

			chunks := [][]byte{
				[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`),
				[]byte(`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`),
				[]byte(fmt.Sprintf(`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":%s}}`, tt.terminalUsageJSON)),
			}

			var outputs [][]byte
			for _, chunk := range chunks {
				outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
			}

			delta, ok := findClaudeStreamMessageDelta(outputs)
			if !ok {
				t.Fatalf("missing message_delta event; outputs=%q", outputs)
			}

			usage := delta.Get("usage")
			if got := usage.Get("input_tokens").Int(); got != tt.wantInputTokens {
				t.Fatalf("input_tokens = %d, want %d", got, tt.wantInputTokens)
			}
			if got := usage.Get("output_tokens").Int(); got != tt.wantOutputTokens {
				t.Fatalf("output_tokens = %d, want %d", got, tt.wantOutputTokens)
			}
			if got := usage.Get("cache_read_input_tokens").Int(); got != tt.wantCacheReadTokens {
				t.Fatalf("cache_read_input_tokens = %d, want %d", got, tt.wantCacheReadTokens)
			}
			if tt.wantCacheWriteTokens == 0 {
				if usage.Get("cache_creation_input_tokens").Exists() {
					t.Fatalf("cache_creation_input_tokens should not be emitted when zero; got %v", usage.Get("cache_creation_input_tokens").Raw)
				}
			} else if got := usage.Get("cache_creation_input_tokens").Int(); got != tt.wantCacheWriteTokens {
				t.Fatalf("cache_creation_input_tokens = %d, want %d", got, tt.wantCacheWriteTokens)
			}
		})
	}
}

func TestConvertCodexResponseToClaudeNonStream_PreservesCacheWriteUsage(t *testing.T) {
	tests := []struct {
		name                 string
		responseJSON         string
		wantInputTokens      int64
		wantOutputTokens     int64
		wantCacheReadTokens  int64
		wantCacheWriteTokens int64
	}{
		{
			name: "cache_write_tokens field",
			responseJSON: `{
				"type":"response.completed",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"stop_reason":"stop",
					"usage":{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":800,"cache_write_tokens":150}},
					"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]
				}
			}`,
			wantInputTokens:      200,
			wantOutputTokens:     200,
			wantCacheReadTokens:  800,
			wantCacheWriteTokens: 150,
		},
		{
			name: "cache_creation_tokens alias",
			responseJSON: `{
				"type":"response.completed",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"stop_reason":"stop",
					"usage":{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":800,"cache_creation_tokens":150}},
					"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]
				}
			}`,
			wantInputTokens:      200,
			wantOutputTokens:     200,
			wantCacheReadTokens:  800,
			wantCacheWriteTokens: 150,
		},
		{
			name: "cached_tokens greater than input_tokens clamps input_tokens to zero",
			responseJSON: `{
				"type":"response.completed",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"stop_reason":"stop",
					"usage":{"input_tokens":500,"output_tokens":100,"input_tokens_details":{"cached_tokens":800,"cache_write_tokens":50}},
					"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]
				}
			}`,
			wantInputTokens:      0,
			wantOutputTokens:     100,
			wantCacheReadTokens:  800,
			wantCacheWriteTokens: 50,
		},
		{
			name: "zero cache_write_tokens does not emit cache_creation_input_tokens",
			responseJSON: `{
				"type":"response.completed",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"stop_reason":"stop",
					"usage":{"input_tokens":1000,"output_tokens":200,"input_tokens_details":{"cached_tokens":800,"cache_write_tokens":0}},
					"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]
				}
			}`,
			wantInputTokens:      200,
			wantOutputTokens:     200,
			wantCacheReadTokens:  800,
			wantCacheWriteTokens: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRequest := []byte(`{"messages":[]}`)
			out := convertResponse(t, "", originalRequest, []byte(tt.responseJSON))
			parsed := gjson.ParseBytes(out)

			usage := parsed.Get("usage")
			if got := usage.Get("input_tokens").Int(); got != tt.wantInputTokens {
				t.Fatalf("input_tokens = %d, want %d", got, tt.wantInputTokens)
			}
			if got := usage.Get("output_tokens").Int(); got != tt.wantOutputTokens {
				t.Fatalf("output_tokens = %d, want %d", got, tt.wantOutputTokens)
			}
			if got := usage.Get("cache_read_input_tokens").Int(); got != tt.wantCacheReadTokens {
				t.Fatalf("cache_read_input_tokens = %d, want %d", got, tt.wantCacheReadTokens)
			}
			if tt.wantCacheWriteTokens == 0 {
				if usage.Get("cache_creation_input_tokens").Exists() {
					t.Fatalf("cache_creation_input_tokens should not be emitted when zero; got %v", usage.Get("cache_creation_input_tokens").Raw)
				}
			} else if got := usage.Get("cache_creation_input_tokens").Int(); got != tt.wantCacheWriteTokens {
				t.Fatalf("cache_creation_input_tokens = %d, want %d", got, tt.wantCacheWriteTokens)
			}
		})
	}
}

func TestConvertCodexResponseToClaude_PreservesReasoningUsage(t *testing.T) {
	tests := []struct {
		name                string
		terminalUsageJSON   string
		wantOutputTokens    int64
		wantReasoningExist  bool
		wantReasoningTokens int64
	}{
		{
			name:                "preserves positive reasoning tokens",
			terminalUsageJSON:   `{"input_tokens":420,"output_tokens":518,"output_tokens_details":{"reasoning_tokens":163},"total_tokens":938}`,
			wantOutputTokens:    518,
			wantReasoningExist:  true,
			wantReasoningTokens: 163,
		},
		{
			name:                "preserves explicit zero reasoning tokens",
			terminalUsageJSON:   `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":0}}`,
			wantOutputTokens:    50,
			wantReasoningExist:  true,
			wantReasoningTokens: 0,
		},
		{
			name:               "omits reasoning detail when absent",
			terminalUsageJSON:  `{"input_tokens":100,"output_tokens":50}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:                "clamps oversized reasoning tokens to output tokens",
			terminalUsageJSON:   `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":999}}`,
			wantOutputTokens:    50,
			wantReasoningExist:  true,
			wantReasoningTokens: 50,
		},
		{
			name:               "rejects negative reasoning tokens",
			terminalUsageJSON:  `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":-5}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:               "rejects negative float reasoning tokens",
			terminalUsageJSON:  `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":-0.5}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:                "clamps oversized int64 overflow reasoning tokens to output tokens",
			terminalUsageJSON:   `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":9223372036854775808}}`,
			wantOutputTokens:    50,
			wantReasoningExist:  true,
			wantReasoningTokens: 50,
		},
		{
			name:               "omits string reasoning detail",
			terminalUsageJSON:  `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":"163"}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:               "omits boolean reasoning detail",
			terminalUsageJSON:  `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":true}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:               "omits null reasoning detail",
			terminalUsageJSON:  `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":null}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRequest := []byte(`{"messages":[]}`)
			param := NewStream("", originalRequest)

			chunks := [][]byte{
				[]byte(`data: {"type":"response.created","response":{"id":"resp_1","model":"gpt-5"}}`),
				[]byte(`data: {"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}}`),
				[]byte(fmt.Sprintf(`data: {"type":"response.completed","response":{"stop_reason":"stop","usage":%s}}`, tt.terminalUsageJSON)),
			}

			var outputs [][]byte
			for _, chunk := range chunks {
				outputs = append(outputs, convertStreamChunk(t, param, chunk)...)
			}

			delta, ok := findClaudeStreamMessageDelta(outputs)
			if !ok {
				t.Fatalf("missing message_delta event; outputs=%q", outputs)
			}

			usage := delta.Get("usage")
			if got := usage.Get("output_tokens").Int(); got != tt.wantOutputTokens {
				t.Fatalf("output_tokens = %d, want %d", got, tt.wantOutputTokens)
			}
			thinkingNode := usage.Get("output_tokens_details.thinking_tokens")
			if tt.wantReasoningExist {
				if !thinkingNode.Exists() {
					t.Fatalf("expected output_tokens_details.thinking_tokens to exist, got none in %s", usage.Raw)
				}
				if got := thinkingNode.Int(); got != tt.wantReasoningTokens {
					t.Fatalf("thinking_tokens = %d, want %d", got, tt.wantReasoningTokens)
				}
			} else {
				if thinkingNode.Exists() {
					t.Fatalf("expected output_tokens_details.thinking_tokens to be absent, got %v", thinkingNode.Raw)
				}
			}
		})
	}
}

func TestConvertCodexResponseToClaudeNonStream_PreservesReasoningUsage(t *testing.T) {
	tests := []struct {
		name                string
		usageJSON           string
		wantOutputTokens    int64
		wantReasoningExist  bool
		wantReasoningTokens int64
	}{
		{
			name:                "preserves positive reasoning tokens",
			usageJSON:           `{"input_tokens":420,"output_tokens":518,"output_tokens_details":{"reasoning_tokens":163},"total_tokens":938}`,
			wantOutputTokens:    518,
			wantReasoningExist:  true,
			wantReasoningTokens: 163,
		},
		{
			name:                "preserves explicit zero reasoning tokens",
			usageJSON:           `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":0}}`,
			wantOutputTokens:    50,
			wantReasoningExist:  true,
			wantReasoningTokens: 0,
		},
		{
			name:               "omits reasoning detail when absent",
			usageJSON:          `{"input_tokens":100,"output_tokens":50}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:                "clamps oversized reasoning tokens to output tokens",
			usageJSON:           `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":999}}`,
			wantOutputTokens:    50,
			wantReasoningExist:  true,
			wantReasoningTokens: 50,
		},
		{
			name:               "rejects negative reasoning tokens",
			usageJSON:          `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":-5}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:               "rejects negative float reasoning tokens",
			usageJSON:          `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":-0.5}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:                "clamps oversized int64 overflow reasoning tokens to output tokens",
			usageJSON:           `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":9223372036854775808}}`,
			wantOutputTokens:    50,
			wantReasoningExist:  true,
			wantReasoningTokens: 50,
		},
		{
			name:               "omits string reasoning detail",
			usageJSON:          `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":"163"}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:               "omits boolean reasoning detail",
			usageJSON:          `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":true}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
		{
			name:               "omits null reasoning detail",
			usageJSON:          `{"input_tokens":100,"output_tokens":50,"output_tokens_details":{"reasoning_tokens":null}}`,
			wantOutputTokens:   50,
			wantReasoningExist: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalRequest := []byte(`{"messages":[]}`)
			responseJSON := fmt.Sprintf(`{
				"type":"response.completed",
				"response":{
					"id":"resp_1",
					"model":"gpt-5",
					"stop_reason":"stop",
					"usage":%s,
					"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]
				}
			}`, tt.usageJSON)
			out := convertResponse(t, "", originalRequest, []byte(responseJSON))
			parsed := gjson.ParseBytes(out)

			usage := parsed.Get("usage")
			if got := usage.Get("output_tokens").Int(); got != tt.wantOutputTokens {
				t.Fatalf("output_tokens = %d, want %d", got, tt.wantOutputTokens)
			}
			thinkingNode := usage.Get("output_tokens_details.thinking_tokens")
			if tt.wantReasoningExist {
				if !thinkingNode.Exists() {
					t.Fatalf("expected output_tokens_details.thinking_tokens to exist, got none in %s", usage.Raw)
				}
				if got := thinkingNode.Int(); got != tt.wantReasoningTokens {
					t.Fatalf("thinking_tokens = %d, want %d", got, tt.wantReasoningTokens)
				}
			} else {
				if thinkingNode.Exists() {
					t.Fatalf("expected output_tokens_details.thinking_tokens to be absent, got %v", thinkingNode.Raw)
				}
			}
		})
	}
}
