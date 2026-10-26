package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace")
	project := filepath.Join(home, ".claude", "projects", "fixture-project")
	for _, dir := range []string{cwd, project, filepath.Join(project, "memory")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("Use the shared fixture build directory.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "memory", "MEMORY.md"), []byte("The fixture project uses port 9031.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(project, "session.jsonl")
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	for _, r := range []map[string]any{
		{"type": "user", "cwd": cwd, "timestamp": "2026-10-26T10:00:00Z", "message": map[string]any{"role": "user", "content": "Keep the fixture sentinel orchard-731 and continue the unfinished task."}},
		{"type": "assistant", "cwd": cwd, "timestamp": "2026-10-26T10:00:01Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "tool_use", "id": "toolu_fixture", "name": "Read", "input": map[string]string{"file_path": "fixture.txt", "full_content": strings.Repeat("long-evidence ", 700) + "TOOL_INPUT_TAIL"}}}}},
		{"type": "user", "cwd": cwd, "timestamp": "2026-10-26T10:00:02Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_fixture", "content": strings.Repeat("result ", 1000) + "TOOL_RESULT_TAIL"}}}},
		{"type": "assistant", "cwd": cwd, "timestamp": "2026-10-26T10:00:03Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "fixture private state", "signature": "fixture-opaque"}, map[string]any{"type": "text", "text": "Task remains unfinished."}}}},
	} {
		if err := enc.Encode(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	return home, cwd, source
}

func TestSnapshotPreservesFullEvidenceAndPrivateContext(t *testing.T) {
	home, cwd, source := fixture(t)
	snapshot, err := Prepare(source, cwd, home, filepath.Join(home, ".claude"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(source)
	archived, _ := os.ReadFile(snapshot.Archive)
	if !bytes.Equal(raw, archived) {
		t.Fatal("archive changed source bytes")
	}
	normalized, _ := os.ReadFile(snapshot.Path)
	for _, want := range []string{"orchard-731", "TOOL_INPUT_TAIL", "TOOL_RESULT_TAIL", "shared fixture build", "port 9031"} {
		if !bytes.Contains(normalized, []byte(want)) {
			t.Fatalf("lost %s", want)
		}
	}
	if bytes.Contains(normalized, []byte("fixture private state")) {
		t.Fatal("opaque reasoning became model-visible")
	}
	if snapshot.Messages != 4 || snapshot.OpaqueBlocks != 1 || snapshot.MemoryProject != "fixture-project" {
		t.Fatalf("wrong manifest: %+v", snapshot)
	}
	for _, path := range []string{snapshot.Path, snapshot.Archive, filepath.Join(filepath.Dir(snapshot.Path), "manifest.json")} {
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatal("non-private file", path)
		}
	}
	second, err := Prepare(source, cwd, home, filepath.Join(home, ".claude"))
	if err != nil || second.Path != snapshot.Path {
		t.Fatal("unchanged snapshot is not stable", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "CLAUDE.md"), []byte("changed instruction"), 0600); err != nil {
		t.Fatal(err)
	}
	changed, err := Prepare(source, cwd, home, filepath.Join(home, ".claude"))
	if err != nil || changed.Path == snapshot.Path {
		t.Fatal("context changes must create a new snapshot", err)
	}
}

func TestSnapshotRejectsWrongSessionAndPartialWrites(t *testing.T) {
	home, cwd, source := fixture(t)
	if _, err := Prepare(source, home, home, filepath.Join(home, ".claude")); err == nil {
		t.Fatal("accepted mismatched workspace")
	}
	outside := filepath.Join(home, "outside.jsonl")
	data, _ := os.ReadFile(source)
	_ = os.WriteFile(outside, data, 0600)
	if _, err := Prepare(outside, cwd, home, filepath.Join(home, ".claude")); err == nil {
		t.Fatal("accepted non-session source")
	}
	_ = os.WriteFile(source, append(data, []byte(`{"type":`)...), 0600)
	if _, err := Prepare(source, cwd, home, filepath.Join(home, ".claude")); err == nil {
		t.Fatal("silently dropped incomplete record")
	}
}

func TestPluginAndHookBindExactSession(t *testing.T) {
	root := t.TempDir()
	pluginPath, err := InstallPlugin(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".claude-plugin/plugin.json", "skills/codex/SKILL.md", "hooks/hooks.json"} {
		if _, err := os.Stat(filepath.Join(pluginPath, name)); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(root, "session-env", "env")
	source := "/fixture/it's $(touch forbidden).jsonl"
	raw, _ := json.Marshal(map[string]string{"transcript_path": source, "cwd": "/fixture/work space"})
	if err := CaptureSession(bytes.NewReader(raw), file); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", `. "$1"; printf '%s' "$CLAUDEX_TRANSCRIPT_PATH"`, "fixture", file)
	output, err := cmd.Output()
	if err != nil || string(output) != source {
		t.Fatalf("hook quote failed: %q %v", output, err)
	}
}

func TestNativeEnvironmentPreservesCodexButDropsGateway(t *testing.T) {
	got := NativeEnvironment([]string{"PATH=/fixture", "CODEX_HOME=/native", "OPENAI_API_KEY=fixture-key", "ANTHROPIC_AUTH_TOKEN=fixture-secret", "CLAUDEX_BINARY=/gateway", "CLAUDE_CODE_AUTO_MODE_SERVER=1", "API_TIMEOUT_MS=600000"})
	if strings.Join(got, "|") != "PATH=/fixture|CODEX_HOME=/native|OPENAI_API_KEY=fixture-key" {
		t.Fatal(got)
	}
}

func TestImportFailureAndCompletionOrdering(t *testing.T) {
	for _, scenario := range []string{"success", "failed", "unsupported", "mismatch", "timeout", "memory_failed", "wrong_source"} {
		t.Run(scenario, func(t *testing.T) {
			home, cwd, source := fixture(t)
			s, err := Prepare(source, cwd, home, filepath.Join(home, ".claude"))
			if err != nil {
				t.Fatal(err)
			}
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("HANDOFF_TEST_APP", scenario)
			t.Setenv("HANDOFF_TEST_SOURCE", s.Path)
			t.Setenv("HANDOFF_TEST_CWD", s.CWD)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			result, err := Import(ctx, executable, filepath.Join(home, ".codex"), s)
			if scenario == "success" {
				if err != nil || result.ThreadID == "" || result.Memory != "imported" {
					t.Fatalf("%+v %v", result, err)
				}
			} else if err == nil {
				t.Fatal("false success")
			}
			if scenario == "memory_failed" && (result.ResumeCommand == "" || result.Memory != "snapshotted_only") {
				t.Fatal("partial memory failure lost the resumable conversation")
			}
		})
	}
}

func TestMain(m *testing.M) {
	if scenario := os.Getenv("HANDOFF_TEST_APP"); scenario != "" {
		fakeApp(scenario)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func fakeApp(scenario string) {
	dec := json.NewDecoder(os.Stdin)
	enc := json.NewEncoder(os.Stdout)
	for {
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if dec.Decode(&req) != nil {
			return
		}
		if req.ID == 0 {
			continue
		}
		result := any(map[string]any{})
		switch req.Method {
		case "externalAgentConfig/import":
			if scenario == "unsupported" {
				_ = enc.Encode(map[string]any{"id": req.ID, "error": map[string]any{"code": -32601, "message": "unknown"}})
				continue
			}
			if scenario == "timeout" {
				time.Sleep(5 * time.Second)
				return
			}
			typ := "SESSIONS"
			if bytes.Contains(req.Params, []byte(`"MEMORY"`)) {
				typ = "MEMORY"
			}
			failures := []any{}
			successes := []any{map[string]any{"source": os.Getenv("HANDOFF_TEST_SOURCE"), "target": "00000000-0000-0000-0000-000000000731"}}
			if scenario == "wrong_source" {
				successes = []any{map[string]any{"source": "/another/session.jsonl", "target": "00000000-0000-0000-0000-000000000731"}}
			}
			if scenario == "failed" || (scenario == "memory_failed" && typ == "MEMORY") {
				failures = append(failures, map[string]string{"message": "fixture import failed"})
				successes = nil
			}
			// An unrelated completed operation must never satisfy this request.
			_ = enc.Encode(map[string]any{"method": "externalAgentConfig/import/completed", "params": map[string]any{"importId": "another-import", "itemTypeResults": []any{}}})
			// Deliberately notify before the request result to test ordering races.
			_ = enc.Encode(map[string]any{"method": "externalAgentConfig/import/completed", "params": map[string]any{"importId": "fixture-import", "itemTypeResults": []any{map[string]any{"itemType": typ, "failures": failures, "successes": successes}}}})
			result = map[string]string{"importId": "fixture-import"}
		case "thread/read":
			cwd := os.Getenv("HANDOFF_TEST_CWD")
			if scenario == "mismatch" {
				cwd = "/different"
			}
			result = map[string]any{"thread": map[string]string{"id": "00000000-0000-0000-0000-000000000731", "cwd": cwd}}
		}
		_ = enc.Encode(map[string]any{"id": req.ID, "result": result})
	}
}

func TestCustomClaudeHomeSnapshotsMemoryAndScopedRules(t *testing.T) {
	home, cwd, source := fixture(t)
	custom := filepath.Join(home, "custom-claude")
	if err := os.Rename(filepath.Join(home, ".claude"), custom); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{filepath.Join(custom, "rules"), filepath.Join(home, ".claude", "rules")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "fixture.md"), []byte("fixture scoped rule"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source = filepath.Join(custom, "projects", "fixture-project", filepath.Base(source))
	s, err := Prepare(source, cwd, home, custom)
	if err != nil {
		t.Fatal(err)
	}
	if s.MemoryFiles != 1 || s.MemoryProject != "" {
		t.Fatalf("wrong custom memory scope: %+v", s)
	}
	if len(s.ContextFiles) != 4 {
		t.Fatalf("missing ancestor or global rules: %v", s.ContextFiles)
	}
}
