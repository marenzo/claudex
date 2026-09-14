package quota

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRecoveryPolicy(t *testing.T) {
	for _, name := range []string{"allowed", "denied", "error", "new_limit", "new_account", "cancelled", "idle"} {
		t.Run(name, func(t *testing.T) {
			now := time.Now()
			s := new(State)
			if name != "idle" {
				s.Limited("a", now.Add(6*24*time.Hour))
			}
			ticks := make(chan time.Time, 1)
			ticks <- now
			close(ticks)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checks, recovered := 0, 0
			s.watch(ctx, ticks, func(context.Context, string) (bool, error) {
				checks++
				switch name {
				case "denied":
					return false, nil
				case "error":
					return true, errors.New("probe failed")
				case "new_limit":
					s.Limited("a", now.Add(7*24*time.Hour))
				case "new_account":
					s.Limited("b", now.Add(7*24*time.Hour))
				case "cancelled":
					cancel()
				}
				return true, nil
			}, func() { recovered++ })
			if name == "allowed" {
				if recovered != 1 || s.Remaining("a", now) != 0 {
					t.Fatal("not recovered")
				}
			} else if recovered != 0 {
				t.Fatal("unsafe recovery")
			}
			if name == "idle" && checks != 0 {
				t.Fatal("probed healthy account")
			}
			if name != "allowed" && name != "idle" {
				account := "a"
				if name == "new_account" {
					account = "b"
				}
				if s.Remaining(account, now) == 0 {
					t.Fatal("lost cooldown")
				}
			}
		})
	}
}
func TestQuotaConcurrentNewFailure(t *testing.T) {
	s := new(State)
	now := time.Now()
	s.Limited("a", now.Add(time.Hour))
	ticks := make(chan time.Time, 1)
	ticks <- now
	close(ticks)
	probing, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go func() {
		s.watch(context.Background(), ticks, func(context.Context, string) (bool, error) { close(probing); <-release; return true, nil }, nil)
		close(done)
	}()
	<-probing
	s.Limited("a", now.Add(2*time.Hour))
	close(release)
	<-done
	if s.Remaining("a", now) != 2*time.Hour {
		t.Fatal("erased newer failure")
	}
}
