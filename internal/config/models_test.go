package config

import "testing"

func TestModelAliases(t *testing.T) {
	for input, want := range map[string]string{
		"astra": "gpt-6-astra", "fable": "gpt-6-astra", "claude-fable-5": "gpt-6-astra",
		"sol": "gpt-5.6-sol", "opus": "gpt-5.6-sol", "claude-opus-4-8[1m]": "gpt-5.6-sol",
		"terra": "gpt-5.6-terra", "sonnet": "gpt-5.6-terra", "claude-sonnet-4-6": "gpt-5.6-terra",
		"luna": "gpt-5.6-luna", "haiku": "gpt-5.6-luna", "claude-haiku-4-5-20251001": "gpt-5.6-luna",
	} {
		t.Run(input, func(t *testing.T) {
			got, ok := FindModel(input)
			if !ok || got.ID != want {
				t.Fatalf("%s: %+v, %t", input, got, ok)
			}
			canonical, ok := FindModel(want)
			if !ok || canonical.ID != want {
				t.Fatal("canonical model missing")
			}
		})
	}
	for _, input := range []string{"unknown", "other-sonnet", "claude-sonnetty", "gpt-7"} {
		if _, ok := FindModel(input); ok {
			t.Fatalf("accepted %s", input)
		}
	}
}
