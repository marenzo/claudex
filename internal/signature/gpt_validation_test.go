package signature

import (
	"encoding/base64"
	"strings"
	"testing"
)

func testGPTReasoningSignature() string {
	payload := make([]byte, 1+8+16+16+32)
	payload[0] = 0x80
	for i := 9; i < len(payload); i++ {
		payload[i] = byte(i)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func TestValidGPTReasoningSignature(t *testing.T) {
	if !IsValidGPTReasoningSignature(testGPTReasoningSignature()) {
		t.Fatal("valid GPT signature rejected")
	}
}

func TestValidateGPTReasoningSignatureRejectsUnicodeEllipsis(t *testing.T) {
	sig := testGPTReasoningSignature()
	polluted := sig[:20] + string(rune(0x2026)) + sig[20:]

	err := validateGPTReasoningSignature(polluted)
	if err == nil {
		t.Fatal("expected invalid GPT reasoning signature")
	}
	if !strings.Contains(err.Error(), "non-base64url character U+2026") {
		t.Fatalf("error = %q, want U+2026 base64url detail", err.Error())
	}
}
