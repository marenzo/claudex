package proxy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/marenzo/claudex/internal/config"
	claude "github.com/marenzo/claudex/internal/translator/codex/claude"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func prepareRequest(raw []byte, cfg config.Config) ([]byte, string, error) {
	root := gjson.ParseBytes(raw)
	if !gjson.ValidBytes(raw) || !root.IsObject() || !root.Get("messages").IsArray() {
		return nil, "", fmt.Errorf("expected a Claude messages request")
	}
	requestedModel := root.Get("model").String()
	if requestedModel == "" {
		requestedModel = cfg.Model
	}
	model, ok := config.FindModel(requestedModel)
	if !ok {
		return nil, "", fmt.Errorf("unsupported model %q; choose gpt-6-astra, gpt-5.6-terra, gpt-5.6-sol, or gpt-5.6-luna", requestedModel)
	}
	if strings.EqualFold(root.Get("tool_choice.name").String(), "Artifact") {
		return nil, "", fmt.Errorf("publishing through Artifact is disabled; use local file tools")
	}
	tools := root.Get("tools")
	if tools.IsArray() {
		filtered := make([]json.RawMessage, 0, len(tools.Array()))
		for _, tool := range tools.Array() {
			if !strings.EqualFold(tool.Get("name").String(), "Artifact") {
				if tool.Get("name").String() == "WebSearch" {
					// Claude Code normally runs this client tool through Anthropic. Expose
					// Codex's native server search instead; execution stays in this request.
					native := []byte(`{"type":"web_search_20250305","name":"WebSearch"}`)
					for _, field := range []string{"allowed_domains", "user_location"} {
						if value := tool.Get(field); value.Exists() {
							native, _ = sjson.SetRawBytes(native, field, []byte(value.Raw))
						}
					}
					filtered = append(filtered, native)
				} else {
					filtered = append(filtered, json.RawMessage(tool.Raw))
				}
			}
		}
		raw, _ = sjson.SetBytes(raw, "tools", filtered)
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
