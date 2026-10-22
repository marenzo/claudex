package main

import (
	"context"
	"encoding/json"
	"github.com/marenzo/claudex/internal/config"
	"github.com/marenzo/claudex/internal/control"
	"github.com/marenzo/claudex/internal/launcher"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// The test executable can act as the real automatically spawned worker. Tests
// need neither a prebuilt binary nor an installed service or user credential.
func TestMain(m *testing.M) {
	if os.Getenv("CLAUDEX_SESSION_WORKER") == "1" {
		if err := dispatch(os.Args[1:]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestAutomaticWorkerSharedOwnershipAndExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Initialize(path); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listen = listener.Addr().String()
	listener.Close()
	if err := config.Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	credential, _ := json.Marshal(map[string]string{"type": "codex", "access_token": "fixture-worker-token", "account_id": "fixture-worker-account"})
	if err := os.MkdirAll(filepath.Dir(cfg.AuthFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.AuthFile, credential, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := control.New("http://"+cfg.Listen, cfg.ClientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	instances := make(chan string, 4)
	failures := make(chan error, 4)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			status, err := launcher.Ensure(ctx, path, c)
			if err != nil {
				failures <- err
			} else {
				instances <- status.Instance
			}
		})
	}
	wg.Wait()
	close(instances)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	instance := ""
	for id := range instances {
		if instance != "" && instance != id {
			t.Fatal("multiple workers started")
		}
		instance = id
	}
	one, stopOne := context.WithCancel(ctx)
	defer stopOne()
	two, stopTwo := context.WithCancel(ctx)
	defer stopTwo()
	done, err := c.Attach(one, "claude")
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Attach(two, "tui")
	if err != nil {
		t.Fatal(err)
	}
	stopOne()
	<-done
	status, err := c.Status(ctx)
	if err != nil || status.Instance != instance {
		t.Fatal("first disconnect stopped shared worker", err)
	}
	stopTwo()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Status(ctx); err != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("worker did not exit after final client")
}
