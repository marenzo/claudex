package claude

import (
	"bytes"
	"encoding/json"
	"strings"

	translatorcommon "github.com/marenzo/claudex/internal/translator/common"

	"github.com/tidwall/gjson"
)

// normalizeToolParameters ensures object schemas contain at least an empty properties map,
// strips dialect keywords ($schema, $id), and drops regex patterns containing unsupported
// Unicode property escapes (\p{...} / \P{...}) that cause upstream schema validation failures.
func normalizeToolParameters(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" || !gjson.Valid(raw) {
		return `{"type":"object","properties":{}}`
	}
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.UseNumber()
	if errDecode := decoder.Decode(&root); errDecode != nil || root == nil {
		return `{"type":"object","properties":{}}`
	}

	stripDialectKeywordsFromSchema(root)

	typeVal, typeExists := root["type"]
	isObject := false
	if !typeExists || typeVal == nil || typeVal == "" {
		root["type"] = "object"
		isObject = true
	} else {
		switch t := typeVal.(type) {
		case string:
			if t == "object" {
				isObject = true
			}
		case []any:
			for _, elem := range t {
				if elemStr, ok := elem.(string); ok && elemStr == "object" {
					isObject = true
					break
				}
			}
		}
	}
	if isObject {
		if props, ok := root["properties"]; !ok || props == nil {
			root["properties"] = map[string]any{}
		}
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if errEncode := enc.Encode(root); errEncode != nil {
		return `{"type":"object","properties":{}}`
	}
	return strings.TrimSpace(buf.String())
}

func stripDialectKeywordsFromSchema(v any) {
	switch schema := v.(type) {
	case map[string]any:
		delete(schema, "$schema")
		delete(schema, "$id")
		if patternVal, ok := schema["pattern"].(string); ok && translatorcommon.HasUnsupportedUnicodePropertyEscape(patternVal) {
			delete(schema, "pattern")
		}

		// Inspect regex keys under patternProperties
		if patternProps, ok := schema["patternProperties"].(map[string]any); ok {
			for patternKey, subSchema := range patternProps {
				if translatorcommon.HasUnsupportedUnicodePropertyEscape(patternKey) {
					delete(patternProps, patternKey)
				} else {
					stripDialectKeywordsFromSchema(subSchema)
				}
			}
		}

		for _, mapKey := range codexSchemaMapKeywords {
			if mapKey == "patternProperties" {
				continue
			}
			if subMap, ok := schema[mapKey].(map[string]any); ok {
				for _, subSchema := range subMap {
					stripDialectKeywordsFromSchema(subSchema)
				}
			}
		}

		for _, valKey := range codexSchemaValueKeywords {
			if val, exists := schema[valKey]; exists {
				switch sub := val.(type) {
				case map[string]any:
					stripDialectKeywordsFromSchema(sub)
				case []any:
					for _, item := range sub {
						stripDialectKeywordsFromSchema(item)
					}
				}
			}
		}
	case []any:
		for _, item := range schema {
			stripDialectKeywordsFromSchema(item)
		}
	}
}

// codexSchemaMapKeywords and codexSchemaValueKeywords reference the unified JSON Schema keywords
// declared in internal/translator/common.
var (
	codexSchemaMapKeywords   = translatorcommon.SchemaMapKeywords
	codexSchemaValueKeywords = translatorcommon.SchemaValueKeywords
)

// codexSchemaNeedsRelaxedMode preserves schemas that allow optional or additional
// properties. Strict output requires every object to disallow additional keys
// and require all declared properties, including nested objects.
func codexSchemaNeedsRelaxedMode(schema gjson.Result) bool {
	if !schema.IsObject() {
		if schema.IsArray() {
			miss := false
			schema.ForEach(func(_, child gjson.Result) bool {
				if codexSchemaNeedsRelaxedMode(child) {
					miss = true
					return false
				}
				return true
			})
			return miss
		}
		return false
	}
	typeValue := schema.Get("type")
	isObject := typeValue.String() == "object" || schema.Get("properties").IsObject()
	for _, variant := range typeValue.Array() {
		isObject = isObject || variant.String() == "object"
	}
	if isObject && schema.Get("additionalProperties").Type != gjson.False {
		return true
	}
	if properties := schema.Get("properties"); properties.IsObject() {
		required := schema.Get("required")
		if !required.IsArray() && len(properties.Map()) > 0 {
			return true
		}
		names := make(map[string]struct{}, len(required.Array()))
		for _, item := range required.Array() {
			if item.Type == gjson.String {
				names[item.String()] = struct{}{}
			}
		}
		for name := range properties.Map() {
			if _, ok := names[name]; !ok {
				return true
			}
		}
	}
	for _, keyword := range codexSchemaMapKeywords {
		children := schema.Get(keyword)
		if !children.IsObject() {
			continue
		}
		miss := false
		children.ForEach(func(_, child gjson.Result) bool {
			if codexSchemaNeedsRelaxedMode(child) {
				miss = true
				return false
			}
			return true
		})
		if miss {
			return true
		}
	}
	for _, keyword := range codexSchemaValueKeywords {
		if child := schema.Get(keyword); child.Exists() && codexSchemaNeedsRelaxedMode(child) {
			return true
		}
	}
	return false
}
