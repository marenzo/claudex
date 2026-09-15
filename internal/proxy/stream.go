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

	"github.com/marenzo/claudex/internal/dashboard"
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

func (s *Server) consume(w http.ResponseWriter, r *http.Request, body io.Reader, original, translated []byte, account string, stream bool, record *dashboard.Request, inputTokens func() (int64, error)) int {
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
		return http.NewResponseController(w).Flush() == nil
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
		record.ErrorKind = kind
		// The transport may include response content or GOAWAY debug data in its
		// error. Only a bounded category and the Go type are safe to retain.
		slog.Warn("Codex stream failed", "kind", kind, "error_type", fmt.Sprintf("%T", err),
			"model", model, "elapsed_ms", time.Since(record.Started).Milliseconds(),
			"received_bytes", wireBytes, "response_started", started,
			"meaningful_output", sawOutputDelta, "client_canceled", r.Context().Err() != nil)
		e := &apiError{Status: 502, Type: "api_error", Message: message + " (" + kind + ")"}
		if err != nil {
			e.Details = &dashboard.ErrorDetails{Source: "transport", Type: fmt.Sprintf("%T", err), Message: err.Error()}
		}
		return fail(e)
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
			if started && !write([]byte("event: ping\ndata: {\"type\":\"ping\"}\n\n")) {
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
				if record.FirstOutputMS == nil {
					elapsed := time.Since(record.Started).Milliseconds()
					record.FirstOutputMS = &elapsed
				}
			}
			if usage := e.Get("response.usage"); usage.Get("input_tokens").Type == gjson.Number && usage.Get("output_tokens").Type == gjson.Number {
				record.UsageReported = true
				record.Usage = dashboard.Usage{Input: max(0, usage.Get("input_tokens").Int()), Cached: max(0, usage.Get("input_tokens_details.cached_tokens").Int()), Output: max(0, usage.Get("output_tokens").Int()), Reasoning: max(0, usage.Get("output_tokens_details.reasoning_tokens").Int())}
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
							Details: &dashboard.ErrorDetails{Source: "upstream", Status: 200, Type: kind, Code: reason}})
					}
				}
				event.data = hydrateOutput(event.data, items, fallback)
				if !stream {
					result, err := claude.ConvertResponse(model, original, event.data)
					if err != nil {
						return fail(&apiError{Status: 502, Type: "api_error", Message: err.Error()})
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(200)
					if _, errWrite := w.Write(result); errWrite != nil {
						return 499
					}
					return 200
				}
			}
			if !stream {
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
				return 200
			}
		}
	}
}

// Codex can send complete items separately and leave response.output empty.
func hydrateOutput(data []byte, items map[int64][]byte, fallback [][]byte) []byte {
	indexes := make([]int64, 0, len(items))
	for index := range items {
		indexes = append(indexes, index)
	}
	sort.Slice(indexes, func(i, j int) bool { return indexes[i] < indexes[j] })
	ordered := make([][]byte, 0, len(items)+len(fallback))
	for _, index := range indexes {
		ordered = append(ordered, items[index])
	}
	ordered = append(ordered, fallback...)
	// Preserve terminal-only items and use completed item bodies to fill a partial
	// terminal output. Stable IDs deduplicate the repeated terminal summaries.
	seen := make(map[string]struct{})
	for _, item := range ordered {
		if id := gjson.GetBytes(item, "id").String(); id != "" {
			seen[id] = struct{}{}
		}
	}
	for _, item := range gjson.GetBytes(data, "response.output").Array() {
		id := item.Get("id").String()
		if _, exists := seen[id]; id != "" && exists {
			continue
		}
		ordered = append(ordered, []byte(item.Raw))
		if id != "" {
			seen[id] = struct{}{}
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
