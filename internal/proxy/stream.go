package proxy

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/marenzo/claudex/internal/metrics"
	"github.com/marenzo/claudex/internal/tokens"
	claude "github.com/marenzo/claudex/internal/translator/codex/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type streamEvent struct {
	data []byte
	err  error
}

// One bounded reader belongs to the HTTP request and exits on cancellation.
func readEvents(ctx context.Context, body io.Reader, events chan<- streamEvent) {
	defer close(events)
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 64*1024), maxRequestBytes)
	var data []byte
	emit := func(event streamEvent) bool {
		select {
		case events <- event:
			return true
		case <-ctx.Done():
			return false
		}
	}
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			if len(data) > 0 && !emit(streamEvent{data: data}) {
				return
			}
			data = nil
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			value := bytes.TrimPrefix(line[5:], []byte(" "))
			if len(data)+len(value)+1 > maxRequestBytes {
				emit(streamEvent{err: errCodexEventTooLarge})
				return
			}
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, value...)
		}
	}
	if errScan := scanner.Err(); errScan != nil {
		emit(streamEvent{err: errScan})
		return
	}
	if len(data) > 0 {
		emit(streamEvent{data: data})
	}
}

func (s *Server) consume(w http.ResponseWriter, r *http.Request, body io.Reader, original, translated []byte, account string, stream bool, record *metrics.Request, inputTokens func() (int64, error), review *safeguardReview) int {
	return s.consumeAttempt(w, r, body, original, translated, account, stream, record, inputTokens, review, nil)
}

func (s *Server) consumeAttempt(w http.ResponseWriter, r *http.Request, body io.Reader, original, translated []byte, account string, stream bool, record *metrics.Request, inputTokens func() (int64, error), review *safeguardReview, recovery *streamRecovery) int {
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	events := make(chan streamEvent, 1)
	go readEvents(ctx, body, events)
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	// Keep-alive pings hide a stalled upstream from the client, so bound the
	// silence between upstream events ourselves.
	idle := time.NewTimer(streamIdleTimeout)
	defer idle.Stop()
	started := false
	if recovery != nil {
		started = recovery.writer.started
	}
	model := gjson.GetBytes(translated, "model").String()
	state := claude.NewStream(model, original)
	var pending [][]byte
	pendingBytes := 0
	items := make(map[int64][]byte)
	var fallback [][]byte
	outputBytes := 0
	sawOutputDelta := false
	wireBytes := 0
	write := func(data []byte) bool {
		if ctx.Err() != nil {
			return false
		}
		if !started {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.Header().Set("X-Accel-Buffering", "no")
			w.WriteHeader(200)
			started = true
		}
		if _, errWrite := w.Write(data); errWrite != nil {
			return false
		}
		if http.NewResponseController(w).Flush() != nil {
			return false
		}
		if record.ClientFirstOutputMS == nil && hasClientOutput(data) {
			elapsed := time.Since(record.Started).Milliseconds()
			record.ClientFirstOutputMS = &elapsed
		}
		return true
	}
	fail := func(e *apiError) int {
		if ctx.Err() != nil {
			record.ErrorKind = "canceled"
			return 499
		}
		recordAPIError(record, e)
		if !e.QuotaUntil.IsZero() {
			s.Quota.Limited(account, e.QuotaUntil)
		}
		if started {
			write(append(append([]byte("event: error\ndata: "), e.body()...), '\n', '\n'))
		} else {
			if e.Status == 429 {
				w.Header().Set("Retry-After", "60")
			}
			writeError(w, e)
		}
		return e.Status
	}
	streamFailure := func(kind, message string, err error) int {
		// Both the reader and request context can become ready on cancellation.
		// Attribute it to the caller whichever select case wins.
		if ctx.Err() != nil {
			record.ErrorKind = "canceled"
			return 499
		}
		retrying := recovery != nil && recovery.allowed && !recovery.writer.committed && transientTransport(err)
		// The transport may include response content or GOAWAY debug data in its
		// error. Only a bounded category and the Go type are safe to retain.
		slog.Warn("Codex stream failed", "kind", kind, "error_type", fmt.Sprintf("%T", err),
			"model", model, "elapsed_ms", time.Since(record.Started).Milliseconds(),
			"received_bytes", wireBytes, "response_started", started,
			"meaningful_output", sawOutputDelta, "client_canceled", r.Context().Err() != nil,
			"trace_id", record.TraceID, "retrying", retrying)
		if retrying {
			recovery.err = err
			return 502
		}
		record.ErrorKind = kind
		e := &apiError{Status: 502, Type: "api_error", Message: message + " (" + kind + ")"}
		if err != nil {
			e.Details = &metrics.ErrorDetails{Source: "transport", Type: fmt.Sprintf("%T", err), Message: err.Error()}
		}
		return fail(e)
	}
	finishReview := func() ([]any, int) {
		started := time.Now()
		if len(review.calls) > 0 {
			defer func() { elapsed := time.Since(started).Milliseconds(); record.ReviewMS = &elapsed }()
		}
		type outcome struct {
			verdicts map[string]toolVerdict
			err      error
		}
		done := make(chan outcome, 1)
		go func() { verdicts, err := s.classifyTools(ctx, review, account); done <- outcome{verdicts, err} }()
		for {
			select {
			case <-ctx.Done():
				return nil, 499
			case <-ping.C:
				if stream && !write([]byte("event: ping\ndata: {\"type\":\"ping\"}\n\n")) {
					return nil, 499
				}
			case result := <-done:
				if ctx.Err() != nil {
					return nil, 499
				}
				if result.err != nil {
					record.ErrorKind = classifierErrorKind(result.err)
					return nil, fail(&apiError{Status: 502, Type: "api_error", Message: "Auto-mode safety review failed (" + record.ErrorKind + "); no tool calls were released. Retry or use client-side classification.",
						Details: &metrics.ErrorDetails{Source: "review", Type: record.ErrorKind, Message: "Auto-mode safety review failed (" + record.ErrorKind + "). No tool calls were released."}})
				}
				return safeguardResults(result.verdicts), 0
			}
		}
	}
	// Translate the bounded handshake batch before committing headers so a
	// failure in the first content event retains its HTTP error status.
	emit := func(batch [][]byte) int {
		chunks := make([][]byte, 0, len(batch))
		for _, data := range batch {
			chunk, err := state.Convert(append([]byte("data: "), data...))
			if err != nil {
				return fail(&apiError{Status: 502, Type: "api_error", Message: err.Error()})
			}
			if len(chunk) == 0 {
				continue
			}
			if bytes.Contains(chunk, []byte(`"type":"message_start"`)) {
				lines := bytes.Split(chunk, []byte("\n"))
				for i, line := range lines {
					if !bytes.HasPrefix(line, []byte("data:")) {
						continue
					}
					payload := bytes.TrimSpace(line[5:])
					if gjson.GetBytes(payload, "type").String() == "message_start" && gjson.GetBytes(payload, "message.usage.input_tokens").Int() == 0 {
						if count, errCount := inputTokenCount(inputTokens, original); errCount == nil {
							payload, _ = sjson.SetBytes(payload, "message.usage.input_tokens", count)
						}
						lines[i] = append([]byte("data: "), payload...)
					}
				}
				chunk = bytes.Join(lines, []byte("\n"))
			}
			chunks = append(chunks, chunk)
		}
		for _, chunk := range chunks {
			if review != nil {
				if err := review.hold(chunk); err != nil {
					return fail(&apiError{Status: 502, Type: "api_error", Message: err.Error()})
				}
				continue
			}
			if !write(chunk) {
				return 499
			}
		}
		return 0
	}
	for {
		select {
		case <-ctx.Done():
			record.ErrorKind = "canceled"
			return 499
		case <-ping.C:
			if stream && (started || review != nil) && !write([]byte("event: ping\ndata: {\"type\":\"ping\"}\n\n")) {
				return 499
			}
		case <-idle.C:
			// The deferred cancel stops the reader goroutine and releases the upstream body.
			return streamFailure("upstream_stalled", "Codex stream stalled: no data received for "+streamIdleTimeout.String(), errStreamStalled)
		case event, ok := <-events:
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(streamIdleTimeout)
			if !ok {
				return streamFailure("incomplete_stream", "Codex stream ended before completion", io.EOF)
			}
			if event.err != nil {
				return streamFailure(streamErrorKind(event.err), "Codex stream was interrupted", event.err)
			}
			if bytes.Equal(event.data, []byte("[DONE]")) {
				continue
			}
			if !gjson.ValidBytes(event.data) {
				return fail(&apiError{Status: 502, Type: "api_error", Message: "Codex sent malformed stream data"})
			}
			// Bound all streamed data, including arguments and deferred translator
			// events that arrive before output_item.done. Inference duration stays uncapped.
			wireBytes += len(event.data)
			if wireBytes > 2*maxRequestBytes {
				return streamFailure("stream_too_large", "Codex stream exceeds size limit", nil)
			}
			e := gjson.ParseBytes(event.data)
			kind := e.Get("type").String()
			if HasMeaningfulCodexOutputDelta(event.data) {
				sawOutputDelta = true
				if record.UpstreamFirstOutputMS == nil {
					elapsed := time.Since(record.Started).Milliseconds()
					record.UpstreamFirstOutputMS = &elapsed
				}
			}
			if usage := e.Get("response.usage"); usage.Get("input_tokens").Type == gjson.Number && usage.Get("output_tokens").Type == gjson.Number {
				record.UsageReported = true
				record.Usage = metrics.Usage{Input: max(0, usage.Get("input_tokens").Int()), Cached: max(0, usage.Get("input_tokens_details.cached_tokens").Int()), Output: max(0, usage.Get("output_tokens").Int()), Reasoning: max(0, usage.Get("output_tokens_details.reasoning_tokens").Int())}
			}
			if kind == "error" || kind == "response.failed" {
				return fail(parseUpstreamError(200, event.data, time.Now()))
			}
			if strings.EqualFold(e.Get("item.name").String(), "Artifact") {
				return fail(&apiError{Status: 502, Type: "api_error", Message: "Artifact publishing is disabled; use local file tools"})
			}
			if kind == "response.output_item.done" && e.Get("item").IsObject() {
				item := []byte(e.Get("item").Raw)
				outputBytes += len(item)
				if outputBytes > maxRequestBytes {
					return fail(&apiError{Status: 502, Type: "api_error", Message: "Codex output exceeds size limit"})
				}
				if index := e.Get("output_index"); index.Exists() {
					items[index.Int()] = item
				} else {
					fallback = append(fallback, item)
				}
			}
			terminal := kind == "response.completed" || kind == "response.incomplete"
			if terminal {
				if !e.Get("response").IsObject() || (e.Get("response.output").Exists() && !e.Get("response.output").IsArray()) {
					return fail(&apiError{Status: 502, Type: "api_error", Message: "Codex sent malformed terminal response data"})
				}
				if status := e.Get("response.status"); status.Exists() && (status.Type != gjson.String || status.String() != strings.TrimPrefix(kind, "response.")) {
					return fail(&apiError{Status: 502, Type: "api_error", Message: "Codex sent inconsistent terminal response status"})
				}
				if err := validateTerminalToolSnapshots(e.Get("response.output"), items, fallback); err != nil {
					return fail(&apiError{Status: 502, Type: "api_error", Message: err.Error()})
				}
				if e.Get("response.error").IsObject() {
					return fail(parseUpstreamError(200, event.data, time.Now()))
				}
				if IsCodexTerminalEmptyIncomplete(event.data, len(items)+len(fallback), stream && sawOutputDelta) {
					return fail(&apiError{Status: 502, Type: "api_error", Message: CodexEmptyIncompleteStreamMessage})
				}
				for _, item := range e.Get("response.output").Array() {
					if strings.EqualFold(item.Get("name").String(), "Artifact") {
						return fail(&apiError{Status: 502, Type: "api_error", Message: "Artifact publishing is disabled; use local file tools"})
					}
				}
				if kind == "response.incomplete" {
					reason := e.Get("response.incomplete_details.reason").String()
					switch reason {
					case "max_output_tokens", "content_filter":
					default:
						return fail(&apiError{Status: 502, Type: "api_error", Message: "Codex terminated with an incomplete response",
							Details: &metrics.ErrorDetails{Source: "upstream", Status: 200, Type: kind, Code: reason}})
					}
				}
				event.data = hydrateOutput(event.data, items, fallback)
				if !stream {
					if _, err := state.Convert(append([]byte("data: "), event.data...)); err != nil {
						return fail(&apiError{Status: 502, Type: "api_error", Message: err.Error()})
					}
					result, err := claude.ConvertResponse(model, original, event.data)
					if err != nil {
						return fail(&apiError{Status: 502, Type: "api_error", Message: err.Error()})
					}
					if review != nil {
						if err := review.readMessage(result); err != nil {
							return fail(&apiError{Status: 502, Type: "api_error", Message: err.Error()})
						}
						results, status := finishReview()
						if status != 0 {
							return status
						}
						result, _ = sjson.SetBytes(result, "safeguard_results", results)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					if _, errWrite := w.Write(result); errWrite != nil {
						return 499
					}
					if hasMessageOutput(result) {
						elapsed := time.Since(record.Started).Milliseconds()
						record.ClientFirstOutputMS = &elapsed
					}
					return 200
				}
			}
			if !stream {
				// JSON replies consume the same upstream event stream. Validate its
				// complete history too; a later snapshot must not erase a changed call.
				if _, err := state.Convert(append([]byte("data: "), event.data...)); err != nil {
					return fail(&apiError{Status: 502, Type: "api_error", Message: err.Error()})
				}
				continue
			}
			// Keep handshake events uncommitted so early quota failures retain HTTP 429.
			if !started && (kind == "response.created" || kind == "response.in_progress" || kind == "codex.rate_limits" || kind == "codex.response.metadata") && len(pending) < 16 && pendingBytes+len(event.data) < 1024*1024 {
				pending = append(pending, event.data)
				pendingBytes += len(event.data)
				continue
			}
			if status := emit(append(pending, event.data)); status != 0 {
				return status
			}
			pending = nil
			if terminal {
				if review != nil {
					results, status := finishReview()
					if status != 0 {
						return status
					}
					for _, chunk := range review.chunks {
						if !write(attachSafeguards(chunk, results)) {
							return 499
						}
					}
				}
				return 200
			}
		}
	}
}

// Pings, message_start, usage and empty blocks are not output. Count only
// translated content actually flushed to the client, including complete tool
// blocks whose upstream emits no argument delta.
func hasClientOutput(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue
		}
		e := gjson.ParseBytes(bytes.TrimSpace(line[5:]))
		switch e.Get("type").String() {
		case "content_block_delta":
			for _, key := range []string{"text", "thinking", "partial_json"} {
				if e.Get("delta."+key).String() != "" {
					return true
				}
			}
		case "content_block_start":
			part := e.Get("content_block")
			if part.Get("text").String() != "" || part.Get("thinking").String() != "" || part.Get("type").String() == "tool_use" {
				return true
			}
		}
	}
	return false
}

func hasMessageOutput(data []byte) bool {
	for _, part := range gjson.GetBytes(data, "content").Array() {
		if part.Get("text").String() != "" || part.Get("thinking").String() != "" || part.Get("type").String() == "tool_use" {
			return true
		}
	}
	return false
}

// Codex can send complete items separately and leave response.output empty.
func hydrateOutput(data []byte, items map[int64][]byte, fallback [][]byte) []byte {
	// Completed output indexes anchor sparse terminal items in their original
	// positions. Appending terminal-only text after all calls changes its meaning.
	slots := make(map[int64][]byte, len(items))
	byID := make(map[string]int64)
	for index, item := range items {
		slots[index] = item
		if id := gjson.GetBytes(item, "id").String(); id != "" {
			byID[id] = index
		}
	}
	var cursor int64
	for _, item := range gjson.GetBytes(data, "response.output").Array() {
		id := item.Get("id").String()
		if index, exists := byID[id]; id != "" && exists {
			cursor = index + 1
			continue
		}
		for slots[cursor] != nil {
			cursor++
		}
		slots[cursor] = []byte(item.Raw)
		if id != "" {
			byID[id] = cursor
		}
		cursor++
	}
	indexes := make([]int64, 0, len(slots))
	for index := range slots {
		indexes = append(indexes, index)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	ordered := make([][]byte, 0, len(slots)+len(fallback))
	for _, index := range indexes {
		ordered = append(ordered, slots[index])
	}
	for _, item := range fallback {
		id := gjson.GetBytes(item, "id").String()
		if _, exists := byID[id]; id != "" && exists {
			continue
		}
		ordered = append(ordered, item)
		if id != "" {
			byID[id] = -1
		}
	}

	if len(ordered) == 0 {
		return data
	}
	array := append(append([]byte("["), bytes.Join(ordered, []byte(","))...), ']')
	data, _ = sjson.SetRawBytes(data, "response.output", array)
	return data
}

// streamIdleTimeout bounds the silence between upstream stream events. Long
// reasoning still emits events, so a fully silent stream indicates a stalled
// connection rather than a slow model. Tests shorten it.
var streamIdleTimeout = 10 * time.Minute

var errStreamStalled = errors.New("upstream stream stalled")

// countInputTokensAsync starts the local input token count right away and
// returns a function that waits for the result.
func countInputTokensAsync(raw []byte) func() (int64, error) {
	done := make(chan struct{})
	var count int64
	var err error
	go func() {
		defer close(done)
		count, err = tokens.CountClaudeInputTokens(raw)
	}()
	return func() (int64, error) {
		<-done
		return count, err
	}
}

// inputTokenCount uses a count started earlier when there is one, and counts
// synchronously otherwise.
func inputTokenCount(async func() (int64, error), original []byte) (int64, error) {
	if async != nil {
		return async()
	}
	return tokens.CountClaudeInputTokens(original)
}

// Hydration fills sparse terminal arrays; it must not hide a contradictory call.
func validateTerminalToolSnapshots(output gjson.Result, items map[int64][]byte, fallback [][]byte) error {
	byID := make(map[string]gjson.Result)
	remember := func(raw []byte) {
		item := gjson.ParseBytes(raw)
		if id := item.Get("id").String(); id != "" {
			byID[id] = item
		}
	}
	for _, raw := range items {
		remember(raw)
	}
	for _, raw := range fallback {
		remember(raw)
	}
	seen := make(map[string]bool)
	for _, item := range output.Array() {
		if id := item.Get("id").String(); id != "" {
			if seen[id] {
				return errors.New("duplicate terminal output ID from Codex")
			}
			seen[id] = true
		}
		old, ok := byID[item.Get("id").String()]
		if !ok || (old.Get("type").String() != "function_call" && item.Get("type").String() != "function_call") {
			continue
		}
		for _, field := range []string{"type", "name", "call_id", "arguments"} {
			value, prior := item.Get(field), old.Get(field)
			if value.Exists() && prior.Exists() && value.String() != "" && prior.String() != "" && value.String() != prior.String() {
				return errors.New("inconsistent terminal tool call from Codex")
			}
		}
	}
	return nil
}
