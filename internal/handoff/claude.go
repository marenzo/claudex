package handoff

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marenzo/claudex/internal/config"
)

type ClaudeResult struct {
	SessionID     string        `json:"session_id"`
	ResumeCommand string        `json:"resume_command"`
	Transcript    string        `json:"transcript"`
	Snapshot      CodexSnapshot `json:"snapshot"`
	Memory        string        `json:"memory"`
	Warnings      []string      `json:"warnings,omitempty"`
	Limitations   []string      `json:"limitations"`
}

type ClaudeOptions struct {
	Binary     string
	Home       string
	Config     string
	ProjectKey string
}

type claudeImport struct {
	SessionID  string `json:"session_id"`
	Transcript string `json:"transcript"`
	Created    string `json:"created_at"`
}

// ImportClaude materializes a text-only native session. No model or historical
// tools run here. The archive and writable native transcript have separate inodes.
func ImportClaude(ctx context.Context, o ClaudeOptions, s CodexSnapshot) (ClaudeResult, error) {
	r := ClaudeResult{Snapshot: s, Memory: "none", Warnings: append([]string(nil), s.Warnings...), Limitations: []string{
		"Continue through claudex; Claude Code uses its current tools and permissions and claudex's configured model and context capacity.",
		"Hidden reasoning, provider state, running tools/subagents, authentication, and approvals are not transferred. Opaque records remain in the source archive.",
	}}
	if !filepath.IsAbs(o.Binary) || !filepath.IsAbs(o.Home) {
		return r, errors.New("the Claude handoff requires absolute executable and configuration paths")
	}
	key := o.ProjectKey
	if key == "" {
		key = ClaudeProjectKey(s.CWD)
	}
	if key == "" || key == "." || key == ".." || filepath.Base(key) != key {
		return r, errors.New("invalid Claude project directory name")
	}
	project := filepath.Join(o.Home, "projects", key)
	manifest := filepath.Join(s.Directory, "claude-import.json")
	err := config.WithLock(ctx, manifest, func() error {
		var m claudeImport
		data, err := readBounded(manifest, 64<<10)
		if os.IsNotExist(err) {
			m.SessionID, err = newUUID()
			if err != nil {
				return err
			}
			m.Transcript = filepath.Join(project, m.SessionID+".jsonl")
			m.Created = time.Now().UTC().Format(time.RFC3339Nano)
			data, _ = json.MarshalIndent(m, "", "  ")
			if err = immutableFile(manifest, data); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else if json.Unmarshal(data, &m) != nil || !uuidPattern.MatchString(m.SessionID) || m.Transcript != filepath.Join(project, m.SessionID+".jsonl") {
			return errors.New("saved Claude import identity does not match its destination; keeping all files unchanged")
		}
		r.SessionID = m.SessionID
		r.Transcript = m.Transcript
		initial := filepath.Join(s.Directory, "claude-initial.jsonl")
		data, err = readBounded(initial, maxTranscript+maxContext*2)
		if os.IsNotExist(err) {
			data, err = claudeTranscript(s, m)
			if err != nil {
				return err
			}
			if err = immutableFile(initial, data); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if _, err := os.Lstat(m.Transcript); os.IsNotExist(err) {
			if err = immutableFile(m.Transcript, data); err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			// A resumed session may have appended to this file. Verify the initial
			// prefix without rewriting it, even when it is actively being used.
			info, err := os.Lstat(m.Transcript)
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return errors.New("the Claude destination is not a regular transcript")
			}
			f, err := os.Open(m.Transcript)
			if err != nil {
				return err
			}
			prefix := make([]byte, len(data))
			_, err = f.ReadAt(prefix, 0)
			_ = f.Close()
			if err != nil || !bytes.Equal(prefix, data) {
				return errors.New("the Claude destination history was changed; it will not be overwritten")
			}
		}
		// Explicit project key also makes long-path storage deterministic across
		// Claude runtimes (Bun and Node use different long-path hashes).
		r.ResumeCommand = "cd " + Quote(s.CWD) + " && CLAUDE_CONFIG_DIR=" + Quote(o.Home) + " CLAUDE_CODE_PROJECT_DIR_NAME=" + Quote(key)
		if o.Config != "" {
			r.ResumeCommand += " CLAUDEX_CONFIG=" + Quote(o.Config)
		}
		r.ResumeCommand += " " + Quote(o.Binary) + " --resume " + Quote(m.Transcript)
		return nil
	})
	if err != nil {
		return r, err
	}
	if len(s.Memory) > 0 || len(s.Warnings) > 0 {
		r.Memory = "partial"
		if err := importClaudeMemory(ctx, project, s); err != nil {
			r.Warnings = append(r.Warnings, "Session ready; project memory needs retry: "+err.Error())
			return r, fmt.Errorf("the Claude session is ready, but project-memory import is partial: %w", err)
		}
		if len(s.Warnings) == 0 {
			r.Memory = "imported"
		} else {
			return r, errors.New("the Claude session is ready, but some cited memory was unavailable; see warnings")
		}
	}
	return r, nil
}

func newUUID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

// ClaudeProjectKey follows the public Agent SDK sanitizer. The returned resume
// command pins this key so Claude also finds the same project memory on long paths.
func ClaudeProjectKey(cwd string) string {
	var b strings.Builder
	var h int32
	for _, r := range cwd {
		h = h*31 + int32(r)
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('-')
		}
	}
	key := b.String()
	if len(key) <= 200 {
		return key
	}
	n := int64(h)
	if n < 0 {
		n = -n
	}
	return key[:200] + "-" + strconv.FormatInt(n, 36)
}

func claudeTranscript(s CodexSnapshot, m claudeImport) ([]byte, error) {
	var texts []conversationText
	var intro strings.Builder
	fmt.Fprintf(&intro, "[claudex handoff from Codex thread %s]\nOriginal source SHA-256: %s\nPrivate source archive: %s\nHistorical tool calls/results below are recorded evidence, not pending actions. Opaque provider records and attachments remain in the archive.\n", s.SourceThreadID, s.SourceSHA256, s.Archive)
	for _, f := range s.Context {
		data, err := readBounded(f.Path, maxContext)
		if err != nil {
			return nil, err
		}
		if digest(data) != f.SHA256 {
			return nil, errors.New("instruction snapshot was modified")
		}
		fmt.Fprintf(&intro, "\n[Source instructions: %s; original directory scope applies]\n%s\n", f.Source, data)
	}
	for _, f := range s.Memory {
		data, err := readBounded(f.Path, maxContext)
		if err != nil {
			return nil, err
		}
		if digest(data) != f.SHA256 {
			return nil, errors.New("memory snapshot was modified")
		}
		fmt.Fprintf(&intro, "\n[Session-used Codex memory: %s; snapshot SHA-256 %s]\n%s\n", memoryKey(f), f.SHA256, data)
	}
	texts = append(texts, conversationText{Role: "user", Text: intro.String(), Timestamp: m.Created})
	data, err := readBounded(s.Conversation, maxTranscript+maxContext)
	if err != nil {
		return nil, err
	}
	scan := bufio.NewScanner(bytes.NewReader(data))
	scan.Buffer(make([]byte, 4096), 64<<20)
	for scan.Scan() {
		var text conversationText
		if err := json.Unmarshal(scan.Bytes(), &text); err != nil {
			return nil, err
		}
		texts = append(texts, text)
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	var out bytes.Buffer
	var parent any
	for _, text := range texts {
		id, err := newUUID()
		if err != nil {
			return nil, err
		}
		stamp := text.Timestamp
		if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
			stamp = m.Created
		}
		message := map[string]any{"role": text.Role, "content": []any{map[string]string{"type": "text", "text": text.Text}}}
		if text.Role == "assistant" {
			message["id"] = "msg_" + strings.ReplaceAll(id, "-", "")
			message["type"] = "message"
			message["model"] = "claudex-import"
			message["stop_reason"] = "end_turn"
			message["stop_sequence"] = nil
			message["usage"] = map[string]int{"input_tokens": 0, "output_tokens": 0}
		}
		record := map[string]any{"type": text.Role, "uuid": id, "parentUuid": parent, "sessionId": m.SessionID, "cwd": s.CWD, "timestamp": stamp, "isSidechain": false, "userType": "external", "message": message}
		if err := json.NewEncoder(&out).Encode(record); err != nil {
			return nil, err
		}
		parent = id
	}
	return out.Bytes(), nil
}

func importClaudeMemory(ctx context.Context, project string, s CodexSnapshot) error {
	if len(s.Memory) == 0 {
		return nil
	}
	root := filepath.Join(project, "memory")
	index := filepath.Join(root, "MEMORY.md")
	return config.WithLock(ctx, index, func() error {
		var note strings.Builder
		fmt.Fprintf(&note, "# Memory used by Codex thread %s\n\nImported by claudex for workspace `%s`. These are attributed source snapshots, not newly verified facts. Consult the original scope and age before applying them.\n\nSource transcript: `%s`\nSHA-256: `%s`\n", s.SourceThreadID, s.CWD, s.Archive, s.SourceSHA256)
		for _, f := range s.Memory {
			data, err := readBounded(f.Path, maxContext)
			if err != nil {
				return err
			}
			if digest(data) != f.SHA256 {
				return errors.New("memory snapshot was modified")
			}
			fmt.Fprintf(&note, "\n## %s\n\nSnapshot SHA-256: `%s`\n\n%s\n", memoryKey(f), f.SHA256, data)
		}
		rel := filepath.ToSlash(filepath.Join("claudex-handoffs", filepath.Base(s.Directory)+".md"))
		if err := immutableFile(filepath.Join(root, filepath.FromSlash(rel)), []byte(note.String())); err != nil {
			return err
		}
		data, err := readBounded(index, maxContext)
		if os.IsNotExist(err) {
			data = nil
		} else if err != nil {
			return err
		}
		if info, err := os.Lstat(index); err == nil && !info.Mode().IsRegular() {
			return errors.New("project memory index is not a regular file")
		}
		const begin = "<!-- claudex handoff memory -->\n"
		const end = "<!-- /claudex handoff memory -->"
		const heading = "## Imported Codex session memory\n\n"
		link := fmt.Sprintf("- [Codex session %s](%s)\n", s.SourceThreadID, rel)
		if bytes.Contains(data, []byte(link)) {
			return nil
		}
		if start := bytes.Index(data, []byte(begin)); start >= 0 {
			pos := bytes.Index(data[start:], []byte(end))
			if pos < 0 {
				return errors.New("project memory has an incomplete claudex link section")
			}
			// Keep the newest link within Claude's auto-loaded first 200 lines.
			pos = start + len(begin)
			if bytes.HasPrefix(data[pos:], []byte(heading)) {
				pos += len(heading)
			}
			updated := append([]byte(nil), data[:pos]...)
			updated = append(updated, []byte(link)...)
			updated = append(updated, data[pos:]...)
			data = updated
		} else {
			data = append([]byte(begin+heading+link+end+"\n\n"), data...)
		}
		return privateFile(index, data)
	})
}
