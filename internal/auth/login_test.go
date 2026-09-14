package auth

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestBrowserOpenerDoesNotBlockLoginAndIsReaped(t *testing.T) {
	for _, action := range []string{"finish", "cancel"} {
		t.Run(action, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestBrowserOpenerHelper$")
			command.Env = append(os.Environ(), "CLAUDEX_BROWSER_TEST_HELPER=1")
			input, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer input.Close()
			output, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			done := startBrowser(ctx, command)
			defer func() { cancel(); <-done }()
			// The helper announces readiness, then waits for input. Login must
			// remain free to process its callback while the opener is waiting.
			if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "ready\n" {
				t.Fatalf("browser helper did not start: %q, %v", line, err)
			}
			select {
			case <-done:
				t.Fatal("browser opener exited before being released")
			default:
			}
			if action == "cancel" {
				cancel()
			} else if _, err := input.Write([]byte("finish\n")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("browser opener was not reaped")
			}
			if command.ProcessState == nil || (action == "finish" && !command.ProcessState.Success()) {
				t.Fatalf("unexpected browser process state: %v", command.ProcessState)
			}
		})
	}
}

func TestBrowserOpenerHelper(t *testing.T) {
	if os.Getenv("CLAUDEX_BROWSER_TEST_HELPER") != "1" {
		return
	}
	if _, err := io.WriteString(os.Stdout, "ready\n"); err != nil {
		os.Exit(1)
	}
	var input [1]byte
	if _, err := os.Stdin.Read(input[:]); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestBrowserOpenerStartFailureFinishes(t *testing.T) {
	command := exec.CommandContext(context.Background(), "/nonexistent/claudex-browser")
	select {
	case <-startBrowser(context.Background(), command):
	default:
		t.Fatal("failed browser opener did not finish")
	}
}
