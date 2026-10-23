package proxy

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/marenzo/claudex/internal/config"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func TestReviewPrefixSurvivesLiveStateAndCacheAnnotationChanges(t *testing.T) {
	raw := []byte(reviewRequest(false))
	raw, _ = sjson.SetBytes(raw, "system", []any{map[string]any{"type": "text", "text": "Private memory index must remain evidence", "cache_control": map[string]string{"type": "ephemeral"}}})
	raw, _ = sjson.SetRawBytes(raw, "messages", []byte(`[{"role":"user","content":[{"type":"text","text":"Read fixture","cache_control":{"type":"ephemeral"}}]},{"role":"assistant","content":[{"type":"tool_use","id":"old","name":"Bash","input":{"cache_control":"ARGUMENT_MUST_STAY","count":9007199254740993}}]}]`))
	first, err := newSafeguardReview(raw, config.Config{AutoModeClassifierModel: "sol"})
	if err != nil {
		t.Fatal(err)
	}
	first.payload["tool_uses"] = json.RawMessage(`[{"id":"pending","name":"Read","input":{}}]`)
	before := classifierEvidence(first.payload)
	raw, _ = sjson.DeleteBytes(raw, "system.0.cache_control")
	raw, _ = sjson.DeleteBytes(raw, "messages.0.content.0.cache_control")
	raw, _ = sjson.SetBytes(raw, "safeguards.0.classifier_context.git_state", map[string]any{"status": map[string]int{"modified": 2}})
	raw, _ = sjson.SetBytes(raw, "safeguards.0.classifier_context.rules.deny", []string{"Bash(*)"})
	second, err := newSafeguardReview(raw, config.Config{AutoModeClassifierModel: "sol"})
	if err != nil {
		t.Fatal(err)
	}
	second.payload["tool_uses"] = first.payload["tool_uses"]
	after := classifierEvidence(second.payload)
	for i := range before[:len(before)-1] {
		a, _ := json.Marshal(before[i])
		b, _ := json.Marshal(after[i])
		if string(a) != string(b) {
			t.Fatal("live context or a moved cache annotation changed stable evidence")
		}
	}
	current := after[len(after)-1].Content[0].Text
	if gjson.Get(current, "classifier_context.rules.deny.0").String() != "Bash(*)" || gjson.Get(current, "classifier_context.git_state.status.modified").Int() != 2 {
		t.Fatal("current boundaries or state lost")
	}
	joined, _ := json.Marshal(after)
	if !strings.Contains(string(joined), "ARGUMENT_MUST_STAY") || !strings.Contains(string(joined), "9007199254740993") || !strings.Contains(string(joined), "Private memory index") {
		t.Fatal("normalization changed permission evidence")
	}
}

func TestReviewCacheComparisonIsPrivateIsolatedAndBounded(t *testing.T) {
	input := []classifierEvidenceMessage{{Role: "user", Content: []classifierEvidenceText{{Type: "input_text", Text: "PRIVATE_TEXT"}}}}
	var tracker, other reviewCacheTracker
	first := tracker.observe("PRIVATE_SESSION", "sol", input, json.RawMessage(`{"rules":"PRIVATE_RULE"}`))
	if first.Previous || first.SharedMessages != 0 {
		t.Fatal("invented prior context")
	}
	second := tracker.observe("PRIVATE_SESSION", "sol", input, json.RawMessage(`{"rules":"CHANGED_RULE"}`))
	if !second.Previous || !second.ContextChanged || second.PrefixChanged || second.SharedMessages != 1 {
		t.Fatalf("context update not distinguished from prefix change: %+v", second)
	}
	encoded, _ := json.Marshal(second)
	if strings.Contains(string(encoded), "PRIVATE") || strings.Contains(string(encoded), "CHANGED_RULE") {
		t.Fatal("diagnostics disclosed source evidence")
	}
	independent := other.observe("PRIVATE_SESSION", "sol", input, json.RawMessage(`{}`))
	if independent.Affinity == first.Affinity || independent.Prefix == first.Prefix {
		t.Fatal("fingerprints can be compared across gateway instances")
	}
	if tracker.observe("PRIVATE_SESSION", "astra", input, json.RawMessage(`{}`)).Previous {
		t.Fatal("models share cache comparison")
	}
	for i := range reviewCacheSessions + 3 {
		tracker.observe(fmt.Sprint(i), "sol", input, json.RawMessage(`{}`))
	}
	if len(tracker.entries) != reviewCacheSessions {
		t.Fatal("unbounded sessions")
	}
	if tracker.observe("PRIVATE_SESSION", "sol", input, json.RawMessage(`{}`)).Previous {
		t.Fatal("oldest group was not evicted")
	}
	long := make([]classifierEvidenceMessage, reviewCacheMessages+10)
	copy(long, input)
	view := tracker.observe("large", "sol", long, json.RawMessage(`{}`))
	if !view.Truncated || len(tracker.entries[view.Affinity].messages) != reviewCacheMessages {
		t.Fatal("unbounded message comparisons")
	}
}

func TestReviewCacheEndpointSurvivesBeyondDetailedComparisonLimit(t *testing.T) {
	request := []byte(reviewRequest(false))
	var messages []any
	for i := range reviewCacheMessages + 20 {
		messages = append(messages, map[string]any{"role": "user", "content": fmt.Sprintf("Fixture evidence %d", i)})
	}
	request, _ = sjson.SetBytes(request, "messages", messages)
	first, err := newSafeguardReview(request, config.Config{AutoModeClassifierModel: "sol"})
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately use another JSON key order to test canonical pending/history encoding.
	first.payload["tool_uses"] = json.RawMessage(`[{"name":"Read","input":{"z":2,"a":1},"id":"pending"}]`)
	var tracker reviewCacheTracker
	before := classifierEvidence(first.payload)
	tracker.observe("session", "sol", before, first.payload["classifier_context"])
	request, _ = sjson.SetBytes(request, "messages.-1", map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "pending", "name": "Read", "input": map[string]int{"a": 1, "z": 2}}}})
	request, _ = sjson.SetBytes(request, "messages.-1", map[string]any{"role": "user", "content": "Continue"})
	request, _ = sjson.SetBytes(request, "safeguards.0.classifier_context.rules.ask", []string{"Read(*)"})
	second, err := newSafeguardReview(request, config.Config{AutoModeClassifierModel: "sol"})
	if err != nil {
		t.Fatal(err)
	}
	second.payload["tool_uses"] = json.RawMessage(`[{"id":"next","input":{},"name":"Read"}]`)
	after := classifierEvidence(second.payload)
	view := tracker.observe("session", "sol", after, second.payload["classifier_context"])
	if !view.PreviousEndpointMatched || !view.Truncated || !view.ContextChanged {
		t.Fatalf("full cache endpoint not verified beyond the sampled prefix: %+v", view)
	}
	// Reproduce the previous layout: the changing context was itself a user
	// message, so the saved last-user endpoint never reappeared in the next turn.
	before[len(before)-1].Role = "user"
	after[len(after)-1].Role = "user"
	var control reviewCacheTracker
	control.observe("session", "sol", before, first.payload["classifier_context"])
	if control.observe("session", "sol", after, second.payload["classifier_context"]).PreviousEndpointMatched {
		t.Fatal("changing permission tail incorrectly considered reusable")
	}
}
