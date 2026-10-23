package claude

import (
	"net/url"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

const codexCitationOpen = "\ue200cite\ue202"
const codexCitationClose = "\ue201"

type codexURLCitation struct {
	start, end              int
	positioned              bool
	address, original, link string
	used                    bool
}

type codexCitationPart struct {
	cursor                                   int // Consumed raw bytes; retain upstream text separately for snapshot deduplication.
	final                                    bool
	sources                                  []*codexURLCitation
	markerStart, markerEnd                   int
	runeStart, runeEnd, utf16Start, utf16End int
}

func (s *streamState) citationPart(key codexContentPartKey) *codexCitationPart {
	if s.CitationParts == nil {
		s.CitationParts = make(map[codexContentPartKey]*codexCitationPart)
	}
	if s.CitationParts[key] == nil {
		s.CitationParts[key] = &codexCitationPart{}
	}
	return s.CitationParts[key]
}

func (p *codexCitationPart) add(annotations gjson.Result) {
	for _, annotation := range annotations.Array() {
		link := strings.TrimSpace(codexCitationLinks(gjson.Parse("["+annotation.Raw+"]"), make(map[string]struct{})))
		if link == "" {
			continue
		}
		original := annotation.Get("url").String()
		parsed, err := url.Parse(original)
		if err != nil {
			continue
		}
		address := stripTracking(parsed)
		start, end := annotation.Get("start_index"), annotation.Get("end_index")
		positioned := start.Type == gjson.Number && end.Type == gjson.Number && start.Int() >= 0 && end.Int() > start.Int()
		duplicate := false
		for _, source := range p.sources {
			if source.address == address && source.start == int(start.Int()) && source.end == int(end.Int()) && source.positioned == positioned {
				duplicate = true
				break
			}
		}
		if !duplicate {
			p.sources = append(p.sources, &codexURLCitation{start: int(start.Int()), end: int(end.Int()), positioned: positioned, address: address, original: original, link: link})
		}
	}
}

func appendCodexPartCitations(output []byte, params *streamState, outputIndex, contentIndex int64, annotations gjson.Result, final bool) []byte {
	key := codexContentPartKey{OutputIndex: outputIndex, ContentIndex: contentIndex}
	params.citationPart(key).add(annotations)
	return appendCodexRenderedText(output, params, key, final)
}

func appendCodexRenderedText(output []byte, params *streamState, key codexContentPartKey, final bool) []byte {
	part := params.citationPart(key)
	raw := ""
	if content := params.ContentText[key]; content != nil {
		raw = content.String()
	}
	return appendCodexText(output, params, part.render(raw, final))
}

func finishCodexCitations(output []byte, params *streamState) []byte {
	keys := make([]codexContentPartKey, 0, len(params.CitationParts))
	for key := range params.CitationParts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].OutputIndex == keys[j].OutputIndex {
			return keys[i].ContentIndex < keys[j].ContentIndex
		}
		return keys[i].OutputIndex < keys[j].OutputIndex
	})
	for _, key := range keys {
		output = appendCodexRenderedText(output, params, key, true)
	}
	return output
}

func renderCodexCitations(raw string, annotations gjson.Result) string {
	p := &codexCitationPart{}
	p.add(annotations)
	return p.render(raw, true)
}

// Interpret only the known citation escape. Hold split markers and following
// text until annotations resolve them, without ever emitting internal IDs.
func (p *codexCitationPart) render(raw string, final bool) string {
	p.final = p.final || final
	var out strings.Builder
	for p.cursor < len(raw) {
		rest := raw[p.cursor:]
		index := strings.Index(rest, codexCitationOpen)
		if index < 0 {
			keep := citationPrefixSuffix(rest)
			if keep > 0 && citationInCode(raw, p.cursor+len(rest)-keep) {
				keep = 0
			}
			out.WriteString(rest[:len(rest)-keep])
			p.cursor += len(rest) - keep
			if keep > 0 && p.final {
				out.WriteString("[source unavailable]")
				p.cursor = len(raw)
			}
			break
		}
		out.WriteString(rest[:index])
		p.cursor += index
		if citationInCode(raw, p.cursor) {
			out.WriteString(codexCitationOpen)
			p.cursor += len(codexCitationOpen)
			continue
		}
		rest = raw[p.cursor:]
		closeIndex := strings.Index(rest[len(codexCitationOpen):], codexCitationClose)
		if closeIndex < 0 {
			if !p.final {
				break
			}
			// A truncated marker must not swallow a following paragraph.
			end := len(rest)
			if line := strings.IndexByte(rest, '\n'); line >= 0 {
				end = line
			}
			out.WriteString(citationSpacing(raw, p.cursor) + "[source unavailable]")
			p.cursor += end
			continue
		}
		end := p.cursor + len(codexCitationOpen) + closeIndex + len(codexCitationClose)
		links, matched := p.linksFor(raw, p.cursor, end)
		references := strings.Count(raw[p.cursor:end], "\ue202")
		if (len(links) == 0 || len(matched) < references) && !p.final {
			break
		}
		out.WriteString(citationSpacing(raw, p.cursor))
		if len(links) == 0 {
			out.WriteString("[source unavailable]")
		} else {
			out.WriteString(strings.Join(links, " "))
			for _, source := range matched {
				source.used = true
			}
		}
		p.cursor = end
	}
	if p.final {
		var sources []string
		seen := make(map[string]struct{})
		for _, source := range p.sources {
			if source.used {
				continue
			}
			source.used = true
			if strings.Contains(raw, source.original) || strings.Contains(raw, source.address) {
				continue
			}
			if _, exists := seen[source.address]; exists {
				continue
			}
			seen[source.address] = struct{}{}
			sources = append(sources, "- "+source.link)
		}
		if len(sources) > 0 {
			out.WriteString("\n\nSources:\n" + strings.Join(sources, "\n"))
		}
	}
	return out.String()
}

func citationSpacing(raw string, start int) string {
	if start == 0 {
		return ""
	}
	r, _ := utf8.DecodeLastRuneInString(raw[:start])
	if unicode.IsSpace(r) {
		return ""
	}
	return " "
}

func citationPrefixSuffix(text string) int {
	for n := min(len(text), len(codexCitationOpen)-1); n > 0; n-- {
		if strings.HasSuffix(text, codexCitationOpen[:n]) {
			return n
		}
	}
	return 0
}

func (p *codexCitationPart) linksFor(raw string, start, end int) ([]string, []*codexURLCitation) {
	if len(p.sources) == 0 {
		return nil, nil
	}
	// Prefer character offsets; accept exact byte/UTF-16 marker boundaries too.
	// Never turn an ordinal reference ID into a guessed source URL.
	if p.markerStart != start || p.markerEnd != end {
		p.markerStart, p.markerEnd = start, end
		p.runeStart, p.utf16Start = citationCharacterOffsets(raw[:start])
		runes, utf16 := citationCharacterOffsets(raw[start:end])
		p.runeEnd, p.utf16End = p.runeStart+runes, p.utf16Start+utf16
	}
	var links []string
	var matched []*codexURLCitation
	seen := make(map[string]struct{})
	for _, source := range p.sources {
		if !source.positioned {
			continue
		}
		matches := source.start < p.runeEnd && source.end > p.runeStart
		matches = matches || (source.start == start && source.end == end) || (source.start == p.utf16Start && source.end == p.utf16End)
		if !matches {
			continue
		}
		matched = append(matched, source)
		if _, exists := seen[source.address]; exists {
			continue
		}
		seen[source.address] = struct{}{}
		links = append(links, source.link)
	}
	return links, matched
}

func citationCharacterOffsets(text string) (runes, utf16 int) {
	for _, r := range text {
		runes++
		utf16++
		if r > 0xffff {
			utf16++
		}
	}
	return runes, utf16
}

// A code example can intentionally contain provider markup. Preserve both
// fenced code and inline backtick spans instead of rewriting their contents.
func citationInCode(raw string, at int) bool {
	var fence byte
	fenceLen, inlineLen, lineStart := 0, 0, 0
	for i := 0; i < at; {
		if raw[i] == '\n' {
			lineStart = i + 1
			i++
			continue
		}
		char := raw[i]
		if char != '`' && char != '~' {
			i++
			continue
		}
		end := i + 1
		for end < at && raw[end] == char {
			end++
		}
		run := end - i
		lineFence := run >= 3 && i-lineStart <= 3 && strings.TrimSpace(raw[lineStart:i]) == "" && inlineLen == 0
		if lineFence {
			if fence == 0 {
				fence, fenceLen = char, run
			} else if fence == char && run >= fenceLen {
				lineEnd := strings.IndexByte(raw[end:], '\n')
				if lineEnd < 0 {
					lineEnd = len(raw) - end
				}
				if strings.TrimSpace(raw[end:end+lineEnd]) == "" {
					fence, fenceLen = 0, 0
				}
			}
		} else if fence == 0 && char == '`' {
			slashes := 0
			for j := i - 1; j >= 0 && raw[j] == '\\'; j-- {
				slashes++
			}
			if slashes%2 == 0 {
				if inlineLen == 0 {
					inlineLen = run
				} else if inlineLen == run {
					inlineLen = 0
				}
			}
		}
		i = end
	}
	return fence != 0 || inlineLen != 0
}
