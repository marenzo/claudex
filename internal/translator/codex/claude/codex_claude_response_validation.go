package claude

import (
	"errors"
	"strings"

	translatorcommon "github.com/marenzo/claudex/internal/translator/common"
	"github.com/tidwall/gjson"
)

// validateFunctionCallEvent uses the same calls and aliases as conversion, so a
// terminal snapshot can finish a partial call but cannot contradict bytes already
// sent to Claude. Errors deliberately exclude arguments and tool identities.
func validateFunctionCallEvent(params *streamState, event gjson.Result) error {
	kind := event.Get("type").String()
	if kind == "response.output_item.added" || kind == "response.output_item.done" || kind == "response.function_call_arguments.delta" || kind == "response.function_call_arguments.done" {
		item := event.Get("item")
		if item.Get("type").String() == "function_call" || strings.HasPrefix(kind, "response.function_call_arguments.") {
			if err := validateFunctionCallIdentity(params, codexFunctionCallKeys(event, item), item, event.Get("call_id").String()); err != nil {
				return err
			}
		}
	}
	switch kind {
	case "response.function_call_arguments.done":
		call := codexFunctionCallForEvent(params, event, gjson.Result{})
		return validateFunctionCallArguments(call, event.Get("arguments"))
	case "response.output_item.done":
		item := event.Get("item")
		if item.Get("type").String() == "function_call" {
			call := codexFunctionCallForEvent(params, event, item)
			return validateFunctionCallArguments(call, item.Get("arguments"))
		}
	case "response.completed", "response.incomplete":
		completed := make(map[*codexFunctionCallStream]struct{})
		for index, item := range event.Get("response.output").Array() {
			if item.Get("type").String() != "function_call" {
				continue
			}
			keys := terminalFunctionCallKeys(index, item)
			if err := validateFunctionCallIdentity(params, keys, item, ""); err != nil {
				return err
			}
			call := codexFunctionCallForKeys(params, keys)
			if item.Get("name").String() == "" && (call == nil || call.Name == "") {
				return errors.New("incomplete tool call from Codex")
			}
			if err := validateFunctionCallArguments(call, item.Get("arguments")); err != nil {
				return err
			}
			completed[call] = struct{}{}
		}
		for _, call := range params.FunctionCallQueue {
			if _, ok := completed[call]; ok || call.Closed {
				continue
			}
			if call.Name == "" {
				return errors.New("incomplete tool call from Codex")
			}
			if err := validateFunctionCallArguments(call, gjson.Result{}); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateFunctionCallArguments(call *codexFunctionCallStream, snapshot gjson.Result) error {
	arguments := snapshot.String()
	if snapshot.Exists() && snapshot.Type != gjson.String {
		return errors.New("invalid tool arguments from Codex")
	}
	if call != nil {
		if arguments == "" {
			arguments = call.Arguments
		} else if (call.HasReceivedArgumentsDelta && !strings.HasPrefix(arguments, call.Arguments)) ||
			(call.EmittedArgumentsLength > 0 && !strings.HasPrefix(arguments, call.Arguments[:call.EmittedArgumentsLength])) ||
			(call.Closed && arguments != call.Arguments) {
			return errors.New("inconsistent tool arguments from Codex")
		}
	}
	if translatorcommon.ValidateUnambiguousJSON([]byte(arguments)) != nil || !gjson.Parse(arguments).IsObject() {
		return errors.New("incomplete or invalid tool arguments from Codex")
	}
	return nil
}

func validateFunctionCallIdentity(params *streamState, keys []string, item gjson.Result, eventCallID string) error {
	call := codexFunctionCallForKeys(params, keys)
	if call == nil {
		return nil
	}
	for _, key := range keys {
		if other := params.FunctionCalls[key]; other != nil && other != call {
			return errors.New("conflicting tool call identity from Codex")
		}
	}
	if name := item.Get("name").String(); name != "" && call.Name != "" && name != call.Name {
		return errors.New("inconsistent tool name from Codex")
	}
	for _, id := range []string{item.Get("call_id").String(), eventCallID} {
		if id != "" && call.CallID != "" && id != call.CallID {
			return errors.New("inconsistent tool ID from Codex")
		}
	}
	return nil
}
