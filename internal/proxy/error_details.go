package proxy

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/marenzo/claudex/internal/dashboard"
)

const errorMessageLimit = 4096

var (
	errorBearer      = regexp.MustCompile(`(?i)\bBearer\s+[^\s"'<>]+`)
	errorSecretField = regexp.MustCompile(`(?i)(["']?(?:access_token|refresh_token|id_token|api[_-]?key|client[_-]?key|authorization|cookie|account[_-]?id)["']?\s*[:=]\s*)(?:"[^"\r\n]*"|'[^'\r\n]*'|[^\s,;&<>]+)`)
	errorJWT         = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`)
	errorAPIKey      = regexp.MustCompile(`\b(?:sk-|sess-|ghp_|github_pat_)[A-Za-z0-9_-]{12,}`)
	errorEmail       = regexp.MustCompile(`(?i)\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b`)
)

func recordAPIError(record *dashboard.Request, e *apiError) {
	if e.Details != nil {
		detail := *e.Details
		record.Error = &detail
	} else {
		record.Error = &dashboard.ErrorDetails{Source: "proxy", Type: e.Type, Message: e.Message}
	}
	if record.Error.Message == "" {
		record.Error.Message = e.Message
	}
}

// Only error excerpts reach this function, never normal request/response bodies.
// Exact known credentials are removed before truncation; common credential and
// identity formats are additionally redacted. Arbitrary upstream echoes cannot
// be guaranteed free of sensitive data, so excerpts stay in authenticated memory.
func redactErrorDetails(detail *dashboard.ErrorDetails, secrets []string) *dashboard.ErrorDetails {
	if detail == nil {
		return nil
	}
	result := *detail
	for _, field := range []struct {
		value *string
		limit int
	}{{&result.Type, 128}, {&result.Code, 128}, {&result.Message, errorMessageLimit}} {
		value := *field.value
		for _, secret := range secrets {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, "[redacted]")
			}
		}
		value = errorBearer.ReplaceAllString(value, "Bearer [redacted]")
		value = errorSecretField.ReplaceAllString(value, "${1}[redacted]")
		value = errorJWT.ReplaceAllString(value, "[redacted]")
		value = errorAPIKey.ReplaceAllString(value, "[redacted]")
		value = errorEmail.ReplaceAllString(value, "[redacted]")
		value = strings.Map(func(r rune) rune {
			if unicode.IsControl(r) && r != '\n' && r != '\t' {
				return -1
			}
			return r
		}, strings.ToValidUTF8(value, "�"))
		if len(value) > field.limit {
			value = value[:field.limit]
			for !utf8.ValidString(value) {
				value = value[:len(value)-1]
			}
			result.Truncated = true
		}
		// Slicing alone can keep a multi-megabyte upstream string alive in the
		// recent-request ring. Own only the bounded excerpt's backing storage.
		*field.value = strings.Clone(value)
	}
	return &result
}
