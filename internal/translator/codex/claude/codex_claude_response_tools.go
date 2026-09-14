package claude

import (
	"strconv"
	"strings"

	translatorcommon "github.com/marenzo/claudex/internal/translator/common"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type codexFunctionCallStream struct {
	CallID                    string
	Name                      string
	BlockIndex                int
	Arguments                 string
	EmittedArgumentsLength    int
	HasReceivedArgumentsDelta bool
	EmitInitialEmptyDelta     bool
	Started                   bool
	Done                      bool
	Closed                    bool
}

func codexFunctionCallID(itemResult gjson.Result) string {
	return itemResult.Get("call_id").String()
}

func codexFunctionCallKeys(rootResult, itemResult gjson.Result) []string {
	keys := make([]string, 0, 5)
	if outputIndex := rootResult.Get("output_index"); outputIndex.Exists() {
		keys = appendUniqueCodexFunctionCallKey(keys, "output:"+outputIndex.Raw)
	}
	if callID := codexFunctionCallID(itemResult); callID != "" {
		keys = appendUniqueCodexFunctionCallKey(keys, "call:"+callID)
	}
	if callID := rootResult.Get("call_id").String(); callID != "" {
		keys = appendUniqueCodexFunctionCallKey(keys, "call:"+callID)
	}
	if itemID := itemResult.Get("id").String(); itemID != "" {
		keys = appendUniqueCodexFunctionCallKey(keys, "item:"+itemID)
	}
	if itemID := rootResult.Get("item_id").String(); itemID != "" {
		keys = appendUniqueCodexFunctionCallKey(keys, "item:"+itemID)
	}
	return keys
}

func appendUniqueCodexFunctionCallKey(keys []string, key string) []string {
	if key == "" {
		return keys
	}
	for _, existing := range keys {
		if existing == key {
			return keys
		}
	}
	return append(keys, key)
}

func codexFunctionCallForKeys(params *streamState, keys []string) *codexFunctionCallStream {
	if params == nil || params.FunctionCalls == nil {
		return nil
	}
	for _, key := range keys {
		if call := params.FunctionCalls[key]; call != nil {
			return call
		}
	}
	return nil
}

func codexFunctionCallForEvent(params *streamState, rootResult, itemResult gjson.Result) *codexFunctionCallStream {
	keys := codexFunctionCallKeys(rootResult, itemResult)
	if len(keys) > 0 {
		return codexFunctionCallForKeys(params, keys)
	}
	if params == nil {
		return nil
	}
	return params.LastFunctionCall
}

func recordCodexFunctionCall(params *streamState, rootResult, itemResult gjson.Result) *codexFunctionCallStream {
	keys := codexFunctionCallKeys(rootResult, itemResult)
	call := codexFunctionCallForKeys(params, keys)
	if call == nil {
		call = &codexFunctionCallStream{BlockIndex: -1}
		params.FunctionCallQueue = append(params.FunctionCallQueue, call)
	}
	addCodexFunctionCallAliases(params, call, keys)
	params.LastFunctionCall = call
	return call
}

func addCodexFunctionCallAliases(params *streamState, call *codexFunctionCallStream, keys []string) {
	if params == nil || call == nil {
		return
	}
	if params.FunctionCalls == nil {
		params.FunctionCalls = map[string]*codexFunctionCallStream{}
	}
	for _, key := range keys {
		params.FunctionCalls[key] = call
	}
}

func updateCodexFunctionCallIdentity(params *streamState, call *codexFunctionCallStream, rootResult, itemResult gjson.Result) {
	if call == nil {
		return
	}
	if callID := codexFunctionCallID(itemResult); callID != "" {
		call.CallID = callID
	}
	if name := itemResult.Get("name").String(); name != "" {
		call.Name = name
	}
	addCodexFunctionCallAliases(params, call, codexFunctionCallKeys(rootResult, itemResult))
}

func updateCodexFunctionCallArguments(call *codexFunctionCallStream, arguments string, delta bool) {
	if call == nil || arguments == "" {
		return
	}
	if delta {
		call.Arguments += arguments
		call.HasReceivedArgumentsDelta = true
		return
	}
	if !call.HasReceivedArgumentsDelta {
		call.Arguments = arguments
		return
	}
	if strings.HasPrefix(arguments, call.Arguments) {
		call.Arguments = arguments
	}
}

func appendCodexFunctionCallStart(output []byte, originalRequestRawJSON []byte, callID, name string, blockIndex int) []byte {
	template := []byte(`{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"","name":"","input":{}}}`)
	template, _ = sjson.SetBytes(template, "index", blockIndex)
	template, _ = sjson.SetBytes(template, "content_block.id", shortenCodexCallIDIfNeeded(translatorcommon.SanitizeClaudeToolID(callID)))
	template, _ = sjson.SetBytes(template, "content_block.name", resolveCodexClaudeToolUseName(originalRequestRawJSON, name))
	return translatorcommon.AppendSSEEventBytes(output, "content_block_start", template, 2)
}

func appendCodexFunctionCallArgumentDelta(output []byte, partialJSON string, blockIndex int) []byte {
	template := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":""}}`)
	template, _ = sjson.SetBytes(template, "index", blockIndex)
	template, _ = sjson.SetBytes(template, "delta.partial_json", partialJSON)
	return translatorcommon.AppendSSEEventBytes(output, "content_block_delta", template, 2)
}

func appendCodexFunctionCallStop(output []byte, blockIndex int) []byte {
	template := []byte(`{"type":"content_block_stop","index":0}`)
	template, _ = sjson.SetBytes(template, "index", blockIndex)
	return translatorcommon.AppendSSEEventBytes(output, "content_block_stop", template, 2)
}

func appendCodexFunctionCallBufferedArguments(output []byte, params *streamState, call *codexFunctionCallStream) []byte {
	if params == nil || call == nil || params.ActiveFunctionCall != call || !call.Started || call.Closed {
		return output
	}
	if call.EmittedArgumentsLength >= len(call.Arguments) {
		return output
	}

	output = appendCodexFunctionCallArgumentDelta(output, call.Arguments[call.EmittedArgumentsLength:], call.BlockIndex)
	call.EmittedArgumentsLength = len(call.Arguments)
	return output
}

func appendCodexFunctionCallQueue(output []byte, params *streamState, originalRequestRawJSON []byte) []byte {
	if params == nil {
		return output
	}

	for {
		if active := params.ActiveFunctionCall; active != nil {
			output = appendCodexFunctionCallBufferedArguments(output, params, active)
			if !active.Done {
				return output
			}
			output = appendCodexFunctionCallStop(output, active.BlockIndex)
			if params.BlockIndex <= active.BlockIndex {
				params.BlockIndex = active.BlockIndex + 1
			}
			active.Closed = true
			params.ActiveFunctionCall = nil
			removeCodexFunctionCallFromQueue(params, active)
		}

		for len(params.FunctionCallQueue) > 0 && params.FunctionCallQueue[0].Closed {
			params.FunctionCallQueue = params.FunctionCallQueue[1:]
		}
		if len(params.FunctionCallQueue) == 0 {
			return output
		}

		call := params.FunctionCallQueue[0]
		if call.Name == "" {
			return output
		}

		call.BlockIndex = params.BlockIndex
		output = appendCodexFunctionCallStart(output, originalRequestRawJSON, call.CallID, call.Name, call.BlockIndex)
		if call.EmitInitialEmptyDelta {
			output = appendCodexFunctionCallArgumentDelta(output, "", call.BlockIndex)
		}
		call.Started = true
		params.ActiveFunctionCall = call
		params.HasEmittedToolUse = true
		params.HasContent = true
		output = appendCodexFunctionCallBufferedArguments(output, params, call)
	}
}

func removeCodexFunctionCallFromQueue(params *streamState, call *codexFunctionCallStream) {
	if params == nil || call == nil {
		return
	}
	for index, queued := range params.FunctionCallQueue {
		if queued != call {
			continue
		}
		params.FunctionCallQueue = append(params.FunctionCallQueue[:index], params.FunctionCallQueue[index+1:]...)
		return
	}
}

func appendCodexFunctionCallsFromTerminal(output []byte, params *streamState, originalRequestRawJSON []byte, responseData gjson.Result) []byte {
	if params == nil {
		return output
	}

	responseData.Get("output").ForEach(func(index, item gjson.Result) bool {
		if item.Get("type").String() != "function_call" {
			return true
		}

		keys := terminalFunctionCallKeys(int(index.Int()), item)
		call := codexFunctionCallForKeys(params, keys)
		if call == nil {
			call = &codexFunctionCallStream{BlockIndex: -1}
			params.FunctionCallQueue = append(params.FunctionCallQueue, call)
		}
		addCodexFunctionCallAliases(params, call, keys)
		updateCodexFunctionCallIdentity(params, call, gjson.Result{}, item)
		updateCodexFunctionCallArguments(call, item.Get("arguments").String(), false)
		call.Done = true
		return true
	})

	queuedCalls := params.FunctionCallQueue[:0]
	for _, call := range params.FunctionCallQueue {
		if call.Closed {
			continue
		}
		call.Done = true
		queuedCalls = append(queuedCalls, call)
	}
	params.FunctionCallQueue = queuedCalls
	output = appendCodexFunctionCallQueue(output, params, originalRequestRawJSON)

	clearCodexFunctionCalls(params)
	return output
}

func terminalFunctionCallKeys(index int, item gjson.Result) []string {
	keys := codexFunctionCallKeys(gjson.Result{}, item)
	if itemOutputIndex := item.Get("output_index"); itemOutputIndex.Exists() {
		keys = appendUniqueCodexFunctionCallKey(keys, "output:"+itemOutputIndex.Raw)
	}
	return appendUniqueCodexFunctionCallKey(keys, "output:"+strconv.Itoa(index))
}

func clearCodexFunctionCalls(params *streamState) {
	if params == nil {
		return
	}
	clear(params.FunctionCalls)
	params.FunctionCallQueue = nil
	params.ActiveFunctionCall = nil
	params.LastFunctionCall = nil
}

func resolveCodexClaudeToolUseName(originalRequestRawJSON []byte, name string) string {
	rev := buildReverseMapFromClaudeOriginalShortToOriginal(originalRequestRawJSON)
	if orig, ok := rev[name]; ok {
		return orig
	}
	return name
}
