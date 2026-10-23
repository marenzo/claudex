//go:build live

package proxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/feedback"
	feedbackeval "github.com/marenzo/claudex/internal/feedback/eval"
	"github.com/tidwall/gjson"
)

type feedbackEvalResult struct {
	Model               string              `json:"model"`
	Arm                 string              `json:"arm"`
	CaseID              string              `json:"case_id"`
	Repeat              int                 `json:"repeat"`
	ResolvedModel       string              `json:"resolved_model,omitempty"`
	DurationMS          int64               `json:"duration_ms"`
	Calls               []feedbackeval.Call `json:"calls"`
	Answer              string              `json:"answer,omitempty"`
	Score               feedbackeval.Score  `json:"score"`
	InfrastructureError string              `json:"infrastructure_error,omitempty"`
}

// Synthetic cases only. Generated tool calls are graded, never executed. This
// measures selection and structured factual checks, not production incident rates.
func TestLiveFeedbackEvaluation(t *testing.T) {
	if os.Getenv("CLAUDEX_FEEDBACK_EVAL") != "1" {
		t.Skip("set CLAUDEX_FEEDBACK_EVAL=1 and CLAUDEX_TEST_AUTH_FILE")
	}
	s, credential := liveProtocolServer(t)
	cases, err := feedbackeval.Cases()
	if err != nil {
		t.Fatal(err)
	}
	models := strings.Split(os.Getenv("CLAUDEX_FEEDBACK_EVAL_MODELS"), ",")
	if models[0] == "" {
		models = []string{"gpt-6.1-sol", "gpt-6-astra"}
	}
	repeats := 1
	if value := os.Getenv("CLAUDEX_FEEDBACK_EVAL_REPEATS"); value != "" {
		repeats, err = strconv.Atoi(value)
		if err != nil || repeats < 1 || repeats > 10 {
			t.Fatal("repeats must be 1..10")
		}
	}
	arms := []string{"baseline", "clarified"}
	if os.Getenv("CLAUDEX_FEEDBACK_EVAL_ARM") == "clarified" {
		arms = []string{"clarified"}
	}
	slots := make(chan struct{}, 4)
	var wg sync.WaitGroup
	var mu sync.Mutex
	results := []feedbackEvalResult{}
	for _, model := range models {
		for _, arm := range arms {
			for _, c := range cases {
				for repeat := 1; repeat <= repeats; repeat++ {
					wg.Go(func() {
						slots <- struct{}{}
						defer func() { <-slots }()
						result := feedbackEvalResult{Model: model, Arm: arm, CaseID: c.ID, Repeat: repeat}
						started := time.Now()
						ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
						defer cancel()
						description := feedback.Description
						if arm == "baseline" {
							description = feedbackeval.Baseline
						}
						tools := []any{}
						if !c.Disabled {
							tools = append(tools, map[string]any{"name": feedback.ClaudeToolName, "description": description, "input_schema": feedback.Schema()})
						}
						req, _ := json.Marshal(map[string]any{"model": model, "system": feedbackeval.System, "messages": []any{map[string]any{"role": "user", "content": c.Scenario}}, "tools": tools, "tool_choice": map[string]any{"type": "auto"}, "output_config": map[string]any{"effort": "low"}, "thinking": map[string]any{"display": "omitted"}})
						body, session, err := prepareRequest(req, s.Config)
						if err == nil {
							response, _, e := s.Client.Responses(ctx, credential, body, session)
							err = e
							if err == nil {
								if response.StatusCode != 200 {
									err = fmt.Errorf("HTTP %d", response.StatusCode)
									io.Copy(io.Discard, io.LimitReader(response.Body, 8192))
									response.Body.Close()
								} else {
									events := make(chan streamEvent, 1)
									go readEvents(ctx, response.Body, events)
									completed := false
									for event := range events {
										if event.err != nil {
											err = fmt.Errorf("stream read failed")
											break
										}
										data := gjson.ParseBytes(event.data)
										switch data.Get("type").String() {
										case "response.output_item.done":
											item := data.Get("item")
											if item.Get("type").String() == "function_call" {
												result.Calls = append(result.Calls, feedbackeval.Call{Name: item.Get("name").String(), Arguments: json.RawMessage(item.Get("arguments").String())})
											}
										case "response.output_text.delta":
											result.Answer += data.Get("delta").String()
										case "response.completed":
											completed = true
											result.ResolvedModel = data.Get("response.model").String()
										case "response.failed", "response.incomplete", "error":
											err = fmt.Errorf("upstream %s", data.Get("type").String())
										}
									}
									response.Body.Close()
									if err == nil && !completed {
										err = fmt.Errorf("missing completion")
									}
								}
							}
						}
						result.DurationMS = time.Since(started).Milliseconds()
						if err != nil {
							result.InfrastructureError = err.Error()
						} else {
							result.Score = feedbackeval.Grade(c, result.Calls)
						}
						mu.Lock()
						results = append(results, result)
						mu.Unlock()
						t.Logf("%s/%s/%s/%d passed=%t errors=%v infrastructure=%s", model, arm, c.ID, repeat, result.Score.Passed, result.Score.Errors, result.InfrastructureError)
					})
				}
			}
		}
	}
	wg.Wait()
	digest := sha256.Sum256([]byte(feedback.Description))
	output := map[string]any{"generated_at": time.Now().UTC(), "description_sha256": hex.EncodeToString(digest[:]), "fingerprints": feedbackeval.Fingerprints(), "reasoning_effort": "low", "repeats": repeats, "baseline": "generic description with identical tool name/schema; not Anthropic native tool", "limitations": "synthetic single-turn decisions; deterministic field/anchor checks are not full semantic grounding; outputs retained for human review", "results": results}
	if path := os.Getenv("CLAUDEX_FEEDBACK_EVAL_OUTPUT"); path != "" {
		raw, e := json.MarshalIndent(output, "", "  ")
		if e != nil {
			t.Fatal(e)
		}
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(path, append(raw, '\n'), 0600); e != nil {
			t.Fatal(e)
		}
	}
	total := map[string]int{}
	passed := map[string]int{}
	infra := 0
	for _, r := range results {
		key := r.Model + "/" + r.Arm
		total[key]++
		if r.InfrastructureError != "" {
			infra++
			continue
		}
		if r.Score.Passed {
			passed[key]++
		} else if r.Arm == "clarified" {
			t.Errorf("clarified regression %s/%s: %v", r.Model, r.CaseID, r.Score.Errors)
		}
	}
	for key, n := range total {
		t.Logf("SUMMARY %s %d/%d passed", key, passed[key], n)
	}
	if infra > 0 {
		t.Errorf("%d infrastructure failures (not behavioral failures)", infra)
	}
}

// Regrade retained synthetic outputs without making model requests. Original
// outputs are kept separately; this makes grader corrections auditable.
func TestReplayFeedbackEvaluation(t *testing.T) {
	path := os.Getenv("CLAUDEX_FEEDBACK_EVAL_REPLAY")
	if path == "" {
		t.Skip("set CLAUDEX_FEEDBACK_EVAL_REPLAY")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var artifact map[string]json.RawMessage
	if err := json.Unmarshal(raw, &artifact); err != nil {
		t.Fatal(err)
	}
	var results []feedbackEvalResult
	if err := json.Unmarshal(artifact["results"], &results); err != nil {
		t.Fatal(err)
	}
	cases, err := feedbackeval.Cases()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]feedbackeval.Case{}
	for _, c := range cases {
		byID[c.ID] = c
	}
	totals, passes := map[string]int{}, map[string]int{}
	for i := range results {
		r := &results[i]
		c, ok := byID[r.CaseID]
		if !ok {
			t.Fatal("unknown case", r.CaseID)
		}
		if r.InfrastructureError != "" {
			t.Errorf("infrastructure: %s", r.InfrastructureError)
			continue
		}
		r.Score = feedbackeval.Grade(c, r.Calls)
		key := r.Model + "/" + r.Arm
		totals[key]++
		if r.Score.Passed {
			passes[key]++
		} else if r.Arm == "clarified" {
			t.Errorf("%s/%s: %v", key, r.CaseID, r.Score.Errors)
		}
	}
	for key, n := range totals {
		t.Logf("SUMMARY %s %d/%d passed", key, passes[key], n)
	}
	artifact["results"], _ = json.Marshal(results)
	artifact["regrade"], _ = json.Marshal(map[string]any{"at": time.Now().UTC(), "source": path, "fingerprints": feedbackeval.Fingerprints(), "note": "Removed context-insensitive prose prohibitions; structured reproduction assertions unchanged. Original outputs were not regenerated."})
	if output := os.Getenv("CLAUDEX_FEEDBACK_EVAL_OUTPUT"); output != "" {
		if filepath.Clean(output) == filepath.Clean(path) {
			t.Fatal("regrade output must not overwrite original")
		}
		out, err := json.MarshalIndent(artifact, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(output, append(out, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
}
