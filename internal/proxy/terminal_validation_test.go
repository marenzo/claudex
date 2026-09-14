package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestMalformedAndEmptyIncompleteTerminalsFail(t *testing.T) {
	for _, terminal := range []string{
		`{"type":"response.completed"}`,
		`{"type":"response.completed","response":null}`,
		`{"type":"response.completed","response":{"output":{}}}`,
		`{"type":"response.incomplete","response":{"output":[],"incomplete_details":{"reason":"server_error"}}}`,
		`{"type":"response.incomplete","response":{"output":[],"usage":{"output_tokens":5}}}`,
		`{"type":"response.incomplete","response":{"output":[],"incomplete_details":{"reason":"max_output_tokens"},"usage":{"output_tokens":0.0}}}`,
		`{"type":"response.incomplete","response":{"output":[{"type":"message","content":[]}],"incomplete_details":{"reason":"max_output_tokens"}}}`,
		`{"type":"response.incomplete","response":{"output":[{"type":"reasoning"}],"incomplete_details":{"reason":"max_output_tokens"}}}`,
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", terminal, stream), func(t *testing.T) {
				s := fixture(t, func(*http.Request) (*http.Response, error) {
					return upstreamResponse(200, sse(created, terminal)), nil
				})
				w := request(s, "/v1/messages", fmt.Sprintf(`{"messages":[],"stream":%t}`, stream))
				jsonError := w.Code == 502 && gjson.Get(w.Body.String(), "error.type").String() == "api_error"
				streamError := stream && w.Code == 200 && strings.Contains(w.Body.String(), "event: error")
				if (!jsonError && !streamError) || strings.Contains(w.Body.String(), "event: message_stop") {
					t.Fatalf("terminal accepted as success: %d %s", w.Code, w.Body)
				}
			})
		}
	}
}

func TestIncompletePartialOutputPreservesHonestStopReason(t *testing.T) {
	for _, tc := range []struct{ part, textField, reason, stop string }{
		{"output_text", "text", "max_output_tokens", "max_tokens"},
		{"refusal", "refusal", "content_filter", "refusal"},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.part, stream), func(t *testing.T) {
				item := fmt.Sprintf(`{"type":"message","id":"m1","content":[{"type":%q,%q:"partial"}]}`, tc.part, tc.textField)
				terminal := fmt.Sprintf(`{"type":"response.incomplete","response":{"id":"r1","output":[%s],"incomplete_details":{"reason":%q},"usage":{"output_tokens":1}}}`, item, tc.reason)
				s := fixture(t, func(*http.Request) (*http.Response, error) {
					return upstreamResponse(200, sse(created, terminal)), nil
				})
				w := request(s, "/v1/messages", fmt.Sprintf(`{"messages":[],"stream":%t}`, stream))
				if w.Code != 200 || !strings.Contains(w.Body.String(), `"stop_reason":"`+tc.stop+`"`) || !strings.Contains(w.Body.String(), "partial") {
					t.Fatalf("partial output lost: %d %s", w.Code, w.Body)
				}
			})
		}
	}
}

func TestIncompleteAfterDeltaCannotBecomeSuccessfulEmptyTurn(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				return upstreamResponse(200, sse(created,
					`{"type":"response.output_text.delta","delta":"partial"}`,
					`{"type":"response.incomplete","response":{"output":[],"incomplete_details":{"reason":"server_error"}}}`)), nil
			})
			w := request(s, "/v1/messages", fmt.Sprintf(`{"messages":[],"stream":%t}`, stream))
			if stream {
				if !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") {
					t.Fatalf("incomplete stream became success: %s", w.Body)
				}
			} else if w.Code != 502 {
				t.Fatalf("non-stream response lost partial output silently: %d %s", w.Code, w.Body)
			}
		})
	}
}

func TestTruncatedStreamedToolCannotComplete(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		return upstreamResponse(200, sse(created,
			`{"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc1","call_id":"call1","name":"Read","arguments":""}}`,
			`{"type":"response.function_call_arguments.delta","item_id":"fc1","output_index":0,"delta":"{\"file_path\":"}`,
			`{"type":"response.incomplete","response":{"output":[],"incomplete_details":{"reason":"max_output_tokens"},"usage":{"output_tokens":5}}}`,
		)), nil
	})
	w := request(s, "/v1/messages", `{"messages":[],"stream":true}`)
	if !strings.Contains(w.Body.String(), "event: error") || strings.Contains(w.Body.String(), "event: message_stop") || strings.Contains(w.Body.String(), "event: content_block_stop") {
		t.Fatalf("truncated tool completed: %s", w.Body)
	}
}

func TestIncompletePreservesNonTextContent(t *testing.T) {
	for _, tc := range []struct{ item, want string }{
		{`{"type":"reasoning","encrypted_content":"encrypted-state"}`, `"signature":"encrypted-state"`},
		{`{"type":"function_call","name":"Read","call_id":"call1","arguments":"{}"}`, `"type":"tool_use"`},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.item, stream), func(t *testing.T) {
				terminal := fmt.Sprintf(`{"type":"response.incomplete","response":{"id":"r1","output":[%s],"incomplete_details":{"reason":"max_output_tokens"}}}`, tc.item)
				s := fixture(t, func(*http.Request) (*http.Response, error) {
					return upstreamResponse(200, sse(created, terminal)), nil
				})
				w := request(s, "/v1/messages", fmt.Sprintf(`{"messages":[],"stream":%t}`, stream))
				if w.Code != 200 || !strings.Contains(w.Body.String(), tc.want) || strings.Contains(w.Body.String(), "event: error") {
					t.Fatalf("valid incomplete content rejected: %d %s", w.Code, w.Body)
				}
			})
		}
	}
}

func TestIncompletePreservesDeltaOnlyRefusal(t *testing.T) {
	s := fixture(t, func(*http.Request) (*http.Response, error) {
		return upstreamResponse(200, sse(created,
			`{"type":"response.refusal.delta","delta":"Cannot comply"}`,
			`{"type":"response.incomplete","response":{"output":[],"incomplete_details":{"reason":"content_filter"},"usage":{"output_tokens":0}}}`)), nil
	})
	w := request(s, "/v1/messages", `{"messages":[],"stream":true}`)
	if !strings.Contains(w.Body.String(), `"stop_reason":"refusal"`) || !strings.Contains(w.Body.String(), "event: message_stop") || strings.Contains(w.Body.String(), "event: error") {
		t.Fatalf("valid refusal rejected: %s", w.Body)
	}
}
