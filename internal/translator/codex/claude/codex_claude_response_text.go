package claude

import (
	"strings"

	translatorcommon "github.com/marenzo/claudex/internal/translator/common"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type codexContentPartKey struct {
	OutputIndex  int64
	ContentIndex int64
}

func codexContentOutputIndex(params *streamState, event gjson.Result) int64 {
	outputIndex := event.Get("output_index")
	index := outputIndex.Int()
	itemID := event.Get("item_id").String()
	if itemID == "" {
		itemID = event.Get("item.id").String()
	}
	if itemID != "" {
		if previous, ok := params.ContentOutputIndices[itemID]; ok && !outputIndex.Exists() {
			return previous
		}
		if params.ContentOutputIndices == nil {
			params.ContentOutputIndices = make(map[string]int64)
		}
		params.ContentOutputIndices[itemID] = index
	}
	return index
}

// Output text is repeated in several done events and the terminal response.
// Track each content part separately and emit only the text not already sent.
func appendCodexContentText(output []byte, params *streamState, outputIndex, contentIndex int64, text string, isDelta bool) []byte {
	if params.ContentText == nil {
		params.ContentText = make(map[codexContentPartKey]*strings.Builder)
	}
	key := codexContentPartKey{OutputIndex: outputIndex, ContentIndex: contentIndex}
	content := params.ContentText[key]
	if content == nil {
		content = &strings.Builder{}
		params.ContentText[key] = content
	}
	if !isDelta {
		previous := content.String()
		if !strings.HasPrefix(text, previous) {
			return output
		}
		text = text[len(previous):]
	}
	content.WriteString(text)
	return appendCodexText(output, params, text)
}

func appendCodexText(output []byte, params *streamState, text string) []byte {
	if text == "" {
		return output
	}
	params.HasContent = true
	output = append(output, finalizeCodexThinkingBlock(params)...)
	output = append(output, startCodexTextBlock(params)...)
	delta := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
	delta, _ = sjson.SetBytes(delta, "index", params.BlockIndex)
	delta, _ = sjson.SetBytes(delta, "delta.text", text)
	return translatorcommon.AppendSSEEventBytes(output, "content_block_delta", delta, 2)
}

func appendCodexMessageFallback(output []byte, params *streamState, event gjson.Result) []byte {
	output = appendCodexMessageText(output, params, codexContentOutputIndex(params, event), event.Get("item"))
	return append(output, stopCodexTextBlock(params)...)
}

func appendCodexMessageText(output []byte, params *streamState, outputIndex int64, item gjson.Result) []byte {
	for contentIndex, part := range item.Get("content").Array() {
		switch part.Get("type").String() {
		case "output_text":
			output = appendCodexContentText(output, params, outputIndex, int64(contentIndex), part.Get("text").String(), false)
		case "refusal":
			params.HasRefusal = true
			output = appendCodexContentText(output, params, outputIndex, int64(contentIndex), part.Get("refusal").String(), false)
		}
	}
	return output
}

func appendCodexTerminalContent(output []byte, params *streamState, response gjson.Result) []byte {
	for outputIndex, item := range response.Get("output").Array() {
		switch item.Get("type").String() {
		case "reasoning":
			output = appendCodexTerminalReasoning(output, params, item)
			continue
		case "web_search_call":
			// Search results cannot be represented as Anthropic server-tool blocks:
			// replay requires Anthropic-issued encrypted_content. Keep the final text
			// and URL annotations, which are emitted by the message item.
			continue
		case "message":
		default:
			continue
		}
		// Output hydration can compact sparse indices. Match stable item IDs to
		// the original streamed index so repeated terminal text is not duplicated.
		index := int64(outputIndex)
		if streamedIndex, ok := params.ContentOutputIndices[item.Get("id").String()]; ok {
			index = streamedIndex
		}
		output = appendCodexMessageText(output, params, index, item)
	}
	return output
}

// A terminal snapshot can carry reasoning that never had output_item.done.
// Preserve its encrypted state once, including when summaries are hidden.
func appendCodexTerminalReasoning(output []byte, params *streamState, item gjson.Result) []byte {
	signature := item.Get("encrypted_content").String()
	if signature == "" {
		return output
	}
	if _, emitted := params.EmittedThinkingSignatures[signature]; emitted {
		return output
	}
	output = append(output, stopCodexTextBlock(params)...)
	output = append(output, finalizeCodexThinkingBlock(params)...)
	params.ThinkingSignature = signature
	output = append(output, startCodexThinkingBlock(params)...)
	if !params.OmitThinkingSummary {
		for index, part := range item.Get("summary").Array() {
			if index > 0 {
				output = append(output, appendCodexThinkingDelta(params, codexThinkingSummaryPartSeparator)...)
			}
			output = append(output, appendCodexThinkingDelta(params, part.Get("text").String())...)
		}
	}
	output = append(output, finalizeCodexThinkingBlock(params)...)
	params.ThinkingSignature = ""
	return output
}
