package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"

	"github.com/marenzo/claudex/internal/feedback"
)

func cmdFeedback(args []string) error {
	action := "status"
	if len(args) > 0 && len(args[0]) > 0 && args[0][0] != '-' {
		action = args[0]
		args = args[1:]
	}
	if action != "on" && action != "off" && action != "status" && action != "list" {
		return usageError{"usage: claudex ctl feedback [on|off|status|list] [--config PATH | --url URL --key-file PATH]"}
	}
	set := flag.NewFlagSet("feedback", flag.ContinueOnError)
	path, address, key := connectionFlags(set)
	before := set.String("before", "", "Continue list from next_cursor")
	if err := parseFlags(set, args, "usage: claudex ctl feedback [on|off|status|list] [--config PATH | --url URL --key-file PATH]"); err != nil {
		return err
	}
	if *before != "" && action != "list" {
		return usageError{"--before applies only to feedback list"}
	}
	c, err := connect(*path, *address, *key)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if action == "list" {
		var result struct {
			Records    []feedback.Record `json:"records"`
			NextCursor string            `json:"next_cursor"`
		}
		endpoint := "/_claudex/feedback"
		if *before != "" {
			endpoint += "?before=" + url.QueryEscape(*before)
		}
		if err := c.JSON(ctx, "GET", endpoint, nil, "", &result); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	settings, err := c.Settings(ctx)
	if err != nil {
		return err
	}
	if action == "on" || action == "off" {
		prefs := settings.Preferences
		prefs.Feedback.Enabled = action == "on"
		settings, err = c.Save(ctx, prefs, settings.Revision)
		if err != nil {
			return err
		}
	}
	state := "off"
	if settings.Preferences.Feedback.Enabled {
		state = "on"
	}
	fmt.Printf("Local feedback: %s. Anthropic feedback remains disabled in claudex sessions.\n", state)
	if action == "on" {
		fmt.Println("Start a new Claude session to load ClaudexFeedback.")
	}
	if action == "off" {
		fmt.Println("New reports are rejected immediately; existing reports remain available.")
	}
	return nil
}

// Launched only as a stdio MCP child; stdout must contain JSON-RPC messages only.
func cmdFeedbackMCP(args []string) error {
	set := flag.NewFlagSet("feedback-mcp", flag.ContinueOnError)
	path, address, key := connectionFlags(set)
	launchID := set.String("launch-id", "", "Launcher-generated identity")
	if err := parseFlags(set, args, "usage: claudex ctl feedback-mcp --launch-id ID [--config PATH | --url URL --key-file PATH]"); err != nil {
		return err
	}
	c, err := connect(*path, *address, *key)
	if err != nil {
		return err
	}
	source := feedback.Source{LaunchID: *launchID}
	// Validate without accepting any model-provided session identity.
	if source.Validate() != nil {
		return usageError{"feedback-mcp requires a launch ID"}
	}
	ctx, cancel := signalContext()
	defer cancel()
	m := feedback.MCP{Source: source,
		Enabled: func(ctx context.Context) (bool, error) {
			s, err := c.Settings(ctx)
			return s.Preferences.Feedback.Enabled, err
		},
		Submit: func(ctx context.Context, sub feedback.Submission) (feedback.Receipt, error) {
			var receipt feedback.Receipt
			err := c.JSON(ctx, "POST", "/_claudex/feedback", sub, "", &receipt)
			return receipt, err
		},
	}
	return m.Serve(ctx, os.Stdin, os.Stdout)
}
