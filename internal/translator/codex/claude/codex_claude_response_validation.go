package claude

import (
	"errors"
	"strings"

	"github.com/tidwall/gjson"
)

// validateFunctionCallEvent uses the same calls and aliases as conversion, so a
// terminal snapshot can finish a partial call but cannot contradict bytes already
// sent to Claude. Errors deliberately exclude arguments and tool identities.
func validateFunctionCallEvent(params *streamState, event gjson.Result) error {
	switch event.Get("type").String() {
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
			call := codexFunctionCallForKeys(params, terminalFunctionCallKeys(index, item))
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
	if !gjson.Valid(arguments) || !gjson.Parse(arguments).IsObject() {
		return errors.New("incomplete or invalid tool arguments from Codex")
	}
	return nil
}
