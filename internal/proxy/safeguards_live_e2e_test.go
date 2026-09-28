//go:build live && e2e

package proxy

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/marenzo/claudex/internal/auth"
)

// The generator is a fixture proposing only printf. GPT performs the real
// permission review; the real Claude Code client then runs the harmless command.
func TestLiveClaudeGPTSafeguardDecision(t *testing.T) {
	claude, path := os.Getenv("CLAUDEX_TEST_CLAUDE_BIN"), os.Getenv("CLAUDEX_TEST_AUTH_FILE")
	if claude == "" || path == "" {
		t.Skip("set both CLAUDEX_TEST_CLAUDE_BIN and CLAUDEX_TEST_AUTH_FILE")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var credential auth.Credential
	if err = json.Unmarshal(raw, &credential); err != nil {
		t.Fatal(err)
	}
	if credential.RefreshToken != "" {
		t.Fatal("live fixture must omit refresh_token")
	}
	runClaudeSafeguardProbe(t, claude, "not_flagged", &credential)
}
