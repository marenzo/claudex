//go:build e2e

// Package e2e drives the real Claude Code CLI against the claudex gateway inside
// a minimal Docker container, using a real Codex subscription. It is opt-in:
//
//	make e2e                      # or: go test -tags e2e -count=1 -timeout 45m -v ./e2e/
//
// Credentials come from the host claudex config (CLAUDEX_E2E_CONFIG, default
// ~/.config/claudex/config.json). The auth file and client key are copied into
// a temporary directory that is mounted into the container, so the host
// installation is never modified. If the container refreshed the OAuth token,
// the refreshed credential is copied back only when the host file is unchanged.
package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/launcher"
)

const (
	image     = "claudex-e2e:test"
	container = "claudex-e2e"
	turnLimit = 12 * time.Minute
)

// turn is one prompt inside the shared Claude session plus its assertions.
type turn struct {
	name      string
	prompt    string
	wantTools []string // at least one of these tools must be called; empty means no requirement
	check     func(t *testing.T, h *harness, result string)
	// tolerateThrash accepts Claude Code's autocompact thrash guard ending the
	// turn, provided at least one compaction already succeeded.
	tolerateThrash bool
}

type harness struct {
	t           *testing.T
	env         []string
	sessionID   string
	turns       int
	compactions int  // compact_boundary events seen in the shared session
	thrashed    bool // the last turn ended at Claude Code's autocompact thrash guard
}

type streamEvent struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	Message   struct {
		Content []struct {
			Type string `json:"type"`
			Name string `json:"name"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"message"`
}

func TestClaudeCodeHarnessThroughGateway(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed")
	}
	root := repoRoot(t)
	creds := prepareCredentials(t)
	buildImage(t, root)
	startContainer(t, creds)

	cfg, err := config.Load(filepath.Join(creds, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	settings, err := launcher.Settings(cfg)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(filepath.Join(creds, "client-key"))
	if err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t}
	for name, value := range settings.Env {
		if name == "CLAUDE_CODE_AUTO_COMPACT_WINDOW" {
			continue // overridden below
		}
		h.env = append(h.env, name+"="+value)
	}
	h.env = append(h.env,
		"ANTHROPIC_AUTH_TOKEN="+strings.TrimSpace(string(key)),
		"DISABLE_AUTOUPDATER=1",
		"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"CLAUDE_CODE_EFFORT_LEVEL="+settings.EffortLevel,
		// Force automatic compaction early so the test exercises it at ~100k tokens.
		"CLAUDE_CODE_AUTO_COMPACT_WINDOW=100000",
	)

	data := fixtureData()
	dataLines := strings.Count(data, "\n")
	dataWords := len(strings.Fields(data))
	h.writeFile("/work/data.txt", data)
	for i, name := range []string{"a.go", "b.go", "c.go"} {
		h.writeFile("/work/src/"+name, fmt.Sprintf("package src\n\nvar V%d = %d\n", i, i))
	}
	h.writeFile("/work/src/notes.txt", "not go\n")
	big, bigLine3777 := bigFixture()
	h.writeFile("/work/big.txt", big)

	turns := []turn{
		{
			name:      "write_file",
			prompt:    "Create the file /work/hello.txt containing exactly the text HELLO-E2E and nothing else. Do not explain.",
			wantTools: []string{"Write", "Bash"},
			check: func(t *testing.T, h *harness, _ string) {
				h.expectFile("/work/hello.txt", "HELLO-E2E")
			},
		},
		{
			name:      "read_and_edit",
			prompt:    "Read /work/hello.txt and change its content to HELLO-E2E-2 using an edit, keeping a single line.",
			wantTools: []string{"Edit", "Write", "Bash"},
			check: func(t *testing.T, h *harness, _ string) {
				h.expectFile("/work/hello.txt", "HELLO-E2E-2")
			},
		},
		{
			name:      "bash_command",
			prompt:    "Run the shell command `uname -a` and save its output to /work/uname.txt. Then reply with just the kernel name (first word of the output).",
			wantTools: []string{"Bash"},
			check: func(t *testing.T, h *harness, result string) {
				out := h.readFile("/work/uname.txt")
				if !strings.HasPrefix(out, "Linux") {
					t.Errorf("uname.txt = %q", out)
				}
				if !strings.Contains(result, "Linux") {
					t.Errorf("result %q does not mention Linux", result)
				}
			},
		},
		{
			name:   "glob_and_grep",
			prompt: "How many files with the .go extension exist under /work/src? Use a file search (Glob or Grep if available, otherwise a shell find). Reply with only the number.",
			// Claude Code 2.1.x no longer offers Glob, Grep, or TodoWrite to the model by
			// default (verified from a captured request), so shell fallbacks are accepted.
			wantTools: []string{"Glob", "Grep", "Bash"},
			check: func(t *testing.T, h *harness, result string) {
				expectNumber(t, result, 3)
			},
		},
		{
			name:      "subagent",
			prompt:    "Delegate to a subagent: have it count the number of lines in /work/data.txt and report back. Reply with only the number it found.",
			wantTools: []string{"Agent", "Task"},
			check: func(t *testing.T, h *harness, result string) {
				expectNumber(t, result, dataLines)
			},
		},
		{
			name:      "parallel_subagents",
			prompt:    "Launch two subagents in parallel in one step: one counts the words in /work/data.txt, the other counts the lines. Reply in the exact form `words=<n> lines=<m>`.",
			wantTools: []string{"Agent", "Task"},
			check: func(t *testing.T, h *harness, result string) {
				expectPattern(t, result, fmt.Sprintf(`words=\s*%d`, dataWords))
				expectPattern(t, result, fmt.Sprintf(`lines=\s*%d`, dataLines))
			},
		},
		{
			name:      "todo_plan",
			prompt:    "Plan the steps (use the TodoWrite tool if it is available), then create /work/a.txt, /work/b.txt and /work/c.txt, each containing only its own file name without the extension (a, b, c). Reply DONE when finished.",
			wantTools: []string{"TodoWrite", "Write", "Bash"},
			check: func(t *testing.T, h *harness, _ string) {
				for _, name := range []string{"a", "b", "c"} {
					h.expectFile("/work/"+name+".txt", name)
				}
			},
		},
		{
			name:      "hash_via_bash",
			prompt:    "Compute the SHA-256 hex digest of /work/hello.txt with a shell command and write only the 64-character digest to /work/hello.sha. Reply with the digest.",
			wantTools: []string{"Bash"},
			check: func(t *testing.T, h *harness, result string) {
				sum := sha256.Sum256([]byte(h.readFile("/work/hello.txt")))
				want := hex.EncodeToString(sum[:])
				if got := strings.TrimSpace(h.readFile("/work/hello.sha")); got != want {
					t.Errorf("hello.sha = %q, want %q", got, want)
				}
				if !strings.Contains(result, want) {
					t.Errorf("result does not contain digest: %q", result)
				}
			},
		},
		{
			name:      "script_creation",
			prompt:    "Write an executable shell script /work/count.sh that prints the number of lines in /work/data.txt, run it, and save its output to /work/count.out. Reply with the number.",
			wantTools: []string{"Bash", "Write"},
			check: func(t *testing.T, h *harness, result string) {
				expectNumber(t, h.readFile("/work/count.out"), dataLines)
				expectNumber(t, result, dataLines)
			},
		},
		{
			name:      "error_recovery",
			prompt:    "Read /work/missing.txt. If it does not exist, create it with the content RECOVERED and reply CREATED; otherwise reply EXISTS.",
			wantTools: []string{"Read", "Write", "Bash"},
			check: func(t *testing.T, h *harness, result string) {
				h.expectFile("/work/missing.txt", "RECOVERED")
				if !strings.Contains(result, "CREATED") {
					t.Errorf("result %q", result)
				}
			},
		},
		{
			name:   "structured_output",
			prompt: "List the names of the .txt files directly inside /work (not subdirectories). Reply with only a JSON object of the form {\"files\": [..]} sorted alphabetically, no code fences, no prose.",
			check: func(t *testing.T, h *harness, result string) {
				var parsed struct {
					Files []string `json:"files"`
				}
				cleaned := strings.TrimSpace(strings.Trim(strings.TrimSpace(result), "`"))
				cleaned = strings.TrimPrefix(cleaned, "json")
				start, end := strings.Index(cleaned, "{"), strings.LastIndex(cleaned, "}")
				if start < 0 || end < start {
					t.Fatalf("no JSON object in %q", result)
				}
				if err := json.Unmarshal([]byte(cleaned[start:end+1]), &parsed); err != nil {
					t.Fatalf("invalid JSON %q: %v", result, err)
				}
				have := strings.Join(parsed.Files, ",")
				for _, want := range []string{"hello.txt", "data.txt", "a.txt", "missing.txt", "uname.txt"} {
					if !strings.Contains(have, want) {
						t.Errorf("files %v missing %s", parsed.Files, want)
					}
				}
			},
		},
		{
			name:   "session_recall",
			prompt: "Without using any tools: what was the exact content you first wrote to /work/hello.txt at the start of this session, before it was edited? Reply with only that text.",
			check: func(t *testing.T, h *harness, result string) {
				if !strings.Contains(result, "HELLO-E2E") || strings.Contains(result, "HELLO-E2E-2") {
					t.Errorf("recall failed: %q", result)
				}
			},
		},
		{
			name:      "memory_write",
			prompt:    "Remember for future sessions that this project's codename is ORCHID: add a short line stating that to /work/CLAUDE.md (create it if needed). Reply SAVED.",
			wantTools: []string{"Write", "Edit", "Bash"},
			check: func(t *testing.T, h *harness, _ string) {
				if !strings.Contains(h.readFile("/work/CLAUDE.md"), "ORCHID") {
					t.Errorf("CLAUDE.md does not mention ORCHID")
				}
			},
		},
		{
			name:      "multi_file_refactor",
			prompt:    "In every .go file under /work/src, rename the package from `src` to `fixtures` and verify with grep that no file still says `package src`. Reply with the number of files changed.",
			wantTools: []string{"Edit", "Bash", "Write"},
			check: func(t *testing.T, h *harness, result string) {
				for _, name := range []string{"a.go", "b.go", "c.go"} {
					if content := h.readFile("/work/src/" + name); !strings.Contains(content, "package fixtures") {
						t.Errorf("%s not renamed: %q", name, content)
					}
				}
				expectNumber(t, result, 3)
			},
		},
		{
			name:   "web_search",
			prompt: "Use web search to find the latest stable Go (golang) release version, then reply in the form `go <major>.<minor>` and nothing else.",
			check: func(t *testing.T, h *harness, result string) {
				expectPattern(t, result, `go\s*1\.\d+`)
			},
		},
		{
			name:   "large_generation",
			prompt: "Write /work/table.md containing a Markdown table with exactly 60 data rows; columns: n, n squared, n cubed for n from 1 to 60. Reply with the value in the last row's cubed column.",
			check: func(t *testing.T, h *harness, result string) {
				table := h.readFile("/work/table.md")
				rows := 0
				for _, line := range strings.Split(table, "\n") {
					if strings.HasPrefix(strings.TrimSpace(line), "|") && !strings.Contains(line, "---") {
						rows++
					}
				}
				if rows < 61 { // header + 60 rows
					t.Errorf("table has %d pipe rows", rows)
				}
				if !strings.Contains(table, "216000") || !strings.Contains(result, "216000") {
					t.Errorf("last cube missing; result %q", result)
				}
			},
		},
		{
			name:           "compaction_at_100k",
			prompt:         "Read /work/big.txt completely with the Read tool in consecutive 200-line chunks (offset 1, limit 200; then offset 201; and so on until the end of the file). Do not use Bash or subagents, and do not ask for confirmation; if a chunk is rejected as too large, halve the chunk size and continue. Then reply exactly in the form `line 3777: <token>` with the token found on line 3777.",
			wantTools:      []string{"Read"},
			tolerateThrash: true,
			check: func(t *testing.T, h *harness, result string) {
				if h.compactions == 0 {
					t.Errorf("no compact_boundary event observed after reading ~88k tokens with a 100k compaction window")
				}
				if h.thrashed {
					return // compaction worked; the thrash guard ending the read loop is accepted
				}
				if !strings.Contains(result, bigLine3777) {
					t.Errorf("line 3777 token %q not in %q", bigLine3777, result)
				}
			},
		},
		{
			name:   "post_compaction_recall",
			prompt: "Without using any tools: what is this project codename, and which file did you create first in this session? Reply in the form `codename=<x> first=<file>`.",
			check: func(t *testing.T, h *harness, result string) {
				if !strings.Contains(strings.ToUpper(result), "ORCHID") {
					t.Errorf("codename lost after compaction: %q", result)
				}
				if !strings.Contains(result, "hello.txt") {
					t.Logf("note: first file not recalled after compaction: %q", result)
				}
			},
		},
	}

	for _, tc := range turns {
		t.Run(tc.name, func(t *testing.T) {
			result, tools := h.turn(t, tc.prompt, true, tc.tolerateThrash)
			if len(tc.wantTools) > 0 && !usedAny(tools, tc.wantTools) {
				t.Errorf("expected one of %v to be used, got %v", tc.wantTools, tools)
			}
			if tc.check != nil {
				tc.check(t, h, result)
			}
		})
	}

	t.Run("memory_recall_fresh_session", func(t *testing.T) {
		result, _ := h.turn(t, "Without using any tools, what is this project's codename? Reply with one word.", false, false)
		if !strings.Contains(strings.ToUpper(result), "ORCHID") {
			t.Errorf("fresh session did not load CLAUDE.md memory: %q", result)
		}
	})

	if h.compactions == 0 {
		t.Errorf("automatic compaction never happened in the shared session")
	}
	if h.turns < 10 {
		t.Errorf("only %d turns ran in the shared session", h.turns)
	}
	t.Logf("shared session %s completed %d turns", h.sessionID, h.turns)
}

// turn runs one prompt. resume=true continues the shared session; the first
// call creates it. resume=false starts a fresh session in the same workspace.
func (h *harness) turn(t *testing.T, prompt string, resume, tolerateThrash bool) (string, []string) {
	t.Helper()
	args := []string{"exec", "-w", "/work"}
	for _, entry := range h.env {
		args = append(args, "-e", entry)
	}
	// The prompt goes through stdin because --disallowedTools is variadic and would swallow it.
	args = append(args, "-i", container, "claude", "-p", "--verbose", "--output-format", "stream-json",
		"--dangerously-skip-permissions", "--disallowedTools", "Artifact")
	if resume && h.sessionID != "" {
		args = append(args, "--resume", h.sessionID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), turnLimit)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = strings.NewReader(prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	started := time.Now()
	err := cmd.Run()
	t.Logf("turn took %s", time.Since(started).Round(time.Second))
	var result string
	var tools []string
	sessionID := ""
	thrashed := false
	for _, line := range bytes.Split(stdout.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var event streamEvent
		if json.Unmarshal(line, &event) != nil {
			continue
		}
		if event.SessionID != "" {
			sessionID = event.SessionID
		}
		switch event.Type {
		case "system":
			if event.Subtype == "compact_boundary" {
				h.compactions++
				t.Logf("compaction observed")
			}
		case "assistant":
			for _, block := range event.Message.Content {
				if block.Type == "tool_use" {
					tools = append(tools, block.Name)
				}
			}
		case "result":
			result = event.Result
			if event.IsError {
				if tolerateThrash && h.compactions > 0 && strings.Contains(event.Result, "Autocompact is thrashing") {
					thrashed = true
					t.Logf("accepted: Claude Code's thrash guard ended the turn after %d compactions", h.compactions)
				} else {
					t.Fatalf("claude reported an error result: %s\nstderr: %s", event.Result, stderr.String())
				}
			}
		}
	}
	h.thrashed = thrashed
	if err != nil && !thrashed {
		t.Fatalf("claude failed: %v\nstdout tail: %s\nstderr: %s", err, tail(stdout.String()), stderr.String())
	}
	if result == "" {
		t.Fatalf("no result event\nstdout tail: %s\nstderr: %s", tail(stdout.String()), stderr.String())
	}
	if resume {
		if h.sessionID == "" {
			h.sessionID = sessionID
		} else if sessionID != "" && sessionID != h.sessionID {
			t.Fatalf("session changed from %s to %s", h.sessionID, sessionID)
		}
		h.turns++
	}
	t.Logf("tools=%v result=%q", tools, truncate(result, 300))
	return result, tools
}

func (h *harness) writeFile(path, content string) {
	h.t.Helper()
	cmd := exec.Command("docker", "exec", "-i", container, "sh", "-c", "mkdir -p \"$(dirname '"+path+"')\" && cat > '"+path+"'")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		h.t.Fatalf("write %s: %v %s", path, err, out)
	}
}

func (h *harness) readFile(path string) string {
	h.t.Helper()
	out, err := exec.Command("docker", "exec", container, "cat", path).CombinedOutput()
	if err != nil {
		h.t.Errorf("read %s: %v %s", path, err, out)
		return ""
	}
	return string(out)
}

func (h *harness) expectFile(path, want string) {
	h.t.Helper()
	if got := strings.TrimSpace(h.readFile(path)); got != want {
		h.t.Errorf("%s = %q, want %q", path, got, want)
	}
}

func expectNumber(t *testing.T, text string, want int) {
	t.Helper()
	matches := regexp.MustCompile(`\d+`).FindAllString(text, -1)
	for _, match := range matches {
		if n, _ := strconv.Atoi(match); n == want {
			return
		}
	}
	t.Errorf("expected %d in %q", want, text)
}

func expectPattern(t *testing.T, text, pattern string) {
	t.Helper()
	if !regexp.MustCompile(pattern).MatchString(text) {
		t.Errorf("expected /%s/ in %q", pattern, text)
	}
}

func usedAny(tools, want []string) bool {
	for _, tool := range tools {
		for _, candidate := range want {
			if tool == candidate {
				return true
			}
		}
	}
	return false
}

// bigFixture is ~270 KB (roughly 88k tokens) of unique lines. Reading it in full
// pushes the session past the 100k compaction window once. It is read in 200-line
// chunks (about 4.4k tokens each) so many reads fit after a compaction; 1000-line
// chunks (about 22k tokens) refill the window within three reads and trip Claude
// Code's autocompact thrash guard. It returns the content and the token on line 3777.
func bigFixture() (string, string) {
	var b strings.Builder
	var token3777 string
	for i := 1; i <= 4000; i++ {
		sum := sha256.Sum256([]byte(fmt.Sprintf("claudex-e2e-%d", i)))
		token := hex.EncodeToString(sum[:8])
		if i == 3777 {
			token3777 = token
		}
		fmt.Fprintf(&b, "%05d %s lorem ipsum dolor sit amet consectetur %d\n", i, token, i*7)
	}
	return b.String(), token3777
}

func fixtureData() string {
	var b strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&b, "row-%02d alpha beta\n", i)
	}
	return b.String()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(wd)
}

// prepareCredentials copies the host auth file and client key into a private
// temporary directory and writes the container config next to them.
func prepareCredentials(t *testing.T) string {
	t.Helper()
	hostConfig := os.Getenv("CLAUDEX_E2E_CONFIG")
	if hostConfig == "" {
		hostConfig = config.DefaultPath()
	}
	host, err := config.Load(hostConfig)
	if err != nil {
		t.Skipf("no usable host claudex config (%v); set CLAUDEX_E2E_CONFIG", err)
	}
	auth, err := os.ReadFile(host.AuthFile)
	if err != nil {
		t.Skipf("no Codex sign-in at %s: %v", host.AuthFile, err)
	}
	key, err := os.ReadFile(host.ClientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp("", "claudex-e2e-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "auth"), 0o700); err != nil {
		t.Fatal(err)
	}
	mounted := filepath.Join(dir, "auth", "codex.json")
	if err := os.WriteFile(mounted, auth, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "client-key"), key, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := host
	cfg.Listen = "127.0.0.1:8317"
	cfg.AuthFile = "/data/auth/codex.json"
	cfg.ClientKeyFile = "/data/client-key"
	if model := os.Getenv("CLAUDEX_E2E_MODEL"); model != "" {
		cfg.Model = model
	}
	if effort := os.Getenv("CLAUDEX_E2E_EFFORT"); effort != "" {
		cfg.ReasoningEffort = effort
	}
	encoded, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	// Hand a refreshed token back to the host if the host copy did not move meanwhile.
	t.Cleanup(func() {
		after, errAfter := os.ReadFile(mounted)
		current, errCurrent := os.ReadFile(host.AuthFile)
		if errAfter != nil || errCurrent != nil || bytes.Equal(after, auth) || !bytes.Equal(current, auth) {
			return
		}
		if err := os.WriteFile(host.AuthFile, after, 0o600); err != nil {
			t.Logf("could not copy refreshed credential back: %v", err)
		} else {
			t.Logf("copied refreshed Codex credential back to %s", host.AuthFile)
		}
	})
	return dir
}

func buildImage(t *testing.T, root string) {
	t.Helper()
	if os.Getenv("CLAUDEX_E2E_SKIP_BUILD") != "" {
		return
	}
	cmd := exec.Command("docker", "build", "-q", "-f", filepath.Join(root, "e2e", "Dockerfile"), "-t", image, root)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
}

func startContainer(t *testing.T, creds string) {
	t.Helper()
	_ = exec.Command("docker", "rm", "-f", container).Run()
	args := []string{"run", "-d", "--name", container, "--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
		"--volume", creds + ":/data", image}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() {
		if t.Failed() {
			logs, _ := exec.Command("docker", "logs", "--tail", "60", container).CombinedOutput()
			t.Logf("container logs:\n%s", logs)
		}
		_ = exec.Command("docker", "rm", "-f", container).Run()
	})
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		logs, _ := exec.Command("docker", "logs", container).CombinedOutput()
		if bytes.Contains(logs, []byte("gateway ready")) {
			return
		}
		if bytes.Contains(logs, []byte("did not become healthy")) || bytes.Contains(logs, []byte("exited before")) {
			t.Fatalf("gateway failed to start:\n%s", logs)
		}
		time.Sleep(500 * time.Millisecond)
	}
	logs, _ := exec.Command("docker", "logs", container).CombinedOutput()
	t.Fatalf("gateway not ready after 60s:\n%s", logs)
}

func tail(s string) string {
	if len(s) > 2000 {
		return "..." + s[len(s)-2000:]
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
