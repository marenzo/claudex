package config

import "testing"

func TestModelAliases(t *testing.T) {
	for input, want := range map[string]string{
		"astra": "gpt-6-astra", "fable": "gpt-6-astra", "claude-fable-5": "gpt-6-astra",
		"sol": "gpt-6.1-sol", "opus": "gpt-6.1-sol", "claude-opus-4-8[1m]": "gpt-6.1-sol",
		"terra": "gpt-5.6-terra", "sonnet": "gpt-6-sol", "claude-sonnet-4-6": "gpt-6-sol",
		"luna": "gpt-6-luna", "haiku": "gpt-6-luna", "claude-haiku-4-5-20251001": "gpt-6-luna",
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
	for _, input := range []string{"unknown", "other-sonnet", "claude-sonnetty", "gpt-7", "gpt-5.6-sol", "sol5.6", "gpt-5.6-luna", "luna5.6", "gpt-image-2.5"} {
		if _, ok := FindModel(input); ok {
			t.Fatalf("accepted %s", input)
		}
	}
}

func TestAgreedModelsRemainSelectable(t *testing.T) {
	for _, id := range []string{"gpt-6-astra", "gpt-6.1-sol", "gpt-6-sol", "gpt-6-luna", "gpt-5.6-terra"} {
		got, ok := FindModel(id)
		if !ok || got.ID != id {
			t.Fatalf("pin %s resolved to %+v", id, got)
		}
	}
	for alias, id := range map[string]string{"sol6": "gpt-6-sol"} {
		got, ok := FindModel(alias)
		if !ok || got.ID != id {
			t.Fatalf("alias %s resolved to %+v", alias, got)
		}
	}
}
