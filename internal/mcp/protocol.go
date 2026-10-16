// Package mcp adapts the shared management registry to MCP tools transports.
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"unicode/utf8"

	"serverman/internal/core"
)

const ProtocolVersion = "2025-11-25"
const maxMessage = (1 << 20) + (16 << 10)

func SupportedVersion(version string) bool {
	return version == ProtocolVersion || version == "2025-06-18" || version == "2025-03-26"
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

func failure(id json.RawMessage, code int, message string) *response {
	if len(id) == 0 {
		id = json.RawMessage(`null`)
	}
	return &response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}}
}

func parse(data []byte) (request, *response) {
	var message request
	if len(data) > maxMessage {
		return message, failure(nil, -32600, "Message exceeds the size limit")
	}
	if !utf8.Valid(data) || !json.Valid(data) {
		return message, failure(nil, -32700, "Invalid JSON")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return message, failure(nil, -32600, "Expected a JSON-RPC object; batches are not supported")
	}
	if err := json.Unmarshal(trimmed, &message); err != nil || message.JSONRPC != "2.0" {
		return message, failure(nil, -32600, "Invalid JSON-RPC request")
	}
	if len(message.ID) != 0 {
		if _, ok := idKey(message.ID); !ok {
			return message, failure(nil, -32600, "Request ID must be a string or a safe integer")
		}
	}
	if message.Method == "" {
		if len(message.ID) > 0 && ((len(message.Result) > 0) != (len(message.Error) > 0)) {
			return message, nil
		}
		return message, failure(message.ID, -32600, "Request method is required")
	}
	if len(message.Result) > 0 || len(message.Error) > 0 {
		return message, failure(message.ID, -32600, "Request cannot contain a result or error")
	}
	if len(message.Params) > 0 {
		if text := bytes.TrimSpace(message.Params); len(text) == 0 || text[0] != '{' {
			if len(message.ID) == 0 {
				return request{JSONRPC: "2.0"}, nil
			}
			return message, failure(message.ID, -32602, "Parameters must be an object")
		}
	}
	return message, nil
}

func idKey(raw json.RawMessage) (string, bool) {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "", false
	}
	switch value := value.(type) {
	case string:
		data, _ := json.Marshal(value)
		return "s:" + string(data), true
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) || math.Trunc(value) != value || math.Abs(value) > 9007199254740991 {
			return "", false
		}
		return "n:" + strconv.FormatFloat(value, 'f', 0, 64), true
	default:
		return "", false
	}
}

type Server struct {
	registry *core.Registry
	tools    []map[string]any
	names    map[string]bool
}

func New(registry *core.Registry) *Server {
	server := &Server{registry: registry, tools: []map[string]any{}, names: map[string]bool{}}
	for _, tool := range registry.Tools() {
		server.names[tool.Name] = true
		server.tools = append(server.tools, map[string]any{"name": tool.Name, "title": tool.Title, "description": tool.Description, "inputSchema": tool.InputSchema, "annotations": map[string]bool{"readOnlyHint": tool.ReadOnly, "destructiveHint": tool.Destructive, "idempotentHint": tool.ReadOnly, "openWorldHint": true}})
	}
	return server
}

func (s *Server) dispatch(ctx context.Context, message request) *response {
	result := &response{JSONRPC: "2.0", ID: message.ID}
	switch message.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string          `json:"protocolVersion"`
			Capabilities    json.RawMessage `json:"capabilities"`
			ClientInfo      struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		if json.Unmarshal(message.Params, &params) != nil || params.ProtocolVersion == "" || params.ClientInfo.Name == "" || params.ClientInfo.Version == "" || len(params.Capabilities) == 0 || bytes.TrimSpace(params.Capabilities)[0] != '{' {
			return failure(message.ID, -32602, "Initialize requires protocolVersion, clientInfo and capabilities")
		}
		version := params.ProtocolVersion
		if !SupportedVersion(version) {
			version = ProtocolVersion
		}
		result.Result = map[string]any{"protocolVersion": version, "serverInfo": map[string]string{"name": "serverman", "title": "Serverman", "version": "dev"}, "capabilities": map[string]any{"tools": map[string]bool{"listChanged": false}}, "instructions": "Use system_capabilities for backend availability and write policy. All tools share the web API's validation and permissions. Writes return a job; poll jobs_get until terminal, and use jobs_cancel to stop remaining work. Destructive tools require confirm=true. A stdio process owns its job history; keep the connection open until its jobs finish."}
	case "ping":
		result.Result = map[string]any{}
	case "tools/list":
		var params struct {
			Cursor string `json:"cursor"`
		}
		if len(message.Params) > 0 && json.Unmarshal(message.Params, &params) != nil {
			return failure(message.ID, -32602, "Invalid tool list parameters")
		}
		if params.Cursor != "" {
			return failure(message.ID, -32602, "Unknown cursor; the complete tool list is returned in one response")
		}
		result.Result = map[string]any{"tools": s.tools}
	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
			Task      json.RawMessage `json:"task"`
		}
		if json.Unmarshal(message.Params, &params) != nil || params.Name == "" {
			return failure(message.ID, -32602, "Tool name is required")
		}
		if len(params.Task) > 0 {
			return failure(message.ID, -32602, "Task-augmented calls are not supported; management writes return Serverman jobs")
		}
		if !s.names[params.Name] {
			return failure(message.ID, -32602, "Unknown tool")
		}
		value, err := s.registry.Invoke(ctx, params.Name, params.Arguments, "mcp")
		if err != nil {
			problem := &core.Error{Code: "operation_failed", Message: err.Error()}
			var typed *core.Error
			if errors.As(err, &typed) {
				problem = typed
			}
			value = map[string]any{"error": problem}
		}
		data, encodeErr := json.Marshal(value)
		if encodeErr != nil {
			return failure(message.ID, -32603, "Could not encode tool result")
		}
		if len(data) == 0 || data[0] != '{' {
			key := "value"
			if len(data) > 0 && data[0] == '[' {
				key = "items"
			}
			data, _ = json.Marshal(map[string]any{key: json.RawMessage(data)})
		}
		result.Result = map[string]any{"content": []map[string]string{{"type": "text", "text": string(data)}}, "structuredContent": json.RawMessage(data), "isError": err != nil}
	default:
		return failure(message.ID, -32601, "Method not supported")
	}
	return result
}
