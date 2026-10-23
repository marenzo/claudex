// Package eval defines a fixed feedback selection/argument benchmark. Expected
// labels are not sent to the model. This does not grade arbitrary prose truth.
package eval

import (
	"crypto/sha256"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marenzo/claudex/internal/feedback"
)

//go:embed cases.json
var casesJSON []byte

type Case struct {
	ID           string   `json:"id"`
	Scenario     string   `json:"scenario"`
	Report       bool     `json:"report"`
	Disabled     bool     `json:"disabled,omitempty"`
	Category     string   `json:"category,omitempty"`
	Status       string   `json:"status,omitempty"`
	Reproduction string   `json:"reproduction,omitempty"`
	Modes        []string `json:"modes,omitempty"`
	Anchors      []string `json:"anchors,omitempty"`
	Forbidden    []string `json:"forbidden,omitempty"`
}

func Cases() ([]Case, error) {
	var cases []Case
	err := json.Unmarshal(casesJSON, &cases)
	return cases, err
}

const System = "You are a coding assistant finishing a task. The user message supplies the observed session events. Continue appropriately, using available tools only when warranted by their definitions. Do not execute any commands."

// Baseline isolates the added selection/attribution instructions, holding the
// tool name, schema and scenario fixed. It is NOT the native Anthropic tool.
const Baseline = "Record feedback about a product problem, a missing capability, or the model's own mistakes in this session. Saves the supplied observation to the local Claudex collector for later analysis. Nothing is sent to Anthropic."

type Call struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}
type Score struct {
	Selection      bool     `json:"selection"`
	Schema         bool     `json:"schema"`
	Classification bool     `json:"classification"`
	Status         bool     `json:"status"`
	Reproduction   bool     `json:"reproduction"`
	Evidence       bool     `json:"evidence"`
	Passed         bool     `json:"passed"`
	Errors         []string `json:"errors,omitempty"`
}

func Grade(c Case, calls []Call) Score {
	s := Score{Selection: len(calls) == 0 && !c.Report || len(calls) == 1 && c.Report}
	fail := func(v bool, message string) {
		if !v {
			s.Errors = append(s.Errors, message)
		}
	}
	fail(s.Selection, "wrong number of feedback calls")
	if !c.Report {
		s.Passed = s.Selection
		return s
	}
	if len(calls) != 1 {
		return s
	}
	s.Schema = calls[0].Name == feedback.ClaudeToolName
	r, err := feedback.Decode(calls[0].Arguments)
	s.Schema = s.Schema && err == nil
	fail(s.Schema, "wrong tool name or invalid schema")
	if !s.Schema {
		return s
	}
	mode := false
	for _, m := range c.Modes {
		mode = mode || r.FailureMode == m
	}
	s.Classification = r.Category == c.Category && mode
	s.Status = r.Status == c.Status
	s.Reproduction = r.Reproduction == c.Reproduction
	fail(s.Classification, "wrong category/failure mode")
	fail(s.Status, "wrong recovery status")
	fail(s.Reproduction, "unsupported or wrong reproduction status")
	// Anchors check preservation of supplied facts, not semantic entailment. A
	// human should inspect stored outputs before making broad grounding claims.
	text := strings.ToLower(strings.Join([]string{r.Title, r.Observed, r.Expected, r.Impact, strings.Join(r.Evidence, "\n")}, "\n"))
	s.Evidence = true
	for _, a := range c.Anchors {
		if !strings.Contains(text, strings.ToLower(a)) {
			s.Evidence = false
			s.Errors = append(s.Errors, fmt.Sprintf("missing evidence anchor %q", a))
		}
	}
	for _, a := range c.Forbidden {
		if strings.Contains(text, strings.ToLower(a)) {
			s.Evidence = false
			s.Errors = append(s.Errors, "forbidden unsupported/sensitive text")
		}
	}
	s.Passed = s.Selection && s.Schema && s.Classification && s.Status && s.Reproduction && s.Evidence
	return s
}

// Fingerprints capture the inputs and labels used by a run or offline regrade.
func Fingerprints() map[string]string {
	schema, _ := json.Marshal(feedback.Schema())
	values := map[string][]byte{"cases": casesJSON, "schema": schema, "system": []byte(System), "baseline": []byte(Baseline), "description": []byte(feedback.Description)}
	hashes := map[string]string{}
	for name, value := range values {
		hashes[name] = fmt.Sprintf("%x", sha256.Sum256(value))
	}
	return hashes
}
