package proxy

import (
	"testing"

	"github.com/marenzo/claudex/internal/config"
	"github.com/tidwall/gjson"
)

func TestReasoningPolicy(t *testing.T) {
	tests := []struct {
		name, model, fallback, fields, want string
		wantError                           bool
	}{
		{"default", "astra", "high", ``, "high", false},
		{"explicit max", "astra", "high", `,"output_config":{"effort":"max"}`, "max", false},
		{"auto with ultra default", "astra", "ultra", `,"output_config":{"effort":"auto"}`, "xhigh", false},
		{"empty with ultra default", "astra", "ultra", `,"output_config":{"effort":""}`, "xhigh", false},
		{"ultracode alias", "astra", "high", `,"output_config":{"effort":"ultracode"}`, "xhigh", false},
		{"disabled astra", "astra", "high", `,"thinking":{"type":"disabled"},"output_config":{"effort":"max"}`, "low", false},
		{"disabled terra", "terra", "high", `,"thinking":{"type":"disabled"},"output_config":{"effort":"max"}`, "none", false},
		{"disabled sol", "sol", "high", `,"thinking":{"type":"disabled"}`, "none", false},
		{"disabled luna", "luna", "high", `,"thinking":{"type":"disabled"}`, "none", false},
		{"explicit none astra rejected", "astra", "high", `,"output_config":{"effort":"none"}`, "", true},
		{"explicit none sol", "sol", "high", `,"output_config":{"effort":"none"}`, "none", false},
		{"minimal compatibility", "astra", "high", `,"output_config":{"effort":"minimal"}`, "low", false},
		{"budget auto", "astra", "ultra", `,"thinking":{"type":"enabled","budget_tokens":-1}`, "xhigh", false},
		{"budget low", "astra", "high", `,"thinking":{"type":"enabled","budget_tokens":512}`, "low", false},
		{"budget medium", "terra", "high", `,"thinking":{"type":"enabled","budget_tokens":8192}`, "medium", false},
		{"budget high", "terra", "low", `,"thinking":{"type":"enabled","budget_tokens":24576}`, "high", false},
		{"budget xhigh", "terra", "low", `,"thinking":{"type":"enabled","budget_tokens":32768}`, "xhigh", false},
		{"explicit beats budget", "terra", "low", `,"thinking":{"type":"enabled","budget_tokens":1024},"output_config":{"effort":"max"}`, "max", false},
		{"negative budget rejected", "terra", "high", `,"thinking":{"type":"enabled","budget_tokens":-2}`, "", true},
		{"fractional budget rejected", "terra", "high", `,"thinking":{"type":"enabled","budget_tokens":1.5}`, "", true},
		{"string budget rejected", "terra", "high", `,"thinking":{"type":"enabled","budget_tokens":"1024"}`, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, _, err := prepareRequest([]byte(`{"messages":[]`+tt.fields+`}`), config.Config{Model: tt.model, ReasoningEffort: tt.fallback})
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, want error %t", err, tt.wantError)
			}
			if err == nil && gjson.GetBytes(body, "reasoning.effort").String() != tt.want {
				t.Fatalf("reasoning = %s, want effort %s", gjson.GetBytes(body, "reasoning").Raw, tt.want)
			}
		})
	}
}

func TestReasoningSummaryVisibility(t *testing.T) {
	for _, fields := range []string{``, `,"thinking":{"type":"adaptive"}`, `,"thinking":{"type":"adaptive","display":"omitted"}`, `,"thinking":{"type":"disabled"}`} {
		raw := []byte(`{"messages":[]` + fields + `}`)
		body, _, err := prepareRequest(raw, config.Config{Model: "gpt-6-astra", ReasoningEffort: "high"})
		if err != nil {
			t.Fatal(err)
		}
		hidden := gjson.GetBytes(raw, "thinking.display").String() == "omitted" || gjson.GetBytes(raw, "thinking.type").String() == "disabled"
		if got := gjson.GetBytes(body, "reasoning.summary").Exists(); got == hidden {
			t.Fatalf("summary opt-in mismatch: %s", body)
		}
		if got := gjson.GetBytes(body, "include.0").String(); got != "reasoning.encrypted_content" {
			t.Fatal("encrypted continuity missing")
		}
	}
}
