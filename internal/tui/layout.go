package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marenzo/claudex/internal/metrics"
)

func selectedStyle(light bool) lipgloss.Style {
	p := colors(light)
	return lipgloss.NewStyle().Background(lipgloss.Color(p.raised)).Foreground(lipgloss.Color(p.focus)).Bold(true)
}
func wrap(s string, w int) string {
	return ansi.Wrap(strings.ReplaceAll(s, "\t", "    "), max(1, w), "")
}
func (m *model) renderContent() {
	m.hits = nil
	switch m.tab {
	case 0:
		m.body = m.overview()
	case 1:
		m.body = m.requests()
	case 2:
		m.body = m.models()
	case 3:
		m.body = m.preferences()
	}
}
func (m *model) View() tea.View {
	w, h, rail := m.dimensions()
	header, footer := m.chrome()
	body := fit(m.pageHead(w), w, 3) + "\n" + fit(m.body, w, h)
	if rail {
		body = beside(m.navigation(h+3), body, 18, w, h+3)
	}
	content := strings.Split(body, "\n")
	for i, line := range content {
		content[i] = "  " + line
	}
	text := header + "\n\n" + strings.Join(content, "\n")
	if m.editing || m.searching {
		label := "Value"
		if m.searching {
			label = "Search requests"
		}
		text += "\n" + ink(label+" · Enter apply · Esc cancel", colors(m.light).dim) + "\n" + m.input.View()
	}
	text += "\n" + footer
	text = fit(text, max(1, m.width), max(1, m.height))
	if m.modal != "" {
		text = m.overlay(text)
	}
	p := colors(m.light)
	view := tea.NewView(text)
	view.AltScreen = true
	view.MouseMode = tea.MouseModeCellMotion
	view.BackgroundColor = lipgloss.Color(p.bg)
	view.ForegroundColor = lipgloss.Color(p.text)
	return view
}
func (m *model) changeTab(tab int) {
	m.tab = tab
	m.focus = 0
	m.expanded = false
	m.view.GotoTop()
	m.renderContent()
}
func (m *model) scroll(n int) {
	v := &m.view
	w, _, _ := m.dimensions()
	if m.tab == 3 && m.focus == 1 && w >= 100 {
		v = &m.side
	}
	if n < 0 {
		v.ScrollUp(-n)
	} else {
		v.ScrollDown(n)
	}
}
func (m *model) togglePause() tea.Cmd {
	m.paused = !m.paused
	m.generation++
	if m.pollCancel != nil {
		m.pollCancel()
	}
	m.renderContent()
	if !m.paused {
		return m.poll()
	}
	return nil
}
func (m *model) saveDraft() tea.Cmd {
	if m.tab != 3 || m.saving || !m.dirty || !m.settings.Writable {
		return nil
	}
	if m.draftInstance != m.status.Instance {
		m.notice = "Gateway restarted; discard the draft and reload before saving."
		m.renderContent()
		return nil
	}
	m.saving = true
	m.renderContent()
	p, rev := m.draft, m.draftRevision
	return func() tea.Msg { s, err := m.c.Save(m.ctx, p, rev); return saved{s, err} }
}

type hit struct {
	x, y, w, h int
	action     string
	id         int64
}

func (m *model) click(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Mouse().Button != tea.MouseLeft || m.modal != "" || m.editing || m.searching {
		return nil
	}
	x, y := msg.Mouse().X, msg.Mouse().Y
	_, _, rail := m.dimensions()
	if rail && x >= 2 && x < 20 && y >= 7 && y <= 14 {
		m.changeTab((y - 7) / 2)
		return nil
	}
	if !rail && y == 2 {
		position := 0
		for i, name := range tabs {
			if m.width < 55 {
				name = []string{"Home", "Requests", "Models", "Settings"}[i]
			}
			length := len(name) + 4
			if m.width < 55 {
				length = len(name) + 2
			}
			if x >= position && x < position+length {
				m.changeTab(i)
				return nil
			}
			position += length + 1
		}
	}
	ox, oy := m.contentOrigin()
	if y == 4 {
		w, _, _ := m.dimensions()
		if m.tab == 1 && x >= ox+w-17 {
			_, cmd := m.Update(keyPress('x'))
			return cmd
		}
		if m.tab == 0 {
			start := ox + w - 24
			if x >= start {
				i := min(2, max(0, (x-start)/8))
				m.minutes = []int{15, 60, 120}[i]
				m.minute = 0
				m.renderContent()
				return nil
			}
		}
	}
	x -= ox
	y -= oy
	for _, h := range m.hits {
		if x >= h.x && x < h.x+h.w && y >= h.y && y < h.y+h.h {
			switch h.action {
			case "key":
				_, cmd := m.Update(keyPress(rune(h.id)))
				return cmd
			case "request":
				m.selected = h.id
				m.focus = 0
				m.view.GotoTop()
			case "outcome":
				value := []string{"ok", "errors", "canceled"}[h.id]
				if m.filter.Status == value {
					value = ""
				}
				m.filter.Status = value
				m.selected = 0
			}
			m.renderContent()
			return nil
		}
	}
	w, _, _ := m.dimensions()
	if (m.tab == 1 || m.tab == 2 || m.tab == 3) && w >= 100 {
		boundary := w / 2
		if m.tab == 1 {
			boundary = (w-2)*17/27 + 2
		}
		if x >= boundary {
			m.focus = 1
		} else {
			m.focus = 0
		}
	}
	m.renderContent()
	return nil
}

func keyPress(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Text: string(code)} }

type command struct{ label, key string }

func (m *model) commands() []command {
	pause := "Pause updates"
	if m.paused {
		pause = "Resume updates"
	}
	all := []command{{"Overview", "1"}, {"Requests", "2"}, {"Models", "3"}, {"Settings", "4"}, {pause, "p"}, {"Change chart time range", "w"}, {"Search requests", "search"}, {"Clear request filters", "clear"}, {"Save settings", "save"}, {"Discard settings draft", "discard"}, {"Keyboard shortcuts", "?"}, {"Quit", "q"}}
	query := strings.ToLower(m.input.Value())
	var out []command
	for _, c := range all {
		if strings.Contains(strings.ToLower(c.label), query) {
			out = append(out, c)
		}
	}
	return out
}
func (m *model) openCommands() {
	m.editing = false
	m.searching = false
	m.modal = "commands"
	m.commandIndex = 0
	m.input.SetValue("")
	m.input.SetWidth(max(1, min(m.width-10, 55)))
	m.renderContent()
}
func (m *model) modalKey(key string, msg tea.Msg) tea.Cmd {
	if key == "ctrl+c" && m.modal != "quit" {
		m.modal = ""
		m.input.Blur()
		_, cmd := m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
		return cmd
	}
	if key == "esc" {
		m.modal = ""
		m.confirmQuit = false
		m.input.Blur()
		m.renderContent()
		return nil
	}
	if m.modal == "quit" {
		if key == "q" || key == "enter" || key == "ctrl+c" {
			if m.pollCancel != nil {
				m.pollCancel()
			}
			return tea.Quit
		}
		return nil
	}
	if m.modal == "help" {
		if key == "?" || key == "enter" {
			m.modal = ""
		}
		m.renderContent()
		return nil
	}
	choices := m.commands()
	switch key {
	case "up":
		m.commandIndex = max(0, m.commandIndex-1)
	case "down":
		m.commandIndex = min(max(0, len(choices)-1), m.commandIndex+1)
	case "enter":
		if len(choices) == 0 {
			return nil
		}
		action := choices[min(m.commandIndex, len(choices)-1)].key
		m.modal = ""
		m.input.Blur()
		switch action {
		case "search":
			m.changeTab(1)
			m.searching = true
			m.input.SetValue(m.filter.Search)
			m.size()
			m.renderContent()
			return m.input.Focus()
		case "clear":
			m.filter = metrics.Filter{}
			m.minuteFilter = time.Time{}
			m.selected = 0
			m.changeTab(1)
		case "save":
			m.changeTab(3)
			return m.saveDraft()
		case "discard":
			m.changeTab(3)
			if !m.saving {
				m.resetDraft()
			}
		default:
			_, cmd := m.Update(tea.KeyPressMsg{Code: rune(action[0]), Text: action})
			return cmd
		}
	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		m.commandIndex = 0
		return cmd
	}
	m.renderContent()
	return nil
}
func (m *model) overlay(base string) string {
	p := colors(m.light)
	w := max(4, min(m.width-4, 68))
	h := max(3, min(m.height-4, 20))
	title := "Keyboard shortcuts"
	content := "1–4       Overview / Requests / Models / Settings\nTab       Focus next pane or chart\n↑↓ / j k  Select a request, model or settings field\nPgUp/PgDn Scroll details and settings\nEnter     Open request / edit setting / minute requests\nEsc       Back or close\n\n/         Search requests\nm / e / f Filter model / effort / outcome\no / x     Sort / clear request filters\nw / z     Chart time range / expand focused chart\np         Pause or resume updates\nCtrl+s    Save settings     d  Discard settings\nCtrl+k    Search commands\nq         Quit\n\nPress Esc to close"
	if m.modal == "quit" {
		title = "Unsaved settings"
		h = min(h, 8)
		content = "Your changes have not been saved.\n\nEnter or q  Discard changes and quit\nEsc         Keep editing"
	}
	if m.modal == "commands" {
		title = "Commands"
		content = m.input.View() + "\n\n"
		choices := m.commands()
		count := max(1, h-6)
		start := max(0, m.commandIndex-count+1)
		for i := start; i < min(len(choices), start+count); i++ {
			line := "  " + choices[i].label
			if i == m.commandIndex {
				line = selectedStyle(m.light).Render("▸ " + choices[i].label)
			}
			content += line + "\n"
		}
		if len(choices) == 0 {
			content += "No matching commands.\n"
		}
		content += "\n" + ink("↑↓ choose · Enter run · Esc close", p.dim)
	}
	pane := m.panel(title, wrap(content, w-4), w, h, true)
	px := (m.width - w) / 2
	py := (m.height - h) / 2
	lines := strings.Split(base, "\n")
	overlay := strings.Split(pane, "\n")
	for i, line := range overlay {
		if py+i >= 0 && py+i < len(lines) {
			old := lines[py+i]
			lines[py+i] = ansi.Cut(old, 0, max(0, px)) + line + ansi.Cut(old, max(0, px+w), m.width)
		}
	}
	return fit(strings.Join(lines, "\n"), max(1, m.width), max(1, m.height))
}
