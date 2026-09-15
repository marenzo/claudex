package quota

import "testing"

func TestCodexUsageAllowsRecovery(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"available", `{"rate_limit":{"allowed":true,"limit_reached":false}}`, true},
		{"all pools available", `{"rate_limit":{"allowed":true,"limit_reached":false},"additional_rate_limits":[{"rate_limit":{"allowed":true,"limit_reached":false}}],"rate_limit_reached_type":null}`, true},
		{"review independent", `{"rate_limit":{"allowed":true,"limit_reached":false},"code_review_rate_limit":{"allowed":false,"limit_reached":true}}`, true},
		{"exhausted", `{"rate_limit":{"allowed":false,"limit_reached":true}}`, false},
		{"conflicting flags", `{"rate_limit":{"allowed":true,"limit_reached":true}}`, false},
		{"missing flag", `{"rate_limit":{"allowed":true}}`, false},
		{"missing allowance", `{}`, false},
		{"null allowance", `{"rate_limit":null}`, false},
		{"wrong flag type", `{"rate_limit":{"allowed":"true","limit_reached":false}}`, false},
		{"limited additional pool", `{"rate_limit":{"allowed":true,"limit_reached":false},"additional_rate_limits":[{"rate_limit":{"allowed":false,"limit_reached":true}}]}`, false},
		{"unknown additional pool", `{"rate_limit":{"allowed":true,"limit_reached":false},"additional_rate_limits":[{}]}`, false},
		{"wrong additional shape", `{"rate_limit":{"allowed":true,"limit_reached":false},"additional_rate_limits":{}}`, false},
		{"limited marker", `{"rate_limit":{"allowed":true,"limit_reached":false},"rate_limit_reached_type":"weekly"}`, false},
		{"error response", `{"rate_limit":{"allowed":true,"limit_reached":false},"error":{"message":"failed"}}`, false},
		{"invalid JSON", `{"rate_limit":`, false},
		{"trailing JSON", `{"rate_limit":{"allowed":true,"limit_reached":false}} {}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CodexUsageAllowsRecovery([]byte(tc.body)); got != tc.want {
				t.Fatalf("recovery allowed = %v, want %v", got, tc.want)
			}
		})
	}
}
