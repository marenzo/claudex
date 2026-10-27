package handoff

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const sourceUUID = "12345678-1234-4234-8234-123456789abc"

func codexFixture(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	home, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	cwd := filepath.Join(home, "work 'space")
	for _, dir := range []string{cwd, filepath.Join(home, ".codex", "sessions"), filepath.Join(home, ".codex", "memories")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeTestFile(t, filepath.Join(cwd, "AGENTS.md"), "REPO_GUIDANCE_9031\n")
	writeTestFile(t, filepath.Join(home, ".codex", "AGENTS.md"), "GLOBAL_GUIDANCE_9031\n")
	writeTestFile(t, filepath.Join(home, ".codex", "memories", "MEMORY.md"), "unrelated memory\nSESSION_MEMORY_9031\nmore unrelated memory\n")
	writeTestFile(t, filepath.Join(home, ".codex", "memories", "uncited.md"), "MUST_NOT_COPY\n")
	source := filepath.Join(home, ".codex", "sessions", "rollout-fixture-"+sourceUUID+".jsonl")
	var b bytes.Buffer
	emit := func(kind string, p any) {
		t.Helper()
		if err := json.NewEncoder(&b).Encode(map[string]any{"type": kind, "timestamp": "2026-10-27T20:00:00Z", "payload": p}); err != nil {
			t.Fatal(err)
		}
	}
	message := func(role, text string) {
		emit("response_item", map[string]any{"type": "message", "role": role, "content": []any{map[string]string{"type": "input_text", "text": text}}})
	}
	emit("session_meta", map[string]string{"id": sourceUUID, "cwd": cwd, "cli_version": "0.164.0"})
	emit("event_msg", map[string]string{"type": "task_started", "turn_id": "one"})
	emit("turn_context", map[string]string{"turn_id": "one", "cwd": cwd})
	message("developer", "PROVIDER_POLICY_MUST_NOT_REPLAY")
	message("user", "orchard-731")
	emit("response_item", map[string]any{"type": "function_call", "name": "exec_command", "call_id": "call-one", "arguments": strings.Repeat("input ", 2000) + "TOOL_INPUT_TAIL"})
	emit("response_item", map[string]any{"type": "function_call_output", "call_id": "call-one", "output": strings.Repeat("output ", 2000) + "TOOL_RESULT_TAIL"})
	emit("response_item", map[string]any{"type": "reasoning", "content": []any{map[string]string{"type": "reasoning_text", "text": "PRIVATE_REASONING"}}})
	message("assistant", "Saved observation.\n<oai-mem-citation>\n<citation_entries>\nMEMORY.md:2-2|note=[test observation]\n</citation_entries>\n<rollout_ids></rollout_ids>\n</oai-mem-citation>")
	emit("event_msg", map[string]string{"type": "agent_message", "message": "MIRROR_MUST_NOT_DUPLICATE"})
	emit("compacted", map[string]any{"message": "COMPACTION_CHECKPOINT", "replacement_history": []any{map[string]any{"type": "message", "role": "user", "content": []any{map[string]string{"type": "input_text", "text": "orchard-731"}}}}})
	emit("event_msg", map[string]string{"type": "task_started", "turn_id": "two"})
	message("user", "ROLLED_BACK_USER")
	message("assistant", "ROLLED_BACK_ASSISTANT")
	emit("event_msg", map[string]any{"type": "thread_rolled_back", "num_turns": 1})
	emit("event_msg", map[string]string{"type": "task_started", "turn_id": "three"})
	message("user", "Continue the task.")
	message("assistant", "Work remains.")
	writeTestFile(t, source, b.String())
	return home, cwd, source
}

func writeTestFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCodexSnapshotActiveHistoryAndContext(t *testing.T) {
	home, cwd, source := codexFixture(t)
	s, err := PrepareCodex("", sourceUUID, filepath.Join(home, ".codex"), filepath.Join(home, ".claude"))
	if err != nil {
		t.Fatal(err)
	}
	if s.CWD != cwd || s.Source != source || len(s.Memory) != 1 || len(s.Context) != 2 {
		t.Fatalf("bad snapshot: %+v", s)
	}
	data, _ := os.ReadFile(s.Conversation)
	for _, want := range []string{"TOOL_INPUT_TAIL", "TOOL_RESULT_TAIL", "COMPACTION_CHECKPOINT", "Work remains."} {
		if !bytes.Contains(data, []byte(want)) {
			t.Fatalf("lost %s", want)
		}
	}
	for _, unwanted := range []string{"ROLLED_BACK_", "MIRROR_", "PRIVATE_REASONING", "PROVIDER_POLICY_"} {
		if bytes.Contains(data, []byte(unwanted)) {
			t.Fatalf("replayed %s", unwanted)
		}
	}
	if bytes.Count(data, []byte("orchard-731")) != 1 {
		t.Fatal("duplicated compaction history")
	}
	memory, _ := os.ReadFile(s.Memory[0].Path)
	if string(memory) != "SESSION_MEMORY_9031" {
		t.Fatalf("copied more than cited range: %s", memory)
	}
	raw, _ := os.ReadFile(source)
	archive, _ := os.ReadFile(s.Archive)
	if !bytes.Equal(raw, archive) {
		t.Fatal("source archive changed")
	}
	second, err := PrepareCodex(source, "", filepath.Join(home, ".codex"), filepath.Join(home, ".claude"))
	if err != nil || s.Directory != second.Directory {
		t.Fatal("unstable snapshot", err)
	}
	writeTestFile(t, filepath.Join(cwd, "AGENTS.override.md"), "OVERRIDE_GUIDANCE")
	changed, err := PrepareCodex(source, "", filepath.Join(home, ".codex"), filepath.Join(home, ".claude"))
	if err != nil || changed.Directory == s.Directory {
		t.Fatal("changed instructions must create new snapshot", err)
	}
}

func TestCodexSourceSelectionAndMalformedRecords(t *testing.T) {
	home, _, source := codexFixture(t)
	codex := filepath.Join(home, ".codex")
	for _, args := range [][2]string{{"", ""}, {source, sourceUUID}, {"relative.jsonl", ""}, {"", "not-a-uuid"}} {
		if _, err := ResolveCodexSource(args[0], args[1], codex); err == nil {
			t.Fatal("accepted ambiguous selection", args)
		}
	}
	raw, _ := os.ReadFile(source)
	writeTestFile(t, filepath.Join(codex, "archived_sessions", "duplicate-"+sourceUUID+".jsonl"), string(raw))
	if _, err := ResolveCodexSource("", sourceUUID, codex); err == nil {
		t.Fatal("guessed among duplicate sources")
	}
	if _, err := PrepareCodex(source, "ffffffff-ffff-4fff-8fff-ffffffffffff", codex, filepath.Join(home, ".claude")); err == nil {
		t.Fatal("accepted conflicting selectors")
	}
	for _, suffix := range []string{`{"type":`, "{\"type\":\"future_record\",\"payload\":{}}\n"} {
		writeTestFile(t, source, string(raw)+suffix)
		if _, err := PrepareCodex(source, "", codex, filepath.Join(home, ".claude")); err == nil {
			t.Fatal("accepted invalid/unsupported source")
		}
	}
}

func TestMemoryCitationsAreBoundedAndScoped(t *testing.T) {
	home, _, _ := codexFixture(t)
	codex := filepath.Join(home, ".codex")
	outside := filepath.Join(home, "outside.md")
	writeTestFile(t, outside, "SECRET_OUTSIDE")
	if err := os.Symlink(outside, filepath.Join(codex, "memories", "escape.md")); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []ContextFile{{Source: "escape.md", Start: 1, End: 1}, {Source: "../../outside.md", Start: 1, End: 1}, {Source: outside, Start: 1, End: 1}, {Source: "MEMORY.md", Start: 0, End: 1}, {Source: "MEMORY.md", Start: 1, End: 1000}} {
		if _, err := readMemoryRange(codex, ref); err == nil {
			t.Fatal("accepted invalid memory citation", ref)
		}
	}
	text := conversationText{Role: "user", Text: "<oai-mem-citation><citation_entries>\nMEMORY.md:1-1|note=[test]\n</citation_entries></oai-mem-citation>"}
	if len(memoryReferences([]conversationText{text})) != 0 {
		t.Fatal("user text selected global memory")
	}
}

func TestCodexSnapshotMissingMemoryStillResumes(t *testing.T) {
	home, _, source := codexFixture(t)
	if err := os.Remove(filepath.Join(home, ".codex", "memories", "MEMORY.md")); err != nil {
		t.Fatal(err)
	}
	s, err := PrepareCodex(source, "", filepath.Join(home, ".codex"), filepath.Join(home, ".claude"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := ImportClaude(context.Background(), ClaudeOptions{Binary: "/test/claudex", Home: filepath.Join(home, ".claude")}, s)
	if err == nil || r.Memory != "partial" || r.ResumeCommand == "" || len(r.Warnings) == 0 {
		t.Fatalf("missing partial receipt: %+v %v", r, err)
	}
}

func TestCodexOpaqueAndDelegationEvidence(t *testing.T) {
	for _, record := range []string{
		`{"type":"custom_tool_call","name":"apply_patch","call_id":"patch","input":"CUSTOM_INPUT_TAIL"}`,
		`{"type":"custom_tool_call_output","call_id":"patch","output":[{"type":"text","text":"CUSTOM_OUTPUT_TAIL"},{"type":"image","data":"OPAQUE_IMAGE_BYTES"}]}`,
		`{"type":"agent_message","author":"/root/worker","recipient":"/root","content":[{"type":"input_text","text":"DELEGATION_REPORT"}]}`,
		`{"type":"image_generation_call","revised_prompt":"an image","result":"OPAQUE_IMAGE_BYTES"}`,
	} {
		item, _, err := rolloutText(json.RawMessage(record), "", 2)
		if err != nil || item == nil {
			t.Fatal("lost recorded evidence", err)
		}
		if strings.Contains(item.Text, "OPAQUE_IMAGE_BYTES") {
			t.Fatal("replayed opaque bytes")
		}
	}
	key := ClaudeProjectKey("/" + strings.Repeat("long-path/", 30) + "Unicode-λ")
	if len(key) > 215 || strings.Contains(key, "/") {
		t.Fatal("invalid long project key", key)
	}
	env := ClaudeEnvironment([]string{"CODEX_THREAD_ID=old", "CLAUDEX_CONFIG=relative.json", "CLAUDE_CONFIG_DIR=relative", "PATH=/bin", "CODEX_HOME=/native"})
	if strings.Join(env, "\n") != "PATH=/bin\nCODEX_HOME=/native" {
		t.Fatal("stale source/destination bindings survived launch", env)
	}
}

func TestClaudeImportRetryMemoryRepairAndIndependentArchive(t *testing.T) {
	home, _, source := codexFixture(t)
	claudeHome := filepath.Join(home, ".claude")
	s, err := PrepareCodex(source, "", filepath.Join(home, ".codex"), claudeHome)
	if err != nil {
		t.Fatal(err)
	}
	opts := ClaudeOptions{Binary: "/test/claudex 'binary", Home: claudeHome, Config: "/test/config file.json"}
	project := filepath.Join(claudeHome, "projects", ClaudeProjectKey(s.CWD))
	index := filepath.Join(project, "memory", "MEMORY.md")
	original := "# My memory\n\n" + strings.Repeat("existing user note\n", 250)
	writeTestFile(t, index, original)
	// A filesystem conflict makes memory partial, while the session stays usable.
	writeTestFile(t, filepath.Join(project, "memory", "claudex-handoffs"), "user-owned conflict")
	r, err := ImportClaude(context.Background(), opts, s)
	if err == nil || r.Memory != "partial" || r.ResumeCommand == "" {
		t.Fatalf("missing usable partial receipt: %+v %v", r, err)
	}
	if err := os.Remove(filepath.Join(project, "memory", "claudex-handoffs")); err != nil {
		t.Fatal(err)
	}
	initial, _ := os.ReadFile(r.Transcript)
	if err := os.WriteFile(r.Transcript, append(initial, []byte("{\"type\":\"system\",\"fixture\":\"CONTINUED\"}\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	second, err := ImportClaude(context.Background(), opts, s)
	if err != nil || second.SessionID != r.SessionID || second.Memory != "imported" {
		t.Fatalf("repair failed: %+v %v", second, err)
	}
	continued, _ := os.ReadFile(r.Transcript)
	if !bytes.Contains(continued, []byte("CONTINUED")) {
		t.Fatal("retry overwrote continued session")
	}
	info, _ := os.Stat(r.Transcript)
	archiveInfo, _ := os.Stat(s.Archive)
	initialInfo, _ := os.Stat(filepath.Join(s.Directory, "claude-initial.jsonl"))
	if os.SameFile(info, archiveInfo) || os.SameFile(info, initialInfo) {
		t.Fatal("writable session aliases immutable archive")
	}
	indexData, _ := os.ReadFile(index)
	if !bytes.HasSuffix(indexData, []byte(original)) || bytes.Count(indexData, []byte("](claudex-handoffs/")) != 1 {
		t.Fatal("memory index changed existing notes or duplicated link")
	}
	for _, want := range []string{"REPO_GUIDANCE_9031", "SESSION_MEMORY_9031", "TOOL_RESULT_TAIL"} {
		if !bytes.Contains(initial, []byte(want)) {
			t.Fatal("missing imported context", want)
		}
	}
	for _, unwanted := range []string{"MUST_NOT_COPY", "unrelated memory", "PRIVATE_REASONING", "ROLLED_BACK"} {
		if bytes.Contains(initial, []byte(unwanted)) {
			t.Fatal("copied unrelated context", unwanted)
		}
	}
	if !strings.Contains(r.ResumeCommand, "--resume") || !strings.Contains(r.ResumeCommand, "CLAUDEX_CONFIG=") {
		t.Fatal("resume lost launcher/config")
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			again, err := ImportClaude(context.Background(), opts, s)
			if err != nil || again.SessionID != r.SessionID {
				t.Error("concurrent retry", err)
			}
		}()
	}
	wg.Wait()
}

func TestInstallCodexSkillOwnershipAndUpdate(t *testing.T) {
	home := t.TempDir()
	ctx := context.Background()
	path, err := InstallCodexSkill(ctx, home, "/one/claudex")
	if err != nil {
		t.Fatal(err)
	}
	first, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexSkill(ctx, home, "/one/claudex"); err != nil {
		t.Fatal("repeat install", err)
	}
	if _, err := InstallCodexSkill(ctx, home, "/two/claudex 'quoted"); err != nil {
		t.Fatal("upgrade", err)
	}
	second, _ := filepath.EvalSymlinks(path)
	if first == second {
		t.Fatal("did not switch skill bundle")
	}
	data, _ := os.ReadFile(filepath.Join(path, "SKILL.md"))
	if !bytes.Contains(data, []byte("--to cc --json")) || bytes.Contains(data, []byte("{{CLAUDEX_BINARY}}")) {
		t.Fatal("unmaterialized skill")
	}
	writeTestFile(t, filepath.Join(path, "SKILL.md"), "my edited skill")
	if _, err := InstallCodexSkill(ctx, home, "/three/claudex"); err == nil {
		t.Fatal("overwrote edited skill")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(path, "SKILL.md"), "user-owned")
	if _, err := InstallCodexSkill(ctx, home, "/three/claudex"); err == nil {
		t.Fatal("overwrote user skill")
	}
}
