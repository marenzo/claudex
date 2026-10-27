package handoff

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// CodexSnapshot is a content-addressed, local-only view of one saved main thread.
// Provider records remain in Archive; Conversation contains only transferable text.
type CodexSnapshot struct {
	Source         string        `json:"source"`
	SourceSHA256   string        `json:"source_sha256"`
	SourceThreadID string        `json:"source_thread_id"`
	CWD            string        `json:"cwd"`
	Directory      string        `json:"directory"`
	Archive        string        `json:"archive"`
	Conversation   string        `json:"conversation"`
	Messages       int           `json:"messages"`
	OpaqueBlocks   int           `json:"opaque_blocks"`
	Context        []ContextFile `json:"context_files"`
	Memory         []ContextFile `json:"memory_files"`
	Warnings       []string      `json:"warnings,omitempty"`
}

type ContextFile struct {
	Source string `json:"source"`
	Path   string `json:"snapshot_path"`
	SHA256 string `json:"sha256"`
	Start  int    `json:"start_line,omitempty"`
	End    int    `json:"end_line,omitempty"`
}

type conversationText struct {
	Role      string `json:"role"`
	Text      string `json:"text"`
	Timestamp string `json:"timestamp,omitempty"`
	Citations bool   `json:"-"`
}

type rolloutRecord struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// ResolveCodexSource never guesses the newest session. A thread selection must
// match exactly one saved rollout, whose metadata is checked again by PrepareCodex.
func ResolveCodexSource(source, threadID, codexHome string) (string, error) {
	if source != "" && threadID != "" {
		return "", errors.New("choose either --source or --thread-id")
	}
	if source != "" {
		if !filepath.IsAbs(source) || filepath.Ext(source) != ".jsonl" {
			return "", errors.New("--source must be an absolute Codex rollout JSONL path")
		}
		return filepath.EvalSymlinks(source)
	}
	if !uuidPattern.MatchString(threadID) {
		return "", errors.New("no exact Codex thread binding; use --thread-id UUID or --source /absolute/rollout.jsonl")
	}
	var matches []string
	for _, name := range []string{"sessions", "archived_sessions"} {
		root := filepath.Join(codexHome, name)
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) && path == root {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			base := strings.ToLower(d.Name())
			id := strings.ToLower(threadID)
			if strings.HasSuffix(base, "-"+id+".jsonl") || base == id+".jsonl" {
				matches = append(matches, path)
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("exact Codex thread %s matched %d saved rollouts; select an explicit --source", threadID, len(matches))
	}
	return filepath.EvalSymlinks(matches[0])
}

// PrepareCodex accepts complete JSONL snapshots only. Mirrored UI events are not
// conversation records. Rollbacks remove turns; compaction checkpoints annotate
// the original text without replaying replacement_history over it.
func PrepareCodex(source, threadID, codexHome, claudeHome string) (CodexSnapshot, error) {
	var s CodexSnapshot
	var err error
	codexHome, err = filepath.Abs(codexHome)
	if err != nil {
		return s, err
	}
	claudeHome, err = filepath.Abs(claudeHome)
	if err != nil {
		return s, err
	}
	s.Source, err = ResolveCodexSource(source, threadID, codexHome)
	if err != nil {
		return s, err
	}
	raw, err := readBounded(s.Source, maxTranscript)
	if err != nil {
		return s, err
	}
	s.SourceSHA256 = digest(raw)
	texts, cwd, id, opaque, err := parseRollout(raw)
	if err != nil {
		return s, err
	}
	if threadID != "" && !strings.EqualFold(threadID, id) {
		return s, errors.New("rollout metadata does not match the selected Codex thread")
	}
	s.SourceThreadID = id
	if !filepath.IsAbs(cwd) {
		return s, errors.New("the Codex rollout has no absolute workspace")
	}
	s.CWD, err = filepath.EvalSymlinks(cwd)
	if err != nil {
		return s, err
	}
	info, err := os.Stat(s.CWD)
	if err != nil || !info.IsDir() {
		return s, errors.New("the Codex workspace is unavailable")
	}
	s.Messages = len(texts)
	s.OpaqueBlocks = opaque
	if s.Messages == 0 {
		return s, errors.New("the Codex rollout contains no importable conversation")
	}
	contextData := map[string][]byte{}
	files, err := codexInstructions(codexHome, s.CWD)
	if err != nil {
		return s, err
	}
	total := 0
	for _, path := range files {
		data, err := readBounded(path, maxContext-total)
		if err != nil {
			return s, err
		}
		total += len(data)
		s.Context = append(s.Context, ContextFile{Source: path, SHA256: digest(data)})
		contextData[path] = data
	}
	refs := memoryReferences(texts)
	if len(refs)+len(files) > 1000 {
		return s, errors.New("handoff context exceeds 1000 files or citation ranges")
	}
	for _, ref := range refs {
		data, err := readMemoryRange(codexHome, ref)
		if err != nil {
			s.Warnings = append(s.Warnings, fmt.Sprintf("Memory %s:%d-%d could not be snapshotted: %v", ref.Source, ref.Start, ref.End, err))
			continue
		}
		total += len(data)
		if total > maxContext {
			return s, errors.New("handoff context exceeds 16 MiB")
		}
		ref.SHA256 = digest(data)
		s.Memory = append(s.Memory, ref)
		contextData[memoryKey(ref)] = data
	}
	// Context bytes, including citation ranges, participate in snapshot identity.
	var identity bytes.Buffer
	identity.WriteString("claudex-codex-to-cc-v1\x00" + s.Source + "\x00" + s.CWD + "\x00")
	identity.Write(raw)
	for _, f := range s.Context {
		identity.WriteString(f.Source + "\x00" + f.SHA256)
	}
	for _, f := range s.Memory {
		identity.WriteString(memoryKey(f) + "\x00" + f.SHA256)
	}
	for _, w := range s.Warnings {
		identity.WriteString(w)
	}
	s.Directory = filepath.Join(claudeHome, "projects", "claudex-handoffs", digest(identity.Bytes()))
	s.Archive = filepath.Join(s.Directory, "transcript.jsonl")
	s.Conversation = filepath.Join(s.Directory, "conversation.jsonl")
	if err := immutableFile(s.Archive, raw); err != nil {
		return s, err
	}
	var conversation bytes.Buffer
	for _, text := range texts {
		if err := json.NewEncoder(&conversation).Encode(text); err != nil {
			return s, err
		}
	}
	if err := immutableFile(s.Conversation, conversation.Bytes()); err != nil {
		return s, err
	}
	for i := range s.Context {
		f := &s.Context[i]
		f.Path = filepath.Join(s.Directory, "context", fmt.Sprintf("%04d.md", i))
		if err := immutableFile(f.Path, contextData[f.Source]); err != nil {
			return s, err
		}
	}
	for i := range s.Memory {
		f := &s.Memory[i]
		f.Path = filepath.Join(s.Directory, "memory", fmt.Sprintf("%04d.md", i))
		if err := immutableFile(f.Path, contextData[memoryKey(*f)]); err != nil {
			return s, err
		}
	}
	manifest, _ := json.MarshalIndent(s, "", "  ")
	if err := immutableFile(filepath.Join(s.Directory, "snapshot.json"), manifest); err != nil {
		return s, err
	}
	return s, nil
}

func parseRollout(raw []byte) ([]conversationText, string, string, int, error) {
	var texts []conversationText
	cwd, id := "", ""
	opaque := 0
	type boundary struct {
		start   int
		cwd, id string
	}
	var turns []boundary
	turnID := ""
	explicitTurns := false
	begin := func(next string) {
		if next != "" && next == turnID {
			return
		}
		turns = append(turns, boundary{len(texts), cwd, next})
		turnID = next
	}
	scan := bufio.NewScanner(bytes.NewReader(raw))
	scan.Buffer(make([]byte, 4096), 64<<20)
	line := 0
	for scan.Scan() {
		line++
		if len(bytes.TrimSpace(scan.Bytes())) == 0 {
			continue
		}
		var r rolloutRecord
		if err := json.Unmarshal(scan.Bytes(), &r); err != nil {
			return nil, "", "", 0, fmt.Errorf("the Codex rollout line %d is incomplete or invalid; wait for the writer and retry: %w", line, err)
		}
		var p struct {
			ID, CWD, Message string
			Replacement      []json.RawMessage `json:"replacement_history"`
		}
		// The wire uses snake_case for event fields.
		var event struct {
			Type     string `json:"type"`
			TurnID   string `json:"turn_id"`
			NumTurns int    `json:"num_turns"`
		}
		if r.Type == "session_meta" || r.Type == "turn_context" || r.Type == "compacted" {
			if err := json.Unmarshal(r.Payload, &p); err != nil {
				return nil, "", "", 0, fmt.Errorf("invalid Codex payload at line %d", line)
			}
		}
		if r.Type == "event_msg" || r.Type == "turn_context" {
			if err := json.Unmarshal(r.Payload, &event); err != nil {
				return nil, "", "", 0, fmt.Errorf("invalid Codex event at line %d: %w", line, err)
			}
		}
		switch r.Type {
		case "session_meta":
			if id != "" || !uuidPattern.MatchString(p.ID) || !filepath.IsAbs(p.CWD) {
				return nil, "", "", 0, errors.New("invalid or duplicate Codex session metadata")
			}
			id = p.ID
			cwd = p.CWD
		case "turn_context":
			if event.TurnID != "" {
				explicitTurns = true
				begin(event.TurnID)
			}
			if p.CWD != "" {
				cwd = p.CWD
			}
		case "event_msg":
			switch event.Type {
			case "task_started", "turn_started":
				if event.TurnID != "" {
					explicitTurns = true
					begin(event.TurnID)
				}
			case "thread_rolled_back":
				if event.NumTurns < 0 || event.NumTurns > len(turns) {
					return nil, "", "", 0, errors.New("the Codex rollback exceeds recorded turns; cannot determine active history")
				}
				if event.NumTurns > 0 {
					b := turns[len(turns)-event.NumTurns]
					texts = texts[:b.start]
					cwd = b.cwd
					turns = turns[:len(turns)-event.NumTurns]
					turnID = ""
					if len(turns) > 0 {
						turnID = turns[len(turns)-1].id
					}
				}
			}
		case "response_item":
			item, hidden, err := rolloutText(r.Payload, r.Timestamp, line)
			if err != nil {
				return nil, "", "", 0, err
			}
			opaque += hidden
			if item != nil {
				if item.Role == "user" && !explicitTurns {
					begin("")
				}
				texts = append(texts, *item)
			}
		case "compacted":
			if len(texts) == 0 {
				for _, replacement := range p.Replacement {
					item, hidden, err := rolloutText(replacement, r.Timestamp, line)
					if err != nil {
						return nil, "", "", 0, err
					}
					opaque += hidden
					if item != nil {
						texts = append(texts, *item)
					}
				}
			}
			if p.Message != "" {
				texts = append(texts, conversationText{Role: "assistant", Text: "[Codex compaction checkpoint; original recorded history retained]\n" + p.Message, Timestamp: r.Timestamp})
			}
		case "inter_agent_communication":
			var communication struct{ Author, Recipient, Content string }
			if err := json.Unmarshal(r.Payload, &communication); err != nil {
				return nil, "", "", 0, err
			}
			if communication.Content != "" {
				texts = append(texts, conversationText{Role: "assistant", Text: fmt.Sprintf("[Recorded agent message from %s to %s]\n%s", communication.Author, communication.Recipient, communication.Content), Timestamp: r.Timestamp})
			}
		case "world_state", "token_usage_record", "retained_context", "inter_agent_communication_metadata", "security_risk_score", "realtime_item": // Runtime metadata is archive-only.
		default:
			return nil, "", "", 0, fmt.Errorf("unsupported Codex rollout record %q at line %d; update claudex before importing", r.Type, line)
		}
	}
	if err := scan.Err(); err != nil {
		return nil, "", "", 0, err
	}
	if id == "" {
		return nil, "", "", 0, errors.New("source is not a native Codex rollout (missing session_meta)")
	}
	return texts, cwd, id, opaque, nil
}

func rolloutText(raw json.RawMessage, timestamp string, line int) (*conversationText, int, error) {
	var p struct {
		Type      string          `json:"type"`
		Role      string          `json:"role"`
		Author    string          `json:"author"`
		Recipient string          `json:"recipient"`
		Content   json.RawMessage `json:"content"`
		Output    json.RawMessage `json:"output"`
		Arguments json.RawMessage `json:"arguments"`
		Input     string          `json:"input"`
		Action    json.RawMessage `json:"action"`
		Name      string          `json:"name"`
		Namespace string          `json:"namespace"`
		CallID    string          `json:"call_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, 0, err
	}
	text := ""
	role := "assistant"
	hidden := 0
	citations := false
	switch p.Type {
	case "message":
		if p.Role != "user" && p.Role != "assistant" {
			return nil, 0, nil
		}
		role = p.Role
		citations = role == "assistant"
		text, hidden = codexContent(p.Content, line)
	case "agent_message":
		content, count := codexContent(p.Content, line)
		hidden = count
		text = fmt.Sprintf("[Recorded agent message from %s to %s]\n%s", p.Author, p.Recipient, content)
	case "function_call", "custom_tool_call", "local_shell_call", "tool_search_call":
		arguments := p.Arguments
		if p.Type == "local_shell_call" {
			arguments = p.Action
		}
		body := string(arguments)
		var decoded string
		if json.Unmarshal(arguments, &decoded) == nil {
			body = decoded
		}
		if p.Type == "custom_tool_call" {
			body = p.Input
		}
		name := p.Name
		if p.Namespace != "" {
			name = p.Namespace + "." + name
		}
		if name == "" {
			name = p.Type
		}
		text = fmt.Sprintf("[Recorded Codex tool call: %s; call_id=%s]\n%s", name, p.CallID, body)
	case "function_call_output", "custom_tool_call_output":
		body, count := codexContent(p.Output, line)
		hidden = count
		text = fmt.Sprintf("[Recorded Codex tool result; call_id=%s]\n%s", p.CallID, body)
	case "web_search_call", "image_generation_call", "tool_search_output":
		// Action/results are textual evidence. Encrypted provider state is never replayed.
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		delete(fields, "encrypted_content")
		delete(fields, "internal_chat_message_metadata_passthrough")
		if p.Type == "image_generation_call" {
			delete(fields, "result")
			fields["archive_reference"], _ = json.Marshal(fmt.Sprintf("Image bytes remain in source archive, line %d", line))
			hidden++
		}
		data, _ := json.Marshal(fields)
		text = "[Recorded Codex " + p.Type + "]\n" + string(data)
	case "reasoning", "additional_tools", "compaction", "compaction_summary", "configuration_update", "compaction_trigger", "context_compaction":
		return nil, 1, nil
	default:
		return nil, 0, fmt.Errorf("unsupported Codex response item %q at line %d; update claudex before importing", p.Type, line)
	}
	if text == "" {
		return nil, hidden, nil
	}
	return &conversationText{Role: role, Text: text, Timestamp: timestamp, Citations: citations}, hidden, nil
}

func codexContent(raw json.RawMessage, line int) (string, int) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, 0
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return fmt.Sprintf("[Opaque content in source archive, line %d]", line), 1
	}
	var parts []string
	opaque := 0
	for _, b := range blocks {
		switch b.Type {
		case "input_text", "output_text", "text":
			parts = append(parts, b.Text)
		default:
			opaque++
			parts = append(parts, fmt.Sprintf("[Opaque %s content in source archive, line %d]", b.Type, line))
		}
	}
	return strings.Join(parts, "\n"), opaque
}

func codexInstructions(home, cwd string) ([]string, error) {
	var dirs []string
	for dir := cwd; ; dir = filepath.Dir(dir) {
		dirs = append([]string{dir}, dirs...)
		if filepath.Dir(dir) == dir {
			break
		}
	}
	dirs = append([]string{home}, dirs...)
	var files []string
	seen := map[string]bool{}
	for _, dir := range dirs {
		for _, name := range []string{"AGENTS.override.md", "AGENTS.md"} {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if os.IsNotExist(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if !info.Mode().IsRegular() {
				return nil, fmt.Errorf("instruction is not a regular file: %s", path)
			}
			if !seen[path] {
				files = append(files, path)
				seen[path] = true
			}
			break
		}
	}
	return files, nil
}

var citationBlocks = regexp.MustCompile(`(?s)<oai-mem-citation>.*?<citation_entries>(.*?)</citation_entries>.*?</oai-mem-citation>`)
var citationLine = regexp.MustCompile(`^(.+?):([0-9]+)-([0-9]+)\|note=\[.*\]$`)

func memoryKey(f ContextFile) string { return fmt.Sprintf("%s:%d-%d", f.Source, f.Start, f.End) }
func memoryReferences(texts []conversationText) []ContextFile {
	var refs []ContextFile
	seen := map[string]bool{}
	for _, text := range texts {
		if !text.Citations {
			continue
		}
		for _, block := range citationBlocks.FindAllStringSubmatch(text.Text, -1) {
			for _, line := range strings.Split(block[1], "\n") {
				m := citationLine.FindStringSubmatch(strings.TrimSpace(line))
				if m == nil {
					continue
				}
				start, _ := strconv.Atoi(m[2])
				end, _ := strconv.Atoi(m[3])
				f := ContextFile{Source: m[1], Start: start, End: end}
				key := memoryKey(f)
				if !seen[key] {
					seen[key] = true
					refs = append(refs, f)
				}
			}
		}
	}
	return refs
}

func readMemoryRange(home string, f ContextFile) ([]byte, error) {
	root, err := filepath.EvalSymlinks(filepath.Join(home, "memories"))
	if err != nil {
		return nil, err
	}
	if filepath.IsAbs(f.Source) || filepath.Ext(f.Source) != ".md" || f.Start < 1 || f.End < f.Start {
		return nil, errors.New("invalid memory citation")
	}
	path, err := filepath.EvalSymlinks(filepath.Join(root, f.Source))
	if err != nil {
		return nil, err
	}
	if !within(root, path) {
		return nil, errors.New("memory citation escapes the selected Codex memory directory")
	}
	data, err := readBounded(path, maxContext)
	if err != nil {
		return nil, err
	}
	lines := bytes.Split(data, []byte("\n"))
	if f.End > len(lines) {
		return nil, errors.New("cited lines are no longer available")
	}
	return bytes.Join(lines[f.Start-1:f.End], []byte("\n")), nil
}
