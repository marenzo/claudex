//go:build e2e

package proxy

import (
	"context"
	"encoding/base64"
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

// Real Claude discovers the bundled skill, executes its native-image command,
// and reads the resulting image. Inference and command output are local fixtures;
// the image backend contract is tested separately in internal/imagegen.
func TestClaudeNativeImagegenSkill(t *testing.T) {
	claude := os.Getenv("CLAUDEX_TEST_CLAUDE_BIN")
	if claude == "" {
		t.Skip("set CLAUDEX_TEST_CLAUDE_BIN")
	}
	root := t.TempDir()
	imagePath := filepath.Join(root, "fox.png")
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(imagePath, pngBytes, 0600); err != nil {
		t.Fatal(err)
	}
	receipt, _ := json.Marshal(map[string]any{"path": imagePath, "prompt": "fixture watercolor fox", "model": "gpt-image-2", "width": 1, "height": 1})
	invocation := filepath.Join(root, "invocation")
	binary := filepath.Join(root, "claudex")
	script := "#!/bin/sh\nif [ \"$2\" = handoff-session ]; then exit 0; fi\nprintf '%s\\n' \"$@\" > " + handoff.Quote(invocation) + "\nprintf '%s\\n' " + handoff.Quote(string(receipt)) + "\n"
	if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	plugin, err := handoff.InstallPlugin(filepath.Join(root, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	call := func(name, id string, args any) string {
		arguments, _ := json.Marshal(args)
		item, _ := json.Marshal(map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": id, "name": name, "arguments": string(arguments)})
		return `{"type":"response.output_item.done","output_index":0,"item":` + string(item) + `}`
	}
	command := `"$CLAUDEX_BINARY" ctl imagegen --prompt 'fixture watercolor fox' --out ` + handoff.Quote(imagePath) + ` --json`
	bash := call("Bash", "imagegen", map[string]any{"command": command, "description": "Generate fixture image through native Codex", "timeout": 600000})
	read := call("Read", "inspect", map[string]any{"file_path": imagePath})
	var mu sync.Mutex
	calls := 0
	loaded, sawReceipt, sawImage := false, false, false
	s := fixture(t, func(r *http.Request) (*http.Response, error) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		calls++
		loaded = loaded || strings.Contains(string(raw), "same subscription image endpoints")
		for _, item := range gjson.GetBytes(raw, "input").Array() {
			if item.Get("type").String() == "function_call_output" && item.Get("call_id").String() == "imagegen" {
				sawReceipt = strings.Contains(item.Get("output").Raw, "gpt-image-2")
			}
			for _, part := range item.Get("content").Array() {
				if part.Get("type").String() == "input_image" {
					sawImage = true
				}
			}
			for _, part := range item.Get("output").Array() {
				if part.Get("type").String() == "input_image" {
					sawImage = true
				}
			}
		}
		if calls == 1 {
			return upstreamResponse(200, sse(created, bash, completed)), nil
		}
		if calls == 2 {
			return upstreamResponse(200, sse(created, read, completed)), nil
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
	cmd := exec.CommandContext(ctx, claude, "--setting-sources", "", "--plugin-dir", plugin, "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--settings", string(settingsJSON), "--model", "astra", "--allowedTools", "Bash", "Read", "--max-turns", "4", "--output-format", "json", "-p", "/claudex:imagegen fixture watercolor fox")
	cmd.Dir = root
	for _, key := range []string{"PATH", "TMPDIR", "LANG"} {
		if v, ok := os.LookupEnv(key); ok {
			cmd.Env = append(cmd.Env, key+"="+v)
		}
	}
	cmd.Env = append(cmd.Env, "HOME="+root, "CLAUDE_CONFIG_DIR="+filepath.Join(root, ".claude"), "CLAUDEX_BINARY="+binary, "ANTHROPIC_BASE_URL="+server.URL, "ANTHROPIC_AUTH_TOKEN="+strings.Repeat("k", 32), "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_AUTOUPDATER=1", "DISABLE_FEEDBACK_COMMAND=1", "CLAUDE_CODE_SEND_FEEDBACK=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("Claude imagegen fixture failed: %v: %.2000s", err, output)
	}
	mu.Lock()
	defer mu.Unlock()
	args, err := os.ReadFile(invocation)
	if err != nil || !strings.HasPrefix(string(args), "ctl\nimagegen\n--prompt\nfixture watercolor fox\n") || !loaded || !sawReceipt || !sawImage {
		t.Fatalf("loaded=%v receipt=%v image=%v calls=%d command=%q err=%v output=%.1500s", loaded, sawReceipt, sawImage, calls, args, err, output)
	}
	t.Log("real Claude loaded /claudex:imagegen, invoked the bundled helper, received its receipt, and inspected the saved PNG")
}
