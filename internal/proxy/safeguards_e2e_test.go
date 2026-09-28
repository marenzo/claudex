//go:build e2e

package proxy

import (
	"bytes"
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

	"github.com/marenzo/claudex/internal/auth"
	"github.com/tidwall/gjson"
)

// Exercise the real Claude Code permission decision through Claudex, with both
// model responses provided by local fixtures. No subscription or live service is used.
func TestClaudeSafeguardDecisions(t *testing.T) {
	claude := os.Getenv("CLAUDEX_TEST_CLAUDE_BIN")
	if claude == "" {
		t.Skip("set CLAUDEX_TEST_CLAUDE_BIN to opt into the local Claude Code protocol test")
	}
	for _, outcome := range []string{"flagged", "not_flagged"} {
		t.Run(outcome, func(t *testing.T) {
			runClaudeSafeguardProbe(t, claude, outcome, nil)
		})
	}
}

func runClaudeSafeguardProbe(t *testing.T, claude, outcome string, live *auth.Credential) {
	t.Helper()
	var mu sync.Mutex
	mainCalls, classifierCalls := 0, 0
	var toolResult string
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			return nil, err
		}
		mu.Lock()
		defer mu.Unlock()
		if gjson.GetBytes(raw, "text.format.name").String() == "claudex_permission_verdicts" {
			classifierCalls++
			if live != nil {
				r.Body = io.NopCloser(bytes.NewReader(raw))
				return http.DefaultTransport.RoundTrip(r)
			}
			return upstreamResponse(200, classifierResponse(`{"verdicts":[{"id":"toolu_test","outcome":"`+outcome+`","explanation":"Controlled protocol fixture"}]}`)), nil
		}
		mainCalls++
		if mainCalls == 1 {
			return upstreamResponse(200, toolResponse()), nil
		}
		for _, item := range gjson.GetBytes(raw, "input").Array() {
			if item.Get("type").String() == "function_call_output" && item.Get("call_id").String() == "toolu_test" {
				toolResult = item.Get("output").String()
			}
		}
		return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed)), nil
	})
	s.Config.AutoModeClassifierModel = "sol"
	if live != nil {
		authPath := filepath.Join(t.TempDir(), "auth.json")
		data, _ := json.Marshal(live)
		if err := auth.WritePrivate(authPath, data); err != nil {
			t.Fatal(err)
		}
		s.Client.Auth = auth.NewStore(authPath)
		s.Config.AutoModeClassifierModel = "astra"
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	root := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "--bare", "--setting-sources", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--permission-mode", "auto", "--model", "astra", "--tools", "Bash", "--max-turns", "2", "--no-session-persistence", "--output-format", "json", "-p", "Print SAFE_PROBE using printf, then stop.")
	cmd.Dir = root
	for _, key := range []string{"PATH", "HOME", "TMPDIR", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "ANTHROPIC_BASE_URL="+server.URL, "ANTHROPIC_API_KEY="+strings.Repeat("k", 32), "CLAUDE_CONFIG_DIR="+filepath.Join(root, "claude-config"), "CLAUDE_CODE_AUTO_MODE_SERVER=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("Claude Code: %v: %s", err, out)
	}
	mu.Lock()
	defer mu.Unlock()
	if classifierCalls != 1 || mainCalls != 2 {
		t.Fatalf("main=%d classifier=%d: %s", mainCalls, classifierCalls, out)
	}
	if outcome == "flagged" {
		if !strings.Contains(toolResult, "denied by the Claude Code auto mode classifier") {
			t.Fatalf("flagged tool not denied: %s", toolResult)
		}
	} else if strings.TrimSpace(toolResult) != "SAFE_PROBE" {
		t.Fatalf("allowed tool did not execute: %q", toolResult)
	}
	var result map[string]any
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("invalid CLI result: %s", out)
	}
	t.Logf("real Claude Code accepted %s verdict through Claudex (live GPT: %t)", outcome, live != nil)
}
