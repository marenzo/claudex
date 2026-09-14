package dashboard

import (
	"sync"
	"testing"
	"time"
)

func TestConcurrentMetricsAreBoundedAndConsistent(t *testing.T) {
	s := New()
	var workers sync.WaitGroup
	for i := 0; i < 600; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			s.Begin()
			s.Finish(Request{Started: time.Now(), Model: "gpt-6-astra", Status: 200, DurationMS: 2, UsageReported: true, Usage: Usage{Input: 10, Cached: 3, Output: 4, Reasoning: 2}})
		}()
	}
	workers.Wait()
	s.Recovered()
	snap := s.Snapshot()
	if snap.Active != 0 || snap.Totals.Requests != 600 || len(snap.Recent) != RecentLimit || snap.Totals.Tokens.Input != 6000 || snap.Totals.Tokens.Output != 2400 || snap.QuotaRecoveries != 1 {
		t.Fatalf("inconsistent totals: %+v", snap.Totals)
	}
	snap.Models["gpt-6-astra"] = Totals{}
	if s.Snapshot().Models["gpt-6-astra"].Requests != 600 {
		t.Fatal("snapshot shares mutable map")
	}
	s.Begin()
	s.Finish(Request{Model: "gpt-5.6-sol", Status: 429})
	snap = s.Snapshot()
	if snap.Recent[0].Status != 429 || snap.Totals.Errors != 1 || snap.Totals.RateLimited != 1 || snap.Totals.UsageReported != 600 {
		t.Fatal("error or missing usage accounting")
	}
}

func TestMinuteHistorySurvivesSamplingAndExpires(t *testing.T) {
	start := time.Date(2026, 9, 14, 10, 0, 5, 0, time.UTC)
	now := start
	s := New()
	s.started, s.now = start, func() time.Time { return now }
	for i := 0; i < RecentLimit+20; i++ {
		s.Begin()
		s.Finish(Request{Started: start.Add(-time.Hour), Model: "gpt-6-astra", Status: 200, UsageReported: true, Usage: Usage{Input: 10}})
	}
	now = start.Add(2 * time.Minute)
	s.Begin()
	s.Finish(Request{Started: start, Model: "gpt-5.6-sol", Status: 429})
	snap := s.Snapshot()
	if len(snap.History) != 3 || snap.History[0].Totals.Requests != 520 || snap.History[1].Totals.Requests != 0 || snap.History[2].Totals.RateLimited != 1 {
		t.Fatalf("completion-minute accounting: %+v", snap.History)
	}
	if snap.Recent[0].ID != 521 || !snap.Recent[0].Completed.Equal(now) || len(snap.Recent) != RecentLimit || snap.Totals.Requests != 521 {
		t.Fatal("lifetime or retained sequence changed", snap.Totals)
	}
	if !snap.Generated.Equal(now) || snap.UptimeSeconds != 120 || snap.RecentLimit != RecentLimit {
		t.Fatal("snapshot metadata does not use the observation clock")
	}
	now = start.Add(120 * time.Minute)
	snap = s.Snapshot()
	if len(snap.History) != HistoryMinutes || !snap.History[0].Time.Equal(start.Truncate(time.Minute).Add(time.Minute)) || snap.History[1].Totals.Requests != 1 {
		t.Fatal("history did not expire exactly one bucket")
	}
	// A reused slot cannot resurrect the old minute's 520 requests.
	s.Begin()
	s.Finish(Request{Started: now, Model: "gpt-6-astra", Status: 200})
	if got := s.Snapshot().History[HistoryMinutes-1].Totals.Requests; got != 1 {
		t.Fatalf("reused ring bucket contains %d requests", got)
	}
	now = start.Add(241 * time.Minute)
	for _, bucket := range s.Snapshot().History {
		if bucket.Totals.Requests != 0 {
			t.Fatal("stale bucket returned after idle period")
		}
	}
	if s.Snapshot().Totals.Requests != 522 {
		t.Fatal("history expiration reset lifetime totals")
	}
}

func TestSnapshotsOwnTheirData(t *testing.T) {
	s := New()
	first := int64(12)
	detail := &ErrorDetails{Source: "proxy", Message: "original error"}
	s.Begin()
	s.Finish(Request{Started: time.Now(), Model: "gpt-6-astra", Status: 500, FirstOutputMS: &first, Error: detail})
	first = 999
	detail.Message = "caller changed"
	snap := s.Snapshot()
	if *snap.Recent[0].FirstOutputMS != 12 || snap.Recent[0].Error.Message != "original error" {
		t.Fatal("retained caller pointer")
	}
	*snap.Recent[0].FirstOutputMS = 400
	snap.Recent[0].Model = "changed"
	snap.Recent[0].Error.Message = "snapshot changed"
	snap.History[0].Totals.Requests = 999
	again := s.Snapshot()
	if *again.Recent[0].FirstOutputMS != 12 || again.Recent[0].Model != "gpt-6-astra" || again.History[0].Totals.Requests != 1 || again.Recent[0].Error.Message != "original error" {
		t.Fatal("snapshot exposes mutable store memory")
	}
}

func TestCancellationsAreSeparateFromFailuresAtEveryScope(t *testing.T) {
	now := time.Date(2026, 9, 14, 12, 0, 30, 0, time.UTC)
	s := New()
	s.started, s.now = now, func() time.Time { return now }
	for _, status := range []int{200, 400, 429, 499, 502, 499} {
		s.Begin()
		s.Finish(Request{Started: now, Model: "gpt-6-astra", Status: status, DurationMS: 20, UsageReported: true, Usage: Usage{Input: 10}})
	}
	snap := s.Snapshot()
	for scope, totals := range map[string]Totals{"process": snap.Totals, "model": snap.Models["gpt-6-astra"], "minute": snap.History[0].Totals} {
		if totals.Requests != 6 || totals.Errors != 3 || totals.Canceled != 2 || totals.RateLimited != 1 {
			t.Fatalf("%s mixed outcomes: %+v", scope, totals)
		}
		if totals.DurationMS != 120 || totals.UsageReported != 6 || totals.Tokens.Input != 60 {
			t.Fatalf("%s dropped canceled duration or reported usage: %+v", scope, totals)
		}
	}
	if snap.Active != 0 || len(snap.Recent) != 6 || snap.Recent[0].Status != 499 {
		t.Fatal("cancellations lost from retained records")
	}
	// A cancellation-only minute/model has requests but no successful or failed
	// outcomes. Consumers must not interpret its zero Errors count as success.
	now = now.Add(time.Minute)
	s.Begin()
	s.Finish(Request{Started: now, Model: "gpt-5.6-sol", Status: 499})
	snap = s.Snapshot()
	for scope, totals := range map[string]Totals{"model": snap.Models["gpt-5.6-sol"], "minute": snap.History[1].Totals} {
		if totals.Requests != 1 || totals.Canceled != 1 || totals.Errors != 0 || totals.RateLimited != 0 {
			t.Fatalf("%s cancellation-only outcomes: %+v", scope, totals)
		}
	}
}
