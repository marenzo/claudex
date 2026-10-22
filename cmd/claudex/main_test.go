package main

import (
	"errors"
	"strings"
	"testing"
)

func TestCommandMistakesAreUsageErrors(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"bogus"}, "unknown control command"},
		{[]string{"run", "typo"}, "usage: claudex ctl run"},
		{[]string{"run", "-bogus"}, "usage: claudex ctl run"},
		{[]string{"setup", "extra"}, "usage: claudex ctl setup"},
		{[]string{"config", "extra", "more", "args"}, "usage: claudex ctl config"},
		{[]string{"logs", "extra"}, "usage: claudex ctl logs"},
		{[]string{"stop", "extra"}, "unknown control command"},
		{[]string{"--version", "extra"}, "usage: claudex ctl --version"},
		{[]string{"uninstall", "extra"}, "unknown control command"},
		{[]string{"--bogus"}, "usage: claudex ctl ["},
	} {
		err := command(test.args)
		var misuse usageError
		if !errors.As(err, &misuse) || !strings.Contains(err.Error(), test.want) {
			t.Errorf("%q: got %v, want a usage error containing %q", test.args, err, test.want)
		}
	}
}

func TestHelpIsNotAnError(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"--help"}, {"setup", "-help"}, {"run", "-h"}, {"config", "--help"}} {
		if err := dispatch(append([]string{"ctl"}, args...)); err != nil {
			t.Errorf("%q: %v", args, err)
		}
	}
}

func TestTUIConnectionFlagsAreDispatched(t *testing.T) {
	if interactive() {
		t.Skip("requires a noninteractive test runner")
	}
	for _, args := range [][]string{{"--config", "/missing"}, {"--url", "http://127.0.0.1:8317", "--key-file", "/missing"}} {
		err := command(args)
		if err == nil || !strings.Contains(err.Error(), "TUI needs a terminal") {
			t.Fatalf("flags not routed to TUI: %v", err)
		}
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
