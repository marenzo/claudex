package proxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
)

func TestStreamErrorKindsDoNotExposeErrorText(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{"scanner limit", bufio.ErrTooLong, "event_too_large"},
		{"event limit", errCodexEventTooLarge, "event_too_large"},
		{"canceled", context.Canceled, "canceled"},
		{"deadline", context.DeadlineExceeded, "read_timeout"},
		{"network timeout", &net.DNSError{Err: "PRIVATE_DETAIL", Name: "private.example", IsTimeout: true}, "read_timeout"},
		{"truncated stream", io.ErrUnexpectedEOF, "unexpected_eof"},
		{"connection reset", syscall.ECONNRESET, "connection_reset"},
		{"broken pipe", syscall.EPIPE, "connection_reset"},
		{"http2 peer reset", errors.New("stream error: stream ID 17; INTERNAL_ERROR; received from peer"), "http2_stream_internal_error"},
		{"http2 canceled stream", errors.New("stream error: stream ID 17; CANCEL"), "http2_stream_cancel"},
		{"http2 refused stream", errors.New("stream error: stream ID 17; REFUSED_STREAM; received from peer"), "http2_stream_refused_stream"},
		{"http2 connection error", errors.New("connection error: PROTOCOL_ERROR"), "http2_connection_protocol_error"},
		{"http2 goaway", errors.New(`http2: server sent GOAWAY and closed the connection; LastStreamID=17, ErrCode=NO_ERROR, debug="PRIVATE_DETAIL\" secret"`), "http2_goaway_no_error"},
		{"unknown error", errors.New("PRIVATE_DETAIL https://private.example/secret"), "transport_read_error"},
		{"clean eof", io.EOF, "transport_read_error"},
		{"nil", nil, "transport_read_error"},
		{"unknown http2 code", errors.New("stream error: stream ID 17; PRIVATE_DETAIL"), "transport_read_error"},
		{"embedded pattern", errors.New("PRIVATE_DETAIL stream error: stream ID 17; INTERNAL_ERROR; received from peer"), "transport_read_error"},
		{"unknown cause", errors.New("stream error: stream ID 17; INTERNAL_ERROR; PRIVATE_DETAIL"), "transport_read_error"},
		{"invalid stream id", errors.New("stream error: stream ID PRIVATE_DETAIL; INTERNAL_ERROR"), "transport_read_error"},
		{"unclosed goaway debug", errors.New(`http2: server sent GOAWAY and closed the connection; LastStreamID=17, ErrCode=NO_ERROR, debug="PRIVATE_DETAIL`), "transport_read_error"},
		{"oversized error", errors.New("stream error: stream ID 17; INTERNAL_ERROR; " + strings.Repeat("PRIVATE_DETAIL", 2000)), "transport_read_error"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := streamErrorKind(test.err); got != test.want {
				t.Fatalf("category = %q, want %q", got, test.want)
			}
			if test.err != nil {
				wrapped := fmt.Errorf("PRIVATE_DETAIL: %w", test.err)
				if got := streamErrorKind(wrapped); got != test.want {
					t.Fatalf("wrapped category = %q, want %q", got, test.want)
				}
			}
		})
	}
}
