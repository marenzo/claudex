package tokens

import (
	"strings"
	"testing"
)

func TestCountSkipsImageBytesButCountsToolText(t *testing.T) {
	raw := `{"messages":[{"role":"user","content":[{"type":"text","text":"hello"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"IMAGE"}}]}]}`
	a, err := CountClaudeInputTokens([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := CountClaudeInputTokens([]byte(strings.Replace(raw, "IMAGE", strings.Repeat("A", 1<<20), 1)))
	if err != nil || a != b {
		t.Fatal("image base64 counted as text", a, b, err)
	}
	c, err := CountClaudeInputTokens([]byte(`{"messages":[{"role":"user","content":[{"type":"tool_result","tool_use_id":"a","content":"` + strings.Repeat("test result ", 100) + `"}]}]}`))
	if err != nil || c <= a {
		t.Fatal("tool output not counted", c, err)
	}
}
