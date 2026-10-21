package claude

import (
	"fmt"

	"github.com/tidwall/gjson"
)

// ResolveToolCatalog applies Claude's mid-conversation tool changes. Responses
// requests are stateless, so their top-level tools must describe the catalog
// available NOW, including tools introduced after the initial declaration.
// Retain removed definitions separately so a later reference can re-enable one.
func ResolveToolCatalog(raw []byte) ([]gjson.Result, error) {
	root := gjson.ParseBytes(raw)
	known := make(map[string]gjson.Result)
	active := make(map[string]bool)
	var order []string
	declare := func(tool gjson.Result) error {
		name := tool.Get("name")
		if !tool.IsObject() || name.Type != gjson.String || name.String() == "" {
			return fmt.Errorf("tool definition requires a name")
		}
		if _, ok := known[name.String()]; !ok {
			order = append(order, name.String())
		}
		known[name.String()] = tool
		active[name.String()] = true
		return nil
	}
	if tools := root.Get("tools"); tools.Exists() && !tools.IsArray() {
		return nil, fmt.Errorf("tools must be an array")
	}
	for _, tool := range root.Get("tools").Array() {
		if _, exists := known[tool.Get("name").String()]; exists {
			return nil, fmt.Errorf("duplicate tool name")
		}
		if err := declare(tool); err != nil {
			return nil, err
		}
	}
	for _, message := range root.Get("messages").Array() {
		for _, part := range message.Get("content").Array() {
			kind := part.Get("type").String()
			if kind != "tool_addition" && kind != "tool_removal" {
				continue
			}
			if message.Get("role").String() != "system" {
				return nil, fmt.Errorf("tool changes require a system message")
			}
			tool := part.Get("tool")
			var name string
			switch tool.Get("type").String() {
			case "tool_definition":
				definition := tool.Get("definition")
				name = definition.Get("name").String()
				if kind == "tool_addition" {
					if err := declare(definition); err != nil {
						return nil, err
					}
				}
			case "tool_reference":
				name = tool.Get("name").String()
				if kind == "tool_addition" {
					if _, exists := known[name]; !exists {
						return nil, fmt.Errorf("tool addition references an unknown definition")
					}
					active[name] = true
				}
			default:
				return nil, fmt.Errorf("unsupported inline tool change")
			}
			if name == "" {
				return nil, fmt.Errorf("tool change requires a name")
			}
			if kind == "tool_removal" {
				delete(active, name)
			}
		}
	}
	tools := make([]gjson.Result, 0, len(active))
	for _, name := range order {
		if active[name] {
			tools = append(tools, known[name])
		}
	}
	return tools, nil
}
