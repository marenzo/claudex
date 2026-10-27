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

	"github.com/marenzo/claudex/internal/handoff"
	"github.com/marenzo/claudex/internal/launcher"
)

// Native Claude resumes the generated session against a scripted claudex
// provider, loads the actual memory index, then the saved conversation returns
// through the native Codex importer. No subscription/model credentials are used.
func TestCodexToClaudeResumeAndRoundTrip(t *testing.T) {
	claude, binary, codex := os.Getenv("CLAUDEX_TEST_CLAUDE_BIN"), os.Getenv("CLAUDEX_TEST_BINARY"), os.Getenv("CLAUDEX_TEST_CODEX_BIN")
	if claude == "" || binary == "" || codex == "" {
		t.Skip("set CLAUDEX_TEST_CLAUDE_BIN, CLAUDEX_TEST_BINARY, CLAUDEX_TEST_CODEX_BIN")
	}
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(root, "workspace")
	claudeHome := filepath.Join(root, ".claude")
	codexHome := filepath.Join(root, ".codex")
	for _, dir := range []string{cwd, filepath.Join(codexHome, "memories")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(cwd, "AGENTS.md"), "ROUNDTRIP_GUIDANCE_731\n")
	write(filepath.Join(codexHome, "memories", "MEMORY.md"), "UNCITED_MEMORY_731\nCITED_MEMORY_731\n")
	write(filepath.Join(codexHome, "config.toml"), "[memories]\ngenerate_memories=false\nuse_memories=false\n")
	source := filepath.Join(root, "rollout.jsonl")
	var rollout bytes.Buffer
	enc := json.NewEncoder(&rollout)
	for _, record := range []map[string]any{
		{"type": "session_meta", "payload": map[string]string{"id": "12345678-1234-4234-8234-123456789abc", "cwd": cwd}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "ROUNDTRIP_USER_731"}}}},
		{"type": "response_item", "payload": map[string]string{"type": "function_call", "name": "exec_command", "call_id": "fixturecall", "arguments": strings.Repeat("full input ", 1000) + "ROUNDTRIP_INPUT_TAIL"}},
		{"type": "response_item", "payload": map[string]string{"type": "function_call_output", "call_id": "fixturecall", "output": strings.Repeat("full output ", 1000) + "ROUNDTRIP_OUTPUT_TAIL"}},
		{"type": "response_item", "payload": map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "ROUNDTRIP_ASSISTANT_731\n<oai-mem-citation><citation_entries>\nMEMORY.md:2-2|note=[fixture]\n</citation_entries><rollout_ids></rollout_ids></oai-mem-citation>"}}}},
	} {
		if err := enc.Encode(record); err != nil {
			t.Fatal(err)
		}
	}
	write(source, rollout.String())
	snapshot, err := handoff.PrepareCodex(source, "", codexHome, claudeHome)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	// Verify Claude honors the exact project directory pinned in resume_command.
	const projectKey = "claudex-roundtrip-custom-project"
	options := handoff.ClaudeOptions{Binary: binary, Home: claudeHome, ProjectKey: projectKey}
	receipt, err := handoff.ImportClaude(ctx, options, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var request []byte
	calls := 0
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		request = raw
		calls++
		mu.Unlock()
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
	plugin, err := handoff.InstallPlugin(filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, claude, "--debug-file", filepath.Join(root, "debug.log"), "--setting-sources", "", "--plugin-dir", plugin, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--settings", string(settingsJSON), "--model", "astra", "--max-turns", "1", "--output-format", "json", "--resume", receipt.Transcript, "-p", "Continue the imported task. Do not run any tool.")
	cmd.Dir = cwd
	for _, key := range []string{"PATH", "TMPDIR", "LANG"} {
		if value, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+root, "CLAUDE_CONFIG_DIR="+claudeHome, "CLAUDE_CODE_PROJECT_DIR_NAME="+projectKey, "CODEX_HOME="+codexHome, "CLAUDEX_BINARY="+binary, "ANTHROPIC_BASE_URL="+server.URL, "ANTHROPIC_AUTH_TOKEN="+strings.Repeat("k", 32), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("native Claude resume: %v %.5000s", err, output)
	}
	mu.Lock()
	raw := append([]byte(nil), request...)
	count := calls
	mu.Unlock()
	for _, want := range []string{"ROUNDTRIP_USER_731", "ROUNDTRIP_INPUT_TAIL", "ROUNDTRIP_OUTPUT_TAIL", "ROUNDTRIP_ASSISTANT_731", "ROUNDTRIP_GUIDANCE_731", "CITED_MEMORY_731", "Imported Codex session memory"} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("native Claude lost %s; output %.2000s; request %.4000s", want, output, raw)
		}
	}
	if bytes.Contains(raw, []byte("UNCITED_MEMORY_731")) || count != 1 {
		t.Fatal("copied unrelated memory or executed unexpected history", count)
	}
	continued, err := handoff.ImportClaude(ctx, options, snapshot)
	if err != nil || continued.SessionID != receipt.SessionID {
		t.Fatal("retry after actual resume", err)
	}
	// Forward import only trusts a selected source under the configured projects.
	t.Setenv("HOME", root)
	t.Setenv("CODEX_HOME", codexHome)
	forward, err := handoff.Prepare(receipt.Transcript, cwd, root, claudeHome)
	if err != nil {
		t.Fatal(err)
	}
	native, err := handoff.Import(ctx, codex, codexHome, forward)
	if err != nil || native.ThreadID == "" {
		t.Fatal("roundtrip native import", err)
	}
	t.Log("real Claude resumed imported text/tool evidence and native project memory, persisted continuation, reused identity, and imported back into native Codex")
}
