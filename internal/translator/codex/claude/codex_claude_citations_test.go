package claude

import (
	"strings"
	"testing"

	"github.com/tidwall/gjson"
)

func TestNativeSearchSourcesAndReplay(t *testing.T) {
	request := []byte(`{"tools":[{"type":"web_search_20250305","name":"web_search"}],"messages":[]}`)
	terminal := []byte(`{"type":"response.completed","response":{"id":"r","model":"gpt-6-astra","output":[{"id":"ws","type":"web_search_call","action":{"type":"search","query":"Go","sources":[{"type":"url","url":"https://go.dev/dl/","title":"Go downloads"}]}},{"type":"message","content":[{"type":"output_text","text":"Go release","annotations":[{"type":"url_citation","url":"https://go.dev/dl/","title":"Go downloads"}]}]}]}}`)
	result := convertResponse(t, "", request, terminal)
	if strings.Contains(string(result), `"type":"web_search_tool_result"`) || !strings.Contains(string(result), "[Go downloads](https://go.dev/dl/)") {
		t.Fatalf("lost sources: %s", result)
	}
	replay := []byte(`{"messages":[{"role":"assistant","content":` + gjson.GetBytes(result, "content").Raw + `}],"tools":[]}`)
	translated := ConvertRequest("gpt-6-astra", replay)
	if strings.Contains(string(translated), "Web search query: Go") || !strings.Contains(string(translated), "https://go.dev/dl/") {
		t.Fatalf("lost replay: %s", translated)
	}
}
func TestCitationStreamingDeduplication(t *testing.T) {
	state := NewStream("", nil)
	var output strings.Builder
	for _, event := range []string{
		`{"type":"response.created","response":{"id":"r"}}`,
		`{"type":"response.output_text.delta","delta":"Release"}`,
		`{"type":"response.output_text.annotation.added","annotation":{"type":"url_citation","url":"https://go.dev/","title":"Go"}}`,
		`{"type":"response.content_part.done","part":{"type":"output_text","annotations":[{"type":"url_citation","url":"https://go.dev/","title":"Go"}]}}`,
		`{"type":"response.completed","response":{"output":[]}}`,
	} {
		for _, chunk := range convertStreamChunk(t, state, []byte("data: "+event)) {
			output.Write(chunk)
		}
	}
	if strings.Count(output.String(), "https://go.dev/") != 1 {
		t.Fatalf("citation duplicated or lost: %s", output.String())
	}
}

func TestCitationLinksArePlainAndTrackingFree(t *testing.T) {
	annotations := gjson.Parse(`[{"type":"url_citation","url":"https://go.dev/dl/?utm_source=openai","title":"Go (downloads)"},{"type":"url_citation","url":"https://example.com/a(b)?q=1&utm_source=openai","title":"x"}]`)
	got := codexCitationLinks(annotations, map[string]struct{}{})
	want := " [Go (downloads)](https://go.dev/dl/) [x](https://example.com/a%28b%29?q=1)"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}
