//go:build e2e

package handoff

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
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
)

// Uses the real Codex importer in a temporary home, without credentials or a model turn.
func TestNativeCodexHandoff(t *testing.T) {
	binary := os.Getenv("CLAUDEX_TEST_CODEX_BIN")
	if binary == "" {
		t.Skip("set CLAUDEX_TEST_CODEX_BIN to test the installed native importer")
	}
	home, cwd, source := fixture(t)
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	for _, key := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL", "CODEX_HOME_OVERRIDE"} {
		t.Setenv(key, "")
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	// Disable automatic background memory generation in this offline fixture.
	if err := os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte("[memories]\ngenerate_memories = false\nuse_memories = false\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := Prepare(source, cwd, home, filepath.Join(home, ".claude"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	result, err := Import(ctx, binary, filepath.Join(home, ".codex"), s)
	if err != nil {
		t.Fatalf("native import: %v; receipt: %+v", err, result)
	}
	if result.ThreadID == "" || result.Memory != "imported" {
		t.Fatalf("incomplete receipt: %+v", result)
	}
	a, err := startApp(ctx, binary, cwd, filepath.Join(home, ".codex"), false)
	if err != nil {
		t.Fatal(err)
	}
	defer a.close()
	if err := a.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "claudex_test", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil); err != nil {
		t.Fatal(err)
	}
	_ = a.send(map[string]any{"method": "initialized", "params": map[string]any{}})
	var response json.RawMessage
	if err := a.request(ctx, "thread/read", map[string]any{"threadId": result.ThreadID, "includeTurns": true}, &response); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"orchard-731", "TOOL_INPUT_TAIL", "TOOL_RESULT_TAIL", "shared fixture build", "port 9031"} {
		if !bytes.Contains(response, []byte(want)) {
			t.Fatalf("native history lost %q", want)
		}
	}
	if err := a.request(ctx, "thread/resume", map[string]any{"threadId": result.ThreadID, "cwd": cwd, "excludeTurns": true}, &response); err != nil {
		t.Fatal("native resume failed", err)
	}
	memory := filepath.Join(home, ".codex", "memories", "extensions", "external_agent_import", "resources", "fixture-project", "MEMORY.md")
	data, err := os.ReadFile(memory)
	if err != nil || !bytes.Contains(data, []byte("port 9031")) {
		t.Fatalf("native project memory missing: %v", err)
	}
	// A retry of the identical snapshot must resolve the same verified thread.
	second, err := Import(ctx, binary, filepath.Join(home, ".codex"), s)
	if err != nil || second.ThreadID != result.ThreadID {
		t.Fatalf("retry created another thread: %+v %v", second, err)
	}
	t.Log("native import, full tool evidence, project memory, thread reload/resume, and idempotent retry passed without inference")
}

func TestNativeCodexHandoffSkill(t *testing.T) {
	binary, helper := os.Getenv("CLAUDEX_TEST_CODEX_BIN"), os.Getenv("CLAUDEX_TEST_BINARY")
	if binary == "" || helper == "" {
		t.Skip("set CLAUDEX_TEST_CODEX_BIN and CLAUDEX_TEST_BINARY")
	}
	home, cwd, _ := codexFixture(t)
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	for _, name := range []string{"OPENAI_API_KEY", "CODEX_API_KEY", "OPENAI_BASE_URL", "CODEX_THREAD_ID", "CODEX_SESSION_ID", "CLAUDE_CONFIG_DIR", "CLAUDEX_CONFIG"} {
		t.Setenv(name, "")
	}
	path, err := InstallCodexSkill(context.Background(), home, helper)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	a, err := startApp(ctx, binary, cwd, filepath.Join(home, ".codex"), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "claudex_test", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil); err != nil {
		t.Fatal(err)
	}
	_ = a.send(map[string]any{"method": "initialized", "params": map[string]any{}})
	var skills json.RawMessage
	if err := a.request(ctx, "skills/list", map[string]any{"cwds": []string{cwd}, "forceReload": true}, &skills); err != nil {
		t.Fatal(err)
	}
	a.close()
	if !bytes.Contains(skills, []byte(`"name":"claudex:handoff"`)) {
		t.Fatalf("native skill not namespaced (%s): %.4000s", path, skills)
	}
	var mu sync.Mutex
	loaded := false
	receipt := ""
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			z, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer z.Close()
			reader = z
		}
		raw, _ := io.ReadAll(reader)
		mu.Lock()
		defer mu.Unlock()
		calls++
		loaded = loaded || bytes.Contains(raw, []byte("uses `CODEX_THREAD_ID`"))
		var req struct {
			Input []struct {
				Type   string          `json:"type"`
				Output json.RawMessage `json:"output"`
			} `json:"input"`
		}
		_ = json.Unmarshal(raw, &req)
		for _, item := range req.Input {
			if item.Type == "function_call_output" && bytes.Contains(item.Output, []byte("resume_command")) {
				receipt = string(item.Output)
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		event := func(v any) { data, _ := json.Marshal(v); fmt.Fprintf(w, "data: %s\n\n", data) }
		event(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_fixture"}})
		if calls == 1 {
			args, _ := json.Marshal(map[string]string{"cmd": Quote(helper) + " ctl handoff --to cc --json"})
			event(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "function_call", "name": "exec_command", "id": "fc_fixture", "call_id": "call_fixture", "arguments": string(args)}})
		} else {
			event(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_fixture", "content": []any{map[string]string{"type": "output_text", "text": "Handoff prepared."}}}})
		}
		event(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "usage": map[string]int{"input_tokens": 100, "output_tokens": 10, "total_tokens": 110}}})
	}))
	defer server.Close()
	args := []string{"exec", "--skip-git-repo-check", "--json", "--sandbox", "workspace-write", "-c", `approval_policy="never"`, "-c", `model="fixture"`, "-c", `model_provider="fixture"`, "-c", `model_providers.fixture.name="Fixture"`, "-c", `model_providers.fixture.base_url="` + server.URL + `"`, "-c", `model_providers.fixture.wire_api="responses"`, "-c", `memories.generate_memories=false`, "-c", `memories.use_memories=false`, "Use $claudex:handoff to continue this exact session in Claude Code."}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = cwd
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Codex skill run: %v %.5000s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	if !loaded || !strings.Contains(receipt, "session_id") || !strings.Contains(receipt, "--resume") {
		t.Fatalf("loaded=%v calls=%d receipt=%.4000s output=%.4000s", loaded, calls, receipt, output)
	}
	t.Log("real Codex discovered and loaded $claudex:handoff, executed the helper with its exact CODEX_THREAD_ID, and received a Claude resume command")
}
