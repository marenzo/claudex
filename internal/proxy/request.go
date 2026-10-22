package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/marenzo/claudex/internal/config"
	claude "github.com/marenzo/claudex/internal/translator/codex/claude"
	translatorcommon "github.com/marenzo/claudex/internal/translator/common"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func prepareRequest(raw []byte, cfg config.Config) ([]byte, string, error) {
	root := gjson.ParseBytes(raw)
	if !gjson.ValidBytes(raw) || !root.IsObject() || !root.Get("messages").IsArray() {
		return nil, "", fmt.Errorf("expected a Claude messages request")
	}
	if err := translatorcommon.ValidateUnambiguousJSON(raw); err != nil {
		return nil, "", fmt.Errorf("ambiguous JSON in Claude messages request")
	}
	for _, message := range root.Get("messages").Array() {
		for _, part := range message.Get("content").Array() {
			if err := validateDocumentPart(part); err != nil {
				return nil, "", err
			}
			if part.Get("type").String() == "tool_result" {
				for _, content := range part.Get("content").Array() {
					if err := validateDocumentPart(content); err != nil {
						return nil, "", err
					}
				}
			}
		}
	}

	requestedModel := root.Get("model").String()
	if requestedModel == "" {
		requestedModel = cfg.Model
	}
	model, ok := config.FindModel(requestedModel)
	if !ok {
		return nil, "", fmt.Errorf("unsupported model %q; choose gpt-6.1-sol, gpt-5.6-terra, gpt-6-luna, gpt-6-astra, or gpt-6-sol", requestedModel)
	}
	if strings.EqualFold(root.Get("tool_choice.name").String(), "Artifact") {
		return nil, "", fmt.Errorf("publishing through Artifact is disabled; use local file tools")
	}
	tools, err := claude.ResolveToolCatalog(raw)
	if err != nil {
		return nil, "", err
	}
	for _, tool := range tools {
		if callers := tool.Get("allowed_callers"); callers.Exists() {
			if !callers.IsArray() || len(callers.Array()) == 0 {
				return nil, "", fmt.Errorf("allowed_callers must allow direct tool calls")
			}
			for _, caller := range callers.Array() {
				if caller.Type != gjson.String || caller.String() != "direct" {
					return nil, "", fmt.Errorf("programmatic allowed_callers are not supported by Claudex")
				}
			}
		}
		kind := tool.Get("type").String()
		if kind != "" && kind != "custom" && kind != "function" && kind != "web_search_20250305" && kind != "web_search_20260209" {
			return nil, "", fmt.Errorf("Input tag '%s' is not supported; Claudex supports client functions and web search", kind)
		}
		if kind == "web_search_20250305" || kind == "web_search_20260209" || tool.Get("name").String() == "WebSearch" {
			if tool.Get("max_uses").Exists() {
				return nil, "", fmt.Errorf("web search max_uses cannot be enforced by the Codex subscription endpoint")
			}
			for _, field := range []string{"allowed_domains", "blocked_domains"} {
				domains := tool.Get(field)
				if !domains.Exists() {
					continue
				}
				if !domains.IsArray() || len(domains.Array()) > 100 {
					return nil, "", fmt.Errorf("web search %s requires at most 100 domains", field)
				}
				for _, domain := range domains.Array() {
					if domain.Type != gjson.String || strings.TrimSpace(domain.String()) == "" {
						return nil, "", fmt.Errorf("web search %s requires domain strings", field)
					}
				}
			}
		}
	}
	if err := validateToolChoice(root, tools); err != nil {
		return nil, "", err
	}
	body := claude.ConvertRequest(model.ID, raw)
	for _, tool := range gjson.GetBytes(body, "tools").Array() {
		if tool.Get("type").String() == "web_search" {
			body, _ = sjson.SetBytes(body, "include.-1", "web_search_call.action.sources")
			break
		}
	}
	effort, err := resolveReasoningEffort(root, cfg.ReasoningEffort, model)
	if err != nil {
		return nil, "", err
	}
	body, _ = sjson.SetBytes(body, "reasoning.effort", effort)
	// Summaries are display output, independent of encrypted replay state.
	if root.Get("thinking.display").String() != "omitted" && root.Get("thinking.type").String() != "disabled" {
		body, _ = sjson.SetBytes(body, "reasoning.summary", "auto")
	}
	session := ""
	if userID := root.Get("metadata.user_id").String(); userID != "" {
		digest := sha256.Sum256([]byte(userID))
		session = hex.EncodeToString(digest[:16])
		body, _ = sjson.SetBytes(body, "prompt_cache_key", session)
	}
	return body, session, nil
}

// A malformed forced choice must not be silently broadened into auto selection.
func validateToolChoice(root gjson.Result, tools []gjson.Result) error {
	choice := root.Get("tool_choice")
	if !choice.Exists() || choice.Type == gjson.Null {
		return nil
	}
	if !choice.IsObject() && choice.Type != gjson.String {
		return fmt.Errorf("tool_choice must be an object or string")
	}
	kind := choice.Get("type").String()
	if choice.Type == gjson.String {
		kind = choice.String()
	}
	if v := choice.Get("disable_parallel_tool_use"); v.Exists() && v.Type != gjson.True && v.Type != gjson.False {
		return fmt.Errorf("tool_choice.disable_parallel_tool_use must be boolean")
	}
	switch kind {
	case "auto", "any", "none":
		return nil
	case "tool":
		name := choice.Get("name")
		if name.Type != gjson.String || name.String() == "" {
			return fmt.Errorf("tool_choice requires a tool name")
		}
		for _, tool := range tools {
			if tool.Get("name").String() == name.String() {
				return nil
			}
		}
		return fmt.Errorf("tool_choice names an undeclared tool")
	default:
		return fmt.Errorf("unsupported tool_choice type")
	}
}

func validateDocumentPart(part gjson.Result) error {
	if part.Get("type").String() != "document" {
		return nil
	}
	source := part.Get("source")
	switch source.Get("type").String() {
	case "text":
		if source.Get("data").Type == gjson.String {
			return nil
		}
	case "base64":
		if strings.EqualFold(strings.TrimSpace(source.Get("media_type").String()), "application/pdf") && (source.Get("data").String() != "" || source.Get("base64").String() != "") {
			return nil
		}
	}
	return fmt.Errorf("unsupported document source; supply base64 application/pdf or text data")
}
