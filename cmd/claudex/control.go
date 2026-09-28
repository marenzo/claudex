package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/launcher"
)

func cmdOverview(args []string) error {
	set := flag.NewFlagSet("ctl", flag.ContinueOnError)
	verbose := set.Bool("verbose", false, "Show every installation check")
	jsonOutput := set.Bool("json", false, "Print structured status")
	path := set.String("config", config.DefaultPath(), "Path to the local JSON config")
	if err := parseFlags(set, args, "usage: claudex ctl [--verbose|--json] [--config PATH]"); err != nil {
		return err
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	checks := launcher.NewStatus(paths, *path).Run()
	cfg, cfgErr := config.Load(*path)
	if cfgErr == nil {
		for _, check := range checks {
			if check.Name != "gateway" || check.Level != launcher.LevelOK {
				continue
			}
			state, err := launcher.NewService(paths).ReadControl(context.Background(), cfg)
			if err != nil {
				checks = append(checks, launcher.Check{Level: launcher.LevelFail, Name: "gateway control", Detail: err.Error()})
			} else if state.Revision != launcher.Revision(cfg) {
				checks = append(checks, launcher.Check{Level: launcher.LevelFail, Name: "gateway settings", Detail: "running gateway differs from config; run claudex ctl restart"})
			}
			break
		}
	}
	failed := false
	for _, check := range checks {
		failed = failed || check.Level == launcher.LevelFail
	}
	if *jsonOutput {
		out := map[string]any{"version": buildVersion(), "checks": checks}
		if cfgErr == nil {
			reviewer, _ := preference(cfg, "reviewer")
			out["preferences"] = map[string]any{"model": cfg.Model, "effort": cfg.ReasoningEffort,
				"reviewer": reviewer, "dashboard": cfg.Dashboard}
		}
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(out); err != nil {
			return err
		}
	} else if *verbose {
		fmt.Printf("Claudex %s\n", buildVersion())
		launcher.PrintChecks(os.Stdout, checks)
	} else {
		fmt.Printf("Claudex %s\n", buildVersion())
		for _, check := range checks {
			if check.Name == "gateway" || check.Name == "codex sign-in" || check.Level == launcher.LevelFail {
				fmt.Printf("%-14s %s\n", check.Name, check.Detail)
			}
		}
		if cfgErr == nil {
			fmt.Printf("Default        %s · %s\nReviewer       %s\nDashboard      %t\n",
				cfg.Model, cfg.ReasoningEffort, reviewerName(cfg), cfg.Dashboard)
		}
		fmt.Println("Actions: config · setup · logs · restart")
	}
	if failed {
		return errors.New("one or more checks failed")
	}
	return nil
}

func reviewerName(cfg config.Config) string {
	if cfg.AutoModeClassifierModel == "" {
		return "Claude client"
	}
	return cfg.AutoModeClassifierModel + " (experimental)"
}

func cmdLogs(args []string) error {
	set := flag.NewFlagSet("logs", flag.ContinueOnError)
	follow := set.Bool("follow", false, "Follow the gateway log")
	set.BoolVar(follow, "f", false, "Follow the gateway log")
	lines := set.Int("lines", 50, "Number of recent lines")
	if err := parseFlags(set, args, "usage: claudex ctl logs [--follow] [--lines N]"); err != nil {
		return err
	}
	if *lines < 1 || *lines > 10000 {
		return usageError{"--lines must be between 1 and 10000"}
	}
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	path := paths.LogFile()
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("gateway log unavailable at %s: %w", path, err)
	}
	arg := "-n"
	cmdArgs := []string{arg, fmt.Sprint(*lines)}
	if *follow {
		cmdArgs = append(cmdArgs, "-F")
	}
	cmd := exec.Command("/usr/bin/tail", append(cmdArgs, path)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	return cmd.Run()
}
