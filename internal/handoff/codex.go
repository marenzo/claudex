package handoff

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type Result struct {
	ThreadID      string   `json:"thread_id"`
	ResumeCommand string   `json:"resume_command"`
	Snapshot      Snapshot `json:"snapshot"`
	Memory        string   `json:"memory"`
	Limitations   []string `json:"limitations"`
}

type rpcMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}
type appServer struct {
	cmd      *exec.Cmd
	input    io.WriteCloser
	output   chan rpcMessage
	failures chan error
	pending  []rpcMessage
	next     int
}

// NativeEnvironment removes claudex's provider routing while retaining native
// Codex configuration and authentication (including CODEX_HOME when selected).
func NativeEnvironment(env []string) []string {
	var out []string
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "ANTHROPIC_") || strings.HasPrefix(name, "CLAUDE_CODE_") || strings.HasPrefix(name, "CLAUDEX_") || name == "API_TIMEOUT_MS" {
			continue
		}
		out = append(out, entry)
	}
	return out
}

func startApp(ctx context.Context, binary, cwd, codexHome string, importMemory bool) (*appServer, error) {
	args := []string{"app-server", "--listen", "stdio://"}
	if importMemory {
		args = append(args, "--enable", "external_agent_memory_import")
	}
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = cwd
	cmd.Env = append(NativeEnvironment(os.Environ()), "CODEX_HOME="+codexHome)
	cmd.Stderr = io.Discard
	input, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	a := &appServer{cmd: cmd, input: input, output: make(chan rpcMessage, 16), failures: make(chan error, 1)}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() {
		scan := bufio.NewScanner(output)
		scan.Buffer(make([]byte, 4096), 16<<20)
		for scan.Scan() {
			var message rpcMessage
			if err := json.Unmarshal(scan.Bytes(), &message); err != nil {
				a.failures <- errors.New("native Codex returned invalid JSON-RPC")
				return
			}
			select {
			case a.output <- message:
			case <-ctx.Done():
				return
			}
		}
		err := scan.Err()
		if err == nil {
			err = errors.New("native Codex app-server closed before completing the handoff")
		}
		a.failures <- err
	}()
	return a, nil
}
func (a *appServer) close()           { _ = a.input.Close(); _ = a.cmd.Process.Kill(); _ = a.cmd.Wait() }
func (a *appServer) send(v any) error { return json.NewEncoder(a.input).Encode(v) }
func (a *appServer) receive(ctx context.Context) (rpcMessage, error) {
	select {
	case m := <-a.output:
		if len(m.ID) > 0 && m.Method != "" { // Import should never need a tool or permission decision.
			_ = a.send(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "Handoff accepts no server-initiated actions"}})
			return rpcMessage{}, errors.New("native Codex requested an unexpected action during local import")
		}
		return m, nil
	case err := <-a.failures:
		return rpcMessage{}, err
	case <-ctx.Done():
		return rpcMessage{}, ctx.Err()
	}
}
func (a *appServer) request(ctx context.Context, method string, params any, out any) error {
	a.next++
	id := a.next
	if err := a.send(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		m, err := a.receive(ctx)
		if err != nil {
			return err
		}
		var received int
		if json.Unmarshal(m.ID, &received) == nil && received == id {
			if m.Error != nil {
				if m.Error.Code == -32601 {
					return errors.New("installed Codex lacks the native import API; update Codex CLI and retry")
				}
				return fmt.Errorf("native Codex %s failed (%d): %s", method, m.Error.Code, m.Error.Message)
			}
			if out != nil {
				return json.Unmarshal(m.Result, out)
			}
			return nil
		}
		if m.Method == "externalAgentConfig/import/completed" {
			a.pending = append(a.pending, m)
		}
	}
}

type importCompleted struct {
	ImportID string `json:"importId"`
	Results  []struct {
		ItemType  string `json:"itemType"`
		Successes []struct {
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"successes"`
		Failures []struct {
			Message string `json:"message"`
		} `json:"failures"`
	} `json:"itemTypeResults"`
}

func (a *appServer) importItems(ctx context.Context, items []any) (importCompleted, error) {
	var requested struct {
		ID string `json:"importId"`
	}
	var completed importCompleted
	if err := a.request(ctx, "externalAgentConfig/import", map[string]any{"migrationSource": "claude", "source": "claudex", "migrationItems": items}, &requested); err != nil {
		return completed, err
	}
	if requested.ID == "" {
		return completed, errors.New("native Codex did not identify its import operation")
	}
	for {
		var m rpcMessage
		if len(a.pending) > 0 {
			m = a.pending[0]
			a.pending = a.pending[1:]
		} else {
			var err error
			m, err = a.receive(ctx)
			if err != nil {
				return completed, err
			}
		}
		if m.Method != "externalAgentConfig/import/completed" {
			continue
		}
		if err := json.Unmarshal(m.Params, &completed); err != nil {
			return completed, err
		}
		if completed.ImportID == requested.ID {
			return completed, nil
		}
	}
}

func Import(ctx context.Context, binary, codexHome string, s Snapshot) (Result, error) {
	result := Result{Snapshot: s, Memory: "none", Limitations: []string{
		"Native Codex chooses its model, context window, compaction, tools, authentication, and permissions; active Claude processes and approvals are not migrated.",
		"Full text and tool evidence are imported; opaque reasoning and unsupported attachments remain in the private transcript archive, not active model state.",
	}}
	if s.MemoryFiles > 0 {
		result.Memory = "snapshotted_only"
	}
	if err := os.MkdirAll(codexHome, 0700); err != nil {
		return result, err
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	a, err := startApp(ctx, binary, s.CWD, codexHome, s.MemoryProject != "")
	if err != nil {
		return result, err
	}
	defer a.close()
	if err := a.request(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "claudex_handoff", "title": "claudex handoff", "version": "1"}, "capabilities": map[string]bool{"experimentalApi": true}}, nil); err != nil {
		return result, err
	}
	if err := a.send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return result, err
	}
	items := []any{map[string]any{"itemType": "SESSIONS", "description": "Continue Claude Code work in native Codex", "cwd": nil, "details": map[string]any{"sessions": []any{map[string]any{"path": s.Path, "cwd": s.CWD, "title": nil}}}}}
	completed, err := a.importItems(ctx, items)
	if err != nil {
		return result, err
	}
	for _, r := range completed.Results {
		if r.ItemType != "SESSIONS" {
			continue
		}
		if len(r.Failures) > 0 {
			return result, fmt.Errorf("native Codex session import failed: %s", r.Failures[0].Message)
		}
		for _, success := range r.Successes {
			if success.Source == s.Path && uuidPattern.MatchString(success.Target) {
				result.ThreadID = success.Target
			}
		}
	}
	if result.ThreadID == "" {
		result.ThreadID = ledgerThread(codexHome, s.Path)
	}
	if result.ThreadID == "" {
		return result, errors.New("native Codex completed import without a verified thread; the private snapshot is preserved for retry")
	}
	// Check the exact saved thread, never the most recently created session.
	var read struct {
		Thread struct {
			ID  string `json:"id"`
			CWD string `json:"cwd"`
		} `json:"thread"`
	}
	if err := a.request(ctx, "thread/read", map[string]any{"threadId": result.ThreadID, "includeTurns": false}, &read); err != nil {
		return result, err
	}
	threadCWD, pathErr := filepath.EvalSymlinks(read.Thread.CWD)
	if pathErr != nil || read.Thread.ID != result.ThreadID || threadCWD != s.CWD {
		return result, errors.New("imported Codex thread identity or workspace did not match")
	}
	result.ResumeCommand = "codex resume " + result.ThreadID + " --cd " + Quote(s.CWD)
	if s.MemoryProject != "" {
		result.Memory = "snapshotted_only"
		completed, err = a.importItems(ctx, []any{map[string]any{"itemType": "MEMORY", "description": "Current Claude project memory", "cwd": nil, "details": map[string]any{"memory": []string{s.MemoryProject}}}})
		if err != nil {
			return result, fmt.Errorf("thread is ready, but native project-memory import failed: %w", err)
		}
		for _, r := range completed.Results {
			if r.ItemType == "MEMORY" {
				if len(r.Failures) > 0 {
					return result, fmt.Errorf("thread is ready, but native project-memory import failed: %s", r.Failures[0].Message)
				}
				if len(r.Successes) > 0 {
					result.Memory = "imported"
				}
			}
		}
		if result.Memory != "imported" {
			return result, errors.New("thread is ready with memory snapshots, but Codex did not confirm native memory import")
		}
	}
	return result, nil
}

func ledgerThread(home, source string) string {
	data, err := readBounded(filepath.Join(home, "external_agent_session_imports.json"), 64<<20)
	if err != nil {
		return ""
	}
	var ledger struct {
		Records []struct {
			Source string `json:"source_path"`
			Hash   string `json:"content_sha256"`
			ID     string `json:"imported_thread_id"`
		} `json:"records"`
	}
	if json.Unmarshal(data, &ledger) != nil {
		return ""
	}
	raw, err := readBounded(source, maxTranscript+maxContext*2)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(raw)
	for i := len(ledger.Records) - 1; i >= 0; i-- {
		r := ledger.Records[i]
		if r.Source == source && r.Hash == hex.EncodeToString(digest[:]) && uuidPattern.MatchString(r.ID) {
			return r.ID
		}
	}
	return ""
}
