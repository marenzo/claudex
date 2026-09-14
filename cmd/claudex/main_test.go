package main

import (
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvalidCLIArgumentsFailBeforeChangingFiles(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"positional_command", []string{"login"}, "unexpected argument"},
		{"trailing_argument", []string{"-init", "typo"}, "unexpected argument"},
		{"conflicting_actions", []string{"-init", "-login"}, "use only one"},
		{"version_and_init", []string{"-version", "-init"}, "use only one"},
		{"version_and_login", []string{"-version", "-login"}, "use only one"},
		{"browser_without_login", []string{"-no-browser"}, "requires -login"},
	} {
		t.Run(test.name, func(t *testing.T) {
			previousFlags, previousArgs := flag.CommandLine, os.Args
			t.Cleanup(func() { flag.CommandLine, os.Args = previousFlags, previousArgs })
			flag.CommandLine = flag.NewFlagSet("claudex", flag.ContinueOnError)
			flag.CommandLine.SetOutput(io.Discard)
			path := filepath.Join(t.TempDir(), "config.json")
			os.Args = append([]string{"claudex", "-config", path}, test.args...)
			if err := run(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("run() returned %v, expected %q", err, test.want)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("invalid arguments touched config: %v", err)
			}
		})
	}
}

func TestListenOverrideRequiresExplicitIPAndValidPort(t *testing.T) {
	for _, address := range []string{"0.0.0.0:8317", "127.0.0.1:1", "[::]:8317", "[::1]:65535"} {
		if err := validateListen(address); err != nil {
			t.Errorf("rejected %q: %v", address, err)
		}
	}
	for _, address := range []string{"localhost:8317", ":8317", "0.0.0.0:http", "127.0.0.1:0", "127.0.0.1:-1", "127.0.0.1:65536", "::1:8317"} {
		if err := validateListen(address); err == nil {
			t.Errorf("accepted %q", address)
		}
	}
}

func TestSubcommandFlagMistakesAreUsageErrors(t *testing.T) {
	for _, args := range [][]string{{"install", "-bogus"}, {"install", "extra"}, {"status", "-bogus"}, {"status", "extra"}} {
		handled, err := subcommand(args)
		var misuse usageError
		if !handled || !errors.As(err, &misuse) {
			t.Errorf("%v: handled=%t err=%v, want a usage error", args, handled, err)
		}
	}
}
