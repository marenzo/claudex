//go:build e2e

package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/feedback"
	"github.com/marenzo/claudex/internal/launcher"
	"github.com/tidwall/gjson"
)

// Real Claude Code discovers and executes the built stdio MCP tool against a
// fixture gateway. Inference responses are scripted; no real provider is used.
func TestClaudeLocalFeedbackMCP(t *testing.T) {
	claude := os.Getenv("CLAUDEX_TEST_CLAUDE_BIN")
	binary := os.Getenv("CLAUDEX_TEST_BINARY")
	if claude == "" || binary == "" {
		t.Skip("set CLAUDEX_TEST_CLAUDE_BIN and CLAUDEX_TEST_BINARY")
	}
	var mu sync.Mutex
	calls := 0
	found := false
	native := false
	result := ""
	sub := gjson.Parse(feedbackFixtureBody()).Get("report").Raw
	item, _ := json.Marshal(map[string]any{"type": "function_call", "id": "fc_feedback", "call_id": "call_feedback", "name": feedback.ClaudeToolName, "arguments": sub})
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		calls++
		for _, tool := range gjson.GetBytes(raw, "tools").Array() {
			name := tool.Get("name").String()
			found = found || name == feedback.ClaudeToolName
			native = native || name == "SendFeedback" || name == "SendUserFeedback"
		}
		if calls == 1 {
			return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+string(item)+`}`, completed)), nil
		}
		for _, v := range gjson.GetBytes(raw, "input").Array() {
			if v.Get("type").String() == "function_call_output" && v.Get("call_id").String() == "call_feedback" {
				result = v.Get("output").String()
			}
		}
		return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed)), nil
	})
	s.Config.Feedback.Enabled = true
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	mcp, err := launcher.FeedbackMCPConfig(binary, server.URL, s.Config.ClientKeyFile, strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := launcher.Settings(s.Config)
	if err != nil {
		t.Fatal(err)
	}
	settings.Env["ANTHROPIC_BASE_URL"] = server.URL
	settingsJSON, _ := json.Marshal(settings)
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "--bare", "--setting-sources", "", "--strict-mcp-config", "--mcp-config", string(mcp), "--settings", string(settingsJSON), "--model", "astra", "--disallowedTools", "Artifact,SendFeedback,SendUserFeedback", "--max-turns", "3", "--no-session-persistence", "--output-format", "json", "-p", "Record the observed fixture issue using ClaudexFeedback, then stop.")
	cmd.Dir = root
	// Isolate account/config state. No production Claude home or credentials.
	for _, key := range []string{"PATH", "TMPDIR", "LANG"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+root, "CLAUDE_CONFIG_DIR="+filepath.Join(root, "claude"), "ANTHROPIC_BASE_URL="+server.URL, "ANTHROPIC_AUTH_TOKEN="+strings.Repeat("k", 32), "DISABLE_FEEDBACK_COMMAND=1", "CLAUDE_CODE_SEND_FEEDBACK=0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Claude fixture: %v: %.4000s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if !found || native || calls != 2 || gjson.Get(result, "state").String() != "untriaged" || gjson.Get(result, "id").String() == "" {
		t.Fatalf("tool=%v native=%v calls=%d result=%s output=%.3000s", found, native, calls, result, out)
	}
	records, err := s.feedbackStore().List(ctx)
	if err != nil || len(records) != 1 {
		t.Fatal("record missing", err, len(records))
	}
	if records[0].Source.ClientVersion == "" {
		t.Fatal("missing MCP client version")
	}
	t.Logf("real Claude Code loaded only the local feedback tool, executed it and received a durable receipt; client=%s", records[0].Source.ClientVersion)
}
