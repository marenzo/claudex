package tui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/metrics"
)

// Semantic colors keep navigation, outcomes and token categories consistent.
type palette struct{ bg, surface, text, dim, border, focus, good, bad, blue, amber, raised, input, ok string }

func colors(light bool) palette {
	if light {
		return palette{"#F7F6F2", "#FFFEFA", "#202636", "#596478", "#D5D7E0", "#6149B8", "#11746A", "#AF354F", "#4865B5", "#855A19", "#EEEDF4", "#566B8A", "#287347"}
	}
	return palette{"#11131B", "#171B26", "#ECEDF4", "#A3AFC3", "#354055", "#BAA9FF", "#7BDCC6", "#F3A0AE", "#8FACF1", "#EDC98A", "#22293A", "#A3B8D4", "#93D4AF"}
}
func ink(s, color string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(s)
}
func strong(s, color string) string {
	return lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(color)).Render(s)
}
func cell(s string, w int) string {
	w = max(0, w)
	s = ansi.Truncate(strings.NewReplacer("\n", " ", "\t", "    ").Replace(s), w, "…")
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// fit creates an exact cell rectangle. Content is already escaped at its boundary.
func fit(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := strings.Split(s, "\n")
	out := make([]string, h)
	for i := range out {
		if i < len(lines) {
			out[i] = cell(lines[i], w)
		} else {
			out[i] = strings.Repeat(" ", w)
		}
	}
	return strings.Join(out, "\n")
}
func beside(a, b string, aw, bw, h int) string { return spaced(a, b, aw, bw, h, 1) }
func spaced(a, b string, aw, bw, h, gap int) string {
	left, right := strings.Split(fit(a, aw, h), "\n"), strings.Split(fit(b, bw, h), "\n")
	for i := range left {
		left[i] += strings.Repeat(" ", gap) + right[i]
	}
	return strings.Join(left, "\n")
}
func surface(s string, w int, bg, fg string) string {
	base := ansi.NewStyle().ForegroundColor(lipgloss.Color(fg)).BackgroundColor(lipgloss.Color(bg)).String()
	// Child styles end with a reset. Restore the surrounding surface after each
	// reset so colored labels do not punch holes in cards or selected rows.
	text := strings.NewReplacer("\x1b[m", "\x1b[m"+base, "\x1b[0m", "\x1b[0m"+base, "\x1b[49m", ansi.NewStyle().BackgroundColor(lipgloss.Color(bg)).String()).Replace(cell(s, w))
	return base + text + ansi.ResetStyle
}
func right(s string, w int) string { return strings.Repeat(" ", max(0, w-ansi.StringWidth(s))) + s }
func pair(left, rightText string, w int) string {
	if ansi.StringWidth(left)+ansi.StringWidth(rightText)+2 > w {
		return cell(left, w)
	}
	return cell(left, w-ansi.StringWidth(rightText)) + rightText
}
func (m *model) chip(label string, active bool) string {
	p := colors(m.light)
	fg, bg := p.dim, p.surface
	if active {
		fg, bg = p.focus, p.raised
	}
	return surface(" "+label+" ", ansi.StringWidth(label)+2, bg, fg)
}
func (m *model) panel(title, content string, w, h int, focused bool) string {
	if w < 4 || h < 4 {
		return fit(content, w, h)
	}
	p := colors(m.light)
	border := p.border
	if focused {
		border = p.focus
	}
	lines := []string{ink("╭"+strings.Repeat("─", w-2)+"╮", border)}
	titleText := strong(title, p.text)
	if focused && w > 30 {
		titleText = pair(titleText, ink("•", p.focus), w-4)
	}
	inside := append([]string{cell(titleText, w-4)}, strings.Split(fit(content, w-4, h-3), "\n")...)
	for _, line := range inside {
		lines = append(lines, ink("│", border)+surface(" "+cell(line, w-4)+" ", w-2, p.surface, p.text)+ink("│", border))
	}
	lines = append(lines, ink("╰"+strings.Repeat("─", w-2)+"╯", border))
	return strings.Join(lines, "\n")
}
func modelName(id string) string {
	switch id {
	case "gpt-6.1-sol":
		return "Sol 6.1"
	case "gpt-6-sol":
		return "Sol 6"
	case "gpt-5.6-terra":
		return "Terra 5.6"
	case "gpt-6-luna":
		return "Luna 6"
	case "gpt-6-astra":
		return "Astra 6"
	}
	return Safe(id)
}
func modelIDs() []string {
	var out []string
	for _, v := range config.Models() {
		out = append(out, v.ID)
	}
	return out
}

const wordmark = `  ___ _      _   _   _ ___  _____  __
 / __| |    /_\ | | | |   \| __\ \/ /
| (__| |__ / _ \| |_| | |) | _| >  <
 \___|____/_/ \_\\___/|___/|___/_/\_\`

func (m *model) dimensions() (w, h int, rail bool) {
	rail = m.width >= 120 && m.height >= 36
	w = m.width - 4
	if rail {
		w -= 18
	}
	h = m.height - 9
	if m.editing || m.searching {
		h -= 2
	}
	return max(1, w), max(1, h), rail
}
func (m *model) contentOrigin() (x, y int) {
	_, _, rail := m.dimensions()
	x = 2
	if rail {
		x += 19
	}
	return x, 7
}
func (m *model) pageHead(w int) string {
	p := colors(m.light)
	subtitle := ""
	controls := ""
	switch m.tab {
	case 0:
		subtitle = "Traffic, token reuse, and response times"
		for _, n := range []int{15, 60, 120} {
			controls += m.chip(fmt.Sprintf("%dm", n), m.minutes == n) + " "
		}
	case 1:
		subtitle = fmt.Sprintf("Last %d completed requests · %s", len(m.snapshot.Recent), []string{"newest completions first", "longest durations first", "highest cache reuse first"}[m.sort])
		if m.filter != (metrics.Filter{}) || !m.minuteFilter.IsZero() {
			subtitle = fmt.Sprintf("%d matching · %s · %s · %s effort", len(m.records()), outcomeFilter(m.filter.Status), modelName(orAll(m.filter.Model)), orAll(m.filter.Effort))
		}
		controls = m.chip("x Clear filters", m.filter != (metrics.Filter{}) || !m.minuteFilter.IsZero())
	case 2:
		subtitle = "Usage since gateway started · response times within selected window"
		controls = m.chip(fmt.Sprintf("w Last %dm", m.minutes), false)
	case 3:
		subtitle = "Review your changes, then save them together"
		label := "✓ Saved"
		if m.dirty {
			label = "● Unsaved changes"
		}
		if m.saving {
			label = "Saving…"
		}
		if !m.settings.Writable && m.settings.Revision != "" {
			label = "Read only"
		}
		controls = m.chip(label, m.dirty)
	}
	return fit(pair(strong(tabs[m.tab], p.text), controls, w)+"\n"+ink(subtitle, p.dim)+"\n", w, 3)
}
func (m *model) chrome() (string, string) {
	p := colors(m.light)
	connection := "● Connected"
	connectionColor := p.ok
	if m.snapshot.Generated.IsZero() {
		connection = "Connecting…"
		connectionColor = p.dim
	}
	if m.paused {
		connection = "Ⅱ Updates paused"
		connectionColor = p.amber
	}
	if m.stale != "" {
		connection = "Connection lost · retrying"
		connectionColor = p.bad
	}
	brand := strong("[c/x] CLAUDEX", p.focus)
	if m.width >= 100 {
		brand += ink(" / request monitor", p.dim)
	}
	status := ink(connection, connectionColor)
	if m.width >= 80 {
		status += ink(fmt.Sprintf("   %d running · %d clients", m.snapshot.Active, m.status.Clients), p.dim)
	}
	top := surface(" "+pair(brand, status, max(1, m.width-2))+" ", m.width, p.surface, p.text)
	nav := ""
	_, _, rail := m.dimensions()
	if !rail {
		for i, n := range tabs {
			if m.width < 55 {
				n = []string{"Home", "Requests", "Models", "Settings"}[i]
			}
			if m.width < 55 {
				label := fmt.Sprintf("%d %s", i+1, n)
				if i == m.tab {
					nav += strong(label, p.focus)
				} else {
					nav += ink(label, p.dim)
				}
				nav += " "
			} else {
				nav += m.chip(fmt.Sprintf("%d %s", i+1, n), m.tab == i) + " "
			}
		}
	}
	header := top + "\n" + ink(strings.Repeat("─", max(1, m.width)), p.border) + "\n" + surface(nav, m.width, p.surface, p.dim)
	help := "1–4 views   Tab focus   Ctrl+k commands   ? help   q quit"
	switch m.tab {
	case 0:
		help = "Tab focus   z expand   ←→ minute   Enter inspect   ? help"
	case 1:
		help = "↑↓ select   Enter inspect   / find   f outcome   ? help"
	case 2:
		help = "↑↓ model   Tab focus   w time range   ? help"
	case 3:
		help = "↑↓ field   ←→ choose   Enter edit   Ctrl+s save   ? help"
	}
	if m.width < 60 {
		help = "1–4 views · Tab focus · ? help · q quit"
		if m.tab == 1 {
			help = "↑↓ select · Enter inspect · / find"
		}
		if m.tab == 3 {
			help = "↑↓ field · Enter edit · s save · ? help"
		}
	}
	updated := ""
	if !m.snapshot.Generated.IsZero() {
		updated = "updated " + m.snapshot.Generated.Local().Format("15:04:05")
	}
	if m.paused {
		updated = "updates paused"
	}
	hint := surface(" "+pair(ink(help, p.dim), ink(updated, p.dim), max(1, m.width-2))+" ", m.width, p.surface, p.text)
	rule := ink(strings.Repeat("─", max(1, m.width)), p.border)
	note := m.notice
	if note == "" && m.stale != "" {
		note = "Last snapshot retained. " + m.stale
	}
	if note != "" {
		rule = surface(" ▎ "+Safe(note), m.width, p.raised, p.amber)
	}
	return fit(header, m.width, 3), fit(rule+"\n"+hint, m.width, 2)
}
func (m *model) navigation(h int) string {
	p := colors(m.light)
	lines := []string{"", ink(" WORKSPACE", p.dim), ""}
	for i, n := range tabs {
		label := fmt.Sprintf(" %d %s", i+1, n)
		if i == m.tab {
			label = surface("▎"+label, 16, p.raised, p.focus)
		} else {
			label = ink(" "+label, p.dim)
		}
		lines = append(lines, label, "")
	}
	bottom := []string{ink(" Recent history", p.dim), ink(" 500 requests", p.dim), ink(" 2h of charts", p.dim), "", ink(" Ctrl+k commands", p.dim), ink(" ? help", p.dim), ""}
	for len(lines) < h-len(bottom) {
		lines = append(lines, "")
	}
	lines = append(lines, bottom...)
	result := strings.Split(fit(strings.Join(lines, "\n"), 17, h), "\n")
	for i, line := range result {
		result[i] = surface(cell(line, 17), 17, p.surface, p.dim) + ink("│", p.border)
	}
	return strings.Join(result, "\n")
}
