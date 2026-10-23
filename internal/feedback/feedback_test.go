package feedback

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixtureReport() Report {
	return Report{Category: "model_behavior", Status: "recovered", Title: "Incorrect elapsed time reported", Observed: "Reported an 18 minute cap as elapsed; corrected to 56 seconds.", Expected: "Distinguish limits from measured durations.", Impact: "Misled the user about review progress.", Evidence: []string{"fixture.log: elapsed=56s cap=18m"}, Reproduction: "untested", FailureMode: "overconfidence_and_hallucination"}
}
func fixtureSubmission() Submission {
	return Submission{Source: Source{LaunchID: strings.Repeat("a", 32)}, Report: fixtureReport()}
}
func TestReportRejectsMisclassificationAndUnboundedInput(t *testing.T) {
	good := fixtureReport()
	for _, mutate := range []func(*Report){func(r *Report) { r.Category = "product_defect" }, func(r *Report) { r.FailureMode = "none" }, func(r *Report) { r.Evidence = nil }, func(r *Report) { r.Title = strings.Repeat("界", 161) }, func(r *Report) { r.Observed = "\x1b[2J" }, func(r *Report) { r.Reproduction = "confirmed" }} {
		r := good
		mutate(&r)
		if r.Validate() == nil {
			t.Fatalf("accepted invalid report: %+v", r)
		}
	}
	raw, _ := json.Marshal(good)
	for _, bad := range [][]byte{append(append([]byte(nil), raw...), []byte(` {}`)...), []byte(`{"category":"model_behavior","extra":"unexpected"}`), []byte(strings.Repeat(" ", MaxReportBytes+1))} {
		if _, err := Decode(bad); err == nil {
			t.Fatal("accepted invalid JSON")
		}
	}
	if _, err := Decode(raw); err != nil {
		t.Fatal(err)
	}
}
func TestStoreDurablePrivateIdempotentAndScoped(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: filepath.Join(t.TempDir(), "feedback")}
	sub := fixtureSubmission()
	var wg sync.WaitGroup
	receipts := make(chan Receipt, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Go(func() { r, e := s.Put(ctx, sub, "test", "configured-model"); receipts <- r; errs <- e })
	}
	wg.Wait()
	close(receipts)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	originals := 0
	id := ""
	for r := range receipts {
		if !r.Duplicate {
			originals++
		}
		if id != "" && id != r.ID {
			t.Fatal("duplicate identity changed")
		}
		id = r.ID
	}
	if originals != 1 {
		t.Fatal("duplicate records", originals)
	}
	// Recreate the store as after a gateway restart.
	records, err := (Store{Dir: s.Dir}).List(ctx)
	if err != nil || len(records) != 1 {
		t.Fatal("not durable", err, len(records))
	}
	if records[0].State != "untriaged" || records[0].ConfiguredModel != "configured-model" {
		t.Fatal("invalid provenance")
	}
	for path, want := range map[string]os.FileMode{s.Dir: 0700, filepath.Join(s.Dir, id+".json"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != want {
			t.Fatal("permissions", path, err)
		}
	}
	sub.Source.LaunchID = strings.Repeat("b", 32)
	r, err := s.Put(ctx, sub, "test", "configured-model")
	if err != nil || r.Duplicate || r.ID == id {
		t.Fatal("different launches conflated", err)
	}
}
func TestStoreRetentionFullAndSymlink(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: filepath.Join(t.TempDir(), "feedback")}
	sub := fixtureSubmission()
	receipt, err := s.Put(ctx, sub, "test", "model")
	if err != nil {
		t.Fatal(err)
	}
	records, _ := s.List(ctx)
	old := records[0]
	old.CreatedAt = time.Now().Add(-Retention - time.Hour)
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(s.Dir, receipt.ID+".json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if list, err := s.List(ctx); err != nil || len(list) != 0 {
		t.Fatal("expired record retained", err)
	}
	for i := range MaxRecords {
		r := old
		r.ID = strings.Repeat("0", 60) + fmtHex(i)
		r.CreatedAt = time.Now()
		raw, _ := json.Marshal(r)
		if err := os.WriteFile(filepath.Join(s.Dir, r.ID+".json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Put(ctx, sub, "test", "model"); !errors.Is(err, ErrFull) {
		t.Fatal("store cap", err)
	}
	path := filepath.Join(s.Dir, strings.Repeat("0", 64)+".json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", path); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(ctx); err == nil {
		t.Fatal("read symlink")
	}
}
func fmtHex(i int) string {
	const digits = "0123456789abcdef"
	return string([]byte{digits[(i>>12)&15], digits[(i>>8)&15], digits[(i>>4)&15], digits[i&15]})
}
func TestRedactionBeforePersistence(t *testing.T) {
	r := fixtureReport()
	r.Observed = "Bearer sample-token password=hunter2 sk-abcdefghijklmnop PRIVATE_LOCAL_KEY"
	clean := Redact(r, "PRIVATE_LOCAL_KEY")
	for _, secret := range []string{"sample-token", "hunter2", "sk-abcdefghijklmnop", "PRIVATE_LOCAL_KEY"} {
		if strings.Contains(clean.Observed, secret) {
			t.Fatal("secret retained")
		}
	}
	if r.Observed == clean.Observed {
		t.Fatal("input unexpectedly unchanged")
	}
}
func TestMCPProtocolAndLiveDisable(t *testing.T) {
	report, _ := json.Marshal(fixtureReport())
	calls := 0
	checks := 0
	m := MCP{Source: fixtureSubmission().Source, Enabled: func(context.Context) (bool, error) { checks++; return checks < 3, nil }, Submit: func(_ context.Context, sub Submission) (Receipt, error) {
		calls++
		if sub.Source.ClientVersion != "fixture-cli" {
			t.Error("missing client version")
		}
		return Receipt{ID: "local-id", State: "untriaged"}, nil
	}}
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25","clientInfo":{"version":"fixture-cli"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"ClaudexFeedback","arguments":` + string(report) + `}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ClaudexFeedback","arguments":` + string(report) + `}}
{"jsonrpc":"2.0","id":5,"method":"tools/list"}
`
	var out bytes.Buffer
	if err := m.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 5 || calls != 1 {
		t.Fatal("protocol/disable failure", calls, out.String())
	}
	var reply struct {
		Result struct {
			IsError bool  `json:"isError"`
			Tools   []any `json:"tools"`
		} `json:"result"`
	}
	json.Unmarshal([]byte(lines[3]), &reply)
	if !reply.Result.IsError {
		t.Fatal("disabled tool claimed success")
	}
	json.Unmarshal([]byte(lines[4]), &reply)
	if len(reply.Result.Tools) != 0 {
		t.Fatal("disabled tool exposed")
	}
}
func TestMCPMalformedAndFailedSubmission(t *testing.T) {
	m := MCP{Enabled: func(context.Context) (bool, error) { return true, nil }, Submit: func(context.Context, Submission) (Receipt, error) {
		return Receipt{}, errors.New("secret internal details")
	}}
	var out bytes.Buffer
	report, _ := json.Marshal(fixtureReport())
	input := "not json\n" + `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"unknown"}}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"ClaudexFeedback","arguments":` + string(report) + `}}` + "\n"
	if err := m.Serve(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"code":-32700`) || !strings.Contains(out.String(), `"isError":true`) || strings.Contains(out.String(), "secret internal") {
		t.Fatal(out.String())
	}
}
