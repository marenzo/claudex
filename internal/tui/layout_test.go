package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/marenzo/claudex/internal/metrics"
)

func fixtureModel() *model {
	m := newModel(context.Background(), &control.Client{URL: "http://127.0.0.1:8317"}, false)
	now := time.Date(2026, 10, 22, 14, 40, 30, 0, time.UTC)
	m.status = control.Status{Instance: "fixture", Clients: 3}
	m.settings = control.Settings{Revision: "fixture", Writable: true, Preferences: control.PreferencesFor(config.DefaultsFor("/fixture")), Listen: "127.0.0.1:8317", AuthFile: "~/.config/claudex/auth.json", ClientKeyFile: "~/.config/claudex/client-key"}
	m.resetDraft()
	m.snapshot = metrics.Snapshot{Generated: now, Started: now.Add(-time.Hour * 3), Active: 4, UptimeSeconds: 10800, Models: map[string]metrics.Totals{}}
	ids := modelIDs()
	for i := range 240 {
		d := int64(2200 + (i*7919)%150000)
		f := int64(400 + i*51%12000)
		status := 200
		if i%19 == 0 {
			status = 429
		}
		if i%31 == 0 {
			status = 499
		}
		r := metrics.Request{ID: int64(240 - i), Started: now.Add(-time.Duration(i)*time.Second*18 - time.Duration(d)*time.Millisecond), Completed: now.Add(-time.Duration(i) * time.Second * 18), Model: ids[i%len(ids)], Effort: "high", Status: status, DurationMS: d, FirstOutputMS: &f, UsageReported: status == 200, Usage: metrics.Usage{Input: int64(12000 + i*813), Cached: int64(9000 + i*600), Output: int64(700 + i*73), Reasoning: int64(200 + i*31)}}
		if !r.UsageReported {
			r.Usage = metrics.Usage{}
			r.FirstOutputMS = nil
		}
		if status == 429 {
			r.Error = &metrics.ErrorDetails{Source: "upstream", Type: "rate_limit_error", Status: 429, Message: "The upstream service is temporarily at capacity. Try again shortly."}
		}
		m.snapshot.Recent = append(m.snapshot.Recent, r)
	}
	m.snapshot.Totals = totalsFor(m.snapshot.Recent)
	for _, id := range ids {
		var rows []metrics.Request
		for _, r := range m.snapshot.Recent {
			if r.Model == id {
				rows = append(rows, r)
			}
		}
		m.snapshot.Models[id] = totalsFor(rows)
	}
	for i := 119; i >= 0; i-- {
		at := now.Truncate(time.Minute).Add(-time.Duration(i) * time.Minute)
		var rows []metrics.Request
		for _, r := range m.snapshot.Recent {
			if r.Completed.Truncate(time.Minute).Equal(at) {
				rows = append(rows, r)
			}
		}
		m.snapshot.History = append(m.snapshot.History, metrics.Bucket{Time: at, Totals: totalsFor(rows)})
	}
	m.selected = 239
	return m
}
func TestResponsiveScreensAndModalsFit(t *testing.T) {
	m := fixtureModel()
	for _, w := range []int{30, 40, 60, 80, 100, 120, 160, 220} {
		for _, h := range []int{12, 16, 24, 36, 48, 64} {
			for _, light := range []bool{false, true} {
				for tab := range 4 {
					m.width, m.height, m.light, m.tab = w, h, light, tab
					m.renderContent()
					for _, modal := range []string{"", "commands", "help", "quit"} {
						m.modal = modal
						v := m.View().Content
						lines := strings.Split(v, "\n")
						if len(lines) != h {
							t.Fatalf("%dx%d tab%d modal%s: %d lines", w, h, tab, modal, len(lines))
						}
						for _, line := range lines {
							if ansi.StringWidth(line) != w {
								t.Fatalf("%dx%d tab%d modal%s: width %d", w, h, tab, modal, ansi.StringWidth(line))
							}
						}
					}
					m.modal = ""
					if w >= 60 && h >= 24 {
						bw, bh, _ := m.dimensions()
						for _, line := range strings.Split(m.body, "\n") {
							if ansi.StringWidth(line) > bw {
								t.Fatalf("body overflow %dx%d tab%d: %q", w, h, tab, ansi.Strip(line))
							}
						}
						if len(strings.Split(m.body, "\n")) > bh {
							t.Fatalf("body height %dx%d tab%d", w, h, tab)
						}
					}
				}
			}
		}
	}
}
func TestRequestUsageAndOutcomeSemantics(t *testing.T) {
	m := fixtureModel()
	m.width = 160
	m.height = 48
	m.tab = 1
	m.renderContent()
	unknown := m.snapshot.Recent[0]
	detail := ansi.Strip(m.requestDetail(unknown, 60))
	if !strings.Contains(detail, "Token counts unavailable") || strings.Contains(detail, "Input breakdown") {
		t.Fatal("unknown usage invented", detail)
	}
	r := m.snapshot.Recent[1]
	detail = ansi.Strip(m.requestDetail(r, 60))
	if !strings.Contains(detail, number(r.Usage.Input-r.Usage.Cached)) || !strings.Contains(detail, number(r.Usage.Output-r.Usage.Reasoning)) {
		t.Fatal("wrong token subsets", detail)
	}
	m.filter.Status = "errors"
	for _, r := range m.records() {
		if r.Status == 499 || r.Status < 400 {
			t.Fatal("failure filter includes other outcomes")
		}
	}
	m.filter = metrics.Filter{Search: "no matching records"}
	m.renderContent()
	if !strings.Contains(ansi.Strip(m.body), "No matching requests") {
		t.Fatal("missing empty state")
	}
}
func TestSelectionSurvivesPollingSortingAndEviction(t *testing.T) {
	m := fixtureModel()
	m.tab = 1
	m.width = 160
	m.height = 48
	m.renderContent()
	selected := m.selected
	m.Update(pollResult{status: m.status, settings: m.settings, snapshot: m.snapshot})
	if m.selected != selected {
		t.Fatal("selection changed on refresh")
	}
	m.Update(key("o"))
	if m.selected != selected {
		t.Fatal("selection changed on sort")
	}
	for i, r := range m.snapshot.Recent {
		if r.ID == selected {
			m.snapshot.Recent = append(m.snapshot.Recent[:i], m.snapshot.Recent[i+1:]...)
			break
		}
	}
	m.renderContent()
	if m.selected == selected || m.selected == 0 {
		t.Fatal("evicted selection not reconciled")
	}
}
func TestInspectorFocusScrollAndCompactBack(t *testing.T) {
	m := fixtureModel()
	m.tab = 1
	m.width = 80
	m.height = 24
	m.renderContent()
	m.Update(key("enter"))
	if !m.expanded || m.focus != 1 {
		t.Fatal("detail not focused")
	}
	m.scroll(1000)
	m.renderContent()
	if m.view.YOffset() == 0 {
		t.Fatal("details cannot scroll")
	}
	m.scroll(-1000)
	if m.view.YOffset() != 0 {
		t.Fatal("cannot scroll up from bottom")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.expanded || m.focus != 0 {
		t.Fatal("back did not restore list")
	}
}
func TestCommandsAndDraftProtection(t *testing.T) {
	m := fixtureModel()
	m.field = 0
	m.setField("sol")
	if !m.dirty {
		t.Fatal("fixture should be dirty")
	}
	m.Update(key("q"))
	if m.modal != "quit" {
		t.Fatal("missing unsaved prompt")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if !m.dirty || m.modal != "" {
		t.Fatal("draft lost on cancel")
	}
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.modal != "commands" {
		t.Fatal("palette not open")
	}
	m.input.SetValue("requests")
	m.Update(key("enter"))
	if m.tab != 1 || !m.dirty {
		t.Fatal("command navigation failed")
	}
	m.Update(key("/"))
	m.Update(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if m.modal != "commands" || m.searching {
		t.Fatal("global palette unavailable while typing")
	}
}
func TestMouseOutcomeAndRequestSelection(t *testing.T) {
	m := fixtureModel()
	m.width = 160
	m.height = 48
	m.tab = 1
	m.renderContent()
	for _, action := range []string{"outcome", "request"} {
		var target hit
		for _, h := range m.hits {
			if h.action == action {
				target = h
				break
			}
		}
		m.click(tea.MouseClickMsg{X: target.x + 21, Y: target.y + 7, Button: tea.MouseLeft})
		if action == "outcome" && m.filter.Status != "ok" {
			t.Fatal("outcome click failed")
		}
		if action == "request" && m.selected != target.id {
			t.Fatal("row click failed")
		}
	}
}
func TestRenderFixtures(t *testing.T) {
	dir := os.Getenv("CLAUDEX_TUI_RENDER_DIR")
	if dir == "" {
		t.Skip("set CLAUDEX_TUI_RENDER_DIR to inspect rendered terminal cells")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	m := fixtureModel()
	m.status.Product = "claudex"
	m.status.API = control.APIVersion
	for name, value := range map[string]any{"status": m.status, "config": m.settings, "metrics": m.snapshot} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, size := range [][2]int{{160, 48}, {120, 36}, {80, 24}, {40, 16}} {
		for tab := range 4 {
			m := fixtureModel()
			m.width, m.height, m.tab = size[0], size[1], tab
			m.renderContent()
			name := fmt.Sprintf("%dx%d-%s.ansi", size[0], size[1], tabs[tab])
			if err := os.WriteFile(filepath.Join(dir, name), []byte(m.View().Content), 0600); err != nil {
				t.Fatal(err)
			}
			if size[0] == 160 {
				m.light = true
				m.renderContent()
				if err := os.WriteFile(filepath.Join(dir, strings.TrimSuffix(name, ".ansi")+"-light.ansi"), []byte(m.View().Content), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
