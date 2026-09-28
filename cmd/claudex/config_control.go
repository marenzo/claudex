package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/launcher"
)

const configUsage = "usage: claudex ctl config [--config PATH] [model|effort|reviewer|dashboard [VALUE]|edit|--json]"

func cmdConfig(args []string) error {
	set := flag.NewFlagSet("config", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	path := set.String("config", config.DefaultPath(), "Path to the local JSON config")
	jsonOutput := set.Bool("json", false, "Print preferences as JSON")
	if err := set.Parse(args); err == flag.ErrHelp {
		fmt.Println(configUsage)
		return nil
	} else if err != nil || set.NArg() > 2 || (*jsonOutput && set.NArg() > 0) {
		return usageError{configUsage}
	}
	if set.NArg() > 0 && set.Arg(0) == "edit" {
		if set.NArg() != 1 {
			return usageError{configUsage}
		}
		return editConfig(*path)
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if *jsonOutput {
		reviewer, _ := preference(cfg, "reviewer")
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"model": cfg.Model, "effort": cfg.ReasoningEffort,
			"reviewer": reviewer, "dashboard": cfg.Dashboard})
	}
	if set.NArg() == 0 {
		printPreferences(cfg)
		if interactive() {
			return guidedConfig(*path, cfg)
		}
		return nil
	}
	key := set.Arg(0)
	if set.NArg() == 1 {
		value, err := preference(cfg, key)
		if err != nil {
			return err
		}
		fmt.Println(value)
		return nil
	}
	next, err := changedPreference(cfg, key, set.Arg(1))
	if err != nil {
		return err
	}
	return applyConfiguration(*path, cfg, next)
}

func printPreferences(cfg config.Config) {
	fmt.Printf("Model       %s\nEffort      %s\nReviewer    %s\nDashboard   %t\n",
		cfg.Model, cfg.ReasoningEffort, reviewerName(cfg), cfg.Dashboard)
}

func preference(cfg config.Config, key string) (string, error) {
	switch key {
	case "model":
		return cfg.Model, nil
	case "effort":
		return cfg.ReasoningEffort, nil
	case "reviewer":
		if cfg.AutoModeClassifierModel == "" {
			return "client", nil
		}
		return cfg.AutoModeClassifierModel, nil
	case "dashboard":
		if cfg.Dashboard {
			return "on", nil
		}
		return "off", nil
	default:
		return "", usageError{"choose model, effort, reviewer, or dashboard"}
	}
}

func changedPreference(cfg config.Config, key, value string) (config.Config, error) {
	switch key {
	case "model":
		model, ok := config.FindModel(value)
		if !ok {
			return cfg, usageError{"model must be astra, terra, sol, or luna"}
		}
		cfg.Model = model.ID
	case "effort":
		cfg.ReasoningEffort = value
	case "reviewer":
		if value == "client" {
			cfg.AutoModeClassifierModel = ""
		} else {
			model, ok := config.FindModel(value)
			if !ok {
				return cfg, usageError{"reviewer must be client, astra, terra, sol, or luna"}
			}
			cfg.AutoModeClassifierModel = model.ID
		}
	case "dashboard":
		switch value {
		case "on":
			cfg.Dashboard = true
		case "off":
			cfg.Dashboard = false
		default:
			return cfg, usageError{"dashboard must be on or off"}
		}
	default:
		return cfg, usageError{"choose model, effort, reviewer, or dashboard"}
	}
	return cfg, cfg.Validate()
}

func guidedConfig(path string, cfg config.Config) error {
	fmt.Print("Change [model/effort/reviewer/dashboard] (Enter to finish): ")
	in := bufio.NewReader(os.Stdin)
	key, err := in.ReadString('\n')
	if err != nil {
		return err
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if _, err := preference(cfg, key); err != nil {
		return err
	}
	fmt.Printf("New %s: ", key)
	value, err := in.ReadString('\n')
	if err != nil {
		return err
	}
	next, err := changedPreference(cfg, key, strings.TrimSpace(value))
	if err != nil {
		return err
	}
	return applyConfiguration(path, cfg, next)
}

func editConfig(path string) error {
	if !interactive() {
		return usageError{"config edit needs a terminal; use claudex ctl config KEY VALUE"}
	}
	previous, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".claudex-edit-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(previous); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command("/bin/sh", "-c", editor+` "$1"`, "claudex-editor", tmp.Name())
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	updated, err := os.ReadFile(tmp.Name())
	if err != nil {
		return err
	}
	if string(updated) == string(previous) {
		fmt.Println("No changes.")
		return nil
	}
	next, err := config.Load(tmp.Name())
	if err != nil {
		return err
	}
	old, err := config.Load(path)
	if err != nil {
		paths, errPaths := launcher.DefaultPaths()
		if errPaths != nil {
			return errPaths
		}
		if paths.ConfigFile() == path && launcher.NewService(paths).Launchd.Loaded(launcher.Label) {
			return fmt.Errorf("current config is invalid; stop the gateway before repairing %s: %w", path, err)
		}
		return launcher.WithControlLock(paths, func() error {
			current, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if string(current) != string(previous) {
				return fmt.Errorf("config changed while editing; retry")
			}
			return config.Save(path, next)
		})
	}
	return applyConfiguration(path, old, next)
}

func applyConfiguration(path string, old, next config.Config) error {
	paths, err := launcher.DefaultPaths()
	if err != nil {
		return err
	}
	ctx, stop := signalContext()
	defer stop()
	if err := launcher.ApplyConfig(ctx, launcher.NewService(paths), path, old, next); err != nil {
		return err
	}
	fmt.Println("Preferences applied. Open a new Claude session to use changed startup settings.")
	return nil
}
