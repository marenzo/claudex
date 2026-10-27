package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/handoff"
	"github.com/marenzo/claudex/internal/launcher"
)

func cmdHandoff(args []string) error {
	set := flag.NewFlagSet("handoff", flag.ContinueOnError)
	to := set.String("to", "codex", "Destination: codex or cc (case insensitive)")
	source := set.String("source", "", "Explicit absolute source transcript; otherwise use the exact current-session binding")
	threadID := set.String("thread-id", "", "Exact saved Codex thread UUID (only with --to cc)")
	launch := set.Bool("launch", false, "Open the destination after import; requires an interactive terminal")
	asJSON := set.Bool("json", false, "Print the verified handoff receipt as JSON")
	if err := parseFlags(set, args, "usage: claudex ctl handoff [--to codex|cc] [--source PATH | --thread-id UUID] [--launch | --json]"); err != nil {
		return err
	}
	if *launch && (*asJSON || !interactive()) {
		return usageError{"--launch requires a terminal and cannot be combined with --json"}
	}
	if *source != "" && *threadID != "" {
		return usageError{"choose either --source or --thread-id"}
	}
	switch strings.ToLower(*to) {
	case "cc":
		return cmdHandoffToClaude(*source, *threadID, *launch, *asJSON)
	case "codex":
		if *threadID != "" {
			return usageError{"--thread-id is only supported with --to cc"}
		}
	default:
		return usageError{"--to must be codex or cc"}
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

func cmdHandoffToClaude(source, threadID string, launch, asJSON bool) error {
	if _, err := launcher.ClaudePath(); err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	claudeHome := os.Getenv("CLAUDE_CONFIG_DIR")
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	claudeHome, err = filepath.Abs(claudeHome)
	if err != nil {
		return err
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	if source == "" && threadID == "" {
		threadID = os.Getenv("CODEX_THREAD_ID")
	}
	binary, err := os.Executable()
	if err != nil {
		return err
	}
	configPath, err := filepath.Abs(config.DefaultPath())
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	snapshot, err := handoff.PrepareCodex(source, threadID, codexHome, claudeHome)
	if err != nil {
		return err
	}
	options := handoff.ClaudeOptions{Binary: binary, Home: claudeHome, Config: configPath, ProjectKey: os.Getenv("CLAUDE_CODE_PROJECT_DIR_NAME")}
	result, importErr := handoff.ImportClaude(ctx, options, snapshot)
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return err
		}
	} else {
		if result.ResumeCommand != "" {
			fmt.Printf("Claude session ready through claudex. Continue in your terminal:\n\n%s\n\n", result.ResumeCommand)
		}
		fmt.Printf("Private transcript snapshot: %s\nProject memory: %s\n", snapshot.Archive, result.Memory)
		for _, warning := range result.Warnings {
			fmt.Println(warning)
		}
		for _, limit := range result.Limitations {
			fmt.Println(limit)
		}
	}
	if importErr != nil {
		return importErr
	}
	if launch {
		if err := os.Chdir(snapshot.CWD); err != nil {
			return err
		}
		key := options.ProjectKey
		if key == "" {
			key = handoff.ClaudeProjectKey(snapshot.CWD)
		}
		env := append(handoff.ClaudeEnvironment(os.Environ()), "CLAUDE_CONFIG_DIR="+claudeHome, "CLAUDE_CODE_PROJECT_DIR_NAME="+key, "CLAUDEX_CONFIG="+configPath)
		return syscall.Exec(binary, []string{binary, "--resume", result.Transcript}, env)
	}
	return nil
}
