package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/marenzo/claudex/internal/handoff"
)

func cmdHandoff(args []string) error {
	set := flag.NewFlagSet("handoff", flag.ContinueOnError)
	source := set.String("source", "", "Explicit Claude transcript; defaults to this session's hook binding")
	launch := set.Bool("launch", false, "Open native Codex after import; requires an interactive terminal")
	asJSON := set.Bool("json", false, "Print the verified handoff receipt as JSON")
	if err := parseFlags(set, args, "usage: claudex ctl handoff [--source PATH] [--launch | --json]"); err != nil {
		return err
	}
	if *launch && (*asJSON || !interactive()) {
		return usageError{"--launch requires a terminal and cannot be combined with --json"}
	}
	binary, err := exec.LookPath("codex")
	if err != nil {
		return errors.New("install the native Codex CLI and put codex on PATH before transferring")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	cwd := ""
	if *source == "" {
		*source = os.Getenv("CLAUDEX_TRANSCRIPT_PATH")
		cwd = os.Getenv("CLAUDEX_SESSION_CWD")
	}
	ctx, cancel := signalContext()
	defer cancel()
	// Claude buffers transcript writes. Give the active session one flush interval
	// before snapshotting; explicit offline sources do not need this delay.
	if cwd != "" {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
	snapshot, err := handoff.Prepare(*source, cwd, home, claudeHome)
	if err != nil {
		return err
	}
	result, importErr := handoff.Import(ctx, binary, codexHome, snapshot)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	} else {
		if result.ResumeCommand != "" {
			fmt.Printf("Native Codex thread ready. Continue in your terminal:\n\n%s\n\n", result.ResumeCommand)
		}
		fmt.Printf("Private transcript snapshot: %s\nProject memory: %s\n", snapshot.Archive, result.Memory)
		for _, limit := range result.Limitations {
			fmt.Println(limit)
		}
	}
	if importErr != nil {
		return importErr
	}
	if *launch {
		return syscall.Exec(binary, []string{binary, "resume", result.ThreadID, "--cd", snapshot.CWD}, handoff.NativeEnvironment(os.Environ()))
	}
	return nil
}
