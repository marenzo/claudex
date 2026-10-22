package metrics

import (
	"testing"
	"time"
)

func TestWindowCalendarBoundaryAndNearestRank(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 15, 30, 0, time.UTC)
	first := int64(42)
	s := Snapshot{Generated: now, Recent: []Request{{ID: 1, Completed: now.Add(-14*time.Minute - 30*time.Second), DurationMS: 1000, FirstOutputMS: &first}, {ID: 2, Completed: now.Add(-14*time.Minute - 31*time.Second), DurationMS: 90000}, {ID: 3, Completed: now, DurationMS: 5000, Status: 499}}}
	sample := Window(s, 15)
	if len(sample.Records) != 2 || sample.P50 == nil || *sample.P50 != 1000 || *sample.P95 != 5000 || *sample.FirstP50 != 42 || sample.Histogram[0] != 1 || sample.Histogram[1] != 1 {
		t.Fatalf("sample %+v", sample)
	}
	if Percentile(nil, .5) != nil {
		t.Fatal("missing sample treated as zero")
	}
}
func TestRequestFiltersSeparateCancellationAndSearchErrors(t *testing.T) {
	r := Request{ID: 17, Model: "gpt-6-astra", Effort: "high", Status: 499, Error: &ErrorDetails{Message: "read_timeout"}}
	if (Filter{Status: "errors"}).Match(r) || !(Filter{Status: "canceled", Search: "READ TIMEOUT"}).Match(r) || (Filter{Status: "ok"}).Match(r) || !(Filter{Search: "17"}).Match(r) {
		t.Fatal("filter semantics changed")
	}
}
