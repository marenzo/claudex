package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/marenzo/claudex/internal/launcher"
	"github.com/marenzo/claudex/internal/tui"
	"os"
	"os/exec"
	"path/filepath"
)

func connectionFlags(set *flag.FlagSet) (*string, *string, *string) {
	return set.String("config", config.DefaultPath(), "Configuration path"), set.String("url", "", "Gateway URL instead of local config"), set.String("key-file", "", "Client key file for --url")
}
func connect(path, address, key string) (*control.Client, error) {
	if address != "" {
		if key == "" {
			return nil, usageError{"--url requires --key-file"}
		}
		return control.New(address, key)
	}
	if key != "" {
		return nil, usageError{"--key-file requires --url"}
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	return control.New("http://"+cfg.Listen, cfg.ClientKeyFile)
}
func cmdTUI(args []string, settings bool) error {
	name := "ctl"
	if settings {
		name = "ctl config"
	}
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	path, address, key := connectionFlags(set)
	if err := parseFlags(set, args, "usage: claudex "+name+" [--config PATH | --url URL --key-file PATH]"); err != nil {
		return err
	}
	if !interactive() {
		return usageError{"the TUI needs a terminal; use claudex ctl status --json"}
	}
	c, err := connect(*path, *address, *key)
	if err != nil {
		return err
	}
	ctx, cancel := signalContext()
	defer cancel()
	return tui.Run(ctx, c, settings)
}
func cmdStatus(args []string) error {
	set := flag.NewFlagSet("status", flag.ContinueOnError)
	path, address, key := connectionFlags(set)
	asJSON := set.Bool("json", false, "Print JSON")
	if err := parseFlags(set, args, "usage: claudex ctl status [--json] [--config PATH | --url URL --key-file PATH]"); err != nil {
		return err
	}
	c, err := connect(*path, *address, *key)
	if err != nil {
		return err
	}
	s, err := c.Status(context.Background())
	if err != nil {
		return fmt.Errorf("gateway unavailable: %w; run claudex or claudex ctl run", err)
	}
	if *asJSON {
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(s)
	}
	fmt.Printf("claudex %s · %s\n%s · %d clients · %d active\nModel %s · effort %s · reviewer %s\n", s.Version, c.URL, s.Mode, s.Clients, s.Active, s.Preferences.Model, s.Preferences.Effort, s.Preferences.Reviewer)
	return nil
}
func cmdLogs(args []string) error {
	set := flag.NewFlagSet("logs", flag.ContinueOnError)
	path := set.String("config", config.DefaultPath(), "Configuration path")
	follow := set.Bool("follow", false, "Follow log rotation")
	lines := set.Int("lines", 50, "Recent lines")
	if err := parseFlags(set, args, "usage: claudex ctl logs [--config PATH] [--follow] [--lines N]"); err != nil {
		return err
	}
	if *lines < 1 || *lines > 10000 {
		return usageError{"--lines must be 1..10000"}
	}
	absolute, err := filepath.Abs(*path)
	if err != nil {
		return err
	}
	log := launcher.LogPath(absolute)
	if _, err := os.Stat(log); err != nil {
		return fmt.Errorf("gateway log unavailable: %w", err)
	}
	argv := []string{"-n", fmt.Sprint(*lines)}
	if *follow {
		argv = append(argv, "-F")
	}
	argv = append(argv, log)
	cmd := exec.Command("tail", argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
