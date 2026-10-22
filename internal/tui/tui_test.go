package tui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"github.com/charmbracelet/x/ansi"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/marenzo/claudex/internal/metrics"
	"strings"
	"testing"
	"time"
)

func TestSafeBlocksTerminalControlAndPreservesText(t *testing.T) {
	got := Safe("hello\x1b]52;c;secret\a\x1b[2Jworld\x00\u009b\u202e\nnext")
	if got != "helloworld\nnext" {
		t.Fatalf("unsafe rendering %q", got)
	}
}
func key(s string) tea.KeyPressMsg {
	if s == "enter" {
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}
func TestPollingPreservesDraftAndRejectsSupersededResults(t *testing.T) {
	m := newModel(context.Background(), &control.Client{URL: "http://localhost"}, true)
	cfg := config.DefaultsFor("/fixture")
	prefs := control.PreferencesFor(cfg)
	first := pollResult{status: control.Status{Instance: "one"}, settings: control.Settings{Revision: "a", Preferences: prefs}, snapshot: metrics.Snapshot{Generated: time.Now()}}
	m.Update(first)
	m.field = 0
	m.setField("sol")
	draft := m.draft
	m.Update(pollResult{status: control.Status{Instance: "two"}, settings: control.Settings{Revision: "b", Preferences: prefs}, snapshot: first.snapshot})
	if m.draft != draft || m.draftInstance != "one" || !m.dirty {
		t.Fatal("draft overwritten on restart")
	}
	m.generation++
	m.Update(pollResult{generation: 0, status: control.Status{Instance: "stale"}})
	if m.status.Instance != "two" {
		t.Fatal("superseded poll applied")
	}
	m.Update(key("d"))
	if m.dirty || m.draftInstance != "two" {
		t.Fatal("discard failed")
	}
}
func TestAllViewsFitNarrowTerminalAndRetainUnknownUsage(t *testing.T) {
	m := newModel(context.Background(), &control.Client{URL: "http://localhost"}, false)
	m.snapshot = metrics.Snapshot{Generated: time.Now(), Totals: metrics.Totals{Requests: 1}, Recent: []metrics.Request{{ID: 1, Model: "gpt-6-astra", Status: 499}}}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 16})
	for tab := range 4 {
		m.tab = tab
		m.renderContent()
		text := m.View().Content
		for _, line := range strings.Split(text, "\n") {
			if ansi.StringWidth(line) > 40 {
				t.Fatal("overflow", tab, line)
			}
		}
	}
	if !strings.Contains(m.overview(), "Tokens —") {
		t.Fatal("unknown tokens displayed as zero")
	}
}
