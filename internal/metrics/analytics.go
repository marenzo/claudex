package metrics

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Percentile uses nearest rank and leaves an empty sample unknown.
func Percentile(values []int64, fraction float64) *int64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	i := max(0, min(len(sorted)-1, int(math.Ceil(float64(len(sorted))*fraction))-1))
	v := sorted[i]
	return &v
}

type Sample struct {
	Records                 []Request
	History                 []Bucket
	Histogram               [8]int
	P50, P95, P99, FirstP50 *int64
	FirstCount              int
}

var HistogramLabels = []string{"≤1s", "1–5s", "5–15s", "15–30s", "30–60s", "1–2m", "2–5m", ">5m"}

func Window(snapshot Snapshot, minutes int) Sample {
	cutoff := snapshot.Generated.Truncate(time.Minute).Add(-time.Duration(minutes-1) * time.Minute)
	var s Sample
	var durations, first []int64
	bounds := []int64{1000, 5000, 15000, 30000, 60000, 120000, 300000, math.MaxInt64}
	for _, r := range snapshot.Recent {
		if r.Completed.Before(cutoff) {
			continue
		}
		s.Records = append(s.Records, r)
		durations = append(durations, r.DurationMS)
		if r.FirstOutputMS != nil {
			first = append(first, *r.FirstOutputMS)
		}
		for i, b := range bounds {
			if r.DurationMS <= b {
				s.Histogram[i]++
				break
			}
		}
	}
	for _, b := range snapshot.History {
		if !b.Time.Before(cutoff) {
			s.History = append(s.History, b)
		}
	}
	s.P50 = Percentile(durations, .5)
	s.P95 = Percentile(durations, .95)
	s.P99 = Percentile(durations, .99)
	s.FirstP50 = Percentile(first, .5)
	s.FirstCount = len(first)
	return s
}

type Filter struct{ Model, Effort, Status, Search string }

func (f Filter) Match(r Request) bool {
	if f.Model != "" && f.Model != r.Model {
		return false
	}
	if f.Effort != "" && f.Effort != r.Effort {
		return false
	}
	switch f.Status {
	case "ok":
		if r.Status >= 400 {
			return false
		}
	case "errors":
		if r.Status < 400 || r.Status == 499 {
			return false
		}
	case "canceled":
		if r.Status != 499 {
			return false
		}
	case "429":
		if r.Status != 429 {
			return false
		}
	}
	if f.Search == "" {
		return true
	}
	var details string
	if r.Error != nil {
		details = r.Error.Source + " " + r.Error.Type + " " + r.Error.Code + " " + r.Error.Message + " " + formatID(int64(r.Error.Status))
	}
	return strings.Contains(normalize(r.Model+" "+r.Effort+" "+r.ErrorKind+" "+details+" "+formatID(r.ID)+" "+formatID(int64(r.Status))), normalize(f.Search))
}
func normalize(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), "_", " "))
}
