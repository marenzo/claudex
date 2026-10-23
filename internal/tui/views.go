package tui

import (
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marenzo/claudex/internal/metrics"
)

func number(n int64) string { return strconv.FormatInt(n, 10) }
func ratio(a, b int64) string {
	if b <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(a)/float64(b))
}
func duration(n *int64) string {
	if n == nil {
		return "—"
	}
	return (time.Duration(*n) * time.Millisecond).Round(10 * time.Millisecond).String()
}
func orAll(s string) string {
	if s == "" {
		return "all"
	}
	return Safe(s)
}
func outcome(r metrics.Request) string {
	if r.Role == metrics.Review && r.Status < 400 && r.Flagged > 0 {
		return fmt.Sprintf("%d blocked", r.Flagged)
	}
	if r.Status == 499 {
		return "○ Canceled"
	}
	if r.Status == 429 {
		return "× Limited"
	}
	if r.Status >= 400 {
		return "× Failed"
	}
	return "✓ OK"
}
func (m *model) outcomeColor(r metrics.Request) string {
	if r.Role == metrics.Review && r.Status < 400 && r.Flagged > 0 {
		return colors(m.light).amber
	}
	p := colors(m.light)
	if r.Status == 499 {
		return p.dim
	}
	if r.Status >= 400 {
		return p.bad
	}
	return p.ok
}

func roleLabel(role metrics.Role) string {
	switch role {
	case metrics.Generation:
		return "Generation"
	case metrics.Review:
		return "Auto reviews"
	default:
		return "All calls"
	}
}

func (m *model) scopedSnapshot() metrics.Snapshot { return m.snapshot.ForRole(m.role) }
func totalsFor(rows []metrics.Request) metrics.Totals {
	var t metrics.Totals
	for _, r := range rows {
		t.Requests++
		if r.Status == 499 {
			t.Canceled++
		} else if r.Status >= 400 {
			t.Errors++
		}
		if r.Status == 429 {
			t.RateLimited++
		}
		if r.UsageReported {
			t.UsageReported++
			t.Tokens.Input += r.Usage.Input
			t.Tokens.Cached += r.Usage.Cached
			t.Tokens.Output += r.Usage.Output
			t.Tokens.Reasoning += r.Usage.Reasoning
			if r.Role == metrics.Review && r.Status == 200 {
				t.ReviewedTools += int64(r.ToolCalls)
				t.FlaggedTools += int64(r.Flagged)
			}
		}
	}
	return t
}
func (m *model) usageText(t metrics.Totals, w int) string {
	p := colors(m.light)
	if t.UsageReported == 0 {
		return ink("Token counts unavailable", p.dim) + "\nNo usage was reported for these requests."
	}
	parts := []segment{{"Cached input", t.Tokens.Cached, p.good}, {"New input", max(0, t.Tokens.Input-t.Tokens.Cached), p.focus}, {"Output", t.Tokens.Output, p.blue}}
	return allocation(parts, w) + "\n" + ink("● Cache "+compact(t.Tokens.Cached), p.good) + "  " + ink("● New "+compact(t.Tokens.Input-t.Tokens.Cached), p.focus) + "  " + ink("● Out "+compact(t.Tokens.Output), p.blue) + "\n" + ratio(t.Tokens.Cached, t.Tokens.Input) + " of input reused from cache"
}
func (m *model) requestSummary(w, h int, rows []metrics.Request) string {
	p := colors(m.light)
	all, t := totalsFor(m.scopedSnapshot().Recent), totalsFor(rows)
	parts := []segment{{"Successful", all.Requests - all.Errors - all.Canceled, p.ok}, {"Failed", all.Errors, p.bad}, {"Canceled", all.Canceled, p.dim}}
	var times []int64
	for _, r := range rows {
		times = append(times, r.DurationMS)
	}
	if h == 2 {
		usage := "Token counts unavailable"
		if t.UsageReported > 0 {
			usage = compact(t.Tokens.Input+t.Tokens.Output) + " tokens · " + ratio(t.Tokens.Cached, t.Tokens.Input) + " input cached"
		}
		return fit(ink(fmt.Sprintf("%d successful · %d failed · %d canceled", parts[0].value, all.Errors, all.Canceled), p.dim)+"\n"+ink(usage, p.good), w, h)
	}
	if h < 9 || w < 90 {
		usage := "Token counts unavailable"
		if t.UsageReported > 0 {
			usage = compact(t.Tokens.Input+t.Tokens.Output) + " tokens · " + ratio(t.Tokens.Cached, t.Tokens.Input) + " input from cache"
		}
		return m.panel("Recent requests", fmt.Sprintf("%d successful · %d failed · %d canceled\n%d matching · median %s\n%s", parts[0].value, all.Errors, all.Canceled, len(rows), duration(metrics.Percentile(times, .5)), usage), w, h, false)
	}
	lw := (w - 2) * 6 / 11
	rw := w - lw - 2
	legendW := max(1, lw-18)
	var legend []string
	for i, v := range parts {
		label := []string{"✓ ", "× ", "○ "}[i] + v.label
		if m.filter.Status == []string{"ok", "errors", "canceled"}[i] {
			label = "▸ " + v.label
		}
		legend = append(legend, pair(ink(label, v.color), strong(number(v.value), p.text)+" "+ink(ratio(v.value, all.Requests), p.dim), legendW), "")
		m.hits = append(m.hits, hit{16, 2 + i*2, legendW, 1, "outcome", int64(i)})
	}
	ring := donut(parts, 12, 6)
	ringLines := strings.Split(ring, "\n")
	label := compact(all.Requests)
	start := (12 - len(label)) / 2
	ringLines[3] = ansi.Cut(ringLines[3], 0, start) + strong(label, p.text) + ansi.Cut(ringLines[3], start+len(label), 12)
	left := beside(strings.Join(ringLines, "\n"), strings.Join(legend, "\n"), 12, legendW, 6) + "\n" + ink(fmt.Sprintf("All %d recent requests · choose an outcome to filter", all.Requests), p.dim)
	usage := ink("Token counts unavailable", p.dim) + "\nNo matching request reported token usage.\n\n"
	if t.UsageReported > 0 {
		usage = pair(ink("Input from cache", p.dim), strong(ratio(t.Tokens.Cached, t.Tokens.Input), p.good), rw-4) + "\n\n" + allocation([]segment{{"Cached", t.Tokens.Cached, p.good}, {"New", t.Tokens.Input - t.Tokens.Cached, p.input}, {"Output", t.Tokens.Output, p.blue}}, rw-4) + "\n" + ink("■ cached "+compact(t.Tokens.Cached), p.good) + "  " + ink("■ new "+compact(t.Tokens.Input-t.Tokens.Cached), p.input) + "  " + ink("■ output "+compact(t.Tokens.Output), p.blue) + "\n\n"
	}
	usage += pair(ink("Median response", p.dim), strong(duration(metrics.Percentile(times, .5)), p.text), rw-4) + "\n" + ink(fmt.Sprintf("Token counts available: %d of %d requests", t.UsageReported, t.Requests), p.dim)
	if t.ReviewedTools > 0 {
		usage += "\n" + ink(fmt.Sprintf("%d tools reviewed · %d blocked", t.ReviewedTools, t.FlaggedTools), p.amber)
	}
	return spaced(m.panel("Request outcomes", left, lw, h, false), m.panel("Usage & response time", usage, rw, h, false), lw, rw, h, 2)
}
func (m *model) requestFilters(w int) string {
	p := colors(m.light)
	query := "/ Search requests…"
	if m.filter.Search != "" {
		query = "/ " + Safe(m.filter.Search)
	}
	items := []struct {
		key    rune
		label  string
		active bool
	}{{'f', outcomeFilter(m.filter.Status), m.filter.Status != ""}, {'m', modelName(orAll(m.filter.Model)), m.filter.Model != ""}}
	if w >= 100 {
		items = append(items, struct {
			key    rune
			label  string
			active bool
		}{'e', orAll(m.filter.Effort), m.filter.Effort != ""})
	}
	var filters string
	for i, item := range items {
		if i > 0 {
			filters += " "
		}
		filters += m.chip(string(item.key)+" "+item.label, item.active)
	}
	queryW := max(14, w-ansi.StringWidth(filters)-2)
	m.hits = append(m.hits, hit{0, 0, queryW, 1, "key", int64('/')})
	x := queryW + 2
	for _, item := range items {
		size := len(item.label) + 4
		m.hits = append(m.hits, hit{x, 0, size, 1, "key", int64(item.key)})
		x += size + 1
	}
	return surface(" "+cell(query, queryW-2)+" ", queryW, p.surface, p.dim) + "  " + filters + "\n" + strings.Repeat(" ", w)
}
func (m *model) requestTable(w, h int, rows []metrics.Request, y int) string {
	p := colors(m.light)
	iw := max(1, w-4)
	var lines []string
	wide := iw >= 66
	typeW := 0
	if m.role == "" && iw >= 70 {
		typeW = 12
	}
	first := iw >= 88+typeW
	effort := iw >= 99+typeW
	timeW, modelW, resultW, durationW, cacheW := 10, 14, 12, 10, 8
	if iw < 48 {
		timeW, modelW, resultW, durationW = 6, max(6, iw-24), 10, 6
	}
	row := func(at, model, role, result, elapsed, cache, delta, eff string) string {
		text := cell(at, timeW) + cell(model, modelW)
		if typeW > 0 {
			text += cell(role, typeW)
		}
		text += cell(result, resultW) + right(elapsed, durationW-1) + " "
		if wide {
			text += right(cache, cacheW-1) + " "
		}
		if first {
			text += right(delta, 9) + " "
		}
		if effort {
			text += " " + eff
		}
		return cell("  "+text, iw)
	}
	header := row("Started", "Model", "Type", "Result", "Duration", "Cache", "To client", "Effort")
	lines = append(lines, surface(header, iw, p.raised, p.dim), ink(strings.Repeat("─", iw), p.border))
	if len(rows) == 0 {
		lines = append(lines, "", strong("No matching requests", p.text), ink("Press x to clear filters.", p.dim))
		return m.panel("Completed requests", strings.Join(lines, "\n"), w, h, m.focus == 0)
	}
	selected := 0
	for i, r := range rows {
		if r.ID == m.selected {
			selected = i
			break
		}
	}
	stride := 1
	if h >= 24 {
		stride = 2
	}
	count := max(1, (h-7)/stride)
	start := max(0, min(selected-count/2, len(rows)-count))
	end := min(len(rows), start+count)
	for i := start; i < end; i++ {
		r := rows[i]
		cache := "—"
		if r.UsageReported {
			cache = ratio(r.Usage.Cached, r.Usage.Input)
		}
		at := r.Started.Local().Format("15:04:05")
		if iw < 48 {
			at = r.Started.Local().Format("15:04")
		}
		line := row(at, modelName(r.Model), roleLabel(r.Role), ink(outcome(r), m.outcomeColor(r)), duration(&r.DurationMS), cache, duration(r.ClientFirstOutputMS), Safe(r.Effort))
		if r.ID == m.selected {
			line = surface("▎"+ansi.Cut(line, 1, iw), iw, p.raised, p.focus)
		}
		lines = append(lines, line)
		m.hits = append(m.hits, hit{1, y + 4 + (i-start)*stride, w - 2, stride, "request", r.ID})
		if stride == 2 {
			line = ink(strings.Repeat("─", iw), p.border)
			if r.ID == m.selected {
				line = surface("▎"+strings.Repeat(" ", iw-1), iw, p.raised, p.focus)
			}
			lines = append(lines, line)
		}
	}
	for len(lines) < h-5 {
		lines = append(lines, "")
	}
	lines = append(lines, "", ink(pair(fmt.Sprintf("%d–%d of %d requests", start+1, end, len(rows)), "o "+[]string{"Newest first", "Longest first", "Most cache reuse"}[m.sort], iw), p.dim))
	return m.panel("Completed requests", strings.Join(lines, "\n"), w, h, m.focus == 0)
}
func outcomeFilter(s string) string {
	switch s {
	case "ok":
		return "successful"
	case "errors":
		return "failed"
	case "canceled":
		return "canceled"
	case "429":
		return "rate limited"
	default:
		return "all outcomes"
	}
}
func (m *model) requestDetail(r metrics.Request, w int) string {
	p := colors(m.light)
	var b strings.Builder
	line := func(label, value string) { b.WriteString(pair(ink(label, p.dim), value, w) + "\n") }
	b.WriteString(strong(modelName(r.Model), p.text) + ink(" · "+Safe(r.Effort)+" effort", p.dim) + "\n")
	line("Type", roleLabel(r.Role))
	line(outcome(r), strong(duration(&r.DurationMS), m.outcomeColor(r)))
	b.WriteString("\n")
	line("Started", r.Started.Local().Format("Jan 02 15:04:05"))
	line("Ended", r.Completed.Local().Format("Jan 02 15:04:05"))
	b.WriteString("\n" + strong("Response timeline", p.text) + "\n")
	timeline := []rune(strings.Repeat("─", max(1, w)))
	if r.ClientFirstOutputMS != nil && r.DurationMS > 0 {
		i := min(len(timeline)-1, max(0, int(float64(*r.ClientFirstOutputMS)/float64(r.DurationMS)*float64(len(timeline)-1))))
		timeline[i] = '◆'
	}
	timeline[len(timeline)-1] = '●'
	b.WriteString(ink(string(timeline), p.focus) + "\n")
	line("First from model", duration(r.UpstreamFirstOutputMS))
	if r.UpstreamAttempts > 1 {
		line("Upstream attempts", strconv.Itoa(r.UpstreamAttempts))
	}
	if r.Role == metrics.Generation {
		line("First to client", duration(r.ClientFirstOutputMS))
		if r.ReviewMS != nil {
			line("Auto review time", duration(r.ReviewMS))
		}
		b.WriteString(ink("Client timing excludes keep-alive pings.", p.dim) + "\n\n")
	} else {
		line("Tools reviewed", number(int64(r.ToolCalls)))
		line("Tools blocked", number(int64(r.Flagged)))
		b.WriteString(ink("Review duration includes queue and model time.", p.dim) + "\n\n")
	}
	if r.UsageReported {
		line("Input breakdown", strong(number(r.Usage.Input)+" tokens", p.text))
		b.WriteString(breakdown([]segment{{"From cache", r.Usage.Cached, p.good}, {"New input", max(0, r.Usage.Input-r.Usage.Cached), p.input}}, w) + "\n\n")
		line("Output breakdown", strong(number(r.Usage.Output)+" tokens", p.text))
		b.WriteString(breakdown([]segment{{"Reasoning", r.Usage.Reasoning, p.blue}, {"Other output", max(0, r.Usage.Output-r.Usage.Reasoning), p.focus}}, w) + "\n")
		b.WriteString(ink("Reasoning is included in output.", p.dim) + "\n")
	} else {
		b.WriteString(ink("Token counts unavailable\nThis request did not report usage.", p.dim) + "\n")
	}
	if r.Status == 499 {
		b.WriteString("\nThe caller canceled or disconnected.\n")
	} else if r.Status == 429 {
		b.WriteString("\n" + strong("Rate limited (HTTP 429)", p.bad) + "\nThe upstream could not accept the request.\n")
	} else if r.Status >= 400 {
		fmt.Fprintf(&b, "\n%s\n", strong(fmt.Sprintf("Request failed (HTTP %d)", r.Status), p.bad))
	}
	if r.Error != nil {
		e := r.Error
		b.WriteString("\n" + strong("Error details", p.text) + "\n" + Safe(e.Message) + "\n")
		for _, v := range []struct{ k, v string }{{"Source", e.Source}, {"Type", e.Type}, {"Code", e.Code}} {
			if v.v != "" {
				line(v.k, Safe(v.v))
			}
		}
		if e.Status != 0 {
			line("Upstream HTTP status", strconv.Itoa(e.Status))
		}
		if e.Truncated {
			b.WriteString("Error excerpt shortened.\n")
		}
	}
	if r.Cache != nil {
		v := r.Cache
		b.WriteString("\n" + strong("Cache diagnostics", p.text) + "\n")
		if !v.Previous {
			b.WriteString("First observed review in this cache group.\n")
		} else {
			line("Shared prefix", fmt.Sprintf("%d messages · %s bytes", v.SharedMessages, compact(int64(v.SharedBytes))))
			if v.PreviousEndpointMatched {
				b.WriteString("Previous cache boundary is preserved.\n")
			}
			if v.PrefixChanged {
				b.WriteString("Instructions or tool definitions changed.\n")
			}
			if v.ContextChanged {
				b.WriteString("Current permission or live context changed.\n")
			}
		}
		if v.Truncated {
			b.WriteString("Prefix comparison limited to 256 messages.\n")
		}
	}
	if r.TraceID != "" {
		b.WriteString("\n" + strong("Request links", p.text) + "\n")
		line("Trace", Safe(r.TraceID))
	}
	if r.ParentTraceID != "" {
		line("Generation trace", Safe(r.ParentTraceID))
	}
	b.WriteString(ink("\nMessage content and tool arguments are not stored.", p.dim))
	return b.String()
}
func (m *model) detailPane(r metrics.Request, w, h int) string {
	m.view.SetWidth(max(1, w-4))
	m.view.SetHeight(max(1, h-3))
	m.view.SetContent(wrap(m.requestDetail(r, max(1, w-4)), max(1, w-4)))
	title := fmt.Sprintf("Request #%d", r.ID)
	if m.focus == 1 {
		title += " · scroll ↑↓"
	}
	return m.panel(title, m.view.View(), w, h, m.focus == 1)
}
func (m *model) requests() string {
	w, h, _ := m.dimensions()
	rows := m.records()
	m.ensureSelection(rows)
	var selected metrics.Request
	for _, r := range rows {
		if r.ID == m.selected {
			selected = r
			break
		}
	}
	if m.expanded && (w < 100 || h < 24) && selected.ID != 0 {
		return m.detailPane(selected, w, h)
	}
	top := ""
	topH := 0
	if h >= 24 {
		top = m.requestFilters(w) + "\n"
		topH = 2
	}
	summaryH := 10
	if h < 27 {
		summaryH = 6
	}
	if h < 17 {
		summaryH = 2
	}
	if h < 13 {
		summaryH = 0
	}
	if summaryH > 0 {
		oldHits := len(m.hits)
		top += m.requestSummary(w, summaryH, rows) + "\n"
		for i := oldHits; i < len(m.hits); i++ {
			m.hits[i].y += topH
		}
		topH += summaryH + 1
	}
	bodyH := max(1, h-topH)
	if w >= 100 && selected.ID != 0 && bodyH >= 11 {
		lw := (w - 2) * 17 / 27
		rw := w - lw - 2
		return top + spaced(m.requestTable(lw, bodyH, rows, topH), m.detailPane(selected, rw, bodyH), lw, rw, bodyH, 2)
	}
	return top + m.requestTable(w, bodyH, rows, topH)
}
func (m *model) overview() string {
	w, h, _ := m.dimensions()
	p := colors(m.light)
	s := metrics.Window(m.scopedSnapshot(), m.minutes)
	var t metrics.Totals
	var activity, failed, canceled, cached, input, output []int64
	for _, b := range s.History {
		x := b.Totals
		t.Requests += x.Requests
		t.Errors += x.Errors
		t.Canceled += x.Canceled
		t.UsageReported += x.UsageReported
		t.Tokens.Input += x.Tokens.Input
		t.Tokens.Cached += x.Tokens.Cached
		t.Tokens.Output += x.Tokens.Output
		activity = append(activity, x.Requests)
		failed = append(failed, x.Errors)
		canceled = append(canceled, x.Canceled)
		cached = append(cached, x.Tokens.Cached)
		input = append(input, max(0, x.Tokens.Input-x.Tokens.Cached))
		output = append(output, x.Tokens.Output)
	}
	stats := func(sw int) string {
		labels := []string{"Completed", "Success rate", "Input from cache", "95% within"}
		values := []string{compact(t.Requests), ratio(t.Requests-t.Errors-t.Canceled, t.Requests-t.Canceled), "—", duration(s.P95)}
		if t.UsageReported > 0 {
			values[2] = ratio(t.Tokens.Cached, t.Tokens.Input)
		}
		notes := []string{fmt.Sprintf("%d failed · %d canceled", t.Errors, t.Canceled), "Excludes cancellations", fmt.Sprintf("%d report usage", t.UsageReported), fmt.Sprintf("%d retained requests", len(s.Records))}
		colors := []string{p.text, p.ok, p.good, p.blue}
		cols := 4
		if sw < 72 {
			cols = 2
		}
		var blocks []string
		for start := 0; start < 4; start += cols {
			widths := make([]int, cols)
			for i := range widths {
				widths[i] = (sw - (cols-1)*2) / cols
			}
			widths[cols-1] += sw - (cols-1)*2 - widths[0]*cols
			var rows [3]string
			for i := range cols {
				j := start + i
				rows[0] += cell(ink(labels[j], p.dim), widths[i])
				rows[1] += cell(strong(values[j], colors[j]), widths[i])
				rows[2] += cell(ink(notes[j], p.dim), widths[i])
				if i < cols-1 {
					for k := range rows {
						rows[k] += "  "
					}
				}
			}
			blocks = append(blocks, strings.Join(rows[:], "\n"))
		}
		return strings.Join(blocks, "\n\n")
	}
	top := stats(w)
	if h >= 38 && w >= 120 {
		top = spaced(ink(wordmark, p.focus), stats(w-41), 39, w-41, 4, 2)
	}
	if h < 19 {
		cache := "Tokens —"
		if t.UsageReported > 0 {
			cache = compact(t.Tokens.Input+t.Tokens.Output) + " tokens"
		}
		top = strong(fmt.Sprintf("%d requests · %d failed", t.Requests, t.Errors), p.text) + "\n" + ink(cache+" · 95% within "+duration(s.P95), p.dim)
	}

	topH := strings.Count(top, "\n") + 1
	available := max(3, h-topH-1)
	chart := func(which, cw, ch int) string {
		iw := max(1, cw-4)
		ih := max(1, ch-3)
		content := ""
		title := ""
		switch which {
		case 0:
			title = "Request activity"
			content = ink(fmt.Sprintf("Last %dm · every completed request", m.minutes), p.dim) + "\n" + columnChart(activity, iw, max(1, ih-7), p.focus) + "\n" + ink("Failures  "+spark(failed, max(1, iw-10)), p.bad) + "\n" + ink("Canceled  "+spark(canceled, max(1, iw-10)), p.dim)
			if len(s.History) > 0 {
				idx := min(max(0, m.minute), len(s.History)-1)
				b := s.History[idx]
				content += "\n" + fmt.Sprintf("%s  %d OK · %d failed · %d canceled", b.Time.Local().Format("15:04"), b.Totals.Requests-b.Totals.Errors-b.Totals.Canceled, b.Totals.Errors, b.Totals.Canceled)
				if b.Totals.UsageReported > 0 {
					u := b.Totals.Tokens
					content += "\n" + fmt.Sprintf("Input %s · cached %s · output %s", number(u.Input), number(u.Cached), number(u.Output)) + "\n" + fmt.Sprintf("Reasoning %s · %d report usage", number(u.Reasoning), b.Totals.UsageReported)
				} else {
					content += "\nToken counts unavailable for this minute"
				}
				content += "\n" + ink("←→ minute · Enter requests · current minute partial", p.dim)

			}
		case 1:
			title = "Token flow"
			var peak int64
			for _, series := range [][]int64{cached, input, output} {
				for _, v := range series {
					peak = max(peak, v)
				}
			}
			content = ink("● Cached ", p.good) + ink("● New input ", p.input) + ink("● Output", p.blue) + "\n" + ink(fmt.Sprintf("Scale: 0–%s tokens per minute", compact(peak)), p.dim) + "\n" + lineChart([][]int64{cached, input, output}, []string{p.good, p.input, p.blue}, iw, max(1, ih-6)) + "\n" + fmt.Sprintf("Counts reported for %d of %d requests", t.UsageReported, t.Requests) + "\n" + ink("Cached input is part of total input.", p.dim)
		case 2:
			title = "Response times"
			content = ink(fmt.Sprintf("%d recent requests · failures included", len(s.Records)), p.dim) + "\nMedian " + duration(s.P50) + " · 95% within " + duration(s.P95) + "\n"
			peak := 0
			for _, n := range s.Histogram {
				peak = max(peak, n)
			}
			for i, n := range s.Histogram {
				content += cell(metrics.HistogramLabels[i], 7) + ink(bar(int64(n), int64(peak), max(1, iw-14)), p.blue) + fmt.Sprintf(" %d\n", n)
			}
			content += "First to client " + duration(s.FirstP50) + fmt.Sprintf(" (%d reported)", s.FirstCount) + "\n99% within " + duration(s.P99)
		case 3:
			title = "Model & effort mix"
			mix := map[string]int64{}
			for _, r := range s.Records {
				mix[modelName(r.Model)+" · "+Safe(r.Effort)]++
			}
			var names []string
			for k := range mix {
				names = append(names, k)
			}
			sort.Slice(names, func(i, j int) bool {
				if mix[names[i]] == mix[names[j]] {
					return names[i] < names[j]
				}
				return mix[names[i]] > mix[names[j]]
			})
			for _, n := range names {
				content += cell(n, max(1, iw-7)) + fmt.Sprintf(" %5d\n", mix[n])
			}
			if len(names) == 0 {
				content = "Usage appears after the first request."
			}
			content += "\n" + ink("Counts use retained requests in this window.", p.dim)
			all := m.snapshot.Totals
			content += fmt.Sprintf("\n\nSince gateway started: %d requests\n%d failed · %d canceled · %d rate limited\nSuccess %s (excludes cancellations)\n", all.Requests, all.Errors, all.Canceled, all.RateLimited, ratio(all.Requests-all.Errors-all.Canceled, all.Requests-all.Canceled)) + m.usageText(all, iw) + fmt.Sprintf("\nUsage reported for %d of %d requests", all.UsageReported, all.Requests)
		}
		return m.panel(title, content, cw, ch, m.focus == which)
	}
	if m.zoom || w < 85 || available < 22 {
		return top + "\n" + chart(m.focus%4, w, available)
	}
	lw := (w - 1) / 2
	rw := w - lw - 1
	upper := (available - 1) / 2
	lower := available - upper - 1
	return top + "\n" + beside(chart(0, lw, upper), chart(1, rw, upper), lw, rw, upper) + "\n" + beside(chart(2, lw, lower), chart(3, rw, lower), lw, rw, lower)
}
func spark(values []int64, w int) string {
	if len(values) == 0 {
		return "—"
	}
	w = max(1, w)
	var peak int64
	for _, v := range values {
		peak = max(peak, v)
	}
	levels := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for i := 0; i < min(w, len(values)); i++ {
		var v int64
		for j := i * len(values) / min(w, len(values)); j < (i+1)*len(values)/min(w, len(values)); j++ {
			v = max(v, values[j])
		}
		level := int64(0)
		if peak > 0 {
			level = v * 7 / peak
		}
		b.WriteRune(levels[level])
	}
	return b.String()
}
func bar(n, peak int64, w int) string {
	if n <= 0 || peak <= 0 {
		return ""
	}
	return strings.Repeat("━", max(1, min(w, int(float64(n)/float64(peak)*float64(w)))))
}
func (m *model) models() string {
	w, h, _ := m.dimensions()
	p := colors(m.light)
	ids := modelIDs()
	m.modelIndex = min(m.modelIndex, len(ids)-1)
	snapshot := m.scopedSnapshot()
	sample := metrics.Window(snapshot, m.minutes)
	var cards []string
	for i, id := range ids {
		t := snapshot.Models[id]
		name := modelName(id)
		if m.settings.Preferences.Model == id {
			name += " · default"
		}
		prefix := "  "
		if i == m.modelIndex {
			prefix = "▸ "
		}
		cards = append(cards, strong(prefix+name, p.focus), fmt.Sprintf("  %d requests · %d failed · %d canceled", t.Requests, t.Errors, t.Canceled), ink("  Input cache "+ratio(t.Tokens.Cached, t.Tokens.Input), p.good), "")
	}
	id := ids[m.modelIndex]
	t := snapshot.Models[id]
	var times, first []int64
	for _, r := range sample.Records {
		if r.Model == id {
			times = append(times, r.DurationMS)
			if r.ClientFirstOutputMS != nil {
				first = append(first, *r.ClientFirstOutputMS)
			}
		}
	}
	detail := strong(modelName(id), p.focus) + "\n" + ink(roleLabel(m.role)+" · totals since gateway started", p.dim) + "\n\n" + fmt.Sprintf("%d completed · %d rate limited\nFailure rate %s (excludes cancellations)\n\n", t.Requests, t.RateLimited, ratio(t.Errors, t.Requests-t.Canceled)) + m.usageText(t, max(1, w/2-6)) + fmt.Sprintf("\nCounts reported for %d of %d requests\n\n", t.UsageReported, t.Requests) + strong("Response time", p.text) + fmt.Sprintf("\n%d retained requests in last %dm\nMedian %s\n95%% within %s\nFirst to client median %s\n\n", len(times), m.minutes, duration(metrics.Percentile(times, .5)), duration(metrics.Percentile(times, .95)), duration(metrics.Percentile(first, .5)))
	if t.UsageReported > 0 {
		detail += fmt.Sprintf("Input %s · cached %s\nOutput %s · reasoning %s\nReasoning is included in output.\n", number(t.Tokens.Input), number(t.Tokens.Cached), number(t.Tokens.Output), number(t.Tokens.Reasoning))
	}
	detail += fmt.Sprintf("\nGateway uptime %s\n%d quota recoveries\nPositive probes that cleared a cooldown.", (time.Duration(m.snapshot.UptimeSeconds) * time.Second).String(), m.snapshot.QuotaRecoveries)
	if w >= 90 {
		lw := (w - 1) / 2
		rw := w - lw - 1
		m.view.SetWidth(rw - 4)
		m.view.SetHeight(h - 3)
		m.view.SetContent(wrap(detail, rw-4))
		return beside(m.panel("Models", strings.Join(cards, "\n"), lw, h, m.focus == 0), m.panel("Selected model", m.view.View(), rw, h, m.focus == 1), lw, rw, h)
	}
	m.view.SetWidth(max(1, w-4))
	m.view.SetHeight(max(1, h-3))
	m.view.SetContent(wrap("↑↓ choose model\n\n"+detail, max(1, w-4)))
	return m.panel("Models", m.view.View(), w, h, true)
}
func (m *model) preferences() string {
	w, h, _ := m.dimensions()
	p := colors(m.light)
	if m.settings.Revision == "" {
		return m.panel("Settings", "Loading gateway settings…", w, h, false)
	}

	names := []string{"Default model", "Reasoning effort", "Command reviewer", "Context window", "Compact at"}
	descriptions := []string{"Model used by new Claude sessions.", "How much reasoning the model performs.", "Reviews commands in auto mode. Effort: high.", "Maximum conversation size in tokens.", "Summarize the conversation at this many tokens."}
	var lines []string
	positions := make([]int, 5)
	fieldW := max(8, w-4)
	if w >= 100 {
		fieldW = (w-1)/2 - 4
	}
	for i, name := range names {
		switch i {
		case 0:
			lines = append(lines, strong("GENERATION", p.focus), ink("New Claude sessions", p.dim), "")
		case 2:
			lines = append(lines, ink(strings.Repeat("─", fieldW), p.border), strong("AUTO-MODE REVIEW", p.focus), "")
		case 3:
			lines = append(lines, ink(strings.Repeat("─", fieldW), p.border), strong("CONVERSATION SIZE", p.focus), "")
		}
		positions[i] = len(lines)
		value := ""
		switch i {
		case 0:
			value = modelName(m.draft.Model)
		case 1:
			value = m.draft.Effort
		case 2:
			value = modelName(m.draft.Reviewer)
			if value == "" {
				value = "Claude client"
			}
		case 3:
			value = strconv.Itoa(m.draft.ContextWindow)
		case 4:
			value = strconv.Itoa(m.draft.CompactWindow)
		}
		labelW := min(21, max(8, fieldW/2))
		valueW := max(4, fieldW-labelW)
		choice := " " + value + " "
		if i < 3 {
			choice += " ‹ ›"
		}
		fg := p.text
		prefix := "  "
		if m.field == i && m.focus == 0 {
			fg = p.focus
			prefix = "▎ "
		}
		label := cell(ink(prefix+name, fg), labelW) + surface(choice, valueW, p.raised, fg)
		lines = append(lines, label, ink("  "+descriptions[i], p.dim), "")
	}
	lines = append(lines, ink(strings.Repeat("─", fieldW), p.border), "", m.chip("s Save changes", m.dirty)+" "+m.chip("d Discard", false), "", ink("Requests already running keep their settings.\nNew Claude sessions pick up startup defaults.", p.dim))
	if !m.settings.Writable {
		lines = append(lines, "", ink("This gateway's settings are read-only.", p.amber))
	}
	startup := ink("Read only · locations, not file contents", p.dim) + "\n\nListen address\n" + Safe(m.settings.Listen) + "\n\nCredential file\n" + Safe(m.settings.AuthFile) + "\n\nClient key file\n" + Safe(m.settings.ClientKeyFile) + "\n\n" + ink("Stop the gateway before editing these startup fields.\n\nRecent history: 500 requests, 120 minutes.\nCleared when the gateway restarts.\nMessage content and tool arguments are not stored.\nError excerpts are bounded and redacted.", p.dim)
	if w >= 100 {
		lw := (w - 1) / 2
		rw := w - lw - 1
		m.view.SetWidth(lw - 4)
		m.view.SetHeight(max(1, min(h, 36)-3))
		m.view.SetContent(wrap(strings.Join(lines, "\n"), lw-4))
		m.revealSetting(lines, positions, lw-4)
		m.side.SetWidth(rw - 4)
		m.side.SetHeight(max(1, min(h, 28)-3))
		m.side.SetContent(wrap(startup, rw-4))
		return beside(m.panel("Preferences", m.view.View(), lw, min(h, 36), m.focus == 0), m.panel("Connection & files", m.side.View(), rw, min(h, 28), m.focus == 1), lw, rw, h)
	}
	content := strings.Join(lines, "\n") + "\n\n" + startup
	m.view.SetWidth(max(1, w-4))
	m.view.SetHeight(max(1, h-3))
	m.view.SetContent(wrap(content, max(1, w-4)))
	m.revealSetting(lines, positions, max(1, w-4))
	return m.panel("Settings", m.view.View(), w, h, true)
}

func (m *model) revealSetting(lines []string, positions []int, w int) {
	if !m.revealField {
		return
	}
	prefix := strings.Join(lines[:positions[m.field]], "\n")
	line := strings.Count(wrap(prefix, w), "\n") + 1
	if line < m.view.YOffset() {
		m.view.SetYOffset(line)
	} else if line+3 >= m.view.YOffset()+m.view.Height() {
		m.view.SetYOffset(max(0, line+4-m.view.Height()))
	}
	m.revealField = false
}
