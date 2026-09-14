package proxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"
	"syscall"
)

var errCodexEventTooLarge = errors.New("codex event exceeds size limit")

// Go's HTTP/2 transport error types are internal to net/http. Match only their
// known formats and protocol codes, never arbitrary error text or GOAWAY debug
// data. The result is a bounded diagnostic category suitable for public logs.
const http2Codes = `(NO_ERROR|PROTOCOL_ERROR|INTERNAL_ERROR|FLOW_CONTROL_ERROR|SETTINGS_TIMEOUT|STREAM_CLOSED|FRAME_SIZE_ERROR|REFUSED_STREAM|CANCEL|COMPRESSION_ERROR|CONNECT_ERROR|ENHANCE_YOUR_CALM|INADEQUATE_SECURITY|HTTP_1_1_REQUIRED)`

var (
	http2StreamError     = regexp.MustCompile(`^stream error: stream ID [0-9]+; ` + http2Codes + `(?:; received from peer)?$`)
	http2ConnectionError = regexp.MustCompile(`^connection error: ` + http2Codes + `$`)
	http2GoAwayError     = regexp.MustCompile(`^http2: server sent GOAWAY and closed the connection; LastStreamID=[0-9]+, ErrCode=` + http2Codes + `, debug="(?:[^"\\]|\\.)*"$`)
)

func streamErrorKind(err error) string {
	switch {
	case errors.Is(err, bufio.ErrTooLong), errors.Is(err, errCodexEventTooLarge):
		return "event_too_large"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "read_timeout"
	case errors.Is(err, io.ErrUnexpectedEOF):
		return "unexpected_eof"
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.EPIPE):
		return "connection_reset"
	}
	var networkError net.Error
	if errors.As(err, &networkError) && networkError.Timeout() {
		return "read_timeout"
	}
	for current, depth := err, 0; current != nil && depth < 8; current, depth = errors.Unwrap(current), depth+1 {
		message := current.Error()
		if len(message) > 16*1024 {
			continue
		}
		if match := http2StreamError.FindStringSubmatch(message); match != nil {
			return "http2_stream_" + strings.ToLower(match[1])
		}
		if match := http2ConnectionError.FindStringSubmatch(message); match != nil {
			return "http2_connection_" + strings.ToLower(match[1])
		}
		if match := http2GoAwayError.FindStringSubmatch(message); match != nil {
			return "http2_goaway_" + strings.ToLower(match[1])
		}
	}
	return "transport_read_error"
}
