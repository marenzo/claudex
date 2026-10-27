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

	"github.com/marenzo/claudex/internal/handoff"
	"github.com/marenzo/claudex/internal/launcher"
	"github.com/tidwall/gjson"
)

// Exercises real Claude skill loading, SessionStart binding, Bash execution, and
// native Codex import. All Claude inference is scripted; no live credentials.
func TestClaudeToNativeCodexSkill(t *testing.T) {
	claude, binary, codex := os.Getenv("CLAUDEX_TEST_CLAUDE_BIN"), os.Getenv("CLAUDEX_TEST_BINARY"), os.Getenv("CLAUDEX_TEST_CODEX_BIN")
	if claude == "" || binary == "" || codex == "" {
		t.Skip("set CLAUDEX_TEST_CLAUDE_BIN, CLAUDEX_TEST_BINARY, and CLAUDEX_TEST_CODEX_BIN")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "workspace")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex", "config.toml"), []byte("[memories]\ngenerate_memories = false\nuse_memories = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// Resolve the requested native binary, independently of the test runner's PATH.
	binDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(binDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(codex, filepath.Join(binDir, "codex")); err != nil {
		t.Fatal(err)
	}
	plugin, err := handoff.InstallPlugin(filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	calls := 0
	skillLoaded := false
	receipt := ""
	args, _ := json.Marshal(map[string]any{"command": `"$CLAUDEX_BINARY" ctl handoff --to codex --json`, "description": "Transfer this fixture session to native Codex", "timeout": 120000})
	item, _ := json.Marshal(map[string]any{"type": "function_call", "id": "fc_handoff", "call_id": "call_handoff", "name": "Bash", "arguments": string(args)})
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		calls++
		skillLoaded = skillLoaded || strings.Contains(string(raw), "SessionStart hook binds the exact")
		if calls == 1 {
			return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+string(item)+`}`, completed)), nil
		}
		for _, v := range gjson.GetBytes(raw, "input").Array() {
			if v.Get("type").String() == "function_call_output" && v.Get("call_id").String() == "call_handoff" {
				receipt = v.Get("output").String()
			}
		}
		return upstreamResponse(200, sse(created, `{"type":"response.output_item.done","output_index":0,"item":`+messageItem+`}`, completed)), nil
	})
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	settings, err := launcher.Settings(s.Config)
	if err != nil {
		t.Fatal(err)
	}
	settings.Env["ANTHROPIC_BASE_URL"] = server.URL
	settingsJSON, _ := json.Marshal(settings)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "--debug-file", filepath.Join(root, "claude-debug.log"), "--setting-sources", "", "--plugin-dir", plugin, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--settings", string(settingsJSON), "--model", "astra", "--allowedTools", "Bash", "--max-turns", "3", "--output-format", "json", "-p", "/claudex:handoff")
	cmd.Dir = cwd
	for _, key := range []string{"PATH", "TMPDIR", "LANG"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"), "HOME="+root, "CLAUDE_CONFIG_DIR="+filepath.Join(root, ".claude"), "CODEX_HOME="+filepath.Join(root, ".codex"), "CLAUDEX_BINARY="+binary, "ANTHROPIC_BASE_URL="+server.URL, "ANTHROPIC_AUTH_TOKEN="+strings.Repeat("k", 32), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_FEEDBACK_COMMAND=1", "CLAUDE_CODE_SEND_FEEDBACK=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Claude skill fixture failed: %v: %.3000s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	if !skillLoaded || !strings.Contains(receipt, `"thread_id":`) || !strings.Contains(receipt, "codex resume") || !strings.Contains(receipt, "/projects/") {
		debug, _ := os.ReadFile(filepath.Join(root, "claude-debug.log"))
		for _, line := range strings.Split(string(debug), "\n") {
			if strings.Contains(strings.ToLower(line), "hook") {
				t.Log(line)
			}
		}
		_ = filepath.WalkDir(filepath.Join(root, ".claude"), func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
				t.Log("transcript file:", path)
			}
			return nil
		})
		t.Fatalf("skill=%v calls=%d receipt=%.5000s output=%.1500s", skillLoaded, calls, receipt, output)
	}
	ledger, err := os.ReadFile(filepath.Join(root, ".codex", "external_agent_session_imports.json"))
	if err != nil || !strings.Contains(string(ledger), "imported_thread_id") {
		t.Fatal("native thread not persisted", err)
	}
	t.Log("real Claude loaded /claudex:handoff, captured its transcript via SessionStart, ran the built-in handoff, and received a native Codex resume command")
}
