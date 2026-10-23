// Package tui is the terminal dashboard included in every gateway binary.
package tui

import (
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"fmt"
	"github.com/charmbracelet/x/ansi"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/marenzo/claudex/internal/metrics"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var tabs = []string{"Overview", "Requests", "Models", "Settings"}

// Safe removes terminal escapes, C0/C1 controls and bidi formatting from data.
// Only application-owned styles may introduce terminal control sequences.
func Safe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' {
			return r
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, ansi.Strip(s))
}

type pollResult struct {
	generation int
	status     control.Status
	settings   control.Settings
	snapshot   metrics.Snapshot
	err        error
}
type pollTick int
type saved struct {
	settings control.Settings
	err      error
}
type model struct {
	ctx                                                    context.Context
	c                                                      *control.Client
	width, height, tab, minutes, field                     int
	view                                                   viewport.Model
	side                                                   viewport.Model
	input                                                  textinput.Model
	status                                                 control.Status
	settings                                               control.Settings
	snapshot                                               metrics.Snapshot
	draft                                                  control.Preferences
	draftRevision                                          string
	draftInstance                                          string
	dirty, editing, searching, paused, saving, confirmQuit bool
	stale, notice                                          string
	filter                                                 metrics.Filter
	role                                                   metrics.Role
	selected                                               int64
	expanded                                               bool
	generation                                             int
	pollCancel                                             context.CancelFunc
	light, zoom, revealField                               bool
	focus, sort, modelIndex, minute                        int
	minuteFilter                                           time.Time
	body                                                   string
	hits                                                   []hit
	roleChipX, roleChipW                                   int
	modal                                                  string
	commandIndex                                           int
}

func newModel(ctx context.Context, c *control.Client, settings bool) *model {
	m := &model{ctx: ctx, c: c, width: 80, height: 24, minutes: 60, role: metrics.Generation, view: viewport.New(), side: viewport.New(), input: textinput.New()}
	if settings {
		m.tab = 3
	}
	m.input.CharLimit = 256
	m.size()
	m.renderContent()
	return m
}
func Run(ctx context.Context, c *control.Client, settings bool) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if _, err := c.Status(ctx); err != nil {
		return fmt.Errorf("gateway unavailable: %w; start claudex or claudex ctl run", err)
	}
	// Ownership is independent of refresh/pause. Reconnect after gateway restarts.
	go func() {
		for {
			done, err := c.Attach(ctx, "tui")
			if err == nil {
				select {
				case <-ctx.Done():
					return
				case <-done:
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	m := newModel(ctx, c, settings)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	if ctx.Err() != nil && errors.Is(err, tea.ErrProgramKilled) {
		return nil
	}
	return err
}
func (m *model) size() {
	w, h, _ := m.dimensions()
	m.view.SetWidth(w)
	m.view.SetHeight(h)
	m.input.SetWidth(max(1, m.width-8))
}
func (m *model) Init() tea.Cmd { return tea.Batch(m.poll(), tea.RequestBackgroundColor) }
func (m *model) poll() tea.Cmd {
	ctx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
	m.pollCancel = cancel
	generation := m.generation
	return func() tea.Msg {
		defer cancel()
		r := pollResult{generation: generation}
		r.status, r.err = m.c.Status(ctx)
		if r.err == nil {
			r.snapshot, r.err = m.c.Metrics(ctx)
		}
		if r.err == nil {
			r.settings, r.err = m.c.Settings(ctx)
		}
		return r
	}
}
func (m *model) resetDraft() {
	m.draft = m.settings.Preferences
	m.draftRevision = m.settings.Revision
	m.draftInstance = m.status.Instance
	m.dirty = false
	m.notice = ""
}
func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.BackgroundColorMsg:
		m.light = !v.IsDark()
	case tea.MouseClickMsg:
		return m, m.click(v)
	case tea.MouseWheelMsg:
		if m.modal != "" || m.editing || m.searching {
			return m, nil
		}
		direction := 1
		if v.Mouse().Button == tea.MouseWheelUp {
			direction = -1
		}
		if m.tab == 1 && m.focus == 0 {
			m.moveRequest(direction * 3)
		} else {
			m.scroll(direction * 3)
		}
		m.renderContent()
		return m, nil
	case tea.WindowSizeMsg:
		m.width = v.Width
		m.height = v.Height
		m.size()
	case pollTick:
		if int(v) == m.generation && !m.paused {
			return m, m.poll()
		}
		return m, nil
	case pollResult:
		if v.generation != m.generation {
			return m, nil
		}
		if v.err != nil {
			m.stale = Safe(v.err.Error())
		} else {
			if m.status.Instance != "" && m.status.Instance != v.status.Instance {
				m.selected = 0
				m.minuteFilter = time.Time{}
				m.expanded = false
				if m.dirty {
					m.notice = "Gateway restarted. Draft preserved; discard it to load current settings."
				}
			}
			m.status = v.status
			m.settings = v.settings
			m.snapshot = v.snapshot
			m.stale = ""
			if !m.dirty && !m.editing {
				m.resetDraft()
			}
		}
		m.renderContent()
		generation := m.generation
		return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg { return pollTick(generation) })
	case saved:
		m.saving = false
		if v.err != nil {
			m.notice = "Save failed: " + Safe(v.err.Error())
		} else {
			m.settings = v.settings
			m.resetDraft()
			m.notice = "Saved and applied. Open a new Claude session for startup defaults."
		}
		m.renderContent()
		return m, nil
	case tea.KeyPressMsg:
		key := v.String()
		if m.modal != "" {
			return m, m.modalKey(key, msg)
		}
		if key == "ctrl+k" {
			m.openCommands()
			return m, m.input.Focus()
		}
		if key == "ctrl+c" && (m.editing || m.searching) {
			m.editing = false
			m.searching = false
			m.input.Blur()
		}
		if m.editing || m.searching {
			switch key {
			case "esc":
				m.editing = false
				m.searching = false
				m.input.Blur()
			case "enter":
				if m.searching {
					m.filter.Search = m.input.Value()
					m.searching = false
					m.selected = 0
				} else {
					m.setField(m.input.Value())
					m.editing = false
				}
				m.input.Blur()
			default:
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg)
				return m, cmd
			}
			m.renderContent()
			return m, nil
		}
		switch key {
		case "q", "ctrl+c":
			if m.dirty && !m.confirmQuit {
				m.confirmQuit = true
				m.modal = "quit"
				m.renderContent()
				return m, nil
			}
			if m.pollCancel != nil {
				m.pollCancel()
			}
			return m, tea.Quit
		case "?":
			m.modal = "help"
		case ":":
			m.openCommands()
			return m, m.input.Focus()
		case "esc":
			m.confirmQuit = false
			m.notice = ""
			m.expanded = false
			m.zoom = false
			m.focus = 0
			m.view.GotoTop()
		case "tab", "shift+tab":
			count := 2
			if m.tab == 0 {
				count = 4
			}
			direction := 1
			if key == "shift+tab" {
				direction = -1
			}
			m.focus = (m.focus + count + direction) % count
			w, h, _ := m.dimensions()
			if m.tab == 1 && (w < 100 || h < 24) {
				m.expanded = m.focus == 1
				m.view.GotoTop()
			}

		case "1", "2", "3", "4":
			m.changeTab(int(key[0] - '1'))
		case "p":
			return m, m.togglePause()
		case "w":
			if m.minutes == 15 {
				m.minutes = 60
			} else if m.minutes == 60 {
				m.minutes = 120
			} else {
				m.minutes = 15
			}
			m.minute = 0
		case "g":
			if m.tab != 3 {
				m.role = metrics.Role(cycle([]string{string(metrics.Generation), string(metrics.Review), ""}, string(m.role), 1))
				m.selected = 0
				m.view.GotoTop()
			}
		case "z":
			if m.tab == 0 {
				m.zoom = !m.zoom
			}
		case "/":
			if m.tab == 1 {
				m.searching = true
				m.input.SetValue(m.filter.Search)
				m.renderContent()
				return m, m.input.Focus()
			}
		case "m":
			if m.tab == 1 {
				m.filter.Model = cycle(append([]string{""}, modelIDs()...), m.filter.Model, 1)
				m.selected = 0
			}
		case "e":
			if m.tab == 1 {
				m.filter.Effort = cycle([]string{"", "none", "low", "medium", "high", "xhigh", "max", "ultra"}, m.filter.Effort, 1)
				m.selected = 0
			}
		case "f":
			if m.tab == 1 {
				m.filter.Status = cycle([]string{"", "ok", "errors", "canceled", "429"}, m.filter.Status, 1)
				m.selected = 0
			}
		case "o":
			if m.tab == 1 {
				m.sort = (m.sort + 1) % 3
			}
		case "x":
			if m.tab == 1 {
				m.filter = metrics.Filter{}
				m.minuteFilter = time.Time{}
				m.selected = 0
			}
		case "s", "ctrl+s":
			return m, m.saveDraft()
		case "d":
			if m.tab == 3 && !m.saving {
				m.resetDraft()
			}
		case "up", "k", "down", "j":
			direction := 1
			if key == "up" || key == "k" {
				direction = -1
			}
			if m.tab == 3 && m.focus == 0 {
				m.field = (m.field + direction + 6) % 6
				m.revealField = true
			} else if m.tab == 1 && m.focus == 0 {
				m.moveRequest(direction)
			} else if m.tab == 2 && m.focus == 0 {
				m.modelIndex = (m.modelIndex + direction + len(modelIDs())) % len(modelIDs())
				m.view.GotoTop()
			} else {
				m.scroll(direction)
			}
		case "left", "h", "right", "l":
			direction := 1
			if key == "left" || key == "h" {
				direction = -1
			}
			if m.tab == 3 && m.focus == 0 && !m.saving && m.settings.Writable {
				m.cycleField(direction)
			} else if m.tab == 0 {
				m.minute = max(0, min(len(metrics.Window(m.scopedSnapshot(), m.minutes).History)-1, m.minute+direction))
			}
		case "enter":
			if m.tab == 1 {
				m.expanded = !m.expanded
				if m.expanded {
					m.focus = 1
				} else {
					m.focus = 0
				}
				m.view.GotoTop()
			} else if m.tab == 0 {
				history := metrics.Window(m.scopedSnapshot(), m.minutes).History
				if len(history) > 0 {
					m.minuteFilter = history[min(m.minute, len(history)-1)].Time
					m.changeTab(1)
				}
			} else if m.tab == 3 && m.focus == 0 && !m.saving && m.settings.Writable {
				m.editing = true
				m.input.SetValue(m.fieldValue())
				m.renderContent()
				return m, m.input.Focus()
			}
		case "pgdown", "pgup", "home", "end":
			var cmd tea.Cmd
			w, _, _ := m.dimensions()
			if m.tab == 3 && m.focus == 1 && w >= 100 {
				m.side, cmd = m.side.Update(msg)
			} else if m.tab == 1 && m.focus == 0 {
				direction := 1
				if key == "pgup" || key == "home" {
					direction = -1
				}
				step := max(1, m.height-20)
				if key == "home" || key == "end" {
					step = 500
				}
				m.moveRequest(direction * step)
			} else {
				m.view, cmd = m.view.Update(msg)
			}
			m.renderContent()
			return m, cmd
		}

	}
	m.renderContent()
	return m, nil
}
func cycle(values []string, current string, direction int) string {
	for i, v := range values {
		if v == current {
			return values[(i+direction+len(values))%len(values)]
		}
	}
	return values[0]
}
func (m *model) fieldValue() string {
	switch m.field {
	case 0:
		return m.draft.Model
	case 1:
		return m.draft.Effort
	case 2:
		return m.draft.Reviewer
	case 3:
		return strconv.Itoa(m.draft.ContextWindow)
	case 4:
		return strconv.Itoa(m.draft.CompactWindow)
	default:
		if m.draft.Feedback.Enabled {
			return "on"
		}
		return "off"
	}
}
func (m *model) setField(value string) {
	next := m.draft
	switch m.field {
	case 0:
		x, ok := config.FindModel(value)
		if !ok {
			m.notice = "Choose sol, terra, luna, astra, or sol6."
			return
		}
		next.Model = x.ID
	case 1:
		next.Effort = value
	case 2:
		if value == "" || value == "client" {
			next.Reviewer = ""
		} else {
			x, ok := config.FindModel(value)
			if !ok {
				m.notice = "Choose client, sol, terra, luna, astra, or sol6."
				return
			}
			next.Reviewer = x.ID
		}
	case 3:
		n, err := strconv.Atoi(value)
		if err != nil {
			m.notice = "Context window must be an integer."
			return
		}
		next.ContextWindow = n
	case 4:
		n, err := strconv.Atoi(value)
		if err != nil {
			m.notice = "Compaction window must be an integer."
			return
		}
		next.CompactWindow = n
	case 5:
		if value != "on" && value != "off" {
			m.notice = "Choose on or off."
			return
		}
		next.Feedback.Enabled = value == "on"
	}
	// Validate against dummy startup paths: these are not sent in settings writes.
	_, err := next.Apply(config.DefaultsFor("/settings"))
	if err != nil {
		m.notice = Safe(err.Error())
		return
	}
	m.draft = next
	m.dirty = next != m.settings.Preferences
	m.notice = ""
}
func (m *model) cycleField(direction int) {
	var values []string
	switch m.field {
	case 0, 2:
		if m.field == 2 {
			values = append(values, "")
		}
		for _, x := range config.Models() {
			values = append(values, x.ID)
		}
	case 1:
		values = []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	case 5:
		values = []string{"off", "on"}
	default:
		return
	}
	m.setField(cycle(values, m.fieldValue(), direction))
}
func (m *model) records() []metrics.Request {
	var out []metrics.Request
	for _, r := range m.scopedSnapshot().Recent {
		if m.filter.Match(r) && (m.minuteFilter.IsZero() || r.Completed.Truncate(time.Minute).Equal(m.minuteFilter)) {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if m.sort == 1 && out[i].DurationMS != out[j].DurationMS {
			return out[i].DurationMS > out[j].DurationMS
		}
		if m.sort == 2 {
			a, b := -1.0, -1.0
			if out[i].UsageReported && out[i].Usage.Input > 0 {
				a = float64(out[i].Usage.Cached) / float64(out[i].Usage.Input)
			}
			if out[j].UsageReported && out[j].Usage.Input > 0 {
				b = float64(out[j].Usage.Cached) / float64(out[j].Usage.Input)
			}
			if a != b {
				return a > b
			}
		}
		return out[i].ID > out[j].ID
	})
	return out
}
func (m *model) ensureSelection(rows []metrics.Request) {
	for _, r := range rows {
		if r.ID == m.selected {
			return
		}
	}
	m.selected = 0
	m.view.GotoTop()
	if len(rows) > 0 {
		m.selected = rows[0].ID
	}
}
func (m *model) moveRequest(direction int) {
	rows := m.records()
	m.ensureSelection(rows)
	if len(rows) == 0 {
		return
	}
	index := 0
	for i, r := range rows {
		if r.ID == m.selected {
			index = i
			break
		}
	}
	m.selected = rows[max(0, min(len(rows)-1, index+direction))].ID
	m.view.GotoTop()
}
