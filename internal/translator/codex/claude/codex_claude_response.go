package claude

import (
	"bytes"
	"errors"
	"strings"

	translatorcommon "github.com/marenzo/claudex/internal/translator/common"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

var dataTag = []byte("data:")

// codexThinkingSummaryPartSeparator joins consecutive reasoning summary parts inside
// the single thinking block that represents one Codex reasoning item.
const codexThinkingSummaryPartSeparator = "\n\n"

// Stream translates one Codex response into Claude SSE events. It is owned by a
// single request. The proxy handles transport, cancellation, and upstream errors.
type Stream struct {
	model    string
	original []byte
	state    streamState
}

// NewStream fixes the model and original request for one response. The caller
// must not modify original while the stream is in use.
func NewStream(model string, original []byte) *Stream {
	return &Stream{model: model, original: original, state: streamState{
		OmitThinkingSummary: omitThinkingSummary(original),
	}}
}

type streamState struct {
	HasContent                bool
	HasEmittedToolUse         bool
	BlockIndex                int
	HasRefusal                bool
	ContentText               map[codexContentPartKey]*strings.Builder
	ContentOutputIndices      map[string]int64
	TextBlockOpen             bool
	ThinkingBlockOpen         bool
	ThinkingSignature         string
	EmittedThinkingSignatures map[string]struct{}
	ThinkingSummarySeen       bool
	OmitThinkingSummary       bool
	CitationURLs              map[string]struct{}
	FunctionCalls             map[string]*codexFunctionCallStream
	FunctionCallQueue         []*codexFunctionCallStream
	ActiveFunctionCall        *codexFunctionCallStream
	LastFunctionCall          *codexFunctionCallStream
	DeferredStreamEvents      [][]byte
}

// Convert translates an SSE data line. Empty output leaves response headers
// uncommitted. Invalid completed tool arguments fail before their block closes.
// Do not call it again after an error or a terminal response.
func (s *Stream) Convert(rawJSON []byte) ([]byte, error) {
	if !bytes.HasPrefix(rawJSON, dataTag) {
		return nil, nil
	}
	streamEventRawJSON := rawJSON
	rawJSON = bytes.TrimSpace(rawJSON[5:])

	output := make([]byte, 0, 512)
	rootResult := gjson.ParseBytes(rawJSON)
	params := &s.state
	originalRequestRawJSON := s.original
	if err := validateFunctionCallEvent(params, rootResult); err != nil {
		return nil, err
	}

	typeResult := rootResult.Get("type")
	typeStr := typeResult.String()
	if params.ActiveFunctionCall != nil && shouldDeferCodexStreamEvent(typeStr, rootResult) {
		params.DeferredStreamEvents = append(params.DeferredStreamEvents, bytes.Clone(streamEventRawJSON))
		return nil, nil
	}
	var template []byte

	switch typeStr {
	case "response.created":
		template = []byte(`{"type":"message_start","message":{"id":"","type":"message","role":"assistant","model":"","stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0},"content":[],"stop_reason":null}}`)
		responseModel := rootResult.Get("response.model").String()
		if responseModel == "" {
			responseModel = s.model
		}
		template, _ = sjson.SetBytes(template, "message.model", responseModel)
		template, _ = sjson.SetBytes(template, "message.id", rootResult.Get("response.id").String())

		output = translatorcommon.AppendSSEEventBytes(output, "message_start", template, 2)
	case "response.reasoning_summary_part.added":
		if params.OmitThinkingSummary {
			break
		}
		output = append(output, stopCodexTextBlock(params)...)
		// Codex splits a single reasoning item into several summary parts, but only
		// output_item.done carries that item's final encrypted_content. Keep one
		// thinking block open for the whole item and separate the parts with a blank
		// line, so the only signature ever emitted is the final one.
		if params.ThinkingBlockOpen {
			output = append(output, appendCodexThinkingDelta(params, codexThinkingSummaryPartSeparator)...)
		} else {
			output = append(output, startCodexThinkingBlock(params)...)
		}
		params.ThinkingSummarySeen = true
	case "response.reasoning_summary_text.delta":
		if params.OmitThinkingSummary {
			break
		}
		output = append(output, stopCodexTextBlock(params)...)
		output = append(output, startCodexThinkingBlock(params)...)
		output = append(output, appendCodexThinkingDelta(params, rootResult.Get("delta").String())...)
	case "response.reasoning_summary_part.done":
		// Intentionally does not close the thinking block: it stays open until
		// output_item.done delivers the reasoning item's final encrypted_content.
	case "response.content_part.added":
		output = append(output, finalizeCodexThinkingBlock(params)...)
		partType := rootResult.Get("part.type").String()
		if partType == "refusal" {
			params.HasRefusal = true
		}
		if partType == "output_text" || partType == "refusal" {
			output = append(output, startCodexTextBlock(params)...)
		}
	case "response.output_text.delta", "response.output_text.done":
		text := rootResult.Get("text").String()
		isDelta := typeStr == "response.output_text.delta"
		if isDelta {
			text = rootResult.Get("delta").String()
		}
		output = appendCodexContentText(output, params, codexContentOutputIndex(params, rootResult), rootResult.Get("content_index").Int(), text, isDelta)
	case "response.refusal.delta", "response.refusal.done":
		text := rootResult.Get("refusal").String()
		isDelta := typeStr == "response.refusal.delta"
		if isDelta {
			text = rootResult.Get("delta").String()
		}
		params.HasRefusal = true
		output = appendCodexContentText(output, params, codexContentOutputIndex(params, rootResult), rootResult.Get("content_index").Int(), text, isDelta)
	case "response.output_text.annotation.added":
		annotations := gjson.Parse("[" + rootResult.Get("annotation").Raw + "]")
		output = appendCodexCitationLinks(output, params, annotations)
	case "response.content_part.done":
		output = appendCodexCitationLinks(output, params, rootResult.Get("part.annotations"))
		partType := rootResult.Get("part.type").String()
		if partType == "refusal" {
			params.HasRefusal = true
			output = appendCodexContentText(output, params, codexContentOutputIndex(params, rootResult), rootResult.Get("content_index").Int(), rootResult.Get("part.refusal").String(), false)
		} else if partType == "output_text" {
			output = appendCodexContentText(output, params, codexContentOutputIndex(params, rootResult), rootResult.Get("content_index").Int(), rootResult.Get("part.text").String(), false)
		}
		if partType == "output_text" || partType == "refusal" {
			output = append(output, stopCodexTextBlock(params)...)
		}
	case "response.web_search_call.searching", "response.web_search_call.completed", "response.web_search_call.in_progress":
		// Wait for populated web_search_call items on output_item.done.
	case "response.completed", "response.incomplete":
		template = []byte(`{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"input_tokens":0,"output_tokens":0}}`)
		responseData := rootResult.Get("response")
		output = append(output, finalizeCodexThinkingBlock(params)...)
		output = append(output, stopCodexTextBlock(params)...)
		output = appendCodexFunctionCallsFromTerminal(output, params, originalRequestRawJSON, responseData)
		var err error
		output, err = s.appendDeferredEvents(output)
		if err != nil {
			return nil, err
		}
		output = appendCodexTerminalContent(output, params, responseData)
		for _, item := range responseData.Get("output").Array() {
			for _, part := range item.Get("content").Array() {
				output = appendCodexCitationLinks(output, params, part.Get("annotations"))
			}
		}
		output = append(output, finalizeCodexThinkingBlock(params)...)
		output = append(output, stopCodexTextBlock(params)...)
		if typeStr == "response.incomplete" && !params.HasContent {
			return nil, errors.New("incomplete response without usable content from Codex")
		}
		template, _ = sjson.SetBytes(template, "delta.stop_reason", mapCodexStopReasonToClaude(codexStopReason(responseData), params.HasEmittedToolUse, params.HasRefusal))
		template = setClaudeStopSequence(template, "delta.stop_sequence", responseData)
		inputTokens, outputTokens, cachedTokens, cacheWriteTokens := extractResponsesUsage(responseData.Get("usage"))
		template, _ = sjson.SetBytes(template, "usage.input_tokens", inputTokens)
		template, _ = sjson.SetBytes(template, "usage.output_tokens", outputTokens)
		if cachedTokens > 0 {
			template, _ = sjson.SetBytes(template, "usage.cache_read_input_tokens", cachedTokens)
		}
		if cacheWriteTokens > 0 {
			template, _ = sjson.SetBytes(template, "usage.cache_creation_input_tokens", cacheWriteTokens)
		}
		template = setClaudeReasoningUsage(template, responseData.Get("usage"))

		output = translatorcommon.AppendSSEEventBytes(output, "message_delta", template, 2)
		output = translatorcommon.AppendSSEEventBytes(output, "message_stop", []byte(`{"type":"message_stop"}`), 2)
	case "response.output_item.added":
		itemResult := rootResult.Get("item")
		itemType := itemResult.Get("type").String()
		switch itemType {
		case "function_call":
			output = append(output, finalizeCodexThinkingBlock(params)...)
			output = append(output, stopCodexTextBlock(params)...)

			call := recordCodexFunctionCall(params, rootResult, itemResult)
			updateCodexFunctionCallIdentity(params, call, rootResult, itemResult)
			if call.Name != "" {
				call.EmitInitialEmptyDelta = true
			}
			output = appendCodexFunctionCallQueue(output, params, originalRequestRawJSON)
		case "reasoning":
			output = append(output, stopCodexTextBlock(params)...)
			// A previous reasoning item that never reported output_item.done must not
			// leak its still-open block into this one.
			output = append(output, finalizeCodexThinkingBlock(params)...)
			params.ThinkingSummarySeen = false
			// Kept only as a fallback for streams whose output_item.done omits
			// encrypted_content; it is a pre-content snapshot, never the final value.
			params.ThinkingSignature = itemResult.Get("encrypted_content").String()
		case "web_search_call":
			// Defer server_tool_use until output_item.done carries action/query.
		}
	case "response.output_item.done":
		itemResult := rootResult.Get("item")
		itemType := itemResult.Get("type").String()
		switch itemType {
		case "message":
			output = appendCodexMessageFallback(output, params, rootResult)
		case "function_call":
			output = append(output, finalizeCodexThinkingBlock(params)...)
			output = append(output, stopCodexTextBlock(params)...)
			call := codexFunctionCallForEvent(params, rootResult, itemResult)
			if call == nil {
				call = recordCodexFunctionCall(params, rootResult, itemResult)
			}
			updateCodexFunctionCallIdentity(params, call, rootResult, itemResult)
			updateCodexFunctionCallArguments(call, itemResult.Get("arguments").String(), false)
			call.Done = true
			output = appendCodexFunctionCallQueue(output, params, originalRequestRawJSON)
		case "reasoning":
			output = append(output, stopCodexTextBlock(params)...)
			if signature := itemResult.Get("encrypted_content").String(); signature != "" {
				params.ThinkingSignature = signature
			}
			if params.ThinkingSummarySeen {
				output = append(output, finalizeCodexThinkingBlock(params)...)
			} else {
				output = append(output, finalizeCodexSignatureOnlyThinkingBlock(params)...)
			}
			params.ThinkingSignature = ""
			params.ThinkingSummarySeen = false
		case "web_search_call":
			// Codex has already executed the search. Anthropic search-result blocks
			// require opaque encrypted_content that only Anthropic can create, so
			// exposing a synthetic block would make this history impossible to resume
			// with a Claude model. The answer and URL annotations are emitted normally.
		}
	case "response.function_call_arguments.delta":
		call := codexFunctionCallForEvent(params, rootResult, gjson.Result{})
		if call == nil {
			call = recordCodexFunctionCall(params, rootResult, gjson.Result{})
		}
		updateCodexFunctionCallArguments(call, rootResult.Get("delta").String(), true)
		output = appendCodexFunctionCallBufferedArguments(output, params, call)
	case "response.function_call_arguments.done":
		call := codexFunctionCallForEvent(params, rootResult, gjson.Result{})
		if call == nil {
			call = recordCodexFunctionCall(params, rootResult, gjson.Result{})
		}
		updateCodexFunctionCallArguments(call, rootResult.Get("arguments").String(), false)
		output = appendCodexFunctionCallBufferedArguments(output, params, call)
	}

	if len(params.FunctionCallQueue) == 0 {
		return s.appendDeferredEvents(output)
	}
	return output, nil
}

func shouldDeferCodexStreamEvent(typeStr string, rootResult gjson.Result) bool {
	switch typeStr {
	case "response.completed", "response.incomplete", "response.function_call_arguments.delta", "response.function_call_arguments.done":
		return false
	case "response.output_item.added", "response.output_item.done":
		return rootResult.Get("item.type").String() != "function_call"
	default:
		return true
	}
}

func (s *Stream) appendDeferredEvents(output []byte) ([]byte, error) {
	params := &s.state
	if len(params.DeferredStreamEvents) == 0 {
		return output, nil
	}

	events := params.DeferredStreamEvents
	params.DeferredStreamEvents = nil
	for _, event := range events {
		translated, err := s.Convert(event)
		if err != nil {
			return nil, err
		}
		output = append(output, translated...)
	}
	return output, nil
}

// ConvertResponse converts a terminal Codex event to a Claude message. Incomplete
// tool calls and incomplete responses without usable content are rejected.
func ConvertResponse(modelName string, originalRequestRawJSON, rawJSON []byte) ([]byte, error) {
	revNames := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)

	rootResult := gjson.ParseBytes(rawJSON)
	typeStr := rootResult.Get("type").String()
	if typeStr != "response.completed" && typeStr != "response.incomplete" {
		return nil, errors.New("invalid terminal response from Codex")
	}

	responseData := rootResult.Get("response")
	if !responseData.IsObject() {
		return nil, errors.New("invalid terminal response from Codex")
	}
	if err := validateFunctionCallEvent(&streamState{}, rootResult); err != nil {
		return nil, err
	}

	out := []byte(`{"id":"","type":"message","role":"assistant","model":"","content":[],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":0,"output_tokens":0}}`)
	out, _ = sjson.SetBytes(out, "id", responseData.Get("id").String())
	responseModel := responseData.Get("model").String()
	if responseModel == "" {
		responseModel = modelName
	}
	out, _ = sjson.SetBytes(out, "model", responseModel)
	inputTokens, outputTokens, cachedTokens, cacheWriteTokens := extractResponsesUsage(responseData.Get("usage"))
	out, _ = sjson.SetBytes(out, "usage.input_tokens", inputTokens)
	out, _ = sjson.SetBytes(out, "usage.output_tokens", outputTokens)
	if cachedTokens > 0 {
		out, _ = sjson.SetBytes(out, "usage.cache_read_input_tokens", cachedTokens)
	}
	if cacheWriteTokens > 0 {
		out, _ = sjson.SetBytes(out, "usage.cache_creation_input_tokens", cacheWriteTokens)
	}
	out = setClaudeReasoningUsage(out, responseData.Get("usage"))

	hasToolCall := false
	hasRefusal := false
	var contentBlocks [][]byte

	if output := responseData.Get("output"); output.Exists() && output.IsArray() {
		output.ForEach(func(_, item gjson.Result) bool {
			switch item.Get("type").String() {
			case "reasoning":
				thinkingBuilder := strings.Builder{}
				signature := item.Get("encrypted_content").String()
				if summary := item.Get("summary"); summary.Exists() {
					if summary.IsArray() {
						summary.ForEach(func(_, part gjson.Result) bool {
							if txt := part.Get("text"); txt.Exists() {
								thinkingBuilder.WriteString(txt.String())
							} else {
								thinkingBuilder.WriteString(part.String())
							}
							return true
						})
					} else {
						thinkingBuilder.WriteString(summary.String())
					}
				}
				if thinkingBuilder.Len() == 0 {
					if content := item.Get("content"); content.Exists() {
						if content.IsArray() {
							content.ForEach(func(_, part gjson.Result) bool {
								if txt := part.Get("text"); txt.Exists() {
									thinkingBuilder.WriteString(txt.String())
								} else {
									thinkingBuilder.WriteString(part.String())
								}
								return true
							})
						} else {
							thinkingBuilder.WriteString(content.String())
						}
					}
				}
				if omitThinkingSummary(originalRequestRawJSON) {
					thinkingBuilder.Reset()
				}
				if thinkingBuilder.Len() > 0 || signature != "" {
					block := []byte(`{"type":"thinking","thinking":""}`)
					block, _ = sjson.SetBytes(block, "thinking", thinkingBuilder.String())
					if signature != "" {
						block, _ = sjson.SetBytes(block, "signature", signature)
					}
					contentBlocks = append(contentBlocks, block)
				}
			case "message":
				if content := item.Get("content"); content.Exists() {
					if content.IsArray() {
						content.ForEach(func(_, part gjson.Result) bool {
							var text string
							switch part.Get("type").String() {
							case "output_text":
								text = part.Get("text").String() + codexCitationLinks(part.Get("annotations"), make(map[string]struct{}))
							case "refusal":
								hasRefusal = true
								text = part.Get("refusal").String()
							}
							if text != "" {
								block := []byte(`{"type":"text","text":""}`)
								block, _ = sjson.SetBytes(block, "text", text)
								contentBlocks = append(contentBlocks, block)
							}
							return true
						})
					} else {
						text := content.String()
						if text != "" {
							block := []byte(`{"type":"text","text":""}`)
							block, _ = sjson.SetBytes(block, "text", text)
							contentBlocks = append(contentBlocks, block)
						}
					}
				}
			case "web_search_call":
				// Search is server-side Codex activity. Its answer and URL annotations
				// are carried by the adjacent message item.
			case "function_call":
				hasToolCall = true
				name := item.Get("name").String()
				if original, ok := revNames[name]; ok {
					name = original
				}

				toolBlock := []byte(`{"type":"tool_use","id":"","name":"","input":{}}`)
				toolBlock, _ = sjson.SetBytes(toolBlock, "id", shortenCodexCallIDIfNeeded(translatorcommon.SanitizeClaudeToolID(item.Get("call_id").String())))
				toolBlock, _ = sjson.SetBytes(toolBlock, "name", name)
				inputRaw := item.Get("arguments").String()
				toolBlock, _ = sjson.SetRawBytes(toolBlock, "input", []byte(inputRaw))
				contentBlocks = append(contentBlocks, toolBlock)
			}
			return true
		})
	}

	if len(contentBlocks) > 0 {
		out = translatorcommon.SetRawArrayItems(out, "content", contentBlocks)
	} else if typeStr == "response.incomplete" {
		return nil, errors.New("incomplete response without usable content from Codex")
	}

	out, _ = sjson.SetBytes(out, "stop_reason", mapCodexStopReasonToClaude(codexStopReason(responseData), hasToolCall, hasRefusal))
	out = setClaudeStopSequence(out, "stop_sequence", responseData)

	return out, nil
}

func codexStopReason(responseData gjson.Result) string {
	if stopReason := responseData.Get("stop_reason"); stopReason.Exists() && stopReason.String() != "" {
		if stopReason.String() == "stop" && codexStopSequence(responseData).String() != "" {
			return "stop_sequence"
		}
		return stopReason.String()
	}
	if reason := responseData.Get("incomplete_details.reason"); reason.Exists() && reason.String() != "" {
		return reason.String()
	}
	if codexStopSequence(responseData).String() != "" {
		return "stop_sequence"
	}
	return ""
}

func mapCodexStopReasonToClaude(stopReason string, hasToolCall, hasRefusal bool) string {
	if hasToolCall {
		return "tool_use"
	}

	switch stopReason {
	case "", "stop", "completed", "end_turn":
		if hasRefusal {
			return "refusal"
		}
		return "end_turn"
	case "max_tokens", "max_output_tokens":
		return "max_tokens"
	case "tool_use", "tool_calls", "function_call":
		return "end_turn"
	case "stop_sequence", "pause_turn", "refusal", "model_context_window_exceeded":
		return stopReason
	case "content_filter":
		return "refusal"
	default:
		return "end_turn"
	}
}

func codexStopSequence(responseData gjson.Result) gjson.Result {
	return responseData.Get("stop_sequence")
}

func setClaudeStopSequence(out []byte, path string, responseData gjson.Result) []byte {
	if stopSequence := codexStopSequence(responseData); stopSequence.Exists() && stopSequence.String() != "" {
		out, _ = sjson.SetRawBytes(out, path, []byte(stopSequence.Raw))
	}
	return out
}

func extractResponsesUsage(usage gjson.Result) (int64, int64, int64, int64) {
	if !usage.Exists() || usage.Type == gjson.Null {
		return 0, 0, 0, 0
	}

	inputTokens := usage.Get("input_tokens").Int()
	outputTokens := usage.Get("output_tokens").Int()
	cachedTokens := usage.Get("input_tokens_details.cached_tokens").Int()
	cacheWriteTokens := usage.Get("input_tokens_details.cache_write_tokens").Int()
	if cacheWriteTokens == 0 {
		cacheWriteTokens = usage.Get("input_tokens_details.cache_creation_tokens").Int()
	}

	if cachedTokens > 0 {
		if inputTokens >= cachedTokens {
			inputTokens -= cachedTokens
		} else {
			inputTokens = 0
		}
	}

	return inputTokens, outputTokens, cachedTokens, cacheWriteTokens
}

func setClaudeReasoningUsage(out []byte, usage gjson.Result) []byte {
	detail := usage.Get("output_tokens_details.reasoning_tokens")
	if !detail.Exists() || detail.Type != gjson.Number {
		return out
	}
	if strings.HasPrefix(detail.Raw, "-") || detail.Num < 0 {
		return out
	}
	outputTokens := max(int64(0), usage.Get("output_tokens").Int())
	var tokens int64
	if detail.Num >= float64(outputTokens) {
		tokens = outputTokens
	} else {
		tokens = detail.Int()
	}
	updated, errSetBytes := sjson.SetBytes(out, "usage.output_tokens_details.thinking_tokens", tokens)
	if errSetBytes != nil {
		return out
	}
	return updated
}

// buildReverseMapFromClaudeOriginalShortToOriginal builds a map[short]original from original Claude request tools.
func buildReverseMapFromClaudeOriginalShortToOriginal(original []byte) map[string]string {
	rev := map[string]string{}
	for orig, short := range buildReverseMapFromClaudeOriginalToShort(original) {
		rev[short] = orig
	}
	return rev
}

func startCodexTextBlock(params *streamState) []byte {
	if params.TextBlockOpen {
		return nil
	}

	template := []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
	params.TextBlockOpen = true

	return translatorcommon.AppendSSEEventBytes(nil, "content_block_start", template, 2)
}

func stopCodexTextBlock(params *streamState) []byte {
	if !params.TextBlockOpen {
		return nil
	}

	template := []byte(`{"type":"content_block_stop","index":0}`)
	template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
	params.TextBlockOpen = false
	params.BlockIndex++

	return translatorcommon.AppendSSEEventBytes(nil, "content_block_stop", template, 2)
}

func startCodexThinkingBlock(params *streamState) []byte {
	if params.ThinkingBlockOpen {
		return nil
	}

	template := []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`)
	template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
	params.ThinkingBlockOpen = true

	return translatorcommon.AppendSSEEventBytes(nil, "content_block_start", template, 2)
}

// appendCodexThinkingDelta emits a thinking_delta for the currently open thinking block.
func appendCodexThinkingDelta(params *streamState, text string) []byte {
	if text == "" {
		return nil
	}
	if text != codexThinkingSummaryPartSeparator {
		params.HasContent = true
	}

	template := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`)
	template, _ = sjson.SetBytes(template, "index", params.BlockIndex)
	template, _ = sjson.SetBytes(template, "delta.thinking", text)

	return translatorcommon.AppendSSEEventBytes(nil, "content_block_delta", template, 2)
}

func finalizeCodexSignatureOnlyThinkingBlock(params *streamState) []byte {
	if params.ThinkingSignature == "" {
		return nil
	}

	output := startCodexThinkingBlock(params)
	output = append(output, finalizeCodexThinkingBlock(params)...)
	return output
}

func finalizeCodexThinkingBlock(params *streamState) []byte {
	if !params.ThinkingBlockOpen {
		return nil
	}

	output := make([]byte, 0, 256)
	if params.ThinkingSignature != "" {
		params.HasContent = true
		if params.EmittedThinkingSignatures == nil {
			params.EmittedThinkingSignatures = make(map[string]struct{})
		}
		params.EmittedThinkingSignatures[params.ThinkingSignature] = struct{}{}
		signatureDelta := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":""}}`)
		signatureDelta, _ = sjson.SetBytes(signatureDelta, "index", params.BlockIndex)
		signatureDelta, _ = sjson.SetBytes(signatureDelta, "delta.signature", params.ThinkingSignature)
		output = translatorcommon.AppendSSEEventBytes(output, "content_block_delta", signatureDelta, 2)
	}

	contentBlockStop := []byte(`{"type":"content_block_stop","index":0}`)
	contentBlockStop, _ = sjson.SetBytes(contentBlockStop, "index", params.BlockIndex)
	output = translatorcommon.AppendSSEEventBytes(output, "content_block_stop", contentBlockStop, 2)

	params.BlockIndex++
	params.ThinkingBlockOpen = false

	return output
}

// Hidden summaries still carry encrypted_content for the next request.
func omitThinkingSummary(original []byte) bool {
	return gjson.GetBytes(original, "thinking.display").String() == "omitted" || gjson.GetBytes(original, "thinking.type").String() == "disabled"
}
