package launcher

import (
	"encoding/json"
	"github.com/marenzo/claudex/internal/config"
	"reflect"
	"strings"
	"testing"
)

func TestSettingsNormalizeAliasesAndFamilies(t *testing.T) {
	cfg := config.DefaultsFor("/fixture")
	for alias, canonical := range map[string]string{"terra": "gpt-5.6-terra", " GPT-5.6-TERRA ": "gpt-5.6-terra",
		"claude-sonnet-4-6[1m]": "gpt-6-sol", "Opus": "gpt-6.1-sol", "claude-haiku": "gpt-6-luna", "FABLE": "gpt-6-astra"} {
		cfg.Model = alias
		settings, err := Settings(cfg)
		if err != nil {
			t.Fatalf("%q: %v", alias, err)
		}
		if settings.Model != canonical || settings.Env["ANTHROPIC_MODEL"] != canonical || settings.Env["ANTHROPIC_CUSTOM_MODEL_OPTION"] != canonical {
			t.Errorf("%q resolved to %q", alias, settings.Model)
		}
	}
	cfg.Model = "terra"
	cfg.ReasoningEffort = "ultra"
	settings, _ := Settings(cfg)
	if settings.Env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"] != "GPT-5.6 Terra (Codex subscription)" {
		t.Errorf("unexpected option name %q", settings.Env["ANTHROPIC_CUSTOM_MODEL_OPTION_NAME"])
	}
	if settings.EffortLevel != "ultracode" || !reflect.DeepEqual(settings.Permissions.Deny, []string{"Artifact", "SendFeedback", "SendUserFeedback"}) {
		t.Errorf("unexpected settings %+v", settings)
	}
	for family, target := range map[string]string{"HAIKU": "gpt-6-luna", "SONNET": "gpt-6-sol", "OPUS": "gpt-6.1-sol", "FABLE": "gpt-6-astra"} {
		if settings.Env["ANTHROPIC_DEFAULT_"+family+"_MODEL"] != target {
			t.Errorf("%s mapped to %q", family, settings.Env["ANTHROPIC_DEFAULT_"+family+"_MODEL"])
		}
	}
	if settings.Env["ANTHROPIC_SMALL_FAST_MODEL"] != "gpt-6-luna" || settings.Env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" {
		t.Errorf("unexpected env %v", settings.Env)
	}
	if _, ok := settings.Env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Error("settings file must not contain the client key")
	}
	cfg.Model = "unknown-model"
	if _, err := Settings(cfg); err == nil || !strings.Contains(err.Error(), "unsupported model") {
		t.Errorf("unexpected error %v", err)
	}
}

func TestRewriteArgsPreservesClaudeArguments(t *testing.T) {
	for _, test := range []struct{ in, want []string }{
		{[]string{"--model", "terra", "--effort=ultra"}, []string{"--model", "gpt-5.6-terra", "--effort=ultracode"}},
		{[]string{"--model=opus", "--effort", "max"}, []string{"--model=opus", "--effort", "max"}},
		{[]string{"--model=SOL", "--effort", "ultra"}, []string{"--model=gpt-6.1-sol", "--effort", "ultracode"}},
		{[]string{"--", "--model=sol", "--effort=ultra"}, []string{"--", "--model=sol", "--effort=ultra"}},
	} {
		if got := RewriteArgs(test.in); !reflect.DeepEqual(got, test.want) {
			t.Errorf("RewriteArgs(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

func TestPickerReplacesBuiltInsWithOnlyAgreedModels(t *testing.T) {
	settings, err := Settings(config.DefaultsFor("/fixture"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"gpt-6.1-sol", "gpt-5.6-terra", "gpt-6-luna", "gpt-6-astra", "gpt-6-sol"}
	if !settings.ModelPicker.ReplaceBuiltInOptions || !reflect.DeepEqual(settings.AvailableModels, want) {
		t.Fatalf("picker does not replace and restrict built-ins: %+v", settings.ModelPicker)
	}
	var got []string
	for _, option := range settings.ModelPicker.Options {
		got = append(got, option.Model)
		if option.Label == "" || option.Description == "" {
			t.Fatal("unlabeled model row")
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("picker model IDs: %v", got)
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if json.Unmarshal(raw, &decoded) != nil || decoded["availableModels"] == nil || decoded["modelPicker"] == nil {
		t.Fatal("Claude Code picker settings missing from launcher JSON")
	}
}

func TestSettingsSelectClassifierLocation(t *testing.T) {
	cfg := config.DefaultsFor("/fixture")
	settings, err := Settings(cfg)
	if err != nil || settings.Env["CLAUDE_CODE_AUTO_MODE_SERVER"] != "0" {
		t.Fatalf("client-side default: %v %+v", err, settings)
	}
	cfg.AutoModeClassifierModel = "astra"
	settings, err = Settings(cfg)
	if err != nil || settings.Env["CLAUDE_CODE_AUTO_MODE_SERVER"] != "1" {
		t.Fatalf("server opt in: %v %+v", err, settings)
	}
}

func TestLocalFeedbackNeverRestoresAnthropicFeedback(t *testing.T) {
	cfg := config.DefaultsFor("/fixture")
	for _, enabled := range []bool{false, true} {
		cfg.Feedback.Enabled = enabled
		s, err := Settings(cfg)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("DISABLE_FEEDBACK_COMMAND", "0")
		t.Setenv("CLAUDE_CODE_SEND_FEEDBACK", "1")
		env := strings.Join(Environment(s, "http://127.0.0.1:8317", "fixture-key"), "\n")
		if !strings.Contains(env, "DISABLE_FEEDBACK_COMMAND=1") || !strings.Contains(env, "CLAUDE_CODE_SEND_FEEDBACK=0") {
			t.Fatal("Anthropic feedback re-enabled")
		}
		if enabled != (len(s.Permissions.Allow) == 1) {
			t.Fatal("wrong local permission")
		}
	}
	raw, err := FeedbackMCPConfig("/path with spaces/claudex", "http://127.0.0.1:8317", "/fixture/client-key", strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Servers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Servers["claudex_feedback"].Command != "/path with spaces/claudex" {
		t.Fatal("unsafe executable config")
	}
}
