package feedback

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
)

// MCP is a small stdio-only MCP server with one bounded local-write tool. The
// callbacks enforce current gateway settings, including for already-open sessions.
type MCP struct {
	Enabled func(context.Context) (bool, error)
	Submit  func(context.Context, Submission) (Receipt, error)
	Source  Source
}
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

func (m MCP) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 64<<10)
	enc := json.NewEncoder(out)
	initialized := false
	send := func(id json.RawMessage, result any, code int, message string) error {
		if len(id) == 0 {
			id = json.RawMessage("null")
		}
		reply := map[string]any{"jsonrpc": "2.0", "id": id}
		if code != 0 {
			reply["error"] = map[string]any{"code": code, "message": message}
		} else {
			reply["result"] = result
		}
		return enc.Encode(reply)
	}
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var req rpcRequest
		if json.Unmarshal(scanner.Bytes(), &req) != nil {
			if err := send(nil, nil, -32700, "Invalid JSON"); err != nil {
				return err
			}
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" || !validRPCID(req.ID) {
			if err := send(nil, nil, -32600, "Invalid request"); err != nil {
				return err
			}
			continue
		}
		if len(req.ID) == 0 {
			continue
		} // client notifications, including initialized/cancelled
		var result any
		code := 0
		message := ""
		switch req.Method {
		case "initialize":
			var p struct {
				ProtocolVersion string `json:"protocolVersion"`
				ClientInfo      struct {
					Version string `json:"version"`
				} `json:"clientInfo"`
			}
			if json.Unmarshal(req.Params, &p) != nil || p.ProtocolVersion == "" {
				code = -32602
				message = "protocolVersion is required"
				break
			}
			protocol := p.ProtocolVersion
			if !member(protocol, "2025-11-25", "2025-06-18", "2025-03-26", "2024-11-05") {
				protocol = "2025-11-25"
			}
			if textField(p.ClientInfo.Version, 100) == nil {
				m.Source.ClientVersion = p.ClientInfo.Version
			}
			initialized = true
			result = map[string]any{"protocolVersion": protocol, "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": MCPName, "version": "1"}}
		case "ping":
			result = map[string]any{}
		case "tools/list":
			if !initialized {
				code = -32000
				message = "Initialize first"
				break
			}
			enabled, err := m.Enabled(ctx)
			if err != nil {
				code = -32603
				message = "Feedback gateway unavailable"
				break
			}
			tools := []any{}
			if enabled {
				tools = append(tools, Tool())
			}
			result = map[string]any{"tools": tools}
		case "tools/call":
			if !initialized {
				code = -32000
				message = "Initialize first"
				break
			}
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			if json.Unmarshal(req.Params, &p) != nil || p.Name != ToolName {
				code = -32602
				message = "Unknown feedback tool"
				break
			}
			enabled, err := m.Enabled(ctx)
			if err != nil {
				result = toolError("Feedback gateway unavailable; nothing recorded.")
				break
			}
			if !enabled {
				result = toolError("Feedback collection is disabled; nothing recorded. Do not retry.")
				break
			}
			report, err := Decode(p.Arguments)
			if err != nil {
				result = toolError("Invalid feedback: " + err.Error())
				break
			}
			receipt, err := m.Submit(ctx, Submission{Source: m.Source, Report: report})
			if err != nil {
				result = toolError("Feedback was not confirmed saved. Check collection status before retrying once with identical arguments.")
				break
			}
			raw, _ := json.Marshal(receipt)
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "Recorded locally for triage; not validated or fixed. " + string(raw)}}, "structuredContent": receipt, "isError": false}
		default:
			code = -32601
			message = "Method not found"
		}
		if err := send(req.ID, result, code, message); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return errors.New("MCP input exceeded limit or could not be read")
	}
	return nil
}
func toolError(message string) map[string]any {
	return map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": message}}}
}
func validRPCID(id json.RawMessage) bool {
	if len(id) == 0 {
		return true
	}
	d := json.NewDecoder(bytes.NewReader(id))
	d.UseNumber()
	var v any
	if d.Decode(&v) != nil {
		return false
	}
	switch v.(type) {
	case string, json.Number:
		return true
	}
	return false
}
