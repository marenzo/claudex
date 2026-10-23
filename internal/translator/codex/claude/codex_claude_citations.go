package claude

import (
	"net/url"
	"strings"
	"unicode"

	"github.com/tidwall/gjson"
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
		if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
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
		title = strings.Join(strings.FieldsFunc(title, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
		if title == "" {
			title = u.Host
		}
		title = strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`", "<", "\\<", ">", "\\>").Replace(title)
		// Plain Markdown links: some clients HTML-escape the <url> form. Encode every
		// byte that could end or confuse the link target instead.
		address = strings.NewReplacer("<", "%3C", ">", "%3E", "(", "%28", ")", "%29", "[", "%5B", "]", "%5D", "\n", "%0A", "\r", "%0D", " ", "%20").Replace(stripTracking(u))
		out.WriteString(" [" + title + "](" + address + ")")
	}
	return out.String()
}

// stripTracking removes the utm_source=openai marker Codex appends to cited URLs.
func stripTracking(u *url.URL) string {
	query := u.Query()
	if query.Get("utm_source") != "openai" && query.Get("utm_source") != "chatgpt.com" {
		return u.String()
	}
	query.Del("utm_source")
	copy := *u
	copy.RawQuery = query.Encode()
	return copy.String()
}
