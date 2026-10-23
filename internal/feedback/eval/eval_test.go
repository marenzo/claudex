package eval

import (
	"encoding/json"
	"github.com/marenzo/claudex/internal/feedback"
	"testing"
)

func TestDatasetAndGraderCatchFalsePasses(t *testing.T) {
	cases, err := Cases()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	positive, negative := 0, 0
	for _, c := range cases {
		if ids[c.ID] || c.ID == "" || c.Scenario == "" {
			t.Fatal("invalid fixture", c.ID)
		}
		ids[c.ID] = true
		if c.Report {
			positive++
			if len(c.Modes) == 0 || len(c.Anchors) == 0 {
				t.Fatal("unscorable fixture", c.ID)
			}
		} else {
			negative++
		}
	}
	if positive < 5 || negative < 5 {
		t.Fatal("missing positive/negative balance")
	}
	c := cases[0]
	if Grade(c, nil).Passed {
		t.Fatal("missing report passed")
	}
	r := feedback.Report{Category: c.Category, Status: c.Status, Title: "cargo-run-17", Observed: "946 MiB private target created", Expected: "Use shared target", Impact: "Wasted disk", Evidence: []string{"cargo-run-17"}, Reproduction: c.Reproduction, FailureMode: c.Modes[0]}
	call := func(r feedback.Report) Call { raw, _ := json.Marshal(r); return Call{feedback.ClaudeToolName, raw} }
	if !Grade(c, []Call{call(r)}).Passed {
		t.Fatal("valid report failed")
	}
	r.Reproduction = "verified"
	if Grade(c, []Call{call(r)}).Passed {
		t.Fatal("invented reproduction passed")
	}
	r.Reproduction = c.Reproduction
	r.Category = "product_defect"
	r.FailureMode = "none"
	if Grade(c, []Call{call(r)}).Passed {
		t.Fatal("product misattribution passed")
	}
	if Grade(cases[3], []Call{call(r)}).Passed {
		t.Fatal("unnecessary report passed")
	}
	if Grade(c, []Call{call(r), call(r)}).Passed {
		t.Fatal("duplicate report passed")
	}
}

func TestGraderDoesNotMistakeUnverifiedStatementForVerification(t *testing.T) {
	cases, _ := Cases()
	for _, c := range cases {
		if c.ID != "user_reported_repeats" {
			continue
		}
		r := feedback.Report{Category: c.Category, Status: c.Status, Title: "User reports repeated failure", Observed: "I have not independently verified this or its cause.", Expected: "Valid requests succeed", Impact: "User reports blocked task", Evidence: []string{"User cites user-log-8; I have not inspected it."}, Reproduction: "untested", FailureMode: "none"}
		raw, _ := json.Marshal(r)
		if score := Grade(c, []Call{{feedback.ClaudeToolName, raw}}); !score.Passed {
			t.Fatal(score)
		}
		r.Reproduction = "verified"
		raw, _ = json.Marshal(r)
		if Grade(c, []Call{{feedback.ClaudeToolName, raw}}).Passed {
			t.Fatal("unsupported verified reproduction passed")
		}
		return
	}
	t.Fatal("fixture missing")
}
