package claude

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

func citationAnnotations(raw, marker string, sources ...string) gjson.Result {
	start := strings.Index(raw, marker)
	var annotations []map[string]any
	for _, source := range sources {
		annotations = append(annotations, map[string]any{"type": "url_citation", "start_index": utf8.RuneCountInString(raw[:start]), "end_index": utf8.RuneCountInString(raw[:start+len(marker)]), "url": source, "title": "Source"})
	}
	encoded, _ := json.Marshal(annotations)
	return gjson.ParseBytes(encoded)
}

func TestCitationMarkersResolveAtEveryChunkBoundary(t *testing.T) {
	marker := "\ue200cite\ue202turn0view0\ue202turn0view1\ue201"
	raw := "✅ café: supported. " + marker + "\nNext paragraph."
	annotations := citationAnnotations(raw, marker, "https://example.org/a", "https://example.org/b")
	want := "✅ café: supported. [Source](https://example.org/a) [Source](https://example.org/b)\nNext paragraph."
	for split := 0; split <= len(raw); split++ {
		p := &codexCitationPart{}
		prefix := p.render(raw[:split], false)
		if strings.Contains(prefix, "\ue200") || strings.Contains(prefix, "turn0view") {
			t.Fatalf("partial marker leaked at byte %d", split)
		}
		p.add(annotations)
		got := prefix + p.render(raw, true)
		if got != want {
			t.Fatalf("split %d: %q; want %q", split, got, want)
		}
	}
}

func TestCitationOffsetsStayWithinTheirContentPart(t *testing.T) {
	marker := "\ue200cite\ue202turn0view0\ue201"
	raw := "First claim. " + marker + " Second claim. " + marker
	first := citationAnnotations(raw, marker, "https://example.org/source")
	var anns []map[string]any
	json.Unmarshal([]byte(first.Raw), &anns)
	second := map[string]any{}
	for key, value := range anns[0] {
		second[key] = value
	}
	second["start_index"] = utf8.RuneCountInString(raw[:strings.LastIndex(raw, marker)])
	second["end_index"] = utf8.RuneCountInString(raw)
	anns = append(anns, second)
	encoded, _ := json.Marshal(anns)
	got := renderCodexCitations(raw, gjson.ParseBytes(encoded))
	if strings.Count(got, "[Source](https://example.org/source)") != 2 || strings.Contains(got, "Sources:") {
		t.Fatal("repeated inline provenance lost or duplicated", got)
	}
}

func TestMissingCitationMetadataDoesNotExposeInternalIDsOrInventLinks(t *testing.T) {
	for _, raw := range []string{"Claim. \ue200cite\ue202turn0view0\ue201\nNext paragraph.", "Claim. \ue200cite\ue202turn0view0\nNext paragraph.", "Claim. \ue200ci"} {
		got := renderCodexCitations(raw, gjson.Result{})
		if strings.ContainsAny(got, "\ue200\ue201\ue202") || strings.Contains(got, "turn0view") || strings.Contains(got, "http") || !strings.Contains(got, "[source unavailable]") {
			t.Fatal("unsafe unresolved citation", got)
		}
		if strings.Contains(raw, "Next paragraph.") && !strings.Contains(got, "Next paragraph.") {
			t.Fatal("truncated citation swallowed prose")
		}
	}
}

func TestNativeMarkdownLinksRemainUnchangedAndFallbackSourcesAreSeparated(t *testing.T) {
	native := "Download [Go](https://go.dev/dl/)."
	annotations := gjson.Parse(`[{"type":"url_citation","url":"https://go.dev/dl/","title":"Go downloads"}]`)
	if got := renderCodexCitations(native, annotations); got != native {
		t.Fatal("native Markdown was changed or duplicated", got)
	}
	plain := renderCodexCitations("Download Go.", annotations)
	if plain != "Download Go.\n\nSources:\n- [Go downloads](https://go.dev/dl/)" {
		t.Fatal("sources glued to prose", plain)
	}
	unsafe := gjson.Parse(`[{"type":"url_citation","url":"javascript:alert(1)","title":"bad"}]`)
	if got := renderCodexCitations("Ordinary text.", unsafe); got != "Ordinary text." {
		t.Fatal("unsafe annotation became a link", got)
	}
}

func TestCitationMarkupInsideCodeStaysLiteral(t *testing.T) {
	marker := "\ue200cite\ue202turn0view0\ue201"
	for _, raw := range []string{"Use `" + marker + "` literally.", "```text\n" + marker + "\n```\nDone.", "~~~text\n" + marker + "\n~~~\nDone."} {
		p := &codexCitationPart{}
		var got strings.Builder
		for i := range len(raw) {
			got.WriteString(p.render(raw[:i+1], false))
		}
		got.WriteString(p.render(raw, true))
		if got.String() != raw {
			t.Fatalf("code example changed: %q", got.String())
		}
	}
}

func TestCitationStreamMatchesJSONWithLateAndRepeatedAnnotations(t *testing.T) {
	marker := "\ue200cite\ue202turn0view0\ue201"
	raw := "Release verified. " + marker + "\nMore detail."
	annotations := citationAnnotations(raw, marker, "https://example.org/releases")
	encodedText, _ := json.Marshal(raw)
	part := `{"type":"output_text","text":` + string(encodedText) + `,"annotations":` + annotations.Raw + `}`
	item := `{"type":"message","id":"message","role":"assistant","content":[` + part + `]}`
	events := []string{`{"type":"response.created","response":{"id":"response"}}`}
	// Split every rune into its own SSE event; annotations arrive after text.
	for _, r := range raw {
		encoded, _ := json.Marshal(string(r))
		events = append(events, `{"type":"response.output_text.delta","item_id":"message","output_index":2,"content_index":0,"delta":`+string(encoded)+`}`)
	}
	for _, annotation := range annotations.Array() {
		events = append(events, `{"type":"response.output_text.annotation.added","item_id":"message","output_index":2,"content_index":0,"annotation":`+annotation.Raw+`}`)
	}
	events = append(events, `{"type":"response.content_part.done","item_id":"message","output_index":2,"content_index":0,"part":`+part+`}`, `{"type":"response.output_item.done","output_index":2,"item":`+item+`}`, `{"type":"response.completed","response":{"id":"response","output":[`+item+`]}}`)
	state := NewStream("sol", nil)
	var stream strings.Builder
	for _, event := range events {
		chunks := convertStreamChunk(t, state, []byte("data: "+event))
		for _, chunk := range chunks {
			for _, line := range strings.Split(string(chunk), "\n") {
				if strings.HasPrefix(line, "data: ") {
					payload := gjson.Parse(strings.TrimPrefix(line, "data: "))
					if payload.Get("delta.type").String() == "text_delta" {
						stream.WriteString(payload.Get("delta.text").String())
					}
				}
			}
		}
	}
	nonstream := convertResponse(t, "sol", nil, []byte(events[len(events)-1]))
	if stream.String() != gjson.GetBytes(nonstream, "content.0.text").String() || strings.Contains(stream.String(), "turn0view") || strings.Count(stream.String(), "https://example.org/releases") != 1 {
		t.Fatal("stream/JSON rendering diverged", stream.String(), string(nonstream))
	}
}
