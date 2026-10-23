package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

func TestCitationFormattingThroughGateway(t *testing.T) {
	first, second := "\ue200cite\ue202turn0view0\ue201", "\ue200cite\ue202turn0view1\ue202turn0view2\ue201"
	raw := "First release fact. " + first + "\n\nSecond release fact. " + second
	var annotations []map[string]any
	for i, marker := range []string{first, second, second} {
		start := strings.Index(raw, marker)
		annotations = append(annotations, map[string]any{"type": "url_citation", "start_index": utf8.RuneCountInString(raw[:start]), "end_index": utf8.RuneCountInString(raw[:start+len(marker)]), "title": fmt.Sprintf("Source %d", i+1), "url": fmt.Sprintf("https://example.org/release-%d", i+1)})
	}
	part := map[string]any{"type": "output_text", "text": raw, "annotations": annotations}
	item := map[string]any{"type": "message", "id": "message", "role": "assistant", "content": []any{part}}
	encode := func(value any) string { data, _ := json.Marshal(value); return string(data) }
	events := []string{created, encode(map[string]any{"type": "response.output_text.delta", "item_id": "message", "output_index": 0, "content_index": 0, "delta": raw})}
	for _, annotation := range annotations {
		events = append(events, encode(map[string]any{"type": "response.output_text.annotation.added", "item_id": "message", "output_index": 0, "content_index": 0, "annotation": annotation}))
	}
	events = append(events, encode(map[string]any{"type": "response.content_part.done", "item_id": "message", "output_index": 0, "content_index": 0, "part": part}), encode(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}), completed)
	for _, stream := range []bool{false, true} {
		s := fixture(t, func(*http.Request) (*http.Response, error) { return upstreamResponse(200, sse(events...)), nil })
		response := request(s, "/v1/messages", fmt.Sprintf(`{"model":"sol","stream":%t,"messages":[{"role":"user","content":"Review public release facts."}]}`, stream))
		if response.Code != 200 {
			t.Fatal("citation response failed", response.Code)
		}
		text := ""
		if !stream {
			text = gjson.Get(response.Body.String(), "content.0.text").String()
		} else {
			for _, line := range strings.Split(response.Body.String(), "\n") {
				if strings.HasPrefix(line, "data: ") {
					e := gjson.Parse(strings.TrimPrefix(line, "data: "))
					if e.Get("delta.type").String() == "text_delta" {
						text += e.Get("delta.text").String()
					}
				}
			}
		}
		want := "First release fact. [Source 1](https://example.org/release-1)\n\nSecond release fact. [Source 2](https://example.org/release-2) [Source 3](https://example.org/release-3)"
		if text != want {
			t.Fatalf("stream=%t: %q", stream, text)
		}
	}
}
