package claude

import (
	"net/url"
	"strings"

	translatorcommon "github.com/marenzo/claudex/internal/translator/common"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Codex URL annotations have no Anthropic encrypted_index. Render real source
// links as Markdown instead of inventing an Anthropic citation credential.
func codexCitationLinks(annotations gjson.Result, seen map[string]struct{}) string {
	var out strings.Builder
	for _, annotation := range annotations.Array() {
		if annotation.Get("type").String() != "url_citation" {
			continue
		}
		address := annotation.Get("url").String()
		u, err := url.Parse(address)
		if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
			continue
		}
		if _, exists := seen[address]; exists {
			continue
		}
		seen[address] = struct{}{}
		title := annotation.Get("title").String()
		if title == "" {
			title = u.Host
		}
		title = strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "\n", " ", "\r", " ").Replace(title)
		// Plain Markdown links: some clients HTML-escape the <url> form. Encode every
		// byte that could end or confuse the link target instead.
		address = strings.NewReplacer("<", "%3C", ">", "%3E", "(", "%28", ")", "%29", "\n", "%0A", "\r", "%0D", " ", "%20").Replace(stripTracking(u))
		out.WriteString(" [" + title + "](" + address + ")")
	}
	return out.String()
}

func appendCodexCitationLinks(output []byte, params *streamState, annotations gjson.Result) []byte {
	if params.CitationURLs == nil {
		params.CitationURLs = make(map[string]struct{})
	}
	text := codexCitationLinks(annotations, params.CitationURLs)
	if text == "" {
		return output
	}
	output = append(output, finalizeCodexThinkingBlock(params)...)
	output = append(output, startCodexTextBlock(params)...)
	delta := []byte(`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
	delta, _ = sjson.SetBytes(delta, "index", params.BlockIndex)
	delta, _ = sjson.SetBytes(delta, "delta.text", text)
	return translatorcommon.AppendSSEEventBytes(output, "content_block_delta", delta, 2)
}

// stripTracking removes the utm_source=openai marker Codex appends to cited URLs.
func stripTracking(u *url.URL) string {
	query := u.Query()
	if query.Get("utm_source") != "openai" {
		return u.String()
	}
	query.Del("utm_source")
	copy := *u
	copy.RawQuery = query.Encode()
	return copy.String()
}
