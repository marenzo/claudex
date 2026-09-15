// Package dashboard keeps bounded operational metrics and redacted error excerpts
// for the optional local dashboard. Normal request/response bodies are not retained.
package dashboard

import (
	"sync"
	"time"
)

const (
	RecentLimit    = 500
	HistoryMinutes = 120
)

type Usage struct {
	Input     int64 `json:"input"` // Includes cached input, matching Codex usage.
	Cached    int64 `json:"cached"`
	Output    int64 `json:"output"`
	Reasoning int64 `json:"reasoning"` // A subset of output, not an additional charge.
}

type Request struct {
	ID            int64         `json:"id"`
	Started       time.Time     `json:"started"`
	Completed     time.Time     `json:"completed"`
	Model         string        `json:"model"`
	Effort        string        `json:"effort"`
	Status        int           `json:"status"`
	ErrorKind     string        `json:"error_kind,omitempty"` // A static category, never a raw error message.
	Error         *ErrorDetails `json:"error,omitempty"`
	DurationMS    int64         `json:"duration_ms"`
	FirstOutputMS *int64        `json:"first_output_ms,omitempty"`
	Usage         Usage         `json:"tokens"`
	UsageReported bool          `json:"usage_reported"`
}

// ErrorDetails holds a bounded, redacted excerpt prepared by the gateway.
// Status is the upstream HTTP status, which can be 200 for an SSE error.
type ErrorDetails struct {
	Source    string `json:"source"`
	Type      string `json:"type,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message"`
	Status    int    `json:"status,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

func cloneRequest(record Request) Request {
	if record.FirstOutputMS != nil {
		elapsed := *record.FirstOutputMS
		record.FirstOutputMS = &elapsed
	}
	if record.Error != nil {
		detail := *record.Error
		record.Error = &detail
	}
	return record
}

// Bucket accounts for every request completed in a minute, independently of the
// sampled recent-request ring. The current minute is intentionally partial.
type Bucket struct {
	Time   time.Time `json:"time"`
	Totals Totals    `json:"totals"`
}

type Totals struct {
	Requests      int64 `json:"requests"`
	Errors        int64 `json:"errors"`
	Canceled      int64 `json:"canceled"` // Client cancellations (499), excluded from Errors.
	RateLimited   int64 `json:"rate_limited"`
	DurationMS    int64 `json:"duration_ms"`
	UsageReported int64 `json:"usage_reported"`
	Tokens        Usage `json:"tokens"`
}

type Snapshot struct {
	Generated       time.Time         `json:"generated"`
	Started         time.Time         `json:"started"`
	UptimeSeconds   int64             `json:"uptime_seconds"`
	Active          int               `json:"active"`
	Totals          Totals            `json:"totals"`
	Models          map[string]Totals `json:"models"`
	Recent          []Request         `json:"recent"`
	RecentLimit     int               `json:"recent_limit"`
	History         []Bucket          `json:"history"`
	QuotaRecoveries int64             `json:"quota_recoveries"`
}

type Store struct {
	mu         sync.Mutex
	started    time.Time
	active     int
	totals     Totals
	models     map[string]Totals
	recent     [RecentLimit]Request
	next       int
	count      int
	recoveries int64
	history    [HistoryMinutes]Bucket
	now        func() time.Time
}

func New() *Store { return &Store{started: time.Now(), models: make(map[string]Totals), now: time.Now} }

func (s *Store) Begin()     { s.mu.Lock(); defer s.mu.Unlock(); s.active++ }
func (s *Store) Recovered() { s.mu.Lock(); defer s.mu.Unlock(); s.recoveries++ }

func (s *Store) Finish(record Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active--
	record.Completed = s.now()
	record.ID = s.totals.Requests + 1
	record = cloneRequest(record)
	add(&s.totals, record)
	totals := s.models[record.Model]
	add(&totals, record)
	s.models[record.Model] = totals
	minute := record.Completed.Truncate(time.Minute)
	bucket := &s.history[minute.Unix()/60%HistoryMinutes]
	if !bucket.Time.Equal(minute) {
		*bucket = Bucket{Time: minute}
	}
	add(&bucket.Totals, record)
	s.recent[s.next] = record
	s.next = (s.next + 1) % RecentLimit
	if s.count < RecentLimit {
		s.count++
	}
}

func add(t *Totals, r Request) {
	t.Requests++
	t.DurationMS += r.DurationMS
	if r.Status == 499 {
		t.Canceled++
	} else if r.Status >= 400 {
		t.Errors++
	}
	if r.Status == 429 {
		t.RateLimited++
	}
	if r.UsageReported {
		t.UsageReported++
	}
	t.Tokens.Input += r.Usage.Input
	t.Tokens.Cached += r.Usage.Cached
	t.Tokens.Output += r.Usage.Output
	t.Tokens.Reasoning += r.Usage.Reasoning
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	snapshot := Snapshot{Generated: now, Started: s.started, UptimeSeconds: int64(now.Sub(s.started).Seconds()), Active: s.active, Totals: s.totals, Models: make(map[string]Totals, len(s.models)), Recent: make([]Request, 0, s.count), RecentLimit: RecentLimit, History: make([]Bucket, 0, HistoryMinutes), QuotaRecoveries: s.recoveries}
	lastMinute := now.Truncate(time.Minute)
	for i := HistoryMinutes - 1; i >= 0; i-- {
		minute := lastMinute.Add(-time.Duration(i) * time.Minute)
		if minute.Before(s.started.Truncate(time.Minute)) {
			continue
		}
		bucket := s.history[minute.Unix()/60%HistoryMinutes]
		if !bucket.Time.Equal(minute) {
			bucket = Bucket{Time: minute}
		}
		snapshot.History = append(snapshot.History, bucket)
	}
	for model, totals := range s.models {
		snapshot.Models[model] = totals
	}
	for i := 0; i < s.count; i++ {
		record := s.recent[(s.next-1-i+RecentLimit)%RecentLimit]
		snapshot.Recent = append(snapshot.Recent, cloneRequest(record))
	}
	return snapshot
}
