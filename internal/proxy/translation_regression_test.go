package proxy

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/marenzo/claudex/internal/config"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestTerminalStatusAndToolConflicts(t *testing.T) {
	safe := `{"type":"function_call","id":"fc_conflict","call_id":"toolu_one","name":"Read","arguments":"{\"file_path\":\"/fixture/safe\"}"}`
	unsafe := strings.ReplaceAll(safe, "/fixture/safe", "/fixture/other")
	for name, events := range map[string]string{
		"terminal_status":         sse(created, `{"type":"response.completed","response":{"status":"failed","output":[`+messageItem+`]}}`),
		"terminal_arguments":      sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+safe+`}`, `{"type":"response.completed","response":{"status":"completed","output":[`+unsafe+`]}}`),
		"terminal_identity":       sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+safe+`}`, `{"type":"response.completed","response":{"status":"completed","output":[`+strings.Replace(safe, `"name":"Read"`, `"name":"Bash"`, 1)+`]}}`),
		"changed_event_identity":  sse(created, `{"type":"response.output_item.added","output_index":0,"item":`+safe+`}`, `{"type":"response.output_item.done","output_index":0,"item":`+strings.Replace(safe, `"name":"Read"`, `"name":"Bash"`, 1)+`}`, completed),
		"repeated_done_arguments": sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+safe+`}`, `{"type":"response.output_item.done","output_index":0,"item":`+unsafe+`}`, completed),
		"duplicate_terminal_id":   sse(created, `{"type":"response.completed","response":{"status":"completed","output":[`+safe+`,`+unsafe+`]}}`),
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", name, stream), func(t *testing.T) {
				s := fixture(t, func(*http.Request) (*http.Response, error) { return upstreamResponse(200, events), nil })
				raw := `{"messages":[]}`
				if stream {
					s.Config.AutoModeClassifierModel = "sol"
					raw = reviewRequest(true)
				}
				w := request(s, "/v1/messages", raw)
				if w.Code != 502 || strings.Contains(w.Body.String(), "content_block_start") {
					t.Fatalf("conflicting response accepted: %d %s", w.Code, w.Body)
				}
			})
		}
	}

}

func TestClassifierExcludesOnlyOpaqueReplayState(t *testing.T) {
	raw := []byte(reviewRequest(false))
	for _, kind := range []string{"thinking", "redacted_thinking"} {
		field := "signature"
		if kind == "redacted_thinking" {
			field = "data"
		}
		message := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": kind, field: strings.Repeat("x", 2*classifierInputLimit), "thinking": "visible fixture summary"}}}
		input, _ := sjson.SetBytes(raw, "messages.-1", message)
		review, err := newSafeguardReview(input, config.Config{AutoModeClassifierModel: "sol"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(review.payload["messages"]), "visible fixture summary") || strings.Contains(string(review.payload["messages"]), strings.Repeat("x", 200)) {
			t.Fatal("visible/opaque distinction lost")
		}
		if !strings.Contains(string(input), strings.Repeat("x", 200)) {
			t.Fatal("mutated original generation input")
		}
	}
	input, _ := sjson.SetBytes(raw, "messages.0.content", strings.Repeat("x", 2*classifierInputLimit))
	if _, err := newSafeguardReview(input, config.Config{AutoModeClassifierModel: "sol"}); err == nil {
		t.Fatal("dropped oversized human context")
	}
}

func TestMalformedChoicesFailBeforeInference(t *testing.T) {
	for _, choice := range []string{`{"type":"banana"}`, `{"type":"tool"}`, `{"type":"tool","name":"Missing"}`, `{"type":"auto","disable_parallel_tool_use":"true"}`} {
		raw := []byte(`{"messages":[],"tools":[{"name":"Read","input_schema":{"type":"object"}}],"tool_choice":` + choice + `}`)
		if _, _, err := prepareRequest(raw, config.Config{Model: "sol", ReasoningEffort: "high"}); err == nil {
			t.Errorf("broadened malformed tool choice %s", choice)
		}
	}
	raw := []byte(`{"messages":[],"tools":[{"name":"web_search","type":"web_search_20990101"}]}`)
	if _, _, err := prepareRequest(raw, config.Config{Model: "sol", ReasoningEffort: "high"}); err == nil {
		t.Error("accepted unsupported native search version")
	}
}

func TestRequestRejectsDroppedConstraints(t *testing.T) {
	for _, raw := range []string{
		`{"messages":[],"messages":[{"role":"user","content":"different"}]}`,
		`{"messages":[],"tools":[{"name":"WebSearch","max_uses":1}]}`,
		`{"messages":[],"tools":[{"name":"web_search","type":"web_search_20250305","blocked_domains":"example.com"}]}`,
		`{"messages":[{"role":"user","content":[{"type":"document","source":{"type":"url","url":"https://example.com/fixture.pdf"}}]}]}`,
	} {
		if _, _, err := prepareRequest([]byte(raw), config.Config{Model: "sol", ReasoningEffort: "high"}); err == nil {
			t.Errorf("silently accepted unsupported constraint: %s", raw)
		}
	}
}

func TestSearchBlocksPreserved(t *testing.T) {
	for _, name := range []string{"WebSearch", "web_search"} {
		kind := `"type":"web_search_20250305",`
		if name == "WebSearch" {
			kind = ""
		}
		raw := []byte(fmt.Sprintf(`{"messages":[],"tools":[{%s"name":%q,"blocked_domains":["blocked.example"],"input_schema":{"type":"object"}}]}`, kind, name))
		body, _, err := prepareRequest(raw, config.Config{Model: "sol", ReasoningEffort: "high"})
		if err != nil {
			t.Fatal(err)
		}
		if gjson.GetBytes(body, "tools.0.filters.blocked_domains.0").String() != "blocked.example" {
			t.Fatalf("search restriction lost: %s", body)
		}
	}
}

func TestSparseTerminalOrder(t *testing.T) {
	item0 := `{"id":"msg_first","type":"message","content":[{"type":"output_text","text":"before"}]}`
	item1 := `{"id":"fc_second","type":"function_call","name":"Read","call_id":"toolu_two","arguments":"{}"}`
	data := []byte(`{"type":"response.completed","response":{"output":[` + item0 + `,` + item1 + `]}}`)
	got := hydrateOutput(data, map[int64][]byte{1: []byte(item1)}, nil)
	if gjson.GetBytes(got, "response.output.0.id").String() != "msg_first" {
		t.Fatalf("terminal order changed: %s", got)
	}
}

func TestReviewerRejectsUnexpectedActions(t *testing.T) {
	verdict := `{"verdicts":[{"id":"toolu_test","outcome":"not_flagged","explanation":"fixture"}]}`
	for name, extra := range map[string]string{
		"added_action": sse(`{"type":"response.output_item.added","item":{"type":"function_call","name":"Bash","arguments":"{}"}}`),
		"done_action":  sse(`{"type":"response.output_item.done","item":{"type":"web_search_call"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			calls := 0
			s := fixture(t, func(*http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return upstreamResponse(200, toolResponse()), nil
				}
				return upstreamResponse(200, extra+classifierResponse(verdict)), nil
			})
			s.Config.AutoModeClassifierModel = "sol"
			w := request(s, "/v1/messages", reviewRequest(true))
			if w.Code != 502 || strings.Contains(w.Body.String(), "content_block_start") {
				t.Fatalf("reviewer action accepted: %d %s", w.Code, w.Body)
			}
		})
	}
}
