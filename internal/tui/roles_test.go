package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marenzo/claudex/internal/metrics"
)

func TestCallTypeSelectionSeparatesSameModelAndPersistsAcrossViews(t *testing.T) {
	m := fixtureModel()
	store := metrics.New()
	for _, role := range []metrics.Role{metrics.Generation, metrics.Review} {
		store.Begin(role)
		r := metrics.Request{Role: role, TraceID: string(role), Model: "gpt-6.1-sol", Status: 200, UsageReported: true, Usage: metrics.Usage{Input: 100}}
		if role == metrics.Generation {
			r.Usage.Cached = 90
		} else {
			r.ToolCalls, r.Flagged, r.ParentTraceID = 1, 1, "generation"
		}
		store.Finish(r)
	}
	m.snapshot = store.Snapshot()
	m.tab, m.width, m.height = 1, 160, 50
	m.renderContent()
	if rows := m.records(); len(rows) != 1 || rows[0].Role != metrics.Generation || m.scopedSnapshot().Totals.Tokens.Cached != 90 {
		t.Fatal("generation not the initial scope")
	}
	m.Update(keyPress('g'))
	if rows := m.records(); len(rows) != 1 || rows[0].Role != metrics.Review || m.scopedSnapshot().Totals.Tokens.Cached != 0 {
		t.Fatal("review scope combines generation cache")
	}
	if !strings.Contains(ansi.Strip(m.requestDetail(m.records()[0], 70)), "1 blocked") {
		t.Fatal("a blocking verdict looks like an ordinary generation success")
	}
	m.changeTab(2)
	if m.role != metrics.Review {
		t.Fatal("scope lost across navigation")
	}
	m.View()
	ox, _ := m.contentOrigin()
	m.click(tea.MouseClickMsg{X: ox + m.roleChipX + 1, Y: 4, Button: tea.MouseLeft})
	if m.role != "" || len(m.records()) != 2 || m.scopedSnapshot().Totals.Requests != 2 {
		t.Fatal("scope chip does not select all calls")
	}
}
