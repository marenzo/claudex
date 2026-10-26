//go:build e2e

package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
