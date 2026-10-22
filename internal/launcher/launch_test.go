package launcher

import (
	"context"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPreservesClaudeExitAndSanitizesProviders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "{}\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	root := t.TempDir()
	key := filepath.Join(root, "key")
	os.WriteFile(key, []byte(strings.Repeat("k", 32)), 0600)
	c, err := control.New(server.URL, key)
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "claude")
	os.WriteFile(script, []byte("#!/bin/sh\n[ -z \"$CLAUDE_CODE_OAUTH_TOKEN\" ] || exit 9\n[ -z \"$CLAUDE_CODE_USE_BEDROCK\" ] || exit 10\n[ \"$ANTHROPIC_BASE_URL\" = \""+server.URL+"\" ] || exit 11\nexit 17\n"), 0700)
	t.Setenv("CLAUDE_CODE_OAUTH_TOKEN", "fixture-old-provider")
	t.Setenv("CLAUDE_CODE_USE_BEDROCK", "1")
	err = Run(script, []string{"--resume"}, config.DefaultsFor(root), c)
	native, ok := err.(*exec.ExitError)
	if !ok || native.ExitCode() != 17 {
		t.Fatal("native exit lost", err)
	}
}
func TestEnsureRejectsAnotherGatewayWithoutReplacingIt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401) }))
	defer server.Close()
	root := t.TempDir()
	key := filepath.Join(root, "key")
	os.WriteFile(key, []byte(strings.Repeat("k", 32)), 0600)
	c, err := control.New(server.URL, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(context.Background(), filepath.Join(root, "config.json"), c); err == nil {
		t.Fatal("replaced foreign gateway")
	}
}
